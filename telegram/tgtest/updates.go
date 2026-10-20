package tgtest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"
)

// Webhook posts updates to a bot's webhook the way Telegram does: JSON body,
// secret header, the bot's own path.
type Webhook struct {
	Handler http.Handler
	// Path is the bot's webhook path, e.g. "/telegram/<uuid>".
	Path string
	// Secret is the X-Telegram-Bot-Api-Secret-Token value.
	Secret string
}

// SecretHeader is the header Telegram signs webhook calls with.
const SecretHeader = "X-Telegram-Bot-Api-Secret-Token" // #nosec G101 -- a header name, not a credential

// Post delivers one update and returns the webhook's response.
func (h Webhook) Post(t testing.TB, update any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("encoding update: %v", err)
	}
	return h.PostRaw(t, body)
}

// PostRaw delivers a raw body, for updates the SDK types cannot express.
func (h Webhook) PostRaw(t testing.TB, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, h.Path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if h.Secret != "" {
		req.Header.Set(SecretHeader, h.Secret)
	}
	rec := httptest.NewRecorder()
	h.Handler.ServeHTTP(rec, req)
	return rec
}

// WebhookFromSetWebhook builds the Webhook a bot registered with the fake:
// the path and secret of its last setWebhook call. Using what the bot told
// Telegram, rather than recomputing it, keeps tests on the real contract.
func WebhookFromSetWebhook(t testing.TB, f *FakeBotAPI, token string, handler http.Handler) Webhook {
	t.Helper()
	calls := f.Calls("setWebhook")
	last := -1
	for i := range calls {
		if calls[i].Token == token {
			last = i
		}
	}
	if last < 0 {
		t.Fatalf("bot never called setWebhook: %s", f.summary())
		return Webhook{}
	}
	path := calls[last].Params["url"]
	if i := strings.Index(path, "://"); i >= 0 {
		path = path[i+3:]
		if j := strings.Index(path, "/"); j >= 0 {
			path = path[j:]
		}
	}
	return Webhook{Handler: handler, Path: path, Secret: calls[last].Params["secret_token"]}
}

// Updates builds updates with increasing update_id, from one private chat
// per user.
type Updates struct {
	next atomic.Int64
	msg  atomic.Int64
}

// firstUpdateID is where update ids start; Telegram's are large and positive.
const firstUpdateID = 1000

// NewUpdates starts update ids at a fixed base.
func NewUpdates() *Updates {
	u := &Updates{}
	u.next.Store(firstUpdateID)
	u.msg.Store(1)
	return u
}

// User is the sender of the updates a test builds.
func User(id int64) *models.User {
	return &models.User{ID: id, FirstName: "Reader", LanguageCode: "ru"}
}

// Text is a private text message from userID. A leading /command gets the
// bot_command entity Telegram adds, which the SDK's command matching needs.
func (u *Updates) Text(userID int64, text string) *models.Update {
	msg := &models.Message{
		ID:   int(u.msg.Add(1)),
		From: User(userID),
		Chat: models.Chat{ID: userID, Type: models.ChatTypePrivate},
		Date: int(time.Now().Unix()),
		Text: text,
	}
	if strings.HasPrefix(text, "/") {
		cmd := text
		if i := strings.IndexAny(cmd, " \n"); i >= 0 {
			cmd = cmd[:i]
		}
		msg.Entities = []models.MessageEntity{{
			Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: len(utf16.Encode([]rune(cmd))),
		}}
	}
	return &models.Update{ID: u.next.Add(1), Message: msg}
}

// ManagedBot is the managed_bot update a manager gets when creatorID made,
// or now owns, the bot of token, named username.
func (u *Updates) ManagedBot(creatorID int64, token, username string) *models.Update {
	return &models.Update{ID: u.next.Add(1), ManagedBot: &models.ManagedBotUpdated{
		User: User(creatorID),
		Bot:  &models.User{ID: botID(token), IsBot: true, FirstName: "Мои книги", Username: username},
	}}
}

// Callback is a press of the button with data on message (a message the bot
// sent, as recorded by the fake).
func (u *Updates) Callback(userID int64, on *Call, data string) *models.Update {
	chatID := on.Int64("chat_id")
	return &models.Update{
		ID: u.next.Add(1),
		CallbackQuery: &models.CallbackQuery{
			ID:   "cbq-" + strconv.FormatInt(u.next.Load(), 10),
			From: *User(userID),
			Message: models.MaybeInaccessibleMessage{
				Type: models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{
					ID:   int(on.MessageID),
					Chat: models.Chat{ID: chatID, Type: models.ChatTypePrivate},
					Date: int(time.Now().Unix()),
					Text: on.Params["text"],
				},
			},
			ChatInstance: "ci-" + strconv.FormatInt(chatID, 10),
			Data:         data,
		},
	}
}

// Tap presses the inline button labeled text on message on, as the user
// sees it. It fails the test if the message has no such button.
func (u *Updates) Tap(t testing.TB, userID int64, on *Call, text string) *models.Update {
	t.Helper()
	if on.MessageID == 0 {
		t.Fatalf("message %s has no message id yet: wait for it with WaitAnswered", on.Method)
	}
	data, ok := ButtonData(t, on, text)
	if !ok {
		t.Fatalf("message %d has no button %q: %v %v", on.MessageID, text, ButtonTexts(t, on), RichButtons(t, on))
	}
	return u.Callback(userID, on, data)
}

// Keyboard decodes the inline keyboard of a recorded call.
func Keyboard(t testing.TB, c *Call) [][]models.InlineKeyboardButton {
	t.Helper()
	if _, ok := c.Params["reply_markup"]; !ok {
		return nil
	}
	var markup models.InlineKeyboardMarkup
	if err := c.Decode("reply_markup", &markup); err != nil {
		t.Fatalf("decoding reply_markup of %s: %v", c.Method, err)
	}
	return markup.InlineKeyboard
}

// ButtonTexts lists the inline button labels of a call, row by row.
func ButtonTexts(t testing.TB, c *Call) [][]string {
	t.Helper()
	var rows [][]string
	for _, row := range Keyboard(t, c) {
		var texts []string
		for i := range row {
			texts = append(texts, row[i].Text)
		}
		rows = append(rows, texts)
	}
	return rows
}

// ButtonData returns the callback data of the button labeled text, in the
// keyboard or inside the rich message. A label that two buttons with
// different actions share fails the test: the press would be a guess.
func ButtonData(t testing.TB, c *Call, text string) (string, bool) {
	t.Helper()
	var found []string
	for _, row := range Keyboard(t, c) {
		for i := range row {
			if row[i].Text == text && row[i].CallbackData != "" {
				found = append(found, row[i].CallbackData)
			}
		}
	}
	for _, b := range RichButtons(t, c) {
		if b.Text == text && b.Data != "" {
			found = append(found, b.Data)
		}
	}
	if len(found) == 0 {
		return "", false
	}
	for _, data := range found[1:] {
		if data != found[0] {
			t.Fatalf("message %d has %d buttons labeled %q", c.MessageID, len(found), text)
		}
	}
	return found[0], true
}

// Labels are the labels of every button of a call: the keyboard's, row by
// row, then those inside the rich message.
func Labels(t testing.TB, c *Call) []string {
	t.Helper()
	var out []string
	for _, row := range ButtonTexts(t, c) {
		out = append(out, row...)
	}
	for _, b := range RichButtons(t, c) {
		out = append(out, b.Text)
	}
	return out
}

// RichButton is a button inside a rich message: in a row of buttons, or a
// link in the text.
type RichButton struct {
	Text     string
	Data     string
	URL      string
	Style    string
	Disabled bool
}

// RichButtons lists the buttons inside the rich message of a call, in the
// order they are shown.
func RichButtons(t testing.TB, c *Call) []RichButton {
	t.Helper()
	raw, ok := c.Params["rich_message"]
	if !ok {
		return nil
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decoding rich_message of %s: %v", c.Method, err)
	}
	var out []RichButton
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			if b, ok := richButton(v); ok {
				out = append(out, b)
				return
			}
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(v[k])
			}
		}
	}
	walk(doc)
	return out
}

// richButton reads a RichMessageButton: a text and an action, or disabled.
func richButton(v map[string]any) (RichButton, bool) {
	label, ok := v["text"].(string)
	if !ok {
		return RichButton{}, false
	}
	data, hasData := v["callback_data"].(string)
	link, hasURL := v["url"].(string)
	_, disabled := v["disabled"]
	if !hasData && !hasURL && !disabled {
		return RichButton{}, false
	}
	style, _ := v["style"].(string)
	return RichButton{Text: label, Data: data, URL: link, Style: style, Disabled: disabled}, true
}
