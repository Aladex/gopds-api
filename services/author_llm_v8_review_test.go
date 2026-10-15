package services_test

import (
	"context"
	"encoding/json"
	"testing"

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

// Review B1 and B2 of 9cc500c. A V8 (particle) rejection — of the production
// pair, of a judge, or of a stored reply at apply time — settles the verdict
// but leaves the input in manual review: no LLM result is selected, and a new
// book of the same fingerprint stays in review too. A stored result whose
// reply fails V8 (runs 15 and 16 applied two) is not reused for new credits,
// and the credits it resolved go back to review at the next resolve run, as
// do run 17's inputs a V8 rejection closed. Admin decisions keep precedence.

// Run 17's shape: initials in the first and middle fields and a two-word last
// name led by a listed particle (55 of its 56 V8 rejections had a particle in
// the last field).
var (
	leGuin   = [3]string{"У.", "К.", "Ле Гуин"}
	vanGog   = [3]string{"В.", "В.", "Ван Гог"}
	vonBraun = [3]string{"В.", "М.", "Фон Браун"}
)

// particleCatalog is the eval catalog plus the three particle inputs.
func particleCatalog(t *testing.T, e *llmEnv) {
	t.Helper()
	ingestEvalCatalogue(t, e)
	ingestBooks(t, e, [][3]string{leGuin, vanGog, vonBraun}, 300)
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
}

// particleRole answers byFields, except that the listed models give a
// particle word of the last field the particle role — run 16's agreed reply,
// whose assembly dropped the particle from the family name.
func particleRole(roleModels ...string) func(req *fakeRequest) fakeReply {
	answer := byFields(nil)
	return func(req *fakeRequest) fakeReply {
		if isFingerprint(req) || !contains(roleModels, req.Model) {
			return answer(req)
		}
		results := make([]any, 0, len(req.Items))
		for _, it := range req.Items {
			roles := make([]any, 0, len(it.Tokens))
			for _, tok := range it.Tokens {
				role := map[string]string{"first": "given", "middle": "additional", "last": "family",
					"nickname": "nickname"}[tok.Field]
				if tok.Field == "last" && contains([]string{"Ле", "Ван", "Фон"}, tok.Text) {
					role = "particle"
				}
				roles = append(roles, map[string]any{"i": tok.I, "role": role})
			}
			results = append(results, map[string]any{"id": it.ID, "form": "person", "roles": roles, "case_fix": []any{}})
		}
		out, _ := json.Marshal(map[string]any{"results": results})
		return fakeReply{Content: string(out)}
	}
}

// The production_a and judge_b models of the test configuration.
const (
	productionAModel = "gpt-6-luna"
	judgeBModel      = "claude-opus-5"
)

// heldForReview asserts that the input's credits — credits of them — are all in
// review behind one open review item, and none selects an LLM result.
func (e *llmEnv) heldForReview(t *testing.T, last string, credits int) {
	t.Helper()
	fp := e.fingerprintOf(last)
	want := make([]string, credits)
	for i := range want {
		want[i] = "review:-"
	}
	assert.Equal(t, want, e.selectionsOf(t, last), "the input left manual review")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_review_item WHERE scope_fingerprint = ?
		AND reason = 'ambiguous_decision' AND status = 'open'`, fp), "the input's review item is not open")
	assert.Zero(t, e.count(`SELECT count(*) FROM book_contributor_credit_selection s
		JOIN contributor_normalization_result r ON r.id = s.result_id
		WHERE s.source_fingerprint = ? AND r.method = 'llm'`, fp), "an LLM result was selected")
}

// v8Rejections counts the input's attempts V8 rejected.
func (e *llmEnv) v8Rejections(fp []byte) int {
	return e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.source_fingerprint = ? AND a.validator_class = 'v8_violation'`, fp)
}

// skipV8 plays the binary before V8 until the test ends or it is turned off.
func skipV8(t *testing.T) {
	t.Helper()
	llmres.TestHookSkipV8.Store(true)
	t.Cleanup(func() { llmres.TestHookSkipV8.Store(false) })
}

// applyAsRun17 writes, over an input's settled verdict, what run 17's binary
// wrote for a V8 rejection: the review item closed by the service without a
// result, every credit unresolved llm_invalid.
func (e *llmEnv) applyAsRun17(t *testing.T, last string) {
	t.Helper()
	fp := e.fingerprintOf(last)
	_, err := e.db.Exec(`UPDATE book_contributor_credit_selection SET state = 'unresolved', basis = NULL,
			policy_version = NULL, unresolved_reason = 'llm_invalid', decided_at = now()
		WHERE source_fingerprint = ?`, fp)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE contributor_review_item SET status = 'closed', resolution = 'automatic', finished_at = now()
		WHERE source_fingerprint = ? AND status = 'open'`, fp)
	require.NoError(t, err)
}

func TestAuthorLLMResolveProductionV8KeepsTheReviewOpen(t *testing.T) {
	e := newLLMEnv(t)
	particleCatalog(t, e)
	ctx := context.Background()
	e.fake.setRespond(particleRole(productionAModel, productionBModel))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)

	fp := e.fingerprintOf("Ле Гуин")
	require.Positive(t, e.v8Rejections(fp), "the pair's replies were not V8 rejections")
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND verdict = 'invalid' AND outcome_reason = 'llm_invalid' AND stratum = 'initials.cyrl'`, pilot.ID, fp),
		"the verdict was not settled for the report")
	e.heldForReview(t, "Ле Гуин", 1)
	assert.Equal(t, "selected", e.selections()["Петров"].State, "the other inputs are applied as before")

	// A new book of the input stays in review: the rejection decided nothing.
	ingestBooks(t, e, [][3]string{leGuin}, 400)
	e.heldForReview(t, "Ле Гуин", 2)
}

func TestAuthorLLMResolveJudgeV8KeepsTheReviewOpen(t *testing.T) {
	e := newLLMEnv(t)
	particleCatalog(t, e)
	ctx := context.Background()
	// The pair keeps the particle in the family; judge_a gives it the
	// particle role.
	e.fake.setRespond(particleRole(judgeAModel))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)

	fp := e.fingerprintOf("Ле Гуин")
	require.Positive(t, e.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.source_fingerprint = ? AND j.slot = 'judge_a' AND a.validator_class = 'v8_violation'`, fp))
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND judgement = 'invalid' AND outcome_reason = 'llm_judge_invalid'`, pilot.ID, fp))
	e.heldForReview(t, "Ле Гуин", 1)

	ingestBooks(t, e, [][3]string{leGuin}, 400)
	e.heldForReview(t, "Ле Гуин", 2)
}

// A reply answered and confirmed before V8, applied after it: V8 rejects it
// at apply time.
func TestAuthorLLMResolveLegacyReplyRejectedAtApply(t *testing.T) {
	e := newLLMEnv(t)
	particleCatalog(t, e)
	ctx := context.Background()
	skipV8(t)
	e.fake.setRespond(particleRole(productionAModel, productionBModel, judgeAModel, judgeBModel))
	// The binary changes between the answers and the application.
	services.OnApplyForTest(t, func(input *llmreq.ItemInput) {
		for _, f := range input.Fields {
			if f.Field == llmres.FieldLast && f.Text == "Ле Гуин" {
				llmres.TestHookSkipV8.Store(false)
			}
		}
	})
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)

	fp := e.fingerprintOf("Ле Гуин")
	require.Zero(t, e.v8Rejections(fp), "the answers were not the old binary's")
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND verdict = 'agree' AND judgement = 'confirmed' AND outcome_reason = 'llm_invalid' AND result_id IS NULL`,
		pilot.ID, fp))
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_normalization_result WHERE source_fingerprint = ?
		AND method = 'llm'`, fp), "an LLM result was written")
	e.heldForReview(t, "Ле Гуин", 1)

	ingestBooks(t, e, [][3]string{leGuin}, 400)
	e.heldForReview(t, "Ле Гуин", 2)
}

// Runs 15 and 16 applied results whose reply dropped the particle. Under V8
// such a stored result is not reused for a new credit — the new book puts
// the input's credits back in review — and the next resolve run gives the
// credits of the other such inputs their review back, except an input an
// admin corrected.
func TestAuthorLLMResolveLegacyResultNotReused(t *testing.T) {
	e := newLLMEnv(t)
	particleCatalog(t, e)
	ctx := context.Background()
	skipV8(t)
	e.fake.setRespond(particleRole(productionAModel, productionBModel, judgeAModel, judgeBModel))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)

	// Run 16's wrong result: the particle is gone from the family name.
	sel := e.selections()
	for _, last := range []string{"Ле Гуин", "Ван Гог", "Фон Браун"} {
		require.Equal(t, "selected", sel[last].State, last)
		require.Equal(t, "llm", deref(sel[last].Method), last)
	}
	var family string
	_, err = e.db.QueryOne(pg.Scan(&family), `SELECT r.family_name FROM book_contributor_credit_selection s
		JOIN contributor_normalization_result r ON r.id = s.result_id WHERE s.source_fingerprint = ?`,
		e.fingerprintOf("Ле Гуин"))
	require.NoError(t, err)
	require.Equal(t, "Гуин", family)

	// The admin corrects one of them through the review's override.
	review, err := services.NewAuthorMetadataReviewService(e.db, services.DefaultAuthorMetadataReviewConfig())
	require.NoError(t, err)
	_, err = review.ApplyOverride(ctx, database.OverrideScope{Fingerprint: e.fingerprintOf("Ван Гог")}, e.adminUser(),
		&services.AdminCorrection{GivenName: "В.", AdditionalName: "В.", FamilyName: "Ван Гог",
			DisplayName: "В. В. Ван Гог", SortName: "Ван Гог, В. В.", SearchKey: "ван гог в в", Script: "Cyrl",
			Kind: models.NormalizationKind("person")})
	require.NoError(t, err)

	// The binary with V8: a new book of an input is not given the result.
	llmres.TestHookSkipV8.Store(false)
	ingestBooks(t, e, [][3]string{leGuin}, 400)
	e.heldForReview(t, "Ле Гуин", 2)
	require.Equal(t, []string{"selected:-"}, e.selectionsOf(t, "Фон Браун"), "no new book, nothing reopened yet")

	// The next run gives the other input its review back, asks nothing.
	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(full.ID)
	e.heldForReview(t, "Фон Браун", 1)
	e.heldForReview(t, "Ле Гуин", 2)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ?`, full.ID), "an input was asked again")
	assert.Equal(t, "fingerprint_override", deref(e.selections()["Ван Гог"].Basis), "the admin's correction was replaced")
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_review_item WHERE scope_fingerprint = ? AND status = 'open'`,
		e.fingerprintOf("Ван Гог")))
}

// Run 17 closed the review of its 56 V8 rejections: credits unresolved
// llm_invalid. A new book of such an input puts its credits back in review,
// and the next resolve run gives the other inputs' credits their review
// back without asking — but not an input an admin decided.
func TestAuthorLLMResolveReopensRun17V8Rejections(t *testing.T) {
	e := newLLMEnv(t)
	particleCatalog(t, e)
	ctx := context.Background()
	e.fake.setRespond(particleRole(productionAModel, productionBModel))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	for _, last := range []string{"Ле Гуин", "Ван Гог", "Фон Браун"} {
		require.Positive(t, e.v8Rejections(e.fingerprintOf(last)), last)
		e.applyAsRun17(t, last)
	}
	_, err = e.db.Exec(`UPDATE book_contributor_credit_selection SET unresolved_reason = 'review_left_unresolved',
		decided_by_user_id = ?1, decided_at = now() WHERE source_fingerprint = ?0`, e.fingerprintOf("Ван Гог"), e.adminUser())
	require.NoError(t, err)

	ingestBooks(t, e, [][3]string{leGuin}, 400)
	e.heldForReview(t, "Ле Гуин", 2)
	require.Equal(t, []string{"unresolved:llm_invalid"}, e.selectionsOf(t, "Фон Браун"), "no new book, nothing reopened yet")

	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(full.ID)
	e.heldForReview(t, "Фон Браун", 1)
	e.heldForReview(t, "Ле Гуин", 2)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ?`, full.ID), "an input was asked again")
	assert.Equal(t, []string{"unresolved:review_left_unresolved"}, e.selectionsOf(t, "Ван Гог"),
		"the admin's decision was replaced")
}

// A run paused while it enqueues gives no input its review back; resumed, it
// does.
func TestAuthorLLMResolvePausedEnqueueReopensNoHeldInput(t *testing.T) {
	e := newLLMEnv(t)
	particleCatalog(t, e)
	ctx := context.Background()
	e.fake.setRespond(particleRole(productionAModel, productionBModel))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	e.applyAsRun17(t, "Фон Браун")

	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	paused := false
	services.SetResolveEnqueuePlannedHookForTest(t, func() {
		if !paused {
			paused = true
			require.NoError(t, database.PauseAuthorLLMRun(ctx, e.db, full.ID, models.AuthorLLMRunPausedAdmin))
		}
	})
	_, err = e.resolveDriver().Step(ctx)
	require.NoError(t, err)
	require.True(t, paused)
	assert.Equal(t, []string{"unresolved:llm_invalid"}, e.selectionsOf(t, "Фон Браун"), "a paused run reopened an input")

	require.NoError(t, database.ResumeAuthorLLMRun(ctx, e.db, full.ID))
	e.resolveUntilDone(full.ID)
	e.heldForReview(t, "Фон Браун", 1)
}
