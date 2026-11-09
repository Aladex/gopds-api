package screens

import (
	"strings"
	"testing"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testListKeys = &ListKeys{
	Page: func(int) *cbv2.Envelope { return env() },
	Book: func(int64) *cbv2.Envelope { return env() },
	Home: env(),
}

// A listed card says how many files it holds, as the site's chip on a list:
// after the second line, plain and rich; a single book's row is as it was.
func TestListRowSaysVariants(t *testing.T) {
	books := []models.Book{
		{ID: 1, Title: "Хоббит", Series: []*models.Series{{Ser: "Средиземье", SerNo: 1}}},
		{ID: 2, Title: "Ты дура"},
		{ID: 3, Title: "Сильмариллион", Series: []*models.Series{{Ser: "Средиземье", SerNo: 2}}},
	}
	list := &BookList{Books: books, Total: 3, Sub: SeriesSub, Variants: []*models.BookVariantsInfo{
		{Key: uuid.New(), Count: 6, Kind: models.BookVariantsVariants},
		{Key: uuid.New(), Count: 22, Kind: models.BookVariantsRevisions},
		nil,
	}}
	s := BookListScreen(list, testListKeys)
	text := s.Doc.Plain()
	assert.Contains(t, text, "1. Хоббит\n   Средиземье, кн. 1 · 6 вариантов\n")
	assert.Contains(t, text, "2. Ты дура\n   22 редакции\n")
	assert.True(t, strings.HasSuffix(text, "3. Сильмариллион\n   Средиземье, кн. 2"), text)

	require.NotNil(t, s.Rich)
	var items []Item
	for _, b := range s.Rich.Doc.Blocks {
		if l, ok := b.(List); ok {
			items = l.Items
		}
	}
	require.Len(t, items, 3)
	assert.Equal(t, "Средиземье, кн. 1 · 6 вариантов", items[0].Sub)
	assert.Equal(t, "22 редакции", items[1].Sub)
	assert.Equal(t, "Средиземье, кн. 2", items[2].Sub)

	single := BookListScreen(&BookList{Books: books, Total: 3, Sub: SeriesSub}, testListKeys)
	assert.Equal(t, strings.ReplaceAll(strings.ReplaceAll(text, " · 6 вариантов", ""), "\n   22 редакции", ""),
		single.Doc.Plain(), "without cards the rows are as they were")
}
