// main.go

package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"gopds-api/database"
	_ "gopds-api/internal/swaggerdocs" // Import to include documentation for Swagger UI
	"gopds-api/logging"
	"gopds-api/middlewares"
	"gopds-api/services"
	"gopds-api/sessions"
	"gopds-api/tasks" // Import the tasks package for WatchDirectory
	"gopds-api/telegram"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
)

// @title GOPDS API
// @version 1.0
// @description GOPDS API for a comprehensive book management system
// @contact.name API Support
// @contact.email aladex@gmail.com
// @BasePath /api

// Global variable for the Telegram service
var telegramService *telegram.TelegramService

// previewService is the book-preview pipeline, assembled from configuration
// alongside the other dependencies. The phase-4 HTTP handlers consume it.
var previewService *services.PreviewService

// conversionWatchInterval is how often the e-book conversion directory is
// checked.
const conversionWatchInterval = 10 * time.Minute

func main() {
	os.Exit(run())
}

// run is the whole server lifetime. Its exit code goes to os.Exit only after
// it returned, so every deferred shutdown below — including the author
// metadata workers and the database pool — also runs on the error paths.
func run() (code int) {
	loadConfiguration()

	db := initializeDatabase()
	database.SetDB(db)

	// The author metadata workers start only once the database answers and
	// stop before its pool closes: one deferred call keeps that order, and,
	// registered first, it still runs after every other deferred shutdown.
	authorMetadata := initializeAuthorMetadata(db, &cfg.AuthorMetadata)
	defer func() {
		if err := shutdownAuthorMetadataThenDatabase(authorMetadata, db, authorMetadataShutdownTimeout); err != nil && code == 0 {
			code = 1
		}
	}()

	// One search service for every adapter, built on the same pool the
	// package-global database helpers use.
	searchService := services.NewSearchService(database.NewPGSearchRepository(db))

	mainRedisClient, tokenRedisClient := initializeSessionManagement()
	sessions.SetRedisConnections(mainRedisClient, tokenRedisClient)
	rateLimitRedis := sessions.RedisConnection(2, cfg)
	middlewares.SetRateLimitRedis(rateLimitRedis)

	// The preview pipeline is built where the rest of the dependencies are,
	// from the same configuration; its Redis is the preview's own (or the
	// shared instance's preview DB when preview.redis.* is unset).
	previewService = initializePreviewService()
	defer previewService.Shutdown()

	// Telegram: bot manager, service and its periodic health checks.
	stopTelegramHealthCheck := initializeTelegram(mainRedisClient, searchService)
	defer stopTelegramHealthCheck()

	// Set the Gin mode based on the application configuration.
	if !cfg.App.DevelMode {
		gin.SetMode(gin.ReleaseMode)
	}

	ensureUserPathExists(cfg.App.UsersPath)
	ensureUserPathExists(cfg.App.MobiConversionDir)

	// Initialize application services (WebSocket manager, etc.)
	initializeServices()

	// Start watching the directory for e-book conversion tasks
	go tasks.WatchDirectory(cfg.App.MobiConversionDir, conversionWatchInterval)

	route := gin.New()
	setupMiddleware(route)
	setupRoutes(route, cfg.Donate, searchService)

	return serveUntilShutdown(route)
}

// initializeTelegram sets up the Telegram bot manager and service and starts
// the bots' periodic health checks. It returns the function that stops them;
// run defers it at the point where the health checks used to be deferred.
func initializeTelegram(mainRedisClient *redis.Client, searchService *services.SearchService) (stopHealthCheck func()) {
	// Initialize the Telegram bot manager
	telegramConfig := &telegram.Config{
		BaseURL: cfg.GetTelegramWebhookBaseURL(),
	}
	telegramBotManager := telegram.NewBotManager(telegramConfig, mainRedisClient, searchService)

	// Initialize Telegram service
	var err error
	telegramService, err = telegram.NewTelegramService(telegramBotManager)
	if err != nil {
		logging.Errorf("Failed to initialize Telegram service: %v", err)
		// Continue without Telegram functionality
		telegramService = nil
	}

	// Start periodic health checks for Telegram bots
	healthCheckCtx, healthCheckCancel := context.WithCancel(context.Background())
	if telegramService != nil {
		telegramService.GetBotManager().StartHealthCheck(healthCheckCtx, 24*time.Hour)
	}

	// Link BotManager with the database package for admin panel integration
	database.SetTelegramBotManager(telegramBotManager)
	return healthCheckCancel
}

// serveUntilShutdown serves HTTP until a shutdown signal arrives, then shuts
// the server down with a timeout of 5 seconds. It returns the exit code.
func serveUntilShutdown(route *gin.Engine) int {
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
			return 1
		}
	case <-time.After(1 * time.Second):
		logging.Info("Server started successfully")
	}

	// Wait for a shutdown signal to gracefully shutdown the server with
	// a timeout of 5 seconds.
	quit := notifyShutdown()

	<-quit
	logging.Info("Server is shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logging.Errorf("Server forced to shutdown: %v", err)
		return 1
	}

	logging.Info("Server exited")
	return 0
}
