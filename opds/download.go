package opds

import (
	"errors"
	"fmt"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/utils"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
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

	format := c.Param("format")
	userID := c.GetInt64("user_id")

	book, err := database.GetBook(bookID)
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, err)
		return
	}

	if !book.Approved {
		httputil.NewError(c, http.StatusNotFound, errors.New("book_not_approved"))
		return
	}

	// Log user access if needed
	if userID != 0 {
		logging.Infof("User %d downloading book %d in format %s", userID, bookID, format)
	}

	zipPath := viper.GetString("app.files_path") + book.Path
	if !utils.FileExists(zipPath) {
		httputil.NewError(c, http.StatusNotFound, errors.New("book file not found"))
		return
	}

	bp := utils.NewBookProcessor(book.FileName, zipPath)

	var rc io.ReadCloser
	contentDisp := "attachment; filename=%s.%s"

	switch strings.ToLower(format) {
	case "epub":
		rc, err = bp.Epub()
	case "mobi":
		rc, err = bp.Mobi()
	case "fb2":
		rc, err = bp.FB2()
	case "zip":
		rc, err = bp.Zip(book.FileName)
	default:
		httputil.NewError(c, http.StatusBadRequest, errors.New("unknown book format"))
		return
	}

	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	defer func() {
		if cerr := rc.Close(); cerr != nil {
			logging.Errorf("failed to close file: %v", cerr)
		}
	}()

	c.Header("Content-Disposition", fmt.Sprintf(contentDisp, book.DownloadName(), format))
	c.Header("Content-Type", bookTypes[strings.ToLower(format)])
	_, err = io.Copy(c.Writer, rc)

	if err != nil {
		logging.Infof("Client closed connection: %v", err)
		return
	}
}
