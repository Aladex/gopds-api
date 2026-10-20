package telegram_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hotfix (v0.60.0): pinning the home panel makes Telegram deliver a service
// message whose sender is the bot itself. The intake took it for a stranger's
// message and told the owner the bot was someone else's.

// serviceUpdate is a private-chat message in chatID from sender, with the
// service field given as raw JSON — the shape Telegram sends, which the SDK
// types cannot encode for pinned_message.
func (v *v2Stack) serviceUpdate(chatID int64, sender, field string) []byte {
	id := v.u.Text(ownerTG, "").ID
	return fmt.Appendf(nil, `{"update_id":%d,"message":{"message_id":%d,"from":%s,`+
		`"chat":{"id":%d,"first_name":"Reader","type":"private"},"date":%d,%s}}`,
		id, id, sender, chatID, time.Now().Unix(), field)
}

// postSkipped delivers a raw update and expects it acknowledged unread.
func (v *v2Stack) postSkipped(body []byte) {
	v.t.Helper()
	rec := v.hook(botToken1).PostRaw(v.t, body)
	require.Equal(v.t, http.StatusOK, rec.Code)
	var out struct{ Status string }
	require.NoError(v.t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(v.t, "skipped", out.Status, "%s", body)
	v.bm.WaitIdle()
}

func TestTheBotsOwnPinIsNotAStrangersMessage(t *testing.T) {
	v := newV2(t)
	home := v.home()
	pins := v.fake.Calls("pinChatMessage")
	require.NotEmpty(t, pins)
	require.Equal(t, home, pins[len(pins)-1].Int64("message_id"))
	bot := v.botID()
	toOwner := len(v.messagesTo(ownerTG))
	inbox := v.count(`SELECT count(*) FROM telegram_update_inbox`)

	self := fmt.Sprintf(`{"id":%d,"is_bot":true,"first_name":"Мои книги","username":"my_books_bot"}`, bot)
	pinned := fmt.Sprintf(`"pinned_message":{"message_id":%d,"from":%s,`+
		`"chat":{"id":%d,"first_name":"Reader","type":"private"},"date":%d,"text":"Бот подключён."}`,
		home, self, ownerTG, time.Now().Unix())
	// What prod got: the pin's service message, from the bot.
	v.postSkipped(v.serviceUpdate(ownerTG, self, pinned))
	// The bot's own id is enough, should is_bot ever be missing, and on any
	// message, not only a service one.
	v.postSkipped(v.serviceUpdate(ownerTG, fmt.Sprintf(`{"id":%d,"first_name":"Мои книги"}`, bot),
		`"text":"Бот подключён."`))
	// Another bot's text is no stranger's either.
	const otherBot = int64(7100)
	v.postSkipped(v.serviceUpdate(otherBot, fmt.Sprintf(`{"id":%d,"is_bot":true,"first_name":"Other"}`, otherBot),
		`"text":"Основание"`))
	// The owner's own service message is not something they wrote: no
	// «send text» notice.
	v.postSkipped(v.serviceUpdate(ownerTG, fmt.Sprintf(`{"id":%d,"is_bot":false,"first_name":"Reader"}`, ownerTG),
		`"chat_background_set":{"type":{"type":"chat_theme","theme_name":"🌷"}}`))

	v.settle(ownerTG, toOwner)
	v.settle(otherBot, 0)
	assert.Equal(t, inbox, v.count(`SELECT count(*) FROM telegram_update_inbox`), "nothing recorded")
	for _, key := range v.mr.Keys() {
		assert.False(t, strings.HasPrefix(key, "tg2:stranger:"), "no stranger answered: %s", key)
	}

	// The owner goes on as before.
	v.say("Мир")
	assert.Len(t, v.messagesTo(ownerTG), toOwner+1, "the search's results panel")
}
