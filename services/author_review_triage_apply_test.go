package services

// Task 5 of design/review-triage: the apply decides the planned items of the
// audited steps one transaction each through the review service, skips what
// changed since the dry run, and survives a lost lease; the undo revokes the
// batch's overrides per step or per batch and reopens the items.

import (
	"context"
	"encoding/json"
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

// withAudited replaces the audit gate for one test: the steps passed with
// the labels of the decision's bar.
func withAudited(t *testing.T, steps ...authortriage.Step) {
	t.Helper()
	prevSteps, prevLog := auditedStepsFn, auditLogFn
	t.Cleanup(func() { auditedStepsFn, auditLogFn = prevSteps, prevLog })
	auditedStepsFn = func() []authortriage.Step { return steps }
	auditLogFn = func() []authortriage.AuditEntry {
		out := []authortriage.AuditEntry{}
		for _, s := range steps {
			out = append(out, authortriage.AuditEntry{Step: s, Labeled: authortriage.SampleSize(s), Passed: true})
		}
		return out
	}
}

// fastLoop applies with no pause between chunks.
func (e *triageEnv) fastLoop() *AuthorReviewTriageLoop {
	return e.loop(AuthorReviewTriageConfig{Pause: time.Millisecond})
}

// drain runs the loop until the batch neither applies nor undoes.
func (e *triageEnv) drain(loop *AuthorReviewTriageLoop, batchID int64) *database.TriageBatch {
	e.t.Helper()
	for range 200 {
		view, err := database.ReadTriageBatch(e.ctx, e.db, batchID)
		require.NoError(e.t, err)
		if view.Batch.Status != database.TriageBatchApplying && view.Batch.Status != database.TriageBatchUndoing {
			return &view.Batch
		}
		_, err = loop.RunOnce(e.ctx)
		require.NoError(e.t, err)
	}
	e.t.Fatal("the batch never settled")
	return nil
}

// applySet is one open item of each action and of step 1.
type applySet struct {
	fourModel, edit, classify, accept triageSeed
}

// seedApplySet seeds a step-1 item, an all-caps name the rules edit (step
// 3), a placeholder classified unknown (step 5) and a handle kept as it is
// (step 7).
func (e *triageEnv) seedApplySet() applySet {
	e.t.Helper()
	var s applySet
	s.fourModel = e.seedOpen(authornorm.ClassInitials, "first", "А.", "last", "Сидоров")
	e.storeAnswers(&s.fourModel, 1, fourModelAnswers()...)
	for _, family := range []string{"Иванов", "Петров", "Смирнов", "Козлов", "Волков"} {
		e.seedSelected("first", "Михаил", "last", family)
	}
	s.edit = e.seedOpen(authornorm.ClassCaseDefect, "first", "МИХАИЛ", "last", "АНДРЕЕВ")
	s.classify = e.seedOpen(authornorm.ClassCaseDefect, "last", "неизвестен", "first", "Автор")
	s.accept = e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	return s
}

// apply plans the open items and applies the audited steps to the end.
func (e *triageEnv) apply(steps ...authortriage.Step) *database.TriageBatch {
	e.t.Helper()
	withAudited(e.t, steps...)
	batch := e.planOnce(e.fastLoop())
	require.NoError(e.t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))
	return e.drain(e.fastLoop(), batch.ID)
}

func (e *triageEnv) itemStatus(item int64) (status, resolution string) {
	e.t.Helper()
	var row struct {
		Status     string `pg:"status"`
		Resolution string `pg:"resolution"`
	}
	_, err := e.db.QueryOne(&row, `SELECT status, coalesce(resolution, '') AS resolution
		FROM contributor_review_item WHERE id = ?`, item)
	require.NoError(e.t, err)
	return row.Status, row.Resolution
}

func (e *triageEnv) openItemsOf(s *triageSeed) []int64 {
	e.t.Helper()
	var ids []int64
	_, err := e.db.Query(&ids, `SELECT id FROM contributor_review_item WHERE scope_fingerprint = ? AND status = 'open'`,
		s.fp)
	require.NoError(e.t, err)
	return ids
}

func TestApplyDecidesEachPlannedItem(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	batch := e.apply(authortriage.StepFourModel, authortriage.StepRules, authortriage.StepPlaceholderOrg,
		authortriage.StepKeepHandle)
	assert.Equal(t, database.TriageBatchApplied, batch.Status)
	assert.Equal(t, 4, batch.ItemsTotal)
	assert.Equal(t, 4, batch.ItemsDone)
	assert.Equal(t, int64(9), *batch.ApplyRequestedByUserID)

	rows := e.rowsOf(batch.ID)
	for _, c := range []struct {
		seed   triageSeed
		action string
	}{
		{set.edit, "edited"}, {set.classify, "classified"}, {set.accept, "accepted"},
	} {
		row := rows[c.seed.item]
		assert.Equal(t, c.action, row.Action, "the plan's action")
		assert.Equal(t, database.TriageOutcomeApplied, row.Outcome)
		status, resolution := e.itemStatus(c.seed.item)
		assert.Equal(t, "closed", status)
		assert.Equal(t, c.action, resolution)
		sel := selectionOf(t, e.db, c.seed.credit)
		assert.Equal(t, models.CreditSelectionSelected, sel.State)
		require.NotNil(t, sel.OverrideID)
		assert.Equal(t, row.OverrideID, *sel.OverrideID, "the log names the override the credit points at")
		require.NotNil(t, sel.ResultID)
		assert.Equal(t, row.ResultID, *sel.ResultID)
		require.NotNil(t, sel.Basis)
		assert.Equal(t, "fingerprint_override", string(*sel.Basis))
		assert.Equal(t, 9, e.count(`SELECT created_by_user_id FROM contributor_manual_override WHERE id = ?`,
			row.OverrideID), "the actor is the admin who asked for the apply")
	}
	var display, kind string
	_, err := e.db.QueryOne(pg.Scan(&display, &kind), `SELECT display_name, kind FROM contributor_normalization_result
		WHERE id = ?`, rows[set.edit.item].ResultID)
	require.NoError(t, err)
	assert.Equal(t, "Михаил Андреев", display)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_normalization_result
		WHERE id = ? AND kind = 'unknown'`, rows[set.classify.item].ResultID))
	assert.Equal(t, database.TriageOutcomeApplied, rows[set.fourModel.item].Outcome)
}

// reopen closes the seed's item as retried by the admin and lets the
// resolver open a new item for the same fingerprint.
func (e *triageEnv) reopen(s *triageSeed) int64 {
	e.t.Helper()
	_, err := reviewService(e.t, e.db, DefaultAuthorMetadataReviewConfig()).Retry(e.ctx, s.item, 7)
	require.NoError(e.t, err)
	result, err := authornorm.Normalize(s.source, reviewExtractor)
	require.NoError(e.t, err)
	var resultID int64
	_, err = e.db.QueryOne(pg.Scan(&resultID), `SELECT id FROM contributor_normalization_result
		WHERE normalization_key = ?`, result.NormalizationKey[:])
	require.NoError(e.t, err)
	require.NoError(e.t, e.db.RunInTransaction(e.ctx, func(tx *pg.Tx) error {
		_, rErr := database.ResolveCredits(e.ctx, tx, s.fp, reviewExtractor, &database.AutomaticOutcome{
			ResultID: resultID, DecisionClass: result.DecisionClass,
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview}})
		return rErr
	}))
	open := e.openItemsOf(s)
	require.Len(e.t, open, 1)
	require.NotEqual(e.t, s.item, open[0])
	return open[0]
}

func TestApplySkipsChangedItems(t *testing.T) {
	e := newTriageEnv(t)
	byHand := e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	retried := e.seedOpen(authornorm.ClassMononym, "last", "Аргус")
	pinned := e.seedOpen(authornorm.ClassMononym, "last", "Кассиопея")
	untouched := e.seedOpen(authornorm.ClassMononym, "last", "Мефистофель")
	withAudited(t, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())

	_, err := reviewService(t, e.db, DefaultAuthorMetadataReviewConfig()).Accept(e.ctx, byHand.item, 7)
	require.NoError(t, err)
	replacement := e.reopen(&retried)
	e.pinByName(&pinned)

	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))
	e.drain(e.fastLoop(), batch.ID)
	rows := e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageOutcomeConflict, rows[byHand.item].Outcome)
	assert.Equal(t, database.TriageOutcomeConflict, rows[retried.item].Outcome)
	assert.Equal(t, database.TriageOutcomeSkippedChanged, rows[pinned.item].Outcome)
	assert.Equal(t, database.TriageOutcomeApplied, rows[untouched.item].Outcome)

	status, _ := e.itemStatus(replacement)
	assert.Equal(t, "open", status, "never applied to the replacement item")
	status, _ = e.itemStatus(pinned.item)
	assert.Equal(t, "open", status)
	assert.Equal(t, models.CreditSelectionReview, selectionOf(t, e.db, pinned.credit).State)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_manual_override WHERE scope_fingerprint = ?`,
		byHand.fp), "only the admin's own override")
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_manual_override WHERE scope_fingerprint = ?`,
		retried.fp))
}

// A plan row whose proposal is not the open item's (the item changed under
// the dry run) is skipped and the item stays open.
func TestApplySkipsAChangedProposal(t *testing.T) {
	e := newTriageEnv(t)
	s := e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	var proposal int64
	_, err := e.db.QueryOne(pg.Scan(&proposal), `SELECT proposal_result_id FROM contributor_review_item WHERE id = ?`,
		s.item)
	require.NoError(t, err)
	other := proposal + 1000
	id := e.storePlan([]database.TriagePlanItem{{ReviewItemID: s.item, Fingerprint: s.fp, Class: "mononym",
		Credits: 1, ProposalResultID: &other, Step: 7, Rule: "keep_handle", Action: "accepted",
		Planned: json.RawMessage(`{"display":"Зорро","kind":"person"}`), Evidence: json.RawMessage(`{}`),
		Outcome: database.TriageOutcomePlanned}})
	withAudited(t, authortriage.StepKeepHandle)
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, id, 9))
	e.drain(e.fastLoop(), id)
	assert.Equal(t, database.TriageOutcomeSkippedChanged, e.rowsOf(id)[s.item].Outcome)
	status, _ := e.itemStatus(s.item)
	assert.Equal(t, "open", status)
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_manual_override`))
}

func TestApplyOnlyAuditedSteps(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	before := selectionOf(t, e.db, set.edit.credit)
	batch := e.apply(authortriage.StepPlaceholderOrg)
	rows := e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageOutcomeApplied, rows[set.classify.item].Outcome)
	for _, s := range []triageSeed{set.fourModel, set.edit, set.accept} {
		assert.Equal(t, database.TriageOutcomeNotAudited, rows[s.item].Outcome)
		status, _ := e.itemStatus(s.item)
		assert.Equal(t, "open", status)
	}
	assert.Equal(t, before, selectionOf(t, e.db, set.edit.credit), "a step not audited is untouched")
	assert.Equal(t, []int16{int16(authortriage.StepPlaceholderOrg)}, batch.AppliedSteps)
	assert.Equal(t, 1, batch.ItemsTotal)
}

// The shipped gate: nothing is audited until the audit commit, so an apply
// is refused and the plan stays as it was.
func TestApplyRefusedWithNothingAudited(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	batch := e.planOnce(e.fastLoop())
	admin := NewAuthorReviewTriageAdmin(e.db)
	before := e.authorLayer()
	err := admin.Apply(e.ctx, batch.ID, 9)
	require.ErrorIs(t, err, database.ErrTriageNothingAudited)
	view, err := admin.Batch(e.ctx, batch.ID)
	require.NoError(t, err)
	assert.Equal(t, database.TriageBatchPlanned, view.Batch.Status)
	assert.Equal(t, database.TriageOutcomePlanned, e.rowsOf(batch.ID)[set.edit.item].Outcome)
	worked, err := e.fastLoop().RunOnce(e.ctx)
	require.NoError(t, err)
	assert.False(t, worked)
	assert.Equal(t, before, e.authorLayer())

	withAudited(t, authortriage.StepRules)
	assert.ErrorIs(t, admin.Apply(e.ctx, batch.ID+1, 9), database.ErrTriageBatchNotFound)
	e.exec(`UPDATE author_review_triage_batch SET status = 'superseded' WHERE id = ?`, batch.ID)
	assert.ErrorIs(t, admin.Apply(e.ctx, batch.ID, 9), database.ErrTriageBatchState)
}

func TestApplyRefusesAnotherRuleVersion(t *testing.T) {
	e := newTriageEnv(t)
	var id int64
	_, err := e.db.QueryOne(pg.Scan(&id), `INSERT INTO author_review_triage_batch (rule_version, status,
		created_by_user_id) VALUES ('triage-v0', 'planned', 7) RETURNING id`)
	require.NoError(t, err)
	withAudited(t, authortriage.StepRules)
	assert.ErrorIs(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, id, 9), database.ErrTriageRuleVersion)
}

func TestApplyWaitsForAnEntityBuild(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	withAudited(t, authortriage.StepRules)
	batch := e.planOnce(e.fastLoop())
	var build int64
	_, err := e.db.QueryOne(pg.Scan(&build), `INSERT INTO author_entity_build_run (status) VALUES ('pending')
		RETURNING id`)
	require.NoError(t, err)
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))

	loop := e.fastLoop()
	worked, err := loop.RunOnce(e.ctx)
	require.NoError(t, err)
	assert.False(t, worked, "the worker rests while a build runs")
	assert.Equal(t, database.TriageOutcomePlanned, e.rowsOf(batch.ID)[set.edit.item].Outcome)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_review_triage_batch
		WHERE id = ? AND lease_until > now()`, batch.ID), "it keeps its lease")

	e.exec(`UPDATE author_entity_build_run SET status = 'completed', finished_at = now() WHERE id = ?`, build)
	e.drain(loop, batch.ID)
	assert.Equal(t, database.TriageOutcomeApplied, e.rowsOf(batch.ID)[set.edit.item].Outcome)
}

func TestOneFailingItemDoesNotStopTheBatch(t *testing.T) {
	e := newTriageEnv(t)
	bad := e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	good := e.seedOpen(authornorm.ClassMononym, "last", "Аргус")
	row := func(s *triageSeed, action, planned string) database.TriagePlanItem {
		var proposal int64
		_, err := e.db.QueryOne(pg.Scan(&proposal), `SELECT proposal_result_id FROM contributor_review_item
			WHERE id = ?`, s.item)
		require.NoError(t, err)
		return database.TriagePlanItem{ReviewItemID: s.item, Fingerprint: s.fp, Class: "mononym", Credits: 1,
			ProposalResultID: &proposal, Step: 7, Rule: "keep_handle", Action: action,
			Planned: json.RawMessage(planned), Evidence: json.RawMessage(`{}`), Outcome: database.TriageOutcomePlanned}
	}
	id := e.storePlan([]database.TriagePlanItem{
		row(&bad, "edited", `{"display":"","kind":"person"}`),
		row(&good, "accepted", `{"display":"Аргус","kind":"person"}`),
	})
	withAudited(t, authortriage.StepKeepHandle)
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, id, 9))
	batch := e.drain(e.fastLoop(), id)
	assert.Equal(t, database.TriageBatchApplied, batch.Status)
	rows := e.rowsOf(id)
	assert.Equal(t, database.TriageOutcomeFailed, rows[bad.item].Outcome)
	assert.Equal(t, database.TriageOutcomeApplied, rows[good.item].Outcome)
	status, _ := e.itemStatus(bad.item)
	assert.Equal(t, "open", status, "the failed item's decision rolled back")
}

func TestApplyRecordsItsAudit(t *testing.T) {
	e := newTriageEnv(t)
	e.seedApplySet()
	withAudited(t, authortriage.StepRules, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))
	view, err := NewAuthorReviewTriageAdmin(e.db).Batch(e.ctx, batch.ID)
	require.NoError(t, err)
	want, err := json.Marshal(auditLogFn())
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(view.Batch.Audit))
	assert.Equal(t, database.TriageBatchApplying, view.Batch.Status)
	assert.Equal(t, 2, view.Batch.ItemsTotal, "the planned items of the audited steps")
	assert.Zero(t, view.Batch.ItemsDone)
	assert.JSONEq(t, string(batch.Counts), string(view.Batch.Counts), "the plan's own totals stay")
	_, err = NewAuthorReviewTriageAdmin(e.db).StartDryRun(e.ctx, 7)
	assert.ErrorIs(t, err, database.ErrTriageActiveBatch, "no dry run while a batch applies")
}

// A worker that lost its lease after one chunk: a second one finishes the
// batch, and an item the first one still holds in its chunk is never decided
// twice.
func TestApplyResumesAfterLeaseLoss(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	withAudited(t, authortriage.StepFourModel, authortriage.StepRules, authortriage.StepPlaceholderOrg,
		authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))

	first := e.loop(AuthorReviewTriageConfig{Chunk: 1, Pause: time.Millisecond})
	worked, err := first.RunOnce(e.ctx)
	require.NoError(t, err)
	require.True(t, worked)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_review_triage_item WHERE batch_id = ? AND outcome = ?`,
		batch.ID, database.TriageOutcomeApplied))
	stale, err := database.NextTriageApplyItems(e.ctx, e.db, batch.ID, 10)
	require.NoError(t, err)
	require.Len(t, stale, 3)

	e.exec(`UPDATE author_review_triage_batch SET lease_until = now() - interval '1 second' WHERE id = ?`, batch.ID)
	second := e.fastLoop()
	worked, err = second.RunOnce(e.ctx)
	require.NoError(t, err)
	require.True(t, worked)
	assert.ErrorIs(t, database.ProgressTriageBatch(e.ctx, e.db, batch.ID, first.owner, defaultTriageLease, 1),
		database.ErrTriageLeaseLost, "the first worker's progress is refused")
	done := e.drain(second, batch.ID)
	assert.Equal(t, database.TriageBatchApplied, done.Status)
	assert.Equal(t, 4, done.ItemsDone)

	view, err := database.ReadTriageBatch(e.ctx, e.db, batch.ID)
	require.NoError(t, err)
	for i := range stale {
		require.NoError(t, first.applyItem(e.ctx, &view.Batch, &stale[i]), "the first worker's stale chunk")
	}
	rows := e.rowsOf(batch.ID)
	for _, s := range []triageSeed{set.fourModel, set.edit, set.classify, set.accept} {
		assert.Equal(t, database.TriageOutcomeApplied, rows[s.item].Outcome)
		assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_manual_override WHERE scope_fingerprint = ?`,
			s.fp), "exactly one override per scope")
	}
	worked, err = first.RunOnce(e.ctx)
	require.NoError(t, err)
	assert.False(t, worked, "nothing is left to take")
}

func TestApplyPacesItsChunks(t *testing.T) {
	e := newTriageEnv(t)
	for _, name := range []string{"Зорро", "Аргус", "Кассиопея", "Мефистофель", "Гаргантюа", "Пантагрюэль"} {
		e.seedOpen(authornorm.ClassMononym, "last", name)
	}
	withAudited(t, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	require.Equal(t, 6, batch.ItemsTotal)
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))
	const pause = 300 * time.Millisecond
	start := time.Now()
	done := e.drain(e.loop(AuthorReviewTriageConfig{Chunk: 2, Pause: pause}), batch.ID)
	assert.GreaterOrEqual(t, time.Since(start), 2*pause, "three chunks, a pause after each")
	assert.Equal(t, 6, done.ItemsDone)
	var decided []time.Time
	_, err := e.db.Query(&decided, `SELECT decided_at FROM author_review_triage_item WHERE batch_id = ?
		ORDER BY step, id`, batch.ID)
	require.NoError(t, err)
	require.Len(t, decided, 6)
	for i := 1; i < len(decided); i++ {
		if i%2 == 0 {
			assert.GreaterOrEqual(t, decided[i].Sub(decided[i-1]), pause, "a pause between two chunks")
		}
	}
}

// The worker stops when its context ends, between two items, and leaves
// the rest planned for the next one.
func TestApplyStopsWhenCanceled(t *testing.T) {
	e := newTriageEnv(t)
	for _, name := range []string{"Зорро", "Аргус", "Кассиопея"} {
		e.seedOpen(authornorm.ClassMononym, "last", name)
	}
	withAudited(t, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))
	ctx, cancel := context.WithCancel(e.ctx)
	stopped := make(chan error, 1)
	go func() {
		stopped <- e.loop(AuthorReviewTriageConfig{Chunk: 1, Pause: time.Hour}).Run(ctx)
	}()
	require.Eventually(t, func() bool {
		return e.count(`SELECT count(*) FROM author_review_triage_item WHERE batch_id = ? AND outcome = 'applied'`,
			batch.ID) == 1
	}, 20*time.Second, 20*time.Millisecond)
	cancel()
	select {
	case err := <-stopped:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not stop")
	}
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_review_triage_item WHERE batch_id = ? AND outcome = ?`,
		batch.ID, database.TriageOutcomePlanned))
	worked, err := e.fastLoop().RunOnce(e.ctx)
	require.NoError(t, err)
	assert.False(t, worked, "the stopped worker's lease holds until it expires")
	e.exec(`UPDATE author_review_triage_batch SET lease_until = now() - interval '1 second' WHERE id = ?`, batch.ID)
	done := e.drain(e.fastLoop(), batch.ID)
	assert.Equal(t, database.TriageBatchApplied, done.Status, "another worker finishes it")
}

// undo asks for the undo of step (0: all) and runs it to the end.
func (e *triageEnv) undo(batchID int64, step int16) int {
	e.t.Helper()
	marked, err := NewAuthorReviewTriageAdmin(e.db).Undo(e.ctx, batchID, 11, step)
	require.NoError(e.t, err)
	done := e.drain(e.fastLoop(), batchID)
	require.Equal(e.t, database.TriageBatchApplied, done.Status)
	return marked
}

func TestUndoStepRestoresOnlyThatStep(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	batch := e.apply(authortriage.StepFourModel, authortriage.StepRules, authortriage.StepPlaceholderOrg,
		authortriage.StepKeepHandle)
	assert.Equal(t, 1, e.undo(batch.ID, int16(authortriage.StepRules)))

	rows := e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageUndoRestored, rows[set.edit.item].UndoOutcome)
	sel := selectionOf(t, e.db, set.edit.credit)
	assert.Equal(t, models.CreditSelectionReview, sel.State)
	var proposal int64
	_, err := e.db.QueryOne(pg.Scan(&proposal), `SELECT proposal_result_id FROM contributor_review_item WHERE id = ?`,
		set.edit.item)
	require.NoError(t, err)
	reopened := e.openItemsOf(&set.edit)
	require.Len(t, reopened, 1)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_review_item WHERE id = ? AND proposal_result_id = ?`,
		reopened[0], proposal), "the new item has the same proposal")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_manual_override WHERE id = ? AND revoked_by_user_id = 11`,
		rows[set.edit.item].OverrideID), "the actor is the admin who asked for the undo")

	for _, s := range []triageSeed{set.fourModel, set.classify, set.accept} {
		assert.Empty(t, rows[s.item].UndoOutcome)
		assert.Equal(t, models.CreditSelectionSelected, selectionOf(t, e.db, s.credit).State)
	}
}

func TestUndoWholeBatchThenDryRunSkipsThem(t *testing.T) {
	e := newTriageEnv(t)
	set := e.seedApplySet()
	batch := e.apply(authortriage.StepFourModel, authortriage.StepRules, authortriage.StepPlaceholderOrg,
		authortriage.StepKeepHandle)
	assert.Equal(t, 4, e.undo(batch.ID, 0))
	for _, s := range []triageSeed{set.fourModel, set.edit, set.classify, set.accept} {
		assert.Equal(t, database.TriageUndoRestored, e.rowsOf(batch.ID)[s.item].UndoOutcome)
		assert.Equal(t, models.CreditSelectionReview, selectionOf(t, e.db, s.credit).State)
	}
	_, err := NewAuthorReviewTriageAdmin(e.db).Undo(e.ctx, batch.ID, 11, 0)
	assert.ErrorIs(t, err, database.ErrTriageBatchState, "an undo done is not asked again")
	assert.Equal(t, 4, e.count(`SELECT count(*) FROM contributor_manual_override WHERE revoked_at IS NOT NULL`))

	next := e.planOnce(e.fastLoop())
	for _, s := range []triageSeed{set.fourModel, set.edit, set.classify, set.accept} {
		reopened := e.openItemsOf(&s)
		require.Len(t, reopened, 1)
		assert.Equal(t, string(authortriage.ExcludedUndoneBefore), e.rowsOf(next.ID)[reopened[0]].Exclusion)
	}
}

// seedMoved writes another book with the same source read under another
// extractor version: a current credit the proposal cannot be selected for.
func (e *triageEnv) seedMoved(s *triageSeed) int64 {
	e.t.Helper()
	e.book++
	book := e.book
	require.NoError(e.t, e.db.RunInTransaction(e.ctx, func(tx *pg.Tx) error {
		if _, err := tx.Exec(`INSERT INTO opds_catalog_book
			(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
			VALUES (?, 'moved.fb2', 'fixture.zip', 'fb2', now(), '', 'ru', 'fixture', '', ?)`, book, md5hex(book)); err != nil {
			return err
		}
		_, err := database.PersistExtraction(tx, &database.ExtractionInput{
			BookID: book, BookMD5: md5hex(book), ExtractorVersion: "extractor-v2",
			NormalizerVersion: authornorm.NormalizerVersion, Origin: models.BookMetadataSnapshotLive,
			Outcome: models.BookMetadataSnapshotExtracted, ArchivePath: "fixture.zip", EntryName: "moved.fb2",
			XMLProvenance: []byte(`{}`), SourceISBNs: []string{}, SourceSequences: []byte(`[]`), QualityFlags: []string{},
			Credits: []database.ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: s.source}},
		})
		return err
	}))
	return book
}

func TestBlockedUndoIsRetriedByTheNextRequest(t *testing.T) {
	e := newTriageEnv(t)
	blocked := e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	gone := e.seedOpen(authornorm.ClassMononym, "last", "Аргус")
	batch := e.apply(authortriage.StepKeepHandle)
	moved := e.seedMoved(&blocked)
	e.exec(`UPDATE contributor_manual_override SET revoked_at = now(), revoked_by_user_id = 7
		WHERE scope_fingerprint = ?`, gone.fp)

	assert.Equal(t, 2, e.undo(batch.ID, 0))
	rows := e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageUndoBlocked, rows[blocked.item].UndoOutcome)
	assert.Equal(t, database.TriageUndoGone, rows[gone.item].UndoOutcome)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_manual_override
		WHERE id = ? AND revoked_at IS NULL`, rows[blocked.item].OverrideID), "a blocked undo revokes nothing")

	e.exec(`SELECT author_layer_delete_books(?::bigint[])`, pg.Array([]int64{moved}))
	assert.Equal(t, 1, e.undo(batch.ID, 0), "the blocked item is asked again, the gone one is not")
	rows = e.rowsOf(batch.ID)
	assert.Equal(t, database.TriageUndoRestored, rows[blocked.item].UndoOutcome)
	assert.Equal(t, database.TriageUndoGone, rows[gone.item].UndoOutcome)
	assert.Equal(t, models.CreditSelectionReview, selectionOf(t, e.db, blocked.credit).State)
}

// The undo maps what a revoke with restore did: superseded only when every
// credit stayed on a newer override, restored otherwise.
func TestTriageUndoOutcomeMapping(t *testing.T) {
	assert.Equal(t, database.TriageUndoRestored, triageUndoOutcome(database.RestoreReport{Credits: 2, Reopened: true}))
	assert.Equal(t, database.TriageUndoRestored, triageUndoOutcome(database.RestoreReport{Credits: 2, Reopened: true,
		Superseded: true}), "some credit reopened: restored, superseded is a detail")
	assert.Equal(t, database.TriageUndoSuperseded, triageUndoOutcome(database.RestoreReport{Credits: 1,
		Superseded: true}))
	assert.Equal(t, database.TriageUndoRestored, triageUndoOutcome(database.RestoreReport{}),
		"a deleted book: restored with no credit")
}

// A newer admin override on the scope keeps its credit: the undo revokes
// only the batch's own override, and nothing reopens.
func TestUndoUnderANewerOverrideIsSuperseded(t *testing.T) {
	e := newTriageEnv(t)
	s := e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	batch := e.apply(authortriage.StepKeepHandle)
	_, err := reviewService(t, e.db, DefaultAuthorMetadataReviewConfig()).ApplyOverride(e.ctx,
		database.OverrideScope{Fingerprint: s.fp}, 7, &AdminCorrection{DisplayName: "Зорро Великий",
			SearchKey: "зорро великий", Script: "Cyrl", Kind: models.NormalizationPerson})
	require.NoError(t, err)
	e.undo(batch.ID, 0)
	assert.Equal(t, database.TriageUndoSuperseded, e.rowsOf(batch.ID)[s.item].UndoOutcome)
	assert.Equal(t, models.CreditSelectionSelected, selectionOf(t, e.db, s.credit).State)
	assert.Empty(t, e.openItemsOf(&s))
}

func TestUndoRefusals(t *testing.T) {
	e := newTriageEnv(t)
	e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	withAudited(t, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	admin := NewAuthorReviewTriageAdmin(e.db)
	_, err := admin.Undo(e.ctx, batch.ID, 11, 0)
	assert.ErrorIs(t, err, database.ErrTriageBatchState, "a plan not applied")
	_, err = admin.Undo(e.ctx, batch.ID, 11, 9)
	assert.ErrorIs(t, err, database.ErrTriageBatchState, "no step 9")
	_, err = admin.Undo(e.ctx, batch.ID+1, 11, 0)
	assert.True(t, errors.Is(err, database.ErrTriageBatchNotFound))
}

// An item whose credit another transaction holds fails on the worker's lock
// timeout, with its SQLSTATE logged, and the batch goes on.
func TestALockedItemFailsWithItsSQLState(t *testing.T) {
	e := newTriageEnv(t)
	locked := e.seedOpen(authornorm.ClassMononym, "last", "Зорро")
	free := e.seedOpen(authornorm.ClassMononym, "last", "Аргус")
	withAudited(t, authortriage.StepKeepHandle)
	batch := e.planOnce(e.fastLoop())
	require.NoError(t, NewAuthorReviewTriageAdmin(e.db).Apply(e.ctx, batch.ID, 9))
	holder, err := e.db.Begin()
	require.NoError(t, err)
	_, err = holder.Exec(`SELECT id FROM book_contributor_credit WHERE id = ? FOR UPDATE`, locked.credit)
	require.NoError(t, err)
	release := time.AfterFunc(5*time.Second, func() { _ = holder.Rollback() })
	t.Cleanup(func() { release.Stop(); _ = holder.Rollback() })

	e.drain(e.loop(AuthorReviewTriageConfig{Pause: time.Millisecond, LockTimeout: 200 * time.Millisecond}), batch.ID)
	assert.Equal(t, database.TriageOutcomeFailed, e.rowsOf(batch.ID)[locked.item].Outcome)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_review_triage_item WHERE review_item_id = ? AND sqlstate = ?`,
		locked.item, "55P03"))
	assert.Equal(t, database.TriageOutcomeApplied, e.rowsOf(batch.ID)[free.item].Outcome)
}
