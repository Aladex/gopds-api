package parser

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

const (
	bodySampleLimit = 4096
	stripSymbols    = " \"'&-.\n#`"
)

// FB2Parser extracts metadata from FB2 XML streams.
type FB2Parser struct {
	readCover bool
	handlers  map[string]*TagHandler

	coverNameHandler   *TagHandler
	coverBinaryHandler *TagHandler
	coverID            string
	inCoverBinary      bool
	coverData          strings.Builder
	coverFound         bool

	inBody        bool
	bodySample    strings.Builder
	bodySampleLen int

	// rootSeen records that the document's first start element was checked
	// against the FictionBook root requirement.
	rootSeen bool
}

// NewFB2Parser creates a parser configured to read cover data if requested.
func NewFB2Parser(readCover bool) *FB2Parser {
	parser := &FB2Parser{
		readCover: readCover,
		handlers: map[string]*TagHandler{
			"title":         NewTagHandler([]string{"description", "title-info", "book-title"}),
			"authorFirst":   NewTagHandler([]string{"description", "title-info", "author", "first-name"}),
			"authorLast":    NewTagHandler([]string{"description", "title-info", "author", "last-name"}),
			"genre":         NewTagHandler([]string{"description", "title-info", "genre"}),
			"lang":          NewTagHandler([]string{"description", "title-info", "lang"}),
			"series":        NewTagHandler([]string{"description", "title-info", "sequence"}),
			"annotation":    NewTagHandler([]string{"description", "title-info", "annotation", "p"}),
			"annotationRaw": NewTagHandler([]string{"description", "title-info", "annotation"}),
			"annotationDoc": NewTagHandler([]string{"description", "document-info", "annotation"}),
			"docdate":       NewTagHandler([]string{"description", "document-info", "date"}),
		},
	}

	if readCover {
		parser.coverNameHandler = NewTagHandler([]string{"description", "title-info", "coverpage", "image"})
		parser.coverBinaryHandler = NewTagHandler([]string{"binary"})
	}

	return parser
}

// Parse reads FB2 XML from reader and returns parsed metadata.
func (p *FB2Parser) Parse(reader io.Reader) (*BookFile, error) {
	// Read content to handle encoding detection
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	// Resolve the charset and convert to UTF-8 (BOM, strict UTF-8 validity,
	// or the XML declaration; no statistical guessing).
	decodedContent, err := DecodeToUTF8(content)
	if err != nil {
		return nil, err
	}
	decodedContent = sanitizeControlChars(decodedContent)
	decodedContent = sanitizeInvalidTagOpenings(decodedContent)
	decodedContent = sanitizeInvalidProcessingInstructions(decodedContent)
	decodedContent = sanitizeInvalidAmpersands(decodedContent)
	decodedContent = sanitizeXMLVersion(decodedContent)
	decodedContent = sanitizeBrokenSelfClosingTags(decodedContent)
	decodedContent = sanitizeBrokenEndTags(decodedContent)
	decodedContent = sanitizeBrokenLangTag(decodedContent)
	decodedContent = sanitizeMissingXlinkPrefix(decodedContent)
	book, err := p.parseContent(decodedContent)
	if err == nil {
		p.ensureBodySample(decodedContent, book)
		return book, nil
	}
	if errors.Is(err, ErrDamagedContent) {
		// The root verdict: trimming after the description cannot change
		// the document's first element, so the fallback cannot help.
		return nil, err
	}

	fallback := trimAfterDescription(decodedContent)
	if fallback != nil {
		fallbackBook, fallbackErr := p.parseContent(fallback)
		if fallbackErr == nil {
			p.ensureBodySample(decodedContent, fallbackBook)
			fallbackBook.Issues = append(fallbackBook.Issues, err.Error(), "parsed_without_body")
			return fallbackBook, nil
		}
	}

	return nil, err
}

func (p *FB2Parser) parseContent(content []byte) (*BookFile, error) {
	p.reset()

	decodedReader := bytes.NewReader(content)
	decoder := xml.NewDecoder(decodedReader)
	// Set CharsetReader to handle various encodings declared in XML
	decoder.CharsetReader = makeCharsetReader
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := token.(type) {
		case xml.StartElement:
			if !p.rootSeen {
				p.rootSeen = true
				// The root check runs here, on the sanitized text the
				// decoder actually reads: no earlier stage may judge the
				// prolog, because any reading that differs from the
				// sanitizers' loses real books or smuggles false roots.
				if t.Name.Local != "FictionBook" {
					return nil, fmt.Errorf("%w: root element is %q, not FictionBook", ErrDamagedContent, t.Name.Local)
				}
			}
			p.handleStart(t)
		case xml.EndElement:
			p.handleEnd(t)
		case xml.CharData:
			p.handleChar(t)
		}
	}

	if !p.rootSeen {
		return nil, fmt.Errorf("%w: the document has no root element", ErrDamagedContent)
	}

	book := &BookFile{
		Title:      p.extractTitle(),
		Authors:    p.extractAuthors(),
		Tags:       p.extractTags(),
		Series:     p.extractSeries(),
		Language:   p.extractLanguage(),
		DocDate:    p.extractDocDate(),
		Annotation: p.extractAnnotation(),
		BodySample: p.extractBodySample(),
		TextSample: p.extractTextSample(),
		Mimetype:   "fb2",
	}

	if p.readCover {
		cover, err := p.extractCover()
		if err != nil {
			book.Issues = append(book.Issues, err.Error())
		} else {
			book.Cover = cover
		}
	}

	return book, nil
}

func (p *FB2Parser) reset() {
	p.inBody = false
	p.bodySample.Reset()
	p.bodySampleLen = 0
	p.coverID = ""
	p.inCoverBinary = false
	p.coverData.Reset()
	p.coverFound = false
	p.rootSeen = false
	for _, handler := range p.handlers {
		handler.Reset()
	}
	if p.coverNameHandler != nil {
		p.coverNameHandler.Reset()
	}
	if p.coverBinaryHandler != nil {
		p.coverBinaryHandler.Reset()
	}
}

func (p *FB2Parser) handleStart(elem xml.StartElement) {
	local := normalizeName(elem.Name.Local)
	attrs := normalizeAttrs(elem.Attr)

	if local == "body" {
		p.inBody = true
	}

	for _, handler := range p.handlers {
		handler.OpenTag(local, attrs)
	}

	if p.readCover && p.coverNameHandler != nil {
		if p.coverNameHandler.OpenTag(local, attrs) {
			if href, ok := p.coverNameHandler.GetAttribute("href"); ok {
				p.coverID = normalizeCoverID(href)
			}
		}
	}

	if p.readCover && p.coverBinaryHandler != nil {
		p.coverBinaryHandler.OpenTag(local, attrs)
		if local == "binary" && p.coverID != "" {
			if id, ok := attrs["id"]; ok && strings.EqualFold(id, p.coverID) {
				p.inCoverBinary = true
			}
		}
	}
}

func (p *FB2Parser) handleEnd(elem xml.EndElement) {
	local := normalizeName(elem.Name.Local)

	if local == "body" {
		p.inBody = false
	}

	for _, handler := range p.handlers {
		handler.CloseTag(local)
	}

	if p.readCover && p.coverNameHandler != nil {
		p.coverNameHandler.CloseTag(local)
	}

	if p.readCover && p.coverBinaryHandler != nil {
		p.coverBinaryHandler.CloseTag(local)
		if local == "binary" && p.inCoverBinary {
			p.coverFound = true
			p.inCoverBinary = false
		}
	}
}

func (p *FB2Parser) handleChar(data xml.CharData) {
	text := string(data)

	if p.inBody && p.bodySampleLen < bodySampleLimit {
		remaining := bodySampleLimit - p.bodySampleLen
		if len(text) > remaining {
			text = text[:remaining]
		}
		p.bodySample.WriteString(text)
		p.bodySampleLen += len(text)
	}

	for _, handler := range p.handlers {
		handler.SetValue(text)
	}

	if p.readCover && p.inCoverBinary {
		p.coverData.WriteString(text)
	}
}

func (p *FB2Parser) extractTitle() string {
	values := p.handlers["title"].GetValues()
	if len(values) == 0 {
		return ""
	}
	title := sanitizeText(values[0])
	title = normalizeWhitespace(title)
	return normalizeNameCase(title)
}

func (p *FB2Parser) extractAuthors() []Author {
	firstNames := p.handlers["authorFirst"].GetValues()
	lastNames := p.handlers["authorLast"].GetValues()

	maxLen := len(firstNames)
	if len(lastNames) > maxLen {
		maxLen = len(lastNames)
	}

	var authors []Author
	for i := 0; i < maxLen; i++ {
		var first, last string
		if i < len(firstNames) {
			first = normalizeWhitespace(firstNames[i])
			first = normalizeNameCase(first)
		}
		if i < len(lastNames) {
			last = normalizeWhitespace(lastNames[i])
			last = normalizeNameCase(last)
		}
		// Format: LastName FirstName (family name first)
		full := strings.TrimSpace(strings.Join([]string{last, first}, " "))
		if full == "" {
			continue
		}
		authors = append(authors, Author{
			Name:    full,
			Sortkey: last,
		})
	}

	return authors
}

func (p *FB2Parser) extractTags() []string {
	values := p.handlers["genre"].GetValues()
	tags := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(strings.ToLower(value))
		if trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return tags
}

func (p *FB2Parser) extractSeries() *Series {
	attrs := p.handlers["series"].GetAttributes("name")
	if len(attrs) == 0 {
		return nil
	}
	title := strings.TrimSpace(attrs[0])
	if title == "" {
		return nil
	}
	indexes := p.handlers["series"].GetAttributes("number")
	index := ""
	if len(indexes) > 0 {
		index = strings.TrimSpace(indexes[0])
	}
	return &Series{
		Title: title,
		Index: index,
	}
}

func (p *FB2Parser) extractLanguage() string {
	values := p.handlers["lang"].GetValues()
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(values[0]))
}

func (p *FB2Parser) extractAnnotation() string {
	text := strings.TrimSpace(p.handlers["annotation"].GetText("\n"))
	if text != "" {
		return text
	}
	raw := strings.TrimSpace(p.handlers["annotationRaw"].GetText("\n"))
	if raw != "" {
		return raw
	}
	return strings.TrimSpace(p.handlers["annotationDoc"].GetText("\n"))
}

func (p *FB2Parser) extractDocDate() string {
	if value, ok := p.handlers["docdate"].GetAttribute("value"); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	values := p.handlers["docdate"].GetValues()
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func (p *FB2Parser) extractCover() ([]byte, error) {
	if !p.coverFound {
		return nil, nil
	}
	encoded := stripWhitespace(p.coverData.String())
	if encoded == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(encoded)
}

func (p *FB2Parser) extractBodySample() string {
	return strings.TrimSpace(p.bodySample.String())
}

func (p *FB2Parser) extractTextSample() string {
	annotation := p.extractAnnotation()
	body := p.extractBodySample()
	return truncateSample(annotation, body)
}

func normalizeName(name string) string {
	return strings.ToLower(name)
}

func normalizeAttrs(attrs []xml.Attr) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		key := normalizeName(attr.Name.Local)
		normalized[key] = attr.Value
	}
	return normalized
}

func normalizeCoverID(href string) string {
	href = strings.TrimSpace(href)
	href = strings.TrimPrefix(href, "#")
	return strings.ToLower(href)
}

func sanitizeText(value string) string {
	value = strings.TrimSpace(value)
	return strings.Trim(value, stripSymbols)
}

func normalizeWhitespace(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func normalizeNameCase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !isMostlyUpper(value) {
		return value
	}

	parts := strings.Fields(value)
	if len(parts) == 0 {
		return value
	}

	for i := range parts {
		parts[i] = titleCaseToken(parts[i])
	}
	return strings.Join(parts, " ")
}

func isMostlyUpper(value string) bool {
	letters := 0
	upper := 0
	lower := 0

	for _, r := range value {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.IsUpper(r) {
			upper++
		} else if unicode.IsLower(r) {
			lower++
		}
	}

	if letters < 4 {
		return false
	}
	if lower == 0 && upper > 0 {
		return true
	}
	return upper*100/letters >= 80
}

func titleCaseToken(token string) string {
	leading, core, trailing := splitTokenPunct(token)
	if core == "" {
		return token
	}
	if isAbbreviation(core) || isRomanNumeral(core) {
		return leading + strings.ToUpper(core) + trailing
	}

	lower := strings.ToLower(core)
	r, size := utf8.DecodeRuneInString(lower)
	if r == utf8.RuneError {
		return token
	}
	return leading + strings.ToUpper(string(r)) + lower[size:] + trailing
}

func splitTokenPunct(token string) (string, string, string) {
	if token == "" {
		return "", "", ""
	}

	start := 0
	end := len(token)
	for start < end {
		r, size := utf8.DecodeRuneInString(token[start:end])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			break
		}
		start += size
	}
	for start < end {
		r, size := utf8.DecodeLastRuneInString(token[start:end])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			break
		}
		end -= size
	}
	return token[:start], token[start:end], token[end:]
}

func isAbbreviation(token string) bool {
	clean := strings.Trim(token, ".")
	if clean == "" {
		return false
	}
	if utf8.RuneCountInString(clean) > 3 {
		return false
	}
	for _, r := range clean {
		if !unicode.IsLetter(r) {
			return false
		}
		if unicode.IsLower(r) {
			return false
		}
	}
	return true
}

func isRomanNumeral(token string) bool {
	for _, r := range token {
		switch r {
		case 'I', 'V', 'X', 'L', 'C', 'D', 'M':
			continue
		default:
			return false
		}
	}
	return token != ""
}

func trimAfterDescription(content []byte) []byte {
	lower := bytes.ToLower(content)
	end := bytes.Index(lower, []byte("</description>"))
	if end == -1 {
		return nil
	}
	end += len("</description>")
	trimmed := content[:end]
	if !bytes.Contains(bytes.ToLower(trimmed), []byte("</fictionbook>")) {
		trimmed = append(trimmed, []byte("</FictionBook>")...)
	}
	return trimmed
}

func sanitizeInvalidTagOpenings(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		b := content[i]
		if b != '<' {
			out = append(out, b)
			continue
		}
		if i+1 >= len(content) || !isLikelyXMLTagStart(content[i+1]) {
			out = append(out, '&', 'l', 't', ';')
			changed = true
			continue
		}
		out = append(out, b)
	}
	if !changed {
		return content
	}
	return out
}

func isLikelyXMLTagStart(b byte) bool {
	switch b {
	case '/', '?', '!', '_':
		return true
	default:
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
}

func sanitizeInvalidProcessingInstructions(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if content[i] == '<' && i+1 < len(content) && content[i+1] == '?' {
			if i == 0 && hasPrefixFold(content, []byte("<?xml")) {
				out = append(out, '<', '?')
				i++
				continue
			}
			out = append(out, '&', 'l', 't', ';', '?')
			i++
			changed = true
			continue
		}
		out = append(out, content[i])
	}
	if !changed {
		return content
	}
	return out
}

func hasPrefixFold(data []byte, prefix []byte) bool {
	if len(data) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		a := data[i]
		b := prefix[i]
		if a >= 'A' && a <= 'Z' {
			a = a - 'A' + 'a'
		}
		if b >= 'A' && b <= 'Z' {
			b = b - 'A' + 'a'
		}
		if a != b {
			return false
		}
	}
	return true
}

func sanitizeInvalidAmpersands(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if content[i] != '&' {
			out = append(out, content[i])
			continue
		}

		semi := -1
		for j := i + 1; j < len(content) && j-i <= 32; j++ {
			if content[j] == ';' {
				semi = j
				break
			}
		}
		if semi == -1 {
			out = append(out, '&', 'a', 'm', 'p', ';')
			changed = true
			continue
		}

		entity := content[i+1 : semi]
		if isValidEntity(entity) {
			out = append(out, content[i:semi+1]...)
			i = semi
			continue
		}

		out = append(out, '&', 'a', 'm', 'p', ';')
		changed = true
	}
	if !changed {
		return content
	}
	return out
}

func isValidEntity(entity []byte) bool {
	if len(entity) == 0 {
		return false
	}
	switch string(entity) {
	case "amp", "lt", "gt", "quot", "apos":
		return true
	}
	if entity[0] != '#' {
		return false
	}
	if len(entity) >= 2 && (entity[1] == 'x' || entity[1] == 'X') {
		if len(entity) == 2 {
			return false
		}
		for i := 2; i < len(entity); i++ {
			b := entity[i]
			if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
				return false
			}
		}
		return true
	}
	for i := 1; i < len(entity); i++ {
		b := entity[i]
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

func sanitizeControlChars(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		b := content[i]
		if b == '\t' || b == '\n' || b == '\r' {
			out = append(out, b)
			continue
		}
		if b < 0x20 {
			out = append(out, ' ')
			changed = true
			continue
		}
		out = append(out, b)
	}
	if !changed {
		return content
	}
	return out
}

func (p *FB2Parser) ensureBodySample(content []byte, book *BookFile) {
	if book == nil || book.BodySample != "" {
		return
	}
	body := extractBodyTextFallback(content)
	if body == "" {
		return
	}
	book.BodySample = body
	book.TextSample = truncateSample(book.Annotation, body)
}

func extractBodyTextFallback(content []byte) string {
	lower := bytes.ToLower(content)
	bodyStart := bytes.Index(lower, []byte("<body"))
	if bodyStart != -1 {
		tagEnd := bytes.IndexByte(lower[bodyStart:], '>')
		if tagEnd != -1 {
			start := bodyStart + tagEnd + 1
			bodyEnd := bytes.Index(lower[start:], []byte("</body>"))
			end := len(content)
			if bodyEnd != -1 {
				end = start + bodyEnd
			}
			if end > start {
				if sample := stripTagsToSample(content[start:end]); sample != "" {
					return sample
				}
			}
		}
	}

	descEnd := bytes.Index(lower, []byte("</description>"))
	if descEnd == -1 {
		return ""
	}
	start := descEnd + len("</description>")
	end := len(content)
	binaryStart := bytes.Index(lower[start:], []byte("<binary"))
	if binaryStart != -1 {
		end = start + binaryStart
	}
	if end <= start {
		return ""
	}
	return stripTagsToSample(content[start:end])
}

func stripTagsToSample(segment []byte) string {
	out := make([]byte, 0, bodySampleLimit)
	inTag := false
	spacePending := false

	for i := 0; i < len(segment); i++ {
		b := segment[i]
		if b == '<' {
			inTag = true
			spacePending = true
			continue
		}
		if b == '>' {
			inTag = false
			continue
		}
		if inTag {
			continue
		}
		if b == '\n' || b == '\r' || b == '\t' {
			spacePending = true
			continue
		}
		if spacePending {
			if len(out) > 0 {
				out = append(out, ' ')
			}
			spacePending = false
		}
		out = append(out, b)
		if len(out) >= bodySampleLimit {
			break
		}
	}

	return strings.TrimSpace(string(out))
}

func truncateSample(annotation string, body string) string {
	sample := strings.TrimSpace(strings.TrimSpace(annotation) + " " + strings.TrimSpace(body))
	runes := []rune(sample)
	if len(runes) > 2000 {
		sample = string(runes[:2000])
	}
	return strings.TrimSpace(sample)
}

// sanitizeBrokenSelfClosingTags repairs a self-closing tag that lost its
// closing bracket. It looks back to confirm the slash really belongs to a
// tag: a slash in prose before markup -- "100 руб. / <emphasis>" -- is text,
// and rewriting it blindly put a literal "/>" into annotations and turned
// closing tags into opening ones.
func sanitizeBrokenSelfClosingTags(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))

	for i := 0; i < len(content); i++ {
		if content[i] != '/' {
			out = append(out, content[i])
			continue
		}

		// Check if this is potentially a broken self-closing tag: "/" followed by whitespace or "<"
		if i+1 >= len(content) {
			out = append(out, content[i])
			continue
		}

		next := content[i+1]

		// Pattern: /</tag> or /</ or /< -> convert to />
		if next == '<' {
			// Look back to find the opening < of the current tag
			if !isPartOfSelfClosingTag(content, i) {
				out = append(out, content[i])
				continue
			}

			// Skip whitespace between / and <
			j := i + 1
			for j < len(content) && (content[j] == ' ' || content[j] == '\t' || content[j] == '\n' || content[j] == '\r') {
				j++
			}

			if j < len(content) && content[j] == '<' {
				out = append(out, '/', '>')
				changed = true

				// The closing tag that follows is kept. Dropping it used to
				// look like part of the damage, but it is usually a real
				// boundary: swallowing </section> turned two sibling sections
				// into a nested pair, which rewrites the book's structure
				// instead of repairing a missing bracket.
				i = j - 1
				continue
			}
		}

		// Pattern: / followed by whitespace before < (e.g., "/ <" or "/ \n<")
		if next == ' ' || next == '\t' || next == '\n' || next == '\r' {
			if !isPartOfSelfClosingTag(content, i) {
				out = append(out, content[i])
				continue
			}

			// Look ahead for <
			j := i + 1
			for j < len(content) && (content[j] == ' ' || content[j] == '\t' || content[j] == '\n' || content[j] == '\r') {
				j++
			}

			if j < len(content) && content[j] == '<' {
				out = append(out, '/', '>')
				changed = true
				i = j - 1
				continue
			}
		}

		out = append(out, content[i])
	}

	if !changed {
		return content
	}
	return out
}

// extractTagNameAt extracts the tag name starting at the given position
func extractTagNameAt(content []byte, start int) string {
	end := start
	for end < len(content) && isNameChar(content[end]) {
		end++
	}
	if end == start {
		return ""
	}
	return string(content[start:end])
}

// isXMLSpace reports whether the byte is XML whitespace.
func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// hasAttributeBefore reports whether an attribute assignment appears between
// the tag name and the slash, which marks a tag that was probably self-closing
// before its bracket went missing. An attribute is whitespace, then a name,
// then '='; requiring that shape keeps "<foo=bar" from counting, and looking
// past the name is what the previous scan failed to do, so ordinary
// name="value" never matched and the repair covered only a four-tag whitelist.
func hasAttributeBefore(content []byte, nameStart, slashPos int) bool {
	for i := nameStart; i < slashPos; i++ {
		if !isXMLSpace(content[i]) {
			continue
		}
		j := i
		for j < slashPos && isXMLSpace(content[j]) {
			j++
		}
		nameLen := 0
		for j < slashPos && isXMLNameByte(content[j]) {
			j++
			nameLen++
		}
		for j < slashPos && isXMLSpace(content[j]) {
			j++
		}
		if nameLen > 0 && j < slashPos && content[j] == '=' {
			return true
		}
	}
	return false
}

// isXMLNameByte reports whether the byte may appear in an attribute name.
func isXMLNameByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == ':' || b == '-' || b == '_' || b == '.':
		return true
	default:
		return false
	}
}

// isPartOfSelfClosingTag checks if the "/" at position i is part of a self-closing tag
func isPartOfSelfClosingTag(content []byte, slashPos int) bool {
	// Look back to find the opening <
	openPos := -1
	for i := slashPos - 1; i >= 0 && i > slashPos-200; i-- {
		if content[i] == '<' {
			openPos = i
			break
		}
		if content[i] == '>' {
			// Found closing > before opening <, so this / is not part of a tag
			return false
		}
	}

	if openPos == -1 {
		return false
	}

	// Extract tag name
	nameStart := openPos + 1
	for nameStart < slashPos && isXMLSpace(content[nameStart]) {
		nameStart++
	}

	if nameStart >= slashPos {
		return false
	}

	// Check if this looks like an opening tag (not </ or <! or <?)
	if content[nameStart] == '/' || content[nameStart] == '!' || content[nameStart] == '?' {
		return false
	}

	// Tags that are typically self-closing in FB2
	tagName := extractTagNameAt(content, nameStart)
	switch strings.ToLower(tagName) {
	case "image", "empty-line", "br", "img":
		return true
	}

	// A tag carrying attributes is likely a malformed self-closing one.
	hasAttributes := hasAttributeBefore(content, nameStart, slashPos)

	return hasAttributes
}

func sanitizeMissingXlinkPrefix(content []byte) []byte {
	if !bytes.Contains(content, []byte("xmlns:xlink")) {
		return content
	}
	if bytes.Contains(content, []byte("xmlns:l")) {
		return content
	}
	out := bytes.ReplaceAll(content, []byte(" l:href=\""), []byte(" xlink:href=\""))
	return out
}

func sanitizeBrokenEndTags(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if content[i] != '<' || i+2 >= len(content) || content[i+1] != '/' {
			out = append(out, content[i])
			continue
		}

		j := i + 2
		for j < len(content) && isNameChar(content[j]) {
			j++
		}
		if j == i+2 {
			out = append(out, content[i])
			continue
		}

		if j < len(content) && content[j] != '>' {
			out = append(out, content[i:j]...)
			out = append(out, '>')
			changed = true
			i = j - 1
			continue
		}

		out = append(out, content[i])
	}
	if !changed {
		return content
	}
	return out
}

func sanitizeBrokenLangTag(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if i+5 >= len(content) || content[i] != '<' {
			out = append(out, content[i])
			continue
		}
		if !bytes.HasPrefix(content[i:], []byte("<lang")) {
			out = append(out, content[i])
			continue
		}

		nextTagOffset := bytes.IndexByte(content[i+1:], '<')
		if nextTagOffset == -1 {
			out = append(out, content[i])
			continue
		}
		nextTag := i + 1 + nextTagOffset

		gt := bytes.IndexByte(content[i:nextTag], '>')
		if gt != -1 {
			out = append(out, content[i])
			continue
		}

		if !bytes.HasPrefix(content[nextTag:], []byte("</lang>")) {
			out = append(out, content[i])
			continue
		}

		out = append(out, []byte("<lang>")...)
		out = append(out, content[i+5:nextTag]...)
		changed = true
		i = nextTag - 1
		continue
	}
	if !changed {
		return content
	}
	return out
}

func isNameChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == ':', b == '.':
		return true
	default:
		return false
	}
}

func sanitizeXMLVersion(content []byte) []byte {
	if len(content) == 0 {
		return content
	}

	declEnd := bytes.Index(content, []byte("?>"))
	if declEnd == -1 || declEnd > 200 {
		return content
	}
	decl := string(content[:declEnd])
	versionIdx := strings.Index(decl, "version=")
	if versionIdx == -1 {
		return content
	}

	versionIdx += len("version=")
	if versionIdx >= len(decl) {
		return content
	}

	quote := decl[versionIdx]
	if quote != '"' && quote != '\'' {
		return content
	}

	versionIdx++
	end := strings.IndexByte(decl[versionIdx:], quote)
	if end == -1 {
		return content
	}

	version := strings.TrimSpace(decl[versionIdx : versionIdx+end])
	if version == "1.0" {
		return content
	}

	newDecl := decl[:versionIdx] + "1.0" + decl[versionIdx+end:]
	return append([]byte(newDecl), content[declEnd:]...)
}

func stripWhitespace(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		switch r {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// makeCharsetReader creates a reader that converts from the specified charset to UTF-8
func makeCharsetReader(charsetLabel string, input io.Reader) (io.Reader, error) {
	charsetLabel = strings.ToLower(charsetLabel)
	switch charsetLabel {
	case labelUTF8, labelUTF8Bare:
		// Already UTF-8, return as-is
		return input, nil
	case labelCP1251, labelCP1251AliasFlat, labelCP1251AliasDash:
		return transform.NewReader(input, charmap.Windows1251.NewDecoder()), nil
	case labelLatin1, labelLatin1Alias, labelLatin1AliasFlat:
		return transform.NewReader(input, charmap.ISO8859_1.NewDecoder()), nil
	case labelLatin5, labelLatin5Alias, labelLatin5AliasFlat:
		return transform.NewReader(input, charmap.ISO8859_5.NewDecoder()), nil
	case labelKOI8R, labelKOI8RAlias:
		return transform.NewReader(input, charmap.KOI8R.NewDecoder()), nil
	case "koi8-u", "koi8u":
		return transform.NewReader(input, charmap.KOI8U.NewDecoder()), nil
	default:
		reader, err := charset.NewReaderLabel(charsetLabel, input)
		if err != nil {
			return input, nil
		}
		return reader, nil
	}
}

// --- Public methods for combined parsing (used by converter.ParseFB2Complete) ---

// HandleStartElement processes an XML start element during combined parsing.
// This method is called by ParseFB2Complete to feed metadata parser events.
func (p *FB2Parser) HandleStartElement(elem xml.StartElement) {
	p.handleStart(elem)
}

// HandleEndElement processes an XML end element during combined parsing.
// This method is called by ParseFB2Complete to feed metadata parser events.
func (p *FB2Parser) HandleEndElement(elem xml.EndElement) {
	p.handleEnd(elem)
}

// HandleCharData processes XML character data during combined parsing.
// This method is called by ParseFB2Complete to feed metadata parser events.
func (p *FB2Parser) HandleCharData(data xml.CharData) {
	p.handleChar(data)
}

// Reset resets the parser state for reuse in combined parsing.
func (p *FB2Parser) Reset() {
	p.reset()
}

// BuildBookFile constructs a BookFile from collected metadata after parsing.
// This method is called by ParseFB2Complete after the XML has been traversed.
func (p *FB2Parser) BuildBookFile(originalContent []byte) (*BookFile, error) {
	book := &BookFile{
		Title:      p.extractTitle(),
		Authors:    p.extractAuthors(),
		Tags:       p.extractTags(),
		Series:     p.extractSeries(),
		Language:   p.extractLanguage(),
		DocDate:    p.extractDocDate(),
		Annotation: p.extractAnnotation(),
		BodySample: p.extractBodySample(),
		TextSample: p.extractTextSample(),
		Mimetype:   "fb2",
	}

	if p.readCover {
		cover, err := p.extractCover()
		if err != nil {
			book.Issues = append(book.Issues, err.Error())
		} else {
			book.Cover = cover
		}
	}

	// Ensure body sample is present
	p.ensureBodySample(originalContent, book)

	return book, nil
}
