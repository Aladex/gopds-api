package converter

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"strings"
)

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

// FB2Document holds a parsed FB2 body with full structure including sections,
// paragraphs with inline formatting, binary images, and footnotes.
type FB2Document struct {
	Title  string            // Document title from description/title-info
	Body   *FB2BodySection   // Main body with hierarchical sections
	Binary map[string][]byte // Binary images (id -> decoded base64 data)
	Notes  []*FB2BodySection // Footnotes/endnotes from notes body
}

// FB2BodySection represents a section (chapter) with nested subsections.
// Sections can contain paragraphs and recursively nested child sections.
type FB2BodySection struct {
	ID          string            // Section ID attribute
	Title       string            // Section title text
	Paragraphs  []*FB2Paragraph   // Content paragraphs
	SubSections []*FB2BodySection // Nested subsections
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
	decoded := tryDecodeCharset(xmlContent)
	decoded = sanitizeControlChars(decoded)
	decoded = sanitizeInvalidTagOpenings(decoded)
	decoded = sanitizeInvalidProcessingInstructions(decoded)
	decoded = sanitizeInvalidAmpersands(decoded)
	decoded = sanitizeXMLVersion(decoded)
	decoded = sanitizeBrokenSelfClosingTags(decoded)
	decoded = sanitizeBrokenEndTags(decoded)
	decoded = sanitizeBrokenLangTag(decoded)
	decoded = sanitizeMissingXlinkPrefix(decoded)

	doc := &FB2Document{}
	decoder := xml.NewDecoder(bytes.NewReader(decoded))
	decoder.CharsetReader = makeCharsetReader
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose

	state := &fb2BodyState{}
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
			state.handleStart(doc, t)
		case xml.EndElement:
			state.handleEnd(doc, t)
		case xml.CharData:
			state.handleChar(t)
		}
	}

	if doc.Body == nil && len(state.sectionStack) > 0 {
		doc.Body = state.sectionStack[0]
	}
	return doc, nil
}

type fb2BodyState struct {
	inBody           bool
	bodyDepth        int
	skipBody         bool
	inNotes          bool
	sectionStack     []*FB2BodySection
	inTitle          bool
	inSubtitle       bool
	inParagraph      bool
	inTitleParagraph bool
	inCite           bool
	inEpigraph       bool
	inTextAuthor     bool
	inPoem           bool
	inStanza         bool
	inTable          bool
	currentParagraph *FB2Paragraph
	currentTag       string
	inlineStack      []*FB2InlineElement
	currentTable     *FB2Table
	currentRow       []*FB2TableCell
	currentCell      *FB2TableCell
	inBinary         bool
	currentBinaryID  string
	binaryBuf        strings.Builder
}

func (s *fb2BodyState) handleStart(doc *FB2Document, elem xml.StartElement) {
	local := normalizeName(elem.Name.Local)
	attrs := normalizeAttrs(elem.Attr)

	if local == "binary" {
		s.inBinary = true
		s.currentBinaryID = strings.ToLower(strings.TrimSpace(attrs["id"]))
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
		section := &FB2BodySection{ID: attrs["id"]}
		if len(s.sectionStack) == 0 {
			if s.inNotes {
				doc.Notes = append(doc.Notes, section)
			} else if doc.Body == nil {
				doc.Body = section
			} else {
				doc.Body.SubSections = append(doc.Body.SubSections, section)
			}
		} else {
			parent := s.sectionStack[len(s.sectionStack)-1]
			parent.SubSections = append(parent.SubSections, section)
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
		s.appendInline(&FB2InlineElement{Type: InlineTypeImage, Attrs: attrs})
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
	paragraph.Text = text
	section := s.currentSection()
	if section == nil {
		if s.inNotes {
			section = &FB2BodySection{}
			doc.Notes = append(doc.Notes, section)
		} else {
			if doc.Body == nil {
				doc.Body = &FB2BodySection{}
			}
			section = doc.Body
		}
	}
	section.Paragraphs = append(section.Paragraphs, paragraph)
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
			if doc.Body == nil {
				doc.Body = &FB2BodySection{}
			}
			section = doc.Body
		}
	}
	section.Paragraphs = append(section.Paragraphs, paragraph)
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
		doc.Binary = make(map[string][]byte)
	}
	if _, exists := doc.Binary[s.currentBinaryID]; !exists {
		doc.Binary[s.currentBinaryID] = decoded
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
