package services

// B1 of design/review-triage/reports/REVIEW-4-5.md: an administrator stops a
// running apply or undo, and it stays stopped. The item in flight commits or
// rolls back whole; no item starts after the stop, not one already loaded in
// the worker's chunk, not after a restart or an expired lease. What was
// applied stays, the rest stays planned, and the applied part can be undone.

import (
	"context"
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

// lockWaiters counts the sessions of the scratch database waiting on a lock.
func (e *triageEnv) lockWaiters() int {
	e.t.Helper()
	return e.count(`SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'`)
}

// stopSet is three planned step-7 items, decided in this order.
type stopSet struct {
	first, held, loaded triageSeed
}

// applyingStopSet plans three handles kept as they are and asks for their
// apply.
func (e *triageEnv) applyingStopSet() (stopSet, *database.TriageBatch) {
	e.t.Helper()
	var s stopSet
	s.first = e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	s.held = e.seedOpen(authornorm.ClassMononym, "last", "Аргус")
	s.loaded = e.seedOpen(authornorm.ClassMononym, "last", "Кассиопея")
	withAudited(e.t, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	require.Equal(e.t, 3, batch.ItemsTotal)
	require.NoError(e.t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))
	return s, batch
}

// stopMidChunk runs one chunk of the whole apply and stops it while the
// second item is in flight: the second item's credit is held, so its
// transaction waits; the stop waits for it; the hold is released, the second
// item commits, the stop is acknowledged, and the third item, loaded in the
// same chunk, never starts.
func (e *triageEnv) stopMidChunk(s *stopSet, batchID int64) *database.TriageBatch {
	e.t.Helper()
	holder, err := e.db.Begin()
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = holder.Rollback() })
	_, err = holder.Exec(`SELECT id FROM book_contributor_credit WHERE id = ? FOR UPDATE`, s.held.credit)
	require.NoError(e.t, err)

	loop := e.loop(AuthorReviewTriageConfig{Pause: time.Millisecond, LockTimeout: 30 * time.Second})
	worked := make(chan error, 1)
	go func() {
		_, runErr := loop.RunOnce(e.ctx)
		worked <- runErr
	}()
	require.Eventually(e.t, func() bool { return e.lockWaiters() == 1 }, 20*time.Second, 10*time.Millisecond,
		"the worker waits on the second item's credit")
	require.Equal(e.t, database.TriageOutcomeApplied, e.rowsOf(batchID)[s.first.item].Outcome)

	type stopResult struct {
		batch *database.TriageBatch
		err   error
	}
	stopped := make(chan stopResult, 1)
	go func() {
		b, stopErr := NewAuthorReviewTriageAdmin(e.db).Stop(context.Background(), batchID, 13)
		stopped <- stopResult{b, stopErr}
	}()
	require.Eventually(e.t, func() bool { return e.lockWaiters() == 2 }, 20*time.Second, 10*time.Millisecond,
		"the stop waits for the item in flight")
	select {
	case r := <-stopped:
		e.t.Fatalf("the stop was acknowledged while an item was in flight: %+v", r)
	default:
	}
	require.NoError(e.t, holder.Rollback())

	var r stopResult
	select {
	case r = <-stopped:
	case <-time.After(20 * time.Second):
		e.t.Fatal("the stop was never acknowledged")
	}
	require.NoError(e.t, r.err)
	select {
	case runErr := <-worked:
		require.NoError(e.t, runErr, "a stop is not a failure of the worker")
	case <-time.After(20 * time.Second):
		e.t.Fatal("the worker did not end its chunk")
	}
	return r.batch
}

func TestStopMidChunkKeepsTheAppliedAndStartsNoOtherItem(t *testing.T) {
	e := newTriageEnv(t)
	s, batch := e.applyingStopSet()
	got := e.stopMidChunk(&s, batch.ID)
	assert.Equal(t, database.TriageBatchStopped, got.Status)
	require.NotNil(t, got.StopRequestedByUserID)
	assert.Equal(t, int64(13), *got.StopRequestedByUserID)
	assert.NotNil(t, got.StopRequestedAt)
	assert.Equal(t, 2, got.ItemsDone, "the stop counts what was decided")

	rows := e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageOutcomeApplied, rows[s.first.item].Outcome)
	assert.Equal(t, database.TriageOutcomeApplied, rows[s.held.item].Outcome, "the item in flight committed")
	assert.Equal(t, database.TriageOutcomePlanned, rows[s.loaded.item].Outcome, "the loaded item never started")
	status, _ := e.itemStatus(s.loaded.item)
	assert.Equal(t, "open", status)
	assert.Equal(t, models.CreditSelectionReview, selectionOf(t, e.db, s.loaded.credit).State)
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_manual_override WHERE scope_fingerprint = ?`,
		s.loaded.fp))
	for _, applied := range []triageSeed{s.first, s.held} {
		assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_manual_override
			WHERE id = ? AND revoked_at IS NULL`, rows[applied.item].OverrideID), "the applied override stays")
	}
}

// A stopped batch is not taken again: not by a restarted worker, not after
// its lease would have expired, and a stale worker's loaded item is refused.
func TestStopHoldsAcrossARestartAndAnExpiredLease(t *testing.T) {
	e := newTriageEnv(t)
	s, batch := e.applyingStopSet()

	first := e.loop(AuthorReviewTriageConfig{Chunk: 1, Pause: time.Millisecond})
	worked, err := first.RunOnce(e.ctx)
	require.NoError(t, err)
	require.True(t, worked)
	stale, err := database.NextTriageApplyItems(e.ctx, e.db, batch.ID, 10)
	require.NoError(t, err)
	require.Len(t, stale, 2)
	view, err := database.ReadTriageBatch(e.ctx, e.db, batch.ID)
	require.NoError(t, err)

	got, err := NewAuthorReviewTriageAdmin(e.db).Stop(e.ctx, batch.ID, 13)
	require.NoError(t, err)
	assert.Equal(t, database.TriageBatchStopped, got.Status)
	assert.Equal(t, 1, got.ItemsDone)

	e.exec(`UPDATE author_review_triage_batch SET lease_until = now() - interval '1 second' WHERE id = ?`, batch.ID)
	for _, loop := range []*AuthorReviewTriageLoop{first, e.fastLoop()} {
		worked, err = loop.RunOnce(e.ctx)
		require.NoError(t, err)
		assert.False(t, worked, "a stopped batch is never taken")
	}
	for i := range stale {
		assert.ErrorIs(t, first.applyItem(e.ctx, &view.Batch, &stale[i]), database.ErrTriageStopped,
			"an item already loaded does not start")
	}
	ctx, cancel := context.WithTimeout(e.ctx, 300*time.Millisecond)
	defer cancel()
	require.NoError(t, e.loop(AuthorReviewTriageConfig{Poll: 10 * time.Millisecond}).Run(ctx))

	rows := e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageOutcomeApplied, rows[s.first.item].Outcome)
	for _, left := range []triageSeed{s.held, s.loaded} {
		assert.Equal(t, database.TriageOutcomePlanned, rows[left.item].Outcome)
		status, _ := e.itemStatus(left.item)
		assert.Equal(t, "open", status)
	}
	done, err := database.ReadTriageBatch(e.ctx, e.db, batch.ID)
	require.NoError(t, err)
	assert.Equal(t, database.TriageBatchStopped, done.Batch.Status)
	assert.Equal(t, 1, done.Batch.ItemsDone)
	assert.ErrorIs(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9), database.ErrTriageBatchState,
		"a stopped apply is not resumed")
}

// The undo of a stopped apply revokes only what it applied; the rows it
// never reached stay planned and their review items stay open, untouched.
func TestUndoAfterAStopTouchesOnlyTheAppliedRows(t *testing.T) {
	e := newTriageEnv(t)
	s, batch := e.applyingStopSet()
	e.stopMidChunk(&s, batch.ID)
	loadedSelection := selectionOf(t, e.db, s.loaded.credit)

	marked, err := NewAuthorReviewTriageAdmin(e.db).Undo(e.ctx, batch.ID, 11, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, marked)
	done := e.drain(e.fastLoop(), batch.ID)
	assert.Equal(t, database.TriageBatchStopped, done.Status, "an undone stopped apply is still a stopped one")

	rows := e.rowsOf(batch.ID)
	for _, applied := range []triageSeed{s.first, s.held} {
		assert.Equal(t, database.TriageUndoRestored, rows[applied.item].UndoOutcome)
		assert.Equal(t, models.CreditSelectionReview, selectionOf(t, e.db, applied.credit).State)
		assert.Len(t, e.openItemsOf(&applied), 1)
	}
	loaded := rows[s.loaded.item]
	assert.Equal(t, database.TriageOutcomePlanned, loaded.Outcome)
	assert.Empty(t, loaded.UndoOutcome)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_review_triage_item
		WHERE id = ? AND undo_requested_at IS NOT NULL`, loaded.ID), "never asked to be undone")
	status, _ := e.itemStatus(s.loaded.item)
	assert.Equal(t, "open", status, "the same review item, still open")
	assert.Equal(t, loadedSelection, selectionOf(t, e.db, s.loaded.credit))
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM contributor_manual_override WHERE revoked_by_user_id = 11`))
}

// An undo can be stopped too; asked again, it undoes the rest, and a batch
// whose apply went through is applied again at its end.
func TestStopAnUndoThenAskItAgain(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	batch := e.apply(authortriage.StepFourModel, authortriage.StepRules, authortriage.StepPlaceholderOrg,
		authortriage.StepKeepHandle)
	admin := NewAuthorReviewTriageAdmin(e.db)
	marked, err := admin.Undo(e.ctx, batch.ID, 11, 0)
	require.NoError(t, err)
	require.Equal(t, 4, marked)
	first := e.loop(AuthorReviewTriageConfig{Chunk: 1, Pause: time.Millisecond})
	worked, err := first.RunOnce(e.ctx)
	require.NoError(t, err)
	require.True(t, worked)
	stale, err := database.NextTriageUndoItems(e.ctx, e.db, batch.ID, 10)
	require.NoError(t, err)
	require.Len(t, stale, 3)
	view, err := database.ReadTriageBatch(e.ctx, e.db, batch.ID)
	require.NoError(t, err)

	got, err := admin.Stop(e.ctx, batch.ID, 13)
	require.NoError(t, err)
	assert.Equal(t, database.TriageBatchStopped, got.Status)
	assert.Equal(t, 1, got.ItemsDone)
	for i := range stale {
		assert.ErrorIs(t, first.undoItem(e.ctx, &view.Batch, &stale[i]), database.ErrTriageStopped,
			"an undo already loaded does not start")
	}
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_manual_override WHERE revoked_at IS NOT NULL`))
	worked, err = e.fastLoop().RunOnce(e.ctx)
	require.NoError(t, err)
	assert.False(t, worked)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_review_triage_item WHERE batch_id = ? AND undo_outcome = ?`,
		batch.ID, database.TriageUndoRestored))

	assert.Equal(t, 3, e.undo(batch.ID, 0), "the rest is asked again")
	for _, s := range []triageSeed{set.fourModel, set.edit, set.classify, set.accept} {
		assert.Equal(t, database.TriageUndoRestored, e.rowsOf(batch.ID)[s.item].UndoOutcome)
	}
}

func TestStopRefusals(t *testing.T) {
	e := newTriageEnv(t)
	e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	withAudited(t, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	admin := NewAuthorReviewTriageAdmin(e.db)
	_, err := admin.Stop(e.ctx, batch.ID, 13)
	assert.ErrorIs(t, err, database.ErrTriageBatchState, "a plan not applying")
	_, err = admin.Stop(e.ctx, batch.ID+1, 13)
	assert.ErrorIs(t, err, database.ErrTriageBatchNotFound)
	_, err = admin.Stop(e.ctx, batch.ID, 0)
	assert.ErrorIs(t, err, database.ErrTriageBatchState, "no administrator")

	require.NoError(t, admin.Apply(e.ctx, batch.ID, 9))
	done := e.drain(e.fastLoop(), batch.ID)
	require.Equal(t, database.TriageBatchApplied, done.Status)
	_, err = admin.Stop(e.ctx, batch.ID, 13)
	assert.ErrorIs(t, err, database.ErrTriageBatchState, "an apply that went through")

	got, err := admin.StartDryRun(e.ctx, 7)
	require.NoError(t, err)
	_, err = admin.Stop(e.ctx, got.ID, 13)
	assert.ErrorIs(t, err, database.ErrTriageBatchState, "a dry run is not stopped")
}

// A dry run may start while a batch is stopped: a stopped batch is not
// active.
func TestAStoppedBatchLetsANewDryRunStart(t *testing.T) {
	e := newTriageEnv(t)
	s, batch := e.applyingStopSet()
	_, err := NewAuthorReviewTriageAdmin(e.db).Stop(e.ctx, batch.ID, 13)
	require.NoError(t, err)
	next := e.planOnce(e.fastLoop())
	rows := e.rowsOf(next.ID)
	for _, left := range []triageSeed{s.first, s.held, s.loaded} {
		assert.Equal(t, database.TriageOutcomePlanned, rows[left.item].Outcome, "still open, planned again")
	}
}

// A chunk that outlives its lease records its progress on the way, which
// renews the lease: another worker cannot take the batch while it works.
func TestALongChunkRenewsItsLease(t *testing.T) {
	e := newTriageEnv(t)
	s, batch := e.applyingStopSet()
	hold := func(credit int64) *pg.Tx {
		tx, err := e.db.Begin()
		require.NoError(t, err)
		t.Cleanup(func() { _ = tx.Rollback() })
		_, err = tx.Exec(`SELECT id FROM book_contributor_credit WHERE id = ? FOR UPDATE`, credit)
		require.NoError(t, err)
		return tx
	}
	second, third := hold(s.held.credit), hold(s.loaded.credit)

	const lease = 600 * time.Millisecond
	loop := e.loop(AuthorReviewTriageConfig{Pause: time.Millisecond, Lease: lease, LockTimeout: 30 * time.Second})
	worked := make(chan error, 1)
	go func() {
		_, runErr := loop.RunOnce(e.ctx)
		worked <- runErr
	}()
	require.Eventually(t, func() bool { return e.lockWaiters() == 1 }, 20*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		return e.count(`SELECT count(*) FROM author_review_triage_batch WHERE id = ? AND lease_until < now()`,
			batch.ID) == 1
	}, 20*time.Second, 10*time.Millisecond, "the second item outlives the lease taken with the chunk")

	// Release the second item only; the third stays held, so the worker is
	// still in its chunk when the lease is read.
	require.NoError(t, second.Rollback())
	require.Eventually(t, func() bool {
		return e.count(`SELECT count(*) FROM author_review_triage_item WHERE batch_id = ? AND outcome = 'applied'`,
			batch.ID) == 2 && e.lockWaiters() == 1
	}, 20*time.Second, 10*time.Millisecond, "the worker waits on the third item")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_review_triage_batch
		WHERE id = ? AND lease_owner = ? AND lease_until > now() AND items_done = 2`, batch.ID, loop.owner),
		"the lease was renewed with the progress, inside the chunk")
	other, err := database.ClaimTriageBatch(e.ctx, e.db, e.fastLoop().owner, lease)
	require.NoError(t, err)
	assert.Nil(t, other, "no other worker takes the batch")

	require.NoError(t, third.Rollback())
	select {
	case runErr := <-worked:
		require.NoError(t, runErr)
	case <-time.After(20 * time.Second):
		t.Fatal("the chunk never ended")
	}
	done := e.drain(loop, batch.ID)
	assert.Equal(t, database.TriageBatchApplied, done.Status)
	assert.Equal(t, 3, done.ItemsDone)
}
