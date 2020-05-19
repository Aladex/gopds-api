package api

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
	"strings"
)

var nameRegExp = regexp.MustCompile(`[^A-Za-z0-9а-яА-ЯёЁ]+`)
var bookTypes = map[string]string{
	"fb2":  "application/x-fictionbook",
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
	var bookRequest models.BookDownload
	var rc io.ReadCloser
	contentDisp := "attachment; filename=%s.%s"
	if err := c.ShouldBindJSON(&bookRequest); err == nil {

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
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}
