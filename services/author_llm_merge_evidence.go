package services

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
)

// A merge eval report is bound to the labels it was computed from (E4c
// review round 1): it lists the items whose labels gate it and a digest of
// their label rows as read. The report is stored only while they are
// unchanged, and a registration is refused once one of them changed: a
// label written after a report passed never lets the stale report register
// a kind.

// mergeAdjudicationBinding is a merge report's labels as read: the items
// whose labels gate it, in id order, and AuthorLLMAdjudicationDigest of
// them, hex.
type mergeAdjudicationBinding struct {
	Items  []int64 `json:"items"`
	SHA256 string  `json:"sha256"`
}

// mergeReportComputedHook, a test seam, runs once a merge report is
// computed (its labels read), before it is stored.
var mergeReportComputedHook func(runID int64)

// gatedItems are the items whose labels gate an evaluation: every audit
// sample (a holdout's extra Latin questions' included), accepted suite item
// and contested accept it lists.
func gatedItems(r *llmeval.Report) []int64 {
	ids := []int64{}
	addReview := func(m *llmeval.ContestedMetrics) {
		if m == nil {
			return
		}
		for _, byScript := range m.IDs {
			ids = append(ids, byScript...)
		}
	}
	for _, s := range r.Strata {
		for key := range s.Combos {
			c := s.Combos[key]
			if c.Audit != nil {
				ids = append(ids, c.Audit.Sample...)
			}
			if c.Adversarial != nil {
				ids = append(ids, c.Adversarial.AcceptedIDs...)
			}
			addReview(c.ContestedReview)
			if c.Supplement != nil {
				addReview(c.Supplement.ContestedReview)
				if c.Supplement.Audit != nil {
					ids = append(ids, c.Supplement.Audit.Sample...)
				}
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// bindAdjudications binds an evaluation to the labels it read.
func bindAdjudications(r *llmeval.Report, labels map[int64]database.AuthorLLMAdjudication) *mergeAdjudicationBinding {
	items := gatedItems(r)
	return &mergeAdjudicationBinding{Items: items,
		SHA256: hex.EncodeToString(database.AuthorLLMAdjudicationDigest(items, labels))}
}

// stored is the binding as the database checks it.
func (b *mergeAdjudicationBinding) stored() (*database.AuthorLLMAdjudicationBinding, error) {
	sum, err := hex.DecodeString(b.SHA256)
	if err != nil || len(sum) != 32 {
		return nil, fmt.Errorf("a merge eval report's adjudication digest %q", b.SHA256)
	}
	return &database.AuthorLLMAdjudicationBinding{Items: b.Items, SHA256: sum}, nil
}

// mergeEvidenceBinding is a stored merge report's binding; a report
// without one (stored before it was bound) cannot be shown current and
// refuses a registration until it is recomputed.
func mergeEvidenceBinding(report *database.AuthorLLMEvalReport) (*database.AuthorLLMAdjudicationBinding, error) {
	var doc struct {
		Adjudications *mergeAdjudicationBinding `json:"adjudications"`
	}
	if err := json.Unmarshal([]byte(report.Report), &doc); err != nil {
		return nil, fmt.Errorf("reading a merge eval report: %w", err)
	}
	if doc.Adjudications == nil {
		return nil, database.ErrMergeRegistrationEvidenceStale
	}
	return doc.Adjudications.stored()
}
