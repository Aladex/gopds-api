package services_test

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/llm"
	"gopds-api/models"
	"gopds-api/services"
)

// Review B3 of 9cc500c: a fingerprint check that keeps failing transiently
// had no deadline — the run never finished. The check gets the items'
// window: once it has failed MaxAttempts times and the window since its
// first failure has passed, the participant is paused (check_unavailable)
// instead of sending the check again. Nothing it answered is confirmed
// meanwhile, and a resume starts a fresh window.

// checkClock is a worker clock a test moves by hand.
type checkClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *checkClock) at() *time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now
	return &now
}

func (c *checkClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// clockedWorker is a worker on the default transient window, on clock.
func (e *llmEnv) clockedWorker(t *testing.T, clock *checkClock) *services.AuthorLLMWorker {
	t.Helper()
	client := llm.NewClient(config.LLMConfig{BaseURL: e.srv.URL + "/v1", Timeout: 30 * time.Second})
	// No TransientWindow: the worker's default.
	w, err := services.NewAuthorLLMWorker(e.db, client, &services.AuthorLLMWorkerConfig{
		Settings: e.settings, Identity: e.identity, ConfigVersion: e.version, ArchivesDir: e.archives,
		Clock: clock.at,
	})
	require.NoError(t, err)
	return w
}

// failingCheck makes the fixed check of production_a answer 503 while down
// holds; every other request is answered.
func failingCheck(e *llmEnv, down *atomic.Bool) {
	answer := checked(nil)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if isFingerprint(req) && req.Model == productionAModel && down.Load() {
			return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"busy"}}`}
		}
		return answer(req)
	})
}

// sendCheck claims production_a's next call, which must be its check, and
// processes it.
func sendCheck(t *testing.T, w *services.AuthorLLMWorker) {
	t.Helper()
	claim, refusal, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	require.NotNil(t, claim, "production_a refused: %s", refusal)
	require.Equal(t, models.AuthorLLMCallFingerprint, claim.Kind)
	require.NoError(t, w.Process(context.Background(), llmreq.SlotProductionA, claim))
}

// checkPause is production_a's pause reason, "" when it is not paused.
func (e *llmEnv) checkPause() string {
	return deref(e.state(llmreq.SlotProductionA).PausedReason)
}

// The run's first check fails: no job of the participant is ever sent.
func TestAuthorLLMFailingStartCheckPausesAfterTheWindow(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.MaxAttempts = 1
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "unchecked", givenFamily, 1)
	var down atomic.Bool
	down.Store(true)
	failingCheck(e, &down)
	clock := &checkClock{now: time.Now()}
	w := e.clockedWorker(t, clock)

	// Six hours of failed checks, one every hour: within the window.
	for range 6 {
		sendCheck(t, w)
		require.Empty(t, e.checkPause(), "a check failing within the window paused")
		clock.advance(time.Hour)
	}
	// Ordinary requests are not affected: production_b checks and answers.
	claimProcess(t, w, llmreq.SlotProductionB)
	claimProcess(t, w, llmreq.SlotProductionB)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_b'
		AND status = 'answered'`, run))

	// Past the window the participant is paused, with the reason.
	clock.advance(25 * time.Hour)
	sendCheck(t, w)
	assert.Equal(t, string(models.AuthorLLMPausedCheckUnavailable), e.checkPause())
	claim, refusal, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	assert.Nil(t, claim, "a paused participant sent a call")
	assert.Equal(t, database.AuthorLLMClaimPaused, refusal)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_a'
		AND status = 'pending' AND attempt_count = 0`, run), "an unchecked participant answered")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ?`, run))

	// The admin's Continue starts a fresh window: one more failure does
	// not pause.
	require.NoError(t, e.llmAdmin().ResumeParticipant(context.Background(), string(llmreq.SlotProductionA)))
	clock.advance(time.Hour)
	sendCheck(t, w)
	assert.Empty(t, e.checkPause(), "the resumed participant inherited the old window")
	// The provider recovers: the check passes and the job is answered.
	down.Store(false)
	clock.advance(time.Hour)
	sendCheck(t, w)
	claimProcess(t, w, llmreq.SlotProductionA)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_a'
		AND status = 'answered'`, run))
}

// The check of the unchecked tail fails: the answers it would cover are
// never confirmed, and the participant is paused past the window.
func TestAuthorLLMFailingTailCheckPausesAfterTheWindow(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.MaxAttempts = 1
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "tail", givenFamily, 1)
	var down atomic.Bool
	failingCheck(e, &down)
	clock := &checkClock{now: time.Now()}
	w := e.clockedWorker(t, clock)
	// The start check fails for six hours, then passes: the answered check
	// ends that count.
	down.Store(true)
	for range 6 {
		sendCheck(t, w)
		clock.advance(time.Hour)
	}
	down.Store(false)
	passChecks(t, w)
	// The answers come after the start checks: they wait for the tail's.
	clock.advance(time.Minute)
	claimProcess(t, w, llmreq.SlotProductionA)
	claimProcess(t, w, llmreq.SlotProductionB)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND confirmed_at IS NULL`, run))

	down.Store(true)
	for range 6 {
		sendCheck(t, w)
		require.Empty(t, e.checkPause())
		clock.advance(time.Hour)
	}
	// A day after the start check's first failure, but within the tail's
	// own window.
	clock.advance(14 * time.Hour)
	sendCheck(t, w)
	require.Empty(t, e.checkPause(), "the passed start check did not end the count")
	clock.advance(11 * time.Hour)
	sendCheck(t, w)
	assert.Equal(t, string(models.AuthorLLMPausedCheckUnavailable), e.checkPause())
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND confirmed_at IS NOT NULL`, run),
		"an answer no check covered was confirmed")
	p, err := database.AuthorLLMResolveRunProgress(context.Background(), e.db, run)
	require.NoError(t, err)
	assert.False(t, p.Done(), "the run finished on unchecked answers")
}

// A check whose call never settles — its lease runs out — is a failed check
// too: past the window the abandonment pauses the participant.
func TestAuthorLLMAbandonedCheckPausesAfterTheWindow(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.MaxAttempts = 1
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "hung", givenFamily, 1)
	e.fake.setRespond(checked(nil))
	clock := &checkClock{now: time.Now()}
	w := e.clockedWorker(t, clock)
	claimOnly := func() {
		t.Helper()
		claim, refusal, err := w.Claim(context.Background(), llmreq.SlotProductionA)
		require.NoError(t, err)
		require.NotNil(t, claim, "production_a refused: %s", refusal)
		require.Equal(t, models.AuthorLLMCallFingerprint, claim.Kind)
	}
	// The process dies between each check and its settle.
	claimOnly()
	for range 6 {
		clock.advance(time.Hour)
		claimOnly()
		require.Empty(t, e.checkPause(), "an abandoned check within the window paused")
	}
	clock.advance(25 * time.Hour)
	claim, refusal, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	assert.Nil(t, claim)
	assert.Equal(t, database.AuthorLLMClaimPaused, refusal)
	assert.Equal(t, string(models.AuthorLLMPausedCheckUnavailable), e.checkPause())
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_a'
		AND attempt_count > 0`, run), "an unchecked participant answered")

	// Resumed, the participant gets a fresh window.
	require.NoError(t, database.ResumeAuthorLLMParticipant(context.Background(), e.db, e.version, llmreq.SlotProductionA))
	claimOnly()
	clock.advance(time.Hour)
	claimOnly()
	assert.Empty(t, e.checkPause(), "the resumed participant inherited the old window")
}

// A pause a failing check left belongs to its run: a new run lifts it and
// starts the window again.
func TestAuthorLLMNewRunLiftsTheCheckPause(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm")}, nil)
	_, err := e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'check_unavailable', paused_at = now(),
			check_failures = 9, check_failing_since = now() - interval '2 days'
		WHERE config_version = ? AND slot = 'production_a'`, e.version)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE author_llm_provider_state SET check_failures = 3, check_failing_since = now() - interval '2 days'
		WHERE config_version = ? AND slot = 'judge_a'`, e.version)
	require.NoError(t, err)
	_, err = e.llmAdmin().Start(context.Background(), "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotJudgeA} {
		st := e.state(slot)
		assert.Nil(t, st.PausedReason, slot)
		assert.Zero(t, st.CheckFailures, slot)
		assert.Nil(t, st.CheckFailingSince, slot)
	}
}
