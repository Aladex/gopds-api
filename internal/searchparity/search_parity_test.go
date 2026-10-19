package searchparity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopds-api/api"
	"gopds-api/models"
	"gopds-api/opds"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One needle, two clients. REST and OPDS each bind their own query
// string, page in their own units and render their own format, so the only
// thing that can keep them saying the same thing is that the ranking, the
// filtering and the total all happen below them. These tests hold a recording
// stand-in where the search service goes and assert both halves of that: the
// request each client sends for equivalent input, and that each client shows
// exactly the page it was handed, in the order it was handed.
//
// The Telegram bot was the third client until S6 deleted its command
// processor; the v2 bot sends its typed text to the service as the query, and
// its own tests (telegram/router) cover that.

// parityService records what each adapter asks for and answers with a fixed
// page. It never ranks or filters: a difference between clients here is the
// client's doing, which is the whole point.
type parityService struct {
	bookReqs   []models.BookSearchRequest
	authorReqs []models.AuthorSearchRequest

	bookPage   models.BookSearchPage
	authorPage models.AuthorSearchPage
	err        error
}

//nolint:gocritic // the port takes the request by value; this implements it
func (p *parityService) SearchBooks(_ context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	p.bookReqs = append(p.bookReqs, req)
	return p.bookPage, p.err
}

func (p *parityService) SearchAuthors(_ context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	p.authorReqs = append(p.authorReqs, req)
	return p.authorPage, p.err
}

//nolint:gocritic // the port takes the request by value; this implements it
func (p *parityService) Suggestions(_ context.Context, _ models.SuggestionRequest) (models.SuggestionResult, error) {
	return models.SuggestionResult{}, nil
}

// parityBooks is deliberately not in id order: a client that re-sorts what it
// was given fails here rather than in production.
func parityBooks() []models.Book {
	return []models.Book{
		{ID: 903, Title: "Парити третья", Lang: "ru", Approved: true},
		{ID: 101, Title: "Парити первая", Lang: "ru", Approved: true},
		{ID: 507, Title: "Парити вторая", Lang: "ru", Approved: true},
	}
}

func parityAuthors() []models.Author {
	return []models.Author{
		{ID: 77, FullName: "Паритетов Автор", BooksCount: 12},
		{ID: 12, FullName: "Паритетова Автора", BooksCount: 4},
		{ID: 45, FullName: "Паритетский Автор", BooksCount: 1},
	}
}

func bookIDs(books []models.Book) []int64 {
	ids := make([]int64, 0, len(books))
	for i := range books {
		ids = append(ids, books[i].ID)
	}
	return ids
}

// restBookIDs is bookIDs over the REST list, whose rows wrap models.Book.
func restBookIDs(books []api.ListedBook) []int64 {
	ids := make([]int64, 0, len(books))
	for i := range books {
		ids = append(ids, books[i].ID)
	}
	return ids
}

// restBooks drives the REST adapter and returns the books it rendered.
func restBooks(t *testing.T, svc *parityService, query string) api.ExportAnswer {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Set("is_superuser", false)
		c.Next()
	})
	h := &api.SearchHandler{Search: svc}
	r.GET("/api/books/list", h.Books)
	r.GET("/api/books/authors", h.Authors)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, query, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var got api.ExportAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got
}

// opdsBody drives the OPDS adapter and returns the rendered feed.
func opdsBody(t *testing.T, svc *parityService, route, query string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	})
	h := &opds.SearchHandler{Search: svc}
	r.GET("/opds/books", h.Books)
	r.GET("/opds/search-author", h.Authors)
	r.GET("/opds/lang/:lang/search-books", h.BooksByLanguage)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, query, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, "route %s body=%s", route, rec.Body.String())
	return rec.Body.String()
}

// assertOrderedIn checks that the needles appear in the body in the given
// order — how a rendered feed reveals the order it was handed.
func assertOrderedIn(t *testing.T, body string, needles []string) {
	t.Helper()
	at := -1
	for _, needle := range needles {
		found := strings.Index(body, needle)
		require.GreaterOrEqual(t, found, 0, "%q is missing from the feed", needle)
		assert.Greater(t, found, at, "%q is out of order in the feed", needle)
		at = found
	}
}

func TestSearchParityTitleAcrossClients(t *testing.T) {
	const needle = "война и мир"
	page := models.BookSearchPage{Books: parityBooks(), Total: 57, Limit: 10}

	rest := &parityService{bookPage: page}
	got := restBooks(t, rest, "/api/books/list?title="+strings.ReplaceAll(needle, " ", "+")+"&limit=10")

	feed := &parityService{bookPage: page}
	body := opdsBody(t, feed, "books", "/opds/books?title="+strings.ReplaceAll(needle, " ", "+"))

	// Every client asked for the same needle on behalf of the same reader.
	require.Len(t, rest.bookReqs, 1)
	require.Len(t, feed.bookReqs, 1)
	for name, req := range map[string]models.BookSearchRequest{
		"rest": rest.bookReqs[0], "opds": feed.bookReqs[0],
	} {
		assert.Equal(t, needle, req.Query, "%s sent a different needle", name)
		assert.Equal(t, int64(42), req.UserID, "%s lost the reader", name)
		assert.False(t, req.Moderator, "%s must not claim moderation", name)
		assert.False(t, req.Unapproved, "%s must not ask for the queue", name)
		assert.False(t, req.IncludeHidden, "%s must not ask for hidden books", name)
	}

	// Every client showed the page it was handed, in the order it was handed.
	want := bookIDs(page.Books)
	assert.Equal(t, want, restBookIDs(got.Books), "REST reordered or dropped rows")
	assertOrderedIn(t, body, []string{"Парити третья", "Парити первая", "Парити вторая"})

	// And REST describes the total in its own units: pages of its limit.
	assert.Equal(t, 6, got.Length, "REST must page 57 rows by 10")
}

func TestSearchParityAuthorAcrossClients(t *testing.T) {
	const needle = "толстой"
	page := models.AuthorSearchPage{Authors: parityAuthors(), Total: 23, Limit: 10}

	rest := &parityService{authorPage: page}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int64(42)); c.Next() })
	h := &api.SearchHandler{Search: rest}
	r.GET("/api/books/authors", h.Authors)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/api/books/authors?author="+needle+"&limit=10", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var restAnswer api.AuthorAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &restAnswer))

	feed := &parityService{authorPage: page}
	body := opdsBody(t, feed, "search-author", "/opds/search-author?name="+needle)

	require.Len(t, rest.authorReqs, 1)
	require.Len(t, feed.authorReqs, 1)
	for name, req := range map[string]models.AuthorSearchRequest{
		"rest": rest.authorReqs[0], "opds": feed.authorReqs[0],
	} {
		assert.Equal(t, needle, req.Query, "%s sent a different needle", name)
	}

	wantNames := []string{"Паритетов Автор", "Паритетова Автора", "Паритетский Автор"}
	gotNames := make([]string, 0, len(restAnswer.Authors))
	for _, a := range restAnswer.Authors {
		gotNames = append(gotNames, a.FullName)
	}
	assert.Equal(t, wantNames, gotNames, "REST reordered or dropped authors")
	assertOrderedIn(t, body, wantNames)
}

// A language is expressed differently by each client — a query parameter, a
// path segment — and has to arrive as the same field.
func TestSearchParityLanguageReachesTheService(t *testing.T) {
	page := models.BookSearchPage{Books: parityBooks(), Total: 3, Limit: 10}

	rest := &parityService{bookPage: page}
	restBooks(t, rest, "/api/books/list?title=война&lang=ru&limit=10")

	feed := &parityService{bookPage: page}
	opdsBody(t, feed, "lang", "/opds/lang/ru/search-books?title=война")

	for name, reqs := range map[string][]models.BookSearchRequest{
		"rest": rest.bookReqs, "opds": feed.bookReqs,
	} {
		require.Len(t, reqs, 1, "%s made the wrong number of calls", name)
		assert.Equal(t, "ru", reqs[0].Language, "%s lost the language", name)
	}
}

// A typo is not a special case for any client: it travels as ordinary text and
// whatever the service ranks for it is what every client shows.
func TestSearchParityTypoTravelsUnchanged(t *testing.T) {
	const typo = "вйона и мир"
	page := models.BookSearchPage{Books: parityBooks()[:1], Total: 1, Limit: 10}

	rest := &parityService{bookPage: page}
	got := restBooks(t, rest, "/api/books/list?title="+strings.ReplaceAll(typo, " ", "+")+"&limit=10")

	feed := &parityService{bookPage: page}
	body := opdsBody(t, feed, "books", "/opds/books?title="+strings.ReplaceAll(typo, " ", "+"))

	for name, reqs := range map[string][]models.BookSearchRequest{
		"rest": rest.bookReqs, "opds": feed.bookReqs,
	} {
		require.Len(t, reqs, 1)
		assert.Equal(t, typo, reqs[0].Query, "%s altered the typo before sending it", name)
	}
	assert.Equal(t, []int64{903}, restBookIDs(got.Books))
	assert.Contains(t, body, "Парити третья")
}

// Nothing found must stay nothing found: no client may invent a row, and none
// may turn an empty page into an error.
func TestSearchParityZeroResultAcrossClients(t *testing.T) {
	empty := models.BookSearchPage{Books: nil, Total: 0, Limit: 10}
	const nonsense = "qqqzzzxxxwwwvvv"

	rest := &parityService{bookPage: empty}
	got := restBooks(t, rest, "/api/books/list?title="+nonsense+"&limit=10")
	assert.Empty(t, got.Books, "REST invented rows for a nonsense query")
	assert.Equal(t, 0, got.Length)

	feed := &parityService{bookPage: empty}
	body := opdsBody(t, feed, "books", "/opds/books?title="+nonsense)
	assert.NotContains(t, body, "Парити", "OPDS invented rows for a nonsense query")
}

// A repository failure is an outage, and every client has to say so rather
// than render an empty catalog.
func TestSearchParityFailureIsNotAnEmptyPage(t *testing.T) {
	boom := errors.New("connection refused")

	rest := &parityService{err: boom}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int64(42)); c.Next() })
	h := &api.SearchHandler{Search: rest}
	r.GET("/api/books/list", h.Books)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/books/list?title=война", http.NoBody))
	assert.GreaterOrEqual(t, rec.Code, 500, "REST answered an outage with %d", rec.Code)
}
