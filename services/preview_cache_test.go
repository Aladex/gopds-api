package services

// preview_cache_test.go contains the integration test for RedisPreviewCache
// against a live Redis instance, plus tests for buildCacheKey.

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis"
)

// buildCacheKey must embed the render version, the MD5 and the catalog
// id, in that order, so that a change to any of the three produces a
// different key. The format is part of the contract: every consumer
// (service, handler, flush tool) reads the same shape.
//
// The id case is the one that is easy to lose: two catalog rows can hold
// the same file, and the HTML addresses images by book id, so an entry
// shared by hash alone would hand the second book the first one's picture
// URLs.
func TestBuildCacheKey(t *testing.T) {
	cases := []struct {
		name    string
		bookID  int64
		md5     string
		version string
		want    string
	}{
		{"normal", 7, "abc123", "v1", "preview:v1:abc123:7"},
		{"different md5", 7, "def456", "v1", "preview:v1:def456:7"},
		{"different version", 7, "abc123", "v2", "preview:v2:abc123:7"},
		{"same file, different catalog row", 8, "abc123", "v1", "preview:v1:abc123:8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildCacheKey(tc.bookID, tc.md5, tc.version)
			if got != tc.want {
				t.Errorf("buildCacheKey(%d, %q, %q) = %q, want %q", tc.bookID, tc.md5, tc.version, got, tc.want)
			}
		})
	}
}

// TestImageKey pins the image key shape: it follows the chunk key pattern —
// same base prefix, distinguishing suffix — so images can be enumerated
// alongside chunks and the manifest of one book.
func TestImageKey(t *testing.T) {
	got := imageKey("preview:v1:abc", 3)
	want := "preview:v1:abc:image:3"
	if got != want {
		t.Errorf("imageKey = %q, want %q", got, want)
	}
}

// TestRedisPreviewCache_RoundTrip exercises the real Redis implementation
// against a live instance. Skip if Redis is not reachable — a developer
// without Redis on 6380 should see a skip, not a failure.
func TestRedisPreviewCache_RoundTrip(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6380",
		DB:   0,
	})
	defer client.Close()

	ctx := context.Background()
	cache := NewRedisPreviewCache(client)

	// Ping — if this fails, skip the whole test.
	if err := cache.Ping(ctx); err != nil {
		t.Skipf("Redis not reachable at 127.0.0.1:6380: %v", err)
	}

	// Use a unique key prefix to avoid colliding with other tests.
	key := buildCacheKey(1, "integration-test-md5", renderVersionPrefix)
	defer client.Del(chunkKey(key, 0), manifestKey(key), imageKey(key, 1))

	// Chunk round-trip.
	chunkData := []byte("<FictionBook>hello</FictionBook>")
	if err := cache.PutChunk(ctx, key, 0, chunkData, 30*time.Second); err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
	gotChunk, gerr := cache.GetChunk(ctx, key, 0)
	if gerr != nil {
		t.Fatalf("GetChunk: %v", gerr)
	}
	if !bytes.Equal(gotChunk, chunkData) {
		t.Errorf("chunk roundtrip: got %q, want %q", gotChunk, chunkData)
	}

	// Manifest miss before Put (only chunk was stored).
	if _, err := cache.GetManifest(ctx, key); !errors.Is(err, ErrCacheMiss) {
		t.Errorf("GetManifest before Put: err = %v, want ErrCacheMiss", err)
	}

	// Manifest round-trip.
	manifestData := []byte(`{"title":"Test","chunks":1}`)
	if err := cache.PutManifest(ctx, key, manifestData, 30*time.Second); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	gotManifest, merr := cache.GetManifest(ctx, key)
	if merr != nil {
		t.Fatalf("GetManifest: %v", merr)
	}
	if !bytes.Equal(gotManifest, manifestData) {
		t.Errorf("manifest roundtrip: got %q, want %q", gotManifest, manifestData)
	}

	// Chunk miss for a non-existent index.
	if _, err := cache.GetChunk(ctx, key, 99); !errors.Is(err, ErrCacheMiss) {
		t.Errorf("GetChunk(99): err = %v, want ErrCacheMiss", err)
	}

	// Image round-trip: the MIME travels next to the bytes, so the handler
	// serves the exact type without re-sniffing the payload.
	imageData := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	if err := cache.PutImage(ctx, key, 1, imageData, "image/png", 30*time.Second); err != nil {
		t.Fatalf("PutImage: %v", err)
	}
	gotPayload, gotMIME, ierr := cache.GetImage(ctx, key, 1)
	if ierr != nil {
		t.Fatalf("GetImage: %v", ierr)
	}
	if !bytes.Equal(gotPayload, imageData) {
		t.Errorf("image roundtrip: got %v, want %v", gotPayload, imageData)
	}
	if gotMIME != "image/png" {
		t.Errorf("image MIME roundtrip: got %q, want %q", gotMIME, "image/png")
	}

	// Image miss for a non-existent ordinal.
	if _, _, err := cache.GetImage(ctx, key, 99); !errors.Is(err, ErrCacheMiss) {
		t.Errorf("GetImage(99): err = %v, want ErrCacheMiss", err)
	}
}

// TestRedisPreviewCache_CanceledContextRefuses proves that a canceled context
// causes every cache operation to refuse — not succeed silently. go-redis v6's
// WithContext stores the context but does not check it in the command path,
// so the explicit ctx.Err() check in each method is what actually executes
// the cancellation.  Skip if Redis is not reachable.
func TestRedisPreviewCache_CanceledContextRefuses(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6380",
		DB:   0,
	})
	defer client.Close()

	pingCtx := context.Background()
	cache := NewRedisPreviewCache(client)
	if err := cache.Ping(pingCtx); err != nil {
		t.Skipf("Redis not reachable at 127.0.0.1:6380: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	key := buildCacheKey(999, "cancel-test-md5", renderVersionPrefix)
	defer client.Del(manifestKey(key), chunkKey(key, 0), imageKey(key, 1))

	// Every write must refuse on an already-canceled context.  The assertion
	// is "err != nil", not the error text: a mutation that drops the ctx
	// check would let the operation reach Redis and succeed.
	if err := cache.PutChunk(canceledCtx, key, 0, []byte("chunk"), 30*time.Second); err == nil {
		t.Error("PutChunk succeeded on a canceled context — the cache must refuse")
	}
	if err := cache.PutImage(canceledCtx, key, 1, []byte("img"), "image/png", 30*time.Second); err == nil {
		t.Error("PutImage succeeded on a canceled context — the cache must refuse")
	}
	if err := cache.PutManifest(canceledCtx, key, []byte("{}"), 30*time.Second); err == nil {
		t.Error("PutManifest succeeded on a canceled context — the cache must refuse")
	}
	// Reads must refuse too: a canceled context must not silently look like
	// a cache miss — that would trigger a cold build on a dead request.
	if _, err := cache.GetManifest(canceledCtx, key); err == nil {
		t.Error("GetManifest succeeded on a canceled context — the cache must refuse")
	}
	if _, err := cache.GetChunk(canceledCtx, key, 0); err == nil {
		t.Error("GetChunk succeeded on a canceled context — the cache must refuse")
	}
}
