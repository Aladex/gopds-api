package services_test

import (
	"context"
	"sync/atomic"
	"testing"

	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A provider's count of the fixed request jitters by one token for the same
// model (prod, judge_b on glm-5.3: 544 against the learned 543, twice in two
// days, each time a stalled merge run). jitteringJudge answers byFields,
// except that judge_b's fixed request counts base, and every third call from
// the second one off by delta: base, base+delta, base, base, base+delta, ...
// Any three calls in a row are two of base and one of base+delta.
func jitteringJudge(base *atomic.Int64, delta int64) func(req *fakeRequest) fakeReply {
	answer := byFields(nil)
	var n atomic.Int64
	return func(req *fakeRequest) fakeReply {
		reply := answer(req)
		if isFingerprint(req) && req.Model == "claude-opus-5" {
			if b := base.Load(); b != 0 {
				reply.Prompt = b
				if n.Add(1)%3 == 2 {
					reply.Prompt += delta
				}
			}
		}
		return reply
	}
}

// A one-token jitter of the periodic check is not drift: the resolve run
// completes with judge_b never paused and nothing re-learned.
func TestAuthorLLMOneTokenJitterIsNotDrift(t *testing.T) {
	for name, delta := range map[string]int64{"one more": 1, "one fewer": -1} {
		t.Run(name, func(t *testing.T) {
			e := newLLMEnv(t)
			e.settings.FingerprintEvery = 2
			ingestEvalCatalogue(t, e)
			ctx := context.Background()
			e.completedFullEval(t, true, []llmeval.StratumDecision{
				passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
			var base atomic.Int64
			base.Store(checkPrompt)
			e.fake.setRespond(jitteringJudge(&base, delta))
			run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
			require.NoError(t, err)

			e.resolveUntilDone(run.ID)
			assert.Nil(t, e.state("judge_b").PausedReason)
			assert.Empty(t, e.judgeBRelearns(run.ID))
			assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_call WHERE run_id = ? AND slot = 'judge_b'
				AND kind = 'fingerprint' AND outcome = 'answered' AND prompt_tokens = ?`, run.ID, checkPrompt+delta),
				"no check saw the jitter")
		})
	}
}

// A re-learn whose calls jitter by one (N, N+1, N in some order) settles:
// learned, with the most frequent count as the new reference, and the run
// completes on it while the checks keep jittering.
func TestAuthorLLMRelearnToleratesOneTokenJitter(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 3
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var prompt atomic.Int64
	e.fake.setRespond(jitteringJudge(&prompt, 1))
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	d, w := e.resolveDriver(), e.worker()
	step := func() error { _, stepErr := d.Step(ctx); return stepErr }
	stepUntil(t, step, w, func() bool {
		return e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b' AND status = 'answered'`,
			run.ID) > 0
	})
	prompt.Store(checkPrompt + 50) // another model, whose count jitters by one
	stepUntil(t, step, w, func() bool { return e.state("judge_b").PausedReason != nil })
	require.Equal(t, "drift", deref(e.state("judge_b").PausedReason))

	require.NoError(t, e.llmAdmin().ResumeParticipant(ctx, "judge_b"))
	e.resolveUntilDone(run.ID)
	rows := e.judgeBRelearns(run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, "learned", rows[0].Status, "reason: %s", deref(rows[0].Reason))
	assert.Equal(t, int64(checkPrompt+50), promptOf(t, rows[0].New))
	assert.Nil(t, e.state("judge_b").PausedReason)
	// The calls the re-learn itself settled on, selected as it selects them
	// (database/author_llm_relearn.go): its mode, from its start, its
	// repeats; the periodic checks after it do not count.
	var used []int64
	_, err = e.db.Query(&used, `SELECT c.prompt_tokens FROM author_llm_call c, author_llm_relearn rl
		WHERE rl.run_id = ?0 AND rl.slot = 'judge_b' AND c.run_id = rl.run_id AND c.slot = rl.slot
			AND c.kind = 'fingerprint' AND c.outcome = 'answered' AND c.output_mode = rl.output_mode
			AND c.created_at >= rl.started_at
		ORDER BY c.id
		LIMIT (SELECT repeats FROM author_llm_relearn WHERE run_id = ?0 AND slot = 'judge_b')`, run.ID)
	require.NoError(t, err)
	require.Len(t, used, services.DefaultAuthorLLMEvalSizes().FingerprintRepeats)
	assert.Contains(t, used, int64(checkPrompt+50))
	assert.Contains(t, used, int64(checkPrompt+51), "the re-learn's own calls never saw the jitter")
}
