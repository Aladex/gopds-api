package api

import (
	"archive/zip"
	"bytes"
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

// archiveOf is a zip of n small entries, 0.fb2 upwards.
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

// bucketLibrary installs a store over a fake bucket and makes every book row
// point at its archive, under the name in entry.
func bucketLibrary(t *testing.T, cfg archivestore.Config, entry *string) *s3test.Server {
	t.Helper()
	server := s3test.New(t)
	root := t.TempDir() + "/"
	cfg.Root, cfg.Prefix, cfg.CacheDir = root, "archives/", t.TempDir()
	cfg.Client = server.Client(time.Second, -1)
	store, err := archivestore.New(&cfg)
	require.NoError(t, err)
	previous := archivestore.Default()
	archivestore.SetDefault(store)
	t.Cleanup(func() { archivestore.SetDefault(previous) })

	files := viper.GetString("app.files_path")
	viper.Set("app.files_path", root)
	t.Cleanup(func() { viper.Set("app.files_path", files) })

	getter := bookByID
	bookByID = func(int64) (models.Book, error) {
		return models.Book{Path: bucketArchive, FileName: *entry, Title: "Book", Approved: true}, nil
	}
	t.Cleanup(func() { bookByID = getter })
	return server
}

func getBookFile(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.GET("/files/:format/:id", GetBookFile)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/files/fb2/1", http.NoBody)
	router.ServeHTTP(response, request)
	return response
}

// A book that came with a replaced archive, asked for while the store is down:
// the directory known here lacks it, and that proves nothing.
func TestNewBookDuringAnOutageIs503(t *testing.T) {
	entry := "0.fb2"
	server := bucketLibrary(t, archivestore.Config{}, &entry)
	server.Put("archives/"+bucketArchive, archiveOf(t, 1))
	require.Equal(t, http.StatusOK, getBookFile(t).Code)

	server.Put("archives/"+bucketArchive, archiveOf(t, 2))
	server.SetDown(true)
	entry = "1.fb2"
	response := getBookFile(t)
	assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	assert.Equal(t, "30", response.Header().Get("Retry-After"))

	server.SetDown(false)
	assert.Equal(t, http.StatusOK, getBookFile(t).Code)
}
