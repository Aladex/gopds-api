package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The review triage's two tables (migration 84, design/review-triage): a
// batch is one dry run of a rule version and its life, an item is one open
// review item as that dry run saw it, with the plan for it and later the
// outcome of the apply and of an undo. A dry run writes these tables and
// nothing else.

var (
	ErrTriageActiveBatch = errors.New("database: a triage batch is already active")
	ErrTriageBatchState  = errors.New("database: the triage batch is not in a state that allows this")
	// ErrTriageLeaseLost: another worker holds the batch now.
	ErrTriageLeaseLost = errors.New("database: the triage batch's lease is held by another worker")
)

// Triage batch statuses.
const (
	TriageBatchPlanning   = "planning"
	TriageBatchPlanned    = "planned"
	TriageBatchApplying   = "applying"
	TriageBatchApplied    = "applied"
	TriageBatchUndoing    = "undoing"
	TriageBatchFailed     = "failed"
	TriageBatchSuperseded = "superseded"
)

// Triage item outcomes.
const (
	TriageOutcomePlanned        = "planned"
	TriageOutcomeLeft           = "left"
	TriageOutcomeExcluded       = "excluded"
	TriageOutcomeNotAudited     = "not_audited"
	TriageOutcomeApplied        = "applied"
	TriageOutcomeSkippedChanged = "skipped_changed"
	TriageOutcomeConflict       = "conflict"
	TriageOutcomeFailed         = "failed"
)

// Triage undo outcomes.
const (
	TriageUndoRestored   = "restored"
	TriageUndoSuperseded = "superseded"
	TriageUndoBlocked    = "blocked"
	TriageUndoGone       = "gone"
	TriageUndoFailed     = "failed"
)

// TriageBatch is one dry run and its life.
type TriageBatch struct {
	ID                     int64           `pg:"id" json:"id"`
	RuleVersion            string          `pg:"rule_version" json:"rule_version"`
	Status                 string          `pg:"status" json:"status"`
	CreatedByUserID        int64           `pg:"created_by_user_id" json:"created_by_user_id"`
	CreatedAt              time.Time       `pg:"created_at" json:"created_at"`
	PlannedAt              *time.Time      `pg:"planned_at" json:"planned_at,omitempty"`
	ApplyRequestedAt       *time.Time      `pg:"apply_requested_at" json:"apply_requested_at,omitempty"`
	UndoRequestedAt        *time.Time      `pg:"undo_requested_at" json:"undo_requested_at,omitempty"`
	FinishedAt             *time.Time      `pg:"finished_at" json:"finished_at,omitempty"`
	ApplyRequestedByUserID *int64          `pg:"apply_requested_by_user_id" json:"apply_requested_by_user_id,omitempty"`
	UndoRequestedByUserID  *int64          `pg:"undo_requested_by_user_id" json:"undo_requested_by_user_id,omitempty"`
	AppliedSteps           []int16         `pg:"applied_steps,array" json:"applied_steps"`
	Audit                  json.RawMessage `pg:"audit,type:jsonb" json:"audit,omitempty" swaggertype:"object"`
	Counts                 json.RawMessage `pg:"counts,type:jsonb" json:"counts,omitempty" swaggertype:"object"`
	ItemsTotal             int             `pg:"items_total,use_zero" json:"items_total"`
	ItemsDone              int             `pg:"items_done,use_zero" json:"items_done"`
	Failure                string          `pg:"failure" json:"failure,omitempty"`
}

const triageBatchColumns = `id, rule_version, status, created_by_user_id, created_at, planned_at,
	apply_requested_at, undo_requested_at, finished_at, apply_requested_by_user_id, undo_requested_by_user_id,
	applied_steps, audit, counts, items_total, items_done, coalesce(failure, '') AS failure`

// TriagePlanItem is one open review item with the plan a dry run made for
// it. Step 0 is no step: the item is left or excluded.
type TriagePlanItem struct {
	ReviewItemID     int64
	Fingerprint      []byte
	Class            string
	Credits          int
	ProposalResultID *int64
	Step             int16
	Rule, Action     string
	Planned          json.RawMessage
	Evidence         json.RawMessage
	Exclusion        string
	Outcome          string
}

// triageOneActiveIndex keeps one batch active at a time.
const triageOneActiveIndex = "author_review_triage_batch_one_active"

// uniqueViolationOf reports a unique violation of the named index.
func uniqueViolationOf(err error, index string) bool {
	var pgErr pg.Error
	return errors.As(err, &pgErr) && pgErr.Field('C') == sqlstateUniqueViolation && pgErr.Field('n') == index
}

// triageActiveStatuses are the statuses the worker takes a batch in.
const triageActiveStatuses = `('planning', 'applying', 'undoing')`

// StartTriageBatch records a new dry run of ruleVersion by actor; the worker
// plans it. ErrTriageActiveBatch: a batch is planning, applying or undoing.
func StartTriageBatch(ctx context.Context, db pg.DBI, ruleVersion string, actor int64) (*TriageBatch, error) {
	if actor <= 0 || strings.TrimSpace(ruleVersion) == "" {
		return nil, ErrTriageBatchState
	}
	var batches []TriageBatch
	_, err := db.QueryContext(ctx, &batches, `
		INSERT INTO public.author_review_triage_batch (rule_version, created_by_user_id)
		SELECT ?0, ?1 WHERE NOT EXISTS (
			SELECT 1 FROM public.author_review_triage_batch WHERE status IN `+triageActiveStatuses+`)
		RETURNING `+triageBatchColumns, ruleVersion, actor)
	switch {
	case uniqueViolationOf(err, triageOneActiveIndex):
		return nil, ErrTriageActiveBatch
	case err != nil:
		return nil, fmt.Errorf("starting a triage batch: %w", err)
	case len(batches) == 0:
		return nil, ErrTriageActiveBatch
	}
	return &batches[0], nil
}

// ClaimTriageBatch takes the active batch for owner: one whose lease expired
// or is already owner's. Nil when there is none to take.
func ClaimTriageBatch(ctx context.Context, db pg.DBI, owner uuid.UUID, lease time.Duration) (*TriageBatch, error) {
	var batches []TriageBatch
	_, err := db.QueryContext(ctx, &batches, `
		UPDATE public.author_review_triage_batch
		SET lease_owner = ?0, lease_until = now() + ?1 * interval '1 millisecond'
		WHERE id = (
			SELECT id FROM public.author_review_triage_batch
			WHERE status IN `+triageActiveStatuses+`
				AND (lease_owner IS NULL OR lease_until < now() OR lease_owner = ?0)
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+triageBatchColumns, owner, lease.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("claiming the triage batch: %w", err)
	}
	if len(batches) == 0 {
		return nil, nil
	}
	return &batches[0], nil
}

// RenewTriageLease extends owner's lease on the batch to lease from now.
func RenewTriageLease(ctx context.Context, db pg.DBI, batchID int64, owner uuid.UUID, lease time.Duration) error {
	res, err := db.ExecContext(ctx, `
		UPDATE public.author_review_triage_batch SET lease_until = now() + ? * interval '1 millisecond'
		WHERE id = ? AND lease_owner = ? AND status IN `+triageActiveStatuses, lease.Milliseconds(), batchID, owner)
	if err != nil {
		return fmt.Errorf("renewing the triage lease: %w", err)
	}
	if res.RowsAffected() != 1 {
		return ErrTriageLeaseLost
	}
	return nil
}

// triagePlanInsertRows is how many plan rows one INSERT writes.
const triagePlanInsertRows = 1000

// StoreTriagePlan writes the plan of owner's planning batch in one
// transaction: every item, the counts, status planned. Every older planned
// batch is superseded: its plan is stale.
func StoreTriagePlan(
	ctx context.Context, db *pg.DB, batchID int64, owner uuid.UUID, items []TriagePlanItem, counts json.RawMessage,
) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := holdTriageBatch(ctx, tx, batchID, owner, TriageBatchPlanning); err != nil {
			return err
		}
		for from := 0; from < len(items); from += triagePlanInsertRows {
			if err := insertTriagePlanRows(ctx, tx, batchID, items[from:min(from+triagePlanInsertRows, len(items))]); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE public.author_review_triage_batch SET status = 'superseded'
			WHERE status = 'planned' AND id <> ?`, batchID); err != nil {
			return fmt.Errorf("superseding the older plans: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE public.author_review_triage_batch
			SET status = 'planned', planned_at = now(), items_total = ?, counts = ?::jsonb,
				lease_owner = NULL, lease_until = NULL
			WHERE id = ?`, len(items), string(counts), batchID)
		if err != nil {
			return fmt.Errorf("finishing the plan: %w", err)
		}
		return nil
	})
}

// holdTriageBatch locks owner's batch in status, or says why it cannot.
func holdTriageBatch(ctx context.Context, tx pg.DBI, batchID int64, owner uuid.UUID, status string) error {
	var held []struct {
		Status string    `pg:"status"`
		Owner  uuid.UUID `pg:"lease_owner,type:uuid"`
	}
	if _, err := tx.QueryContext(ctx, &held, `
		SELECT status, lease_owner FROM public.author_review_triage_batch WHERE id = ? FOR UPDATE`, batchID); err != nil {
		return fmt.Errorf("locking the triage batch: %w", err)
	}
	switch {
	case len(held) == 0 || held[0].Status != status:
		return ErrTriageBatchState
	case held[0].Owner != owner:
		return ErrTriageLeaseLost
	}
	return nil
}

// triagePlanColumns is how many columns one plan row writes.
const triagePlanColumns = 13

func insertTriagePlanRows(ctx context.Context, tx pg.DBI, batchID int64, rows []TriagePlanItem) error {
	var b strings.Builder
	b.WriteString(`INSERT INTO public.author_review_triage_item (batch_id, review_item_id, source_fingerprint,
		decision_class, credits, proposal_result_id, step, rule, action, planned, evidence, exclusion, outcome) VALUES `)
	params := make([]interface{}, 0, len(rows)*triagePlanColumns)
	for i := range rows {
		r := &rows[i]
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?)")
		var step interface{}
		if r.Step != 0 {
			step = r.Step
		}
		params = append(params, batchID, r.ReviewItemID, r.Fingerprint, r.Class, r.Credits, r.ProposalResultID,
			step, nullable(r.Rule), nullable(r.Action), rawJSONOrNil(r.Planned), rawJSONOrNil(r.Evidence),
			nullable(r.Exclusion), r.Outcome)
	}
	if _, err := tx.ExecContext(ctx, b.String(), params...); err != nil {
		return fmt.Errorf("writing the plan rows: %w", err)
	}
	return nil
}

func rawJSONOrNil(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// FailTriageBatch ends owner's active batch as failed. failure is a class,
// never a name or an error text.
func FailTriageBatch(ctx context.Context, db pg.DBI, batchID int64, owner uuid.UUID, failure string) error {
	res, err := db.ExecContext(ctx, `
		UPDATE public.author_review_triage_batch
		SET status = 'failed', failure = ?, finished_at = now(), lease_owner = NULL, lease_until = NULL
		WHERE id = ? AND lease_owner = ? AND status IN `+triageActiveStatuses, failure, batchID, owner)
	if err != nil {
		return fmt.Errorf("failing the triage batch: %w", err)
	}
	if res.RowsAffected() != 1 {
		return ErrTriageLeaseLost
	}
	return nil
}

// ListTriageBatches lists the newest batches first.
func ListTriageBatches(ctx context.Context, db pg.DBI, limit int) ([]TriageBatch, error) {
	batches := []TriageBatch{}
	if _, err := db.QueryContext(ctx, &batches, `SELECT `+triageBatchColumns+`
		FROM public.author_review_triage_batch ORDER BY id DESC LIMIT ?`, limit); err != nil {
		return nil, fmt.Errorf("listing the triage batches: %w", err)
	}
	return batches, nil
}

// readTriageBatchRow reads one batch; ErrTriageBatchState when there is none.
func readTriageBatchRow(ctx context.Context, db pg.DBI, batchID int64) (*TriageBatch, error) {
	var batches []TriageBatch
	if _, err := db.QueryContext(ctx, &batches, `SELECT `+triageBatchColumns+`
		FROM public.author_review_triage_batch WHERE id = ?`, batchID); err != nil {
		return nil, fmt.Errorf("reading the triage batch: %w", err)
	}
	if len(batches) == 0 {
		return nil, ErrTriageBatchNotFound
	}
	return &batches[0], nil
}

// ErrTriageBatchNotFound: no batch has the ID.
var ErrTriageBatchNotFound = errors.New("database: the triage batch does not exist")

// TriageCell is one (step, class) cell of a plan with its heaviest examples.
type TriageCell struct {
	Step     int16           `pg:"step" json:"step"`
	Class    string          `pg:"class" json:"class"`
	Items    int             `pg:"items" json:"items"`
	Credits  int             `pg:"credits" json:"credits"`
	Examples []TriageExample `pg:"-" json:"examples"`
}

// TriageExample is one item of a cell: the proposal before, the plan after.
type TriageExample struct {
	ReviewItemID int64           `pg:"review_item_id" json:"review_item_id"`
	Credits      int             `pg:"credits" json:"credits"`
	Action       string          `pg:"action" json:"action"`
	Rule         string          `pg:"rule" json:"rule"`
	Before       json.RawMessage `pg:"before,type:jsonb" json:"before" swaggertype:"object"`
	After        json.RawMessage `pg:"after,type:jsonb" json:"after" swaggertype:"object"`
}

// TriageBatchView is a batch with its plan's table and its outcomes.
type TriageBatchView struct {
	Batch    TriageBatch          `json:"batch"`
	Cells    []TriageCell         `json:"cells"`
	Left     []TriageCount        `json:"left"`
	Excluded []TriageCount        `json:"excluded"`
	Outcomes []TriageOutcomeCount `json:"outcomes"`
}

// TriageCount is the items and credits of one key: a class or a reason.
type TriageCount struct {
	Key     string `pg:"key" json:"key"`
	Items   int    `pg:"items" json:"items"`
	Credits int    `pg:"credits" json:"credits"`
}

// TriageOutcomeCount is how many items of a step ended in an outcome.
type TriageOutcomeCount struct {
	Step        int16  `pg:"step" json:"step"`
	Outcome     string `pg:"outcome" json:"outcome"`
	UndoOutcome string `pg:"undo_outcome" json:"undo_outcome,omitempty"`
	Items       int    `pg:"items" json:"items"`
}

// triageExamplesPerCell is how many examples a cell shows.
const triageExamplesPerCell = 10

// triageNameJSON renders a result's nine name fields as authortriage.Name.
const triageNameJSON = `jsonb_build_object('given', coalesce(p.given_name, ''),
	'additional', coalesce(array_to_string(p.additional_names, ' '), ''), 'family', coalesce(p.family_name, ''),
	'nickname', coalesce(p.nickname, ''), 'prefix', coalesce(p.prefix, ''), 'suffix', coalesce(p.suffix, ''),
	'display', coalesce(p.display_name, ''), 'sort', coalesce(p.sort_name, ''), 'kind', p.kind)`

// ReadTriageBatch reads a batch with the counts of its plan per (step,
// class), the ten heaviest items of each cell, what it left and excluded,
// and the outcomes per step.
func ReadTriageBatch(ctx context.Context, db pg.DBI, batchID int64) (*TriageBatchView, error) {
	batch, err := readTriageBatchRow(ctx, db, batchID)
	if err != nil {
		return nil, err
	}
	view := &TriageBatchView{Batch: *batch, Cells: []TriageCell{}, Left: []TriageCount{}, Excluded: []TriageCount{},
		Outcomes: []TriageOutcomeCount{}}
	if _, err = db.QueryContext(ctx, &view.Cells, `
		SELECT step, decision_class AS class, count(*) AS items, sum(credits) AS credits
		FROM public.author_review_triage_item WHERE batch_id = ? AND step IS NOT NULL
		GROUP BY step, decision_class ORDER BY step, decision_class`, batchID); err != nil {
		return nil, fmt.Errorf("counting the plan's cells: %w", err)
	}
	if exErr := attachTriageExamples(ctx, db, batchID, view.Cells); exErr != nil {
		return nil, exErr
	}
	if _, err = db.QueryContext(ctx, &view.Left, `
		SELECT decision_class AS key, count(*) AS items, sum(credits) AS credits
		FROM public.author_review_triage_item WHERE batch_id = ? AND outcome = 'left'
		GROUP BY decision_class ORDER BY count(*) DESC, decision_class`, batchID); err != nil {
		return nil, fmt.Errorf("counting what the plan left: %w", err)
	}
	if _, err = db.QueryContext(ctx, &view.Excluded, `
		SELECT exclusion AS key, count(*) AS items, sum(credits) AS credits
		FROM public.author_review_triage_item WHERE batch_id = ? AND outcome = 'excluded'
		GROUP BY exclusion ORDER BY count(*) DESC, exclusion`, batchID); err != nil {
		return nil, fmt.Errorf("counting what the plan excluded: %w", err)
	}
	if _, err = db.QueryContext(ctx, &view.Outcomes, `
		SELECT step, outcome, coalesce(undo_outcome, '') AS undo_outcome, count(*) AS items
		FROM public.author_review_triage_item WHERE batch_id = ? AND step IS NOT NULL
		GROUP BY step, outcome, undo_outcome ORDER BY step, outcome, undo_outcome`, batchID); err != nil {
		return nil, fmt.Errorf("counting the outcomes: %w", err)
	}
	return view, nil
}

// attachTriageExamples gives each cell its triageExamplesPerCell heaviest
// items, ties by row ID.
func attachTriageExamples(ctx context.Context, db pg.DBI, batchID int64, cells []TriageCell) error {
	var examples []struct {
		TriageExample
		Step  int16  `pg:"step"`
		Class string `pg:"class"`
	}
	if _, err := db.QueryContext(ctx, &examples, `
		SELECT t.step, t.decision_class AS class, t.review_item_id, t.credits, t.action, t.rule,
			`+triageNameJSON+` AS before, t.planned AS after
		FROM (SELECT x.*, row_number() OVER (PARTITION BY x.step, x.decision_class
				ORDER BY x.credits DESC, x.id) AS n
			FROM public.author_review_triage_item x WHERE x.batch_id = ? AND x.step IS NOT NULL) t
		LEFT JOIN public.contributor_normalization_result p ON p.id = t.proposal_result_id
		WHERE t.n <= ?
		ORDER BY t.step, t.decision_class, t.n`, batchID, triageExamplesPerCell); err != nil {
		return fmt.Errorf("reading the plan's examples: %w", err)
	}
	type cellKey struct {
		step  int16
		class string
	}
	cell := map[cellKey]int{}
	for i := range cells {
		cell[cellKey{cells[i].Step, cells[i].Class}] = i
		cells[i].Examples = []TriageExample{}
	}
	for i := range examples {
		if at, ok := cell[cellKey{examples[i].Step, examples[i].Class}]; ok {
			cells[at].Examples = append(cells[at].Examples, examples[i].TriageExample)
		}
	}
	return nil
}

// TriageAuditRow is one planned item as the auditors label it. Pool is set by
// the service: sample or reserve.
type TriageAuditRow struct {
	ReviewItemID int64           `pg:"review_item_id"`
	Pool         string          `pg:"-"`
	Step         int16           `pg:"step"`
	Rule         string          `pg:"rule"`
	Class        string          `pg:"class"`
	Credits      int             `pg:"credits"`
	Source       []string        `pg:"source,array"`
	Before       []string        `pg:"before,array"`
	After        []string        `pg:"after,array"`
	Action       string          `pg:"action"`
	Evidence     json.RawMessage `pg:"evidence,type:jsonb"`
}

// triageNameKeys are the JSON keys of authortriage.Name, in its order.
var triageNameKeys = strings.Fields("given additional family nickname prefix suffix display sort kind")

// triageNameFields lists a name's fields as "field=value", the empty ones
// left out, in the order of authortriage.Name.
func triageNameFields(name string) string {
	parts := make([]string, 0, len(triageNameKeys))
	for _, f := range triageNameKeys {
		parts = append(parts, fmt.Sprintf(`CASE WHEN coalesce(%[1]s ->> '%[2]s', '') <> ''
			THEN '%[2]s=' || (%[1]s ->> '%[2]s') END`, name, f))
	}
	return "array_remove(ARRAY[" + strings.Join(parts, ", ") + "], NULL)"
}

// TriageAuditCandidates lists every item of the batch with a step in the
// batch's own random order (md5 of batch and row ID), with the fingerprint's
// source as its lowest-ID current author credit reads it.
func TriageAuditCandidates(ctx context.Context, db pg.DBI, batchID int64) ([]TriageAuditRow, error) {
	rows := []TriageAuditRow{}
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT t.review_item_id, t.step, t.rule, t.decision_class AS class, t.credits, t.action, t.evidence,
			ARRAY[coalesce(src.source_first_name, ''), coalesce(src.source_middle_name, ''),
				coalesce(src.source_last_name, ''), coalesce(src.source_nickname, ''),
				coalesce(src.source_display_name, '')] AS source,
			`+triageNameFields("bp.before")+` AS before,
			`+triageNameFields("t.planned")+` AS after
		FROM public.author_review_triage_item t
		LEFT JOIN public.contributor_normalization_result p ON p.id = t.proposal_result_id
		CROSS JOIN LATERAL (SELECT `+triageNameJSON+` AS before) bp
		LEFT JOIN LATERAL (
			SELECT c.source_first_name, c.source_middle_name, c.source_last_name, c.source_nickname,
				c.source_display_name
			FROM public.book_contributor_credit c
			JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			WHERE c.source_fingerprint = t.source_fingerprint AND c.role = 'author'
			ORDER BY c.id LIMIT 1) src ON true
		WHERE t.batch_id = ?0 AND t.step IS NOT NULL
		ORDER BY md5(?0::text || ':' || t.id::text)`, batchID); err != nil {
		return nil, fmt.Errorf("reading the audit candidates: %w", err)
	}
	return rows, nil
}

// The apply and the undo of a plan (Task 5 of design/review-triage). An
// apply decides the planned items of the audited steps one transaction each,
// through the review service; an undo revokes the overrides a batch wrote.
// Both go on in the background under the batch's lease; every item records
// its own outcome, so a worker that stops leaves exactly what is left.

var (
	ErrTriageNothingAudited = errors.New("database: no step of the rule version passed its audit")
	ErrTriageRuleVersion    = errors.New("database: the plan was made under another rule version")
	// ErrTriageItemTaken: the item's outcome was written by another worker.
	ErrTriageItemTaken = errors.New("database: the triage item was already handled")
)

// lockTriageBatch locks a batch for an administrator's request.
func lockTriageBatch(ctx context.Context, tx pg.DBI, batchID int64) (*TriageBatch, error) {
	var batches []TriageBatch
	if _, err := tx.QueryContext(ctx, &batches, `SELECT `+triageBatchColumns+`
		FROM public.author_review_triage_batch WHERE id = ? FOR UPDATE`, batchID); err != nil {
		return nil, fmt.Errorf("locking the triage batch: %w", err)
	}
	if len(batches) == 0 {
		return nil, ErrTriageBatchNotFound
	}
	return &batches[0], nil
}

// RequestTriageApply hands a planned batch to the worker to apply the items
// of steps, the steps that passed the audit recorded in audit. The planned
// items of other steps become not_audited. Progress counts what is left to
// decide; the plan's own totals stay in counts.
func RequestTriageApply(
	ctx context.Context, db *pg.DB, batchID, actor int64, ruleVersion string, steps []int16, audit json.RawMessage,
) error {
	if actor <= 0 {
		return ErrTriageBatchState
	}
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		batch, err := lockTriageBatch(ctx, tx, batchID)
		switch {
		case err != nil:
			return err
		case batch.Status != TriageBatchPlanned:
			return ErrTriageBatchState
		case batch.RuleVersion != ruleVersion:
			return ErrTriageRuleVersion
		case len(steps) == 0:
			return ErrTriageNothingAudited
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE public.author_review_triage_item SET outcome = 'not_audited'
			WHERE batch_id = ? AND outcome = 'planned' AND NOT (step = ANY (?::smallint[]))`,
			batchID, pg.Array(steps)); err != nil {
			return fmt.Errorf("setting aside the steps not audited: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE public.author_review_triage_batch
			SET status = 'applying', apply_requested_by_user_id = ?1, apply_requested_at = now(),
				applied_steps = ?2::smallint[], audit = ?3::jsonb, items_done = 0, lease_owner = NULL,
				lease_until = NULL,
				items_total = (SELECT count(*) FROM public.author_review_triage_item
					WHERE batch_id = ?0 AND outcome = 'planned')
			WHERE id = ?0`, batchID, actor, pg.Array(steps), string(audit))
		return err
	})
	if uniqueViolationOf(err, triageOneActiveIndex) {
		return ErrTriageActiveBatch
	}
	return err
}

// RequestTriageUndo hands an applied batch to the worker to undo the applied
// items of step, or of every step when step is 0: those whose override is
// not revoked yet and that are not gone. A blocked or failed undo is
// therefore asked again; a gone one never is. It returns how many items it
// marked; none is ErrTriageBatchState.
func RequestTriageUndo(ctx context.Context, db *pg.DB, batchID, actor int64, step int16) (int, error) {
	if actor <= 0 || step < 0 {
		return 0, ErrTriageBatchState
	}
	var marked int
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		batch, err := lockTriageBatch(ctx, tx, batchID)
		switch {
		case err != nil:
			return err
		case batch.Status != TriageBatchApplied:
			return ErrTriageBatchState
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE public.author_review_triage_item SET undo_requested_at = now(), undo_outcome = NULL
			WHERE batch_id = ?0 AND outcome = 'applied' AND revoked_at IS NULL
				AND undo_outcome IS DISTINCT FROM 'gone' AND (?1 = 0 OR step = ?1)`, batchID, step)
		if err != nil {
			return fmt.Errorf("marking the items to undo: %w", err)
		}
		if marked = res.RowsAffected(); marked == 0 {
			return ErrTriageBatchState
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE public.author_review_triage_batch
			SET status = 'undoing', undo_requested_by_user_id = ?, undo_requested_at = now(), items_total = ?,
				items_done = 0, lease_owner = NULL, lease_until = NULL
			WHERE id = ?`, actor, marked, batchID)
		return err
	})
	if uniqueViolationOf(err, triageOneActiveIndex) {
		return 0, ErrTriageActiveBatch
	}
	if err != nil {
		return 0, err
	}
	return marked, nil
}

// TriageWorkItem is one item the worker applies or undoes.
type TriageWorkItem struct {
	ID               int64           `pg:"id"`
	ReviewItemID     int64           `pg:"review_item_id"`
	Step             int16           `pg:"step"`
	Action           string          `pg:"action"`
	Planned          json.RawMessage `pg:"planned,type:jsonb"`
	ProposalResultID *int64          `pg:"proposal_result_id"`
	Class            string          `pg:"decision_class"`
	Fingerprint      []byte          `pg:"source_fingerprint"`
	OverrideID       *int64          `pg:"override_id"`
}

const triageWorkColumns = `id, review_item_id, step, action, planned, proposal_result_id, decision_class,
	source_fingerprint, override_id`

// NextTriageApplyItems lists the next items to decide: planned, by step and
// row.
func NextTriageApplyItems(ctx context.Context, db pg.DBI, batchID int64, limit int) ([]TriageWorkItem, error) {
	var items []TriageWorkItem
	if _, err := db.QueryContext(ctx, &items, `SELECT `+triageWorkColumns+`
		FROM public.author_review_triage_item WHERE batch_id = ? AND outcome = 'planned'
		ORDER BY step, id LIMIT ?`, batchID, limit); err != nil {
		return nil, fmt.Errorf("reading the items to apply: %w", err)
	}
	return items, nil
}

// NextTriageUndoItems lists the next items to undo: applied, asked to be
// undone and not undone yet, by step and row.
func NextTriageUndoItems(ctx context.Context, db pg.DBI, batchID int64, limit int) ([]TriageWorkItem, error) {
	var items []TriageWorkItem
	if _, err := db.QueryContext(ctx, &items, `SELECT `+triageWorkColumns+`
		FROM public.author_review_triage_item
		WHERE batch_id = ? AND outcome = 'applied' AND undo_requested_at IS NOT NULL AND undo_outcome IS NULL
		ORDER BY step, id LIMIT ?`, batchID, limit); err != nil {
		return nil, fmt.Errorf("reading the items to undo: %w", err)
	}
	return items, nil
}

// RecordTriageApplied logs the decision of a planned item inside the
// decision's own transaction. An item another worker handled is
// ErrTriageItemTaken, which rolls the decision back.
func RecordTriageApplied(ctx context.Context, tx pg.DBI, itemID int64, rep ReviewReport) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE public.author_review_triage_item
		SET outcome = 'applied', result_id = ?, override_id = ?, decided_at = now()
		WHERE id = ? AND outcome = 'planned'`, nullableInt(rep.ResultID), nullableInt(rep.OverrideID), itemID)
	if err != nil {
		return fmt.Errorf("logging the applied item: %w", err)
	}
	if res.RowsAffected() != 1 {
		return ErrTriageItemTaken
	}
	return nil
}

// RecordTriageOutcome logs an item that was not applied: skipped_changed,
// conflict or failed (with its SQLSTATE). An item no longer planned keeps
// what it has.
func RecordTriageOutcome(ctx context.Context, db pg.DBI, itemID int64, outcome, sqlstate string) error {
	if _, err := db.ExecContext(ctx, `
		UPDATE public.author_review_triage_item SET outcome = ?, sqlstate = ?, decided_at = now()
		WHERE id = ? AND outcome = 'planned'`, outcome, nullable(sqlstate), itemID); err != nil {
		return fmt.Errorf("logging the item's outcome: %w", err)
	}
	return nil
}

// RecordTriageUndo logs the undo of an item. Restored and superseded revoked
// the batch's override: they are written in the revoke's transaction and
// carry the revoke; an item another worker undid is ErrTriageItemTaken.
// Blocked, gone and failed revoked nothing and are written on their own.
func RecordTriageUndo(ctx context.Context, db pg.DBI, itemID, actor int64, undoOutcome string) error {
	revoked := undoOutcome == TriageUndoRestored || undoOutcome == TriageUndoSuperseded
	var by interface{}
	if revoked {
		by = actor
	}
	res, err := db.ExecContext(ctx, `
		UPDATE public.author_review_triage_item
		SET undo_outcome = ?0, revoked_by_user_id = ?1,
			revoked_at = CASE WHEN ?1::bigint IS NOT NULL THEN now() END
		WHERE id = ?2 AND undo_requested_at IS NOT NULL AND undo_outcome IS NULL`, undoOutcome, by, itemID)
	if err != nil {
		return fmt.Errorf("logging the item's undo: %w", err)
	}
	if revoked && res.RowsAffected() != 1 {
		return ErrTriageItemTaken
	}
	return nil
}

// ProgressTriageBatch adds done items to owner's batch and renews the lease.
func ProgressTriageBatch(
	ctx context.Context, db pg.DBI, batchID int64, owner uuid.UUID, lease time.Duration, done int,
) error {
	res, err := db.ExecContext(ctx, `
		UPDATE public.author_review_triage_batch
		SET items_done = least(items_total, items_done + ?), lease_until = now() + ? * interval '1 millisecond'
		WHERE id = ? AND lease_owner = ? AND status IN ('applying', 'undoing')`,
		done, lease.Milliseconds(), batchID, owner)
	if err != nil {
		return fmt.Errorf("recording the triage progress: %w", err)
	}
	if res.RowsAffected() != 1 {
		return ErrTriageLeaseLost
	}
	return nil
}

// FinishTriageBatch ends owner's apply or undo: the batch is applied.
func FinishTriageBatch(ctx context.Context, db pg.DBI, batchID int64, owner uuid.UUID) error {
	res, err := db.ExecContext(ctx, `
		UPDATE public.author_review_triage_batch
		SET status = 'applied', items_done = items_total, finished_at = now(), lease_owner = NULL,
			lease_until = NULL
		WHERE id = ? AND lease_owner = ? AND status IN ('applying', 'undoing')`, batchID, owner)
	if err != nil {
		return fmt.Errorf("finishing the triage batch: %w", err)
	}
	if res.RowsAffected() != 1 {
		return ErrTriageLeaseLost
	}
	return nil
}
