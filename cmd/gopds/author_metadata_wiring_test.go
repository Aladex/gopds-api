package main

// Phase 15 wiring: the author metadata configuration reaches the workers'
// behavior, no worker starts before the database is ready, and the runner is
// shut down before the database pool closes.

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/migrate"
	"gopds-api/internal/testdb"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
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
	workers, err := buildAuthorMetadataWorkers(db, &c)
	require.NoError(t, err)
	require.Len(t, workers, 1, "one local loop, and no extraction worker until phase 8 registers one")
	report, err := workers[0].(*services.AuthorMetadataLocalLoop).RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, report.Claimed, "claim size 2 takes two jobs")

	c.LocalNormalization.ClaimSize = 3
	workers, err = buildAuthorMetadataWorkers(db, &c)
	require.NoError(t, err)
	report, err = workers[0].(*services.AuthorMetadataLocalLoop).RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, report.Claimed, "claim size 3 takes three")

	c.LocalNormalization.Concurrency = 2
	workers, err = buildAuthorMetadataWorkers(db, &c)
	require.NoError(t, err)
	assert.Len(t, workers, 2, "concurrency is the number of local loops")
}

// The enabled flag keeps every worker off: nothing claims.
func TestAuthorMetadataDisabledStartsNothing(t *testing.T) {
	db := scratchDB(t)
	seedAuthorJobs(t, db, 1)
	c := wiringConfig()
	c.Enabled = false
	c.PollInterval = time.Millisecond

	runner := initializeAuthorMetadata(db, &c)
	assert.Nil(t, runner)
	time.Sleep(50 * time.Millisecond)
	var attempts int
	_, err := db.QueryOne(pg.Scan(&attempts), `SELECT count(*) FROM contributor_normalization_job_attempt`)
	require.NoError(t, err)
	assert.Zero(t, attempts)

	c.Enabled = true
	runner = initializeAuthorMetadata(db, &c)
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
