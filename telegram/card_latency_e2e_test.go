package telegram_test

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/posters"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Opening a card (brief-author-card-latency.md): the press is answered before
// the card is drawn, and a card whose cover Telegram does not have yet is
// shown at once without it, then in full.

// fixtureTitle is the fixture book's title as a results page links it.
func fixtureTitle(t *testing.T, results *tgtest.Call) string {
	t.Helper()
	for _, text := range tgtest.Labels(t, results) {
		if strings.Contains(text, "война и МИР") {
			return text
		}
	}
	t.Fatalf("no fixture book in %v", tgtest.Labels(t, results))
	return ""
}

// callsSince are the calls of methods made at or after since, in order.
func (v *v2Stack) callsSince(since time.Time, methods ...string) []tgtest.Call {
	var out []tgtest.Call
	for _, m := range methods {
		for _, c := range v.fake.Calls(m) {
			if !c.At.Before(since) {
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func methodsOf(calls []tgtest.Call) []string {
	out := make([]string, 0, len(calls))
	for i := range calls {
		out = append(out, calls[i].Method)
	}
	return out
}

// pressMethods are the calls a press of a title makes.
var pressMethods = []string{"answerCallbackQuery", "editMessageText", "sendChatAction"}

// (a) A cold cover: the press is answered first, without a word; the card
// is shown opening, the chat says a photo is coming, then the card arrives
// with its cover uploaded.
func TestACardWithAColdCoverOpensAtOnceThenInFull(t *testing.T) {
	v := newV2(t)
	v.giveCover(v.books[scanfixture.EntryMultiAuthor])
	v.say("мир")
	results := v.lastSend()
	since := time.Now()
	v.tap(&results, fixtureTitle(t, &results))

	calls := v.callsSince(since, pressMethods...)
	require.Equal(t, []string{"answerCallbackQuery", "editMessageText", "sendChatAction", "editMessageText"}, methodsOf(calls))
	assert.Empty(t, calls[0].Params["text"], "answered without a toast")

	opening := calls[1]
	assert.Contains(t, content(&opening), screens.CopyOpeningCard)
	assert.Contains(t, content(&opening), "война и МИР")
	assert.NotContains(t, opening.Files, "cover", "nothing uploaded yet")
	assert.Equal(t, [][]string{{screens.ButtonToResults, screens.ButtonHome}}, tgtest.ButtonTexts(t, &opening))

	assert.Equal(t, "upload_photo", calls[2].Params["action"])

	card := calls[3]
	assert.Equal(t, "attach://cover", richPhotoMedia(t, &card))
	assert.Contains(t, card.Files, "cover")
	assert.Equal(t, []string{"EPUB", "FB2", "MOBI", "ZIP"}, tgtest.ButtonTexts(t, &card)[0])
	assert.Equal(t, opening.Int64("message_id"), card.Int64("message_id"))
	opened, _ := tgtest.ButtonData(t, &opening, screens.ButtonHome)
	full, _ := tgtest.ButtonData(t, &card, screens.ButtonHome)
	assert.Equal(t, opened, full, "one revision: the opening's buttons stay good")
}

// (b) A warm cover — this bot was given it before — is not uploaded or
// read again: one edit, by its file_id, after the answer.
func TestACardWithAWarmCoverOpensInOneEdit(t *testing.T) {
	v := newV2(t)
	v.giveCover(v.books[scanfixture.EntryMultiAuthor])
	results, card := v.openFixtureCard()
	require.Contains(t, card.Files, "cover", "the first open uploads")
	v.tap(&card, screens.ButtonToResults)
	back := v.latest(results.MessageID)

	since := time.Now()
	v.tap(&back, fixtureTitle(t, &back))
	calls := v.callsSince(since, pressMethods...)
	require.Equal(t, []string{"answerCallbackQuery", "editMessageText"}, methodsOf(calls))
	assert.Empty(t, calls[0].Params["text"])
	assert.True(t, strings.HasPrefix(richPhotoMedia(t, &calls[1]), "photo-"), "by its file_id")
	assert.Empty(t, calls[1].Files, "no upload")
}

// (c) Without a cover, or with plain cards, there is nothing to wait for:
// one edit, after the answer.
func TestACardWithoutACoverOrPlainOpensInOneEdit(t *testing.T) {
	// A stack each: one test's stack owns the database until its cleanup.
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "no cover", true: "plain cards"}[plain], func(t *testing.T) {
			v := newV2(t)
			if plain {
				v.plainCards()
				v.giveCover(v.books[scanfixture.EntryMultiAuthor])
			}
			v.say("мир")
			results := v.lastSend()
			since := time.Now()
			v.tap(&results, fixtureTitle(t, &results))
			assert.Equal(t, []string{"answerCallbackQuery", "editMessageText"}, methodsOf(v.callsSince(since, pressMethods...)))
		})
	}
}

// (d) A book gone from the catalog still says so: the press is answered
// with the toast, and the panel stays.
func TestAGoneBooksTitleStillSaysSo(t *testing.T) {
	v := newV2(t)
	v.say("мир")
	results := v.lastSend()
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, v.books[scanfixture.EntryMultiAuthor])
	require.NoError(t, err)
	since := time.Now()
	v.tap(&results, fixtureTitle(t, &results))
	calls := v.callsSince(since, pressMethods...)
	require.Equal(t, []string{"answerCallbackQuery"}, methodsOf(calls))
	assert.Equal(t, screens.CopyUnavailable, calls[0].Params["text"])
}

// (e) A press on the opening card while its cover is still uploading waits
// for the chat, then wins: the way back is not refused as an old screen.
func TestANewerPressDuringAColdOpenWins(t *testing.T) {
	v := newV2(t)
	v.giveCover(v.books[scanfixture.EntryMultiAuthor])
	v.say("мир")
	results := v.lastSend()
	gate := make(chan struct{})
	v.fake.On(tgtest.Rule{Method: "editMessageText", Times: 1, Reply: v.fake.Gate(gate),
		Match: func(c tgtest.Call) bool { _, upload := c.Files["cover"]; return upload }})

	opened := make(chan struct{})
	go func() { v.tap(&results, fixtureTitle(t, &results)); close(opened) }()
	var opening tgtest.Call
	require.Eventually(t, func() bool {
		for _, c := range v.fake.AnsweredCalls("editMessageText") {
			if strings.Contains(content(&c), screens.CopyOpeningCard) {
				opening = c
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the opening card is shown while the cover uploads")

	back := v.u.Tap(t, ownerTG, &opening, screens.ButtonToResults)
	pressed := make(chan struct{})
	go func() { v.post(back); close(pressed) }()
	time.Sleep(100 * time.Millisecond) // the press reaches the chat while the upload holds it
	close(gate)
	<-opened
	<-pressed

	latest := v.latest(results.MessageID)
	assert.Contains(t, content(&latest), "Поиск: «мир»", "the newer press won")
	for _, a := range v.fake.Calls("answerCallbackQuery") {
		assert.NotEqual(t, screens.CopyScreenChanged, a.Params["text"], "the opening's button is not an old screen's")
	}
}

// (f) A warm open does not read the cover file: Telegram has it. A file the
// library can no longer read still shows by its file_id.
func TestAWarmOpenDoesNotReadTheCoverFile(t *testing.T) {
	v := newV2(t)
	bookID := v.books[scanfixture.EntryMultiAuthor]
	v.giveCover(bookID)
	results, card := v.openFixtureCard()
	require.Contains(t, card.Files, "cover")
	v.tap(&card, screens.ButtonToResults)
	back := v.latest(results.MessageID)

	var book models.Book
	require.NoError(t, v.db.Model(&book).Where("id = ?", bookID).Select())
	path := posters.FilePath(v.posters, book.Path, book.FileName)
	require.NoError(t, os.Chmod(path, 0))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("the file stays readable (running as root)")
	}

	v.tap(&back, fixtureTitle(t, &back))
	warm := v.latest(results.MessageID)
	assert.True(t, strings.HasPrefix(richPhotoMedia(t, &warm), "photo-"), "the cover by its file_id")
}

// (g) The home panel opens a cold card the same way: from the favorites, the
// answer, the opening card, the chat action, the card.
func TestTheHomePanelOpensAColdCardAtOnceToo(t *testing.T) {
	v := newV2(t)
	home := v.favoriteFixture()
	v.giveCover(v.books[scanfixture.EntryMultiAuthor])
	v.tap(&home, "★ Избранное")
	favs := v.latest(home.MessageID)
	since := time.Now()
	v.tap(&favs, links(t, &favs)[0])
	calls := v.callsSince(since, pressMethods...)
	require.Equal(t, []string{"answerCallbackQuery", "editMessageText", "sendChatAction", "editMessageText"}, methodsOf(calls))
	assert.Contains(t, content(&calls[1]), screens.CopyOpeningCard)
	assert.Equal(t, [][]string{{screens.ButtonToFavorites, screens.ButtonHome}}, tgtest.ButtonTexts(t, &calls[1]))
	assert.Equal(t, "attach://cover", richPhotoMedia(t, &calls[3]))
	assert.Equal(t, "rich", v.homeMode(home.MessageID))
}
