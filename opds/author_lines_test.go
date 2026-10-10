package opds

import (
	"context"
	"net/url"
	"testing"

	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The search feeds read the author lines of a page in one call and render
// each name of a line: a name linked to a catalog author with that author's
// <uri> and "all books" link, a name without one with neither.

type pageLookup struct {
	calls [][]int64
	lines map[int64]models.BookAuthorDisplay
}

func (l *pageLookup) BookAuthorDisplay(_ context.Context, ids []int64) (map[int64]models.BookAuthorDisplay, error) {
	l.calls = append(l.calls, append([]int64(nil), ids...))
	return l.lines, nil
}

func routerWithLines(search services.PublicSearch, lookup services.AuthorDisplayLookup) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupOpdsRoutes(r.Group("/opds"), search, &services.AuthorLines{Lookup: lookup})
	return r
}

func TestOpdsSearchFeedsReadThePageAuthorLinesOnce(t *testing.T) {
	books := cannedBooks(3)
	five := int64(5)
	lookup := &pageLookup{lines: map[int64]models.BookAuthorDisplay{
		1001: {Source: models.AuthorDisplayLayer, Authors: []models.AuthorDisplay{
			{Name: "Лев Николаевич Толстой", LegacyAuthorID: &five}, {Name: "Мастер"},
		}},
	}}
	search := &fakePublicSearch{booksPage: models.BookSearchPage{Books: books, Total: 3, Limit: 10}}
	r := routerWithLines(search, lookup)

	for _, path := range []string{
		"/opds/books?title=" + url.QueryEscape("война"),
		"/opds/lang/ru/search-books?title=" + url.QueryEscape("война"),
	} {
		lookup.calls = nil
		rec := doGET(t, r, path)
		require.Equal(t, 200, rec.Code)
		assert.Equal(t, [][]int64{{1000, 1001, 1002}}, lookup.calls, "%s: one lookup for the page", path)

		feed := rec.Body.String()
		// A book the lookup had no line for keeps its legacy author.
		assert.Equal(t, []lineAuthor{{Name: "Толстой Лев", URI: "/opds/author/5", Related: "/opds/new/0/5"}},
			entryAuthors(t, feed, 1000), path)
		assert.Equal(t, []lineAuthor{
			{Name: "Лев Николаевич Толстой", URI: "/opds/author/5", Related: "/opds/new/0/5"},
			{Name: "Мастер"},
		}, entryAuthors(t, feed, 1001), path)
	}
}

func TestOpdsSearchFeedWithoutAuthorLinesShowsTheLegacyAuthors(t *testing.T) {
	search := &fakePublicSearch{booksPage: models.BookSearchPage{Books: cannedBooks(1), Total: 1, Limit: 10}}
	rec := doGET(t, newOpdsTestRouter(search), "/opds/books?title="+url.QueryEscape("война"))
	require.Equal(t, 200, rec.Code)
	assert.Equal(t, []lineAuthor{{Name: "Толстой Лев", URI: "/opds/author/5", Related: "/opds/new/0/5"}},
		entryAuthors(t, rec.Body.String(), 1000))
}
