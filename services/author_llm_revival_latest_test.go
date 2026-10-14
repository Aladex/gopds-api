package services_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Review B1 of d44e400: whether an input is asked again is decided by its
// latest verdict under the configuration. An older transient verdict never
// revives an input whose latest outcome is final — an apply_failed of
// another cause than the fingerprint defect included.

// threeRuns runs a pilot and two full resolve runs. In the first, judge_a
// answers 503 about the input; in the second, applying the input panics; the
// third is a normal run. It returns the three run ids.
func threeRuns(t *testing.T, e *llmEnv, last string) [3]int64 {
	t.Helper()
	ctx := context.Background()
	var mu sync.Mutex
	down := true
	e.fake.setRespond(judgeADownForTokens([]string{last}, &mu, &down))
	var runs [3]int64
	for i, mode := range []string{"pilot", "full", "full"} {
		switch i {
		case 1:
			mu.Lock()
			down = false
			mu.Unlock()
			services.PanicOnApplyForTest(t, func(l string) bool { return l == last })
		case 2:
			services.PanicOnApplyForTest(t, func(string) bool { return false })
		}
		run, err := e.llmAdmin().Start(ctx, "resolve", mode, e.adminUser())
		require.NoError(t, err)
		e.resolveUntilDone(run.ID)
		runs[i] = run.ID
	}
	return runs
}

// The reviewer's sequence: transient, then an apply_failed of another cause,
// then a normal run — which must not ask the input again.
func TestAuthorLLMResolveLatestFinalApplyFailedBlocksRevival(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	fp := e.fingerprintOf("Смирнов")
	runs := threeRuns(t, e, "Смирнов")
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND outcome_reason = 'llm_judge_failed'`, runs[0], fp))
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND outcome_reason = 'apply_failed'`, runs[1], fp), "the second run did not ask the transient input again")

	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND source_fingerprint = ?`, runs[2], fp),
		"an input whose latest outcome is final was asked again")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?`, runs[2], fp))
	assert.Equal(t, "review", e.selections()["Смирнов"].State)
}

// The companion: the same sequence for an input of the fingerprint defect
// class — its latest apply_failed is eligible, and the third run applies it.
func TestAuthorLLMResolveLatestDefectApplyFailedRevives(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ingestRawAuthors(t, e, run15Shapes, 300)
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	fp := e.fingerprintOf("Беляев")
	runs := threeRuns(t, e, "Беляев")
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND outcome_reason = 'apply_failed'`, runs[1], fp))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND result_id IS NOT NULL`, runs[2], fp), "the defect-class input was not asked again")
	assert.Equal(t, "selected", e.selections()["Беляев"].State)
}

// The mirror order: an apply_failed of the defect class, then a transient
// failure when it is asked again — the latest outcome is transient, and the
// next run asks it again and applies it.
func TestAuthorLLMResolveLatestTransientAfterApplyFailedRevives(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ingestRawAuthors(t, e, run15Shapes, 300)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	fp := e.fingerprintOf("Беляев")
	var mu sync.Mutex
	down := false
	e.fake.setRespond(judgeADownForTokens([]string{"Беляев"}, &mu, &down))
	services.PanicOnApplyForTest(t, func(l string) bool { return l == "Беляев" })
	var runs []int64
	for i, mode := range []string{"pilot", "full", "full"} {
		switch i {
		case 1:
			services.PanicOnApplyForTest(t, func(string) bool { return false })
			mu.Lock()
			down = true
			mu.Unlock()
		case 2:
			mu.Lock()
			down = false
			mu.Unlock()
		}
		run, err := e.llmAdmin().Start(ctx, "resolve", mode, e.adminUser())
		require.NoError(t, err)
		e.resolveUntilDone(run.ID)
		runs = append(runs, run.ID)
	}
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND outcome_reason = 'apply_failed'`, runs[0], fp))
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND outcome_reason = 'llm_judge_failed'`, runs[1], fp))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND result_id IS NOT NULL`, runs[2], fp))
	assert.Equal(t, "selected", e.selections()["Беляев"].State)
}
