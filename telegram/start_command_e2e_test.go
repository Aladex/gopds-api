package telegram_test

import (
	"net/http"
	"testing"

	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot's one command (brief-author-start-command.md): /start sends a new
// home panel to the bottom of the chat and takes the old one away.

// homes counts the chat's home panels.
func (v *v2Stack) homes() int { return v.count(`SELECT count(*) FROM telegram_panel WHERE is_home`) }

// callsFor are a method's calls about one message.
func (v *v2Stack) callsFor(method string, messageID int64) []tgtest.Call {
	var out []tgtest.Call
	for _, c := range v.fake.Calls(method) {
		if c.Int64("message_id") == messageID {
			out = append(out, c)
		}
	}
	return out
}

// refuseDeletes makes Telegram refuse to delete a message, as it does one
// older than 48 hours.
func (v *v2Stack) refuseDeletes() {
	v.fake.On(tgtest.Rule{Method: "deleteMessage", Reply: func(w http.ResponseWriter, _ *http.Request, _ *tgtest.Call) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message can't be deleted for everyone"}`))
	}})
}

// (a) After a few searches /start brings a new home panel to the bottom of
// the chat, pinned; the old one is unpinned and deleted. One home, however
// many /start.
func TestStartBringsANewHomeToTheBottom(t *testing.T) {
	v := newV2(t)
	old := v.home()
	v.say("мир")
	v.say("война")
	v.say("/start")

	fresh := v.home()
	require.NotEqual(t, old, fresh)
	last := v.lastSend()
	assert.Equal(t, fresh, last.MessageID, "the newest message in the chat")
	assert.Equal(t, "sendMessage", last.Method, "plain, like every home panel")
	assert.Contains(t, last.Params["text"], screens.CopyHomeChoose)
	assert.Contains(t, tgtest.ButtonTexts(t, &last), []string{screens.ButtonFavorites, screens.ButtonRecent})
	pins := v.fake.Calls("pinChatMessage")
	assert.Equal(t, fresh, pins[len(pins)-1].Int64("message_id"))
	assert.Equal(t, "true", pins[len(pins)-1].Params["disable_notification"], "pinned silently")
	assert.Len(t, v.callsFor("unpinChatMessage", old), 1)
	assert.Len(t, v.callsFor("deleteMessage", old), 1)
	assert.Equal(t, 1, v.homes())

	v.say("/start")
	again := v.home()
	assert.NotEqual(t, fresh, again)
	assert.Len(t, v.callsFor("deleteMessage", fresh), 1)
	assert.Equal(t, 1, v.homes(), "no pile-up of home panels")
	assert.Empty(t, v.callsFor("deleteMessage", again))
}

// (b) A home Telegram will not delete loses its keyboard, and a button
// still pressed on it is refused as an old screen's.
func TestAnOldHomeThatStaysIsRefused(t *testing.T) {
	v := newV2(t)
	v.refuseDeletes()
	first := v.sends()[0]
	old := first.MessageID
	v.say("/start")

	strip := v.callsFor("editMessageReplyMarkup", old)
	require.Len(t, strip, 1, "its buttons taken away")
	assert.JSONEq(t, `{"inline_keyboard":[]}`, strip[0].Params["reply_markup"])
	assert.Len(t, v.callsFor("unpinChatMessage", old), 1)

	edits := len(v.calls())
	v.tap(&first, screens.ButtonFavorites)
	assert.Len(t, v.calls(), edits, "the old home does not move")
	assert.Equal(t, screens.CopyScreenChanged, v.lastAnswer())
}

// (c) An old home showing a rich list keeps the list's links in its text
// when it cannot be deleted; they are refused too.
func TestAnOldHomesRichLinksAreRefused(t *testing.T) {
	v := newV2(t)
	home := v.favoriteFixture()
	v.tap(&home, screens.ButtonFavorites)
	favs := v.latest(home.MessageID)
	require.NotEmpty(t, links(t, &favs))
	v.refuseDeletes()
	v.say("/start")
	require.NotEqual(t, home.MessageID, v.home())

	edits := len(v.calls())
	v.tap(&favs, links(t, &favs)[0])
	assert.Len(t, v.calls(), edits)
	assert.Equal(t, screens.CopyScreenChanged, v.lastAnswer())
}

// (d) A bot adopted from the old interface gets its one command when the
// service starts, before anyone writes to it.
func TestAnAdoptedBotGetsItsCommandAtStart(t *testing.T) {
	v := adoptedReader(t)
	v.bm.WaitIdle()
	assertOneCommand(t, v.fake, ownerTG)
}

// (e) A managed bot gets its one command when its creator links it; the
// manager gets none.
func TestAManagedBotGetsItsCommand(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("managed-reader")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	s.link(botToken1, creatorTG, s.newLink(user))
	s.bm.WaitIdle()
	assertOneCommand(t, s.fake, creatorTG)
	for _, c := range s.fake.Calls("setMyCommands") {
		assert.Equal(t, botToken1, c.Token, "the manager has no commands")
	}
}
