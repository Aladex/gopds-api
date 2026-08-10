package converter

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopds-api/internal/fb2sanitize"
	"gopds-api/internal/parser"
)

// Section and inline nesting accepted by the parser are bounded separately:
// they are different trees with different growth reasons. A catalog probe of
// 488 books from 100 archives found at most 6 nested sections and 4 nested
// inline elements, so 32 leaves a wide margin; anything deeper is a
// pathological file that would overflow the stack in the recursive renderers
// downstream.
const (
	maxFB2SectionDepth = 32
	maxFB2InlineDepth  = 32
)

// Typed errors of the body-parsing pipeline, so callers can branch with
// errors.Is instead of matching message text.
var (
	// ErrNotFictionBook marks input whose first element is not FictionBook —
	// or that the decoder cannot reach any element of at all. A nested
	// FictionBook, a bare <body> or a lexical "fictionbook" substring cannot
	// tell a book from HTML, so they do not count. Returning an empty
	// document here would masquerade as an empty book.
	ErrNotFictionBook = errors.New("fb2: not a FictionBook document")
	// ErrDepthLimit marks section or inline nesting beyond the bounds above:
	// the EPUB renderer walks these trees recursively, so an unbounded file
	// would turn a download into a stack overflow.
	ErrDepthLimit = errors.New("fb2: nesting depth limit exceeded")
)

// fictionBookRoot is the only local name the first element of a real book may
// have. A bare body cannot tell FB2 from HTML, so it does not qualify.
const fictionBookRoot = "FictionBook"

// Paragraph kinds represent different types of FB2 paragraphs
const (
	ParagraphKindNormal     = "p"
	ParagraphKindCite       = "cite"
	ParagraphKindEpigraph   = "epigraph"
	ParagraphKindTextAuthor = "text-author"
	ParagraphKindSubtitle   = "subtitle"
	ParagraphKindPoem       = "poem"
	ParagraphKindPoemLine   = "poem-line"
	ParagraphKindPoemBreak  = "poem-break"
	ParagraphKindEmptyLine  = "empty-line"
	ParagraphKindTable      = "table"
	ParagraphKindImage      = "image"
)

// Inline element types represent formatting and embedded content
const (
	InlineTypeText     = "text"
	InlineTypeStrong   = "strong"
	InlineTypeEmphasis = "emphasis"
	InlineTypeCode     = "code"
	InlineTypeSup      = "sup"
	InlineTypeSub      = "sub"
	InlineTypeLink     = "a"
	InlineTypeImage    = "image"
	InlineTypeBreak    = "br"
)

// FB2Binary holds one decoded <binary> element. The declared content-type is
// kept alongside the data because the parser is the only place it survives;
// consumers decide how much to trust it.
type FB2Binary struct {
	Data []byte // Decoded base64 payload
	MIME string // Declared content-type attribute, may be empty or wrong
}

// FB2Document holds a parsed FB2 body with full structure including sections,
// paragraphs with inline formatting, binary images, and footnotes.
type FB2Document struct {
	Title  string               // Document title from description/title-info
	Body   *FB2BodySection      // Root container of the main body; never a real chapter
	Binary map[string]FB2Binary // Binary images (id -> decoded payload + declared type)
	Notes  []*FB2BodySection    // Footnotes/endnotes from notes body
}

// FB2BodySection represents a section (chapter) with nested subsections.
// The document root (FB2Document.Body) uses the same type as a container:
// its Content holds the top-level chapters and any text placed directly
// under <body>.
type FB2BodySection struct {
	ID      string            // Section ID attribute
	Title   string            // Section title text
	Content []*FB2ContentItem // Paragraphs and subsections in document order
}

// FB2ContentItem is one child of a section, in the order it appears in the
// source. Exactly one of the fields is non-nil. A single ordered sequence is
// the only faithful representation: separate paragraph and section pools
// cannot express text placed between two sections.
type FB2ContentItem struct {
	Paragraph *FB2Paragraph   // Set when the item is a content paragraph
	Section   *FB2BodySection // Set when the item is a nested section
}

// FB2Paragraph holds paragraph content with inline formatting elements.
// The Kind field determines rendering style (normal, cite, epigraph, etc.).
type FB2Paragraph struct {
	ID      string              // Paragraph ID attribute
	Kind    string              // Paragraph type (use ParagraphKind* constants)
	Content []*FB2InlineElement // Inline formatted content
	Text    string              // Plain text version for fallback
	Table   *FB2Table           // Table data if Kind is ParagraphKindTable
}

// FB2InlineElement represents inline formatting inside a paragraph.
// Supports text, strong, emphasis, code, sup, sub, links, images, and breaks.
type FB2InlineElement struct {
	Type     string              // Element type (use InlineType* constants)
	Content  string              // Text content for InlineTypeText
	Children []*FB2InlineElement // Nested inline elements
	Attrs    map[string]string   // Element attributes (href, id, etc.)
}

// FB2Table represents a simple table structure with rows and cells.
type FB2Table struct {
	Rows [][]*FB2TableCell // Table rows, each containing cells
}

// FB2TableCell holds inline content for a single table cell.
type FB2TableCell struct {
	Header  bool                // True if this is a header cell (th)
	Content []*FB2InlineElement // Cell content with inline formatting
	Text    string              // Plain text version
}

// ParseFB2Body parses FB2 XML content into a complete document structure.
//
// This function applies all sanitization steps to handle malformed XML,
// then parses the body element with full support for:
//   - Hierarchical sections and subsections
//   - Inline formatting (strong, emphasis, code, sup, sub)
//   - Images and links
//   - Tables, poems, citations, epigraphs
//   - Binary images (base64 decoded)
//   - Footnotes/endnotes
//
// Returns FB2Document with parsed structure or error if parsing fails.
func ParseFB2Body(xmlContent []byte) (*FB2Document, error) {
	// Step 1: Decode charset and run the shared repair chain
	decoded, err := parser.DecodeToUTF8(xmlContent)
	if err != nil {
		return nil, err
	}
	decoded = fb2sanitize.Apply(decoded)

	// Step 2: Balance critical FB2 structure tags
	decoded = balanceSectionTags(decoded) // Balance <section> tags
	decoded = balanceCommonTags(decoded)  // Balance <p>, <title>, <cite>, etc.

	// Step 3: Final XML repair for any remaining issues
	decoded = repairBrokenXML(decoded)

	doc := &FB2Document{}
	decoder := xml.NewDecoder(bytes.NewReader(decoded))
	decoder.CharsetReader = makeCharsetReader
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose

	state := &fb2BodyState{}
	rootSeen := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if !rootSeen {
				// The sanitizers could not make the root reachable, so this
				// is the same refusal the metadata scanner gives.
				return nil, fmt.Errorf("%w: %w", ErrNotFictionBook, err)
			}
			// The root is verified, so this is a book; whatever broke after
			// it is salvaged lexically.
			return parseFB2BodyLoose(decoded), nil
		}

		switch t := token.(type) {
		case xml.StartElement:
			if !rootSeen {
				rootSeen = true
				// The same criterion the metadata scanner applies: the first
				// element the decoder reaches decides whether this is a book.
				if t.Name.Local != fictionBookRoot {
					return nil, fmt.Errorf("%w: root element is %q, not FictionBook", ErrNotFictionBook, t.Name.Local)
				}
			}
			state.handleStart(doc, t)
		case xml.EndElement:
			state.handleEnd(doc, t)
		case xml.CharData:
			state.handleChar(t)
		}
		if state.err != nil {
			return nil, state.err
		}
	}

	if state.err != nil {
		return nil, state.err
	}
	// A document without a single element is garbage, not an empty book:
	// returning an empty document would masquerade as one.
	if !rootSeen {
		return nil, fmt.Errorf("%w: the document has no root element", ErrNotFictionBook)
	}
	return doc, nil
}

func repairBrokenXML(content []byte) []byte {
	if len(content) == 0 {
		return content
	}

	out := make([]byte, 0, len(content))
	stack := make([]string, 0, 64)

	for i := 0; i < len(content); {
		if content[i] != '<' {
			out = append(out, content[i])
			i++
			continue
		}
		gt := bytes.IndexByte(content[i:], '>')
		if gt == -1 {
			out = append(out, content[i:]...)
			break
		}
		gt += i
		tagBody := content[i+1 : gt]
		tagStr := strings.TrimSpace(string(tagBody))
		if tagStr == "" {
			i = gt + 1
			continue
		}

		if strings.HasPrefix(tagStr, "!--") || strings.HasPrefix(tagStr, "?") || strings.HasPrefix(tagStr, "!") {
			out = append(out, content[i:gt+1]...)
			i = gt + 1
			continue
		}

		isEnd := strings.HasPrefix(tagStr, "/")
		name := parseTagName(tagStr)
		name = normalizeName(name)
		if name == "" {
			out = append(out, content[i:gt+1]...)
			i = gt + 1
			continue
		}

		if isEnd {
			if idx := lastIndex(stack, name); idx != -1 {
				stack = stack[:idx]
				out = append(out, content[i:gt+1]...)
			}
			i = gt + 1
			continue
		}

		selfClosing := strings.HasSuffix(tagStr, "/")
		out = append(out, content[i:gt+1]...)
		if !selfClosing && !isVoidTag(name) {
			stack = append(stack, name)
		}
		i = gt + 1
	}

	for i := len(stack) - 1; i >= 0; i-- {
		out = append(out, []byte("</"+stack[i]+">")...)
	}
	return out
}

func parseTagName(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	if tag[0] == '/' {
		tag = strings.TrimSpace(tag[1:])
	}
	if tag == "" {
		return ""
	}
	var name strings.Builder
	for i := 0; i < len(tag); i++ {
		b := tag[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == ':' || b == '_' || b == '-' || b == '.' {
			name.WriteByte(b)
			continue
		}
		break
	}
	return name.String()
}

func lastIndex(stack []string, name string) int {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == name {
			return i
		}
	}
	return -1
}

func isVoidTag(name string) bool {
	switch name {
	case "br", "img", "image", "empty-line":
		return true
	default:
		return false
	}
}

// parseFB2BodyLoose is the lexical last resort for a document whose
// FictionBook root the XML decoder already verified before breaking. It never
// decides whether the input is a book — the root check alone does that — so
// no lexical marker scan lives here.
func parseFB2BodyLoose(content []byte) *FB2Document {
	segment := extractBodySegment(content)
	section := &FB2BodySection{}
	title := extractFirstTagText(segment, "title")
	if title != "" {
		section.Title = normalizeWhitespace(title)
	}
	paragraphs := extractParagraphs(segment)
	for _, text := range paragraphs {
		section.Content = append(section.Content, &FB2ContentItem{Paragraph: &FB2Paragraph{Text: text}})
	}
	if len(section.Content) == 0 && section.Title == "" {
		// A verified book whose body text could not be salvaged at all.
		return &FB2Document{Body: &FB2BodySection{}}
	}
	return &FB2Document{Body: section}
}

func extractBodySegment(content []byte) []byte {
	lower := bytes.ToLower(content)
	bodyStart := bytes.Index(lower, []byte("<body"))
	if bodyStart == -1 {
		return content
	}
	tagEnd := bytes.IndexByte(lower[bodyStart:], '>')
	if tagEnd == -1 {
		return content[bodyStart:]
	}
	start := bodyStart + tagEnd + 1
	bodyEnd := bytes.Index(lower[start:], []byte("</body>"))
	end := len(content)
	if bodyEnd != -1 {
		end = start + bodyEnd
	}
	return content[start:end]
}

func extractFirstTagText(content []byte, tag string) string {
	openTag := "<" + tag
	closeTag := "</" + tag + ">"
	lower := bytes.ToLower(content)
	openIdx := bytes.Index(lower, []byte(openTag))
	if openIdx == -1 {
		return ""
	}
	gt := bytes.IndexByte(lower[openIdx:], '>')
	if gt == -1 {
		return ""
	}
	start := openIdx + gt + 1
	closeIdx := bytes.Index(lower[start:], []byte(closeTag))
	if closeIdx == -1 {
		return ""
	}
	return stripTags(content[start : start+closeIdx])
}

func extractParagraphs(content []byte) []string {
	var paragraphs []string
	lower := bytes.ToLower(content)
	i := 0
	for {
		openIdx := bytes.Index(lower[i:], []byte("<p"))
		if openIdx == -1 {
			break
		}
		openIdx += i
		gt := bytes.IndexByte(lower[openIdx:], '>')
		if gt == -1 {
			break
		}
		start := openIdx + gt + 1
		end := len(content)
		closeIdx := bytes.Index(lower[start:], []byte("</p>"))
		nextOpen := bytes.Index(lower[start:], []byte("<p"))
		if closeIdx != -1 {
			end = start + closeIdx
		}
		if nextOpen != -1 && start+nextOpen < end {
			end = start + nextOpen
		}
		text := normalizeWhitespace(stripTags(content[start:end]))
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
		i = end
	}
	return paragraphs
}

func stripTags(content []byte) string {
	var out strings.Builder
	out.Grow(len(content))
	inTag := false
	for i := 0; i < len(content); i++ {
		b := content[i]
		switch b {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteByte(b)
			}
		}
	}
	return strings.TrimSpace(out.String())
}

type fb2BodyState struct {
	inBody            bool
	bodyDepth         int
	skipBody          bool
	inNotes           bool
	err               error
	sectionStack      []*FB2BodySection
	inTitle           bool
	inSubtitle        bool
	inParagraph       bool
	inTitleParagraph  bool
	inCite            bool
	inEpigraph        bool
	inTextAuthor      bool
	inPoem            bool
	inStanza          bool
	inTable           bool
	currentParagraph  *FB2Paragraph
	currentTag        string
	inlineStack       []*FB2InlineElement
	currentTable      *FB2Table
	currentRow        []*FB2TableCell
	currentCell       *FB2TableCell
	inBinary          bool
	currentBinaryID   string
	currentBinaryMIME string
	binaryBuf         strings.Builder
}

func (s *fb2BodyState) handleStart(doc *FB2Document, elem xml.StartElement) {
	local := normalizeName(elem.Name.Local)
	attrs := normalizeAttrs(elem.Attr)

	if local == "binary" {
		s.inBinary = true
		s.currentBinaryID = strings.ToLower(strings.TrimSpace(attrs["id"]))
		s.currentBinaryMIME = strings.TrimSpace(attrs["content-type"])
		s.binaryBuf.Reset()
		return
	}

	if local == "body" {
		s.bodyDepth++
		if s.inBody {
			return
		}
		if isNotesBody(attrs) {
			s.inNotes = true
		} else {
			s.inNotes = false
		}
		s.inBody = true
		return
	}

	if !s.inBody || s.skipBody {
		return
	}

	switch local {
	case "section":
		// Bounded by maxFB2SectionDepth: the EPUB renderer walks the tree
		// recursively, so an unbounded file would turn a download into a
		// stack overflow.
		if len(s.sectionStack) >= maxFB2SectionDepth {
			s.err = fmt.Errorf("%w: section nesting exceeds maximum depth %d", ErrDepthLimit, maxFB2SectionDepth)
			return
		}
		section := &FB2BodySection{ID: attrs["id"]}
		if len(s.sectionStack) == 0 {
			if s.inNotes {
				doc.Notes = append(doc.Notes, section)
			} else {
				root := ensureBodyRoot(doc)
				root.Content = append(root.Content, &FB2ContentItem{Section: section})
			}
		} else {
			parent := s.sectionStack[len(s.sectionStack)-1]
			parent.Content = append(parent.Content, &FB2ContentItem{Section: section})
		}
		s.sectionStack = append(s.sectionStack, section)
	case "title":
		s.inTitle = true
	case "subtitle":
		s.inSubtitle = true
		s.startTextBlock("subtitle", ParagraphKindSubtitle, attrs)
	case "epigraph":
		s.inEpigraph = true
	case "text-author":
		s.inTextAuthor = true
		s.startTextBlock("text-author", s.textAuthorKind(), attrs)
	case "cite":
		s.inCite = true
	case "poem":
		s.inPoem = true
	case "stanza":
		s.inStanza = true
	case "table":
		if !s.inTable {
			s.inTable = true
			s.currentTable = &FB2Table{}
			s.currentRow = nil
			s.currentCell = nil
			s.appendParagraphRaw(doc, &FB2Paragraph{Kind: ParagraphKindTable, Table: s.currentTable})
		}
	case "tr", "row":
		if s.inTable {
			s.currentRow = nil
		}
	case "td", "th":
		if s.inTable {
			s.currentCell = &FB2TableCell{Header: local == "th"}
		}
	case "p":
		s.inTitleParagraph = s.inTitle
		kind := s.defaultParagraphKind()
		s.startTextBlock("p", kind, attrs)
	case "v":
		if s.inPoem {
			s.startTextBlock("v", ParagraphKindPoemLine, attrs)
		}
	case "empty-line":
		s.appendEmptyLine(doc)
	case "strong", "emphasis", "code", "sup", "sub", "a":
		s.pushInline(local, attrs)
	case "image":
		if s.inParagraph || s.currentCell != nil {
			s.appendInline(&FB2InlineElement{Type: InlineTypeImage, Attrs: attrs})
			return
		}
		paragraph := &FB2Paragraph{
			Kind:    ParagraphKindImage,
			Content: []*FB2InlineElement{{Type: InlineTypeImage, Attrs: attrs}},
		}
		s.appendParagraphRaw(doc, paragraph)
	case "br":
		s.appendInline(&FB2InlineElement{Type: InlineTypeBreak})
	}
}

func (s *fb2BodyState) handleEnd(doc *FB2Document, elem xml.EndElement) {
	local := normalizeName(elem.Name.Local)

	if local == "binary" {
		if s.inBinary {
			s.storeBinary(doc)
		}
		s.inBinary = false
		s.currentBinaryID = ""
		s.currentBinaryMIME = ""
		s.binaryBuf.Reset()
		return
	}

	if local == "body" {
		if s.bodyDepth > 0 {
			s.bodyDepth--
		}
		if s.bodyDepth == 0 {
			s.inBody = false
			s.inNotes = false
			s.skipBody = false
		}
		return
	}

	if !s.inBody || s.skipBody {
		return
	}

	switch local {
	case "title":
		s.inTitle = false
	case "subtitle":
		s.inSubtitle = false
		s.finishTextBlock(doc, "subtitle")
	case "epigraph":
		s.inEpigraph = false
	case "text-author":
		s.inTextAuthor = false
		s.finishTextBlock(doc, "text-author")
	case "cite":
		s.inCite = false
	case "poem":
		s.inPoem = false
	case "stanza":
		s.inStanza = false
		if s.inPoem {
			s.appendPoemBreak(doc)
		}
	case "p":
		s.finishTextBlock(doc, "p")
	case "v":
		s.finishTextBlock(doc, "v")
	case "strong", "emphasis", "code", "sup", "sub", "a":
		s.popInline(local)
	case "td", "th":
		if s.inTable {
			s.finalizeCell()
		}
	case "tr", "row":
		if s.inTable && len(s.currentRow) > 0 {
			s.currentTable.Rows = append(s.currentTable.Rows, s.currentRow)
			s.currentRow = nil
		}
	case "table":
		if s.inTable {
			s.inTable = false
			s.currentTable = nil
			s.currentRow = nil
			s.currentCell = nil
			s.inlineStack = nil
		}
	case "section":
		if len(s.sectionStack) > 0 {
			s.sectionStack = s.sectionStack[:len(s.sectionStack)-1]
		}
	}
}

func (s *fb2BodyState) handleChar(data xml.CharData) {
	if s.inBinary {
		s.binaryBuf.Write([]byte(data))
		return
	}
	if !s.inBody || s.skipBody {
		return
	}
	if s.inParagraph || s.currentCell != nil {
		s.appendText(string(data))
	}
}

func (s *fb2BodyState) currentSection() *FB2BodySection {
	if len(s.sectionStack) == 0 {
		return nil
	}
	return s.sectionStack[len(s.sectionStack)-1]
}

func (s *fb2BodyState) appendParagraph(doc *FB2Document, paragraph *FB2Paragraph, text string) {
	if paragraph == nil {
		paragraph = &FB2Paragraph{}
	}
	if text != "" {
		paragraph.Text = text
	}
	s.appendParagraphRaw(doc, paragraph)
}

// ensureBodyRoot lazily creates the root container. It is a pure container:
// real chapters are always its children, never the root itself.
func ensureBodyRoot(doc *FB2Document) *FB2BodySection {
	if doc.Body == nil {
		doc.Body = &FB2BodySection{}
	}
	return doc.Body
}

func (s *fb2BodyState) appendParagraphRaw(doc *FB2Document, paragraph *FB2Paragraph) {
	if paragraph == nil {
		return
	}
	section := s.currentSection()
	if section == nil {
		if s.inNotes {
			section = &FB2BodySection{}
			doc.Notes = append(doc.Notes, section)
		} else {
			section = ensureBodyRoot(doc)
		}
	}
	section.Content = append(section.Content, &FB2ContentItem{Paragraph: paragraph})
}

func (s *fb2BodyState) appendInline(el *FB2InlineElement) {
	if el == nil {
		return
	}
	if len(s.inlineStack) == 0 {
		if s.currentParagraph != nil {
			s.currentParagraph.Content = append(s.currentParagraph.Content, el)
		} else if s.currentCell != nil {
			s.currentCell.Content = append(s.currentCell.Content, el)
		}
		return
	}
	parent := s.inlineStack[len(s.inlineStack)-1]
	parent.Children = append(parent.Children, el)
}

func (s *fb2BodyState) pushInline(tag string, attrs map[string]string) {
	if !s.inParagraph && s.currentCell == nil {
		return
	}
	// Same depth bound as sections: inline elements render recursively too.
	if len(s.inlineStack) >= maxFB2InlineDepth {
		s.err = fmt.Errorf("%w: inline nesting exceeds maximum depth %d", ErrDepthLimit, maxFB2InlineDepth)
		return
	}
	el := &FB2InlineElement{
		Type:  tag,
		Attrs: attrs,
	}
	s.appendInline(el)
	s.inlineStack = append(s.inlineStack, el)
}

func (s *fb2BodyState) popInline(tag string) {
	if len(s.inlineStack) == 0 {
		return
	}
	top := s.inlineStack[len(s.inlineStack)-1]
	if top.Type != tag {
		return
	}
	s.inlineStack = s.inlineStack[:len(s.inlineStack)-1]
}

func (s *fb2BodyState) appendText(text string) {
	if s.currentParagraph == nil && s.currentCell == nil {
		return
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if normalized == "" {
		return
	}
	s.appendInline(&FB2InlineElement{Type: InlineTypeText, Content: normalized})
}

func (s *fb2BodyState) startTextBlock(tag string, kind string, attrs map[string]string) {
	if s.inParagraph {
		return
	}
	s.inParagraph = true
	s.currentTag = tag
	s.currentParagraph = &FB2Paragraph{ID: attrs["id"], Kind: kind}
	s.inlineStack = nil
}

func (s *fb2BodyState) finishTextBlock(doc *FB2Document, tag string) {
	if !s.inParagraph || s.currentTag != tag {
		return
	}
	text := s.normalizeTextForKind(s.currentParagraph)
	if text != "" {
		if s.inTitleParagraph {
			if section := s.currentSection(); section != nil && section.Title == "" {
				section.Title = text
			}
		} else if s.inTable && s.currentCell != nil {
			s.appendCellParagraph(s.currentParagraph)
		} else {
			s.appendParagraph(doc, s.currentParagraph, text)
		}
	}
	s.inParagraph = false
	s.inTitleParagraph = false
	s.currentParagraph = nil
	s.currentTag = ""
	s.inlineStack = nil
}

func (s *fb2BodyState) normalizeTextForKind(paragraph *FB2Paragraph) string {
	if paragraph == nil {
		return ""
	}
	raw := inlineText(paragraph)
	if paragraph.Kind == ParagraphKindPoemLine {
		return strings.TrimSpace(raw)
	}
	return normalizeWhitespace(raw)
}

func (s *fb2BodyState) defaultParagraphKind() string {
	if s.inEpigraph {
		if s.inTextAuthor {
			return "epigraph-author"
		}
		return ParagraphKindEpigraph
	}
	if s.inCite {
		return ParagraphKindCite
	}
	if s.inPoem {
		return ParagraphKindPoem
	}
	return ParagraphKindNormal
}

func (s *fb2BodyState) textAuthorKind() string {
	if s.inEpigraph {
		return ParagraphKindTextAuthor
	}
	return ParagraphKindTextAuthor
}

func (s *fb2BodyState) appendEmptyLine(doc *FB2Document) {
	if s.inPoem {
		s.appendPoemBreak(doc)
		return
	}
	s.appendParagraphRaw(doc, &FB2Paragraph{Kind: ParagraphKindEmptyLine})
}

func (s *fb2BodyState) appendPoemBreak(doc *FB2Document) {
	s.appendParagraphRaw(doc, &FB2Paragraph{Kind: ParagraphKindPoemBreak})
}

func (s *fb2BodyState) storeBinary(doc *FB2Document) {
	if doc == nil || s.currentBinaryID == "" {
		return
	}
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(s.binaryBuf.String()))
	decoded, err := io.ReadAll(decoder)
	if err != nil || len(decoded) == 0 {
		return
	}
	if doc.Binary == nil {
		doc.Binary = make(map[string]FB2Binary)
	}
	if _, exists := doc.Binary[s.currentBinaryID]; !exists {
		doc.Binary[s.currentBinaryID] = FB2Binary{Data: decoded, MIME: s.currentBinaryMIME}
	}
}

func (s *fb2BodyState) appendCellParagraph(paragraph *FB2Paragraph) {
	if paragraph == nil || s.currentCell == nil {
		return
	}
	if len(s.currentCell.Content) > 0 {
		s.currentCell.Content = append(s.currentCell.Content, &FB2InlineElement{Type: InlineTypeBreak})
	}
	if len(paragraph.Content) > 0 {
		s.currentCell.Content = append(s.currentCell.Content, paragraph.Content...)
	} else if strings.TrimSpace(paragraph.Text) != "" {
		s.currentCell.Content = append(s.currentCell.Content, &FB2InlineElement{Type: InlineTypeText, Content: paragraph.Text})
	}
}

func (s *fb2BodyState) finalizeCell() {
	if s.currentCell == nil {
		return
	}
	s.currentCell.Text = normalizeWhitespace(inlineTextElements(s.currentCell.Content))
	s.currentRow = append(s.currentRow, s.currentCell)
	s.currentCell = nil
}

func inlineText(paragraph *FB2Paragraph) string {
	if paragraph == nil {
		return ""
	}
	return inlineTextElements(paragraph.Content)
}

func inlineTextElements(elements []*FB2InlineElement) string {
	if len(elements) == 0 {
		return ""
	}
	var out strings.Builder
	for _, el := range elements {
		if el == nil {
			continue
		}
		switch el.Type {
		case InlineTypeText:
			out.WriteString(el.Content)
		default:
			if len(el.Children) > 0 {
				out.WriteString(inlineTextElements(el.Children))
			}
		}
	}
	return out.String()
}

func isNotesBody(attrs map[string]string) bool {
	if attrs == nil {
		return false
	}
	name, ok := attrs["name"]
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(name), "notes")
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
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

func normalizeWhitespace(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}
