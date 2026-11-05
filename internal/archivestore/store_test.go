package archivestore

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/objectstore"
	"gopds-api/internal/objectstore/s3test"
)

const (
	testPrefix  = "archives/"
	testArchive = "fb2-000001-999999.zip"
	testTimeout = 300 * time.Millisecond
	// bigEntries × bigEntrySize makes an archive of about 60 MiB: a
	// miniature of the real ones, with their number of entries.
	bigEntries   = 3000
	bigEntrySize = 20 << 10
)

func bookName(i int) string { return fmt.Sprintf("%06d.fb2", i) }

// buildArchive makes a zip of incompressible books, so that its size in bytes
// means what it would mean for a real archive.
func buildArchive(t *testing.T, entries, entrySize int, seed uint64) []byte {
	t.Helper()
	random := rand.New(rand.NewPCG(seed, seed))
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	body := make([]byte, entrySize)
	for i := range entries {
		for j := range body {
			body[j] = byte(random.Uint32())
		}
		// Deflated, as the real archives are, even though it gains nothing.
		f, err := w.Create(bookName(i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type fixture struct {
	server *s3test.Server
	store  *Store
	root   string
	path   string
	data   []byte
}

func newFixture(t *testing.T, cfg *Config, data []byte) *fixture {
	t.Helper()
	server := s3test.New(t)
	server.Put(testPrefix+testArchive, data)
	root := t.TempDir() + "/"
	cfg.Root = root
	cfg.Prefix = testPrefix
	cfg.Client = server.Client(testTimeout, 1)
	cfg.CacheDir = t.TempDir()
	store, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{server: server, store: store, root: root, path: root + testArchive, data: data}
}

// readBook reads one book through the store.
func readBook(t *testing.T, s *Store, path, name string) []byte {
	t.Helper()
	data, err := tryBook(context.Background(), s, path, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func tryBook(ctx context.Context, s *Store, path, name string) ([]byte, error) {
	entry, err := s.Find(ctx, path, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = entry.Close() }()
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func wantBook(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOneBookReadsASmallFractionOfTheArchive(t *testing.T) {
	data := buildArchive(t, bigEntries, bigEntrySize, 1)
	fx := newFixture(t, &Config{}, data)

	got := readBook(t, fx.store, fx.path, bookName(1500))

	if !bytes.Equal(got, wantBook(t, data, bookName(1500))) {
		t.Fatal("the book came back different")
	}
	read := fx.server.Bytes()
	t.Logf("archive %d bytes, %d entries: %d requests, %d bytes (%.3f%%)",
		len(data), bigEntries, fx.server.Requests(), read, 100*float64(read)/float64(len(data)))
	if fx.server.Requests() != 3 {
		t.Errorf("first book made %d requests, want 3 (HEAD, directory, book): %+v", fx.server.Requests(), fx.server.Reads())
	}
	// One tail chunk and one book. Against a real archive of several
	// gigabytes the same bytes are a hundredth of a percent.
	if limit := tailChunk + bigEntrySize + 2*localHeaderSlack; read > int64(limit) {
		t.Errorf("read %d bytes, want at most %d", read, limit)
	}
}

func TestSecondBookSkipsTheCentralDirectory(t *testing.T) {
	data := buildArchive(t, bigEntries, bigEntrySize, 2)
	fx := newFixture(t, &Config{}, data)
	readBook(t, fx.store, fx.path, bookName(10))
	first := fx.server.Reads()
	directoryOffset := first[1].Off
	fx.server.Reset()

	got := readBook(t, fx.store, fx.path, bookName(2500))

	if !bytes.Equal(got, wantBook(t, data, bookName(2500))) {
		t.Fatal("the book came back different")
	}
	reads := fx.server.Reads()
	t.Logf("second book: %d requests, %d bytes", len(reads), fx.server.Bytes())
	if len(reads) != 1 {
		t.Fatalf("second book made %d requests, want 1: %+v", len(reads), reads)
	}
	if reads[0].Off >= directoryOffset {
		t.Errorf("read at %d, inside the central directory at %d", reads[0].Off, directoryOffset)
	}
	if reads[0].Bytes > bigEntrySize+2*localHeaderSlack {
		t.Errorf("read %d bytes for a book of %d", reads[0].Bytes, bigEntrySize)
	}
}

func TestLocalFileWinsAndMakesNoRequest(t *testing.T) {
	remote := buildArchive(t, 3, 100, 3)
	local := buildArchive(t, 3, 100, 4)
	fx := newFixture(t, &Config{}, remote)
	if err := os.WriteFile(fx.path, local, 0o600); err != nil {
		t.Fatal(err)
	}

	got := readBook(t, fx.store, fx.path, bookName(1))

	if !bytes.Equal(got, wantBook(t, local, bookName(1))) {
		t.Fatal("the remote copy won over the local file")
	}
	if err := fx.store.Check(context.Background(), fx.path); err != nil {
		t.Fatal(err)
	}
	whole, err := fx.store.OpenWhole(context.Background(), fx.path)
	if err != nil {
		t.Fatal(err)
	}
	_ = whole.Close()
	if n := fx.server.Requests(); n != 0 {
		t.Errorf("local file made %d requests", n)
	}
}

func TestBrokenLocalFileIsNotPapered(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 2, 100, 5))
	if err := os.WriteFile(fx.path, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := tryBook(context.Background(), fx.store, fx.path, bookName(0))
	if !errors.Is(err, zip.ErrFormat) {
		t.Fatalf("err = %v, want the local file's own failure", err)
	}
	if n := fx.server.Requests(); n != 0 {
		t.Errorf("made %d requests over a file that is on disk", n)
	}
}

func TestChangedObjectIsRereadByETag(t *testing.T) {
	before := buildArchive(t, 20, 1000, 6)
	after := buildArchive(t, 20, 1000, 7)
	if len(before) != len(after) {
		t.Skip("the two archives must be the same size for this case")
	}
	fx := newFixture(t, &Config{}, before)
	readBook(t, fx.store, fx.path, bookName(3))

	fx.server.Put(testPrefix+testArchive, after)
	got := readBook(t, fx.store, fx.path, bookName(4))

	if !bytes.Equal(got, wantBook(t, after, bookName(4))) {
		t.Fatal("served bytes of the old archive")
	}
}

func TestChangedObjectIsRereadBySize(t *testing.T) {
	before := buildArchive(t, 20, 1000, 8)
	after := buildArchive(t, 40, 1000, 9)
	fx := newFixture(t, &Config{}, before)
	readBook(t, fx.store, fx.path, bookName(3))

	fx.server.Put(testPrefix+testArchive, after)
	// Only the new archive has this book, and nothing in a cached directory
	// says so: no ranged read happens that could notice the change.
	got := readBook(t, fx.store, fx.path, bookName(30))
	if !bytes.Equal(got, wantBook(t, after, bookName(30))) {
		t.Fatal("the book came back different")
	}
}

func TestChangedSizeWithTheSameETagIsCaught(t *testing.T) {
	fx := newFixture(t, &Config{BookCacheBytes: 0}, buildArchive(t, 20, 1000, 10))
	ctx := context.Background()
	key := testPrefix + testArchive
	dir, err := fx.store.directory(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	stale := *dir
	stale.info.Size++
	stale.tailOffset++
	reader := newRangedReader(ctx, fx.store.client, key, &stale)
	defer func() { _ = reader.Close() }()
	if _, err := reader.ReadAt(make([]byte, 10), 0); !errors.Is(err, objectstore.ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
}

func TestDirectoryIsRevalidatedAfterItsWindow(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 5, 1000, 11))
	clock := time.Now()
	fx.store.now = func() time.Time { return clock }
	readBook(t, fx.store, fx.path, bookName(1))
	fx.server.Reset()

	clock = clock.Add(directoryRevalidate + time.Second)
	readBook(t, fx.store, fx.path, bookName(2))

	reads := fx.server.Reads()
	if len(reads) != 2 || reads[0].Method != "HEAD" {
		t.Fatalf("want HEAD then the book, got %+v", reads)
	}
}

func TestOutageErrorsWithinTheTimeout(t *testing.T) {
	for name, breakIt := range map[string]func(*s3test.Server){
		"refuses": func(s *s3test.Server) { s.SetDown(true) },
		"hangs":   func(s *s3test.Server) { s.SetHang(true) },
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t, &Config{}, buildArchive(t, 5, 1000, 12))
			breakIt(fx.server)
			defer fx.server.SetHang(false)

			started := time.Now()
			err := fx.store.Check(context.Background(), fx.path)
			elapsed := time.Since(started)

			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
			// Two attempts and one backoff, with room for a slow machine.
			if limit := 3*testTimeout + time.Second; elapsed > limit {
				t.Errorf("took %s, want under %s", elapsed, limit)
			}
		})
	}
}

func TestOutageAfterTheDirectoryIsCachedFailsTheBookOnly(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 5, 1000, 13))
	readBook(t, fx.store, fx.path, bookName(0))
	fx.server.SetDown(true)

	_, err := tryBook(context.Background(), fx.store, fx.path, bookName(1))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	fx.server.SetDown(false)
	readBook(t, fx.store, fx.path, bookName(1))
}

func TestBrokenStreamResumesWhereItStopped(t *testing.T) {
	data := buildArchive(t, 10, 50<<10, 14)
	fx := newFixture(t, &Config{}, data)
	if err := fx.store.Check(context.Background(), fx.path); err != nil {
		t.Fatal(err)
	}
	fx.server.DropAfter(10 << 10)
	fx.server.Reset()
	entry, err := fx.store.Find(context.Background(), fx.path, bookName(4))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = entry.Close() }()
	rc, err := entry.Open()
	if err == nil {
		fx.server.DropAfter(0)
		var got []byte
		got, err = io.ReadAll(rc)
		_ = rc.Close()
		if err == nil && !bytes.Equal(got, wantBook(t, data, bookName(4))) {
			t.Fatal("a resumed book came back different")
		}
	}
	if err != nil {
		t.Fatalf("a dropped connection was not resumed: %v", err)
	}
}

func TestStoreIgnoringRangeIsRefused(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 5, 1000, 15))
	fx.server.SetIgnoreRange(true)
	err := fx.store.Check(context.Background(), fx.path)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestMissingEverywhereIsNotFound(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 1, 10, 16))
	missing := fx.root + "other.zip"
	err := fx.store.Check(context.Background(), missing)
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotFound and fs.ErrNotExist", err)
	}
	if _, err := fx.store.OpenWhole(context.Background(), missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OpenWhole: %v", err)
	}
}

func TestEntryNotInArchive(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 1, 10, 17))
	if _, err := fx.store.Find(context.Background(), fx.path, "absent.fb2"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPathOutsideTheRootStaysLocal(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 1, 10, 18))
	outside := filepath.Join(t.TempDir(), testArchive)
	if err := fx.store.Check(context.Background(), outside); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	escaping := fx.root + "../" + testArchive
	if err := fx.store.Check(context.Background(), escaping); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if n := fx.server.Requests(); n != 0 {
		t.Errorf("made %d requests for a path outside the root", n)
	}
}

func TestDefaultStoreIsTheDiskAlone(t *testing.T) {
	s := Default()
	if s.Remote() {
		t.Fatal("the default store has a remote")
	}
	missing := filepath.Join(t.TempDir(), "absent.zip")
	_, want := zip.OpenReader(missing)
	_, got := s.OpenWhole(context.Background(), missing)
	if got == nil || got.Error() != want.Error() {
		t.Fatalf("OpenWhole err = %v, want zip.OpenReader's own %v", got, want)
	}
	if _, got = s.Find(context.Background(), missing, "x"); got == nil || got.Error() != want.Error() {
		t.Fatalf("Find err = %v, want %v", got, want)
	}
	if err := s.Check(context.Background(), missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Check err = %v", err)
	}
	if names, err := s.List(context.Background()); names != nil || err != nil {
		t.Fatalf("List = %v, %v", names, err)
	}
}

func TestList(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 1, 10, 19))
	fx.server.Put(testPrefix+"b.zip", []byte("x"))
	fx.server.Put(testPrefix+"c.zip", []byte("x"))
	fx.server.Put(testPrefix+"readme.txt", []byte("x"))
	fx.server.Put("covers/a.jpg", []byte("x"))

	names, err := fx.store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprint([]string{"b.zip", "c.zip", testArchive})
	if fmt.Sprint(names) != want {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

// TestZip64Archive crosses both limits of a first read at once: over 65,535
// entries is a zip64 end record, and a directory larger than one tail chunk
// makes the loader grow backwards.
func TestZip64Archive(t *testing.T) {
	const entries = 70_000
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for i := range entries {
		f, err := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%0100d", i), Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	fx := newFixture(t, &Config{}, out.Bytes())

	got := readBook(t, fx.store, fx.path, fmt.Sprintf("%0100d", entries-1))

	if want := byte(entries % 256); len(got) != 1 || got[0] != want-1 {
		t.Fatalf("got %v, want one byte of %d", got, want-1)
	}
}

func TestDirectoryCacheIsBounded(t *testing.T) {
	cache := newDirectoryCache(100)
	for i := range 5 {
		cache.put(fmt.Sprint(i), &directory{tail: make([]byte, 40)})
	}
	if cache.total > 100 {
		t.Fatalf("holds %d bytes over a limit of 100", cache.total)
	}
	if cache.get("0") != nil || cache.get("4") == nil {
		t.Fatal("did not keep the most recent")
	}
	cache.put("big", &directory{tail: make([]byte, 101)})
	if cache.get("big") != nil {
		t.Fatal("kept an entry over the whole limit")
	}
}

func TestConcurrentFirstOpensShareOneLoad(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 200, 1000, 20))
	var failed atomic.Int32
	done := make(chan struct{})
	for i := range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := tryBook(context.Background(), fx.store, fx.path, bookName(i)); err != nil {
				failed.Add(1)
			}
		}()
	}
	for range 8 {
		<-done
	}
	if failed.Load() != 0 {
		t.Fatalf("%d reads failed", failed.Load())
	}
	heads := 0
	for _, read := range fx.server.Reads() {
		if read.Method == "HEAD" {
			heads++
		}
	}
	if heads > 2 {
		t.Errorf("%d HEADs for one archive", heads)
	}
}

func TestSizedReadsGrowOverALongRead(t *testing.T) {
	// Without a hint, as a worker reading through *zip.File does: small reads
	// must not become a request each.
	fx := newFixture(t, &Config{}, buildArchive(t, 200, 20<<10, 21))
	archive, err := fx.store.OpenArchive(context.Background(), fx.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	fx.server.Reset()
	for _, f := range archive.File[:100] {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	if n := fx.server.Requests(); n > 6 {
		t.Errorf("100 adjacent entries of 2 MB made %d requests", n)
	}
}

func TestCanceledContextStopsARead(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 5, 1000, 22))
	if err := fx.store.Check(context.Background(), fx.path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tryBook(ctx, fx.store, fx.path, bookName(1)); err == nil {
		t.Fatal("a canceled read succeeded")
	}
}
