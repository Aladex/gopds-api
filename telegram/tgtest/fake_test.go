package tgtest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gopds-api/telegram/tgtest"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const token = "42:test-token"

func newBot(t *testing.T, f *tgtest.FakeBotAPI) *bot.Bot {
	t.Helper()
	b, err := bot.New(token, bot.WithServerURL(f.URL()), bot.WithSkipGetMe())
	require.NoError(t, err)
	return b
}

// A rule answers as many calls as it was given, then the healthy default
// takes over; recorded calls keep the fields, the keyboard and the message id.
func TestRuleTimesThenDefault(t *testing.T) {
	f := tgtest.NewFakeBotAPI(t)
	f.On(tgtest.Rule{Method: "sendMessage", Reply: tgtest.Forbidden(""), Times: 1})
	b := newBot(t, f)
	ctx := context.Background()
	markup := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Меню", CallbackData: "m"}}}}

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: 7, Text: "first", ReplyMarkup: markup})
	require.ErrorIs(t, err, bot.ErrorForbidden)
	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: 7, Text: "second", ReplyMarkup: markup})
	require.NoError(t, err)

	calls := f.WaitAnswered(t, "sendMessage", 2, time.Second)
	assert.Equal(t, "first", calls[0].Params["text"])
	assert.Zero(t, calls[0].MessageID, "a refused send creates no message")
	assert.Equal(t, int64(msg.ID), calls[1].MessageID)
	assert.Equal(t, [][]string{{"Меню"}}, tgtest.ButtonTexts(t, &calls[1]))
	data, ok := tgtest.ButtonData(t, &calls[1], "Меню")
	assert.True(t, ok)
	assert.Equal(t, "m", data)
	assert.Equal(t, "7", calls[1].Params["chat_id"])
}

func TestUploadsAreRecorded(t *testing.T) {
	f := tgtest.NewFakeBotAPI(t)
	b := newBot(t, f)

	msg, err := b.SendDocument(context.Background(), &bot.SendDocumentParams{
		ChatID:   7,
		Document: &models.InputFileUpload{Filename: "book.epub", Data: bytesReader("EPUB")},
	})
	require.NoError(t, err)
	require.NotNil(t, msg.Document)
	assert.Equal(t, "book.epub", msg.Document.FileName)

	doc := f.Calls("sendDocument")[0].Files["document"]
	assert.Equal(t, "book.epub", doc.Filename)
	assert.Equal(t, []byte("EPUB"), doc.Data)
}

// Commands carry the bot_command entity Telegram adds, measured in UTF-16.
func TestTextUpdateMarksCommands(t *testing.T) {
	u := tgtest.NewUpdates()
	cmd := u.Text(1, "/b Основание")
	require.Len(t, cmd.Message.Entities, 1)
	assert.Equal(t, models.MessageEntityTypeBotCommand, cmd.Message.Entities[0].Type)
	assert.Equal(t, 2, cmd.Message.Entities[0].Length)

	plain := u.Text(1, "Основание")
	assert.Empty(t, plain.Message.Entities)
	assert.Greater(t, plain.ID, cmd.ID, "update ids increase")
}

func TestWebhookFromSetWebhook(t *testing.T) {
	f := tgtest.NewFakeBotAPI(t)
	b := newBot(t, f)
	_, err := b.SetWebhook(context.Background(), &bot.SetWebhookParams{
		URL: "https://bot.example.test/telegram/abc", SecretToken: "s3cret",
	})
	require.NoError(t, err)

	var seen *http.Request
	hook := tgtest.WebhookFromSetWebhook(t, f, token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
	}))
	hook.Post(t, tgtest.NewUpdates().Text(1, "hi"))
	require.NotNil(t, seen)
	assert.Equal(t, "/telegram/abc", seen.URL.Path)
	assert.Equal(t, "s3cret", seen.Header.Get(tgtest.SecretHeader))
}

func bytesReader(s string) *strings.Reader { return strings.NewReader(s) }

// Two sends whose replies hang have both reached the server: the journal
// shows them as received and unanswered, so a test asserting "nothing else
// was sent" cannot pass while they are in flight.
func TestHangingSendsAreRecordedOnReceipt(t *testing.T) {
	f := tgtest.NewFakeBotAPI(t)
	f.On(tgtest.Rule{Method: "sendMessage", Reply: f.Hang(10 * time.Second)})
	b := newBot(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	for _, text := range []string{"one", "two"} {
		go func(text string) {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: 7, Text: text})
			done <- struct{}{}
		}(text)
	}

	received := f.WaitCalls(t, "sendMessage", 2, 5*time.Second)
	assert.Len(t, received, 2)
	for _, c := range received {
		assert.False(t, c.Answered)
		assert.Zero(t, c.MessageID)
	}
	assert.Empty(t, f.AnsweredCalls("sendMessage"))

	cancel()
	<-done
	<-done
	f.Quiet(t, "sendMessage", 2, 50*time.Millisecond)
	assert.Empty(t, f.AnsweredCalls("sendMessage"), "a reply the client gave up on did not answer")
}

// Over HTTP/2 a dropped connection aborts the handler with a panic; the call
// was received all the same and stays in the journal.
func TestAnAbortedHTTP2SendIsRecorded(t *testing.T) {
	srv := httptest.NewUnstartedServer(nil)
	srv.EnableHTTP2 = true
	f := tgtest.NewFakeBotAPIFromServer(t, srv)
	srv.Config.Handler = f.Handler()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	f.On(tgtest.Rule{Method: "sendMessage", Reply: tgtest.CloseAfterRead()})

	b, err := bot.New(token, bot.WithServerURL(srv.URL), bot.WithSkipGetMe(), bot.WithHTTPClient(time.Minute, srv.Client()))
	require.NoError(t, err)
	_, err = b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: 7, Text: "lost"})
	require.Error(t, err)

	calls := f.WaitCalls(t, "sendMessage", 1, 5*time.Second)
	require.Len(t, calls, 1)
	assert.Equal(t, "lost", calls[0].Params["text"])
	assert.False(t, calls[0].Answered)
}

// A refused call is answered, with its status.
func TestARefusedCallIsAnsweredWithItsStatus(t *testing.T) {
	f := tgtest.NewFakeBotAPI(t)
	f.On(tgtest.Rule{Method: "sendMessage", Reply: tgtest.Forbidden("")})
	_, err := newBot(t, f).SendMessage(context.Background(), &bot.SendMessageParams{ChatID: 7, Text: "x"})
	require.Error(t, err)
	answered := f.WaitAnswered(t, "sendMessage", 1, time.Second)
	assert.Equal(t, http.StatusForbidden, answered[0].Status)
}
