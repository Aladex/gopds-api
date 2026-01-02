package main

import (
	"gopds-api/api"
	"gopds-api/logging"
)

// initializeServices initializes application services
func initializeServices() {
	// Initialize WebSocket manager for admin notifications
	api.InitWebSocketManager()
	logging.Info("Application services initialized")
}
