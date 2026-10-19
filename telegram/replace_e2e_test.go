package telegram_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Notes item 7 (review-s5.md S1): a card waiting for its file and replaced
// by a plain panel hands the file's completion to the replacement — the
// reviewer's TestReviewS5PlainReplacementReceivesPendingJobCompletion, with
// its rich control.
func TestAReplacedPendingCardGetsItsCompletion(t *testing.T) {
	for name, plain := range map[string]bool{"replaced by plain": true, "rich control": false} {
		t.Run(name, func(t *testing.T) { pendingCompletion(t, plain) })
	}
}

func pendingCompletion(t *testing.T, plainStyle bool) {
	var releasePrepare func()
	v := newV2(t, func(s *stack) { releasePrepare = gatedPrepare(s) })
	defer releasePrepare()
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	preparing := v.latest(card.MessageID)
	require.True(t, hasButton(t, &preparing, "⏳ Готовлю EPUB…"))
	if plainStyle {
		home := v.homePanel()
		v.tap(&home, "Настройки")
		settings := v.homePanel()
		v.tap(&settings, "Вид карточек: расширенный")
		style := v.homePanel()
		v.tap(&style, "Простой")
	}
	sends := len(v.sends())
	v.tap(&preparing, "☆ В избранное")
	current := v.latest(card.MessageID)
	expectedSends := sends
	if plainStyle {
		expectedSends++
		require.Len(t, v.sends(), expectedSends)
		current = v.lastSend()
		require.Empty(t, current.Params["rich_message"])
	}
	require.True(t, hasButton(t, &current, "⏳ Готовлю EPUB…"))
	releasePrepare()
	v.waitJobSent()
	v.delivery.WaitIdle()
	v.bm.WaitIdle()
	require.Eventually(t, func() bool {
		final := v.latest(current.MessageID)
		return hasButton(t, &final, "EPUB") && !hasButton(t, &final, "⏳ Готовлю EPUB…")
	}, 5*time.Second, 10*time.Millisecond, "the current card's marker clears when its file is sent")
	assert.Len(t, v.sends(), expectedSends, "no further card from the retired one")
	assert.Len(t, v.fake.Calls("sendDocument"), 1)
}
