package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func set(ids ...int64) map[int64]struct{} {
	out := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

func TestRecallAt(t *testing.T) {
	tests := []struct {
		name  string
		got   []int64
		want  map[int64]struct{}
		k     int
		want2 float64
	}{
		{name: "all relevant found", got: []int64{7, 8, 9}, want: set(7, 9), k: 10, want2: 1},
		{name: "half of relevant found", got: []int64{1, 2, 3}, want: set(1, 3, 5), k: 10, want2: 2.0 / 3.0},
		{name: "k cuts the tail", got: []int64{1, 2, 3, 5}, want: set(1, 3, 5), k: 2, want2: 1.0 / 3.0},
		{name: "nothing relevant found", got: []int64{8, 9}, want: set(7), k: 10, want2: 0},
		{name: "empty result", got: nil, want: set(7), k: 10, want2: 0},
		{name: "empty relevance set", got: []int64{7}, want: set(), k: 10, want2: 0},
		{name: "zero k", got: []int64{7}, want: set(7), k: 0, want2: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.want2, recallAt(tt.got, tt.want, tt.k), 1e-9)
		})
	}
}

func TestReciprocalRank(t *testing.T) {
	tests := []struct {
		name string
		got  []int64
		want map[int64]struct{}
		rr   float64
	}{
		{name: "first hit", got: []int64{7, 8}, want: set(7), rr: 1},
		{name: "second hit", got: []int64{8, 7}, want: set(7), rr: 0.5},
		{name: "tenth hit", got: []int64{1, 2, 3, 4, 5, 6, 8, 9, 10, 7}, want: set(7), rr: 0.1},
		{name: "miss", got: []int64{8, 9}, want: set(7), rr: 0},
		{name: "empty result", got: nil, want: set(7), rr: 0},
		{name: "empty relevance set", got: []int64{7}, want: set(), rr: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.rr, reciprocalRank(tt.got, tt.want))
		})
	}
}

func TestZeroResultRate(t *testing.T) {
	tests := []struct {
		name   string
		totals []int
		rate   float64
	}{
		{name: "half empty", totals: []int{0, 5, 0, 3}, rate: 0.5},
		{name: "none empty", totals: []int{1, 5, 3}, rate: 0},
		{name: "all empty", totals: []int{0, 0}, rate: 1},
		{name: "no queries", totals: nil, rate: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.rate, zeroResultRate(tt.totals), 1e-9)
		})
	}
}

func TestAggregate(t *testing.T) {
	reports := []queryReport{
		{RelevantIDs: []int64{1, 2}, RecallAtK: 0.5, ReciprocalRank: 1, Total: 5},
		// A query with an empty relevance set is not scoreable: it must not
		// drag Recall/MRR down, but its zero total still counts.
		{RelevantIDs: []int64{}, RecallAtK: 0, ReciprocalRank: 0, Total: 0},
		{RelevantIDs: []int64{3}, RecallAtK: 1, ReciprocalRank: 0.5, Total: 3},
	}

	agg := aggregate(reports)

	assert.Equal(t, 3, agg.TotalQueries)
	assert.Equal(t, 2, agg.ScoredQueries)
	assert.InDelta(t, 0.75, agg.RecallAtK, 1e-9)
	assert.InDelta(t, 0.75, agg.MRR, 1e-9)
	assert.InDelta(t, 1.0/3.0, agg.ZeroResultRate, 1e-9)
}

func TestPercentile(t *testing.T) {
	tests := []struct {
		name   string
		values []int64
		p      float64
		want   int64
	}{
		{name: "median of four", values: []int64{40, 10, 30, 20}, p: 0.5, want: 20},
		{name: "p95 of four", values: []int64{40, 10, 30, 20}, p: 0.95, want: 40},
		{name: "single value", values: []int64{5}, p: 0.95, want: 5},
		{name: "empty", values: nil, p: 0.95, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, percentile(tt.values, tt.p))
		})
	}

	t.Run("does not mutate caller order", func(t *testing.T) {
		values := []int64{3, 1, 2}
		assert.Equal(t, int64(2), percentile(values, 0.5))
		assert.Equal(t, []int64{3, 1, 2}, values)
	})
}

func TestCompareAggregates(t *testing.T) {
	// One shared query per case, so the aggregate is the query's own number
	// and the verdict is about the rule rather than the arithmetic.
	base := []queryReport{{
		evalQuery: evalQuery{Name: "shared"}, RecallAtK: 0.5,
		ReciprocalRank: 0.5, RelevantIDs: []int64{1}, Total: 3,
	}}

	current := func(recall, rr float64, total int) []queryReport {
		return []queryReport{{
			evalQuery: evalQuery{Name: "shared"}, RecallAtK: recall,
			ReciprocalRank: rr, RelevantIDs: []int64{1},
			Total: total, ZeroResult: total == 0,
		}}
	}

	for _, tt := range []struct {
		name    string
		queries []queryReport
		verdict string
	}{
		{"equal aggregates pass", current(0.5, 0.5, 3), verdictPass},
		{"improved aggregates pass", current(0.9, 1, 3), verdictPass},
		{"a recall drop is a regression", current(0.4, 0.5, 3), verdictRegression},
		{"an MRR drop is a regression", current(0.5, 0.25, 3), verdictRegression},
		{"zero results where there were some is a regression", current(0.5, 0.5, 0), verdictRegression},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmp := compareAggregates(tt.queries, base)
			assert.Equal(t, tt.verdict, cmp.Verdict)
			assert.Equal(t, 1, cmp.SharedQueries)
			assert.Empty(t, cmp.MissingQueries)
			assert.Empty(t, cmp.AddedQueries)
		})
	}

	t.Run("per-query recall drops are listed but do not fail on their own", func(t *testing.T) {
		baseline := []queryReport{
			{evalQuery: evalQuery{Name: "kept"}, RecallAtK: 0.5, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
			{evalQuery: evalQuery{Name: "traded"}, RecallAtK: 0.5, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
		}
		queries := []queryReport{
			// One query gives up recall, the other more than makes it up:
			// a ranking change, not a regression.
			{evalQuery: evalQuery{Name: "kept"}, RecallAtK: 0.9, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
			{evalQuery: evalQuery{Name: "traded"}, RecallAtK: 0.25, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
		}

		cmp := compareAggregates(queries, baseline)

		assert.Equal(t, []string{"traded"}, cmp.RegressedQueries)
		assert.Equal(t, verdictPass, cmp.Verdict, "an aggregate win covers a per-query trade")
	})

	// The hole this closes: an inconvenient query can be deleted from the set
	// and the remaining average will happily read as an improvement.
	t.Run("a baseline query this run does not measure fails", func(t *testing.T) {
		baseline := []queryReport{
			{evalQuery: evalQuery{Name: "kept"}, RecallAtK: 0.5, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
			{evalQuery: evalQuery{Name: "deleted"}, RecallAtK: 0.1, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
		}
		queries := []queryReport{
			{evalQuery: evalQuery{Name: "kept"}, RecallAtK: 0.9, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
		}

		cmp := compareAggregates(queries, baseline)

		assert.Equal(t, []string{"deleted"}, cmp.MissingQueries)
		assert.Equal(t, verdictRegression, cmp.Verdict, "dropping a golden query must not read as a win")
		assert.Positive(t, cmp.RecallDelta, "and the surviving average must still look like an improvement")
	})

	// The corpus grew between Phase 1 and Phase 8, and the published deltas
	// silently mixed the denominators. A new query is measured and reported,
	// but it cannot move a delta against a baseline that never saw it.
	t.Run("a query the baseline never had is excluded from the deltas", func(t *testing.T) {
		baseline := []queryReport{
			{evalQuery: evalQuery{Name: "shared"}, RecallAtK: 0.5, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
		}
		queries := []queryReport{
			{evalQuery: evalQuery{Name: "shared"}, RecallAtK: 0.5, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
			{evalQuery: evalQuery{Name: "added"}, RecallAtK: 0.01, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
		}

		cmp := compareAggregates(queries, baseline)

		assert.Equal(t, []string{"added"}, cmp.AddedQueries)
		assert.Equal(t, 1, cmp.SharedQueries)
		assert.InDelta(t, 0, cmp.RecallDelta, 1e-9, "the added query must not drag the delta")
		assert.Equal(t, verdictPass, cmp.Verdict)
	})

	// An aggregate win must not bury a golden query that stopped finding its
	// accepted item.
	t.Run("losing the accepted item fails even when the average improves", func(t *testing.T) {
		baseline := []queryReport{
			{evalQuery: evalQuery{Name: "critical"}, RecallAtK: 0.2, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
			{evalQuery: evalQuery{Name: "other"}, RecallAtK: 0.2, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
		}
		queries := []queryReport{
			{evalQuery: evalQuery{Name: "critical"}, RecallAtK: 0.2, ReciprocalRank: 0, RelevantIDs: []int64{1}, Total: 3},
			{evalQuery: evalQuery{Name: "other"}, RecallAtK: 0.9, ReciprocalRank: 1, RelevantIDs: []int64{1}, Total: 3},
		}

		cmp := compareAggregates(queries, baseline)

		assert.Equal(t, []string{"critical"}, cmp.LostQueries)
		assert.Equal(t, verdictRegression, cmp.Verdict)
		assert.Positive(t, cmp.RecallDelta, "the average did improve, which is the point")
	})
}
