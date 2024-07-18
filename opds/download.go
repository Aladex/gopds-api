package opds

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
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

// DownloadBook returns a book file
func DownloadBook(c *gin.Context) {
	bookID, err := strconv.ParseInt(c.Param("id"), 10, 0)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_book_id"))
		return
	}
	bookRequest := models.BookDownload{
		BookID: bookID,
		Format: c.Param("format"),
	}

	book, err := database.GetBook(bookRequest.BookID)
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, err)
		return
	}

	zipPath := viper.GetString("app.files_path") + book.Path
	bp := utils.NewBookProcessor(book.FileName, zipPath)

	var rc io.ReadCloser
	contentDisp := "attachment; filename=%s.%s"

	// Get the file reader for the requested format
	rc, err = bp.GetConverter(c.Param("format"))
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err) // Send a 400 Bad Request if there is an error getting the file reader.
		return
	}

	defer func() {
		if cerr := rc.Close(); cerr != nil {
			logrus.Printf("failed to close file: %v", cerr)
		}
	}()

	c.Header("Content-Disposition", fmt.Sprintf(contentDisp, book.DownloadName(), bookRequest.Format))
	c.Header("Content-Type", bookTypes[strings.ToLower(bookRequest.Format)])
	_, err = io.Copy(c.Writer, rc)

	if err != nil {
		logrus.WithFields(logrus.Fields{
			"status":      c.Writer.Status(),
			"method":      c.Request.Method,
			"error":       "client closed connection",
			"ip":          c.ClientIP(),
			"book_format": bookRequest.Format,
			"user-agent":  c.Request.UserAgent(),
		}).Info()
		return
	}
}
