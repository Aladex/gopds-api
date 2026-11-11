package database

import (
	"context"
	"encoding/json"
	"fmt"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authortriage"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The read-only loaders of a triage dry run: the open review items with the
// facts the rule set decides on, the judges' stored answers, the structures
// of a same-name entity, and the names the build's rules read.

// TriageOpenItem is one open review item with its facts. Pinned is the
// FingerprintPinned predicate; UndoneBefore: a batch of the same rule
// version applied a decision to the fingerprint and an undo restored it.
// EntityRule and EntityID are the build's newest first assignment of the
// fingerprint.
type TriageOpenItem struct {
	ID               int64                                  `pg:"id"`
	ScopeCreditID    *int64                                 `pg:"scope_credit_id"`
	Fingerprint      []byte                                 `pg:"fingerprint"`
	Reason           string                                 `pg:"reason"`
	Class            string                                 `pg:"class"`
	ProposalResultID *int64                                 `pg:"proposal_result_id"`
	Proposal         *models.ContributorNormalizationResult `pg:"-"`
	Credits          int                                    `pg:"credits,use_zero"`
	Pinned           bool                                   `pg:"pinned,use_zero"`
	UndoneBefore     bool                                   `pg:"undone_before,use_zero"`
	EntityRule       string                                 `pg:"entity_rule"`
	EntityID         *uuid.UUID                             `pg:"entity_id,type:uuid"`
}

// triageOpenItemsSQL pages the open items by ID (?2 after, ?3 rows) with
// their facts; ?1 is the rule version of the undo test.
const triageOpenItemsSQL = `
SELECT i.id, i.scope_credit_id, i.source_fingerprint AS fingerprint, i.reason,
	coalesce(i.decision_class, '') AS class, i.proposal_result_id, coalesce(cc.credits, 0) AS credits,
	` + fingerprintPinnedPredicate + ` AS pinned,
	EXISTS (SELECT 1 FROM public.author_review_triage_item t
		JOIN public.author_review_triage_batch b ON b.id = t.batch_id
		WHERE t.source_fingerprint = i.source_fingerprint AND t.revoked_at IS NOT NULL
			AND t.undo_outcome = 'restored' AND b.rule_version = ?1) AS undone_before,
	coalesce(ea.rule, '') AS entity_rule, ea.entity_id
FROM public.contributor_review_item i
CROSS JOIN LATERAL (SELECT i.source_fingerprint AS fp) pin
LEFT JOIN LATERAL (
	SELECT count(*) AS credits FROM public.book_contributor_credit c
	JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
	WHERE c.source_fingerprint = i.source_fingerprint AND c.role = 'author') cc ON true
LEFT JOIN LATERAL (
	SELECT a.rule, a.entity_id FROM public.author_entity_assignment a
	WHERE a.source_fingerprint = i.source_fingerprint AND a.ordinal = 0
	ORDER BY a.updated_at DESC LIMIT 1) ea ON true
WHERE i.status = 'open' AND i.id > ?2
ORDER BY i.id
LIMIT ?3`

// LoadTriageOpenItems reads a page of the open review items after the ID
// after, with their proposals.
func LoadTriageOpenItems(
	ctx context.Context, db pg.DBI, ruleVersion string, after int64, limit int,
) ([]TriageOpenItem, error) {
	var items []TriageOpenItem
	if _, err := db.QueryContext(ctx, &items, triageOpenItemsSQL, nil, ruleVersion, after, limit); err != nil {
		return nil, fmt.Errorf("reading the open review items: %w", err)
	}
	var ids []int64
	for i := range items {
		if items[i].ProposalResultID != nil {
			ids = append(ids, *items[i].ProposalResultID)
		}
	}
	if len(ids) == 0 {
		return items, nil
	}
	var proposals []models.ContributorNormalizationResult
	if err := db.ModelContext(ctx, &proposals).Where("id IN (?)", pg.In(ids)).Select(); err != nil {
		return nil, fmt.Errorf("reading the proposals: %w", err)
	}
	byID := make(map[int64]*models.ContributorNormalizationResult, len(proposals))
	for i := range proposals {
		byID[proposals[i].ID] = &proposals[i]
	}
	for i := range items {
		if items[i].ProposalResultID != nil {
			items[i].Proposal = byID[*items[i].ProposalResultID]
		}
	}
	return items, nil
}

// TriageStoredAnswer is one model's stored answer of an eval run on the
// input of a fingerprint (eval sets v1.s.*, first repeat only).
type TriageStoredAnswer struct {
	Fingerprint      []byte          `pg:"fingerprint"`
	RunID            int64           `pg:"run_id"`
	ConfigVersion    string          `pg:"config_version"`
	Arm              string          `pg:"arm"`
	BatchSize        int             `pg:"batch_size"`
	Slot             string          `pg:"slot"`
	ExtractorVersion string          `pg:"extractor_version"`
	Input            json.RawMessage `pg:"input,type:jsonb"`
	Reply            json.RawMessage `pg:"reply,type:jsonb"`
	Canonical        []byte          `pg:"canonical"`
	Refused          bool            `pg:"refused,use_zero"`
}

// triageAnswersPerRead is how many fingerprints one read of the stored
// answers takes.
const triageAnswersPerRead = 500

// LoadTriageStoredAnswers reads the stored eval answers of the fingerprints
// (RESEARCH §7), triageAnswersPerRead fingerprints a read. The join names
// the eval runs because the job's eval key leads with the run: by item
// alone a read scans the whole key (18 s per read on prod, 134 ms so).
func LoadTriageStoredAnswers(ctx context.Context, db pg.DBI, fingerprints [][]byte) ([]TriageStoredAnswer, error) {
	answers := []TriageStoredAnswer{}
	for from := 0; from < len(fingerprints); from += triageAnswersPerRead {
		var page []TriageStoredAnswer
		if _, err := db.QueryContext(ctx, &page, `
			SELECT e.source_fingerprint AS fingerprint, j.run_id, j.config_version, coalesce(j.arm, '-') AS arm,
				j.batch_size, j.slot, e.extractor_version, j.input, a.reply, a.canonical_sha256 AS canonical, a.refused
			FROM public.author_llm_eval_item e
			JOIN public.author_llm_run r ON r.kind = 'eval'
			JOIN public.author_llm_job j ON j.run_id = r.id AND j.eval_item_id = e.id AND j.repeat_no = 0
				AND j.answer_attempt_id IS NOT NULL
			JOIN public.author_llm_attempt a ON a.id = j.answer_attempt_id AND a.outcome = 'answered'
			WHERE e.set_version LIKE 'v1.s.%' AND e.source_fingerprint = ANY (?::bytea[])
			ORDER BY e.source_fingerprint, j.run_id, j.id`,
			pg.Array(fingerprints[from:min(from+triageAnswersPerRead, len(fingerprints))])); err != nil {
			return nil, fmt.Errorf("reading the stored answers: %w", err)
		}
		answers = append(answers, page...)
	}
	return answers, nil
}

// LoadTriageSameNameStructures reads, per entity, the distinct person
// structures of the selected author credits of current snapshots whose
// fingerprint the build assigned to it.
func LoadTriageSameNameStructures(
	ctx context.Context, db pg.DBI, entities []uuid.UUID,
) (map[uuid.UUID][]authortriage.Name, error) {
	out := map[uuid.UUID][]authortriage.Name{}
	if len(entities) == 0 {
		return out, nil
	}
	var rows []struct {
		EntityID uuid.UUID `pg:"entity_id,type:uuid"`
		authortriage.Name
	}
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT DISTINCT a.entity_id, coalesce(r.given_name, '') AS given,
			coalesce(r.additional_names[1], '') AS additional, coalesce(r.family_name, '') AS family,
			coalesce(r.nickname, '') AS nickname, coalesce(r.prefix, '') AS prefix, coalesce(r.suffix, '') AS suffix,
			coalesce(r.display_name, '') AS display, coalesce(r.sort_name, '') AS sort, r.kind
		FROM public.author_entity_assignment a
		JOIN public.book_contributor_credit c ON c.source_fingerprint = a.source_fingerprint AND c.role = 'author'
		JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		JOIN public.book_contributor_credit_selection sel ON sel.credit_id = c.id AND sel.state = 'selected'
		JOIN public.contributor_normalization_result r ON r.id = sel.result_id AND r.kind = 'person'
		WHERE a.entity_id = ANY (?::uuid[])
		ORDER BY 1, 2, 3, 4, 5, 6, 7, 8, 9`, pg.Array(entities)); err != nil {
		return nil, fmt.Errorf("reading the same-name structures: %w", err)
	}
	for i := range rows {
		out[rows[i].EntityID] = append(out[rows[i].EntityID], rows[i].Name)
	}
	return out, nil
}

// LoadReviewTriageNames reads what the rules read of the build's input: the
// selected author credits (their given names) and the person credits under
// review (the source of a proposal). The caller bounds its statements.
func LoadReviewTriageNames(ctx context.Context, db pg.DBI) (*authorentity.Input, error) {
	in := &authorentity.Input{}
	if err := loadEntityCredits(ctx, db, in); err != nil {
		return nil, err
	}
	if err := loadEntityUnselected(ctx, db, in); err != nil {
		return nil, err
	}
	return in, nil
}
