package services_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The LLM block of the admin card: the chain, the transitions, the run's
// progress, tokens and cost, the pauses and the report download.

func (e *llmEnv) llmAdmin() *services.AuthorLLMAdmin {
	return services.NewAuthorLLMAdmin(e.db, e.version, "", &e.settings)
}

// adminUser is the administrator who starts the runs.
func (e *llmEnv) adminUser() int64 {
	e.t.Helper()
	var id int64
	_, err := e.db.QueryOne(pg.Scan(&id), `INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', true, 'llm-admin', 'llm-admin@fixture.local', now()) RETURNING id`)
	require.NoError(e.t, err)
	return id
}

func stepsByKey(st *services.AuthorLLMStatus) map[string]services.AuthorLLMStep {
	out := map[string]services.AuthorLLMStep{}
	for _, s := range st.Steps {
		out[s.Key] = s
	}
	return out
}

func TestAuthorLLMAdminSwitchedOff(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	admin := services.NewAuthorLLMAdmin(e.db, "", config.AuthorLLMDisabledNotConfigured, &e.settings)

	st, err := admin.Status(ctx)
	require.NoError(t, err)
	assert.False(t, st.Enabled)
	assert.Equal(t, config.AuthorLLMDisabledNotConfigured, st.Reason)
	require.Len(t, st.Steps, 5)
	for _, s := range st.Steps {
		assert.False(t, s.Enabled, s.Key)
	}
	_, err = admin.Start(ctx, "eval", "pilot", 1)
	assert.ErrorIs(t, err, services.ErrAuthorLLMNotConfigured)
	assert.ErrorIs(t, admin.ResumeEndpoint(ctx), services.ErrAuthorLLMNotConfigured)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_run`))
}

func TestAuthorLLMAdminChainAndTransitions(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	admin := e.llmAdmin()
	actor := e.adminUser()

	// A fresh installation: only the first step is open.
	st, err := admin.Status(ctx)
	require.NoError(t, err)
	assert.True(t, st.Enabled)
	assert.Nil(t, st.Run)
	assert.Equal(t, []string{"eval_pilot", "eval_full", "resolve_pilot", "resolve_full", "swap"},
		[]string{st.Steps[0].Key, st.Steps[1].Key, st.Steps[2].Key, st.Steps[3].Key, st.Steps[4].Key})
	steps := stepsByKey(st)
	assert.True(t, steps["eval_pilot"].Enabled)
	assert.False(t, steps["eval_full"].Enabled)
	assert.Equal(t, services.AuthorLLMStepPrerequisite, steps["eval_full"].Blocked)
	for _, k := range []string{"resolve_pilot", "resolve_full", "swap"} {
		assert.False(t, steps[k].Available, k)
		assert.False(t, steps[k].Enabled, k)
		assert.Equal(t, services.AuthorLLMStepNotAvailable, steps[k].Blocked, k)
	}
	assert.Equal(t, e.settings.Budget.OneOffTokens, st.BudgetTokens)
	require.Len(t, st.Participants, 4)
	assert.Equal(t, e.settings.Models.ProductionA, st.Participants[0].Model)

	// Steps out of order, unknown runs and runs not built yet are refused.
	_, err = admin.Start(ctx, "eval", "full", actor)
	assert.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite)
	_, err = admin.Start(ctx, "resolve", "pilot", actor)
	assert.ErrorIs(t, err, services.ErrAuthorLLMRunKindUnavailable)
	_, err = admin.Start(ctx, "swap", "full", actor)
	assert.ErrorIs(t, err, services.ErrAuthorLLMRunKindUnavailable)
	_, err = admin.Start(ctx, "eval", "everything", actor)
	assert.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_run`))

	// The start is instant: the stages run in the background.
	began := time.Now()
	run, err := admin.Start(ctx, "eval", "pilot", actor)
	require.NoError(t, err)
	assert.Less(t, time.Since(began), 2*time.Second)
	assert.Equal(t, "running", run.Status)
	assert.Equal(t, "freeze", run.Stage)
	assert.True(t, run.CurrentConfig)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_run WHERE id = ? AND created_by_user_id = ?`, run.ID, actor),
		"the run is not recorded as the administrator's")
	_, err = admin.Start(ctx, "eval", "pilot", actor)
	assert.ErrorIs(t, err, database.ErrAuthorLLMActiveRun)
	st, err = admin.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, run.ID, st.Run.ID)
	assert.Equal(t, services.AuthorLLMStepActiveRun, stepsByKey(st)["eval_pilot"].Blocked)

	// Pause and resume, each only from the status that allows it.
	paused, err := admin.Pause(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, "paused", paused.Status)
	assert.Equal(t, "admin", paused.PausedReason)
	_, err = admin.Pause(ctx, run.ID)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState)
	resumed, err := admin.Resume(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", resumed.Status)
	_, err = admin.Resume(ctx, run.ID)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState)
	_, err = admin.Pause(ctx, run.ID+1000)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunNotFound)
	_, _, err = admin.EvalReport(ctx, run.ID)
	assert.ErrorIs(t, err, services.ErrAuthorLLMReportNotFound)

	// A stage's progress: its jobs that ended out of all of them.
	e.stepTo(run.ID, "fingerprint")
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	_, err = driver.Step(ctx)
	require.NoError(t, err)
	st, err = admin.Status(ctx)
	require.NoError(t, err)
	require.NotNil(t, st.Run.Progress)
	assert.Equal(t, services.AuthorLLMProgress{Done: 0, Total: 12}, *st.Run.Progress)
	assert.Positive(t, st.Run.EstimateTokens)
	assert.Equal(t, int64(float64(st.Run.EstimateTokens)*e.settings.Budget.RunOverrun), st.Run.LimitTokens)

	// Completed: the report line, the download, and the next step open.
	e.evalUntilDone(run.ID)
	st, err = admin.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "completed", st.Run.Status)
	require.NotNil(t, st.Run.Report)
	report, sha, err := admin.EvalReport(ctx, run.ID)
	require.NoError(t, err)
	sum := sha256.Sum256(report)
	assert.Equal(t, hex.EncodeToString(sum[:]), sha)
	assert.Equal(t, sha, st.Run.Report.SHA256)
	assert.Equal(t, 1, st.Run.Report.StrataTotal, "a pilot measures one stratum")
	assert.False(t, st.Run.Report.Registrable, "fixture-sized evidence")
	assert.Len(t, st.Run.Report.Repeats, 4, "every participant's changed repeats")
	spent, err := database.AuthorLLMTokensSpent(ctx, e.db, run.ID)
	require.NoError(t, err)
	assert.Positive(t, spent)
	assert.Equal(t, spent, st.Run.SpentTokens)
	assert.Nil(t, st.Run.Cost, "no price configured")
	steps = stepsByKey(st)
	assert.True(t, steps["eval_pilot"].Done)
	assert.True(t, steps["eval_full"].Enabled)
	assert.False(t, steps["resolve_pilot"].Enabled, "resolve is not built yet")
}

func TestAuthorLLMAdminCostAndPauses(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	e.settings.Price = config.AuthorLLMPrice{Unit: "USD", ProductionA: 1, ProductionB: 2, JudgeA: 3, JudgeB: 4}
	admin := e.llmAdmin()
	run, err := admin.Start(ctx, "eval", "pilot", e.adminUser())
	require.NoError(t, err)
	e.evalUntilDone(run.ID)

	bySlot, err := database.AuthorLLMTokensBySlot(ctx, e.db, run.ID)
	require.NoError(t, err)
	want := (float64(bySlot["production_a"])*1 + float64(bySlot["production_b"])*2 +
		float64(bySlot["judge_a"])*3 + float64(bySlot["judge_b"])*4) / 1e6
	st, err := admin.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, "USD", st.PriceUnit)
	require.NotNil(t, st.Run.Cost)
	assert.InDelta(t, want, *st.Run.Cost, 1e-9)
	assert.Positive(t, *st.Run.Cost)
	assert.Equal(t, st.Run.SpentTokens, st.SpentTokens, "one run spent everything")

	// A participant's pause and the endpoint's, each with its reason, each
	// lifted by its own action.
	_, err = e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'model_mismatch', paused_at = now()
		WHERE config_version = ? AND slot = 'production_b'`, e.version)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE author_llm_endpoint_state SET paused_reason = 'quota_exhausted', paused_at = now()`)
	require.NoError(t, err)
	st, err = admin.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, "model_mismatch", st.Participants[1].PausedReason)
	assert.Empty(t, st.Participants[0].PausedReason)
	assert.Equal(t, "quota_exhausted", st.EndpointPausedReason)

	assert.ErrorIs(t, admin.ResumeParticipant(ctx, "judge_c"), services.ErrAuthorLLMInvalidSlot)
	require.NoError(t, admin.ResumeParticipant(ctx, "production_b"))
	require.NoError(t, admin.ResumeEndpoint(ctx))
	st, err = admin.Status(ctx)
	require.NoError(t, err)
	assert.Empty(t, st.Participants[1].PausedReason)
	assert.Empty(t, st.EndpointPausedReason)
}

// The active run is shown even when it belongs to an older configuration,
// so it can still be paused; its steps are not the current chain's.
func TestAuthorLLMAdminShowsTheActiveRunOfAnotherConfig(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeDev, nil, nil)
	require.NoError(t, err)
	admin := services.NewAuthorLLMAdmin(e.db, "another-version", "", &e.settings)
	st, err := admin.Status(ctx)
	require.NoError(t, err)
	require.NotNil(t, st.Run)
	assert.Equal(t, run, st.Run.ID)
	assert.False(t, st.Run.CurrentConfig)
	assert.Equal(t, services.AuthorLLMStepActiveRun, stepsByKey(st)["eval_pilot"].Blocked)
	_, err = admin.Pause(ctx, run)
	require.NoError(t, err)
}

// The read-only sampling check reports, by reason, the rows the freeze would
// leave out — poisoned ones included — and names nobody.
func TestAuthorLLMSamplingCheckReportsPoisonedRows(t *testing.T) {
	long := "Ааа Ббб Ввв Ггг Ддд Еее Жжж Ззз Иии Ккк Ллл Ммм Ннн Ооо Ппп Ррр Ссс"
	authors := append(slices.Clone(evalAuthors),
		[3]string{"", "", long},
		[3]string{"Ð\u0098Ð²Ð°Ð½", "", "ÐŸÐµÑ‚Ñ€Ð¾Ð²"},
		[3]string{" ", " ", " "},
		[3]string{"", "", "!!! ???"},
		[3]string{"", "", "Иванов И.И., Петров П.П.; Сидоров С.С."},
	)
	withEvalAuthors(t, authors)
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()

	var report *services.AuthorLLMSamplingReport
	require.NoError(t, e.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.Exec(`SET TRANSACTION READ ONLY`); err != nil {
			return err
		}
		var err error
		report, err = services.AuthorLLMSamplingCheck(ctx, tx, services.DefaultAuthorLLMEvalSizes())
		return err
	}))
	var longFP []byte
	_, err := e.db.QueryOne(pg.Scan(&longFP), `SELECT source_fingerprint FROM book_contributor_credit WHERE source_display_name = ?`, long)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Skipped["input_too_long"])
	assert.Equal(t, []string{hex.EncodeToString(longFP)}, report.SkippedSamples["input_too_long"])
	assert.Positive(t, report.Strata["initials.cyrl"])
	assert.Positive(t, report.Fingerprints)
	assert.Zero(t, report.AnchorPanics)
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	for _, name := range []string{"Ааа", "Ð", "Иванов", "Петров", "!!!"} {
		assert.NotContains(t, string(raw), name, "the report names nobody")
	}
}
