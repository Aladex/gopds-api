package tasks

import (
	"gopds-api/logging"
	"os"
	"path/filepath"
	"time"
)

// WatchDirectory monitors a directory and deletes files older than an hour.
func WatchDirectory(dirPath string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				logging.Infof("Error accessing path %s: %v", path, err)
				return nil
			}

			// Skip directories
			if info.IsDir() {
				return nil
			}

			// Check file age
			if time.Since(info.ModTime()) > time.Hour {
				logging.Infof("Deleting file: %s (last modified: %v)", path, info.ModTime())
				err := os.Remove(path)
				if err != nil {
					logging.Infof("Failed to delete file %s: %v", path, err)
				}
			}

			return nil
		})

		if err != nil {
			logging.Infof("Error walking the directory %s: %v", dirPath, err)
		}
	}
}
