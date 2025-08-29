package main

import (
	"gopds-api/logging"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// setupMiddleware configures global middleware for the gin.Engine instance.
// It includes a custom logger and, if in development mode, a CORS middleware.
func setupMiddleware(route *gin.Engine) {
	route.Use(logging.GinrusLogger())
	if cfg.App.DevelMode {
		route.Use(corsOptionsMiddleware())
	}
}

// serveStaticFilesMiddleware serves static files from the root directory
func serveStaticFilesMiddleware(fs http.FileSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		// Directly serve index.html for requests to root or /index.html
		if requestPath == "/" {
			indexFile, err := fs.Open("booksdump-frontend/build/index.html")
			if err != nil {
				// Log the error and send a 404 response if index.html cannot be opened
				logging.Errorf("Error opening index.html: %v", err)
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
			defer indexFile.Close()

			// Serve index.html content
			http.ServeContent(c.Writer, c.Request, "index.html", time.Now(), indexFile)
			c.Abort()
		}

		// Handle serving other static files from distFolders
		for _, folder := range distFolders {
			if strings.HasPrefix(requestPath, folder) {
				filePath := path.Join("booksdump-frontend/build", requestPath)
				file, err := fs.Open(filePath)
				if err == nil {
					defer file.Close()
					http.ServeContent(c.Writer, c.Request, filePath, time.Now(), file)
					c.Abort()
					return
				}
			}
		}

		// If no static file is found, proceed with the next middleware
		c.Next()
	}
}

// corsOptionsMiddleware returns a middleware that enables CORS support.
// It is only used in development mode for easier testing and development.
func corsOptionsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Header("Access-Control-Allow-Origin", "http://127.0.0.1:3000")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "authorization, origin, content-type, accept, token")
			c.Header("Allow", "HEAD,GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Content-Type", "application/json")
			c.AbortWithStatus(http.StatusOK)
		} else {
			c.Header("Access-Control-Allow-Origin", "http://127.0.0.1:3000")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "authorization, origin, content-type, accept, token")
			c.Header("Allow", "HEAD,GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Next()
		}
	}
}
