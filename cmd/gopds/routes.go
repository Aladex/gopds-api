package main

import (
	"net/http"
	"strings"

	assets "gopds-api"
	"gopds-api/api"
	"gopds-api/config"
	"gopds-api/middlewares"
	"gopds-api/opds"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// setupRoutes defines all route handlers and groups them by their functionality.
// It includes routes for Swagger UI, file handling, default operations, OPDS feed, API, admin, and Telegram bot interactions.
func setupRoutes(route *gin.Engine, donate []config.DonateMethod, search services.PublicSearch) {
	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	setupFileRoutes(route.Group("/files", middlewares.AuthMiddleware()))
	setupFileRoutes(route.Group("/api/files", middlewares.AuthMiddleware()))
	setupDefaultRoutes(route, donate)
	setupOpdsRoutes(route.Group("/opds", middlewares.BasicAuth()))
	// Add public auth routes (no auth middleware)
	setupPublicAuthRoutes(route.Group("/api"))
	// WebSocket: Origin check BEFORE auth, so evil origins get 403 not 401
	route.GET("/api/ws", api.OriginCheckMiddleware(), middlewares.AuthMiddleware(), api.UnifiedWebSocketHandler)
	// Add authenticated API routes with CSRF protection for state-changing operations
	setupApiRoutes(route.Group("/api", middlewares.AuthMiddleware()), search)
	setupLogoutRoutes(route.Group("/api", middlewares.AuthMiddleware()))
	// Add Telegram webhook routes (public, no auth required)
	setupTelegramWebhookRoutes(route.Group("/telegram"))
	// Add Telegram API routes (requires authentication for bot management)
	setupTelegramApiRoutes(route.Group("/api/telegram", middlewares.AuthMiddleware()))
	route.Use(serveStaticFilesMiddleware(NewHTTPFS(assets.Assets)))
	rootFiles := listRootFiles()
	for _, file := range rootFiles {
		route.GET(file, func(c *gin.Context) {
			setStaticCacheHeaders(c, c.Request.URL.Path)
			c.FileFromFS("booksdump-frontend/build"+c.Request.URL.Path, NewHTTPFS(assets.Assets))
		})
	}

	route.NoRoute(spaFallbackHandler(NewHTTPFS(assets.Assets)))
}

// spaFallbackHandler distinguishes API/service requests from SPA navigation for
// paths no route matched. Browsers must receive the application shell so that
// client-side routing can take over on a deep link or a reload, while backend
// namespaces keep reporting a missing resource.
func spaFallbackHandler(fs http.FileSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		indexFile, err := fs.Open("booksdump-frontend/build/index.html")
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		defer indexFile.Close()

		setStaticCacheHeaders(c, "index.html")
		http.ServeContent(c.Writer, c.Request, "index.html", buildTime, indexFile)
		c.Abort()
	}
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
func setupDefaultRoutes(route *gin.Engine, donate []config.DonateMethod) {
	route.GET("/books-posters/*filepath", api.Posters)
	route.GET("/api/status", api.StatusCheck)
	// Public: every value served here is an address meant to be handed out.
	route.GET("/api/donate", api.DonateMethods(donate))
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
func setupApiRoutes(group *gin.RouterGroup, search services.PublicSearch) {
	booksGroup := group.Group("/books")
	api.SetupBookRoutes(booksGroup, &api.SearchHandler{Search: search})

	publicCollections := &api.PublicCollectionsHandler{
		Svc: services.NewPublicCuratedCollectionsService(),
	}
	publicCollections.Register(group.Group("/collections"))

	// Setup admin routes with admin middleware
	adminGroup := group.Group("/admin", middlewares.AdminMiddleware())
	setupAdminRoutes(adminGroup)
}

// setupAdminRoutes configures routes for administrative functionalities.
func setupAdminRoutes(group *gin.RouterGroup) {
	api.SetupAdminRoutes(group)

	curatedHandler := &api.CuratedCollectionsHandler{
		Svc: services.NewCuratedCollectionsService(),
	}
	curatedHandler.Register(group.Group("/collections"))
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
