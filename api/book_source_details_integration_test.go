package api

import (
	"net/http"
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

// The book list reads the card's publisher and ISBN from the real source
// layer on both of its branches: the search (an exact book ID) and the
// ordinary list. The scan path writes a live snapshot of every book with
// neither; a later snapshot that supersedes it is the one shown.
func TestBookListSourceDetailsFromCurrentSnapshot(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)
	withDetails := books[scanfixture.EntryMultiAuthor]
	untouched := books[scanfixture.EntryNoAuthor]

	// Supersede the scan's snapshot of one book with one that names a
	// publisher and two ISBNs.
	_, err := db.Exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE book_id = ?`, withDetails)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO book_metadata_snapshot
		(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name,
		 source_isbns, source_publisher)
		SELECT id, md5, 'card-superseding', 'live', 'extracted', path, filename,
		 ARRAY[' 978-5-389-00000-1', '978-5-389-00000-2 '], ' Азбука '
		FROM opds_catalog_book WHERE id = ?`, withDetails)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(0))
		c.Set("is_superuser", false)
		c.Next()
	})
	SetupBookRoutes(r.Group("/api/books"), &SearchHandler{
		Search:  services.NewSearchService(database.NewPGSearchRepository(db)),
		Sources: database.NewPGBookSourceRepository(db),
	})

	for _, path := range []string{
		"/api/books/list?book_id=" + strconv.FormatInt(withDetails, 10),
		"/api/books/list?limit=10",
	} {
		t.Run(path, func(t *testing.T) {
			rec := doJSON(t, r, http.MethodGet, path, nil)
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

			byID := map[string]map[string]string{}
			for _, book := range listedSourceFields(t, rec.Body.Bytes()) {
				byID[string(book["id"])] = map[string]string{
					"publisher": string(book["publisher"]), "isbn": string(book["isbn"])}
			}
			assert.Equal(t, map[string]string{
				"publisher": `"Азбука"`, "isbn": `["978-5-389-00000-1","978-5-389-00000-2"]`,
			}, byID[strconv.FormatInt(withDetails, 10)])
			if other, listed := byID[strconv.FormatInt(untouched, 10)]; listed {
				assert.Equal(t, map[string]string{"publisher": `null`, "isbn": `[]`}, other)
			}
		})
	}
}
