package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The production API routes give the book list its source layer: a book whose
// current snapshot names a publisher and ISBNs is listed with them. Without
// the wiring every book would be listed with null and [].
func TestSetupApiRoutesWiresBookSourceDetails(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)
	book := books[scanfixture.EntryLatin]

	_, err := db.Exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE book_id = ?`, book)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO book_metadata_snapshot
		(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name,
		 source_isbns, source_publisher)
		SELECT id, md5, 'card-wiring', 'live', 'extracted', path, filename,
		 ARRAY['0-00-000000-0'], 'Wiring House'
		FROM opds_catalog_book WHERE id = ?`, book)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Stands in for the auth middleware: a signed-in non-moderator.
	group := r.Group("/api", func(c *gin.Context) {
		c.Set("user_id", int64(0))
		c.Set("is_superuser", false)
		c.Next()
	})
	setupApiRoutes(group, services.NewSearchService(database.NewPGSearchRepository(db)), db)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/books/list?book_id="+strconv.FormatInt(book, 10), http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var page struct {
		Books []struct {
			ID        int64    `json:"id"`
			Publisher *string  `json:"publisher"`
			ISBN      []string `json:"isbn"`
		} `json:"books"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Books, 1)
	require.NotNil(t, page.Books[0].Publisher)
	assert.Equal(t, "Wiring House", *page.Books[0].Publisher)
	assert.Equal(t, []string{"0-00-000000-0"}, page.Books[0].ISBN)
}
