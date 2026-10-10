package main

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One switch, authors.display_source, governs every surface that lists
// books. With it on, the production routes serve OPDS feeds whose entries
// name the file's authors; with it off, the catalog's.

// opdsEntryAuthors serves the newest-books feed through the production
// routes, as a reader signed in with Basic auth, and returns the <author>
// names of the book's entry.
func opdsEntryAuthors(t *testing.T, r *gin.Engine, bookID int64) []string {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/opds/new/0/0", http.NoBody)
	req.SetBasicAuth("opds-reader", "opds-secret")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var feed struct {
		Entries []struct {
			ID      string   `xml:"id"`
			Authors []string `xml:"author>name"`
		} `xml:"entry"`
	}
	require.NoError(t, xml.Unmarshal(rec.Body.Bytes(), &feed))
	for _, e := range feed.Entries {
		if e.ID == strconv.FormatInt(bookID, 10) {
			return e.Authors
		}
	}
	t.Fatalf("book %d is not in the feed", bookID)
	return nil
}

func TestTheAuthorSwitchGovernsTheOpdsFeeds(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)
	require.NoError(t, database.CreateUser(models.RegisterRequest{
		Login: "opds-reader", Password: "opds-secret", Email: "opds-reader@fixture.local",
	}))
	search := services.NewSearchService(database.NewPGSearchRepository(db))
	multi := books[scanfixture.EntryMultiAuthor]

	gin.SetMode(gin.TestMode)
	off := gin.New()
	setupRoutes(off, nil, search, db, false)
	assert.Equal(t, []string{"Толстой лев", "пушкин Анна"}, opdsEntryAuthors(t, off, multi))

	on := gin.New()
	setupRoutes(on, nil, search, db, true)
	assert.Equal(t, []string{"лев Николаевич Толстой", "пушкин", "Анна", "Аноним"}, opdsEntryAuthors(t, on, multi))
}

// The bot reads the same switch: off, its processors name legacy authors;
// on, they read the author layer of the same database.
func TestTheAuthorSwitchReachesTheBot(t *testing.T) {
	assert.Nil(t, authorLinesOf(nil, false))
	lines := authorLinesOf(nil, true)
	require.NotNil(t, lines)
	assert.IsType(t, &database.PGBookSourceRepository{}, lines.Lookup)
}
