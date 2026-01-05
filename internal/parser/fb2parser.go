package parser

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"strings"
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
}

// NewFB2Parser creates a parser configured to read cover data if requested.
func NewFB2Parser(readCover bool) *FB2Parser {
	parser := &FB2Parser{
		readCover: readCover,
		handlers: map[string]*TagHandler{
			"title":       NewTagHandler([]string{"description", "title-info", "book-title"}),
			"authorFirst": NewTagHandler([]string{"description", "title-info", "author", "first-name"}),
			"authorLast":  NewTagHandler([]string{"description", "title-info", "author", "last-name"}),
			"genre":       NewTagHandler([]string{"description", "title-info", "genre"}),
			"lang":        NewTagHandler([]string{"description", "title-info", "lang"}),
			"series":      NewTagHandler([]string{"description", "title-info", "sequence"}),
			"annotation":  NewTagHandler([]string{"description", "title-info", "annotation", "p"}),
			"docdate":     NewTagHandler([]string{"description", "document-info", "date"}),
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
	p.reset()

	decoder := xml.NewDecoder(reader)
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
			p.handleStart(t)
		case xml.EndElement:
			p.handleEnd(t)
		case xml.CharData:
			p.handleChar(t)
		}
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
	return sanitizeText(values[0])
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
			first = strings.TrimSpace(firstNames[i])
		}
		if i < len(lastNames) {
			last = strings.TrimSpace(lastNames[i])
		}
		full := strings.TrimSpace(strings.Join([]string{first, last}, " "))
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
	text := p.handlers["annotation"].GetText("\n")
	return strings.TrimSpace(text)
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
