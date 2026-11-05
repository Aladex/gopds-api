package archivestore

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const cacheLimit = 1 << 20

func TestBookCacheServesARepeatWithoutARequest(t *testing.T) {
	data := buildArchive(t, 10, 1000, 30)
	fx := newFixture(t, &Config{BookCacheBytes: cacheLimit}, data)
	first := readBook(t, fx.store, fx.path, bookName(2))
	fx.server.Reset()

	second := readBook(t, fx.store, fx.path, bookName(2))

	if !bytes.Equal(first, second) {
		t.Fatal("the cached book differs")
	}
	if n := fx.server.Requests(); n != 0 {
		t.Errorf("a cached book made %d requests", n)
	}
}

func TestBookCacheDisabled(t *testing.T) {
	fx := newFixture(t, &Config{BookCacheBytes: 0}, buildArchive(t, 10, 1000, 31))
	readBook(t, fx.store, fx.path, bookName(2))
	fx.server.Reset()
	readBook(t, fx.store, fx.path, bookName(2))
	if fx.server.Requests() == 0 {
		t.Fatal("something cached with the cache off")
	}
}

func TestLocalModeDoesNotUseTheBookCache(t *testing.T) {
	cacheDir := t.TempDir()
	data := buildArchive(t, 3, 100, 32)
	fx := newFixture(t, &Config{BookCacheBytes: cacheLimit}, data)
	fx.store.books.dir = cacheDir
	if err := os.WriteFile(fx.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	readBook(t, fx.store, fx.path, bookName(1))
	if entries, _ := os.ReadDir(cacheDir); len(entries) != 0 {
		t.Fatalf("a local read left %d cache entries", len(entries))
	}
}

func TestBookCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	cache, err := newBookCache(t.TempDir(), 300)
	if err != nil {
		t.Fatal(err)
	}
	put := func(key string) {
		rc := cache.tee(key, 70, io.NopCloser(strings.NewReader(strings.Repeat("x", 70))))
		if _, err := io.Copy(io.Discard, rc); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	a, b, c, d, e := testKey("a"), testKey("b"), testKey("c"), testKey("d"), testKey("e")
	put(a)
	put(b)
	put(c)
	put(d)
	if cache.open(a) == nil {
		t.Fatal("a fell out too early")
	}
	put(e)
	if cache.open(b) != nil {
		t.Error("b, the oldest, is still there")
	}
	if cache.open(a) == nil {
		t.Error("a was used recently and went anyway")
	}
	if cache.total > 300 {
		t.Errorf("holds %d bytes over a limit of 300", cache.total)
	}
}

func TestBookCacheKeepsNothingUnverified(t *testing.T) {
	cases := map[string]func(rc io.ReadCloser){
		"read partly": func(rc io.ReadCloser) { _, _ = io.CopyN(io.Discard, rc, 10) },
		"not read":    func(io.ReadCloser) {},
	}
	for name, read := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cache, err := newBookCache(dir, 1000)
			if err != nil {
				t.Fatal(err)
			}
			rc := cache.tee(testKey("partial"), 100, io.NopCloser(strings.NewReader(strings.Repeat("x", 100))))
			read(rc)
			_ = rc.Close()
			if cache.open(testKey("partial")) != nil {
				t.Fatal("kept a book that was not read to its end")
			}
			if files := cacheFiles(t, dir); len(files) != 0 {
				t.Fatalf("left %v behind", files)
			}
		})
	}
}

func TestBookCacheKeepsNoBookTooLargeForIt(t *testing.T) {
	cache, err := newBookCache(t.TempDir(), 400)
	if err != nil {
		t.Fatal(err)
	}
	source := io.NopCloser(strings.NewReader("x"))
	if rc := cache.tee(testKey("huge"), 101, source); rc != source {
		t.Fatal("tried to keep a book over a quarter of the cache")
	}
}

func TestBookCacheFailureLeavesTheReadAlone(t *testing.T) {
	dir := t.TempDir()
	cache, err := newBookCache(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	// A file where the entry's directory should go.
	key := testKey("blocked")
	if writeErr := os.WriteFile(filepath.Dir(cache.path(key)), nil, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	rc := cache.tee(key, 5, io.NopCloser(strings.NewReader("hello")))
	got, err := io.ReadAll(rc)
	if err != nil || string(got) != "hello" {
		t.Fatalf("read = %q, %v", got, err)
	}
}

func TestBookCacheSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first, err := newBookCache(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	key := testKey("kept")
	rc := first.tee(key, 5, io.NopCloser(strings.NewReader("hello")))
	if _, copyErr := io.Copy(io.Discard, rc); copyErr != nil {
		t.Fatal(copyErr)
	}
	_ = rc.Close()
	stray := strayPath(first, key)
	if writeErr := os.WriteFile(stray, []byte("half"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	second, err := newBookCache(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	cached := second.open(key)
	if cached == nil {
		t.Fatal("the cache forgot its book")
	}
	defer func() { _ = cached.Close() }()
	if got, _ := io.ReadAll(cached); string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatal("an unfinished file survived the restart")
	}
}

func TestBookCacheRestartKeepsTheLRUOrder(t *testing.T) {
	dir := t.TempDir()
	cache, err := newBookCache(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	newer, older := testKey("newer"), testKey("older")
	for i, key := range []string{newer, older} {
		name := cache.path(key)
		if mkErr := os.MkdirAll(filepath.Dir(name), 0o700); mkErr != nil {
			t.Fatal(mkErr)
		}
		if writeErr := os.WriteFile(name, make([]byte, 400), 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		date := past.Add(time.Duration(-i) * time.Minute)
		if timeErr := os.Chtimes(name, date, date); timeErr != nil {
			t.Fatal(timeErr)
		}
	}
	second, err := newBookCache(dir, 700)
	if err != nil {
		t.Fatal(err)
	}
	if second.open(older) != nil || second.open(newer) == nil {
		t.Fatal("eviction at load did not take the oldest")
	}
}

func TestReplacedArchiveDoesNotServeTheOldBook(t *testing.T) {
	before := buildArchive(t, 10, 1000, 33)
	after := buildArchive(t, 10, 1000, 34)
	fx := newFixture(t, &Config{BookCacheBytes: cacheLimit}, before)
	clock := time.Now()
	fx.store.now = func() time.Time { return clock }
	readBook(t, fx.store, fx.path, bookName(1))

	fx.server.Put(testPrefix+testArchive, after)
	clock = clock.Add(directoryRevalidate + time.Second)
	got := readBook(t, fx.store, fx.path, bookName(1))

	if !bytes.Equal(got, wantBook(t, after, bookName(1))) {
		t.Fatal("served the old archive's book")
	}
}

func TestCachedBookSurvivesAnOutage(t *testing.T) {
	fx := newFixture(t, &Config{BookCacheBytes: cacheLimit}, buildArchive(t, 5, 1000, 35))
	readBook(t, fx.store, fx.path, bookName(1))
	fx.server.SetDown(true)
	if _, err := tryBook(context.Background(), fx.store, fx.path, bookName(1)); err != nil {
		t.Fatalf("a cached book failed in an outage: %v", err)
	}
}

// testKey is a key of the real form.
func testKey(name string) string { return bookCacheKey("archive", "etag", name) }

// strayPath is where an interrupted write of key would be.
func strayPath(c *bookCache, key string) string {
	return filepath.Join(filepath.Dir(c.path(key)), key[:8]+"-1"+tempSuffix)
}

func cacheFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(name string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, name)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// diskUse is what the cache directory really holds, finished or not.
func diskUse(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

// openBook starts a read of size bytes and stops one short of its end: the
// book is on disk, unfinished, and the reader is still open.
func openBook(t *testing.T, c *bookCache, key string, size int) io.ReadCloser {
	t.Helper()
	rc := c.tee(key, uint64(size), io.NopCloser(strings.NewReader(strings.Repeat("x", size))))
	if _, err := io.CopyN(io.Discard, rc, int64(size-1)); err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestUnfinishedBooksStayInsideTheBudget(t *testing.T) {
	const limit, size = 1000, 200
	dir := t.TempDir()
	cache, err := newBookCache(dir, limit)
	if err != nil {
		t.Fatal(err)
	}
	var readers []io.ReadCloser
	for i := range 12 {
		readers = append(readers, openBook(t, cache, testKey(strconv.Itoa(i)), size))
	}
	if used := diskUse(t, dir); used > limit {
		t.Fatalf("%d bytes on disk over a limit of %d", used, limit)
	}
	for _, rc := range readers {
		_ = rc.Close()
	}
	if used := diskUse(t, dir); used != 0 {
		t.Fatalf("%d bytes left behind", used)
	}
	if cache.reserved != 0 {
		t.Fatalf("%d bytes still reserved", cache.reserved)
	}
}

func TestOverlappingReadersOfOneBookWriteItOnce(t *testing.T) {
	const size = 200
	dir := t.TempDir()
	cache, err := newBookCache(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	key := testKey("same")
	var readers []io.ReadCloser
	for range 12 {
		readers = append(readers, openBook(t, cache, key, size))
	}
	if used := diskUse(t, dir); used >= 2*size {
		t.Fatalf("%d bytes on disk for one book of %d", used, size)
	}
	for _, rc := range readers {
		if _, err := io.Copy(io.Discard, rc); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	got := cache.open(key)
	if got == nil {
		t.Fatal("the first reader to finish did not keep the book")
	}
	_ = got.Close()
	if cache.reserved != 0 {
		t.Fatalf("%d bytes still reserved", cache.reserved)
	}
}

func TestReservationMakesRoomFromFinishedBooks(t *testing.T) {
	cache, err := newBookCache(t.TempDir(), 300)
	if err != nil {
		t.Fatal(err)
	}
	store := func(key string) {
		rc := openBook(t, cache, key, 70)
		if _, err := io.Copy(io.Discard, rc); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	old, kept := testKey("old"), testKey("kept")
	store(old)
	store(testKey("next"))
	store(kept)
	store(testKey("more"))
	if cache.open(old) == nil {
		t.Fatal("evicted too early")
	}

	rc := openBook(t, cache, testKey("new"), 70)
	defer func() { _ = rc.Close() }()

	if cache.open(testKey("next")) != nil {
		t.Error("the oldest book stayed while a new one was being written past the limit")
	}
	if cache.total+cache.reserved > 300 {
		t.Errorf("%d kept and %d reserved over a limit of 300", cache.total, cache.reserved)
	}
}

func TestReaderOfAnEvictedBookKeepsReading(t *testing.T) {
	cache, err := newBookCache(t.TempDir(), 300)
	if err != nil {
		t.Fatal(err)
	}
	first := testKey("first")
	rc := cache.tee(first, 70, io.NopCloser(strings.NewReader(strings.Repeat("a", 70))))
	if _, copyErr := io.Copy(io.Discard, rc); copyErr != nil {
		t.Fatal(copyErr)
	}
	_ = rc.Close()
	reader := cache.open(first)
	if reader == nil {
		t.Fatal("not kept")
	}
	defer func() { _ = reader.Close() }()

	for i := range 5 {
		next := cache.tee(testKey(strconv.Itoa(i)), 70, io.NopCloser(strings.NewReader(strings.Repeat("b", 70))))
		if _, copyErr := io.Copy(io.Discard, next); copyErr != nil {
			t.Fatal(copyErr)
		}
		_ = next.Close()
	}

	if cache.open(first) != nil {
		t.Fatal("the test needs this book evicted")
	}
	got, err := io.ReadAll(reader)
	if err != nil || string(got) != strings.Repeat("a", 70) {
		t.Fatalf("read %d bytes: %v", len(got), err)
	}
}

// A declared size is the archive's word. One that lies must not write past
// what it reserved.
func TestBookLongerThanDeclaredIsNotKept(t *testing.T) {
	dir := t.TempDir()
	cache, err := newBookCache(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	key := testKey("liar")
	rc := cache.tee(key, 10, io.NopCloser(strings.NewReader(strings.Repeat("x", 100))))
	got, err := io.ReadAll(rc)
	if err != nil || len(got) != 100 {
		t.Fatalf("the read itself must go on: %d bytes, %v", len(got), err)
	}
	_ = rc.Close()
	if cache.open(key) != nil {
		t.Fatal("kept a book past its reservation")
	}
	if used := diskUse(t, dir); used != 0 {
		t.Fatalf("%d bytes left", used)
	}
	if cache.reserved != 0 {
		t.Fatalf("%d bytes still reserved", cache.reserved)
	}
}

func TestStrangeFilesInTheCacheAreIgnored(t *testing.T) {
	dir := t.TempDir()
	good := testKey("good")
	goodFile := filepath.Join(dir, good[:2], good)
	if err := os.MkdirAll(filepath.Dir(goodFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goodFile, []byte("book"), 0o600); err != nil {
		t.Fatal(err)
	}
	strays := []string{"x", "README", ".DS_Store", strings.Repeat("z", 64)}
	for _, name := range strays {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("s", 500)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	misplaced := testKey("misplaced")
	if err := os.WriteFile(filepath.Join(dir, misplaced), []byte(strings.Repeat("s", 500)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(goodFile, filepath.Join(dir, good[:2], testKey("link"))); err != nil {
		t.Fatal(err)
	}

	cache, err := newBookCache(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.items) != 1 || cache.total != 4 {
		t.Fatalf("indexed %d entries, %d bytes", len(cache.items), cache.total)
	}
	// Past the limit, which is where a stray name used to reach path(key).
	rc := cache.tee(testKey("big"), 20, io.NopCloser(strings.NewReader(strings.Repeat("x", 20))))
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	got := cache.open(good)
	if got == nil {
		t.Fatal("a good book was lost")
	}
	_ = got.Close()
}
