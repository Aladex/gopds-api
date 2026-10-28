package services_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/llm"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The merge configuration in the LLM layer (E4c, plan §4.1): a second
// configuration on the same endpoint and participants, its own version and
// worker, answering the merge question; the name normalization's
// configuration runs as without it, and the endpoint's controls are shared.

// mergeConfig is the merge configuration of an env's endpoint.
type mergeConfig struct {
	identity llmreq.Identity
	version  string
}

func (e *llmEnv) mergeConfig() *mergeConfig {
	e.t.Helper()
	identity, err := llmreq.NewMergeIdentity(e.srv.URL+"/v1", testParticipants, testOutput)
	require.NoError(e.t, err)
	version, err := database.EnsureAuthorLLMConfig(context.Background(), e.db, &identity, 4)
	require.NoError(e.t, err)
	require.NotEqual(e.t, e.version, version)
	return &mergeConfig{identity: identity, version: version}
}

func (e *llmEnv) mergeWorker(m *mergeConfig) *services.AuthorLLMWorker {
	e.t.Helper()
	client := llm.NewClient(config.LLMConfig{BaseURL: e.srv.URL + "/v1", Timeout: 30 * time.Second})
	w, err := services.NewAuthorLLMWorker(e.db, client, &services.AuthorLLMWorkerConfig{
		Settings: e.settings, Identity: m.identity, ConfigVersion: m.version,
		BaseBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond,
	})
	require.NoError(e.t, err)
	return w
}

// mergeQuestion is a question with its two cards.
func mergeQuestion(nameA, nameB string) *authorentity.MergeInput {
	book := authorentity.MergeCardBookOf("Оно", &authorentity.CardBookFields{Year: "1986", Lang: "ru",
		Genres: []string{"sf_horror"}}, nil)
	a := authorentity.MergeCardOf([]string{nameA}, nil, 2, []authorentity.MergeCardBook{book})
	b := authorentity.MergeCardOf([]string{nameB}, nil, 1, []authorentity.MergeCardBook{book})
	return &authorentity.MergeInput{CardVersion: authorentity.MergeCardVersion, A: a, B: b}
}

// mergeRun starts an eval run of the merge configuration: a merge
// question comes to the merge worker as an eval item until E4d's merge runs.
func (e *llmEnv) mergeRun(m *mergeConfig) int64 {
	e.t.Helper()
	run, err := database.CreateAuthorLLMRun(context.Background(), e.db, &database.AuthorLLMRunSpec{
		Kind: models.AuthorLLMRunEval, Mode: models.AuthorLLMEvalModeMerge0, ConfigVersion: m.version})
	require.NoError(e.t, err)
	require.NoError(e.t, database.StartAuthorLLMRun(context.Background(), e.db, run))
	e.exec(`INSERT INTO author_llm_eval_set (set_version, sha256, items, created_by_run_id)
		VALUES ('merge-s1', decode(repeat('ab', 32), 'hex'), 3, ?) ON CONFLICT DO NOTHING`, run)
	return run
}

// mergePair freezes one merge question as an eval item (its digest the
// key, its cards the input) and enqueues the production pair's questions.
func (e *llmEnv) mergePair(m *mergeConfig, runID *int64, seed string, q *authorentity.MergeInput) {
	e.t.Helper()
	raw, err := json.Marshal(q)
	require.NoError(e.t, err)
	var item int64
	_, err = e.db.QueryOne(pg.Scan(&item), `INSERT INTO author_llm_eval_item (set_version, item_set, stratum,
			source_fingerprint, extractor_version, input, weight)
		VALUES ('merge-s1', 'holdout', 'swap.cyrl', ?, ?, ?::jsonb, 1) RETURNING id`,
		testFingerprint(seed), authorentity.MergeCardVersion, string(raw))
	require.NoError(e.t, err)
	var jobs []database.AuthorLLMNewJob
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
		jobs = append(jobs, database.AuthorLLMNewJob{ConfigVersion: m.version, Slot: slot, RunID: runID,
			Purpose: models.AuthorLLMPurposeEvalCandidate, EvalItemID: &item, MergeInput: q})
	}
	n, err := database.EnqueueAuthorLLMJobs(context.Background(), e.db, jobs)
	require.NoError(e.t, err)
	require.Equal(e.t, 2, n)
}

// mergeAnswers answers every merge item of a request: answer(card names of
// side a) decides it.
func mergeAnswers(answer func(nameA string) string) func(req *fakeRequest) fakeReply {
	return func(req *fakeRequest) fakeReply {
		var msg struct {
			Cards map[string]authorentity.MergeCard `json:"cards"`
			Items []struct {
				ID string `json:"id"`
				A  string `json:"a"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(req.User), &msg) != nil || msg.Cards == nil {
			return fakeReply{}
		}
		results := make([]any, 0, len(msg.Items))
		for _, it := range msg.Items {
			results = append(results, map[string]any{"id": it.ID, "answer": answer(msg.Cards[it.A].Names[0])})
		}
		out, _ := json.Marshal(map[string]any{"results": results})
		return fakeReply{Content: string(out)}
	}
}

// A merge question goes request → reply → validate → settle through the
// merge worker: the merge prompt and schema, the two cards, the closed
// answer stored canonically — same and different with the merge tier,
// cannot_tell as the refusal, anything else invalid. No verdict is written
// (E4d); the name normalization's worker never claims a merge job.
func TestAMergeQuestionIsAskedAndSettled(t *testing.T) {
	e := newLLMEnv(t)
	m := e.mergeConfig()
	run := e.mergeRun(m)
	e.mergePair(m, &run, "same", mergeQuestion("Стивен Кинг", "Кинг Стивен"))
	e.mergePair(m, &run, "unsure", mergeQuestion("С. Кинг", "Стивен Кинг"))
	e.mergePair(m, &run, "bad", mergeQuestion("Брэм Стокер", "Стивен Кинг"))
	e.fake.setRespond(mergeAnswers(func(name string) string {
		switch name {
		case "Стивен Кинг":
			return llmreq.MergeSame
		case "С. Кинг":
			return llmreq.MergeCannotTell
		}
		return "maybe"
	}))

	e.drain(e.worker())
	assert.Zero(t, len(e.fake.served()), "the name normalization's worker asked a merge question")
	e.drain(e.mergeWorker(m))

	served := e.fake.served()
	require.NotEmpty(t, served)
	for _, req := range served {
		assert.Contains(t, req.Raw, "You decide whether two author records")
		assert.Contains(t, req.User, `"cards":{`)
	}
	byAnswer := func(query string) int {
		return e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
			WHERE j.config_version = ? AND `+query, m.version)
	}
	assert.Equal(t, 2, byAnswer(`a.outcome = 'answered' AND a.reply = '{"answer":"same"}' AND a.tier = 'merge'
		AND NOT a.refused`))
	assert.Equal(t, 2, byAnswer(`a.outcome = 'answered' AND a.reply = '{"answer":"cannot_tell"}' AND a.tier IS NULL
		AND a.refused`))
	assert.Positive(t, byAnswer(`a.outcome = 'invalid' AND a.validator_class = 'item_invalid'`))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict`), "an eval answer wrote a verdict")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.config_version = ?`, e.version), "a merge job was answered under the normalization's version")
}

// One bucket batch whose books share the full name's card sends that card
// once.
func TestAMergeBatchSendsASharedCardOnce(t *testing.T) {
	full := mergeQuestion("x", "Алексей Викторович Иванов").B
	items := make([]llmreq.MergeItem, 0, 3)
	for i, title := range []string{"Психология", "Сердце пармы", "Золото бунта"} {
		book := authorentity.MergeCardOf([]string{"Алексей Иванов"}, nil, 1,
			[]authorentity.MergeCardBook{authorentity.MergeCardBookOf(title, &authorentity.CardBookFields{}, nil)})
		items = append(items, llmreq.MergeItem{Key: string(rune('1' + i)), Input: authorentity.MergeInput{
			CardVersion: authorentity.MergeCardVersion, A: book, B: full}})
	}
	user, err := llmreq.BuildMergeUserMessage(items)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(user), "Алексей Викторович Иванов"), "the shared card went more than once")
	var msg struct {
		Cards map[string]json.RawMessage  `json:"cards"`
		Items []struct{ ID, A, B string } `json:"items"`
	}
	require.NoError(t, json.Unmarshal(user, &msg))
	assert.Len(t, msg.Cards, 4)
	assert.Equal(t, msg.Items[0].B, msg.Items[2].B)
}

// K and the merge configuration on one endpoint share its controls (plan
// §4.1): one active run across both; the open-call ceiling counts both
// configurations' calls; the quota pause stops both; the one-off budget
// sums both. Claims stay per configuration: neither worker claims the
// other's job.
func TestTwoConfigsShareGlobalControls(t *testing.T) {
	e := newLLMEnv(t)
	m := e.mergeConfig()
	ctx := context.Background()
	kRun := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&kRun, "k", givenFamily, 1)

	_, err := database.CreateAuthorLLMRun(ctx, e.db, &database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval,
		Mode: models.AuthorLLMEvalModeMerge0, ConfigVersion: m.version})
	assert.Error(t, err, "a second active run, of the merge configuration")

	mergeOpts := func(slot llmreq.Slot) *database.AuthorLLMClaimOptions {
		opts := e.claimOptions(slot, database.NewLeaseOwner(), nil)
		opts.ConfigVersion, opts.PromptBound = m.version, llmreq.MergeRequestBoundBytes
		return opts
	}
	claim, refusal, err := database.ClaimAuthorLLMCall(ctx, e.db, mergeOpts(llmreq.SlotProductionA))
	require.NoError(t, err)
	assert.Nil(t, claim, "the merge configuration claimed a K job")
	assert.Equal(t, database.AuthorLLMClaimIdle, refusal)
	kClaim, _, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), nil))
	require.NoError(t, err)
	require.NotNil(t, kClaim, "K claims its own job")

	// The K call stays open while its run ends and a merge run starts.
	e.exec(`UPDATE author_llm_run SET status = 'completed', finished_at = now() WHERE id = ?`, kRun)
	mRun := e.mergeRun(m)
	e.mergePair(m, &mRun, "m", mergeQuestion("Стивен Кинг", "Кинг Стивен"))

	one := mergeOpts(llmreq.SlotProductionA)
	one.ConcurrencyTotal = 1
	claim, refusal, err = database.ClaimAuthorLLMCall(ctx, e.db, one)
	require.NoError(t, err)
	assert.Nil(t, claim)
	assert.Equal(t, database.AuthorLLMClaimConcurrency, refusal, "K's open call does not count at the endpoint")

	e.exec(`UPDATE author_llm_endpoint_state SET paused_reason = 'quota_exhausted', paused_at = now()
		WHERE base_url = ?`, m.identity.BaseURL)
	claim, refusal, err = database.ClaimAuthorLLMCall(ctx, e.db, mergeOpts(llmreq.SlotProductionA))
	require.NoError(t, err)
	assert.Nil(t, claim)
	assert.Equal(t, database.AuthorLLMClaimEndpointPaused, refusal, "the quota pause does not stop the merge configuration")
	e.exec(`UPDATE author_llm_endpoint_state SET paused_reason = NULL, paused_at = NULL`)

	// K's spend counts against the merge configuration's one-off budget.
	spent := int64(e.count(`SELECT coalesce(sum(tokens), 0)::int FROM author_llm_token_tally`))
	require.Positive(t, spent)
	reserve := int64(llmreq.MergeRequestBoundBytes(1, 0)) + llmreq.PromptTemplateTokens + int64(testOutput.Ceiling(1))
	budget := mergeOpts(llmreq.SlotProductionA)
	budget.OneOffTokens = spent + reserve - 1
	claim, refusal, err = database.ClaimAuthorLLMCall(ctx, e.db, budget)
	require.NoError(t, err)
	assert.Nil(t, claim)
	assert.Equal(t, database.AuthorLLMClaimBudgetOneOff, refusal, "K's spend is not in the one-off budget")

	require.NoError(t, database.ResumeAuthorLLMRun(ctx, e.db, mRun))
	budget.OneOffTokens = spent + reserve
	claim, _, err = database.ClaimAuthorLLMCall(ctx, e.db, budget)
	require.NoError(t, err)
	require.NotNil(t, claim, "the merge configuration claims its own job within the budget")
	assert.Equal(t, llmreq.MergeRequestBoundBytes(1, 0), claim.PromptBoundBytes, "the merge bound was not reserved")
}

func (e *llmEnv) exec(query string, params ...any) {
	e.t.Helper()
	_, err := e.db.Exec(query, params...)
	require.NoError(e.t, err, query)
}
