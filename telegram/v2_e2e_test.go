package telegram_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/posters"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/tgtest"

	"github.com/go-pg/pg/v10"
	tgbot "github.com/go-telegram/bot/models"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice S4: the v2 router — no commands, a pinned home, typed search with
// its own panel, the book card rich or plain, files from the card.

// v2Stack is a stack whose bot is bound to the reader through the v2 router.
type v2Stack struct {
	*stack
	user    int64
	posters string
}

// newV2 binds botToken1 to a reader, the bot switched to the v2 router
// before the link is opened. configure runs before the service starts.
func newV2(t *testing.T, configure ...func(*stack)) *v2Stack {
	t.Helper()
	s := newStack(t)
	for _, c := range configure {
		c(s)
	}
	dir := t.TempDir()
	previous := viper.GetString("app.posters_path")
	viper.Set("app.posters_path", dir)
	t.Cleanup(func() { viper.Set("app.posters_path", previous) })
	s.start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	// A bot is v2 by default (S6): nothing to switch.
	v := &v2Stack{stack: s, user: user, posters: dir}
	v.post(s.u.Text(ownerTG, "/start link_"+secret))
	return v
}

// post delivers an update and waits until it is handled.
func (v *v2Stack) post(update *tgbot.Update) {
	v.t.Helper()
	rec := v.hook(botToken1).Post(v.t, update)
	require.Equal(v.t, http.StatusOK, rec.Code)
	v.bm.WaitIdle()
}

func (v *v2Stack) say(text string) { v.post(v.u.Text(ownerTG, text)) }

func (v *v2Stack) tap(on *tgtest.Call, label string) { v.post(v.u.Tap(v.t, ownerTG, on, label)) }

// panelMethods are the calls that put a panel's content in the chat.
var panelMethods = []string{"sendMessage", "sendRichMessage", "editMessageText"}

// calls are the answered panel calls to the owner's chat, in order.
func (v *v2Stack) calls() []tgtest.Call {
	var out []tgtest.Call
	for _, m := range panelMethods {
		for _, c := range v.fake.AnsweredCalls(m) {
			if c.Params["chat_id"] == strconv.FormatInt(ownerTG, 10) {
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// sends are the panel messages sent (not edited).
func (v *v2Stack) sends() []tgtest.Call {
	var out []tgtest.Call
	for _, c := range v.calls() {
		if c.Method != "editMessageText" {
			out = append(out, c)
		}
	}
	return out
}

// lastSend is the newest message sent to the chat.
func (v *v2Stack) lastSend() tgtest.Call {
	v.t.Helper()
	s := v.sends()
	require.NotEmpty(v.t, s)
	return s[len(s)-1]
}

// edits are the edits of one message.
func (v *v2Stack) edits(messageID int64) []tgtest.Call {
	var out []tgtest.Call
	for _, c := range v.calls() {
		if c.Method == "editMessageText" && c.Int64("message_id") == messageID {
			out = append(out, c)
		}
	}
	return out
}

// latest is the newest content of one message: its last edit, or its send.
func (v *v2Stack) latest(messageID int64) tgtest.Call {
	v.t.Helper()
	var last *tgtest.Call
	for _, c := range v.calls() {
		if c.MessageID == messageID {
			c := c
			last = &c
		}
	}
	require.NotNil(v.t, last, "no call for message %d", messageID)
	return *last
}

// content is what a call shows: its text, or its rich blocks as JSON.
func content(c *tgtest.Call) string {
	if rich, ok := c.Params["rich_message"]; ok {
		return rich
	}
	return c.Params["text"]
}

// richBlockTypes lists the block types of a rich call.
func richBlockTypes(t *testing.T, c *tgtest.Call) []string {
	t.Helper()
	var rich struct {
		Blocks []struct {
			Type string `json:"type"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal([]byte(c.Params["rich_message"]), &rich))
	var types []string
	for _, b := range rich.Blocks {
		types = append(types, b.Type)
	}
	return types
}

// richPhotoMedia is the media of a rich call's photo block, "" without one.
func richPhotoMedia(t *testing.T, c *tgtest.Call) string {
	t.Helper()
	var rich struct {
		Blocks []struct {
			Type  string `json:"type"`
			Photo struct {
				Media string `json:"media"`
			} `json:"photo"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal([]byte(c.Params["rich_message"]), &rich))
	for _, b := range rich.Blocks {
		if b.Type == "photo" {
			return b.Photo.Media
		}
	}
	return ""
}

func (v *v2Stack) plainCards() {
	v.t.Helper()
	_, err := v.db.Exec(`INSERT INTO telegram_user_settings (user_id, card_style) VALUES (?, 'plain')
		ON CONFLICT (user_id) DO UPDATE SET card_style = 'plain'`, v.user)
	require.NoError(v.t, err)
}

// extraBooks is how many books addBooks adds: three pages of results.
const extraBooks = 12

// addBooks adds extraBooks approved books titled «{prefix} {i}».
func (v *v2Stack) addBooks(prefix string) {
	v.t.Helper()
	for i := 1; i <= extraBooks; i++ {
		book := &models.Book{Path: "extra.zip", Format: "fb2", FileName: prefix + strconv.Itoa(i) + ".fb2",
			Title: prefix + " " + strconv.Itoa(i), Lang: "ru", Approved: true, RegisterDate: time.Now(),
			MD5: prefix + strconv.Itoa(i)}
		_, err := v.db.Model(book).Insert()
		require.NoError(v.t, err)
	}
}

// giveCover puts a cover file for the book where the library keeps them.
func (v *v2Stack) giveCover(bookID int64) []byte {
	v.t.Helper()
	var book models.Book
	require.NoError(v.t, v.db.Model(&book).Where("id = ?", bookID).Select())
	path := posters.FilePath(v.posters, book.Path, book.FileName)
	require.NoError(v.t, os.MkdirAll(filepath.Dir(path), 0o750))
	data := []byte("cover bytes of book " + strconv.FormatInt(bookID, 10))
	require.NoError(v.t, os.WriteFile(path, data, 0o600))
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET cover = true WHERE id = ?`, bookID)
	require.NoError(v.t, err)
	return data
}

// home is the chat's home panel message id.
func (v *v2Stack) home() int64 {
	v.t.Helper()
	var id int64
	_, err := v.db.QueryOne(pg.Scan(&id), `SELECT message_id FROM telegram_panel WHERE is_home`)
	require.NoError(v.t, err)
	return id
}

// (a) A v2 bot's command menu is cleared — every scope and language — and a
// process serving it registers none.
func TestTheV2BotHasNoCommands(t *testing.T) {
	v := newV2(t)
	deletes := v.fake.Calls("deleteMyCommands")
	require.Len(t, deletes, 9)
	seen := map[string]bool{}
	for _, c := range deletes {
		seen[c.Params["scope"]+"|"+c.Params["language_code"]] = true
	}
	for _, scope := range []string{`{"type":"default"}`, `{"type":"all_private_chats"}`,
		`{"type":"chat","chat_id":` + strconv.FormatInt(ownerTG, 10) + `}`} {
		for _, lang := range []string{"", "ru", "en"} {
			assert.True(t, seen[scope+"|"+lang], "deleteMyCommands %s %q", scope, lang)
		}
	}
	menus := v.fake.Calls("setChatMenuButton")
	require.Len(t, menus, 2)
	assert.Contains(t, menus[1].Params["chat_id"], strconv.FormatInt(ownerTG, 10))

	registered := len(v.fake.Calls("setMyCommands"))
	v.secondPod() // a process that serves the bot as v2 from its start
	v.say("/start")
	assert.Len(t, v.fake.Calls("setMyCommands"), registered, "no commands registered for a v2 bot")
	assert.Len(t, v.fake.Calls("deleteMyCommands"), 9, "cleared once")
}

// (b) The link opens the home panel, pinned; /start again sends nothing new
// and pins the same message.
func TestStartPinsTheHomePanel(t *testing.T) {
	v := newV2(t)
	home := v.home()
	require.Len(t, v.sends(), 1)
	first := v.sends()[0]
	assert.Contains(t, content(&first), "Бот подключён.")
	pins := v.fake.Calls("pinChatMessage")
	require.Len(t, pins, 1)
	assert.Equal(t, home, pins[0].Int64("message_id"))

	v.say("/start")
	assert.Len(t, v.sends(), 1, "nothing new")
	pins = v.fake.Calls("pinChatMessage")
	require.Len(t, pins, 2)
	assert.Equal(t, home, pins[1].Int64("message_id"))

	v.say("Мир")
	assert.Len(t, v.fake.Calls("pinChatMessage"), 2, "a message is not a pin trigger")
}

// (c) Typed text opens a new results panel with both tabs; the pinned home
// is not touched.
func TestTypedTextOpensAResultsPanel(t *testing.T) {
	v := newV2(t)
	home := v.home()
	v.say("Мир")
	results := v.lastSend()
	assert.NotEqual(t, home, results.MessageID)
	tabs := tgtest.RichButtons(t, &results)
	require.GreaterOrEqual(t, len(tabs), 2)
	assert.True(t, strings.HasPrefix(tabs[0].Text, "Книги · "), tabs[0].Text)
	assert.Equal(t, "primary", tabs[0].Style, "the open tab")
	assert.True(t, strings.HasPrefix(tabs[1].Text, "Авторы · "), tabs[1].Text)
	assert.Empty(t, tabs[1].Style)
	assert.Contains(t, content(&results), "Поиск: «Мир»")
	assert.Empty(t, v.edits(home), "the home panel stays as it is")
}

// (d) Paging the first of two results panels edits that panel, with its own
// query (П-1).
func TestPagingKeepsItsOwnQuery(t *testing.T) {
	v := newV2(t)
	v.addBooks("Основание")
	v.addBooks("Маяк")
	v.say("Основание")
	first := v.lastSend()
	v.say("Маяк")
	second := v.lastSend()
	require.NotEqual(t, first.MessageID, second.MessageID)

	v.tap(&first, "→")
	edits := v.edits(first.MessageID)
	require.Len(t, edits, 1)
	page := content(&edits[0])
	assert.Contains(t, page, "Поиск: «Основание»")
	assert.Contains(t, page, "Страница 2 из 3")
	assert.NotContains(t, page, "Маяк")
	assert.Empty(t, v.edits(second.MessageID))
}

// (e) Two presses of the same «Вперёд →», both read the panel before either
// moved it: one edit (П-10). Only the revision compare-and-set can tell them
// apart — the early revision check sees the same revision for both.
func TestADoublePressEditsOnce(t *testing.T) {
	var target atomic.Int64 // the panel's message id, once known
	var reads atomic.Int32
	both := make(chan struct{})
	// Installed before the service starts: go-pg's hooks are not to be
	// added while queries run.
	v := newV2(t, func(s *stack) {
		s.db.AddQueryHook(queryHook(func(_ context.Context, q string) error {
			id := target.Load()
			if id == 0 || !strings.Contains(q, `FROM "telegram_panel"`) ||
				!strings.Contains(q, `"message_id" = `+strconv.FormatInt(id, 10)) {
				return nil
			}
			if reads.Add(1) == 2 {
				close(both)
			}
			select {
			case <-both:
			case <-time.After(3 * time.Second):
			}
			return nil
		}))
	})
	v.addBooks("Основание")
	v.say("Основание")
	panel := v.lastSend()
	target.Store(panel.MessageID)
	first := v.u.Tap(t, ownerTG, &panel, "→")
	second := v.u.Tap(t, ownerTG, &panel, "→")
	var wg sync.WaitGroup
	for _, u := range []*tgbot.Update{first, second} {
		wg.Add(1)
		go func(u *tgbot.Update) {
			defer wg.Done()
			v.hook(botToken1).Post(t, u)
		}(u)
	}
	wg.Wait()
	v.bm.WaitIdle()
	require.Equal(t, int32(2), reads.Load(), "both presses read the panel")
	assert.Len(t, v.edits(panel.MessageID), 1)
	var texts []string
	for _, a := range v.fake.Calls("answerCallbackQuery") {
		texts = append(texts, a.Params["text"])
	}
	assert.Contains(t, texts, "Этот экран уже изменился.")
}

// openFixtureCard searches the fixture book and opens its card.
func (v *v2Stack) openFixtureCard() (results, card tgtest.Call) {
	v.t.Helper()
	v.say("мир")
	results = v.lastSend()
	var label string
	for _, text := range tgtest.Labels(v.t, &results) {
		if strings.Contains(text, "война и МИР") {
			label = text
		}
	}
	require.NotEmpty(v.t, label, "the fixture book is listed: %v", tgtest.Labels(v.t, &results))
	v.tap(&results, label)
	return results, v.latest(results.MessageID)
}

// (f) A rich card: photo, heading, authors, table, annotation, in that
// order; the cover is uploaded once, then reused by its file_id.
func TestARichCardUploadsItsCoverOnce(t *testing.T) {
	v := newV2(t)
	bookID := v.books[scanfixture.EntryMultiAuthor]
	data := v.giveCover(bookID)
	results, card := v.openFixtureCard()
	require.Equal(t, "editMessageText", card.Method)
	types := richBlockTypes(t, &card)
	require.GreaterOrEqual(t, len(types), 3)
	assert.Equal(t, []string{"photo", "heading", "paragraph"}, types[:3])
	assert.Contains(t, types, "table")
	assert.Equal(t, "attach://cover", richPhotoMedia(t, &card))
	require.Contains(t, card.Files, "cover")
	assert.Equal(t, data, card.Files["cover"].Data)
	var fileID string
	_, err := v.db.QueryOne(pg.Scan(&fileID), `SELECT file_id FROM telegram_cover_file WHERE book_id = ?`, bookID)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(fileID, "photo-"), fileID)
	rows := tgtest.ButtonTexts(t, &card)
	assert.Equal(t, []string{"EPUB", "FB2", "MOBI", "ZIP"}, rows[0])
	assert.Equal(t, []string{"☆ В избранное"}, rows[1])

	v.tap(&card, "← К результатам")
	back := v.latest(results.MessageID)
	assert.Contains(t, content(&back), "Поиск: «мир»")
	card = v.openFixtureCardFrom(&back)
	assert.Equal(t, fileID, richPhotoMedia(t, &card))
	assert.NotContains(t, card.Files, "cover", "no second upload")
}

// openFixtureCardFrom opens the fixture book from a results panel.
func (v *v2Stack) openFixtureCardFrom(results *tgtest.Call) tgtest.Call {
	v.t.Helper()
	for _, text := range tgtest.Labels(v.t, results) {
		if strings.Contains(text, "война и МИР") {
			v.tap(results, text)
			return v.latest(results.MessageID)
		}
	}
	v.t.Fatalf("no fixture book in %v", tgtest.Labels(v.t, results))
	return tgtest.Call{}
}

// (g) A file_id Telegram refuses is forgotten, and the cover uploaded.
func TestARefusedFileIDIsUploadedAgain(t *testing.T) {
	v := newV2(t)
	bookID := v.books[scanfixture.EntryMultiAuthor]
	v.giveCover(bookID)
	var ref int64
	_, err := v.db.QueryOne(pg.Scan(&ref), `SELECT id FROM telegram_bot`)
	require.NoError(t, err)
	_, err = v.db.Exec(`INSERT INTO telegram_cover_file (bot_ref, book_id, file_id) VALUES (?, ?, 'stale-id')`, ref, bookID)
	require.NoError(t, err)
	v.fake.On(tgtest.Rule{Method: "editMessageText", Times: 1,
		Match: func(c tgtest.Call) bool { return strings.Contains(c.Params["rich_message"], "stale-id") },
		Reply: func(w http.ResponseWriter, _ *http.Request, _ *tgtest.Call) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: wrong file identifier/HTTP URL specified"}`))
		}})
	_, card := v.openFixtureCard()
	assert.Equal(t, "attach://cover", richPhotoMedia(t, &card))
	assert.Contains(t, card.Files, "cover")
}

// (h) Telegram refusing rich messages: the same screen goes out plain, the
// panel is plain, and the bot is remembered as refused.
func TestARichRefusalFallsBackToPlain(t *testing.T) {
	v := newV2(t, func(s *stack) {
		s.fake.On(tgtest.Rule{Method: "sendRichMessage", Reply: func(w http.ResponseWriter, _ *http.Request, _ *tgtest.Call) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: rich messages are not supported"}`))
		}})
	})
	home := v.lastSend()
	assert.Equal(t, "sendMessage", home.Method, "the home panel is plain in both modes")
	assert.Contains(t, home.Params["text"], "Напиши название книги")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_panel WHERE mode = 'plain' AND is_home`))
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_bot_ui WHERE rich_rejected_at IS NOT NULL`))

	v.say("мир")
	results := v.lastSend()
	assert.Equal(t, "sendMessage", results.Method, "sent plain after the refusal")
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_panel WHERE message_id = ? AND mode = 'plain'`, results.MessageID))
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_bot_ui WHERE rich_rejected_at IS NOT NULL`))

	tried := len(v.fake.Calls("sendRichMessage"))
	v.say("мир")
	assert.Equal(t, "sendMessage", v.lastSend().Method, "kept plain")
	assert.Len(t, v.fake.Calls("sendRichMessage"), tried, "without trying rich again")
}

// (i) A plain card: the annotation in a collapsed quote; one too long for
// the message is cut, and its full text is a press away.
func TestAPlainCardCutsALongAnnotation(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	bookID := v.books[scanfixture.EntryMultiAuthor]
	long := strings.Repeat("Длинное предложение аннотации. ", 300) // the column holds 10 000
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET annotation = ? WHERE id = ?`, long, bookID)
	require.NoError(t, err)
	_, card := v.openFixtureCard()
	text := card.Params["text"]
	assert.Contains(t, text, "<blockquote expandable>")
	assert.LessOrEqual(t, len([]rune(text)), 4096)
	assert.Contains(t, tgtest.ButtonTexts(t, &card), []string{"Аннотация полностью"})

	v.tap(&card, "Аннотация полностью")
	page := v.latest(card.MessageID)
	assert.Contains(t, page.Params["text"], "аннотация")
	assert.Contains(t, page.Params["text"], "Страница 1 из")
	assert.Contains(t, tgtest.ButtonTexts(t, &page), []string{"← К книге", "Домой"})
}

// gatedPrepare holds every preparation until released.
func gatedPrepare(s *stack) (release func()) {
	gate := make(chan struct{})
	var once sync.Once
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		<-gate
		return delivery.File{Name: "x." + f, Data: []byte("PK book")}, nil
	}
	return func() { once.Do(func() { close(gate) }) }
}

// (j) A format pressed on the card: the button says it is being prepared,
// the document comes with its name, caption and «Открыть карточку», and the
// card is painted again without the mark.
func TestAFileFromTheCard(t *testing.T) {
	var release func()
	v := newV2(t, func(s *stack) { release = gatedPrepare(s) })
	t.Cleanup(release)
	_, card := v.openFixtureCard()
	messages := len(v.messagesTo(ownerTG))
	v.tap(&card, "EPUB")
	marked := v.latest(card.MessageID)
	assert.Equal(t, "⏳ Готовлю EPUB…", tgtest.ButtonTexts(t, &marked)[0][0])
	assertDisabled(t, &marked, "⏳ Готовлю EPUB…")

	release()
	v.waitJobSent()
	v.delivery.WaitIdle()
	doc := v.fake.Calls("sendDocument")
	require.Len(t, doc, 1)
	assert.True(t, strings.HasSuffix(doc[0].Files["document"].Filename, " - война и МИР.epub"), doc[0].Files["document"].Filename)
	assert.True(t, strings.HasPrefix(doc[0].Params["caption"], "война и МИР — "), doc[0].Params["caption"])
	assert.Contains(t, doc[0].Params["caption"], "\nEPUB · ")
	assert.Equal(t, [][]string{{"Открыть карточку"}}, tgtest.ButtonTexts(t, &doc[0]))
	assert.Len(t, v.messagesTo(ownerTG), messages, "no status message for a card's job")
	final := v.latest(card.MessageID)
	assert.Equal(t, "EPUB", tgtest.ButtonTexts(t, &final)[0][0])
}

// (j) A panel that moved on before the file arrived is left alone.
func TestAMovedPanelIsNotRepainted(t *testing.T) {
	var release func()
	v := newV2(t, func(s *stack) { release = gatedPrepare(s) })
	t.Cleanup(release)
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	marked := v.latest(card.MessageID)
	v.tap(&marked, "← К результатам")
	edits := len(v.edits(card.MessageID))
	release()
	v.waitJobSent()
	v.delivery.WaitIdle()
	assert.Len(t, v.edits(card.MessageID), edits)
}

func (v *v2Stack) waitJobSent() {
	v.t.Helper()
	require.Eventually(v.t, func() bool {
		return v.count(`SELECT count(*) FROM telegram_delivery WHERE state = ?`, models.DeliverySent) == 1
	}, 10*time.Second, 10*time.Millisecond)
}

// (k) «Открыть карточку» on the document opens the card in a new panel.
func TestTheDocumentOpensItsCard(t *testing.T) {
	var release func()
	v := newV2(t, func(s *stack) { release = gatedPrepare(s) })
	release()
	_, card := v.openFixtureCard()
	v.tap(&card, "FB2")
	v.waitJobSent()
	v.delivery.WaitIdle()
	doc := v.fake.Calls("sendDocument")[0]
	before := len(v.sends())
	v.tap(&doc, "Открыть карточку")
	require.Len(t, v.sends(), before+1)
	opened := v.lastSend()
	assert.Contains(t, content(&opened), "война и МИР")
	assert.Equal(t, []string{"EPUB", "FB2", "MOBI", "ZIP"}, tgtest.ButtonTexts(t, &opened)[0])
}

// (l) A home message that is gone is sent again and pinned; «Домой» pins
// the home again when the reader's chat has none pinned.
func TestAGoneHomeIsSentAgain(t *testing.T) {
	v := newV2(t)
	old := v.home()
	v.fake.On(tgtest.Rule{Method: "pinChatMessage", Times: 1,
		Match: func(c tgtest.Call) bool { return c.Int64("message_id") == old },
		Reply: func(w http.ResponseWriter, _ *http.Request, _ *tgtest.Call) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message to pin not found"}`))
		}})
	v.say("/start")
	fresh := v.home()
	assert.NotEqual(t, old, fresh)
	pins := v.fake.Calls("pinChatMessage")
	assert.Equal(t, fresh, pins[len(pins)-1].Int64("message_id"))

	v.fake.Unpin(ownerTG)
	_, err := v.db.Exec(`UPDATE telegram_panel SET pin_checked_at = now() - interval '1 hour'`)
	require.NoError(t, err)
	v.say("мир")
	results := v.lastSend()
	v.tap(&results, "Домой")
	pins = v.fake.Calls("pinChatMessage")
	assert.Equal(t, fresh, pins[len(pins)-1].Int64("message_id"), "pinned again on «Домой»")
}

// (m) What is not a search gets its hint, and nothing reaches the search.
func TestWhatIsNotASearch(t *testing.T) {
	v := newV2(t)
	views := v.count(`SELECT count(*) FROM telegram_view`)
	v.say("/foo")
	assert.Equal(t, "Команды здесь не нужны. Напиши название книги или имя автора.", lastText(v))
	v.say(strings.Repeat("я", 201))
	assert.Contains(t, lastText(v), "Запрос слишком длинный.")
	v.say("   ")
	assert.Equal(t, "Напиши название книги или имя автора.", lastText(v))
	sticker := v.u.Text(ownerTG, "")
	sticker.Message.Sticker = &tgbot.Sticker{FileID: "s"}
	v.post(sticker)
	assert.Equal(t, "Для поиска нужно название книги или имя автора текстом.", lastText(v))
	assert.Equal(t, views, v.count(`SELECT count(*) FROM telegram_view`))
}

func lastText(v *v2Stack) string {
	last := v.lastSend()
	text := content(&last)
	if last.Method == "sendRichMessage" {
		var rich struct {
			Blocks []struct {
				Text json.RawMessage `json:"text"`
			} `json:"blocks"`
		}
		_ = json.Unmarshal([]byte(text), &rich)
		var parts []string
		for _, b := range rich.Blocks {
			var s string
			if json.Unmarshal(b.Text, &s) == nil {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	return text
}

// (n) A tampered, foreign or old-generation button changes nothing.
func TestAForgedButtonIsRefused(t *testing.T) {
	v := newV2(t)
	v.addBooks("Основание")
	v.say("Основание")
	panel := v.lastSend()
	data, ok := tgtest.ButtonData(t, &panel, "→")
	require.True(t, ok)
	revision := v.count(`SELECT revision FROM telegram_panel WHERE message_id = ?`, panel.MessageID)

	tampered := []byte(data)
	tampered[len(tampered)-1] ^= 1
	if tampered[len(tampered)-1] == data[len(data)-1] {
		tampered[len(tampered)-1] = 'A'
	}
	v.post(v.u.Callback(ownerTG, &panel, string(tampered)))
	answers := v.fake.Calls("answerCallbackQuery")
	assert.Equal(t, "Эта кнопка недоступна.", answers[len(answers)-1].Params["text"])

	_, err := v.db.Exec(`UPDATE telegram_binding SET generation = generation + 1`)
	require.NoError(t, err)
	v.post(v.u.Callback(ownerTG, &panel, data))
	answers = v.fake.Calls("answerCallbackQuery")
	assert.Equal(t, "Эта кнопка недоступна.", answers[len(answers)-1].Params["text"])

	assert.Equal(t, revision, v.count(`SELECT revision FROM telegram_panel WHERE message_id = ?`, panel.MessageID))
	assert.Empty(t, v.edits(panel.MessageID))
}

// (o) The old /b and /a still search, unadvertised.
func TestTheOldSearchCommandsStillSearch(t *testing.T) {
	v := newV2(t)
	v.say("/b Мир")
	books := v.lastSend()
	assert.Contains(t, content(&books), "Поиск: «Мир»")
	assert.Equal(t, "primary", tgtest.RichButtons(t, &books)[0].Style, "the books tab is open")
	v.say("/a Толстой")
	authors := v.lastSend()
	assert.Equal(t, "primary", tgtest.RichButtons(t, &authors)[1].Style, "the authors tab is open")
}

// assertDisabled checks that the button with text on c is a disabled one
// (plan.md P1-6): no callback, no link, no style — a press does nothing.
func assertDisabled(t *testing.T, c *tgtest.Call, text string) {
	t.Helper()
	for _, row := range tgtest.Keyboard(t, c) {
		for i := range row {
			b := &row[i]
			if b.Text != text {
				continue
			}
			assert.NotNil(t, b.Disabled, "%q is disabled", text)
			assert.Empty(t, b.CallbackData, "%q carries no callback", text)
			assert.Empty(t, b.URL)
			assert.Empty(t, b.Style)
			return
		}
	}
	t.Fatalf("no button %q: %v", text, tgtest.ButtonTexts(t, c))
}
