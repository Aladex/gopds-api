package archivestore

import (
	"archive/zip"
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"gopds-api/internal/objectstore"
	"gopds-api/logging"
)

const (
	// tailChunk is how much of an archive's end is fetched at a time. The
	// largest archives hold 6,000 entries at about 60 bytes each, so one
	// chunk is normally the whole central directory.
	tailChunk = 1 << 20
	// maxTailBytes refuses a file that claims an absurd directory.
	maxTailBytes = 64 << 20
)

// directory is what is kept of an archive between opens: the raw bytes from
// the start of its central directory to the end of the file, and the version
// they belong to.
//
// The bytes rather than the parsed zip.Reader, because a zip.File is tied to
// the ReaderAt that parsed it: a shared one could not carry each request's
// context or its stream. Parsing 6,000 headers from memory takes milliseconds
// and no requests, and the raw form is the smaller of the two.
type directory struct {
	info       objectstore.Info
	tail       []byte
	tailOffset int64
	checked    time.Time
	// unverified is why this copy could not be checked against the store. It
	// is good for the entries it lists; one it lacks proves nothing. Only the
	// copy handed to the callers of that load has it, never the cached one.
	unverified error
}

func (d *directory) weight() int64 { return int64(len(d.tail)) }

// directory returns an archive's directory, from the cache when it is known
// and recent. Concurrent first opens of one archive share one load.
func (s *Store) directory(ctx context.Context, key string) (*directory, error) {
	cached := s.directories.get(key)
	if cached != nil && s.now().Sub(cached.checked) < directoryRevalidate {
		return cached, nil
	}
	// The load outlives the caller that started it: the others are waiting on
	// it too. The client's timeouts only bound silence, and a store that keeps
	// dripping is never silent, so the load has a deadline of its own.
	result := s.loads.DoChan(key, func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.readBudget)
		defer cancel()
		return s.loadDirectory(loadCtx, key, cached)
	})
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	case done := <-result:
		if done.Err != nil {
			return nil, done.Err
		}
		return done.Val.(*directory), nil
	}
}

func (s *Store) loadDirectory(ctx context.Context, key string, cached *directory) (*directory, error) {
	info, err := s.client.Head(ctx, key)
	if err != nil {
		if cached != nil && errors.Is(err, ErrUnavailable) {
			// An outage is no reason to forget a directory that was good a
			// moment ago: every ranged read still checks the version.
			logging.Warnf("archivestore: revalidating %s: %v", key, err)
			stale := *cached
			stale.unverified = err
			return &stale, nil
		}
		return nil, err
	}
	if cached != nil && cached.info.ETag == info.ETag && cached.info.Size == info.Size {
		fresh := *cached
		fresh.checked = s.now()
		s.directories.put(key, &fresh)
		return &fresh, nil
	}
	loader := &tailLoader{ctx: ctx, client: s.client, key: key, etag: info.ETag, size: info.Size, low: info.Size, start: info.Size}
	if _, err := zip.NewReader(loader, info.Size); err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, err
	}
	dir := &directory{info: info, tail: loader.tailFrom(loader.low), tailOffset: loader.low, checked: s.now()}
	s.directories.put(key, dir)
	return dir, nil
}

// tailLoader is the ReaderAt a first open parses: it pulls the end of the
// object into memory, growing backwards as archive/zip asks, and remembers the
// lowest offset asked for. That offset is where the central directory starts,
// found without parsing zip64 records by hand.
type tailLoader struct {
	ctx    context.Context
	client *objectstore.Client
	key    string
	etag   string
	size   int64
	buf    []byte // the object from start to size
	start  int64
	low    int64
}

// ReadAt reads the tail, fetching what is not there yet.
func (l *tailLoader) ReadAt(p []byte, off int64) (int, error) {
	if off >= l.size {
		return 0, io.EOF
	}
	if off < l.start {
		if err := l.grow(off); err != nil {
			return 0, err
		}
	}
	l.low = min(l.low, off)
	n := copy(p, l.buf[off-l.start:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (l *tailLoader) grow(off int64) error {
	from := max(0, min(off, l.start-tailChunk))
	if l.size-from > maxTailBytes {
		return fmt.Errorf("archivestore: %s has a directory beyond %d bytes: %w", l.key, int64(maxTailBytes), zip.ErrFormat)
	}
	body, err := l.client.Get(l.ctx, l.key, from, l.start-from, l.etag)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	chunk := bytes.NewBuffer(make([]byte, 0, l.start-from+int64(len(l.buf))))
	if _, err := chunk.ReadFrom(body); err != nil {
		return err
	}
	if int64(chunk.Len()) != l.start-from {
		return fmt.Errorf("%w: short range from %s", ErrUnavailable, l.key)
	}
	chunk.Write(l.buf)
	l.buf, l.start = chunk.Bytes(), from
	return nil
}

// tailFrom copies the bytes from off to the end, so that the cached directory
// does not pin the chunk it came in.
func (l *tailLoader) tailFrom(off int64) []byte {
	return bytes.Clone(l.buf[off-l.start:])
}

// directoryCache is an LRU of directories, bounded by bytes.
type directoryCache struct {
	mu    sync.Mutex
	limit int64
	total int64
	order *list.List
	items map[string]*list.Element
}

type directoryItem struct {
	key string
	dir *directory
}

func newDirectoryCache(limit int64) *directoryCache {
	return &directoryCache{limit: limit, order: list.New(), items: map[string]*list.Element{}}
}

func (c *directoryCache) get(key string) *directory {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return nil
	}
	c.order.MoveToFront(element)
	return element.Value.(*directoryItem).dir
}

func (c *directoryCache) put(key string, dir *directory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
	if dir.weight() > c.limit {
		return
	}
	c.items[key] = c.order.PushFront(&directoryItem{key: key, dir: dir})
	c.total += dir.weight()
	for c.total > c.limit {
		oldest := c.order.Back()
		c.removeLocked(oldest.Value.(*directoryItem).key)
	}
}

// expire keeps a directory but makes its next use ask the store.
func (c *directoryCache) expire(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		item := element.Value.(*directoryItem)
		stale := *item.dir
		stale.checked = time.Time{}
		item.dir = &stale
	}
}

func (c *directoryCache) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
}

func (c *directoryCache) removeLocked(key string) {
	element, ok := c.items[key]
	if !ok {
		return
	}
	c.total -= element.Value.(*directoryItem).dir.weight()
	c.order.Remove(element)
	delete(c.items, key)
}
