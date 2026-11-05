package services_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/archivestore"
	"gopds-api/internal/objectstore/s3test"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bucketLibrary installs a store over a fake bucket, and returns its root and
// the directory temporary copies go to.
func bucketLibrary(t *testing.T) (server *s3test.Server, root, tempDir string) {
	t.Helper()
	server = s3test.New(t)
	root = t.TempDir() + "/"
	cacheDir := t.TempDir()
	store, err := archivestore.New(&archivestore.Config{
		Root: root, Client: server.Client(time.Second, -1), Prefix: "archives/", CacheDir: cacheDir,
	})
	require.NoError(t, err)
	previous := archivestore.Default()
	archivestore.SetDefault(store)
	t.Cleanup(func() { archivestore.SetDefault(previous) })
	return server, root, filepath.Join(cacheDir, "tmp")
}

func emptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a temporary copy was left behind")
}

func TestScanOfAnArchiveOnlyInObjectStorage(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	database.SetDB(db)
	server, root, tempDir := bucketLibrary(t)
	source := scanfixture.WriteArchive(t, t.TempDir(), time.Now())
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	server.Put("archives/"+scanfixture.ArchiveName, data)
	scanner := services.NewBookScanService(root, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)

	unscanned, err := scanner.GetUnscannedArchives()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(root, scanfixture.ArchiveName)}, unscanned)

	report, err := scanner.ScanArchive(unscanned[0])

	require.NoError(t, err)
	assert.Len(t, scanfixture.Entries(), report.BooksProcessed)
	emptyDir(t, tempDir)
	_, statErr := os.Stat(unscanned[0])
	assert.True(t, os.IsNotExist(statErr), "a scan must not bring the archive back to disk")

	unscanned, err = scanner.GetUnscannedArchives()
	require.NoError(t, err)
	assert.Empty(t, unscanned, "a scanned archive stays scanned with no file on disk")
}

func TestFailedFetchIsOneFailedArchive(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	database.SetDB(db)
	server, root, tempDir := bucketLibrary(t)
	server.Put("archives/broken.zip", []byte("this is not a zip archive at all"))
	scanner := services.NewBookScanService(root, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)

	report, err := scanner.ScanArchive(filepath.Join(root, "broken.zip"))

	require.Error(t, err)
	require.NotNil(t, report)
	assert.NotEmpty(t, report.Errors)
	emptyDir(t, tempDir)
}

func TestDuplicateScanAcrossDiskBucketAndMissingArchives(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	server, root, tempDir := bucketLibrary(t)

	// One archive on disk, one only in the bucket, one nowhere.
	local := scanfixture.WriteArchive(t, root, time.Now())
	data, err := os.ReadFile(local)
	require.NoError(t, err)
	server.Put("archives/remote.zip", data)

	id := int64(900000)
	insert := func(path, name string) int64 {
		id++
		_, execErr := db.Exec(`INSERT INTO opds_catalog_book
			(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
			VALUES (?, ?, ?, 'fb2', now(), '', 'ru', 'duplicate fixture', '', '')`, id, name, path)
		require.NoError(t, execErr)
		return id
	}
	name := scanfixture.Entries()[0]
	onDisk := insert(scanfixture.ArchiveName, name)
	inBucket := insert("remote.zip", name)
	nowhere := insert("gone.zip", name)
	job := &models.AdminScanJob{Status: "pending"}
	_, err = db.Model(job).Insert()
	require.NoError(t, err)

	err = services.ScanDuplicates(context.Background(), db, job.ID, nil, root, 2)
	require.NoError(t, err)

	hash := func(bookID int64) string {
		var value string
		_, queryErr := db.QueryOne(pg.Scan(&value), `SELECT coalesce(md5, '') FROM opds_catalog_book WHERE id = ?`, bookID)
		require.NoError(t, queryErr)
		return value
	}
	assert.Len(t, hash(onDisk), 32)
	assert.Equal(t, hash(onDisk), hash(inBucket), "the same bytes hash the same wherever they are")
	assert.Empty(t, hash(nowhere), "a missing archive fails its own books only")

	finished := &models.AdminScanJob{ID: job.ID}
	require.NoError(t, db.Model(finished).WherePK().Select())
	assert.Equal(t, "completed", finished.Status)
	assert.GreaterOrEqual(t, finished.ProcessedBooks, 3)
	emptyDir(t, tempDir)
}
