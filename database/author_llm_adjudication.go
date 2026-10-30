package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
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

// ErrAuthorLLMNotAdjudicable is an item that is neither an adversarial
// suite item the production rule accepted in a merge eval run, a
// contested accept of one, nor an item of a census's audit sample.
var ErrAuthorLLMNotAdjudicable = errors.New(
	"database: the eval item is neither an accepted adversarial suite item, a contested accept nor an audited one")

// AuthorLLMAdjudication is an item's current label.
type AuthorLLMAdjudication struct {
	// ID is the label's row: a newer label of the item is a newer row.
	ID            int64     `pg:"id" json:"-"`
	EvalItemID    int64     `pg:"eval_item_id" json:"eval_item_id"`
	Label         string    `pg:"label" json:"label"`
	Note          string    `pg:"note" json:"note"`
	AdjudicatedBy string    `pg:"adjudicated_by" json:"adjudicated_by"`
	CreatedAt     time.Time `pg:"created_at" json:"created_at"`
}

// mergeSameSHA256 is the canonical digest of a merge "same" answer.
var mergeSameSHA256 = sha256.Sum256(llmreq.MergeCanonical(llmreq.MergeSame))

// AdjudicateAuthorLLMEvalItem records a label of an adversarial suite item
// the production rule accepted in some merge eval run (both production
// participants and both judges answered "same" in one run), or of a
// contested accept (author_llm_eval_item_contested, migration 68: the pair
// answered same, the judges did not both confirm it), or of an item of a
// census's audit sample (author_llm_eval_item_audited, migration 70: all
// four answered same, and it is sampled). ErrAuthorLLMNotAdjudicable
// otherwise.
func AdjudicateAuthorLLMEvalItem(ctx context.Context, db pg.DBI, itemID int64, label, note, by string) error {
	if !ValidAdjudicationLabel(label) {
		return fmt.Errorf("database: unknown adjudication label %q", label)
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO author_llm_eval_adjudication (eval_item_id, label, note, adjudicated_by)
		SELECT e.id, ?1, ?2, ?3 FROM author_llm_eval_item e
		WHERE e.id = ?0 AND ((e.item_set = 'adversarial'
			AND EXISTS (
				SELECT 1 FROM author_llm_job j
				JOIN author_llm_run r ON r.id = j.run_id AND r.kind = 'eval' AND r.mode LIKE 'merge%'
				JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
				WHERE j.eval_item_id = e.id AND j.status = 'answered' AND j.repeat_no = 0
					AND j.purpose IN ('eval_candidate', 'eval_judge') AND a.canonical_sha256 = ?4
				GROUP BY j.run_id
				HAVING count(DISTINCT j.slot) = 4))
			OR public.author_llm_eval_item_contested(e.id) OR public.author_llm_eval_item_audited(e.id))`,
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
		SELECT DISTINCT ON (eval_item_id) id, eval_item_id, label, note, adjudicated_by, created_at
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

// AuthorLLMAdjudicationBinding ties a merge eval report to the labels it
// was computed from: the items whose labels gate it (the audit samples,
// the accepted suite items, the contested accepts) and a digest of each
// one's current label row as read (AuthorLLMAdjudicationDigest).
type AuthorLLMAdjudicationBinding struct {
	Items  []int64
	SHA256 []byte
}

// AuthorLLMAdjudicationDigest is sha256 over items in id order, each as
// "<item id>:<its current label's row id, 0 when none>\n". Any label
// written to one of them after labels were read changes it, whatever the
// order the labels commit in.
func AuthorLLMAdjudicationDigest(items []int64, labels map[int64]AuthorLLMAdjudication) []byte {
	sorted := slices.Compact(slices.Sorted(slices.Values(items)))
	h := sha256.New()
	for _, id := range sorted {
		_, _ = h.Write([]byte(strconv.FormatInt(id, 10) + ":" + strconv.FormatInt(labels[id].ID, 10) + "\n"))
	}
	return h.Sum(nil)
}

// adjudicationsCurrent reports whether the binding's items still carry the
// labels it was computed from. It takes the adjudication table SHARE until
// tx ends: a label being written is waited for and seen, and none is
// written before tx commits what it checked.
func adjudicationsCurrent(ctx context.Context, tx *pg.Tx, b *AuthorLLMAdjudicationBinding) (bool, error) {
	if _, err := tx.ExecContext(ctx, `LOCK TABLE public.author_llm_eval_adjudication IN SHARE MODE`); err != nil {
		return false, fmt.Errorf("locking the adjudications: %w", err)
	}
	labels, err := AuthorLLMAdjudications(ctx, tx, b.Items)
	if err != nil {
		return false, err
	}
	return bytes.Equal(AuthorLLMAdjudicationDigest(b.Items, labels), b.SHA256), nil
}

// checkAdjudicationBinding refuses with ErrAuthorLLMReportStale a report
// whose labels changed since they were read; a nil binding (K's reports)
// checks nothing.
func checkAdjudicationBinding(ctx context.Context, tx *pg.Tx, b *AuthorLLMAdjudicationBinding) error {
	if b == nil {
		return nil
	}
	current, err := adjudicationsCurrent(ctx, tx, b)
	if err != nil {
		return err
	}
	if !current {
		return ErrAuthorLLMReportStale
	}
	return nil
}
