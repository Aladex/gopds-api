package main

import (
	assets "gopds-api"
	"gopds-api/api"
	"gopds-api/middlewares"
	"gopds-api/opds"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// setupRoutes defines all route handlers and groups them by their functionality.
// It includes routes for Swagger UI, file handling, default operations, OPDS feed, API, admin, and Telegram bot interactions.
func setupRoutes(route *gin.Engine) {
	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	setupFileRoutes(route.Group("/files", middlewares.AuthMiddleware()))
	setupFileRoutes(route.Group("/api/files", middlewares.AuthMiddleware()))
	setupDefaultRoutes(route)
	setupOpdsRoutes(route.Group("/opds", middlewares.BasicAuth()))
	// Add public auth routes (no auth middleware)
	setupPublicAuthRoutes(route.Group("/api"))
	// WebSocket: Origin check BEFORE auth, so evil origins get 403 not 401
	route.GET("/api/ws", api.OriginCheckMiddleware(), middlewares.AuthMiddleware(), api.UnifiedWebSocketHandler)
	// Add authenticated API routes with CSRF protection for state-changing operations
	setupApiRoutes(route.Group("/api", middlewares.AuthMiddleware()))
	setupLogoutRoutes(route.Group("/api", middlewares.AuthMiddleware()))
	// Add Telegram webhook routes (public, no auth required)
	setupTelegramWebhookRoutes(route.Group("/telegram"))
	// Add Telegram API routes (requires authentication for bot management)
	setupTelegramApiRoutes(route.Group("/api/telegram", middlewares.AuthMiddleware()))
	route.Use(serveStaticFilesMiddleware(NewHTTPFS(assets.Assets)))
	rootFiles := listRootFiles()
	for _, file := range rootFiles {
		route.GET(file, func(c *gin.Context) {
			c.FileFromFS("booksdump-frontend/build"+c.Request.URL.Path, NewHTTPFS(assets.Assets))
		})
	}

	// NoRoute handler: distinguish API/service requests from SPA navigation.
	route.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path

		// 1. Known service prefixes — always JSON 404.
		if strings.HasPrefix(p, "/api/") ||
			strings.HasPrefix(p, "/opds/") ||
			strings.HasPrefix(p, "/files/") ||
			strings.HasPrefix(p, "/telegram/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// 2. Client explicitly wants JSON (e.g. mobile app, curl) — JSON 404.
		accept := c.GetHeader("Accept")
		if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// 3. Everything else — SPA fallback (client-side routing).
		indexFile, err := NewHTTPFS(assets.Assets).Open("booksdump-frontend/build/index.html")
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		defer indexFile.Close()

		http.ServeContent(c.Writer, c.Request, "index.html", time.Now(), indexFile)
		c.Abort()
	})
}

// setupFileRoutes configures routes related to file operations.
func setupFileRoutes(group *gin.RouterGroup) {
	group.GET("/books/get/:format/:id", api.GetBookFile)
	group.HEAD("/books/get/:format/:id", api.HeadBookFile)
	group.GET("/books/conversion/:id", api.DownloadConvertedBook)
	group.HEAD("/books/conversion/:id", api.HeadConvertedBook)
	group.GET("/books/conversion/epub/:id", api.DownloadConvertedEpub)
	group.HEAD("/books/conversion/epub/:id", api.HeadConvertedEpub)
}

// setupDefaultRoutes configures default routes for the application.
func setupDefaultRoutes(route *gin.Engine) {
	route.GET("/books-posters/*filepath", api.Posters)
	route.GET("/api/status", api.StatusCheck)
	route.GET("/opds-opensearch.xml", opds.OpenSearch)
	// Add CSRF protection to password change endpoints
	route.POST("/api/change-password", middlewares.CSRFMiddleware(), api.ChangeUserState)
	route.POST("/api/change-request", middlewares.CSRFMiddleware(), api.ChangeRequest)
	route.POST("/api/token", api.TokenValidation)
}

// setupOpdsRoutes configures routes for OPDS feed interactions.
func setupOpdsRoutes(group *gin.RouterGroup) {
	opds.SetupOpdsRoutes(group)
}

func setupLogoutRoutes(group *gin.RouterGroup) {
	api.SetupLogoutRoute(group)
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

// setupPublicAuthRoutes configures public authentication routes that do not require middleware authorization.
func setupPublicAuthRoutes(group *gin.RouterGroup) {
	api.SetupAuthRoutes(group)
}

// setupTelegramWebhookRoutes configures routes for Telegram webhook interactions.
func setupTelegramWebhookRoutes(group *gin.RouterGroup) {
	if telegramService != nil {
		telegramService.SetupWebhookRoutes(group)
	}
}

// setupTelegramApiRoutes configures Telegram API routes (authenticated).
func setupTelegramApiRoutes(group *gin.RouterGroup) {
	if telegramService != nil {
		telegramService.SetupApiRoutes(group)
	}
}
