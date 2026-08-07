package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"testing"

	"gopds-api/database"
	"gopds-api/httputil"
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
// stand in for the real catalogue there.
var searchTestDB *pg.DB

func TestMain(m *testing.M) {
	if conn := connectSearchTestDB(); conn != nil {
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

func connectSearchTestDB() *pg.DB {
	host := os.Getenv("GOPDS_POSTGRES_DBHOST")
	user := os.Getenv("GOPDS_POSTGRES_DBUSER")
	name := os.Getenv("GOPDS_POSTGRES_DBNAME")
	if host == "" || user == "" || name == "" {
		return nil
	}
	conn := pg.Connect(&pg.Options{
		Addr:      host,
		User:      user,
		Password:  os.Getenv("GOPDS_POSTGRES_DBPASS"),
		Database:  name,
		OnConnect: database.DisableJIT,
	})
	if _, err := conn.Exec("SELECT 1"); err != nil {
		_ = conn.Close()
		return nil
	}
	return conn
}

func requireSearchTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if searchTestDB == nil {
		t.Skip("no database configured: set GOPDS_POSTGRES_DBHOST/DBUSER/DBNAME")
	}
}

// fakeSearch records the requests the handler passes down and answers with
// canned pages, so the tests see the adapter boundary exactly.
type fakeSearch struct {
	booksPage   models.BookSearchPage
	booksErr    error
	authorsPage models.AuthorSearchPage
	authorsErr  error

	booksReqs  []models.BookSearchRequest
	authorReqs []models.AuthorSearchRequest
}

func (f *fakeSearch) SearchBooks(_ context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	f.booksReqs = append(f.booksReqs, req)
	return f.booksPage, f.booksErr
}

func (f *fakeSearch) SearchAuthors(_ context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	f.authorReqs = append(f.authorReqs, req)
	return f.authorsPage, f.authorsErr
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
	assert.True(t, req.Unapproved)
	assert.False(t, req.IncludeHidden,
		"a non-superuser must never reach the repository with IncludeHidden set")
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
