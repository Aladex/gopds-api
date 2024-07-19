package main

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	assets "gopds-api"
	"gopds-api/api"
	"gopds-api/middlewares"
	"gopds-api/opds"
)

// setupRoutes defines all route handlers and groups them by their functionality.
// It includes routes for Swagger UI, file handling, default operations, OPDS feed, API, admin, and Telegram bot interactions.
func setupRoutes(route *gin.Engine) {
	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	setupFileRoutes(route.Group("/files", middlewares.TokenMiddleware()))
	setupDefaultRoutes(route)
	setupOpdsRoutes(route.Group("/opds", middlewares.BasicAuth()))
	setupApiRoutes(route.Group("/api", middlewares.AuthMiddleware()))
	route.Use(serveStaticFilesMiddleware(NewHTTPFS(assets.Assets)))
	// Serve favicon.ico from the root directory of assets.Assets
	route.GET("/favicon.ico", func(c *gin.Context) {
		c.FileFromFS("frontend_src/dist/favicon.ico", NewHTTPFS(assets.Assets))
	})

	// Adjust the NoRoute handler to serve index.html for unmatched routes
	route.NoRoute(func(c *gin.Context) {
		c.FileFromFS("frontend_src/dist/index.html", NewHTTPFS(assets.Assets))
	})
}

// setupFileRoutes configures routes related to file operations.
func setupFileRoutes(group *gin.RouterGroup) {
	group.GET("/books/get/:format/:id", api.GetBookFile)
}

// setupDefaultRoutes configures default routes for the application.
func setupDefaultRoutes(route *gin.Engine) {
	route.GET("/books-posters/*filepath", api.Posters)
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
