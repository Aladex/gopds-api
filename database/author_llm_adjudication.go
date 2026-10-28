package database

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"gopds-api/internal/authornorm/llmreq"

	"github.com/go-pg/pg/v10"
)

// Adjudications of adversarial suite items (migration 67, E4c-2b-0): a
// human label of an accepted namesake item, history kept, the latest row
// current.

// The labels of an adjudication.
const (
	AdjudicationSame      = "same"
	AdjudicationDifferent = "different"
	AdjudicationUnclear   = "unclear"
)

// ValidAdjudicationLabel reports a closed label.
func ValidAdjudicationLabel(label string) bool {
	return label == AdjudicationSame || label == AdjudicationDifferent || label == AdjudicationUnclear
}

// ErrAuthorLLMNotAdjudicable is an item that is not an adversarial suite
// item the production rule accepted in a merge eval run.
var ErrAuthorLLMNotAdjudicable = errors.New("database: the eval item is not an accepted adversarial suite item")

// AuthorLLMAdjudication is an item's current label.
type AuthorLLMAdjudication struct {
	EvalItemID    int64     `pg:"eval_item_id" json:"eval_item_id"`
	Label         string    `pg:"label" json:"label"`
	Note          string    `pg:"note" json:"note"`
	AdjudicatedBy string    `pg:"adjudicated_by" json:"adjudicated_by"`
	CreatedAt     time.Time `pg:"created_at" json:"created_at"`
}

// mergeSameSHA256 is the canonical digest of a merge "same" answer.
var mergeSameSHA256 = sha256.Sum256(llmreq.MergeCanonical(llmreq.MergeSame))

// AdjudicateAuthorLLMEvalItem records a label of an adversarial suite item
// the production rule accepted in some merge eval run: both production
// participants and both judges answered "same" in one run.
// ErrAuthorLLMNotAdjudicable otherwise.
func AdjudicateAuthorLLMEvalItem(ctx context.Context, db pg.DBI, itemID int64, label, note, by string) error {
	if !ValidAdjudicationLabel(label) {
		return fmt.Errorf("database: unknown adjudication label %q", label)
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO author_llm_eval_adjudication (eval_item_id, label, note, adjudicated_by)
		SELECT e.id, ?1, ?2, ?3 FROM author_llm_eval_item e
		WHERE e.id = ?0 AND e.item_set = 'adversarial'
			AND EXISTS (
				SELECT 1 FROM author_llm_job j
				JOIN author_llm_run r ON r.id = j.run_id AND r.kind = 'eval' AND r.mode LIKE 'merge%'
				JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
				WHERE j.eval_item_id = e.id AND j.status = 'answered' AND j.repeat_no = 0
					AND j.purpose IN ('eval_candidate', 'eval_judge') AND a.canonical_sha256 = ?4
				GROUP BY j.run_id
				HAVING count(DISTINCT j.slot) = 4)`,
		itemID, label, note, by, mergeSameSHA256[:])
	if err != nil {
		return fmt.Errorf("recording an adjudication: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrAuthorLLMNotAdjudicable
	}
	return nil
}

// AuthorLLMAdjudications are the current labels of the given items.
func AuthorLLMAdjudications(ctx context.Context, db pg.DBI, itemIDs []int64) (map[int64]AuthorLLMAdjudication, error) {
	out := map[int64]AuthorLLMAdjudication{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	var rows []AuthorLLMAdjudication
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT DISTINCT ON (eval_item_id) eval_item_id, label, note, adjudicated_by, created_at
		FROM author_llm_eval_adjudication WHERE eval_item_id IN (?)
		ORDER BY eval_item_id, id DESC`, pg.In(itemIDs)); err != nil {
		return nil, fmt.Errorf("reading the adjudications: %w", err)
	}
	for _, r := range rows {
		out[r.EvalItemID] = r
	}
	return out, nil
}

// AuthorLLMEvalItemsByID reads frozen items by id, by id.
func AuthorLLMEvalItemsByID(ctx context.Context, db pg.DBI, ids []int64) ([]AuthorLLMEvalItemRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []AuthorLLMEvalItemRow
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT id, set_version, item_set, stratum, source_fingerprint, input, weight, context_sha256, expected
		FROM author_llm_eval_item WHERE id IN (?) ORDER BY id`, pg.In(ids)); err != nil {
		return nil, fmt.Errorf("reading eval items: %w", err)
	}
	return rows, nil
}
