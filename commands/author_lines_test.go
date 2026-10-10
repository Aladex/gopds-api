package commands

import (
	"context"
	"testing"

	"gopds-api/models"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot's book lists name each book's authors by its author line, read
// once per page. The selection buttons point at books, never at authors, so
// a name without a catalog author costs no button.

type pageLookup struct {
	calls [][]int64
	lines map[int64]models.BookAuthorDisplay
}

func (l *pageLookup) BookAuthorDisplay(_ context.Context, ids []int64) (map[int64]models.BookAuthorDisplay, error) {
	l.calls = append(l.calls, append([]int64(nil), ids...))
	return l.lines, nil
}

func layerLineOf(names ...string) models.BookAuthorDisplay {
	authors := make([]models.AuthorDisplay, len(names))
	for i, n := range names {
		authors[i] = models.AuthorDisplay{Name: n}
	}
	return models.BookAuthorDisplay{Source: models.AuthorDisplayLayer, Authors: authors}
}

func TestBotSearchListsShowTheAuthorLine(t *testing.T) {
	user := &models.User{ID: 7}
	cases := []struct {
		name string
		run  func(cp *CommandProcessor) (*CommandResult, error)
		want string
	}{
		{"title search", func(cp *CommandProcessor) (*CommandResult, error) {
			return cp.ExecuteDirectBookSearch(context.Background(), "война", 1)
		}, "📚 Результаты поиска для \"война\":\nСтраница 1 из 1 (всего найдено 2 книг)\n\n" +
			"1. Book 1 — Лев Николаевич Толстой, Мастер\n" +
			"2. Book 2 — Author 2\n\n" +
			"💡 Выберите книгу по номеру или используйте навигацию:"},
		{"title and author search", func(cp *CommandProcessor) (*CommandResult, error) {
			return cp.ExecuteDirectCombinedSearch(context.Background(), "война", "толстой", 1)
		}, "📚 Search results for \"война\" by толстой:\nPage 1 of 1 (total found 2 books)\n\n" +
			"1. Book 1 — Лев Николаевич Толстой, Мастер\n" +
			"2. Book 2 — Author 2\n\n" +
			"💡 Select a book by number or use navigation:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			search := &fakePublicSearch{bookPage: models.BookSearchPage{Books: cannedBooks(1, 2), Total: 2}}
			lookup := &pageLookup{lines: map[int64]models.BookAuthorDisplay{
				1: layerLineOf("Лев Николаевич Толстой", "Мастер"),
			}}
			cp := newTestProcessor(search, user)
			cp.authorLines = &services.AuthorLines{Lookup: lookup}

			res, err := c.run(cp)
			require.NoError(t, err)
			assert.Equal(t, c.want, res.Message)
			assert.Equal(t, [][]int64{{1, 2}}, lookup.calls, "one lookup for the page")
			assert.Equal(t, []string{"select:1", "select:2"}, callbackDataOf(res.ReplyMarkup))
		})
	}
}

// A line from the layer may have no name at all only when the book has no
// author; the list then says so, as it did for a book without legacy authors.
func TestBotListNamesABookWithoutAuthors(t *testing.T) {
	search := &fakePublicSearch{bookPage: models.BookSearchPage{Books: cannedBooks(1), Total: 1}}
	lookup := &pageLookup{lines: map[int64]models.BookAuthorDisplay{1: layerLineOf()}}
	cp := newTestProcessor(search, &models.User{ID: 7})
	cp.authorLines = &services.AuthorLines{Lookup: lookup}

	res, err := cp.ExecuteDirectBookSearch(context.Background(), "война", 1)
	require.NoError(t, err)
	assert.Contains(t, res.Message, "1. Book 1 — Автор неизвестен\n")
}
