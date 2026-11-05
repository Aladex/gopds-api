package api

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	assets "gopds-api"
	"gopds-api/internal/coverstore"
	"gopds-api/internal/safepath"
	"gopds-api/internal/thumbs"
	"gopds-api/logging"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

const (
	// coverCacheControl lets a browser keep a cover for a day and revalidate
	// it with Last-Modified after that. A list of ten books is otherwise ten
	// downloads on every visit.
	coverCacheControl = "public, max-age=86400"
	// noCoverRevalidate is for the stand-in of a cover that may yet appear.
	noCoverRevalidate = "no-cache"
	// noCoverDoNotKeep is for the stand-in during an outage.
	noCoverDoNotKeep = "no-store"
	// messageNoThumbnail and messageReadFailed are the two error answers more
	// than one branch gives.
	messageNoThumbnail = "no thumbnail"
	messageReadFailed  = "error reading file"
	// sniffBytes is what http.DetectContentType looks at.
	sniffBytes = 512
	// defaultThumbsDir is under the posters directory, on the same volume.
	defaultThumbsDir = ".thumbs"
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
//
// @Summary Serve a file from the specified path
// @Description Serve a file from the specified path, or a default image if not found
// @Tags posters
// @Param  filepath path string true "Relative file path"
// @Produce  image/png
// @Success 200 {file} file "Requested file"
// @Failure 403 {object} httputil.HTTPError "Invalid file path"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /book-posters/{filepath} [get]
func Posters(c *gin.Context) {
	postersPath := viper.GetString("app.posters_path") // Retrieve the base path for posters from the app's configuration.
	relativePath := c.Param("filepath")                // Extract the relative path from the request's parameters.

	// The guard this replaced compared the joined path against the base with a
	// string prefix, which lets a sibling directory through: "/srv/posters-old"
	// starts with "/srv/posters". Nothing exploited it, because a router
	// wildcard always arrives with a leading slash and Clean strips ".." from
	// an absolute path — safe by accident rather than by the check.
	if _, err := safepath.Resolve(postersPath, relativePath); err != nil {
		logging.Warnf("Refused a poster path outside the base: %v", err)
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid file path"})
		return
	}
	safePath := strings.TrimPrefix(filepath.Clean(relativePath), string(filepath.Separator))

	// If path is /book-posters/cover-loading.png, serve the default cover-loading.png from assets
	if safePath == "cover-loading.png" {
		asset, err := assets.Assets.ReadFile("static_assets/posters/cover-loading.png")
		if err != nil {
			logging.Error("cover-loading.png not found in assets:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cover image not available"})
			return
		}

		c.Data(http.StatusOK, "image/png", asset)
		return
	}

	cover, err := coverstore.Default().Open(c.Request.Context(), postersPath, relativePath)
	if err != nil {
		cacheControl := noCoverRevalidate
		if !errors.Is(err, fs.ErrNotExist) {
			// The store being down says nothing about the cover: this answer
			// must not outlive the outage in anyone's cache.
			logging.Warnf("Cover %s is not available: %v", safePath, err)
			cacheControl = noCoverDoNotKeep
		}
		serveNoCover(c, cacheControl)
		return
	}
	defer func() { _ = cover.Close() }()

	contentType, err := sniffContentType(cover)
	if err != nil {
		logging.Error("Error reading file:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": messageReadFailed})
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", coverCacheControl)
	http.ServeContent(c.Writer, c.Request, "", cover.ModTime, cover)
}

// sniffContentType reads the start of a cover and rewinds it. A cover's name
// always ends in .jpg, whatever the book's author actually embedded.
func sniffContentType(r io.ReadSeeker) (string, error) {
	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(head[:n]), nil
}

// serveNoCover is the picture for a book without one: 200, as ever, because
// that is what an <img> and an OPDS client expect from this route.
func serveNoCover(c *gin.Context, cacheControl string) {
	asset, err := assets.Assets.ReadFile("static_assets/posters/no-cover.png")
	if err != nil {
		logging.Error("no-cover.png not found in assets:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no cover image available"})
		return
	}

	c.Header("Cache-Control", cacheControl)
	c.Data(http.StatusOK, "image/png", asset)
}

// NewThumbs builds the thumbnail service from the configuration. Thumbnails
// sit on the same volume as the covers unless app.thumbs_path says otherwise.
func NewThumbs() *thumbs.Service {
	postersPath := viper.GetString("app.posters_path")
	dir := viper.GetString("app.thumbs_path")
	if dir == "" {
		dir = filepath.Join(postersPath, defaultThumbsDir)
	}
	return thumbs.New(postersPath, dir, coverstore.Default)
}

// Thumbs serves a small cover. Unlike Posters it answers 404 for a cover that
// is missing or broken: this route exists for pages that draw their own tile
// instead, and an <img> only learns of failure from the status.
//
// The original's path works here unchanged, so a page swaps one prefix for the
// other.
func Thumbs(service *thumbs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, err := service.Path(c.Request.Context(), c.Param("filepath"))
		switch {
		case err == nil:
		case errors.Is(err, safepath.ErrEscapesBase):
			logging.Warnf("Refused a thumbnail path outside the base: %v", err)
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid file path"})
			return
		case errors.Is(err, thumbs.ErrNoSource), errors.Is(err, thumbs.ErrBroken):
			c.JSON(http.StatusNotFound, gin.H{"error": messageNoThumbnail})
			return
		case errors.Is(err, thumbs.ErrUnavailable):
			logging.Warnf("Thumbnail %s is not available: %v", c.Param("filepath"), err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cover storage unavailable"})
			return
		default:
			logging.Errorf("Failed to make thumbnail %s: %v", c.Param("filepath"), err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "error making thumbnail"})
			return
		}
		// #nosec G304 -- name comes from safepath.Resolve inside service.Path,
		// which refuses anything outside the thumbnail directory.
		file, err := os.Open(name)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": messageNoThumbnail})
			return
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": messageReadFailed})
			return
		}
		c.Header("Content-Type", "image/jpeg")
		c.Header("Cache-Control", coverCacheControl)
		http.ServeContent(c.Writer, c.Request, "", info.ModTime(), file)
	}
}
