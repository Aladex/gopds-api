package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task E3: the scan errors list carries the author metadata run's per-book
// failures next to the scan's own, by archive, entry and closed class.
func TestScanErrorsIncludeAuthorMetadataFailures(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	previous := authorMetadataScanFailures
	authorMetadataScanFailures = func(context.Context) ([]database.AuthorMetadataScanFailure, error) {
		return []database.AuthorMetadataScanFailure{
			{Archive: "a.zip", Entry: "1.fb2", Class: "invalid_fb2", At: at},
		}, nil
	}
	t.Cleanup(func() { authorMetadataScanFailures = previous })
	scanState = bookScanState{}
	resetAuthorSourceFailures()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/errors", GetScanErrors)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/errors", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var body ScanErrorsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEmpty(t, body.Errors)
	last := body.Errors[len(body.Errors)-1]
	assert.Equal(t, "a.zip", last.ArchiveName)
	assert.Equal(t, "1.fb2", last.FileName)
	assert.Equal(t, "author_metadata: invalid_fb2", last.Error)
	assert.True(t, at.Equal(last.Timestamp))
}
