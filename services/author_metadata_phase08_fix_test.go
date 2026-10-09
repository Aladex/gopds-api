package services_test

// Phase 8 fix round 1 (handover): regression tests for the six blockers and
// the source-I/O nit of the phase-8 review, built from the reviewer's
// reproductions.

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/parser"
	"gopds-api/internal/scanfixture"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runFinished(t *testing.T, db *pg.DB, runID int64) bool {
	t.Helper()
	var finished bool
	_, err := db.QueryOne(pg.Scan(&finished), `SELECT finished_at IS NOT NULL FROM author_metadata_run WHERE id = ?`, runID)
	require.NoError(t, err)
	return finished
}

// B1: a run whose extraction is terminal and whose books carry no author
// credit is complete — no local worker has anything to do for it.
func TestRunWithoutAuthorCreditsCompletes(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	book := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "translator_only.fb2", md5Of(book))
	run := startWorkerRun(t, db, 101)

	n, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	a, err := database.AuthorCreditAccountingForRun(ctx, db, run.ID)
	require.NoError(t, err)
	require.Zero(t, a.Credits)

	st := runRow(t, db, run.ID)
	require.True(t, st.extractionDone)
	assert.Equal(t, "completed", st.status, "terminal extraction with nothing pending completes the run")
	assert.True(t, runFinished(t, db, run.ID))
	_, err = services.NewAuthorMetadataRunService(db).StartRun(ctx, workerRunRequest(101))
	assert.NoError(t, err, "a completed run frees the active slot")
}

// B1: with author credits, the run waits for the local stream and completes
// once every current author credit is accounted for — open review included.
func TestRunCompletesAfterLocalDrain(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	book := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "multi_contributor.fb2", md5Of(book))
	run := startWorkerRun(t, db, 101)
	extraction := workerConfig(t, db, dir, nil)

	_, err := extraction.ProcessAvailable(ctx)
	require.NoError(t, err)
	_, err = extraction.ProcessAvailable(ctx)
	require.NoError(t, err)
	st := runRow(t, db, run.ID)
	require.True(t, st.extractionDone)
	assert.Equal(t, "running", st.status, "pending author credits keep the run running")

	cfg := services.DefaultAuthorMetadataLocalWorkerConfig()
	local, err := services.NewAuthorMetadataLocalWorker(db, &cfg)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		rep, runErr := local.RunOnce(ctx)
		require.NoError(t, runErr)
		if rep.Claimed == 0 {
			break
		}
	}
	a, err := database.AuthorCreditAccountingForRun(ctx, db, run.ID)
	require.NoError(t, err)
	require.True(t, a.Settled())
	require.Positive(t, a.Credits)

	_, err = extraction.ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Equal(t, "completed", runRow(t, db, run.ID).status, "both stages settled")
	assert.True(t, runFinished(t, db, run.ID))
}

// The completion check refuses every run that is not settled: extraction not
// terminal, a credit still pending, or a run that is not running.
func TestCompleteRunIfSettledRefusesUnsettledRuns(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	book := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "multi_contributor.fb2", md5Of(book))
	run := startWorkerRun(t, db, 101)
	svc := services.NewAuthorMetadataRunService(db)

	done, err := svc.CompleteIfSettled(ctx, run.ID)
	require.NoError(t, err)
	assert.False(t, done, "extraction has not run")

	_, err = workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	done, err = svc.CompleteIfSettled(ctx, run.ID)
	require.NoError(t, err)
	assert.False(t, done, "author credits are pending")

	// Local claims stop while a run is paused, so the credits are settled
	// first and the run paused afterwards.
	cfg := services.DefaultAuthorMetadataLocalWorkerConfig()
	local, err := services.NewAuthorMetadataLocalWorker(db, &cfg)
	require.NoError(t, err)
	_, err = local.RunOnce(ctx)
	require.NoError(t, err)
	a, err := database.AuthorCreditAccountingForRun(ctx, db, run.ID)
	require.NoError(t, err)
	require.True(t, a.Settled())
	require.NoError(t, svc.PauseRun(ctx, run.ID))
	done, err = svc.CompleteIfSettled(ctx, run.ID)
	require.NoError(t, err)
	assert.False(t, done, "a paused run is not completed behind the administrator's back")

	require.NoError(t, svc.ResumeRun(ctx, run.ID))
	done, err = svc.CompleteIfSettled(ctx, run.ID)
	require.NoError(t, err)
	assert.True(t, done)
	done, err = svc.CompleteIfSettled(ctx, run.ID)
	require.NoError(t, err)
	assert.False(t, done, "completing twice is not a second completion")
}

// B2: a selector field that does not belong to the mode is refused, not
// silently dropped; nothing is seeded.
func TestStartRunRejectsMixedSelectors(t *testing.T) {
	for _, mode := range []models.AuthorMetadataRunMode{models.AuthorMetadataRunSmoke, models.AuthorMetadataRunPilotArchive} {
		t.Run(string(mode), func(t *testing.T) {
			db := scanfixture.ScratchDB(t)
			seedCatalogBook(t, db, 101, "happy.zip", "multi_contributor.fb2")
			req := startRequest(mode)
			req.BookIDs = []int64{101}
			req.Archive = "happy.zip"
			_, err := services.NewAuthorMetadataRunService(db).StartRun(context.Background(), req)
			assert.ErrorIs(t, err, database.ErrInvalidRunSelector)
			var n int
			_, err = db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM author_metadata_run`)
			require.NoError(t, err)
			assert.Zero(t, n)
		})
	}
}

// B3: a run cannot be started for a version pair this build does not run.
func TestStartRunRejectsForeignVersions(t *testing.T) {
	for _, which := range []string{"extractor", "normalizer"} {
		t.Run(which, func(t *testing.T) {
			db := scanfixture.ScratchDB(t)
			seedCatalogBook(t, db, 101, "happy.zip", "multi_contributor.fb2")
			req := workerRunRequest(101)
			if which == "extractor" {
				req.ExtractorVersion = "different-extractor"
			} else {
				req.NormalizerVersion = "different-normalizer"
			}
			_, err := services.NewAuthorMetadataRunService(db).StartRun(context.Background(), req)
			assert.ErrorIs(t, err, services.ErrRunVersionMismatch)
			var n int
			_, err = db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM author_metadata_run`)
			require.NoError(t, err)
			assert.Zero(t, n)
		})
	}
}

// B3: a run seeded past the service with another version pair is refused by
// the worker before any work is committed under it.
func TestExtractionWorkerRefusesRunOfOtherVersions(t *testing.T) {
	for _, which := range []string{"extractor", "normalizer"} {
		t.Run(which, func(t *testing.T) {
			db := scanfixture.ScratchDB(t)
			ctx := context.Background()
			dir := copyFixtureArchive(t, "happy.zip")
			book := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
			seedWorkerBook(t, db, 101, "happy.zip", "multi_contributor.fb2", md5Of(book))
			run := &models.AuthorMetadataRun{
				Mode: models.AuthorMetadataRunSmoke, Status: models.AuthorMetadataRunRunning,
				ExtractorVersion: workerExtractor, NormalizerVersion: authornorm.NormalizerVersion,
				SelectorBookIDs: []int64{101},
			}
			if which == "extractor" {
				run.ExtractorVersion = "different-extractor"
			} else {
				run.NormalizerVersion = "different-normalizer"
			}
			require.NoError(t, database.SeedRun(ctx, db, run, []int64{101}))

			_, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
			require.NoError(t, err)
			st := runRow(t, db, run.ID)
			assert.Equal(t, "failed_systemic", st.status)
			require.NotNil(t, st.class)
			assert.Equal(t, "version_mismatch", *st.class)
			var snapshots int
			_, err = db.QueryOne(pg.Scan(&snapshots), `SELECT count(*) FROM book_metadata_snapshot`)
			require.NoError(t, err)
			assert.Zero(t, snapshots, "nothing is persisted under a run of other versions")
		})
	}
}

type failingExtractor struct{ err error }

func (e failingExtractor) Extract(parser.ExtractBookInput) (authornorm.SourceMetadata, error) {
	return authornorm.SourceMetadata{}, e.err
}

// B4: the extraction error taxonomy — a document-shape error is per book, a
// configuration error ends the run, an error of unknown kind is a bounded
// per-book retry, and only a source read failure pauses the run.
func TestExtractionErrorTaxonomy(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantRun     string
		wantItem    string
		wantClass   string
		wantClasses []string
	}{
		// One attempt, ended at once: a document problem is not retried.
		{"metadata shape", fmt.Errorf("mapping: %w", authornorm.ErrInvalidSourceMetadata), "completed", "metadata_parse_failed", "",
			[]string{""}},
		{"extractor configuration", fmt.Errorf("extract: %w", parser.ErrInvalidExtractorInput),
			"failed_systemic", "pending", "extractor_misconfigured", nil},
		{"unknown error", errors.New("something nobody classified"), "completed", "metadata_parse_failed", "",
			[]string{"extraction_failed", "extraction_failed", "max_attempts_exceeded"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := scanfixture.ScratchDB(t)
			ctx := context.Background()
			dir := copyFixtureArchive(t, "happy.zip")
			book := fixtureEntry(t, "happy.zip", "translator_only.fb2")
			seedWorkerBook(t, db, 101, "happy.zip", "translator_only.fb2", md5Of(book))
			run := startWorkerRun(t, db, 101)
			worker := workerConfig(t, db, dir, func(c *services.ExtractionWorkerConfig) { c.Extractor = failingExtractor{err: tc.err} })

			for i := 0; i < 5; i++ {
				_, err := worker.ProcessAvailable(ctx)
				require.NoError(t, err)
			}
			st := runRow(t, db, run.ID)
			assert.Equal(t, tc.wantRun, st.status)
			assert.Equal(t, tc.wantItem, bookStatuses(t, db, run.ID)[101])
			if tc.wantClass != "" {
				require.NotNil(t, st.class)
				assert.Equal(t, tc.wantClass, *st.class)
			}
			if tc.wantClasses != nil {
				assert.Equal(t, tc.wantClasses, itemAttemptClasses(t, db, run.ID))
			}
		})
	}
}

func itemAttemptClasses(t *testing.T, db *pg.DB, runID int64) []string {
	t.Helper()
	var classes []string
	_, err := db.Query(&classes, `SELECT coalesce(a.error_class, '') FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id WHERE i.run_id = ? ORDER BY a.id`, runID)
	require.NoError(t, err)
	return classes
}

// failingSource opens an archive whose every entry stops reading midway.
type failingSource struct{}

func (failingSource) Open(context.Context, string) (services.ArchiveReader, error) {
	return failingArchive{}, nil
}

type failingArchive struct{}

func (failingArchive) Close() error { return nil }

func (failingArchive) OpenEntry(string) (io.ReadCloser, error) { return failingEntry{}, nil }

type failingEntry struct{}

func (failingEntry) Close() error { return nil }

func (failingEntry) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// B4 + N1: a source read failure pauses the run without a per-book status,
// and the failing item's attempt is closed with the closed class and its
// lease released, so a resume retries it at once.
func TestSourceReadFailurePausesAndReleasesTheItem(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	seedWorkerBook(t, db, 101, "healthy.zip", "bad.fb2", md5Of([]byte("bad")))
	run := startWorkerRun(t, db, 101)
	cfg := services.DefaultExtractionWorkerConfig(t.TempDir(), parser.MetadataExtractor{
		MetadataMaxBytes: services.AuthorMetadataMaxBytes, ExtractorVersion: workerExtractor,
	})
	cfg.Retry = workerRetryPolicy(3)
	worker, err := services.NewAuthorMetadataExtractionWorker(db, failingSource{}, &cfg)
	require.NoError(t, err)

	n, err := worker.ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)
	st := runRow(t, db, run.ID)
	assert.Equal(t, "paused", st.status)
	require.NotNil(t, st.class)
	assert.Equal(t, "archive_unreadable", *st.class)
	assert.Equal(t, "pending", bookStatuses(t, db, run.ID)[101])

	var leased bool
	_, err = db.QueryOne(pg.Scan(&leased), `SELECT lease_owner IS NOT NULL FROM author_metadata_run_item WHERE run_id = ?`, run.ID)
	require.NoError(t, err)
	assert.False(t, leased, "the failing item's lease is released")
	assert.Equal(t, []string{"archive_unreadable"}, itemAttemptClasses(t, db, run.ID))
}

// B5: a per-book terminal status that cannot be recorded is not reported as
// processed; an invariant failure ends the run and leaves the item pending.
func TestTerminalPersistenceFailureIsSystemic(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	seedWorkerBook(t, db, 101, "happy.zip", "absent.fb2", md5Of([]byte("missing")))
	run := startWorkerRun(t, db, 101)
	_, err := db.Exec(`ALTER TABLE author_metadata_run ADD CONSTRAINT reject_terminal_counter CHECK (items_terminal = 0)`)
	require.NoError(t, err)

	n, err := workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "a rolled-back item is not reported terminal")
	st := runRow(t, db, run.ID)
	assert.Equal(t, "failed_systemic", st.status)
	require.NotNil(t, st.class)
	assert.Equal(t, "database_invariant", *st.class)
	assert.Equal(t, "pending", bookStatuses(t, db, run.ID)[101])
}

// logText is everything a log entry carries: message, every structured field
// and the serialized line as the formatter writes it.
func logText(t *testing.T, hook *logrustest.Hook) string {
	t.Helper()
	var b strings.Builder
	for _, e := range hook.AllEntries() {
		b.WriteString(e.Message)
		for k, v := range e.Data {
			fmt.Fprintf(&b, " %s=%v", k, v)
		}
		line, err := e.String()
		require.NoError(t, err)
		b.WriteString(line)
	}
	return b.String()
}

// B6: a database error whose text carries source data never reaches a log,
// neither in the message nor in a structured field.
func TestDatabaseFailureLogsCarryNoSourceText(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	book := fixtureEntry(t, "happy.zip", "multi_contributor.fb2")
	seedWorkerBook(t, db, 101, "happy.zip", "multi_contributor.fb2", md5Of(book))
	startWorkerRun(t, db, 101)
	_, err := db.Exec(`
		CREATE FUNCTION canary_snapshot_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'private-canary:' || coalesce(NEW.source_title, ''); END $$;
		CREATE TRIGGER canary_snapshot_failure BEFORE INSERT ON book_metadata_snapshot
			FOR EACH ROW EXECUTE FUNCTION canary_snapshot_failure();
		CREATE FUNCTION canary_attempt_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'private-canary-attempt'; END $$;
		CREATE TRIGGER canary_attempt_failure BEFORE UPDATE ON author_metadata_run_item_attempt
			FOR EACH ROW EXECUTE FUNCTION canary_attempt_failure();`)
	require.NoError(t, err)

	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	_, err = workerConfig(t, db, dir, nil).ProcessAvailable(ctx)
	require.NoError(t, err)

	out := logText(t, hook)
	require.NotEmpty(t, hook.AllEntries(), "the failure path logged")
	assert.NotContains(t, out, "private-canary", "a database error text reached a log")
}

func workerRunRequest(ids ...int64) *services.StartRunRequest {
	return &services.StartRunRequest{
		Mode:              models.AuthorMetadataRunSmoke,
		BookIDs:           ids,
		ExtractorVersion:  workerExtractor,
		NormalizerVersion: workerNormalizer,
	}
}

// Fix round 2 (re-review round 1).

// nextRunSource wraps the real zip source; closing the first archive runs
// after once — the moment another replica or an administrator completes the
// run and starts the next one, before this worker's next claim.
type nextRunSource struct {
	once  sync.Once
	after func()
}

type nextRunArchive struct {
	services.ArchiveReader
	source *nextRunSource
}

func (a *nextRunArchive) Close() error {
	err := a.ArchiveReader.Close()
	a.source.once.Do(a.source.after)
	return err
}

func (s *nextRunSource) Open(ctx context.Context, path string) (services.ArchiveReader, error) {
	a, err := services.ZipArchiveSource{}.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	return &nextRunArchive{ArchiveReader: a, source: s}, nil
}

// B7: a worker holding run 1 that claims an item of run 2 — run 1 completed
// and run 2 started between two of its claims — processes it as run 2's item:
// counted for run 2, its snapshot attributed to run 2, never failed as an
// invariant of run 1.
func TestClaimOfTheNextRunIsProcessedForThatRun(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	dir := copyFixtureArchive(t, "happy.zip")
	book := fixtureEntry(t, "happy.zip", "translator_only.fb2")
	for _, id := range []int64{101, 102} {
		seedWorkerBook(t, db, id, "happy.zip", "translator_only.fb2", md5Of(book))
	}
	first := startWorkerRun(t, db, 101)
	svc := services.NewAuthorMetadataRunService(db)
	var second *models.AuthorMetadataRun
	var switchErr error
	source := &nextRunSource{after: func() {
		done, err := svc.CompleteIfSettled(ctx, first.ID)
		if err != nil || !done {
			switchErr = fmt.Errorf("completing run 1: done=%v err=%w", done, err)
			return
		}
		second, switchErr = svc.StartRun(ctx, workerRunRequest(102))
	}}
	cfg := services.DefaultExtractionWorkerConfig(dir, parser.MetadataExtractor{
		MetadataMaxBytes: services.AuthorMetadataMaxBytes, ExtractorVersion: services.AuthorMetadataExtractorVersion,
	})
	cfg.Retry = workerRetryPolicy(3)
	cfg.ClaimLimit = 1
	worker, err := services.NewAuthorMetadataExtractionWorker(db, source, &cfg)
	require.NoError(t, err)

	_, err = worker.ProcessAvailable(ctx)
	require.NoError(t, err)
	require.NoError(t, switchErr)
	require.NotNil(t, second)

	assert.Zero(t, f08count(t, db, `SELECT count(*) FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id
		WHERE i.run_id = ? AND a.error_class IS NOT NULL`, second.ID), "no false failure on the next run's book")
	assert.Zero(t, f08count(t, db, `SELECT count(*) FROM book_metadata_snapshot WHERE book_id = 102 AND run_id <> ?`, second.ID),
		"no snapshot attributed to the previous run")
	st1, st2 := runRow(t, db, first.ID), runRow(t, db, second.ID)
	assert.Equal(t, "completed", st1.status)
	assert.Equal(t, 1, st1.terminal, "run 1 counts only its own item")
	assert.Equal(t, "extracted_no_author", bookStatuses(t, db, second.ID)[102], "the next run's book is processed for that run")
	assert.Equal(t, 1, st2.terminal)
	assert.Equal(t, "completed", st2.status, "and the next run completes on its own")
}

func f08count(t *testing.T, db *pg.DB, query string, params ...interface{}) int {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), query, params...)
	require.NoError(t, err)
	return n
}

// typedSource opens an archive whose entries fail their first read with err.
type typedSource struct{ err error }

type typedArchive struct{ err error }

type typedEntry struct{ err error }

func (s typedSource) Open(context.Context, string) (services.ArchiveReader, error) {
	return typedArchive(s), nil
}

func (a typedArchive) Close() error { return nil }

func (a typedArchive) OpenEntry(string) (io.ReadCloser, error) { return typedEntry(a), nil }

func (e typedEntry) Close() error { return nil }

func (e typedEntry) Read([]byte) (int, error) { return 0, e.err }

// B8: a read failure of the entry stream is a source failure whatever error
// type it wraps — even one the per-book classifier would take for a document
// problem.
func TestSourceReadOriginOutranksTheWrappedType(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"document shape", authornorm.ErrInvalidSourceMetadata},
		{"xml syntax", &xml.SyntaxError{Msg: "upstream read failed", Line: 1}},
		{"metadata limit", parser.ErrMetadataLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := scanfixture.ScratchDB(t)
			ctx := context.Background()
			seedWorkerBook(t, db, 101, "upstream.zip", "b.fb2", md5Of([]byte("b")))
			run := startWorkerRun(t, db, 101)
			cfg := services.DefaultExtractionWorkerConfig(t.TempDir(), parser.MetadataExtractor{
				MetadataMaxBytes: services.AuthorMetadataMaxBytes, ExtractorVersion: services.AuthorMetadataExtractorVersion,
			})
			cfg.Retry = workerRetryPolicy(3)
			worker, err := services.NewAuthorMetadataExtractionWorker(db, typedSource{err: tc.err}, &cfg)
			require.NoError(t, err)

			n, err := worker.ProcessAvailable(ctx)
			require.NoError(t, err)
			assert.Zero(t, n)
			st := runRow(t, db, run.ID)
			assert.Equal(t, "paused", st.status)
			require.NotNil(t, st.class)
			assert.Equal(t, "archive_unreadable", *st.class)
			assert.Equal(t, "pending", bookStatuses(t, db, run.ID)[101])
			assert.Equal(t, []string{"archive_unreadable"}, itemAttemptClasses(t, db, run.ID))
		})
	}
}
