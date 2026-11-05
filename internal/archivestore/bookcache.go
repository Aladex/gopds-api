package archivestore

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopds-api/internal/safeio"
	"gopds-api/logging"
)

// maxCachedShare keeps one huge book from flushing the cache: an entry over
// this fraction of it is served but not kept.
const maxCachedShare = 4

const tempSuffix = ".tmp"

// bookCacheKey names one entry of one version of one archive. A replaced
// archive therefore misses, and its old books age out on their own.
func bookCacheKey(key, etag, name string) string {
	sum := sha256.Sum256([]byte(key + "\x00" + etag + "\x00" + name))
	return hex.EncodeToString(sum[:])
}

// bookCache keeps extracted books on disk, LRU by size. A nil cache is valid
// and keeps nothing, so that a limit of zero needs no second code path.
//
// A book is stored only after archive/zip has reached the end of the entry,
// which is where it verifies the checksum: a broken download is never served
// back. The cache is rebuilt from the directory at start, so it survives a
// restart.
type bookCache struct {
	dir   string
	limit int64

	mu    sync.Mutex
	total int64
	order *list.List
	items map[string]*list.Element
}

type cachedBook struct {
	key  string
	size int64
}

func newBookCache(dir string, limit int64) (*bookCache, error) {
	if err := os.MkdirAll(dir, safeio.DirMode); err != nil {
		return nil, err
	}
	c := &bookCache{dir: dir, limit: limit, order: list.New(), items: map[string]*list.Element{}}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// load rebuilds the index, oldest first, and clears unfinished files.
func (c *bookCache) load() error {
	type found struct {
		key      string
		size     int64
		modified time.Time
	}
	var books []found
	err := filepath.WalkDir(c.dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(name, tempSuffix) {
			removeFile(name)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		books = append(books, found{key: filepath.Base(name), size: info.Size(), modified: info.ModTime()})
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(books, func(i, j int) bool { return books[i].modified.Before(books[j].modified) })
	for _, b := range books {
		c.add(b.key, b.size)
	}
	return nil
}

func (c *bookCache) path(key string) string {
	return filepath.Join(c.dir, key[:2], key)
}

// add indexes a stored book and evicts down to the limit.
func (c *bookCache) add(key string, size int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		c.total -= element.Value.(*cachedBook).size
		c.order.Remove(element)
	}
	c.items[key] = c.order.PushFront(&cachedBook{key: key, size: size})
	c.total += size
	for c.total > c.limit {
		c.evictOldest()
	}
}

func (c *bookCache) evictOldest() {
	oldest := c.order.Back().Value.(*cachedBook)
	c.dropLocked(oldest.key)
	removeFile(c.path(oldest.key))
}

func (c *bookCache) dropLocked(key string) {
	element, ok := c.items[key]
	if !ok {
		return
	}
	c.total -= element.Value.(*cachedBook).size
	c.order.Remove(element)
	delete(c.items, key)
}

// open returns a stored book, nil on a miss. A file evicted while it is being
// read stays readable through the descriptor.
func (c *bookCache) open(key string) io.ReadCloser {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	element, ok := c.items[key]
	if ok {
		c.order.MoveToFront(element)
	}
	c.mu.Unlock()
	if !ok {
		return nil
	}
	name := c.path(key)
	// #nosec G304 -- name is built from the cache directory and a SHA-256 hex
	// digest; no part of it is text from a request.
	file, err := os.Open(name)
	if err != nil {
		c.mu.Lock()
		c.dropLocked(key)
		c.mu.Unlock()
		return nil
	}
	// The file's date is the LRU order a restart will read back.
	now := time.Now()
	if err := os.Chtimes(name, now, now); err != nil {
		logging.Debugf("archivestore: touching %s: %v", name, err)
	}
	return file
}

// tee returns rc with the book being written to the cache as it is read. A
// book too large to keep, or a cache that cannot be written, leaves the read
// alone: the cache must never be why a book fails.
func (c *bookCache) tee(key string, size uint64, rc io.ReadCloser) io.ReadCloser {
	// #nosec G115 -- the limit comes from the configuration and is positive:
	// New builds no cache for a limit of zero, and validation refuses less.
	if c == nil || size == 0 || size > uint64(c.limit/maxCachedShare) {
		return rc
	}
	final := c.path(key)
	if err := os.MkdirAll(filepath.Dir(final), safeio.DirMode); err != nil {
		logging.Warnf("archivestore: book cache: %v", err)
		return rc
	}
	temp, err := os.CreateTemp(filepath.Dir(final), key[:8]+"-*"+tempSuffix)
	if err != nil {
		logging.Warnf("archivestore: book cache: %v", err)
		return rc
	}
	return &teeReader{cache: c, key: key, final: final, source: rc, temp: temp}
}

// teeReader copies a stream into a temporary file, and stores the file if the
// stream reaches its verified end.
type teeReader struct {
	cache   *bookCache
	key     string
	final   string
	source  io.ReadCloser
	temp    *os.File
	written int64
}

// Read reads the book and keeps a copy.
func (t *teeReader) Read(p []byte) (int, error) {
	n, err := t.source.Read(p)
	if n > 0 && t.temp != nil {
		if _, werr := t.temp.Write(p[:n]); werr != nil {
			logging.Warnf("archivestore: book cache: %v", werr)
			t.abandon()
		} else {
			t.written += int64(n)
		}
	}
	if errors.Is(err, io.EOF) && t.temp != nil {
		t.store()
	}
	return n, err
}

func (t *teeReader) store() {
	temp := t.temp
	t.temp = nil
	if err := temp.Close(); err != nil {
		removeFile(temp.Name())
		return
	}
	if err := os.Rename(temp.Name(), t.final); err != nil {
		logging.Warnf("archivestore: book cache: %v", err)
		removeFile(temp.Name())
		return
	}
	t.cache.add(t.key, t.written)
}

func (t *teeReader) abandon() {
	if t.temp == nil {
		return
	}
	_ = t.temp.Close()
	removeFile(t.temp.Name())
	t.temp = nil
}

// Close ends the read. A book not read to its end is not kept.
func (t *teeReader) Close() error {
	t.abandon()
	return t.source.Close()
}
