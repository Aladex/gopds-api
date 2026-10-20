package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gopds-api/telegram/tgapi"
	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	previewToken = "4242:preview"
	previewChat  = int64(777)
	waitCalls    = 5 * time.Second
)

// startPreview sends the three lists to a fake Bot API.
func startPreview(t *testing.T) (*previewer, *tgtest.FakeBotAPI, []tgtest.Call) {
	t.Helper()
	fake := tgtest.NewFakeBotAPI(t)
	p, err := newPreviewer(previewChat, 4242)
	require.NoError(t, err)
	p.b, err = tgapi.New(previewToken, tgapi.Endpoint{ServerURL: fake.URL()})
	require.NoError(t, err)
	require.NoError(t, p.sendAll(context.Background()))
	sent := fake.WaitAnswered(t, "sendRichMessage", 3, waitCalls)
	return p, fake, sent
}

// lastEdit is the newest edit of a message.
func lastEdit(t *testing.T, fake *tgtest.FakeBotAPI, n int) tgtest.Call {
	t.Helper()
	edits := fake.WaitAnswered(t, "editMessageText", n, waitCalls)
	return edits[n-1]
}

func headingOf(t *testing.T, c *tgtest.Call) string {
	t.Helper()
	var msg struct {
		Blocks []struct {
			Type string `json:"type"`
			Text any    `json:"text"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal([]byte(c.Params["rich_message"]), &msg))
	require.NotEmpty(t, msg.Blocks)
	text, _ := msg.Blocks[0].Text.(string)
	return text
}

// (p) The preview sends the results, the author's books and the favorites,
// each a rich list with its titles as buttons.
func TestThePreviewSendsTheThreeLists(t *testing.T) {
	_, _, sent := startPreview(t)
	assert.Equal(t, "Поиск: «мир»", headingOf(t, &sent[0]))
	assert.Equal(t, "Аркадий и Борис Стругацкие", headingOf(t, &sent[1]))
	assert.Equal(t, "Избранное", headingOf(t, &sent[2]))
	for i := range sent {
		links := 0
		for _, b := range tgtest.RichButtons(t, &sent[i]) {
			if b.Style == "link" {
				links++
			}
		}
		assert.GreaterOrEqual(t, links, 4, "message %d: its titles are links", i)
	}
}

// (p) A press edits the list as the bot would: the next page, the other
// tab, a card, and back to the list.
func TestThePreviewsButtonsEditTheList(t *testing.T) {
	p, fake, sent := startPreview(t)
	u := tgtest.NewUpdates()
	results := sent[0]
	press := func(on *tgtest.Call, label string) {
		p.handle(context.Background(), u.Tap(t, previewChat, on, label))
	}

	press(&results, "→")
	page2 := lastEdit(t, fake, 1)
	assert.Contains(t, page2.Params["rich_message"], "Страница 2 из 5")

	press(&page2, "Авторы · 7")
	authors := lastEdit(t, fake, 2)
	assert.Contains(t, authors.Params["rich_message"], "41 книга")

	press(&authors, "Книги · 23")
	books := lastEdit(t, fake, 3)
	press(&books, "Пикник на обочине")
	card := lastEdit(t, fake, 4)
	assert.Contains(t, card.Params["rich_message"], `"details"`, "the card, with its annotation")

	press(&card, "← К результатам")
	back := lastEdit(t, fake, 5)
	assert.Contains(t, back.Params["rich_message"], "Пикник на обочине")
	assert.Contains(t, back.Params["rich_message"], "Страница 1 из 5")

	// A button of a screen already moved on is refused, not acted on.
	p.handle(context.Background(), u.Tap(t, previewChat, &results, "→"))
	fake.Quiet(t, "editMessageText", 5, 200*time.Millisecond)
	answers := fake.Calls("answerCallbackQuery")
	assert.Equal(t, "Этот экран уже изменился.", answers[len(answers)-1].Params["text"])
}

// (p) -send-only sends the lists and the states a press leads to, each a
// message of its own, and neither reads updates nor touches the webhook.
func TestThePreviewSendOnlySendsTheNextStatesToo(t *testing.T) {
	fake := tgtest.NewFakeBotAPI(t)
	p, err := newPreviewer(previewChat, 4242)
	require.NoError(t, err)
	p.b, err = tgapi.New(previewToken, tgapi.Endpoint{ServerURL: fake.URL()})
	require.NoError(t, err)
	require.NoError(t, p.sendStates(context.Background()))
	sent := fake.WaitAnswered(t, "sendRichMessage", 6, waitCalls)
	assert.Contains(t, sent[3].Params["rich_message"], "Страница 2 из 5", "the results' next page")
	assert.Contains(t, sent[4].Params["rich_message"], "41 книга", "the authors tab")
	assert.Contains(t, sent[5].Params["rich_message"], "Страница 2 из 3", "the author's next page")
	for _, m := range []string{"getUpdates", "getWebhookInfo", "setWebhook", "deleteWebhook", "editMessageText"} {
		assert.Empty(t, fake.Calls(m), m)
	}
}
