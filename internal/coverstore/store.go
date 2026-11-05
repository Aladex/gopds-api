// Package coverstore reads book covers wherever they are: the disk first, then
// object storage when it is configured. Covers are written to disk only, by
// the scanner and the admin upload; nothing here writes or deletes one. The one
// thing it can write is a thumbnail, under a prefix of its own.
package coverstore

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopds-api/internal/objectstore"
	"gopds-api/internal/safepath"
)

const (
	// maxRemoteCover bounds what one remote cover may take in memory. The
	// largest on disk are a few megabytes.
	maxRemoteCover = 32 << 20

	// missLimit bounds the remembered misses: at about 100 bytes a path it
	// stays under a few megabytes.
	missLimit = 50_000
	// missTTL is how long a miss is believed. A cover uploaded to the bucket
	// meanwhile shows up after this long.
	missTTL = 10 * time.Minute

	thumbContentType = "image/jpeg"
)

// Errors a read reports.
var (
	// ErrNotFound is a cover neither on disk nor in object storage.
	ErrNotFound = fmt.Errorf("cover not found: %w", fs.ErrNotExist)
	// ErrUnavailable is a cover object storage could not deliver right now.
	ErrUnavailable = objectstore.ErrUnavailable
	// ErrUnreadable is a cover that is on disk and cannot be opened. The disk
	// has the final word: object storage is not asked.
	ErrUnreadable = errors.New("cover on disk cannot be read")
)

// Cover is an open cover. Close it.
type Cover struct {
	io.ReadSeeker
	Size    int64
	ModTime time.Time
	// Local reports a cover read from disk.
	Local  bool
	closer io.Closer
}

// Close releases the cover.
func (c *Cover) Close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}

// Store reads covers. The zero value reads the disk alone.
type Store struct {
	client *objectstore.Client
	prefix string
	misses *missCache
	// thumbPrefix is where thumbnails live in the bucket. Empty keeps them
	// on local disk alone.
	thumbPrefix string
}

var defaultStore atomic.Pointer[Store]

// Default is the application's store: the disk alone until SetDefault.
func Default() *Store {
	if s := defaultStore.Load(); s != nil {
		return s
	}
	return &Store{}
}

// SetDefault installs the application's store. Called once at startup; tests
// restore the previous one.
func SetDefault(s *Store) { defaultStore.Store(s) }

// New builds a store over a bucket.
func New(client *objectstore.Client, prefix string) *Store {
	return &Store{client: client, prefix: prefix, misses: newMissCache(missLimit, missTTL)}
}

// WithThumbs keeps thumbnails in the bucket under prefix as well. It needs a
// client allowed to write there.
func (s *Store) WithThumbs(prefix string) *Store {
	s.thumbPrefix = prefix
	return s
}

// ThumbsInBucket reports whether thumbnails are kept in the bucket.
func (s *Store) ThumbsInBucket() bool { return s.client != nil && s.thumbPrefix != "" }

// OpenThumb opens the thumbnail of the cover at rel in the bucket. ModTime is
// the date of the object.
func (s *Store) OpenThumb(ctx context.Context, rel string) (*Cover, error) {
	if !s.ThumbsInBucket() {
		return nil, ErrNotFound
	}
	return s.fetch(ctx, s.keyUnder(s.thumbPrefix, rel))
}

// PutThumb stores the thumbnail of the cover at rel in the bucket.
func (s *Store) PutThumb(ctx context.Context, rel string, data []byte) error {
	if !s.ThumbsInBucket() {
		return nil
	}
	key := s.keyUnder(s.thumbPrefix, rel)
	if err := s.client.Put(ctx, key, data, thumbContentType); err != nil {
		return err
	}
	s.misses.remove(key)
	return nil
}

// RemoteModTime is the date of the cover at rel in the bucket.
func (s *Store) RemoteModTime(ctx context.Context, rel string) (time.Time, error) {
	if s.client == nil {
		return time.Time{}, ErrNotFound
	}
	key := s.key(rel)
	if s.misses.has(key) {
		return time.Time{}, ErrNotFound
	}
	remote, err := s.client.Head(ctx, key)
	if err != nil {
		return time.Time{}, s.remember(key, err)
	}
	return remote.LastModified, nil
}

// Open opens the cover at rel under base. Without object storage a cover that
// is not on disk is simply ErrNotFound.
func (s *Store) Open(ctx context.Context, base, rel string) (*Cover, error) {
	cover, err := OpenOnDisk(base, rel)
	if !errors.Is(err, ErrNotFound) || s.client == nil {
		return cover, err
	}
	return s.openRemote(ctx, rel)
}

// OpenOnDisk opens the cover at rel under base without ever asking object
// storage: ErrNotFound means it is not on disk. Opening is what decides that;
// a check made earlier can be out of date by the time it is acted on.
func OpenOnDisk(base, rel string) (*Cover, error) {
	full, err := safepath.Resolve(base, rel)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- full comes from safepath.Resolve above, which refuses
	// anything outside the configured posters directory.
	file, err := os.Open(full)
	if err == nil {
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return nil, statErr
		}
		return &Cover{ReadSeeker: file, Size: info.Size(), ModTime: info.ModTime(), Local: true, closer: file}, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if _, statErr := os.Stat(full); statErr == nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	return nil, err
}

func (s *Store) openRemote(ctx context.Context, rel string) (*Cover, error) {
	return s.fetch(ctx, s.key(rel))
}

// fetch reads one object into memory, remembering a miss.
func (s *Store) fetch(ctx context.Context, key string) (*Cover, error) {
	if s.misses.has(key) {
		return nil, ErrNotFound
	}
	body, err := s.client.Get(ctx, key, 0, 0, "")
	if err != nil {
		return nil, s.remember(key, err)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, maxRemoteCover+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRemoteCover {
		return nil, fmt.Errorf("%s is over %d bytes", key, maxRemoteCover)
	}
	return &Cover{ReadSeeker: bytes.NewReader(data), Size: int64(len(data)), ModTime: body.LastModified}, nil
}

// remember keeps a real miss. An outage is not one: forgetting that keeps
// covers away for ten minutes after the store is back.
func (s *Store) remember(key string, err error) error {
	if errors.Is(err, objectstore.ErrNotFound) {
		s.misses.add(key)
		return ErrNotFound
	}
	return err
}

// Stat is a cover's size, without reading it.
func (s *Store) Stat(ctx context.Context, base, rel string) (int64, error) {
	full, err := safepath.Resolve(base, rel)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(full)
	if err == nil {
		return info.Size(), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if s.client == nil {
		return 0, ErrNotFound
	}
	key := s.key(rel)
	if s.misses.has(key) {
		return 0, ErrNotFound
	}
	remote, err := s.client.Head(ctx, key)
	if err != nil {
		return 0, s.remember(key, err)
	}
	return remote.Size, nil
}

// ModTimeOnDisk is the date of a cover on disk; false when there is none. It
// never asks object storage, so it is cheap enough for every request.
func ModTimeOnDisk(base, rel string) (time.Time, bool) {
	full, err := safepath.Resolve(base, rel)
	if err != nil {
		return time.Time{}, false
	}
	info, err := os.Stat(full)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// key is the object key of a cover.
func (s *Store) key(rel string) string { return s.keyUnder(s.prefix, rel) }

func (s *Store) keyUnder(prefix, rel string) string {
	return prefix + strings.TrimLeft(filepath.ToSlash(filepath.Clean(rel)), "/")
}

// missCache remembers objects the store does not have, bounded in both number
// and age, so that a book without a cover is not one request per view.
type missCache struct {
	mu    sync.Mutex
	limit int
	ttl   time.Duration
	now   func() time.Time
	order *list.List
	items map[string]*list.Element
}

type miss struct {
	key     string
	expires time.Time
}

func newMissCache(limit int, ttl time.Duration) *missCache {
	return &missCache{limit: limit, ttl: ttl, now: time.Now, order: list.New(), items: map[string]*list.Element{}}
}

func (c *missCache) has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return false
	}
	if c.now().After(element.Value.(*miss).expires) {
		c.order.Remove(element)
		delete(c.items, key)
		return false
	}
	return true
}

// remove forgets a miss that has since stopped being one.
func (c *missCache) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		c.order.Remove(element)
		delete(c.items, key)
	}
}

func (c *missCache) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		element.Value.(*miss).expires = c.now().Add(c.ttl)
		c.order.MoveToFront(element)
		return
	}
	c.items[key] = c.order.PushFront(&miss{key: key, expires: c.now().Add(c.ttl)})
	if c.order.Len() > c.limit {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*miss).key)
	}
}
