package main

import (
	"gopds-api/config"
	"gopds-api/logging"
)

var cfg *config.Config

func init() {
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
