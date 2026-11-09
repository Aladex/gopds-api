package screens

import (
	"strings"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tableOf is the card's table rows, as label → value, in order.
func tableOf(t *testing.T, s *Screen) [][2]string {
	t.Helper()
	for _, b := range s.Doc.Blocks {
		if table, ok := b.(Table); ok {
			return table.Rows
		}
	}
	return nil
}

// The card names the translators as the site's colophon does: the names as
// credited, two at most, «и др.» for the rest, after the year; no row
// without a translator. The same table goes out plain and rich.
func TestCardTableTranslator(t *testing.T) {
	book := &models.Book{ID: 7, Title: "Хоббит", Lang: "ru", DocDate: time.Now().AddDate(-20, 0, 0).Format(time.DateOnly)}
	cases := []struct {
		name  string
		names []string
		more  bool
		want  string
	}{
		{"two names", []string{"Н. Прохорова", "А. Иванов"}, false, "Н. Прохорова, А. Иванов"},
		{"a team", []string{"Н. Прохорова", "А. Иванов"}, true, "Н. Прохорова, А. Иванов и др."},
		{"one name", []string{"Нора Галь"}, false, "Нора Галь"},
	}
	for _, tc := range cases {
		for _, rich := range []bool{false, true} {
			s, _ := CardScreen(&Card{Book: book, Rich: rich, DefaultFormat: FormatEPUB,
				Translators: tc.names, TranslatorsMore: tc.more}, testCardKeys)
			rows := tableOf(t, &s)
			require.NotEmpty(t, rows, tc.name)
			last := rows[len(rows)-1]
			assert.Equal(t, [2]string{"Перевод", tc.want}, last, "%s, rich %t", tc.name, rich)
			assert.Equal(t, "Год", rows[len(rows)-2][0], "%s: the translator follows the year", tc.name)
		}
	}

	s, _ := CardScreen(&Card{Book: book, DefaultFormat: FormatEPUB}, testCardKeys)
	for _, row := range tableOf(t, &s) {
		assert.NotEqual(t, "Перевод", row[0], "no translator, no row")
	}
	assert.NotContains(t, s.Doc.Plain(), "Перевод")
}

// The translator is part of the card's head, so a plain card still fits.
func TestTheWorstBookWithATeamStillFits(t *testing.T) {
	team := []string{strings.Repeat("Переводчик ", 30), strings.Repeat("Переводчица ", 30)}
	plain, _ := CardScreen(&Card{Book: worstBook(), DefaultFormat: FormatEPUB, SiteURL: "https://x",
		Translators: team, TranslatorsMore: true}, testCardKeys)
	assert.LessOrEqual(t, plain.Doc.PlainLength(), 4096)
}
