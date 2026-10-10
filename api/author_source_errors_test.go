package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scanErrorsBody(t *testing.T) (body ScanErrorsResponse, raw string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/errors", GetScanErrors)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/errors", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body, rec.Body.String()
}

func authorEntries(body ScanErrorsResponse) []ScanErrorResponse {
	var out []ScanErrorResponse
	for _, e := range body.Errors {
		if strings.HasPrefix(e.Error, services.AuthorMetadataErrorPrefix) {
			out = append(out, e)
		}
	}
	return out
}

// failSourceInserts makes every author source write fail in the scratch
// database, as a lost connection or a broken invariant would.
func failSourceInserts(t *testing.T) {
	t.Helper()
	_, err := database.GetDB().Exec(`CREATE FUNCTION fail_source_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected source failure' USING ERRCODE = 'XX001'; END $$;
		CREATE TRIGGER fail_source_insert BEFORE INSERT ON book_metadata_snapshot
		FOR EACH ROW EXECUTE FUNCTION fail_source_insert()`)
	require.NoError(t, err)
}

// Review B3: the response closes the class whatever its source hands over.
func TestScanErrorsCloseEveryAuthorClass(t *testing.T) {
	previous := authorMetadataScanFailures
	authorMetadataScanFailures = func(context.Context) ([]database.AuthorMetadataScanFailure, error) {
		return []database.AuthorMetadataScanFailure{
			{Archive: "a.zip", Entry: "1.fb2", Class: "privacy_canary_name", At: time.Now()},
		}, nil
	}
	t.Cleanup(func() { authorMetadataScanFailures = previous })
	scanState = bookScanState{}
	resetAuthorSourceFailures()

	body, raw := scanErrorsBody(t)
	assert.NotContains(t, raw, "privacy_canary_name")
	entries := authorEntries(body)
	require.Len(t, entries, 1)
	assert.Equal(t, "author_metadata: extraction_failed", entries[0].Error)
}

// Review B8, the reviewer's probe kept: a fix scan's per-book author source
// failures reach the shared scan errors list, through the real orchestration.
func TestFixScanAuthorFailuresReachTheScanErrorsList(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	db := scanfixture.ScratchDB(t)
	dir, covers := t.TempDir(), t.TempDir()
	viper.Set("app.files_path", dir)
	viper.Set("app.posters_path", covers)
	viper.Set("scanning.enable_language_detection", false)
	t.Cleanup(func() {
		viper.Set("app.files_path", "")
		viper.Set("app.posters_path", "")
	})
	scanner := services.NewBookScanService(dir, covers, nil, false, nil)
	ids := scanfixture.Ingest(t, dir, time.Now(), scanner.ProcessBook)
	_, err := db.Exec(`SELECT author_layer_delete_books(ARRAY(SELECT id FROM opds_catalog_book))`)
	require.NoError(t, err)
	failSourceInserts(t)
	fixState, scanState = fixScanState{}, bookScanState{}
	resetAuthorSourceFailures()

	require.True(t, fixState.tryStart("fix", time.Now(), func() {}))
	runFixScan(context.Background(), "fix", 1)
	status := fixState.snapshot()
	require.Equal(t, len(ids), status.BooksUpdated, "the legacy updates committed")
	require.Equal(t, len(ids), status.ErrorCount)

	body, raw := scanErrorsBody(t)
	entries := authorEntries(body)
	require.Len(t, entries, len(ids), "every book's author source failure is listed")
	for _, e := range entries {
		assert.Equal(t, "author_metadata: author_metadata_unavailable", e.Error)
		assert.Equal(t, scanfixture.ArchiveName, e.ArchiveName)
		assert.NotEmpty(t, e.FileName)
	}
	assert.NotContains(t, raw, "injected source failure")
}

// Review B8: an approved single-book rescan whose author source write fails
// reports the closed class in the same list; the approval itself succeeds.
func TestApprovedRescanAuthorFailureReachesTheScanErrorsList(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	db := scanfixture.ScratchDB(t)
	dir, covers := t.TempDir(), t.TempDir()
	viper.Set("app.files_path", dir)
	viper.Set("app.posters_path", covers)
	t.Cleanup(func() {
		viper.Set("app.files_path", "")
		viper.Set("app.posters_path", "")
	})
	scanner := services.NewBookScanService(dir, covers, nil, false, nil)
	ids := scanfixture.Ingest(t, dir, time.Now(), scanner.ProcessBook)
	id := ids[scanfixture.EntryMultiAuthor]
	// A book whose layer is missing, so the approval has a source to write.
	_, err := db.Exec(`SELECT author_layer_delete_books(ARRAY[?]::bigint[])`, id)
	require.NoError(t, err)
	_, err = services.NewRescanService(dir, covers, nil).RescanBookPreview(id, 0)
	require.NoError(t, err)
	failSourceInserts(t)
	scanState = bookScanState{}
	resetAuthorSourceFailures()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/books/:id/rescan/approve", ApproveRescan)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/books/"+strconv.FormatInt(id, 10)+"/rescan/approve", strings.NewReader(`{"action":"approve"}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "author_metadata", "the approval response is unchanged")

	body, _ := scanErrorsBody(t)
	entries := authorEntries(body)
	require.Len(t, entries, 1)
	assert.Equal(t, "author_metadata: author_metadata_unavailable", entries[0].Error)
	assert.Equal(t, scanfixture.ArchiveName, entries[0].ArchiveName)
	assert.Equal(t, scanfixture.EntryMultiAuthor, entries[0].FileName)
}

// The in-memory list closes every class it is handed, keeps the newest
// maxAuthorSourceFailures entries, and a new fix scan replaces only the
// previous fix scan's entries.
func TestAuthorSourceClassesAndBounds(t *testing.T) {
	resetAuthorSourceFailures()
	t.Cleanup(resetAuthorSourceFailures)
	now := time.Now()
	authorSourceFailures.record("a.zip", "1.fb2", "privacy_canary_name", true, now)
	authorSourceFailures.record("a.zip", "2.fb2", "metadata_parse_failed", false, now)
	got := authorSourceFailures.list()
	require.Len(t, got, 2)
	assert.Equal(t, "author_metadata: author_metadata_unavailable", got[0].Error)
	assert.Equal(t, "author_metadata: metadata_parse_failed", got[1].Error)

	authorSourceFailures.startFixScan()
	got = authorSourceFailures.list()
	require.Len(t, got, 1, "the approved rescan's entry outlives a new fix scan")
	assert.Equal(t, "2.fb2", got[0].FileName)

	for i := 0; i < maxAuthorSourceFailures+10; i++ {
		authorSourceFailures.record("a.zip", strconv.Itoa(i)+".fb2", "invalid_fb2", true, now)
	}
	got = authorSourceFailures.list()
	require.Len(t, got, maxAuthorSourceFailures)
	assert.Equal(t, strconv.Itoa(maxAuthorSourceFailures+9)+".fb2", got[len(got)-1].FileName, "the newest are kept")
}

// Review B8, the reviewer's mixed-error probe kept: 501 legacy failures that
// come first in the fix scan's order do not starve the author source failures
// that come after them.
func TestFixScanAuthorFailuresAreNotStarvedByLegacyErrors(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	db := scanfixture.ScratchDB(t)
	dir, covers := t.TempDir(), t.TempDir()
	viper.Set("app.files_path", dir)
	viper.Set("app.posters_path", covers)
	viper.Set("scanning.enable_language_detection", false)
	t.Cleanup(func() {
		viper.Set("app.files_path", "")
		viper.Set("app.posters_path", "")
	})
	scanner := services.NewBookScanService(dir, covers, nil, false, nil)
	ids := scanfixture.Ingest(t, dir, time.Now(), scanner.ProcessBook)
	_, err := db.Exec(`SELECT author_layer_delete_books(ARRAY(SELECT id FROM opds_catalog_book))`)
	require.NoError(t, err)
	failSourceInserts(t)
	_, err = db.Exec(`INSERT INTO opds_catalog_book (title, path, filename, format, registerdate, docdate, lang, annotation, md5)
		SELECT 'fixture', '000-missing.zip', g::text || '.fb2', 'fb2', now(), '', 'ru', '', ''
		FROM generate_series(1, 501) g`)
	require.NoError(t, err)
	fixState, scanState = fixScanState{}, bookScanState{}
	resetAuthorSourceFailures()

	require.True(t, fixState.tryStart("fix", time.Now(), func() {}))
	runFixScan(context.Background(), "fix", 1)
	status := fixState.snapshot()
	require.Equal(t, len(ids), status.BooksUpdated)
	require.Equal(t, len(ids)+501, status.ErrorCount)

	body, _ := scanErrorsBody(t)
	require.Len(t, authorEntries(body), len(ids), "every author source failure is listed")
}
