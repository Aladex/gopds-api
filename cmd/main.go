// main.go

package main

import (
	"context"
	"errors"
	"gopds-api/database"
	_ "gopds-api/docs" // Import to include documentation for Swagger UI
	"gopds-api/logging"
	"gopds-api/sessions"
	"gopds-api/tasks" // Import the tasks package for WatchDirectory
	"gopds-api/telegram"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gin-gonic/gin"
)

// @title GOPDS API
// @version 1.0
// @description GOPDS API for a comprehensive book management system
// @contact.name API Support
// @contact.email aladex@gmail.com
// @BasePath /api

// Global variable for the Telegram bot manager
var telegramBotManager *telegram.BotManager

func main() {
	db := initializeDatabase()
	defer closeDatabaseConnection(db)
	database.SetDB(db)

	mainRedisClient, tokenRedisClient := initializeSessionManagement()
	sessions.SetRedisConnections(mainRedisClient, tokenRedisClient)

	// Initialize the Telegram bot manager
	telegramConfig := &telegram.Config{
		BaseURL: cfg.GetServerBaseURL(), // Need to add this function to config
	}
	telegramBotManager = telegram.NewBotManager(telegramConfig, mainRedisClient)

	// Initialize Telegram components
	telegram.InitializeTelegram(telegramBotManager)

	// Link BotManager with the database package for admin panel integration
	database.SetTelegramBotManager(telegramBotManager)

	// Set the Gin mode based on the application configuration.
	if !cfg.App.DevelMode {
		gin.SetMode(gin.ReleaseMode)
	}

	ensureUserPathExists(cfg.App.UsersPath)
	ensureUserPathExists(cfg.App.MobiConversionDir)

	// Start watching the directory for e-book conversion tasks
	go tasks.WatchDirectory(cfg.App.MobiConversionDir, 10*time.Minute)

	route := gin.New()
	setupMiddleware(route)
	setupRoutes(route)

	server := &http.Server{
		Addr:           cfg.GetServerAddress(),
		Handler:        route,
		ReadTimeout:    time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout:   time.Duration(cfg.Server.WriteTimeout) * time.Second,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}

	// Channel to listen for server start errors
	serverErrors := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		logging.Infof("Server is starting at http://%s", cfg.GetServerAddress())
		serverErrors <- server.ListenAndServe()
	}()

	// Wait for server to start and then log successful start message
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Errorf("Could not listen on %s: %v\n", server.Addr, err)
			os.Exit(1)
		}
	case <-time.After(1 * time.Second):
		logging.Info("Server started successfully")
	}

	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 5 seconds.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)

	<-quit
	logging.Info("Server is shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logging.Errorf("Server forced to shutdown: %v", err)
		os.Exit(1)
	}

	logging.Info("Server exited")
}
