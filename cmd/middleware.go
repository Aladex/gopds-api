package main

import (
	"fmt"
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
	route.Use(securityHeadersMiddleware())
	if cfg.App.DevelMode {
		route.Use(corsOptionsMiddleware())
	}
}

// securityHeadersMiddleware adds Content-Security-Policy and other security headers.
// CSP connect-src is built per-request from the Host header so that the WebSocket
// URL always matches the origin the browser sees (works for both production domain
// and local binary runs).
func securityHeadersMiddleware() gin.HandlerFunc {
	// Static part of CSP (everything except connect-src)
	const cspTemplate = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; font-src 'self'; connect-src %s; manifest-src 'self'; " +
		"frame-src 'self'; frame-ancestors 'self'; form-action 'self'; base-uri 'self'; object-src 'none'"

	return func(c *gin.Context) {
		host := c.Request.Host // includes port if non-standard

		// Determine ws scheme from the request (TLS or plain)
		wsScheme := "ws"
		if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
			wsScheme = "wss"
		}

		connectSrc := fmt.Sprintf("'self' %s://%s", wsScheme, host)
		csp := fmt.Sprintf(cspTemplate, connectSrc)

		c.Header("Content-Security-Policy", csp)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Next()
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
