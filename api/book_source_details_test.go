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

// The book list carries what the public card shows from the source layer:
// each book's publisher (or null) and ISBN list (or []), read for the whole
// page in one lookup.

// fakeSources answers the source lookup from a fixed table and records the
// IDs of every call.
type fakeSources struct {
	details map[int64]models.BookSourceDetail
	err     error
	calls   [][]int64
}

func (f *fakeSources) BookSourceDetails(_ context.Context, ids []int64) (map[int64]models.BookSourceDetail, error) {
	f.calls = append(f.calls, append([]int64(nil), ids...))
	return f.details, f.err
}

func newSourcesTestRouter(search *fakeSearch, sources BookSourceLookup) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(0))
		c.Set("is_superuser", false)
		c.Next()
	})
	SetupBookRoutes(r.Group("/api/books"), &SearchHandler{Search: search, Sources: sources})
	return r
}

// listedSourceFields decodes only the two source fields of every listed book,
// keeping the raw JSON so null and [] stay distinguishable.
func listedSourceFields(t *testing.T, body []byte) []map[string]json.RawMessage {
	t.Helper()
	var page struct {
		Books []map[string]json.RawMessage `json:"books"`
	}
	require.NoError(t, json.Unmarshal(body, &page))
	return page.Books
}

func TestSearchHandler_Books_CarriesPublisherAndISBN(t *testing.T) {
	publisher := "Издательство «Мир»"
	search := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{{ID: 10, Title: "С выходными данными"}, {ID: 20, Title: "Без снимка"},
			{ID: 30, Title: "Пустые данные"}},
		Total: 3, Limit: 10,
	}}
	sources := &fakeSources{details: map[int64]models.BookSourceDetail{
		10: {Publisher: &publisher, ISBN: []string{"978-5-03-000000-1", "978-5-03-000000-2"}},
		30: {ISBN: []string{}},
	}}
	r := newSourcesTestRouter(search, sources)

	rec := doJSON(t, r, http.MethodGet, "/api/books/list?title=book", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	books := listedSourceFields(t, rec.Body.Bytes())
	require.Len(t, books, 3)
	assert.JSONEq(t, `"Издательство «Мир»"`, string(books[0]["publisher"]))
	assert.JSONEq(t, `["978-5-03-000000-1","978-5-03-000000-2"]`, string(books[0]["isbn"]))
	// No snapshot: no entry in the lookup, and the fields still say so.
	assert.Equal(t, `null`, string(books[1]["publisher"]))
	assert.Equal(t, `[]`, string(books[1]["isbn"]))
	assert.Equal(t, `null`, string(books[2]["publisher"]))
	assert.Equal(t, `[]`, string(books[2]["isbn"]))

	// One lookup for the whole page, not one per book.
	assert.Equal(t, [][]int64{{10, 20, 30}}, sources.calls)
}

func TestSearchHandler_Books_NilISBNFromTheLookupIsAnEmptyList(t *testing.T) {
	publisher := "Только издательство"
	search := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{{ID: 10}}, Total: 1, Limit: 10,
	}}
	sources := &fakeSources{details: map[int64]models.BookSourceDetail{10: {Publisher: &publisher}}}

	rec := doJSON(t, newSourcesTestRouter(search, sources), http.MethodGet, "/api/books/list?title=book", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	books := listedSourceFields(t, rec.Body.Bytes())
	require.Len(t, books, 1)
	assert.Equal(t, `[]`, string(books[0]["isbn"]))
}

func TestSearchHandler_Books_EmptyPageAsksNoLookup(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{Limit: 10}}
	sources := &fakeSources{}

	rec := doJSON(t, newSourcesTestRouter(search, sources), http.MethodGet, "/api/books/list?title=book", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	assert.JSONEq(t, `{"books":[],"length":0}`, rec.Body.String())
	assert.Empty(t, sources.calls)
}

func TestSearchHandler_Books_LookupFailureIsAServerError(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{{ID: 10}}, Total: 1, Limit: 10,
	}}
	sources := &fakeSources{err: errors.New("source layer unavailable")}

	rec := doJSON(t, newSourcesTestRouter(search, sources), http.MethodGet, "/api/books/list?title=book", nil)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body=%s", rec.Body.String())
}

// The ordinary list (no search target) reads its rows from the catalog, so
// this one needs the database; the lookup failure must surface there too.
func TestSearchHandler_Books_OrdinaryListLookupFailureIsAServerError(t *testing.T) {
	requireSearchTestDB(t)
	sources := &fakeSources{err: errors.New("source layer unavailable")}

	rec := doJSON(t, newSourcesTestRouter(&fakeSearch{}, sources), http.MethodGet, "/api/books/list?limit=1", nil)

	if len(sources.calls) == 0 {
		t.Skip("the catalog has no visible book, so the list asks no lookup")
	}
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body=%s", rec.Body.String())
}
