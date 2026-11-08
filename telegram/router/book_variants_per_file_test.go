package router

import (
	"context"
	"testing"

	"gopds-api/internal/scanfixture"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot lists files, never cards: a book uploaded three times and grouped
// into one card for the site is three rows of the author's list, under
// either author identity.
func TestBotAuthorListListsEveryFileOfAGroup(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	card := scanfixture.NewVariantCard(t, db)
	b := &Bot{Binding: &models.TelegramBinding{}}
	ids := func(page *bookPage) []int64 {
		out := make([]int64, len(page.list.Books))
		for i := range page.list.Books {
			out[i] = page.list.Books[i].ID
		}
		return out
	}

	legacy := &Router{}
	page, err := legacy.loadList(context.Background(), b, &models.TelegramView{Kind: ViewAuthorBooks, AuthorID: card.Author}, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, page.list.Total)
	assert.ElementsMatch(t, card.Books, ids(page))

	var handle int64
	_, err = db.QueryOne(pg.Scan(&handle), `INSERT INTO telegram_author_entity (entity_id) VALUES (?) RETURNING id`, card.Entity)
	require.NoError(t, err)
	entities := &Router{cfg: Config{Entities: true}}
	page, err = entities.loadList(context.Background(), b, &models.TelegramView{Kind: ViewAuthorBooks, AuthorID: handle}, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, page.list.Total)
	assert.ElementsMatch(t, card.Books, ids(page))
}
