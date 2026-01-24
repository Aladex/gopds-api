package converter

import (
	"bytes"
	"gopds-api/internal/parser"
	"testing"
)

// TestParseFB2Complete_Basic tests basic combined parsing
func TestParseFB2Complete_Basic(t *testing.T) {
	data := loadTestData(t, "simple.fb2")

	doc, bookFile, err := ParseFB2Complete(data, true)
	if err != nil {
		t.Fatalf("ParseFB2Complete failed: %v", err)
	}

	if doc == nil {
		t.Fatal("Expected non-nil FB2Document")
	}

	if bookFile == nil {
		t.Fatal("Expected non-nil BookFile")
	}

	// Verify body was parsed
	if doc.Body == nil {
		t.Error("Expected body to be parsed")
	}

	// Verify metadata was parsed
	if bookFile.Title == "" {
		t.Error("Expected title to be parsed")
	}

	if len(bookFile.Authors) == 0 {
		t.Error("Expected authors to be parsed")
	}
}

// TestParseFB2Complete_CompareWithSeparateParsing tests that combined parsing
// produces identical results to separate metadata + body parsing
func TestParseFB2Complete_CompareWithSeparateParsing(t *testing.T) {
	testFiles := []string{
		"simple.fb2",
		"formatting.fb2",
		"nested_sections.fb2",
		"cyrillic.fb2",
		"special_elements.fb2",
	}

	for _, filename := range testFiles {
		t.Run(filename, func(t *testing.T) {
			data := loadTestData(t, filename)

			// Combined parsing
			docCombined, bookFileCombined, err := ParseFB2Complete(data, true)
			if err != nil {
				t.Fatalf("ParseFB2Complete failed: %v", err)
			}

			// Separate parsing (old way)
			metadataParser := parser.NewFB2Parser(true)
			bookFileSeparate, err := metadataParser.Parse(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Separate metadata parsing failed: %v", err)
			}

			docSeparate, err := ParseFB2Body(data)
			if err != nil {
				t.Fatalf("Separate body parsing failed: %v", err)
			}

			// Compare metadata
			if bookFileCombined.Title != bookFileSeparate.Title {
				t.Errorf("Title mismatch: combined=%s, separate=%s",
					bookFileCombined.Title, bookFileSeparate.Title)
			}

			if len(bookFileCombined.Authors) != len(bookFileSeparate.Authors) {
				t.Errorf("Authors count mismatch: combined=%d, separate=%d",
					len(bookFileCombined.Authors), len(bookFileSeparate.Authors))
			}

			if bookFileCombined.Language != bookFileSeparate.Language {
				t.Errorf("Language mismatch: combined=%s, separate=%s",
					bookFileCombined.Language, bookFileSeparate.Language)
			}

			// Compare body structure (check section counts)
			if (docCombined.Body == nil) != (docSeparate.Body == nil) {
				t.Error("Body presence mismatch between combined and separate parsing")
			}

			if docCombined.Body != nil && docSeparate.Body != nil {
				if docCombined.Body.Title != docSeparate.Body.Title {
					t.Errorf("Body title mismatch: combined=%s, separate=%s",
						docCombined.Body.Title, docSeparate.Body.Title)
				}

				if len(docCombined.Body.Paragraphs) != len(docSeparate.Body.Paragraphs) {
					t.Errorf("Body paragraphs count mismatch: combined=%d, separate=%d",
						len(docCombined.Body.Paragraphs), len(docSeparate.Body.Paragraphs))
				}

				if len(docCombined.Body.SubSections) != len(docSeparate.Body.SubSections) {
					t.Errorf("Body subsections count mismatch: combined=%d, separate=%d",
						len(docCombined.Body.SubSections), len(docSeparate.Body.SubSections))
				}
			}

			// Compare binary images
			if len(docCombined.Binary) != len(docSeparate.Binary) {
				t.Errorf("Binary images count mismatch: combined=%d, separate=%d",
					len(docCombined.Binary), len(docSeparate.Binary))
			}

			// Compare notes
			if len(docCombined.Notes) != len(docSeparate.Notes) {
				t.Errorf("Notes count mismatch: combined=%d, separate=%d",
					len(docCombined.Notes), len(docSeparate.Notes))
			}
		})
	}
}

// TestParseFB2Complete_WithCover tests parsing with cover extraction
func TestParseFB2Complete_WithCover(t *testing.T) {
	data := loadTestData(t, "images_notes.fb2")

	doc, bookFile, err := ParseFB2Complete(data, true)
	if err != nil {
		t.Fatalf("ParseFB2Complete failed: %v", err)
	}

	// Check that images were parsed in body
	if len(doc.Binary) == 0 {
		t.Error("Expected binary images to be parsed")
	}

	// Check that cover was extracted in metadata (if present in file)
	// Note: images_notes.fb2 has binary image but may not have coverpage element
	t.Logf("Cover extracted: %v bytes", len(bookFile.Cover))
}

// TestParseFB2Complete_WithoutCover tests parsing without cover extraction
func TestParseFB2Complete_WithoutCover(t *testing.T) {
	data := loadTestData(t, "images_notes.fb2")

	doc, bookFile, err := ParseFB2Complete(data, false)
	if err != nil {
		t.Fatalf("ParseFB2Complete failed: %v", err)
	}

	// Body images should still be parsed
	if len(doc.Binary) == 0 {
		t.Error("Expected binary images to be parsed in body")
	}

	// Cover should not be extracted
	if len(bookFile.Cover) > 0 {
		t.Error("Did not expect cover to be extracted when readCover=false")
	}
}

// TestParseFB2Complete_Cyrillic tests combined parsing with cyrillic content
func TestParseFB2Complete_Cyrillic(t *testing.T) {
	data := loadTestData(t, "cyrillic.fb2")

	doc, bookFile, err := ParseFB2Complete(data, true)
	if err != nil {
		t.Fatalf("ParseFB2Complete failed: %v", err)
	}

	// Check metadata
	if bookFile.Title == "" {
		t.Error("Expected cyrillic title to be parsed")
	}

	// Check body
	if doc.Body == nil {
		t.Fatal("Expected body to be parsed")
	}

	if doc.Body.Title != "Глава 1" {
		t.Errorf("Expected cyrillic section title 'Глава 1', got '%s'", doc.Body.Title)
	}
}

// TestParseFB2Complete_MalformedXML tests handling of malformed XML
func TestParseFB2Complete_MalformedXML(t *testing.T) {
	data := loadTestData(t, "malformed.fb2")

	doc, bookFile, err := ParseFB2Complete(data, true)

	// Should handle gracefully after sanitization
	if err != nil {
		t.Logf("ParseFB2Complete returned error for malformed XML: %v", err)
		return
	}

	if doc == nil || bookFile == nil {
		t.Error("Expected non-nil results after sanitization")
	}
}

// TestParseFB2Complete_EmptyBody tests handling of FB2 without body
func TestParseFB2Complete_EmptyBody(t *testing.T) {
	xmlContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description>
    <title-info>
      <book-title>Empty Book</book-title>
      <author>
        <first-name>Test</first-name>
        <last-name>Author</last-name>
      </author>
      <lang>en</lang>
    </title-info>
  </description>
</FictionBook>`)

	doc, bookFile, err := ParseFB2Complete(xmlContent, false)

	// Should handle gracefully
	if err != nil {
		t.Logf("ParseFB2Complete returned error for empty body: %v", err)
	}

	if doc == nil || bookFile == nil {
		t.Fatal("Expected non-nil results")
	}

	// Metadata should be parsed
	if bookFile.Title != "Empty Book" {
		t.Errorf("Expected title 'Empty Book', got '%s'", bookFile.Title)
	}

	if len(bookFile.Authors) == 0 {
		t.Error("Expected at least one author")
	}
}

// TestParseFB2Complete_InvalidXML tests handling of completely invalid XML
func TestParseFB2Complete_InvalidXML(t *testing.T) {
	xmlContent := []byte(`This is not XML at all!`)

	doc, bookFile, err := ParseFB2Complete(xmlContent, false)

	// Should either return an error OR handle gracefully with minimal document
	// This behavior is consistent with ParseFB2Body
	if err != nil {
		t.Logf("Got expected error for invalid XML: %v", err)
		return
	}

	// If no error, should have minimal valid results
	if doc == nil || bookFile == nil {
		t.Error("Expected non-nil results for gracefully handled invalid XML")
	}
	t.Logf("Parser handled invalid XML gracefully")
}

// TestParseFB2Complete_SpecialElements tests parsing of special FB2 elements
func TestParseFB2Complete_SpecialElements(t *testing.T) {
	data := loadTestData(t, "special_elements.fb2")

	doc, bookFile, err := ParseFB2Complete(data, true)
	if err != nil {
		t.Fatalf("ParseFB2Complete failed: %v", err)
	}

	// Check body has special elements
	if doc.Body == nil || len(doc.Body.Paragraphs) == 0 {
		t.Fatal("Expected body with paragraphs")
	}

	// Check for different paragraph kinds
	foundPoem := false
	foundCite := false
	foundTable := false

	for _, p := range doc.Body.Paragraphs {
		switch p.Kind {
		case ParagraphKindPoem, ParagraphKindPoemLine:
			foundPoem = true
		case ParagraphKindCite:
			foundCite = true
		case ParagraphKindTable:
			foundTable = true
		}
	}

	if !foundPoem {
		t.Error("Expected to find poem elements")
	}
	if !foundCite {
		t.Error("Expected to find cite elements")
	}
	if !foundTable {
		t.Error("Expected to find table element")
	}

	// Metadata should also be present
	if bookFile.Title == "" {
		t.Error("Expected title in metadata")
	}
}
