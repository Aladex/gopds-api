package opds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OPDS lists files: no search it runs asks for one hit per card.

// recordingSearch answers every search empty and records the book searches.
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

var _ services.PublicSearch = (*recordingSearch)(nil)

func TestOPDSSearchNeverGroups(t *testing.T) {
	search := &recordingSearch{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int64(0)); c.Next() })
	SetupOpdsRoutes(r.Group("/opds"), search, &services.AuthorLines{}, nil)

	title := url.QueryEscape("хоббит")
	for _, path := range []string{
		"/opds/books?title=" + title,
		"/opds/books?title=" + title + "&page=1",
		"/opds/lang/ru/search-books?title=" + title,
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
	}
	require.Len(t, search.books, 3, "every path searched")
	for _, req := range search.books {
		assert.False(t, req.GroupCards, "OPDS asked for cards: %+v", req)
	}
}
