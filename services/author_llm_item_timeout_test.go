package services_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/llm"
	"gopds-api/models"
	"gopds-api/services"
)

// One item that hangs the model held a run for the whole transient window:
// eval runs 24 and 25 waited a day on one judge_b job ("А.Ч. Бхактиведанта
// Свами Прабхупада ачарья-основатель …"), every call of it a 120 s timeout
// while the judge answered everything else. A job that times out alone in its
// call gets a longer timeout each time (TimeoutEscalation); only after
// ItemTimeoutLimit timeouts at the last step, the participant answering other
// calls meanwhile, does it fail like any other failed job. An outage of the
// endpoint still only throttles and retries, and a batch call's timeout says
// nothing about one item.

// stuckItem is the input whose calls never come back in time.
var stuckItem = llmreq.ItemInput{
	Fields: []llmres.FieldValue{{Field: llmres.FieldFirst, Text: "Застрял"}, {Field: llmres.FieldLast, Text: "Навсегда"}},
	Script: "Cyrl", Flags: []string{},
}

// itemTimeoutEnv is an environment whose one-item calls time out fast.
type itemTimeoutEnv struct {
	*llmEnv
	w *services.AuthorLLMWorker

	mu sync.Mutex
	// down makes every call but the fingerprint check hang.
	down bool
	// stuckAnswers makes the stuck item's calls answer with this status
	// instead of hanging, in order, one per call; empty hangs.
	stuckAnswers []int
	// stuckDelay, when set, answers the stuck item correctly after it
	// instead of hanging.
	stuckDelay time.Duration
	// fed counts the one-item inputs feed enqueued; fedAt is, per input,
	// the failure count of its job a feed last followed.
	fed   int
	fedAt map[string]int
	// afterFailure, when set, runs with every feed.
	afterFailure func()
}

// The one-item call timeouts of these tests: requestTimeout, then each step
// of escalation; a hanging call outlasts them all.
const requestTimeout = 200 * time.Millisecond

var escalation = []time.Duration{400 * time.Millisecond, 800 * time.Millisecond}

func newItemTimeoutEnv(t *testing.T) *itemTimeoutEnv {
	t.Helper()
	e := &itemTimeoutEnv{llmEnv: newLLMEnv(t), fedAt: map[string]int{}}
	e.settings.RequestTimeout = requestTimeout
	e.settings.RequestTimeoutPerItem = requestTimeout / 2
	e.settings.TimeoutEscalation = escalation
	e.settings.MaxAttempts = 2
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if isFingerprint(req) {
			return fakeReply{}
		}
		e.mu.Lock()
		hang := e.down
		status := 0
		var delay time.Duration
		// The stuck item hangs production_a only; production_b answers it.
		if strings.Contains(req.User, "Застрял") && req.Model == testParticipants[0].Model {
			hang, delay = e.stuckDelay == 0, e.stuckDelay
			if len(e.stuckAnswers) > 0 {
				status, e.stuckAnswers = e.stuckAnswers[0], e.stuckAnswers[1:]
				hang, delay = false, 0
			}
		}
		e.mu.Unlock()
		if hang {
			time.Sleep(escalation[len(escalation)-1] + requestTimeout) // past every timeout
			return fakeReply{}
		}
		time.Sleep(delay)
		if status != 0 {
			return fakeReply{Status: status, Body: `{"error":{"message":"busy"}}`}
		}
		return fakeReply{}
	})
	e.w = e.newWorker()
	return e
}

// passedCheck records an answered fingerprint check of production_a now: a
// copy of the check passChecks answered.
func (e *itemTimeoutEnv) passedCheck() {
	e.t.Helper()
	_, err := e.db.Exec(`INSERT INTO author_llm_call (config_version, slot, run_id, kind, items, output_mode,
			request_sha256, prompt_bound_bytes, output_ceiling, reserved_tokens, settled_tokens, check_seq, created_at,
			finished_at, outcome, latency_ms, response_model, usage, prompt_tokens, completion_tokens)
		SELECT config_version, slot, run_id, kind, items, output_mode, request_sha256, prompt_bound_bytes,
			output_ceiling, reserved_tokens, settled_tokens, check_seq, now(), now(), outcome, latency_ms, response_model, usage,
			prompt_tokens, completion_tokens
		FROM author_llm_call WHERE slot = 'production_a' AND kind = 'fingerprint' AND outcome = 'answered'
		ORDER BY id LIMIT 1`)
	require.NoError(e.t, err)
}

// start passes the participants' checks, once the run has its jobs.
func (e *itemTimeoutEnv) start() { passChecks(e.t, e.w) }

// newWorker is a worker whose backoff keeps a failed job behind an input
// enqueued right after its failure.
func (e *itemTimeoutEnv) newWorker() *services.AuthorLLMWorker {
	e.t.Helper()
	client := llm.NewClient(config.LLMConfig{BaseURL: e.srv.URL + "/v1", Timeout: 30 * time.Second})
	w, err := services.NewAuthorLLMWorker(e.db, client, &services.AuthorLLMWorkerConfig{
		Settings: e.settings, Identity: e.identity, ConfigVersion: e.version, ArchivesDir: e.archives,
		BaseBackoff: 50 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
	})
	require.NoError(e.t, err)
	return w
}

// feed enqueues a fresh one-item input, which the participant answers before
// the failed job's retry is due.
func (e *itemTimeoutEnv) feed(run *int64) {
	e.t.Helper()
	e.fed++
	e.reviewPair(run, "feed-"+strings.Repeat("x", e.fed), givenFamily, 1)
}

// round claims and processes one call of production_a, reporting whether
// there was one.
func (e *itemTimeoutEnv) round() bool {
	e.t.Helper()
	claim, _, err := e.w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(e.t, err)
	if claim == nil {
		return false
	}
	require.NoError(e.t, e.w.Process(context.Background(), llmreq.SlotProductionA, claim))
	return true
}

// latencies are the call latencies of the stuck input's production_a
// attempts, in order.
func (e *itemTimeoutEnv) latencies() []time.Duration {
	e.t.Helper()
	var ms []int
	_, err := e.db.Query(&ms, `SELECT c.latency_ms FROM author_llm_attempt a
		JOIN author_llm_call c ON c.id = a.call_id JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.slot = 'production_a' AND j.source_fingerprint = ? ORDER BY a.attempt_no`, testFingerprint("stuck"))
	require.NoError(e.t, err)
	out := make([]time.Duration, len(ms))
	for i, m := range ms {
		out[i] = time.Duration(m) * time.Millisecond
	}
	return out
}

// requireTimeouts checks each call ran to the timeout of its step, and not
// to the next one's.
func requireTimeouts(t *testing.T, got []time.Duration, want ...time.Duration) {
	t.Helper()
	require.Len(t, got, len(want))
	for i, w := range want {
		require.GreaterOrEqual(t, got[i], w, "call %d ended before its timeout", i+1)
		require.Less(t, got[i], w+150*time.Millisecond, "call %d outlasted its timeout", i+1)
	}
}

// job is production_a's job of one input: status, failures, class.
func (e *itemTimeoutEnv) job(seed string) (status string, failures int, class string) {
	e.t.Helper()
	var row struct {
		Status   string
		Failures int
		Class    *string
	}
	_, err := e.db.QueryOne(&row, `SELECT status, failure_count AS failures, last_error_class AS class FROM author_llm_job
		WHERE slot = 'production_a' AND source_fingerprint = ?`, testFingerprint(seed))
	require.NoError(e.t, err)
	if row.Class != nil {
		class = *row.Class
	}
	return row.Status, row.Failures, class
}

// untilAttempts runs rounds until the input's production_a job has the given
// failure count or ended, waiting out its backoff. With a run it feeds a
// fresh input after every failure, so the participant answers between two
// attempts of the job.
func (e *itemTimeoutEnv) untilAttempts(run *int64, seed string, failures int) {
	e.t.Helper()
	for range 400 {
		status, n, _ := e.job(seed)
		if n > e.fedAt[seed] && run != nil {
			e.feed(run)
			if e.afterFailure != nil {
				e.afterFailure()
			}
			e.fedAt[seed] = n
		}
		if n >= failures || models.AuthorLLMJobStatus(status).IsTerminal() {
			return
		}
		if !e.round() {
			time.Sleep(5 * time.Millisecond)
		}
	}
	e.t.Fatalf("job %s never reached %d failures", seed, failures)
}

func TestAuthorLLMStuckItemEscalatesThenFails(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	// An input answered before the stuck one's first call.
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 1)
	e.start()

	// Three timeouts, each call longer: 200 ms, 400 ms, 800 ms. The job
	// waits, past its MaxAttempts but inside the transient window.
	e.untilAttempts(&run, "stuck", 3)
	status, failures, _ := e.job("stuck")
	require.Equal(t, "pending", status)
	require.Equal(t, 3, failures)

	// The second timeout at the last step ends it as failed.
	e.untilAttempts(&run, "stuck", 4)
	status, failures, class := e.job("stuck")
	require.Equal(t, "failed", status, "a stuck item kept the run waiting")
	require.Equal(t, 4, failures)
	require.Equal(t, "item_timeout", class)
	requireTimeouts(t, e.latencies(), requestTimeout, escalation[0], escalation[1], escalation[1])

	// It ends the input like any failed job: a failed verdict, not a hang.
	for range 50 {
		if e.count(`SELECT count(*) FROM author_llm_verdict WHERE source_fingerprint = ?`, testFingerprint("stuck")) > 0 {
			break
		}
		claim, _, err := e.w.Claim(context.Background(), llmreq.SlotProductionB)
		require.NoError(t, err)
		if claim == nil {
			break
		}
		require.NoError(t, e.w.Process(context.Background(), llmreq.SlotProductionB, claim))
	}
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE source_fingerprint = ? AND verdict = 'failed'`,
		testFingerprint("stuck")))
}

func TestAuthorLLMEscalatedCallIsAnsweredNormally(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	// The model needs 300 ms for this name: past the first timeout, within
	// the second.
	e.stuckDelay = 300 * time.Millisecond
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 1)
	e.start()

	e.untilAttempts(&run, "stuck", 100)
	status, failures, _ := e.job("stuck")
	require.Equal(t, "answered", status)
	require.Equal(t, 1, failures)
	lat := e.latencies()
	require.Len(t, lat, 2)
	require.GreaterOrEqual(t, lat[0], requestTimeout)
	require.Less(t, lat[1], escalation[0])
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.slot = 'production_a' AND j.source_fingerprint = ? AND a.outcome = 'answered'
			AND a.canonical_sha256 IS NOT NULL AND a.id = j.answer_attempt_id`, testFingerprint("stuck")))
}

func TestAuthorLLMEscalatedCallLeaseOutlastsItsTimeout(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 1)
	e.start()
	e.untilAttempts(&run, "stuck", 2)

	// The stuck job's third call is at the last step: its lease covers the
	// longest timeout, so no claim takes it back as abandoned meanwhile.
	for range 400 {
		claim, _, err := e.w.Claim(context.Background(), llmreq.SlotProductionA)
		require.NoError(t, err)
		if claim == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		stuck := bytes.Equal(claim.Jobs[0].SourceFingerprint, testFingerprint("stuck"))
		if stuck {
			require.Equal(t, 2, claim.TimeoutStep)
			require.Equal(t, escalation[1]+time.Minute, claim.LeaseExpiresAt.Sub(claim.Now))
		} else {
			require.Zero(t, claim.TimeoutStep)
		}
		require.NoError(t, e.w.Process(context.Background(), llmreq.SlotProductionA, claim))
		if stuck {
			return
		}
	}
	t.Fatal("the stuck job was never claimed again")
}

func TestAuthorLLMItemTimeoutsCountOnlyInARow(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 1)
	e.start()

	// timeout, 503, then timeouts: the 503 breaks the streak and the
	// escalation starts over.
	e.untilAttempts(&run, "stuck", 1)
	e.mu.Lock()
	e.stuckAnswers = []int{http.StatusServiceUnavailable}
	e.mu.Unlock()
	e.untilAttempts(&run, "stuck", 5)
	status, failures, _ := e.job("stuck")
	require.Equal(t, "pending", status, "a 503 between the timeouts did not break the streak")
	require.Equal(t, 5, failures)
	lat := e.latencies()
	requireTimeouts(t, []time.Duration{lat[0], lat[2], lat[3], lat[4]}, requestTimeout, requestTimeout, escalation[0],
		escalation[1])

	e.untilAttempts(&run, "stuck", 6)
	status, _, class := e.job("stuck")
	require.Equal(t, "failed", status)
	require.Equal(t, "item_timeout", class)
}

func TestAuthorLLMItemTimeoutLimitIsConfigurable(t *testing.T) {
	e := newItemTimeoutEnv(t)
	e.settings.ItemTimeoutLimit = 3
	e.w = e.newWorker()
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 1)
	e.start()

	e.untilAttempts(&run, "stuck", 4)
	status, _, _ := e.job("stuck")
	require.Equal(t, "pending", status)
	e.untilAttempts(&run, "stuck", 5)
	status, _, class := e.job("stuck")
	require.Equal(t, "failed", status)
	require.Equal(t, "item_timeout", class)
}

func TestAuthorLLMEscalatedTimeoutsKeepTheWindow(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 1)
	e.start()
	window := e.state(llmreq.SlotProductionA).Concurrency

	// The first timeout halves the participant's window as any transient
	// failure; the long calls after it do not halve it again, so one name
	// does not leave the participant a single call at a time.
	e.untilAttempts(&run, "stuck", 3)
	require.Equal(t, max(1, window/2), e.state(llmreq.SlotProductionA).Concurrency)
}

func TestAuthorLLMEndpointOutageStillRetries(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.mu.Lock()
	e.down = true
	e.mu.Unlock()
	e.reviewPair(&run, "a", givenFamily, 1)
	e.reviewPair(&run, "b", givenFamily, 1)

	// Nothing answers, though fresh inputs keep coming and the participant's
	// fingerprint checks pass: every timeout is the endpoint's, not the item's.
	e.afterFailure = e.passedCheck
	e.start()
	e.untilAttempts(&run, "a", 5)
	e.untilAttempts(&run, "b", 5)
	for _, seed := range []string{"a", "b"} {
		status, failures, class := e.job(seed)
		require.Equal(t, "pending", status, "an outage failed job %s", seed)
		require.GreaterOrEqual(t, failures, 5)
		require.Equal(t, "timeout", class)
	}
	require.Equal(t, 1, e.state(llmreq.SlotProductionA).Concurrency, "the outage throttles the participant")
	require.Nil(t, e.state(llmreq.SlotProductionA).PausedReason)

	// The endpoint recovers: both are answered.
	e.mu.Lock()
	e.down = false
	e.mu.Unlock()
	e.untilAttempts(nil, "a", 100)
	e.untilAttempts(nil, "b", 100)
	for _, seed := range []string{"a", "b"} {
		status, _, _ := e.job(seed)
		require.Equal(t, "answered", status)
	}
}

func TestAuthorLLMTimeoutsOfAnOutageDoNotCountAfterIt(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	// An input answered before the outage vouches for no timeout in it.
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.start()
	e.untilAttempts(nil, "fine", 100)
	e.mu.Lock()
	e.down = true
	e.mu.Unlock()
	e.reviewPair(&run, "stuck", stuckItem, 1)

	// Two timeouts in the outage, then the endpoint recovers and answers
	// everything else: the stuck item needs its four timeouts after it.
	e.untilAttempts(&run, "stuck", 2)
	e.mu.Lock()
	e.down = false
	e.mu.Unlock()
	e.untilAttempts(&run, "stuck", 4)
	status, failures, _ := e.job("stuck")
	require.Equal(t, "pending", status, "timeouts of the outage counted against the item")
	require.Equal(t, 4, failures)
	e.untilAttempts(&run, "stuck", 6)
	status, _, class := e.job("stuck")
	require.Equal(t, "failed", status)
	require.Equal(t, "item_timeout", class)
}

// sharedTimeouts runs the two stuck two-item jobs until they timed out
// together n times.
func (e *itemTimeoutEnv) sharedTimeouts(run *int64, n int) {
	e.t.Helper()
	for range 20 {
		_, have, _ := e.job("stuck")
		if have >= n {
			break
		}
		e.feed(run)
		// Both stuck jobs are due again, so they share the next call.
		time.Sleep(150 * time.Millisecond)
		for range 10 {
			if _, m, _ := e.job("stuck"); m > have || !e.round() {
				break
			}
		}
	}
	require.Equal(e.t, 2*n, e.count(`SELECT count(*) FROM author_llm_attempt a
		JOIN author_llm_call c ON c.id = a.call_id JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.slot = 'production_a' AND j.source_fingerprint IN (?, ?) AND c.items = 2 AND a.outcome = 'timeout'`,
		testFingerprint("stuck"), testFingerprint("stuck2")), "the stuck items did not share their calls")
}

func TestAuthorLLMBatchTimeoutsDoNotFailAnItem(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	// Two stuck items travel together in two-item calls; one-item inputs of
	// the same participant are answered between them.
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 2)
	e.reviewPair(&run, "stuck2", stuckItem, 2)
	e.start()

	e.sharedTimeouts(&run, 5)
	two := e.settings.CallTimeout(2)
	requireTimeouts(t, e.latencies(), two, two, two, two, two)
	for _, seed := range []string{"stuck", "stuck2"} {
		status, failures, class := e.job(seed)
		require.Equal(t, "pending", status, "a batch call's timeouts failed one of its items")
		require.Equal(t, 5, failures)
		require.Equal(t, "timeout", class)
	}
}

func TestAuthorLLMBatchTimeoutsDoNotStartAStreak(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 2)
	e.reviewPair(&run, "stuck2", stuckItem, 2)
	e.start()
	e.sharedTimeouts(&run, 2)

	// The partner ends; the stuck item goes alone from now on.
	_, err := e.db.Exec(`UPDATE author_llm_job SET status = 'failed', finished_at = now()
		WHERE slot = 'production_a' AND source_fingerprint = ?`, testFingerprint("stuck2"))
	require.NoError(t, err)
	e.fedAt["stuck"] = 1 // feed after the second shared timeout too

	// Its escalation starts at its first one-item timeout: its batch
	// timeouts do not count.
	e.untilAttempts(&run, "stuck", 5)
	status, _, _ := e.job("stuck")
	require.Equal(t, "pending", status, "a batch call's timeout counted in the item's streak")
	lat := e.latencies()
	requireTimeouts(t, lat[2:], requestTimeout, escalation[0], escalation[1])
	e.untilAttempts(&run, "stuck", 6)
	status, _, class := e.job("stuck")
	require.Equal(t, "failed", status)
	require.Equal(t, "item_timeout", class)
}

func TestAuthorLLMReopenedJobCountsTimeoutsAfresh(t *testing.T) {
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 1)
	e.start()
	e.untilAttempts(&run, "stuck", 4)
	status, _, _ := e.job("stuck")
	require.Equal(t, "failed", status)

	// Reopened as a resolve run reopens a transient failure: its earlier
	// timeouts are another run of failures, and the escalation starts over.
	_, err := e.db.Exec(`UPDATE author_llm_job SET status = 'pending', failure_count = 0, transient_since = NULL,
			finished_at = NULL, next_attempt_at = now()
		WHERE slot = 'production_a' AND source_fingerprint = ?`, testFingerprint("stuck"))
	require.NoError(t, err)
	e.fedAt["stuck"] = 0
	e.untilAttempts(&run, "stuck", 3)
	status, _, _ = e.job("stuck")
	require.Equal(t, "pending", status, "timeouts before the reopening counted again")
	requireTimeouts(t, e.latencies()[4:], requestTimeout, escalation[0], escalation[1])
	e.untilAttempts(&run, "stuck", 4)
	status, _, class := e.job("stuck")
	require.Equal(t, "failed", status)
	require.Equal(t, "item_timeout", class)
}

func TestAuthorLLMStuckLastJobFails(t *testing.T) {
	// Run 25's shape: the participant answers others while the stuck job
	// starts timing out, then the stuck job is all that is left — nothing
	// else to answer between its later timeouts.
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.reviewPair(&run, "stuck", stuckItem, 1)
	e.start()
	e.untilAttempts(&run, "stuck", 1) // one more input after the first timeout
	e.untilAttempts(nil, "stuck", 4)
	status, failures, class := e.job("stuck")
	require.Equal(t, "failed", status, "the last stuck job held the run")
	require.Equal(t, 4, failures)
	require.Equal(t, "item_timeout", class)
}

func TestAuthorLLMLastJobInAnOutageStillRetries(t *testing.T) {
	// The participant answered before the outage; in it the job is all that
	// is left and nothing is answered after its first timeout.
	e := newItemTimeoutEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "fine", givenFamily, 1)
	e.start()
	e.untilAttempts(nil, "fine", 100)
	e.mu.Lock()
	e.down = true
	e.mu.Unlock()
	e.reviewPair(&run, "last", givenFamily, 1)
	e.untilAttempts(nil, "last", 5)
	status, failures, class := e.job("last")
	require.Equal(t, "pending", status, "an outage failed the last job")
	require.Equal(t, 5, failures)
	require.Equal(t, "timeout", class)
}
