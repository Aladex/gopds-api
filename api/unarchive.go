package api

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"
	"io"
	"net/http"
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
	var bookRequest models.BookDownload
	contentDisp := "attachment; filename=%s.%s"
	if err := c.ShouldBindJSON(&bookRequest); err == nil {
		book, err := database.GetBook(bookRequest.BookID)
		if err != nil {
			httputil.NewError(c, http.StatusNotFound, err)
			return
		}
		zipPath := config.AppConfig.GetString("app.files_path") + book.Path

		bp := utils.NewBookProcessor(book.FileName, zipPath)
		var rc io.ReadCloser

		switch strings.ToLower(bookRequest.Format) {
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
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}

		defer rc.Close()

		c.Header("Content-Disposition", fmt.Sprintf(contentDisp, book.DownloadName(), bookRequest.Format))
		c.Header("Content-Type", bookTypes[strings.ToLower(bookRequest.Format)])
		_, err = io.Copy(c.Writer, rc)

		if err != nil {
			logging.CustomLog.WithFields(logrus.Fields{
				"status":      c.Writer.Status(),
				"method":      c.Request.Method,
				"error":       "Client closed connection",
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
