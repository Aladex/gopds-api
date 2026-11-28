package services_test

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DeepSeek's content moderation answers some inputs with HTTP 200, no model
// id and a canned refusal instead of the JSON answer. That is the provider
// refusing the item, as z.ai's 400 code 1301 is, not a substituted model: it
// must take the provider_rejected path, not void the attempt as a
// model_mismatch and requeue it forever.

// moderationStubs are the canned refusals seen on production.
var moderationStubs = []string{
	"关于这个问题，我没有相关信息，您可以尝试问我其它问题，我会尽力为您解答~",
	"作为一个人工智能语言模型，我还没学习如何回答这个问题，您可以向我问一些其它的问题，我会尽力帮您解决的。",
}

// moderated is the fake endpoint's moderation stub: a 200 with an empty model
// id and the canned text, in the usage shape the endpoint reports.
func moderated(i int) fakeReply {
	return fakeReply{Body: completionBody("", moderationStubs[i%len(moderationStubs)], 4549, 33)}
}

// A single-item call answered with the stub ends the job rejected, counted
// like a refusal; the participant goes on, and the attempt says where the
// rejection came from.
func TestAuthorLLMModerationStubEndsTheItemRejected(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "stub-0", givenFamily, 1)
	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		if req.Model == "deepseek-v4-pro" {
			return moderated(0)
		}
		return fakeReply{}
	}))
	w := e.worker()
	passChecks(t, w)
	claimProcess(t, w, llmreq.SlotProductionB)

	assert.Nil(t, e.state(llmreq.SlotProductionB).PausedReason, "a moderation stub paused the participant")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_b'
		AND status = 'invalid' AND last_error_class = 'provider_rejected' AND invalid_count = 0
		AND failure_count = 0 AND finished_at IS NOT NULL`), "the stubbed item ends rejected")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.slot = 'production_b' AND a.outcome = 'invalid' AND a.validator_class = 'provider_rejected'
			AND a.provider_code = 'moderation_stub'`),
		"the attempt tells the stub apart from an HTTP 400")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_call c
		WHERE c.slot = 'production_b' AND c.kind = 'jobs' AND c.outcome = 'answered'
			AND c.error_class = 'provider_rejected' AND c.response_model IS NULL`))
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_attempt WHERE outcome = 'model_mismatch'`))

	e.drain(w)
	assert.Nil(t, e.state(llmreq.SlotProductionB).PausedReason)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE source_fingerprint = ? AND verdict = 'refused'`,
		testFingerprint("stub-0")), "the pair's verdict reads the stub as a refusal")
}

// A batch answered with the stub splits to single items: only the item the
// provider refuses alone ends rejected.
func TestAuthorLLMModerationStubSplitsABatch(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	inputs := map[string]string{"batch-0": "Петров", "batch-1": "Сидорова", "batch-2": "Кузнецов"}
	for seed, last := range inputs {
		e.reviewPair(&run, seed, llmreq.ItemInput{Fields: []llmres.FieldValue{
			{Field: llmres.FieldFirst, Text: "Анна"}, {Field: llmres.FieldLast, Text: last}}, Script: "Cyrl", Flags: []string{}}, 3)
	}
	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		if req.Model == "deepseek-v4-pro" && carries(req, "Сидорова") {
			return moderated(1)
		}
		return fakeReply{}
	}))
	w := e.worker()
	passChecks(t, w)
	batch := claimProcess(t, w, llmreq.SlotProductionB)
	require.Len(t, batch.Jobs, 3, "one call carries the batch")
	assert.Nil(t, e.state(llmreq.SlotProductionB).PausedReason, "a stubbed batch paused the participant")
	assert.Equal(t, 3, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_b' AND status = 'pending'
		AND retry_single AND invalid_count = 0 AND failure_count = 0 AND last_error_class = 'provider_rejected'`),
		"the batch's items go again alone, uncounted")

	time.Sleep(15 * time.Millisecond) // past the test backoff
	e.drain(w)
	assert.Nil(t, e.state(llmreq.SlotProductionB).PausedReason)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_b'
		AND status = 'invalid' AND last_error_class = 'provider_rejected'`))
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_b' AND status = 'answered'`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE source_fingerprint = ? AND verdict = 'refused'`,
		testFingerprint("batch-1")))
}

// Stubs on three distinct items in a row pause the participant bad_request,
// as three 400 rejections do; a stub does not empty the run, an answered
// call does.
func TestAuthorLLMModerationStubsInARowPause(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	lasts := []string{"Петров", "Сидоров", "Кузнецов", "Смирнов", "Попов", "Соколов"}
	for i, last := range lasts {
		e.reviewPair(&run, "streak-"+strconv.Itoa(i), llmreq.ItemInput{Fields: []llmres.FieldValue{
			{Field: llmres.FieldFirst, Text: "Иван"}, {Field: llmres.FieldLast, Text: last}}, Script: "Cyrl", Flags: []string{}}, 1)
	}
	var stub atomic.Bool
	stub.Store(true)
	var (
		mu   sync.Mutex
		sent []string
	)
	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		if req.Model != "deepseek-v4-pro" {
			return fakeReply{}
		}
		mu.Lock()
		sent = append(sent, lastNameOf(&req.Items[0]))
		n := len(sent)
		mu.Unlock()
		if stub.Load() {
			return moderated(n)
		}
		return fakeReply{}
	}))
	w := e.worker()
	passChecks(t, w)
	paused := func() bool { return e.state(llmreq.SlotProductionB).PausedReason != nil }
	step := func(want string) {
		t.Helper()
		claimProcess(t, w, llmreq.SlotProductionB)
		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, want, sent[len(sent)-1], "the claim order is the job order")
	}

	step("Петров")
	step("Сидоров")
	stub.Store(false)
	step("Кузнецов") // answered: the run of rejected items starts again
	stub.Store(true)
	step("Смирнов")
	step("Попов")
	require.False(t, paused(), "an answered call did not start the count again")
	step("Соколов") // the third distinct item in a row
	require.True(t, paused(), "three distinct stubs in a row did not pause the participant")
	assert.Equal(t, "bad_request", *e.state(llmreq.SlotProductionB).PausedReason)
	assert.Equal(t, 4, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_b'
		AND status = 'invalid' AND last_error_class = 'provider_rejected'`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job j WHERE j.slot = 'production_b'
		AND j.status = 'pending' AND j.failure_count = 0 AND j.invalid_count = 0
		AND EXISTS (SELECT 1 FROM author_llm_attempt a WHERE a.job_id = j.id AND a.provider_code = 'moderation_stub')`),
		"the pausing call's item goes back to the queue uncounted")
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_attempt WHERE outcome = 'model_mismatch'`))
}

// Only a reply with no model id and no JSON in it is a provider refusal. A
// JSON answer without a model id, or any reply under a model id outside the
// expected list, may come from a substituted model: it stays a
// model_mismatch, voided and requeued as before.
func TestAuthorLLMModerationStubNeedsNoModelAndNoJSON(t *testing.T) {
	cases := []struct {
		name  string
		reply func(req *fakeRequest) fakeReply
	}{
		{"empty model, JSON answer", func(req *fakeRequest) fakeReply {
			return fakeReply{Body: completionBody("", correctResults(req.Items), 100, 20)}
		}},
		{"wrong model, JSON answer", func(*fakeRequest) fakeReply { return fakeReply{Model: "deepseek-v4-flash"} }},
		{"wrong model, canned text", func(*fakeRequest) fakeReply {
			return fakeReply{Body: completionBody("deepseek-v4-flash", moderationStubs[0], 4549, 33)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newLLMEnv(t)
			run := e.run(&database.AuthorLLMRunSpec{})
			e.reviewPair(&run, "mismatch-0", givenFamily, 1)
			e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
				if req.Model == "deepseek-v4-pro" {
					return tc.reply(req)
				}
				return fakeReply{}
			}))
			w := e.worker()
			passChecks(t, w)
			claimProcess(t, w, llmreq.SlotProductionB)
			assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
				WHERE j.slot = 'production_b' AND a.outcome = 'model_mismatch'`))
			assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_b'
				AND status = 'pending' AND last_error_class = 'model_mismatch' AND invalid_count = 0`))
			assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_job WHERE last_error_class = 'provider_rejected'`))
			assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_call WHERE slot = 'production_b' AND kind = 'jobs'
				AND error_class = 'model_mismatch'`))
		})
	}
}

// A fingerprint check answered with the stub fails the check as before: the
// rejection path never sees a fingerprint call.
func TestAuthorLLMModerationStubFailsAFingerprintCheck(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "check-0", givenFamily, 1)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if isFingerprint(req) && req.Model == "deepseek-v4-pro" {
			return moderated(0)
		}
		return checked(nil)(req)
	})
	w := e.worker()
	claim := claimProcess(t, w, llmreq.SlotProductionB)
	require.Equal(t, "fingerprint", string(claim.Kind))
	st := e.state(llmreq.SlotProductionB)
	require.NotNil(t, st.PausedReason, "a stubbed check passed")
	assert.Equal(t, "drift", *st.PausedReason)
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_call WHERE error_class = 'provider_rejected'`))
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_job WHERE last_error_class = 'provider_rejected'`))
}
