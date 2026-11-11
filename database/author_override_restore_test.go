package database

// Review triage, Task 3: a review decision taken against an expected
// proposal, the override it wrote, and its undo — the override revoked and
// the fingerprint's credits back in review on the proposal, reopening the
// item.

import (
	"context"
	"strings"
	"testing"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decidedItem is a seeded review item accepted by admin: the item, its
// credit, the proposal and the report of the accept.
type decidedItem struct {
	item, credit, proposal int64
	r                      authornorm.Result
	report                 ReviewReport
}

func (f *authorSchemaFixture) proposalOf(item int64) int64 {
	f.t.Helper()
	return f.returningID(`SELECT proposal_result_id FROM contributor_review_item WHERE id = ?`, item)
}

func (f *authorSchemaFixture) acceptedItem(admin int64) decidedItem {
	f.t.Helper()
	item, credit, r := seedReviewItem(f.t, f)
	d := decidedItem{item: item, credit: credit, r: r, proposal: f.proposalOf(item)}
	var err error
	d.report, err = ApplyReviewAction(context.Background(), f.tx, item, admin,
		ReviewDecision{Action: ReviewAccept, ExpectProposalResultID: d.proposal})
	require.NoError(f.t, err)
	require.Equal(f.t, models.CreditSelectionSelected, f.selection(credit).State)
	return d
}

func TestReviewActionRefusesAChangedProposal(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	item, credit, r := seedReviewItem(t, f)
	proposal := f.proposalOf(item)

	for _, decision := range []ReviewDecision{
		{Action: ReviewAccept, ExpectProposalResultID: proposal + 1},
		{Action: ReviewClassify, Kind: models.NormalizationCollective, ExpectProposalResultID: proposal + 1},
		{Action: ReviewEdit, Correction: correctionOf("Иван Петров"), ExpectProposalResultID: proposal + 1},
	} {
		_, err := ApplyReviewAction(ctx, f.tx, item, admin, decision)
		assert.ErrorIs(t, err, ErrReviewProposalChanged, "%s", decision.Action)
	}
	assert.Len(t, f.openReviewItems(r.SourceFingerprint), 1, "the item stays open")
	assert.Equal(t, models.CreditSelectionReview, f.selection(credit).State)
	assert.Zero(t, f.count(`SELECT count(*) FROM contributor_manual_override`))

	_, err := ApplyReviewAction(ctx, f.tx, item, admin,
		ReviewDecision{Action: ReviewAccept, ExpectProposalResultID: proposal})
	require.NoError(t, err, "the expected proposal is decided")
	assert.Empty(t, f.openReviewItems(r.SourceFingerprint))
}

func TestReviewReportCarriesTheOverride(t *testing.T) {
	ctx := context.Background()
	for _, decision := range []ReviewDecision{
		{Action: ReviewAccept},
		{Action: ReviewEdit, Correction: correctionOf("Иван Петров")},
		{Action: ReviewClassify, Kind: models.NormalizationCollective},
	} {
		t.Run(string(decision.Action), func(t *testing.T) {
			f := withScratchTx(t)
			admin := f.user()
			item, credit, r := seedReviewItem(t, f)
			report, err := ApplyReviewAction(ctx, f.tx, item, admin, decision)
			require.NoError(t, err)
			newest := f.returningID(`SELECT max(id) FROM contributor_manual_override WHERE scope_fingerprint = ?`,
				r.SourceFingerprint[:])
			assert.Equal(t, newest, report.OverrideID)
			selected := f.selection(credit)
			require.NotNil(t, selected.OverrideID)
			assert.Equal(t, report.OverrideID, *selected.OverrideID)
		})
	}

	t.Run("no override, no id", func(t *testing.T) {
		f := withScratchTx(t)
		admin := f.user()
		item, _, _ := seedReviewItem(t, f)
		report, err := ApplyReviewAction(ctx, f.tx, item, admin, ReviewDecision{Action: ReviewLeaveUnresolved})
		require.NoError(t, err)
		assert.Zero(t, report.OverrideID)
	})
}

func TestRevokeRestoresTheReviewState(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	d := f.acceptedItem(admin)

	report, err := RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID, admin, d.proposal, d.r.DecisionClass)
	require.NoError(t, err)
	assert.Equal(t, RestoreReport{Credits: 1, Reopened: true}, report)

	selected := f.selection(d.credit)
	assert.Equal(t, models.CreditSelectionReview, selected.State)
	require.NotNil(t, selected.ResultID)
	assert.Equal(t, d.proposal, *selected.ResultID)
	assert.Nil(t, selected.OverrideID)
	assert.Nil(t, selected.Basis)

	open := f.openReviewItems(d.r.SourceFingerprint)
	require.Len(t, open, 1)
	assert.NotEqual(t, d.item, open[0].ID, "a new item")
	require.NotNil(t, open[0].ProposalResultID)
	assert.Equal(t, d.proposal, *open[0].ProposalResultID, "on the same proposal")
	assert.Equal(t, models.ReviewAmbiguousDecision, open[0].Reason)

	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_manual_override
		WHERE id = ? AND revoked_at IS NOT NULL AND revoked_by_user_id = ?`, d.report.OverrideID, admin))
	active, err := ActiveOverrideForCredit(f.tx, d.credit)
	require.NoError(t, err)
	assert.Nil(t, active)

	// The reopened item is decided again like any other.
	_, err = ApplyReviewAction(ctx, f.tx, open[0].ID, admin, ReviewDecision{Action: ReviewAccept})
	require.NoError(t, err)
	assert.Equal(t, models.CreditSelectionSelected, f.selection(d.credit).State)
}

func TestRevokeKeepsANewerOverride(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	d := f.acceptedItem(admin)
	newer, err := ApplyOverride(ctx, f.tx, OverrideScope{Fingerprint: d.r.SourceFingerprint[:]}, admin,
		correctionOf("Иван Петров"))
	require.NoError(t, err)

	report, err := RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID, admin, d.proposal, d.r.DecisionClass)
	require.NoError(t, err)
	assert.Equal(t, RestoreReport{Credits: 1, Superseded: true}, report)
	selected := f.selection(d.credit)
	assert.Equal(t, models.CreditSelectionSelected, selected.State)
	require.NotNil(t, selected.OverrideID)
	assert.Equal(t, newer.OverrideID, *selected.OverrideID, "the admin's newer override stays")
	assert.Empty(t, f.openReviewItems(d.r.SourceFingerprint))
}

// The plan expected a deleted book to take its override with it. It does not:
// author_layer_delete_books keeps every fingerprint override (migration 26,
// amendment 1), and a triage decision is one. The revoke goes through with no
// credit to restore, so the decision does not come back with the book.
func TestRevokeAfterTheBookWasDeleted(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	d := f.acceptedItem(admin)
	book := f.returningID(`SELECT s.book_id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE c.id = ?`, d.credit)
	f.exec(`SELECT author_layer_delete_books(?::bigint[])`, pg.Array([]int64{book}))
	require.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_manual_override WHERE id = ?`, d.report.OverrideID),
		"the fingerprint override outlives its book")

	report, err := RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID, admin, d.proposal, d.r.DecisionClass)
	require.NoError(t, err)
	assert.Equal(t, RestoreReport{}, report)
	active, err := ActiveOverrideForFingerprint(f.tx, d.r.SourceFingerprint[:])
	require.NoError(t, err)
	assert.Nil(t, active, "a returning book is not decided by the undone override")
}

func TestRevokeOfAMissingOrCreditOverrideIsRefused(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	d := f.acceptedItem(admin)

	_, err := RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID+1000, admin, d.proposal, d.r.DecisionClass)
	assert.ErrorIs(t, err, ErrOverrideNotFound)
	_, err = RevokeOverrideAndRestore(ctx, f.tx, 0, admin, d.proposal, d.r.DecisionClass)
	assert.ErrorIs(t, err, ErrOverrideNotFound)

	byCredit, err := ApplyOverride(ctx, f.tx, OverrideScope{CreditID: d.credit}, admin, correctionOf("Иван Петров"))
	require.NoError(t, err)
	_, err = RevokeOverrideAndRestore(ctx, f.tx, byCredit.OverrideID, admin, d.proposal, d.r.DecisionClass)
	assert.ErrorIs(t, err, ErrInvalidOverrideScope)

	_, err = RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID, 0, d.proposal, d.r.DecisionClass)
	assert.ErrorIs(t, err, ErrInvalidReviewActor)
	_, err = RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID, admin, d.report.ResultID, d.r.DecisionClass)
	assert.ErrorIs(t, err, ErrInvalidAutomaticOutcome, "a manual result is no proposal")
	assert.Equal(t, 0, f.count(`SELECT count(*) FROM contributor_manual_override WHERE revoked_at IS NOT NULL`))
}

func TestRevokeRefusesWhenACreditMovedToAnotherExtractor(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	d := f.acceptedItem(admin)
	moved := f.extraction(&extractionSpec{book: f.book(), extractor: "extractor-v2",
		normalizer: authornorm.NormalizerVersion, credits: []ExtractionCredit{authorCredit(initialsSource(t))}})
	_, err := PersistExtraction(f.tx, moved)
	require.NoError(t, err)

	f.exec(`SAVEPOINT revoke_moved`)
	_, err = RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID, admin, d.proposal, d.r.DecisionClass)
	assert.ErrorIs(t, err, ErrRestoreExtractorMoved)
	f.exec(`ROLLBACK TO SAVEPOINT revoke_moved`)
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_manual_override
		WHERE id = ? AND revoked_at IS NULL`, d.report.OverrideID), "the override stays active")
	assert.Equal(t, models.CreditSelectionSelected, f.selection(d.credit).State)
}

func TestRevokeTwiceIsRefused(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	d := f.acceptedItem(admin)
	_, err := RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID, admin, d.proposal, d.r.DecisionClass)
	require.NoError(t, err)
	_, err = RevokeOverrideAndRestore(ctx, f.tx, d.report.OverrideID, admin, d.proposal, d.r.DecisionClass)
	assert.ErrorIs(t, err, ErrOverrideRevoked)
	assert.Len(t, f.openReviewItems(d.r.SourceFingerprint), 1, "the first undo's item, once")
}

// A revoke and a resolver on the same fingerprint, the revoke gated after it
// locked every credit: the resolver waits and both finish, no 40P01.
func TestRevokeKeepsLockOrder(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()

	seed, err := s.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: seed, next: &next}
	admin := f.user()
	d := f.acceptedItem(admin)
	require.NoError(t, seed.Commit())

	auto := &AutomaticOutcome{ResultID: d.proposal, DecisionClass: d.r.DecisionClass,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview}}

	revokeTx, err := s.Begin()
	require.NoError(t, err)
	gate := &revokeGate{Tx: revokeTx, ready: make(chan struct{}), release: make(chan struct{})}
	revokeDone := make(chan error, 1)
	go func() {
		_, revokeErr := RevokeOverrideAndRestore(ctx, gate, d.report.OverrideID, admin, d.proposal, d.r.DecisionClass)
		if revokeErr != nil {
			_ = revokeTx.Rollback()
			revokeDone <- revokeErr
			return
		}
		revokeDone <- revokeTx.Commit()
	}()
	select {
	case <-gate.ready:
	case err = <-revokeDone:
		t.Fatalf("the revoke ended before revoking: %v", err)
	}

	resolveTx, err := s.Begin()
	require.NoError(t, err)
	resolution := resolveConcurrently(ctx, resolveTx, resolveTx, d.credit, auto)
	waitBlockedOrFinished(t, s, resolution)
	assert.False(t, resolution.finished.Load(), "the resolver waits: the revoke locked the credits first")

	close(gate.release)
	require.NoError(t, <-revokeDone)
	require.NoError(t, <-resolution.done, "the resolver finishes without a deadlock")

	selected, err := selectionOn(s, d.credit)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionReview, selected.State)
	var open int
	_, err = s.QueryOne(pg.Scan(&open), `SELECT count(*) FROM contributor_review_item
		WHERE scope_fingerprint = ? AND status = 'open'`, d.r.SourceFingerprint[:])
	require.NoError(t, err)
	assert.Equal(t, 1, open)
}

// revokeGate pauses the revoke exactly before it revokes the override, after
// it locked the credits.
type revokeGate struct {
	*pg.Tx
	ready, release chan struct{}
}

func (g *revokeGate) ExecContext(ctx context.Context, q interface{}, p ...interface{}) (pg.Result, error) {
	if sql, ok := q.(string); ok && strings.Contains(sql, "UPDATE contributor_manual_override") &&
		strings.Contains(sql, "revoked_at = now()") {
		close(g.ready)
		<-g.release
	}
	return g.Tx.ExecContext(ctx, q, p...)
}

func TestFingerprintPinned(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	entity := func(id string, pinned bool) {
		f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key, name_pinned)
			VALUES (?, 'person', 'Имя', 'Имя', 'имя', ?)`, id, pinned)
	}
	assign := func(fp []byte, id, rule string) {
		f.exec(`INSERT INTO author_entity_assignment (source_fingerprint, extractor_version, entity_id, method, rule)
			VALUES (?, 'v1', ?, 'deterministic', ?)`, fp, id, rule)
	}
	pin := func(fp []byte, revoked bool) {
		id := f.returningID(`INSERT INTO author_entity_merge_pin (created_by_user_id) VALUES (?) RETURNING id`, admin)
		f.exec(`INSERT INTO author_entity_merge_pin_member (pin_id, side, source_fingerprint) VALUES (?, 'a', ?)`,
			id, fp)
		if revoked {
			f.exec(`UPDATE author_entity_merge_pin SET revoked_at = now(), revoked_by_user_id = ? WHERE id = ?`,
				admin, id)
		}
	}
	pinned := func(fp []byte) bool {
		got, err := FingerprintPinned(ctx, f.tx, fp)
		require.NoError(t, err)
		return got
	}

	const (
		plain     = "00000000-0000-7000-8000-000000000001"
		namePin   = "00000000-0000-7000-8000-000000000002"
		heldByPin = "00000000-0000-7000-8000-000000000003"
	)
	entity(plain, false)
	entity(namePin, true)
	entity(heldByPin, false)

	free := fingerprint(32, 0x71)
	assign(free, plain, "same_name")
	assert.False(t, pinned(free), "an ordinary assignment")
	assert.False(t, pinned(fingerprint(32, 0x70)), "no assignment at all")

	byMergePin := fingerprint(32, 0x72)
	assign(byMergePin, plain, "same_name")
	pin(byMergePin, false)
	assert.True(t, pinned(byMergePin), "a member of a live merge pin")

	byRevokedPin := fingerprint(32, 0x73)
	pin(byRevokedPin, true)
	assert.False(t, pinned(byRevokedPin), "a revoked merge pin pins nothing")

	byName := fingerprint(32, 0x74)
	assign(byName, namePin, "same_name")
	assert.True(t, pinned(byName), "an entity with a pinned name")

	beside := fingerprint(32, 0x75)
	assign(beside, heldByPin, "same_name")
	assert.False(t, pinned(beside))
	assign(fingerprint(32, 0x76), heldByPin, "pin:manual")
	assert.True(t, pinned(beside), "an entity holding a pinned assignment")
	assert.False(t, pinned(free), "another entity's pins do not count")
}
