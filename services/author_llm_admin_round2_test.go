package services_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Review B1 of phase 4: a run of an older configuration cannot go on — the
// worker and the driver serve only their own configuration — so it may not
// be resumed, and the card can close it to free the one active slot.

// withEndpoint is the same installation after a restart with another
// endpoint: a new configuration identity and version, its own fake provider,
// and (through llmAdmin, worker and the eval driver) its own admin, worker and
// driver. Nothing of the previous configuration keeps running.
func (e *llmEnv) withEndpoint(t *testing.T) *llmEnv {
	t.Helper()
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	identity, err := llmreq.NewIdentity(srv.URL+"/v1", testParticipants, testOutput)
	require.NoError(t, err)
	version, err := database.EnsureAuthorLLMConfig(context.Background(), e.db, &identity, 4)
	require.NoError(t, err)
	require.NotEqual(t, e.version, version)
	next := *e
	next.fake, next.srv, next.identity, next.version = fake, srv, identity, version
	return &next
}

func TestAuthorLLMRunOfAnOlderConfigurationIsClosedNotResumed(t *testing.T) {
	a := newLLMEnv(t)
	ingestEvalCatalogue(t, a)
	a.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	actor := a.adminUser()

	// Configuration A: a pilot reaches its fingerprint stage, a worker
	// claims one call and dies before its settle, and the run is paused.
	adminA := a.llmAdmin()
	run, err := adminA.Start(ctx, "eval", "pilot", actor)
	require.NoError(t, err)
	a.stepTo(run.ID, "fingerprint")
	driverA := services.NewAuthorLLMEvalDriver(a.db, &a.identity, a.version, evalSizes, a.archives, nil, time.Second)
	_, err = driverA.Step(ctx)
	require.NoError(t, err)
	owner := database.NewLeaseOwner()
	now := time.Now()
	claim, refusal, err := database.ClaimAuthorLLMCall(ctx, a.db, a.claimOptions(llmreq.SlotProductionA, owner, &now))
	require.NoError(t, err)
	require.NotNil(t, claim, "refusal %s", refusal)
	spent, err := database.AuthorLLMTokensSpent(ctx, a.db, run.ID)
	require.NoError(t, err)
	_, err = adminA.Pause(ctx, run.ID)
	require.NoError(t, err)

	// Configuration B after a restart: the old run is shown, not resumable.
	b := a.withEndpoint(t)
	b.fake.setRespond(byFields(distinctLabs))
	adminB := b.llmAdmin()
	st, err := adminB.Status(ctx)
	require.NoError(t, err)
	require.NotNil(t, st.Run)
	assert.Equal(t, run.ID, st.Run.ID)
	assert.False(t, st.Run.CurrentConfig)
	_, err = adminB.Resume(ctx, run.ID)
	assert.ErrorIs(t, err, services.ErrAuthorLLMRunConfigChanged)
	_, status := b.runStage(run.ID)
	assert.Equal(t, string(models.AuthorLLMRunPaused), status, "a refused resume changed the run")
	_, err = adminB.Start(ctx, "eval", "pilot", actor)
	require.ErrorIs(t, err, database.ErrAuthorLLMActiveRun, "the old run still holds the slot")

	// Closing ends it, keeps what it was charged and fences its open call.
	closed, err := adminB.Close(ctx, run.ID, actor)
	require.NoError(t, err)
	assert.Equal(t, string(models.AuthorLLMRunClosed), closed.Status)
	r, err := database.GetAuthorLLMRun(ctx, b.db, run.ID)
	require.NoError(t, err)
	require.NotNil(t, r.ClosedByUserID)
	assert.Equal(t, actor, *r.ClosedByUserID)
	assert.NotNil(t, r.FinishedAt)
	assert.Nil(t, r.PausedReason)
	var call struct {
		Outcome  string
		Settled  int64
		Reserved int64
	}
	_, err = b.db.QueryOne(&call, `SELECT outcome, settled_tokens AS settled, reserved_tokens AS reserved
		FROM author_llm_call WHERE id = ?`, claim.CallID)
	require.NoError(t, err)
	assert.Equal(t, "abandoned", call.Outcome)
	assert.Equal(t, call.Reserved, call.Settled, "the closed call keeps its reserve as spent")
	after, err := database.AuthorLLMTokensSpent(ctx, b.db, run.ID)
	require.NoError(t, err)
	assert.Equal(t, spent, after, "closing changed what the run was charged")
	assert.Zero(t, b.count(`SELECT count(*) FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.run_id = ? AND a.finished_at IS NULL`, run.ID), "an attempt of the closed run is still open")
	assert.Zero(t, b.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND status = 'claimed'`, run.ID))

	// The late reply of the dead worker is fenced: nothing is written.
	_, err = database.SettleAuthorLLMCall(ctx, a.db, &database.AuthorLLMSettlement{CallID: claim.CallID, Owner: owner,
		Outcome: models.AuthorLLMCallAnswered, MaxAttempts: 5, ConcurrencyMax: 16,
		BaseBackoff: time.Second, MaxBackoff: time.Minute})
	assert.ErrorIs(t, err, database.ErrAuthorLLMLeaseLost)
	assert.Zero(t, b.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND status = 'answered'`, run.ID))

	_, err = adminB.Close(ctx, run.ID, actor)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState, "a closed run closed twice")

	// The slot is free: a pilot of configuration B starts and completes.
	runB, err := adminB.Start(ctx, "eval", "pilot", actor)
	require.NoError(t, err)
	stages := b.evalUntilDone(runB.ID)
	assert.Equal(t, "report", stages[len(stages)-1])
	st, err = adminB.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, runB.ID, st.Run.ID)
	assert.True(t, stepsByKey(st)["eval_full"].Enabled)
}

// The current configuration's run may be closed only while paused: a
// running one is paused first.
func TestAuthorLLMCloseOfTheCurrentConfigurationNeedsAPause(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	actor := e.adminUser()
	admin := e.llmAdmin()
	run, err := admin.Start(ctx, "eval", "dev", actor)
	require.NoError(t, err)
	_, err = admin.Close(ctx, run.ID, actor)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunState)
	_, status := e.runStage(run.ID)
	assert.Equal(t, string(models.AuthorLLMRunRunning), status)
	_, err = admin.Pause(ctx, run.ID)
	require.NoError(t, err)
	closed, err := admin.Close(ctx, run.ID, actor)
	require.NoError(t, err)
	assert.Equal(t, string(models.AuthorLLMRunClosed), closed.Status)
	_, err = admin.Close(ctx, run.ID+1000, actor)
	assert.ErrorIs(t, err, database.ErrAuthorLLMRunNotFound)
}
