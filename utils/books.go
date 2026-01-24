package utils

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"gopds-api/internal/converter"
	"gopds-api/internal/parser"
	"gopds-api/logging"
	"io"
	"os"
	"os/exec"
	"strings"

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

func deleteTmpFile(filename, format string) {
	if err := os.Remove(filename + ".fb2"); err != nil {
		logging.Errorf("failed to delete tmp file: %v", err)
	}
	if err := os.Remove(filename + format); err != nil {
		logging.Errorf("failed to delete converted file: %v", err)
	}
}

func (bp *BookProcessor) process(format string, cmdArgs []string, convert bool) (io.ReadCloser, error) {
	r, err := zip.OpenReader(bp.path)
	if err != nil {
		return nil, err
	}
	defer closeResource(r)

	for _, f := range r.File {
		if f.Name == bp.filename {
			return bp.processFile(f, format, cmdArgs, convert)
		}
	}
	return nil, errors.New("book not found")
}

func (bp *BookProcessor) processFile(f *zip.File, format string, cmdArgs []string, convert bool) (io.ReadCloser, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer closeResource(rc)

	if !convert {
		return bp.readWithoutConversion(rc)
	}

	tmpFilename := uuid.New().String()
	tmpFile, err := os.Create(tmpFilename + ".fb2")
	if err != nil {
		return nil, err
	}
	defer closeTmpFile(tmpFile)

	if _, err = io.Copy(tmpFile, rc); err != nil {
		return nil, err
	}

	defer deleteTmpFile(tmpFilename, format)

	cmdArgs = append(cmdArgs, tmpFilename+".fb2", ".")
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	convertedBook, err := os.Open(tmpFilename + format)
	if err != nil {
		return nil, err
	}

	return convertedBook, nil
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

	// Parse metadata (title, authors, language, cover, etc.)
	metadataParser := parser.NewFB2Parser(true)
	bookFile, err := metadataParser.Parse(bytes.NewReader(fb2Content))
	if err != nil {
		logging.Errorf("Failed to parse FB2 metadata for %s: %v", bp.filename, err)
		return nil, fmt.Errorf("failed to parse FB2 metadata: %w", err)
	}

	// Parse body structure (sections, paragraphs, formatting)
	doc, err := converter.ParseFB2Body(fb2Content)
	if err != nil {
		logging.Errorf("Failed to parse FB2 body for %s: %v", bp.filename, err)
		return nil, fmt.Errorf("failed to parse FB2 body: %w", err)
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

func (bp *BookProcessor) Mobi() (io.ReadCloser, error) {
	return bp.process(".mobi", []string{"external_fb2mobi/fb2c", "convert", "--to", "mobi"}, true)
}

func (bp *BookProcessor) FB2() (io.ReadCloser, error) {
	return bp.process("", nil, false)
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
			_, err = io.Copy(zf, rc)
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
		return io.ReadAll(rc)
	}
	return nil, errors.New("book not found")
}
