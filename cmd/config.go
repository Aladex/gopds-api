package main

import (
	"gopds-api/config"
	"gopds-api/logging"
)

var cfg *config.Config

// loadConfiguration loads the application configuration and prepares the list of
// embedded frontend directories. It runs at the start of main rather than from an
// init function: initializing a package must not perform I/O or panic, otherwise
// the package cannot be tested at all — every test in it would abort before
// starting, with no way to prepare the environment first.
func loadConfiguration() {
	var err error
	cfg, err = config.Load()
	if err != nil {
		logging.Errorf("Failed to load configuration: %v", err)
		panic(err)
	}

	// Initialize dist folders for static files
	if err := initializeDistFolders(); err != nil {
		logging.Errorf("Error initializing dist folders: %v", err)
		panic(err)
	}
}
