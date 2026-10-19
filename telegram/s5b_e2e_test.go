package telegram_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/telegram/tgtest"

	tgbot "github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice S5b (plan-ui.md §3, §5.3): inline search for the owner, and the pick
// that becomes a card.

// inlineAnswer is an answerInlineQuery as the fake recorded it.
type inlineAnswer struct {
	call    tgtest.Call
	Results []struct {
		Type                string `json:"type"`
		ID                  string `json:"id"`
		Title               string `json:"title"`
		Description         string `json:"description"`
		ThumbnailURL        string `json:"thumbnail_url"`
		InputMessageContent struct {
			MessageText string `json:"message_text"`
			Entities    []struct {
				Type   string `json:"type"`
				Offset int    `json:"offset"`
				Length int    `json:"length"`
				URL    string `json:"url"`
			} `json:"entities"`
		} `json:"input_message_content"`
		ReplyMarkup json.RawMessage `json:"reply_markup"`
	}
}

// ask types an inline query and returns its answer.
func (v *v2Stack) ask(from int64, chatType, query, offset string) inlineAnswer {
	v.t.Helper()
	update := v.u.Text(from, "")
	id := "iq-" + strconv.FormatInt(update.ID, 10)
	update.Message = nil
	update.InlineQuery = &tgbot.InlineQuery{ID: id, From: tgtest.User(from), Query: query, Offset: offset, ChatType: chatType}
	v.post(update)
	var found *tgtest.Call
	require.Eventually(v.t, func() bool {
		for _, c := range v.fake.Calls("answerInlineQuery") {
			if c.Params["inline_query_id"] == id {
				c := c
				found = &c
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "no answer to the inline query")
	answer := inlineAnswer{call: *found}
	require.NoError(v.t, json.Unmarshal([]byte(found.Params["results"]), &answer.Results), found.Params["results"])
	return answer
}

// sender is the chat type of a query typed in the chat with the bot.
const sender = "sender"

// The guard: the owner in the bot's chat gets books; another user, the owner
// in a group, or a blocked binding get nothing; every answer is personal and
// cached 5 s. A query arriving proves inline mode on.
func TestTheInlineGuard(t *testing.T) {
	v := newV2(t)
	owner := v.ask(ownerTG, sender, "мир", "")
	require.NotEmpty(t, owner.Results, "the owner gets books")
	assert.Equal(t, "b"+strconv.FormatInt(v.books[scanfixture.EntryMultiAuthor], 10), owner.Results[0].ID)
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_bot_ui WHERE supports_inline`), "a query proves inline mode")

	stranger := v.ask(ownerTG+1, sender, "мир", "")
	assert.Empty(t, stranger.Results, "another user")
	group := v.ask(ownerTG, "group", "мир", "")
	assert.Empty(t, group.Results, "the owner in a group")
	_, err := v.db.Exec(`UPDATE telegram_binding SET blocked_at = now() WHERE status = 'active'`)
	require.NoError(t, err)
	blocked := v.ask(ownerTG, sender, "мир", "")
	assert.Empty(t, blocked.Results, "a blocked binding")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_binding WHERE blocked_at IS NOT NULL`),
		"an inline query does not unblock the bot")

	for _, a := range []inlineAnswer{owner, stranger, group, blocked} {
		assert.Equal(t, "true", a.call.Params["is_personal"])
		assert.Equal(t, "5", a.call.Params["cache_time"])
	}
}

// A result is the book, and a link to its page over its title; nothing else.
func TestAnInlineResultIsALinkToTheBook(t *testing.T) {
	v := newV2(t)
	a := v.ask(ownerTG, sender, "мир", "")
	require.NotEmpty(t, a.Results)
	r := a.Results[0]
	bookID := v.books[scanfixture.EntryMultiAuthor]
	assert.Equal(t, "article", r.Type)
	assert.Contains(t, r.Title, "война и МИР")
	assert.True(t, strings.HasPrefix(r.InputMessageContent.MessageText, "📖 война и МИР"), r.InputMessageContent.MessageText)
	require.Len(t, r.InputMessageContent.Entities, 1)
	assert.Equal(t, "text_link", r.InputMessageContent.Entities[0].Type)
	assert.Equal(t, stackSiteURL+"/books/page/1?book_id="+strconv.FormatInt(bookID, 10), r.InputMessageContent.Entities[0].URL)
	assert.Empty(t, r.ReplyMarkup, "no buttons: a pick is the reader's own message")
	assert.Empty(t, r.ThumbnailURL, "no media host configured")
}

// 45 hits: pages at 0, 20 and 40, then none.
func TestInlinePaging(t *testing.T) {
	v := newV2(t)
	for i := 0; i < 45; i++ {
		v.seedBook("Пагинация "+strconv.Itoa(i), "ru", true)
	}
	first := v.ask(ownerTG, sender, "Пагинация", "")
	assert.Len(t, first.Results, 20)
	assert.Equal(t, "20", first.call.Params["next_offset"])
	second := v.ask(ownerTG, sender, "Пагинация", "20")
	assert.Len(t, second.Results, 20)
	assert.Equal(t, "40", second.call.Params["next_offset"])
	last := v.ask(ownerTG, sender, "Пагинация", "40")
	assert.Len(t, last.Results, 5)
	assert.Empty(t, last.call.Params["next_offset"])
	beyond := v.ask(ownerTG, sender, "Пагинация", "200")
	assert.Empty(t, beyond.Results)
}

// An empty query lists the favorites, newest first; none, nothing.
func TestTheEmptyInlineQueryIsTheFavorites(t *testing.T) {
	v := newV2(t)
	none := v.ask(ownerTG, sender, "", "")
	assert.Empty(t, none.Results)
	older := v.seedBook("Старое избранное", "ru", true)
	newer := v.seedBook("Новое избранное", "ru", true)
	for _, id := range []int64{older, newer} {
		_, err := v.db.Exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?)`, v.user, id)
		require.NoError(t, err)
	}
	favs := v.ask(ownerTG, sender, "", "")
	require.Len(t, favs.Results, 2)
	assert.Equal(t, "b"+strconv.FormatInt(newer, 10), favs.Results[0].ID)
	assert.Equal(t, "b"+strconv.FormatInt(older, 10), favs.Results[1].ID)
}

// Nothing private in any answer: no API path, token, signature, file id or
// callback.
func TestInlineAnswersCarryNoSecrets(t *testing.T) {
	v := newV2(t)
	v.giveCover(v.books[scanfixture.EntryMultiAuthor])
	_, card := v.openFixtureCard()
	v.tap(&card, "☆ В избранное")
	v.ask(ownerTG, sender, "мир", "")
	v.ask(ownerTG, sender, "", "")
	var fileIDs []string
	_, err := v.db.Query(&fileIDs, `SELECT file_id FROM telegram_cover_file`)
	require.NoError(t, err)
	for _, c := range v.fake.Calls("answerInlineQuery") {
		body := strings.ToLower(c.Params["results"])
		for _, secret := range []string{"/api/", "token", "sig=", "callback_data", strings.ToLower(botToken1), "photo-"} {
			assert.NotContains(t, body, secret)
		}
		for _, id := range fileIDs {
			assert.NotContains(t, c.Params["results"], id)
		}
	}
}

// botID is the bot's Telegram id.
func (v *v2Stack) botID() int64 {
	v.t.Helper()
	return int64(v.count(`SELECT tg_bot_id FROM telegram_bot`))
}

// pick sends a message as Telegram does for a chosen inline result: the
// owner's text, via bot, with a link over the title.
func (v *v2Stack) pick(via int64, link string) *tgbot.Update {
	update := v.u.Text(ownerTG, "📖 война и МИР — Автор")
	update.Message.ViaBot = &tgbot.User{ID: via, IsBot: true, FirstName: "Test Bot"}
	update.Message.Entities = []tgbot.MessageEntity{{Type: tgbot.MessageEntityTypeTextLink, Offset: 3, Length: 11, URL: link}}
	v.post(update)
	return update
}

// A pick of this bot's result becomes a card in a new panel, and the link
// message goes.
func TestAPickedBookBecomesACard(t *testing.T) {
	v := newV2(t)
	bookID := v.books[scanfixture.EntryMultiAuthor]
	sends := len(v.sends())
	update := v.pick(v.botID(), stackSiteURL+"/books/page/1?book_id="+strconv.FormatInt(bookID, 10))
	require.Len(t, v.sends(), sends+1, "a new card panel")
	card := v.lastSend()
	assert.Contains(t, content(&card), "война и МИР")
	assert.True(t, hasButton(t, &card, "EPUB"))
	deletes := v.fake.Calls("deleteMessage")
	require.Len(t, deletes, 1)
	assert.Equal(t, strconv.Itoa(update.Message.ID), deletes[0].Params["message_id"])
}

// Another bot's message, a link that is not this site's book page, or a book
// no longer there: no card; the last says the book is gone.
func TestAPickThatIsNotOursMakesNoCard(t *testing.T) {
	v := newV2(t)
	bookID := v.books[scanfixture.EntryMultiAuthor]
	link := stackSiteURL + "/books/page/1?book_id=" + strconv.FormatInt(bookID, 10)
	sends, messages := len(v.sends()), len(v.messagesTo(ownerTG))

	v.pick(v.botID()+1, link)
	v.pick(v.botID(), "https://elsewhere.example/books/page/1?book_id="+strconv.FormatInt(bookID, 10))
	v.pick(v.botID(), link+"&x=1")
	assert.Len(t, v.sends(), sends, "no card")
	assert.Len(t, v.messagesTo(ownerTG), messages, "and no search of the text")

	_, err := v.db.Exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, bookID)
	require.NoError(t, err)
	v.pick(v.botID(), link)
	assert.Contains(t, v.messagesTo(ownerTG), "Эта книга больше недоступна.")
	assert.Empty(t, v.fake.Calls("deleteMessage"))
}

// supportsInline makes the fake's getMe say inline mode is on or off.
func supportsInline(s *stack, on *bool) {
	s.fake.On(tgtest.Rule{Method: "getMe", Reply: func(w http.ResponseWriter, r *http.Request, c *tgtest.Call) {
		me := tgtest.BotUser(c.Token)
		me["supports_inline_queries"] = *on
		tgtest.OK(me)(w, r, c)
	}})
}

// «Проверить»: off, a toast; on, the enabled copy and the home offers inline
// search; turned off and read at a start, the home drops it.
func TestCheckingInlineModeFlipsTheHome(t *testing.T) {
	on := false
	v := newV2(t, func(s *stack) { supportsInline(s, &on) })
	home := v.homePanel()
	assert.False(t, hasButton(t, &home, "🔍 Найти с подсказками"))
	v.tap(&home, "Настройки")
	settings := v.homePanel()
	assert.Contains(t, content(&settings), "Подсказки при поиске: выключены")
	v.tap(&settings, "Подсказки при поиске")
	instructions := v.homePanel()
	v.tap(&instructions, "Проверить")
	assert.Equal(t, "Пока не включено. Проверь шаги выше.", v.lastAnswer())

	on = true
	v.tap(&instructions, "Проверить")
	enabled := v.homePanel()
	assert.Contains(t, content(&enabled), "Подсказки включены. Кнопка «Найти с подсказками» появилась на главной.")
	v.tap(&enabled, "Домой")
	home = v.homePanel()
	require.True(t, hasButton(t, &home, "🔍 Найти с подсказками"), "%v", tgtest.ButtonTexts(t, &home))
	var markup struct {
		InlineKeyboard [][]struct {
			Text                         string  `json:"text"`
			SwitchInlineQueryCurrentChat *string `json:"switch_inline_query_current_chat"`
		} `json:"inline_keyboard"`
	}
	require.NoError(t, json.Unmarshal([]byte(home.Params["reply_markup"]), &markup))
	require.NotNil(t, markup.InlineKeyboard[0][0].SwitchInlineQueryCurrentChat)
	assert.Empty(t, *markup.InlineKeyboard[0][0].SwitchInlineQueryCurrentChat)

	on = false
	v.tap(&home, "Настройки")
	settings = v.homePanel()
	assert.Contains(t, content(&settings), "Подсказки при поиске: включены")
	v.tap(&settings, "Подсказки при поиске")
	instructions = v.homePanel()
	assert.Contains(t, content(&instructions), "Подсказки при поиске включены")

	// Inline mode turned off in BotFather: the next start reads it.
	v.secondPod()
	require.Eventually(t, func() bool { return v.count(`SELECT count(*) FROM telegram_bot_ui WHERE supports_inline`) == 0 },
		5*time.Second, 20*time.Millisecond, "a start reads getMe's flag")
	v.tap(&instructions, "← К настройкам")
	settings = v.homePanel()
	v.tap(&settings, "Домой")
	home = v.homePanel()
	assert.False(t, hasButton(t, &home, "🔍 Найти с подсказками"), "off again, the row goes")
}
