package converter

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopds-api/internal/parser"
)

// Helper function to load test data
func loadTestData(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to load test data %s: %v", filename, err)
	}
	return data
}

// TestParseFB2Body_Simple tests basic parsing of a minimal valid FB2 file
func TestParseFB2Body_Simple(t *testing.T) {
	data := loadTestData(t, "simple.fb2")

	doc, err := ParseFB2Body(data)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}

	if doc == nil {
		t.Fatal("Expected non-nil document")
	}

	if doc.Body == nil {
		t.Fatal("Expected body to be parsed")
	}

	// The single chapter is a child of the root container.
	if len(doc.Body.SubSections()) != 1 {
		t.Fatalf("Expected 1 top-level section, got %d", len(doc.Body.SubSections()))
	}
	section := doc.Body.SubSections()[0]
	if section.Title != "Chapter 1" {
		t.Errorf("Expected section title 'Chapter 1', got '%s'", section.Title)
	}

	// Check that we have 2 paragraphs
	if len(section.Paragraphs()) != 2 {
		t.Errorf("Expected 2 paragraphs, got %d", len(section.Paragraphs()))
	}

	// Verify first paragraph
	if len(section.Paragraphs()) > 0 {
		p1 := section.Paragraphs()[0]
		if p1.Kind != ParagraphKindNormal {
			t.Errorf("Expected paragraph kind %s, got %s", ParagraphKindNormal, p1.Kind)
		}
		expected := "First paragraph of the test book."
		if p1.Text != expected {
			t.Errorf("Expected paragraph text '%s', got '%s'", expected, p1.Text)
		}
	}
}

// TestParseFB2Body_InlineFormatting tests parsing of inline formatting elements
func TestParseFB2Body_InlineFormatting(t *testing.T) {
	data := loadTestData(t, "formatting.fb2")

	doc, err := ParseFB2Body(data)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}

	if doc == nil || doc.Body == nil || len(doc.Body.SubSections()) == 0 {
		t.Fatal("Expected valid document with at least one section")
	}

	section := doc.Body.SubSections()[0]

	// We should have multiple paragraphs with formatting
	if len(section.Paragraphs()) < 5 {
		t.Errorf("Expected at least 5 paragraphs, got %d", len(section.Paragraphs()))
	}

	// Test paragraph with bold (strong)
	if len(section.Paragraphs()) > 0 {
		p := section.Paragraphs()[0]
		foundStrong := false
		for _, inline := range p.Content {
			if inline.Type == InlineTypeStrong {
				foundStrong = true
				// Text content should be in Children
				if len(inline.Children) > 0 && inline.Children[0].Type == InlineTypeText {
					if inline.Children[0].Content != "bold" {
						t.Errorf("Expected strong text 'bold', got '%s'", inline.Children[0].Content)
					}
				}
			}
		}
		if !foundStrong {
			t.Error("Expected to find strong element in first paragraph")
		}
	}

	// Test paragraph with emphasis (italic)
	if len(section.Paragraphs()) > 1 {
		p := section.Paragraphs()[1]
		foundEmphasis := false
		for _, inline := range p.Content {
			if inline.Type == InlineTypeEmphasis {
				foundEmphasis = true
				// Text content should be in Children
				if len(inline.Children) > 0 && inline.Children[0].Type == InlineTypeText {
					if inline.Children[0].Content != "italic" {
						t.Errorf("Expected emphasis text 'italic', got '%s'", inline.Children[0].Content)
					}
				}
			}
		}
		if !foundEmphasis {
			t.Error("Expected to find emphasis element in second paragraph")
		}
	}

	// Test paragraph with code
	if len(section.Paragraphs()) > 2 {
		p := section.Paragraphs()[2]
		foundCode := false
		for _, inline := range p.Content {
			if inline.Type == InlineTypeCode {
				foundCode = true
				// Text content should be in Children
				if len(inline.Children) > 0 && inline.Children[0].Type == InlineTypeText {
					if inline.Children[0].Content != "inline code" {
						t.Errorf("Expected code text 'inline code', got '%s'", inline.Children[0].Content)
					}
				}
			}
		}
		if !foundCode {
			t.Error("Expected to find code element in third paragraph")
		}
	}

	// Test superscript
	if len(section.Paragraphs()) > 3 {
		p := section.Paragraphs()[3]
		foundSup := false
		for _, inline := range p.Content {
			if inline.Type == InlineTypeSup {
				foundSup = true
			}
		}
		if !foundSup {
			t.Error("Expected to find sup element in fourth paragraph")
		}
	}

	// Test subscript
	if len(section.Paragraphs()) > 4 {
		p := section.Paragraphs()[4]
		foundSub := false
		for _, inline := range p.Content {
			if inline.Type == InlineTypeSub {
				foundSub = true
			}
		}
		if !foundSub {
			t.Error("Expected to find sub element in fifth paragraph")
		}
	}
}

// TestParseFB2Body_NestedSections tests hierarchical section structure
func TestParseFB2Body_NestedSections(t *testing.T) {
	data := loadTestData(t, "nested_sections.fb2")

	doc, err := ParseFB2Body(data)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}

	if doc == nil || doc.Body == nil {
		t.Fatal("Expected valid document with body")
	}

	// doc.Body is a container for the whole <body>, never the first section.
	// Both chapters are its direct children: ch2 is a sibling of ch1 in the
	// source, and the model must keep it that way.
	root := doc.Body
	if root.Title != "" {
		t.Errorf("Expected empty root title, got '%s'", root.Title)
	}
	if len(root.Paragraphs()) != 0 {
		t.Errorf("Expected no root-level paragraphs, got %d", len(root.Paragraphs()))
	}
	if len(root.SubSections()) != 2 {
		t.Fatalf("Expected 2 top-level sections (ch1, ch2), got %d", len(root.SubSections()))
	}

	ch1 := root.SubSections()[0]
	if ch1.ID != "ch1" {
		t.Errorf("Expected section ID 'ch1', got '%s'", ch1.ID)
	}
	if ch1.Title != "Chapter 1" {
		t.Errorf("Expected section title 'Chapter 1', got '%s'", ch1.Title)
	}
	if len(ch1.Paragraphs()) != 1 {
		t.Errorf("Expected 1 paragraph in Chapter 1, got %d", len(ch1.Paragraphs()))
	}

	if len(ch1.SubSections()) != 2 {
		t.Fatalf("Expected 2 subsections in Chapter 1 (ch1-1, ch1-2), got %d", len(ch1.SubSections()))
	}

	sec11 := ch1.SubSections()[0]
	if sec11.ID != "ch1-1" {
		t.Errorf("Expected section ID 'ch1-1', got '%s'", sec11.ID)
	}
	if len(sec11.SubSections()) != 1 {
		t.Fatalf("Expected 1 subsection in Section 1.1, got %d", len(sec11.SubSections()))
	}

	sec111 := sec11.SubSections()[0]
	if sec111.ID != "ch1-1-1" {
		t.Errorf("Expected section ID 'ch1-1-1', got '%s'", sec111.ID)
	}
	if sec111.Title != "Subsection 1.1.1" {
		t.Errorf("Expected section title 'Subsection 1.1.1', got '%s'", sec111.Title)
	}

	sec12 := ch1.SubSections()[1]
	if sec12.ID != "ch1-2" {
		t.Errorf("Expected section ID 'ch1-2', got '%s'", sec12.ID)
	}

	// Chapter 2 stays a sibling of Chapter 1, not a child.
	ch2 := root.SubSections()[1]
	if ch2.ID != "ch2" {
		t.Errorf("Expected section ID 'ch2', got '%s'", ch2.ID)
	}
	if ch2.Title != "Chapter 2" {
		t.Errorf("Expected section title 'Chapter 2', got '%s'", ch2.Title)
	}
	if len(ch2.SubSections()) != 0 {
		t.Errorf("Expected 0 subsections in Chapter 2, got %d", len(ch2.SubSections()))
	}
}

// TestParseFB2Body_Cyrillic tests parsing of cyrillic text
func TestParseFB2Body_Cyrillic(t *testing.T) {
	data := loadTestData(t, "cyrillic.fb2")

	doc, err := ParseFB2Body(data)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}

	if doc == nil || doc.Body == nil || len(doc.Body.SubSections()) == 0 {
		t.Fatal("Expected valid document with at least one section")
	}

	section := doc.Body.SubSections()[0]

	// Check cyrillic title
	if section.Title != "Глава 1" {
		t.Errorf("Expected cyrillic title 'Глава 1', got '%s'", section.Title)
	}

	// Check that we can parse cyrillic text in paragraphs
	if len(section.Paragraphs()) > 0 {
		p := section.Paragraphs()[0]
		expected := "Первый параграф с кириллическим текстом."
		if p.Text != expected {
			t.Errorf("Expected cyrillic text '%s', got '%s'", expected, p.Text)
		}
	}

	// Check cyrillic formatting
	if len(section.Paragraphs()) > 1 {
		p := section.Paragraphs()[1]
		foundStrong := false
		for _, inline := range p.Content {
			if inline.Type == InlineTypeStrong {
				// Text content should be in Children
				if len(inline.Children) > 0 && inline.Children[0].Type == InlineTypeText {
					if inline.Children[0].Content == "жирным" {
						foundStrong = true
					}
				}
			}
		}
		if !foundStrong {
			t.Error("Expected to find cyrillic strong element 'жирным'")
		}
	}
}

// TestParseFB2Body_SpecialElements tests parsing of poems, citations, tables, etc.
func TestParseFB2Body_SpecialElements(t *testing.T) {
	data := loadTestData(t, "special_elements.fb2")

	doc, err := ParseFB2Body(data)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}

	if doc == nil || doc.Body == nil || len(doc.Body.SubSections()) == 0 {
		t.Fatal("Expected valid document with at least one section")
	}

	section := doc.Body.SubSections()[0]

	// Check for poem paragraphs
	foundPoem := false
	foundTextAuthor := false
	foundCite := false
	foundEpigraph := false
	foundTable := false

	for _, p := range section.Paragraphs() {
		switch p.Kind {
		case ParagraphKindPoem, ParagraphKindPoemLine:
			foundPoem = true
		case ParagraphKindTextAuthor:
			foundTextAuthor = true
		case ParagraphKindCite:
			foundCite = true
		case ParagraphKindEpigraph:
			foundEpigraph = true
		case ParagraphKindTable:
			foundTable = true
			// Verify table structure
			if p.Table == nil {
				t.Error("Expected table data for table paragraph")
			} else {
				// Check that we have rows
				if len(p.Table.Rows) == 0 {
					t.Error("Expected table to have rows")
				}
				// First row should be headers
				if len(p.Table.Rows) > 0 {
					firstRow := p.Table.Rows[0]
					if len(firstRow) > 0 && !firstRow[0].Header {
						t.Error("Expected first row cells to be headers")
					}
				}
			}
		}
	}

	if !foundPoem {
		t.Error("Expected to find poem elements")
	}
	if !foundTextAuthor {
		t.Error("Expected to find text-author elements")
	}
	if !foundCite {
		t.Error("Expected to find cite elements")
	}
	if !foundEpigraph {
		t.Error("Expected to find epigraph elements")
	}
	if !foundTable {
		t.Error("Expected to find table element")
	}
}

// TestParseFB2Body_ImagesAndNotes tests parsing of images and footnotes
func TestParseFB2Body_ImagesAndNotes(t *testing.T) {
	data := loadTestData(t, "images_notes.fb2")

	doc, err := ParseFB2Body(data)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}

	if doc == nil {
		t.Fatal("Expected non-nil document")
	}

	// Check binary images
	if len(doc.Binary) == 0 {
		t.Error("Expected to find binary images")
	}

	if img, ok := doc.Binary["img1"]; !ok {
		t.Error("Expected to find image with ID 'img1'")
	} else if len(img.Data) == 0 {
		t.Error("Expected image data to be decoded")
	}

	// Check notes
	if len(doc.Notes) == 0 {
		t.Error("Expected to find notes")
	}

	if len(doc.Notes) < 2 {
		t.Errorf("Expected at least 2 notes, got %d", len(doc.Notes))
	}

	// Check that notes have IDs
	if len(doc.Notes) > 0 {
		note1 := doc.Notes[0]
		if note1.ID != "note1" {
			t.Errorf("Expected note ID 'note1', got '%s'", note1.ID)
		}
		if len(note1.Paragraphs()) == 0 {
			t.Error("Expected note to have paragraphs")
		}
	}

	// Check that main body has image references
	if doc.Body != nil && len(doc.Body.SubSections()) > 0 {
		section := doc.Body.SubSections()[0]
		foundImage := false
		for _, p := range section.Paragraphs() {
			for _, inline := range p.Content {
				if inline.Type == InlineTypeImage {
					foundImage = true
					if href, ok := inline.Attrs["href"]; ok {
						if href != "#img1" {
							t.Errorf("Expected image href '#img1', got '%s'", href)
						}
					}
				}
			}
		}
		if !foundImage {
			t.Error("Expected to find image element in body")
		}
	}
}

// TestParseFB2Body_MalformedXML tests graceful handling of malformed XML
func TestParseFB2Body_MalformedXML(t *testing.T) {
	data := loadTestData(t, "malformed.fb2")

	// Should not panic and should attempt to parse
	doc, err := ParseFB2Body(data)

	// We expect sanitization to fix most issues, so parsing should succeed
	// If it fails, the error should be graceful
	if err != nil {
		t.Logf("Parsing malformed XML returned error (expected): %v", err)
		return
	}

	if doc == nil {
		t.Fatal("Expected document to be parsed after sanitization")
	}

	// Check that some content was parsed
	if doc.Body == nil {
		t.Error("Expected body to be present after sanitization")
	}
}

// TestParseFB2Body_EmptyBody tests handling of FB2 without body
func TestParseFB2Body_EmptyBody(t *testing.T) {
	xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description>
    <title-info>
      <book-title>Empty Book</book-title>
    </title-info>
  </description>
</FictionBook>`)

	doc, err := ParseFB2Body(xmlContent)

	// Should handle gracefully
	if err != nil {
		t.Logf("Parsing empty body returned error: %v", err)
	}

	if doc == nil {
		t.Fatal("Expected non-nil document even for empty body")
	}

	// Body might be nil or empty
	if doc.Body != nil && len(doc.Body.SubSections()) > 0 {
		t.Error("Expected no sections for empty body")
	}
}

// TestParseFB2Body_InvalidXML tests handling of completely invalid XML
func TestParseFB2Body_InvalidXML(t *testing.T) {
	xmlContent := []byte(`This is not XML at all!`)

	doc, err := ParseFB2Body(xmlContent)

	// Total garbage must be a typed error, not a silently empty book:
	// the caller cannot tell "empty document" from "not a document" otherwise.
	if !errors.Is(err, ErrNotFictionBook) {
		t.Errorf("Expected a typed ErrNotFictionBook for non-XML input, got document %+v and error %v", doc, err)
	}
}

// TestSanitizeBrokenSelfClosingTags tests universal repairs for broken self-closing tags
func TestSanitizeBrokenSelfClosingTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			// The closing tag is kept: only the missing bracket was damage.
			// Swallowing </section> merged two sibling sections into a nested
			// pair, which rewrote the book rather than repairing it.
			name:     "Broken image tag with section closing",
			input:    `<image xlink:href="#img1" /</section>`,
			expected: `<image xlink:href="#img1" /></section>`,
		},
		{
			name:     "Broken self-closing with space before tag",
			input:    `<empty-line / <p>text</p>`,
			expected: `<empty-line /><p>text</p>`,
		},
		{
			name:     "Broken self-closing with newline",
			input:    "<image href=\"#img1\" /\n<section>",
			expected: `<image href="#img1" /><section>`,
		},
		{
			name:     "Normal self-closing tags should not change",
			input:    `<image href="#img1" /><br/>`,
			expected: `<image href="#img1" /><br/>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeBrokenSelfClosingTags([]byte(tt.input))
			resultStr := string(result)
			if resultStr != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, resultStr)
			}
		})
	}
}

// TestBalanceSectionTags tests section tag balancing
func TestBalanceSectionTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantBody bool
	}{
		{
			name: "Unclosed section should be auto-closed",
			input: `<?xml version="1.0"?>
<FictionBook>
<body>
<section>
<title><p>Chapter 1</p></title>
<p>Text</p>
</body>
</FictionBook>`,
			wantBody: true,
		},
		{
			name: "Orphaned closing section should be removed",
			input: `<?xml version="1.0"?>
<FictionBook>
<body>
<p>Text</p>
</section>
</body>
</FictionBook>`,
			wantBody: true,
		},
		{
			name: "Nested unclosed sections",
			input: `<?xml version="1.0"?>
<FictionBook>
<body>
<section>
<title><p>Chapter 1</p></title>
<section>
<title><p>Section 1.1</p></title>
<p>Text</p>
</body>
</FictionBook>`,
			wantBody: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := ParseFB2Body([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseFB2Body failed: %v", err)
			}

			if tt.wantBody && doc.Body == nil {
				t.Error("Expected body to be parsed")
			}
		})
	}
}

// TestBalanceCommonTags tests balancing of common FB2 tags
func TestBalanceCommonTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantText string
	}{
		{
			name: "Unclosed paragraph",
			input: `<?xml version="1.0"?>
<FictionBook>
<body>
<section>
<p>First paragraph
<p>Second paragraph</p>
</section>
</body>
</FictionBook>`,
			wantText: "First paragraph",
		},
		{
			name: "Unclosed cite",
			input: `<?xml version="1.0"?>
<FictionBook>
<body>
<section>
<cite>
<p>Citation text</p>
<section>
<p>Next section</p>
</section>
</section>
</body>
</FictionBook>`,
			wantText: "Citation text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := ParseFB2Body([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseFB2Body failed: %v", err)
			}

			if doc.Body == nil {
				t.Fatal("Expected body to be parsed")
			}

			found := false
			var texts []string
			collectParagraphTexts(doc.Body, &texts)
			for _, text := range texts {
				if text == tt.wantText {
					found = true
					break
				}
			}

			if !found {
				t.Errorf("Expected to find paragraph with text '%s'", tt.wantText)
			}
		})
	}
}

// TestUniversalRepairs tests the complete repair pipeline
func TestUniversalRepairs(t *testing.T) {
	// Complex malformed FB2 with multiple issues
	input := `<?xml version="1.0"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"
             xmlns:xlink="http://www.w3.org/1999/xlink">
<body>
<section>
<title><p>Chapter with Issues</p></title>
<p>Normal text with <strong>bold</strong> and <emphasis>italic</emphasis>.</p>
<image xlink:href="#img1" /</section>
<section>
<title><p>Another Chapter</p>
<p>Text in unclosed section
</body>
</FictionBook>`

	doc, err := ParseFB2Body([]byte(input))
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}

	if doc == nil || doc.Body == nil {
		t.Fatal("Expected valid document with body")
	}

	// Should have parsed at least the first section
	if doc.Body.Title == "" && len(doc.Body.SubSections()) == 0 {
		t.Error("Expected to find at least one section title")
	}

	// Should have some paragraphs
	totalParagraphs := len(doc.Body.Paragraphs())
	for _, sub := range doc.Body.SubSections() {
		totalParagraphs += len(sub.Paragraphs())
	}

	if totalParagraphs == 0 {
		t.Error("Expected to find some paragraphs")
	}

	t.Logf("Successfully parsed malformed document with %d total paragraphs", totalParagraphs)
}

// collectParagraphTexts gathers paragraph texts from a section tree in
// document order: paragraphs and nested sections are visited interleaved,
// exactly as they appear in the source. Tests use it to assert that no text
// is lost, duplicated, or reordered.
func collectParagraphTexts(section *FB2BodySection, out *[]string) {
	if section == nil {
		return
	}
	for _, item := range section.Content {
		if item == nil {
			continue
		}
		if item.Paragraph != nil {
			if item.Paragraph.Text != "" {
				*out = append(*out, item.Paragraph.Text)
			}
			continue
		}
		collectParagraphTexts(item.Section, out)
	}
}

func documentParagraphTexts(doc *FB2Document) []string {
	var out []string
	if doc == nil {
		return out
	}
	collectParagraphTexts(doc.Body, &out)
	for _, note := range doc.Notes {
		collectParagraphTexts(note, &out)
	}
	return out
}

func joinedParagraphTexts(doc *FB2Document) string {
	return strings.Join(documentParagraphTexts(doc), "\n")
}

// TestParseFB2Body_BodyLevelText verifies that paragraphs placed directly
// under <body> survive alongside child sections, and that the whole document
// keeps a single reading sequence: text standing between two sections must
// stay between them, not pool at the start of the body.
func TestParseFB2Body_BodyLevelText(t *testing.T) {
	xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <body>
    <p>ROOT MARKER ALPHA</p>
    <section id="s1">
      <title><p>First</p></title>
      <p>SECTION ONE MARKER</p>
    </section>
    <p>ROOT MARKER BETA</p>
    <section id="s2">
      <title><p>Second</p></title>
      <p>SECTION TWO MARKER</p>
    </section>
  </body>
</FictionBook>`)

	doc, err := ParseFB2Body(xmlContent)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}
	if doc == nil || doc.Body == nil {
		t.Fatal("Expected valid document with body")
	}

	root := doc.Body
	if len(root.Paragraphs()) != 2 {
		t.Errorf("Expected 2 root-level paragraphs, got %d", len(root.Paragraphs()))
	}
	if len(root.SubSections()) != 2 {
		t.Fatalf("Expected 2 top-level sections, got %d", len(root.SubSections()))
	}
	if root.SubSections()[0].ID != "s1" || root.SubSections()[1].ID != "s2" {
		t.Errorf("Expected section order s1, s2; got %s, %s",
			root.SubSections()[0].ID, root.SubSections()[1].ID)
	}

	// One continuous sequence across paragraph and section boundaries:
	// ALPHA before section one, BETA between the sections.
	want := []string{
		"ROOT MARKER ALPHA",
		"SECTION ONE MARKER",
		"ROOT MARKER BETA",
		"SECTION TWO MARKER",
	}
	got := documentParagraphTexts(doc)
	if len(got) != len(want) {
		t.Fatalf("Expected %d paragraphs in document order, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Position %d: expected '%s', got '%s'", i, want[i], got[i])
		}
	}
}

// TestParseFB2Body_BodyWithoutSections verifies that a body holding plain
// paragraphs and no sections still parses into a single-part document.
func TestParseFB2Body_BodyWithoutSections(t *testing.T) {
	xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <body>
    <p>ONLY MARKER</p>
  </body>
</FictionBook>`)

	doc, err := ParseFB2Body(xmlContent)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}
	if doc == nil || doc.Body == nil {
		t.Fatal("Expected valid document with body")
	}
	if len(doc.Body.Paragraphs()) != 1 || doc.Body.Paragraphs()[0].Text != "ONLY MARKER" {
		t.Errorf("Expected single root paragraph 'ONLY MARKER', got %+v", doc.Body.Paragraphs())
	}
	if len(doc.Body.SubSections()) != 0 {
		t.Errorf("Expected no sections, got %d", len(doc.Body.SubSections()))
	}
}

// TestParseFB2Body_SectionOrderPreserved verifies that untitled and empty
// sections keep their position: numbering must not slip when a section has
// no title or no content.
func TestParseFB2Body_SectionOrderPreserved(t *testing.T) {
	xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <body>
    <section id="s1"><title><p>Alpha</p></title><p>TEXT ALPHA</p></section>
    <section id="s2"></section>
    <section id="s3"><p>TEXT UNTITLED</p></section>
    <section id="s4"><title><p>Beta</p></title><p>TEXT BETA</p></section>
  </body>
</FictionBook>`)

	doc, err := ParseFB2Body(xmlContent)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}
	if doc == nil || doc.Body == nil {
		t.Fatal("Expected valid document with body")
	}

	sections := doc.Body.SubSections()
	if len(sections) != 4 {
		t.Fatalf("Expected 4 top-level sections, got %d", len(sections))
	}
	wantIDs := []string{"s1", "s2", "s3", "s4"}
	for i, want := range wantIDs {
		if sections[i].ID != want {
			t.Errorf("Section %d: expected ID '%s', got '%s'", i, want, sections[i].ID)
		}
	}
	if sections[0].Title != "Alpha" {
		t.Errorf("Expected title 'Alpha', got '%s'", sections[0].Title)
	}
	if sections[2].Title != "" {
		t.Errorf("Expected empty title for untitled section, got '%s'", sections[2].Title)
	}
	if len(sections[2].Paragraphs()) != 1 || sections[2].Paragraphs()[0].Text != "TEXT UNTITLED" {
		t.Errorf("Expected untitled section to keep its paragraph, got %+v", sections[2].Paragraphs())
	}
	if sections[3].Title != "Beta" {
		t.Errorf("Expected title 'Beta', got '%s'", sections[3].Title)
	}
}

// TestParseFB2Body_NotesBodySeparation verifies that <body name="notes">
// lands in doc.Notes and never mixes with the main body.
func TestParseFB2Body_NotesBodySeparation(t *testing.T) {
	xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <body>
    <section id="main1"><p>MAIN TEXT ONE</p></section>
    <section id="main2"><p>MAIN TEXT TWO</p></section>
  </body>
  <body name="notes">
    <section id="n1"><p>NOTE TEXT ONE</p></section>
    <section id="n2"><p>NOTE TEXT TWO</p></section>
  </body>
</FictionBook>`)

	doc, err := ParseFB2Body(xmlContent)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}
	if doc == nil || doc.Body == nil {
		t.Fatal("Expected valid document with body")
	}

	if len(doc.Notes) != 2 {
		t.Fatalf("Expected 2 notes, got %d", len(doc.Notes))
	}
	if doc.Notes[0].ID != "n1" || doc.Notes[1].ID != "n2" {
		t.Errorf("Expected note IDs n1, n2; got %s, %s", doc.Notes[0].ID, doc.Notes[1].ID)
	}

	if len(doc.Body.SubSections()) != 2 {
		t.Fatalf("Expected 2 main sections, got %d", len(doc.Body.SubSections()))
	}
	for _, section := range doc.Body.SubSections() {
		if section.ID == "n1" || section.ID == "n2" {
			t.Errorf("Note section '%s' leaked into the main body", section.ID)
		}
	}

	var bodyTexts []string
	collectParagraphTexts(doc.Body, &bodyTexts)
	if joined := strings.Join(bodyTexts, "\n"); strings.Contains(joined, "NOTE TEXT") {
		t.Error("Note text leaked into the main body")
	}
}

// TestParseFB2Body_BinaryMIME verifies that the declared content-type of a
// <binary> element survives parsing alongside the decoded payload.
func TestParseFB2Body_BinaryMIME(t *testing.T) {
	data := loadTestData(t, "images_notes.fb2")

	doc, err := ParseFB2Body(data)
	if err != nil {
		t.Fatalf("ParseFB2Body failed: %v", err)
	}

	img, ok := doc.Binary["img1"]
	if !ok {
		t.Fatal("Expected to find image with ID 'img1'")
	}
	if img.MIME != "image/png" {
		t.Errorf("Expected MIME 'image/png', got '%s'", img.MIME)
	}
	if !bytes.HasPrefix(img.Data, []byte{0x89, 'P', 'N', 'G'}) {
		t.Error("Expected decoded PNG payload")
	}
}

// TestParseFB2Body_Encodings runs charset handling over real byte fixtures
// from testdata: declared charsets, BOM-marked Unicode, and declarations
// that lie. The fixtures are files with actual encoded bytes, produced by
// iconv (an encoder independent of the x/text charmaps under test):
//
//	iconv -f UTF-8 -t WINDOWS-1251 / KOI8-R / ISO-8859-1 / ISO-8859-5 / UTF-16LE / UTF-16BE
//
// Building them in-code with x/text would be a circular check: a bug shared
// by the library and the decoder would pass unnoticed.
//
// Sixth iteration: there is no statistical detection anymore. A whole-file
// strict UTF-8 check and the XML declaration decide; undeclared single-byte
// content is a typed error, not a guess.
func TestParseFB2Body_Encodings(t *testing.T) {
	ruMarker := "Съешь ещё этих мягких французских булок, да выпей чаю"

	decoded := []struct {
		name   string
		file   string
		marker string
	}{
		{"windows-1251 declared", "encoding_cp1251.fb2", ruMarker},
		{"koi8-r declared", "encoding_koi8r.fb2", ruMarker},
		{"iso-8859-1 declared", "encoding_latin1.fb2", "café naïve"},
		{"iso-8859-5 declared", "encoding_iso8859_5.fb2", ruMarker},
		{"utf-8 with BOM", "encoding_utf8_bom.fb2", ruMarker},
		{"utf-16le with BOM", "encoding_utf16le_bom.fb2", ruMarker},
		{"utf-16be with BOM", "encoding_utf16be_bom.fb2", ruMarker},
		{"utf-8 bytes lying about being windows-1251", "encoding_utf8_declared_cp1251.fb2", ruMarker},
		{"utf-8 bytes lying about being iso-8859-1", "encoding_utf8_declared_latin1.fb2", ruMarker},
	}

	for _, tt := range decoded {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := ParseFB2Body(loadTestData(t, tt.file))
			if err != nil {
				t.Fatalf("ParseFB2Body failed: %v", err)
			}
			if joined := joinedParagraphTexts(doc); !strings.Contains(joined, tt.marker) {
				t.Errorf("Marker text not decoded correctly; got: %.200s", joined)
			}
		})
	}

	// Undeclared (or falsely declared) single-byte content is refused with a
	// typed error: the catalog census (88k books) found no book that needs a
	// statistical guess, so the system refuses to guess.
	refused := []struct {
		name string
		file string
		want error
	}{
		{"windows-1251 without declaration", "encoding_cp1251_nodecl.fb2", parser.ErrUndeclaredCharset},
		{"koi8-r without declaration", "encoding_koi8r_nodecl.fb2", parser.ErrUndeclaredCharset},
		// Declared utf-8 but the bytes are wholesale windows-1251: damage is
		// far above the local-repair budget, so this is an error, not a repair.
		{"windows-1251 bytes lying about being utf-8", "encoding_cp1251_declared_utf8.fb2", parser.ErrDamagedContent},
	}

	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseFB2Body(loadTestData(t, tt.file))
			if !errors.Is(err, tt.want) {
				t.Errorf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

// TestParseFB2Body_DeclaredUTF8LocalDamage pins the repair contract for a
// book that declares UTF-8 and carries one corrupt byte: the byte is
// replaced with U+FFFD and the book is read. Refusing the whole book over
// one byte was the production defect this test guards.
func TestParseFB2Body_DeclaredUTF8LocalDamage(t *testing.T) {
	xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook><body><section><p>МАРКЕР ПЕРВЫЙ</p><p>МАРКЕР ВТОРОЙ</p></section></body></FictionBook>`)

	// One corrupt byte inside markup: the '<' of the closing root tag.
	tail := bytes.LastIndex(xmlContent, []byte("</FictionBook>"))
	if tail == -1 {
		t.Fatal("fixture broken: no closing root tag")
	}
	xmlContent[tail] = 0xFF // never valid anywhere in UTF-8

	doc, err := ParseFB2Body(xmlContent)
	if err != nil {
		t.Fatalf("one corrupt byte must not refuse the book: %v", err)
	}
	joined := joinedParagraphTexts(doc)
	for _, marker := range []string{"МАРКЕР ПЕРВЫЙ", "МАРКЕР ВТОРОЙ"} {
		if !strings.Contains(joined, marker) {
			t.Errorf("marker %q lost; got: %.200s", marker, joined)
		}
	}
}

// TestParseFB2Body_XML11VersionEndToEnd proves the pipeline-wide verdict for
// prolog damage: a declared windows-1251 book whose declaration says
// version="1.1" must be read, not refused at the charset stage — the root
// check judges only the root, and sanitizeXMLVersion repairs the version
// downstream. The Cyrillic bytes are iconv-produced (cff0e8e2e5f2 =
// "Привет"), same provenance as the parser-level fixtures; the rest of the
// fixture stays ASCII so the file really is windows-1251 throughout.
func TestParseFB2Body_XML11VersionEndToEnd(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.1" encoding="windows-1251"?>`)
	b.WriteString(`<FictionBook><body><section><p>MARKER-ONE `)
	b.Write([]byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2})
	b.WriteString(`</p></section></body></FictionBook>`)

	doc, err := ParseFB2Body(b.Bytes())
	if err != nil {
		t.Fatalf("version 1.1 is prolog damage, not a refusal: %v", err)
	}
	joined := joinedParagraphTexts(doc)
	if !strings.Contains(joined, "MARKER-ONE Привет") {
		t.Errorf("marker lost or misdecoded; got: %.200s", joined)
	}
}

// TestParseFB2Body_BrokenXMLOutcomes pins down the three distinguishable
// outcomes for damaged input: an error for garbage, a clean empty book,
// and partial text extraction for a truncated document. The garbage case
// lives in TestParseFB2Body_InvalidXML.
func TestParseFB2Body_BrokenXMLOutcomes(t *testing.T) {
	t.Run("valid book without body is not an error", func(t *testing.T) {
		xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description>
    <title-info>
      <book-title>Empty Book</book-title>
    </title-info>
  </description>
</FictionBook>`)

		doc, err := ParseFB2Body(xmlContent)
		if err != nil {
			t.Fatalf("Expected no error for a valid book without body, got: %v", err)
		}
		if doc == nil {
			t.Fatal("Expected non-nil document")
		}
		if texts := documentParagraphTexts(doc); len(texts) != 0 {
			t.Errorf("Expected no paragraphs in an empty book, got %d", len(texts))
		}
	})

	t.Run("truncated document yields partial text", func(t *testing.T) {
		xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<body>
<section>
<title><p>Chapter</p></title>
<p>PARTIAL MARKER ONE</p>
<p>this paragraph is never closed`)

		doc, err := ParseFB2Body(xmlContent)
		if err != nil {
			t.Fatalf("Expected partial extraction without error, got: %v", err)
		}
		if joined := joinedParagraphTexts(doc); !strings.Contains(joined, "PARTIAL MARKER ONE") {
			t.Errorf("Expected partial text to be extracted, got: %.200s", joined)
		}
	})
}

// TestParseFB2Body_DepthLimit verifies that pathological nesting is rejected
// with an error instead of crashing the process with a stack overflow. The
// assertions sit exactly on the configured limits: "somewhere far" tests
// would keep passing even if the limit drifted.
func TestParseFB2Body_DepthLimit(t *testing.T) {
	nested := func(depth int) []byte {
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="utf-8"?><FictionBook><body>`)
		for i := 0; i < depth; i++ {
			b.WriteString("<section>")
		}
		b.WriteString("<p>DEEP MARKER</p>")
		for i := 0; i < depth; i++ {
			b.WriteString("</section>")
		}
		b.WriteString("</body></FictionBook>")
		return []byte(b.String())
	}

	t.Run("section nesting at the limit parses", func(t *testing.T) {
		doc, err := ParseFB2Body(nested(maxFB2SectionDepth))
		if err != nil {
			t.Fatalf("ParseFB2Body failed at the exact limit %d: %v", maxFB2SectionDepth, err)
		}
		if joined := joinedParagraphTexts(doc); !strings.Contains(joined, "DEEP MARKER") {
			t.Error("Expected deep marker text to be parsed")
		}
	})

	t.Run("section nesting one past the limit is an error", func(t *testing.T) {
		_, err := ParseFB2Body(nested(maxFB2SectionDepth + 1))
		if err == nil {
			t.Fatalf("Expected a depth-limit error for %d nested sections", maxFB2SectionDepth+1)
		}
		if !errors.Is(err, ErrDepthLimit) {
			t.Errorf("Expected a typed ErrDepthLimit, got: %v", err)
		}
	})

	inlineNested := func(depth int) []byte {
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="utf-8"?><FictionBook><body><section><p>`)
		for i := 0; i < depth; i++ {
			b.WriteString("<strong>")
		}
		b.WriteString("INLINE MARKER")
		for i := 0; i < depth; i++ {
			b.WriteString("</strong>")
		}
		b.WriteString("</p></section></body></FictionBook>")
		return []byte(b.String())
	}

	t.Run("inline nesting at the limit parses", func(t *testing.T) {
		doc, err := ParseFB2Body(inlineNested(maxFB2InlineDepth))
		if err != nil {
			t.Fatalf("ParseFB2Body failed at the exact limit %d: %v", maxFB2InlineDepth, err)
		}
		if joined := joinedParagraphTexts(doc); !strings.Contains(joined, "INLINE MARKER") {
			t.Error("Expected inline marker text to be parsed")
		}
	})

	t.Run("inline nesting one past the limit is an error", func(t *testing.T) {
		_, err := ParseFB2Body(inlineNested(maxFB2InlineDepth + 1))
		if err == nil {
			t.Fatalf("Expected a depth-limit error for %d nested inline elements", maxFB2InlineDepth+1)
		}
		if !errors.Is(err, ErrDepthLimit) {
			t.Errorf("Expected a typed ErrDepthLimit, got: %v", err)
		}
	})
}
