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

// With telegram.cards off the bot lists files: a book uploaded three times
// and grouped into one card for the site is three rows of the author's list,
// under either author identity.
func TestBotAuthorListPerFileWhenCardsOff(t *testing.T) {
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

// With telegram.cards on the author's list is the site's: the three files
// are one row, shown on the card's default, saying it holds three.
func TestBotAuthorListOneRowPerCard(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	card := scanfixture.NewVariantCard(t, db)
	b := &Bot{Binding: &models.TelegramBinding{}}
	var first int64
	_, err := db.QueryOne(pg.Scan(&first), `SELECT book_id FROM book_variant_member WHERE book_id IN (?)
		ORDER BY variant_rank LIMIT 1`, pg.In(card.Books))
	require.NoError(t, err)

	r := &Router{cfg: Config{Cards: true}}
	page, err := r.loadList(context.Background(), b, &models.TelegramView{Kind: ViewAuthorBooks, AuthorID: card.Author}, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, page.list.Total)
	require.Len(t, page.list.Books, 1)
	assert.Equal(t, first, page.list.Books[0].ID)
	require.NotNil(t, page.list.Variants[0])
	assert.Equal(t, 3, page.list.Variants[0].Count)
	assert.Equal(t, models.BookVariantsVariants, page.list.Variants[0].Kind)
}
