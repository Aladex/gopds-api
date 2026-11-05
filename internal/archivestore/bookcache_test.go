package archivestore

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
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
