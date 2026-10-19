package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"gopds-api/internal/scanfixture"
	"gopds-api/telegram/delivery"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice S6 (plan.md §1 S6, plan-ui.md §5.4): every bot on the v2 router,
// maintenance as the way back, the old interface's keyboard taken away once,
// and the legacy router and the bot's LLM path gone.

const (
	copyKeyboardGone = "Клавиатура внизу больше не нужна — меню теперь в сообщениях."
	copyMaintenance  = "Поиск в боте временно недоступен. Книги можно найти на сайте."
)

// adoptedReader is a reader whose bot was connected the old way: adopted at
// start, served by v2, its binding never told the keyboard is gone.
func adoptedReader(t *testing.T) *v2Stack {
	t.Helper()
	s := newStack(t)
	user := s.seedUser("old-reader")
	owner := ownerTG
	s.seedLegacyBot(user, botToken1, uuid.NewString(), &owner)
	s.start()
	return &v2Stack{stack: s, user: user}
}

// A start switches every bot still on the legacy router to v2; another
// start finds nothing more to switch.
func TestAStartSwitchesEveryBotToV2(t *testing.T) {
	v := adoptedReader(t)
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_bot WHERE router = 'v2'`), "an adopted bot is v2")

	_, err := v.db.Exec(`UPDATE telegram_bot SET router = 'legacy'`)
	require.NoError(t, err)
	v.secondPod()
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_bot WHERE router = 'v2'`), "the next start switches it")
	v.secondPod()
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_bot WHERE router <> 'v2'`))
}

// The first thing an old chat gets from the v2 bot is the one message that
// takes the old keyboard away; then the update is handled as usual, and the
// message never comes again. The menu is cleared on the same first update.
func TestTheOldKeyboardGoesOnTheFirstUpdate(t *testing.T) {
	v := adoptedReader(t)
	v.say("мир")
	sends := v.sends()
	require.GreaterOrEqual(t, len(sends), 2)
	notice := sends[0]
	assert.Equal(t, copyKeyboardGone, notice.Params["text"])
	var markup struct {
		RemoveKeyboard bool `json:"remove_keyboard"`
	}
	require.NoError(t, json.Unmarshal([]byte(notice.Params["reply_markup"]), &markup))
	assert.True(t, markup.RemoveKeyboard)
	results := v.lastSend()
	assert.Contains(t, content(&results), "Поиск: «мир»", "the update is handled as usual")
	assert.Len(t, v.fake.Calls("deleteMyCommands"), 9, "every scope and language cleared")
	menus := len(v.fake.Calls("setChatMenuButton"))
	assert.Positive(t, menus, "the menu button reset")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_binding WHERE reply_kb_removed_at IS NOT NULL`))

	v.say("/start")
	v.say("война")
	count := 0
	for _, c := range v.fake.Calls("sendMessage") {
		if c.Params["text"] == copyKeyboardGone {
			count++
		}
	}
	assert.Equal(t, 1, count, "once per binding")
	assert.Len(t, v.fake.Calls("deleteMyCommands"), 9, "once per bot")
	assert.Len(t, v.fake.Calls("setChatMenuButton"), menus)
}

// A chat linked since S6 never had the old keyboard: no such message.
func TestANewLinkGetsNoKeyboardMessage(t *testing.T) {
	v := newV2(t)
	v.say("мир")
	for _, c := range v.fake.Calls("sendMessage") {
		assert.NotEqual(t, copyKeyboardGone, c.Params["text"])
	}
}

// The keyboard belongs to the chat: a chat linked again before its first
// update to the v2 bot still has the old keyboard, and gets the one message;
// linked once more, it is the latest binding's mark that counts.
func TestARelinkedChatStillLosesTheOldKeyboard(t *testing.T) {
	v := adoptedReader(t)
	secret, _ := v.connect(v.user, botToken1)
	v.post(v.u.Text(ownerTG, "/start link_"+secret))
	v.say("мир")
	assert.Equal(t, 1, keyboardNotices(v))
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active' AND reply_kb_removed_at IS NOT NULL`))

	secret, _ = v.connect(v.user, botToken1)
	v.post(v.u.Text(ownerTG, "/start link_"+secret))
	v.say("война")
	assert.Equal(t, 1, keyboardNotices(v), "the adopted binding's unset mark is history")
}

// A chat that got the message keeps it got across a relink: never twice.
func TestARelinkAfterTheMessageSendsItNoMore(t *testing.T) {
	v := adoptedReader(t)
	v.say("мир")
	secret, _ := v.connect(v.user, botToken1)
	v.post(v.u.Text(ownerTG, "/start link_"+secret))
	v.say("война")
	assert.Equal(t, 1, keyboardNotices(v))
}

func keyboardNotices(v *v2Stack) int {
	count := 0
	for _, c := range v.fake.Calls("sendMessage") {
		if c.Params["text"] == copyKeyboardGone {
			count++
		}
	}
	return count
}

// A bot in maintenance answers with the notice and the site's address; it
// searches nothing, and its binding and jobs keep their state.
func TestMaintenanceAnswersWithTheSite(t *testing.T) {
	var releasePrepare func()
	v := newV2(t, func(s *stack) { releasePrepare = gatedPrepare(s) })
	defer releasePrepare()
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	_, err := v.db.Exec(`UPDATE telegram_bot SET router = 'maintenance'`)
	require.NoError(t, err)

	sends := len(v.sends())
	v.say("Основание")
	require.Len(t, v.sends(), sends+1)
	notice := v.lastSend()
	assert.Equal(t, copyMaintenance, notice.Params["text"])
	var markup struct {
		InlineKeyboard [][]struct {
			URL string `json:"url"`
		} `json:"inline_keyboard"`
	}
	require.NoError(t, json.Unmarshal([]byte(notice.Params["reply_markup"]), &markup))
	assert.Equal(t, stackSiteURL+"/", markup.InlineKeyboard[0][0].URL)

	edits := len(v.edits(card.MessageID))
	v.tap(&card, "☆ В избранное")
	assert.Equal(t, copyMaintenance, v.lastAnswer(), "a press gets the notice")
	assert.Len(t, v.edits(card.MessageID), edits, "and changes nothing")

	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active'`), "the binding stays")
	releasePrepare()
	v.waitJobSent()
	assert.Len(t, v.fake.Calls("sendDocument"), 1, "a job already taken still delivers")

	_, err = v.db.Exec(`UPDATE telegram_bot SET router = 'v2'`)
	require.NoError(t, err)
	v.say("мир")
	back := v.lastSend()
	assert.Contains(t, content(&back), "Поиск: «мир»", "back from maintenance, the bot searches again")
}

// An old card's file button still makes a delivery job.
func TestAnOldFileButtonStillDelivers(t *testing.T) {
	var releasePrepare func()
	v := newV2(t, func(s *stack) { releasePrepare = gatedPrepare(s) })
	releasePrepare()
	home := v.homePanel()
	bookID := v.books[scanfixture.EntryMultiAuthor]
	v.post(v.u.Callback(ownerTG, &home, "download:epub:"+strconv.FormatInt(bookID, 10)))
	preparing := fmt.Sprintf(delivery.ToastPreparing, "EPUB")
	assert.Equal(t, preparing, v.lastAnswer())
	v.waitJobSent()
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_delivery WHERE book_id = ? AND format = 'epub'`, bookID))
	v.post(v.u.Callback(ownerTG, &home, "download:epub:"+strconv.FormatInt(bookID, 10)))
	assert.NotEqual(t, preparing, v.lastAnswer(), "a second press is not a second file")
	assert.Len(t, v.fake.Calls("sendDocument"), 1)
}

// The bot reaches neither the LLM nor the old command processor: the
// structural guard of plan.md S6. The command processor is gone from the
// bot's dependencies altogether. gopds-api/llm still is one, but only
// through database and services, for the catalog's scan, genre and author
// work; no package of the bot imports it.
func TestTheBotHasNoLLMPath(t *testing.T) {
	out, err := exec.CommandContext(context.Background(), "go", "list", "-deps", "gopds-api/telegram/...").CombinedOutput()
	require.NoError(t, err, "%s", out)
	deps := strings.Fields(string(out))
	require.Contains(t, deps, "gopds-api/telegram/router", "the listing is the bot's")
	assert.NotContains(t, deps, "gopds-api/commands")

	format := "{{.ImportPath}}: {{join .Imports \" \"}}"
	out, err = exec.CommandContext(context.Background(), "go", "list", "-f", format, "gopds-api/telegram/...").CombinedOutput()
	require.NoError(t, err, "%s", out)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg, imports, _ := strings.Cut(line, ": ")
		for _, imp := range strings.Fields(imports) {
			assert.NotEqual(t, "gopds-api/llm", imp, "%s imports the LLM", pkg)
			assert.NotEqual(t, "gopds-api/commands", imp, "%s imports the command processor", pkg)
		}
	}
}
