package services_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A completed eval's report is computed again from its stored answers when
// the gate code changes: a new version, the old one kept, and the resolve
// run registers from the latest. The fixture is far too small for the real
// gate to pass a stratum, so the gate code is stood in for: the older one
// is the real gate plus the run-wide invalid reason run 14 was stopped by,
// the newer one the real gate deciding initials.cyrl passed.

func olderGate(in *llmeval.Input) llmeval.Report {
	r := llmeval.Evaluate(in)
	r.Decision.Registrable = false
	r.Decision.Reasons = append(r.Decision.Reasons, "invalid: judge_b 2590 of 14837")
	return r
}

func newerGate(in *llmeval.Input) llmeval.Report {
	r := llmeval.Evaluate(in)
	r.Decision.Registrable, r.Decision.Reasons = true, nil
	for i := range r.Decision.Strata {
		if d := &r.Decision.Strata[i]; d.Stratum == "initials.cyrl" {
			*d = llmeval.StratumDecision{Stratum: d.Stratum, Passed: true, Arm: llmeval.ArmA, ItemsPerRequest: 1,
				Tiers: []string{"confirm"}}
		}
	}
	return r
}

// completedFullEval runs a full eval to its end with the gate the test set.
func completedFullEval(t *testing.T, e *llmEnv) int64 {
	t.Helper()
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	w := e.worker()
	stepUntil(t, func() error { _, stepErr := driver.Step(ctx); return stepErr }, w, func() bool {
		_, status := e.runStage(run)
		return status == string(models.AuthorLLMRunCompleted)
	})
	return run
}

// reportVersion reads one version of a run's report as downloaded, checks
// its digest, and returns it as a map with its digest.
func reportVersion(t *testing.T, e *llmEnv, run int64, version int) (doc map[string]json.RawMessage, sha string) {
	t.Helper()
	raw, sha, err := services.AuthorLLMEvalReportVersionFile(context.Background(), e.db, run, version)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(sum[:]), sha, "the digest is not the downloaded bytes'")
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc, sha
}

func TestAuthorLLMEvalReportRecomputedFromStoredAnswers(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	admin := e.llmAdmin()
	services.SetEvalGateForTest(t, olderGate)
	run := completedFullEval(t, e)

	v1, sha1 := reportVersion(t, e, run, 0)
	assert.JSONEq(t, `1`, string(v1["report_version"]))
	_, err := admin.Start(ctx, string(models.AuthorLLMRunResolve), models.AuthorLLMEvalModePilot, e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite, "the older gate registers nothing")
	calls := e.count(`SELECT count(*) FROM author_llm_call`)
	jobs := e.count(`SELECT count(*) FROM author_llm_job`)

	// The same gate computes the same report: nothing is stored.
	state, err := admin.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	require.NotNil(t, state.Report)
	assert.Equal(t, 1, state.Report.Version)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_eval_report WHERE run_id = ?`, run))

	// A stratum frozen after the run is not the run's: the recomputation
	// measures the sets the stored report measured.
	require.NoError(t, database.FreezeAuthorLLMEvalSet(ctx, e.db, run, "v1.s.mononym.cyrl", make([]byte, 32),
		[]database.AuthorLLMNewEvalItem{{ItemSet: models.AuthorLLMEvalHoldout, Stratum: "mononym.cyrl",
			SourceFingerprint: make([]byte, 32), ExtractorVersion: "fixture", Input: json.RawMessage(`{}`)}}))
	_, err = admin.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_eval_report WHERE run_id = ?`, run),
		"a set frozen later changed the report")

	// The newer gate: a second version that supersedes the first.
	services.SetEvalGateForTest(t, newerGate)
	state, err = admin.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	require.NotNil(t, state.Report)
	assert.Equal(t, 2, state.Report.Version)
	assert.True(t, state.Report.Registrable)
	require.Len(t, state.Report.Versions, 2)
	assert.Equal(t, sha1, state.Report.Versions[0].SHA256, "the first version is kept unchanged")
	assert.False(t, state.Report.Versions[0].Registrable)
	assert.True(t, state.Report.Versions[1].Registrable)
	assert.Equal(t, calls, e.count(`SELECT count(*) FROM author_llm_call`), "a recomputation called a model")
	assert.Equal(t, jobs, e.count(`SELECT count(*) FROM author_llm_job`), "a recomputation enqueued work")

	v2, sha2 := reportVersion(t, e, run, 0)
	assert.Equal(t, sha2, state.Report.SHA256)
	assert.JSONEq(t, `2`, string(v2["report_version"]))
	var firstID int64
	_, err = e.db.QueryOne(pg.Scan(&firstID), `SELECT id FROM author_llm_eval_report WHERE run_id = ? AND version = 1`, run)
	require.NoError(t, err)
	var supersedes struct {
		ID      int64  `json:"id"`
		Version int    `json:"version"`
		SHA256  string `json:"sha256"`
	}
	require.NoError(t, json.Unmarshal(v2["supersedes"], &supersedes))
	assert.Equal(t, firstID, supersedes.ID)
	assert.Equal(t, 1, supersedes.Version)
	assert.Equal(t, sha1, supersedes.SHA256)
	// Only the evaluation is computed again; the rest is the stored one.
	for key, value := range v1 {
		if key == "evaluation" || key == "report_version" {
			continue
		}
		assert.JSONEq(t, string(value), string(v2[key]), "key %s changed", key)
	}
	assert.NotEqual(t, string(v1["evaluation"]), string(v2["evaluation"]))
	again, againSHA := reportVersion(t, e, run, 1)
	assert.Equal(t, sha1, againSHA, "the first version is still downloadable as it was")
	assert.JSONEq(t, string(v1["evaluation"]), string(again["evaluation"]))

	// Idempotent: the newer gate again stores nothing.
	_, err = admin.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_eval_report WHERE run_id = ?`, run))

	// The resolve run registers from the latest version.
	started, err := admin.Start(ctx, string(models.AuthorLLMRunResolve), models.AuthorLLMEvalModePilot, e.adminUser())
	require.NoError(t, err)
	var evidence struct {
		ID  int64  `pg:"evidence_report_id"`
		SHA []byte `pg:"evidence_report_sha256"`
	}
	_, err = e.db.QueryOne(&evidence, `SELECT evidence_report_id, evidence_report_sha256 FROM author_acceptance_class
		WHERE config_version = ? AND source = 'llm_eval' AND script = 'Cyrl'`, e.version)
	require.NoError(t, err)
	var secondID int64
	_, err = e.db.QueryOne(pg.Scan(&secondID), `SELECT id FROM author_llm_eval_report WHERE run_id = ? AND version = 2`, run)
	require.NoError(t, err)
	assert.Equal(t, secondID, evidence.ID)
	assert.Equal(t, sha2, hex.EncodeToString(evidence.SHA))
	r, err := database.GetAuthorLLMRun(ctx, e.db, started.ID)
	require.NoError(t, err)
	assert.Contains(t, string(r.Settings), sha2, "the resolve run rests on the latest version")

	// A resolve run has no eval report to recompute.
	_, err = admin.RecomputeEvalReport(ctx, started.ID)
	assert.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun)
}

// Refused: a run of another configuration, a run that is not completed, and
// — under the store's own fence — a run whose answers changed since they
// were read.
func TestAuthorLLMEvalReportRecomputeRefusals(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	run := completedFullEval(t, e)

	other := services.NewAuthorLLMAdmin(e.db, "llm1-another-configuration", "", &e.settings)
	_, err := other.RecomputeEvalReport(ctx, run)
	assert.ErrorIs(t, err, services.ErrAuthorLLMRunConfigChanged)

	settings := services.DefaultAuthorLLMEvalSettings()
	running, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	_, err = e.llmAdmin().RecomputeEvalReport(ctx, running)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState)
	require.NoError(t, database.PauseAuthorLLMRun(ctx, e.db, running, models.AuthorLLMRunPausedAdmin))
	_, err = e.llmAdmin().Close(ctx, running, e.adminUser())
	require.NoError(t, err)
	_, err = e.llmAdmin().RecomputeEvalReport(ctx, running)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState)

	// The store checks again under the lock: the configuration, the run's
	// state and the re-learn generation the report was computed from.
	report := []byte(`{"evaluation": {}}`)
	_, _, err = database.RecomputeAuthorLLMEvalReport(ctx, e.db, run, "llm1-another-configuration", report, 0)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState)
	_, _, err = database.RecomputeAuthorLLMEvalReport(ctx, e.db, running, e.version, report, 0)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState)
	_, _, err = database.RecomputeAuthorLLMEvalReport(ctx, e.db, run, e.version, report, 1)
	assert.ErrorIs(t, err, database.ErrAuthorLLMReportStale)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_eval_report WHERE run_id = ?`, run))
	latest, stored, err := database.RecomputeAuthorLLMEvalReport(ctx, e.db, run, e.version, report, 0)
	require.NoError(t, err)
	assert.True(t, stored)
	assert.Equal(t, 2, latest.Version)
}

// The report the real gate computes carries the per-stratum invalid shares
// and the pooled repeat, read from the run's jobs.
func TestAuthorLLMEvalReportShowsTheStratumGates(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	var input llmeval.Input
	services.SetEvalGateForTest(t, func(in *llmeval.Input) llmeval.Report {
		input = *in
		return llmeval.Evaluate(in)
	})
	run := completedFullEval(t, e)

	// The pooled gate's evidence is on the items: every repeat the run
	// counted is on its item, reproduced as counted.
	require.NotEmpty(t, input.Repeat)
	for _, c := range input.Repeat {
		total, reproduced := 0, 0
		for _, it := range input.Items {
			if same, ok := it.Repeated[c.Participant]; ok {
				total++
				if same {
					reproduced++
				}
			}
		}
		assert.Equal(t, c.Total, total, c.Participant)
		assert.Equal(t, c.Matched, reproduced, c.Participant)
	}

	doc, _ := reportVersion(t, e, run, 0)
	var ev llmeval.Report
	require.NoError(t, json.Unmarshal(doc["evaluation"], &ev))
	s, ok := ev.Strata["initials.cyrl"]
	require.True(t, ok)
	require.Len(t, s.JudgeInvalid, 2)
	for _, j := range s.JudgeInvalid {
		assert.Equal(t, s.Holdout, j.Total, "%s answered every measured item", j.Participant)
		assert.Zero(t, j.Invalid)
		assert.True(t, j.Passed)
	}
	for key, m := range s.Combos {
		require.Len(t, m.Invalid, 2, key)
		for _, p := range m.Invalid {
			assert.Equal(t, s.Holdout, p.Total, "%s %s", key, p.Participant)
			assert.True(t, p.Passed)
		}
		assert.True(t, m.InvalidPassed, key)
	}
	// No stratum passed at fixture size: the pooled set is empty and gives
	// no reason of its own.
	assert.Empty(t, ev.PooledRepeat.Strata)
	for _, reason := range ev.Decision.Reasons {
		assert.NotContains(t, reason, "pooled")
	}
}
