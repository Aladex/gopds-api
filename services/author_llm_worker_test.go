package services_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/internal/scanfixture"
	"gopds-api/llm"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The LLM worker against a real database and a fake OpenAI-compatible
// endpoint: what each kind of response does to the calls, attempts, jobs,
// the participants' shared state, the token budget and the verdicts.

// fakeItem is one item of a request as the fake endpoint parses it.
type fakeItem struct {
	ID     string `json:"id"`
	Tokens []struct {
		I     int    `json:"i"`
		Text  string `json:"text"`
		Field string `json:"field"`
	} `json:"tokens"`
	BookContext json.RawMessage `json:"book_context"`
}

// fakeRequest is one request the fake endpoint served.
type fakeRequest struct {
	Model string
	User  string
	Items []fakeItem
	Raw   string
}

// fakeReply is what the fake endpoint answers to one request.
type fakeReply struct {
	Status  int
	Header  map[string]string
	Body    string
	Content string
	Model   string
	Prompt  int64
	Output  int64
}

// fakeLLM is a programmable chat-completions endpoint.
type fakeLLM struct {
	mu       sync.Mutex
	requests []fakeRequest
	// respond decides the reply; nil answers every item correctly.
	respond func(req *fakeRequest) fakeReply
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &body)
	req := fakeRequest{Model: body.Model, Raw: string(raw)}
	if len(body.Messages) == 2 {
		req.User = body.Messages[1].Content
		var msg struct {
			Items []fakeItem `json:"items"`
		}
		_ = json.Unmarshal([]byte(req.User), &msg)
		req.Items = msg.Items
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	respond := f.respond
	f.mu.Unlock()

	reply := fakeReply{}
	if respond != nil {
		reply = respond(&req)
	}
	if reply.Status == 0 {
		reply.Status = http.StatusOK
	}
	for k, v := range reply.Header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(reply.Status)
	if reply.Body != "" {
		_, _ = w.Write([]byte(reply.Body))
		return
	}
	if reply.Content == "" {
		reply.Content = correctResults(req.Items)
	}
	if reply.Model == "" {
		reply.Model = req.Model
	}
	if reply.Prompt == 0 {
		reply.Prompt = 100
		if isFingerprint(&req) {
			reply.Prompt = checkPrompt
		}
	}
	if reply.Output == 0 {
		reply.Output = 20
	}
	_, _ = w.Write([]byte(completionBody(reply.Model, reply.Content, reply.Prompt, reply.Output)))
}

func (f *fakeLLM) setRespond(fn func(req *fakeRequest) fakeReply) {
	f.mu.Lock()
	f.respond = fn
	f.mu.Unlock()
}

func (f *fakeLLM) served() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

// completionBody is a chat completion with content and usage.
func completionBody(model, content string, prompt, output int64) string {
	out, err := json.Marshal(map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": content}}},
		"usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": output, "total_tokens": prompt + output},
	})
	if err != nil {
		panic(err)
	}
	return string(out)
}

// itemReply is the correct reply to a two-token given-family item.
func itemReply(id string) map[string]any {
	return map[string]any{"id": id, "form": "person", "order": "given_first",
		"roles":    []any{map[string]any{"i": 0, "role": "given"}, map[string]any{"i": 1, "role": "family"}},
		"case_fix": []any{}}
}

// correctResults answers every item with itemReply.
func correctResults(items []fakeItem) string {
	results := make([]any, 0, len(items))
	for _, it := range items {
		results = append(results, itemReply(it.ID))
	}
	out, _ := json.Marshal(map[string]any{"results": results})
	return string(out)
}

// llmEnv is one scratch database with a stored configuration pointing at a
// fake endpoint.
type llmEnv struct {
	t        *testing.T
	db       *pg.DB
	fake     *fakeLLM
	srv      *httptest.Server
	identity llmreq.Identity
	version  string
	settings config.AuthorLLMConfig
	archives string
}

// testOutput is the default output ceiling of the section.
var testOutput = llmreq.OutputLimits{Field: llmreq.OutputFieldMaxCompletionTokens, PerCall: 1000, PerItem: 3000}

var testParticipants = []llmreq.Participant{
	{Slot: llmreq.SlotProductionA, Model: "gpt-6-luna"},
	{Slot: llmreq.SlotProductionB, Model: "deepseek-v4-pro"},
	{Slot: llmreq.SlotJudgeA, Model: "gpt-6-sol"},
	{Slot: llmreq.SlotJudgeB, Model: "claude-opus-5"},
}

func newLLMEnv(t *testing.T) *llmEnv {
	t.Helper()
	db := scanfixture.ScratchDB(t)
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	identity, err := llmreq.NewIdentity(srv.URL+"/v1", testParticipants, testOutput)
	require.NoError(t, err)
	version, err := database.EnsureAuthorLLMConfig(context.Background(), db, &identity, 4)
	require.NoError(t, err)
	settings := config.DefaultAuthorLLMConfig()
	settings.RequestTimeout = 5 * time.Second
	return &llmEnv{t: t, db: db, fake: fake, srv: srv, identity: identity, version: version, settings: settings,
		archives: t.TempDir()}
}

func (e *llmEnv) worker() *services.AuthorLLMWorker {
	e.t.Helper()
	client := llm.NewClient(config.LLMConfig{BaseURL: e.srv.URL + "/v1", Timeout: 30 * time.Second})
	w, err := services.NewAuthorLLMWorker(e.db, client, &services.AuthorLLMWorkerConfig{
		Settings: e.settings, Identity: e.identity, ConfigVersion: e.version, ArchivesDir: e.archives,
		BaseBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond,
	})
	require.NoError(e.t, err)
	return w
}

// drain runs the worker until a round settles nothing.
func (e *llmEnv) drain(w *services.AuthorLLMWorker) {
	e.t.Helper()
	for range 50 {
		n, err := w.RunOnce(context.Background())
		require.NoError(e.t, err)
		if n == 0 {
			return
		}
		time.Sleep(15 * time.Millisecond) // past the test backoff
	}
	e.t.Fatal("the LLM worker never ran out of work")
}

// run starts a run; a resolve run by default, with the evidence a
// production run carries (the learned model ids and the 777-token
// references of the production pair) unless the spec names its own.
func (e *llmEnv) run(spec *database.AuthorLLMRunSpec) int64 {
	e.t.Helper()
	spec.ConfigVersion = e.version
	if spec.Kind == "" {
		spec.Kind, spec.Mode = models.AuthorLLMRunResolve, "pilot"
	}
	if spec.Kind != models.AuthorLLMRunEval {
		if spec.ExpectedModels == nil {
			spec.ExpectedModels = map[llmreq.Slot][]string{
				llmreq.SlotProductionA: {"gpt-6-luna"}, llmreq.SlotProductionB: {"deepseek-v4-pro", "deepseek-v4-pro-0813"},
			}
		}
		if spec.Reference == nil {
			ref := database.AuthorLLMFingerprintReference{PromptTokens: checkPrompt, OutputMode: "json_schema"}
			spec.Reference = map[llmreq.Slot]database.AuthorLLMFingerprintReference{
				llmreq.SlotProductionA: ref, llmreq.SlotProductionB: ref,
			}
		}
	}
	id, err := database.CreateAuthorLLMRun(context.Background(), e.db, spec)
	require.NoError(e.t, err)
	require.NoError(e.t, database.StartAuthorLLMRun(context.Background(), e.db, id))
	return id
}

func testFingerprint(seed string) []byte {
	sum := sha256.Sum256([]byte(seed))
	return sum[:]
}

var givenFamily = llmreq.ItemInput{
	Fields: []llmres.FieldValue{{Field: llmres.FieldFirst, Text: "Иван"}, {Field: llmres.FieldLast, Text: "Петров"}},
	Script: "Cyrl", Flags: []string{},
}

// reviewPair enqueues the production pair's jobs for one input.
func (e *llmEnv) reviewPair(runID *int64, seed string, input llmreq.ItemInput, batch int) {
	e.t.Helper()
	var jobs []database.AuthorLLMNewJob
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
		jobs = append(jobs, database.AuthorLLMNewJob{ConfigVersion: e.version, Slot: slot, RunID: runID,
			Purpose: models.AuthorLLMPurposeReview, SourceFingerprint: testFingerprint(seed), ExtractorVersion: "x1",
			BatchSize: batch, Input: input})
	}
	n, err := database.EnqueueAuthorLLMJobs(context.Background(), e.db, jobs)
	require.NoError(e.t, err)
	require.Equal(e.t, 2, n)
}

func (e *llmEnv) count(query string, params ...any) int {
	e.t.Helper()
	var n int
	_, err := e.db.QueryOne(pg.Scan(&n), query, params...)
	require.NoError(e.t, err)
	return n
}

func (e *llmEnv) state(slot llmreq.Slot) *models.AuthorLLMProviderState {
	e.t.Helper()
	st, err := database.GetAuthorLLMProviderState(context.Background(), e.db, e.version, slot)
	require.NoError(e.t, err)
	return st
}

func (e *llmEnv) claimOptions(slot llmreq.Slot, owner string, at *time.Time) *database.AuthorLLMClaimOptions {
	s := e.settings
	return &database.AuthorLLMClaimOptions{ConfigVersion: e.version, Slot: slot, Owner: owner,
		Lease:            func(items int) time.Duration { return s.CallTimeout(items) + time.Minute },
		ConcurrencyTotal: s.ConcurrencyTotal, FingerprintEvery: s.FingerprintEvery, MaxAttempts: s.MaxAttempts,
		Output: testOutput, OneOffTokens: s.Budget.OneOffTokens, RunOverrun: s.Budget.RunOverrun, At: at}
}

func TestAuthorLLMPairAgreesAndSettlesUsage(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "agree", givenFamily, 1)

	e.drain(e.worker())

	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_job WHERE status = 'answered'`))
	var v struct {
		Verdict     string
		Tier        *string
		ConfirmedAt *time.Time
	}
	_, err := e.db.QueryOne(&v, `SELECT verdict, tier, confirmed_at FROM author_llm_verdict`)
	require.NoError(t, err)
	assert.Equal(t, "agree", v.Verdict)
	require.NotNil(t, v.Tier)
	assert.Equal(t, "confirm", *v.Tier)
	assert.NotNil(t, v.ConfirmedAt, "the tail checks confirm it")

	// Usage replaces the reserve: 100 + 20 per job call, 777 + 20 per check
	// (two at the start, two for the tail).
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_call
		WHERE kind = 'jobs' AND outcome = 'answered' AND settled_tokens = 120 AND prompt_tokens = 100
			AND response_model IS NOT NULL`))
	assert.Equal(t, 4, e.count(`SELECT count(*) FROM author_llm_call WHERE kind = 'fingerprint' AND settled_tokens = 797`))
	spent, err := database.AuthorLLMTokensSpent(context.Background(), e.db, run)
	require.NoError(t, err)
	assert.Equal(t, int64(2*120+4*797), spent)
	require.NoError(t, database.FoldAuthorLLMTokenTally(context.Background(), e.db))
	spent, err = database.AuthorLLMTokensSpent(context.Background(), e.db, run)
	require.NoError(t, err)
	assert.Equal(t, int64(2*120+4*797), spent, "folding keeps the sum")

	// Each participant got its own model, the system prompt and the schema.
	var served []fakeRequest
	for _, req := range e.fake.served() {
		if !isFingerprint(&req) {
			served = append(served, req)
		}
	}
	require.Len(t, served, 2)
	asked := map[string]bool{served[0].Model: true, served[1].Model: true}
	assert.True(t, asked["gpt-6-luna"] && asked["deepseek-v4-pro"], "models = %v", asked)
	assert.Contains(t, served[0].Raw, `"json_schema"`)
}

func TestAuthorLLMDisagreementAndInvalidReplies(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "disagree", givenFamily, 1)
	e.reviewPair(&run, "invalid", llmreq.ItemInput{Fields: []llmres.FieldValue{
		{Field: llmres.FieldFirst, Text: "Анна"}, {Field: llmres.FieldLast, Text: "Сидорова"}}, Script: "Cyrl"}, 1)

	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		if req.Model != "deepseek-v4-pro" {
			return fakeReply{}
		}
		if req.Items[0].Tokens[0].Text == "Анна" {
			// Roles that do not cover the tokens: V2.
			return fakeReply{Content: `{"results":[{"id":"1","form":"person","order":"given_first",` +
				`"roles":[{"i":0,"role":"given"}],"case_fix":[]}]}`}
		}
		return fakeReply{Content: `{"results":[{"id":"1","form":"person","order":"family_first",` +
			`"roles":[{"i":0,"role":"family"},{"i":1,"role":"given"}],"case_fix":[]}]}`}
	}))
	e.drain(e.worker())

	var verdicts []struct {
		Verdict string
		Seed    []byte `pg:"source_fingerprint"`
	}
	_, err := e.db.Query(&verdicts, `SELECT verdict, source_fingerprint FROM author_llm_verdict ORDER BY id`)
	require.NoError(t, err)
	got := map[string]string{}
	for _, v := range verdicts {
		if bytes.Equal(v.Seed, testFingerprint("disagree")) {
			got["disagree"] = v.Verdict
		} else {
			got["invalid"] = v.Verdict
		}
	}
	assert.Equal(t, map[string]string{"disagree": "disagree", "invalid": "invalid"}, got)
	// The invalid reply was retried once, then the job ended invalid.
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.status = 'invalid' AND a.outcome = 'invalid' AND a.validator_class = 'v2_violation'`))
}

func TestAuthorLLMCrashBetweenCallAndSettle(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "crash", givenFamily, 1)
	passChecks(t, e.worker())
	checks, err := database.AuthorLLMTokensSpent(ctx, e.db, run)
	require.NoError(t, err)

	dead := database.NewLeaseOwner()
	claim, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, dead, nil))
	require.NoError(t, err)
	require.NotNil(t, claim, "refusal %s", refusal)
	reserved := testOutput.Reserve(1, 0)
	spent, err := database.AuthorLLMTokensSpent(ctx, e.db, run)
	require.NoError(t, err)
	assert.Equal(t, checks+reserved, spent, "the reserve is spent from the claim on")

	// The process died: nothing settles. Past the lease, the next claim
	// closes the call as abandoned and claims the job again.
	later := claim.LeaseExpiresAt.Add(time.Second)
	again, _, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), &later))
	require.NoError(t, err)
	require.NotNil(t, again)
	assert.Equal(t, claim.Jobs[0].JobID, again.Jobs[0].JobID)
	assert.Equal(t, 2, again.Jobs[0].AttemptNo)

	var dead1 struct {
		Outcome       string
		SettledTokens int64
		ErrorClass    string
	}
	_, err = e.db.QueryOne(&dead1, `SELECT outcome, settled_tokens, error_class FROM author_llm_call WHERE id = ?`, claim.CallID)
	require.NoError(t, err)
	assert.Equal(t, "abandoned", dead1.Outcome)
	assert.Equal(t, reserved, dead1.SettledTokens, "an abandoned call keeps its reserve as spent")
	assert.Equal(t, "lease_expired", dead1.ErrorClass)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt WHERE call_id = ? AND outcome = 'abandoned'`, claim.CallID))
	assert.Equal(t, 1, e.count(`SELECT failure_count FROM author_llm_job WHERE id = ?`, claim.Jobs[0].JobID))
	spent, err = database.AuthorLLMTokensSpent(ctx, e.db, run)
	require.NoError(t, err)
	assert.Equal(t, checks+2*reserved, spent, "the abandoned reserve and the new one")

	// The late settle of the dead owner changes nothing.
	_, err = database.SettleAuthorLLMCall(ctx, e.db, &database.AuthorLLMSettlement{CallID: claim.CallID, Owner: dead,
		Outcome: models.AuthorLLMCallAnswered, MaxAttempts: 5, ConcurrencyMax: 16,
		BaseBackoff: time.Second, MaxBackoff: time.Minute})
	assert.ErrorIs(t, err, database.ErrAuthorLLMLeaseLost)
}

func TestAuthorLLMReplicasRespectConcurrencyAndBudget(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	run := e.run(&database.AuthorLLMRunSpec{})
	for i := range 30 {
		e.reviewPair(&run, "replica-"+strconv.Itoa(i), givenFamily, 1)
	}
	passChecks(t, e.worker())

	// Two replicas claim production_a at once, never settling: the window
	// (4) holds across them.
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := 0
	for range 2 {
		owner := database.NewLeaseOwner()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				c, _, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, owner, nil))
				assert.NoError(t, err)
				if c != nil {
					mu.Lock()
					claimed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 4, claimed)
	assert.Equal(t, 4, e.count(`SELECT count(*) FROM author_llm_call WHERE slot = 'production_a' AND finished_at IS NULL`))

	// The one-off budget admits exactly two more reserves of production_b,
	// whoever claims them.
	reserve := testOutput.Reserve(1, 0)
	spent, err := database.AuthorLLMTokensSpent(ctx, e.db, run)
	require.NoError(t, err)
	limited := e.claimOptions(llmreq.SlotProductionB, database.NewLeaseOwner(), nil)
	limited.OneOffTokens = spent + 2*reserve
	other := *limited
	other.Owner = database.NewLeaseOwner()
	var got int
	var refusals []database.AuthorLLMClaimRefusal
	for i := range 6 {
		opts := limited
		if i%2 == 1 {
			opts = &other
		}
		c, refusal, claimErr := database.ClaimAuthorLLMCall(ctx, e.db, opts)
		require.NoError(t, claimErr)
		if c != nil {
			got++
		} else {
			refusals = append(refusals, refusal)
		}
	}
	assert.Equal(t, 2, got)
	// The first refusal pauses the run; after that the run has nothing to
	// claim.
	assert.Equal(t, []database.AuthorLLMClaimRefusal{database.AuthorLLMClaimBudgetOneOff,
		database.AuthorLLMClaimIdle, database.AuthorLLMClaimIdle, database.AuthorLLMClaimIdle}, refusals)
	r, err := database.GetAuthorLLMRun(ctx, e.db, run)
	require.NoError(t, err)
	assert.Equal(t, models.AuthorLLMRunPaused, r.Status)
	require.NotNil(t, r.PausedReason)
	assert.Equal(t, "budget", *r.PausedReason)
}

func TestAuthorLLMRunOverrunPausesTheRun(t *testing.T) {
	e := newLLMEnv(t)
	reserve := testOutput.Reserve(1, 0)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "overrun-1", givenFamily, 1)
	e.reviewPair(&run, "overrun-2", givenFamily, 1)
	ctx := context.Background()
	passChecks(t, e.worker())
	// An estimate whose 1.5x admits one more reserve and not two.
	spent, err := database.AuthorLLMTokensSpent(ctx, e.db, run)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE author_llm_run SET estimate_tokens = ? WHERE id = ?`,
		int64(float64(spent+reserve)/1.5)+1, run)
	require.NoError(t, err)
	c, _, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), nil))
	require.NoError(t, err)
	require.NotNil(t, c)
	c, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), nil))
	require.NoError(t, err)
	assert.Nil(t, c)
	assert.Equal(t, database.AuthorLLMClaimBudgetRun, refusal)
	r, err := database.GetAuthorLLMRun(ctx, e.db, run)
	require.NoError(t, err)
	assert.Equal(t, models.AuthorLLMRunPaused, r.Status)
	assert.Equal(t, "budget", *r.PausedReason)
}

func TestAuthorLLM429HalvesTheWindow(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "throttle", givenFamily, 1)
	w := e.worker()
	passChecks(t, w)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		return fakeReply{Status: 429, Header: map[string]string{"Retry-After": "30"}, Body: `{"error":{"type":"rate"}}`}
	})
	_, err := w.RunOnce(context.Background())
	require.NoError(t, err)

	st := e.state(llmreq.SlotProductionA)
	assert.Equal(t, 2, st.Concurrency, "4 halved")
	require.NotNil(t, st.ThrottledUntil)
	assert.Greater(t, time.Until(*st.ThrottledUntil), 20*time.Second, "Retry-After throttles the participant")
	assert.Equal(t, 0, e.count(`SELECT failure_count FROM author_llm_job WHERE slot = 'production_a'`),
		"a 429 is not the job's failure")
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_job WHERE status = 'pending'`))
	_, refusal, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	assert.Equal(t, database.AuthorLLMClaimThrottled, refusal)
}

func TestAuthorLLMPausesByErrorClass(t *testing.T) {
	for _, tc := range []struct {
		status   int
		reason   string
		endpoint bool
	}{
		{402, "quota_exhausted", true},
		{401, "auth", false},
		{403, "auth", false},
		{400, "bad_request", false},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			e := newLLMEnv(t)
			ctx := context.Background()
			run := e.run(&database.AuthorLLMRunSpec{})
			e.reviewPair(&run, "pause", givenFamily, 1)
			w := e.worker()
			passChecks(t, w)
			e.fake.setRespond(func(req *fakeRequest) fakeReply {
				return fakeReply{Status: tc.status, Body: `{"error":{"message":"no"}}`}
			})
			claimProcess(t, w, llmreq.SlotProductionA)

			endpoint, err := database.GetAuthorLLMEndpointState(ctx, e.db, e.version)
			require.NoError(t, err)
			if tc.endpoint {
				require.NotNil(t, endpoint.PausedReason)
				assert.Equal(t, tc.reason, *endpoint.PausedReason)
			} else {
				assert.Nil(t, endpoint.PausedReason)
			}
			for _, slot := range llmreq.Slots() {
				st := e.state(slot)
				if slot == llmreq.SlotProductionA && !tc.endpoint {
					require.NotNil(t, st.PausedReason, "slot %s", slot)
					assert.Equal(t, tc.reason, *st.PausedReason)
				} else {
					assert.Nil(t, st.PausedReason, "slot %s", slot)
				}
			}
			want := database.AuthorLLMClaimPaused
			if tc.endpoint {
				want = database.AuthorLLMClaimEndpointPaused
			}
			for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
				_, refusal, claimErr := w.Claim(ctx, slot)
				require.NoError(t, claimErr)
				if slot == llmreq.SlotProductionA || tc.endpoint {
					assert.Equal(t, want, refusal, "slot %s", slot)
				}
			}
			assert.Equal(t, 0, e.count(`SELECT failure_count FROM author_llm_job WHERE slot = 'production_a'`))
			if tc.endpoint {
				require.NoError(t, database.ResumeAuthorLLMEndpoint(ctx, e.db, e.version))
				endpoint, err = database.GetAuthorLLMEndpointState(ctx, e.db, e.version)
				require.NoError(t, err)
				assert.Nil(t, endpoint.PausedReason)
				return
			}
			require.NoError(t, database.ResumeAuthorLLMParticipant(ctx, e.db, e.version, llmreq.SlotProductionA))
			assert.Nil(t, e.state(llmreq.SlotProductionA).PausedReason)
		})
	}
}

func TestAuthorLLMModelMismatchPausesAfterThree(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{ExpectedModels: map[llmreq.Slot][]string{
		llmreq.SlotProductionA: {"gpt-6-luna"}, llmreq.SlotProductionB: {"deepseek-v4-pro-0813"},
	}})
	for i := range 4 {
		e.reviewPair(&run, "mismatch-"+strconv.Itoa(i), givenFamily, 1)
	}
	// production_b's check answers under the learned snapshot id; its jobs
	// then answer under another.
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		switch {
		case req.Model != "deepseek-v4-pro":
			return fakeReply{}
		case isFingerprint(req):
			return fakeReply{Model: "deepseek-v4-pro-0813"}
		}
		return fakeReply{Model: "deepseek-v4-flash"}
	})
	w := e.worker()
	passChecks(t, w)
	for range 3 {
		claim, refusal, err := w.Claim(context.Background(), llmreq.SlotProductionB)
		require.NoError(t, err)
		require.NotNil(t, claim, "refusal %s", refusal)
		require.NoError(t, w.Process(context.Background(), llmreq.SlotProductionB, claim))
	}
	st := e.state(llmreq.SlotProductionB)
	require.NotNil(t, st.PausedReason)
	assert.Equal(t, "model_mismatch", *st.PausedReason)
	assert.Equal(t, 3, e.count(`SELECT count(*) FROM author_llm_attempt WHERE outcome = 'model_mismatch'`))
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_b' AND status <> 'pending'`),
		"a mismatched answer is nobody's answer")
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_verdict`))
}

// fingerprintRun is a run whose production pair is checked against a
// reference of 777 prompt tokens: every production run.
func (e *llmEnv) fingerprintRun() int64 {
	return e.run(&database.AuthorLLMRunSpec{})
}

// isFingerprint tells the fixed check request from a job request.
func isFingerprint(req *fakeRequest) bool {
	return len(req.Items) == 1 && len(req.Items[0].Tokens) > 0 && req.Items[0].Tokens[0].Text == "Сергей"
}

func TestAuthorLLMFingerprintChecksConfirmVerdicts(t *testing.T) {
	e := newLLMEnv(t)
	run := e.fingerprintRun()
	e.reviewPair(&run, "checked", givenFamily, 1)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if isFingerprint(req) {
			return fakeReply{Prompt: 777}
		}
		return fakeReply{}
	})
	w := e.worker()

	// The run starts with a check of each participant before any job.
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
		claim, _, err := w.Claim(context.Background(), slot)
		require.NoError(t, err)
		require.NotNil(t, claim)
		assert.Equal(t, models.AuthorLLMCallFingerprint, claim.Kind)
		require.NoError(t, w.Process(context.Background(), slot, claim))
		assert.NotNil(t, e.state(slot).LastCheckPassedAt)
	}
	// The answers come after the last check: the verdict is conditional.
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
		claim, _, err := w.Claim(context.Background(), slot)
		require.NoError(t, err)
		require.Equal(t, models.AuthorLLMCallJobs, claim.Kind)
		require.NoError(t, w.Process(context.Background(), slot, claim))
	}
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE verdict = 'agree' AND confirmed_at IS NULL`))

	// Nothing left to answer: the tail gets a check, which confirms it.
	e.drain(w)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE verdict = 'agree' AND confirmed_at IS NOT NULL`))
	assert.Equal(t, 4, e.count(`SELECT count(*) FROM author_llm_call WHERE kind = 'fingerprint' AND outcome = 'answered'`))
}

func TestAuthorLLMFingerprintDriftHoldsTheWindow(t *testing.T) {
	e := newLLMEnv(t)
	run := e.fingerprintRun()
	e.reviewPair(&run, "drift", givenFamily, 1)
	var checks atomic.Int32
	var drifted atomic.Bool
	drifted.Store(true)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if !isFingerprint(req) {
			return fakeReply{}
		}
		if req.Model == "deepseek-v4-pro" && drifted.Load() {
			if checks.Add(1) > 1 {
				// The gateway moved the model to another tier: another
				// tokenizer, another count for the same request.
				return fakeReply{Prompt: 1544}
			}
		}
		return fakeReply{Prompt: 777}
	})
	w := e.worker()
	e.drain(w)

	st := e.state(llmreq.SlotProductionB)
	require.NotNil(t, st.PausedReason)
	assert.Equal(t, "drift", *st.PausedReason)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE verdict = 'held_drift'`))
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_verdict WHERE confirmed_at IS NOT NULL`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_b' AND status = 'pending'
		AND last_error_class = 'drift'`), "the drifted participant's answers go back to the queue")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a' AND status = 'answered'`),
		"the other participant's answer stands")

	// Resumed, the participant checks again before it answers; a new
	// verdict replaces the held one.
	drifted.Store(false)
	require.NoError(t, database.ResumeAuthorLLMParticipant(context.Background(), e.db, e.version, llmreq.SlotProductionB))
	e.drain(w)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE verdict = 'agree' AND confirmed_at IS NOT NULL`))
}

func TestAuthorLLMBatchValidatesItemsIndependently(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	for i := range 3 {
		e.reviewPair(&run, "batch-"+strconv.Itoa(i), givenFamily, 3)
	}
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if len(req.Items) != 3 {
			return fakeReply{}
		}
		// Item 2 is missing, item 3 comes twice.
		results := []any{itemReply("1"), itemReply("3"), itemReply("3"), itemReply("9")}
		out, _ := json.Marshal(map[string]any{"results": results})
		return fakeReply{Content: string(out)}
	})
	w := e.worker()
	passChecks(t, w)
	claim, _, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	require.Len(t, claim.Jobs, 3, "one call carries the batch")
	require.NoError(t, w.Process(context.Background(), llmreq.SlotProductionA, claim))

	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt WHERE call_id = ? AND outcome = 'answered'`, claim.CallID))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt WHERE call_id = ? AND validator_class = 'item_missing'`, claim.CallID))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt
		WHERE call_id = ? AND validator_class = 'item_duplicate'`, claim.CallID))
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a' AND retry_single AND status = 'pending'`))

	// A retried item goes alone, even with a fresh job of its group ready
	// to keep it company.
	e.reviewPair(&run, "batch-late", givenFamily, 3)
	time.Sleep(15 * time.Millisecond)
	retry, _, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	require.Len(t, retry.Jobs, 1)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE id = ? AND retry_single`, retry.Jobs[0].JobID))

	// A reply that is not the batch envelope invalidates every item.
	e.fake.setRespond(func(req *fakeRequest) fakeReply { return fakeReply{Content: "```json\n{}\n```"} })
	claim, _, err = w.Claim(context.Background(), llmreq.SlotProductionB)
	require.NoError(t, err)
	require.Len(t, claim.Jobs, 3)
	require.NoError(t, w.Process(context.Background(), llmreq.SlotProductionB, claim))
	assert.Equal(t, 3, e.count(`SELECT count(*) FROM author_llm_attempt
		WHERE call_id = ? AND validator_class = 'malformed_batch'`, claim.CallID))
}

func TestAuthorLLMRecordedProviderResponses(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{})
	// Lu Xun as the recorded tool-call reply labels him: one field, family
	// first, two tokens.
	luXun := llmreq.ItemInput{Fields: []llmres.FieldValue{{Field: llmres.FieldLast, Text: "Лу Синь"}}, Script: "Cyrl",
		Flags: []string{"single_field"}}
	var jobs []database.AuthorLLMNewJob
	jobs = append(jobs, database.AuthorLLMNewJob{ConfigVersion: e.version, Slot: llmreq.SlotProductionB, RunID: &run,
		Purpose: models.AuthorLLMPurposeReview, SourceFingerprint: testFingerprint("lu"), ExtractorVersion: "x1",
		OutputMode: "tool", Input: luXun})
	_, err := database.EnqueueAuthorLLMJobs(context.Background(), e.db, jobs)
	require.NoError(t, err)

	// The recorded DeepSeek reply: 58 tool-call fragments of one call, the
	// snapshot model id. Its arguments are the single-item reply; the batch
	// wraps them under results with the item's id.
	recorded, err := readRecorded("deepseek_v4_pro_tool_fragments.json")
	require.NoError(t, err)
	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		return fakeReply{Body: wrapToolFragments(recorded, "1")}
	}))
	w := e.worker()
	passChecks(t, w)
	claim, _, err := w.Claim(context.Background(), llmreq.SlotProductionB)
	require.NoError(t, err)
	require.NoError(t, w.Process(context.Background(), llmreq.SlotProductionB, claim))

	var call struct {
		ResponseModel string
		PromptTokens  int64
		Reasoning     int64 `pg:"reasoning_tokens"`
		Usage         map[string]any
	}
	_, err = e.db.QueryOne(&call, `SELECT response_model, prompt_tokens, reasoning_tokens, usage
		FROM author_llm_call WHERE id = ?`, claim.CallID)
	require.NoError(t, err)
	assert.Equal(t, "deepseek-v4-pro-0813", call.ResponseModel)
	assert.Equal(t, int64(1001), call.PromptTokens)
	assert.Equal(t, int64(312), call.Reasoning)
	assert.Contains(t, call.Usage, "credit", "provider fields are kept for reference")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt
		WHERE call_id = ? AND outcome = 'answered' AND tier = 'restructure'`, claim.CallID))
	served := e.fake.served()
	assert.Contains(t, served[len(served)-1].Raw, `"tool_choice"`)
}

// readRecorded reads a recorded provider response from the llm package's
// test data.
func readRecorded(name string) (map[string]any, error) {
	raw, err := os.ReadFile("../llm/testdata/" + name)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(raw, &out)
	return out, err
}

// wrapToolFragments rewrites a recorded single-item tool reply into the batch
// shape: the first fragment opens {"results":[{"id":...,, the last closes ]}.
// The fragmentation itself is kept as recorded.
func wrapToolFragments(recorded map[string]any, id string) string {
	body := deepCopy(recorded)
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	calls := msg["tool_calls"].([]any)
	calls[0].(map[string]any)["function"].(map[string]any)["name"] = llmreq.SchemaName
	first := calls[1].(map[string]any)["function"].(map[string]any)
	first["arguments"] = fmt.Sprintf(`{"results":[{"id":%q,`, id)
	last := calls[len(calls)-1].(map[string]any)["function"].(map[string]any)
	last["arguments"] = last["arguments"].(string) + "]}"
	out, _ := json.Marshal(body)
	return string(out)
}

func deepCopy(m map[string]any) map[string]any {
	raw, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
