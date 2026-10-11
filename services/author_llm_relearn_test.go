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
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A participant the provider switches mid-run (another model behind the
// same name: another prompt token count for the fixed request) is paused for
// drift by its periodic check. Resuming it re-learns its fingerprint in the
// current run — the fixed request repeated until the calls agree, the output
// mode negotiated — and its answers of the old fingerprint are never counted:
// an eval re-asks all of them, a resolve run re-asks those its last passed
// check did not cover. Prod, eval runs 7 and 8: judge_b went 1013 → 5950 →
// 449 in an hour, and resuming re-checked against the old reference forever.

// switchingJudge answers byFields with the eval fixture's distinct labs,
// except that judge_b's fixed request counts *prompt tokens (0 means the
// fixture's own count).
func switchingJudge(prompt *atomic.Int64, base map[string]int64) func(req *fakeRequest) fakeReply {
	answer := byFields(base)
	return func(req *fakeRequest) fakeReply {
		reply := answer(req)
		if isFingerprint(req) && req.Model == "claude-opus-5" {
			if p := prompt.Load(); p != 0 {
				reply.Prompt = p
			}
		}
		return reply
	}
}

// relearnRow is a participant's re-learned fingerprint as the run keeps it.
type relearnRow struct {
	Status     string          `pg:"status"`
	OutputMode string          `pg:"output_mode"`
	Old        json.RawMessage `pg:"old_reference"`
	New        json.RawMessage `pg:"new_reference"`
	Reason     *string         `pg:"reason"`
	Started    time.Time       `pg:"started_at"`
	Finished   *time.Time      `pg:"finished_at"`
}

// judgeBRelearns are judge_b's re-learns in the run, oldest first.
func (e *llmEnv) judgeBRelearns(run int64) []relearnRow {
	e.t.Helper()
	var rows []relearnRow
	_, err := e.db.Query(&rows, `SELECT status, output_mode, old_reference, new_reference, reason, started_at,
		finished_at FROM author_llm_relearn WHERE run_id = ? AND slot = 'judge_b' ORDER BY id`, run)
	require.NoError(e.t, err)
	return rows
}

func promptOf(t *testing.T, ref json.RawMessage) int64 {
	t.Helper()
	var r database.AuthorLLMFingerprintReference
	require.NoError(t, json.Unmarshal(ref, &r))
	return r.PromptTokens
}

// stepUntil runs the driver step and the worker until done says so.
func stepUntil(t *testing.T, step func() error, w *services.AuthorLLMWorker, done func() bool) {
	t.Helper()
	for range 400 {
		require.NoError(t, step())
		_, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		if done() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("the condition never held")
}

func TestAuthorLLMEvalJudgeDriftRelearnsAndReasksEveryAnswer(t *testing.T) {
	// In the judge stage, and in the last stage before the report: the
	// stage must wait for the re-asked answers of the earlier ones.
	for stage, purpose := range map[string]string{"judge": "eval_judge", "repeat": "eval_repeat"} {
		t.Run(stage, func(t *testing.T) { evalJudgeDriftRelearns(t, stage, purpose) })
	}
}

func evalJudgeDriftRelearns(t *testing.T, switchStage, purpose string) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 2
	ingestEvalCatalogue(t, e)
	var prompt atomic.Int64
	e.fake.setRespond(switchingJudge(&prompt, distinctLabs))
	ctx := context.Background()
	run, err := e.llmAdmin().Start(ctx, "eval", "pilot", e.adminUser())
	require.NoError(t, err)
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	w := e.worker()
	step := func() error { _, stepErr := driver.Step(ctx); return stepErr }

	// judge_b is answering the stage when the provider switches it.
	stepUntil(t, step, w, func() bool {
		stage, _ := e.runStage(run.ID)
		return stage == switchStage && e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b'
			AND purpose = ? AND status = 'answered'`, run.ID, purpose) > 0
	})
	prompt.Store(950)
	stepUntil(t, step, w, func() bool { return e.state("judge_b").PausedReason != nil })
	require.Equal(t, "drift", deref(e.state("judge_b").PausedReason))

	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	stepUntil(t, step, w, func() bool {
		_, status := e.runStage(run.ID)
		return status == string(models.AuthorLLMRunCompleted)
	})

	rows := e.judgeBRelearns(run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, "learned", rows[0].Status)
	assert.Equal(t, int64(900), promptOf(t, rows[0].Old))
	assert.Equal(t, int64(950), promptOf(t, rows[0].New))
	require.NotNil(t, rows[0].Finished)
	// Every judge_b answer the report counts came after the re-learn; the
	// other participants were not asked again.
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job j
		JOIN author_llm_attempt a ON a.id = j.answer_attempt_id JOIN author_llm_call c ON c.id = a.call_id
		WHERE j.run_id = ? AND j.slot = 'judge_b' AND j.purpose <> 'eval_fingerprint' AND c.created_at < ?`,
		run.ID, *rows[0].Finished), "an answer of the old fingerprint is counted")
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job j
		JOIN author_llm_attempt a ON a.id = j.answer_attempt_id JOIN author_llm_call c ON c.id = a.call_id
		WHERE j.run_id = ? AND j.slot = 'production_a' AND c.created_at < ?`, run.ID, rows[0].Started),
		"the other participants' answers stay")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND status IN ('pending', 'claimed')`, run.ID))

	// The report keeps the history: the old and the new reference, and when.
	raw, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run.ID)
	require.NoError(t, err)
	var doc struct {
		Relearns []struct {
			Participant string                                  `json:"participant"`
			Status      string                                  `json:"status"`
			Old         database.AuthorLLMFingerprintReference  `json:"old_reference"`
			New         *database.AuthorLLMFingerprintReference `json:"new_reference"`
			StartedAt   time.Time                               `json:"started_at"`
		} `json:"fingerprint_relearns"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Len(t, doc.Relearns, 1)
	assert.Equal(t, "judge_b", doc.Relearns[0].Participant)
	assert.Equal(t, int64(900), doc.Relearns[0].Old.PromptTokens)
	require.NotNil(t, doc.Relearns[0].New)
	assert.Equal(t, int64(950), doc.Relearns[0].New.PromptTokens)
}

func TestAuthorLLMResolveJudgeDriftRelearnsTheNewModel(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 3
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var prompt atomic.Int64
	e.fake.setRespond(switchingJudge(&prompt, nil))
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	d, w := e.resolveDriver(), e.worker()
	step := func() error { _, stepErr := d.Step(ctx); return stepErr }
	stepUntil(t, step, w, func() bool {
		return e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b' AND status = 'answered'`,
			run.ID) > 0
	})
	prompt.Store(checkPrompt + 50) // another model, stable from now on
	stepUntil(t, step, w, func() bool { return e.state("judge_b").PausedReason != nil })
	require.Equal(t, "drift", deref(e.state("judge_b").PausedReason))
	lastPass := e.state("judge_b").LastCheckPassedAt

	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	e.resolveUntilDone(run.ID)
	rows := e.judgeBRelearns(run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, "learned", rows[0].Status)
	assert.Equal(t, int64(checkPrompt+50), promptOf(t, rows[0].New))
	assert.Nil(t, e.state("judge_b").PausedReason)
	// No judgement of the old model's unchecked answers was applied.
	if lastPass != nil {
		assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND result_id IS NOT NULL
			AND judged_b_at > ? AND judged_b_at < ?`, run.ID, *lastPass, *rows[0].Finished))
	}
	for _, last := range initialsCyrl {
		assert.Equal(t, "selected", e.selections()[last].State, last)
	}
}

func TestAuthorLLMRelearnThatDoesNotSettleStaysPaused(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 3
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var prompt atomic.Int64
	var flip atomic.Bool
	answer := switchingJudge(&prompt, nil)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		reply := answer(req)
		if isFingerprint(req) && req.Model == "claude-opus-5" && flip.Load() {
			// The provider is still moving: every count differs.
			reply.Prompt = checkPrompt + 50 + prompt.Add(1)
		}
		return reply
	})
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	d, w := e.resolveDriver(), e.worker()
	step := func() error { _, stepErr := d.Step(ctx); return stepErr }
	stepUntil(t, step, w, func() bool {
		return e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b' AND status = 'answered'`,
			run.ID) > 0
	})
	flip.Store(true)
	stepUntil(t, step, w, func() bool { return e.state("judge_b").PausedReason != nil })
	answered := e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b' AND status = 'answered'`,
		run.ID)

	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	stepUntil(t, step, w, func() bool { return e.state("judge_b").PausedReason != nil })
	assert.Equal(t, string(models.AuthorLLMPausedRelearnUnstable), deref(e.state("judge_b").PausedReason))
	rows := e.judgeBRelearns(run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, "unstable", rows[0].Status)
	assert.Contains(t, deref(rows[0].Reason), "unstable")
	assert.Equal(t, answered, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b'
		AND status = 'answered'`, run.ID), "an unstable re-learn voids nothing")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND last_error_class = 'relearned'`, run.ID))

	// The provider settles: resuming again learns it.
	flip.Store(false)
	prompt.Store(checkPrompt + 70)
	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	e.resolveUntilDone(run.ID)
	rows = e.judgeBRelearns(run.ID)
	require.Len(t, rows, 2)
	assert.Equal(t, "learned", rows[1].Status)
	assert.Equal(t, int64(checkPrompt+70), promptOf(t, rows[1].New))
}

func TestAuthorLLMRelearnNegotiatesTheOutputMode(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 3
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var prompt atomic.Int64
	var rejecting atomic.Bool
	answer := switchingJudge(&prompt, nil)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if req.Model == "claude-opus-5" && rejecting.Load() && responseFormat(req) == "json_schema" {
			return fakeReply{Status: http.StatusBadRequest, Body: formatRejected}
		}
		return answer(req)
	})
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	d, w := e.resolveDriver(), e.worker()
	step := func() error { _, stepErr := d.Step(ctx); return stepErr }
	stepUntil(t, step, w, func() bool {
		return e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b' AND status = 'answered'`,
			run.ID) > 0
	})
	// The provider moved judge_b to a model that refuses json_schema.
	prompt.Store(checkPrompt + 50)
	stepUntil(t, step, w, func() bool { return e.state("judge_b").PausedReason != nil })
	rejecting.Store(true)
	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	e.resolveUntilDone(run.ID)
	rows := e.judgeBRelearns(run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, "learned", rows[0].Status)
	assert.Equal(t, "json_object", rows[0].OutputMode)
	var ref database.AuthorLLMFingerprintReference
	require.NoError(t, json.Unmarshal(rows[0].New, &ref))
	assert.Equal(t, "json_object", ref.OutputMode)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b'
		AND status = 'answered' AND output_mode = 'json_schema' AND finished_at > ?`, run.ID, rows[0].Started),
		"judge_b asked in the rejected mode after the re-learn")
	for _, last := range initialsCyrl {
		assert.Equal(t, "selected", e.selections()[last].State, last)
	}
}

// A drift pause belongs to the run that saw it: a new run learns its own
// fingerprints, so starting one lifts the drift (and unstable re-learn)
// pauses an older run left.
func TestAuthorLLMNewRunLiftsDriftPauses(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	_, err := e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = CASE slot WHEN 'judge_b' THEN 'drift'
		ELSE 'relearn_unstable' END, paused_at = now() WHERE config_version = ? AND slot IN ('judge_b', 'judge_a')`, e.version)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'auth', paused_at = now()
		WHERE config_version = ? AND slot = 'production_a'`, e.version)
	require.NoError(t, err)
	_, err = e.llmAdmin().Start(ctx, "eval", "pilot", e.adminUser())
	require.NoError(t, err)
	assert.Nil(t, e.state("judge_b").PausedReason)
	assert.Nil(t, e.state("judge_a").PausedReason)
	assert.Equal(t, "auth", deref(e.state("production_a").PausedReason), "only the run-bound pauses are lifted")
}

// Resolve's start lifts them too.
func TestAuthorLLMResolveStartLiftsDriftPauses(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm")}, nil)
	_, err := e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'drift', paused_at = now()
		WHERE config_version = ? AND slot = 'judge_b'`, e.version)
	require.NoError(t, err)
	_, err = e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	assert.Nil(t, e.state("judge_b").PausedReason)
}

// Without an active run there is nothing to re-learn in: the resume only
// lifts the pause.
func TestAuthorLLMResumeWithoutARunOnlyLifts(t *testing.T) {
	e := newLLMEnv(t)
	_, err := e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'drift', paused_at = now()
		WHERE config_version = ? AND slot = 'judge_b'`, e.version)
	require.NoError(t, err)
	require.NoError(t, e.llmAdmin().ResumeParticipant(context.Background(), "judge_b"))
	assert.Nil(t, e.state("judge_b").PausedReason)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_relearn`))
}

// A judge_b call sent before the drift is answered by the old model. Whether
// it settles after the drift check (before the resume) or is still open at
// the resume, its answer never counts: the re-learn voids the first, the
// resume fences the second, and the input is asked again.
func TestAuthorLLMRelearnNeverKeepsAnAnswerSentBeforeIt(t *testing.T) {
	for name, settleBeforeResume := range map[string]bool{"settled before the resume": true, "open at the resume": false} {
		t.Run(name, func(t *testing.T) {
			e := newLLMEnv(t)
			e.settings.FingerprintEvery = 1
			ingestEvalCatalogue(t, e)
			ctx := context.Background()
			e.completedFullEval(t, true,
				[]llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
			var prompt atomic.Int64
			e.fake.setRespond(switchingJudge(&prompt, nil))
			run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
			require.NoError(t, err)
			d, w := e.resolveDriver(), e.worker()
			_, err = d.Step(ctx) // enqueue
			require.NoError(t, err)
			drain := func(slot llmreq.Slot) {
				for range 200 {
					claim, _, claimErr := w.Claim(ctx, slot)
					require.NoError(t, claimErr)
					if claim == nil {
						return
					}
					require.NoError(t, w.Process(ctx, slot, claim))
				}
			}
			drain(llmreq.SlotProductionA)
			drain(llmreq.SlotProductionB)
			require.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b'`, run.ID))
			// judge_b's start check passes; then a job call A goes out.
			require.Equal(t, models.AuthorLLMCallFingerprint, claimProcess(t, w, llmreq.SlotJudgeB).Kind)
			early, _, err := w.Claim(ctx, llmreq.SlotJudgeB)
			require.NoError(t, err)
			require.NotNil(t, early)
			require.Equal(t, models.AuthorLLMCallJobs, early.Kind)
			// The provider switches; another job and the due check catch it.
			prompt.Store(checkPrompt + 50)
			require.Equal(t, models.AuthorLLMCallJobs, claimProcess(t, w, llmreq.SlotJudgeB).Kind)
			require.Equal(t, models.AuthorLLMCallFingerprint, claimProcess(t, w, llmreq.SlotJudgeB).Kind)
			require.Equal(t, "drift", deref(e.state("judge_b").PausedReason))
			if settleBeforeResume {
				require.NoError(t, w.Process(ctx, llmreq.SlotJudgeB, early))
			}
			require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
			if !settleBeforeResume {
				// The re-learn finishes first; the fenced call settles after
				// it, and changes nothing.
				for range 10 {
					if e.count(`SELECT count(*) FROM author_llm_relearn WHERE run_id = ? AND status = 'learned'`,
						run.ID) > 0 {
						break
					}
					claimProcess(t, w, llmreq.SlotJudgeB)
				}
				require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_relearn WHERE run_id = ? AND status = 'learned'`,
					run.ID))
				_ = w.Process(ctx, llmreq.SlotJudgeB, early)
			}
			e.resolveUntilDone(run.ID)
			assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job j
				JOIN author_llm_attempt a ON a.id = j.answer_attempt_id WHERE a.call_id = ?`, early.CallID),
				"an answer of the call sent before the drift counts")
			for _, last := range initialsCyrl {
				assert.Equal(t, "selected", e.selections()[last].State, last)
			}
		})
	}
}
