// Package safeio copies from sources that are not trusted to say how big they
// are.
//
// Everything the catalog serves comes out of a zip: a book is an entry in a
// library archive, opened and copied to a temporary file, to an HTTP response,
// or into a hash. A zip entry declares its uncompressed size in the central
// directory, and that number is written by whoever made the archive. A small
// file can declare a small size and expand to fill a disk.
package safeio

import (
	"errors"
	"fmt"
	"io"
)

// MaxBookBytes is the ceiling for one book taken out of a library archive.
//
// An FB2 file is XML; even carrying embedded cover art and illustrations, real
// books in this library run to tens of megabytes. Half a gigabyte is far above
// anything the catalog holds and far below what a decompression bomb reaches,
// which is the gap the limit needs to sit in.
const MaxBookBytes = 512 << 20

// ErrTooLarge reports that the source held more than the caller allowed.
var ErrTooLarge = errors.New("source is larger than the allowed limit")

// Copy writes at most limit bytes from src to dst.
//
// It reads one byte past the limit to tell "exactly at the limit" from "more
// than fits", so a source that is bigger fails rather than arriving silently
// truncated — a half-copied book is worse than a refused one, because nothing
// downstream can tell it happened.
func Copy(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit < 0 {
		return 0, fmt.Errorf("limit must not be negative, got %d", limit)
	}

	written, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return written, err
	}
	if written > limit {
		return limit, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}
	return written, nil
}

// CopyBuffer is Copy through a caller-supplied buffer, for hot paths that
// already keep one around.
func CopyBuffer(dst io.Writer, src io.Reader, buf []byte, limit int64) (int64, error) {
	if limit < 0 {
		return 0, fmt.Errorf("limit must not be negative, got %d", limit)
	}

	written, err := io.CopyBuffer(dst, io.LimitReader(src, limit+1), buf)
	if err != nil {
		return written, err
	}
	if written > limit {
		return limit, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}
	return written, nil
}
