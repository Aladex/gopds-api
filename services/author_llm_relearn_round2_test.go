package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
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

// Review round 3 of the drift re-learn: the eval's report belongs to one
// generation of answers (a re-learn at the report stage, or one landing
// while the report is being computed, makes it wait for the re-asked
// answers), a re-learned eval hands its effective evidence to the resolve
// run, and a re-learn starts from the latest learned reference.

// evalAtReportStage starts an eval pilot and runs it up to the report
// stage, without computing the report yet.
func evalAtReportStage(t *testing.T, e *llmEnv, prompt *atomic.Int64) (int64, *services.AuthorLLMEvalDriver, *services.AuthorLLMWorker) {
	t.Helper()
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(switchingJudge(prompt, distinctLabs))
	ctx := context.Background()
	run, err := e.llmAdmin().Start(ctx, "eval", "pilot", e.adminUser())
	require.NoError(t, err)
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	w := e.worker()
	stepUntil(t, func() error { _, err := driver.Step(ctx); return err }, w, func() bool {
		stage, status := e.runStage(run.ID)
		return stage == "report" && status == string(models.AuthorLLMRunRunning)
	})
	return run.ID, driver, w
}

// driftJudgeB is what the periodic check would do: judge_b paused for drift.
// The provider now serves another, stable model.
func (e *llmEnv) driftJudgeB(prompt *atomic.Int64) {
	e.t.Helper()
	prompt.Store(950)
	_, err := e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'drift', paused_at = now(),
		check_run_id = NULL WHERE config_version = ? AND slot = 'judge_b'`, e.version)
	require.NoError(e.t, err)
}

// assertOneGeneration checks a completed eval counted no judge_b answer of
// the old model and left nothing to answer.
func (e *llmEnv) assertOneGeneration(run int64) {
	e.t.Helper()
	rows := e.judgeBRelearns(run)
	require.Len(e.t, rows, 1)
	require.Equal(e.t, "learned", rows[0].Status)
	assert.Zero(e.t, e.count(`SELECT count(*) FROM author_llm_job j
		JOIN author_llm_attempt a ON a.id = j.answer_attempt_id JOIN author_llm_call c ON c.id = a.call_id
		WHERE j.run_id = ? AND j.slot = 'judge_b' AND j.purpose <> 'eval_fingerprint' AND c.created_at < ?`,
		run, *rows[0].Finished), "an answer of the old model is counted")
	assert.Zero(e.t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND status IN ('pending', 'claimed')`, run),
		"the report was stored with answers still to come")
}

func TestAuthorLLMEvalReportWaitsForRelearnAtReportStage(t *testing.T) {
	e := newLLMEnv(t)
	var prompt atomic.Int64
	run, driver, w := evalAtReportStage(t, e, &prompt)
	ctx := context.Background()

	e.driftJudgeB(&prompt)
	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	_, err := driver.Step(ctx)
	require.NoError(t, err)
	_, status := e.runStage(run)
	assert.Equal(t, string(models.AuthorLLMRunRunning), status, "the report was stored while judge_b re-learns")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_eval_report WHERE run_id = ?`, run))

	stepUntil(t, func() error { _, stepErr := driver.Step(ctx); return stepErr }, w, func() bool {
		_, st := e.runStage(run)
		return st == string(models.AuthorLLMRunCompleted)
	})
	e.assertOneGeneration(run)
}

func TestAuthorLLMEvalReportRejectsSnapshotInvalidatedByRelearn(t *testing.T) {
	// The report has read the old answers when, before it is stored:
	//   - judge_b re-learns and every re-asked answer is already back (the
	//     store sees nothing pending, only another re-learn generation);
	//   - judge_b's periodic check fails and voids its unchecked answers
	//     (same generation, answers pending again).
	for name, relearn := range map[string]bool{"re-learned and re-asked": true, "drift voided answers": false} {
		t.Run(name, func(t *testing.T) { reportRejectsInvalidatedSnapshot(t, relearn) })
	}
}

func reportRejectsInvalidatedSnapshot(t *testing.T, relearn bool) {
	e := newLLMEnv(t)
	// Rare periodic checks: judge_b's answers since its start check are
	// unchecked, and a failing check voids them.
	e.settings.FingerprintEvery = 1000
	var prompt atomic.Int64
	run, driver, w := evalAtReportStage(t, e, &prompt)
	ctx := context.Background()
	var fired atomic.Bool
	services.SetEvalReportCollectedHookForTest(t, func() {
		if fired.Swap(true) {
			return
		}
		if !relearn {
			// judge_b's next check is due, and fails.
			prompt.Store(950)
			_, err := e.db.Exec(`UPDATE author_llm_provider_state SET answered_since_check = 2000
				WHERE config_version = ? AND slot = 'judge_b'`, e.version)
			require.NoError(t, err)
			for range 20 {
				if e.state("judge_b").PausedReason != nil {
					return
				}
				_, runErr := w.RunOnce(ctx)
				require.NoError(t, runErr)
			}
			t.Fatal("the drift never landed")
		}
		e.driftJudgeB(&prompt)
		require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
		for range 60 {
			if e.count(`SELECT count(*) FROM author_llm_relearn WHERE run_id = ? AND status = 'learned'`, run) > 0 &&
				e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND status IN ('pending', 'claimed')`, run) == 0 {
				return
			}
			_, runErr := w.RunOnce(ctx)
			require.NoError(t, runErr)
		}
		t.Fatal("the re-learn and its re-asks never finished")
	})
	_, err := driver.Step(ctx)
	require.NoError(t, err)
	require.True(t, fired.Load())
	_, status := e.runStage(run)
	assert.Equal(t, string(models.AuthorLLMRunRunning), status, "a report of the old answers was stored")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_eval_report WHERE run_id = ?`, run))

	if !relearn {
		require.Equal(t, "drift", deref(e.state("judge_b").PausedReason))
		require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	}
	stepUntil(t, func() error { _, stepErr := driver.Step(ctx); return stepErr }, w, func() bool {
		_, st := e.runStage(run)
		return st == string(models.AuthorLLMRunCompleted)
	})
	e.assertOneGeneration(run)
}

// Follow-up 1: a full eval whose judge_b was re-learned (another model id,
// another output mode) hands the re-learned evidence to the report's
// fingerprints and to the resolve run it backs; the original stays history.
func TestAuthorLLMFullEvalRelearnHandsItsEvidenceToResolve(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 2
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	// The provider switches judge_b: its fixed request counts 950 (the
	// periodic check catches it), and from the resume on it names another
	// model id and refuses json_schema.
	var switched, renamed atomic.Bool
	answer := byFields(distinctLabs)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if req.Model != "claude-opus-5" || !switched.Load() {
			return answer(req)
		}
		if renamed.Load() && responseFormat(req) == "json_schema" {
			return fakeReply{Status: http.StatusBadRequest, Body: formatRejected}
		}
		reply := answer(req)
		if renamed.Load() {
			reply.Model = "claude-opus-5-0901"
		}
		if isFingerprint(req) {
			reply.Prompt = 950
		}
		return reply
	})
	settings := services.DefaultAuthorLLMEvalSettings()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	w := e.worker()
	step := func() error { _, stepErr := driver.Step(ctx); return stepErr }
	stepUntil(t, step, w, func() bool {
		stage, _ := e.runStage(run)
		return stage == "judge" && e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b'
			AND purpose = 'eval_judge' AND status = 'answered'`, run) > 0
	})
	switched.Store(true)
	stepUntil(t, step, w, func() bool { return e.state("judge_b").PausedReason != nil })
	require.Equal(t, "drift", deref(e.state("judge_b").PausedReason))
	renamed.Store(true)
	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	stepUntil(t, step, w, func() bool {
		_, status := e.runStage(run)
		return status == string(models.AuthorLLMRunCompleted)
	})

	ev, err := database.LatestAuthorLLMFullEval(ctx, e.db, e.version)
	require.NoError(t, err)
	require.NotNil(t, ev)
	var reference map[string]database.AuthorLLMFingerprintReference
	require.NoError(t, json.Unmarshal(ev.Reference, &reference))
	var expected map[string][]string
	require.NoError(t, json.Unmarshal(ev.ExpectedModels, &expected))
	assert.Equal(t, int64(950), reference["judge_b"].PromptTokens)
	assert.Equal(t, "json_object", reference["judge_b"].OutputMode)
	assert.Equal(t, []string{"claude-opus-5-0901"}, expected["judge_b"])
	assert.Equal(t, int64(800), reference["production_b"].PromptTokens, "the others' evidence is the stage's")

	raw, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run)
	require.NoError(t, err)
	var doc struct {
		Fingerprints []llmeval.ParticipantFingerprint `json:"fingerprints"`
		Relearns     []struct {
			Old database.AuthorLLMFingerprintReference `json:"old_reference"`
		} `json:"fingerprint_relearns"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	for _, fp := range doc.Fingerprints {
		if fp.Participant == "judge_b" {
			assert.Equal(t, int64(950), fp.PromptTokens)
			assert.Equal(t, []string{"claude-opus-5-0901"}, fp.Models)
		}
	}
	require.Len(t, doc.Relearns, 1)
	assert.Equal(t, int64(900), doc.Relearns[0].Old.PromptTokens, "the original stays as history")

	// The resolve run started from this eval checks judge_b against what
	// the eval last measured.
	resolve, _, err := database.StartAuthorLLMResolveRun(ctx, e.db, &database.AuthorLLMResolveStart{
		ConfigVersion: e.version, Mode: models.AuthorLLMEvalModePilot, Evidence: ev, CreatedBy: e.adminUser(),
		Settings: database.AuthorLLMResolveSettings{ReportID: ev.ReportID, Strata: map[string]database.AuthorLLMResolveStratum{
			"initials.cyrl": {Class: "initials", Script: "Cyrl", Arm: "A", ItemsPerRequest: 1, Tiers: []string{"confirm"}}}},
	})
	require.NoError(t, err)
	r, err := database.GetAuthorLLMRun(ctx, e.db, resolve)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(r.Reference, &reference))
	require.NoError(t, json.Unmarshal(r.ExpectedModels, &expected))
	assert.Equal(t, int64(950), reference["judge_b"].PromptTokens)
	assert.Equal(t, "json_object", reference["judge_b"].OutputMode)
	assert.Equal(t, []string{"claude-opus-5-0901"}, expected["judge_b"])
}

// Follow-up 2: after a learned re-learn and a later one that did not
// settle, the participant's current reference is the learned one — the next
// re-learn replaces it, and the others' lab checks see it.
func TestAuthorLLMRelearnStartsFromTheLatestLearnedReference(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm")}, nil)
	var prodB atomic.Int64
	answer := byFields(nil)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		reply := answer(req)
		if isFingerprint(req) && req.Model == "deepseek-v4-pro" && prodB.Load() != 0 {
			reply.Prompt = prodB.Load()
		}
		return reply
	})
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	// judge_b's history: learned 827, then an attempt that did not settle.
	_, err = e.db.Exec(`
		INSERT INTO author_llm_relearn (run_id, config_version, slot, status, repeats, reasoning, output_mode,
			old_reference, new_reference, models, reason, started_at, finished_at)
		VALUES (?0, ?1, 'judge_b', 'learned', 3, false, 'json_schema', '{"prompt_tokens": 777, "output_mode": "json_schema"}',
				'{"prompt_tokens": 827, "output_mode": "json_schema"}', '["claude-opus-5"]', NULL,
				now() - interval '2 minutes', now() - interval '110 seconds'),
			(?0, ?1, 'judge_b', 'unstable', 3, false, 'json_schema', '{"prompt_tokens": 827, "output_mode": "json_schema"}',
				NULL, NULL, 'unstable: judge_b prompt tokens 830 and 831', now() - interval '1 minute',
				now() - interval '50 seconds')`, run.ID, e.version)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = CASE slot WHEN 'judge_b' THEN 'relearn_unstable'
		ELSE 'drift' END, paused_at = now() WHERE config_version = ? AND slot IN ('judge_b', 'production_b')`, e.version)
	require.NoError(t, err)

	// production_b moves to judge_b's learned lab: the re-learn refuses it.
	prodB.Store(827)
	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "production_b"))
	w := e.worker()
	for range 30 {
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		if e.count(`SELECT count(*) FROM author_llm_relearn WHERE run_id = ? AND slot = 'production_b'
			AND status <> 'relearning'`, run.ID) > 0 {
			break
		}
	}
	var status, reason string
	_, err = e.db.QueryOne(pg.Scan(&status, &reason), `SELECT status, coalesce(reason, '') FROM author_llm_relearn
		WHERE run_id = ? AND slot = 'production_b'`, run.ID)
	require.NoError(t, err)
	assert.Equal(t, "unstable", status)
	assert.Contains(t, reason, "same_lab")

	// judge_b's next re-learn replaces the learned 827, not the run's 777.
	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	rows := e.judgeBRelearns(run.ID)
	require.Len(t, rows, 3)
	assert.Equal(t, "relearning", rows[2].Status)
	assert.Equal(t, int64(827), promptOf(t, rows[2].Old))
}
