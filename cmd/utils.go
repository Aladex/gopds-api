package main

import (
	"github.com/sirupsen/logrus"
	"os"
)

// ensureUserPathExists checks if the specified path exists and creates it if it does not.
// It is used to ensure necessary directories are available at application start.
func ensureUserPathExists(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		logrus.Fatalln(os.MkdirAll(path, 0755))
	}
}
