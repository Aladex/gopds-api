package services_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression from the phase-3 review, round 3 (R3-B1): a duplicate stage
// enqueue and the worker's expired-lease budget pause must not deadlock.

// beforeQueryGate holds a connection just before the first query that
// matches, until released.
type beforeQueryGate struct {
	match   func(query string) bool
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBeforeQueryGate(match func(string) bool) *beforeQueryGate {
	return &beforeQueryGate{match: match, reached: make(chan struct{}), release: make(chan struct{})}
}

func (g *beforeQueryGate) BeforeQuery(ctx context.Context, ev *pg.QueryEvent) (context.Context, error) {
	q, err := ev.FormattedQuery()
	if err != nil {
		return ctx, nil
	}
	if g.match(string(q)) {
		g.once.Do(func() {
			close(g.reached)
			<-g.release
		})
	}
	return ctx, nil
}

func (g *beforeQueryGate) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

func (g *beforeQueryGate) await(t *testing.T, what string) {
	t.Helper()
	select {
	case <-g.reached:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s never came", what)
	}
}

type claimResult struct {
	claim   *database.AuthorLLMClaim
	refusal database.AuthorLLMClaimRefusal
	err     error
}

// claimWaitsOrReturns runs a claim and returns once it either returned or is
// seen waiting on a lock (waited).
func (e *llmEnv) claimWaitsOrReturns(opts *database.AuthorLLMClaimOptions) (result <-chan claimResult, waited bool) {
	e.t.Helper()
	done := make(chan claimResult, 1)
	go func() {
		c, r, err := database.ClaimAuthorLLMCall(context.Background(), e.db, opts)
		done <- claimResult{c, r, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case res := <-done:
			out := make(chan claimResult, 1)
			out <- res
			return out, false
		default:
		}
		if e.count(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%author_llm_run%'`) > 0 {
			return done, true
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatal("the claim neither returned nor waited")
	return done, false
}

// evalDeadlockIterations is how many times the scenario runs, each on a new
// run over the same frozen sets.
const evalDeadlockIterations = 12

func TestAuthorLLMEvalDuplicateEnqueueAndBudgetPauseNeverDeadlock(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	retries := database.AuthorLLMTxRetries()

	for i := range evalDeadlockIterations {
		run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
		require.NoError(t, err, "iteration %d", i)
		e.stepTo(run, "fingerprint")

		// Driver B saw no fingerprint job: it is held before its enqueue
		// takes the run lock, and again before its first INSERT.
		hooked := pg.Connect(e.db.Options())
		atLock := newBeforeQueryGate(func(q string) bool {
			return strings.Contains(q, "FROM author_llm_run WHERE id IN") && strings.Contains(q, " FOR ")
		})
		// Past the lock, driver B checks the stage again before it would
		// insert: it is held there, with the run locked.
		var locked atomic.Bool
		atInsert := newBeforeQueryGate(func(q string) bool {
			return locked.Load() && strings.Contains(q, "SELECT EXISTS (SELECT 1 FROM author_llm_job")
		})
		hooked.AddQueryHook(atLock)
		hooked.AddQueryHook(atInsert)
		driverB := services.NewAuthorLLMEvalDriver(hooked, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
		stepped := make(chan error, 1)
		go func() {
			_, stepErr := driverB.Step(ctx)
			stepped <- stepErr
		}()
		atLock.await(t, "driver B's enqueue")

		// Driver A enqueues the whole stage and commits.
		driverA := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
		_, err = driverA.Step(ctx)
		require.NoError(t, err)
		fingerprintJobs := `SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose = 'eval_fingerprint'`
		require.Equal(t, 12, e.count(fingerprintJobs, run))

		// The worker claims one job and never settles it.
		// The operation clock moves a day further every iteration, past the
		// previous iteration's leases.
		now := time.Now().Add(time.Duration(i) * 24 * time.Hour)
		first, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db,
			e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), &now))
		require.NoError(t, err)
		require.NotNil(t, first, "iteration %d: refusal %s", i, refusal)

		// Driver B takes the run lock and is held before its duplicate
		// INSERT (its check of the stage under the lock).
		locked.Store(true)
		close(atLock.release)
		atInsert.await(t, "driver B's INSERT")

		// Past its lease, the next claim requeues that job and finds the
		// budget spent: it pauses the run — while driver B is mid-enqueue.
		later := first.LeaseExpiresAt.Add(time.Second)
		opts := e.claimOptions(llmreq.SlotProductionB, database.NewLeaseOwner(), &later)
		opts.OneOffTokens = 1
		claimed, waited := e.claimWaitsOrReturns(opts)
		// A run an enqueue holds is skipped: the claim neither waits for it
		// under the claim lock nor touches its rows.
		assert.False(t, waited, "iteration %d: the claim waited on a run mid-enqueue", i)
		if !waited {
			assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_call WHERE finished_at IS NULL`),
				"iteration %d: the claim abandoned a call of a run it does not hold", i)
		}
		close(atInsert.release)
		require.NoError(t, <-stepped, "iteration %d: driver B", i)
		res := <-claimed
		require.NoError(t, res.err, "iteration %d: claim", i)
		_ = hooked.Close()

		// The budget pause lands, on that claim or on the next one.
		for range 3 {
			if _, status := e.runStage(run); status == string(models.AuthorLLMRunPaused) {
				break
			}
			_, _, err = database.ClaimAuthorLLMCall(ctx, e.db, opts)
			require.NoError(t, err)
		}
		r, err := database.GetAuthorLLMRun(ctx, e.db, run)
		require.NoError(t, err)
		require.Equal(t, models.AuthorLLMRunPaused, r.Status, "iteration %d", i)
		require.NotNil(t, r.PausedReason)
		assert.Equal(t, string(models.AuthorLLMRunPausedBudget), *r.PausedReason)
		assert.Equal(t, 12, e.count(fingerprintJobs, run), "iteration %d: the stage lost jobs", i)
		assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_call WHERE finished_at IS NULL`),
			"iteration %d: the expired call was not abandoned", i)

		require.NoError(t, database.CompleteAuthorLLMRun(ctx, e.db, run))
	}
	assert.Equal(t, retries, database.AuthorLLMTxRetries(), "a transaction deadlocked and was retried")
}

// The claim abandons the expired calls of the runs it locks and leaves those
// of a run another transaction holds for a later claim: it never touches a
// row of a run it has not locked first.
func TestAuthorLLMClaimLeavesTheCallsOfAHeldRun(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	openCall := `SELECT count(*) FROM author_llm_call WHERE id = ? AND finished_at IS NULL`

	// One open call in a run that has ended, and one in the running run.
	runWithCall := func() (run, call int64) {
		run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
		require.NoError(t, err)
		e.stepTo(run, "fingerprint")
		_, err = driver.Step(ctx)
		require.NoError(t, err)
		now := time.Now()
		c, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotJudgeA, database.NewLeaseOwner(), &now))
		require.NoError(t, err)
		require.NotNil(t, c, "refusal %s", refusal)
		return run, c.CallID
	}
	ended, endedCall := runWithCall()
	require.NoError(t, database.CompleteAuthorLLMRun(ctx, e.db, ended))
	held, heldCall := runWithCall()

	// An enqueue holds the running run.
	tx, err := e.db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(`SELECT id FROM author_llm_run WHERE id = ? FOR SHARE`, held)
	require.NoError(t, err)

	later := time.Now().Add(24 * time.Hour)
	_, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotJudgeB, database.NewLeaseOwner(), &later))
	require.NoError(t, err)
	assert.Equal(t, database.AuthorLLMClaimIdle, refusal)
	assert.Zero(t, e.count(openCall, endedCall), "the expired call of a free run was not abandoned")
	assert.Equal(t, 1, e.count(openCall, heldCall), "the claim abandoned a call of a run it does not hold")

	require.NoError(t, tx.Rollback())
	_, _, err = database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotJudgeB, database.NewLeaseOwner(), &later))
	require.NoError(t, err)
	assert.Zero(t, e.count(openCall, heldCall), "the expired call was not abandoned once the run was free")
}
