package services

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deterministic interleavings of the acceptance pass with a concurrent credit
// write (review finding B2, re-review round 1). A query hook pauses the pass
// at an exact statement; the test then writes from another connection of the
// same pool and resumes it. Every case runs with the local workers off: only
// the pass itself, as at start.

// pauseAfter pauses the first query whose text matches, right after it
// returned, until released.
type pauseAfter struct {
	matches func(query string) bool
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func newPauseAfter(matches func(query string) bool) *pauseAfter {
	h := &pauseAfter{matches: matches, entered: make(chan struct{}), release: make(chan struct{})}
	h.armed.Store(true)
	return h
}

func (h *pauseAfter) BeforeQuery(ctx context.Context, _ *pg.QueryEvent) (context.Context, error) {
	return ctx, nil
}

func (h *pauseAfter) AfterQuery(ctx context.Context, e *pg.QueryEvent) error {
	q, err := e.FormattedQuery()
	if err != nil || !h.matches(string(q)) || !h.armed.CompareAndSwap(true, false) {
		return nil //nolint:nilerr // an unformattable query is simply not the one to pause on
	}
	close(h.entered)
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isDuplicateCheck(q string) bool {
	return strings.Contains(q, "SELECT EXISTS (") && strings.Contains(q, "ANY (c.quality_flags)")
}

func isCommit(q string) bool { return strings.TrimSpace(q) == "COMMIT" }

// waitedOnAnInputLock reports whether some session of the database waits for
// an advisory lock now.
func waitedOnAnInputLock(t *testing.T, db *pg.DB) bool {
	t.Helper()
	var waiting int
	_, err := db.QueryOne(pg.Scan(&waiting), `SELECT count(*) FROM pg_locks
		WHERE locktype = 'advisory' AND NOT granted
			AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`)
	require.NoError(t, err)
	return waiting > 0
}

func plainAndRepeated(t *testing.T) (plain, repeated authornorm.SourceValue) {
	return source(t, "first", "Иван", "last", "Петров"), source(t, "first", "Иван", "first", "", "last", "Петров")
}

// Probe A: the flagged credit is written while the pass sits between its
// duplicate check and its page. The page must not select a credit the check
// did not see: either the write waits for the page (one per-input lock), or
// the page never sees it.
func TestAcceptanceFlagCommitAfterCheck(t *testing.T) {
	db := localWorkerDB(t)
	resetPipeline(t, db)
	f := &workerFixture{t: t, db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plain, repeated := plainAndRepeated(t)
	earlier := f.waitingCredits(author(plain))
	resultID := *f.selection(earlier[0]).ResultID
	stored, err := database.LoadLocalResult(ctx, db, resultID)
	require.NoError(t, err)
	decision, err := authornorm.ProductionAcceptancePolicy().Decide(&stored)
	require.NoError(t, err)
	f.registerStructuredPerson()

	pause := newPauseAfter(isDuplicateCheck)
	db.AddQueryHook(pause)
	done := make(chan error, 1)
	go func() { done <- NewAuthorAcceptancePass(db).Run(ctx) }()
	select {
	case <-pause.entered:
	case <-ctx.Done():
		t.Fatal("the pass never reached its duplicate check")
	}

	var late []int64
	persisted := make(chan struct{})
	go func() {
		defer close(persisted)
		late = f.persistBook(author(repeated))
	}()
	// The write either finishes (nothing holds it) or waits on the input lock.
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for !blocked && time.Now().Before(deadline) {
		select {
		case <-persisted:
			deadline = time.Now()
		default:
			blocked = waitedOnAnInputLock(t, db)
			time.Sleep(10 * time.Millisecond)
		}
	}
	resolveRace := func() {
		// The old completion: the late credit is resolved from the plain
		// result as well, so it waits with policy_not_registered.
		require.NoError(t, db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			_, resolveErr := database.ResolveCredits(ctx, tx, stored.SourceFingerprint[:], stored.ExtractorVersion,
				&database.AutomaticOutcome{ResultID: resultID, DecisionClass: stored.DecisionClass, Decision: decision})
			return resolveErr
		}))
	}
	if blocked {
		// The write waited for the page: the pass saw the input plain, and
		// the flagged credit joins after it, with no resolution yet.
		close(pause.release)
		require.NoError(t, <-done)
		<-persisted
	} else {
		<-persisted
		resolveRace()
		require.Equal(t, models.CreditSelectionUnresolved, f.selection(late[0]).State)
		close(pause.release)
		require.NoError(t, <-done)
	}

	assert.True(t, blocked, "the credit write must wait for the pass's page on the same input")
	if s := f.selection(late[0]); s != nil {
		assert.NotEqual(t, models.CreditSelectionSelected, s.State,
			"a flagged credit committed before the page is read must not be autoaccepted")
	}
	// The earlier credit was selected under a check that saw the input plain,
	// in the same locked transaction as the page; the later flag is the local
	// reconciliation's case.
	assert.Equal(t, models.CreditSelectionSelected, f.selection(earlier[0]).State)
}

// Probe B: the flag commits after the pass's first page and before its
// second. The second page sees the input ambiguous; the pass must not end
// with its own earlier automatic selection of that input in place.
func TestAcceptanceFlagCommitBetweenPagesWorkersOff(t *testing.T) {
	db := localWorkerDB(t)
	resetPipeline(t, db)
	f := &workerFixture{t: t, db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plain, repeated := plainAndRepeated(t)
	ids := f.waitingCredits(author(plain), author(plain))
	f.registerStructuredPerson()
	audits := f.audits(ids[0])

	pause := newPauseAfter(isCommit)
	db.AddQueryHook(pause)
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := ApplyAcceptancePolicy(ctx, db, authornorm.NormalizerVersion, 1)
		done <- result{n, err}
	}()
	select {
	case <-pause.entered:
	case <-ctx.Done():
		t.Fatal("the pass never committed its first page")
	}
	require.Equal(t, models.CreditSelectionSelected, f.selection(ids[0]).State)
	late := f.persistBook(author(repeated))
	close(pause.release)
	got := <-done
	require.NoError(t, got.err)

	first := f.selection(ids[0])
	assert.Equal(t, models.CreditSelectionUnresolved, first.State,
		"the pass must not end with an automatic selection of an input it saw ambiguous")
	if assert.NotNil(t, first.UnresolvedReason) {
		assert.Equal(t, models.UnresolvedPolicyNotRegistered, *first.UnresolvedReason, "back where the pass found it")
	}
	assert.Equal(t, audits+2, f.audits(ids[0]), "the selection and its demotion are both audited")
	assert.Equal(t, models.CreditSelectionUnresolved, f.selection(ids[1]).State)
	assert.Nil(t, f.selection(late[0]))
	assert.Zero(t, got.n, "the count is what the pass leaves selected")

	// The local worker, when it runs, sends the whole input to review.
	f.drain(f.worker(nil))
	for _, id := range append(ids, late...) {
		assert.Equal(t, models.CreditSelectionReview, f.selection(id).State, "credit %d", id)
	}
}

// The disclosed residual: a flag arriving after the whole pass finished is
// not repaired by a later start with the workers off; the local
// reconciliation repairs it when the workers run.
func TestAcceptanceResidualAfterCompletedPassWorkersOff(t *testing.T) {
	db := localWorkerDB(t)
	resetPipeline(t, db)
	f := &workerFixture{t: t, db: db}
	ctx := context.Background()
	plain, repeated := plainAndRepeated(t)
	earlier := f.waitingCredits(author(plain))
	f.registerStructuredPerson()
	require.NoError(t, NewAuthorAcceptancePass(db).Run(ctx))
	require.Equal(t, models.CreditSelectionSelected, f.selection(earlier[0]).State)

	late := f.persistBook(author(repeated))
	require.NoError(t, NewAuthorAcceptancePass(db).Run(ctx))
	assert.Equal(t, models.CreditSelectionSelected, f.selection(earlier[0]).State)
	assert.Nil(t, f.selection(late[0]))

	f.drain(f.worker(nil))
	assert.Equal(t, models.CreditSelectionReview, f.selection(earlier[0]).State)
	assert.Equal(t, models.CreditSelectionReview, f.selection(late[0]).State)
}

// failPage fails the second credit-page query.
type failPage struct{ pages atomic.Int32 }

func (h *failPage) BeforeQuery(ctx context.Context, e *pg.QueryEvent) (context.Context, error) {
	q, err := e.FormattedQuery()
	if err == nil && strings.Contains(string(q), "SELECT c.id, c.source_fingerprint") &&
		strings.Contains(string(q), "ORDER BY c.id") && h.pages.Add(1) == 2 {
		return ctx, errors.New("injected second credit-page failure")
	}
	return ctx, nil
}

func (h *failPage) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

// Review finding N3: a failure on a later page returns the error together
// with the credits earlier pages committed.
func TestAcceptancePartialPageCount(t *testing.T) {
	db := localWorkerDB(t)
	resetPipeline(t, db)
	f := &workerFixture{t: t, db: db}
	f.waitingCredits(author(tolstoy(t)), author(tolstoy(t)))
	f.registerStructuredPerson()
	db.AddQueryHook(&failPage{})

	n, err := ApplyAcceptancePolicy(context.Background(), db, authornorm.NormalizerVersion, 1)
	require.Error(t, err)
	committed := f.count(`SELECT count(*) FROM book_contributor_credit_selection WHERE state = 'selected'`)
	assert.Equal(t, 1, committed)
	assert.Equal(t, committed, n, "committed earlier pages are counted")
}
