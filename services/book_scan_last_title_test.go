package services_test

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/internal/scanfixture"
	"gopds-api/services"
)

// Round 2/3 (N1 + coverage): the completion frame must carry the title the
// ingest path actually parsed, a new scan job must not inherit the previous
// job's title, and one job's title must survive its trailing empty archives.

type scanEnvelope struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
	Data  struct {
		LastBookTitle string `json:"last_book_title"`
	} `json:"data"`
}

func drainCompletions(ch chan []byte) []scanEnvelope {
	var out []scanEnvelope
	for len(ch) > 0 {
		msg := <-ch
		var env scanEnvelope
		if err := json.Unmarshal(msg, &env); err != nil {
			continue
		}
		if env.Type == services.ScanCompleted {
			out = append(out, env)
		}
	}
	return out
}

func writeEmptyArchive(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// #nosec G304 -- a test fixture with a fixed name in the test's own dir
	f, err := os.Create(path)
	require.NoError(t, err)
	w := zip.NewWriter(f)
	require.NoError(t, w.Close())
	require.NoError(t, f.Close())
	return path
}

func TestScanCompletionTitleLifecycle(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	archive := scanfixture.WriteArchive(t, dir, time.Now())

	mgr := services.NewWebSocketManager()
	ch := make(chan []byte, 64)
	id := mgr.RegisterClient(nil, 1, "admin", true, ch)
	require.True(t, mgr.Subscribe(id, services.TopicScan))

	scanner := services.NewBookScanService(dir, t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	scanner.SetScanEventPublisher(services.NewScanEventPublisher(services.NewAdminWSConnection(mgr)))

	report, err := scanner.ScanArchive(archive)
	require.NoError(t, err)
	require.Positive(t, report.BooksProcessed)
	scanner.PublishScanCompleted(&services.ScanReport{TotalArchives: 1, ProcessedBooks: report.BooksProcessed})

	// The title on the completion frame is the one the parser read, i.e. what
	// the book row holds — not a value set by hand around the progress ticker.
	var dbTitle string
	_, err = db.QueryOne(pg.Scan(&dbTitle), `SELECT title FROM opds_catalog_book
		WHERE path = ? AND filename = ?`, scanfixture.ArchiveName, scanfixture.EntryLatin)
	require.NoError(t, err)

	completions := drainCompletions(ch)
	require.Len(t, completions, 1)
	assert.Equal(t, services.TopicScan, completions[0].Topic)
	assert.Equal(t, dbTitle, completions[0].Data.LastBookTitle)

	// A new scan job starts clean: with nothing processed, no stale title
	// leaks into its completion frame.
	scanner.BeginScanJob()
	empty := writeEmptyArchive(t, dir, "empty.zip")
	_, err = scanner.ScanArchive(empty)
	require.NoError(t, err)
	scanner.PublishScanCompleted(&services.ScanReport{TotalArchives: 1})

	completions = drainCompletions(ch)
	require.Len(t, completions, 1)
	assert.Empty(t, completions[0].Data.LastBookTitle)
}

// Within one scan job the title survives an archive that ingests nothing:
// the completion frame names the last book the job actually processed.
func TestScanJobTitleSurvivesTrailingEmptyArchive(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	archive := scanfixture.WriteArchive(t, dir, time.Now())

	mgr := services.NewWebSocketManager()
	ch := make(chan []byte, 64)
	id := mgr.RegisterClient(nil, 1, "admin", true, ch)
	require.True(t, mgr.Subscribe(id, services.TopicScan))

	scanner := services.NewBookScanService(dir, t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	scanner.SetScanEventPublisher(services.NewScanEventPublisher(services.NewAdminWSConnection(mgr)))

	// One job, two archives: the second one holds no books.
	scanner.BeginScanJob()
	report, err := scanner.ScanArchive(archive)
	require.NoError(t, err)
	require.Positive(t, report.BooksProcessed)
	empty := writeEmptyArchive(t, dir, "empty.zip")
	_, err = scanner.ScanArchive(empty)
	require.NoError(t, err)
	scanner.PublishScanCompleted(&services.ScanReport{TotalArchives: 2, ProcessedBooks: report.BooksProcessed})

	var dbTitle string
	_, err = db.QueryOne(pg.Scan(&dbTitle), `SELECT title FROM opds_catalog_book
		WHERE path = ? AND filename = ?`, scanfixture.ArchiveName, scanfixture.EntryLatin)
	require.NoError(t, err)

	completions := drainCompletions(ch)
	require.Len(t, completions, 1)
	assert.Equal(t, dbTitle, completions[0].Data.LastBookTitle,
		"a trailing empty archive must not erase the job's last ingested title")
}

// Round 4 (R3-N1): a scanner reused for a second job that finds nothing to
// scan must not republish the previous job's title with zero books.
func TestScanAllEmptySecondJobHasNoTitle(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	scanfixture.WriteArchive(t, dir, time.Now())

	mgr := services.NewWebSocketManager()
	ch := make(chan []byte, 64)
	id := mgr.RegisterClient(nil, 1, "admin", true, ch)
	require.True(t, mgr.Subscribe(id, services.TopicScan))

	scanner := services.NewBookScanService(dir, t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	scanner.SetScanEventPublisher(services.NewScanEventPublisher(services.NewAdminWSConnection(mgr)))

	// Job 1 ingests the fixture archive.
	report, err := scanner.ScanAll()
	require.NoError(t, err)
	require.Positive(t, report.ProcessedBooks)

	var dbTitle string
	_, err = db.QueryOne(pg.Scan(&dbTitle), `SELECT title FROM opds_catalog_book
		WHERE path = ? AND filename = ?`, scanfixture.ArchiveName, scanfixture.EntryLatin)
	require.NoError(t, err)

	// Job 2 reuses the scanner; every archive is already scanned.
	second, err := scanner.ScanAll()
	require.NoError(t, err)
	require.Zero(t, second.TotalArchives)

	completions := drainCompletions(ch)
	require.Len(t, completions, 2)
	assert.Equal(t, dbTitle, completions[0].Data.LastBookTitle)
	assert.Empty(t, completions[1].Data.LastBookTitle,
		"an empty follow-up job must not republish the previous job's title")
}
