package services

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/go-pg/pg/v10"
)

// The census audit (E4c, migration 70): a census mode's stratum passes on
// a human audit of its applied set. The latest report lists each
// stratum's sample — merge3's bucket two (migration 72): its holdout's and
// its extra Latin questions' —; an administrator labels them like the
// suite, and a recompute picks the labels up.

// auditIDs are the audit samples a stored merge report lists, in id order
// without repeats.
func auditIDs(report string) ([]int64, error) {
	type sample struct {
		Sample []int64 `json:"sample"`
	}
	type combo struct {
		Audit      *sample `json:"audit"`
		Supplement *struct {
			Audit *sample `json:"audit"`
		} `json:"supplement"`
	}
	return comboIDs(report, func(c *combo) []int64 {
		var ids []int64
		if c.Audit != nil {
			ids = append(ids, c.Audit.Sample...)
		}
		if c.Supplement != nil && c.Supplement.Audit != nil {
			ids = append(ids, c.Supplement.Audit.Sample...)
		}
		return ids
	})
}

// comboIDs are the ids pick reads off every combo of a stored merge
// report, in id order without repeats.
func comboIDs[C any](report string, pick func(*C) []int64) ([]int64, error) {
	var doc struct {
		Evaluation struct {
			Strata map[string]struct {
				Combos map[string]C `json:"combos"`
			} `json:"strata"`
		} `json:"evaluation"`
	}
	if err := json.Unmarshal([]byte(report), &doc); err != nil {
		return nil, fmt.Errorf("reading a merge eval report: %w", err)
	}
	var ids []int64
	for _, stratum := range doc.Evaluation.Strata {
		for _, c := range stratum.Combos {
			ids = append(ids, pick(&c)...)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// AuthorLLMAuditItems are the audit samples a merge eval run's latest
// report lists — census or holdout items all four participants answered
// same, each with its set — in the suite review's shape: a human labels
// them, and any pending or labeled different fails its stratum.
func AuthorLLMAuditItems(ctx context.Context, db pg.DBI, runID int64) (*AuthorLLMAdversarialReview, error) {
	return mergeReviewItems(ctx, db, runID, auditIDs)
}

// AuditItems are a merge eval run's audit samples to adjudicate; the name
// normalization's admin has none.
func (a *AuthorLLMAdmin) AuditItems(ctx context.Context, id int64) (*AuthorLLMAdversarialReview, error) {
	if !a.merge {
		return nil, ErrAuthorLLMInvalidRun
	}
	return AuthorLLMAuditItems(ctx, a.db, id)
}
