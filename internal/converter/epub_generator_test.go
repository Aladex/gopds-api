package converter

import (
	"archive/zip"
	"bytes"
	"gopds-api/internal/parser"
	"io"
	"strings"
	"testing"
)

// TestGenerateEPUB_Basic tests basic EPUB generation
func TestGenerateEPUB_Basic(t *testing.T) {
	// Create minimal FB2Document
	doc := &FB2Document{
		Title: "Test Book",
		Body: &FB2BodySection{
			Title: "Chapter 1",
			Paragraphs: []*FB2Paragraph{
				{
					Kind: ParagraphKindNormal,
					Text: "This is a test paragraph.",
					Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "This is a test paragraph."},
					},
				},
			},
		},
	}

	// Create minimal BookFile
	bookFile := &parser.BookFile{
		Title: "Test Book",
		Authors: []parser.Author{
			{Name: "Test Author"},
		},
		Language: "en",
	}

	// Generate EPUB
	generator := NewEPUBGenerator()
	reader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Fatalf("GenerateEPUB failed: %v", err)
	}
	defer reader.Close()

	// Read EPUB content
	epubData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read EPUB data: %v", err)
	}

	if len(epubData) == 0 {
		t.Fatal("EPUB data is empty")
	}

	t.Logf("Generated EPUB size: %d bytes", len(epubData))
}

// TestGenerateEPUB_ZipStructure tests that EPUB has correct ZIP structure
func TestGenerateEPUB_ZipStructure(t *testing.T) {
	doc := &FB2Document{
		Title: "Test Book",
		Body: &FB2BodySection{
			Title: "Chapter 1",
			Paragraphs: []*FB2Paragraph{
				{Kind: ParagraphKindNormal, Text: "Test content."},
			},
		},
	}

	bookFile := &parser.BookFile{
		Title: "Test Book",
		Authors: []parser.Author{
			{Name: "Test Author"},
		},
		Language: "en",
	}

	generator := NewEPUBGenerator()
	reader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Fatalf("GenerateEPUB failed: %v", err)
	}
	defer reader.Close()

	epubData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read EPUB data: %v", err)
	}

	// Open as ZIP
	zipReader, err := zip.NewReader(bytes.NewReader(epubData), int64(len(epubData)))
	if err != nil {
		t.Fatalf("Failed to open EPUB as ZIP: %v", err)
	}

	// Check for required files
	requiredFiles := map[string]bool{
		"mimetype":               false,
		"META-INF/container.xml": false,
		"OEBPS/content.opf":      false,
		"OEBPS/toc.ncx":          false,
		"OEBPS/title.xhtml":      false,
		"OEBPS/toc.xhtml":        false,
	}

	// Log all files for debugging
	t.Log("Files in EPUB:")
	for _, file := range zipReader.File {
		t.Logf("  - %s", file.Name)
		if _, exists := requiredFiles[file.Name]; exists {
			requiredFiles[file.Name] = true
		}
	}

	// Verify all required files are present
	for filename, found := range requiredFiles {
		if !found {
			t.Errorf("Required file '%s' not found in EPUB", filename)
		}
	}
}

// TestGenerateEPUB_MimetypeFirst tests that mimetype is first file (uncompressed)
func TestGenerateEPUB_MimetypeFirst(t *testing.T) {
	doc := &FB2Document{
		Title: "Test Book",
		Body:  &FB2BodySection{},
	}

	bookFile := &parser.BookFile{
		Title: "Test Book",
		Authors: []parser.Author{
			{Name: "Test Author"},
		},
		Language: "en",
	}

	generator := NewEPUBGenerator()
	reader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Fatalf("GenerateEPUB failed: %v", err)
	}
	defer reader.Close()

	epubData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read EPUB data: %v", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(epubData), int64(len(epubData)))
	if err != nil {
		t.Fatalf("Failed to open EPUB as ZIP: %v", err)
	}

	// First file should be mimetype
	if len(zipReader.File) == 0 {
		t.Fatal("EPUB is empty")
	}

	firstFile := zipReader.File[0]
	if firstFile.Name != "mimetype" {
		t.Errorf("First file should be 'mimetype', got '%s'", firstFile.Name)
	}

	// mimetype should be stored (not compressed)
	if firstFile.Method != zip.Store {
		t.Error("mimetype file should be stored without compression")
	}

	// Read and verify mimetype content
	rc, err := firstFile.Open()
	if err != nil {
		t.Fatalf("Failed to open mimetype: %v", err)
	}
	defer rc.Close()

	mimetypeContent, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("Failed to read mimetype: %v", err)
	}

	expected := "application/epub+zip"
	if string(mimetypeContent) != expected {
		t.Errorf("Expected mimetype '%s', got '%s'", expected, string(mimetypeContent))
	}
}

// TestGenerateEPUB_ContainerXML tests META-INF/container.xml content
func TestGenerateEPUB_ContainerXML(t *testing.T) {
	doc := &FB2Document{
		Title: "Test Book",
		Body:  &FB2BodySection{},
	}

	bookFile := &parser.BookFile{
		Title: "Test Book",
		Authors: []parser.Author{
			{Name: "Test Author"},
		},
		Language: "en",
	}

	generator := NewEPUBGenerator()
	reader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Fatalf("GenerateEPUB failed: %v", err)
	}
	defer reader.Close()

	epubData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read EPUB data: %v", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(epubData), int64(len(epubData)))
	if err != nil {
		t.Fatalf("Failed to open EPUB as ZIP: %v", err)
	}

	// Find container.xml
	var containerFile *zip.File
	for _, file := range zipReader.File {
		if file.Name == "META-INF/container.xml" {
			containerFile = file
			break
		}
	}

	if containerFile == nil {
		t.Fatal("container.xml not found")
	}

	// Read container.xml
	rc, err := containerFile.Open()
	if err != nil {
		t.Fatalf("Failed to open container.xml: %v", err)
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("Failed to read container.xml: %v", err)
	}

	contentStr := string(content)

	// Check for required elements
	if !strings.Contains(contentStr, "OEBPS/content.opf") {
		t.Error("container.xml should reference OEBPS/content.opf")
	}

	if !strings.Contains(contentStr, "rootfile") {
		t.Error("container.xml should contain rootfile element")
	}
}

// TestGenerateEPUB_WithMetadata tests EPUB generation with complete metadata
func TestGenerateEPUB_WithMetadata(t *testing.T) {
	doc := &FB2Document{
		Title: "Test Book",
		Body:  &FB2BodySection{},
	}

	bookFile := &parser.BookFile{
		Title: "Test Book",
		Authors: []parser.Author{
			{Name: "First Author"},
			{Name: "Second Author"},
		},
		Language:   "ru",
		Tags:       []string{"fiction"},
		Annotation: "This is a test annotation.",
		Series: &parser.Series{
			Title: "Test Series",
			Index: "1",
		},
	}

	generator := NewEPUBGenerator()
	reader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Fatalf("GenerateEPUB failed: %v", err)
	}
	defer reader.Close()

	epubData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read EPUB data: %v", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(epubData), int64(len(epubData)))
	if err != nil {
		t.Fatalf("Failed to open EPUB as ZIP: %v", err)
	}

	// Find content.opf
	var opfFile *zip.File
	for _, file := range zipReader.File {
		if file.Name == "OEBPS/content.opf" {
			opfFile = file
			break
		}
	}

	if opfFile == nil {
		t.Fatal("content.opf not found")
	}

	// Read content.opf
	rc, err := opfFile.Open()
	if err != nil {
		t.Fatalf("Failed to open content.opf: %v", err)
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("Failed to read content.opf: %v", err)
	}

	contentStr := string(content)
	t.Logf("content.opf content:\n%s", contentStr)

	// Check metadata elements
	if !strings.Contains(contentStr, "Test Book") {
		t.Error("content.opf should contain book title")
	}

	if !strings.Contains(contentStr, "First Author") {
		t.Error("content.opf should contain first author")
	}

	if !strings.Contains(contentStr, "Second Author") {
		t.Error("content.opf should contain second author")
	}

	if !strings.Contains(contentStr, "<dc:language>ru</dc:language>") {
		t.Error("content.opf should contain language")
	}

	// Series might be in calibre:series meta tag or similar
	if !strings.Contains(contentStr, "Test Series") {
		t.Logf("Warning: Series name not found in content.opf (might use different format)")
	}
}

// TestGenerateEPUB_WithImages tests EPUB generation with embedded images
func TestGenerateEPUB_WithImages(t *testing.T) {
	// Create a minimal PNG image (1x1 red pixel)
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xDD, 0x8D,
		0xB4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}

	doc := &FB2Document{
		Title: "Test Book with Images",
		Body: &FB2BodySection{
			Paragraphs: []*FB2Paragraph{
				{
					Kind: ParagraphKindNormal,
					Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "Text before image "},
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#img1"}},
						{Type: InlineTypeText, Content: " text after image."},
					},
				},
			},
		},
		Binary: map[string][]byte{
			"img1": pngData,
		},
	}

	bookFile := &parser.BookFile{
		Title: "Test Book with Images",
		Authors: []parser.Author{
			{Name: "Test Author"},
		},
		Language: "en",
	}

	generator := NewEPUBGenerator()
	reader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Fatalf("GenerateEPUB failed: %v", err)
	}
	defer reader.Close()

	epubData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read EPUB data: %v", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(epubData), int64(len(epubData)))
	if err != nil {
		t.Fatalf("Failed to open EPUB as ZIP: %v", err)
	}

	// Check for image file
	t.Log("Files in EPUB:")
	foundImage := false
	for _, file := range zipReader.File {
		t.Logf("  - %s", file.Name)
		if strings.Contains(file.Name, "img1") || strings.HasPrefix(file.Name, "OEBPS/images/") {
			foundImage = true

			// Verify image content
			rc, err := file.Open()
			if err != nil {
				t.Fatalf("Failed to open image: %v", err)
			}
			defer rc.Close()

			imgData, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("Failed to read image: %v", err)
			}

			if !bytes.Equal(imgData, pngData) {
				t.Error("Image data doesn't match original")
			}
			break
		}
	}

	if !foundImage {
		t.Error("Image file not found in EPUB")
	}
}

// TestGenerateEPUB_WithNotes tests EPUB generation with footnotes
func TestGenerateEPUB_WithNotes(t *testing.T) {
	doc := &FB2Document{
		Title: "Test Book with Notes",
		Body: &FB2BodySection{
			Paragraphs: []*FB2Paragraph{
				{
					Kind: ParagraphKindNormal,
					Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "Text with footnote"},
						{
							Type: InlineTypeLink,
							Attrs: map[string]string{
								"href": "#note1",
								"type": "note",
							},
							Children: []*FB2InlineElement{
								{Type: InlineTypeSup, Children: []*FB2InlineElement{
									{Type: InlineTypeText, Content: "1"},
								}},
							},
						},
					},
				},
			},
		},
		Notes: []*FB2BodySection{
			{
				ID:    "note1",
				Title: "1",
				Paragraphs: []*FB2Paragraph{
					{Kind: ParagraphKindNormal, Text: "This is the footnote text."},
				},
			},
		},
	}

	bookFile := &parser.BookFile{
		Title: "Test Book with Notes",
		Authors: []parser.Author{
			{Name: "Test Author"},
		},
		Language: "en",
	}

	generator := NewEPUBGenerator()
	reader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Fatalf("GenerateEPUB failed: %v", err)
	}
	defer reader.Close()

	epubData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read EPUB data: %v", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(epubData), int64(len(epubData)))
	if err != nil {
		t.Fatalf("Failed to open EPUB as ZIP: %v", err)
	}

	// Check for notes.xhtml
	foundNotes := false
	for _, file := range zipReader.File {
		if file.Name == "OEBPS/notes.xhtml" {
			foundNotes = true

			// Read notes content
			rc, err := file.Open()
			if err != nil {
				t.Fatalf("Failed to open notes.xhtml: %v", err)
			}
			defer rc.Close()

			content, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("Failed to read notes.xhtml: %v", err)
			}

			contentStr := string(content)
			if !strings.Contains(contentStr, "note1") {
				t.Error("notes.xhtml should contain note1 reference")
			}

			if !strings.Contains(contentStr, "This is the footnote text.") {
				t.Error("notes.xhtml should contain footnote text")
			}
			break
		}
	}

	if !foundNotes {
		t.Error("notes.xhtml not found in EPUB")
	}
}

// TestGenerateEPUB_EmptyDocument tests handling of empty document
func TestGenerateEPUB_EmptyDocument(t *testing.T) {
	doc := &FB2Document{
		Title: "",
		Body:  nil,
	}

	bookFile := &parser.BookFile{
		Title: "Empty Book",
		Authors: []parser.Author{
			{Name: "Unknown"},
		},
		Language: "en",
	}

	// Should not panic
	generator := NewEPUBGenerator()
	reader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Logf("GenerateEPUB returned error for empty document: %v", err)
		return
	}
	defer reader.Close()

	epubData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read EPUB data: %v", err)
	}

	if len(epubData) == 0 {
		t.Error("Expected non-empty EPUB even for empty document")
	}
}
