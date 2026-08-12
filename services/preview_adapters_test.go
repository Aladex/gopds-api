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
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
// root and reads the named entry straight from the zip. The archive sits in
// a SUBDIRECTORY of the root so that a mutation dropping the filesPath
// prefix cannot accidentally find it in the working directory.
func TestZipArchiveLoader_ReadsEntryUnderFilesRoot(t *testing.T) {
	root := t.TempDir()
	writeZipArchive(t, filepath.Join(root, "sub", "archive.zip"), map[string]string{"book.fb2": minimalFB2})

	loader := NewZipArchiveLoader(root)
	data, err := loader.Load(context.Background(), "sub/archive.zip", "book.fb2", 1<<20)
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
	_, err := loader.Load(context.Background(), "archive.zip", "book.fb2", 1<<20)
	if !errors.Is(err, ErrArchiveFileNotFound) {
		t.Fatalf("err = %v, want ErrArchiveFileNotFound", err)
	}
}

// A missing archive FILE is an operational error (disk, permissions, a
// catalog row pointing nowhere) and must not masquerade as "entry not
// found" — the two get different answers upstream.
func TestZipArchiveLoader_MissingArchiveIsNotTypedAsMissingEntry(t *testing.T) {
	loader := NewZipArchiveLoader(t.TempDir())
	_, err := loader.Load(context.Background(), "no-such.zip", "book.fb2", 1<<20)
	if err == nil {
		t.Fatal("err = nil, want an error for a missing archive file")
	}
	if errors.Is(err, ErrArchiveFileNotFound) {
		t.Fatalf("err = %v must not match ErrArchiveFileNotFound — the archive itself is missing", err)
	}
}

// countingReader records how many bytes the code under test actually pulled
// from the source. The size gate exists to bound WORK, so the assertion is on
// this counter, not on the error text: a refusal that first drained the whole
// stream produces the same error and still defeats the gate.
type countingReader struct {
	src io.Reader
	n   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	r.n += int64(n)
	return n, err
}

// A payload over the cap must be refused after reading at most cap+1 bytes.
// Reading cap+1 (not cap) is what tells a book exactly at the cap from one
// over it; reading more is work the gate was created to prevent.
func TestReadBounded_RefusalReadsAtMostCapPlusOne(t *testing.T) {
	const limit = 1024
	src := &countingReader{src: strings.NewReader(strings.Repeat("x", 10*limit))}

	_, err := readBounded(src, limit)
	if !errors.Is(err, ErrFB2TooLarge) {
		t.Fatalf("err = %v, want ErrFB2TooLarge", err)
	}
	if src.n > limit+1 {
		t.Errorf("read %d bytes to refuse a %d-byte cap, want at most %d — "+
			"the gate protects memory, so the refusal must not allocate what it refuses", src.n, limit, limit+1)
	}
}

// A payload of exactly the cap must pass, whole. This is what kills the
// "LimitReader(rc, limit)" mutation: it cannot distinguish "at the cap" from
// "over the cap" and refuses both.
func TestReadBounded_ExactlyAtTheCapPasses(t *testing.T) {
	const limit = 1024
	payload := strings.Repeat("x", limit)
	src := &countingReader{src: strings.NewReader(payload)}

	data, err := readBounded(src, limit)
	if err != nil {
		t.Fatalf("err = %v, want nil for a payload of exactly the cap", err)
	}
	if string(data) != payload {
		t.Fatalf("got %d bytes, want the whole %d-byte payload", len(data), limit)
	}
	if src.n != limit {
		t.Errorf("read %d bytes for a %d-byte payload — the read must not stop early", src.n, limit)
	}
}

// patchDeclaredUncompressedSize rewrites the uncompressed-size field of the
// single central-directory entry, making the archive catalog lie about the
// entry's size. The entry data itself is untouched, so reading the entry to
// its end still succeeds — only the declared size changes.
func patchDeclaredUncompressedSize(t *testing.T, zipPath string, declared uint32) {
	t.Helper()
	raw, err := os.ReadFile(zipPath) // #nosec G304 -- a fixture the test itself wrote
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sig := []byte("PK\x01\x02")
	first := bytes.Index(raw, sig)
	if first < 0 || bytes.Contains(raw[first+len(sig):], sig) {
		t.Fatal("fixture expects a zip with exactly one central directory entry")
	}
	// Central directory header: 4-byte signature, then ten fields; the
	// uncompressed size is the 4 bytes at offset 24.
	binary.LittleEndian.PutUint32(raw[first+24:], declared)
	if err := os.WriteFile(zipPath, raw, 0o600); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
}

// The mirror image: the catalog declares a size UNDER the cap while the
// entry actually holds four times more. A loader that trusted the declared
// size and skipped the bounded read would try to swallow the whole entry.
//
// The refusal here surfaces as a zip format error, not as ErrFB2TooLarge,
// and that is worth understanding rather than papering over: Go's zip reader
// enforces the declared size while streaming (checksumReader fails the read
// once more bytes come out than the catalog promised), so the lie breaks
// the read before the cap+1 bound is even reached. The bounded read stays
// mandatory regardless — it is what makes "never pulls more than cap+1" a
// property of THIS code instead of a side effect of one library version.
// What the fixture pins is the outcome that matters: an entry hiding behind
// a lying catalog is never returned.
func TestZipArchiveLoader_LyingDeclaredSizeNeverYielded(t *testing.T) {
	const limit = 512
	payload := strings.Repeat("x", 4*limit) // actually over the cap
	root := t.TempDir()
	archive := filepath.Join(root, "lying.zip")
	writeZipArchive(t, archive, map[string]string{"book.fb2": payload})
	patchDeclaredUncompressedSize(t, archive, limit) // declares exactly the cap

	loader := NewZipArchiveLoader(root)
	data, err := loader.Load(context.Background(), "lying.zip", "book.fb2", limit)
	if err == nil {
		t.Fatal("err = nil, want a refusal — the entry is over the cap behind a lying declared size")
	}
	if len(data) != 0 {
		t.Fatalf("got %d bytes of an over-cap entry, want none", len(data))
	}
}

// The mirror image: the catalog declares a size OVER the cap for an entry
// that is actually small. The refusal must come from the catalog check alone,
// before any unpacking — proven by the control half, where the same content
// in an honest archive under the same cap loads fine.
func TestZipArchiveLoader_DeclaredOverTheCapRefusedBeforeUnpacking(t *testing.T) {
	limit := int64(len(minimalFB2))
	root := t.TempDir()

	lying := filepath.Join(root, "lying.zip")
	writeZipArchive(t, lying, map[string]string{"book.fb2": minimalFB2})
	patchDeclaredUncompressedSize(t, lying, uint32(limit+1))

	loader := NewZipArchiveLoader(root)
	_, err := loader.Load(context.Background(), "lying.zip", "book.fb2", limit)
	if !errors.Is(err, ErrFB2TooLarge) {
		t.Fatalf("err = %v, want ErrFB2TooLarge — the declared size alone exceeds the cap", err)
	}

	honest := filepath.Join(root, "honest.zip")
	writeZipArchive(t, honest, map[string]string{"book.fb2": minimalFB2})
	data, err := loader.Load(context.Background(), "honest.zip", "book.fb2", limit)
	if err != nil {
		t.Fatalf("honest archive at exactly the cap: err = %v, want the entry bytes — "+
			"the refusal above must come from the declared size, not from a broken loader", err)
	}
	if string(data) != minimalFB2 {
		t.Fatalf("payload mismatch: got %d bytes, want the %d of the fixture", len(data), len(minimalFB2))
	}
}

// A book of exactly the cap loads whole through the zip path. Together with
// the unit-level boundary test this pins the cap+1 convention end to end.
func TestZipArchiveLoader_EntryExactlyAtTheCapLoads(t *testing.T) {
	payload := strings.Repeat("x", 1024)
	root := t.TempDir()
	writeZipArchive(t, filepath.Join(root, "archive.zip"), map[string]string{"book.fb2": payload})

	loader := NewZipArchiveLoader(root)
	data, err := loader.Load(context.Background(), "archive.zip", "book.fb2", int64(len(payload)))
	if err != nil {
		t.Fatalf("err = %v, want the entry bytes for a book at exactly the cap", err)
	}
	if string(data) != payload {
		t.Fatalf("got %d bytes, want the whole %d-byte entry", len(data), len(payload))
	}
}

// The entry bytes may reach the pipeline ONLY through the bounded read. This
// cannot be pinned by watching any archive from outside — Go's zip reader
// enforces the declared size itself, so every over-cap outcome above looks
// the same with or without the bound. The seam stub counts what the live
// entry reader actually gives up: a Load that reads around the gate (the
// "trust the catalog, read unbounded" mutation) never calls the stub, and
// one that reads through it can never see more than cap+1 bytes.
func TestZipArchiveLoader_ReadsEntryThroughTheBoundedGate(t *testing.T) {
	const limit = 1024
	payload := strings.Repeat("x", limit)
	root := t.TempDir()
	writeZipArchive(t, filepath.Join(root, "archive.zip"), map[string]string{"book.fb2": payload})

	called := false
	var pulled int64
	var sawLimit int64
	prev := boundedRead
	boundedRead = func(rc io.Reader, max int64) ([]byte, error) {
		called = true
		sawLimit = max
		cr := &countingReader{src: rc}
		data, err := prev(cr, max)
		pulled = cr.n
		return data, err
	}
	defer func() { boundedRead = prev }()

	loader := NewZipArchiveLoader(root)
	if _, err := loader.Load(context.Background(), "archive.zip", "book.fb2", limit); err != nil {
		t.Fatalf("Load() = %v, want the entry bytes", err)
	}
	if !called {
		t.Fatal("the bounded read was never called — the entry was read around the gate")
	}
	if sawLimit != limit {
		t.Errorf("the read was bounded by %d, want the configured cap %d", sawLimit, limit)
	}
	if pulled > limit+1 {
		t.Errorf("the entry reader gave up %d bytes under a %d-byte cap, want at most %d", pulled, limit, limit+1)
	}
}
