package api

import (
	"bytes"
	"errors"
	"fmt"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/internal/posters"
	"gopds-api/models"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	_ "image/gif"
	_ "image/png"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// UploadBookCover uploads a custom cover image for a book.
// @Summary Upload book cover
// @Description Upload a custom poster image for a book
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Book ID"
// @Accept multipart/form-data
// @Produce json
// @Param cover formData file true "Cover image file"
// @Success 200 {object} models.Result
// @Failure 400 {object} httputil.HTTPError
// @Failure 404 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/books/{id}/cover [post]
func UploadBookCover(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_book_id"))
		return
	}

	fileHeader, err := c.FormFile("cover")
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("missing_cover_file"))
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("failed_to_open_cover: %w", err))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("failed_to_read_cover: %w", err))
		return
	}
	if len(data) == 0 {
		httputil.NewError(c, http.StatusBadRequest, errors.New("empty_cover_file"))
		return
	}
	jpegData, err := convertCoverToJPG(data)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("failed_to_convert_cover: %w", err))
		return
	}

	db := getDB()
	book := &models.Book{ID: bookID}
	err = db.Model(book).WherePK().Select()
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, errors.New("book_not_found"))
		return
	}

	coversDir := viper.GetString("app.posters_path")
	if coversDir == "" {
		coversDir = "./posters/"
	}

	coverPath := posters.FilePath(coversDir, book.Path, book.FileName)
	if err := os.MkdirAll(filepath.Dir(coverPath), 0755); err != nil {
		httputil.NewError(c, http.StatusInternalServerError, fmt.Errorf("failed_to_create_cover_dir: %w", err))
		return
	}

	if err := os.WriteFile(coverPath, jpegData, 0644); err != nil {
		httputil.NewError(c, http.StatusInternalServerError, fmt.Errorf("failed_to_write_cover: %w", err))
		return
	}

	_, err = db.Model(&models.Book{}).
		Set("cover = ?", true).
		Where("id = ?", bookID).
		Update()
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, fmt.Errorf("failed_to_update_cover: %w", err))
		return
	}

	updated, err := database.GetBook(bookID)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, models.Result{
		Result: updated,
		Error:  nil,
	})
}

func convertCoverToJPG(input []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}

	dst := img
	if !isImageOpaque(img) {
		bounds := img.Bounds()
		rgba := image.NewRGBA(bounds)
		draw.Draw(rgba, bounds, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
		draw.Draw(rgba, bounds, img, bounds.Min, draw.Over)
		dst = rgba
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func isImageOpaque(img image.Image) bool {
	type opaqueImage interface {
		Opaque() bool
	}
	if o, ok := img.(opaqueImage); ok {
		return o.Opaque()
	}
	return false
}
