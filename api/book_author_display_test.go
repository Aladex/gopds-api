package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With the author line switched on, every listed book carries
// authors_display — its author names, each with the legacy author it links
// to when there is one — read for the whole page in one lookup. Switched off,
// the field is absent and the list is what it was.

type fakeAuthorLines struct {
	lines map[int64]models.BookAuthorDisplay
	err   error
	calls [][]int64
}

func (f *fakeAuthorLines) BookAuthorDisplay(_ context.Context, ids []int64) (map[int64]models.BookAuthorDisplay, error) {
	f.calls = append(f.calls, append([]int64(nil), ids...))
	return f.lines, f.err
}

func newAuthorLinesTestRouter(search *fakeSearch, authors AuthorDisplayLookup) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(0))
		c.Set("is_superuser", false)
		c.Next()
	})
	SetupBookRoutes(r.Group("/api/books"), &SearchHandler{Search: search, AuthorLines: authors})
	return r
}

func int64Ptr(v int64) *int64 { return &v }

func TestSearchHandler_Books_CarriesTheAuthorLine(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{
			{ID: 10, Authors: []models.Author{{ID: 1, FullName: "Петров Иван"}}},
			{ID: 20, Authors: []models.Author{{ID: 2, FullName: "Иванов Иван"}}},
		},
		Total: 2, Limit: 10,
	}}
	authors := &fakeAuthorLines{lines: map[int64]models.BookAuthorDisplay{
		10: {Source: models.AuthorDisplayLayer, Authors: []models.AuthorDisplay{
			{Name: "Иван Петрович Петров", LegacyAuthorID: int64Ptr(1)},
			{Name: "Мастер"},
		}},
		20: {Source: models.AuthorDisplayLegacy, Fallback: models.AuthorDisplayLegacyUnmatched,
			Authors: []models.AuthorDisplay{{Name: "Иванов Иван", LegacyAuthorID: int64Ptr(2)}}},
	}}

	rec := doJSON(t, newAuthorLinesTestRouter(search, authors), http.MethodGet, "/api/books/list?title=book", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	books := listedSourceFields(t, rec.Body.Bytes())
	require.Len(t, books, 2)
	assert.JSONEq(t, `[{"name":"Иван Петрович Петров","legacy_author_id":1},{"name":"Мастер"}]`,
		string(books[0]["authors_display"]))
	assert.JSONEq(t, `[{"name":"Иванов Иван","legacy_author_id":2}]`, string(books[1]["authors_display"]))
	// The legacy list stays as it was beside the line.
	assert.JSONEq(t, `[{"id":1,"full_name":"Петров Иван"}]`, string(books[0]["authors"]))
	assert.Equal(t, [][]int64{{10, 20}}, authors.calls)
}

func TestSearchHandler_Books_AuthorLineSwitchedOffIsAbsent(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{{ID: 10, Authors: []models.Author{{ID: 1, FullName: "Петров Иван"}}}},
		Total: 1, Limit: 10,
	}}

	rec := doJSON(t, newAuthorLinesTestRouter(search, nil), http.MethodGet, "/api/books/list?title=book", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	books := listedSourceFields(t, rec.Body.Bytes())
	require.Len(t, books, 1)
	_, present := books[0]["authors_display"]
	assert.False(t, present, "body=%s", rec.Body.String())
}

func TestSearchHandler_Books_AuthorLineLookupFailureIsAServerError(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{Books: []models.Book{{ID: 10}}, Total: 1, Limit: 10}}
	authors := &fakeAuthorLines{err: errors.New("source layer unavailable")}

	rec := doJSON(t, newAuthorLinesTestRouter(search, authors), http.MethodGet, "/api/books/list?title=book", nil)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body=%s", rec.Body.String())
}

func TestSearchHandler_Books_AuthorLineEmptyPageAsksNoLookup(t *testing.T) {
	authors := &fakeAuthorLines{}

	rec := doJSON(t, newAuthorLinesTestRouter(&fakeSearch{booksPage: models.BookSearchPage{Limit: 10}}, authors),
		http.MethodGet, "/api/books/list?title=book", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Empty(t, authors.calls)
}

// A book the lookup did not answer for is shown with its legacy authors, as
// the line of a book without credits would be: never with no line at all.
func TestSearchHandler_Books_BookMissingFromTheLookupShowsItsLegacyAuthors(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{{ID: 10, Authors: []models.Author{{ID: 1, FullName: "Петров Иван"}}}},
		Total: 1, Limit: 10,
	}}

	rec := doJSON(t, newAuthorLinesTestRouter(search, &fakeAuthorLines{}), http.MethodGet, "/api/books/list?title=book", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var page struct {
		Books []struct {
			AuthorsDisplay json.RawMessage `json:"authors_display"`
		} `json:"books"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Books, 1)
	assert.JSONEq(t, `[{"name":"Петров Иван","legacy_author_id":1}]`, string(page.Books[0].AuthorsDisplay))
}
