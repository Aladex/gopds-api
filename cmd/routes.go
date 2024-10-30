package main

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	assets "gopds-api"
	"gopds-api/api"
	"gopds-api/middlewares"
	"gopds-api/opds"
	"net/http"
	"strings"
	"time"
)

// setupRoutes defines all route handlers and groups them by their functionality.
// It includes routes for Swagger UI, file handling, default operations, OPDS feed, API, admin, and Telegram bot interactions.
func setupRoutes(route *gin.Engine) {
	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	setupFileRoutes(route.Group("/files", middlewares.AuthMiddleware()))
	setupFileRoutes(route.Group("/api/files", middlewares.AuthMiddleware()))
	setupDefaultRoutes(route)
	setupOpdsRoutes(route.Group("/opds", middlewares.BasicAuth()))
	setupApiRoutes(route.Group("/api", middlewares.AuthMiddleware()))
	setupLogoutRoutes(route.Group("/api", middlewares.AuthMiddleware()))
	route.Use(serveStaticFilesMiddleware(NewHTTPFS(assets.Assets)))
	rootFiles := listRootFiles()
	for _, file := range rootFiles {
		route.GET(file, func(c *gin.Context) {
			c.FileFromFS("booksdump-frontend/build"+c.Request.URL.Path, NewHTTPFS(assets.Assets))
		})
	}

	// Adjust the NoRoute handler to serve index.html for unmatched routes
	registeredRoutes := getRegisteredRoutes(route)
	route.NoRoute(func(c *gin.Context) {
		// Check if the request path is not a registered route
		if !isRegisteredRoute(c.Request.URL.Path, registeredRoutes) {
			indexFile, err := NewHTTPFS(assets.Assets).Open("booksdump-frontend/build/index.html")
			if err != nil {
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
			defer func(indexFile http.File) {
				err := indexFile.Close()
				if err != nil {
					c.AbortWithStatus(http.StatusInternalServerError)
				}
			}(indexFile)
			http.ServeContent(c.Writer, c.Request, "index.html", time.Now(), indexFile)
			c.Abort()
		} else {
			c.AbortWithStatus(http.StatusNotFound)
		}
	})
}

// setupFileRoutes configures routes related to file operations.
func setupFileRoutes(group *gin.RouterGroup) {
	group.GET("/books/get/:format/:id", api.GetBookFile)
	group.GET("/collection/:id/download/:format", api.DownloadCollection)
	group.GET("/books/conversion/:id", api.DownloadConvertedBook)
}

// setupDefaultRoutes configures default routes for the application.
func setupDefaultRoutes(route *gin.Engine) {
	route.GET("/books-posters/*filepath", api.Posters)
	route.GET("/api/status", api.StatusCheck)
	route.POST("/api/login", api.AuthCheck)
	route.POST("/api/register", api.Registration)
	route.POST("/api/change-password", api.ChangeUserState)
	route.POST("/api/change-request", api.ChangeRequest)
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

// getRegisteredRoutes retrieves all registered routes in the Gin engine
func getRegisteredRoutes(route *gin.Engine) map[string]struct{} {
	registeredRoutes := make(map[string]struct{})
	for _, r := range route.Routes() {
		registeredRoutes[r.Path] = struct{}{}
	}
	return registeredRoutes
}

// isRegisteredRoute checks if a given path matches any of the registered routes
func isRegisteredRoute(path string, registeredRoutes map[string]struct{}) bool {
	for route := range registeredRoutes {
		if path == route || strings.HasPrefix(path, route) {
			return true
		}
	}
	return false
}
