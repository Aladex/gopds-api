package tgtest_test

import (
	"context"
	"net/http"
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

	calls := f.WaitCalls(t, "sendMessage", 2, time.Second)
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
