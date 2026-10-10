package services_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/parser"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A full run answers its start at once and is seeded by its own loop; one
// missing or broken archive ends its books instead of pausing the pass, while
// a volume that does not read pauses it as before; and a catalog-sized
// run's status reads stored figures instead of computing them.

// approveCurrentPilot records an approved pilot of the versions this build
// runs, the gate of a full run.
func approveCurrentPilot(t *testing.T, db *pg.DB) {
	t.Helper()
	var pilot int64
	_, err := db.QueryOne(pg.Scan(&pilot), `
		INSERT INTO author_metadata_run
			(mode, status, extractor_version, normalizer_version, selector_archive,
			 items_total, items_terminal, extraction_completed_at, finished_at)
		VALUES ('pilot_archive', 'completed', ?, ?, 'pilot.zip', 0, 0, now(), now())
		RETURNING id`, runServiceExtractor, runServiceNormalizer)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO author_metadata_pilot_approval
		(run_id, extractor_version, normalizer_version, approved_by_user_id)
		VALUES (?, ?, ?, 1)`, pilot, runServiceExtractor, runServiceNormalizer)
	require.NoError(t, err)
}

func seedingWorker(t *testing.T, db *pg.DB, dir string, batch int) *services.AuthorMetadataExtractionWorker {
	t.Helper()
	return workerConfig(t, db, dir, func(cfg *services.ExtractionWorkerConfig) { cfg.SeedBatch = batch })
}

// The start of a full run answers before any item exists, and the slot is
// taken from that moment; the run's loop seeds it batch by batch, a new
// process picking up where the old one stopped, and then extracts it.
func TestStartFullRunAnswersBeforeSeeding(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	approveCurrentPilot(t, db)
	for id := int64(11); id <= 15; id++ {
		seedCatalogBook(t, db, id, "gone.zip", "b.fb2")
	}
	svc := services.NewAuthorMetadataRunService(db)

	run, err := svc.StartRun(ctx, startRequest(models.AuthorMetadataRunFull))
	require.NoError(t, err)
	assert.Equal(t, models.AuthorMetadataRunPending, run.Status, "the start does not wait for the seeding")
	assert.Empty(t, runItems(t, db, run.ID))
	require.NotNil(t, run.SeedTarget)
	assert.Equal(t, 5, *run.SeedTarget)

	_, err = svc.StartRun(ctx, startRequest(models.AuthorMetadataRunFull))
	require.ErrorIs(t, err, database.ErrActiveRunExists, "a second full run is refused while the first is seeded")

	// One process seeds a batch and stops; the next one goes on.
	n, err := seedingWorker(t, db, t.TempDir(), 2).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "a seeding round reports its items as work done")
	assert.Equal(t, []int64{11, 12}, runItems(t, db, run.ID))
	assert.Equal(t, "pending", runRow(t, db, run.ID).status)

	next := seedingWorker(t, db, t.TempDir(), 2)
	for range 3 {
		_, err = next.ProcessAvailable(ctx)
		require.NoError(t, err)
	}
	assert.Equal(t, []int64{11, 12, 13, 14, 15}, runItems(t, db, run.ID), "every book once")
	st := runRow(t, db, run.ID)
	assert.NotEqual(t, "pending", st.status, "the seeded run started")
	assert.Equal(t, 5, st.total)
}

// singleRun drains the worker until nothing more happens.
func drain(t *testing.T, w *services.AuthorMetadataExtractionWorker) {
	t.Helper()
	for range 10 {
		n, err := w.ProcessAvailable(context.Background())
		require.NoError(t, err)
		if n == 0 {
			return
		}
	}
}

// A missing archive on a volume whose other archives open ends its books
// archive_missing, and the pass goes on — even when the missing archive is
// the first thing the worker meets, before it opened any archive itself.
func TestMissingArchiveDoesNotPauseThePass(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	good := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	seedWorkerBook(t, db, 101, "user_books.zip", "a.fb2", md5Of([]byte("a")))
	seedWorkerBook(t, db, 102, "user_books.zip", "b.fb2", md5Of([]byte("b")))
	seedWorkerBook(t, db, 103, "happy.zip", "translator_only.fb2", md5Of(good))
	run := startWorkerRun(t, db, 101, 102, 103)

	drain(t, workerConfig(t, db, dir, nil))

	st := runRow(t, db, run.ID)
	assert.NotEqual(t, "paused", st.status, "one missing archive does not pause the pass")
	assert.Nil(t, st.class)
	assert.Equal(t, map[int64]string{101: "archive_missing", 102: "archive_missing", 103: "extracted_no_author"},
		bookStatuses(t, db, run.ID))
	assert.Equal(t, 3, st.terminal)
}

// An archive that is there but does not open ends its books
// archive_unreadable while the volume's other archives open.
func TestBrokenArchiveEndsItsBooks(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "corrupt.zip"), []byte("not an archive"), 0o600))
	good := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "translator_only.fb2", md5Of(good))
	seedWorkerBook(t, db, 102, "corrupt.zip", "c.fb2", md5Of([]byte("c")))
	run := startWorkerRun(t, db, 101, 102)

	drain(t, workerConfig(t, db, dir, nil))

	st := runRow(t, db, run.ID)
	assert.NotEqual(t, "paused", st.status)
	assert.Equal(t, map[int64]string{101: "extracted_no_author", 102: "archive_unreadable"}, bookStatuses(t, db, run.ID))
	assert.Equal(t, []string{"archive_unreadable"}, attemptOutcomes(t, db, run.ID, 102),
		"the attempt closes with the per-book status")
}

func attemptOutcomes(t *testing.T, db *pg.DB, runID, bookID int64) []string {
	t.Helper()
	var outcomes []string
	_, err := db.Query(&outcomes, `SELECT coalesce(a.outcome, '') FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id
		WHERE i.run_id = ? AND i.book_id = ? ORDER BY a.attempt_no`, runID, bookID)
	require.NoError(t, err)
	return outcomes
}

// When no archive of the volume opens, the volume is gone: the run pauses as
// before and no book gets a per-book status.
func TestVolumeWideFailureStillPauses(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  func(t *testing.T) string
	}{
		{"empty mount point", func(t *testing.T) string { return t.TempDir() }},
		{"no root", func(t *testing.T) string { return filepath.Join(t.TempDir(), "unmounted") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := scanfixture.ScratchDB(t)
			seedWorkerBook(t, db, 101, "a.zip", "a.fb2", md5Of([]byte("a")))
			seedWorkerBook(t, db, 102, "b.zip", "b.fb2", md5Of([]byte("b")))
			seedWorkerBook(t, db, 103, "c.zip", "c.fb2", md5Of([]byte("c")))
			run := startWorkerRun(t, db, 101, 102, 103)

			_, err := workerConfig(t, db, tc.dir(t), nil).ProcessAvailable(context.Background())
			require.NoError(t, err)

			st := runRow(t, db, run.ID)
			assert.Equal(t, "paused", st.status, "a volume that does not read pauses the pass")
			require.NotNil(t, st.class)
			assert.Equal(t, "archive_unreadable", *st.class)
			assert.Zero(t, st.terminal)
			for book, status := range bookStatuses(t, db, run.ID) {
				assert.Equal(t, "pending", status, "book %d keeps no per-book status", book)
			}
		})
	}
}

// flakySource reads every archive through zip, except that every entry of
// flaky.zip fails mid-read.
type flakySource struct{ services.ZipArchiveSource }

func (s flakySource) Open(ctx context.Context, path string) (services.ArchiveReader, error) {
	if filepath.Base(path) == "flaky.zip" {
		return failingArchive{}, nil
	}
	return s.ZipArchiveSource.Open(ctx, path)
}

func runAdmin(t *testing.T, db *pg.DB) *services.AuthorMetadataRunAdmin {
	t.Helper()
	c := config.AuthorMetadataConfig{
		MetadataMaxBytes:   config.AuthorMetadataMaxBytes,
		Extraction:         config.AuthorMetadataStageConfig{Concurrency: 1, ClaimSize: 10, Lease: time.Minute, MaxAttempts: 3},
		LocalNormalization: config.AuthorMetadataStageConfig{Concurrency: 1, ClaimSize: 10, Lease: time.Minute, MaxAttempts: 3},
	}
	admin, err := services.NewAuthorMetadataRunAdmin(services.NewAuthorMetadataRunService(db), db, &c)
	require.NoError(t, err)
	t.Cleanup(admin.Wait)
	return admin
}

// A catalog-sized run reads its credit figures stored: before any is
// stored it answers at once with none (and the time they stand for is nil),
// its extraction numbers are exact all the same, and a refresh stores the
// figures a live computation gives.
func TestLargeRunStatusReadsStoredFigures(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	multi := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "multi_contributor.fb2", md5Of(multi))
	seedWorkerBook(t, db, 102, "gone.zip", "g.fb2", md5Of([]byte("g")))
	run := startWorkerRun(t, db, 101, 102)
	drain(t, workerConfig(t, db, dir, nil))

	live, err := runAdmin(t, db).Get(ctx, run.ID)
	require.NoError(t, err)
	require.NotNil(t, live.AggregatesAsOf, "a small run computes its figures live")
	require.NotZero(t, live.Credits.Selected+live.Credits.Review+live.Credits.Pending)

	large := runAdmin(t, db).WithLiveAggregateLimit(1)
	first, err := large.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, live.Stages.Extraction, first.Stages.Extraction, "extraction numbers are exact at any size")
	large.Wait()

	require.NoError(t, large.RefreshAggregates(ctx, run.ID))
	stored, err := large.Get(ctx, run.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.AggregatesAsOf)
	assert.Equal(t, live.Credits, stored.Credits)
	assert.Equal(t, live.Stages.Local, stored.Stages.Local)
	assert.Equal(t, live.Stages.Review, stored.Stages.Review)

	report, err := large.Report(ctx, run.ID)
	require.NoError(t, err)
	liveReport, err := runAdmin(t, db).Report(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, liveReport.Facts.DBGrowthBytes, report.Facts.DBGrowthBytes)
	assert.Equal(t, liveReport.Ready, report.Ready)
}

// Before anything is stored, a large run's read starts a refresh in the
// background and does not wait for it.
func TestLargeRunStatusRefreshesInTheBackground(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	seedWorkerBook(t, db, 101, "a.zip", "a.fb2", md5Of([]byte("a")))
	run := startWorkerRun(t, db, 101)

	large := runAdmin(t, db).WithLiveAggregateLimit(0)
	first, err := large.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.Nil(t, first.AggregatesAsOf, "nothing stored yet: the read does not compute inline")
	large.Wait()
	second, err := large.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.NotNil(t, second.AggregatesAsOf, "the background refresh stored the figures")
}

// A full run being seeded shows how far its seeding is.
func TestSeedingRunShowsItsProgress(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	approveCurrentPilot(t, db)
	for id := int64(11); id <= 13; id++ {
		seedCatalogBook(t, db, id, "a.zip", "b.fb2")
	}
	admin := runAdmin(t, db)
	st, err := admin.Start(ctx, startRequest(models.AuthorMetadataRunFull))
	require.NoError(t, err)
	require.NotNil(t, st.Seeding)
	assert.Equal(t, services.RunSeeding{Seeded: 0, Target: 3}, *st.Seeding)

	_, err = seedingWorker(t, db, t.TempDir(), 2).ProcessAvailable(ctx)
	require.NoError(t, err)
	st, err = admin.Get(ctx, st.Run.ID)
	require.NoError(t, err)
	require.NotNil(t, st.Seeding)
	assert.Equal(t, services.RunSeeding{Seeded: 2, Target: 3}, *st.Seeding)
	assert.EqualValues(t, 2, st.Stages.Extraction.Total)
}

// The problem archives of a run, and the two actions on one: delete the
// books' records — refused for an archive the run did not find broken — and
// retry, after the file came back.
func TestRunAdminProblemArchives(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	good := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	seedWorkerBook(t, db, 101, "user_books.zip", "a.fb2", md5Of([]byte("a")))
	seedWorkerBook(t, db, 102, "user_books.zip", "b.fb2", md5Of([]byte("b")))
	seedWorkerBook(t, db, 103, "lost.zip", "translator_only.fb2", md5Of(good))
	seedWorkerBook(t, db, 104, "happy.zip", "translator_only.fb2", md5Of(good))
	run := startWorkerRun(t, db, 101, 102, 103, 104)
	worker := workerConfig(t, db, dir, nil)
	drain(t, worker)
	admin := runAdmin(t, db)

	archives, err := admin.Archives(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, []database.RunProblemArchive{
		{Archive: "user_books.zip", Reason: models.AuthorMetadataRunItemArchiveMissing, Books: 2},
		{Archive: "lost.zip", Reason: models.AuthorMetadataRunItemArchiveMissing, Books: 1},
	}, archives)

	_, err = admin.DeleteArchive(ctx, run.ID, "happy.zip")
	require.ErrorIs(t, err, services.ErrArchiveNotFailed, "only an archive the pass found broken")
	_, err = admin.DeleteArchive(ctx, run.ID+100, "user_books.zip")
	require.ErrorIs(t, err, database.ErrRunNotFound)

	deletion, err := admin.DeleteArchive(ctx, run.ID, "user_books.zip")
	require.NoError(t, err)
	assert.EqualValues(t, 2, deletion.BooksTotal)
	drain(t, worker)
	st, err := admin.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, st.Stages.Extraction.Total, "the deleted books left the run")

	// The other file comes back: a retry reads its book again.
	writeTestArchive(t, dir, "lost.zip", map[string][]byte{"translator_only.fb2": good})
	reopened, err := admin.RetryArchive(ctx, run.ID, "lost.zip")
	require.NoError(t, err)
	assert.EqualValues(t, 1, reopened)
	drain(t, worker)
	assert.Equal(t, map[int64]string{103: "extracted_no_author", 104: "extracted_no_author"}, bookStatuses(t, db, run.ID))
	archives, err = admin.Archives(ctx, run.ID)
	require.NoError(t, err)
	assert.Empty(t, archives)
	reopened, err = admin.RetryArchive(ctx, run.ID, "lost.zip")
	require.NoError(t, err)
	assert.Zero(t, reopened, "nothing of the archive is broken any more")
}

func workerExtractorEngine() parser.MetadataExtractor {
	return parser.MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: workerExtractor}
}

// onceFailingSource fails the first open of every archive and opens it after.
type onceFailingSource struct {
	services.ZipArchiveSource
	mu   *sync.Mutex
	seen map[string]bool
}

func (s onceFailingSource) Open(ctx context.Context, path string) (services.ArchiveReader, error) {
	s.mu.Lock()
	first := !s.seen[path]
	s.seen[path] = true
	s.mu.Unlock()
	if first {
		return nil, errors.New("hiccup")
	}
	return s.ZipArchiveSource.Open(ctx, path)
}

// The archive that failed is no witness of the volume: an archive that
// fails and then opens, on a catalog with no other archive, proves
// nothing about the volume, and the run pauses as for a gone volume.
func TestTheFailingArchiveIsNoWitnessOfTheVolume(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := copyFixtureArchive(t, "happy.zip")
	good := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "translator_only.fb2", md5Of(good))
	run := startWorkerRun(t, db, 101)

	cfg := services.DefaultExtractionWorkerConfig(dir, workerExtractorEngine())
	cfg.Retry = workerRetryPolicy(3)
	worker, err := services.NewAuthorMetadataExtractionWorker(db,
		onceFailingSource{mu: &sync.Mutex{}, seen: map[string]bool{}}, &cfg)
	require.NoError(t, err)
	_, err = worker.ProcessAvailable(context.Background())
	require.NoError(t, err)

	st := runRow(t, db, run.ID)
	assert.Equal(t, "paused", st.status)
	assert.Equal(t, "pending", bookStatuses(t, db, run.ID)[101], "no per-book status from a self-witness")
}

// The deletion request answers before the deletion: with the archive's books
// held by another transaction the request still returns at once, pending; a
// second click is the same request; the list shows the progress; the loop
// deletes the books once they are free and the archive leaves the list.
func TestArchiveDeletionRequestReturnsBeforeTheDeletion(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	good := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	seedWorkerBook(t, db, 101, "user_books.zip", "a.fb2", md5Of([]byte("a")))
	seedWorkerBook(t, db, 102, "user_books.zip", "b.fb2", md5Of([]byte("b")))
	seedWorkerBook(t, db, 103, "happy.zip", "translator_only.fb2", md5Of(good))
	run := startWorkerRun(t, db, 101, 102, 103)
	worker := workerConfig(t, db, dir, nil)
	drain(t, worker)
	admin := runAdmin(t, db)

	blocker, err := db.Begin()
	require.NoError(t, err)
	_, err = blocker.Exec(`SELECT id FROM opds_catalog_book WHERE path = 'user_books.zip' FOR UPDATE`)
	require.NoError(t, err)

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	deletion, err := admin.DeleteArchive(reqCtx, run.ID, "user_books.zip")
	require.NoError(t, err, "the request does not wait for the deletion")
	assert.Equal(t, "pending", deletion.Status)
	assert.EqualValues(t, 2, deletion.BooksTotal)
	assert.Zero(t, deletion.BooksDeleted)
	again, err := admin.DeleteArchive(reqCtx, run.ID, "user_books.zip")
	require.NoError(t, err)
	assert.Equal(t, deletion.ID, again.ID, "a second click is the same request")

	archives, err := admin.Archives(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, archives, 1)
	require.NotNil(t, archives[0].Deletion)
	assert.Equal(t, database.ArchiveDeletionProgress{Deleted: 0, Total: 2}, *archives[0].Deletion)

	require.NoError(t, blocker.Rollback())
	drain(t, worker)
	archives, err = admin.Archives(ctx, run.ID)
	require.NoError(t, err)
	assert.Empty(t, archives, "the archive leaves the list once its books are gone")
	var books int
	_, err = db.QueryOne(pg.Scan(&books), `SELECT count(*) FROM opds_catalog_book WHERE path = 'user_books.zip'`)
	require.NoError(t, err)
	assert.Zero(t, books)
}

// completedNoAuthorRun completes a pilot_archive run of one book without
// author credits: the extraction loop completes it and stores its figures.
func completedNoAuthorRun(t *testing.T, db *pg.DB) *models.AuthorMetadataRun {
	t.Helper()
	dir := copyFixtureArchive(t, "happy.zip")
	good := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "translator_only.fb2", md5Of(good))
	svc := services.NewAuthorMetadataRunService(db)
	run, err := svc.StartRun(context.Background(), &services.StartRunRequest{
		Mode: models.AuthorMetadataRunPilotArchive, Archive: "happy.zip",
		ExtractorVersion: workerExtractor, NormalizerVersion: workerNormalizer,
	})
	require.NoError(t, err)
	drain(t, workerConfig(t, db, dir, nil))
	require.Equal(t, "completed", runRow(t, db, run.ID).status)
	return run
}

// storeStaleFigures makes the run's stored figures the ones a refresh left
// a second before the run finished, with the pending credits given.
func storeStaleFigures(t *testing.T, db *pg.DB, runID int64, pending int) {
	t.Helper()
	_, err := db.Exec(`UPDATE author_metadata_run_aggregate
		SET figures = jsonb_set(figures, '{credits,pending}', to_jsonb(?::int)),
			computed_at = (SELECT finished_at - interval '1 second' FROM author_metadata_run r WHERE r.id = run_id)
		WHERE run_id = ?`, pending, runID)
	require.NoError(t, err)
}

// A large run's readiness is never decided from figures computed before it
// finished: stale figures, zero or not, read as "final figures pending" —
// neither ready nor "credits pending" — until a refresh after the finish
// stores fresh ones. The run here is a pilot, as a pilot archive can hold
// more books than the live limit.
func TestLargeRunReadinessWaitsForFiguresAfterTheFinish(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pending int
	}{
		{"stale zero", 0},
		{"stale nonzero", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := scanfixture.ScratchDB(t)
			ctx := context.Background()
			run := completedNoAuthorRun(t, db)
			large := runAdmin(t, db).WithLiveAggregateLimit(0)

			storeStaleFigures(t, db, run.ID, tc.pending)
			report, err := large.Report(ctx, run.ID)
			require.NoError(t, err)
			assert.False(t, report.Ready)
			assert.Equal(t, []string{services.RunNotReadyFiguresPending}, report.NotReadyReasons)
			assert.False(t, report.AggregatesFresh)

			large.Wait()
			report, err = large.Report(ctx, run.ID)
			require.NoError(t, err)
			assert.True(t, report.AggregatesFresh, "the read started a refresh that stored figures after the finish")
			assert.True(t, report.Ready)
			assert.Empty(t, report.NotReadyReasons)
		})
	}
}

// Figures computed after the finish stay the run's final figures to show
// however old they get: a read only refreshes them in the background, and
// never holds the verdict back for their age.
func TestLargeRunFinalFiguresDoNotExpire(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	run := completedNoAuthorRun(t, db)
	_, err := db.Exec(`UPDATE author_metadata_run SET finished_at = clock_timestamp() - interval '20 minutes',
		extraction_completed_at = clock_timestamp() - interval '21 minutes',
		started_at = clock_timestamp() - interval '22 minutes' WHERE id = ?`, run.ID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE author_metadata_run_aggregate SET computed_at = clock_timestamp() - interval '10 minutes'
		WHERE run_id = ?`, run.ID)
	require.NoError(t, err)
	large := runAdmin(t, db).WithLiveAggregateLimit(0)

	report, err := large.Report(ctx, run.ID)
	require.NoError(t, err)
	assert.True(t, report.AggregatesFresh)
	assert.True(t, report.Ready, "ten minutes old: shown as they are, refreshed behind the scenes")
}

// A cached zero cannot hide pending work: a completed run whose credits are
// not all accounted (here: its local jobs never ran) reads "final figures
// pending" while the stored zero is stale, and "credits pending" once the
// refresh tells the truth — never ready.
func TestLargeRunStaleZeroDoesNotHidePendingWork(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	multi := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "multi_contributor.fb2", md5Of(multi))
	run := startWorkerRun(t, db, 101)
	drain(t, workerConfig(t, db, dir, nil))
	// Completed as if before a source change brought new pending work, with
	// figures stored as of before that.
	_, err := db.Exec(`UPDATE author_metadata_run SET status = 'completed', finished_at = clock_timestamp() WHERE id = ?`, run.ID)
	require.NoError(t, err)
	large := runAdmin(t, db).WithLiveAggregateLimit(0)
	require.NoError(t, large.RefreshAggregates(ctx, run.ID))
	storeStaleFigures(t, db, run.ID, 0)

	report, err := large.Report(ctx, run.ID)
	require.NoError(t, err)
	assert.False(t, report.Ready, "a stale zero is not a verdict")
	assert.Contains(t, report.NotReadyReasons, services.RunNotReadyFiguresPending)
	large.Wait()
	report, err = large.Report(ctx, run.ID)
	require.NoError(t, err)
	assert.False(t, report.Ready)
	assert.Contains(t, report.NotReadyReasons, services.RunNotReadyCreditsPending)
	assert.NotContains(t, report.NotReadyReasons, services.RunNotReadyFiguresPending)
}

// gatedSource reads archives through zip and holds every entry open until
// the test lets it go: the test sees which entry the worker is at.
type gatedSource struct {
	services.ZipArchiveSource
	entered chan string
	release chan struct{}
}

type gatedArchive struct {
	services.ArchiveReader
	src gatedSource
}

func (s gatedSource) Open(ctx context.Context, path string) (services.ArchiveReader, error) {
	r, err := s.ZipArchiveSource.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	return gatedArchive{ArchiveReader: r, src: s}, nil
}

func (a gatedArchive) OpenEntry(name string) (io.ReadCloser, error) {
	a.src.entered <- name
	<-a.src.release
	return a.ArchiveReader.OpenEntry(name)
}

// A deletion requested while the worker is in the middle of a long
// extraction is served between extraction batches, not after the drain: by
// the time the worker reaches the next book, the archive's records are gone.
func TestArchiveDeletionIsServedBetweenExtractionBatches(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	multi := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	good := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	seedWorkerBook(t, db, 101, "user_books.zip", "a.fb2", md5Of([]byte("a")))
	seedWorkerBook(t, db, 102, "happy.zip", "multi_contributor.fb2", md5Of(multi))
	seedWorkerBook(t, db, 103, "happy.zip", "translator_only.fb2", md5Of(good))
	run := startWorkerRun(t, db, 101, 102, 103)

	src := gatedSource{entered: make(chan string, 4), release: make(chan struct{})}
	cfg := services.DefaultExtractionWorkerConfig(dir, workerExtractorEngine())
	cfg.ClaimLimit = 1
	cfg.Retry = workerRetryPolicy(3)
	worker, err := services.NewAuthorMetadataExtractionWorker(db, src, &cfg)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, processErr := worker.ProcessAvailable(ctx)
		done <- processErr
	}()
	// Book 101's archive is missing (no entry opens); the worker is at 102.
	require.Equal(t, "multi_contributor.fb2", <-src.entered)
	require.Equal(t, "archive_missing", bookStatuses(t, db, run.ID)[101])

	admin := runAdmin(t, db)
	deletion, err := admin.DeleteArchive(ctx, run.ID, "user_books.zip")
	require.NoError(t, err)
	require.Equal(t, "pending", deletion.Status)

	src.release <- struct{}{}
	require.Equal(t, "translator_only.fb2", <-src.entered, "the extraction goes on")
	var status string
	var deleted int
	_, err = db.QueryOne(pg.Scan(&status, &deleted), `SELECT status, books_deleted
		FROM author_metadata_archive_deletion WHERE id = ?`, deletion.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", status, "served before the next book, not after the drain")
	assert.Equal(t, 1, deleted)

	src.release <- struct{}{}
	require.NoError(t, <-done)
}

// Unrelated writes during and after the refresh — another input's job,
// rewritten again and again — do not keep a completed run's figures
// "being prepared": figures computed after the finish are its final figures
// to show, and the report's verdict follows them.
func TestUnrelatedWritesDoNotHoldACompletedRunInPreparation(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	run := completedNoAuthorRun(t, db)
	_, err := db.Exec(`INSERT INTO contributor_normalization_job
		(normalization_key, source_fingerprint, extractor_version, normalizer_version)
		VALUES (sha256('key'::bytea), sha256('fingerprint'::bytea), ?, ?)`, workerExtractor, workerNormalizer)
	require.NoError(t, err)
	large := runAdmin(t, db).WithLiveAggregateLimit(0)

	stop := make(chan struct{})
	writes := make(chan int, 1)
	go func() {
		n := 0
		defer func() { writes <- n }()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, writeErr := db.Exec(`UPDATE contributor_normalization_job SET next_attempt_at = clock_timestamp()
				WHERE normalization_key = sha256('key'::bytea)`); writeErr == nil {
				n++
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	require.NoError(t, large.RefreshAggregates(ctx, run.ID))
	report, err := large.Report(ctx, run.ID)
	close(stop)
	require.Positive(t, <-writes, "the unrelated writer ran during the refresh")
	require.NoError(t, err)
	assert.True(t, report.AggregatesFresh, "figures computed after the finish are the run's final figures")
	assert.True(t, report.Ready)
	assert.Empty(t, report.NotReadyReasons)
}
