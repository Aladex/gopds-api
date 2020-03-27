package api

import (
	"archive/zip"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/utils"
	"io"
	"net/http"
	"strings"
)

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
	if err := c.ShouldBindJSON(&bookRequest); err == nil {

		book, err := database.GetBook(bookRequest.BookID)
		if err != nil {
			httputil.NewError(c, http.StatusNotFound, err)
			return
		}
		downloadName := strings.Join(strings.Split(strings.ToLower(book.Title), " "), "_")

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
		header := c.Writer.Header()
		header["Content-type"] = []string{"application/octet-stream"}
		header["Content-Disposition"] = []string{"attachment; filename= " + downloadName}
		switch bookRequest.Format {
		case "fb2":
			rc, err := utils.FB2Book(book.FileName, zipPath)
			if err != nil {
				httputil.NewError(c, http.StatusBadRequest, err)
				return
			}
			_, err = io.Copy(c.Writer, rc)
			if err != nil {
				httputil.NewError(c, http.StatusBadRequest, err)
				return
			}
			return
		case "zip":
			rc, err := utils.ZipBook(downloadName, book.FileName, zipPath)
			if err != nil {
				httputil.NewError(c, http.StatusBadRequest, err)
				return
			}
			_, err = io.Copy(c.Writer, rc)
			if err != nil {
				httputil.NewError(c, http.StatusBadRequest, err)
				return
			}
			return
		default:
			httputil.NewError(c, http.StatusBadRequest, errors.New("unknown file format"))
			return
		}
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}
