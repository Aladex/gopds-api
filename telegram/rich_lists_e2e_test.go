package telegram_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"gopds-api/telegram/tgtest"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rich lists: in a rich panel a list's titles, tabs and pages are
// buttons in the message, and its keyboard keeps only the filters, the way
// back and «Домой». A plain panel keeps the list of before.

// numberedRow is a keyboard button that opens the n-th item of a list.
var numberedRow = regexp.MustCompile(`^\d+\. `)

// bookRows are the keyboard's buttons that open a list's items.
func bookRows(t *testing.T, c *tgtest.Call) []string {
	t.Helper()
	var out []string
	for _, row := range tgtest.ButtonTexts(t, c) {
		for _, text := range row {
			if numberedRow.MatchString(text) {
				out = append(out, text)
			}
		}
	}
	return out
}

// links are the titles of a rich list: its buttons styled as links.
func links(t *testing.T, c *tgtest.Call) []string {
	t.Helper()
	var out []string
	for _, b := range tgtest.RichButtons(t, c) {
		if b.Style == "link" {
			out = append(out, b.Text)
		}
	}
	return out
}

// listItem is one item of a rich list: its number (0 for none), its title,
// and how its second line starts.
type listItem struct {
	Value int
	Title string
	Sub   string
}

// listItems are the items of a call's rich lists, in order; Sub keeps the
// second line up to its first « · ».
func listItems(t *testing.T, c *tgtest.Call) []listItem {
	t.Helper()
	var msg struct {
		Blocks []struct {
			Type  string `json:"type"`
			Items []struct {
				Value  int `json:"value"`
				Blocks []struct {
					Text json.RawMessage `json:"text"`
				} `json:"blocks"`
			} `json:"items"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal([]byte(c.Params["rich_message"]), &msg))
	var out []listItem
	for _, b := range msg.Blocks {
		for _, it := range b.Items {
			item := listItem{Value: it.Value}
			var title struct {
				Button struct {
					Text string `json:"text"`
				} `json:"button"`
			}
			if len(it.Blocks) > 0 && json.Unmarshal(it.Blocks[0].Text, &title) == nil {
				item.Title = title.Button.Text
			}
			if len(it.Blocks) > 1 {
				var second []struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(it.Blocks[1].Text, &second) == nil && len(second) > 0 {
					item.Sub, _, _ = strings.Cut(second[0].Text, " · ")
				}
			}
			out = append(out, item)
		}
	}
	return out
}

// (a) A rich results page: the titles are the links that open the books,
// the tabs and the pages are in the message, and the keyboard has no row per
// book.
func TestARichResultsPageIsLaidOutInTheMessage(t *testing.T) {
	v := newV2(t)
	v.addBooks("Основание")
	v.say("Основание")
	results := v.lastSend()
	require.Equal(t, "sendRichMessage", results.Method)
	assert.Empty(t, bookRows(t, &results), "no row per book: %v", tgtest.ButtonTexts(t, &results))
	assert.Equal(t, []string{"Основание 1", "Основание 2", "Основание 3", "Основание 4", "Основание 5"}, links(t, &results))
	assert.Equal(t, []string{"Автор", "Язык"}, tgtest.ButtonTexts(t, &results)[0])
	assert.Equal(t, []string{"Домой"}, tgtest.ButtonTexts(t, &results)[1])
	types := richBlockTypes(t, &results)
	assert.Equal(t, []string{"heading", "buttons", "list", "buttons"}, types)

	v.tap(&results, "→")
	page2 := v.latest(results.MessageID)
	require.Equal(t, "editMessageText", page2.Method)
	assert.Contains(t, content(&page2), "Страница 2 из 3")
	assert.Equal(t, []string{"Основание 6", "Основание 7", "Основание 8", "Основание 9", "Основание 10"}, links(t, &page2))
	assert.Empty(t, bookRows(t, &page2))

	v.tap(&page2, "←")
	page1 := v.latest(results.MessageID)
	assert.Contains(t, content(&page1), "Страница 1 из 3")

	v.tap(&page1, "Авторы · 0")
	authors := v.latest(results.MessageID)
	assert.Contains(t, content(&authors), "Авторов по этому запросу нет.")
	v.tap(&authors, "Книги · 12")
	books := v.latest(results.MessageID)
	assert.Equal(t, links(t, &results), links(t, &books))

	v.tap(&books, "Основание 4")
	card := v.latest(results.MessageID)
	assert.Contains(t, content(&card), `"text":"Основание 4","size":2`, "the card of the title pressed")
	assert.Contains(t, tgtest.ButtonTexts(t, &card)[0], "EPUB")

	v.tap(&card, "← К результатам")
	back := v.latest(results.MessageID)
	assert.Equal(t, links(t, &results), links(t, &back), "back on the page it came from")
	assert.Empty(t, bookRows(t, &back))
}

// (b) A press of a link of a screen that has moved on is refused: the link
// carries the revision of its screen, as a keyboard button does.
func TestARichListsOldLinkIsRefused(t *testing.T) {
	v := newV2(t)
	v.addBooks("Основание")
	v.say("Основание")
	results := v.lastSend()
	v.tap(&results, "→")
	edits := len(v.edits(results.MessageID))
	v.tap(&results, "Основание 1")
	assert.Len(t, v.edits(results.MessageID), edits, "the old page's link does nothing")
	answers := v.fake.Calls("answerCallbackQuery")
	assert.Equal(t, "Этот экран уже изменился.", answers[len(answers)-1].Params["text"])
}

// (c) An author's books, opened from a card, are a rich list too: the way
// back stays in the keyboard, the titles open their cards.
func TestARichAuthorListOpensItsBooks(t *testing.T) {
	v := newV2(t)
	v.seedAuthor("Станислав Лем", "Солярис", "Возвращение со звёзд", "Эдем")
	v.say("Солярис")
	results := v.lastSend()
	v.tap(&results, firstLink(t, &results, "Солярис"))
	card := v.latest(results.MessageID)
	v.tap(&card, "Книги автора")
	list := v.latest(results.MessageID)
	require.Equal(t, "editMessageText", list.Method)
	assert.Empty(t, bookRows(t, &list))
	require.NotEmpty(t, links(t, &list))
	rows := tgtest.ButtonTexts(t, &list)
	assert.Equal(t, []string{"← К книге", "Домой"}, rows[len(rows)-1])
	types := richBlockTypes(t, &list)
	assert.Equal(t, []string{"heading", "footer", "list"}, types)
	assert.Equal(t, []listItem{{1, "Возвращение со звёзд", ""}, {2, "Солярис", ""}, {3, "Эдем", ""}}, listItems(t, &list))

	v.tap(&list, links(t, &list)[0])
	opened := v.latest(results.MessageID)
	assert.Contains(t, tgtest.ButtonTexts(t, &opened)[0], "EPUB", "a card")
	v.tap(&opened, "← К автору")
	assert.Equal(t, links(t, &list), links(t, new(v.latest(results.MessageID))))
}

// firstLink is the first title of a rich list that starts with prefix.
func firstLink(t *testing.T, c *tgtest.Call, prefix string) string {
	t.Helper()
	for _, l := range links(t, c) {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no title %q… in %v", prefix, links(t, c))
	return ""
}

// (d) Plain panels keep the list of before: a row per book in the keyboard,
// the pages there too, and no rich message.
func TestAPlainListKeepsItsKeyboard(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	v.addBooks("Основание")
	v.say("Основание")
	results := v.lastSend()
	require.Equal(t, "sendMessage", results.Method)
	assert.Equal(t, []string{"1. Основание 1", "2. Основание 2", "3. Основание 3", "4. Основание 4", "5. Основание 5"},
		bookRows(t, &results))
	assert.Empty(t, tgtest.RichButtons(t, &results))
	v.tap(&results, "Вперёд →")
	page2 := v.latest(results.MessageID)
	assert.Contains(t, page2.Params["text"], "Страница 2 из 3")
	assert.Equal(t, "6. Основание 6", bookRows(t, &page2)[0])
}

// (e) A list Telegram refuses rich goes out as its plain self: the rows per
// book come back to the keyboard, since the message has no links.
func TestARefusedRichListGoesOutWithItsPlainKeyboard(t *testing.T) {
	v := newV2(t, func(s *stack) {
		s.fake.On(tgtest.Rule{Method: "sendRichMessage", Reply: func(w http.ResponseWriter, _ *http.Request, _ *tgtest.Call) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: rich messages are not supported"}`))
		}})
	})
	v.addBooks("Основание")
	v.say("Основание")
	results := v.lastSend()
	require.Equal(t, "sendMessage", results.Method)
	assert.Len(t, bookRows(t, &results), 5)
	assert.Contains(t, tgtest.ButtonTexts(t, &results), []string{"Вперёд →"})
}

// favoriteFixture puts the fixture book in the favorites and returns the
// home panel as it is then.
func (v *v2Stack) favoriteFixture() tgtest.Call {
	v.t.Helper()
	_, card := v.openFixtureCard()
	v.tap(&card, "☆ В избранное")
	return v.homeAgain()
}

// homeMode is how the chat's home panel is recorded: its kind, and whether
// it is still the home panel.
func (v *v2Stack) homeMode(messageID int64) string {
	v.t.Helper()
	var mode string
	_, err := v.db.QueryOne(pg.Scan(&mode), `SELECT mode FROM telegram_panel WHERE message_id = ? AND is_home`, messageID)
	require.NoError(v.t, err)
	return mode
}

// (f) The home panel switches its kind in place: a list it opens is rich,
// home again is plain — the same pinned message all along.
func TestTheHomePanelGoesRichForAListAndPlainForHome(t *testing.T) {
	v := newV2(t)
	home := v.favoriteFixture()
	require.Equal(t, "plain", v.homeMode(home.MessageID))
	v.tap(&home, "★ Избранное")
	favs := v.latest(home.MessageID)
	require.Equal(t, "editMessageText", favs.Method)
	require.Contains(t, favs.Params, "rich_message", "the plain home message edited into a rich one")
	assert.Equal(t, []string{"heading", "footer", "list"}, richBlockTypes(t, &favs))
	assert.Len(t, links(t, &favs), 1)
	assert.Empty(t, bookRows(t, &favs))
	assert.Equal(t, "rich", v.homeMode(home.MessageID))

	v.tap(&favs, links(t, &favs)[0])
	card := v.latest(home.MessageID)
	assert.Contains(t, card.Params, "rich_message", "a card opened from it is rich too")
	v.tap(&card, "Домой")
	back := v.latest(home.MessageID)
	assert.NotContains(t, back.Params, "rich_message")
	assert.Contains(t, back.Params["text"], "Напиши название книги")
	assert.Equal(t, "plain", v.homeMode(home.MessageID), "home is plain again")
	assert.Equal(t, home.MessageID, v.home(), "still the pinned home")
}

// (g) With plain cards the home panel never goes rich.
func TestTheHomePanelStaysPlainWithPlainCards(t *testing.T) {
	v := newV2(t)
	home := v.favoriteFixture()
	v.plainCards()
	v.tap(&home, "★ Избранное")
	favs := v.latest(home.MessageID)
	assert.NotContains(t, favs.Params, "rich_message")
	assert.Equal(t, []string{"1. война и МИР"}, bookRows(t, &favs))
	assert.Equal(t, "plain", v.homeMode(home.MessageID))
}

// (h) Telegram refusing the rich edit, the list is shown plain in the same
// message, which stays the home panel.
func TestARefusedRichHomeEditShowsTheListPlainInPlace(t *testing.T) {
	v := newV2(t)
	home := v.favoriteFixture()
	v.fake.On(tgtest.Rule{Method: "editMessageText", Times: 1, Reply: func(w http.ResponseWriter, _ *http.Request, c *tgtest.Call) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: rich messages are not supported"}`))
	}})
	sends := len(v.sends())
	v.tap(&home, "★ Избранное")
	favs := v.latest(home.MessageID)
	assert.NotContains(t, favs.Params, "rich_message")
	assert.Equal(t, []string{"1. война и МИР"}, bookRows(t, &favs))
	assert.Equal(t, "plain", v.homeMode(home.MessageID))
	assert.Len(t, v.sends(), sends, "no new message: the home panel moved in place")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_bot_ui WHERE rich_rejected_at IS NOT NULL`))
}
