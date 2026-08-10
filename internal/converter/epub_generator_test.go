package converter

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"gopds-api/internal/parser"
)

// TestGenerateEPUB_Basic tests basic EPUB generation
func TestGenerateEPUB_Basic(t *testing.T) {
	// Create minimal FB2Document
	doc := &FB2Document{
		Title: "Test Book",
		Body: &FB2BodySection{
			Title: "Chapter 1",
			Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{
					Kind: ParagraphKindNormal,
					Text: "This is a test paragraph.",
					Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "This is a test paragraph."},
					},
				}},
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
			Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Text: "Test content."}},
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
			Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{
					Kind: ParagraphKindNormal,
					Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "Text before image "},
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#img1"}},
						{Type: InlineTypeText, Content: " text after image."},
					},
				}},
			},
		},
		Binary: map[string]FB2Binary{
			"img1": {Data: pngData, MIME: "image/png"},
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
			Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{
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
				}},
			},
		},
		Notes: []*FB2BodySection{
			{
				ID:    "note1",
				Title: "1",
				Content: []*FB2ContentItem{
					{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Text: "This is the footnote text."}},
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

// TestGenerateEPUB_ReuseDoesNotLeakNotes feeds one generator two books in a
// row — the first with footnotes, the second without. planNotes returns
// early for a noteless document, so unless GenerateEPUB resets the notes
// state, the second EPUB would carry the first book's notes page, anchors,
// and rewritten links.
func TestGenerateEPUB_ReuseDoesNotLeakNotes(t *testing.T) {
	withNotes := &FB2Document{
		Title: "Book With Notes",
		Body: &FB2BodySection{
			Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{
					Kind: ParagraphKindNormal,
					Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "Text with footnote"},
						{
							Type:  InlineTypeLink,
							Attrs: map[string]string{"href": "#note1", "type": "note"},
							Children: []*FB2InlineElement{
								{Type: InlineTypeSup, Children: []*FB2InlineElement{
									{Type: InlineTypeText, Content: "1"},
								}},
							},
						},
					},
				}},
			},
		},
		Notes: []*FB2BodySection{
			{
				ID:    "note1",
				Title: "1",
				Content: []*FB2ContentItem{
					{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Text: "Footnote of the first book."}},
				},
			},
		},
	}
	withoutNotes := &FB2Document{
		Title: "Book Without Notes",
		Body: &FB2BodySection{
			Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Text: "Plain text of the second book."}},
				// A real section forces buildTOC to emit nodes — without it the
				// TOC returns early and a leaked notes entry would stay hidden.
				{Section: &FB2BodySection{
					ID:    "s1",
					Title: "Only chapter",
					Content: []*FB2ContentItem{
						{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Text: "Chapter text."}},
					},
				}},
			},
		},
	}
	bookFile := &parser.BookFile{Title: "Reused Generator", Language: "en"}

	generator := NewEPUBGenerator()

	generate := func(doc *FB2Document) map[string][]byte {
		t.Helper()
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
			t.Fatalf("EPUB is not a valid ZIP: %v", err)
		}
		files := make(map[string][]byte, len(zipReader.File))
		for _, f := range zipReader.File {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("Failed to open %s: %v", f.Name, err)
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatalf("Failed to read %s: %v", f.Name, err)
			}
			files[f.Name] = content
		}
		return files
	}

	first := generate(withNotes)
	if _, ok := first["OEBPS/notes.xhtml"]; !ok {
		t.Fatal("First book should have notes.xhtml")
	}

	second := generate(withoutNotes)
	if _, ok := second["OEBPS/notes.xhtml"]; ok {
		t.Error("Second book has no notes but the EPUB carries a notes.xhtml from the first book")
	}
	for name, content := range second {
		if !strings.HasSuffix(name, ".xhtml") && !strings.HasSuffix(name, ".opf") && !strings.HasSuffix(name, ".ncx") {
			continue
		}
		if bytes.Contains(content, []byte("notes.xhtml")) {
			t.Errorf("%s of the second book references the first book's notes.xhtml", name)
		}
		if bytes.Contains(content, []byte("note-note1")) {
			t.Errorf("%s of the second book contains the first book's note anchor", name)
		}
		if bytes.Contains(content, []byte("Footnote of the first book")) {
			t.Errorf("%s of the second book contains the first book's footnote text", name)
		}
	}
}

// TestDetectImageMimeType_DeclaredVerifiedByMagicBytes pins the
// thirteenth-iteration use of the declared content-type: the parser keeps it
// on FB2Binary, and the generator trusts it only when the payload's magic
// bytes agree. A declaration the bytes contradict falls back to ID and
// content detection.
func TestDetectImageMimeType_DeclaredVerifiedByMagicBytes(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0x00, 0x01}
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	gif := []byte("GIF89a\x01\x00\x01\x00")
	webp := []byte("RIFF\x04\x00\x00\x00WEBPVP8 ")
	svg := []byte("  <svg xmlns=\"http://www.w3.org/2000/svg\"></svg>")

	cases := []struct {
		name     string
		data     []byte
		imageID  string
		declared string
		want     string
	}{
		{"declared png confirmed by magic", png, "img1", "image/png", "image/png"},
		{"declared jpeg confirmed by magic", jpeg, "img1", "image/jpeg", "image/jpeg"},
		{"declared gif confirmed by magic", gif, "img1", "image/gif", "image/gif"},
		{"declared webp confirmed by magic", webp, "img1", "image/webp", "image/webp"},
		{"declared svg confirmed by markup", svg, "img1", "image/svg+xml", "image/svg+xml"},
		{"declared type is case-insensitive", png, "img1", "IMAGE/PNG", "image/png"},
		{"declared gif beats the jpg ID hint when magic agrees", gif, "img1.jpg", "image/gif", "image/gif"},
		{"declared png contradicted by jpeg bytes", jpeg, "img1", "image/png", "image/jpeg"},
		{"declared jpeg contradicted by png bytes", png, "img1", "image/jpeg", "image/png"},
		{"declared svg contradicted by png bytes", png, "img1", "image/svg+xml", "image/png"},
		{"unsupported declared type falls through", png, "img1", "image/tiff", "image/png"},
		{"no declared type: ID hint wins", jpeg, "cover.jpg", "", "image/jpeg"},
		{"no declared type and no hint: content sniff", png, "img1", "", "image/png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectImageMimeType(tc.data, tc.imageID, tc.declared); got != tc.want {
				t.Errorf("detectImageMimeType(%q, %q, %q) = %q, want %q",
					tc.data[:4], tc.imageID, tc.declared, got, tc.want)
			}
		})
	}
}

// TestBuildImages_DeclaredMimeDecidesTheOutput drives the declaration through
// the real path — FB2Binary, buildImages, EPUB — rather than calling the
// detector directly. A unit test on the detector cannot tell whether the
// generator actually consults the parsed MIME, and a mutation that dropped it
// survived the package until this case existed: the payload is a GIF, the id
// says .jpg, and only the declaration can settle it.
func TestBuildImages_DeclaredMimeDecidesTheOutput(t *testing.T) {
	gif := append([]byte("GIF89a"), make([]byte, 16)...)
	doc := &FB2Document{
		Body: &FB2BodySection{},
		Binary: map[string]FB2Binary{
			"img.jpg": {Data: gif, MIME: "image/gif"},
		},
	}

	images := buildImages(doc)
	if len(images) != 1 {
		t.Fatalf("expected exactly one image, got %d", len(images))
	}
	img, ok := images["img.jpg"]
	if !ok {
		t.Fatalf("the binary is missing from the built images: %v", images)
	}
	if img.MediaType != "image/gif" {
		t.Errorf("declared image/gif over GIF bytes became %q: the parsed MIME is not reaching the generator",
			img.MediaType)
	}
}

// TestMimeMatchesMagic_SVGNeedsItsRoot pins the difference between "this is
// XML" and "this is an SVG". Every XML document opens with a prolog, so
// accepting one as confirmation would let any XML — an HTML page, another FB2
// — be declared an image and written into the EPUB as one.
func TestMimeMatchesMagic_SVGNeedsItsRoot(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"bare svg root", `<svg xmlns="http://www.w3.org/2000/svg"/>`, true},
		{"prolog then svg", `<?xml version="1.0"?><svg/>`, true},
		{"comment then svg", `<!-- note --><svg/>`, true},
		{"doctype then svg", `<!DOCTYPE svg><svg/>`, true},
		{"prolog then html", `<?xml version="1.0"?><html></html>`, false},
		{"prolog then fictionbook", `<?xml version="1.0"?><FictionBook/>`, false},
		{"prolog alone", `<?xml version="1.0"?>`, false},
		{"unterminated prolog", `<?xml version="1.0"`, false},
		{"plain text", `not markup at all`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mimeMatchesMagic("image/svg+xml", []byte(tc.data)); got != tc.want {
				t.Errorf("mimeMatchesMagic(image/svg+xml, %q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}
