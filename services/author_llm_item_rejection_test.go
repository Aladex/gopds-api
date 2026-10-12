package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A provider that rejects one item — the z.ai coding plan's content filter
// answers HTTP 400 with its code 1301 on a "sensitive" input or output — must
// not pause the participant: the item ends rejected, counted like a refusal,
// and the participant keeps answering the others. Only a run of rejections on
// distinct items with no answered call in between says the participant itself
// is broken.

// zaiSensitive is the z.ai error body of a content-filter rejection, in the
// shape the endpoint sends it.
const zaiSensitive = `{"error":{"code":"1301","message":"系统检测到输入或生成内容可能包含不安全或敏感内容，请您避免输入易产生敏感内容的提示语，感谢您的配合。"}}`

// rejected is the fake endpoint's content-filter reply.
func rejected() fakeReply {
	return fakeReply{Status: http.StatusBadRequest, Body: zaiSensitive}
}

// lastNameOf is the item's last-name token, or its first token's text.
func lastNameOf(it *fakeItem) string {
	for _, tok := range it.Tokens {
		if tok.Field == "last" {
			return tok.Text
		}
	}
	if len(it.Tokens) > 0 {
		return it.Tokens[0].Text
	}
	return ""
}

// carries reports whether a request carries an item with this last name.
func carries(req *fakeRequest, last string) bool {
	for i := range req.Items {
		if lastNameOf(&req.Items[i]) == last {
			return true
		}
	}
	return false
}

// The pair's rejection is the pair's refusal, a judge's the judges' refusal:
// neither is applied, and neither participant is paused.
func TestAuthorLLMResolveItemRejectionIsARefusal(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	answer := byFields(nil)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		switch {
		case isFingerprint(req):
		case req.Model == "deepseek-v4-pro" && carries(req, "Попов"):
			return rejected()
		case req.Model == "claude-opus-5" && carries(req, "Петров"):
			return rejected()
		}
		return answer(req)
	})
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)

	sel := e.selections()
	assert.Equal(t, "llm_refused", deref(sel["Попов"].Reason), "the pair's rejected item")
	assert.Equal(t, "llm_judge_refused", deref(sel["Петров"].Reason), "a judge's rejected item")
	for _, last := range initialsCyrl {
		if last == "Попов" || last == "Петров" {
			assert.Equal(t, "unresolved", sel[last].State, last)
			continue
		}
		assert.Equal(t, "selected", sel[last].State, last)
	}
	for _, slot := range llmreq.Slots() {
		assert.Nil(t, e.state(slot).PausedReason, "slot %s", slot)
	}
	for _, slot := range []string{"production_b", "judge_b"} {
		assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = ?
			AND status = 'invalid' AND last_error_class = 'provider_rejected' AND invalid_count = 0
			AND failure_count = 0`, run.ID, slot), slot)
		assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
			WHERE j.run_id = ? AND j.slot = ? AND a.outcome = 'http_error' AND a.validator_class = 'provider_rejected'
				AND a.provider_code = '1301'`, run.ID, slot), "the attempt keeps the provider's code: %s", slot)
	}
}

// A multi-item call the provider rejects splits to single items first: only
// the item the provider refuses alone ends rejected.
func TestAuthorLLMRejectedBatchSplitsToSingles(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	inputs := map[string]string{"batch-0": "Петров", "batch-1": "Сидорова", "batch-2": "Кузнецов"}
	for seed, last := range inputs {
		e.reviewPair(&run, seed, llmreq.ItemInput{Fields: []llmres.FieldValue{
			{Field: llmres.FieldFirst, Text: "Анна"}, {Field: llmres.FieldLast, Text: last}}, Script: "Cyrl", Flags: []string{}}, 3)
	}
	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		if req.Model == "gpt-6-luna" && carries(req, "Сидорова") {
			return rejected()
		}
		return fakeReply{}
	}))
	w := e.worker()
	passChecks(t, w)
	batch := claimProcess(t, w, llmreq.SlotProductionA)
	require.Len(t, batch.Jobs, 3, "one call carries the batch")
	assert.Nil(t, e.state(llmreq.SlotProductionA).PausedReason, "a rejected batch paused the participant")
	assert.Equal(t, 3, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a' AND status = 'pending'
		AND retry_single AND invalid_count = 0 AND failure_count = 0`), "the batch's items go again alone, uncounted")

	time.Sleep(15 * time.Millisecond) // past the test backoff
	e.drain(w)
	assert.Nil(t, e.state(llmreq.SlotProductionA).PausedReason)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a'
		AND status = 'invalid' AND last_error_class = 'provider_rejected'`))
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a' AND status = 'answered'`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE source_fingerprint = ? AND verdict = 'refused'`,
		testFingerprint("batch-1")))
}

// Three rejections of distinct items in a row, with no answered call in
// between, pause the participant bad_request as before. The same item
// rejected twice counts once; an answered call, and the admin's resume of
// the pause, start the count again.
func TestAuthorLLMThreeRejectionsInARowPause(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	input := func(last string) llmreq.ItemInput {
		return llmreq.ItemInput{Fields: []llmres.FieldValue{
			{Field: llmres.FieldFirst, Text: "Иван"}, {Field: llmres.FieldLast, Text: last}}, Script: "Cyrl", Flags: []string{}}
	}
	lasts := []string{"Петров", "Сидоров", "Кузнецов", "Смирнов", "Попов", "Соколов", "Лебедев"}
	for i, last := range lasts {
		e.reviewPair(&run, "streak-"+strconv.Itoa(i), input(last), 1)
		if i == 0 {
			// The same input under another extractor version: another
			// job, the same item.
			_, err := database.EnqueueAuthorLLMJobs(context.Background(), e.db, []database.AuthorLLMNewJob{{
				ConfigVersion: e.version, Slot: llmreq.SlotProductionA, RunID: &run,
				Purpose: models.AuthorLLMPurposeReview, SourceFingerprint: testFingerprint("streak-0"),
				ExtractorVersion: "x2", BatchSize: 1, Input: input(last)}})
			require.NoError(t, err)
		}
	}
	var reject atomic.Bool
	reject.Store(true)
	var sent []string
	var mu sync.Mutex
	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		if req.Model != "gpt-6-luna" {
			return fakeReply{}
		}
		mu.Lock()
		sent = append(sent, lastNameOf(&req.Items[0]))
		mu.Unlock()
		if reject.Load() {
			return rejected()
		}
		return fakeReply{}
	}))
	w := e.worker()
	passChecks(t, w)
	paused := func() bool { return e.state(llmreq.SlotProductionA).PausedReason != nil }
	step := func(want string) {
		t.Helper()
		claimProcess(t, w, llmreq.SlotProductionA)
		mu.Lock()
		defer mu.Unlock()
		if want != "" {
			require.Equal(t, want, sent[len(sent)-1], "the claim order is the job order")
		}
	}

	step("Петров")
	step("Петров") // its second job: still one item
	step("Сидоров")
	require.False(t, paused(), "the same item rejected twice counted twice")
	step("Кузнецов") // the third distinct item in a row
	require.True(t, paused(), "three distinct rejections in a row did not pause the participant")
	assert.Equal(t, "bad_request", *e.state(llmreq.SlotProductionA).PausedReason)
	assert.Equal(t, 3, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a'
		AND status = 'invalid' AND last_error_class = 'provider_rejected'`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job j WHERE j.slot = 'production_a'
		AND j.status = 'pending' AND j.failure_count = 0 AND j.invalid_count = 0 AND j.last_error_class = 'bad_request'
		AND EXISTS (SELECT 1 FROM author_llm_attempt a WHERE a.job_id = j.id AND a.provider_code = '1301')`),
		"the pausing call's item goes back to the queue, as any bad request's, and keeps the code")

	require.NoError(t, database.ResumeAuthorLLMParticipant(context.Background(), e.db, e.version, llmreq.SlotProductionA))
	time.Sleep(15 * time.Millisecond) // past the test backoff
	// The requeued item waits out its backoff; the order of the rest does
	// not matter, every item is distinct.
	step("")
	require.False(t, paused(), "the resume did not start the count again")
	reject.Store(false)
	step("") // answered
	reject.Store(true)
	step("")
	step("")
	assert.False(t, paused(), "an answered call did not start the count again")
}

// A judge rejecting an item in the eval leaves that item without its answer
// — no gold — and the report counts the rejection next to the invalid
// answers, inside the invalid share the gate bounds. The judge is not paused.
func TestAuthorLLMEvalReportCountsProviderRejections(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	answer := byFields(distinctLabs)
	var (
		mu     sync.Mutex
		target string
	)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if req.Model != "claude-opus-5" || isFingerprint(req) {
			return answer(req)
		}
		mu.Lock()
		defer mu.Unlock()
		for i := range req.Items {
			if target == "" && len(req.Items[i].BookContext) > 0 && string(req.Items[i].BookContext) != "null" {
				target = lastNameOf(&req.Items[i])
			}
		}
		if target != "" && carries(req, target) {
			return rejected()
		}
		return answer(req)
	})
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, nil, nil)
	require.NoError(t, err)
	e.evalUntilDone(run)
	require.NotEmpty(t, target, "judge_b was never asked with an excerpt")
	assert.Nil(t, e.state(llmreq.SlotJudgeB).PausedReason)

	var raw string
	_, err = e.db.QueryOne(pg.Scan(&raw), `SELECT report::text FROM author_llm_eval_report WHERE run_id = ?`, run)
	require.NoError(t, err)
	var doc struct {
		Evaluation llmeval.Report `json:"evaluation"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &doc))
	rejectedJobs := e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b'
		AND last_error_class = 'provider_rejected'`, run)
	require.Positive(t, rejectedJobs)
	counts := func(list []llmeval.ParticipantCount) map[string]llmeval.ParticipantCount {
		out := map[string]llmeval.ParticipantCount{}
		for _, c := range list {
			out[c.Participant] = c
		}
		return out
	}
	rej, inv := counts(doc.Evaluation.ProviderRejected), counts(doc.Evaluation.Invalid)
	assert.Equal(t, rejectedJobs, rej["judge_b"].Matched, "the report's rejections of judge_b")
	assert.Equal(t, inv["judge_b"].Total, rej["judge_b"].Total)
	assert.GreaterOrEqual(t, inv["judge_b"].Matched, rej["judge_b"].Matched, "rejections are inside the invalid share")
	assert.Zero(t, rej["production_a"].Matched)
}
