package archivestore

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"gopds-api/internal/objectstore"
)

const (
	// minBlock is the smallest read ahead. archive/zip asks for a few
	// kilobytes at a time; every one of those must not be a request.
	minBlock = 256 << 10
	// maxBlock is the most one blind read ahead may ask for. Growth is what
	// keeps a run of adjacent entries from paying a request per block.
	maxBlock = 8 << 20
	// skipLimit is how far an open stream is read through and thrown away
	// rather than abandoned for a new request.
	skipLimit = 64 << 10
	// localHeaderSlack covers a local header's fixed part, its extra field,
	// and a data descriptor, none of which the central directory sizes.
	localHeaderSlack = 1 << 10
	// maxEntrySpan keeps a lying directory's number well inside int64.
	maxEntrySpan = 1 << 40
	// blockGrowth is how fast the read ahead grows over a long read.
	blockGrowth = 2
)

// entrySpan estimates the bytes an entry occupies in its archive.
func entrySpan(f *zip.File) int64 {
	// #nosec G115 -- clamped first. The number is the archive's word, and all
	// it can do is ask for a longer range than the reader goes on to consume.
	compressed := int64(min(f.CompressedSize64, maxEntrySpan))
	return compressed + int64(len(f.Name)) + int64(len(f.Extra)) + localHeaderSlack
}

// rangedReader is an io.ReaderAt over one version of one object.
//
// The bytes of the central directory are in memory. Everything else comes
// through one open stream, which a forward read continues and any other read
// replaces: archive/zip reads a local header and then the entry straight after
// it, so a book is one stream however many small reads it is made of. Nothing
// is buffered, so the memory cost of reading a book of any size is what the
// caller holds.
//
// A reader belongs to one open, not to the cached directory: it carries that
// request's context, and a stream cannot be shared anyway.
type rangedReader struct {
	ctx    context.Context
	client *objectstore.Client
	key    string
	dir    *directory

	mu     sync.Mutex
	stream *objectstore.Body
	pos    int64 // next byte of the stream
	end    int64 // first byte past what the stream was asked for
	block  int64 // size of the next blind read ahead
	hint   int64 // size of the next read ahead, when known
}

func newRangedReader(ctx context.Context, client *objectstore.Client, key string, dir *directory) *rangedReader {
	return &rangedReader{ctx: ctx, client: client, key: key, dir: dir, block: minBlock}
}

// expect sizes the next request: an entry about to be read.
func (r *rangedReader) expect(span int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hint = span
}

// ReadAt reads from the directory or the object.
func (r *rangedReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("archivestore: negative offset")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	read := 0
	for read < len(p) {
		if off >= r.dir.info.Size {
			return read, io.EOF
		}
		if off >= r.dir.tailOffset {
			read += copy(p[read:], r.dir.tail[off-r.dir.tailOffset:])
			off = r.dir.tailOffset + int64(len(r.dir.tail))
			continue
		}
		limit := min(int64(len(p)-read), r.dir.tailOffset-off)
		n, err := r.readStream(p[read:read+int(limit)], off)
		read += n
		off += int64(n)
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

// readStream fills p from the object, retrying a broken stream a bounded
// number of times from where it stopped.
func (r *rangedReader) readStream(p []byte, off int64) (int, error) {
	read := 0
	failures := 0
	for read < len(p) {
		if err := r.position(off); err != nil {
			return read, err
		}
		n, err := r.stream.Read(p[read:])
		read += n
		off += int64(n)
		r.pos += int64(n)
		switch {
		case err == nil:
		case errors.Is(err, io.EOF) && r.pos >= r.end:
			r.closeStream()
		default:
			r.closeStream()
			failures++
			if r.ctx.Err() != nil || failures > r.client.Retries() {
				return read, err
			}
		}
	}
	return read, nil
}

// position leaves an open stream at off.
func (r *rangedReader) position(off int64) error {
	if r.stream != nil {
		if off >= r.pos && off-r.pos <= skipLimit && off < r.end {
			if off > r.pos {
				if _, err := io.CopyN(io.Discard, r.stream, off-r.pos); err != nil {
					r.closeStream()
					return err
				}
				r.pos = off
			}
			return nil
		}
		r.closeStream()
	}
	if off == r.end && r.hint == 0 {
		// Reading on from where the last request stopped: this is a long read,
		// and it should cost fewer and fewer requests.
		r.block = min(r.block*blockGrowth, maxBlock)
	}
	length := r.block
	if r.hint > 0 {
		length = r.hint
	}
	r.hint = 0
	if off+length > r.dir.tailOffset {
		length = r.dir.tailOffset - off
	}
	stream, err := r.client.Get(r.ctx, r.key, off, length, r.dir.info.ETag)
	if err != nil {
		return err
	}
	if stream.Size != r.dir.info.Size {
		_ = stream.Close()
		return fmt.Errorf("%w: %s", objectstore.ErrChanged, r.key)
	}
	r.stream, r.pos, r.end = stream, off, off+length
	return nil
}

func (r *rangedReader) closeStream() {
	if r.stream != nil {
		_ = r.stream.Close()
		r.stream = nil
	}
}

// Close ends any open stream. The reader stays usable: a later read opens a
// new one.
func (r *rangedReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeStream()
	return nil
}
