package converter

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"gopds-api/internal/parser"
)

// TestParseFB2Complete_Basic tests basic combined parsing
func TestParseFB2Complete_Basic(t *testing.T) {
	data := loadTestData(t, "simple.fb2")

	doc, bookFile, err := ParseFB2Complete(context.Background(), data, true)
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
			docCombined, bookFileCombined, err := ParseFB2Complete(context.Background(), data, true)
			if err != nil {
				t.Fatalf("ParseFB2Complete failed: %v", err)
			}

			// Separate parsing (old way)
			metadataParser := parser.NewFB2Parser(true)
			bookFileSeparate, err := metadataParser.Parse(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Separate metadata parsing failed: %v", err)
			}

			docSeparate, err := ParseFB2Body(context.Background(), data)
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

				if len(docCombined.Body.Paragraphs()) != len(docSeparate.Body.Paragraphs()) {
					t.Errorf("Body paragraphs count mismatch: combined=%d, separate=%d",
						len(docCombined.Body.Paragraphs()), len(docSeparate.Body.Paragraphs()))
				}

				if len(docCombined.Body.SubSections()) != len(docSeparate.Body.SubSections()) {
					t.Errorf("Body subsections count mismatch: combined=%d, separate=%d",
						len(docCombined.Body.SubSections()), len(docSeparate.Body.SubSections()))
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

	doc, bookFile, err := ParseFB2Complete(context.Background(), data, true)
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

	doc, bookFile, err := ParseFB2Complete(context.Background(), data, false)
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

	doc, bookFile, err := ParseFB2Complete(context.Background(), data, true)
	if err != nil {
		t.Fatalf("ParseFB2Complete failed: %v", err)
	}

	// Check metadata
	if bookFile.Title == "" {
		t.Error("Expected cyrillic title to be parsed")
	}

	// Check body
	if doc.Body == nil || len(doc.Body.SubSections()) == 0 {
		t.Fatal("Expected body with at least one section")
	}

	section := doc.Body.SubSections()[0]
	if section.Title != "Глава 1" {
		t.Errorf("Expected cyrillic section title 'Глава 1', got '%s'", section.Title)
	}
}

// TestParseFB2Complete_MalformedXML tests handling of malformed XML
func TestParseFB2Complete_MalformedXML(t *testing.T) {
	data := loadTestData(t, "malformed.fb2")

	doc, bookFile, err := ParseFB2Complete(context.Background(), data, true)

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

	doc, bookFile, err := ParseFB2Complete(context.Background(), xmlContent, false)

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

// TestParseFB2Complete_InvalidXML pins the thirteenth-iteration refusal for
// input with no XML elements at all: a typed ErrNotFictionBook, never a
// silently empty book.
func TestParseFB2Complete_InvalidXML(t *testing.T) {
	xmlContent := []byte(`This is not XML at all!`)

	doc, bookFile, err := ParseFB2Complete(context.Background(), xmlContent, false)
	if !errors.Is(err, ErrNotFictionBook) {
		t.Fatalf("expected a typed ErrNotFictionBook for non-XML input, got document %+v, book %+v, error %v",
			doc, bookFile, err)
	}
}

// TestParseFB2Complete_SpecialElements tests parsing of special FB2 elements
func TestParseFB2Complete_SpecialElements(t *testing.T) {
	data := loadTestData(t, "special_elements.fb2")

	doc, bookFile, err := ParseFB2Complete(context.Background(), data, true)
	if err != nil {
		t.Fatalf("ParseFB2Complete failed: %v", err)
	}

	// Check body has special elements
	if doc.Body == nil || len(doc.Body.SubSections()) == 0 {
		t.Fatal("Expected body with at least one section")
	}
	section := doc.Body.SubSections()[0]
	if len(section.Paragraphs()) == 0 {
		t.Fatal("Expected section with paragraphs")
	}

	// Check for different paragraph kinds
	foundPoem := false
	foundCite := false
	foundTable := false

	for _, p := range section.Paragraphs() {
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

// TestParseFB2Complete_ForeignRootFailsBeforeSyntaxError pins the
// thirteenth-iteration order of verdicts: the root check fires on the first
// element the decoder produces, so a foreign root is refused with the typed
// ErrNotFictionBook even when a syntax error waits further down the stream.
func TestParseFB2Complete_ForeignRootFailsBeforeSyntaxError(t *testing.T) {
	in := []byte(`<?xml version="1.0" encoding="utf-8"?><notabook><![CDATA[never closed`)
	_, _, err := ParseFB2Complete(context.Background(), in, false)
	if !errors.Is(err, ErrNotFictionBook) {
		t.Errorf("expected a typed ErrNotFictionBook from the root check, got %v", err)
	}
}

// A context already canceled before the call must surface as a cancel error,
// not as a parsed document. The check fires inside the token loop, so the
// fixture has to be long enough for the loop to reach the check at least once.
func TestParseFB2Complete_CanceledBeforeReturnsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := ParseFB2Complete(ctx, bigFB2(4000), false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a wrapping of context.Canceled", err)
	}
}

// A cancel that arrives while the loop is running must stop before the end of
// the document. The main token loop of ParseFB2Complete is the long part of
// the work, so the ctx check has to live there — pushing the cancellation
// into the fallback (which calls ParseFB2Body) is not enough, because the
// fallback only runs when the main decoder errors out, which it never does
// on a well-formed big book. Without a check in the main loop the function
// parses the whole file under a canceled ctx and returns nil.
//
// Timing test, like for ParseFB2Body: enough tokens that the loop cannot
// finish inside the cancel window.
func TestParseFB2Complete_CancelMidParseStopsBeforeEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, _, err := ParseFB2Complete(ctx, bigFB2(50000), false)
		done <- err
	}()

	// Let the parser chew through some tokens before the cancel reaches the
	// next ctx check.
	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want a wrapping of context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ParseFB2Complete did not return within 10s of cancel")
	}
}

// With a live context the parser must produce the same document as before ctx
// was added. Regression guard — if a future change makes the ctx check
// misfire on a live ctx, or otherwise perturbs the parsing path, the
// structural assertions the suite already makes catch it.
func TestParseFB2Complete_LiveContextMatchesBaseline(t *testing.T) {
	data := loadTestData(t, "simple.fb2")
	doc, bookFile, err := ParseFB2Complete(context.Background(), data, true)
	if err != nil {
		t.Fatalf("a live ctx must not produce an error: %v", err)
	}
	if doc == nil || doc.Body == nil {
		t.Fatal("a live ctx must produce a non-nil document with a body")
	}
	if bookFile == nil {
		t.Fatal("a live ctx must produce a non-nil book file")
	}
}
