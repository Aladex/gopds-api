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
	"strconv"
	"strings"
)

var bookTypes = map[string]string{
	"fb2":  "application/fb2+xml",
	"zip":  "application/zip",
	"epub": "application/epub+zip",
	"mobi": "application/x-mobipocket-ebook",
}

// GetBookFile returns file of book in answered type
// Auth godoc
// @Summary returns file of book in answered type
// @Description returns file of book in answered type
// @Tags files
// @Accept  json
// @Produce  json
// @Param  body body models.BookDownload true "Book Data"
// @Success 200 {object} models.BookDownload
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /books/file [post]
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

	bp := utils.NewBookProcessor(book.FileName, zipPath) // Create a new BookProcessor for the book file.
	var rc io.ReadCloser                                 // Declare a variable to hold the file reader.

	// Get the file reader for the requested format
	rc, err = bp.GetConverter(c.Param("format"))
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err) // Send a 400 Bad Request if there is an error getting the file reader.
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
