package services

// Phase 15: the runner that owns the author metadata worker goroutines, and
// the loop that drives the local normalization worker inside it.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/parser"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStageWorker records its lifecycle.
type fakeStageWorker struct {
	name     string
	started  atomic.Int32
	finished atomic.Int32
	// ignoreCancel keeps the worker running after cancellation until
	// release is closed.
	ignoreCancel bool
	release      chan struct{}
	err          error
}

func (w *fakeStageWorker) Name() string { return w.name }

func (w *fakeStageWorker) Run(ctx context.Context) error {
	w.started.Add(1)
	defer w.finished.Add(1)
	if w.err != nil {
		return w.err
	}
	<-ctx.Done()
	if w.ignoreCancel {
		<-w.release
	}
	return nil
}

func ready(context.Context) error { return nil }

func runnerEventually(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 2*time.Second, time.Millisecond)
}

// RED 5: workers start only after the readiness check passes, once.
func TestAuthorMetadataRunnerStartsWorkersAfterReadiness(t *testing.T) {
	a, b := &fakeStageWorker{name: "a"}, &fakeStageWorker{name: "b"}
	r := NewAuthorMetadataRunner(a, b)
	require.NoError(t, r.Start(context.Background(), ready))
	runnerEventually(t, func() bool { return a.started.Load() == 1 && b.started.Load() == 1 })

	assert.ErrorIs(t, r.Start(context.Background(), ready), ErrAuthorMetadataRunnerStarted, "one runner, started once")
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, r.Shutdown(shutdown))
	assert.Equal(t, int32(1), a.started.Load(), "a second Start must not start workers again")
}

func TestAuthorMetadataRunnerStartsNothingWhenNotReady(t *testing.T) {
	w := &fakeStageWorker{name: "w"}
	r := NewAuthorMetadataRunner(w)
	notReady := errors.New("database not ready")
	err := r.Start(context.Background(), func(context.Context) error { return notReady })
	assert.ErrorIs(t, err, notReady)
	time.Sleep(20 * time.Millisecond)
	assert.Zero(t, w.started.Load(), "no worker before the database is ready")
	assert.NoError(t, r.Shutdown(context.Background()), "shutting down a runner that never started is a no-op")
}

// RED 6: shutdown cancels the workers and returns only once they returned.
func TestAuthorMetadataRunnerShutdownWaitsForWorkers(t *testing.T) {
	workers := []*fakeStageWorker{{name: "a"}, {name: "b"}, {name: "c"}}
	r := NewAuthorMetadataRunner(workers[0], workers[1], workers[2])
	require.NoError(t, r.Start(context.Background(), ready))
	runnerEventually(t, func() bool {
		for _, w := range workers {
			if w.started.Load() != 1 {
				return false
			}
		}
		return true
	})

	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, r.Shutdown(shutdown))
	for _, w := range workers {
		assert.Equal(t, int32(1), w.finished.Load(), "worker %s still running after Shutdown", w.name)
	}
	assert.NoError(t, r.Shutdown(shutdown), "Shutdown is idempotent")
}

// RED 6: the wait is bounded; a worker that ignores cancellation does not
// hold shutdown forever.
func TestAuthorMetadataRunnerShutdownIsBounded(t *testing.T) {
	stuck := &fakeStageWorker{name: "stuck", ignoreCancel: true, release: make(chan struct{})}
	defer close(stuck.release)
	r := NewAuthorMetadataRunner(stuck)
	require.NoError(t, r.Start(context.Background(), ready))
	runnerEventually(t, func() bool { return stuck.started.Load() == 1 })

	shutdown, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	err := r.Shutdown(shutdown)
	assert.ErrorIs(t, err, ErrAuthorMetadataRunnerShutdownTimeout)
	assert.Less(t, time.Since(began), time.Second)
}

// One worker returning early does not stop the others.
func TestAuthorMetadataRunnerIsolatesWorkerErrors(t *testing.T) {
	failing := &fakeStageWorker{name: "failing", err: errors.New("boom")}
	healthy := &fakeStageWorker{name: "healthy"}
	r := NewAuthorMetadataRunner(failing, healthy)
	require.NoError(t, r.Start(context.Background(), ready))
	runnerEventually(t, func() bool { return failing.finished.Load() == 1 && healthy.started.Load() == 1 })
	assert.Zero(t, healthy.finished.Load())
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, r.Shutdown(shutdown))
	assert.Equal(t, int32(1), healthy.finished.Load())
}

// The local loop takes its claim size, lease, attempts and poll interval
// from the configuration.
func TestAuthorMetadataLocalLoopsFromConfig(t *testing.T) {
	c := config.AuthorMetadataConfig{
		PollInterval: 3 * time.Second,
		LocalNormalization: config.AuthorMetadataStageConfig{
			Concurrency: 3, ClaimSize: 7, Lease: 90 * time.Second, MaxAttempts: 4,
		},
	}
	loops, err := NewAuthorMetadataLocalLoops(nil, &c)
	require.NoError(t, err)
	require.Len(t, loops, 3)
	names := map[string]bool{}
	for _, l := range loops {
		names[l.Name()] = true
		cfg := l.(*AuthorMetadataLocalLoop).worker.cfg
		assert.Equal(t, 7, cfg.ClaimLimit)
		assert.Equal(t, 90*time.Second, cfg.Lease)
		assert.Equal(t, 4, cfg.Retry.MaxAttempts)
		assert.Equal(t, authornorm.NormalizerVersion, cfg.NormalizerVersion)
		assert.Equal(t, 3*time.Second, l.(*AuthorMetadataLocalLoop).poll)
	}
	assert.Len(t, names, 3, "every loop has its own name")

	c.LocalNormalization.ClaimSize = 0
	_, err = NewAuthorMetadataLocalLoops(nil, &c)
	assert.ErrorIs(t, err, ErrInvalidAuthorMetadataLocalWorkerConfig)
}

// capturingStage hands the context the runner gives its worker to the test,
// so the test can wait for cancellation itself instead of guessing a delay.
type capturingStage struct {
	AuthorMetadataStageWorker
	ctx chan context.Context
}

func (c *capturingStage) Run(ctx context.Context) error {
	c.ctx <- ctx
	return c.AuthorMetadataStageWorker.Run(ctx)
}

// awaitSignal waits for ch, failing the test after a generous bound that only
// catches a hang; nothing here depends on how fast the database answers.
func awaitSignal[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// RED 6/7 against PostgreSQL: shutdown in the middle of a job leaves only a
// lease behind, and a restarted runner — a new owner, nothing carried over
// in memory — reclaims the job once the lease expired and finishes it, while
// the interrupted owner is fenced off. Every step is ordered by an explicit
// barrier: the worker is inside the job, the runner's context is canceled,
// only then does the job continue; lease expiry is set on the database clock
// instead of waited for, and both leases are long enough that no database
// latency can expire them on their own.
func TestAuthorMetadataRunnerRestartReclaimsExpiredJobs(t *testing.T) {
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	credits := f.persistBook(author(tolstoy(t)))
	ctx := context.Background()

	entered, unblock := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	blocking := func(v authornorm.SourceValue, e string) (authornorm.Result, error) {
		if once.CompareAndSwap(false, true) {
			close(entered)
			<-unblock
		}
		return authornorm.Normalize(v, e)
	}
	cfg := testWorkerConfig()
	cfg.Lease = time.Hour
	cfg.Normalize = blocking
	first, err := NewAuthorMetadataLocalWorker(s, &cfg)
	require.NoError(t, err)
	stage := &capturingStage{
		AuthorMetadataStageWorker: NewAuthorMetadataLocalLoop("local-0", first, time.Hour),
		ctx:                       make(chan context.Context, 1),
	}
	r := NewAuthorMetadataRunner(stage)
	require.NoError(t, r.Start(ctx, ready))
	runCtx := awaitSignal(t, stage.ctx, "the worker to start")
	awaitSignal(t, entered, "the worker to enter its job")

	shutdownDone := make(chan error, 1)
	go func() {
		shutdown, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		shutdownDone <- r.Shutdown(shutdown)
	}()
	awaitSignal(t, runCtx.Done(), "the runner to cancel its worker")
	close(unblock)
	require.NoError(t, awaitSignal(t, shutdownDone, "the shutdown"))

	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job
		WHERE status = 'pending' AND lease_owner = ?::uuid`, first.owner), "the interrupted job keeps only its lease")
	assert.Nil(t, f.selection(credits[0]))

	// The lease runs out — on the database clock, now.
	f.exec(`UPDATE contributor_normalization_job
		SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE lease_owner IS NOT NULL`)

	// The restarted worker runs on a deliberately slow connection: each
	// result write takes longer than the old fixture's whole lease did.
	cfg.Normalize = authornorm.Normalize
	slow := pg.Connect(s.Options())
	t.Cleanup(func() { _ = slow.Close() })
	slow.AddQueryHook(slowResultWrite{delay: 300 * time.Millisecond})
	second, err := NewAuthorMetadataLocalWorker(slow, &cfg)
	require.NoError(t, err)
	restarted := NewAuthorMetadataRunner(NewAuthorMetadataLocalLoop("local-0", second, 10*time.Millisecond))
	require.NoError(t, restarted.Start(ctx, ready))
	require.Eventually(t, func() bool {
		return f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'completed'`) == 1
	}, 30*time.Second, 5*time.Millisecond, "the restarted runner never finished the reclaimed job")
	shutdown, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	require.NoError(t, restarted.Shutdown(shutdown))

	var job int64
	_, err = s.QueryOne(pg.Scan(&job), `SELECT id FROM contributor_normalization_job`)
	require.NoError(t, err)
	assert.Equal(t, []string{"lease_expired", ""}, f.attemptClasses(job))
	assert.NotNil(t, f.selection(credits[0]))

	// The interrupted owner cannot record its result any more.
	r1, err := authornorm.Normalize(tolstoy(t), workerExtractor)
	require.NoError(t, err)
	d, err := authornorm.ProductionAcceptancePolicy().Decide(&r1)
	require.NoError(t, err)
	err = s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, recordErr := database.RecordLocalNormalization(ctx, tx, job, first.owner, &r1, d)
		return recordErr
	})
	assert.ErrorIs(t, err, authornorm.ErrLeaseLost)

	a, err := database.AuthorCreditAccounting(ctx, s)
	require.NoError(t, err)
	assert.True(t, a.Settled())
}

// A failing batch costs one poll interval, not the worker: here every batch
// fails while an ambiguous class is registered (the policy cannot load), and
// the same loop finishes the job once the registration is gone.
func TestAuthorMetadataLocalLoopSurvivesFailingBatches(t *testing.T) {
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	f.persistBook(author(tolstoy(t)))
	f.registerPair("2", authornorm.ClassInitials, authornorm.ScriptCyrillic)

	cfg := testWorkerConfig()
	w, err := NewAuthorMetadataLocalWorker(s, &cfg)
	require.NoError(t, err)
	r := NewAuthorMetadataRunner(NewAuthorMetadataLocalLoop("local-0", w, 5*time.Millisecond))
	require.NoError(t, r.Start(context.Background(), ready))
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, f.count(`SELECT count(*) FROM contributor_normalization_job_attempt`), "no claim without a policy")

	f.exec(`TRUNCATE author_acceptance_class`)
	runnerEventually(t, func() bool {
		return f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'completed'`) == 1
	})
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Shutdown(shutdown))
}

// slowResultWrite delays every local result insert of a connection: a
// database that is slower than the test's author expected.
type slowResultWrite struct{ delay time.Duration }

func (h slowResultWrite) BeforeQuery(ctx context.Context, e *pg.QueryEvent) (context.Context, error) {
	if q, ok := e.Query.(string); ok && strings.Contains(q, "INSERT INTO contributor_normalization_result") {
		time.Sleep(h.delay)
	}
	return ctx, nil
}

func (slowResultWrite) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

// The extraction loop takes concurrency, claim size, lease, attempts and the
// metadata read limit from the configuration, and runs this build's
// extractor version.
func TestExtractionWorkerConfigFromConfig(t *testing.T) {
	c := config.AuthorMetadataConfig{
		MetadataMaxBytes: 12345,
		Extraction: config.AuthorMetadataStageConfig{
			Concurrency: 3, ClaimSize: 9, Lease: 70 * time.Second, MaxAttempts: 6,
		},
	}
	cfg := ExtractionWorkerConfigFrom("/archives", &c)
	assert.Equal(t, "/archives", cfg.ArchivesDir)
	assert.Equal(t, 3, cfg.Concurrency)
	assert.Equal(t, 9, cfg.ClaimLimit)
	assert.Equal(t, 70*time.Second, cfg.Lease)
	assert.Equal(t, 6, cfg.Retry.MaxAttempts)
	extractor, ok := cfg.Extractor.(parser.MetadataExtractor)
	require.True(t, ok)
	assert.Equal(t, int64(12345), extractor.MetadataMaxBytes)
	assert.Equal(t, AuthorMetadataExtractorVersion, extractor.ExtractorVersion)
	assert.Equal(t, int64(config.AuthorMetadataMaxBytes), int64(AuthorMetadataMaxBytes), "one metadata limit constant")
}
