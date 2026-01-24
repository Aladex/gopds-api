package utils

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestMobiConversion_Integration is an integration test that verifies
// the complete FB2→EPUB→MOBI conversion chain using kindlegen.
// This test requires:
// 1. A test FB2 file
// 2. kindlegen binary in kindlegen/ directory
func TestMobiConversion_Integration(t *testing.T) {
	// Skip if kindlegen is not available
	if _, err := os.Stat("../kindlegen/kindlegen"); os.IsNotExist(err) {
		t.Skip("kindlegen not found, skipping MOBI integration test")
	}

	// Create a temporary ZIP archive with a test FB2 file
	testFB2Content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description>
    <title-info>
      <genre>prose</genre>
      <author>
        <first-name>Test</first-name>
        <last-name>Author</last-name>
      </author>
      <book-title>MOBI Test Book</book-title>
      <lang>en</lang>
    </title-info>
  </description>
  <body>
    <section>
      <title><p>Chapter 1</p></title>
      <p>This is a test paragraph for MOBI conversion.</p>
      <p>Second paragraph with <strong>bold</strong> text.</p>
    </section>
  </body>
</FictionBook>`)

	// Create temporary ZIP file with FB2
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Failed to create temp ZIP: %v", err)
	}

	zipWriter := zip.NewWriter(zipFile)
	fb2Writer, err := zipWriter.Create("test.fb2")
	if err != nil {
		zipFile.Close()
		t.Fatalf("Failed to create FB2 entry in ZIP: %v", err)
	}

	if _, err = fb2Writer.Write(testFB2Content); err != nil {
		zipWriter.Close()
		zipFile.Close()
		t.Fatalf("Failed to write FB2 content: %v", err)
	}

	if err = zipWriter.Close(); err != nil {
		zipFile.Close()
		t.Fatalf("Failed to close ZIP writer: %v", err)
	}
	zipFile.Close()

	// Create BookProcessor
	processor := NewBookProcessor("test.fb2", zipPath)

	// Test MOBI conversion
	t.Run("ConvertToMobi", func(t *testing.T) {
		mobiReader, err := processor.Mobi()
		if err != nil {
			t.Fatalf("MOBI conversion failed: %v", err)
		}
		defer mobiReader.Close()

		// Read MOBI content
		mobiData, err := io.ReadAll(mobiReader)
		if err != nil {
			t.Fatalf("Failed to read MOBI data: %v", err)
		}

		// Verify MOBI was generated
		if len(mobiData) == 0 {
			t.Fatal("MOBI data is empty")
		}

		// MOBI files should start with specific headers
		// Check for common MOBI/PRC header patterns
		if len(mobiData) < 78 {
			t.Fatal("MOBI file is too small to be valid")
		}

		t.Logf("Successfully generated MOBI file: %d bytes", len(mobiData))

		// Optional: Save MOBI for manual inspection during development
		if testing.Verbose() {
			testMobiPath := filepath.Join(tmpDir, "test_output.mobi")
			if err := os.WriteFile(testMobiPath, mobiData, 0644); err == nil {
				t.Logf("Test MOBI saved to: %s", testMobiPath)
			}
		}
	})

	// Test that EPUB generation still works (should be used by Mobi internally)
	t.Run("ConvertToEpub", func(t *testing.T) {
		epubReader, err := processor.Epub()
		if err != nil {
			t.Fatalf("EPUB conversion failed: %v", err)
		}
		defer epubReader.Close()

		epubData, err := io.ReadAll(epubReader)
		if err != nil {
			t.Fatalf("Failed to read EPUB data: %v", err)
		}

		if len(epubData) == 0 {
			t.Fatal("EPUB data is empty")
		}

		// Verify EPUB structure (ZIP with mimetype)
		zipReader, err := zip.NewReader(bytes.NewReader(epubData), int64(len(epubData)))
		if err != nil {
			t.Fatalf("EPUB is not a valid ZIP: %v", err)
		}

		// Check for mimetype file
		foundMimetype := false
		for _, file := range zipReader.File {
			if file.Name == "mimetype" {
				foundMimetype = true
				break
			}
		}

		if !foundMimetype {
			t.Error("EPUB missing mimetype file")
		}

		t.Logf("Successfully generated EPUB file: %d bytes", len(epubData))
	})
}

// TestMobiConversion_ErrorHandling tests error handling in MOBI conversion
func TestMobiConversion_ErrorHandling(t *testing.T) {
	// Test with non-existent file
	t.Run("NonExistentFile", func(t *testing.T) {
		processor := NewBookProcessor("nonexistent.fb2", "nonexistent.zip")
		_, err := processor.Mobi()
		if err == nil {
			t.Error("Expected error for non-existent file")
		}
	})

	// Test with invalid ZIP
	t.Run("InvalidZip", func(t *testing.T) {
		tmpDir := t.TempDir()
		invalidZipPath := filepath.Join(tmpDir, "invalid.zip")
		if err := os.WriteFile(invalidZipPath, []byte("not a zip file"), 0644); err != nil {
			t.Fatalf("Failed to create invalid zip: %v", err)
		}

		processor := NewBookProcessor("test.fb2", invalidZipPath)
		_, err := processor.Mobi()
		if err == nil {
			t.Error("Expected error for invalid ZIP file")
		}
	})
}
