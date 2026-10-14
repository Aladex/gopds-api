package services_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Review B1 of the stratum gates: a resolve start reads the latest eval
// report, plans from it, and only then takes the claim lock to create the run
// and register its pairs. A recomputation that publishes a newer report
// version in between must not let the start register what the newer version
// withdrew; a recomputation after the start's lock leaves the started run
// pinned to the version it planned from.

// publishReport stores a new version of the eval run's report deciding the
// given strata, as a recomputation would.
func (e *llmEnv) publishReport(t *testing.T, evalRun int64, decisions []llmeval.StratumDecision) {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(evalReportDoc{Evaluation: llmeval.Report{Strata: map[string]llmeval.StratumReport{},
		Decision: llmeval.Decision{Registrable: true, Strata: decisions}}})
	require.NoError(t, err)
	generation, err := database.AuthorLLMRelearnGeneration(ctx, e.db, evalRun)
	require.NoError(t, err)
	_, stored, err := database.RecomputeAuthorLLMEvalReport(ctx, e.db, evalRun, e.version, raw, generation)
	require.NoError(t, err)
	require.True(t, stored)
}

// latestReport is the eval run's latest report version: id and digest.
func (e *llmEnv) latestReport(t *testing.T, evalRun int64) (id int64, sha string) {
	t.Helper()
	r, err := database.GetAuthorLLMEvalReport(context.Background(), e.db, evalRun)
	require.NoError(t, err)
	return r.ID, hex.EncodeToString(r.ReportSHA256)
}

// resolvePlanOf is a resolve run's stored plan.
func (e *llmEnv) resolvePlanOf(t *testing.T, run int64) database.AuthorLLMResolveSettings {
	t.Helper()
	r, err := database.GetAuthorLLMRun(context.Background(), e.db, run)
	require.NoError(t, err)
	var plan database.AuthorLLMResolveSettings
	require.NoError(t, json.Unmarshal(r.Settings, &plan))
	return plan
}

// The newer version is published after the start read version 1 and before
// its transaction: the start refuses and writes nothing; the next start plans
// from the newer version.
func TestAuthorLLMResolveStartRefusesEvidencePublishedBeforeItsLock(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	evalRun := e.completedFullEval(t, true,
		[]llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	// Version 2 withdraws the restructure tier.
	published := false
	services.SetResolveStartPlannedHookForTest(t, func() {
		if !published {
			published = true
			e.publishReport(t, evalRun, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm")})
		}
	})
	_, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.ErrorIs(t, err, database.ErrAuthorLLMEvidenceChanged)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_run WHERE kind = 'resolve'`), "a refused start created a run")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'`),
		"a refused start registered a pair")

	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	id, sha := e.latestReport(t, evalRun)
	plan := e.resolvePlanOf(t, run.ID)
	assert.Equal(t, id, plan.ReportID)
	assert.Equal(t, sha, plan.ReportSHA256)
	assert.Equal(t, []string{"confirm"}, plan.Strata["initials.cyrl"].Tiers)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'
		AND evidence_report_id = ?`, id))
}

// The newer version is published while the start holds the claim lock: the
// recomputation waits for it, the start registers from the version it read —
// the latest at that moment — and the running resolve stays pinned to it
// after the newer one lands.
func TestAuthorLLMResolveRunStaysPinnedToEvidenceAfterItsLock(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	evalRun := e.completedFullEval(t, true,
		[]llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	firstID, firstSHA := e.latestReport(t, evalRun)
	done := make(chan struct{})
	database.TestHookResolveStartLocked = func() {
		database.TestHookResolveStartLocked = nil
		go func() {
			defer close(done)
			// Version 2 withdraws the stratum altogether.
			e.publishReport(t, evalRun, []llmeval.StratumDecision{{Stratum: "initials.cyrl"}})
		}()
		// Let the recomputation reach the claim lock and wait on it.
		deadline := time.Now().Add(10 * time.Second)
		for e.count(`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock'
			AND wait_event = 'advisory' AND datname = current_database()`) == 0 {
			require.True(t, time.Now().Before(deadline), "the recomputation never waited on the claim lock")
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Cleanup(func() { database.TestHookResolveStartLocked = nil })
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	<-done
	secondID, _ := e.latestReport(t, evalRun)
	require.NotEqual(t, firstID, secondID, "the recomputation published no version")

	plan := e.resolvePlanOf(t, run.ID)
	assert.Equal(t, firstID, plan.ReportID)
	assert.Equal(t, firstSHA, plan.ReportSHA256)
	registrations := e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval' AND evidence_report_id = ?`,
		firstID)
	assert.Equal(t, 1, registrations)

	// The running resolve goes on under its pinned plan and registration.
	e.resolveUntilDone(run.ID)
	assert.Equal(t, plan, e.resolvePlanOf(t, run.ID))
	assert.Equal(t, registrations, e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'`))
	sel := e.selections()
	for _, last := range initialsCyrl {
		assert.Equal(t, "selected", sel[last].State, last)
	}
}
