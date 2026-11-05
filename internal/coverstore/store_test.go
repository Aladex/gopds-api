package coverstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/internal/objectstore/s3test"
)

const (
	testTimeout = 200 * time.Millisecond
	testRel     = "lib/book-fb2.jpg"
	testKey     = "covers/" + testRel
)

func fixture(t *testing.T) (*s3test.Server, *Store, string) {
	t.Helper()
	server := s3test.New(t)
	return server, New(server.Client(testTimeout, -1), "covers/"), t.TempDir()
}

func read(t *testing.T, cover *Cover) string {
	t.Helper()
	defer func() { _ = cover.Close() }()
	data, err := io.ReadAll(cover)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLocalCoverWinsAndMakesNoRequest(t *testing.T) {
	server, store, base := fixture(t)
	server.Put(testKey, []byte("remote"))
	writeLocal(t, base, "local")

	cover, err := store.Open(context.Background(), base, testRel)
	if err != nil {
		t.Fatal(err)
	}
	if !cover.Local || read(t, cover) != "local" {
		t.Fatal("did not read the local cover")
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

func writeLocal(t *testing.T, base, content string) {
	t.Helper()
	full := filepath.Join(base, testRel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteCover(t *testing.T) {
	server, store, base := fixture(t)
	server.Put(testKey, []byte("remote"))

	cover, err := store.Open(context.Background(), base, "/"+testRel)
	if err != nil {
		t.Fatal(err)
	}
	if cover.Local || cover.ModTime.IsZero() || cover.Size != 6 {
		t.Fatalf("cover = %+v", cover)
	}
	if got := read(t, cover); got != "remote" {
		t.Fatalf("got %q", got)
	}
}

func TestMissIsAskedOnce(t *testing.T) {
	server, store, base := fixture(t)
	for range 5 {
		if _, err := store.Open(context.Background(), base, testRel); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if _, err := store.Stat(context.Background(), base, testRel); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v", err)
		}
	}
	if n := server.Requests(); n != 1 {
		t.Fatalf("%d requests for one known miss", n)
	}
}

func TestMissExpires(t *testing.T) {
	server, store, base := fixture(t)
	clock := time.Now()
	store.misses.now = func() time.Time { return clock }
	if _, err := store.Open(context.Background(), base, testRel); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	server.Put(testKey, []byte("uploaded later"))

	clock = clock.Add(missTTL + time.Second)
	cover, err := store.Open(context.Background(), base, testRel)
	if err != nil {
		t.Fatalf("a cover uploaded later stayed missing: %v", err)
	}
	_ = cover.Close()
}

func TestOutageIsNotRememberedAsAMiss(t *testing.T) {
	server, store, base := fixture(t)
	server.Put(testKey, []byte("remote"))
	server.SetDown(true)
	if _, err := store.Open(context.Background(), base, testRel); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	server.SetDown(false)
	cover, err := store.Open(context.Background(), base, testRel)
	if err != nil {
		t.Fatalf("the outage outlived itself: %v", err)
	}
	_ = cover.Close()
}

func TestMissCacheIsBounded(t *testing.T) {
	cache := newMissCache(3, time.Minute)
	for i := range 10 {
		cache.add(fmt.Sprint(i))
	}
	if len(cache.items) != 3 || cache.order.Len() != 3 {
		t.Fatalf("holds %d", len(cache.items))
	}
	if cache.has("0") || !cache.has("9") {
		t.Fatal("kept the wrong ones")
	}
}

func TestDiskOnlyStore(t *testing.T) {
	base := t.TempDir()
	if _, err := Default().Open(context.Background(), base, testRel); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	writeLocal(t, base, "local")
	cover, err := Default().Open(context.Background(), base, testRel)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, cover) != "local" {
		t.Fatal("wrong cover")
	}
	if size, err := Default().Stat(context.Background(), base, testRel); err != nil || size != 5 {
		t.Fatalf("Stat = %d, %v", size, err)
	}
}

func TestPathOutsideTheBaseIsRefused(t *testing.T) {
	_, store, base := fixture(t)
	// Resolve anchors a relative path, so this lands inside the base and
	// misses; it must not read the file next to it.
	outside := filepath.Join(filepath.Dir(base), "secret.jpg")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(outside) }()
	if _, err := store.Open(context.Background(), base, "../secret.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
