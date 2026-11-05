package services

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/internal/archivestore"
	"gopds-api/internal/objectstore/s3test"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const remoteArchiveName = "remote.zip"

func remoteZip(t *testing.T, entries int) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for i := range entries {
		f, err := w.Create(fmt.Sprintf("%d.fb2", i))
		require.NoError(t, err)
		_, err = fmt.Fprintf(f, "book %d", i)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return out.Bytes()
}

// remoteLibrary installs a store over a fake bucket holding one archive, and
// returns the directory it pretends to live in.
func remoteLibrary(t *testing.T, entries int) (server *s3test.Server, root string) {
	t.Helper()
	server = s3test.New(t)
	server.Put("archives/"+remoteArchiveName, remoteZip(t, entries))
	root = t.TempDir() + "/"
	store, err := archivestore.New(&archivestore.Config{
		Root: root, Client: server.Client(200*time.Millisecond, -1),
		Prefix: "archives/", CacheDir: t.TempDir(),
	})
	require.NoError(t, err)
	previous := archivestore.Default()
	archivestore.SetDefault(store)
	t.Cleanup(func() { archivestore.SetDefault(previous) })
	return server, root
}

func TestExtractionSourceReadsARemoteArchiveWithoutCopyingIt(t *testing.T) {
	server, root := remoteLibrary(t, 40)
	archive, err := ZipArchiveSource{}.Open(context.Background(), root+remoteArchiveName)
	require.NoError(t, err)
	defer func() { _ = archive.Close() }()

	entry, err := archive.OpenEntry("7.fb2")
	require.NoError(t, err)
	data, err := io.ReadAll(entry)
	require.NoError(t, err)
	_ = entry.Close()

	assert.Equal(t, "book 7", string(data))
	_, err = archive.OpenEntry("absent.fb2")
	assert.ErrorIs(t, err, ErrArchiveEntryGone)
	assert.Less(t, server.Requests(), 5, "the worker's open must stay cheap: it opens per claim")
}

func TestExtractionSourceTellsAMissingArchiveFromAnOutage(t *testing.T) {
	server, root := remoteLibrary(t, 1)
	_, err := ZipArchiveSource{}.Open(context.Background(), root+"absent.zip")
	assert.ErrorIs(t, err, fs.ErrNotExist)

	server.SetDown(true)
	_, err = ZipArchiveSource{}.Open(context.Background(), root+remoteArchiveName)
	require.Error(t, err)
	assert.False(t, errors.Is(err, fs.ErrNotExist), "an outage must not read as a missing archive")
}

func TestPreviewLoadsARemoteBook(t *testing.T) {
	_, root := remoteLibrary(t, 5)
	loader := NewZipArchiveLoader(root)

	data, err := loader.Load(context.Background(), remoteArchiveName, "3.fb2", 1<<20)

	require.NoError(t, err)
	assert.Equal(t, "book 3", string(data))
}

func TestPreviewRefusesAnOversizedRemoteBookWithoutReadingIt(t *testing.T) {
	server, root := remoteLibrary(t, 5)
	loader := NewZipArchiveLoader(root)
	require.NoError(t, archivestore.Default().Check(context.Background(), root+remoteArchiveName))
	server.Reset()

	_, err := loader.Load(context.Background(), remoteArchiveName, "3.fb2", 2)

	assert.ErrorIs(t, err, ErrFB2TooLarge)
	assert.Zero(t, server.Requests())
}

func TestPreviewNamesAnEntryMissingFromARemoteArchive(t *testing.T) {
	_, root := remoteLibrary(t, 2)
	_, err := NewZipArchiveLoader(root).Load(context.Background(), remoteArchiveName, "absent.fb2", 0)
	assert.ErrorIs(t, err, ErrArchiveFileNotFound)
}

func TestRescanExtractsFromARemoteArchive(t *testing.T) {
	_, root := remoteLibrary(t, 5)
	service := &RescanService{}

	data, err := service.extractFB2FromArchive(root+remoteArchiveName, "2.fb2")
	require.NoError(t, err)
	assert.Equal(t, "book 2", string(data))

	_, err = service.extractFB2FromArchive(root+remoteArchiveName, "absent.fb2")
	assert.EqualError(t, err, "file not found in archive: absent.fb2")
}

func TestRescanInAnOutageSaysSo(t *testing.T) {
	server, root := remoteLibrary(t, 5)
	server.SetDown(true)
	_, err := (&RescanService{}).extractFB2FromArchive(root+remoteArchiveName, "2.fb2")
	assert.ErrorIs(t, err, archivestore.ErrUnavailable)
}

func TestRemoteOnlyArchivesJoinTheLocalListing(t *testing.T) {
	server, root := remoteLibrary(t, 1)
	server.Put("archives/both.zip", remoteZip(t, 1))
	scanner := &BookScanService{archivesDir: root}

	remote := scanner.remoteOnlyArchives([]string{filepath.Join(root, "both.zip")})

	assert.Equal(t, []string{filepath.Join(root, remoteArchiveName)}, remote)
}

func TestRemoteListingFailureLeavesTheLocalScanAlone(t *testing.T) {
	server, root := remoteLibrary(t, 1)
	server.SetDown(true)
	scanner := &BookScanService{archivesDir: root}
	assert.Empty(t, scanner.remoteOnlyArchives(nil))
}

func TestLocalInstallationListsNothingRemote(t *testing.T) {
	scanner := &BookScanService{archivesDir: t.TempDir()}
	assert.Empty(t, scanner.remoteOnlyArchives(nil))
}

func TestGroupBooksByArchive(t *testing.T) {
	groups := groupBooksByArchive([]models.Book{
		{ID: 1, Path: "b.zip"}, {ID: 2, Path: "a.zip"}, {ID: 3, Path: "b.zip"},
	})
	require.Len(t, groups, 2)
	assert.Equal(t, "b.zip", groups[0].path)
	assert.Len(t, groups[0].books, 2)
	assert.Equal(t, "a.zip", groups[1].path)
}

func TestArchiveWithNothingToHashIsNotOpened(t *testing.T) {
	server, root := remoteLibrary(t, 1)
	group := archiveBooks{path: remoteArchiveName, books: []models.Book{{ID: 1, MD5: "done"}}}

	archive, err := group.open(context.Background(), root+remoteArchiveName)

	require.NoError(t, err)
	assert.Nil(t, archive)
	assert.Zero(t, server.Requests())
}

func TestHashingManyBooksCopiesTheArchiveOnce(t *testing.T) {
	server, root := remoteLibrary(t, 50)
	books := make([]models.Book, 50)
	for i := range books {
		books[i] = models.Book{ID: int64(i), Path: remoteArchiveName, FileName: fmt.Sprintf("%d.fb2", i)}
	}
	group := archiveBooks{path: remoteArchiveName, books: books}

	archive, err := group.open(context.Background(), root+remoteArchiveName)
	require.NoError(t, err)
	server.Reset()
	for i := range books {
		hash, hashErr := bookMD5(archive, books[i].FileName)
		require.NoError(t, hashErr)
		assert.Len(t, hash, 32)
	}
	require.NoError(t, archive.Close())

	assert.Zero(t, server.Requests(), "the books came from the copy")
}

func TestHashingAFewBooksDoesNotCopyTheArchive(t *testing.T) {
	server, root := remoteLibrary(t, 50)
	hash, err := computeBookMD5(root+remoteArchiveName, "9.fb2")
	require.NoError(t, err)
	assert.Len(t, hash, 32)
	assert.LessOrEqual(t, server.Requests(), 3)
}
