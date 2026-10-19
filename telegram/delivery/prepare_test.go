package delivery

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopds-api/models"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// archiveOf writes a library archive holding one FB2 entry of size zero
// bytes and points app.files_path at it.
func archiveOf(t *testing.T, size int64) *models.Book {
	t.Helper()
	return archiveWith(t, func(w io.Writer) error {
		_, err := io.CopyN(w, zeros{}, size)
		return err
	})
}

// archiveWith writes a library archive holding one FB2 entry that write
// fills, and points app.files_path at it.
func archiveWith(t *testing.T, write func(io.Writer) error) *models.Book {
	t.Helper()
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "book.zip"))
	require.NoError(t, err)
	z := zip.NewWriter(f)
	w, err := z.Create("book.fb2")
	require.NoError(t, err)
	require.NoError(t, write(w))
	require.NoError(t, z.Close())
	require.NoError(t, f.Close())
	old := viper.GetString("app.files_path")
	viper.Set("app.files_path", dir+"/")
	t.Cleanup(func() { viper.Set("app.files_path", old) })
	return &models.Book{Path: "book.zip", FileName: "book.fb2", Title: "Book"}
}

// allocated is how many bytes f allocates.
func allocated(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A 4 MiB book against a 1 KiB limit: refused as too large, with memory
// spent near the limit, not near the book.
func TestPreparingUnderALimitDoesNotBufferTheBook(t *testing.T) {
	book := archiveOf(t, 4<<20)
	for format, bound := range map[string]uint64{
		FormatFB2: 256 << 10,
		// The zip writer's compressor costs about 1 MiB on its own,
		// whatever the book.
		FormatZIP: 2 << 20,
	} {
		var err error
		spent := allocated(func() { _, err = PrepareFile(context.Background(), book, format, 1024) })
		require.ErrorIs(t, err, ErrTooLarge, format)
		t.Logf("%s: 4 MiB book, 1 KiB limit, %d bytes allocated", format, spent)
		assert.Less(t, spent, bound, format)
	}
}

// A conversion is bound to the context it is given.
func TestAConversionStopsWithItsContext(t *testing.T) {
	book := archiveWith(t, func(w io.Writer) error {
		if _, err := io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?>`+
			`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"><description><title-info>`+
			`<book-title>Book</book-title></title-info></description><body><section>`); err != nil {
			return err
		}
		if _, err := io.WriteString(w, strings.Repeat("<p>Строка книги.</p>", 100_000)); err != nil {
			return err
		}
		_, err := io.WriteString(w, `</section></body></FictionBook>`)
		return err
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PrepareFile(ctx, book, FormatEPUB, 50<<20)
	require.ErrorIs(t, err, context.Canceled)
}
