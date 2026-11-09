package telegram_test

import (
	"testing"

	"gopds-api/internal/scanfixture"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// creditTranslators writes translator credits on the book's current
// snapshot, in the order given, each at its position.
func (v *v2Stack) creditTranslators(bookID int64, credits map[int]string, order ...int) {
	v.t.Helper()
	var snapshot int64
	_, err := v.db.QueryOne(pg.Scan(&snapshot),
		`SELECT id FROM book_metadata_snapshot WHERE book_id = ? AND is_current`, bookID)
	require.NoError(v.t, err, "the fixture book has a current snapshot")
	for _, position := range order {
		name := credits[position]
		_, err = v.db.Exec(`INSERT INTO book_contributor_credit
			(snapshot_id, role, position, source_display_name, source_fingerprint)
			VALUES (?, 'translator', ?, ?, sha256(convert_to(? || ?::text, 'UTF8')))`,
			snapshot, position, name, name, position)
		require.NoError(v.t, err)
	}
}

// The card names the book's translators the way the site's colophon does:
// by credit position, two names, «и др.» when a third remains. The fixture
// book credits Пётр Переводчиков at position 0; more credits are written
// out of position order.
func TestCardShowsTheTranslator(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	bookID := v.books[scanfixture.EntryMultiAuthor]

	_, card := v.openFixtureCard()
	assert.Contains(t, content(&card), "Пётр Переводчиков", "the fixture's own translator")

	v.creditTranslators(bookID, map[int]string{10: "Вера Третья", 12: "Анна Первая"}, 12, 10)
	results, _ := v.openFixtureCard()
	card = v.latest(results.MessageID)
	text := content(&card)
	assert.Contains(t, text, "Перевод")
	assert.Contains(t, text, "Пётр Переводчиков, Вера Третья и др.")
	assert.NotContains(t, text, "Анна Первая", "two names at most")
}

// Without a source lookup the card has no translator row, and still opens.
func TestCardWithoutSourcesHasNoTranslator(t *testing.T) {
	v := newV2(t, func(s *stack) { s.noSources = true })
	v.plainCards()

	_, card := v.openFixtureCard()
	text := content(&card)
	assert.Contains(t, text, "война и МИР")
	assert.NotContains(t, text, "Перевод")
	assert.NotContains(t, text, "Пётр Переводчиков")
}
