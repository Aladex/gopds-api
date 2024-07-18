package main

import (
	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/spf13/viper"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gopds-api/api"
	"gopds-api/database"
	_ "gopds-api/docs" // Import to include documentation for Swagger UI
	"gopds-api/logging"
	"gopds-api/middlewares"
	"gopds-api/opds"
	"log"
	"net/http"
	"os"
	"time"
)

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Fatal error config file: %s \n", err)
	}
}

// setupMiddleware configures global middleware for the gin.Engine instance.
// It includes a custom logger and, if in development mode, a CORS middleware.
func setupMiddleware(route *gin.Engine) {
	route.Use(logging.GinrusLogger())
	if viper.GetBool("app.devel_mode") {
		route.Use(corsOptionsMiddleware())
	}
}

// setupRoutes defines all route handlers and groups them by their functionality.
// It includes routes for Swagger UI, file handling, default operations, OPDS feed, API, admin, and Telegram bot interactions.
func setupRoutes(route *gin.Engine) {
	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	setupFileRoutes(route.Group("/files", middlewares.TokenMiddleware()))
	setupDefaultRoutes(route)
	setupOpdsRoutes(route.Group("/opds", middlewares.BasicAuth()))
	setupApiRoutes(route.Group("/api", middlewares.AuthMiddleware()))
}

// setupFileRoutes configures routes related to file operations.
func setupFileRoutes(group *gin.RouterGroup) {
	group.GET("/books/get/:format/:id", api.GetBookFile)
}

// setupDefaultRoutes configures default routes for the application.
func setupDefaultRoutes(route *gin.Engine) {
	route.GET("/book-posters/:book", api.GetBookPoster)
	route.GET("/status", api.StatusCheck)
	route.POST("/api/login", api.AuthCheck)
	route.POST("/api/register", api.Registration)
	route.POST("/api/change-password", api.ChangeUserState)
	route.POST("/api/change-request", api.ChangeRequest)
	route.POST("/api/token", api.TokenValidation)
	route.GET("/api/logout", api.LogOut)
	route.GET("/api/drop-sessions", api.DropAllSessions)
}

// setupOpdsRoutes configures routes for OPDS feed interactions.
func setupOpdsRoutes(group *gin.RouterGroup) {
	opds.SetupOpdsRoutes(group)
}

// setupApiRoutes configures API routes for book operations and other functionalities.
func setupApiRoutes(group *gin.RouterGroup) {
	booksGroup := group.Group("/books")
	api.SetupBookRoutes(booksGroup)
	// Setup admin routes with admin middleware
	adminGroup := group.Group("/admin", middlewares.AdminMiddleware())
	setupAdminRoutes(adminGroup)
}

// setupAdminRoutes configures routes for administrative functionalities.
func setupAdminRoutes(group *gin.RouterGroup) {
	api.SetupAdminRoutes(group)
}

// corsOptionsMiddleware returns a middleware that enables CORS support.
// It is only used in development mode for easier testing and development.
func corsOptionsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "authorization, origin, content-type, accept, token")
			c.Header("Allow", "HEAD,GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Content-Type", "application/json")
			c.AbortWithStatus(http.StatusOK)
		} else {
			c.Next()
		}
	}
}

// main initializes the application.
// It sets the gin mode based on the application configuration, ensures the user path exists,
// sets up middleware, routes, and starts the HTTP server.
func main() {
	options := &pg.Options{
		User:     viper.GetString("postgres.dbuser"),
		Password: viper.GetString("postgres.dbpass"),
		Database: viper.GetString("postgres.dbname"),
		Addr:     viper.GetString("postgres.dbhost"),
	}
	db := pg.Connect(options)
	if _, err := db.Exec("SELECT 1"); err != nil {
		log.Fatalln("Failed to connect to database:", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Println("Error closing database connection")
		}
	}()

	database.SetDB(db)

	// Set the Gin mode based on the application configuration.
	if !viper.GetBool("app.devel_mode") {
		gin.SetMode(gin.ReleaseMode)
	}

	ensureUserPathExists(viper.GetString("app.users_path"))

	route := gin.New()
	setupMiddleware(route)
	setupRoutes(route)

	// Log registered routes
	for _, r := range route.Routes() {
		log.Println(r.Method, r.Path)
	}

	server := &http.Server{
		Addr:           ":8085",
		Handler:        route,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	log.Fatal(server.ListenAndServe())
}

// ensureUserPathExists checks if the specified path exists and creates it if it does not.
// It is used to ensure necessary directories are available at application start.
func ensureUserPathExists(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		log.Fatalln(os.MkdirAll(path, 0755))
	}
}
