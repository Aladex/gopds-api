package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/internal/testdb"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchTestDB holds the optional integration connection for the one case that
// exercises the ordinary list path: what it proves is that a request without a
// search target still reaches database.GetBooks untouched, and no fake can
// stand in for the real catalog there.
var searchTestDB *pg.DB

func TestMain(m *testing.M) {
	// Configured but unreachable fails the run rather than quietly skipping
	// the one case that exercises the ordinary list path against real rows.
	if cfg, ok := testdb.Configured(); ok {
		conn, err := testdb.Connect(cfg, database.DisableJIT)
		if err != nil {
			fmt.Fprintf(os.Stderr, "api: %v\n", err)
			os.Exit(1)
		}
		searchTestDB = conn
		database.SetDB(conn)
	}

	code := m.Run()

	// Closed explicitly rather than deferred: os.Exit does not run defers.
	if searchTestDB != nil {
		_ = searchTestDB.Close()
	}
	os.Exit(code)
}

func requireSearchTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if searchTestDB == nil {
		t.Skip(testdb.SkipReason)
	}
}

// fakeSearch records the requests the handler passes down and answers with
// canned pages, so the tests see the adapter boundary exactly.
type fakeSearch struct {
	booksPage   models.BookSearchPage
	booksErr    error
	authorsPage models.AuthorSearchPage
	authorsErr  error
	suggResult  models.SuggestionResult
	suggErr     error

	booksReqs  []models.BookSearchRequest
	authorReqs []models.AuthorSearchRequest
	suggReqs   []models.SuggestionRequest
}

//nolint:gocritic // the port takes the request by value; this implements it
func (f *fakeSearch) SearchBooks(_ context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	f.booksReqs = append(f.booksReqs, req)
	return f.booksPage, f.booksErr
}

func (f *fakeSearch) SearchAuthors(_ context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	f.authorReqs = append(f.authorReqs, req)
	return f.authorsPage, f.authorsErr
}

func (f *fakeSearch) Suggestions(_ context.Context, req models.SuggestionRequest) (models.SuggestionResult, error) {
	f.suggReqs = append(f.suggReqs, req)
	return f.suggResult, f.suggErr
}

var _ services.PublicSearch = (*fakeSearch)(nil)

// newSearchTestRouter mounts the book routes exactly the way production does,
// behind a stub that plays the auth middleware: it sets the identity values
// the real middleware would have validated.
func newSearchTestRouter(search services.PublicSearch, userID int64, superuser bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("is_superuser", superuser)
		c.Next()
	})
	SetupBookRoutes(r.Group("/api/books"), &SearchHandler{Search: search})
	return r
}

func TestSearchHandler_Books_SearchMapsEveryScope(t *testing.T) {
	fake := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{{ID: 1, Title: "Война и мир"}},
		Total: 42, Limit: 10, Offset: 20,
	}}
	r := newSearchTestRouter(fake, 77, false)

	path := "/api/books/list?title=" + url.QueryEscape("война") +
		"&limit=10&offset=20&lang=ru&author=5&series=7&genre=3&collection=9" +
		"&curated_collection=11&fav=true&unapproved=true&include_hidden=true"
	rec := doJSON(t, r, http.MethodGet, path, nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	require.Len(t, fake.booksReqs, 1)
	req := fake.booksReqs[0]
	assert.Equal(t, "война", req.Query)
	assert.Equal(t, int64(77), req.UserID)
	assert.Equal(t, "ru", req.Language)
	assert.Equal(t, int64(5), req.AuthorID)
	assert.Equal(t, int64(7), req.SeriesID)
	assert.Equal(t, int64(3), req.GenreID)
	assert.Equal(t, int64(9), req.CollectionID)
	assert.Equal(t, int64(11), req.CuratedCollectionID)
	assert.True(t, req.Favorites)
	// The adapter no longer clears the widening flags: it forwards them raw
	// and reports identity. The clearing itself is the service's rule, pinned
	// in services/search_test.go.
	assert.True(t, req.Unapproved, "the raw flag travels; the service decides")
	assert.True(t, req.IncludeHidden, "the raw flag travels; the service decides")
	assert.False(t, req.Moderator, "a non-superuser is reported as a non-moderator")
	assert.Equal(t, 10, req.Limit)
	assert.Equal(t, 20, req.Offset)

	var got ExportAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Books, 1)
	assert.Equal(t, int64(1), got.Books[0].ID)
	assert.Equal(t, 5, got.Length, "length is pages of the effective limit: ceil(42/10)")
}

func TestSearchHandler_Books_SuperuserKeepsIncludeHidden(t *testing.T) {
	fake := &fakeSearch{booksPage: models.BookSearchPage{Limit: 10}}
	r := newSearchTestRouter(fake, 77, true)

	rec := doJSON(t, r, http.MethodGet, "/api/books/list?title=x&include_hidden=true", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, fake.booksReqs, 1)
	assert.True(t, fake.booksReqs[0].IncludeHidden)
	assert.True(t, fake.booksReqs[0].Moderator, "a superuser is reported as a moderator")
}

/*
 * Both flags widen what a request may see, so both belong to whoever moderates
 * rather than to whoever asks. include_hidden was gated from the start;
 * unapproved was not, and any signed-in reader could ask for the moderation
 * queue by hand — no screen offers it, which is why it went unnoticed rather
 * than why it was safe.
 *
 * Since the gate moved into the service, this adapter's contract is about what
 * it reports: raw flags plus the caller's identity. The clearing itself is
 * pinned at the service boundary, and the ordinary list — which runs below the
 * service — has its own behavior test further down.
 */
func TestSearchHandler_Books_UnapprovedIsForModeratorsOnly(t *testing.T) {
	t.Run("a superuser still reaches the moderation queue", func(t *testing.T) {
		fake := &fakeSearch{booksPage: models.BookSearchPage{Limit: 10}}
		r := newSearchTestRouter(fake, 77, true)

		rec := doJSON(t, r, http.MethodGet, "/api/books/list?title=x&unapproved=true", nil)

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Len(t, fake.booksReqs, 1)
		assert.True(t, fake.booksReqs[0].Unapproved)
		assert.True(t, fake.booksReqs[0].Moderator)
	})

	// Asking without the flag is the ordinary case and must stay untouched.
	t.Run("nobody who did not ask is given it", func(t *testing.T) {
		fake := &fakeSearch{booksPage: models.BookSearchPage{Limit: 10}}
		r := newSearchTestRouter(fake, 77, true)

		rec := doJSON(t, r, http.MethodGet, "/api/books/list?title=x", nil)

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Len(t, fake.booksReqs, 1)
		assert.False(t, fake.booksReqs[0].Unapproved)
	})
}

/*
 * The ordinary list calls database.GetBooks directly, so the service gate does
 * not cover it and no fake can stand in here. This asserts the behavior
 * instead of the wiring: a non-moderator who asks for the queue by hand still
 * gets approved books only, while a moderator reaches the queue. The catalog
 * carries unapproved books, so both sides have visible answers.
 */
func TestSearchHandler_Books_OrdinaryListUnapprovedGate(t *testing.T) {
	requireSearchTestDB(t)

	t.Run("a non-moderator asking by hand gets approved books only", func(t *testing.T) {
		fake := &fakeSearch{}
		r := newSearchTestRouter(fake, 1, false)

		rec := doJSON(t, r, http.MethodGet, "/api/books/list?limit=100&unapproved=true", nil)

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Empty(t, fake.booksReqs, "the search service must not see list requests")

		var got ExportAnswer
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.NotEmpty(t, got.Books)
		for _, b := range got.Books {
			assert.True(t, b.Approved, "book %d must be approved", b.ID)
		}
	})

	t.Run("a moderator reaches the queue", func(t *testing.T) {
		fake := &fakeSearch{}
		r := newSearchTestRouter(fake, 1, true)

		rec := doJSON(t, r, http.MethodGet, "/api/books/list?limit=100&unapproved=true", nil)

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

		var got ExportAnswer
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.NotEmpty(t, got.Books, "the queue must not be empty for this test to prove anything")
		for _, b := range got.Books {
			assert.False(t, b.Approved, "book %d must be unapproved in the queue", b.ID)
		}
	})
}

func TestSearchHandler_Books_ExactBookID(t *testing.T) {
	fake := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{{ID: 123, Title: "Exact"}},
		Total: 1, Limit: 100,
	}}
	r := newSearchTestRouter(fake, 77, false)

	rec := doJSON(t, r, http.MethodGet, "/api/books/list?book_id=123", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, fake.booksReqs, 1)
	assert.Equal(t, int64(123), fake.booksReqs[0].ExactBookID)
	assert.Empty(t, fake.booksReqs[0].Query)

	var got ExportAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Books, 1)
	assert.Equal(t, 1, got.Length)
}

func TestSearchHandler_Authors_Search(t *testing.T) {
	fake := &fakeSearch{authorsPage: models.AuthorSearchPage{
		Authors: []models.Author{{ID: 1, FullName: "Толстой Лев", BooksCount: 700}},
		Total:   3, Limit: 10,
	}}
	r := newSearchTestRouter(fake, 77, false)

	rec := doJSON(t, r, http.MethodGet,
		"/api/books/authors?author="+url.QueryEscape("толстой")+"&limit=10&offset=0&lang=ru", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	require.Len(t, fake.authorReqs, 1)
	req := fake.authorReqs[0]
	assert.Equal(t, "толстой", req.Query)
	assert.Equal(t, "ru", req.Language)
	assert.Equal(t, 10, req.Limit)
	assert.Equal(t, 0, req.Offset)

	var got AuthorAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Authors, 1)
	assert.Equal(t, "Толстой Лев", got.Authors[0].FullName)
	assert.Equal(t, 1, got.Length, "ceil(3/10)")
}

func TestSearchHandler_ValidationErrorsAre400(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		err  error
	}{
		{"books empty query", "/api/books/list?title=x", services.ErrEmptyQuery},
		{"books invalid pagination", "/api/books/list?title=x", services.ErrInvalidPagination},
		{"authors empty query", "/api/books/authors?author=x", services.ErrEmptyQuery},
		{"authors invalid pagination", "/api/books/authors?author=x", services.ErrInvalidPagination},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSearch{booksErr: tc.err, authorsErr: tc.err}
			r := newSearchTestRouter(fake, 77, false)

			rec := doJSON(t, r, http.MethodGet, tc.path, nil)

			require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
			var got httputil.HTTPError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Equal(t, http.StatusBadRequest, got.Code)
			assert.NotEmpty(t, got.Message)
		})
	}
}

func TestSearchHandler_ServiceErrorIs500JSON(t *testing.T) {
	boom := errors.New("repository exploded")
	for _, tc := range []struct {
		name string
		path string
	}{
		{"books", "/api/books/list?title=x"},
		{"authors", "/api/books/authors?author=x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSearch{booksErr: boom, authorsErr: boom}
			r := newSearchTestRouter(fake, 77, false)

			rec := doJSON(t, r, http.MethodGet, tc.path, nil)

			require.Equal(t, http.StatusInternalServerError, rec.Code, "body=%s", rec.Body.String())
			var got httputil.HTTPError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got),
				"a raw Go error would serialize as {}, never as this shape")
			assert.Equal(t, http.StatusInternalServerError, got.Code)
			assert.Equal(t, "repository exploded", got.Message)
		})
	}
}

func TestSearchHandler_Books_OrdinaryListFallback(t *testing.T) {
	requireSearchTestDB(t)
	fake := &fakeSearch{}
	r := newSearchTestRouter(fake, 1, false)

	t.Run("a request without a search target stays on the ordinary list", func(t *testing.T) {
		rec := doJSON(t, r, http.MethodGet, "/api/books/list?limit=5&offset=0", nil)

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Empty(t, fake.booksReqs, "the search service must not see list requests")

		var got ExportAnswer
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Len(t, got.Books, 5)
		assert.Greater(t, got.Length, 1)
	})

	t.Run("an unnamed limit no longer divides by zero", func(t *testing.T) {
		rec := doJSON(t, r, http.MethodGet, "/api/books/list?offset=0", nil)

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

		var got ExportAnswer
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Greater(t, got.Length, 0)
	})
}

func TestSearchHandler_Autocomplete_Success(t *testing.T) {
	fake := &fakeSearch{suggResult: models.SuggestionResult{
		Suggestions: []models.AutocompleteSuggestion{
			{Value: "Война и мир", Secondary: "Толстой Лев", Type: "book", ID: 1},
			{Value: "Толстой Лев", Type: "author", ID: 2, BooksCount: 10},
		},
	}}
	r := newSearchTestRouter(fake, 77, false)

	rec := doJSON(t, r, http.MethodGet,
		"/api/books/autocomplete?query="+url.QueryEscape("война")+"&type=all&author=5&lang=ru", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	require.Len(t, fake.suggReqs, 1)
	req := fake.suggReqs[0]
	assert.Equal(t, "война", req.Query)
	assert.Equal(t, models.SuggestionAll, req.Kind)
	assert.Equal(t, int64(5), req.AuthorID)
	assert.Equal(t, "ru", req.Language)

	var got AutocompleteResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Suggestions, 2)
	assert.Equal(t, "book", got.Suggestions[0].Type)
	assert.Equal(t, "Толстой Лев", got.Suggestions[0].Secondary,
		"the secondary author text tells identical titles apart")
	assert.Equal(t, "author", got.Suggestions[1].Type)
	assert.Equal(t, 10, got.Suggestions[1].BooksCount)
}

func TestSearchHandler_Autocomplete_Defaults(t *testing.T) {
	fake := &fakeSearch{}
	r := newSearchTestRouter(fake, 77, false)

	rec := doJSON(t, r, http.MethodGet,
		"/api/books/autocomplete?query="+url.QueryEscape("война"), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	require.Len(t, fake.suggReqs, 1)
	assert.Equal(t, models.SuggestionAll, fake.suggReqs[0].Kind, "an unnamed type means all")
	assert.Zero(t, fake.suggReqs[0].AuthorID)

	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.JSONEq(t, `[]`, string(got["suggestions"]),
		"an empty picker serializes as [], not null")
}

func TestSearchHandler_Autocomplete_Validation(t *testing.T) {
	boom := errors.New("repository exploded")
	for _, tc := range []struct {
		name     string
		path     string
		suggErr  error
		wantCode int
	}{
		{"a missing query is a client error", "/api/books/autocomplete", nil, http.StatusBadRequest},
		{"a non-numeric author is a client error", "/api/books/autocomplete?query=x&author=abc", nil, http.StatusBadRequest},
		{
			"an unknown kind is a client error", "/api/books/autocomplete?query=x&type=titles",
			services.ErrInvalidSuggestionKind, http.StatusBadRequest,
		},
		{"a repository failure is a server error", "/api/books/autocomplete?query=x", boom, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSearch{suggErr: tc.suggErr}
			r := newSearchTestRouter(fake, 77, false)

			rec := doJSON(t, r, http.MethodGet, tc.path, nil)

			require.Equal(t, tc.wantCode, rec.Code, "body=%s", rec.Body.String())
			var got httputil.HTTPError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got),
				"errors keep the httputil shape, never a raw Go error")
			assert.Equal(t, tc.wantCode, got.Code)
		})
	}
}
