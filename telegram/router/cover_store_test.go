package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore/s3test"
	"gopds-api/internal/posters"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func coverRouter(dir string) *Router {
	return &Router{cfg: Config{PostersPath: func() string { return dir }}}
}

func TestCoverOnDisk(t *testing.T) {
	dir := t.TempDir()
	book := &models.Book{Cover: true, Path: "a.zip", FileName: "b.fb2"}
	path := posters.FilePath(dir, book.Path, book.FileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("cover"), 0o600))
	r := coverRouter(dir)

	assert.Equal(t, path, r.coverPath(context.Background(), book))
	assert.Equal(t, []byte("cover"), r.readCover(context.Background(), path))
}

func TestCoverMissingAndEmptyAreNotFitToSend(t *testing.T) {
	dir := t.TempDir()
	book := &models.Book{Cover: true, Path: "a.zip", FileName: "b.fb2"}
	r := coverRouter(dir)
	assert.Empty(t, r.coverPath(context.Background(), book))

	path := posters.FilePath(dir, book.Path, book.FileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	assert.Empty(t, r.coverPath(context.Background(), book))
}

func TestCoverOnlyInObjectStorage(t *testing.T) {
	server := s3test.New(t)
	previous := coverstore.Default()
	coverstore.SetDefault(coverstore.New(server.Client(200*time.Millisecond, -1), "covers/"))
	t.Cleanup(func() { coverstore.SetDefault(previous) })
	book := &models.Book{Cover: true, Path: "a.zip", FileName: "b.fb2"}
	server.Put("covers/"+posters.RelativePath(book.Path, book.FileName), []byte("remote cover"))
	dir := t.TempDir()
	r := coverRouter(dir)

	path := r.coverPath(context.Background(), book)

	require.NotEmpty(t, path)
	assert.Equal(t, []byte("remote cover"), r.readCover(context.Background(), path))
}
