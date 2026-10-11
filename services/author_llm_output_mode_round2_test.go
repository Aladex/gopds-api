package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	logrustest "github.com/sirupsen/logrus/hooks/test" //nolint:depguard // tests assert on the structured log entries
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round 2 of the output-mode negotiation (review B1 and N1, and the lock-in
// tests the review listed).

// toolBody is a chat completion whose answer is a call of the schema's tool.
func toolBody(model, arguments string, prompt, output int64) string {
	out, err := json.Marshal(map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls",
			"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
				"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": llmreq.SchemaName, "arguments": arguments}}}}}},
		"usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": output, "total_tokens": prompt + output},
	})
	if err != nil {
		panic(err)
	}
	return string(out)
}

// negotiating answers like byFields, except that each model listed refuses
// the given modes with the official API's 400, and a request in tool mode is
// answered with a tool call.
func negotiating(refused map[string][]string) func(*fakeRequest) fakeReply {
	answer := byFields(distinctLabs)
	return func(req *fakeRequest) fakeReply {
		mode := responseFormat(req)
		for _, m := range refused[req.Model] {
			if m == mode {
				return fakeReply{Status: http.StatusBadRequest, Body: formatRejected}
			}
		}
		reply := answer(req)
		if mode != "tool" || reply.Body != "" || reply.Status != 0 {
			return reply
		}
		content, prompt, model := reply.Content, reply.Prompt, reply.Model
		if content == "" {
			content = correctResults(req.Items)
		}
		if prompt == 0 {
			prompt = 100
			if isFingerprint(req) {
				prompt = checkPrompt
			}
		}
		if model == "" {
			model = req.Model
		}
		return fakeReply{Body: toolBody(model, content, prompt, 20)}
	}
}

func (e *llmEnv) reference(t *testing.T, run int64) map[string]database.AuthorLLMFingerprintReference {
	t.Helper()
	var raw string
	_, err := e.db.QueryOne(pg.Scan(&raw), `SELECT coalesce(reference::text, 'null') FROM author_llm_run WHERE id = ?`, run)
	require.NoError(t, err)
	var out map[string]database.AuthorLLMFingerprintReference
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	return out
}

// 1 (B1). A second driver that saw an empty fingerprint stage before the
// first one enqueued it, and enqueues only after a format rejection moved
// the participant's jobs to the next mode, adds nothing: one logical job per
// fingerprint repeat, whatever its mode now is.
func TestAuthorLLMFingerprintNegotiationAndDuplicateEnqueueAreIdempotent(t *testing.T) {
	for name, refused := range map[string][]string{
		"to json_object": {"json_schema"},
		"to tool":        {"json_schema", "json_object"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLLMEnv(t)
			ingestEvalCatalogue(t, e)
			e.fake.setRespond(negotiating(map[string][]string{"deepseek-v4-pro": refused}))
			ctx := context.Background()
			retries := database.AuthorLLMTxRetries()
			run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, nil, nil)
			require.NoError(t, err)
			e.stepTo(run, "fingerprint")

			// Driver B (as after a restart: a fresh instance on its own
			// connection) saw no fingerprint job and is held before it locks
			// the run.
			hooked := pg.Connect(e.db.Options())
			t.Cleanup(func() { _ = hooked.Close() })
			atLock := newBeforeQueryGate(func(q string) bool {
				return strings.Contains(q, "FROM author_llm_run WHERE id IN") && strings.Contains(q, " FOR ")
			})
			hooked.AddQueryHook(atLock)
			driverB := services.NewAuthorLLMEvalDriver(hooked, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
			stepped := make(chan error, 1)
			go func() {
				_, stepErr := driverB.Step(ctx)
				stepped <- stepErr
			}()
			atLock.await(t, "driver B's enqueue")

			// Driver A enqueues the stage; the worker's settlements move
			// production_b's jobs on to the mode that will answer.
			driverA := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
			_, err = driverA.Step(ctx)
			require.NoError(t, err)
			slotJobs := `SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose = 'eval_fingerprint' AND slot = ?`
			require.Equal(t, 3, e.count(slotJobs, run, "production_b"))
			w := e.worker()
			target := "json_object"
			if len(refused) == 2 {
				target = "tool"
			}
			for range 5 {
				if e.count(slotJobs+` AND output_mode = ?`, run, "production_b", target) > 0 {
					break
				}
				_, err = w.RunOnce(ctx)
				require.NoError(t, err)
			}
			require.Positive(t, e.count(slotJobs+` AND output_mode = ?`, run, "production_b", target))

			close(atLock.release)
			require.NoError(t, <-stepped)
			assert.Equal(t, 3, e.count(slotJobs, run, "production_b"), "a duplicate of production_b's repeats")
			assert.Equal(t, 12, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose = 'eval_fingerprint'`, run))

			stages := e.evalUntilDone(run)
			assert.Equal(t, "report", stages[len(stages)-1])
			assert.Equal(t, target, e.reference(t, run)["production_b"].OutputMode)
			assert.Equal(t, retries, database.AuthorLLMTxRetries(), "a transaction was retried")
		})
	}
}

// 2. Every participant's negotiated mode reaches every purpose it takes part
// in — stored reference, job rows, answered calls and the wire request.
func TestAuthorLLMNegotiatedModeReachesEveryEvalPurpose(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	refused := map[string][]string{
		"gpt-6-luna": {"json_schema"}, "deepseek-v4-pro": {"json_schema"},
		"gpt-6-sol": {"json_schema"}, "claude-opus-5": {"json_schema", "json_object"},
	}
	e.fake.setRespond(negotiating(refused))
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, nil, nil)
	require.NoError(t, err)
	stages := e.evalUntilDone(run)
	require.Equal(t, []string{"freeze", "fingerprint", "anchor", "judge", "candidates", "repeat", "report"}, stages)

	want := map[string]string{"production_a": "json_object", "production_b": "json_object",
		"judge_a": "json_object", "judge_b": "tool"}
	modelOf := map[string]string{"production_a": "gpt-6-luna", "production_b": "deepseek-v4-pro",
		"judge_a": "gpt-6-sol", "judge_b": "claude-opus-5"}
	purposes := map[string][]string{
		"production_a": {"eval_fingerprint", "eval_anchor", "eval_candidate", "eval_repeat"},
		"production_b": {"eval_fingerprint", "eval_anchor", "eval_candidate", "eval_repeat"},
		"judge_a":      {"eval_fingerprint", "eval_anchor", "eval_judge", "eval_repeat"},
		"judge_b":      {"eval_fingerprint", "eval_anchor", "eval_judge", "eval_repeat"},
	}
	ref := e.reference(t, run)
	for slot, mode := range want {
		assert.Equal(t, mode, ref[slot].OutputMode, "%s reference", slot)
		for _, purpose := range purposes[slot] {
			assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = ? AND purpose = ?`,
				run, slot, purpose), "%s has no %s jobs", slot, purpose)
			assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = ? AND purpose = ?
				AND output_mode <> ?`, run, slot, purpose, mode), "%s %s left in another mode", slot, purpose)
		}
		assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_call WHERE run_id = ? AND slot = ? AND outcome = 'answered'
			AND output_mode <> ?`, run, slot, mode), "%s answered in another mode", slot)
		// On the wire: the refused modes once each, then only the chosen one.
		jobs := modesOf(e, modelOf[slot], false)
		require.NotEmpty(t, jobs, slot)
		for _, m := range jobs {
			assert.Equal(t, mode, m, "%s job request", slot)
		}
		checks := modesOf(e, modelOf[slot], true)
		n := len(refused[modelOf[slot]])
		assert.Equal(t, refused[modelOf[slot]], checks[:n], "%s refusals", slot)
		for _, m := range checks[n:] {
			assert.Equal(t, mode, m, "%s fingerprint request", slot)
		}
	}
	r := downloadReport(t, e, run)
	assert.NotContains(t, reasonsOf(&r), "fingerprint")
}

// 3. A participant that refuses json_schema and json_object is fingerprinted,
// checked and asked in tool mode, without a pause or a counted failure.
func TestAuthorLLMFingerprintNegotiatesToolSuccessfully(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(negotiating(map[string][]string{"deepseek-v4-pro": {"json_schema", "json_object"}}))
	ctx := context.Background()
	run, err := e.llmAdmin().Start(ctx, "eval", "pilot", e.adminUser())
	require.NoError(t, err)
	stages := e.evalUntilDone(run.ID)
	require.Equal(t, "report", stages[len(stages)-1])
	require.Contains(t, stages, "candidates", "the run stopped at the fingerprint")

	checks := modesOf(e, "deepseek-v4-pro", true)
	require.Greater(t, len(checks), 2+evalSizes.FingerprintRepeats-1)
	assert.Equal(t, []string{"json_schema", "json_object"}, checks[:2])
	for _, m := range checks[2:] {
		assert.Equal(t, "tool", m)
	}
	for _, m := range modesOf(e, "deepseek-v4-pro", false) {
		assert.Equal(t, "tool", m)
	}
	assert.Equal(t, "tool", e.reference(t, run.ID)["production_b"].OutputMode)
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_call WHERE slot = 'production_b' AND kind = 'fingerprint'
		AND output_mode = 'tool'`), "a periodic check in tool")
	assert.Nil(t, e.state("production_b").PausedReason)
	assert.Zero(t, e.count(`SELECT coalesce(sum(failure_count), 0) FROM author_llm_job WHERE run_id = ? AND slot = 'production_b'
		AND purpose = 'eval_fingerprint'`, run.ID), "a format rejection counted as a failure")
	text, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run.ID)
	require.NoError(t, err)
	var doc struct {
		Settings struct {
			OutputModes map[string]string `json:"output_modes"`
		} `json:"settings"`
	}
	require.NoError(t, json.Unmarshal(text, &doc))
	assert.Equal(t, "tool", doc.Settings.OutputModes["production_b"])
}

// 4a. A negotiated participant keeps its mode through a counted retry (a
// 503) and an invalid batch split into single items.
func TestAuthorLLMNegotiatedModeSurvivesBatchRetryAndSingles(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	base := negotiating(map[string][]string{"deepseek-v4-pro": {"json_schema"}})
	var served503, servedEmpty atomic.Bool
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if req.Model == "deepseek-v4-pro" && !isFingerprint(req) && responseFormat(req) == "json_object" {
			if len(req.Items) > 1 && servedEmpty.CompareAndSwap(false, true) {
				return fakeReply{Content: `{"results":[]}`}
			}
			if served503.CompareAndSwap(false, true) {
				return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"overloaded"}}`}
			}
		}
		return base(req)
	})
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, nil, nil)
	require.NoError(t, err)
	stages := e.evalUntilDone(run)
	require.Equal(t, "report", stages[len(stages)-1])
	require.True(t, served503.Load() && servedEmpty.Load(), "the faults were never injected")

	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_b'
		AND failure_count > 0`, run), "no counted retry")
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_b'
		AND retry_single`, run), "no single-item retry")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_b'
		AND output_mode <> 'json_object'`, run))
	for _, m := range modesOf(e, "deepseek-v4-pro", false) {
		assert.Equal(t, "json_object", m)
	}
}

// 4b. ...and through an expired lease: the requeued job and the next claim
// of it keep the mode; the dead call keeps its reserve and its late settle
// is fenced.
func TestAuthorLLMNegotiatedModeSurvivesLeaseExpiry(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(negotiating(map[string][]string{"deepseek-v4-pro": {"json_schema"}}))
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, nil, nil)
	require.NoError(t, err)
	e.stepTo(run, "anchor")
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	_, err = driver.Step(ctx)
	require.NoError(t, err)

	owner := database.NewLeaseOwner()
	now := time.Now()
	dead, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionB, owner, &now))
	require.NoError(t, err)
	require.NotNil(t, dead, "refusal %s", refusal)
	require.Equal(t, "json_object", dead.OutputMode)
	later := dead.LeaseExpiresAt.Add(time.Second)
	again, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionB,
		database.NewLeaseOwner(), &later))
	require.NoError(t, err)
	require.NotNil(t, again, "refusal %s", refusal)
	assert.Equal(t, "json_object", again.OutputMode, "the claim after the expiry")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_b'
		AND output_mode <> 'json_object'`, run), "a requeued job changed its mode")
	var call struct {
		Outcome  string
		Settled  int64
		Reserved int64
	}
	_, err = e.db.QueryOne(&call, `SELECT outcome, settled_tokens AS settled, reserved_tokens AS reserved
		FROM author_llm_call WHERE id = ?`, dead.CallID)
	require.NoError(t, err)
	assert.Equal(t, "abandoned", call.Outcome)
	assert.Equal(t, call.Reserved, call.Settled)
	_, err = database.SettleAuthorLLMCall(ctx, e.db, &database.AuthorLLMSettlement{CallID: dead.CallID, Owner: owner,
		Outcome: models.AuthorLLMCallAnswered, MaxAttempts: 5, ConcurrencyMax: 16,
		BaseBackoff: time.Second, MaxBackoff: time.Minute})
	assert.ErrorIs(t, err, database.ErrAuthorLLMLeaseLost)
	// The claim after the expiry is what the worker sends: its mode is the
	// request's (the worker asks in claim.OutputMode, which the other tests
	// follow onto the wire). A claim made at a later operation clock cannot be
	// handed to a worker running on the real clock, so the run stops here.
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE id = ? AND status = 'pending'
		AND failure_count = 1 AND output_mode = 'json_object'`, dead.Jobs[0].JobID), "the expired job's row")
}

// 5. After the negotiation a 400 is an ordinary bad request again — from a
// job or from a periodic check: the participant pauses, the mode stays.
func TestAuthorLLMLater400PausesWithoutRenegotiation(t *testing.T) {
	for _, from := range []string{"job", "periodic check"} {
		t.Run(from, func(t *testing.T) {
			e := newLLMEnv(t)
			if from == "periodic check" {
				e.settings.FingerprintEvery = 2 // a check after every two answers
			}
			ingestEvalCatalogue(t, e)
			base := negotiating(map[string][]string{"deepseek-v4-pro": {"json_schema"}})
			var learned atomic.Bool
			e.fake.setRespond(func(req *fakeRequest) fakeReply {
				if learned.Load() && req.Model == "deepseek-v4-pro" && isFingerprint(req) == (from == "periodic check") {
					return fakeReply{Status: http.StatusBadRequest, Body: formatRejected}
				}
				return base(req)
			})
			ctx := context.Background()
			run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, nil, nil)
			require.NoError(t, err)
			e.stepTo(run, "anchor")
			require.Equal(t, "json_object", e.reference(t, run)["production_b"].OutputMode)
			learned.Store(true)
			driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
			w := e.worker()
			for range 6 {
				_, err = driver.Step(ctx)
				require.NoError(t, err)
				_, err = w.RunOnce(ctx)
				require.NoError(t, err)
				if e.state("production_b").PausedReason != nil {
					break
				}
			}
			st := e.state("production_b")
			require.NotNil(t, st.PausedReason, "a later 400 did not pause the participant")
			assert.Equal(t, "bad_request", *st.PausedReason)
			assert.Equal(t, "json_object", e.reference(t, run)["production_b"].OutputMode)
			for _, m := range modesOf(e, "deepseek-v4-pro", true)[1:] {
				assert.Equal(t, "json_object", m, "renegotiated after the fingerprint stage")
			}
			assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_b'
				AND output_mode = 'tool'`, run))
			assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_call WHERE run_id = ? AND slot = 'production_b'
				AND output_mode = 'tool'`, run))
		})
	}
}

// 6. The periodic checks after the answer threshold, through worker
// restarts, keep the negotiated mode.
func TestAuthorLLMNegotiatedPeriodicCheckAfterThresholdAndRestart(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 2
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(negotiating(map[string][]string{"deepseek-v4-pro": {"json_schema"}}))
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, nil, nil)
	require.NoError(t, err)
	stages := e.evalUntilDone(run) // a new worker at every stage
	require.Equal(t, "report", stages[len(stages)-1])
	assert.GreaterOrEqual(t, e.count(`SELECT count(*) FROM author_llm_call WHERE run_id = ? AND slot = 'production_b'
		AND kind = 'fingerprint'`, run), 2, "the threshold forced no second check")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_call WHERE run_id = ? AND slot = 'production_b'
		AND kind = 'fingerprint' AND output_mode <> 'json_object'`, run))
	assert.Equal(t, "json_object", e.reference(t, run)["production_b"].OutputMode)
	checks := modesOf(e, "deepseek-v4-pro", true)
	for _, m := range checks[1:] {
		assert.Equal(t, "json_object", m)
	}
}

// 7 (N1). An explicit start of a new eval lifts the participants' run-bound
// pauses of the current configuration — bad_request, and since the drift
// re-learn also drift — only those, once.
func TestAuthorLLMNewEvalClearsOnlyPreviousBadRequestPause(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	pause := func(slot, reason string) {
		_, err := e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = ?, paused_at = now()
			WHERE config_version = ? AND slot = ?`, reason, e.version, slot)
		require.NoError(t, err)
	}
	pause("production_a", "bad_request")
	pause("production_b", "auth")
	pause("judge_a", "drift")
	pause("judge_b", "model_mismatch")
	_, err := e.db.Exec(`UPDATE author_llm_provider_state SET throttled_until = now() + interval '1 hour'
		WHERE config_version = ? AND slot = 'judge_a'`, e.version)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE author_llm_endpoint_state SET paused_reason = 'quota_exhausted', paused_at = now()`)
	require.NoError(t, err)
	// Another configuration's bad_request pause is not this start's.
	other := e.withEndpoint(t)
	_, err = e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'bad_request', paused_at = now()
		WHERE config_version = ? AND slot = 'production_a'`, other.version)
	require.NoError(t, err)

	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	run, err := e.llmAdmin().Start(ctx, "eval", "dev", e.adminUser())
	require.NoError(t, err)

	a := e.state("production_a")
	assert.Nil(t, a.PausedReason, "the previous bad_request pause stays")
	assert.Nil(t, a.PausedAt)
	// A drift is the old run's too: the new run learns its own fingerprints.
	assert.Nil(t, e.state("judge_a").PausedReason, "the previous drift pause stays")
	for slot, reason := range map[string]string{"production_b": "auth", "judge_b": "model_mismatch"} {
		st := e.state(llmreq.Slot(slot))
		require.NotNil(t, st.PausedReason, slot)
		assert.Equal(t, reason, *st.PausedReason, slot)
	}
	assert.NotNil(t, e.state("judge_a").ThrottledUntil, "a throttle was lifted")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_endpoint_state WHERE paused_reason = 'quota_exhausted'`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_provider_state WHERE config_version = ?
		AND paused_reason = 'bad_request'`, other.version), "another configuration's pause was lifted")
	logged := false
	for _, entry := range hook.AllEntries() {
		if entry.Message == string(services.AuthorMetadataEventLLMParticipantsResumed) &&
			entry.Data["run_id"] == run.ID && entry.Data["count"] == 2 {
			logged = true
		}
	}
	assert.True(t, logged, "the lifted pause is not recorded")

	// A fresh 400 pauses again, and polling does not lift it.
	pause("production_a", "bad_request")
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	w := e.worker()
	for range 3 {
		_, err = driver.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
	}
	st := e.state("production_a")
	require.NotNil(t, st.PausedReason, "polling lifted a pause")
	assert.Equal(t, "bad_request", *st.PausedReason)
}

// 1b (B1). Two drivers that both found a stage empty take turns on the run:
// while one holds it (checked again under the lock, about to insert), the
// other waits, and then finds the jobs instead of inserting its own.
func TestAuthorLLMStageEnqueuersTakeTurns(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, nil, nil)
	require.NoError(t, err)
	e.stepTo(run, "fingerprint")

	hooked := pg.Connect(e.db.Options())
	t.Cleanup(func() { _ = hooked.Close() })
	var locked atomic.Bool
	lockGate := newBeforeQueryGate(func(q string) bool {
		if strings.Contains(q, "FROM author_llm_run WHERE id IN") && strings.Contains(q, " FOR ") {
			locked.Store(true)
		}
		return false
	})
	underLock := newBeforeQueryGate(func(q string) bool {
		return locked.Load() && strings.Contains(q, "INSERT INTO author_llm_job")
	})
	hooked.AddQueryHook(lockGate)
	hooked.AddQueryHook(underLock)
	driverB := services.NewAuthorLLMEvalDriver(hooked, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	stepped := make(chan error, 1)
	go func() {
		_, stepErr := driverB.Step(ctx)
		stepped <- stepErr
	}()
	underLock.await(t, "driver B's INSERT under the lock")

	driverA := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	aDone := make(chan error, 1)
	go func() {
		_, stepErr := driverA.Step(ctx)
		aDone <- stepErr
	}()
	waited := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !waited; {
		select {
		case aErr := <-aDone:
			t.Fatalf("driver A enqueued while driver B held the run (err %v)", aErr)
		default:
		}
		waited = e.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND query LIKE '%author_llm_run%'`) > 0
		time.Sleep(10 * time.Millisecond)
	}
	require.True(t, waited, "driver A did not wait for driver B's lock")
	close(underLock.release)
	require.NoError(t, <-stepped)
	require.NoError(t, <-aDone)
	assert.Equal(t, 12, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose = 'eval_fingerprint'`, run))
}
