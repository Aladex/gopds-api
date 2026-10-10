package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/go-pg/pg/v10"
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
	setupApiRoutes(group, services.NewSearchService(database.NewPGSearchRepository(db)), db, false)

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

// listAuthorLines lists one book through the production routes and returns
// the raw authors_display of it, nil when the field is absent.
func listAuthorLines(t *testing.T, db *pg.DB, layer bool, book int64) json.RawMessage {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group("/api", func(c *gin.Context) {
		c.Set("user_id", int64(0))
		c.Set("is_superuser", false)
		c.Next()
	})
	setupApiRoutes(group, services.NewSearchService(database.NewPGSearchRepository(db)), db, layer)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/books/list?book_id="+strconv.FormatInt(book, 10), http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var page struct {
		Books []map[string]json.RawMessage `json:"books"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Books, 1)
	return page.Books[0]["authors_display"]
}

// The author line is wired from the switch: off, the list is what it was;
// on, each scanned book shows its line — from the file where the catalog's
// words are all the file's, from the catalog where the scan found no
// author at all.
func TestSetupApiRoutesWiresTheAuthorLineBehindTheSwitch(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)

	legacyID := func(name string) int64 {
		var id int64
		_, err := db.QueryOne(pg.Scan(&id), `SELECT id FROM opds_catalog_author WHERE full_name = ?`, name)
		require.NoError(t, err, name)
		return id
	}

	assert.Nil(t, listAuthorLines(t, db, false, books[scanfixture.EntryLatin]), "switched off: no line")

	assert.JSONEq(t, fmt.Sprintf(`[{"name":"jOHN O'brien","legacy_author_id":%d},{"name":"ЛЕВ толстой","legacy_author_id":%d}]`,
		legacyID("O'brien jOHN"), legacyID("толстой ЛЕВ")),
		string(listAuthorLines(t, db, true, books[scanfixture.EntryLatin])))
	// The legacy scan glued "пушкин" and "Анна" into one author; every word
	// is still the file's, so the file's four authors are shown, the glued
	// name linked to none of them.
	assert.JSONEq(t, fmt.Sprintf(`[{"name":"лев Николаевич Толстой","legacy_author_id":%d},{"name":"пушкин"},`+
		`{"name":"Анна"},{"name":"Аноним"}]`, legacyID("Толстой лев")),
		string(listAuthorLines(t, db, true, books[scanfixture.EntryMultiAuthor])))
	assert.JSONEq(t, fmt.Sprintf(`[{"name":"Автор неизвестен","legacy_author_id":%d}]`, legacyID("Автор неизвестен")),
		string(listAuthorLines(t, db, true, books[scanfixture.EntryNoAuthor])))
}

// An administrator's edit of a book's authors lives only in the catalog;
// the author layer still holds what the file says. A book whose catalog
// names an author the file does not stays on its catalog authors, so the
// edit does not vanish from the card.
func TestEditedAuthorsKeepTheBookOnTheCatalog(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)
	book := books[scanfixture.EntryLatin]

	authorID := func(name string) int64 {
		var id int64
		_, err := db.QueryOne(pg.Scan(&id), `SELECT id FROM opds_catalog_author WHERE full_name = ?`, name)
		require.NoError(t, err, name)
		return id
	}
	obrien, tolstoy := authorID("O'brien jOHN"), authorID("толстой ЛЕВ")

	// A co-author added by hand.
	_, err := database.UpdateBook(models.BookUpdateRequest{ID: book, Authors: []models.Author{
		{ID: obrien}, {ID: tolstoy}, {FullName: "Добавленный Соавтор"},
	}})
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`[{"name":"O'brien jOHN","legacy_author_id":%d},{"name":"толстой ЛЕВ","legacy_author_id":%d},`+
		`{"name":"Добавленный Соавтор","legacy_author_id":%d}]`, obrien, tolstoy, authorID("Добавленный Соавтор")),
		string(listAuthorLines(t, db, true, book)))

	// The authors replaced by hand.
	_, err = database.UpdateBook(models.BookUpdateRequest{ID: book, Authors: []models.Author{{FullName: "Исправленный Автор"}}})
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`[{"name":"Исправленный Автор","legacy_author_id":%d}]`, authorID("Исправленный Автор")),
		string(listAuthorLines(t, db, true, book)))
}
