package tasks

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopds-api/logging"
)

// MaxAge is how long a converted file is kept before the sweep removes it.
const MaxAge = time.Hour

// WatchDirectory removes files under dirPath once they are older than MaxAge.
//
// The directory it is pointed at is scratch space for format conversion:
// everything in it is a by-product that some earlier request has already been
// served. Nothing here is a library file.
func WatchDirectory(dirPath string, interval time.Duration) {
	// An empty path would send the sweep at the working directory, which is
	// where conversion drops its own temporary files. Refusing to start is
	// louder than deleting the wrong tree an hour later.
	if strings.TrimSpace(dirPath) == "" {
		logging.Errorf("Not watching for old conversions: no directory configured")
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		sweep(dirPath)
	}
}

// sweep deletes every regular file under root last modified more than MaxAge
// ago, and reports how many it removed.
//
// Split out from the ticker so it can be tested: a loop that only ever runs on
// a timer cannot be asked what it does.
func sweep(root string) int {
	// Opened as a root, so every path below is resolved inside it by the
	// kernel. Walking with plain filepath functions and deleting by the path
	// they hand back leaves a window in which a directory turns into a symlink
	// between being seen and being acted on — and the deletion then lands
	// somewhere else entirely.
	dir, err := os.OpenRoot(root)
	if err != nil {
		logging.Infof("Error opening the conversion directory %s: %v", root, err)
		return 0
	}
	defer func() {
		if closeErr := dir.Close(); closeErr != nil {
			logging.Infof("Error closing the conversion directory %s: %v", root, closeErr)
		}
	}()

	cutoff := time.Now().Add(-MaxAge)
	removed := 0

	err = fs.WalkDir(dir.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			logging.Infof("Error accessing path %s: %v", path, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}

		// Only regular files. A symbolic link is reported as itself rather
		// than as what it points at, so removing one would silently take out
		// whatever had been linked in — and sockets and devices are not
		// leftovers of a conversion at all.
		if !d.Type().IsRegular() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			logging.Infof("Error reading info for %s: %v", path, err)
			return nil
		}
		if info.ModTime().After(cutoff) {
			return nil
		}

		logging.Infof("Deleting file: %s (last modified: %v)", filepath.Join(root, path), info.ModTime())
		if err := dir.Remove(path); err != nil {
			logging.Infof("Failed to delete file %s: %v", path, err)
			return nil
		}
		removed++
		return nil
	})
	if err != nil {
		logging.Infof("Error walking the directory %s: %v", root, err)
	}

	return removed
}
