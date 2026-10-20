package screens

import (
	"testing"
	"time"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recentKeys() *RecentKeys {
	return &RecentKeys{
		Page:   func(int) *cbv2.Envelope { return env() },
		Resend: func(int64, string) *cbv2.Envelope { return env() },
		Open:   func(int64) *cbv2.Envelope { return env() },
		Home:   env(),
	}
}

// A page of recent sends: the book, its last format and the time in UTC —
// whatever zone the time came in — and a send again and the card per book.
func TestTheRecentSends(t *testing.T) {
	now := time.Now()
	moscow := time.FixedZone("MSK", 3*60*60)
	sent := now.Add(-2 * time.Hour).In(moscow)
	book := &models.Book{ID: 7, Title: "Основание", Authors: []models.Author{{FullName: "Айзек Азимов"}}}
	items := []RecentItem{
		{Book: book, BookID: 7, Format: "epub", SentAt: sent},
		{BookID: 8, Format: "fb2", SentAt: now.Add(-24 * time.Hour)},
	}
	s := RecentScreen(items, 7, 0, true, recentKeys())
	plain := s.Doc.Plain()
	assert.Contains(t, plain, "1. Основание — Айзек Азимов")
	assert.Contains(t, plain, "EPUB · "+sent.UTC().Format("02.01.2006, 15:04")+" UTC")
	assert.Contains(t, plain, "по UTC")
	assert.Contains(t, plain, "2. "+copyRecentGoneBook, "a book gone from the catalog is said to be")
	assert.Contains(t, plain, "Страница 1 из 2")
	assert.Equal(t, [][]string{{"1. Отправить EPUB", "О книге 1"}, {ButtonNext}, {ButtonHome}}, texts(&s),
		"a gone book has nothing to press")
}

// An empty list says what it will hold, and that nothing is kept when the
// keeping is off.
func TestNoRecentSends(t *testing.T) {
	kept := RecentScreen(nil, 0, 0, true, recentKeys())
	assert.Contains(t, kept.Doc.Plain(), "Здесь появятся отправленные книги")
	assert.NotContains(t, kept.Doc.Plain(), "не сохраняются")
	assert.Equal(t, [][]string{{ButtonHome}}, texts(&kept))
	off := RecentScreen(nil, 0, 0, false, recentKeys())
	assert.Contains(t, off.Doc.Plain(), "не сохраняются")
}

// The settings mark the choice; clearing is its own button, and its
// confirmation the one danger button.
func TestTheRecentSettings(t *testing.T) {
	k := &RecentSettingsKeys{Keep: func(bool) *cbv2.Envelope { return env() }, Clear: env(), Back: env()}
	on := RecentSettingsScreen(true, k)
	assert.Equal(t, [][]string{{"✓ " + ButtonKeepRecent}, {ButtonNoRecent}, {ButtonClearRecent}, {ButtonToSettings}}, texts(&on))
	off := RecentSettingsScreen(false, k)
	assert.Equal(t, "✓ "+ButtonNoRecent, texts(&off)[1][0])
	assert.Contains(t, off.Doc.Plain(), "Уже сохранённые остаются", "not keeping does not pretend to clear")

	confirm := ClearRecentScreen(env(), env())
	require.Len(t, confirm.Keys, 2)
	assert.Equal(t, "danger", confirm.Keys[0][0].Style)
	assert.Equal(t, ButtonClearRecent, confirm.Keys[0][0].Text)
	assert.Empty(t, confirm.Keys[1][0].Style)
	assert.Contains(t, confirm.Doc.Plain(), "Файлы в чате Telegram останутся.")
}
