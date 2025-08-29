package api

import (
	"bytes"
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
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
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
	bookID, err := strconv.ParseInt(c.Param("id"), 10, 0)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_book_id"))
		return
	}
	format := strings.ToLower(c.Param("format"))
	contentType, ok := bookTypes[format]
	if !ok {
		httputil.NewError(c, http.StatusBadRequest, errors.New("unknown book format"))
		return
	}

	book, err := database.GetBook(bookID)
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, err)
		return
	}
	zipPath := viper.GetString("app.files_path") + book.Path
	if !utils.FileExists(zipPath) {
		httputil.NewError(c, http.StatusNotFound, errors.New("book file not found"))
		return
	}

	bp := utils.NewBookProcessor(book.FileName, zipPath)

	var rc io.ReadCloser
	switch format {
	case "epub":
		rc, err = bp.Epub()
	case "fb2":
		rc, err = bp.FB2()
	case "zip":
		rc, err = bp.Zip(book.FileName)
	default:
		httputil.NewError(c, http.StatusBadRequest, errors.New("unsupported format"))
		return
	}
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	defer rc.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, rc)
	if err != nil {
		logging.Errorf("failed to buffer file: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	reader := bytes.NewReader(buf.Bytes())
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.%s", book.DownloadName(), format))
	http.ServeContent(c.Writer, c.Request, book.DownloadName()+"."+format, time.Now(), reader)
}
