package utils

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"gopds-api/internal/converter"
	"gopds-api/internal/safeio"
	"gopds-api/logging"

	"github.com/google/uuid"
)

type BookProcessor struct {
	filename string
	path     string
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

func NewBookProcessor(filename, path string) *BookProcessor {
	return &BookProcessor{
		filename: filename,
		path:     path,
	}
}

func closeResource(rc io.Closer) {
	if err := rc.Close(); err != nil {
		logging.Errorf("failed to close resource: %v", err)
	}
}

func closeTmpFile(file *os.File) {
	if err := file.Close(); err != nil {
		logging.Errorf("failed to close tmp file: %v", err)
	}
}

// removeTmpFile deletes a temporary file, saying so when it cannot.
//
// A failure here leaks a file into the working directory rather than breaking
// the request, so it is not worth returning — but it is worth knowing about,
// because it accumulates.
func removeTmpFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logging.Errorf("failed to delete tmp file %s: %v", path, err)
	}
}

// process finds the book inside its archive and hands back its bytes.
//
// It used to carry a second mode that wrote the entry to a temporary file and
// ran an external converter over it. Nothing reached it: the only caller is
// FB2, which asks for no conversion, while Epub and Mobi go through
// extractFB2 and convert in-process. Had anything reached it, the command it
// assembled began with the book's own filename — it would have tried to
// execute the book.
func (bp *BookProcessor) process() (io.ReadCloser, error) {
	r, err := zip.OpenReader(bp.path)
	if err != nil {
		return nil, err
	}
	defer closeResource(r)

	for _, f := range r.File {
		if f.Name != bp.filename {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return nil, err
		}

		// Closed here rather than deferred: a defer inside the loop would
		// only run when the whole function returns, and the entry is read in
		// full before that happens anyway.
		content, err := bp.readWithoutConversion(rc)
		closeResource(rc)
		return content, err
	}
	return nil, errors.New("book not found")
}

func (bp *BookProcessor) readWithoutConversion(rc io.ReadCloser) (io.ReadCloser, error) {
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(rc); err != nil {
		return nil, errors.New("failed to read book")
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

// Epub generates an EPUB file from the FB2 book.
// Returns an io.ReadCloser containing the complete EPUB archive.
func (bp *BookProcessor) Epub() (io.ReadCloser, error) {
	fb2Content, err := bp.extractFB2()
	if err != nil {
		logging.Errorf("Failed to extract FB2 from archive %s: %v", bp.path, err)
		return nil, fmt.Errorf("failed to extract FB2: %w", err)
	}

	// Parse FB2 in one pass (both metadata and body structure)
	// This is ~30-40% faster than parsing metadata and body separately.
	//
	// ctx is context.Background() because the EPUB-download handler does not
	// flow a request context through BookProcessor yet. When the API grows a
	// per-download cancellation knob, that ctx belongs here — until then the
	// download is atomic, and a closed client pays only for the work done so
	// far (the response is streamed after this returns).
	doc, bookFile, err := converter.ParseFB2Complete(context.Background(), fb2Content, true)
	if err != nil {
		logging.Errorf("Failed to parse FB2 content for %s: %v", bp.filename, err)
		return nil, fmt.Errorf("failed to parse FB2 content: %w", err)
	}

	// Generate EPUB archive
	generator := converter.NewEPUBGenerator()
	epubReader, err := generator.GenerateEPUB(doc, bookFile)
	if err != nil {
		logging.Errorf("Failed to generate EPUB for %s: %v", bp.filename, err)
		return nil, fmt.Errorf("failed to generate EPUB: %w", err)
	}

	return epubReader, nil
}

// Mobi generates a MOBI file from the FB2 book using the conversion chain:
// FB2 → EPUB → MOBI (using kindlegen)
// Returns an io.ReadCloser containing the MOBI file.
func (bp *BookProcessor) Mobi() (io.ReadCloser, error) {
	// Step 1: Generate EPUB from FB2
	epubReader, err := bp.Epub()
	if err != nil {
		logging.Errorf("Failed to generate EPUB for MOBI conversion %s: %v", bp.filename, err)
		return nil, fmt.Errorf("failed to generate EPUB: %w", err)
	}
	defer epubReader.Close()

	// Step 2: Save EPUB to temporary file
	tmpFilename := uuid.New().String()
	epubTmpFile := tmpFilename + ".epub"
	mobiTmpFile := tmpFilename + ".mobi"

	// #nosec G304 -- a scratch file named after a freshly generated UUID,
	// in the working directory. Nothing outside chooses the name.
	epubFile, err := os.Create(epubTmpFile)
	if err != nil {
		logging.Errorf("Failed to create temp EPUB file for %s: %v", bp.filename, err)
		return nil, fmt.Errorf("failed to create temp EPUB file: %w", err)
	}

	// Copy EPUB content to temp file
	if _, err = io.Copy(epubFile, epubReader); err != nil {
		closeTmpFile(epubFile)
		removeTmpFile(epubTmpFile)
		logging.Errorf("Failed to write EPUB to temp file for %s: %v", bp.filename, err)
		return nil, fmt.Errorf("failed to write EPUB: %w", err)
	}
	closeTmpFile(epubFile)

	// Schedule cleanup of temp files
	defer func() {
		removeTmpFile(epubTmpFile)
		removeTmpFile(mobiTmpFile)
	}()

	// Step 3: Convert EPUB to MOBI using kindlegen
	// Try to find kindlegen in common locations
	kindlegenPath := findKindlegen()
	if kindlegenPath == "" {
		logging.Errorf("kindlegen binary not found for %s", bp.filename)
		return nil, fmt.Errorf("kindlegen binary not found")
	}

	// #nosec G204 -- kindlegenPath comes from findKindlegen, which only
	// ever returns one of a fixed list of locations or a PATH lookup. The
	// arguments are generated temporary filenames.
	cmd := exec.Command(kindlegenPath, epubTmpFile, "-o", tmpFilename+".mobi")
	if err := cmd.Run(); err != nil {
		// kindlegen returns exit code 1 even on successful conversion with warnings
		// Check if MOBI file was actually created
		if _, statErr := os.Stat(mobiTmpFile); os.IsNotExist(statErr) {
			logging.Errorf("kindlegen failed to convert EPUB to MOBI for %s: %v", bp.filename, err)
			return nil, fmt.Errorf("kindlegen conversion failed: %w", err)
		}
		// MOBI was created despite non-zero exit code (warnings)
		logging.Infof("kindlegen completed with warnings for %s", bp.filename)
	}

	// Step 4: Read the generated MOBI file
	// #nosec G304 -- the file kindlegen was just told to write, named
	// after the same generated UUID.
	mobiFile, err := os.Open(mobiTmpFile)
	if err != nil {
		logging.Errorf("Failed to open generated MOBI file for %s: %v", bp.filename, err)
		return nil, fmt.Errorf("failed to open MOBI file: %w", err)
	}

	logging.Infof("Successfully converted %s to MOBI via EPUB", bp.filename)
	return mobiFile, nil
}

func (bp *BookProcessor) FB2() (io.ReadCloser, error) {
	return bp.process()
}

func (bp *BookProcessor) Zip(df string) (io.ReadCloser, error) {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	r, err := zip.OpenReader(bp.path)
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name == bp.filename {
			zf, err := w.Create(df + ".fb2")
			if err != nil {
				return nil, err
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			_, err = safeio.Copy(zf, rc, safeio.MaxBookBytes)
			if err != nil {
				return nil, err
			}
			err = w.Close()
			if err != nil {
				return nil, err
			}
			zipAnswer := io.NopCloser(bytes.NewReader(buf.Bytes()))

			return zipAnswer, nil
		}
	}

	return nil, errors.New("book is not found")
}

func (bp *BookProcessor) extractFB2() ([]byte, error) {
	reader, err := zip.OpenReader(bp.path)
	if err != nil {
		return nil, err
	}
	defer closeResource(reader)

	for _, file := range reader.File {
		if file.Name != bp.filename {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(file.Name), ".fb2") {
			return nil, fmt.Errorf("file is not fb2: %s", file.Name)
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer closeResource(rc)
		return safeio.ReadAll(rc, safeio.MaxBookBytes)
	}
	return nil, errors.New("book not found")
}

// findKindlegen searches for kindlegen binary in common locations.
// Returns the path to kindlegen or empty string if not found.
func findKindlegen() string {
	// Common locations to check (in order of priority)
	locations := []string{
		"kindlegen/kindlegen",      // Relative to project root
		"./kindlegen/kindlegen",    // Explicit relative path
		"../kindlegen/kindlegen",   // One level up (for tests)
		"/usr/local/bin/kindlegen", // System-wide install
		"/usr/bin/kindlegen",       // System-wide install
	}

	for _, path := range locations {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Try finding in PATH
	if path, err := exec.LookPath("kindlegen"); err == nil {
		return path
	}

	return ""
}
