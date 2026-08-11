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

// buildCacheKey must embed the render version and the MD5, in that order,
// so that a change to either produces a different key. The format is part
// of the contract: every consumer (service, handler, flush tool) reads the
// same shape.
func TestBuildCacheKey(t *testing.T) {
	cases := []struct {
		name    string
		md5     string
		version string
		want    string
	}{
		{"normal", "abc123", "v1", "preview:v1:abc123"},
		{"different md5", "def456", "v1", "preview:v1:def456"},
		{"different version", "abc123", "v2", "preview:v2:abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildCacheKey(tc.md5, tc.version)
			if got != tc.want {
				t.Errorf("buildCacheKey(%q, %q) = %q, want %q", tc.md5, tc.version, got, tc.want)
			}
		})
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
	key := buildCacheKey("integration-test-md5", renderVersionPrefix)
	defer client.Del(chunkKey(key, 0), manifestKey(key))

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
}
