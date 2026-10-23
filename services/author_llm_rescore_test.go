package services_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"
	"gopds-api/services"
)

// A run judged by this build's validators previews as itself: every sent
// reply is judged again to the answer it was stored with, so the two
// reports decide the same, and the preview is never registrable.
func TestAuthorLLMRescorePreviewOfAnUnchangedRunChangesNothing(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, nil, nil)
	require.NoError(t, err)
	e.evalUntilDone(run)

	p, err := services.PreviewAuthorLLMEvalRescore(ctx, e.db, run)
	require.NoError(t, err)
	assert.Positive(t, p.Jobs.Rejudged)
	assert.Equal(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose <> 'eval_fingerprint'
		AND status IN ('answered', 'invalid')`, run), p.Jobs.Rejudged, "a terminal job was not judged again")
	assert.Zero(t, p.Jobs.BecameValid)
	assert.Zero(t, p.Jobs.BecameInvalid)
	assert.Zero(t, p.Jobs.CanonicalChanged)
	assert.Equal(t, p.Before.Decision, p.After.Decision)
	assert.Equal(t, p.Before.Strata, p.After.Strata)
	assert.False(t, p.Registrable)
	assert.Equal(t, services.AuthorLLMRescoreNotRegistrable, p.NotRegistrable)

	// Nothing was written: the run keeps its one report.
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_eval_report WHERE run_id = ?`, run))
}

func TestAuthorLLMRescorePreviewRefusesARunThatIsNoCompletedEval(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, nil, nil)
	require.NoError(t, err)
	_, err = services.PreviewAuthorLLMEvalRescore(ctx, e.db, run)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState)
}

// A run whose stored replies this build judges otherwise previews the
// difference: run under the binary before V8, the particle-role replies were
// stored valid; judged again they are invalid, and the preview's report
// counts them so while the stored one does not.
func TestAuthorLLMRescorePreviewJudgesTheStoredRepliesAgain(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ingestBooks(t, e, [][3]string{leGuin, vanGog, vonBraun}, 300)
	particle := particleRole(productionAModel, productionBModel, judgeAModel, judgeBModel)
	fingerprint := byFields(distinctLabs)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if isFingerprint(req) {
			return fingerprint(req)
		}
		return particle(req)
	})
	ctx := context.Background()
	llmres.TestHookSkipV8.Store(true)
	t.Cleanup(func() { llmres.TestHookSkipV8.Store(false) })
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, nil, nil)
	require.NoError(t, err)
	e.evalUntilDone(run)
	llmres.TestHookSkipV8.Store(false)

	p, err := services.PreviewAuthorLLMEvalRescore(ctx, e.db, run)
	require.NoError(t, err)
	assert.Positive(t, p.Jobs.BecameInvalid, "no stored particle reply was judged invalid again")
	assert.Zero(t, p.Jobs.BecameValid)
	invalid := func(r *llmeval.Report) (n int) {
		for _, c := range r.Invalid {
			n += c.Matched
		}
		return n
	}
	assert.Equal(t, invalid(&p.Before)+p.Jobs.BecameInvalid, invalid(&p.After))
}
