package telegram_test

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The S4 fix round, B1 (review-s4.md): one panel transition at a time per
// chat, whichever pod runs it.

// heldPreparing holds the first card edit that says «⏳ Готовлю EPUB» on its
// way to Telegram, until released.
type heldPreparing struct {
	entered, release chan struct{}
	once             sync.Once
}

func (h *heldPreparing) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/editMessageText") && req.Body != nil {
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

// B1, after the reviewer's TestReviewLatePreparingEditMustNotOverwriteFinishedCard.
// The press's «⏳» edit is held in flight while its job finishes. The probe
// as written waited for the job's repaint to land before letting the press's
// edit through — the very order the chat lock now forbids — so here the
// repaint is asserted to wait instead, and the end state is the probe's.
func TestALatePreparingEditDoesNotOverwriteTheFinishedCard(t *testing.T) {
	tr := &heldPreparing{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(tr.release) }) }
	defer letGo()
	var releasePrepare func()
	v := newV2(t, func(s *stack) { s.transport = tr; releasePrepare = gatedPrepare(s) })
	defer releasePrepare()
	_, card := v.openFixtureCard()
	initialEdits := len(v.edits(card.MessageID))

	done := make(chan struct{})
	go func() { v.tap(&card, "EPUB"); close(done) }()
	select {
	case <-tr.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the preparing edit did not reach the transport")
	}
	releasePrepare()
	v.waitJobSent()
	finished := func() bool {
		for _, c := range v.edits(card.MessageID)[initialEdits:] {
			rows := tgtest.ButtonTexts(t, &c)
			if !strings.Contains(content(&c), "⏳") && len(rows) > 0 && rows[0][0] == "EPUB" {
				return true
			}
		}
		return false
	}
	assert.Never(t, finished, 500*time.Millisecond, 20*time.Millisecond,
		"the job's repaint must wait for the press's edit to land")

	letGo()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the press did not finish")
	}
	require.Eventually(t, finished, 10*time.Second, 10*time.Millisecond, "the job's repaint lands after the press's edit")
	v.delivery.WaitIdle()
	assert.Len(t, v.fake.Calls("sendDocument"), 1)
	latest := v.latest(card.MessageID)
	assert.Equal(t, "EPUB", tgtest.ButtonTexts(t, &latest)[0][0], "a delayed preparing edit must not overwrite the completed card")
	edits := len(v.edits(card.MessageID))
	v.tap(&latest, "← К результатам")
	assert.Greater(t, len(v.edits(card.MessageID)), edits, "the visible keyboard agrees with the stored revision")
	answers := v.fake.Calls("answerCallbackQuery")
	assert.NotEqual(t, "Этот экран уже изменился.", answers[len(answers)-1].Params["text"])
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_chat_lock`), "every transition let its chat go")
}

// B1 across pods: a chat held by another process makes the update wait, and
// an update that waited too long is handed back unread, not dropped.
func TestAChatHeldElsewhereHandsTheUpdateBack(t *testing.T) {
	v := newV2(t, func(s *stack) { s.chatWait = 300 * time.Millisecond })
	v.say("мир")
	panel := v.lastSend()
	_, err := v.db.Exec(`INSERT INTO telegram_chat_lock (bot_ref, chat_id, holder, expires_at)
		SELECT id, ?, 'other-pod', now() + interval '1 hour' FROM telegram_bot WHERE router = 'v2'`, ownerTG)
	require.NoError(t, err)
	sends := len(v.sends())
	update := v.u.Text(ownerTG, "Основание")
	rec := v.hook(botToken1).Post(t, update)
	require.Equal(t, http.StatusOK, rec.Code)
	v.bm.WaitIdle()
	assert.Len(t, v.sends(), sends, "nothing is sent while the chat is held")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND state = 'pending'`, update.ID),
		"the update is kept for the next sweep")
	assert.NotEmpty(t, panel.MessageID)
}
