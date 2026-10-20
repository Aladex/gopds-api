package telegram_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice P1-3 (plan.md §6 P1-3): the books Telegram confirmed it delivered,
// listed newest first, to send again; kept or not, and cleared, from the
// settings.

// homeAgain brings the home panel back to the home screen: «Домой», or the
// way back to the settings first.
func (v *v2Stack) homeAgain() tgtest.Call {
	v.t.Helper()
	for range 3 {
		home := v.homePanel()
		labels := tgtest.ButtonTexts(v.t, &home)
		has := func(label string) bool {
			for _, row := range labels {
				for _, l := range row {
					if l == label {
						return true
					}
				}
			}
			return false
		}
		switch {
		case has(screens.ButtonFavorites):
			return home
		case has(screens.ButtonHome):
			v.tap(&home, screens.ButtonHome)
		case has(screens.ButtonToSettings):
			v.tap(&home, screens.ButtonToSettings)
		default:
			v.t.Fatalf("no way home from %v", labels)
		}
	}
	return v.homePanel()
}

// openRecent opens S-RECENT from the home panel and returns it.
func (v *v2Stack) openRecent() tgtest.Call {
	v.t.Helper()
	home := v.homeAgain()
	v.tap(&home, screens.ButtonRecent)
	return v.latest(home.MessageID)
}

// openRecentSettings opens S-RECENT-SETTINGS from the home panel.
func (v *v2Stack) openRecentSettings() tgtest.Call {
	v.t.Helper()
	home := v.homeAgain()
	v.tap(&home, "Настройки")
	settings := v.latest(home.MessageID)
	v.tap(&settings, screens.ButtonRecent)
	return v.latest(home.MessageID)
}

// recentRows are the account's recent sends as stored.
func (v *v2Stack) recentRows() []models.TelegramRecent {
	v.t.Helper()
	var rows []models.TelegramRecent
	require.NoError(v.t, v.db.Model(&rows).Order("sent_at DESC").Select())
	return rows
}

// A confirmed send puts the book at the top, with its format; sent again in
// another format it moves up and keeps one row. A send from the site counts
// too.
func TestASentBookIsInTheRecentSends(t *testing.T) {
	v := newV2(t)
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	v.waitSentJobs(1)
	fixture := v.books[scanfixture.EntryMultiAuthor]
	rows := v.recentRows()
	require.Len(t, rows, 1)
	assert.Equal(t, fixture, rows[0].BookID)
	assert.Equal(t, "epub", rows[0].Format)

	opened := v.openRecent()
	recent := content(&opened)
	items := listItems(t, &opened)
	require.Len(t, items, 1)
	assert.Equal(t, 1, items[0].Value)
	assert.Equal(t, "война и МИР", items[0].Title, "the title opens the card")
	assert.Contains(t, recent, "EPUB · ")
	assert.Contains(t, recent, " UTC", "the time says its zone")

	card = v.latest(card.MessageID)
	v.tap(&card, "FB2")
	v.waitSentJobs(2)
	rows = v.recentRows()
	require.Len(t, rows, 1, "one row per book")
	assert.Equal(t, "fb2", rows[0].Format)

	other := v.books[scanfixture.EntryLatin]
	ws := v.siteSession(v.user)
	ws.ask(other)
	assert.Equal(t, "sent", ws.reply(15*time.Second).Status)
	rows = v.recentRows()
	require.Len(t, rows, 2)
	assert.Equal(t, other, rows[0].BookID, "the newest first")
	list := v.openRecent()
	assert.Equal(t, [][]string{{screens.ButtonHome}}, tgtest.ButtonTexts(t, &list))
	sends := links(t, &list)
	require.Len(t, sends, 4, "a title and its «Отправить» per send: %v", sends)
	assert.Equal(t, []string{"Отправить EPUB", "Отправить FB2"}, []string{sends[1], sends[3]})
}

// «Отправить» sends the book again in its last format, as a new job from the
// recent sends, by the bot's own file; «О книге» opens the card.
func TestResendFromTheRecentSends(t *testing.T) {
	v := newV2(t)
	_, card := v.openFixtureCard()
	v.tap(&card, "MOBI")
	v.waitSentJobs(1)
	first := v.recentRows()[0].SentAt

	list := v.openRecent()
	v.tap(&list, "Отправить MOBI")
	assert.Equal(t, "Отправляю MOBI…", v.lastAnswer(), "the bot has the file: nothing to prepare")
	v.waitSentJobs(2)
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_delivery WHERE origin = 'recent' AND format = 'mobi'`))
	assert.Equal(t, 1, resends(v.fake))
	assert.True(t, v.recentRows()[0].SentAt.After(first), "sent again, moved up")

	list = v.latest(list.MessageID)
	v.tap(&list, "война и МИР")
	opened := v.latest(list.MessageID)
	assert.Contains(t, content(&opened), "война и МИР")
	assert.Contains(t, tgtest.ButtonTexts(t, &opened)[len(tgtest.ButtonTexts(t, &opened))-1], screens.ButtonToRecent)
}

// A send again checks access now: a book withdrawn since is not sent.
func TestResendChecksAccessNow(t *testing.T) {
	v := newV2(t)
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	v.waitSentJobs(1)
	list := v.openRecent()
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, v.books[scanfixture.EntryMultiAuthor])
	require.NoError(t, err)
	v.tap(&list, "Отправить EPUB")
	assert.Equal(t, screens.CopyUnavailable, v.lastAnswer())
	assert.Equal(t, 1, v.count(`SELECT count(*) FROM telegram_delivery`), "no new job")
}

// A send that did not go out is not recent.
func TestAFailedSendIsNotRecent(t *testing.T) {
	v := newV2(t)
	v.fake.On(tgtest.Rule{Method: "sendDocument", Reply: tgtest.Error(http.StatusBadRequest, "Bad Request: file is too big")})
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	require.Eventually(t, func() bool {
		return v.count(`SELECT count(*) FROM telegram_delivery WHERE finished_at IS NOT NULL`) == 1
	}, 10*time.Second, 20*time.Millisecond)
	v.delivery.WaitIdle()
	assert.Empty(t, v.recentRows())
}

// «Не сохранять» stops new rows and keeps the list; «Сохранять» resumes.
func TestNotKeepingRecentSends(t *testing.T) {
	v := newV2(t)
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	v.waitSentJobs(1)

	settings := v.openRecentSettings()
	assert.Equal(t, "✓ "+screens.ButtonKeepRecent, tgtest.ButtonTexts(t, &settings)[0][0], "kept by default")
	v.tap(&settings, screens.ButtonNoRecent)
	assert.Equal(t, screens.CopyRecentNotKept, v.lastAnswer())
	settings = v.latest(settings.MessageID)
	assert.Equal(t, "✓ "+screens.ButtonNoRecent, tgtest.ButtonTexts(t, &settings)[1][0])

	_, card = v.openFixtureCard()
	v.tap(&card, "FB2")
	v.waitSentJobs(2)
	rows := v.recentRows()
	require.Len(t, rows, 1, "the list stays as it was")
	assert.Equal(t, "epub", rows[0].Format, "and nothing new is kept")

	settings = v.openRecentSettings()
	v.tap(&settings, screens.ButtonKeepRecent)
	assert.Equal(t, screens.CopyRecentKept, v.lastAnswer())
	_, card = v.openFixtureCard()
	v.tap(&card, "ZIP")
	v.waitSentJobs(3)
	assert.Equal(t, "zip", v.recentRows()[0].Format)
}

// Clearing asks first, with the one danger button; the list goes, the files
// in the chat stay.
func TestClearingTheRecentSends(t *testing.T) {
	v := newV2(t)
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	v.waitSentJobs(1)

	settings := v.openRecentSettings()
	v.tap(&settings, screens.ButtonClearRecent)
	confirm := v.latest(settings.MessageID)
	assert.Contains(t, content(&confirm), "Очистить недавние отправки?")
	keys := tgtest.Keyboard(t, &confirm)
	assert.Equal(t, "danger", keys[0][0].Style)
	v.tap(&confirm, screens.ButtonCancel)
	assert.Len(t, v.recentRows(), 1, "cancel clears nothing")

	settings = v.latest(settings.MessageID)
	v.tap(&settings, screens.ButtonClearRecent)
	confirm = v.latest(settings.MessageID)
	v.tap(&confirm, screens.ButtonClearRecent)
	assert.Equal(t, screens.CopyRecentCleared, v.lastAnswer())
	assert.Empty(t, v.recentRows())
	back := v.latest(settings.MessageID)
	assert.Contains(t, tgtest.ButtonTexts(t, &back), []string{screens.ButtonClearRecent}, "back at the recent settings")
	assert.Empty(t, v.fake.Calls("deleteMessage"), "no document is deleted")
	recent := v.openRecent()
	assert.Contains(t, content(&recent), "Здесь появятся отправленные книги")
}

// The list holds the last 50 books within 90 days, paged by five, and the
// janitor trims the rest: by age and by count, each on its own.
func TestTheRecentSendsAreTrimmed(t *testing.T) {
	v := newV2(t)
	ctx := context.Background()
	now := time.Now()
	add := func(bookID int64, sentAt time.Time) {
		t.Helper()
		_, err := v.db.Exec(`INSERT INTO telegram_recent (user_id, book_id, format, sent_at) VALUES (?, ?, 'epub', ?)`,
			v.user, bookID, sentAt)
		require.NoError(t, err)
	}
	add(1, now.Add(-91*24*time.Hour))
	for i := range 3 {
		add(int64(800000+i), now.Add(-time.Duration(i+1)*24*time.Hour))
	}
	_, total, err := database.ListTelegramRecent(ctx, v.user, 0, screens.PageSize)
	require.NoError(t, err)
	assert.Equal(t, 3, total, "a send older than 90 days is not listed")
	require.NoError(t, database.PruneTelegramRecent(ctx))
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_recent WHERE book_id = 1`), "and the janitor removes it")
	assert.Equal(t, 3, v.count(`SELECT count(*) FROM telegram_recent`))

	for i := range 55 {
		add(int64(900000+i), now.Add(-time.Duration(i)*time.Minute))
	}
	recent, total, err := database.ListTelegramRecent(ctx, v.user, 0, screens.PageSize)
	require.NoError(t, err)
	assert.Equal(t, database.TelegramRecentLimit, total, "50 at most, even before the janitor")
	assert.Len(t, recent, screens.PageSize)

	list := v.openRecent()
	assert.Contains(t, content(&list), "Страница 1 из 10")
	assert.Contains(t, content(&list), "Книга больше недоступна", "a book gone from the catalog")
	v.tap(&list, "→")
	list = v.latest(list.MessageID)
	assert.Contains(t, content(&list), "Страница 2 из 10")

	require.NoError(t, database.PruneTelegramRecent(ctx))
	assert.Equal(t, database.TelegramRecentLimit, v.count(`SELECT count(*) FROM telegram_recent`))
	assert.Zero(t, v.count(`SELECT count(*) FROM telegram_recent WHERE book_id BETWEEN 800000 AND 800002`),
		"the oldest past the newest 50 go")
}

// A send again that fails is told in the chat, like a press on a card.
func TestAFailedResendIsToldInTheChat(t *testing.T) {
	v := newV2(t)
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	v.waitSentJobs(1)
	list := v.openRecent()
	v.fake.On(tgtest.Rule{Method: "sendDocument", Reply: tgtest.Error(http.StatusBadRequest, "Bad Request: chat not found")})
	before := len(v.fake.Calls("sendMessage"))
	v.tap(&list, "Отправить EPUB")
	require.Eventually(t, func() bool {
		return v.count(`SELECT count(*) FROM telegram_delivery WHERE origin = 'recent' AND finished_at IS NOT NULL`) == 1
	}, 10*time.Second, 20*time.Millisecond)
	v.delivery.WaitIdle()
	calls := v.fake.Calls("sendMessage")
	require.Greater(t, len(calls), before, "the chat is told")
	told := calls[len(calls)-1]
	assert.Contains(t, tgtest.ButtonTexts(t, &told), []string{"Повторить EPUB"})
}

// The janitor trims by the list as it ranked it: a book whose send is
// confirmed while the trim waits on its row is kept, at the top
// (review-p1-3 B1, with the real runner and two sent jobs).
func TestTheJanitorKeepsASendConfirmedMeanwhile(t *testing.T) {
	d := openCard(t, newStack(t))
	d.pressEPUB(t)
	d.waitSentJobs(1)
	var initial models.TelegramRecent
	require.NoError(t, d.db.Model(&initial).Select())
	_, err := d.db.Exec(`UPDATE telegram_recent SET sent_at = now() - interval '1 hour' WHERE user_id = ? AND book_id = ?`,
		initial.UserID, d.bookID)
	require.NoError(t, err)
	// 50 newer sends: the book is the 51st, the janitor's to trim.
	_, err = d.db.Exec(`INSERT INTO telegram_recent (user_id, book_id, format, sent_at)
		SELECT ?, 100000 + n, 'epub', now() - make_interval(secs => n) FROM generate_series(1, 50) n`, initial.UserID)
	require.NoError(t, err)

	// A lock on the book's row only orders the two transactions: the send's
	// confirmation waits on it first, then the janitor's delete.
	blocker, err := d.db.Begin()
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.Exec(`SELECT 1 FROM telegram_recent WHERE user_id = ? AND book_id = ? FOR UPDATE`, initial.UserID, d.bookID)
	require.NoError(t, err)
	waiting := func(query string) func() bool {
		return func() bool {
			return d.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
				AND state = 'active' AND wait_event_type = 'Lock' AND query LIKE ?`, "%"+query+"%") > 0
		}
	}
	d.pressEPUB(t)
	require.Eventually(t, waiting("INSERT INTO telegram_recent"), 5*time.Second, 10*time.Millisecond,
		"the confirmed send waits on its row")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pruned := make(chan error, 1)
	go func() { pruned <- database.PruneTelegramRecent(ctx) }()
	require.Eventually(t, waiting("DELETE FROM telegram_recent r"), 5*time.Second, 10*time.Millisecond,
		"the janitor ranked the book 51st and waits on its row")
	require.NoError(t, blocker.Commit())
	d.waitSentJobs(2)
	select {
	case err := <-pruned:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the janitor never finished")
	}
	require.Len(t, d.fake.Calls("sendDocument"), 2)
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_recent WHERE user_id = ? AND book_id = ?`, initial.UserID, d.bookID),
		"the book just sent again stays")
	// The next trim ranks the book first, and the oldest of the others goes.
	require.NoError(t, database.PruneTelegramRecent(context.Background()))
	assert.Equal(t, database.TelegramRecentLimit, d.count(`SELECT count(*) FROM telegram_recent WHERE user_id = ?`, initial.UserID))
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_recent WHERE user_id = ? AND book_id = ?`, initial.UserID, d.bookID))
	assert.Zero(t, d.count(`SELECT count(*) FROM telegram_recent WHERE book_id = 100050`))
}
