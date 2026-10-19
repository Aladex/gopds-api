package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"

	"gopds-api/internal/safeio"
	"gopds-api/models"
	"gopds-api/utils"

	"github.com/spf13/viper"
)

// File is a prepared book file.
type File struct {
	Name string
	Data []byte
}

// PrepareFunc produces the file of book in format, reading at most limit
// bytes: a larger file fails with ErrTooLarge, never read past the limit.
type PrepareFunc func(ctx context.Context, book *models.Book, format string, limit int64) (File, error)

// Errors a PrepareFunc reports for what it could not produce.
var (
	ErrTooLarge    = errors.New("delivery: file larger than the upload limit")
	ErrFileMissing = errors.New("delivery: the book's archive is missing")
)

// PrepareFile is the PrepareFunc of production: the book's archive under
// app.files_path, converted as the site converts it.
func PrepareFile(ctx context.Context, book *models.Book, format string, limit int64) (File, error) {
	base := viper.GetString("app.files_path")
	if base == "" {
		return File{}, fmt.Errorf("delivery: files path not configured")
	}
	archive := base + book.Path
	if !utils.FileExists(archive) {
		return File{}, ErrFileMissing
	}
	bp := utils.NewBookProcessor(book.FileName, archive)

	var (
		rc  io.ReadCloser
		err error
	)
	switch format {
	case "fb2":
		rc, err = bp.FB2()
	case "epub":
		rc, err = bp.Epub()
	case "mobi":
		rc, err = bp.MobiContext(ctx)
	case "zip":
		rc, err = bp.Zip(book.FileName)
	default:
		return File{}, ErrUnknownFormat
	}
	if err != nil {
		return File{}, err
	}
	defer func() { _ = rc.Close() }()

	data, err := safeio.ReadAll(rc, limit)
	if errors.Is(err, safeio.ErrTooLarge) {
		return File{}, ErrTooLarge
	}
	if err != nil {
		return File{}, err
	}
	return File{Name: fmt.Sprintf("%s.%s", book.DownloadName(), format), Data: data}, nil
}
