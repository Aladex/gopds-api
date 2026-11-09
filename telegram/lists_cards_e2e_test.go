package telegram_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gopds-api/telegram/screens"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot's scoped lists list cards; the recent sends stay a list of files.

// A book's files sent one after the other are two sends, two rows: the
// recent list is the history of files that went out, not of books.
func TestRecentStaysPerFile(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	files := v.variantCard("Солярис", solaris)
	now := time.Now()
	for i, id := range files[:2] {
		_, err := v.db.Exec(`INSERT INTO telegram_recent (user_id, book_id, format, sent_at) VALUES (?, ?, 'epub', ?)`,
			v.user, id, now.Add(-time.Duration(i+1)*time.Minute))
		require.NoError(t, err)
	}
	recent := v.openRecent()
	assert.Equal(t, 2, strings.Count(content(&recent), "Солярис"), content(&recent))
}

// The author's list groups its files; a list's page button sent before the
// release, now past the list's end, opens the last page and says so.
func TestAnOldPageButtonAfterGrouping(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	var specs []fileSpec
	for i := 0; i < 12; i++ {
		specs = append(specs, fileSpec{doc: fmt.Sprintf("twelve-%d", i), version: "1.0",
			translator: fmt.Sprintf("Переводчик %d", i), year: 2000 + i})
	}
	v.variantCard("Солярис", specs)

	// The list as the release before showed it: twelve files, three pages.
	off := *v
	off.stack = func() *stack { s := *v.stack; s.cardsOff = true; return s.start() }()
	card := off.openFile("Солярис", 0)
	off.tap(&card, screens.ButtonAuthorBooks)
	list := off.latest(card.MessageID)
	require.Contains(t, content(&list), "12 книг")
	off.tap(&list, screens.ButtonNext)
	list = off.latest(card.MessageID)
	off.tap(&list, screens.ButtonNext)
	list = off.latest(card.MessageID)
	require.Contains(t, content(&list), "Страница 3 из 3")

	// Pressed after the release: one card, one page.
	v.tap(&list, screens.ButtonPrev)
	list = v.latest(card.MessageID)
	assert.Contains(t, content(&list), "1 книга")
	assert.Contains(t, content(&list), "12 вариантов")
	assert.NotContains(t, content(&list), "Страница")
	assert.Contains(t, v.answerTexts(), "Список изменился. Открыта последняя страница.")
}
