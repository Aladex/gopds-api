package main

// Phase 15 wiring: the author metadata configuration reaches the workers'
// behavior, no worker starts before the database is ready, and the runner is
// shut down before the database pool closes.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/migrate"
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

// configuredDB connects to the integration database, skipping without one.
func configuredDB(t *testing.T) (*pg.DB, testdb.Config) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	cfg, ok := testdb.Configured()
	if !ok {
		t.Skip(testdb.SkipReason)
	}
	db, err := testdb.Connect(cfg, nil)
	require.NoError(t, err)
	return db, cfg
}

// scratchDB creates and migrates a database of its own: the local worker
// claims any ready job, so it must not run against a shared catalog.
func scratchDB(t *testing.T) *pg.DB {
	t.Helper()
	admin, cfg := configuredDB(t)
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("author_wiring_test_%d", time.Now().UnixNano())
	_, err := admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, dropErr := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); dropErr != nil {
			t.Errorf("dropping %s: %v", name, dropErr)
		}
	})
	scratch := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name})
	t.Cleanup(func() { _ = scratch.Close() })
	_, err = migrate.Run(context.Background(), scratch, os.DirFS("../.."), "database_migrations", migrate.AppBaseline())
	require.NoError(t, err)
	return scratch
}

// seedAuthorJobs persists n books with one distinct author each, which
// queues n local normalization jobs.
func seedAuthorJobs(t *testing.T, db *pg.DB, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		var book int64
		_, err := db.QueryOne(pg.Scan(&book), `INSERT INTO opds_catalog_book
			(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
			VALUES ('w.fb2', 'w.zip', 'fb2', now(), '', 'ru', 'w', '', '') RETURNING id`)
		require.NoError(t, err)
		v, err := authornorm.NewSourceValue([]authornorm.SourceComponent{
			{Kind: authornorm.ComponentFirst, Value: "Имя"},
			{Kind: authornorm.ComponentLast, Value: fmt.Sprintf("Фамилия%d", i)},
		})
		require.NoError(t, err)
		require.NoError(t, db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
			_, persistErr := database.PersistExtraction(tx, &database.ExtractionInput{
				BookID: book, BookMD5: fmt.Sprintf("%032x", book), ExtractorVersion: "extractor-v1",
				NormalizerVersion: authornorm.NormalizerVersion, Origin: models.BookMetadataSnapshotLive,
				Outcome: models.BookMetadataSnapshotExtracted, ArchivePath: "w.zip", EntryName: "w.fb2",
				Credits: []database.ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: v}},
			})
			return persistErr
		}))
	}
}

func wiringConfig() config.AuthorMetadataConfig {
	return config.AuthorMetadataConfig{
		Enabled:          true,
		MetadataMaxBytes: config.AuthorMetadataMaxBytes,
		PollInterval:     time.Hour,
		Extraction:       config.AuthorMetadataStageConfig{Concurrency: 1, ClaimSize: 50, Lease: 2 * time.Minute, MaxAttempts: 5},
		LocalNormalization: config.AuthorMetadataStageConfig{
			Concurrency: 1, ClaimSize: 100, Lease: time.Minute, MaxAttempts: 5,
		},
	}
}

// RED 8: one changed configuration value is observed in what the wired
// worker does, not in a struct field.
func TestAuthorMetadataConfigReachesTheLocalWorker(t *testing.T) {
	db := scratchDB(t)
	seedAuthorJobs(t, db, 5)
	ctx := context.Background()

	c := wiringConfig()
	c.LocalNormalization.ClaimSize = 2
	workers, err := buildAuthorMetadataWorkers(db, t.TempDir(), &c)
	require.NoError(t, err)
	require.Len(t, workers, 2, "the extraction loop and one local loop")
	_, isExtraction := workers[0].(*services.AuthorMetadataExtractionLoop)
	require.True(t, isExtraction, "the extraction stage is registered first")
	report, err := workers[1].(*services.AuthorMetadataLocalLoop).RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, report.Claimed, "claim size 2 takes two jobs")

	c.LocalNormalization.ClaimSize = 3
	workers, err = buildAuthorMetadataWorkers(db, t.TempDir(), &c)
	require.NoError(t, err)
	report, err = workers[1].(*services.AuthorMetadataLocalLoop).RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, report.Claimed, "claim size 3 takes three")

	c.LocalNormalization.Concurrency = 2
	workers, err = buildAuthorMetadataWorkers(db, t.TempDir(), &c)
	require.NoError(t, err)
	assert.Len(t, workers, 3, "local concurrency is the number of local loops")
}

// The enabled flag keeps every worker off: nothing claims. Only the one-shot
// acceptance pass runs, and the runner that owns it still shuts down.
func TestAuthorMetadataDisabledStartsNothing(t *testing.T) {
	db := scratchDB(t)
	seedAuthorJobs(t, db, 1)
	c := wiringConfig()
	c.Enabled = false
	c.PollInterval = time.Millisecond

	runner := initializeAuthorMetadata(db, t.TempDir(), &c)
	require.NotNil(t, runner, "the acceptance pass runs whatever the switch says")
	time.Sleep(50 * time.Millisecond)
	var attempts int
	_, err := db.QueryOne(pg.Scan(&attempts), `SELECT count(*) FROM contributor_normalization_job_attempt`)
	require.NoError(t, err)
	assert.Zero(t, attempts)
	stopped, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	require.NoError(t, runner.Shutdown(stopped))

	c.Enabled = true
	runner = initializeAuthorMetadata(db, t.TempDir(), &c)
	require.NotNil(t, runner)
	require.Eventually(t, func() bool {
		var completed int
		_, countErr := db.QueryOne(pg.Scan(&completed), `SELECT count(*) FROM contributor_normalization_job
			WHERE status = 'completed'`)
		return countErr == nil && completed == 1
	}, 5*time.Second, 5*time.Millisecond)
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, runner.Shutdown(shutdown))
}

// closedPool is go-pg's error text for a closed pool; the sentinel itself
// is internal to the driver.
const closedPool = "pg: database is closed"

// lastWordWorker uses the database once more after cancellation, the way a
// real worker finishes or fails its current attempt.
type lastWordWorker struct {
	db      *pg.DB
	started chan struct{}
	ran     atomic.Int32
	lastErr atomic.Value
}

func (w *lastWordWorker) Name() string { return "last-word" }

func (w *lastWordWorker) Run(ctx context.Context) error {
	w.ran.Add(1)
	close(w.started)
	<-ctx.Done()
	_, err := w.db.Exec(`SELECT 1`)
	w.lastErr.Store(fmt.Sprint(err))
	return nil
}

// RED 6 + mutation (close the database before the runner): the runner stops
// first, so no worker touches a closed pool.
func TestAuthorMetadataShutdownBeforeDatabaseClose(t *testing.T) {
	db, _ := configuredDB(t)
	w := &lastWordWorker{db: db, started: make(chan struct{})}
	runner, err := startAuthorMetadataRunner(context.Background(), db, []services.AuthorMetadataStageWorker{w})
	require.NoError(t, err)
	select {
	case <-w.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never started the worker")
	}

	require.NoError(t, shutdownAuthorMetadataThenDatabase(runner, db, 5*time.Second))

	assert.Equal(t, "<nil>", w.lastErr.Load(), "the worker's last database call must precede the pool closing")
	_, err = db.Exec(`SELECT 1`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), closedPool, "the pool is closed afterwards")
}

// RED 5: no worker starts before the database answers.
func TestAuthorMetadataStartsNoWorkerBeforeDatabaseReady(t *testing.T) {
	db, _ := configuredDB(t)
	require.NoError(t, db.Close())
	w := &lastWordWorker{db: db, started: make(chan struct{})}
	runner, err := startAuthorMetadataRunner(context.Background(), db, []services.AuthorMetadataStageWorker{w})
	assert.Error(t, err)
	assert.Nil(t, runner)
	time.Sleep(20 * time.Millisecond)
	assert.Zero(t, w.ran.Load())
	require.Error(t, err)
	assert.Contains(t, err.Error(), closedPool)
}

// The extraction stage takes its metadata read limit from the configuration:
// the same book is extracted under the default limit and refused as
// metadata_parse_failed under a 64-byte one.
func TestAuthorMetadataConfigReachesTheExtractionWorker(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "services", "testdata", "author_metadata", "happy.zip"))
	require.NoError(t, err)
	for _, tc := range []struct {
		maxBytes int64
		want     string
	}{{config.AuthorMetadataMaxBytes, "extracted"}, {64, "metadata_parse_failed"}} {
		t.Run(fmt.Sprint(tc.maxBytes), func(t *testing.T) {
			db := scratchDB(t)
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "happy.zip"), payload, 0o600))
			var book int64
			_, err := db.QueryOne(pg.Scan(&book), `INSERT INTO opds_catalog_book
				(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
				VALUES ('multi_contributor.fb2', 'happy.zip', 'fb2', now(), '', 'ru', 'w', '', '') RETURNING id`)
			require.NoError(t, err)
			ctx := context.Background()
			run, err := services.NewAuthorMetadataRunService(db).StartRun(ctx, &services.StartRunRequest{
				Mode: models.AuthorMetadataRunSmoke, BookIDs: []int64{book},
			})
			require.NoError(t, err)

			c := wiringConfig()
			c.MetadataMaxBytes = tc.maxBytes
			workers, err := buildAuthorMetadataWorkers(db, dir, &c)
			require.NoError(t, err)
			processed, err := workers[0].(*services.AuthorMetadataExtractionLoop).RunOnce(ctx)
			require.NoError(t, err)
			assert.Equal(t, 1, processed)
			var status string
			_, err = db.QueryOne(pg.Scan(&status), `SELECT status FROM author_metadata_run_item WHERE run_id = ?`, run.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.want, status)
		})
	}
}

// persistAuthor persists one book whose single author credit is built from
// kind/value pairs, flagged as the extractor flags a repeated child.
func persistAuthor(t *testing.T, db *pg.DB, book int64, parts ...string) {
	t.Helper()
	kinds := map[string]authornorm.ComponentKind{"first": authornorm.ComponentFirst, "last": authornorm.ComponentLast}
	components := make([]authornorm.SourceComponent, 0, len(parts)/2)
	for i := 0; i+1 < len(parts); i += 2 {
		components = append(components, authornorm.SourceComponent{Kind: kinds[parts[i]], Value: parts[i+1]})
	}
	v, err := authornorm.NewSourceValue(components)
	require.NoError(t, err)
	credit := database.ExtractionCredit{Role: models.ContributorRoleAuthor, Source: v}
	if v.HasDuplicateComponent() {
		credit.QualityFlags = []string{string(authornorm.FlagDuplicateComponent)}
	}
	_, err = db.Exec(`INSERT INTO opds_catalog_book (id, filename, path, format, registerdate, docdate, lang,
		title, annotation, md5) VALUES (?, 'w.fb2', 'w.zip', 'fb2', now(), '', 'ru', 'w', '', '')`, book)
	require.NoError(t, err)
	require.NoError(t, db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		_, persistErr := database.PersistExtraction(tx, &database.ExtractionInput{
			BookID: book, BookMD5: fmt.Sprintf("%032x", book), ExtractorVersion: "extractor-v1",
			NormalizerVersion: authornorm.NormalizerVersion, Origin: models.BookMetadataSnapshotLive,
			Outcome: models.BookMetadataSnapshotExtracted, ArchivePath: "w.zip", EntryName: "w.fb2",
			Credits: []database.ExtractionCredit{credit},
		})
		return persistErr
	}))
}

// shippedPolicyInsert is migration 25's own INSERT of the shipped
// registrations, so a test can replay exactly what a release adds.
func shippedPolicyInsert(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("../../database_migrations/25-author-acceptance-by-script.sql")
	require.NoError(t, err)
	sql := string(body)
	at := strings.Index(sql, "INSERT INTO public.author_acceptance_class")
	require.Positive(t, at, "migration 25 no longer inserts the shipped policy")
	return sql[at:]
}

// An upgrade: the credits were resolved before the release registered
// structured_person, so they wait with policy_not_registered. The next start
// selects them by itself — the workers are off, nobody presses anything.
func TestAcceptancePassSelectsWaitingCreditsAtStart(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()
	persistAuthor(t, db, 700, "first", "Лев", "last", "Толстой")
	persistAuthor(t, db, 701, "first", "Arthur", "last", "Doyle")

	_, err := db.Exec(`TRUNCATE author_acceptance_class`)
	require.NoError(t, err)
	c := wiringConfig()
	workers, err := buildAuthorMetadataWorkers(db, t.TempDir(), &c)
	require.NoError(t, err)
	report, err := workers[1].(*services.AuthorMetadataLocalLoop).RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, report.Completed)
	waiting := func(state, reason string) int {
		var n int
		_, countErr := db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM book_contributor_credit_selection
			WHERE state = ? AND coalesce(unresolved_reason, '') = ?`, state, reason)
		require.NoError(t, countErr)
		return n
	}
	require.Equal(t, 2, waiting("unresolved", "policy_not_registered"))

	_, err = db.Exec(shippedPolicyInsert(t))
	require.NoError(t, err)
	c.Enabled = false
	runner := initializeAuthorMetadata(db, t.TempDir(), &c)
	require.NotNil(t, runner)
	require.Eventually(t, func() bool {
		var selected int
		_, countErr := db.QueryOne(pg.Scan(&selected), `SELECT count(*) FROM book_contributor_credit_selection
			WHERE state = 'selected' AND basis = 'automatic' AND policy_version = '2'`)
		return countErr == nil && selected == 2
	}, 10*time.Second, 10*time.Millisecond)
	stopped, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	require.NoError(t, runner.Shutdown(stopped))
	assert.Zero(t, waiting("unresolved", "policy_not_registered"))
}

// Review finding B1 at start, workers off: an input whose stored parse is
// structured_person but which gained a duplicated-child credit afterwards is
// ambiguous, and the start-up pass must leave it alone — with the workers off
// nothing would repair it. A plain input in the same start is still selected.
func TestAcceptancePassAtStartSkipsAnInputThatTurnedAmbiguous(t *testing.T) {
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	db := scratchDB(t)
	ctx := context.Background()
	persistAuthor(t, db, 710, "first", "Иван", "last", "Петров")
	persistAuthor(t, db, 711, "first", "Arthur", "last", "Doyle")
	_, err := db.Exec(`TRUNCATE author_acceptance_class`)
	require.NoError(t, err)
	c := wiringConfig()
	workers, err := buildAuthorMetadataWorkers(db, t.TempDir(), &c)
	require.NoError(t, err)
	_, err = workers[1].(*services.AuthorMetadataLocalLoop).RunOnce(ctx)
	require.NoError(t, err)
	// The late credit: same fingerprint, a repeated empty first-name child.
	persistAuthor(t, db, 712, "first", "Иван", "first", "", "last", "Петров")

	_, err = db.Exec(shippedPolicyInsert(t))
	require.NoError(t, err)
	c.Enabled = false
	runner := initializeAuthorMetadata(db, t.TempDir(), &c)
	require.NotNil(t, runner)
	// The pass logs once, when it has finished, because it selected Doyle.
	require.Eventually(t, func() bool {
		for _, e := range hook.AllEntries() {
			if e.Message == string(services.AuthorMetadataEventAcceptanceApplied) {
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond)
	stopped, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	require.NoError(t, runner.Shutdown(stopped))

	state := func(book int64) string {
		var s string
		_, stateErr := db.QueryOne(pg.Scan(&s), `SELECT coalesce(sel.state, 'none') FROM book_contributor_credit c
			JOIN book_metadata_snapshot m ON m.id = c.snapshot_id AND m.book_id = ?
			LEFT JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id`, book)
		require.NoError(t, stateErr)
		return s
	}
	assert.Equal(t, "selected", state(711), "the plain input is selected")
	assert.Equal(t, "unresolved", state(710), "the ambiguous input's earlier credit is not selected")
	assert.Equal(t, "none", state(712))
}
