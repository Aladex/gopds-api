package router

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot's searches list cards through the real search service: within an
// author under both identities, in a language, and per file until the read
// model is walked.

func (f *listFixture) searchRouter(entities bool) *Router {
	return &Router{cfg: Config{Cards: true, Entities: entities, SearchTimeout: 5 * time.Second,
		Search: services.NewSearchService(database.NewPGSearchRepositoryFor(f.db, entities))}}
}

func TestAuthorSearchListsCards(t *testing.T) {
	f := newListFixture(t)
	solaris := f.card("Солярис", 3, 0)
	f.lang = "en"
	f.card("Solaris", 2, 0)
	// The same title in another language is another card, and not this
	// search's.
	f.lang = "uk"
	f.card("Солярис", 2, 0)
	f.lang = ""
	f.walk()
	var handle int64
	_, err := f.db.QueryOne(pg.Scan(&handle), `INSERT INTO telegram_author_entity (entity_id) VALUES (?) RETURNING id`, f.entity)
	require.NoError(t, err)
	ctx := context.Background()
	first := f.byRank(solaris)[0]

	for _, tc := range []struct {
		name     string
		entities bool
		author   int64
	}{{"legacy author", false, f.author}, {"author entity", true, handle}} {
		r := f.searchRouter(tc.entities)
		page, loadErr := r.loadList(ctx, f.bot(), &models.TelegramView{Kind: ViewAuthorSearch, AuthorID: tc.author,
			Query: "солярис", Lang: "ru"}, 0)
		require.NoError(t, loadErr, tc.name)
		assert.Equal(t, 1, page.list.Total, "%s: one card", tc.name)
		assert.Equal(t, []int64{first}, pageIDs(page), "%s: on its default", tc.name)
		require.NotNil(t, page.list.Variants[0], tc.name)
		assert.Equal(t, 3, page.list.Variants[0].Count, tc.name)

		page, loadErr = r.loadList(ctx, f.bot(), &models.TelegramView{Kind: ViewAuthorSearch, AuthorID: tc.author,
			Query: "solaris", Lang: "en"}, 0)
		require.NoError(t, loadErr, tc.name)
		assert.Equal(t, 1, page.list.Total, "%s: the English card", tc.name)
		require.NotNil(t, page.list.Variants[0], tc.name)
		assert.Equal(t, 2, page.list.Variants[0].Count, tc.name)
	}

	// Before the walk has finished the search lists files.
	f.exec(`UPDATE book_variant_walk SET finished_at = NULL`)
	page, err := f.searchRouter(false).loadList(ctx, f.bot(), &models.TelegramView{Kind: ViewAuthorSearch,
		AuthorID: f.author, Query: "солярис", Lang: "ru"}, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, page.list.Total)
	assert.Empty(t, page.list.Variants)
}

// A typed search's page of cards reads what a page of single books reads.
func TestSearchReadsDoNotGrowWithCards(t *testing.T) {
	f := newListFixture(t)
	for i := 1; i <= 5; i++ {
		f.card(fmt.Sprintf("Альфа %d", i), 3, 0)
		f.file(fmt.Sprintf("Бета %d", i), fmt.Sprintf("beta-%d", i), 0)
	}
	f.walk()
	r := f.searchRouter(false)
	var n atomic.Int64
	f.db.AddQueryHook(countHook{n: &n})
	count := func(query string) int {
		view := &models.TelegramView{Query: query}
		r.searchPage(context.Background(), view, botAuthor{}, 0, 0) // the readiness is read once
		n.Store(0)
		res := r.searchPage(context.Background(), view, botAuthor{}, 0, 0)
		require.False(t, res.BooksFailed)
		require.Equal(t, 5, res.BookTotal, query)
		return int(n.Load())
	}
	cards, single := count("альфа"), count("бета")
	t.Logf("statements of a search page: 5 cards of 3 files %d, 5 single books %d", cards, single)
	assert.Equal(t, single, cards)
}
