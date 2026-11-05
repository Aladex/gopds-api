package api

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore/s3test"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCoverPath = "/lib/book-fb2.jpg"

// coverRoutes serves the two cover routes over a fresh posters directory.
func coverRoutes(t *testing.T) (route *gin.Engine, dir string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir = t.TempDir()
	viper.Set("app.posters_path", dir)
	viper.Set("app.thumbs_path", "")
	t.Cleanup(func() {
		viper.Set("app.posters_path", "")
		viper.Set("app.thumbs_path", "")
	})
	route = gin.New()
	route.GET("/books-posters/*filepath", Posters)
	route.GET("/books-thumbs/*filepath", Thumbs(NewThumbs()))
	return route, dir
}

func withRemoteCovers(t *testing.T) *s3test.Server {
	t.Helper()
	server := s3test.New(t)
	previous := coverstore.Default()
	coverstore.SetDefault(coverstore.New(server.Client(200*time.Millisecond, -1), "covers/"))
	t.Cleanup(func() { coverstore.SetDefault(previous) })
	return server
}

func testCover(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, 480, 640)), nil))
	return out.Bytes()
}

func writeTestCover(t *testing.T, dir string, data []byte) {
	t.Helper()
	full := filepath.Join(dir, testCoverPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
	require.NoError(t, os.WriteFile(full, data, 0o600))
}

func get(route http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	route.ServeHTTP(recorder, request)
	return recorder
}

func TestPosterOnDiskCarriesCacheHeaders(t *testing.T) {
	route, dir := coverRoutes(t)
	cover := testCover(t)
	writeTestCover(t, dir, cover)

	response := get(route, "/books-posters"+testCoverPath)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	assert.Equal(t, coverCacheControl, response.Header().Get("Cache-Control"))
	assert.Equal(t, cover, response.Body.Bytes())
	modified := response.Header().Get("Last-Modified")
	require.NotEmpty(t, modified)

	again := get(route, "/books-posters"+testCoverPath, "If-Modified-Since", modified)
	assert.Equal(t, http.StatusNotModified, again.Code)
	assert.Empty(t, again.Body.Bytes())
}

func TestPosterKeepsTheTypeOfWhatItIs(t *testing.T) {
	route, dir := coverRoutes(t)
	writeTestCover(t, dir, []byte("\x89PNG\r\n\x1a\n, whatever the .jpg says"))
	response := get(route, "/books-posters"+testCoverPath)
	assert.Equal(t, "image/png", response.Header().Get("Content-Type"))
}

func TestMissingPosterIsStillNoCoverWith200(t *testing.T) {
	route, _ := coverRoutes(t)
	response := get(route, "/books-posters"+testCoverPath)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "image/png", response.Header().Get("Content-Type"))
	assert.Equal(t, noCoverRevalidate, response.Header().Get("Cache-Control"))
	assert.NotEmpty(t, response.Body.Bytes())
}

func TestPosterOnlyInObjectStorage(t *testing.T) {
	route, _ := coverRoutes(t)
	server := withRemoteCovers(t)
	cover := testCover(t)
	server.Put("covers"+testCoverPath, cover)

	response := get(route, "/books-posters"+testCoverPath)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, cover, response.Body.Bytes())
	assert.NotEmpty(t, response.Header().Get("Last-Modified"))
	assert.Equal(t, coverCacheControl, response.Header().Get("Cache-Control"))
}

func TestPosterInAnOutageIsNoCoverThatIsNotKept(t *testing.T) {
	route, _ := coverRoutes(t)
	server := withRemoteCovers(t)
	server.SetDown(true)

	response := get(route, "/books-posters"+testCoverPath)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "image/png", response.Header().Get("Content-Type"))
	assert.Equal(t, noCoverDoNotKeep, response.Header().Get("Cache-Control"))
}

func TestThumbnailIsMadeAndCarriesCacheHeaders(t *testing.T) {
	route, dir := coverRoutes(t)
	cover := testCover(t)
	writeTestCover(t, dir, cover)

	response := get(route, "/books-thumbs"+testCoverPath)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	assert.Equal(t, coverCacheControl, response.Header().Get("Cache-Control"))
	assert.Less(t, response.Body.Len(), len(cover)+1)
	config, err := jpeg.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, 240, config.Width)

	again := get(route, "/books-thumbs"+testCoverPath, "If-Modified-Since", response.Header().Get("Last-Modified"))
	assert.Equal(t, http.StatusNotModified, again.Code)
}

func TestThumbnailOfAMissingOrBrokenCoverIs404(t *testing.T) {
	route, dir := coverRoutes(t)
	assert.Equal(t, http.StatusNotFound, get(route, "/books-thumbs"+testCoverPath).Code)

	writeTestCover(t, dir, []byte("not an image"))
	assert.Equal(t, http.StatusNotFound, get(route, "/books-thumbs"+testCoverPath).Code)
}

func TestThumbnailFromObjectStorageWithoutTheOriginal(t *testing.T) {
	route, dir := coverRoutes(t)
	server := withRemoteCovers(t)
	server.Put("covers"+testCoverPath, testCover(t))

	assert.Equal(t, http.StatusOK, get(route, "/books-thumbs"+testCoverPath).Code)
	_, err := os.Stat(filepath.Join(dir, defaultThumbsDir, testCoverPath))
	assert.NoError(t, err, "the thumbnail stays on local disk")
	_, err = os.Stat(filepath.Join(dir, testCoverPath))
	assert.True(t, os.IsNotExist(err), "the original was not brought back")

	server.Reset()
	assert.Equal(t, http.StatusOK, get(route, "/books-thumbs"+testCoverPath).Code)
	assert.Equal(t, 0, server.Requests())
}

func TestThumbnailInAnOutageIs503(t *testing.T) {
	route, _ := coverRoutes(t)
	server := withRemoteCovers(t)
	server.SetDown(true)
	assert.Equal(t, http.StatusServiceUnavailable, get(route, "/books-thumbs"+testCoverPath).Code)
}

func TestThumbnailPathCannotClimbOut(t *testing.T) {
	route, _ := coverRoutes(t)
	response := get(route, "/books-thumbs/../../etc/passwd")
	assert.NotEqual(t, http.StatusOK, response.Code)
}

func TestThumbnailDirectoryCanBeConfigured(t *testing.T) {
	_, _ = coverRoutes(t)
	dir := t.TempDir()
	viper.Set("app.thumbs_path", dir)
	route := gin.New()
	route.GET("/books-thumbs/*filepath", Thumbs(NewThumbs()))
	writeTestCover(t, viper.GetString("app.posters_path"), testCover(t))

	assert.Equal(t, http.StatusOK, get(route, "/books-thumbs"+testCoverPath).Code)
	_, err := os.Stat(filepath.Join(dir, testCoverPath))
	assert.NoError(t, err)
}

func TestUnreadableLocalPosterIs500(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	route, dir := coverRoutes(t)
	writeTestCover(t, dir, testCover(t))
	full := filepath.Join(dir, testCoverPath)
	require.NoError(t, os.Chmod(full, 0o000))
	t.Cleanup(func() { _ = os.Chmod(full, 0o600) })

	assert.Equal(t, http.StatusInternalServerError, get(route, "/books-posters"+testCoverPath).Code)
}

func TestUnreadableLocalPosterIs500WithStorageToo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	route, dir := coverRoutes(t)
	server := withRemoteCovers(t)
	writeTestCover(t, dir, testCover(t))
	full := filepath.Join(dir, testCoverPath)
	require.NoError(t, os.Chmod(full, 0o000))
	t.Cleanup(func() { _ = os.Chmod(full, 0o600) })

	assert.Equal(t, http.StatusInternalServerError, get(route, "/books-posters"+testCoverPath).Code)
	assert.Zero(t, server.Requests(), "a file that is there is not looked for elsewhere")
}
