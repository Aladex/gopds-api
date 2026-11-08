package router

import (
	"context"
	"testing"

	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/screens"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot lists files: no search it runs asks for one hit per card.

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

func TestBotSearchNeverGroups(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	var user int64
	_, err := db.QueryOne(pg.Scan(&user), `INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', false, 'bot-reader', 'bot-reader@fixture.local', now()) RETURNING id`)
	require.NoError(t, err)

	search := &recordingSearch{}
	r := &Router{cfg: Config{Search: search}}
	b := &Bot{Binding: &models.TelegramBinding{UserID: user}}
	ctx := context.Background()

	// The typed search (search.go), the searches inside a list (lists.go)
	// and the inline answer (inline.go).
	r.searchPage(ctx, &models.TelegramView{Query: "хоббит"}, botAuthor{}, 0, 0)
	_, err = r.loadList(ctx, b, &models.TelegramView{Kind: ViewFavoritesSearch, Query: "хоббит"}, 0)
	require.NoError(t, err)
	require.NoError(t, r.searchList(ctx, &screens.BookList{}, &models.BookSearchRequest{Query: "хоббит", Limit: 10}))
	_, _, err = r.inlineBooks(ctx, b, "хоббит", 0)
	require.NoError(t, err)

	require.Len(t, search.books, 4, "every path searched")
	for _, req := range search.books {
		assert.False(t, req.GroupCards, "the bot asked for cards: %+v", req)
	}
}
