package archivestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopds-api/internal/objectstore"
	"gopds-api/internal/safeio"
	"gopds-api/logging"
)

// fetchSegment is one request of a temporary copy. A long copy is a run of
// them so that a broken connection costs one segment, not seven gigabytes.
const fetchSegment = 256 << 20

// fetchFailures is how many failed segments in a row end a copy.
const fetchFailures = 3

// prepareTempDir makes the temporary directory and clears what a crash left.
func (s *Store) prepareTempDir() error {
	if err := os.MkdirAll(s.tempDir, safeio.DirMode); err != nil {
		return err
	}
	leftovers, err := filepath.Glob(filepath.Join(s.tempDir, "*"+tempSuffix))
	if err != nil {
		return err
	}
	for _, name := range leftovers {
		logging.Warnf("archivestore: removing a leftover temporary copy %s", name)
		removeFile(name)
	}
	return nil
}

// OpenWhole opens an archive for a worker that reads most of it.
//
// A local archive opens as ever. One that is only in object storage is copied
// to a temporary file, which is removed when the Archive is closed, on any
// path. Only one copy exists at a time: the slot is held from before the
// download until Close, so a second worker waits, for as long as its context
// allows. A failed copy is one failed archive.
func (s *Store) OpenWhole(ctx context.Context, localPath string) (*Archive, error) {
	key, ok := s.key(localPath)
	if !ok || onDisk(localPath) {
		return localArchive(localPath)
	}

	select {
	case s.wholeSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-s.wholeSlot }

	temp, err := s.fetch(ctx, key)
	if err != nil {
		release()
		return nil, err
	}
	archive, err := localArchive(temp)
	if err != nil {
		removeFile(temp)
		release()
		return nil, err
	}
	closeArchive := archive.close
	archive.close = func() error {
		defer release()
		defer removeFile(temp)
		return closeArchive()
	}
	return archive, nil
}

// fetch copies one object to a new temporary file and returns its name. The
// file is gone again on any error.
func (s *Store) fetch(ctx context.Context, key string) (string, error) {
	info, err := s.client.Head(ctx, key)
	if err != nil {
		return "", mapError(err)
	}
	if info.Size > s.tempBudget {
		return "", fmt.Errorf("%w: %s is %d bytes, the budget is %d", ErrTooLarge, key, info.Size, s.tempBudget)
	}
	base := strings.NewReplacer("/", "_", `\`, "_").Replace(filepath.Base(key))
	file, err := os.CreateTemp(s.tempDir, base+"-*"+tempSuffix)
	if err != nil {
		return "", err
	}
	name := file.Name()
	err = s.download(ctx, file, info)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		removeFile(name)
		return "", mapError(err)
	}
	return name, nil
}

// download writes the object to file, resuming from the last good byte. A
// segment that made progress resets the count of failures; only a store that
// stopped delivering altogether ends the copy.
func (s *Store) download(ctx context.Context, file *os.File, info objectstore.Info) error {
	var off int64
	failures := 0
	for off < info.Size {
		n, err := s.downloadSegment(ctx, file, info, off)
		off += n
		if err == nil {
			continue
		}
		// A replaced object, a full disk, a caller that left: trying again
		// cannot help.
		if !errors.Is(err, ErrUnavailable) || ctx.Err() != nil {
			return err
		}
		if n > 0 {
			failures = 0
			continue
		}
		failures++
		if failures >= fetchFailures {
			return err
		}
	}
	return nil
}

func (s *Store) downloadSegment(ctx context.Context, file *os.File, info objectstore.Info, off int64) (int64, error) {
	length := min(info.Size-off, int64(fetchSegment))
	body, err := s.client.Get(ctx, info.Key, off, length, info.ETag)
	if err != nil {
		return 0, err
	}
	defer func() { _ = body.Close() }()
	if body.Size != info.Size {
		return 0, fmt.Errorf("%w: %s", objectstore.ErrChanged, info.Key)
	}
	// A partial segment may have written less than it read; the file position
	// is what the next segment continues from, and both move together.
	n, err := io.Copy(file, body)
	if err == nil && n != length {
		err = fmt.Errorf("%w: short range from %s", ErrUnavailable, info.Key)
	}
	return n, err
}
