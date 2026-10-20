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

// ConverterRev is the revision of what PrepareFile makes of a book. A bot's
// cached file_id stands for the file of one revision: bump it whenever the
// converters (utils.BookProcessor's FB2, ZIP, EPUB or MOBI output, or the
// file name) change, and every bot uploads its files afresh.
const ConverterRev = 1

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

	// FB2 and ZIP are produced under the limit itself. EPUB and MOBI are
	// converted first — from the FB2 entry, under the site's own bound — and
	// their result is read under the limit; ctx bounds the conversion.
	var (
		data []byte
		err  error
	)
	switch format {
	case FormatFB2:
		data, err = bp.FB2Limited(limit)
	case FormatZIP:
		data, err = bp.ZipLimited(book.FileName, limit)
	case FormatEPUB:
		data, err = readConverted(bp.EpubContext(ctx))(limit)
	case FormatMOBI:
		data, err = readConverted(bp.MobiContext(ctx))(limit)
	default:
		return File{}, ErrUnknownFormat
	}
	if errors.Is(err, safeio.ErrTooLarge) {
		return File{}, ErrTooLarge
	}
	if err != nil {
		return File{}, err
	}
	return File{Name: fmt.Sprintf("%s.%s", book.DownloadName(), format), Data: data}, nil
}

// readConverted reads a converter's result under a limit.
func readConverted(rc io.ReadCloser, err error) func(limit int64) ([]byte, error) {
	return func(limit int64) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return safeio.ReadAll(rc, limit)
	}
}
