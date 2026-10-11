package services_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A judge whose fingerprint check fails after it answered (another model
// behind the same name) confirms nothing: the judgements its unchecked
// answers made are voided, it is paused for drift, and nothing they
// confirmed is applied. Once resumed it answers again, its new check passes,
// and the confirmed answers are applied. The production pair's drift keeps
// its old behavior: the verdict is held_drift history and never applied.

func TestAuthorLLMResolveJudgeDriftConfirmsNothing(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 3 // a judge's check after every three answers
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	answer := byFields(nil)
	var drifted atomic.Bool
	checks := atomic.Int32{}
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		reply := answer(req)
		if isFingerprint(req) && req.Model == "claude-opus-5" && checks.Add(1) > 1 {
			// Every check of judge_b after its first: another tokenizer, and
			// after the resume a third model, stable — the re-learn learns it.
			reply.Prompt = checkPrompt + 50
			if drifted.Load() {
				reply.Prompt = checkPrompt + 90
			}
		}
		return reply
	})
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)

	d, w := e.resolveDriver(), e.worker()
	for range 200 {
		_, err = d.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		if p := e.state("judge_b").PausedReason; p != nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	require.Equal(t, string(models.AuthorLLMPausedDrift), deref(e.state("judge_b").PausedReason))
	// Nothing judge_b answered after its last passed check is applied.
	last := e.state("judge_b").LastCheckPassedAt
	require.NotNil(t, last)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND result_id IS NOT NULL
		AND judged_b_at > ?`, run.ID, *last), "an answer of the drifted judge confirmed an application")
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND verdict = 'agree'
		AND judgement IS NULL`, run.ID), "the voided answers' judgements are voided")
	for range 20 {
		_, err = d.Step(ctx)
		require.NoError(t, err)
	}
	_, status := e.runStage(run.ID)
	assert.Equal(t, "running", status, "the run waits for the paused judge")

	// The right model is back: the judge is resumed, checked again and its
	// answers count.
	drifted.Store(true)
	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	e.resolveUntilDone(run.ID)
	for _, last := range initialsCyrl {
		assert.Equal(t, "selected", e.selections()[last].State, last)
	}
}

// A judge that cannot answer (paused) holds the agreed answers: nothing is
// confirmed without the judges' judgement, nothing is applied, and the run
// waits. Resumed, the judge answers and the answers are applied.
func TestAuthorLLMResolveWaitsForTheJudges(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'auth', paused_at = now()
		WHERE config_version = ? AND slot = 'judge_b'`, e.version)
	require.NoError(t, err)
	d, w := e.resolveDriver(), e.worker()
	for range 60 {
		_, err = d.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		time.Sleep(15 * time.Millisecond)
	}
	assert.Equal(t, 7, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND verdict = 'agree'`, run.ID))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND confirmed_at IS NOT NULL`, run.ID),
		"an agreed answer was confirmed without the judges")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND settled_at IS NOT NULL`, run.ID))
	_, status := e.runStage(run.ID)
	assert.Equal(t, "running", status)

	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	e.resolveUntilDone(run.ID)
	for _, last := range initialsCyrl {
		assert.Equal(t, "selected", e.selections()[last].State, last)
	}
}

func TestAuthorLLMResolvePairDriftIsNeverApplied(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 3
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	answer := byFields(nil)
	var fixed atomic.Bool
	checks := atomic.Int32{}
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		reply := answer(req)
		if isFingerprint(req) && req.Model == "deepseek-v4-pro" && checks.Add(1) > 1 {
			reply.Prompt = checkPrompt + 50
			if fixed.Load() {
				// After the resume, a stable model the re-learn learns.
				reply.Prompt = checkPrompt + 90
			}
		}
		return reply
	})
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	d, w := e.resolveDriver(), e.worker()
	for range 200 {
		_, err = d.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		if e.state("production_b").PausedReason != nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	require.Equal(t, string(models.AuthorLLMPausedDrift), deref(e.state("production_b").PausedReason))
	held := e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND verdict = 'held_drift'`, run.ID)
	require.Positive(t, held)
	fixed.Store(true)
	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "production_b"))
	e.resolveUntilDone(run.ID)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND verdict = 'held_drift'
		AND (settled_at IS NOT NULL OR result_id IS NOT NULL)`, run.ID), "a held_drift verdict was applied")
	for _, last := range initialsCyrl {
		assert.Equal(t, "selected", e.selections()[last].State, last)
	}
}

// Migration 34's verdict shape: a judgement belongs to an agreed answer, a
// settled verdict was confirmed and is either applied (result and policy) or
// carries a reason, and what was answered stays as it was.
func TestAuthorLLMVerdictKeepsItsShape(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	e.fake.setRespond(pairDiffers())
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	disagreed := e.count(`SELECT id FROM author_llm_verdict WHERE run_id = ? AND verdict = 'disagree'`, run.ID)
	_, err = e.db.Exec(`UPDATE author_llm_verdict SET judgement = 'confirmed', judged_a_at = now(), judged_b_at = now()
		WHERE id = ?`, disagreed)
	assert.Error(t, err, "a judgement of a disagreement")
	applied := e.count(`SELECT id FROM author_llm_verdict WHERE run_id = ? AND result_id IS NOT NULL LIMIT 1`, run.ID)
	for name, stmt := range map[string]string{
		"judgement without a time": `UPDATE author_llm_verdict SET judged_a_at = NULL WHERE id = ?`,
		"settled unconfirmed":      `UPDATE author_llm_verdict SET confirmed_at = NULL WHERE id = ?`,
		"applied without a policy": `UPDATE author_llm_verdict SET policy_version = NULL WHERE id = ?`,
		"applied with a reason":    `UPDATE author_llm_verdict SET outcome_reason = 'llm_refused' WHERE id = ?`,
		"an unknown reason": `UPDATE author_llm_verdict SET result_id = NULL, policy_version = NULL,
			outcome_reason = 'other' WHERE id = ?`,
		"the answer's tier": `UPDATE author_llm_verdict SET tier = 'case' WHERE id = ?`,
	} {
		_, execErr := e.db.Exec(stmt, applied)
		assert.Error(t, execErr, name)
	}
	_, err = e.db.Exec(`UPDATE author_llm_verdict SET result_id = NULL, policy_version = NULL,
		outcome_reason = 'apply_failed' WHERE id = ?`, applied)
	assert.NoError(t, err, "a settled verdict's outcome is one of its mutable columns")
}
