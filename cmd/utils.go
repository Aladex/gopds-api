package main

import (
	"gopds-api/logging"
	"os"
)

// ensureUserPathExists checks if the specified path exists and creates it if it does not.
// It is used to ensure necessary directories are available at application start.
func ensureUserPathExists(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0755); err != nil {
			logging.Errorf("Failed to create directory %s: %v", path, err)
			panic(err)
		}
		logging.Infof("Created directory: %s", path)
	}
}
