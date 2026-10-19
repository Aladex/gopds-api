package telegram_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/internal/safeio"
	"gopds-api/internal/scanfixture"
	"gopds-api/services/kindlesend"
	"gopds-api/telegram/tgtest"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P1-8: Kindle from the bot's card, through the site's send core.

// kindleRelay is the relay and the conversion, faked: what was mailed, and
// what a conversion or a mail answers.
type kindleRelay struct {
	mu      sync.Mutex
	sent    []email.KindleDelivery
	bookErr error
	mailErr error
	// gate, when set, holds every conversion until it is closed.
	gate chan struct{}
}

func (k *kindleRelay) mailed() []email.KindleDelivery {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]email.KindleDelivery(nil), k.sent...)
}

// withKindle gives the stack a Kindle sender on the relay, with its own
// queue, and the sender domain the site has.
func withKindle(relay *kindleRelay) func(*stack) {
	return func(s *stack) {
		previous := viper.GetString("kindle.sender_domain")
		viper.Set("kindle.sender_domain", "booksdump.test")
		s.t.Cleanup(func() { viper.Set("kindle.sender_domain", previous) })
		s.kindle = &kindlesend.Sender{
			Queue:  kindlesend.NewQueue(kindlesend.Concurrency, kindlesend.Admission),
			Target: database.GetKindleTarget,
			Book: func(int64) (kindlesend.Book, error) {
				if relay.gate != nil {
					<-relay.gate
				}
				return kindlesend.Book{Title: "война и МИР", Filename: "war.epub", Data: []byte("PK-epub")}, relay.bookErr
			},
			Mail: func(d email.KindleDelivery) error {
				relay.mu.Lock()
				defer relay.mu.Unlock()
				if relay.mailErr != nil {
					return relay.mailErr
				}
				relay.sent = append(relay.sent, d)
				return nil
			},
		}
	}
}

const readerKindle = "reader@kindle.com"

// setKindle gives the account the Kindle address readerKindle, as the site
// does.
func (v *v2Stack) setKindle() {
	v.t.Helper()
	_, err := database.SetKindleEmail(v.user, readerKindle)
	require.NoError(v.t, err)
}

// The card offers «Kindle» only with an address; it asks first, then mails
// the EPUB to the address from the reader's sender, and says so.
func TestKindleFromTheCard(t *testing.T) {
	relay := &kindleRelay{}
	v := newV2(t, withKindle(relay))
	_, card := v.openFixtureCard()
	assert.False(t, hasButton(t, &card, "Kindle"), "no address, no button")

	v.setKindle()
	results, card := v.openFixtureCard()
	require.True(t, hasButton(t, &card, "Kindle"), "%v", tgtest.ButtonTexts(t, &card))
	v.tap(&card, "Kindle")
	confirm := v.latest(results.MessageID)
	assert.Contains(t, content(&confirm), "Отправить «война и МИР» на Kindle "+readerKindle+"?")
	assert.Equal(t, [][]string{{"Отправить", "Отмена"}}, tgtest.ButtonTexts(t, &confirm))
	assert.Empty(t, relay.mailed(), "nothing before the confirmation")

	v.tap(&confirm, "Отмена")
	back := v.latest(results.MessageID)
	assert.True(t, hasButton(t, &back, "Kindle"), "«Отмена» is the card again")

	v.tap(&back, "Kindle")
	confirm = v.latest(results.MessageID)
	v.tap(&confirm, "Отправить")
	v.bm.WaitIdle()
	v.bm.WaitSends()
	outcome := v.latest(results.MessageID)
	assert.Contains(t, content(&outcome), "Отправлено на Kindle. Книга появится на устройстве через несколько минут.")
	assert.Equal(t, [][]string{{"← К книге", "Домой"}}, tgtest.ButtonTexts(t, &outcome))
	mailed := relay.mailed()
	require.Len(t, mailed, 1)
	assert.Equal(t, readerKindle, mailed[0].To)
	assert.True(t, strings.HasSuffix(mailed[0].From, "@booksdump.test"), mailed[0].From)
	assert.Equal(t, "application/epub+zip", mailed[0].Book.ContentType, "EPUB only")
}

// A second press of the same «Отправить», or a second card's, sends nothing
// more: the first is stale, the second finds the book on its way.
func TestKindleTwiceSendsOnce(t *testing.T) {
	relay := &kindleRelay{gate: make(chan struct{})}
	v := newV2(t, withKindle(relay))
	v.setKindle()
	results, card := v.openFixtureCard()
	v.tap(&card, "Kindle")
	confirm := v.latest(results.MessageID)
	other, otherCard := v.openFixtureCard()
	v.tap(&otherCard, "Kindle")
	otherConfirm := v.latest(other.MessageID)

	v.tap(&confirm, "Отправить")
	sending := v.latest(results.MessageID)
	assert.Contains(t, content(&sending), "Отправляю на Kindle…")
	v.tap(&confirm, "Отправить")
	assert.Equal(t, "Этот экран уже изменился.", v.lastAnswer(), "the same press again is stale")
	v.tap(&otherConfirm, "Отправить")
	inProgress := v.latest(other.MessageID)
	assert.Contains(t, content(&inProgress), "Эта книга уже отправляется на Kindle.")

	close(relay.gate)
	v.bm.WaitIdle()
	v.bm.WaitSends()
	assert.Len(t, relay.mailed(), 1)
	done := v.latest(results.MessageID)
	assert.Contains(t, content(&done), "Отправлено на Kindle")
}

// The send's failures in words.
func TestKindleOutcomesInWords(t *testing.T) {
	for name, c := range map[string]struct {
		relay *kindleRelay
		want  string
	}{
		"too large":         {&kindleRelay{bookErr: safeio.ErrTooLarge}, "Книга слишком большая для Kindle — скачай EPUB."},
		"conversion failed": {&kindleRelay{bookErr: errors.New("broken fb2")}, "Не удалось подготовить EPUB для Kindle."},
		"send failed":       {&kindleRelay{mailErr: errors.New("relay down")}, "Не удалось отправить на Kindle. Попробуй позже."},
	} {
		t.Run(name, func(t *testing.T) {
			v := newV2(t, withKindle(c.relay))
			v.setKindle()
			results, card := v.openFixtureCard()
			v.tap(&card, "Kindle")
			confirm := v.latest(results.MessageID)
			v.tap(&confirm, "Отправить")
			v.bm.WaitIdle()
			v.bm.WaitSends()
			outcome := v.latest(results.MessageID)
			assert.Contains(t, content(&outcome), c.want)
		})
	}
}

// The book is checked again at «Отправить»: one hidden since the question
// is not sent.
func TestKindleRechecksTheBook(t *testing.T) {
	relay := &kindleRelay{}
	v := newV2(t, withKindle(relay))
	v.setKindle()
	results, card := v.openFixtureCard()
	v.tap(&card, "Kindle")
	confirm := v.latest(results.MessageID)
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, v.books[scanfixture.EntryMultiAuthor])
	require.NoError(t, err)
	v.tap(&confirm, "Отправить")
	v.bm.WaitIdle()
	v.bm.WaitSends()
	assert.Equal(t, "Эта книга больше недоступна.", v.lastAnswer())
	assert.Empty(t, relay.mailed())
}

// A panel moved on while the book was on its way gets nothing; the outcome
// comes as a message.
func TestAKindleOutcomeForAMovedPanelIsAMessage(t *testing.T) {
	relay := &kindleRelay{gate: make(chan struct{})}
	v := newV2(t, withKindle(relay))
	v.setKindle()
	results, card := v.openFixtureCard()
	v.tap(&card, "Kindle")
	confirm := v.latest(results.MessageID)
	v.tap(&confirm, "Отправить")
	sending := v.latest(results.MessageID)
	v.tap(&sending, "← К книге")
	edits := len(v.edits(results.MessageID))
	close(relay.gate)
	v.bm.WaitIdle()
	v.bm.WaitSends()
	assert.Len(t, v.edits(results.MessageID), edits, "the card is left as it is")
	assert.Contains(t, v.messagesTo(ownerTG), "Отправлено на Kindle. Книга появится на устройстве через несколько минут.")
}

// Settings name the address, or that there is none, and lead to the site.
func TestSettingsShowTheKindleAddress(t *testing.T) {
	v := newV2(t, withKindle(&kindleRelay{}))
	home := v.homePanel()
	v.tap(&home, "Настройки")
	settings := v.homePanel()
	assert.Contains(t, content(&settings), "Kindle: не настроен")
	var setup string
	for _, row := range rowsWithURLs(t, &settings) {
		if row.text == "Настроить Kindle на сайте" {
			setup = row.url
		}
	}
	assert.Equal(t, stackSiteURL+"/?profile=kindle", setup)

	v.setKindle()
	v.tap(&settings, "Домой")
	home = v.homePanel()
	v.tap(&home, "Настройки")
	settings = v.homePanel()
	assert.Contains(t, content(&settings), "Kindle: "+readerKindle)
}

// urlButton is a button's label and URL.
type urlButton struct{ text, url string }

// rowsWithURLs lists a message's URL buttons.
func rowsWithURLs(t *testing.T, c *tgtest.Call) []urlButton {
	t.Helper()
	var markup struct {
		InlineKeyboard [][]struct {
			Text string `json:"text"`
			URL  string `json:"url"`
		} `json:"inline_keyboard"`
	}
	require.NoError(t, json.Unmarshal([]byte(c.Params["reply_markup"]), &markup))
	var out []urlButton
	for _, row := range markup.InlineKeyboard {
		for _, b := range row {
			if b.URL != "" {
				out = append(out, urlButton{b.Text, b.URL})
			}
		}
	}
	return out
}

// convertingThen holds the conversion until released, and says when it has
// started: the send has passed its checks by then.
func convertingThen(entered chan struct{}) func(*stack) {
	return func(s *stack) {
		book := s.kindle.Book
		s.kindle.Book = func(id int64) (kindlesend.Book, error) { close(entered); return book(id) }
	}
}

// A book withdrawn while it converts is not mailed: the reviewer's
// TestReviewKindleRechecksVisibilityAfterConversion.
func TestKindleRechecksTheBookBeforeMail(t *testing.T) {
	relay := &kindleRelay{gate: make(chan struct{})}
	var release sync.Once
	letGo := func() { release.Do(func() { close(relay.gate) }) }
	defer letGo()
	entered := make(chan struct{})
	v := newV2(t, withKindle(relay), convertingThen(entered))
	v.setKindle()
	results, card := v.openFixtureCard()
	v.tap(&card, "Kindle")
	confirm := v.latest(results.MessageID)
	v.tap(&confirm, "Отправить")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the conversion did not start")
	}
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, v.books[scanfixture.EntryMultiAuthor])
	require.NoError(t, err)
	letGo()
	v.bm.WaitSends()
	assert.Empty(t, relay.mailed(), "a book withdrawn during its conversion is not mailed")
	outcome := v.latest(results.MessageID)
	assert.Contains(t, content(&outcome), "Эта книга больше недоступна.")
}

// A bot relinked while the book converts sends nothing for the old owner:
// the reviewer's TestReviewKindleRechecksBindingAfterConversion.
func TestKindleRechecksTheBindingBeforeMail(t *testing.T) {
	relay := &kindleRelay{gate: make(chan struct{})}
	var release sync.Once
	letGo := func() { release.Do(func() { close(relay.gate) }) }
	defer letGo()
	entered := make(chan struct{})
	v := newV2(t, withKindle(relay), convertingThen(entered))
	v.setKindle()
	_, card := v.openFixtureCard()
	v.tap(&card, "Kindle")
	confirm := v.latest(card.MessageID)
	v.tap(&confirm, "Отправить")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the conversion did not start")
	}
	secret, _ := v.connect(v.user, botToken1)
	rec := v.hook(botToken1).Post(t, v.u.Text(ownerTG+1, "/start link_"+secret))
	require.Equal(t, http.StatusOK, rec.Code)
	v.bm.WaitIdle()
	require.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_binding
		WHERE status = 'active' AND generation = 2 AND tg_user_id = ?`, ownerTG+1))
	letGo()
	v.bm.WaitSends()
	assert.Empty(t, relay.mailed(), "a revoked binding does not authorize the mail once the book is converted")
}
