package router

import (
	"context"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/screens"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot's searches ask for one hit per card exactly while it lists cards.

type recordingSearch struct{ books []models.BookSearchRequest }

//nolint:gocritic // the port takes the request by value; this implements it
func (s *recordingSearch) SearchBooks(_ context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	s.books = append(s.books, req)
	return models.BookSearchPage{Limit: req.Limit}, nil
}

func (s *recordingSearch) SearchAuthors(_ context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	return models.AuthorSearchPage{Limit: req.Limit}, nil
}

func (s *recordingSearch) Suggestions(context.Context, models.SuggestionRequest) (models.SuggestionResult, error) {
	return models.SuggestionResult{}, nil
}

// searchPaths runs every search the bot makes, in order: the typed search
// (search.go), the searches inside a list (lists.go: within the favorites,
// and a list's search as such) and the inline answer (inline.go).
var searchPaths = []string{"typed search", "favorites search", "a list's search", "inline"}

func runSearchPaths(t *testing.T, cards bool) *recordingSearch {
	t.Helper()
	db := scanfixture.ScratchDB(t)
	var user int64
	_, err := db.QueryOne(pg.Scan(&user), `INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', false, 'bot-reader', 'bot-reader@fixture.local', now()) RETURNING id`)
	require.NoError(t, err)
	// The read model walked, so that only the flag decides.
	for step := 0; ; step++ {
		require.Less(t, step, 100, "the walk did not finish")
		res, walkErr := database.BookVariantWalkStep(context.Background(), db, 50, time.Hour, 5*time.Second, time.Time{})
		require.NoError(t, walkErr)
		if res.Finished {
			break
		}
	}

	search := &recordingSearch{}
	r := &Router{cfg: Config{Search: search, Cards: cards}}
	b := &Bot{Binding: &models.TelegramBinding{UserID: user}}
	ctx := context.Background()
	r.searchPage(ctx, &models.TelegramView{Query: "хоббит"}, botAuthor{}, 0, 0)
	_, err = r.loadList(ctx, b, &models.TelegramView{Kind: ViewFavoritesSearch, Query: "хоббит"}, 0)
	require.NoError(t, err)
	require.NoError(t, r.searchList(ctx, &screens.BookList{}, &models.BookSearchRequest{Query: "хоббит", Limit: 10}))
	books, variants, total, err := r.inlineBooks(ctx, b, "хоббит", 0)
	require.NoError(t, err)
	assert.Empty(t, books, "the recording search finds nothing")
	assert.Empty(t, variants)
	assert.Zero(t, total)
	require.NoError(t, err)
	require.Len(t, search.books, len(searchPaths), "every path searched")
	return search
}

// With telegram.cards off no search the bot runs asks for one hit per card.
func TestBotSearchPerFileWhenCardsOff(t *testing.T) {
	search := runSearchPaths(t, false)
	for i, req := range search.books {
		assert.False(t, req.GroupCards, "%s asked for cards", searchPaths[i])
	}
}

// With telegram.cards on every search the bot runs asks for one hit per
// card, as the site's.
func TestBotSearchGroupsWhenCardsOn(t *testing.T) {
	search := runSearchPaths(t, true)
	for i, req := range search.books {
		assert.True(t, req.GroupCards, "%s did not ask for cards", searchPaths[i])
	}
}
