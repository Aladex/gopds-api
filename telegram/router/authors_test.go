package router

import (
	"context"
	"testing"

	"gopds-api/models"
	"gopds-api/services"
	"gopds-api/telegram/screens"

	"github.com/stretchr/testify/assert"
)

type emptyLines struct{}

func (emptyLines) BookAuthorDisplay(_ context.Context, ids []int64) (map[int64]models.BookAuthorDisplay, error) {
	out := map[int64]models.BookAuthorDisplay{}
	for _, id := range ids {
		out[id] = models.BookAuthorDisplay{Source: models.AuthorDisplayLayer}
	}
	return out, nil
}

type noEntities struct{}

func (noEntities) EntityLines(_ context.Context, lines [][]models.AuthorDisplay) ([][]models.AuthorDisplay, error) {
	out := make([][]models.AuthorDisplay, len(lines))
	for i, line := range lines {
		out[i] = models.WithEntities(line, nil)
	}
	return out, nil
}

// Under the entity identity the bot's lists name a book's authors from its
// line alone: a line that names no one shows no name, not the catalog's
// authors (whose IDs and names are the legacy identity's). Under the legacy
// identity an empty line still gives way to them.
func TestTheBotCreditsTheEntityLineAsTheWholeSay(t *testing.T) {
	book := func() []models.Book {
		return []models.Book{{ID: 104540, Authors: []models.Author{{ID: 177615, FullName: "Автор неизвестен"}}}}
	}
	entities := &Router{cfg: Config{Entities: true,
		AuthorLines: &services.AuthorLines{Lookup: emptyLines{}, Entities: noEntities{}}}}
	books := book()
	entities.creditAuthors(context.Background(), books)
	assert.Empty(t, screens.AuthorsLine(&books[0]))

	legacy := &Router{cfg: Config{AuthorLines: &services.AuthorLines{Lookup: emptyLines{}}}}
	books = book()
	legacy.creditAuthors(context.Background(), books)
	assert.Equal(t, "Автор неизвестен", screens.AuthorsLine(&books[0]))
}
