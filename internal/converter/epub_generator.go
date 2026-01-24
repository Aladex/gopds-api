package converter

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"

	"gopds-api/internal/parser"
)

// EPUBGenerator builds a valid EPUB 3.0 archive with EPUB 2.0 compatibility.
// It manages image references, section anchors, and note links during generation.
type EPUBGenerator struct {
	images         map[string]epubImage       // Images from FB2 binary elements
	sectionAnchors map[*FB2BodySection]string // Section ID to anchor mapping
	sectionFiles   map[*FB2BodySection]string // Section to filename mapping
	anchorSeq      int                        // Sequential anchor counter
	notesAnchors   map[string]string          // Note ID to anchor mapping
	notesFile      string                     // Filename for notes page
}

// NewEPUBGenerator creates a new EPUB generator instance.
func NewEPUBGenerator() *EPUBGenerator {
	return &EPUBGenerator{}
}

// GenerateEPUB creates a complete EPUB 3.0 archive from FB2 document and metadata.
//
// The generated EPUB includes:
//   - Cover page (from BookFile.Cover or first image)
//   - Title page with author and book title
//   - Table of contents (EPUB 2.0 NCX + EPUB 3.0 NAV)
//   - All chapters with proper hierarchy and formatting
//   - Notes/footnotes page (if present in FB2)
//   - Embedded images from FB2 binary elements
//   - CSS styling for all paragraph types and formatting
//
// Returns an io.ReadCloser containing the complete EPUB as a ZIP archive,
// or an error if document is nil or generation fails.
//
// The EPUB structure follows the IDPF EPUB 3.0 specification with backward
// compatibility for EPUB 2.0 readers.
func (g *EPUBGenerator) GenerateEPUB(doc *FB2Document, bookFile *parser.BookFile) (io.ReadCloser, error) {
	if doc == nil {
		return nil, fmt.Errorf("fb2 document is nil")
	}

	// Graceful degradation: if body parsing failed, create minimal document
	if doc.Body == nil {
		doc.Body = &FB2BodySection{
			Title: safeTitle(bookFile),
			Paragraphs: []*FB2Paragraph{
				{Text: "Unable to parse book content", Kind: ParagraphKindNormal},
			},
		}
	}

	g.images = buildImages(doc)
	g.sectionAnchors = make(map[*FB2BodySection]string)
	g.sectionFiles = make(map[*FB2BodySection]string)
	g.anchorSeq = 0
	cover := buildCover(bookFile, g.images)
	titlePage := buildTitlePage(bookFile)
	if titlePage != nil {
		g.anchorSeq = 1
	}
	tocPage := buildTocPage()
	notesPage := g.buildNotesPage(doc)

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)

	if err := writeStoredFile(zipWriter, "mimetype", "application/epub+zip"); err != nil {
		return nil, err
	}
	if err := writeFile(zipWriter, "META-INF/container.xml", buildContainerXML()); err != nil {
		return nil, err
	}

	chapters := g.buildSectionFiles(doc)
	if len(chapters) == 0 {
		chapters = append(chapters, &epubChapter{Title: safeTitle(bookFile), Filename: "index001.xhtml", Body: ""})
	}

	for i := range chapters {
		if chapters[i].Filename == "" {
			chapters[i].Filename = fmt.Sprintf("index%03d.xhtml", i+1)
		}
		xhtml := buildChapterXHTML(chapters[i])
		if err := writeFile(zipWriter, path.Join("OEBPS", chapters[i].Filename), xhtml); err != nil {
			return nil, err
		}
	}

	tocNodes := g.buildTOC(doc, chapters, titlePage)

	if cover != nil {
		coverXHTML := buildCoverXHTML(cover)
		if err := writeFile(zipWriter, path.Join("OEBPS", cover.XHTMLFilename), coverXHTML); err != nil {
			return nil, err
		}
		if !cover.FromImages {
			if err := writeFileBytes(zipWriter, path.Join("OEBPS", "images", cover.Image.Filename), cover.Image.Data); err != nil {
				return nil, err
			}
		}
	}

	if titlePage != nil {
		if err := writeFile(zipWriter, path.Join("OEBPS", titlePage.XHTMLFilename), titlePage.Content); err != nil {
			return nil, err
		}
	}

	if err := writeFile(zipWriter, "OEBPS/style.css", buildStyleCSS()); err != nil {
		return nil, err
	}

	for _, image := range g.images {
		if err := writeFileBytes(zipWriter, path.Join("OEBPS", "images", image.Filename), image.Data); err != nil {
			return nil, err
		}
	}

	if notesPage != nil {
		if err := writeFile(zipWriter, path.Join("OEBPS", notesPage.XHTMLFilename), notesPage.Content); err != nil {
			return nil, err
		}
	}

	navXHTML := buildNavXHTML(tocNodes)
	if err := writeFile(zipWriter, "OEBPS/nav.xhtml", navXHTML); err != nil {
		return nil, err
	}

	if tocPage != nil {
		tocXHTML := buildTocXHTML(tocNodes)
		if err := writeFile(zipWriter, path.Join("OEBPS", tocPage.XHTMLFilename), tocXHTML); err != nil {
			return nil, err
		}
	}

	tocNCX := buildTocNCX(bookFile, tocNodes, tocPage)
	if err := writeFile(zipWriter, "OEBPS/toc.ncx", tocNCX); err != nil {
		return nil, err
	}

	contentOPF := buildContentOPF(bookFile, chapters, g.images, cover, titlePage, tocPage, notesPage)
	if err := writeFile(zipWriter, "OEBPS/content.opf", contentOPF); err != nil {
		return nil, err
	}

	if err := zipWriter.Close(); err != nil {
		return nil, err
	}

	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

type epubChapter struct {
	Title    string
	Filename string
	Body     string
	Section  *FB2BodySection
	Depth    int
}

func (g *EPUBGenerator) buildSectionFiles(doc *FB2Document) []*epubChapter {
	if doc == nil || doc.Body == nil {
		return nil
	}
	var nodes []*sectionNode
	topSections := doc.Body.SubSections
	if len(topSections) == 0 {
		topSections = []*FB2BodySection{doc.Body}
	}
	for _, section := range topSections {
		g.collectSections(section, 1, &nodes)
	}

	chapters := make([]*epubChapter, 0, len(nodes))
	for i, node := range nodes {
		if node == nil || node.Section == nil {
			continue
		}
		filename := fmt.Sprintf("index%03d.xhtml", i+1)
		g.sectionFiles[node.Section] = filename
		chapters = append(chapters, g.sectionToFile(node.Section, node.Depth, filename))
	}
	return chapters
}

type sectionNode struct {
	Section *FB2BodySection
	Depth   int
}

func (g *EPUBGenerator) collectSections(section *FB2BodySection, depth int, out *[]*sectionNode) {
	if section == nil || out == nil {
		return
	}
	*out = append(*out, &sectionNode{Section: section, Depth: depth})
	for _, sub := range section.SubSections {
		g.collectSections(sub, depth+1, out)
	}
}

func (g *EPUBGenerator) sectionToFile(section *FB2BodySection, depth int, filename string) *epubChapter {
	if section == nil {
		return &epubChapter{}
	}
	var body strings.Builder
	g.renderSectionShallow(&body, section, depth)
	return &epubChapter{
		Title:    section.Title,
		Body:     body.String(),
		Section:  section,
		Depth:    depth,
		Filename: filename,
	}
}

func (g *EPUBGenerator) renderSection(builder *strings.Builder, section *FB2BodySection, level int) {
	if section == nil {
		return
	}
	g.renderSectionHeader(builder, section, level)
	g.renderParagraphs(builder, section.Paragraphs)

	for _, sub := range section.SubSections {
		g.renderSection(builder, sub, level+1)
	}
}

func (g *EPUBGenerator) renderSectionShallow(builder *strings.Builder, section *FB2BodySection, level int) {
	if section == nil {
		return
	}
	g.renderSectionHeader(builder, section, level)
	g.renderParagraphs(builder, section.Paragraphs)
}

func (g *EPUBGenerator) renderSectionHeader(builder *strings.Builder, section *FB2BodySection, level int) {
	if section == nil || builder == nil {
		return
	}
	anchor := g.sectionAnchor(section)
	if anchor != "" {
		builder.WriteString("<a id=\"")
		builder.WriteString(html.EscapeString(anchor))
		builder.WriteString("\"></a>\n")
	}
	if section.Title == "" {
		return
	}
	heading := fmt.Sprintf("h%d", clampHeading(level))
	builder.WriteString("<")
	builder.WriteString(heading)
	builder.WriteString(">")
	builder.WriteString(html.EscapeString(section.Title))
	builder.WriteString("</")
	builder.WriteString(heading)
	builder.WriteString(">\n")
}

func (g *EPUBGenerator) renderParagraphs(builder *strings.Builder, paragraphs []*FB2Paragraph) {
	if builder == nil || len(paragraphs) == 0 {
		return
	}
	for i := 0; i < len(paragraphs); {
		p := paragraphs[i]
		if p != nil && (p.Kind == "poem-line" || p.Kind == "poem-break") {
			i = g.renderPoemBlock(builder, paragraphs, i)
			continue
		}
		paragraph := g.renderParagraph(p)
		if paragraph != "" {
			builder.WriteString(paragraph)
		}
		i++
	}
}

func clampHeading(level int) int {
	if level < 1 {
		return 1
	}
	if level > 6 {
		return 6
	}
	return level
}

func (g *EPUBGenerator) renderParagraph(p *FB2Paragraph) string {
	if p == nil {
		return ""
	}
	var content strings.Builder
	if len(p.Content) > 0 {
		g.renderInlineElements(&content, p.Content)
	} else if strings.TrimSpace(p.Text) != "" {
		content.WriteString(html.EscapeString(p.Text))
	}
	if strings.TrimSpace(content.String()) == "" {
		return ""
	}
	switch p.Kind {
	case "cite":
		return "<p class=\"cite\">" + content.String() + "</p>\n"
	case "epigraph":
		return "<p class=\"epigraph\">" + content.String() + "</p>\n"
	case "text-author":
		return "<p class=\"text-author\">" + content.String() + "</p>\n"
	case "subtitle":
		return "<p class=\"subtitle\">" + content.String() + "</p>\n"
	case "poem":
		return "<p class=\"poem-line\">" + content.String() + "</p>\n"
	case "poem-line":
		return "<p class=\"poem-line\">" + content.String() + "</p>\n"
	case "poem-break":
		return "<div class=\"stanza\"></div>\n"
	case "empty-line":
		return "<div class=\"emptyline\"></div>\n"
	case "table":
		return g.renderTable(p.Table)
	case "image":
		return "<div class=\"image\">" + content.String() + "</div>\n"
	default:
		return "<p>" + content.String() + "</p>\n"
	}
}

func (g *EPUBGenerator) renderPoemBlock(builder *strings.Builder, paragraphs []*FB2Paragraph, start int) int {
	if builder == nil || len(paragraphs) == 0 || start >= len(paragraphs) {
		return start + 1
	}
	builder.WriteString("<div class=\"poem\">\n")
	builder.WriteString("  <div class=\"stanza\">\n")
	for i := start; i < len(paragraphs); i++ {
		p := paragraphs[i]
		if p == nil {
			continue
		}
		if p.Kind != "poem-line" && p.Kind != "poem-break" {
			builder.WriteString("  </div>\n</div>\n")
			return i
		}
		if p.Kind == "poem-break" {
			builder.WriteString("  </div>\n  <div class=\"stanza\">\n")
			continue
		}
		line := g.inlineContent(p)
		if strings.TrimSpace(line) == "" {
			continue
		}
		builder.WriteString("    <p>")
		builder.WriteString(line)
		builder.WriteString("</p>\n")
	}
	builder.WriteString("  </div>\n</div>\n")
	return len(paragraphs)
}

func (g *EPUBGenerator) inlineContent(p *FB2Paragraph) string {
	if p == nil {
		return ""
	}
	if len(p.Content) == 0 {
		return html.EscapeString(p.Text)
	}
	var content strings.Builder
	g.renderInlineElements(&content, p.Content)
	return content.String()
}

func (g *EPUBGenerator) renderInlineElements(builder *strings.Builder, elements []*FB2InlineElement) {
	for _, el := range elements {
		if el == nil {
			continue
		}
		switch el.Type {
		case "text":
			builder.WriteString(html.EscapeString(el.Content))
		case "strong", "emphasis", "code", "sup", "sub":
			tag := inlineTag(el.Type)
			builder.WriteString("<")
			builder.WriteString(tag)
			builder.WriteString(">")
			g.renderInlineElements(builder, el.Children)
			builder.WriteString("</")
			builder.WriteString(tag)
			builder.WriteString(">")
		case "a":
			href := ""
			if el.Attrs != nil {
				href = strings.TrimSpace(el.Attrs["href"])
			}
			href = g.resolveNoteHref(href, el.Attrs)
			if href == "" {
				g.renderInlineElements(builder, el.Children)
				break
			}
			builder.WriteString("<a href=\"")
			builder.WriteString(html.EscapeString(href))
			builder.WriteString("\">")
			g.renderInlineElements(builder, el.Children)
			builder.WriteString("</a>")
		case "br":
			builder.WriteString("<br/>")
		case "image":
			g.renderImage(builder, el)
		default:
			g.renderInlineElements(builder, el.Children)
		}
	}
}

func (g *EPUBGenerator) resolveNoteHref(href string, attrs map[string]string) string {
	if g == nil || g.notesFile == "" || len(g.notesAnchors) == 0 {
		return href
	}
	if strings.TrimSpace(href) == "" {
		return href
	}
	noteType := ""
	if attrs != nil {
		noteType = strings.ToLower(strings.TrimSpace(attrs["type"]))
	}
	if !strings.HasPrefix(href, "#") && noteType != "note" {
		return href
	}
	noteID := strings.TrimPrefix(strings.TrimSpace(href), "#")
	if noteID == "" {
		return href
	}
	if anchor, ok := g.notesAnchors[noteID]; ok {
		return g.notesFile + "#" + anchor
	}
	return href
}

func inlineTag(tag string) string {
	switch tag {
	case "emphasis":
		return "em"
	default:
		return tag
	}
}

func (g *EPUBGenerator) renderTable(table *FB2Table) string {
	if table == nil || len(table.Rows) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("<table class=\"table\">\n")
	for _, row := range table.Rows {
		if len(row) == 0 {
			continue
		}
		out.WriteString("  <tr>")
		for _, cell := range row {
			if cell == nil {
				continue
			}
			tag := "td"
			if cell.Header {
				tag = "th"
			}
			out.WriteString("<")
			out.WriteString(tag)
			out.WriteString(">")
			if len(cell.Content) > 0 {
				var cellContent strings.Builder
				g.renderInlineElements(&cellContent, cell.Content)
				out.WriteString(cellContent.String())
			} else if strings.TrimSpace(cell.Text) != "" {
				out.WriteString(html.EscapeString(cell.Text))
			}
			out.WriteString("</")
			out.WriteString(tag)
			out.WriteString(">")
		}
		out.WriteString("</tr>\n")
	}
	out.WriteString("</table>\n")
	return out.String()
}

func buildChapterXHTML(chapter *epubChapter) string {
	title := "Chapter"
	if chapter != nil && chapter.Title != "" {
		title = chapter.Title
	}
	body := ""
	if chapter != nil {
		body = chapter.Body
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <title>%s</title>
  <meta charset="utf-8" />
  <link rel="stylesheet" type="text/css" href="style.css" />
</head>
<body>
%s
</body>
</html>`, html.EscapeString(title), body)
}

type tocNode struct {
	Title    string
	File     string
	Anchor   string
	Children []*tocNode
}

type epubTitlePage struct {
	ItemID        string
	XHTMLFilename string
	Content       string
	Title         string
}

type epubTocPage struct {
	ItemID        string
	XHTMLFilename string
	Title         string
}

type epubNotesPage struct {
	ItemID        string
	XHTMLFilename string
	Title         string
	Content       string
	Anchors       map[string]string
}

func (g *EPUBGenerator) buildTOC(doc *FB2Document, chapters []*epubChapter, titlePage *epubTitlePage) []*tocNode {
	if doc == nil || doc.Body == nil {
		return nil
	}
	topSections := doc.Body.SubSections
	if len(topSections) == 0 {
		topSections = []*FB2BodySection{doc.Body}
	}
	var nodes []*tocNode
	if titlePage != nil {
		nodes = append(nodes, &tocNode{
			Title:  titlePage.Title,
			File:   titlePage.XHTMLFilename,
			Anchor: "tocref1",
		})
	}
	for _, section := range topSections {
		if section == nil {
			continue
		}
		node := g.buildTOCSection(section)
		if node != nil {
			nodes = append(nodes, node)
		}
	}
	if g.notesFile != "" {
		nodes = append(nodes, &tocNode{
			Title:  "Примечания",
			File:   g.notesFile,
			Anchor: "",
		})
	}
	return nodes
}

func (g *EPUBGenerator) buildTOCSection(section *FB2BodySection) *tocNode {
	if section == nil {
		return nil
	}
	title := strings.TrimSpace(section.Title)
	if title == "" {
		title = "Section"
	}
	filename := g.sectionFiles[section]
	if filename == "" {
		filename = "index001.xhtml"
	}
	node := &tocNode{
		Title:  title,
		File:   filename,
		Anchor: g.sectionAnchor(section),
	}
	for _, sub := range section.SubSections {
		child := g.buildTOCSection(sub)
		if child != nil {
			node.Children = append(node.Children, child)
		}
	}
	return node
}

func (g *EPUBGenerator) sectionAnchor(section *FB2BodySection) string {
	if section == nil {
		return ""
	}
	if anchor, ok := g.sectionAnchors[section]; ok {
		return anchor
	}
	g.anchorSeq++
	anchor := fmt.Sprintf("tocref%d", g.anchorSeq)
	g.sectionAnchors[section] = anchor
	return anchor
}

func buildNavXHTML(nodes []*tocNode) string {
	var list strings.Builder
	list.WriteString(buildNavList(nodes))

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head>
  <title>Contents</title>
  <meta charset="utf-8" />
  <link rel="stylesheet" type="text/css" href="style.css" />
</head>
<body>
  <nav epub:type="toc">
%s
  </nav>
</body>
</html>`, indentLines(list.String(), "      "))
}

func buildTocXHTML(nodes []*tocNode) string {
	var list strings.Builder
	list.WriteString(buildNavList(nodes))
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <title>Content</title>
  <meta charset="utf-8" />
  <link rel="stylesheet" type="text/css" href="style.css" />
</head>
<body>
  <div class="titleblock_nobreak">
    <p class="title">Содержание</p>
  </div>
%s
</body>
</html>`, indentLines(list.String(), "  "))
}

func buildNavList(nodes []*tocNode) string {
	if len(nodes) == 0 {
		return "<ol></ol>"
	}
	var out strings.Builder
	out.WriteString("<ol>\n")
	for _, node := range nodes {
		if node == nil {
			continue
		}
		out.WriteString("  <li><a href=\"")
		out.WriteString(html.EscapeString(node.File))
		if node.Anchor != "" {
			out.WriteString("#")
			out.WriteString(html.EscapeString(node.Anchor))
		}
		out.WriteString("\">")
		out.WriteString(html.EscapeString(node.Title))
		out.WriteString("</a>")
		if len(node.Children) > 0 {
			out.WriteString("\n")
			out.WriteString(indentLines(buildNavList(node.Children), "  "))
			out.WriteString("  ")
		}
		out.WriteString("</li>\n")
	}
	out.WriteString("</ol>\n")
	return out.String()
}

func buildTocNCX(bookFile *parser.BookFile, nodes []*tocNode, tocPage *epubTocPage) string {
	title := safeTitle(bookFile)
	identifier := buildIdentifier(bookFile)
	var navMap strings.Builder
	playOrder := 0
	buildNCXNavMap(nodes, &playOrder, &navMap)
	if tocPage != nil {
		playOrder++
		navMap.WriteString(fmt.Sprintf("<navPoint id=\"navPoint-%d\" playOrder=\"%d\">", playOrder, playOrder))
		navMap.WriteString("<navLabel><text>")
		navMap.WriteString(html.EscapeString(tocPage.Title))
		navMap.WriteString("</text></navLabel>")
		navMap.WriteString("<content src=\"")
		navMap.WriteString(html.EscapeString(tocPage.XHTMLFilename))
		navMap.WriteString("\"/>")
		navMap.WriteString("</navPoint>\n")
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="%s"/>
    <meta name="dtb:depth" content="1"/>
    <meta name="dtb:totalPageCount" content="0"/>
    <meta name="dtb:maxPageNumber" content="0"/>
  </head>
  <docTitle><text>%s</text></docTitle>
  <navMap>
%s  </navMap>
</ncx>`, html.EscapeString(identifier), html.EscapeString(title), indentLines(navMap.String(), "    "))
}

func buildNCXNavMap(nodes []*tocNode, playOrder *int, out *strings.Builder) {
	if out == nil || playOrder == nil {
		return
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		*playOrder++
		out.WriteString(fmt.Sprintf("<navPoint id=\"navPoint-%d\" playOrder=\"%d\">", *playOrder, *playOrder))
		out.WriteString("<navLabel><text>")
		out.WriteString(html.EscapeString(node.Title))
		out.WriteString("</text></navLabel>")
		out.WriteString("<content src=\"")
		out.WriteString(html.EscapeString(node.File))
		if node.Anchor != "" {
			out.WriteString("#")
			out.WriteString(html.EscapeString(node.Anchor))
		}
		out.WriteString("\"/>")
		if len(node.Children) > 0 {
			buildNCXNavMap(node.Children, playOrder, out)
		}
		out.WriteString("</navPoint>\n")
	}
}

func buildContentOPF(bookFile *parser.BookFile, chapters []*epubChapter, images map[string]epubImage, cover *epubCover, titlePage *epubTitlePage, tocPage *epubTocPage, notesPage *epubNotesPage) string {
	title := safeTitle(bookFile)
	language := "und"
	if bookFile != nil && strings.TrimSpace(bookFile.Language) != "" {
		language = strings.TrimSpace(bookFile.Language)
	}
	identifier := buildIdentifier(bookFile)
	creators := buildCreators(bookFile)
	coverMeta := buildCoverMeta(cover)

	var manifest strings.Builder
	manifest.WriteString("<item id=\"style\" href=\"style.css\" media-type=\"text/css\"/>")
	manifest.WriteString("\n")
	manifest.WriteString("<item id=\"nav\" href=\"nav.xhtml\" media-type=\"application/xhtml+xml\" properties=\"nav\"/>")
	manifest.WriteString("\n")
	manifest.WriteString("<item id=\"ncx\" href=\"toc.ncx\" media-type=\"application/x-dtbncx+xml\"/>")
	manifest.WriteString("\n")
	if tocPage != nil {
		manifest.WriteString(fmt.Sprintf("<item id=\"%s\" href=\"%s\" media-type=\"application/xhtml+xml\"/>", tocPage.ItemID, tocPage.XHTMLFilename))
		manifest.WriteString("\n")
	}
	if notesPage != nil {
		manifest.WriteString(fmt.Sprintf("<item id=\"%s\" href=\"%s\" media-type=\"application/xhtml+xml\"/>", notesPage.ItemID, notesPage.XHTMLFilename))
		manifest.WriteString("\n")
	}
	if titlePage != nil {
		manifest.WriteString(fmt.Sprintf("<item id=\"%s\" href=\"%s\" media-type=\"application/xhtml+xml\"/>", titlePage.ItemID, titlePage.XHTMLFilename))
		manifest.WriteString("\n")
	}
	if cover != nil {
		manifest.WriteString(fmt.Sprintf("<item id=\"%s\" href=\"%s\" media-type=\"application/xhtml+xml\"/>", cover.ItemID, cover.XHTMLFilename))
		manifest.WriteString("\n")
	}
	for _, image := range images {
		props := ""
		if cover != nil && image.ItemID == cover.Image.ItemID {
			props = " properties=\"cover-image\""
		}
		manifest.WriteString(fmt.Sprintf("<item id=\"%s\" href=\"images/%s\" media-type=\"%s\"%s/>", image.ItemID, image.Filename, image.MediaType, props))
		manifest.WriteString("\n")
	}
	if cover != nil && !cover.FromImages {
		manifest.WriteString(fmt.Sprintf("<item id=\"%s\" href=\"images/%s\" media-type=\"%s\" properties=\"cover-image\"/>", cover.Image.ItemID, cover.Image.Filename, cover.Image.MediaType))
		manifest.WriteString("\n")
	}
	for i, chapter := range chapters {
		if chapter == nil {
			continue
		}
		manifest.WriteString(fmt.Sprintf("<item id=\"chap%03d\" href=\"%s\" media-type=\"application/xhtml+xml\"/>", i+1, chapter.Filename))
		manifest.WriteString("\n")
	}

	var spine strings.Builder
	if cover != nil {
		spine.WriteString(fmt.Sprintf("<itemref idref=\"%s\"/>", cover.ItemID))
		spine.WriteString("\n")
	}
	if titlePage != nil {
		spine.WriteString(fmt.Sprintf("<itemref idref=\"%s\"/>", titlePage.ItemID))
		spine.WriteString("\n")
	}
	for i := range chapters {
		spine.WriteString(fmt.Sprintf("<itemref idref=\"chap%03d\"/>", i+1))
		spine.WriteString("\n")
	}
	if tocPage != nil {
		spine.WriteString(fmt.Sprintf("<itemref idref=\"%s\"/>", tocPage.ItemID))
		spine.WriteString("\n")
	}
	if notesPage != nil {
		spine.WriteString(fmt.Sprintf("<itemref idref=\"%s\"/>", notesPage.ItemID))
		spine.WriteString("\n")
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <metadata>
    <dc:identifier id="bookid">%s</dc:identifier>
    <dc:title>%s</dc:title>
%s    <dc:language>%s</dc:language>
%s
  </metadata>
  <manifest>
%s  </manifest>
  <spine toc="ncx">
%s  </spine>
</package>`, html.EscapeString(identifier), html.EscapeString(title), creators, html.EscapeString(language), coverMeta, indentLines(manifest.String(), "    "), indentLines(spine.String(), "    "))
}

func buildContainerXML() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
}

func buildStyleCSS() string {
	return `@page {
  margin: 20px 20px 5px;
}

body {
  font-family: serif;
  line-height: 1.5;
  margin: 20px 20px 5px;
  padding: 0;
}

h1 {
  font-size: 140%;
  font-weight: bold;
  margin-bottom: 1em;
}

h2, h3 {
  font-size: 120%;
  font-weight: bold;
  margin-bottom: 1em;
}

h4, h5, h6 {
  text-align: center;
  font-size: 120%;
  font-weight: bold;
  margin-bottom: 1em;
}

.titleblock {
  page-break-before: always;
  text-indent: 0em;
  margin-top: 2em;
  margin-bottom: 1em;
}

.titleblock_nobreak {
  text-indent: 0em;
  margin-top: 2em;
  margin-bottom: 1em;
}

p.title {
  text-indent: 0em;
  text-align: center;
  padding-bottom: 0;
}

.note {
  margin: 0 0 1em 0;
}

.notenum {
  font-weight: bold;
  text-indent: 0em;
}

p {
  text-indent: 1em;
  text-align: justify;
  padding-bottom: 0.3em;
  margin: 0;
}

.subtitle {
  text-align: center;
  font-weight: bold;
  margin-bottom: 0.5em;
  margin-top: 1em;
  page-break-after: avoid;
  text-indent: 0em;
}

p.subtitle {
  text-indent: 0em;
}

.epigraph {
  text-align: right;
  margin-top: 0.4em;
  margin-bottom: 0.2em;
  margin-left: 4em;
  font-style: italic;
}

.text-author {
  page-break-before: avoid;
  text-align: right;
  font-weight: bold;
}

.cite {
  font-style: italic;
  text-indent: 1em;
  margin-top: 0.3em;
  margin-bottom: 0.3em;
}

.emptyline {
  margin-top: 1em;
}

.poem {
  text-indent: 0em;
  font-style: italic;
  margin-left: 3em;
  margin-bottom: 0em;
  margin-top: 0em;
}

.stanza {
  margin-bottom: 0.5em;
}

.poem p {
  margin-top: 0em;
  margin-bottom: 0em;
  text-indent: 0em;
  text-align: left;
}

img {
  max-width: 100%;
  height: auto;
}

.image {
  text-indent: 0em;
  text-align: center;
}

.image img {
  display: block;
  margin: 0 auto;
  max-width: 100%;
  height: auto;
}

.cover {
  text-align: center;
  margin-top: 1em;
}

table {
  border-collapse: collapse;
  margin: 1em 0;
  width: 100%;
}

td, th {
  border: 1px solid #999;
  padding: 0.4em 0.6em;
}

code {
  font-family: "Courier New", Courier, monospace;
  background: #f2f2f2;
  padding: 0 0.2em;
}`
}

func writeStoredFile(writer *zip.Writer, name string, content string) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	w, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, content)
	return err
}

func writeFile(writer *zip.Writer, name string, content string) error {
	w, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, content)
	return err
}

func writeFileBytes(writer *zip.Writer, name string, content []byte) error {
	w, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(content)
	return err
}

func buildIdentifier(bookFile *parser.BookFile) string {
	source := "unknown"
	if bookFile != nil {
		parts := []string{strings.TrimSpace(bookFile.Title)}
		for _, author := range bookFile.Authors {
			parts = append(parts, strings.TrimSpace(author.Name))
		}
		source = strings.Join(parts, "|")
	}
	hash := sha1.Sum([]byte(source))
	return "urn:sha1:" + hex.EncodeToString(hash[:])
}

func buildCreators(bookFile *parser.BookFile) string {
	if bookFile == nil || len(bookFile.Authors) == 0 {
		return ""
	}
	var out strings.Builder
	for _, author := range bookFile.Authors {
		name := strings.TrimSpace(author.Name)
		if name == "" {
			continue
		}
		out.WriteString("<dc:creator>")
		out.WriteString(html.EscapeString(name))
		out.WriteString("</dc:creator>\n")
	}
	return indentLines(strings.TrimSuffix(out.String(), "\n"), "    ")
}

func safeTitle(bookFile *parser.BookFile) string {
	if bookFile != nil && strings.TrimSpace(bookFile.Title) != "" {
		return strings.TrimSpace(bookFile.Title)
	}
	return "Untitled"
}

func indentLines(content string, indent string) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n") + "\n"
}

type epubImage struct {
	ItemID    string
	Filename  string
	MediaType string
	Data      []byte
}

type epubCover struct {
	ItemID        string
	XHTMLFilename string
	Image         epubImage
	FromImages    bool
}

func buildTitlePage(bookFile *parser.BookFile) *epubTitlePage {
	if bookFile == nil {
		return nil
	}
	title := strings.TrimSpace(bookFile.Title)
	authors := buildAuthorsLine(bookFile)
	if title == "" && authors == "" {
		return nil
	}
	var lines []string
	if authors != "" {
		lines = append(lines, fmt.Sprintf("<p class=\"title\">%s</p>", html.EscapeString(authors)))
	}
	if title != "" {
		lines = append(lines, fmt.Sprintf("<p class=\"title\">%s</p>", html.EscapeString(title)))
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <title>Title</title>
  <meta charset="utf-8" />
  <link rel="stylesheet" type="text/css" href="style.css" />
</head>
<body>
  <div id="tocref1" class="titleblock">
%s  </div>
</body>
</html>`, indentLines(strings.Join(lines, "\n"), "    "))

	return &epubTitlePage{
		ItemID:        "titlepage",
		XHTMLFilename: "title.xhtml",
		Content:       content,
		Title:         strings.TrimSpace(authors + " " + title),
	}
}

func buildTocPage() *epubTocPage {
	return &epubTocPage{
		ItemID:        "toc",
		XHTMLFilename: "toc.xhtml",
		Title:         "Content",
	}
}

func (g *EPUBGenerator) buildNotesPage(doc *FB2Document) *epubNotesPage {
	if doc == nil || len(doc.Notes) == 0 {
		return nil
	}
	anchors := make(map[string]string)
	var body strings.Builder
	body.WriteString("<div class=\"titleblock_nobreak\"><p class=\"title\">Примечания</p></div>\n")
	for i, section := range doc.Notes {
		if section == nil {
			continue
		}
		anchor := buildNoteAnchor(section, i)
		if section.ID != "" {
			anchors[section.ID] = anchor
		}
		body.WriteString("<div class=\"note\" id=\"")
		body.WriteString(html.EscapeString(anchor))
		body.WriteString("\">\n")
		title := strings.TrimSpace(section.Title)
		if title == "" {
			title = section.ID
		}
		if title != "" {
			body.WriteString("<p class=\"notenum\">")
			body.WriteString(html.EscapeString(title))
			body.WriteString("</p>\n")
		}
		g.renderParagraphs(&body, section.Paragraphs)
		body.WriteString("</div>\n")
	}

	content := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <title>Notes</title>
  <meta charset="utf-8" />
  <link rel="stylesheet" type="text/css" href="style.css" />
</head>
<body>
%s
</body>
</html>`, indentLines(body.String(), "  "))

	page := &epubNotesPage{
		ItemID:        "notes",
		XHTMLFilename: "notes.xhtml",
		Title:         "Примечания",
		Content:       content,
		Anchors:       anchors,
	}
	g.notesAnchors = anchors
	g.notesFile = page.XHTMLFilename
	return page
}

func buildNoteAnchor(section *FB2BodySection, index int) string {
	if section != nil {
		id := strings.TrimSpace(section.ID)
		if id != "" {
			return "note-" + id
		}
	}
	return fmt.Sprintf("note-%d", index+1)
}

func buildAuthorsLine(bookFile *parser.BookFile) string {
	if bookFile == nil || len(bookFile.Authors) == 0 {
		return ""
	}
	var names []string
	for _, author := range bookFile.Authors {
		name := strings.TrimSpace(author.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, " ")
}

func buildImages(doc *FB2Document) map[string]epubImage {
	if doc == nil || len(doc.Binary) == 0 {
		return nil
	}
	keys := make([]string, 0, len(doc.Binary))
	for key := range doc.Binary {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	images := make(map[string]epubImage, len(keys))
	for i, key := range keys {
		data := doc.Binary[key]
		if len(data) == 0 {
			continue
		}
		mediaType := detectImageMimeType(data, key)
		ext := extensionForMime(mediaType)
		if ext == "" {
			ext = ".bin"
			mediaType = "application/octet-stream"
		}
		filename := fmt.Sprintf("image_%03d%s", i+1, ext)
		itemID := fmt.Sprintf("img%03d", i+1)
		images[key] = epubImage{
			ItemID:    itemID,
			Filename:  filename,
			MediaType: mediaType,
			Data:      data,
		}
	}
	return images
}

// detectImageMimeType detects MIME type for image data.
// First tries to detect by FB2 image ID (if it contains extension hint),
// then falls back to content-based detection using http.DetectContentType.
func detectImageMimeType(data []byte, imageID string) string {
	// Try to infer from ID/filename if it contains extension hint
	lowerID := strings.ToLower(imageID)
	if strings.Contains(lowerID, ".jpg") || strings.Contains(lowerID, ".jpeg") {
		return "image/jpeg"
	}
	if strings.Contains(lowerID, ".png") {
		return "image/png"
	}
	if strings.Contains(lowerID, ".gif") {
		return "image/gif"
	}
	if strings.Contains(lowerID, ".webp") {
		return "image/webp"
	}
	if strings.Contains(lowerID, ".svg") {
		return "image/svg+xml"
	}

	// Fallback to content-based detection
	detected := http.DetectContentType(data)

	// Ensure it's an image MIME type
	if strings.HasPrefix(detected, "image/") {
		return detected
	}

	// Default to JPEG if detection failed
	return "image/jpeg"
}

func extensionForMime(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	default:
		return ""
	}
}

func (g *EPUBGenerator) renderImage(builder *strings.Builder, el *FB2InlineElement) {
	if el == nil || builder == nil {
		return
	}
	href := ""
	if el.Attrs != nil {
		href = strings.TrimSpace(el.Attrs["href"])
	}
	href = strings.TrimPrefix(href, "#")
	if href == "" || g == nil || len(g.images) == 0 {
		builder.WriteString("<span class=\"fb2-image\">[image]</span>")
		return
	}
	image, ok := g.images[strings.ToLower(href)]
	if !ok {
		builder.WriteString("<span class=\"fb2-image\">[image]</span>")
		return
	}
	builder.WriteString("<img src=\"images/")
	builder.WriteString(html.EscapeString(image.Filename))
	builder.WriteString("\" alt=\"\"/>")
}

func buildCover(bookFile *parser.BookFile, images map[string]epubImage) *epubCover {
	coverData := []byte(nil)
	if bookFile != nil && len(bookFile.Cover) > 0 {
		coverData = bookFile.Cover
	}

	if len(coverData) > 0 {
		for _, img := range images {
			if bytes.Equal(img.Data, coverData) {
				return &epubCover{
					ItemID:        "cover",
					XHTMLFilename: "cover.xhtml",
					Image:         img,
					FromImages:    true,
				}
			}
		}

		mediaType := detectImageMimeType(coverData, "cover")
		ext := extensionForMime(mediaType)
		if ext == "" {
			ext = ".jpg"
			mediaType = "image/jpeg"
		}
		return &epubCover{
			ItemID:        "cover",
			XHTMLFilename: "cover.xhtml",
			Image: epubImage{
				ItemID:    "cover-image",
				Filename:  "cover" + ext,
				MediaType: mediaType,
				Data:      coverData,
			},
			FromImages: false,
		}
	}

	if len(images) == 0 {
		return nil
	}
	keys := make([]string, 0, len(images))
	for key := range images {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	first := images[keys[0]]
	if len(first.Data) == 0 {
		return nil
	}
	return &epubCover{
		ItemID:        "cover",
		XHTMLFilename: "cover.xhtml",
		Image:         first,
		FromImages:    true,
	}
}

func buildCoverXHTML(cover *epubCover) string {
	if cover == nil {
		return ""
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <title>Cover</title>
  <meta charset="utf-8" />
  <link rel="stylesheet" type="text/css" href="style.css" />
</head>
<body>
  <div class="cover"><img src="images/%s" alt=""/></div>
</body>
</html>`, html.EscapeString(cover.Image.Filename))
}

func buildCoverMeta(cover *epubCover) string {
	if cover == nil {
		return ""
	}
	return indentLines(fmt.Sprintf("<meta name=\"cover\" content=\"%s\"/>", cover.Image.ItemID), "    ")
}
