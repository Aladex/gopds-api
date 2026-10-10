package services_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regressions from the phase-2 review: each test is a reviewer's scenario.

// checkPrompt is the reference prompt token count of the fixed check
// request; the fake endpoint reports it for every check unless told
// otherwise.
const checkPrompt = 777

// checked wraps a job responder: the fixed check request always gets the
// reference count under the requested model.
func checked(jobs func(req *fakeRequest) fakeReply) func(req *fakeRequest) fakeReply {
	return func(req *fakeRequest) fakeReply {
		if isFingerprint(req) {
			return fakeReply{Prompt: checkPrompt}
		}
		if jobs == nil {
			return fakeReply{}
		}
		return jobs(req)
	}
}

// productionRun is a resolve run with what a production run carries: the
// learned model ids and the fingerprint references of the production pair.
func (e *llmEnv) productionRun(version string) int64 {
	e.t.Helper()
	ref := database.AuthorLLMFingerprintReference{PromptTokens: checkPrompt, OutputMode: "json_schema"}
	id, err := database.CreateAuthorLLMRun(context.Background(), e.db, &database.AuthorLLMRunSpec{
		Kind: models.AuthorLLMRunResolve, Mode: "pilot", ConfigVersion: version,
		ExpectedModels: map[llmreq.Slot][]string{
			llmreq.SlotProductionA: {"gpt-6-luna"}, llmreq.SlotProductionB: {"deepseek-v4-pro", "deepseek-v4-pro-0813"},
		},
		Reference: map[llmreq.Slot]database.AuthorLLMFingerprintReference{
			llmreq.SlotProductionA: ref, llmreq.SlotProductionB: ref,
		},
	})
	require.NoError(e.t, err)
	require.NoError(e.t, database.StartAuthorLLMRun(context.Background(), e.db, id))
	return id
}

// claimProcess claims one call of a slot and sends it, failing on a refusal.
func claimProcess(t *testing.T, w *services.AuthorLLMWorker, slot llmreq.Slot) *database.AuthorLLMClaim {
	t.Helper()
	claim, refusal, err := w.Claim(context.Background(), slot)
	require.NoError(t, err)
	require.NotNil(t, claim, "slot %s refused: %s", slot, refusal)
	require.NoError(t, w.Process(context.Background(), slot, claim))
	return claim
}

// passChecks runs the start-of-run check of both production participants.
func passChecks(t *testing.T, w *services.AuthorLLMWorker) {
	t.Helper()
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
		claim := claimProcess(t, w, slot)
		require.Equal(t, models.AuthorLLMCallFingerprint, claim.Kind)
	}
}

// B1: the reserve is a hard bound of the request — its prompt bound plus the
// output ceiling the request itself carries — and an endpoint that ignores
// the ceiling stops the participant at once.
func TestAuthorLLMReserveBoundsThePaidCall(t *testing.T) {
	e := newLLMEnv(t)
	run := e.productionRun(e.version)
	e.reviewPair(&run, "bound-1", givenFamily, 1)
	e.reviewPair(&run, "bound-2", givenFamily, 1)
	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		return fakeReply{Prompt: 100, Output: 50_000}
	}))
	w := e.worker()
	passChecks(t, w)

	claim := claimProcess(t, w, llmreq.SlotProductionA)
	var sent fakeRequest
	for _, req := range e.fake.served() {
		if !isFingerprint(&req) {
			sent = req
		}
	}
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(sent.Raw), &body))
	ceiling, ok := body["max_completion_tokens"].(float64)
	require.True(t, ok, "the request carries no output ceiling: %v", body)
	var call struct {
		Reserved int64 `pg:"reserved_tokens"`
		Settled  int64 `pg:"settled_tokens"`
	}
	_, err := e.db.QueryOne(&call, `SELECT reserved_tokens, settled_tokens FROM author_llm_call WHERE id = ?`, claim.CallID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, call.Reserved, int64(ceiling)+int64(len(sent.Raw)),
		"the reserve covers the request's own bytes (an upper bound of its prompt tokens) and its output ceiling")
	assert.Equal(t, int64(50_100), call.Settled, "the reported usage is recorded as it is")
	st := e.state(llmreq.SlotProductionA)
	require.NotNil(t, st.PausedReason, "an endpoint that ignored the ceiling must stop the participant")
	assert.Equal(t, "over_reserve", *st.PausedReason)

	// A crash keeps the whole bound as spent.
	dead, _, err := database.ClaimAuthorLLMCall(context.Background(), e.db,
		e.claimOptions(llmreq.SlotProductionB, database.NewLeaseOwner(), nil))
	require.NoError(t, err)
	require.NotNil(t, dead)
	later := dead.LeaseExpiresAt.Add(time.Second)
	_, _, err = database.ClaimAuthorLLMCall(context.Background(), e.db,
		e.claimOptions(llmreq.SlotProductionB, database.NewLeaseOwner(), &later))
	require.NoError(t, err)
	var abandoned int64
	_, err = e.db.QueryOne(pg.Scan(&abandoned), `SELECT settled_tokens FROM author_llm_call WHERE id = ?`, dead.CallID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, abandoned, int64(ceiling), "an abandoned call keeps a reserve that covers its ceiling")
}

// secondVersion stores another configuration at the same endpoint.
func (e *llmEnv) secondVersion() string {
	e.t.Helper()
	other := append([]llmreq.Participant(nil), testParticipants...)
	other[2].ReasoningEffort = "low"
	identity, err := llmreq.NewIdentity(e.srv.URL+"/v1", other, testOutput)
	require.NoError(e.t, err)
	version, err := database.EnsureAuthorLLMConfig(context.Background(), e.db, &identity, 4)
	require.NoError(e.t, err)
	require.NotEqual(e.t, e.version, version)
	return version
}

// B2: the endpoint's ceiling counts the open calls of every configuration
// at that endpoint.
func TestAuthorLLMEndpointCapacitySpansVersions(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	old := e.productionRun(e.version)
	first, _, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), nil))
	require.NoError(t, err)
	require.NotNil(t, first, "the old version's first call")
	require.NoError(t, database.CompleteAuthorLLMRun(ctx, e.db, old))

	newer := e.secondVersion()
	e.productionRun(newer)
	opts := e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), nil)
	opts.ConfigVersion = newer
	opts.ConcurrencyTotal = 1
	second, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, opts)
	require.NoError(t, err)
	assert.Nil(t, second, "a second call reached an endpoint whose ceiling is one")
	assert.Equal(t, "concurrency", string(refusal))
}

// B2: an exhausted quota is the endpoint's, whatever configuration hit it.
func TestAuthorLLMQuotaPauseSpansVersions(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	old := e.productionRun(e.version)
	e.fake.setRespond(func(req *fakeRequest) fakeReply { return fakeReply{Status: 402, Body: `{"error":{}}`} })
	w := e.worker()
	claim, _, err := w.Claim(ctx, llmreq.SlotProductionA)
	require.NoError(t, err)
	require.NoError(t, w.Process(ctx, llmreq.SlotProductionA, claim))
	require.NoError(t, database.CompleteAuthorLLMRun(ctx, e.db, old))

	newer := e.secondVersion()
	e.productionRun(newer)
	opts := e.claimOptions(llmreq.SlotProductionB, database.NewLeaseOwner(), nil)
	opts.ConfigVersion = newer
	next, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, opts)
	require.NoError(t, err)
	assert.Nil(t, next, "a new configuration kept calling an endpoint out of quota")
	assert.Equal(t, "endpoint_paused", string(refusal))
}

// B3: production work exists only under a run that carries the learned model
// ids and the fingerprint references; nothing run-less is ever enqueued, and
// the learning exception is the eval run's alone.
func TestAuthorLLMProductionWorkNeedsItsEvidence(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	_, err := database.EnqueueAuthorLLMJobs(ctx, e.db, []database.AuthorLLMNewJob{{
		ConfigVersion: e.version, Slot: llmreq.SlotProductionA, Purpose: models.AuthorLLMPurposeReview,
		SourceFingerprint: testFingerprint("runless"), ExtractorVersion: "x1", Input: givenFamily}})
	assert.ErrorIs(t, err, database.ErrAuthorLLMInvalidJob, "a run-less production job")

	_, err = database.CreateAuthorLLMRun(ctx, e.db, &database.AuthorLLMRunSpec{
		Kind: models.AuthorLLMRunResolve, Mode: "pilot", ConfigVersion: e.version})
	assert.Error(t, err, "a resolve run without learned ids and references")

	eval, err := database.CreateAuthorLLMRun(ctx, e.db, &database.AuthorLLMRunSpec{
		Kind: models.AuthorLLMRunEval, Mode: "dev", ConfigVersion: e.version})
	require.NoError(t, err, "an eval run learns")
	_, err = database.EnqueueAuthorLLMJobs(ctx, e.db, []database.AuthorLLMNewJob{{
		ConfigVersion: e.version, Slot: llmreq.SlotProductionA, RunID: &eval, Purpose: models.AuthorLLMPurposeReview,
		SourceFingerprint: testFingerprint("in-eval"), ExtractorVersion: "x1", Input: givenFamily}})
	assert.ErrorIs(t, err, database.ErrAuthorLLMInvalidJob, "a production job under an eval run")
}

// B4: one check per participant in flight at a time.
func TestAuthorLLMOneCheckInFlight(t *testing.T) {
	e := newLLMEnv(t)
	run := e.productionRun(e.version)
	e.reviewPair(&run, "inflight", givenFamily, 1)
	w := e.worker()
	first, _, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	require.Equal(t, models.AuthorLLMCallFingerprint, first.Kind)
	second, refusal, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	assert.Nil(t, second, "a second check or a job while the due check is in flight")
	assert.Equal(t, "check_in_flight", string(refusal))
}

// B4: a check covers the answers finished before it was sent, not the ones
// that finished while it was in flight.
func TestAuthorLLMLateCheckCannotConfirmANewerAnswer(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.FingerprintEvery = 1
	run := e.productionRun(e.version)
	e.reviewPair(&run, "covered", givenFamily, 1)
	e.reviewPair(&run, "late", givenFamily, 1)
	e.fake.setRespond(checked(nil))
	w := e.worker()
	passChecks(t, w)

	ctx := context.Background()
	jobX, _, err := w.Claim(ctx, llmreq.SlotProductionA)
	require.NoError(t, err)
	jobY, _, err := w.Claim(ctx, llmreq.SlotProductionA)
	require.NoError(t, err)
	require.Equal(t, models.AuthorLLMCallJobs, jobY.Kind)
	require.NoError(t, w.Process(ctx, llmreq.SlotProductionA, jobX)) // one answer: a check is due
	check, _, err := w.Claim(ctx, llmreq.SlotProductionA)
	require.NoError(t, err)
	require.Equal(t, models.AuthorLLMCallFingerprint, check.Kind)
	require.NoError(t, w.Process(ctx, llmreq.SlotProductionA, jobY)) // finishes after the check went out
	require.NoError(t, w.Process(ctx, llmreq.SlotProductionA, check))

	// production_b answers both, each covered by a check sent after it.
	for range 2 {
		require.Equal(t, models.AuthorLLMCallJobs, claimProcess(t, w, llmreq.SlotProductionB).Kind)
		require.Equal(t, models.AuthorLLMCallFingerprint, claimProcess(t, w, llmreq.SlotProductionB).Kind)
	}

	verdict := func(seed string) *time.Time {
		var confirmed *time.Time
		_, qErr := e.db.QueryOne(pg.Scan(&confirmed),
			`SELECT confirmed_at FROM author_llm_verdict WHERE source_fingerprint = ?`, testFingerprint(seed))
		require.NoError(t, qErr)
		return confirmed
	}
	assert.NotNil(t, verdict("covered"))
	assert.Nil(t, verdict("late"), "a check sent before the answer confirmed it")

	e.drain(w) // the tail check covers it
	assert.NotNil(t, verdict("late"))
}

// B5: Run under a backlog keeps at most concurrency_total calls in flight and
// returns promptly once canceled, leaving no handler behind.
func TestAuthorLLMRunStopsUnderBacklog(t *testing.T) {
	e := newLLMEnv(t)
	run := e.productionRun(e.version)
	for i := range 200 {
		e.reviewPair(&run, "backlog-"+strconv.Itoa(i), givenFamily, 1)
	}
	var inflight, peak atomic.Int32
	e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		defer inflight.Add(-1)
		return fakeReply{}
	}))
	e.settings.ConcurrencyTotal = 6
	w := e.worker()

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(stopped)
	}()
	deadline := time.Now().Add(60 * time.Second)
	for e.count(`SELECT count(*) FROM author_llm_call WHERE finished_at IS NOT NULL`) < 80 {
		require.True(t, time.Now().Before(deadline), "the worker did not get through the backlog")
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	assert.LessOrEqual(t, peak.Load(), int32(6), "more calls in flight than concurrency_total")
	// The worker's own handlers, not the process's goroutines: the fake
	// endpoint's and the HTTP client's connections legitimately outlive Run.
	assert.Zero(t, w.ActiveHandlers(), "handlers outlived Run")
}

// twoAuthorBook is a book whose two authors each appear in the body.
func twoAuthorBook(now time.Time) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<genre>sf</genre>
<author><first-name>Arkady</first-name><last-name>Brightwater</last-name></author>
<author><first-name>Boris</first-name><last-name>Coldbrook</last-name></author>
<book-title>Two Hands</book-title>
<lang>en</lang>
</title-info>
<document-info><date>%d</date><id>llm-two-authors</id></document-info>
</description>
<body><section><p>Arkady Brightwater and Boris Coldbrook wrote this together, and the clocks were striking.</p></section></body>
</FictionBook>
`, now.Year()))
}

// N3: each credit of a book gets its own excerpt — the other author among the
// other credits, never itself — and both are kept.
func TestAuthorLLMExcerptPerCreditOfOneBook(t *testing.T) {
	e := newLLMEnv(t)
	path := filepath.Join(e.archives, "two.zip")
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	entry, err := zw.Create("two.fb2")
	require.NoError(t, err)
	_, err = entry.Write(twoAuthorBook(time.Now()))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	reader, err := zip.OpenReader(path)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	scanner := services.NewBookScanService(e.archives, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)
	_, err = scanner.ProcessBook(reader.File[0], "two.zip")
	require.NoError(t, err)

	var credits []struct {
		Fingerprint []byte `pg:"source_fingerprint"`
		Extractor   string `pg:"extractor_version"`
		First       string `pg:"source_first_name"`
		Last        string `pg:"source_last_name"`
	}
	_, err = e.db.Query(&credits, `SELECT c.source_fingerprint, s.extractor_version, c.source_first_name, c.source_last_name
		FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.role = 'author' ORDER BY c.position`)
	require.NoError(t, err)
	require.Len(t, credits, 2)

	run := e.productionRun(e.version)
	var jobs []database.AuthorLLMNewJob
	for _, c := range credits {
		jobs = append(jobs, database.AuthorLLMNewJob{ConfigVersion: e.version, Slot: llmreq.SlotProductionA, RunID: &run,
			Purpose: models.AuthorLLMPurposeReview, SourceFingerprint: c.Fingerprint, ExtractorVersion: c.Extractor,
			WithContext: true, Input: llmreq.ItemInput{Fields: []llmres.FieldValue{
				{Field: llmres.FieldFirst, Text: c.First}, {Field: llmres.FieldLast, Text: c.Last}}, Script: "Latn"}})
	}
	_, err = database.EnqueueAuthorLLMJobs(context.Background(), e.db, jobs)
	require.NoError(t, err)
	e.fake.setRespond(checked(nil))
	e.drain(e.worker())

	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_job WHERE context_state = 'attached'`))
	assert.Equal(t, 2, e.count(`SELECT count(DISTINCT sha256) FROM author_llm_context`))
	for _, req := range e.fake.served() {
		if isFingerprint(&req) {
			continue
		}
		var ctxBody struct {
			OtherCredits []struct{ Name string } `json:"other_credits"`
		}
		require.NoError(t, json.Unmarshal(req.Items[0].BookContext, &ctxBody))
		self := req.Items[0].Tokens[0].Text
		require.Len(t, ctxBody.OtherCredits, 1)
		assert.NotContains(t, ctxBody.OtherCredits[0].Name, self, "an excerpt lists its own credit")
	}
}

// B1: a request that came out larger than the bound its claim reserved is
// not sent: sending it could cost more than the reserve.
func TestAuthorLLMRequestOverItsBoundIsNotSent(t *testing.T) {
	e := newLLMEnv(t)
	run := e.productionRun(e.version)
	e.reviewPair(&run, "over-bound", givenFamily, 1)
	w := e.worker()
	passChecks(t, w)
	before := len(e.fake.served())

	claim, _, err := w.Claim(context.Background(), llmreq.SlotProductionA)
	require.NoError(t, err)
	require.Equal(t, models.AuthorLLMCallJobs, claim.Kind)
	claim.PromptBoundBytes = 10
	require.NoError(t, w.Process(context.Background(), llmreq.SlotProductionA, claim))

	assert.Len(t, e.fake.served(), before, "the request was sent")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt
		WHERE call_id = ? AND outcome = 'invalid' AND validator_class = 'request_over_bound'`, claim.CallID))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_call WHERE id = ? AND settled_tokens = 0`, claim.CallID))
}
