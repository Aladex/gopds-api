package kindlesend

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
	"time"

	"gopds-api/internal/archivestore"
	"gopds-api/internal/objectstore/s3test"
	"gopds-api/models"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	bucketArchive = "archive.zip"
	bucketBook    = "book.fb2"
	// A check costs two of these and the book a third.
	requestDelay = 150 * time.Millisecond
	// padding pushes the book out of the tail a check loads, which would
	// otherwise hand it over without a third request.
	padding = 2 << 20
)

const fb2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"><description><title-info>
<book-title>Book</book-title></title-info></description>
<body><section><p>Text</p></section></body></FictionBook>`

func bucketWithOneBook(t *testing.T, budget time.Duration) *s3test.Server {
	t.Helper()
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	f, err := w.Create(bucketBook)
	require.NoError(t, err)
	_, err = io.WriteString(f, fb2)
	require.NoError(t, err)
	stored, err := w.CreateHeader(&zip.FileHeader{Name: "padding", Method: zip.Store})
	require.NoError(t, err)
	_, err = stored.Write(make([]byte, padding))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	server := s3test.New(t)
	server.Put("archives/"+bucketArchive, archive.Bytes())
	server.SetDelay(requestDelay)
	root := t.TempDir() + "/"
	store, err := archivestore.New(&archivestore.Config{
		Root: root, Prefix: "archives/", CacheDir: t.TempDir(),
		Client: server.Client(time.Second, -1), ReadBudget: budget,
	})
	require.NoError(t, err)
	previous := archivestore.Default()
	archivestore.SetDefault(store)
	t.Cleanup(func() { archivestore.SetDefault(previous) })

	files := viper.GetString("app.files_path")
	viper.Set("app.files_path", root)
	t.Cleanup(func() { viper.Set("app.files_path", files) })

	getter := bookByID
	bookByID = func(int64) (models.Book, error) {
		return models.Book{Path: bucketArchive, FileName: bucketBook, Title: "Book"}, nil
	}
	t.Cleanup(func() { bookByID = getter })
	return server
}

func TestEPUBHasOneBudgetForTheCheckAndTheBook(t *testing.T) {
	bucketWithOneBook(t, requestDelay*5/2)
	_, _, err := EPUBOf(1, 1<<20)
	assert.ErrorIs(t, err, archivestore.ErrUnavailable)
}

// The same book with room for all three requests: what stopped it above was
// the budget and nothing else about the book. The count keeps the book where
// that test needs it: served from the tail, it would leave nothing to run out
// on.
func TestEPUBWithinItsBudget(t *testing.T) {
	server := bucketWithOneBook(t, 20*requestDelay)
	_, data, err := EPUBOf(1, 1<<20)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
	assert.Equal(t, 3, server.Requests(), "HEAD, the directory and the book")
}
