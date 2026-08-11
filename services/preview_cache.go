package services

// preview_cache.go defines the cache contract the preview pipeline depends on.
//
// The cache is not optional. The plan's decision is explicit: if Redis is
// unavailable, the preview is refused, not silently degraded — without a
// cache, every page turn would unpack the book from its zip archive, and
// that cost was the reason the cache was introduced in the first place.
// PreviewService checks availability through Ping before any work; a failed
// Ping is a typed refusal, distinct from "book not found" or "format
// unsupported".
//
// The cache stores two things per book: a manifest (the table of contents
// the reader sees first) and the rendered chunks (the portions the reader
// pages through). They are stored under separate keys so a reader can fetch
// one chunk without pulling the whole book. Chunks are written before the
// manifest: if the process dies between the two, a stale manifest without
// any chunks is treated as a miss (rebuild), not as an empty book.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis"
)

// ErrCacheMiss is returned by Get when the key is not in the cache. It is
// not an error in the "something broke" sense — it is the expected answer
// "this book has not been previewed yet, or the TTL expired".
var ErrCacheMiss = errors.New("preview cache: key not found")

// ErrCacheUnavailable is returned when the cache itself is broken (Redis is
// down, network error, etc.). The preview pipeline refuses to work without
// a cache; this sentinel lets the caller distinguish "rebuild the preview"
// (ErrCacheMiss) from "tell the reader the service is degraded"
// (ErrCacheUnavailable).
var ErrCacheUnavailable = errors.New("preview cache: backend is unavailable")

// ErrEmptyMD5 is returned when a book has no content hash. The cache key is
// built from the MD5; a book without one would produce a key like
// "preview:v1:" — a valid-looking Redis key that could collide across
// every book missing an MD5, serving one book's preview under another.
// Refusing is safer than serving the wrong content.
var ErrEmptyMD5 = errors.New("preview: book has no MD5, cannot build a cache key")

// PreviewCache is the narrow surface the preview service consumes. It
// stores and retrieves manifests and chunks by key; the service builds the
// key from the book's MD5 and a render version, so a content change or a
// policy bump invalidates the old entry without a manual flush.
//
// Methods accept a context for cancellation; the Redis v6 client this
// project uses does not propagate context into its commands, but the
// interface carries one so the contract does not change when the client is
// upgraded.
type PreviewCache interface {
	// Ping checks that the cache backend is reachable. Called once per
	// request, before any Get; a non-nil error means the whole request
	// is refused.
	Ping(ctx context.Context) error

	// GetManifest returns the cached manifest for the key, or
	// (nil, ErrCacheMiss) if the key is not present.
	GetManifest(ctx context.Context, key string) ([]byte, error)

	// PutManifest writes the manifest. The caller must write all chunks
	// first; a manifest without chunks is treated as a stale entry.
	PutManifest(ctx context.Context, key string, data []byte, ttl time.Duration) error

	// GetChunk returns one cached chunk by index, or (nil, ErrCacheMiss).
	GetChunk(ctx context.Context, key string, index int) ([]byte, error)

	// PutChunk writes one chunk. Must be called before PutManifest.
	PutChunk(ctx context.Context, key string, index int, data []byte, ttl time.Duration) error
}

// renderVersionPrefix is the version tag embedded in every cache key. Bump
// this when the rendering pipeline changes in a way that invalidates
// previously cached output (new chunk size, new sanitizer, new image
// policy). Old keys expire naturally through TTL; new requests get the new
// version tag and miss the old entries.
const renderVersionPrefix = "v1"

// cacheKeyTTL is the default time-to-live for cache entries. Production
// reads this from config; tests override through the service's TTL field.
const cacheKeyTTL = 24 * time.Hour

// buildCacheKey assembles the Redis key for one book's preview. The key
// carries the render version and the book's content hash, so that:
//
//   - a re-scan of the same book (new MD5) does not serve the old preview;
//   - a bump of renderVersionPrefix does not serve chunks rendered under
//     the old policy.
//
// The format is fixed: preview:{version}:{md5}. An empty MD5 is refused
// upstream (ErrEmptyMD5) and never reaches this function.
func buildCacheKey(md5, renderVersion string) string {
	return fmt.Sprintf("preview:%s:%s", renderVersion, md5)
}

// chunkKey builds the Redis key for one chunk of one book. Manifest and
// chunks share the prefix so they can be enumerated together if a flush is
// ever needed; the suffix distinguishes them.
func chunkKey(baseKey string, index int) string {
	return fmt.Sprintf("%s:chunk:%d", baseKey, index)
}

// manifestKey builds the Redis key for the manifest of one book.
func manifestKey(baseKey string) string {
	return baseKey + ":manifest"
}

// RedisPreviewCache is the production implementation of PreviewCache. It
// uses the same go-redis v6 client the rest of the project uses, pointed at
// a separate database (or a separate instance) configured through
// preview.redis.* keys — the plan requires the ability to move preview to
// its own Redis without touching other subsystems.
type RedisPreviewCache struct {
	client *redis.Client
}

// NewRedisPreviewCache wires the cache against an existing client. The
// caller is responsible for connecting, selecting the right database, and
// handling the connection lifecycle; this struct does not own the client.
func NewRedisPreviewCache(client *redis.Client) *RedisPreviewCache {
	return &RedisPreviewCache{client: client}
}

func (c *RedisPreviewCache) Ping(_ context.Context) error {
	if _, err := c.client.Ping().Result(); err != nil {
		return fmt.Errorf("%w: %v", ErrCacheUnavailable, err)
	}
	return nil
}

func (c *RedisPreviewCache) GetManifest(_ context.Context, key string) ([]byte, error) {
	data, err := c.client.Get(manifestKey(key)).Bytes()
	if err == redis.Nil {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get manifest: %v", ErrCacheUnavailable, err)
	}
	return data, nil
}

func (c *RedisPreviewCache) PutManifest(_ context.Context, key string, data []byte, ttl time.Duration) error {
	if err := c.client.Set(manifestKey(key), data, ttl).Err(); err != nil {
		return fmt.Errorf("%w: put manifest: %v", ErrCacheUnavailable, err)
	}
	return nil
}

func (c *RedisPreviewCache) GetChunk(_ context.Context, key string, index int) ([]byte, error) {
	data, err := c.client.Get(chunkKey(key, index)).Bytes()
	if err == redis.Nil {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get chunk %d: %v", ErrCacheUnavailable, err, index)
	}
	return data, nil
}

func (c *RedisPreviewCache) PutChunk(_ context.Context, key string, index int, data []byte, ttl time.Duration) error {
	if err := c.client.Set(chunkKey(key, index), data, ttl).Err(); err != nil {
		return fmt.Errorf("%w: put chunk %d: %v", ErrCacheUnavailable, err, index)
	}
	return nil
}
