package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/tgtest"

	"github.com/go-pg/pg/v10"
	tgbot "github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice S5 (plan-ui.md §5.2): the lists, the filters, the settings and the
// old interface's buttons.

// seedAuthor adds an author with one approved Russian book per title, and
// returns the books.
func (v *v2Stack) seedAuthor(name string, titles ...string) (books []int64) {
	v.t.Helper()
	author := &models.Author{FullName: name}
	_, err := v.db.Model(author).Insert()
	require.NoError(v.t, err)
	for _, title := range titles {
		books = append(books, v.seedBook(title, "ru", true, author.ID))
	}
	return books
}

// seedBook adds a book in lang by the authors given.
func (v *v2Stack) seedBook(title, lang string, approved bool, authors ...int64) int64 {
	v.t.Helper()
	key := strconv.FormatInt(time.Now().UnixNano(), 36)
	book := &models.Book{Path: "s5.zip", Format: "fb2", FileName: key + ".fb2", Title: title, Lang: lang,
		Approved: approved, RegisterDate: time.Now(), MD5: key}
	_, err := v.db.Model(book).Insert()
	require.NoError(v.t, err)
	for _, a := range authors {
		_, err = v.db.Model(&models.OrderToAuthor{AuthorID: a, BookID: book.ID}).Insert()
		require.NoError(v.t, err)
	}
	return book.ID
}

// reply answers message on with text, as Telegram's reply does.
func (v *v2Stack) reply(on *tgtest.Call, text string) {
	update := v.u.Text(ownerTG, text)
	update.Message.ReplyToMessage = &tgbot.Message{ID: int(on.MessageID),
		Chat: tgbot.Chat{ID: ownerTG, Type: tgbot.ChatTypePrivate}, Text: on.Params["text"]}
	v.post(update)
}

// homePanel is the home panel's latest content.
func (v *v2Stack) homePanel() tgtest.Call { return v.latest(v.home()) }

// hasButton reports a button labeled text.
func hasButton(t *testing.T, c *tgtest.Call, text string) bool {
	return slices.Contains(tgtest.Labels(t, c), text)
}

// buttonStartingWith is the first button whose label starts with prefix.
func buttonStartingWith(t *testing.T, c *tgtest.Call, prefix string) string {
	t.Helper()
	for _, b := range tgtest.Labels(t, c) {
		if strings.HasPrefix(b, prefix) {
			return b
		}
	}
	t.Fatalf("no button starting with %q in %v", prefix, tgtest.Labels(t, c))
	return ""
}

// askAuthor opens the author filter of results and returns the prompt.
func (v *v2Stack) askAuthor(results *tgtest.Call) tgtest.Call {
	v.t.Helper()
	sends := len(v.sends())
	v.tap(results, "Автор")
	require.Len(v.t, v.sends(), sends+1, "the prompt is a message of its own")
	return v.lastSend()
}

// The author filter: the prompt opens the reply box and has «Отмена»; a
// reply gives a pick list; a pick edits the panel the prompt came from.
func TestAnAuthorPickEditsTheSourcePanel(t *testing.T) {
	v := newV2(t)
	v.seedAuthor("Айзек Азимов", "Основание", "Основание и Империя")
	v.seedAuthor("Другой Писатель", "Основание земли")
	v.say("Основание")
	results := v.lastSend()
	require.Contains(t, content(&results), "Основание земли")

	prompt := v.askAuthor(&results)
	assert.Equal(t, "sendMessage", prompt.Method)
	assert.Contains(t, prompt.Params["text"], "Автор для запроса «Основание»")
	var markup struct {
		ForceReply bool `json:"force_reply"`
	}
	require.NoError(t, json.Unmarshal([]byte(prompt.Params["reply_markup"]), &markup))
	assert.True(t, markup.ForceReply, "the reply box opens by itself")
	assert.Equal(t, [][]string{{"Отмена"}}, tgtest.ButtonTexts(t, &prompt))

	resultsEdits := len(v.edits(results.MessageID))
	v.reply(&prompt, "Азимов")
	picks := v.latest(prompt.MessageID)
	assert.Contains(t, picks.Params["text"], "Выбери автора")
	require.True(t, hasButton(t, &picks, "Айзек Азимов"), "%v", tgtest.ButtonTexts(t, &picks))
	assert.True(t, hasButton(t, &picks, "Ввести другое имя"))
	assert.Len(t, v.edits(results.MessageID), resultsEdits, "a reply alone changes no list")

	v.tap(&picks, "Айзек Азимов")
	filtered := v.latest(results.MessageID)
	require.Greater(t, len(v.edits(results.MessageID)), resultsEdits, "the source panel is the one edited")
	assert.Contains(t, content(&filtered), "Автор: Айзек Азимов")
	assert.True(t, hasButton(t, &filtered, "Автор: Азимов"), "%v", tgtest.ButtonTexts(t, &filtered))
	assert.True(t, hasButton(t, &filtered, "✕ Автор"))
	assert.Contains(t, content(&filtered), "Основание и Империя")
	assert.NotContains(t, content(&filtered), "Основание земли")
	assert.NotContains(t, content(&filtered), "Страница 2")
	done := v.latest(prompt.MessageID)
	assert.Equal(t, "Готово.", done.Params["text"])
	assert.Empty(t, done.Params["reply_markup"], "a closed prompt has no buttons")

	v.tap(&filtered, "← К результатам")
	unfiltered := v.latest(results.MessageID)
	assert.Contains(t, content(&unfiltered), "Основание земли", "«Назад» is the search without the author")
}

// Typed text that is not a reply is a new search; the prompt stays open.
func TestPlainTextWhileAPromptIsOpenIsANewSearch(t *testing.T) {
	v := newV2(t)
	v.addBooks("Основание")
	v.say("Основание")
	results := v.lastSend()
	prompt := v.askAuthor(&results)

	v.say("мир")
	search := v.lastSend()
	assert.NotEqual(t, prompt.MessageID, search.MessageID)
	assert.Contains(t, content(&search), "Поиск: «мир»")
	assert.Empty(t, v.edits(prompt.MessageID), "the prompt is untouched")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_prompt WHERE prompt_message_id = ? AND state = 'open'`, prompt.MessageID))
}

// «Отмена» closes the prompt; a later reply to it says it is closed.
func TestCancelClosesThePrompt(t *testing.T) {
	v := newV2(t)
	v.addBooks("Основание")
	v.say("Основание")
	results := v.lastSend()
	prompt := v.askAuthor(&results)

	v.tap(&prompt, "Отмена")
	closed := v.latest(prompt.MessageID)
	assert.Equal(t, "Вопрос закрыт. Новый текст будет обычным поиском.", closed.Params["text"])
	assert.Empty(t, closed.Params["reply_markup"])
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_prompt WHERE prompt_message_id = ? AND state = 'cancelled'`, prompt.MessageID))

	v.reply(&prompt, "Азимов")
	assert.Contains(t, v.messagesTo(ownerTG), "Этот вопрос уже закрыт. Открой фильтр ещё раз.")
}

// A language choice is page one of a new view; «Назад» is the old view's
// page, unchanged.
func TestAllLanguagesIsANewViewAndBackKeepsTheOldOne(t *testing.T) {
	v := newV2(t)
	v.addBooks("Основание")
	for i := 0; i < 3; i++ {
		v.seedBook("Основание english "+strconv.Itoa(i), "en", true)
	}
	home := v.homePanel()
	v.tap(&home, "Настройки")
	settings := v.homePanel()
	v.tap(&settings, "Язык поиска: все языки")
	languages := v.homePanel()
	v.tap(&languages, "Русский")
	settings = v.homePanel()
	assert.Contains(t, content(&settings), "Язык поиска: русский")

	v.say("Основание")
	results := v.lastSend()
	assert.Contains(t, content(&results), "Язык: русский")
	assert.NotContains(t, content(&results), "english")
	v.tap(&results, "→")
	page2 := v.latest(results.MessageID)
	require.Contains(t, content(&page2), "Страница 2 из 3")

	v.tap(&page2, "Язык: русский")
	picker := v.latest(results.MessageID)
	assert.Contains(t, content(&picker), "Язык книг в этом поиске")
	v.tap(&picker, "Все языки")
	all := v.latest(results.MessageID)
	assert.Contains(t, content(&all), "Страница 1 из 3", "page one of the new view")
	assert.NotContains(t, content(&all), "Язык: русский")
	assert.True(t, hasButton(t, &all, "Язык"))

	v.tap(&all, "← К результатам")
	back := v.latest(results.MessageID)
	assert.Contains(t, content(&back), "Страница 2 из 3")
	assert.Contains(t, content(&back), "Язык: русский", "the old view kept its language")
	assert.Equal(t, 2, v.count(`SELECT count(*) FROM telegram_view WHERE kind = 1 AND query = 'Основание'`))
}

// A favorite press is the state its button asked for: pressed again from a
// second panel painted before the first press, it does not remove.
func TestAReplayedFavoritePressDoesNotRemove(t *testing.T) {
	v := newV2(t)
	_, first := v.openFixtureCard()
	_, second := v.openFixtureCard()
	require.NotEqual(t, first.MessageID, second.MessageID)
	bookID := v.books[scanfixture.EntryMultiAuthor]

	v.tap(&first, "☆ В избранное")
	assert.Equal(t, "Добавлено в избранное", v.lastAnswer())
	v.tap(&second, "☆ В избранное")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM favorite_books WHERE book_id = ?`, bookID), "still a favorite")
	again := v.latest(second.MessageID)
	assert.True(t, hasButton(t, &again, "★ В избранном"))
}

// A failed favorite write says so, and the card is not painted again.
func TestAFavoriteWriteThatFailsChangesNothing(t *testing.T) {
	var armed atomic.Bool
	v := newV2(t, func(s *stack) {
		s.db.AddQueryHook(queryHook(func(_ context.Context, q string) error {
			if armed.Load() && strings.Contains(q, "favorite_books") && !strings.HasPrefix(strings.TrimSpace(q), "SELECT") {
				return errors.New("favorites are down")
			}
			return nil
		}))
	})
	_, card := v.openFixtureCard()
	edits := len(v.edits(card.MessageID))
	armed.Store(true)
	v.tap(&card, "☆ В избранное")
	armed.Store(false)
	assert.Equal(t, "Не удалось изменить избранное. Попробуй ещё раз.", v.lastAnswer())
	assert.Len(t, v.edits(card.MessageID), edits, "the button is unchanged")
	assert.Zero(t, v.count(`SELECT count(*) FROM favorite_books`))
}

// seedCollection publishes a curated collection of the books given, in order.
func (v *v2Stack) seedCollection(name string, books ...int64) int64 {
	v.t.Helper()
	var id int64
	_, err := v.db.QueryOne(pg.Scan(&id), `INSERT INTO book_collections (name, is_public, is_curated, created_at, updated_at)
		VALUES (?, true, true, now(), now()) RETURNING id`, name)
	require.NoError(v.t, err)
	for i, b := range books {
		_, err = v.db.Exec(`INSERT INTO book_collection_items (collection_id, book_id, external_title, match_status, position)
			VALUES (?, ?, 'x', 'manual', ?)`, id, b, i)
		require.NoError(v.t, err)
	}
	return id
}

// A collection lists only what the library hands out.
func TestACollectionHidesAnUnapprovedBook(t *testing.T) {
	v := newV2(t)
	shown := v.seedBook("Видимая книга", "ru", true)
	hidden := v.seedBook("Неодобренная книга", "ru", false)
	v.seedCollection("Лучшее", shown, hidden)

	home := v.homePanel()
	v.tap(&home, "Подборки")
	list := v.homePanel()
	assert.Contains(t, content(&list), "Лучшее")
	v.tap(&list, "Лучшее")
	collection := v.homePanel()
	assert.Contains(t, content(&collection), "Видимая книга")
	assert.NotContains(t, content(&collection), "Неодобренная книга")
	assert.Contains(t, content(&collection), "1 книга")
	assert.True(t, hasButton(t, &collection, "← К подборкам"))
}

// A collection withdrawn while open says so.
func TestAWithdrawnCollectionSaysSo(t *testing.T) {
	v := newV2(t)
	id := v.seedCollection("Временная", v.seedBook("Книга", "ru", true))
	home := v.homePanel()
	v.tap(&home, "Подборки")
	list := v.homePanel()
	v.tap(&list, "Временная")
	collection := v.homePanel()
	_, err := v.db.Exec(`UPDATE book_collections SET is_public = false WHERE id = ?`, id)
	require.NoError(t, err)
	v.tap(&collection, "1. Книга")
	card := v.homePanel()
	v.tap(&card, "← К подборке")
	gone := v.homePanel()
	assert.Contains(t, content(&gone), "Эта подборка больше недоступна.")
	assert.True(t, hasButton(t, &gone, "← К подборкам"))
}

// «Простой» makes the next card plain: a rich panel showing one goes out
// again as a plain message.
func TestThePlainCardStyleMakesTheNextCardPlain(t *testing.T) {
	v := newV2(t)
	results, card := v.openFixtureCard()
	require.NotEmpty(t, card.Params["rich_message"], "cards start rich")

	home := v.homePanel()
	v.tap(&home, "Настройки")
	settings := v.homePanel()
	v.tap(&settings, "Вид карточек: расширенный")
	style := v.homePanel()
	v.tap(&style, "Простой")
	assert.Equal(t, "Вид карточек — простой", v.lastAnswer())
	style = v.homePanel()
	assert.True(t, hasButton(t, &style, "✓ Простой"))

	sends := len(v.sends())
	v.tap(&card, "← К результатам")
	require.Len(t, v.sends(), sends+1, "the rich panel goes out again, plain")
	plain := v.lastSend()
	assert.Equal(t, "sendMessage", plain.Method)
	assert.Contains(t, plain.Params["text"], "Поиск: «мир»")
	card = v.openFixtureCardFrom(&plain)
	assert.Empty(t, card.Params["rich_message"])
	assert.Contains(t, card.Params["text"], "война и МИР")
	assert.NotEqual(t, results.MessageID, plain.MessageID)
}

// FB2 as the default format: first on the card and primary, and the site's
// «В Telegram» sends FB2.
func TestFB2AsTheDefaultFormat(t *testing.T) {
	var release func()
	v := newV2(t, func(s *stack) { release = gatedPrepare(s) })
	release()
	home := v.homePanel()
	v.tap(&home, "Настройки")
	settings := v.homePanel()
	v.tap(&settings, "Основной формат: EPUB")
	format := v.homePanel()
	v.tap(&format, "FB2")
	assert.Equal(t, "Основной формат — FB2", v.lastAnswer())
	format = v.homePanel()
	assert.True(t, hasButton(t, &format, "✓ FB2"))

	_, card := v.openFixtureCard()
	rows := tgtest.ButtonTexts(t, &card)
	assert.Equal(t, []string{"FB2", "EPUB", "MOBI", "ZIP"}, rows[0])
	var markup struct {
		InlineKeyboard [][]struct {
			Text  string `json:"text"`
			Style string `json:"style"`
		} `json:"inline_keyboard"`
	}
	require.NoError(t, json.Unmarshal([]byte(card.Params["reply_markup"]), &markup))
	assert.Equal(t, "primary", markup.InlineKeyboard[0][0].Style)

	refused, outcome := v.delivery.SendFromSite(context.Background(), v.user, v.books[scanfixture.EntryMultiAuthor])
	require.Empty(t, refused)
	select {
	case <-outcome:
	case <-time.After(10 * time.Second):
		t.Fatal("the site send did not end")
	}
	docs := v.fake.Calls("sendDocument")
	require.Len(t, docs, 1)
	assert.True(t, strings.HasSuffix(docs[0].Files["document"].Filename, ".fb2"), docs[0].Files["document"].Filename)
}

// An old list's pager says the list is gone.
func TestALegacyPagerSaysTheListIsGone(t *testing.T) {
	v := newV2(t)
	home := v.homePanel()
	sends := len(v.sends())
	v.post(v.u.Callback(ownerTG, &home, "prev_page"))
	require.Len(t, v.sends(), sends+1)
	notice := v.lastSend()
	assert.Equal(t, "Этот список создан до обновления. Повтори поиск.", noticeText(t, &notice))
	assert.Equal(t, [][]string{{"Домой"}}, tgtest.ButtonTexts(t, &notice))
}

// From a card: its author's books, sorted by title, and back to the card;
// its series, by number, «без номера» last.
func TestACardOpensItsAuthorAndSeries(t *testing.T) {
	v := newV2(t)
	books := v.seedAuthor("Станислав Лем", "Солярис", "Возвращение со звёзд", "Эдем")
	series := &models.Series{Ser: "Космос"}
	_, err := v.db.Model(series).Insert()
	require.NoError(t, err)
	for i, n := range []int64{2, 0, 1} {
		_, err = v.db.Model(&models.OrderToSeries{SeriesID: series.ID, BookID: books[i], SerNo: n}).Insert()
		require.NoError(t, err)
	}
	v.say("Солярис")
	results := v.lastSend()
	v.tap(&results, buttonStartingWith(t, &results, "Солярис"))
	card := v.latest(results.MessageID)
	v.tap(&card, "Книги автора")
	list := v.latest(results.MessageID)
	text := content(&list)
	assert.Contains(t, text, "Станислав Лем")
	assert.Contains(t, text, "3 книги")
	assert.Less(t, strings.Index(text, "Возвращение"), strings.Index(text, "Солярис"))
	assert.Less(t, strings.Index(text, "Солярис"), strings.Index(text, "Эдем"))
	assert.True(t, hasButton(t, &list, "← К книге"))
	assert.True(t, hasButton(t, &list, "Искать у автора"))

	v.tap(&list, "← К книге")
	card = v.latest(results.MessageID)
	v.tap(&card, "Серия")
	shelf := v.latest(results.MessageID)
	assert.Contains(t, content(&shelf), "Серия «Космос»")
	// A rich list numbers a series' books by their number in it; one
	// without a number comes last, bulleted, and says so.
	assert.Equal(t, []listItem{{1, "Эдем", "Станислав Лем"}, {2, "Солярис", "Станислав Лем"}, {0, "Возвращение со звёзд", "без номера"}},
		listItems(t, &shelf))
}

// «Искать у автора» and «Найти в избранном» answer on the list they came
// from.
func TestSearchesWithinAnAuthorAndTheFavorites(t *testing.T) {
	v := newV2(t)
	v.seedAuthor("Кир Булычёв", "Девочка с Земли", "Сто лет тому вперёд")
	v.say("Девочка")
	results := v.lastSend()
	v.tap(&results, buttonStartingWith(t, &results, "Девочка"))
	card := v.latest(results.MessageID)
	v.tap(&card, "☆ В избранное")
	card = v.latest(results.MessageID)
	v.tap(&card, "Книги автора")
	list := v.latest(results.MessageID)

	v.tap(&list, "Искать у автора")
	prompt := v.lastSend()
	assert.Contains(t, prompt.Params["text"], "Что найти у автора «Кир Булычёв»?")
	v.reply(&prompt, "вперёд")
	found := v.latest(results.MessageID)
	assert.Contains(t, content(&found), "Поиск у автора: Кир Булычёв — «вперёд»")
	assert.Contains(t, content(&found), "Сто лет тому вперёд")
	assert.NotContains(t, content(&found), "Девочка с Земли")
	assert.Equal(t, "Готово.", v.latest(prompt.MessageID).Params["text"])

	home := v.homePanel()
	v.tap(&home, "★ Избранное")
	favs := v.homePanel()
	assert.Contains(t, content(&favs), "Девочка с Земли")
	v.tap(&favs, "Найти в избранном")
	prompt = v.lastSend()
	v.reply(&prompt, "Девочка")
	found = v.homePanel()
	assert.Contains(t, content(&found), "Поиск в избранном: «Девочка»")
	assert.True(t, hasButton(t, &found, "Всё избранное"))
}

// Empty favorites say how to fill them.
func TestEmptyFavorites(t *testing.T) {
	v := newV2(t)
	home := v.homePanel()
	v.tap(&home, "★ Избранное")
	favs := v.homePanel()
	assert.Contains(t, content(&favs), "В избранном пока нет книг")
	assert.Equal(t, [][]string{{"Домой"}}, tgtest.ButtonTexts(t, &favs))
}

// The inline-mode instructions show the command as code, with a copy button.
func TestTheInlineInstructions(t *testing.T) {
	v := newV2(t)
	home := v.homePanel()
	v.tap(&home, "Настройки")
	settings := v.homePanel()
	v.tap(&settings, "Подсказки при поиске")
	inline := v.homePanel()
	assert.Contains(t, content(&inline), "<code>/setinline</code>")
	var markup struct {
		InlineKeyboard [][]struct {
			Text     string `json:"text"`
			URL      string `json:"url"`
			CopyText *struct {
				Text string `json:"text"`
			} `json:"copy_text"`
		} `json:"inline_keyboard"`
	}
	require.NoError(t, json.Unmarshal([]byte(inline.Params["reply_markup"]), &markup))
	assert.Equal(t, "https://t.me/BotFather", markup.InlineKeyboard[0][0].URL)
	require.NotNil(t, markup.InlineKeyboard[1][0].CopyText)
	assert.Equal(t, "/setinline", markup.InlineKeyboard[1][0].CopyText.Text)
	assert.False(t, hasButton(t, &inline, "Поддержать проект"), "no donation methods, no button")
}

// noticeText is a one-line notice's text, rich or plain.
func noticeText(t *testing.T, c *tgtest.Call) string {
	t.Helper()
	if c.Params["rich_message"] == "" {
		return c.Params["text"]
	}
	var rich struct {
		Blocks []struct {
			Text string `json:"text"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal([]byte(c.Params["rich_message"]), &rich))
	require.Len(t, rich.Blocks, 1)
	return rich.Blocks[0].Text
}
