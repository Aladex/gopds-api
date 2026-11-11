package services

// Task 4 of design/review-triage: the dry run plans every open review item
// once, from the stored answers and the build's facts, and writes nothing
// but the two triage tables; the plan's view and the audit sample.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/internal/authortriage"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// judgesOnly answers: both judges read the name as given and family, the
// production pair as family and given.
func judgesOnlyAnswers() []answer {
	return append(answersOf(personReply(llmres.RoleGiven, llmres.RoleFamily), "judge_a", "judge_b"),
		answersOf(personReply(llmres.RoleFamily, llmres.RoleGiven), "production_a", "production_b")...)
}

func fourModelAnswers() []answer {
	return answersOf(personReply(llmres.RoleGiven, llmres.RoleFamily), allSlots...)
}

func TestDryRunPlansEveryOpenItemOnce(t *testing.T) {
	e := newTriageEnv(t)
	fourModel := e.seedOpen(authornorm.ClassInitials, "first", "А.", "last", "Сидоров")
	e.storeAnswers(&fourModel, 1, fourModelAnswers()...)
	judgesOnly := e.seedOpen(authornorm.ClassInitials, "first", "Б.", "last", "Кузнецов")
	e.storeAnswers(&judgesOnly, 1, judgesOnlyAnswers()...)
	pinned := e.seedOpen(authornorm.ClassInitials, "first", "В.", "last", "Орлов")
	e.storeAnswers(&pinned, 1, fourModelAnswers()...)
	e.pinByName(&pinned)
	duplicate := e.seedOpen(authornorm.ClassDuplicateComponent, "first", "Иван", "first", "Пётр", "last", "Сидоров")
	e.storeAnswers(&duplicate, 1, fourModelAnswers()...)
	for _, family := range []string{"Иванов", "Петров", "Смирнов", "Козлов", "Волков"} {
		e.seedSelected("first", "Михаил", "last", family)
	}
	allCaps := e.seedOpen(authornorm.ClassCaseDefect, "first", "МИХАИЛ", "last", "АНДРЕЕВ")
	unreadable := e.seedOpen(authornorm.ClassInitials, "first", "Г.", "last", "Лебедев")

	before := e.authorLayer()
	batch := e.planOnce(e.loop(AuthorReviewTriageConfig{}))
	assert.Equal(t, 6, batch.ItemsTotal)
	assert.Equal(t, authortriage.RuleVersion, batch.RuleVersion)
	assert.Equal(t, before, e.authorLayer(), "a dry run writes nothing but the triage tables")

	rows := e.rowsOf(batch.ID)
	require.Len(t, rows, 6, "every open item once")
	assert.Equal(t, int16(1), rows[fourModel.item].Step)
	assert.Equal(t, database.TriageOutcomePlanned, rows[fourModel.item].Outcome)
	assert.Equal(t, int16(2), rows[judgesOnly.item].Step)
	assert.Equal(t, database.TriageOutcomeExcluded, rows[pinned.item].Outcome)
	assert.Equal(t, string(authortriage.ExcludedPinned), rows[pinned.item].Exclusion)
	assert.Equal(t, string(authortriage.ExcludedDuplicateComponent), rows[duplicate.item].Exclusion)
	assert.Equal(t, int16(3), rows[allCaps.item].Step)
	assert.Equal(t, string(authortriage.ActionEdit), rows[allCaps.item].Action)
	var after authortriage.Name
	require.NoError(t, json.Unmarshal([]byte(rows[allCaps.item].Planned), &after))
	assert.Equal(t, "Михаил Андреев", after.Display)
	assert.Equal(t, database.TriageOutcomeLeft, rows[unreadable.item].Outcome)
	assert.Zero(t, rows[unreadable.item].Step)

	var counts struct {
		Cells []struct {
			Step  int16  `json:"step"`
			Class string `json:"class"`
			Items int    `json:"items"`
		} `json:"cells"`
		Left     []database.TriageCount `json:"left"`
		Excluded []database.TriageCount `json:"excluded"`
	}
	require.NoError(t, json.Unmarshal(batch.Counts, &counts))
	assert.Len(t, counts.Cells, 3)
	assert.Equal(t, []database.TriageCount{{Key: "initials", Items: 1, Credits: 1}}, counts.Left)
	assert.Len(t, counts.Excluded, 2)

	again, err := e.loop(AuthorReviewTriageConfig{}).RunOnce(e.ctx)
	require.NoError(t, err)
	assert.False(t, again, "a planned batch is not planned twice")
}

// An item a batch of the same rule version applied and an undo restored is
// not planned again: the same rule would pick it again.
func TestDryRunExcludesItemsUndoneBefore(t *testing.T) {
	e := newTriageEnv(t)
	undone := e.seedOpen(authornorm.ClassInitials, "first", "А.", "last", "Сидоров")
	e.storeAnswers(&undone, 1, fourModelAnswers()...)
	otherVersion := e.seedOpen(authornorm.ClassInitials, "first", "Б.", "last", "Кузнецов")
	e.storeAnswers(&otherVersion, 1, fourModelAnswers()...)
	notRestored := e.seedOpen(authornorm.ClassInitials, "first", "В.", "last", "Орлов")
	e.storeAnswers(&notRestored, 1, fourModelAnswers()...)

	history := func(version string, s triageSeed, undo string) {
		var batch int64
		_, err := e.db.QueryOne(pg.Scan(&batch), `INSERT INTO author_review_triage_batch (rule_version, status,
			created_by_user_id) VALUES (?, 'applied', 7) RETURNING id`, version)
		require.NoError(t, err)
		e.exec(`INSERT INTO author_review_triage_item (batch_id, review_item_id, source_fingerprint, decision_class,
			credits, step, rule, action, outcome, revoked_at, revoked_by_user_id, undo_outcome)
			VALUES (?, ?, ?, 'initials', 1, 1, 'four_model', 'accepted', 'applied', now(), 7, ?)`,
			batch, s.item+1000, s.fp, undo)
	}
	history(authortriage.RuleVersion, undone, database.TriageUndoRestored)
	history("triage-v0", otherVersion, database.TriageUndoRestored)
	history(authortriage.RuleVersion, notRestored, database.TriageUndoSuperseded)

	batch := e.planOnce(e.loop(AuthorReviewTriageConfig{}))
	rows := e.rowsOf(batch.ID)
	assert.Equal(t, string(authortriage.ExcludedUndoneBefore), rows[undone.item].Exclusion)
	assert.Equal(t, int16(1), rows[otherVersion.item].Step, "an undo under another rule version")
	assert.Equal(t, int16(1), rows[notRestored.item].Step, "an undo that restored nothing")
}

// A judge reply that no longer validates is no consensus, even when both
// judges stored the same canonical answer.
func TestStoredAnswerThatNoLongerAssemblesIsNoConsensus(t *testing.T) {
	e := newTriageEnv(t)
	s := e.seedOpen(authornorm.ClassInitials, "first", "А.", "last", "Сидоров")
	broken := `{"form":"person","roles":[{"i":0,"role":"given"},{"i":7,"role":"family"}],"case_fix":[]}`
	canonical := sha256.Sum256([]byte(broken))
	var stale []answer
	for _, slot := range allSlots {
		stale = append(stale, answer{slot: slot, reply: *formReply(llmres.FormPerson), raw: broken,
			canonical: canonical[:]})
	}
	e.storeAnswers(&s, 1, stale...)

	batch := e.planOnce(e.loop(AuthorReviewTriageConfig{}))
	row := e.rowsOf(batch.ID)[s.item]
	assert.Equal(t, database.TriageOutcomeLeft, row.Outcome)
	assert.Zero(t, row.Step)
}

// Two runs whose judges agree on different answers are no consensus (the
// stricter rule of the plan): the item is left.
func TestJudgesThatDisagreeAcrossRunsLeaveTheItem(t *testing.T) {
	e := newTriageEnv(t)
	s := e.seedOpen(authornorm.ClassInitials, "first", "А.", "last", "Сидоров")
	e.storeAnswers(&s, 1, fourModelAnswers()...)
	e.storeAnswers(&s, 2, answersOf(personReply(llmres.RoleFamily, llmres.RoleGiven), allSlots...)...)

	batch := e.planOnce(e.loop(AuthorReviewTriageConfig{}))
	assert.Equal(t, database.TriageOutcomeLeft, e.rowsOf(batch.ID)[s.item].Outcome)
}

func TestNewDryRunSupersedesTheOldPlan(t *testing.T) {
	e := newTriageEnv(t)
	s := e.seedOpen(authornorm.ClassInitials, "first", "А.", "last", "Сидоров")
	e.storeAnswers(&s, 1, fourModelAnswers()...)
	loop := e.loop(AuthorReviewTriageConfig{})
	first := e.planOnce(loop)
	second := e.planOnce(loop)

	batches, err := NewAuthorReviewTriageAdmin(e.db).Batches(e.ctx)
	require.NoError(t, err)
	require.Len(t, batches, 2)
	assert.Equal(t, second.ID, batches[0].ID, "the newest first")
	assert.Equal(t, database.TriageBatchPlanned, batches[0].Status)
	assert.Equal(t, first.ID, batches[1].ID)
	assert.Equal(t, database.TriageBatchSuperseded, batches[1].Status)
	assert.Len(t, e.rowsOf(first.ID), 1, "the old plan's rows stay as they were")
}

func TestOnlyOneActiveBatch(t *testing.T) {
	e := newTriageEnv(t)
	admin := NewAuthorReviewTriageAdmin(e.db)
	_, err := admin.StartDryRun(e.ctx, 7)
	require.NoError(t, err)
	_, err = admin.StartDryRun(e.ctx, 7)
	require.ErrorIs(t, err, database.ErrTriageActiveBatch)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_review_triage_batch`))
}

// A planning that fails ends its batch as failed with a class, no text of
// what it read; the next dry run may start.
func TestFailedPlanningFailsTheBatch(t *testing.T) {
	e := newTriageEnv(t)
	admin := NewAuthorReviewTriageAdmin(e.db)
	batch, err := admin.StartDryRun(e.ctx, 7)
	require.NoError(t, err)
	holder, err := e.db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback() })
	_, err = holder.Exec(`LOCK TABLE book_contributor_credit IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)

	_, err = e.loop(AuthorReviewTriageConfig{PlanStatementTimeout: 200 * time.Millisecond}).RunOnce(e.ctx)
	require.Error(t, err)
	require.NoError(t, holder.Rollback())
	view, err := admin.Batch(e.ctx, batch.ID)
	require.NoError(t, err)
	assert.Equal(t, database.TriageBatchFailed, view.Batch.Status)
	assert.Equal(t, "plan_read 57014", view.Batch.Failure)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_review_triage_item`))
	_, err = admin.StartDryRun(e.ctx, 7)
	assert.NoError(t, err, "a failed batch holds nothing")
}

// plannedRows is a plan of n step items of class with credits 1..n.
func plannedRows(step int16, class string, n, firstID int) []database.TriagePlanItem {
	out := make([]database.TriagePlanItem, 0, n)
	for i := range n {
		fp := sha256.Sum256(fmt.Appendf(nil, "%s %d %d", class, step, i))
		out = append(out, database.TriagePlanItem{ReviewItemID: int64(firstID + i), Fingerprint: fp[:], Class: class,
			Credits: i + 1, Step: step, Rule: "rule", Action: string(authortriage.ActionAccept),
			Planned: json.RawMessage(fmt.Sprintf(`{"display":"name %d"}`, i)), Evidence: json.RawMessage(`{}`),
			Outcome: database.TriageOutcomePlanned})
	}
	return out
}

// storePlan stores rows as a planned batch, without the planner.
func (e *triageEnv) storePlan(rows []database.TriagePlanItem) int64 {
	e.t.Helper()
	batch, err := NewAuthorReviewTriageAdmin(e.db).StartDryRun(e.ctx, 7)
	require.NoError(e.t, err)
	owner := uuid.New()
	claimed, err := database.ClaimTriageBatch(e.ctx, e.db, owner, defaultTriageLease)
	require.NoError(e.t, err)
	require.Equal(e.t, batch.ID, claimed.ID)
	require.NoError(e.t, database.StoreTriagePlan(e.ctx, e.db, batch.ID, owner, rows, json.RawMessage(`{}`)))
	return batch.ID
}

func TestBatchViewHasTenExamplesPerCell(t *testing.T) {
	e := newTriageEnv(t)
	rows := plannedRows(1, "mononym", 12, 1)
	fp := rows[0].Fingerprint
	rows = append(rows, plannedRows(3, "case_defect", 2, 100)...)
	rows = append(rows,
		database.TriagePlanItem{ReviewItemID: 200, Fingerprint: fp, Class: "affix", Credits: 4,
			Outcome: database.TriageOutcomeLeft},
		database.TriagePlanItem{ReviewItemID: 201, Fingerprint: fp, Class: "affix", Credits: 2,
			Outcome: database.TriageOutcomeExcluded, Exclusion: "pinned"})
	id := e.storePlan(rows)

	view, err := NewAuthorReviewTriageAdmin(e.db).Batch(e.ctx, id)
	require.NoError(t, err)
	require.Len(t, view.Cells, 2)
	cell := view.Cells[0]
	assert.Equal(t, int16(1), cell.Step)
	assert.Equal(t, 12, cell.Items)
	assert.Equal(t, 78, cell.Credits)
	require.Len(t, cell.Examples, 10)
	for i, ex := range cell.Examples {
		assert.Equal(t, 12-i, ex.Credits, "heaviest first")
	}
	assert.JSONEq(t, `{"display":"name 11"}`, string(cell.Examples[0].After))
	assert.Len(t, view.Cells[1].Examples, 2)
	assert.Equal(t, []database.TriageCount{{Key: "affix", Items: 1, Credits: 4}}, view.Left)
	assert.Equal(t, []database.TriageCount{{Key: "pinned", Items: 1, Credits: 2}}, view.Excluded)
	assert.Equal(t, []database.TriageOutcomeCount{{Step: 1, Outcome: "planned", Items: 12},
		{Step: 3, Outcome: "planned", Items: 2}}, view.Outcomes)

	_, err = NewAuthorReviewTriageAdmin(e.db).Batch(e.ctx, id+1)
	assert.ErrorIs(t, err, database.ErrTriageBatchNotFound)
}

func TestAuditSampleIsStratifiedAndStable(t *testing.T) {
	e := newTriageEnv(t)
	rows := plannedRows(1, "mononym", 300, 1)
	rows = append(rows, plannedRows(1, "particle", 100, 1000)...)
	rows = append(rows, plannedRows(3, "case_defect", 20, 2000)...)
	id := e.storePlan(rows)
	admin := NewAuthorReviewTriageAdmin(e.db)

	sample, err := admin.AuditSample(e.ctx, id)
	require.NoError(t, err)
	tally := map[string]int{}
	seen := map[int64]bool{}
	for i := range sample {
		r := &sample[i]
		tally[fmt.Sprintf("%d %s %s", r.Step, r.Class, r.Pool)]++
		assert.False(t, seen[r.ReviewItemID], "item %d twice", r.ReviewItemID)
		seen[r.ReviewItemID] = true
	}
	assert.Equal(t, map[string]int{
		"1 mononym sample": 225, "1 particle sample": 75, "1 mononym reserve": 45, "1 particle reserve": 15,
		"3 case_defect sample": 20,
	}, tally)

	again, err := admin.AuditSample(e.ctx, id)
	require.NoError(t, err)
	require.Len(t, again, len(sample))
	for i := range sample {
		assert.Equal(t, sample[i].ReviewItemID, again[i].ReviewItemID, "the same order at every call")
	}
	assert.NotEqual(t, int64(1), sample[0].ReviewItemID, "the batch's own random order, not the item order")
	assert.Equal(t, []string{"display=name 0"}, findAudit(sample, 1).After)
}

func findAudit(rows []database.TriageAuditRow, item int64) database.TriageAuditRow {
	for i := range rows {
		if rows[i].ReviewItemID == item {
			return rows[i]
		}
	}
	return database.TriageAuditRow{}
}

// An item that alone carries at least a hundredth of its step's credits is
// always in that step's sample: a heavy placeholder is never left to chance.
func TestAuditSampleAlwaysTakesTheHeavyItems(t *testing.T) {
	e := newTriageEnv(t)
	rows := plannedRows(5, "mononym", 400, 1)
	light := plannedRows(5, "case_defect", 100, 1000)
	for i := range rows {
		rows[i].Credits = 1
	}
	heavy := map[int64]bool{}
	for i := range light {
		light[i].Credits = 1
		if i%40 == 7 {
			light[i].Credits = 300
			heavy[light[i].ReviewItemID] = true
		}
	}
	require.Len(t, heavy, 3)
	id := e.storePlan(append(rows, light...))

	sample, err := NewAuthorReviewTriageAdmin(e.db).AuditSample(e.ctx, id)
	require.NoError(t, err)
	pools := map[string]int{}
	for i := range sample {
		pools[sample[i].Pool]++
		if heavy[sample[i].ReviewItemID] {
			assert.Equal(t, TriagePoolSample, sample[i].Pool)
			delete(heavy, sample[i].ReviewItemID)
		}
	}
	assert.Empty(t, heavy, "every heavy item is in the sample")
	assert.Equal(t, map[string]int{TriagePoolSample: 50, TriagePoolReserve: 10}, pools)
}

// A worker whose lease another took does not store its plan: the batch is
// the new owner's.
func TestPlanOfALostLeaseIsNotStored(t *testing.T) {
	e := newTriageEnv(t)
	batch, err := NewAuthorReviewTriageAdmin(e.db).StartDryRun(e.ctx, 7)
	require.NoError(t, err)
	first, second := uuid.New(), uuid.New()
	claimed, err := database.ClaimTriageBatch(e.ctx, e.db, first, defaultTriageLease)
	require.NoError(t, err)
	require.Equal(t, batch.ID, claimed.ID)
	again, err := database.ClaimTriageBatch(e.ctx, e.db, second, defaultTriageLease)
	require.NoError(t, err)
	assert.Nil(t, again, "a live lease is not taken")
	e.exec(`UPDATE author_review_triage_batch SET lease_until = now() - interval '1 second' WHERE id = ?`, batch.ID)
	taken, err := database.ClaimTriageBatch(e.ctx, e.db, second, defaultTriageLease)
	require.NoError(t, err)
	require.NotNil(t, taken, "an expired lease is taken over")

	err = database.StoreTriagePlan(e.ctx, e.db, batch.ID, first, plannedRows(1, "mononym", 3, 1), json.RawMessage(`{}`))
	require.ErrorIs(t, err, database.ErrTriageLeaseLost)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_review_triage_item`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_review_triage_batch WHERE status = ?`, database.TriageBatchPlanning))
	require.NoError(t, database.StoreTriagePlan(e.ctx, e.db, batch.ID, second, plannedRows(1, "mononym", 3, 1),
		json.RawMessage(`{}`)))
}

// A step-1 row of the audit sample names the confirming pair beside the
// judges: the run, arm and batch size where the four-model proof is.
func TestAuditSampleNamesTheConfirmingPair(t *testing.T) {
	e := newTriageEnv(t)
	judgesOnly := e.seedOpen(authornorm.ClassInitials, "first", "Б.", "last", "Кузнецов")
	e.storeAnswers(&judgesOnly, 1, judgesOnlyAnswers()...)
	e.storeAnswers(&judgesOnly, 2, fourModelAnswers()...)
	batch := e.planOnce(e.loop(AuthorReviewTriageConfig{}))
	require.Equal(t, int16(1), e.rowsOf(batch.ID)[judgesOnly.item].Step)

	sample, err := NewAuthorReviewTriageAdmin(e.db).AuditSample(e.ctx, batch.ID)
	require.NoError(t, err)
	row := findAudit(sample, judgesOnly.item)
	require.Equal(t, int16(1), row.Step)
	var ev struct {
		RunID         int64  `json:"run_id"`
		Arm           string `json:"arm"`
		BatchSize     int    `json:"batch_size"`
		PairRunID     int64  `json:"pair_run_id"`
		PairArm       string `json:"pair_arm"`
		PairBatchSize int    `json:"pair_batch_size"`
	}
	require.NoError(t, json.Unmarshal(row.Evidence, &ev))
	assert.Equal(t, e.llmRun(1), ev.RunID, "the lowest run whose judges agree")
	assert.Equal(t, e.llmRun(2), ev.PairRunID, "the run whose pair confirmed it")
	assert.NotEmpty(t, ev.PairArm)
	assert.Positive(t, ev.PairBatchSize)
}
