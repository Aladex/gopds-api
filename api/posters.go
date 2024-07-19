package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	assets "gopds-api"
)

// Posters serves a file from a path specified in the request.
// It constructs the full path to the file by appending the request's relative path
// to the base posters path defined in the application's configuration.
// If the requested file is not found, it serves a default "no-cover.png" file from assets.
//
// Parameters:
// - c *gin.Context: The context of the request, which includes the HTTP request and response.
//
// The relative file path is expected to be provided as a parameter in the route.
// For example, if the route is defined as "/book-posters/*filepath", the "*filepath" parameter
// can be retrieved and used to construct the full path to the file.
func Posters(c *gin.Context) {
	postersPath := viper.GetString("app.posters_path") // Retrieve the base path for posters from the app's configuration.
	relativePath := c.Param("filepath")                // Extract the relative path from the request's parameters.

	// Clean the path to prevent directory traversal
	safePath := filepath.Clean(relativePath)

	// Join the base path and the safe relative path
	fullPath := filepath.Join(postersPath, safePath)

	// Ensure the full path is within the postersPath directory
	if !strings.HasPrefix(fullPath, filepath.Clean(postersPath)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid file path"})
		return
	}

	// If path is /book-posters/cover-loading.png, serve the default cover-loading.png from assets
	if safePath == "cover-loading.png" {
		asset, err := assets.Assets.ReadFile("static_assets/posters/cover-loading.png")
		if err != nil {
			logrus.Println("cover-loading.png not found in assets:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cover image not available"})
			return
		}

		c.Data(http.StatusOK, "image/png", asset)
		return
	}

	if _, err := os.Stat(fullPath); err == nil {
		fileContent, err := os.ReadFile(fullPath)
		if err != nil {
			logrus.Println("Error reading file:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "error reading file"})
			return
		}
		contentType := http.DetectContentType(fileContent)
		c.Data(http.StatusOK, contentType, fileContent)
		return
	}

	// Serve the default "no-cover.png" from assets if requested file is not found
	asset, err := assets.Assets.ReadFile("static_assets/posters/no-cover.png")
	if err != nil {
		logrus.Println("no-cover.png not found in assets:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no cover image available"})
		return
	}

	c.Data(http.StatusOK, "image/png", asset)
}
