package opds

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopds-api/internal/archivestore"
	"gopds-api/internal/objectstore/s3test"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bucketArchive = "archive.zip"

func archiveOf(t *testing.T, n int) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for i := range n {
		f, err := w.Create(fmt.Sprintf("%d.fb2", i))
		require.NoError(t, err)
		_, err = io.WriteString(f, "<FictionBook/>")
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return out.Bytes()
}

func download(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.GET("/download/:id/:format", DownloadBook)
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/download/1/fb2", http.NoBody)
	router.ServeHTTP(response, request)
	return response
}

// A book that came with a replaced archive, asked for while the store is down:
// the directory known here lacks it, and that proves nothing.
func TestNewBookDuringAnOutageIs503(t *testing.T) {
	server := s3test.New(t)
	server.Put("archives/"+bucketArchive, archiveOf(t, 1))
	root := t.TempDir() + "/"
	store, err := archivestore.New(&archivestore.Config{
		Root: root, Prefix: "archives/", CacheDir: t.TempDir(), Client: server.Client(time.Second, -1),
	})
	require.NoError(t, err)
	previous := archivestore.Default()
	archivestore.SetDefault(store)
	t.Cleanup(func() { archivestore.SetDefault(previous) })

	files := viper.GetString("app.files_path")
	viper.Set("app.files_path", root)
	t.Cleanup(func() { viper.Set("app.files_path", files) })

	entry := "0.fb2"
	getter := bookByID
	bookByID = func(int64) (models.Book, error) {
		return models.Book{Path: bucketArchive, FileName: entry, Title: "Book", Approved: true}, nil
	}
	t.Cleanup(func() { bookByID = getter })

	require.Equal(t, http.StatusOK, download(t).Code)

	server.Put("archives/"+bucketArchive, archiveOf(t, 2))
	server.SetDown(true)
	entry = "1.fb2"
	response := download(t)
	assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	assert.Equal(t, "30", response.Header().Get("Retry-After"))

	server.SetDown(false)
	assert.Equal(t, http.StatusOK, download(t).Code)
}
