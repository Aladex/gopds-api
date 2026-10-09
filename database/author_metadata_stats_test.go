package database

// Phase 20, RED 3/4/6: the durable aggregates the status and report read.

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RED 4: a run with nothing in it reports zeros — never NaN, never negative,
// every map present and empty — and so does a catalog without runs.
func TestAuthorMetadataStatsOfAnEmptyRun(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	run := &models.AuthorMetadataRun{
		// Pending: not started, so no duration either — the throughput must
		// still be 0, not 0/0.
		Mode: models.AuthorMetadataRunFull, Status: models.AuthorMetadataRunPending,
		ExtractorVersion: "extractor-v1", NormalizerVersion: "normalizer-v1",
	}
	require.NoError(t, SeedRun(ctx, s, run, nil))

	stats, err := AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assertNoNaNOrNegative(t, &stats)
	assert.Zero(t, stats.Extraction.Total)
	assert.Zero(t, stats.Extraction.ItemsPerSecond)
	assert.Zero(t, stats.Extraction.DurationS)
	assert.Zero(t, stats.Extraction.OldestPendingAgeS)
	assert.Zero(t, stats.Local.Pending)
	assert.Zero(t, stats.Review.Open)
	assert.Empty(t, stats.Coverage.ByDecisionClass)
	for status, n := range stats.Extraction.ByStatus {
		assert.Zero(t, n, status)
	}
	assert.Len(t, stats.Extraction.ByStatus, len(models.AuthorMetadataRunItemTerminalStatuses()),
		"every terminal status is reported, zero or not")

	_, err = AuthorMetadataStatsForRun(ctx, s, run.ID+1)
	assert.ErrorIs(t, err, ErrRunNotFound, "an unknown run is not a run of zeros")
}

// assertNoNaNOrNegative walks the JSON form: every number is finite and not
// below zero, every map is an object, never null.
func assertNoNaNOrNegative(t *testing.T, stats *AuthorMetadataStats) {
	t.Helper()
	raw, err := json.Marshal(stats)
	require.NoError(t, err, "NaN or Inf cannot be encoded")
	var tree interface{}
	require.NoError(t, json.Unmarshal(raw, &tree))
	var walk func(path string, v interface{})
	walk = func(path string, v interface{}) {
		switch x := v.(type) {
		case float64:
			assert.False(t, math.IsNaN(x) || math.IsInf(x, 0), path)
			assert.GreaterOrEqual(t, x, 0.0, path)
		case map[string]interface{}:
			for k, child := range x {
				walk(path+"."+k, child)
			}
		case nil:
			t.Errorf("%s is null", path)
		}
	}
	walk("stats", tree)
}
