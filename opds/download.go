package opds

import (
	"archive/zip"
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
	"regexp"
	"strconv"
	"strings"
)

var nameRegExp = regexp.MustCompile(`[^A-Za-z0-9а-яА-ЯёЁ]+`)
var bookTypes = map[string]string{
	"fb2":  "application/fb2+xml",
	"zip":  "application/zip",
	"epub": "application/epub+zip",
	"mobi": "application/x-mobipocket-ebook",
}

func DownloadBook(c *gin.Context) {
	bookId, err := strconv.ParseInt(c.Param("id"), 10, 0)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_book_id"))
	}
	bookRequest := models.BookDownload{
		BookID: bookId,
		Format: c.Param("format"),
	}
	book, err := database.GetBook(bookRequest.BookID)
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, err)
		return
	}
	downloadName := nameRegExp.ReplaceAllString(strings.ToLower(book.Title), `_`)
	downloadName = utils.Translit(downloadName)

	zipPath := viper.GetString("app.files_path") + book.Path
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, err)
		return
	}
	defer func() {
		err := r.Close()
		if err != nil {
			httputil.NewError(c, http.StatusNotFound, err)
			return
		}
	}()

	var rc io.ReadCloser
	contentDisp := "attachment; filename=%s.%s"

	switch bookRequest.Format {
	case "fb2":
		c.Header("Content-Disposition", fmt.Sprintf(contentDisp, downloadName, bookRequest.Format))
		rc, err = utils.FB2Book(book.FileName, zipPath)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}

	case "zip":
		rc, err = utils.ZipBook(downloadName, book.FileName, zipPath)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}

	case "epub":
		rc, err = utils.EpubBook(book.FileName, zipPath)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}

	case "mobi":
		rc, err = utils.MobiBook(book.FileName, zipPath)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}

	default:
		httputil.NewError(c, http.StatusBadRequest, errors.New("unknown file format"))
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(contentDisp, downloadName, bookRequest.Format))
	c.Header("Content-Type", bookTypes[bookRequest.Format])
	_, err = io.Copy(c.Writer, rc)

	if err != nil {
		customLog.WithFields(logrus.Fields{
			"status":      c.Writer.Status(),
			"method":      c.Request.Method,
			"error":       "client was dropped connection",
			"ip":          c.ClientIP(),
			"book_format": bookRequest.Format,
			"user-agent":  c.Request.UserAgent(),
		}).Info()
		return
	}
	return

}
