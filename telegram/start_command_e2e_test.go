package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgtest"

	"github.com/google/uuid"
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

// pinLine is the service update of a pin of messageID in the owner's chat,
// sent by sender (raw JSON); it returns the update and the service message's
// id.
func (v *v2Stack) pinLine(sender string, messageID int64) (body []byte, lineID int64) {
	pinned := fmt.Sprintf(`"pinned_message":{"message_id":%d,"from":%s,`+
		`"chat":{"id":%d,"first_name":"Reader","type":"private"},"date":%d,"text":"Напиши название книги"}`,
		messageID, sender, ownerTG, time.Now().Unix())
	body = v.serviceUpdate(ownerTG, sender, pinned)
	var u struct {
		Message struct {
			MessageID int64 `json:"message_id"`
		} `json:"message"`
	}
	require.NoError(v.t, json.Unmarshal(body, &u))
	return body, u.Message.MessageID
}

// (f) The line «… закрепил сообщение» of the bot's own pin of its home panel
// is deleted; no other message is: not a pin of another message, not the
// reader's own pin, not a pin while the bot is in maintenance.
func TestThePinLineOfTheHomePanelIsDeleted(t *testing.T) {
	v := newV2(t)
	v.say("мир")
	results := v.lastSend()
	v.say("/start")
	home := v.home()
	self := fmt.Sprintf(`{"id":%d,"is_bot":true,"first_name":"Мои книги","username":"my_books_bot"}`, v.botID())
	inbox := v.count(`SELECT count(*) FROM telegram_update_inbox`)

	body, line := v.pinLine(self, home)
	v.postSkipped(body)
	require.Len(t, v.callsFor("deleteMessage", line), 1, "the home's pin line goes")

	body, other := v.pinLine(self, results.MessageID)
	v.postSkipped(body)
	assert.Empty(t, v.callsFor("deleteMessage", other), "a pin of another message stays")

	reader := fmt.Sprintf(`{"id":%d,"is_bot":false,"first_name":"Reader"}`, ownerTG)
	body, own := v.pinLine(reader, home)
	rec := v.hook(botToken1).PostRaw(t, body)
	require.Equal(t, http.StatusOK, rec.Code)
	v.bm.WaitIdle()
	assert.Empty(t, v.callsFor("deleteMessage", own), "the reader's own pin stays")

	_, err := v.db.Exec(`UPDATE telegram_bot SET router = 'maintenance'`)
	require.NoError(t, err)
	body, paused := v.pinLine(self, home)
	v.postSkipped(body)
	assert.Empty(t, v.callsFor("deleteMessage", paused), "not a v2 bot's: left alone")

	assert.Equal(t, inbox, v.count(`SELECT count(*) FROM telegram_update_inbox`), "no pin line is recorded")
}

// (g) The startup's command setup and a chat's first update do not both set
// the menu: the one that claims it does, while it is still on its way to
// Telegram.
func TestTheCommandMenuIsSetOnceWhileStartupSetsIt(t *testing.T) {
	s := newStack(t)
	user := s.seedUser("old-reader")
	owner := ownerTG
	s.seedLegacyBot(user, botToken1, uuid.NewString(), &owner)
	gate := make(chan struct{})
	s.fake.On(tgtest.Rule{Method: "setMyCommands", Times: 1, Reply: s.fake.Gate(gate)})
	s.start()
	v := &v2Stack{stack: s, user: user}
	require.Eventually(t, func() bool { return len(v.fake.Calls("setMyCommands")) == 1 },
		5*time.Second, 10*time.Millisecond, "the startup sets the menu")

	// Not v.say: idle waits for the startup, held at the gate.
	rec := v.hook(botToken1).Post(t, v.u.Text(ownerTG, "мир"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Eventually(t, func() bool {
		for _, c := range v.calls() {
			if strings.Contains(content(&c), "Поиск: «мир»") {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the update is handled")
	assert.Len(t, v.fake.Calls("setMyCommands"), 1, "the update finds the menu claimed")
	close(gate)
	v.bm.WaitIdle()
	assertOneCommand(t, v.fake, ownerTG)
}

// (h) The race the review found (B2): the startup and a chat's first update
// both read the menu as not set before either claims it. The claim lets one
// of them through.
func TestTwoSetupsThatBothReadTheMenuUnsetSetItOnce(t *testing.T) {
	var reads atomic.Int32
	var met atomic.Bool // the second read came while the first waited
	both := make(chan struct{})
	s := newStack(t)
	// Installed before the service starts: go-pg's hooks are not to be added
	// while queries run.
	s.db.AddQueryHook(queryHook(func(_ context.Context, q string) error {
		if !strings.Contains(q, "SELECT commands_set_at") {
			return nil
		}
		switch reads.Add(1) {
		case 1:
			select { // the first read waits for the second
			case <-both:
				met.Store(true)
			case <-time.After(3 * time.Second):
			}
		case 2:
			close(both)
		}
		return nil
	}))
	user := s.seedUser("old-reader")
	owner := ownerTG
	s.seedLegacyBot(user, botToken1, uuid.NewString(), &owner)
	s.start()
	v := &v2Stack{stack: s, user: user}
	v.say("мир")
	v.bm.WaitIdle()
	require.True(t, met.Load(), "both read the menu unset before either claimed it")
	assertOneCommand(t, v.fake, ownerTG)
}

// (i) A setup Telegram refused lets its claim go: the next update sets the
// menu, and the one after finds it set.
func TestARefusedCommandSetupIsRetried(t *testing.T) {
	v := newV2(t, func(s *stack) {
		s.fake.On(tgtest.Rule{Method: "setMyCommands", Times: 1, Reply: tgtest.Error(http.StatusBadRequest, "Bad Request: refused")})
	})
	v.bm.WaitIdle()
	refused := len(v.fake.Calls("setMyCommands"))
	require.Positive(t, refused)
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_bot_ui WHERE commands_set_at IS NOT NULL`), "the claim let go")

	v.say("мир")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_bot_ui WHERE commands_set_at IS NOT NULL`))
	retried := len(v.fake.Calls("setMyCommands"))
	assert.Equal(t, refused+2, retried, "set whole on the next update")
	v.say("война")
	assert.Len(t, v.fake.Calls("setMyCommands"), retried, "then left alone")
}
