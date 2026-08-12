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
// The cache stores three things per book: a manifest (the JSON index the
// reader sees first), the rendered chunks (the portions the reader pages
// through), and the prepared images (the resources the chunks' <img> tags
// reference). They are stored under separate keys so a reader can fetch one
// chunk or one image without pulling the whole book. Chunks and images are
// written before the manifest: if the process dies in between, a stale
// manifest without its bytes is treated as a miss (rebuild), not as an empty
// book — and a manifest is never published after a failed write, because it
// is the promise that every referenced byte exists.

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
// stores and retrieves manifests, chunks and prepared images by key; the
// service builds the key from the book's MD5 and a revision covering the
// render version and the image policy, so a content change or a policy bump
// invalidates the old entry without a manual flush.
//
// Every Get distinguishes two failures, typed, because the service maps them
// to opposite decisions:
//
//   - absent key: an error matching errors.Is(err, ErrCacheMiss) — the
//     service rebuilds;
//   - broken backend: anything else, wrapped by implementations into an
//     error matching errors.Is(err, ErrCacheUnavailable) — the service
//     refuses the request. Treating an outage as a miss would turn a Redis
//     failure into a full archive unpack per request.
//
// Methods accept a context for cancellation; each operation goes through
// client.WithContext(ctx), so a canceled build (cold-build timeout or
// Shutdown) interrupts a hung Redis command rather than blocking until the
// network stack gives up on its own.  In go-redis v6 WithContext stores
// the context but does not check it in the command path (true context
// propagation arrived in v7), so each method also checks ctx.Err()
// explicitly before touching Redis — that check is what actually executes
// cancellation today; WithContext carries it forward when the client is
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

	// GetImage returns one prepared image by ordinal, with the MIME stored
	// next to it, or (nil, "", ErrCacheMiss). The MIME travels with the
	// bytes because the handler must serve the exact type the preparation
	// decided on; re-sniffing at serve time could disagree with it.
	GetImage(ctx context.Context, key string, ordinal int) (payload []byte, mime string, err error)

	// PutImage writes one prepared image with its MIME. Must be called
	// before PutManifest: a manifest published before its images would
	// promise resources the cache does not yet hold.
	PutImage(ctx context.Context, key string, ordinal int, payload []byte, mime string, ttl time.Duration) error
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
// carries the catalog id, the revision and the book's content hash, so
// that:
//
//   - a re-scan of the same book (new MD5) does not serve the old preview;
//   - a bump of anything the revision covers (render version, image policy)
//     does not serve chunks or images prepared under the old policy;
//   - two catalog rows holding the same file do not share an entry.
//
// The id is not redundant with the hash. Identical files under different
// ids are a named concept in this project — GetDuplicateGroups finds books
// by matching MD5, and books carry duplicate_hidden — and the rendered HTML
// addresses images as /preview/{bookID}/{revision}/{n}. Keyed by hash alone,
// the second book would be served the first one's HTML, pointing every
// picture at a foreign id.
//
// The format is fixed: preview:{revision}:{md5}:{bookID}. An empty MD5 is
// refused upstream (ErrEmptyMD5) and never reaches this function.
func buildCacheKey(bookID int64, md5, revision string) string {
	return fmt.Sprintf("preview:%s:%s:%d", revision, md5, bookID)
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

// imageKey builds the Redis key for one prepared image of one book, on the
// chunkKey pattern: the book's base prefix plus a distinguishing suffix, so
// every resource of one cutting shares the enumeration prefix.
func imageKey(baseKey string, ordinal int) string {
	return fmt.Sprintf("%s:image:%d", baseKey, ordinal)
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

func (c *RedisPreviewCache) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: ping: %w", ErrCacheUnavailable, err)
	}
	if _, err := c.client.WithContext(ctx).Ping().Result(); err != nil {
		return fmt.Errorf("%w: %v", ErrCacheUnavailable, err)
	}
	return nil
}

func (c *RedisPreviewCache) GetManifest(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: get manifest: %w", ErrCacheUnavailable, err)
	}
	data, err := c.client.WithContext(ctx).Get(manifestKey(key)).Bytes()
	if err == redis.Nil {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get manifest: %v", ErrCacheUnavailable, err)
	}
	return data, nil
}

func (c *RedisPreviewCache) PutManifest(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: put manifest: %w", ErrCacheUnavailable, err)
	}
	if err := c.client.WithContext(ctx).Set(manifestKey(key), data, ttl).Err(); err != nil {
		return fmt.Errorf("%w: put manifest: %v", ErrCacheUnavailable, err)
	}
	return nil
}

func (c *RedisPreviewCache) GetChunk(ctx context.Context, key string, index int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: get chunk %d: %w", ErrCacheUnavailable, index, err)
	}
	data, err := c.client.WithContext(ctx).Get(chunkKey(key, index)).Bytes()
	if err == redis.Nil {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get chunk %d: %v", ErrCacheUnavailable, err, index)
	}
	return data, nil
}

func (c *RedisPreviewCache) PutChunk(ctx context.Context, key string, index int, data []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: put chunk %d: %w", ErrCacheUnavailable, index, err)
	}
	if err := c.client.WithContext(ctx).Set(chunkKey(key, index), data, ttl).Err(); err != nil {
		return fmt.Errorf("%w: put chunk %d: %v", ErrCacheUnavailable, err, index)
	}
	return nil
}

// The image is stored as a two-field hash (payload, mime) rather than as a
// framed blob: the fields are written in one command, so a reader never sees
// bytes without their type, and no framing byte has to be kept out of the
// payload alphabet.
func (c *RedisPreviewCache) PutImage(ctx context.Context, key string, ordinal int, payload []byte, mime string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: put image %d: %w", ErrCacheUnavailable, ordinal, err)
	}
	cl := c.client.WithContext(ctx)
	k := imageKey(key, ordinal)
	if err := cl.HMSet(k, map[string]interface{}{"payload": payload, "mime": mime}).Err(); err != nil {
		return fmt.Errorf("%w: put image %d: %v", ErrCacheUnavailable, ordinal, err)
	}
	if err := cl.Expire(k, ttl).Err(); err != nil {
		return fmt.Errorf("%w: expire image %d: %v", ErrCacheUnavailable, ordinal, err)
	}
	return nil
}

func (c *RedisPreviewCache) GetImage(ctx context.Context, key string, ordinal int) (payload []byte, mime string, err error) {
	if cerr := ctx.Err(); cerr != nil {
		return nil, "", fmt.Errorf("%w: get image %d: %w", ErrCacheUnavailable, ordinal, cerr)
	}
	fields, err := c.client.WithContext(ctx).HGetAll(imageKey(key, ordinal)).Result()
	if err != nil {
		return nil, "", fmt.Errorf("%w: get image %d: %v", ErrCacheUnavailable, ordinal, err)
	}
	data, ok := fields["payload"]
	if !ok {
		return nil, "", ErrCacheMiss
	}
	return []byte(data), fields["mime"], nil
}
