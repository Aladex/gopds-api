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

	"gopds-api/logging"
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
// Methods accept a context for cancellation. In go-redis v6 WithContext
// stores the context but does not check it in the command path (true context
// propagation arrived in v7), so cancellation is executed here, in two
// layers:
//
//   - each method checks ctx.Err() before touching Redis, so an operation
//     never starts after the deadline;
//   - the command itself runs through awaitCmd, which frees the caller the
//     moment the context is done — even mid-command — instead of letting a
//     hung operation run to the client's own read timeout. A command that
//     outlived its caller is bounded by that read timeout and its late
//     outcome is drained and logged; a manifest SET that crossed the build
//     deadline is additionally retracted (see PutManifest), because a
//     manifest published past the deadline breaks the phase-3 invariant.
//
// WithContext is still passed along: it carries the context into the client
// for the day the dependency is upgraded to a version that honors it.
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

// awaitCmd runs op on its own goroutine and returns whichever happens first:
// the operation's result, or the context's cancellation.
//
// go-redis v6 stores the context (WithContext) but never checks it in the
// command path, so without this a command that started before the build
// deadline would run past it, and its caller would wait for the client's own
// read timeout (3 s by default) — for a cold build that means publishing
// after the deadline. Here the caller is freed exactly at the deadline, and
// the client's read timeout stays as the backstop that bounds the abandoned
// goroutine instead.
//
// Abandonment is neither a goroutine leak nor a silent hole: the operation
// is still bounded by the client's read/write timeout, and its late result
// is drained — a late FAILURE is logged, because a backend error arriving
// after the caller left must not become invisible. A late SUCCESS is
// harmless for chunks and images: bytes without a manifest are a stale
// entry, which cachedEntry treats as a miss. It is NOT harmless for the
// manifest, so PutManifest retracts a write that crossed the deadline.
func awaitCmd(ctx context.Context, op func() error) error {
	done := make(chan error, 1) // buffered: the abandoned op never blocks on send
	go func() { done <- op() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		ctxErr := ctx.Err()
		go func() {
			if late := <-done; late != nil && late != redis.Nil {
				logging.Errorf("preview cache: command abandoned on %v completed late: %v", ctxErr, late)
			}
		}()
		return ctxErr
	}
}

func (c *RedisPreviewCache) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: ping: %w", ErrCacheUnavailable, err)
	}
	if err := awaitCmd(ctx, func() error {
		_, perr := c.client.WithContext(ctx).Ping().Result()
		return perr
	}); err != nil {
		return fmt.Errorf("%w: %w", ErrCacheUnavailable, err)
	}
	return nil
}

func (c *RedisPreviewCache) GetManifest(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: get manifest: %w", ErrCacheUnavailable, err)
	}
	var data []byte
	err := awaitCmd(ctx, func() error {
		var gerr error
		data, gerr = c.client.WithContext(ctx).Get(manifestKey(key)).Bytes()
		return gerr
	})
	if err == redis.Nil {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get manifest: %w", ErrCacheUnavailable, err)
	}
	return data, nil
}

// retractScript deletes the manifest only if the stored value is still the
// one the late build wrote. The comparison is what makes the retraction safe
// against a subsequent rebuild of the same key: if another build already
// replaced the entry, the retract must not delete its work. (A rebuild of
// the same book under the same revision writes byte-identical JSON, so the
// comparison cannot tell it apart from the late write — in that case the
// fresh manifest is deleted too, which costs one extra cold build, never a
// broken promise.)
var retractScript = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`

// retractLateManifest removes a manifest whose SET completed after the
// caller's context died. It runs detached (context.Background): the caller's
// context is exactly why we are here, and the operation is bounded by the
// client's own read/write timeout. A failure is logged, not returned — the
// caller was already freed; there is nobody left to return it to.
func (c *RedisPreviewCache) retractLateManifest(key string, data []byte) {
	err := c.client.WithContext(context.Background()).Eval(retractScript, []string{key}, data).Err()
	if err != nil {
		logging.Errorf("preview cache: failed to retract the late manifest %s: %v", key, err)
	}
}

func (c *RedisPreviewCache) PutManifest(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: put manifest: %w", ErrCacheUnavailable, err)
	}
	mk := manifestKey(key)
	err := awaitCmd(ctx, func() error {
		if serr := c.client.WithContext(ctx).Set(mk, data, ttl).Err(); serr != nil {
			return serr
		}
		// The SET may have completed after the deadline fired mid-command:
		// go-redis v6 does not interrupt a command in flight, and awaitCmd
		// may already have freed the caller. A manifest published past the
		// deadline is the phase-3 promise ("every referenced byte exists,
		// published within the build budget") broken on purpose, so a write
		// that crossed it is retracted rather than left in the cache.
		if ctx.Err() != nil {
			c.retractLateManifest(mk, data)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: put manifest: %w", ErrCacheUnavailable, err)
	}
	return nil
}

func (c *RedisPreviewCache) GetChunk(ctx context.Context, key string, index int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: get chunk %d: %w", ErrCacheUnavailable, index, err)
	}
	var data []byte
	err := awaitCmd(ctx, func() error {
		var gerr error
		data, gerr = c.client.WithContext(ctx).Get(chunkKey(key, index)).Bytes()
		return gerr
	})
	if err == redis.Nil {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get chunk %d: %w", ErrCacheUnavailable, index, err)
	}
	return data, nil
}

func (c *RedisPreviewCache) PutChunk(ctx context.Context, key string, index int, data []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: put chunk %d: %w", ErrCacheUnavailable, index, err)
	}
	if err := awaitCmd(ctx, func() error {
		return c.client.WithContext(ctx).Set(chunkKey(key, index), data, ttl).Err()
	}); err != nil {
		return fmt.Errorf("%w: put chunk %d: %w", ErrCacheUnavailable, index, err)
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
	// HMSet and Expire travel in one op: if the deadline fires between them,
	// the caller is still freed at the deadline — not after the second
	// command's own timeout.
	if err := awaitCmd(ctx, func() error {
		cl := c.client.WithContext(ctx)
		k := imageKey(key, ordinal)
		if herr := cl.HMSet(k, map[string]interface{}{"payload": payload, "mime": mime}).Err(); herr != nil {
			return herr
		}
		return cl.Expire(k, ttl).Err()
	}); err != nil {
		return fmt.Errorf("%w: put image %d: %w", ErrCacheUnavailable, ordinal, err)
	}
	return nil
}

func (c *RedisPreviewCache) GetImage(ctx context.Context, key string, ordinal int) (payload []byte, mime string, err error) {
	if cerr := ctx.Err(); cerr != nil {
		return nil, "", fmt.Errorf("%w: get image %d: %w", ErrCacheUnavailable, ordinal, cerr)
	}
	var fields map[string]string
	err = awaitCmd(ctx, func() error {
		var gerr error
		fields, gerr = c.client.WithContext(ctx).HGetAll(imageKey(key, ordinal)).Result()
		return gerr
	})
	if err != nil {
		return nil, "", fmt.Errorf("%w: get image %d: %w", ErrCacheUnavailable, ordinal, err)
	}
	data, ok := fields["payload"]
	if !ok {
		return nil, "", ErrCacheMiss
	}
	return []byte(data), fields["mime"], nil
}
