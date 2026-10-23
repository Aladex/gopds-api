package services_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"
)

// Run 14 chose the excerpt arm in none of its strata: a run started with
// the defaults asks the candidates without the excerpt only, and its gate
// measures arm A alone.
func TestAuthorLLMEvalDefaultsLeaveTheExcerptArmOut(t *testing.T) {
	defaults := services.DefaultAuthorLLMEvalSettings()
	require.True(t, defaults.NoExcerpt)

	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	run, err := services.StartAuthorLLMEval(context.Background(), e.db, e.version, models.AuthorLLMEvalModeFull, nil, nil)
	require.NoError(t, err)
	e.evalUntilDone(run)

	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE arm = 'B'`), "an arm B job was asked")
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE arm = 'A'`))
	r := downloadReport(t, e, run)
	strata, ok := r.Evaluation.Strata["initials.cyrl"]
	require.True(t, ok, "strata = %v", r.Evaluation.Strata)
	for combo := range strata.Combos {
		assert.Contains(t, []string{"A1", "A10"}, combo)
	}
	assert.Len(t, strata.Combos, 2)
}

// A run stored before the setting existed has no key: read again it keeps
// both arms, so it measures — and a recomputation recomputes — what it asked.
func TestAuthorLLMEvalRunStoredWithoutTheKeyKeepsBothArms(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	// The settings a run stored before the key existed.
	legacy := []byte(`{"output_modes":{},"batch_sizes":[1,10]}`)
	run, _, err := database.StartAuthorLLMEvalRun(context.Background(), e.db, e.version, models.AuthorLLMEvalModeFull, legacy, nil)
	require.NoError(t, err)
	e.evalUntilDone(run)

	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE arm = 'B'`))
	strata := downloadReport(t, e, run).Evaluation.Strata["initials.cyrl"]
	assert.Len(t, strata.Combos, 4)
}

// A new run stores the setting explicitly.
func TestAuthorLLMEvalDefaultSettingsStoreTheExcerptKey(t *testing.T) {
	raw, err := json.Marshal(services.DefaultAuthorLLMEvalSettings())
	require.NoError(t, err)
	assert.JSONEq(t, `{"output_modes":{},"batch_sizes":[1,10],"no_excerpt":true}`, string(raw))
}
