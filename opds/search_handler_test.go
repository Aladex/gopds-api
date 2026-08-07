package opds

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePublicSearch records the exact DTO each handler passes down and answers
// with literal pages, so the tests pin the adapter boundary — identity,
// paging, language and feed shape — never the database.
type fakePublicSearch struct {
	booksPage   models.BookSearchPage
	booksErr    error
	authorsPage models.AuthorSearchPage
	authorsErr  error

	booksReqs  []models.BookSearchRequest
	authorReqs []models.AuthorSearchRequest
}

func (f *fakePublicSearch) SearchBooks(_ context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	f.booksReqs = append(f.booksReqs, req)
	return f.booksPage, f.booksErr
}

func (f *fakePublicSearch) SearchAuthors(_ context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	f.authorReqs = append(f.authorReqs, req)
	return f.authorsPage, f.authorsErr
}

func (f *fakePublicSearch) Suggestions(_ context.Context, _ models.SuggestionRequest) (models.SuggestionResult, error) {
	return models.SuggestionResult{}, nil
}

var _ services.PublicSearch = (*fakePublicSearch)(nil)

// newOpdsTestRouter mounts the OPDS routes the way production does, behind a
// stub that plays BasicAuth: it sets the identity the real middleware would
// have validated. OPDS has no superuser notion, so there is nothing else to
// set — and therefore no way an OPDS request ever declares a moderator.
func newOpdsTestRouter(search services.PublicSearch) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/opds", func(c *gin.Context) {
		c.Set("user_id", int64(77))
		c.Next()
	})
	SetupOpdsRoutes(g, search)
	return r
}

// testFeed mirrors the atom shape the feeds serve, with explicit tags: the
// opdsutils types rely on the element type's XMLName for their link slices,
// which encoding/xml does not match back on unmarshal — so the tests carry
// their own reading of the wire format.
type testLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type testEntry struct {
	ID    string     `xml:"id"`
	Links []testLink `xml:"link"`
}

type testFeed struct {
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Links   []testLink  `xml:"link"`
	Entries []testEntry `xml:"entry"`
}

func parseFeed(t *testing.T, rec *httptest.ResponseRecorder) testFeed {
	t.Helper()
	var feed testFeed
	require.NoError(t, xml.Unmarshal(rec.Body.Bytes(), &feed), "body=%s", rec.Body.String())
	return feed
}

// nextFeedLink finds the rel="next" link, the only rel these tests ever look
// up: the truthful next boundary is the behavior under test.
func nextFeedLink(links []testLink) (testLink, bool) {
	for _, l := range links {
		if l.Rel == "next" {
			return l, true
		}
	}
	return testLink{}, false
}

// nextQuery parses the rel="next" href and returns its query values: building
// the link through url.Values is exactly what proves Unicode and spaces are
// escaped once and survive the round trip.
func nextQuery(t *testing.T, link testLink) url.Values {
	t.Helper()
	u, err := url.Parse(link.Href)
	require.NoError(t, err, "next href %q must parse", link.Href)
	return u.Query()
}

// cannedBooks returns n books with distinct IDs, enough for CreateItem to
// build acquisition entries.
func cannedBooks(n int) []models.Book {
	books := make([]models.Book, n)
	for i := range books {
		id := int64(1000 + i)
		books[i] = models.Book{
			ID:      id,
			Title:   fmt.Sprintf("Book %d", id),
			Format:  "fb2",
			Lang:    "ru",
			Authors: []models.Author{{ID: 5, FullName: "Толстой Лев"}},
		}
	}
	return books
}

func TestOpdsSearch_BooksMapsRequestAndFeed(t *testing.T) {
	fake := &fakePublicSearch{booksPage: models.BookSearchPage{
		Books: cannedBooks(10), Total: 21, Limit: 10, Offset: 10,
	}}
	r := newOpdsTestRouter(fake)

	rec := doGET(t, r, "/opds/books?title="+url.QueryEscape("война")+"&page=1")

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "application/atom+xml;charset=utf-8", rec.Header().Get("Content-Type"))

	require.Len(t, fake.booksReqs, 1)
	req := fake.booksReqs[0]
	assert.Equal(t, "война", req.Query)
	assert.Equal(t, 10, req.Limit)
	assert.Equal(t, 10, req.Offset, "page 1 is the second ten rows")
	assert.Equal(t, int64(77), req.UserID)
	assert.Equal(t, "", req.Language, "the global search covers the whole library")

	feed := parseFeed(t, rec)
	assert.Equal(t, "tag:search:books:"+url.QueryEscape("война")+":1", feed.ID)

	next, ok := nextFeedLink(feed.Links)
	require.True(t, ok, "offset 10 + 10 items < total 21 must advertise a next page")
	assert.Equal(t, "/opds/books", uPath(next))
	q := nextQuery(t, next)
	assert.Equal(t, "война", q.Get("title"), "the next link carries the query escaped exactly once")
	assert.Equal(t, "2", q.Get("page"))

	require.Len(t, feed.Entries, 10)
	assert.Equal(t, "1000", feed.Entries[0].ID)
	var acquisition string
	for _, l := range feed.Entries[0].Links {
		if l.Rel == "http://opds-spec.org/acquisition/open-access" {
			acquisition = l.Href
			break
		}
	}
	assert.Equal(t, "/opds/get/fb2/1000", acquisition, "book entries keep their acquisition links")
}

// uPath returns the path portion of a parsed link, a helper so assertions can
// separate "where the link points" from "what it carries".
func uPath(link testLink) string {
	u, err := url.Parse(link.Href)
	if err != nil {
		return ""
	}
	return u.Path
}

func TestOpdsSearch_BooksNextLinkBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		page     int
		items    int
		total    int
		wantNext bool
	}{
		{"a strict remainder advertises next", 0, 10, 25, true},
		{"one extra row advertises next", 0, 10, 11, true},
		{"an exactly full final page does not", 1, 10, 20, false},
		{"a partial final page does not", 1, 5, 15, false},
		{"a single exact page does not", 0, 10, 10, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakePublicSearch{booksPage: models.BookSearchPage{
				Books: cannedBooks(tc.items), Total: tc.total, Limit: 10, Offset: tc.page * 10,
			}}
			r := newOpdsTestRouter(fake)

			rec := doGET(t, r, "/opds/books?title=x&page="+strconv.Itoa(tc.page))

			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			feed := parseFeed(t, rec)
			_, hasNext := nextFeedLink(feed.Links)
			assert.Equal(t, tc.wantNext, hasNext,
				"offset %d + %d items against total %d", tc.page*10, tc.items, tc.total)
		})
	}
}

func TestOpdsSearch_BooksErrorAndEmpty(t *testing.T) {
	t.Run("a service error is a 500, never the not-found feed", func(t *testing.T) {
		fake := &fakePublicSearch{booksErr: errors.New("repository exploded")}
		r := newOpdsTestRouter(fake)

		rec := doGET(t, r, "/opds/books?title=x")

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "tag:search:books:notfound")
	})

	t.Run("an empty page is the not-found feed", func(t *testing.T) {
		fake := &fakePublicSearch{booksPage: models.BookSearchPage{Books: nil, Total: 0, Limit: 10}}
		r := newOpdsTestRouter(fake)

		rec := doGET(t, r, "/opds/books?title=x")

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "tag:search:books:notfound")
	})

	t.Run("a negative page falls back to the first page", func(t *testing.T) {
		fake := &fakePublicSearch{booksPage: models.BookSearchPage{
			Books: cannedBooks(1), Total: 1, Limit: 10,
		}}
		r := newOpdsTestRouter(fake)

		rec := doGET(t, r, "/opds/books?title=x&page=-1")

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Len(t, fake.booksReqs, 1)
		assert.Equal(t, 0, fake.booksReqs[0].Offset)
	})
}

func TestOpdsSearch_AuthorsMapsRequestAndFeed(t *testing.T) {
	fake := &fakePublicSearch{authorsPage: models.AuthorSearchPage{
		Authors: []models.Author{{ID: 5, FullName: "Толстой Лев", BooksCount: 700}},
		Total:   11, Limit: 10,
	}}
	r := newOpdsTestRouter(fake)

	rec := doGET(t, r, "/opds/search-author?name="+url.QueryEscape("толстой"))

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "application/atom+xml;charset=utf-8", rec.Header().Get("Content-Type"))

	require.Len(t, fake.authorReqs, 1)
	req := fake.authorReqs[0]
	assert.Equal(t, "толстой", req.Query)
	assert.Equal(t, 10, req.Limit)
	assert.Equal(t, 0, req.Offset)
	assert.Equal(t, "", req.Language)

	feed := parseFeed(t, rec)
	assert.Equal(t, "tag:search:authors:"+url.QueryEscape("толстой")+":0", feed.ID)

	next, ok := nextFeedLink(feed.Links)
	require.True(t, ok, "0 + 1 item < total 11 must advertise a next page")
	assert.Equal(t, "/opds/search-author", uPath(next))
	q := nextQuery(t, next)
	assert.Equal(t, "толстой", q.Get("name"))
	assert.Equal(t, "1", q.Get("page"))

	require.Len(t, feed.Entries, 1)
	assert.Equal(t, "tag:author:5", feed.Entries[0].ID)
	require.NotEmpty(t, feed.Entries[0].Links)
	assert.Equal(t, "/opds/new/0/5", feed.Entries[0].Links[0].Href,
		"author entries keep browsing to that author's books")
}

func TestOpdsSearch_AuthorsErrorIs500(t *testing.T) {
	fake := &fakePublicSearch{authorsErr: errors.New("repository exploded")}
	r := newOpdsTestRouter(fake)

	rec := doGET(t, r, "/opds/search-author?name=x")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "tag:search:books:notfound")
}

func TestOpdsSearch_BooksByLanguageMapsRequestAndFeed(t *testing.T) {
	fake := &fakePublicSearch{booksPage: models.BookSearchPage{
		Books: cannedBooks(10), Total: 12, Limit: 10,
	}}
	r := newOpdsTestRouter(fake)

	rec := doGET(t, r, "/opds/lang/ru/search-books?title="+url.QueryEscape("война и мир"))

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "application/atom+xml;charset=utf-8", rec.Header().Get("Content-Type"))

	require.Len(t, fake.booksReqs, 1)
	req := fake.booksReqs[0]
	assert.Equal(t, "война и мир", req.Query)
	assert.Equal(t, "ru", req.Language, "the route language becomes the request language")
	assert.Equal(t, 10, req.Limit)
	assert.Equal(t, 0, req.Offset)
	assert.Equal(t, int64(77), req.UserID)

	feed := parseFeed(t, rec)
	assert.Equal(t, "tag:lang:ru:search:books:"+url.QueryEscape("война и мир")+":0", feed.ID)
	assert.Contains(t, feed.Title, "Поиск книг")

	next, ok := nextFeedLink(feed.Links)
	require.True(t, ok, "0 + 10 items < total 12 must advertise a next page")
	assert.Equal(t, "/opds/lang/ru/search-books", uPath(next),
		"the next page stays inside the language")
	q := nextQuery(t, next)
	assert.Equal(t, "война и мир", q.Get("title"), "spaces and Cyrillic survive one round of escaping")
	assert.Equal(t, "1", q.Get("page"))
}

func TestOpdsSearch_AuthorsByLanguageMapsRequestAndFeed(t *testing.T) {
	fake := &fakePublicSearch{authorsPage: models.AuthorSearchPage{
		Authors: []models.Author{{ID: 5, FullName: "Толстой Лев"}},
		Total:   1, Limit: 10,
	}}
	r := newOpdsTestRouter(fake)

	rec := doGET(t, r, "/opds/lang/ru/search-authors?name="+url.QueryEscape("толстой")+"&page=0")

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	require.Len(t, fake.authorReqs, 1)
	req := fake.authorReqs[0]
	assert.Equal(t, "толстой", req.Query)
	assert.Equal(t, "ru", req.Language)

	feed := parseFeed(t, rec)
	assert.Equal(t, "tag:lang:ru:search:authors:"+url.QueryEscape("толстой")+":0", feed.ID)

	_, hasNext := nextFeedLink(feed.Links)
	assert.False(t, hasNext, "a single exact page must not advertise next")

	require.Len(t, feed.Entries, 1)
	require.NotEmpty(t, feed.Entries[0].Links)
	assert.Equal(t, "/opds/lang/ru/author/5/0", feed.Entries[0].Links[0].Href,
		"language author entries keep browsing inside the language")
}
