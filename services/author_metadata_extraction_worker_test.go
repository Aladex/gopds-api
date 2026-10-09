package services_test

import (
	"archive/zip"
	"context"
	"crypto/md5" // #nosec G501 -- the book-content identity the pipeline pins, not a security use
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/parser"
	"gopds-api/internal/scanfixture"
	"gopds-api/internal/testdb"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The extraction worker is the vertical slice of contract 3.3/3.7/3.9: it
// claims run items, opens each archive once, extracts metadata without ever
// reading a body past </description>, classifies every failure into the closed
// terminal mapping or the two systemic paths, and persists snapshot, credits,
// local jobs, item status and run counters in one short transaction per item.

const (
	workerExtractor  = services.AuthorMetadataExtractorVersion
	workerNormalizer = authornorm.NormalizerVersion
	workerFixtureDir = "testdata/author_metadata"
)

// workerRetryPolicy keeps retries immediate and deterministic: a 1ms base
// delay means a failed item is claimable again at once, and zero jitter makes
// the schedule exact.
func workerRetryPolicy(maxAttempts int) services.AuthorMetadataRetryPolicy {
	return services.AuthorMetadataRetryPolicy{
		MaxAttempts: maxAttempts,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
		Jitter:      func(time.Duration) time.Duration { return 0 },
	}
}

func workerConfig(
	t *testing.T,
	db *pg.DB,
	dir string,
	tweak func(*services.ExtractionWorkerConfig),
) *services.AuthorMetadataExtractionWorker {
	t.Helper()
	cfg := services.DefaultExtractionWorkerConfig(dir, parser.MetadataExtractor{
		MetadataMaxBytes: 1 << 20,
		ExtractorVersion: workerExtractor,
	})
	cfg.Lease = 30 * time.Second
	cfg.ClaimLimit = 8
	cfg.Retry = workerRetryPolicy(3)
	if tweak != nil {
		tweak(&cfg)
	}
	worker, err := services.NewAuthorMetadataExtractionWorker(db, services.ZipArchiveSource{}, &cfg)
	require.NoError(t, err)
	return worker
}

// copyFixtureArchive places one static fixture archive in a fresh directory,
// so a test can add its own archives next to it.
func copyFixtureArchive(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	payload, err := os.ReadFile(filepath.Join(workerFixtureDir, name))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), payload, 0o600))
	return dir
}

// fixtureEntry reads one entry of a static fixture archive, for MD5
// computation and byte-level assertions.
func fixtureEntry(t *testing.T, archive, entry string) []byte {
	t.Helper()
	r, err := zip.OpenReader(filepath.Join(workerFixtureDir, archive))
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		if f.Name != entry {
			continue
		}
		rc, err := f.Open()
		require.NoError(t, err)
		payload, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		return payload
	}
	t.Fatalf("entry %s not in %s", entry, archive)
	return nil
}

func writeTestArchive(t *testing.T, dir, name string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	require.NoError(t, err)
	w := zip.NewWriter(f)
	for entry, payload := range entries {
		zw, err := w.Create(entry)
		require.NoError(t, err)
		_, err = zw.Write(payload)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	require.NoError(t, f.Close())
}

func md5Of(payload []byte) string {
	sum := md5.Sum(payload) // #nosec G401 -- book-content identity, not a security primitive
	return hex.EncodeToString(sum[:])
}

func ptrString(s string) *string { return &s }

func seedWorkerBook(t *testing.T, db *pg.DB, id int64, path, entry, md5hex string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO opds_catalog_book
		(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES (?, ?, ?, 'fb2', now(), '', 'ru', 'worker fixture', '', ?)`, id, entry, path, md5hex)
	require.NoError(t, err)
}

func startWorkerRun(t *testing.T, db *pg.DB, ids ...int64) *models.AuthorMetadataRun {
	t.Helper()
	svc := services.NewAuthorMetadataRunService(db)
	run, err := svc.StartRun(context.Background(), &services.StartRunRequest{
		Mode:              models.AuthorMetadataRunSmoke,
		BookIDs:           ids,
		ExtractorVersion:  workerExtractor,
		NormalizerVersion: workerNormalizer,
	})
	require.NoError(t, err)
	return run
}

// bookStatuses maps book ID to run item status for one run.
func bookStatuses(t *testing.T, db *pg.DB, runID int64) map[int64]string {
	t.Helper()
	var rows []struct {
		BookID int64
		Status string
	}
	_, err := db.Query(&rows, `SELECT book_id, status::text FROM author_metadata_run_item WHERE run_id = ?`, runID)
	require.NoError(t, err)
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.BookID] = r.Status
	}
	return out
}

// runState is the projection of a run row the worker tests assert on.
type runState struct {
	status         string
	total          int
	terminal       int
	extractionDone bool
	class          *string
}

func runRow(t *testing.T, db *pg.DB, runID int64) runState {
	t.Helper()
	var st runState
	_, err := db.QueryOne(pg.Scan(&st.status, &st.total, &st.terminal, &st.extractionDone, &st.class), `
		SELECT status::text, items_total, items_terminal, extraction_completed_at IS NOT NULL, last_error_class
		FROM author_metadata_run WHERE id = ?`, runID)
	require.NoError(t, err)
	return st
}

// RED 11 (config half) and contract 3.8: a configuration with a non-positive
// concurrency, claim size, lease or attempt limit is rejected at load, never
// silently widened; the default worker concurrency is 1.
func TestExtractionWorkerConfigValidation(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()

	base := func() services.ExtractionWorkerConfig {
		cfg := services.DefaultExtractionWorkerConfig(dir, parser.MetadataExtractor{
			MetadataMaxBytes: 1 << 20, ExtractorVersion: workerExtractor,
		})
		cfg.Retry = workerRetryPolicy(3)
		return cfg
	}
	assert.Equal(t, 1, base().Concurrency, "default extraction concurrency is 1 (contract 3.8)")

	for name, break_ := range map[string]func(*services.ExtractionWorkerConfig){
		"zero concurrency":     func(c *services.ExtractionWorkerConfig) { c.Concurrency = 0 },
		"negative concurrency": func(c *services.ExtractionWorkerConfig) { c.Concurrency = -2 },
		"zero claim limit":     func(c *services.ExtractionWorkerConfig) { c.ClaimLimit = 0 },
		"zero lease":           func(c *services.ExtractionWorkerConfig) { c.Lease = 0 },
		"zero max attempts":    func(c *services.ExtractionWorkerConfig) { c.Retry.MaxAttempts = 0 },
		"no extractor":         func(c *services.ExtractionWorkerConfig) { c.Extractor = nil },
		"no archives dir":      func(c *services.ExtractionWorkerConfig) { c.ArchivesDir = "" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			break_(&cfg)
			_, err := services.NewAuthorMetadataExtractionWorker(db, services.ZipArchiveSource{}, &cfg)
			require.ErrorIs(t, err, services.ErrInvalidExtractionWorkerConfig)
		})
	}
}

// An idle worker is a no-op: no active run, or a paused one, gives no claims.
func TestExtractionWorkerIdle(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	ctx := context.Background()

	worker := workerConfig(t, db, dir, nil)
	processed, err := worker.ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Zero(t, processed, "no active run")

	book := int64(101)
	seedWorkerBook(t, db, book, "happy.zip", "multi_contributor.fb2", md5Of(fixtureEntry(t, "happy.zip", "multi_contributor.fb2")))
	run := startWorkerRun(t, db, book)
	require.NoError(t, services.NewAuthorMetadataRunService(db).PauseRun(ctx, run.ID))

	processed, err = worker.ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Zero(t, processed, "a paused run gives no claims")
	assert.Equal(t, map[int64]string{book: "pending"}, bookStatuses(t, db, run.ID))
}

// RED 3 + 10: the happy archive yields snapshots, credits, local jobs, the
// closed terminal statuses and extraction completion — which never waits for
// the pending local normalization jobs.
func TestExtractionWorkerHappyArchive(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	ctx := context.Background()

	multi := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	noAuthor := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	bookMulti, bookNoAuthor := int64(101), int64(102)
	seedWorkerBook(t, db, bookMulti, "happy.zip", "multi_contributor.fb2", md5Of(multi))
	seedWorkerBook(t, db, bookNoAuthor, "happy.zip", "translator_only.fb2", md5Of(noAuthor))
	run := startWorkerRun(t, db, bookMulti, bookNoAuthor)

	processed, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, processed)

	assert.Equal(t, map[int64]string{
		bookMulti:    "extracted",
		bookNoAuthor: "extracted_no_author",
	}, bookStatuses(t, db, run.ID))

	t.Run("snapshot and credits of the multi-contributor book", func(t *testing.T) {
		var snapshot struct {
			Outcome         string  `pg:"outcome"`
			BookMD5         string  `pg:"book_md5"`
			Title           *string `pg:"source_title"`
			Lang            *string `pg:"source_lang"`
			SrcLang         *string `pg:"source_src_lang"`
			DocumentID      *string `pg:"source_document_id"`
			DocumentVersion *string `pg:"source_document_version"`
			ArchivePath     string  `pg:"archive_path"`
			EntryName       string  `pg:"entry_name"`
			Origin          string  `pg:"origin"`
			RunID           *int64  `pg:"run_id"`
			IsCurrent       bool    `pg:"is_current"`
		}
		_, err := db.QueryOne(&snapshot, `
			SELECT outcome::text, book_md5, source_title, source_lang, source_src_lang,
				source_document_id, source_document_version, archive_path, entry_name, origin::text, run_id, is_current
			FROM book_metadata_snapshot WHERE book_id = ?`, bookMulti)
		require.NoError(t, err)
		assert.Equal(t, "extracted", snapshot.Outcome)
		assert.Equal(t, md5Of(multi), snapshot.BookMD5)
		require.NotNil(t, snapshot.Title)
		assert.Equal(t, "Метаданные тест", *snapshot.Title)
		require.NotNil(t, snapshot.Lang)
		assert.Equal(t, "ru", *snapshot.Lang)
		require.NotNil(t, snapshot.SrcLang)
		assert.Equal(t, "en", *snapshot.SrcLang)
		require.NotNil(t, snapshot.DocumentID)
		assert.Equal(t, "doc-multi", *snapshot.DocumentID)
		assert.Equal(t, "happy.zip", snapshot.ArchivePath)
		assert.Equal(t, "multi_contributor.fb2", snapshot.EntryName)
		assert.Equal(t, "backfill", snapshot.Origin)
		require.NotNil(t, snapshot.RunID)
		assert.Equal(t, run.ID, *snapshot.RunID)
		assert.True(t, snapshot.IsCurrent)

		var credits []struct {
			Role           string  `pg:"role"`
			Position       int     `pg:"position"`
			First          *string `pg:"source_first_name"`
			Last           *string `pg:"source_last_name"`
			Nickname       *string `pg:"source_nickname"`
			SourceID       *string `pg:"source_id"`
			Display        string  `pg:"source_display_name"`
			FingerprintLen int     `pg:"fingerprint_len"`
		}
		_, err = db.Query(&credits, `
			SELECT role::text, position, source_first_name, source_last_name, source_nickname,
				source_id, source_display_name, octet_length(source_fingerprint) AS fingerprint_len
			FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
			WHERE s.book_id = ? ORDER BY role::text, position`, bookMulti)
		require.NoError(t, err)
		// The whitespace-only author element creates no credit; the
		// empty-first-name one keeps its last name.
		require.Len(t, credits, 4)
		assert.Equal(t, "author", credits[0].Role)
		require.NotNil(t, credits[0].First)
		assert.Equal(t, "Иван", *credits[0].First)
		require.NotNil(t, credits[0].SourceID)
		assert.Equal(t, "au-1", *credits[0].SourceID)
		require.NotNil(t, credits[0].Nickname)
		assert.Equal(t, "ivanp", *credits[0].Nickname)
		assert.Equal(t, 32, credits[0].FingerprintLen)
		assert.Equal(t, "author", credits[2].Role)
		require.NotNil(t, credits[2].Last)
		assert.Equal(t, "Пустов", *credits[2].Last)
		assert.Equal(t, "translator", credits[3].Role)
	})

	t.Run("extraction completion ignores the pending local jobs", func(t *testing.T) {
		st := runRow(t, db, run.ID)
		assert.Equal(t, "running", st.status, "run completion is the pipeline's, not extraction's (phase 10)")
		assert.Equal(t, 2, st.total)
		assert.Equal(t, 2, st.terminal)
		assert.True(t, st.extractionDone, "extraction_completed_at is set with the last item")

		var pendingJobs int
		_, err := db.QueryOne(pg.Scan(&pendingJobs), `
			SELECT count(*) FROM contributor_normalization_job WHERE status = 'pending'`)
		require.NoError(t, err)
		assert.Equal(t, 3, pendingJobs, "one local job per author normalization key; translators get none")
	})
}

// RED 4: a missing entry ends only its own item; the neighbor still extracts.
func TestExtractionWorkerEntryMissing(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	ctx := context.Background()

	good := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	bookMissing, bookGood := int64(101), int64(102)
	seedWorkerBook(t, db, bookMissing, "happy.zip", "absent.fb2", md5Of(good))
	seedWorkerBook(t, db, bookGood, "happy.zip", "translator_only.fb2", md5Of(good))
	run := startWorkerRun(t, db, bookMissing, bookGood)

	processed, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, processed)
	assert.Equal(t, map[int64]string{
		bookMissing: "entry_missing",
		bookGood:    "extracted_no_author",
	}, bookStatuses(t, db, run.ID))

	st := runRow(t, db, run.ID)
	assert.Equal(t, 2, st.terminal)
	assert.True(t, st.extractionDone)
}

// RED 5: the closed error mapping of contract 3.3, one probe per class, and a
// healthy neighbor that keeps extracting.
func TestExtractionWorkerTerminalMapping(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "errors.zip")
	ctx := context.Background()

	good := fixtureEntry(t, "errors.zip", "multi_contributor.fb2")
	books := map[string]int64{
		"multi_contributor.fb2":        101,
		"foreign_root.fb2":             102,
		"malformed.fb2":                103,
		"enc_unsupported_declared.fb2": 104,
	}
	for entry, id := range books {
		md5hex := md5Of(good)
		if entry != "multi_contributor.fb2" {
			md5hex = md5Of(fixtureEntry(t, "errors.zip", entry))
		}
		seedWorkerBook(t, db, id, "errors.zip", entry, md5hex)
	}
	ids := []int64{101, 102, 103, 104}
	run := startWorkerRun(t, db, ids...)

	processed, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4, processed)
	assert.Equal(t, map[int64]string{
		101: "extracted",
		102: "invalid_fb2",
		103: "metadata_parse_failed",
		104: "unsupported_encoding",
	}, bookStatuses(t, db, run.ID))

	t.Run("metadata byte limit is metadata_parse_failed", func(t *testing.T) {
		finishActiveRun(t, db)
		book := int64(201)
		seedWorkerBook(t, db, book, "errors.zip", "multi_contributor.fb2", md5Of(good))
		run := startWorkerRun(t, db, book)
		worker := workerConfig(t, db, dir, func(cfg *services.ExtractionWorkerConfig) {
			cfg.Extractor = parser.MetadataExtractor{MetadataMaxBytes: 64, ExtractorVersion: workerExtractor}
		})
		processed, err := worker.ProcessAvailable(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, processed)
		assert.Equal(t, map[int64]string{book: "metadata_parse_failed"}, bookStatuses(t, db, run.ID))
	})
}

// RED 6: an archive that does not open is a systemic condition — the run
// auto-pauses with a closed class and no item gets a mass per-book status.
// This is the test the plan's mutation (classify archive-open failure as
// entry_missing for all items) must fail.
func TestExtractionWorkerArchiveOpenFailureIsSystemic(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	ctx := context.Background()

	// Not a zip at all.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "corrupt.zip"), []byte("this is not an archive"), 0o600))
	bookA, bookB := int64(101), int64(102)
	seedWorkerBook(t, db, bookA, "corrupt.zip", "a.fb2", md5Of([]byte("a")))
	seedWorkerBook(t, db, bookB, "corrupt.zip", "b.fb2", md5Of([]byte("b")))
	run := startWorkerRun(t, db, bookA, bookB)

	_, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)

	st := runRow(t, db, run.ID)
	assert.Equal(t, "paused", st.status, "an unreadable archive auto-pauses the run")
	require.NotNil(t, st.class)
	assert.Equal(t, "archive_unreadable", *st.class, "the run records the closed class, never free error text")
	assert.Zero(t, st.terminal, "no mass per-book terminal statuses")
	assert.Equal(t, map[int64]string{bookA: "pending", bookB: "pending"}, bookStatuses(t, db, run.ID))

	var snapshots int
	_, err = db.QueryOne(pg.Scan(&snapshots), `SELECT count(*) FROM book_metadata_snapshot`)
	require.NoError(t, err)
	assert.Zero(t, snapshots)

	// The attempts record the closed class; a retry is scheduled, so a resume
	// continues the same run.
	var classes []string
	_, err = db.Query(&classes, `
		SELECT a.error_class FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id
		WHERE i.run_id = ? ORDER BY a.id`, run.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"archive_unreadable", "archive_unreadable"}, classes)
}

// RED 7: a book without a legacy MD5 gets it computed streaming and filled
// only while empty; a non-empty MD5 is never rewritten.
func TestExtractionWorkerMD5Backfill(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	ctx := context.Background()

	payload := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	known := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	bookEmpty, bookKnown := int64(101), int64(102)
	seedWorkerBook(t, db, bookEmpty, "happy.zip", "multi_contributor.fb2", "")
	seedWorkerBook(t, db, bookKnown, "happy.zip", "translator_only.fb2", md5Of(known))
	run := startWorkerRun(t, db, bookEmpty, bookKnown)

	_, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{bookEmpty: "extracted", bookKnown: "extracted_no_author"}, bookStatuses(t, db, run.ID))

	var filledMD5, untouchedMD5 string
	_, err = db.QueryOne(pg.Scan(&filledMD5, &untouchedMD5), `
		SELECT (SELECT md5 FROM opds_catalog_book WHERE id = ?),
			(SELECT md5 FROM opds_catalog_book WHERE id = ?)`, bookEmpty, bookKnown)
	require.NoError(t, err)
	assert.Equal(t, md5Of(payload), filledMD5, "the streaming MD5 backfills the empty legacy column")
	assert.Equal(t, md5Of(known), untouchedMD5)

	var snapshotMD5 string
	_, err = db.QueryOne(pg.Scan(&snapshotMD5), `SELECT book_md5 FROM book_metadata_snapshot WHERE book_id = ?`, bookEmpty)
	require.NoError(t, err)
	assert.Equal(t, filledMD5, snapshotMD5, "the snapshot records the same stable MD5")
}

// RED 8: a second run of the same versions over the same books ends
// already_current, references the existing snapshot and writes nothing new.
func TestExtractionWorkerRetrySameVersions(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	ctx := context.Background()

	payload := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	book := int64(101)
	seedWorkerBook(t, db, book, "happy.zip", "multi_contributor.fb2", md5Of(payload))
	first := startWorkerRun(t, db, book)
	_, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	finishActiveRun(t, db)

	second := startWorkerRun(t, db, book)
	processed, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, map[int64]string{book: "already_current"}, bookStatuses(t, db, second.ID))

	var snapshots, credits, jobs int
	var itemSnapshot, snapshotID int64
	_, err = db.QueryOne(pg.Scan(&snapshots, &credits, &jobs, &itemSnapshot, &snapshotID), `
		SELECT (SELECT count(*) FROM book_metadata_snapshot),
			(SELECT count(*) FROM book_contributor_credit),
			(SELECT count(*) FROM contributor_normalization_job),
			(SELECT snapshot_id FROM author_metadata_run_item WHERE run_id = ?),
			(SELECT id FROM book_metadata_snapshot WHERE book_id = ?)`, second.ID, book)
	require.NoError(t, err)
	assert.Equal(t, 1, snapshots, "no duplicate snapshot")
	assert.Equal(t, 4, credits, "no duplicate credits")
	assert.Equal(t, 3, jobs, "no duplicate local jobs")
	assert.Equal(t, snapshotID, itemSnapshot, "the item references the existing snapshot")
	_ = first
}

// RED 9: a worker that crashes after its claim loses the lease; the restarted
// worker reclaims the item and the book is processed exactly once logically.
func TestExtractionWorkerRestartAfterAbandonedClaim(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	ctx := context.Background()

	payload := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	book := int64(101)
	seedWorkerBook(t, db, book, "happy.zip", "multi_contributor.fb2", md5Of(payload))
	run := startWorkerRun(t, db, book)

	// The crashed worker: claimed the item, never came back.
	crashed := database.NewLeaseOwner()
	claims, err := database.ClaimExtractionItems(ctx, db, crashed, database.LeaseClaimOptions{
		Limit: 1, Lease: time.Minute, MaxAttempts: 3,
	})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	// Its lease runs out.
	_, err = db.Exec(`UPDATE author_metadata_run_item
		SET lease_expires_at = clock_timestamp() - interval '1 microsecond' WHERE run_id = ?`, run.ID)
	require.NoError(t, err)

	processed, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, map[int64]string{book: "extracted"}, bookStatuses(t, db, run.ID))

	var snapshots, credits int
	_, err = db.QueryOne(pg.Scan(&snapshots, &credits), `
		SELECT (SELECT count(*) FROM book_metadata_snapshot),
			(SELECT count(*) FROM book_contributor_credit)`)
	require.NoError(t, err)
	assert.Equal(t, 1, snapshots, "one logical processing: one snapshot")
	assert.Equal(t, 4, credits)

	var attempts []struct {
		AttemptNo  int
		Outcome    *string
		ErrorClass *string
	}
	_, err = db.Query(&attempts, `
		SELECT a.attempt_no, a.outcome::text, a.error_class
		FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id
		WHERE i.run_id = ? ORDER BY a.attempt_no`, run.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.NotNil(t, attempts[0].ErrorClass)
	assert.Equal(t, "lease_expired", *attempts[0].ErrorClass, "the abandoned attempt closed as lease_expired")
	require.NotNil(t, attempts[1].Outcome)
	assert.Equal(t, "extracted", *attempts[1].Outcome)
}

// barrierExtractor measures how many extractions run at once: every call
// signals its entry, waits for the test to release it, and tracks the
// high-water mark of simultaneous calls.
type barrierExtractor struct {
	entered   chan struct{}
	release   chan struct{}
	current   atomic.Int32
	highWater atomic.Int32
}

func (b *barrierExtractor) Extract(in parser.ExtractBookInput) (authornorm.SourceMetadata, error) {
	n := b.current.Add(1)
	for {
		hw := b.highWater.Load()
		if n <= hw || b.highWater.CompareAndSwap(hw, n) {
			break
		}
	}
	b.entered <- struct{}{}
	<-b.release
	b.current.Add(-1)

	value, err := authornorm.NewSourceValue([]authornorm.SourceComponent{
		{Kind: authornorm.ComponentLast, Value: "Фикстура"},
	})
	if err != nil {
		return authornorm.SourceMetadata{}, err
	}
	return authornorm.SourceMetadata{
		BookID:           in.BookID,
		ArchivePath:      in.ArchivePath,
		EntryName:        in.EntryName,
		ExtractorVersion: workerExtractor,
		BookMD5:          in.KnownMD5,
		Title:            ptrString("barrier"),
		Outcome:          authornorm.OutcomeExtracted,
		Contributors: []authornorm.Contributor{
			{Role: authornorm.RoleAuthor, Position: 0, Value: value},
		},
	}, nil
}

// RED 11: the default worker concurrency is measurably 1 — a second extraction
// never starts while the first is in flight — and an injected concurrency of 2
// allows 2, never more.
func TestExtractionWorkerConcurrency(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()

	setup := func(t *testing.T, archives int, idBase int64) (string, *barrierExtractor) {
		dir := t.TempDir()
		payload := fixtureEntry(t, "happy.zip", "translator_only.fb2")
		var ids []int64
		for i := 0; i < archives; i++ {
			name := fmt.Sprintf("arch-%d.zip", i)
			writeTestArchive(t, dir, name, map[string][]byte{"book.fb2": payload})
			id := idBase + int64(i)
			seedWorkerBook(t, db, id, name, "book.fb2", md5Of(payload))
			ids = append(ids, id)
		}
		startWorkerRun(t, db, ids...)
		return dir, &barrierExtractor{entered: make(chan struct{}, archives), release: make(chan struct{}, archives)}
	}

	drain := func(t *testing.T, b *barrierExtractor, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			select {
			case <-b.entered:
			case <-time.After(10 * time.Second):
				t.Fatalf("extraction %d never started", i+1)
			}
		}
	}
	assertBlocked := func(t *testing.T, b *barrierExtractor, why string) {
		t.Helper()
		select {
		case <-b.entered:
			t.Fatalf("another extraction started past the concurrency limit: %s", why)
		case <-time.After(200 * time.Millisecond):
		}
	}

	t.Run("default concurrency is measurably 1", func(t *testing.T) {
		dir, barrier := setup(t, 2, 101)
		cfg := services.DefaultExtractionWorkerConfig(dir, barrier)
		cfg.Lease = 30 * time.Second
		cfg.ClaimLimit = 8
		cfg.Retry = workerRetryPolicy(3)
		worker, err := services.NewAuthorMetadataExtractionWorker(db, services.ZipArchiveSource{}, &cfg)
		require.NoError(t, err)

		done := make(chan error, 1)
		go func() { _, err := worker.ProcessAvailable(ctx); done <- err }()

		drain(t, barrier, 1)
		assertBlocked(t, barrier, "with the default concurrency the second book waits")
		barrier.release <- struct{}{}
		drain(t, barrier, 1)
		barrier.release <- struct{}{}
		require.NoError(t, <-done)
		assert.Equal(t, int32(1), barrier.highWater.Load(), "extractions never overlapped")
		finishActiveRun(t, db)
	})

	t.Run("injected 2 allows 2, not more", func(t *testing.T) {
		dir, barrier := setup(t, 3, 201)
		cfg := services.DefaultExtractionWorkerConfig(dir, barrier)
		cfg.Concurrency = 2
		cfg.Lease = 30 * time.Second
		cfg.ClaimLimit = 8
		cfg.Retry = workerRetryPolicy(3)
		worker, err := services.NewAuthorMetadataExtractionWorker(db, services.ZipArchiveSource{}, &cfg)
		require.NoError(t, err)

		done := make(chan error, 1)
		go func() { _, err := worker.ProcessAvailable(ctx); done <- err }()

		drain(t, barrier, 2)
		assertBlocked(t, barrier, "the third archive waits for a slot")
		barrier.release <- struct{}{}
		barrier.release <- struct{}{}
		drain(t, barrier, 1)
		barrier.release <- struct{}{}
		require.NoError(t, <-done)
		assert.Equal(t, int32(2), barrier.highWater.Load(), "two overlapped, never three")
		finishActiveRun(t, db)
	})
}

// The poison-book rule decided in phase 7: an item that exhausts its attempts
// becomes terminal metadata_parse_failed with the closed class
// max_attempts_exceeded, and the run continues with the other books.
func TestExtractionWorkerPoisonBookExhaustion(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	ctx := context.Background()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "corrupt.zip"), []byte("garbage"), 0o600))

	goodPayload := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	bookGood, bookCorrupt := int64(101), int64(102)
	// The good book seeds first, so its archive group is processed before the
	// corrupt one fails systemically.
	seedWorkerBook(t, db, bookGood, "happy.zip", "translator_only.fb2", md5Of(goodPayload))
	seedWorkerBook(t, db, bookCorrupt, "corrupt.zip", "c.fb2", md5Of([]byte("c")))
	run := startWorkerRun(t, db, bookGood, bookCorrupt)
	svc := services.NewAuthorMetadataRunService(db)

	worker := workerConfig(t, db, dir, func(cfg *services.ExtractionWorkerConfig) {
		cfg.Retry = workerRetryPolicy(2)
	})

	// Round 1: the corrupt archive pauses the run; its item keeps a retry.
	_, err := worker.ProcessAvailable(ctx)
	require.NoError(t, err)
	st := runRow(t, db, run.ID)
	assert.Equal(t, "paused", st.status)
	assert.Equal(t, map[int64]string{bookGood: "extracted_no_author", bookCorrupt: "pending"}, bookStatuses(t, db, run.ID))

	// Round 2: the last attempt exhausts; the item ends metadata_parse_failed
	// and the run can continue.
	require.NoError(t, svc.ResumeRun(ctx, run.ID))
	_, err = worker.ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{bookGood: "extracted_no_author", bookCorrupt: "metadata_parse_failed"},
		bookStatuses(t, db, run.ID))

	var outcome, class *string
	_, err = db.QueryOne(pg.Scan(&outcome, &class), `
		SELECT a.outcome::text, a.error_class
		FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id
		WHERE i.run_id = ? AND i.book_id = ? AND a.attempt_no = 2`, run.ID, bookCorrupt)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.Equal(t, "metadata_parse_failed", *outcome)
	require.NotNil(t, class)
	assert.Equal(t, "max_attempts_exceeded", *class)

	// The run continues: resume, the worker finds nothing left and closes
	// extraction accounting.
	require.NoError(t, svc.ResumeRun(ctx, run.ID))
	_, err = worker.ProcessAvailable(ctx)
	require.NoError(t, err)
	st = runRow(t, db, run.ID)
	assert.Equal(t, 2, st.total)
	assert.Equal(t, 2, st.terminal)
	assert.True(t, st.extractionDone)
}

// RED 13: neither success nor error paths log author names, titles or raw
// metadata — only counts, IDs and closed classes (contract 3.14).
func TestExtractionWorkerLogPrivacy(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "errors.zip")
	ctx := context.Background()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "corrupt.zip"), []byte("garbage"), 0o600))

	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	for i, entry := range []string{"multi_contributor.fb2", "foreign_root.fb2", "enc_unsupported_declared.fb2"} {
		seedWorkerBook(t, db, int64(101+i), "errors.zip", entry, md5Of(fixtureEntry(t, "errors.zip", entry)))
	}
	seedWorkerBook(t, db, 104, "corrupt.zip", "c.fb2", md5Of([]byte("c")))
	// A missing entry and a missing-MD5 book exercise the remaining log paths.
	seedWorkerBook(t, db, 105, "errors.zip", "absent.fb2", md5Of([]byte("x")))
	run := startWorkerRun(t, db, 101, 102, 103, 105)

	_, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)

	finishActiveRun(t, db)
	run2 := startWorkerRun(t, db, 104)
	_, err = workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	_ = run2

	out := logText(t, hook)
	for _, secret := range []string{
		"Иван", "Петров", "Сидорова", "ivanp", "Метаданные тест",
		"Чужой", "Корнев", "Перевод без автора", "Ольга", "Переводская",
	} {
		assert.NotContains(t, out, secret, "log output leaks source metadata")
	}
	_ = run
}

// The glue contract of the shared mapping helper, proven end to end: extract
// the duplicate-components fixture, persist through the worker, and the
// structural duplicate_component flag reaches the credit row. A mapping that
// drops the flag fails this test.
func TestExtractionWorkerDuplicateComponentFlagEndToEnd(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	ctx := context.Background()

	payload, err := os.ReadFile(filepath.Join(scanfixture.ModuleRoot(),
		"internal", "parser", "testdata", "metadata", "duplicate_components.fb2"))
	require.NoError(t, err)
	writeTestArchive(t, dir, "duplicates.zip", map[string][]byte{"duplicate_components.fb2": payload})

	book := int64(101)
	seedWorkerBook(t, db, book, "duplicates.zip", "duplicate_components.fb2", md5Of(payload))
	run := startWorkerRun(t, db, book)

	processed, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, map[int64]string{book: "extracted"}, bookStatuses(t, db, run.ID))

	var flags []string
	var first *string
	_, err = db.QueryOne(pg.Scan(pg.Array(&flags), &first), `
		SELECT c.quality_flags, c.source_first_name
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE s.book_id = ? AND c.role = 'author'`, book)
	require.NoError(t, err)
	assert.Contains(t, flags, "duplicate_component",
		"the repeated first-name element reaches the credit as a structural flag")
	require.NotNil(t, first)
	assert.Equal(t, "Пётр", *first, "the first occurrence keeps the named field")
}

// TestExtractionWorkerRealDataCheck is the phase-8 real-data gate: it runs the
// worker against the restored catalog database for the books of the two sample
// archives with the most books, and reports the terminal-status counts, the
// throughput and the peak RSS. It only runs when GOPDS_REALDATA_ARCHIVES
// points at the sample directory; it writes a real smoke run to the configured
// database, so it never runs against a scratch one.
func TestExtractionWorkerRealDataCheck(t *testing.T) {
	archivesDir := os.Getenv("GOPDS_REALDATA_ARCHIVES")
	if archivesDir == "" {
		t.Skip("set GOPDS_REALDATA_ARCHIVES to the sample archives directory to run the real-data check")
	}
	cfg, ok := testdb.Configured()
	if !ok {
		t.Skip(testdb.SkipReason)
	}
	db, err := testdb.Connect(cfg, nil)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	active, err := database.ActiveRun(ctx, db)
	require.NoError(t, err)
	require.Nil(t, active, "an active run exists; refusing to disturb the configured database")

	// The two sample archives with the most catalog books.
	entries, err := os.ReadDir(archivesDir)
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".zip") {
			names = append(names, entry.Name())
		}
	}
	require.NotEmpty(t, names)
	var picked []struct {
		Path  string `pg:"path"`
		Books int    `pg:"books"`
	}
	_, err = db.Query(&picked, `
		SELECT path, count(*) AS books FROM opds_catalog_book
		WHERE path IN (?) GROUP BY path ORDER BY books DESC LIMIT 2`, pg.In(names))
	require.NoError(t, err)
	require.Len(t, picked, 2, "fewer than two sample archives have catalog books")

	var bookIDs []int64
	_, err = db.Query(&bookIDs, `
		SELECT id FROM opds_catalog_book WHERE path IN (?, ?) ORDER BY id`, picked[0].Path, picked[1].Path)
	require.NoError(t, err)

	run, err := services.NewAuthorMetadataRunService(db).StartRun(ctx, &services.StartRunRequest{
		Mode:              models.AuthorMetadataRunSmoke,
		BookIDs:           bookIDs,
		ExtractorVersion:  services.AuthorMetadataExtractorVersion,
		NormalizerVersion: authornorm.NormalizerVersion,
	})
	require.NoError(t, err)

	workerCfg := services.DefaultExtractionWorkerConfig(archivesDir, parser.MetadataExtractor{
		MetadataMaxBytes: int64(services.AuthorMetadataMaxBytes),
		ExtractorVersion: services.AuthorMetadataExtractorVersion,
	})
	workerCfg.Retry = workerRetryPolicy(3)
	worker, err := services.NewAuthorMetadataExtractionWorker(db, services.ZipArchiveSource{}, &workerCfg)
	require.NoError(t, err)

	start := time.Now()
	processed, err := worker.ProcessAvailable(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err)

	var counts []struct {
		Status string `pg:"status"`
		Items  int    `pg:"items"`
	}
	_, err = db.Query(&counts, `
		SELECT status::text AS status, count(*) AS items FROM author_metadata_run_item
		WHERE run_id = ? GROUP BY status ORDER BY items DESC`, run.ID)
	require.NoError(t, err)

	st := runRow(t, db, run.ID)
	rssKB := peakRSSKB(t)

	t.Logf("real-data check: archives %s (%d books) and %s (%d books)",
		picked[0].Path, picked[0].Books, picked[1].Path, picked[1].Books)
	t.Logf("real-data check: run %d status=%s total=%d terminal=%d extraction_completed=%v class=%v",
		run.ID, st.status, st.total, st.terminal, st.extractionDone, st.class)
	for _, c := range counts {
		t.Logf("real-data check: status %-24s %d", c.Status, c.Items)
	}
	t.Logf("real-data check: processed=%d in %s → %.1f items/sec; peak RSS %.1f MiB",
		processed, elapsed.Round(time.Millisecond), float64(processed)/elapsed.Seconds(), float64(rssKB)/1024)
}

// peakRSSKB reads the process high-water mark from procfs.
func peakRSSKB(t *testing.T) int64 {
	t.Helper()
	status, err := os.ReadFile("/proc/self/status")
	require.NoError(t, err)
	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		fields := strings.Fields(line)
		require.Len(t, fields, 3)
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		require.NoError(t, err)
		return kb
	}
	t.Fatal("VmHWM not found in /proc/self/status")
	return 0
}
