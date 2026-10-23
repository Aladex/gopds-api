package services_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/models"
	"gopds-api/services"
)

// completedEvalOfMode records a completed eval run of mode whose report
// decides strata as given, as completedFullEval does for a full eval.
func (e *llmEnv) completedEvalOfMode(t *testing.T, mode string, decisions []llmeval.StratumDecision) int64 {
	t.Helper()
	return e.completedEvalWithPromptTokens(t, mode, decisions, 0)
}

// completedEvalWithPromptTokens is completedEvalOfMode whose participants'
// learned fixed-request prompt tokens are moved by drift.
func (e *llmEnv) completedEvalWithPromptTokens(
	t *testing.T, mode string, decisions []llmeval.StratumDecision, drift int64,
) int64 {
	t.Helper()
	e.fake.setRespond(byFields(nil))
	expected, reference := allSlotsEvidence()
	for slot, ref := range reference {
		ref.PromptTokens += drift
		reference[slot] = ref
	}
	run := e.run(&database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval, Mode: mode,
		ExpectedModels: expected, Reference: reference})
	doc := evalReportDoc{Evaluation: llmeval.Report{Strata: map[string]llmeval.StratumReport{},
		Decision: llmeval.Decision{Registrable: true, Strata: decisions}}}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, database.StoreAuthorLLMEvalReport(context.Background(), e.db, run, e.version, raw))
	return run
}

// A stage eval freezes and measures its stage's strata alone — here the one
// of stage 2 the catalog has, single_field.cyrl, never the pilot's initials
// — and reports them in the format of a full eval. Every stage it passes
// through is stored scoped, a name the binary before stage evals never
// steps.
func TestAuthorLLMStageEvalMeasuresOnlyItsStrata(t *testing.T) {
	// A particle name: a stratum of stage 1, which a stage-2 eval must leave
	// alone.
	withEvalAuthors(t, append(append([][3]string{}, evalAuthors...), [3]string{"Шарль", "", "де Голль"}))
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	e.fake.setRespond(byFields(distinctLabs))
	pilot, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(pilot)
	frozenBefore := e.count(`SELECT count(*) FROM author_llm_eval_set WHERE set_version LIKE 'v1.s.%'`)

	stage, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeStage2, &settings, nil)
	require.NoError(t, err)
	stages := e.evalUntilDone(stage)
	require.NotEmpty(t, stages)
	for _, s := range stages {
		assert.True(t, strings.HasPrefix(s, "scoped_"), "stage %q is one the old driver steps", s)
	}

	assert.Equal(t, frozenBefore+1, e.count(`SELECT count(*) FROM author_llm_eval_set WHERE set_version LIKE 'v1.s.%'`),
		"the stage eval froze its one stratum and nothing else")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_eval_set WHERE set_version = 'v1.s.single_field.cyrl'`))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_eval_set WHERE set_version = 'v1.s.particle.cyrl'`),
		"the stage-2 eval froze a stage-1 stratum")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job j JOIN author_llm_eval_item i ON i.id = j.eval_item_id
		WHERE j.run_id = ? AND j.purpose IN ('eval_judge', 'eval_candidate', 'eval_repeat')
			AND i.stratum <> 'single_field.cyrl'`, stage), "the stage eval measured another stratum")
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose = 'eval_judge'`, stage))
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose = 'eval_candidate'`, stage))

	r := downloadReport(t, e, stage)
	assert.Equal(t, models.AuthorLLMEvalModeStage2, r.Mode)
	require.NotEmpty(t, r.Evaluation.Strata)
	for name := range r.Evaluation.Strata {
		assert.Equal(t, "single_field.cyrl", name)
	}
	// Only the gates decide: unlike a dev run's, a stage eval's report is
	// registrable whenever no run-wide reason stops it (this tiny catalog
	// has some).
	assert.Equal(t, len(r.Evaluation.Decision.Reasons) == 0, r.Evaluation.Decision.Registrable,
		"reasons %v", r.Evaluation.Decision.Reasons)
	assert.NotContains(t, strings.Join(r.Evaluation.Decision.Reasons, " "), "dev")
}

// A resolve run rests on a stage eval as on a full eval: it registers the
// pairs the report passed, citing that report, with the plan a full eval's
// report of the same decisions gives. Of a full and a stage eval the later
// one is the evidence.
func TestAuthorLLMResolveRestsOnAStageEvalAsOnAFullEval(t *testing.T) {
	decisions := []llmeval.StratumDecision{
		passedStratum("single_field.cyrl", "A", 10, "confirm", "restructure"),
		{Stratum: "single_field.latn"}, // failed
	}
	plans := map[string]json.RawMessage{}
	for _, mode := range []string{models.AuthorLLMEvalModeFull, models.AuthorLLMEvalModeStage2} {
		e := newLLMEnv(t)
		ingestEvalCatalogue(t, e)
		ctx := context.Background()
		evalRun := e.completedEvalOfMode(t, mode, decisions)
		run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
		require.NoError(t, err, mode)

		var rows []struct {
			Class    string `pg:"decision_class"`
			Script   string `pg:"script"`
			Config   string `pg:"config_version"`
			Source   string `pg:"source"`
			ReportID int64  `pg:"evidence_report_id"`
		}
		_, err = e.db.Query(&rows, `SELECT decision_class, script, config_version, source, evidence_report_id
			FROM author_acceptance_class WHERE source = 'llm_eval'`)
		require.NoError(t, err)
		require.Len(t, rows, 1, mode)
		assert.Equal(t, "llm.single_field", rows[0].Class, mode)
		assert.Equal(t, "Cyrl", rows[0].Script, mode)
		assert.Equal(t, e.version, rows[0].Config, mode)
		assert.Equal(t, int64(e.count(`SELECT id FROM author_llm_eval_report WHERE run_id = ?`, evalRun)), rows[0].ReportID,
			mode)

		got, err := database.GetAuthorLLMRun(ctx, e.db, run.ID)
		require.NoError(t, err)
		var settings struct {
			Strata         json.RawMessage `json:"strata"`
			NotRegistrable json.RawMessage `json:"not_registrable"`
		}
		require.NoError(t, json.Unmarshal(got.Settings, &settings))
		plans[mode] = append(append(json.RawMessage{}, settings.Strata...), settings.NotRegistrable...)
	}
	assert.JSONEq(t, string(plans[models.AuthorLLMEvalModeFull][:len(plans[models.AuthorLLMEvalModeFull])-2]),
		string(plans[models.AuthorLLMEvalModeStage2][:len(plans[models.AuthorLLMEvalModeStage2])-2]),
		"the plan from a stage eval is the plan from a full eval")

	// The later eval is the evidence: a stage eval after a full one.
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.completedEvalOfMode(t, models.AuthorLLMEvalModeFull, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm")})
	stage := e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage2, decisions)
	ev, err := database.LatestAuthorLLMResolveEval(context.Background(), e.db, e.version)
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.Equal(t, stage, ev.RunID)
}

// The card offers the three stage evals after the pilot, each with its
// strata and the estimate of its own run, before it starts; a completed
// stage eval whose report registers a stratum opens the resolve pilot.
func TestAuthorLLMCardOffersTheStageEvals(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	admin := e.llmAdmin()
	st, err := admin.Status(ctx)
	require.NoError(t, err)
	steps := stepsByKey(st)
	for _, key := range []string{"eval_stage0", "eval_stage1", "eval_stage2", "eval_stage3"} {
		require.Contains(t, steps, key)
		assert.False(t, steps[key].Enabled, "%s before the pilot", key)
		assert.Equal(t, "prerequisite", steps[key].Blocked, key)
		assert.Positive(t, steps[key].EstimateTokens, key)
	}
	assert.Equal(t, []string{"initials.cyrl", "initials.latn"}, steps["eval_stage0"].Strata)
	assert.Equal(t, []string{"nickname_with_name.cyrl", "particle.cyrl", "particle.latn"}, steps["eval_stage1"].Strata)
	assert.Equal(t, services.AuthorLLMEvalStageStrata(models.AuthorLLMEvalModeStage2), steps["eval_stage2"].Strata)
	assert.Equal(t, []string{"affix.cyrl", "unusual_characters.cyrl", "unusual_characters.latn"},
		steps["eval_stage3"].Strata)
	assert.NotContains(t, steps["eval_stage2"].Strata, "mixed_script.mixed")
	_, err = admin.Start(ctx, "eval", models.AuthorLLMEvalModeStage1, e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite, "a stage eval before the pilot")

	e.fake.setRespond(byFields(distinctLabs))
	settings := services.DefaultAuthorLLMEvalSettings()
	pilot, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(pilot)
	st, err = admin.Status(ctx)
	require.NoError(t, err)
	steps = stepsByKey(st)
	for _, key := range []string{"eval_full", "eval_stage0", "eval_stage1", "eval_stage2", "eval_stage3"} {
		assert.True(t, steps[key].Enabled, "%s after the pilot", key)
	}
	assert.False(t, steps["resolve_pilot"].Enabled, "no full or stage eval yet")

	e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage1, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm")})
	st, err = admin.Status(ctx)
	require.NoError(t, err)
	steps = stepsByKey(st)
	assert.True(t, steps["eval_stage1"].Done)
	assert.True(t, steps["resolve_pilot"].Enabled, "a registrable stage eval opens the resolve pilot")
}

// The estimate before the start is the run's own: once a stage eval froze
// its sets, the card's figure equals the estimate the run recorded; before
// any freeze it counts each missing stratum at the most its freeze can take.
func TestAuthorLLMStageEstimateIsTheRunsOwn(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	e.fake.setRespond(byFields(distinctLabs))
	pilot, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(pilot)

	before, err := services.EstimateAuthorLLMEvalStage(ctx, e.db, models.AuthorLLMEvalModeStage2, evalSizes)
	require.NoError(t, err)
	assert.True(t, before.Upper, "no stage-2 stratum is frozen yet")
	assert.Equal(t, 6*(evalSizes.Holdout+evalSizes.Dev), before.Items)

	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeStage2, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(run)
	after, err := services.EstimateAuthorLLMEvalStage(ctx, e.db, models.AuthorLLMEvalModeStage2, evalSizes)
	require.NoError(t, err)
	got, err := database.GetAuthorLLMRun(ctx, e.db, run)
	require.NoError(t, err)
	// Five of its strata stay unfrozen (the catalog has none of them), so
	// the card still bounds them; over what the run froze, the figures agree.
	assert.True(t, after.Upper)
	assert.Equal(t, got.EstimateTokens, after.FrozenTokens, "the card's estimate is not the run's")
	assert.Less(t, after.Tokens, before.Tokens)
}

// reviewJobsOf counts a resolve run's review jobs of an input.
func (e *llmEnv) reviewJobsOf(run int64, last string) int {
	return e.count(`SELECT count(*) FROM author_llm_job j
		WHERE j.run_id = ? AND j.purpose = 'review' AND j.source_fingerprint IN (
			SELECT source_fingerprint FROM book_contributor_credit WHERE source_last_name = ?)`, run, last)
}

// A stage eval after a full one decides its own strata only: the initials the
// full eval passed stay planned, from the full eval's report, and an initials
// input that arrives after the stage eval is still resolved.
func TestAuthorLLMResolveKeepsTheFullEvalsStrataAfterAStage(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	full := e.completedEvalOfMode(t, models.AuthorLLMEvalModeFull, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")})
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)

	stage := e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage1, []llmeval.StratumDecision{
		passedStratum("particle.cyrl", "A", 10, "confirm")})
	// A late initials input, after the stage eval.
	ingestBooks(t, e, [][3]string{{"Р.", "С.", "Орлов"}}, 200)

	run, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	got, err := database.GetAuthorLLMRun(ctx, e.db, run.ID)
	require.NoError(t, err)
	var settings database.AuthorLLMResolveSettings
	require.NoError(t, json.Unmarshal(got.Settings, &settings))
	require.Contains(t, settings.Strata, "initials.cyrl", "the stage eval hid the full eval's stratum")
	require.Contains(t, settings.Strata, "particle.cyrl")
	fullReport := int64(e.count(`SELECT id FROM author_llm_eval_report WHERE run_id = ?`, full))
	stageReport := int64(e.count(`SELECT id FROM author_llm_eval_report WHERE run_id = ?`, stage))
	assert.Equal(t, fullReport, settings.Strata["initials.cyrl"].ReportID)
	assert.Equal(t, stageReport, settings.Strata["particle.cyrl"].ReportID)

	e.resolveUntilDone(run.ID)
	assert.Positive(t, e.reviewJobsOf(run.ID, "Орлов"), "the late initials input was not sent")
	var state string
	_, err = e.db.QueryOne(pg.Scan(&state), `SELECT s.state FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id WHERE c.source_last_name = 'Орлов'`)
	require.NoError(t, err)
	assert.Equal(t, "selected", state)
}

// Two stages keep each other's strata, each pair citing its own report; a
// newer stage eval with nothing passed, or one whose run-wide gate failed,
// blocks nothing.
func TestAuthorLLMResolveKeepsEveryStagesStrata(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	stage1 := e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage1, []llmeval.StratumDecision{
		passedStratum("particle.cyrl", "A", 10, "confirm")})
	stage2 := e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage2, []llmeval.StratumDecision{
		passedStratum("single_field.cyrl", "A", 1, "restructure")})
	// Newest: a stage eval that passed nothing.
	e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage3, []llmeval.StratumDecision{{Stratum: "affix.cyrl"}})

	st, err := e.llmAdmin().Status(ctx)
	require.NoError(t, err)
	assert.True(t, stepsByKey(st)["resolve_pilot"].Enabled, "an empty newer stage closed the resolve pilot")
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)

	var rows []struct {
		Class    string `pg:"decision_class"`
		ReportID int64  `pg:"evidence_report_id"`
		Ref      string `pg:"evidence_ref"`
	}
	_, err = e.db.Query(&rows, `SELECT decision_class, evidence_report_id, evidence_ref FROM author_acceptance_class
		WHERE source = 'llm_eval' ORDER BY decision_class`)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	report := func(run int64) int64 {
		return int64(e.count(`SELECT id FROM author_llm_eval_report WHERE run_id = ?`, run))
	}
	assert.Equal(t, "llm.particle", rows[0].Class)
	assert.Equal(t, report(stage1), rows[0].ReportID)
	assert.Equal(t, "author_llm_eval_report/"+itoa(int(report(stage1)))+".json", rows[0].Ref)
	assert.Equal(t, "llm.single_field", rows[1].Class)
	assert.Equal(t, report(stage2), rows[1].ReportID)
	assert.Positive(t, run.ID)

	// A newer eval whose run-wide gate failed is no evidence and hides
	// nothing.
	e2 := newLLMEnv(t)
	ingestEvalCatalogue(t, e2)
	e2.completedEvalOfMode(t, models.AuthorLLMEvalModeFull, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm")})
	failed := e2.run(&database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval, Mode: models.AuthorLLMEvalModeStage1})
	raw, err := json.Marshal(evalReportDoc{Evaluation: llmeval.Report{Decision: llmeval.Decision{Registrable: false,
		Reasons: []string{"anchor evidence"}, Strata: []llmeval.StratumDecision{{Stratum: "initials.cyrl"}}}}})
	require.NoError(t, err)
	require.NoError(t, database.StoreAuthorLLMEvalReport(ctx, e2.db, failed, e2.version, raw))
	run2, err := e2.llmAdmin().Start(ctx, "resolve", "pilot", e2.adminUser())
	require.NoError(t, err)
	got, err := database.GetAuthorLLMRun(ctx, e2.db, run2.ID)
	require.NoError(t, err)
	assert.Contains(t, string(got.Settings), `"initials.cyrl"`)
}

// On the stage route — eval pilot, a stage eval, the resolve pilot and full
// run, no full eval — the swap eval opens on the card and the API takes it;
// before the full resolve, neither does.
func TestAuthorLLMSwapEvalFollowsTheFullResolveOnTheStageRoute(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	admin := e.llmAdmin()
	e.fake.setRespond(byFields(distinctLabs))
	settings := services.DefaultAuthorLLMEvalSettings()
	pilot, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(pilot)
	e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage2, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm")})

	resolvePilot, err := admin.Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(resolvePilot.ID)
	st, err := admin.Status(ctx)
	require.NoError(t, err)
	assert.False(t, stepsByKey(st)["swap_eval"].Enabled, "the swap eval before the full resolve")
	_, err = admin.Start(ctx, "eval", models.AuthorLLMEvalModeSwap, e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite)

	resolveFull, err := admin.Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(resolveFull.ID)
	st, err = admin.Status(ctx)
	require.NoError(t, err)
	assert.True(t, stepsByKey(st)["swap_eval"].Enabled, "the card")
	swap, err := admin.Start(ctx, "eval", models.AuthorLLMEvalModeSwap, e.adminUser())
	require.NoError(t, err, "the API refuses what the card offers")
	assert.Equal(t, models.AuthorLLMEvalModeSwap, swap.Mode)
}

// A stage eval that completes after a resolve start planned and before its
// lock changes the evidence set: the start refuses and writes nothing, and
// the next start plans from both reports.
func TestAuthorLLMResolveStartRefusesANewStageEvalBeforeItsLock(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedEvalOfMode(t, models.AuthorLLMEvalModeFull, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm")})
	completed := false
	services.SetResolveStartPlannedHookForTest(t, func() {
		if !completed {
			completed = true
			e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage1, []llmeval.StratumDecision{
				passedStratum("particle.cyrl", "A", 10, "confirm")})
		}
	})
	_, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.ErrorIs(t, err, database.ErrAuthorLLMEvidenceChanged)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_run WHERE kind = 'resolve'`))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'`))

	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	plan := e.resolvePlanOf(t, run.ID)
	assert.Contains(t, plan.Strata, "initials.cyrl")
	assert.Contains(t, plan.Strata, "particle.cyrl")
	assert.Len(t, plan.Sources, 2)
}

// The same model ids with a drifted fingerprint reference are other
// participants: a stage eval measured after the fixed request's prompt
// tokens moved leaves the older full eval's initials stale — not planned,
// not registered — and plans only its own stratum.
func TestAuthorLLMResolveLeavesADriftedOlderReportStale(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedEvalOfMode(t, models.AuthorLLMEvalModeFull, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm")})
	e.completedEvalWithPromptTokens(t, models.AuthorLLMEvalModeStage1, []llmeval.StratumDecision{
		passedStratum("particle.cyrl", "A", 10, "confirm")}, 74)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	plan := e.resolvePlanOf(t, run.ID)
	assert.Contains(t, plan.Strata, "particle.cyrl")
	assert.NotContains(t, plan.Strata, "initials.cyrl", "a stratum measured on another fingerprint was planned")
	assert.Equal(t, []string{"initials.cyrl"}, plan.Stale)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_acceptance_class WHERE decision_class = 'llm.initials'`))
}

// Stage 0 measures the initials alone under the configuration: a new
// configuration registers its initials pairs again without a full eval.
func TestAuthorLLMStage0MeasuresTheInitials(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	e.fake.setRespond(byFields(distinctLabs))
	pilot, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(pilot)

	run, err := e.llmAdmin().Start(ctx, "eval", models.AuthorLLMEvalModeStage0, e.adminUser())
	require.NoError(t, err)
	e.evalUntilDone(run.ID)
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose = 'eval_judge'`, run.ID))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job j JOIN author_llm_eval_item i ON i.id = j.eval_item_id
		WHERE j.run_id = ? AND j.purpose IN ('eval_judge', 'eval_candidate', 'eval_repeat')
			AND i.stratum NOT LIKE 'initials.%'`, run.ID), "stage 0 measured another stratum")
	r := downloadReport(t, e, run.ID)
	assert.Equal(t, models.AuthorLLMEvalModeStage0, r.Mode)
	for name := range r.Evaluation.Strata {
		assert.Equal(t, "initials.cyrl", name)
	}
}
