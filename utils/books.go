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

	"gopds-api/internal/archivestore"
	"gopds-api/internal/converter"
	"gopds-api/internal/safeio"
	"gopds-api/logging"

	"github.com/google/uuid"
)

type BookProcessor struct {
	filename string
	path     string
	// read is the context of a request this book is read for, if there is one.
	read context.Context
}

// ErrBookNotInArchive reports that the archive opened fine but holds no
// entry with the requested name. Typed so callers that distinguish "no such
// entry" from "archive unreadable" (the preview pipeline maps them to
// different answers) can match on it instead of on a message string.
var ErrBookNotInArchive = errors.New("book not found")

func NewBookProcessor(filename, path string) *BookProcessor {
	return &BookProcessor{
		filename: filename,
		path:     path,
	}
}

// WithReadContext makes reading the book out of its archive follow ctx, so that
// a request that ends, or a read that runs out of its budget, stops the read.
// Only the read: a conversion keeps the context it was given, or none.
func (bp *BookProcessor) WithReadContext(ctx context.Context) *BookProcessor {
	bp.read = ctx
	return bp
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
	entry, rc, err := bp.open(context.Background())
	if err != nil {
		return nil, err
	}
	defer closeResource(entry)
	// Closed here rather than deferred with the entry, so that the order is
	// plain: the stream ends before the archive under it does.
	content, err := bp.readWithoutConversion(rc)
	closeResource(rc)
	return content, err
}

// open finds the book in its archive and opens it. Close the stream, then the
// entry. A book the archive does not have is ErrBookNotInArchive.
func (bp *BookProcessor) open(ctx context.Context) (*archivestore.Entry, io.ReadCloser, error) {
	entry, err := bp.find(ctx)
	if err != nil {
		return nil, nil, err
	}
	rc, err := entry.Open()
	if err != nil {
		closeResource(entry)
		return nil, nil, err
	}
	return entry, rc, nil
}

// find locates the book through the archive store: the file on disk when
// there is one, object storage otherwise.
func (bp *BookProcessor) find(ctx context.Context) (*archivestore.Entry, error) {
	if bp.read != nil {
		ctx = bp.read
	}
	entry, err := archivestore.Default().Find(ctx, bp.path, bp.filename)
	if errors.Is(err, archivestore.ErrEntryNotFound) {
		return nil, ErrBookNotInArchive
	}
	return entry, err
}

func (bp *BookProcessor) readWithoutConversion(rc io.ReadCloser) (io.ReadCloser, error) {
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(rc); err != nil {
		// A store that went away is worth naming: the caller answers it
		// differently from a broken archive.
		if errors.Is(err, archivestore.ErrUnavailable) {
			return nil, err
		}
		return nil, errors.New("failed to read book")
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

// Epub generates an EPUB file from the FB2 book.
// Returns an io.ReadCloser containing the complete EPUB archive.
func (bp *BookProcessor) Epub() (io.ReadCloser, error) {
	return bp.EpubContext(context.Background())
}

// EpubContext is Epub with the parse bound to ctx: a canceled or expired
// context stops it.
func (bp *BookProcessor) EpubContext(ctx context.Context) (io.ReadCloser, error) {
	fb2Content, err := bp.extractFB2(ctx)
	if err != nil {
		logging.Errorf("Failed to extract FB2 from archive %s: %v", bp.path, err)
		return nil, fmt.Errorf("failed to extract FB2: %w", err)
	}

	// Parse FB2 in one pass (both metadata and body structure)
	// This is ~30-40% faster than parsing metadata and body separately.
	//
	// The site's download handlers pass context.Background() here: only the
	// read above follows their request, so a conversion is atomic and a closed
	// client pays only for the work done so far.
	doc, bookFile, err := converter.ParseFB2Complete(ctx, fb2Content, true)
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
	return bp.MobiContext(context.Background())
}

// MobiContext is Mobi with kindlegen bound to ctx: a canceled or expired
// context kills the subprocess, so a hung conversion cannot hold its caller
// forever.
func (bp *BookProcessor) MobiContext(ctx context.Context) (io.ReadCloser, error) {
	// Step 1: Generate EPUB from FB2
	epubReader, err := bp.EpubContext(ctx)
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
	cmd := exec.CommandContext(ctx, kindlegenPath, epubTmpFile, "-o", tmpFilename+".mobi")
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("kindlegen stopped: %w", ctx.Err())
		}
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

// FB2Limited is the FB2 book for a caller with a size limit. The entry is
// read through the limit, so an oversized book costs at most about limit
// bytes of memory and fails with safeio.ErrTooLarge.
func (bp *BookProcessor) FB2Limited(limit int64) ([]byte, error) {
	entry, rc, err := bp.open(context.Background())
	if err != nil {
		return nil, err
	}
	defer closeResource(entry)
	defer closeResource(rc)
	return safeio.ReadAll(rc, limit)
}

// ZipLimited is Zip for a caller with a size limit on the archive it gets:
// the archive is written into a buffer that refuses to grow past limit, and
// fails with safeio.ErrTooLarge.
func (bp *BookProcessor) ZipLimited(df string, limit int64) ([]byte, error) {
	entry, rc, err := bp.open(context.Background())
	if err != nil {
		return nil, err
	}
	defer closeResource(entry)
	defer closeResource(rc)
	out := &cappedBuffer{limit: limit}
	w := zip.NewWriter(out)
	zf, err := w.Create(df + ".fb2")
	if err != nil {
		return nil, err
	}
	if _, err := safeio.Copy(zf, rc, safeio.MaxBookBytes); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.buf.Bytes(), nil
}

// cappedBuffer is a buffer that fails a write which would take it past limit.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int64
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if int64(c.buf.Len())+int64(len(p)) > c.limit {
		return 0, fmt.Errorf("%w of %d bytes", safeio.ErrTooLarge, c.limit)
	}
	return c.buf.Write(p)
}

func (bp *BookProcessor) Zip(df string) (io.ReadCloser, error) {
	entry, rc, err := bp.open(context.Background())
	if errors.Is(err, ErrBookNotInArchive) {
		return nil, errors.New("book is not found")
	}
	if err != nil {
		return nil, err
	}
	defer closeResource(entry)
	defer closeResource(rc)

	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	zf, err := w.Create(df + ".fb2")
	if err != nil {
		return nil, err
	}
	if _, err := safeio.Copy(zf, rc, safeio.MaxBookBytes); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

func (bp *BookProcessor) extractFB2(ctx context.Context) ([]byte, error) {
	entry, err := bp.find(ctx)
	if err != nil {
		return nil, err
	}
	defer closeResource(entry)
	if !strings.HasSuffix(strings.ToLower(entry.Name()), ".fb2") {
		return nil, fmt.Errorf("file is not fb2: %s", entry.Name())
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer closeResource(rc)
	return safeio.ReadAll(rc, safeio.MaxBookBytes)
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
