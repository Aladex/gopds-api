package database

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The autopilot's row (migration 62): the lease lets one pod tick, the lock
// refuses once switched off, and a tick that found what the last one did
// writes nothing.

func TestTheAutopilotLeaseIsOnePodsAtATime(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	now := time.Now()
	one, two := uuid.New(), uuid.New()
	st, err := ClaimAuthorAutopilot(ctx, f.tx, one, time.Minute, now)
	require.NoError(t, err)
	require.NotNil(t, st)
	st, err = ClaimAuthorAutopilot(ctx, f.tx, two, time.Minute, now.Add(59*time.Second))
	require.NoError(t, err)
	assert.Nil(t, st, "a second pod took a live lease")
	require.NoError(t, LockAuthorAutopilot(ctx, f.tx, one, time.Minute, now.Add(59*time.Second)), "the holder renews")
	require.ErrorIs(t, LockAuthorAutopilot(ctx, f.tx, two, time.Minute, now), ErrAuthorAutopilotLeaseLost)

	later := now.Add(59*time.Second + time.Minute)
	require.ErrorIs(t, LockAuthorAutopilot(ctx, f.tx, one, time.Minute, later), ErrAuthorAutopilotLeaseLost,
		"a lease that ran out is no lease")
	st, err = ClaimAuthorAutopilot(ctx, f.tx, two, time.Minute, later)
	require.NoError(t, err)
	require.NotNil(t, st, "the next pod takes a lease that ran out")
	require.ErrorIs(t, LockAuthorAutopilot(ctx, f.tx, one, time.Minute, later), ErrAuthorAutopilotLeaseLost)
	require.NoError(t, LockAuthorAutopilot(ctx, f.tx, two, time.Minute, later))
}

// Switched off, the lock refuses every write of the lease holder.
func TestTheAutopilotLockRefusesOnceSwitchedOff(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	now := time.Now()
	owner := uuid.New()
	st, err := ClaimAuthorAutopilot(ctx, f.tx, owner, time.Minute, now)
	require.NoError(t, err)
	require.NotNil(t, st)
	require.NoError(t, LockAuthorAutopilot(ctx, f.tx, owner, time.Minute, now))
	require.NoError(t, SetAuthorAutopilotEnabled(ctx, f.tx, false, 0))
	require.ErrorIs(t, LockAuthorAutopilot(ctx, f.tx, owner, time.Minute, now), ErrAuthorAutopilotOff)
	require.NoError(t, SetAuthorAutopilotEnabled(ctx, f.tx, true, 0))
	require.NoError(t, LockAuthorAutopilot(ctx, f.tx, owner, time.Minute, now))
}

// A tick records what it found once; the same again writes nothing; a
// failure streak is cleared by the next tick that goes through.
func TestAnAutopilotTickRecordsOnlyAChange(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	now := time.Now()
	idle := &AuthorAutopilotTick{State: AuthorAutopilotStateIdle, ResolveNote: "nothing_to_resolve"}
	changed, err := RecordAuthorAutopilotTick(ctx, f.tx, idle, now)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = RecordAuthorAutopilotTick(ctx, f.tx, idle, now)
	require.NoError(t, err)
	assert.False(t, changed, "the same tick was written again")
	changed, err = RecordAuthorAutopilotTick(ctx, f.tx, &AuthorAutopilotTick{
		State: AuthorAutopilotStateLLMRunActive, StateRunID: 7}, now)
	require.NoError(t, err)
	assert.True(t, changed)
	st, err := GetAuthorAutopilot(ctx, f.tx)
	require.NoError(t, err)
	assert.Equal(t, int64(7), *st.StateRunID)
	assert.Nil(t, st.ResolveNote)

	failures, err := RecordAuthorAutopilotFailure(ctx, f.tx, "internal", now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, failures)
	changed, err = RecordAuthorAutopilotTick(ctx, f.tx, &AuthorAutopilotTick{
		State: AuthorAutopilotStateLLMRunActive, StateRunID: 7}, now)
	require.NoError(t, err)
	assert.True(t, changed, "a tick after a failure left the streak")
	st, err = GetAuthorAutopilot(ctx, f.tx)
	require.NoError(t, err)
	assert.Zero(t, st.Failures)
	assert.Nil(t, st.NextAttemptAt)
	assert.Nil(t, st.LastErrorClass)
}
