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
