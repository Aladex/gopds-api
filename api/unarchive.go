package api

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/utils"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var bookTypes = map[string]string{
	"fb2":  "application/fb2+xml",
	"zip":  "application/zip",
	"epub": "application/epub+zip",
	"mobi": "application/x-mobipocket-ebook",
}

var readyChannels sync.Map // Temporary storage for mobi files

// GetBookFile returns the file of a book in the requested format
// Auth godoc
// @Summary Return book file in the specified format
// @Description Retrieve the file of a book in the specified format
// @Tags files
// @Accept  json
// @Produce  json
// @Param  format path string true "Book format" Enums(fb2, zip, epub, mobi)
// @Param  id path int true "Book ID"
// @Success 200 {object} models.BookDownload
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 403 {object} httputil.HTTPError "Forbidden - access denied"
// @Failure 404 {object} httputil.HTTPError "Not found - book not found"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /files/books/get [get]
func GetBookFile(c *gin.Context) {
	bookID, err := strconv.ParseInt(c.Param("id"), 10, 0) // Parse the book ID from the request parameters.
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_book_id")) // Send a 400 Bad Request if the book ID is invalid.
		return
	}
	format := strings.ToLower(c.Param("format")) // Normalize the requested format to lowercase.
	if _, ok := bookTypes[format]; !ok {
		httputil.NewError(c, http.StatusBadRequest, errors.New("unknown book format")) // Send a 400 Bad Request if the format is unsupported.
		return
	}
	contentDisp := "attachment; filename=%s.%s" // Template for the Content-Disposition header.
	book, err := database.GetBook(bookID)       // Retrieve the book details from the database.
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, err) // Send a 404 Not Found if the book is not in the database.
		return
	}
	zipPath := viper.GetString("app.files_path") + book.Path // Construct the path to the book file.
	mobiConversionDir := viper.GetString("app.mobi_conversion_dir")

	if !utils.FileExists(zipPath) {
		httputil.NewError(c, http.StatusNotFound, errors.New("book file not found")) // Send a 404 Not Found if the book file is missing
		return
	}

	bp := utils.NewBookProcessor(book.FileName, zipPath) // Create a new BookProcessor for the book file.
	var rc io.ReadCloser                                 // Declare a variable to hold the file reader.

	switch strings.ToLower(c.Param("format")) {
	case "epub":
		rc, err = bp.Epub()
	case "mobi":
		go func() {
			rc, err := bp.Mobi()
			if err != nil {
				logrus.Error("Failed to convert book to mobi:", err)
				return
			}
			filePath := filepath.Join(mobiConversionDir, fmt.Sprintf("%s.mobi", bookID))
			file, err := os.Create(filePath)
			if err != nil {
				logrus.Error("Failed to create mobi file:", err)
				return
			}
			defer file.Close()

			if _, err = io.Copy(file, rc); err != nil {
				logrus.Error("Failed to write mobi file to disk:", err)
				return
			}

			readyChannels.Store(bookID, rc)
			notifyClientBookReady(bookID)
		}()
		c.JSON(http.StatusOK, "Book conversion started")
		return
	case "fb2":
		rc, err = bp.FB2()
	case "zip":
		rc, err = bp.Zip(book.FileName)
	default:
		httputil.NewError(c, http.StatusBadRequest, errors.New("unknown book format")) // Send a 400 Bad Request if the format is not handled.
		return
	}

	defer func(rc io.ReadCloser) {
		err := rc.Close()
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"status":      c.Writer.Status(),
				"method":      c.Request.Method,
				"error":       "Client closed connection",
				"ip":          c.ClientIP(),
				"book_format": c.Param("format"),
				"user-agent":  c.Request.UserAgent(),
			}).Info()
		}
	}(rc) // Ensure the file reader is closed after serving the file.

	c.Header("Content-Disposition", fmt.Sprintf(contentDisp, book.DownloadName(), c.Param("format"))) // Set the Content-Disposition header.
	c.Header("Content-Type", bookTypes[strings.ToLower(c.Param("format"))])                           // Set the Content-Type header based on the book format.
	_, err = io.Copy(c.Writer, rc)                                                                    // Copy the book content to the response writer.

	if err != nil {
		// Log the error if there is an issue copying the book content to the response writer.
		logrus.WithFields(logrus.Fields{
			"status":      c.Writer.Status(),
			"method":      c.Request.Method,
			"error":       "Client closed connection",
			"ip":          c.ClientIP(),
			"book_format": c.Param("format"),
			"user-agent":  c.Request.UserAgent(),
		}).Info()
		return
	}
	return
}
