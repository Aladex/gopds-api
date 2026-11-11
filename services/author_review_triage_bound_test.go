package services

// Round 2 of design/review-triage/reports/REVIEW-4-5.md: a stop must not
// wait forever. Every item's transaction lives at most ItemTimeout, also
// when it sits idle holding the batch (a worker suspended between two
// statements), and the stop's own wait for the batch is bounded: when it
// runs out the stop answers ErrTriageStopBusy, records nothing, and a later
// stop succeeds. An item that stays within its lifetime still commits, also
// one longer than a single statement's bound.

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authortriage"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idleItem opens an item's transaction the way the worker does (its
// lifetime and the batch check) and leaves it idle, holding the batch: a
// worker that never sends its next statement.
func (e *triageEnv) idleItem(loop *AuthorReviewTriageLoop, batchID int64) *pg.Tx {
	e.t.Helper()
	ictx, cancel := loop.itemContext(e.ctx)
	e.t.Cleanup(cancel)
	tx, err := e.db.BeginContext(ictx)
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = tx.Rollback() })
	require.NoError(e.t, loop.begin(ictx, tx, batchID, database.TriageBatchApplying))
	return tx
}

// stopState reads the batch's status and whether a stop was recorded.
func (e *triageEnv) stopState(batchID int64) (status string, recorded bool) {
	e.t.Helper()
	_, err := e.db.QueryOne(pg.Scan(&status, &recorded), `SELECT status, stop_requested_at IS NOT NULL
		FROM author_review_triage_batch WHERE id = ?`, batchID)
	require.NoError(e.t, err)
	return status, recorded
}

// stopWithin runs a stop with the given wait and fails the test when it
// does not answer within limit.
func (e *triageEnv) stopWithin(wait, limit time.Duration, batchID int64) (*database.TriageBatch, error) {
	e.t.Helper()
	admin := NewAuthorReviewTriageAdmin(e.db)
	admin.stopWait = wait
	type result struct {
		batch *database.TriageBatch
		err   error
	}
	done := make(chan result, 1)
	go func() {
		b, err := admin.Stop(context.Background(), batchID, 13)
		done <- result{b, err}
	}()
	select {
	case r := <-done:
		return r.batch, r.err
	case <-time.After(limit):
		e.t.Fatalf("the stop did not answer within %s", limit)
		return nil, nil
	}
}

// A holder idle in its transaction cannot keep the batch from a stop: a
// stop that runs out of its wait answers busy and records nothing, and once
// the item's lifetime is over the server ends the holder and the next stop
// succeeds.
func TestAnIdleItemCannotKeepTheStopWaiting(t *testing.T) {
	e := newTriageEnv(t)
	_, batch := e.applyingStopSet()
	const lifetime = 800 * time.Millisecond
	loop := e.loop(AuthorReviewTriageConfig{ItemTimeout: lifetime})
	began := time.Now()
	holder := e.idleItem(loop, batch.ID)

	_, err := e.stopWithin(100*time.Millisecond, 5*time.Second, batch.ID)
	require.ErrorIs(t, err, database.ErrTriageStopBusy, "the stop gives up its wait")
	status, recorded := e.stopState(batch.ID)
	assert.Equal(t, database.TriageBatchApplying, status, "a busy stop changes nothing")
	assert.False(t, recorded, "a busy stop records no stop")

	var got *database.TriageBatch
	require.Eventually(t, func() bool {
		got, err = e.stopWithin(100*time.Millisecond, 5*time.Second, batch.ID)
		if errors.Is(err, database.ErrTriageStopBusy) {
			return false
		}
		require.NoError(t, err)
		return true
	}, 15*time.Second, 50*time.Millisecond, "a stop asked again succeeds once the item's lifetime is over")
	assert.GreaterOrEqual(t, time.Since(began), lifetime, "the holder lived its lifetime, not less")
	assert.Equal(t, database.TriageBatchStopped, got.Status)
	assert.NotNil(t, got.StopRequestedAt)
	_, err = holder.Exec(`SELECT 1`)
	assert.Error(t, err, "the server ended the idle holder")
}

// A stop whose wait outlasts the item's lifetime succeeds in one request.
func TestAStopThatOutwaitsAnIdleItemSucceeds(t *testing.T) {
	e := newTriageEnv(t)
	_, batch := e.applyingStopSet()
	loop := e.loop(AuthorReviewTriageConfig{ItemTimeout: 500 * time.Millisecond})
	e.idleItem(loop, batch.ID)

	got, err := e.stopWithin(5*time.Second, 10*time.Second, batch.ID)
	require.NoError(t, err)
	assert.Equal(t, database.TriageBatchStopped, got.Status)
}

// The lifetime also ends an item that keeps running: each of its statements
// within their own bounds, the item past its lifetime. Its queries are
// canceled and it rolls back.
func TestABusyItemEndsWithItsLifetime(t *testing.T) {
	e := newTriageEnv(t)
	_, batch := e.applyingStopSet()
	loop := e.loop(AuthorReviewTriageConfig{ItemTimeout: 500 * time.Millisecond})
	ictx, cancel := loop.itemContext(e.ctx)
	defer cancel()
	tx, err := e.db.BeginContext(ictx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, loop.begin(ictx, tx, batch.ID, database.TriageBatchApplying))

	began := time.Now()
	for err == nil && time.Since(began) < 10*time.Second {
		_, err = tx.ExecContext(ictx, `SELECT pg_sleep(0.05)`)
	}
	require.Error(t, err, "the item's statements are canceled")
	assert.Less(t, time.Since(began), 5*time.Second)

	got, err := e.stopWithin(500*time.Millisecond, 5*time.Second, batch.ID)
	require.NoError(t, err, "the canceled item no longer holds the batch")
	assert.Equal(t, database.TriageBatchStopped, got.Status)
}

// holdCredits locks credits, each in its own transaction, and returns the
// transactions with their backends.
func (e *triageEnv) holdCredits(credits ...int64) (txs []*pg.Tx, pids []int) {
	e.t.Helper()
	txs = make([]*pg.Tx, len(credits))
	pids = make([]int, len(credits))
	for i, credit := range credits {
		tx, err := e.db.Begin()
		require.NoError(e.t, err)
		e.t.Cleanup(func() { _ = tx.Rollback() })
		_, err = tx.QueryOne(pg.Scan(&pids[i]), `SELECT pg_backend_pid()`)
		require.NoError(e.t, err)
		_, err = tx.Exec(`SELECT id FROM book_contributor_credit WHERE id = ? FOR UPDATE`, credit)
		require.NoError(e.t, err)
		txs[i] = tx
	}
	return txs, pids
}

// blockedBy reports whether some session waits on the backend pid.
func (e *triageEnv) blockedBy(pid int) bool {
	return e.count(`SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND ? = ANY(pg_blocking_pids(pid))`, pid) > 0
}

// An apply item that waits past its lifetime is canceled and rolled back:
// the item fails, nothing of it is written, and the worker goes on.
func TestAnApplyItemPastItsLifetimeFails(t *testing.T) {
	e := newTriageEnv(t)
	s, batch := e.applyingStopSet()
	e.holdCredits(s.held.credit)
	loop := e.loop(AuthorReviewTriageConfig{Pause: time.Millisecond, ItemTimeout: 500 * time.Millisecond,
		LockTimeout: 30 * time.Second, StatementTimeout: 30 * time.Second})

	worked := make(chan error, 1)
	go func() {
		_, runErr := loop.RunOnce(e.ctx)
		worked <- runErr
	}()
	select {
	case runErr := <-worked:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the item outlived its lifetime")
	}
	rows := e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageOutcomeApplied, rows[s.first.item].Outcome)
	assert.Equal(t, database.TriageOutcomeFailed, rows[s.held.item].Outcome, "the item past its lifetime fails")
	assert.Equal(t, database.TriageOutcomeApplied, rows[s.loaded.item].Outcome, "the worker goes on")
	status, _ := e.itemStatus(s.held.item)
	assert.Equal(t, "open", status, "nothing of the failed item was written")
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_manual_override WHERE scope_fingerprint = ?`,
		s.held.fp))
}

// An undo item that waits past its lifetime is canceled and rolled back too.
func TestAnUndoItemPastItsLifetimeFails(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	batch := e.apply(authortriage.StepFourModel, authortriage.StepRules, authortriage.StepPlaceholderOrg,
		authortriage.StepKeepHandle)
	marked, err := NewAuthorReviewTriageAdmin(e.db).Undo(e.ctx, batch.ID, 11, 0)
	require.NoError(t, err)
	require.Equal(t, 4, marked)
	e.holdCredits(set.accept.credit)
	loop := e.loop(AuthorReviewTriageConfig{Pause: time.Millisecond, ItemTimeout: 500 * time.Millisecond,
		LockTimeout: 30 * time.Second, StatementTimeout: 30 * time.Second})

	worked := make(chan error, 1)
	go func() {
		_, runErr := loop.RunOnce(e.ctx)
		worked <- runErr
	}()
	select {
	case runErr := <-worked:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the undo item outlived its lifetime")
	}
	rows := e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageUndoFailed, rows[set.accept.item].UndoOutcome)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_manual_override WHERE id = ? AND revoked_at IS NULL`,
		rows[set.accept.item].OverrideID), "the failed undo revoked nothing")
	for _, done := range []triageSeed{set.fourModel, set.edit, set.classify} {
		assert.Equal(t, database.TriageUndoRestored, rows[done.item].UndoOutcome)
	}
}

// Large, healthy items commit: two items of two credits each, every credit
// held a while. Each statement stays within the statement bound, each item
// within its lifetime, while an item takes longer than one statement's bound
// and the chunk longer than one item's lifetime.
func TestLargeHealthyItemsWithinTheirLifetimeCommit(t *testing.T) {
	e := newTriageEnv(t)
	first := e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	firstMore := e.seed("last", "Зорро")
	second := e.seedOpen(authornorm.ClassMononym, "last", "Аргус")
	secondMore := e.seed("last", "Аргус")
	require.Equal(t, first.item, firstMore.item, "one item over two credits")
	require.Equal(t, second.item, secondMore.item, "one item over two credits")
	require.Less(t, first.credit, firstMore.credit)
	require.Less(t, second.credit, secondMore.credit)
	withAudited(t, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	require.Equal(t, 2, batch.ItemsTotal)
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))

	const (
		held      = 1200 * time.Millisecond
		statement = 2 * time.Second
		lifetime  = 3500 * time.Millisecond
	)
	holders, pids := e.holdCredits(first.credit, firstMore.credit, second.credit, secondMore.credit)
	loop := e.loop(AuthorReviewTriageConfig{Pause: time.Millisecond, ItemTimeout: lifetime,
		LockTimeout: statement, StatementTimeout: statement})
	worked := make(chan error, 1)
	go func() {
		_, runErr := loop.RunOnce(e.ctx)
		worked <- runErr
	}()
	began := time.Now()
	for i := range holders {
		require.Eventually(t, func() bool { return e.blockedBy(pids[i]) }, 20*time.Second, 10*time.Millisecond,
			"the worker reaches credit %d", i)
		time.Sleep(held)
		require.NoError(t, holders[i].Rollback())
	}
	select {
	case runErr := <-worked:
		require.NoError(t, runErr)
	case <-time.After(20 * time.Second):
		t.Fatal("the chunk never ended")
	}
	require.Greater(t, time.Since(began), lifetime, "the chunk outlived one item's lifetime")
	rows := e.rowsOf(batch.ID)
	for _, s := range []triageSeed{first, second} {
		assert.Equal(t, database.TriageOutcomeApplied, rows[s.item].Outcome)
		assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_manual_override
			WHERE id = ? AND revoked_at IS NULL`, rows[s.item].OverrideID))
	}
	for _, credit := range []int64{first.credit, firstMore.credit, second.credit, secondMore.credit} {
		assert.Equal(t, models.CreditSelectionSelected, selectionOf(t, e.db, credit).State)
	}
}

// A stop needs a wait: zero would wait forever.
func TestAStopWithoutAWaitIsRefused(t *testing.T) {
	e := newTriageEnv(t)
	_, batch := e.applyingStopSet()
	_, err := database.StopTriageBatch(e.ctx, e.db, batch.ID, 13, 0)
	require.Error(t, err)
	assert.NotErrorIs(t, err, database.ErrTriageStopBusy)
	status, recorded := e.stopState(batch.ID)
	assert.Equal(t, database.TriageBatchApplying, status)
	assert.False(t, recorded)
}
