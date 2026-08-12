package services

// preview_adapters_test.go pins the two production adapters to their
// contracts. The repo test stubs the package-level getBookByID indirection
// (the same pattern parseForGates uses for the parser) because the database
// package exposes a free function over a package-global connection — there
// is nothing else to inject. The loader tests run against real zip files in
// a temp dir: the adapter is a filesystem wrapper, and a fake filesystem
// would only test itself.

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-pg/pg/v10"

	"gopds-api/models"
)

// stubGetBook replaces the database indirection for the duration of a test.
func stubGetBook(t *testing.T, fn func(int64) (models.Book, error)) {
	t.Helper()
	original := getBookByID
	getBookByID = fn
	t.Cleanup(func() { getBookByID = original })
}

// The ORM answers a missing row with pg.ErrNoRows; the BookRepo contract
// answers it with (nil, nil). Conflating the two would make the service
// report "database broken" for what is simply "no such book".
func TestCatalogBookRepo_NotFoundBecomesNilNotError(t *testing.T) {
	stubGetBook(t, func(int64) (models.Book, error) { return models.Book{}, pg.ErrNoRows })

	book, err := NewCatalogBookRepo().GetBook(42)
	if err != nil {
		t.Fatalf("err = %v, want nil — pg.ErrNoRows must be translated, not propagated", err)
	}
	if book != nil {
		t.Fatalf("book = %+v, want nil for a missing row", book)
	}
}

func TestCatalogBookRepo_FoundBookIsReturned(t *testing.T) {
	stubGetBook(t, func(id int64) (models.Book, error) {
		return models.Book{ID: id, Title: "found"}, nil
	})

	book, err := NewCatalogBookRepo().GetBook(7)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if book == nil || book.ID != 7 || book.Title != "found" {
		t.Fatalf("book = %+v, want the row the catalog returned", book)
	}
}

// A broken database is not a missing book: swallowing a real error into
// (nil, nil) would make the service answer "not found" during an outage.
func TestCatalogBookRepo_RealErrorsPropagate(t *testing.T) {
	boom := errors.New("connection refused")
	stubGetBook(t, func(int64) (models.Book, error) { return models.Book{}, boom })

	book, err := NewCatalogBookRepo().GetBook(42)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the database error to propagate", err)
	}
	if book != nil {
		t.Fatalf("book = %+v, want nil on error", book)
	}
}

// writeZipArchive drops a real zip with the given entries under path,
// creating parent directories.
func writeZipArchive(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w := zip.NewWriter(f)
	for name, body := range entries {
		zw, err := w.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := zw.Write([]byte(body)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

// The adapter resolves the catalog-relative archive path against the files
// root and reads the entry through utils.BookProcessor — the same code path
// the download handler uses. The archive sits in a SUBDIRECTORY of the root
// so that a mutation dropping the filesPath prefix cannot accidentally find
// it in the working directory.
func TestZipArchiveLoader_ReadsEntryUnderFilesRoot(t *testing.T) {
	root := t.TempDir()
	writeZipArchive(t, filepath.Join(root, "sub", "archive.zip"), map[string]string{"book.fb2": minimalFB2})

	loader := NewZipArchiveLoader(root)
	data, err := loader.Load(context.Background(), "sub/archive.zip", "book.fb2")
	if err != nil {
		t.Fatalf("Load() = %v, want the entry bytes", err)
	}
	if string(data) != minimalFB2 {
		t.Fatalf("payload mismatch: got %d bytes, want the %d bytes of the fixture", len(data), len(minimalFB2))
	}
}

// An archive that opened but lacks the requested entry is the typed
// ErrArchiveFileNotFound — the phase-4 handler maps it to its own status,
// distinct from "no such book" and "archive unreadable".
func TestZipArchiveLoader_MissingEntryIsTyped(t *testing.T) {
	root := t.TempDir()
	writeZipArchive(t, filepath.Join(root, "archive.zip"), map[string]string{"other.fb2": minimalFB2})

	loader := NewZipArchiveLoader(root)
	_, err := loader.Load(context.Background(), "archive.zip", "book.fb2")
	if !errors.Is(err, ErrArchiveFileNotFound) {
		t.Fatalf("err = %v, want ErrArchiveFileNotFound", err)
	}
}

// A missing archive FILE is an operational error (disk, permissions, a
// catalog row pointing nowhere) and must not masquerade as "entry not
// found" — the two get different answers upstream.
func TestZipArchiveLoader_MissingArchiveIsNotTypedAsMissingEntry(t *testing.T) {
	loader := NewZipArchiveLoader(t.TempDir())
	_, err := loader.Load(context.Background(), "no-such.zip", "book.fb2")
	if err == nil {
		t.Fatal("err = nil, want an error for a missing archive file")
	}
	if errors.Is(err, ErrArchiveFileNotFound) {
		t.Fatalf("err = %v must not match ErrArchiveFileNotFound — the archive itself is missing", err)
	}
}
