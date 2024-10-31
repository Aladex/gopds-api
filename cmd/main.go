// main.go

package main

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopds-api/api"
	"gopds-api/database"
	_ "gopds-api/docs" // Import to include documentation for Swagger UI
	"gopds-api/sessions"
	"gopds-api/tasks" // Import the tasks package
	"net/http"
	"os"
	"os/signal"
	"time"
)

// @title GOPDS API
// @version 1.0
// @description GOPDS API for a comprehensive book management system
// @contact.name API Support
// @contact.email aladex@gmail.com
// @BasePath /api
func main() {
	db := initializeDatabase()
	defer closeDatabaseConnection(db)
	database.SetDB(db)

	mainRedisClient, tokenRedisClient := initializeSessionManagement()
	sessions.SetRedisConnections(mainRedisClient, tokenRedisClient)

	// Set the Gin mode based on the application configuration.
	if !viper.GetBool("app.devel_mode") {
		gin.SetMode(gin.ReleaseMode)
	}

	ensureUserPathExists(viper.GetString("app.users_path"))
	ensureUserPathExists(viper.GetString("app.mobi_conversion_dir"))

	// Start watching the directory for e-book conversion tasks
	go tasks.WatchDirectory(viper.GetString("app.mobi_conversion_dir"), 10*time.Minute)

	route := gin.New()
	setupMiddleware(route)
	setupRoutes(route)

	// Initialize TaskManager
	taskManager := tasks.NewTaskManager()
	api.SetTaskManager(taskManager)

	server := &http.Server{
		Addr:           ":8085",
		Handler:        route,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Channel to listen for server start errors
	serverErrors := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		logrus.Info("Server is starting at http://127.0.0.1:8085")
		serverErrors <- server.ListenAndServe()
	}()

	// Wait for server to start and then log successful start message
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.Fatalf("Could not listen on %s: %v\n", server.Addr, err)
		}
	case <-time.After(1 * time.Second):
		logrus.Info("Server started successfully")
	}

	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 5 seconds.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	logrus.Info("Shutting down server...")

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logrus.Fatal("Server forced to shutdown:", err)
	}

	// Stop all workers before exiting
	taskManager.StopAllWorkers()

	logrus.Info("Server exiting")
}
