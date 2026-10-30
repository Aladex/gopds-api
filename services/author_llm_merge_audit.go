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
// stratum's sample; an administrator labels it like the suite, and a
// recompute picks the labels up.

// auditIDs are the audit samples a stored merge report lists, in id order
// without repeats.
func auditIDs(report string) ([]int64, error) {
	type combo struct {
		Audit *struct {
			Sample []int64 `json:"sample"`
		} `json:"audit"`
	}
	return comboIDs(report, func(c *combo) []int64 {
		if c.Audit == nil {
			return nil
		}
		return c.Audit.Sample
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
// report lists — census items all four participants answered same — in
// the suite review's shape: a human labels them, and any pending or
// labeled different fails its stratum.
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
