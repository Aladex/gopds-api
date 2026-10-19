package telegram_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The S4 fix round, S1 (review-s4.md): a bot switched back to the legacy
// router by its row gets its menu back.

// S1, the reviewer's TestReviewFlagRollbackRestoresCommands: the row alone
// switches a running bot back, menu included.
func TestAFlagRollbackRestoresTheCommands(t *testing.T) {
	v := newV2(t)
	require.NotZero(t, v.count(`SELECT count(*) FROM telegram_bot_ui WHERE commands_cleared_at IS NOT NULL`))
	before := len(v.fake.Calls("setMyCommands"))
	_, err := v.db.Exec(`UPDATE telegram_bot SET router = 'legacy'`)
	require.NoError(t, err)
	v.say("/start")
	assert.Greater(t, len(v.fake.Calls("setMyCommands")), before, "the legacy menu is registered again")
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_bot_ui WHERE commands_cleared_at IS NOT NULL`))
	registered := len(v.fake.Calls("setMyCommands"))
	v.say("/start")
	assert.Len(t, v.fake.Calls("setMyCommands"), registered, "once, not on every update")

	_, err = v.db.Exec(`UPDATE telegram_bot SET router = 'v2'`)
	require.NoError(t, err)
	cleared := len(v.fake.Calls("deleteMyCommands"))
	v.say("/start")
	assert.Greater(t, len(v.fake.Calls("deleteMyCommands")), cleared, "and forward again: v2 clears it")
}
