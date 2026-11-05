package utils

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"gopds-api/internal/archivestore"
	"gopds-api/internal/objectstore/s3test"
	"gopds-api/internal/safeio"
)

const (
	storageBook    = "book.fb2"
	storageArchive = "remote.zip"
	storageFB2     = `<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>Remote</book-title><lang>ru</lang></title-info></description>
<body><section><p>Text from object storage.</p></section></body>
</FictionBook>`
)

// remoteProcessor is a processor of a book that exists only in object storage.
func remoteProcessor(t *testing.T, name string) (*BookProcessor, *s3test.Server) {
	t.Helper()
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	f, err := w.Create(storageBook)
	if err != nil {
		t.Fatal(err)
	}
	if _, writeErr := f.Write([]byte(storageFB2)); writeErr != nil {
		t.Fatal(writeErr)
	}
	// Incompressible padding after the book: the end of an archive comes into
	// memory with its directory, and the book must not be part of that.
	padding, err := w.CreateHeader(&zip.FileHeader{Name: "padding", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, writeErr := padding.Write(make([]byte, 4096)); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := w.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	server := s3test.New(t)
	server.Put("archives/"+storageArchive, archive.Bytes())
	root := t.TempDir() + "/"
	store, err := archivestore.New(&archivestore.Config{
		Root: root, Client: server.Client(200*time.Millisecond, -1),
		Prefix: "archives/", CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	previous := archivestore.Default()
	archivestore.SetDefault(store)
	t.Cleanup(func() { archivestore.SetDefault(previous) })
	return NewBookProcessor(name, root+storageArchive), server
}

func TestBookFormatsFromObjectStorage(t *testing.T) {
	bp, _ := remoteProcessor(t, storageBook)

	fb2, err := bp.FB2()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(fb2)
	if err != nil || string(data) != storageFB2 {
		t.Fatalf("FB2 = %q, %v", data, err)
	}

	limited, err := bp.FB2Limited(1 << 20)
	if err != nil || string(limited) != storageFB2 {
		t.Fatalf("FB2Limited = %q, %v", limited, err)
	}

	if _, limitErr := bp.FB2Limited(5); !errors.Is(limitErr, safeio.ErrTooLarge) {
		t.Fatalf("FB2Limited under the size: %v", limitErr)
	}

	for name, read := range map[string]func() (io.ReadCloser, error){
		"zip":  func() (io.ReadCloser, error) { return bp.Zip("book") },
		"epub": bp.Epub,
	} {
		rc, readErr := read()
		if readErr != nil {
			t.Fatalf("%s: %v", name, readErr)
		}
		book, readErr := io.ReadAll(rc)
		if readErr != nil || len(book) == 0 {
			t.Fatalf("%s: %d bytes, %v", name, len(book), readErr)
		}
		_ = rc.Close()
	}

	packed, err := bp.ZipLimited("book", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zip.NewReader(bytes.NewReader(packed), int64(len(packed))); err != nil {
		t.Fatalf("ZipLimited: %v", err)
	}
}

func TestBookNotInARemoteArchive(t *testing.T) {
	bp, _ := remoteProcessor(t, "absent.fb2")
	if _, err := bp.FB2(); !errors.Is(err, ErrBookNotInArchive) {
		t.Fatalf("FB2: %v", err)
	}
	if _, err := bp.Zip("x"); err == nil || err.Error() != "book is not found" {
		t.Fatalf("Zip: %v", err)
	}
}

func TestOutageReachesTheCallerAsAnOutage(t *testing.T) {
	bp, server := remoteProcessor(t, storageBook)
	server.SetDown(true)

	for name, read := range map[string]func() (io.ReadCloser, error){
		"fb2":  bp.FB2,
		"epub": bp.Epub,
		"zip":  func() (io.ReadCloser, error) { return bp.Zip("book") },
	} {
		if _, err := read(); !errors.Is(err, archivestore.ErrUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOutageDuringTheBookIsAnOutageToo(t *testing.T) {
	bp, server := remoteProcessor(t, storageBook)
	if err := archivestore.Default().Check(context.Background(), bp.path); err != nil {
		t.Fatal(err)
	}
	server.SetDown(true)
	if _, err := bp.FB2(); !errors.Is(err, archivestore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalFailuresKeepTheirWords(t *testing.T) {
	missing := NewBookProcessor("x.fb2", t.TempDir()+"/absent.zip")
	_, err := missing.FB2()
	if err == nil {
		t.Fatal("no error")
	}
	if _, want := zip.OpenReader(missing.path); err.Error() != want.Error() {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestCallerLeavingStopsARemoteRead(t *testing.T) {
	reads := map[string]func(*BookProcessor) error{
		"fb2": func(bp *BookProcessor) error { _, err := bp.FB2(); return err },
		"zip": func(bp *BookProcessor) error { _, err := bp.Zip("name"); return err },
		"limited fb2": func(bp *BookProcessor) error {
			_, err := bp.FB2Limited(1 << 20)
			return err
		},
		"limited zip": func(bp *BookProcessor) error {
			_, err := bp.ZipLimited("name", 1<<20)
			return err
		},
		"epub": func(bp *BookProcessor) error { _, err := bp.Epub(); return err },
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			bp, server := remoteProcessor(t, storageBook)
			gone, leave := context.WithCancel(context.Background())
			leave()
			server.Reset()

			err := read(bp.WithReadContext(gone))

			if !errors.Is(err, archivestore.ErrUnavailable) {
				t.Fatalf("err = %v", err)
			}
			if n := server.Requests(); n != 0 {
				t.Fatalf("%d requests for a caller that left", n)
			}
		})
	}
}

func TestReadContextDoesNotReachTheConversion(t *testing.T) {
	bp, _ := remoteProcessor(t, storageBook)
	ctx := archivestore.Default().StartRead(context.Background())
	rc, err := bp.WithReadContext(ctx).Epub()
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
}
