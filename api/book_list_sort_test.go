package api

import (
	"net/http"
	"net/url"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The list sorts by author on request; any other sort value is the caller's
// mistake, answered before anything is read. A search keeps its ranked order
// whatever the sort says.
func TestBookListRefusesAnUnknownSort(t *testing.T) {
	fake := &fakeSearch{}
	r := newSearchTestRouter(fake, 77, false)
	for _, sort := range []string{"title", "AUTHOR", "author desc"} {
		rec := doJSON(t, r, http.MethodGet, "/api/books/list?limit=10&sort="+url.QueryEscape(sort), nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code, sort)
		assert.Contains(t, rec.Body.String(), "bad_sort", sort)
	}
	assert.Empty(t, fake.booksReqs)
}

func TestBookSearchKeepsItsRankWhateverTheSort(t *testing.T) {
	fake := &fakeSearch{booksPage: models.BookSearchPage{Books: []models.Book{{ID: 1}}, Total: 1, Limit: 10}}
	r := newSearchTestRouter(fake, 77, false)
	rec := doJSON(t, r, http.MethodGet, "/api/books/list?limit=10&sort=author&title="+url.QueryEscape("война"), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, fake.booksReqs, 1)
}
