package telegram_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

// heldPlainCard holds the first plain card sent with «⏳ Готовлю EPUB» on
// its way to Telegram, until released.
type heldPlainCard struct {
	entered, release chan struct{}
	once             sync.Once
}

func (h *heldPlainCard) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/sendMessage") && req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		decoded, _ := url.QueryUnescape(string(body))
		if strings.Contains(decoded, "⏳ Готовлю EPUB") {
			first := false
			h.once.Do(func() { first = true; close(h.entered) })
			if first {
				select {
				case <-h.release:
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

// Notes item 8 (review-s5b.md S1), the reviewer's
// TestReviewReplacementKeepsCompletionWhoseRecordRacesRetirement: a job's
// end read before its card was replaced records its repaint for the retired
// panel after the replacement took its jobs over; the repaint follows the
// job to the replacement.
func TestALateRepaintFollowsItsJobToTheReplacement(t *testing.T) {
	tr := &heldPlainCard{entered: make(chan struct{}), release: make(chan struct{})}
	var sendOnce, insertOnce sync.Once
	letSend := func() { sendOnce.Do(func() { close(tr.release) }) }
	defer letSend()
	inserting, releaseInsert := make(chan struct{}), make(chan struct{})
	letInsert := func() { insertOnce.Do(func() { close(releaseInsert) }) }
	defer letInsert()
	var releasePrepare func()
	var hookOnce sync.Once
	v := newV2(t, func(s *stack) {
		s.transport = tr
		releasePrepare = gatedPrepare(s)
		s.db.AddQueryHook(queryHook(func(_ context.Context, q string) error {
			if strings.HasPrefix(strings.TrimSpace(q), "INSERT INTO") && strings.Contains(q, `"telegram_panel_repaint"`) {
				hookOnce.Do(func() { close(inserting); <-releaseInsert })
			}
			return nil
		}))
	})
	defer releasePrepare()
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	preparing := v.latest(card.MessageID)
	home := v.homePanel()
	v.tap(&home, "Настройки")
	settings := v.homePanel()
	v.tap(&settings, "Вид карточек: расширенный")
	style := v.homePanel()
	v.tap(&style, "Простой")
	pressed := make(chan struct{})
	go func() { v.tap(&preparing, "☆ В избранное"); close(pressed) }()
	select {
	case <-tr.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the replacement's send was not held")
	}
	releasePrepare()
	v.waitJobSent()
	select {
	case <-inserting:
	case <-time.After(5 * time.Second):
		t.Fatal("the job's end did not reach its repaint")
	}
	letSend()
	select {
	case <-pressed:
	case <-time.After(5 * time.Second):
		t.Fatal("the card was not replaced")
	}
	replacement := v.lastSend()
	require.True(t, hasButton(t, &replacement, "⏳ Готовлю EPUB…"))
	require.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_panel WHERE message_id = ? AND node_id = 0`, card.MessageID))
	require.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_delivery WHERE origin_message_id = ?`, replacement.MessageID))
	letInsert()
	v.delivery.WaitIdle()
	v.bm.WaitIdle()
	require.Eventually(t, func() bool {
		final := v.latest(replacement.MessageID)
		return hasButton(t, &final, "EPUB") && !hasButton(t, &final, "⏳ Готовлю EPUB…")
	}, 5*time.Second, 10*time.Millisecond, "a repaint recorded for the retired card reaches the replacement")
	assert.Len(t, v.fake.Calls("sendDocument"), 1)
}
