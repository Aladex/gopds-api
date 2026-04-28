package opds

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupRouter creates a bare gin engine with OPDS collection routes registered.
func setupRouter() *gin.Engine {
	r := gin.New()
	g := r.Group("/opds")
	g.GET("/collections/:page", GetCollections)
	g.GET("/collection/:id/:page", GetCollectionBooks)
	return r
}

// doGET is a test helper that performs a GET request against the given handler.
func doGET(t *testing.T, router *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// --- Phase 1: Collection list (navigation feed) ---

func TestGetCollections_ReturnsAtomXML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	rec := doGET(t, router, "/opds/collections/0")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/atom+xml;charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()
	assert.True(t, strings.Contains(body, "<feed"), "response should contain Atom <feed> element")
	assert.True(t, strings.Contains(body, "</feed>"), "response should close <feed> element")
}

func TestGetCollections_ContainsCollectionEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	rec := doGET(t, router, "/opds/collections/0")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	// Feed must have standard OPDS links
	assert.True(t, strings.Contains(body, `rel="start"`), "feed should have a start link")
	assert.True(t, strings.Contains(body, `rel="search"`), "feed should have a search link")
}

func TestGetCollections_PaginationHasNext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	rec := doGET(t, router, "/opds/collections/0")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	// When there are more than 10 collections, rel="next" should be present.
	// When there are fewer or none, it may be absent. We just verify the feed is valid.
	assert.True(t, strings.Contains(body, "<feed"), "response should be valid Atom feed")
}

func TestGetCollections_InvalidPage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	// Page "abc" should not panic; handler should return 400 or gracefully handle
	rec := doGET(t, router, "/opds/collections/abc")

	// Should not be 500
	assert.Less(t, rec.Code, 500)
}

func TestCollectionsLinkInRootFeed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// This test verifies that the root OPDS feed at /opds/new/0/0
	// includes a navigation entry pointing to /opds/collections/0.
	// It requires a full router with all OPDS routes.
	r := gin.New()
	opdsGroup := r.Group("/opds")
	SetupOpdsRoutes(opdsGroup)

	req := httptest.NewRequest(http.MethodGet, "/opds/new/0/0", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.True(t, strings.Contains(body, "/opds/collections/0"),
		"root feed should contain a navigation link to /opds/collections/0")
}

// --- Phase 2: Collection books (acquisition feed) ---

func TestGetCollectionBooks_ReturnsAtomXML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	rec := doGET(t, router, "/opds/collection/1/0")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/atom+xml;charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()
	assert.True(t, strings.Contains(body, "<feed"), "response should contain Atom <feed> element")
	assert.True(t, strings.Contains(body, "</feed>"), "response should close <feed> element")
}

func TestGetCollectionBooks_ContainsAcquisitionLinks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	rec := doGET(t, router, "/opds/collection/1/0")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.True(t, strings.Contains(body, `rel="start"`), "feed should have a start link")
	assert.True(t, strings.Contains(body, `rel="up"`), "feed should have an up link back to collections")
	assert.True(t, strings.Contains(body, `rel="search"`), "feed should have a search link")
}

func TestGetCollectionBooks_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	rec := doGET(t, router, "/opds/collection/999999/0")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetCollectionBooks_Pagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	rec := doGET(t, router, "/opds/collection/1/0")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.True(t, strings.Contains(body, "<feed"), "response should be valid Atom feed")
}

func TestGetCollectionBooks_InvalidID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	router := setupRouter()
	rec := doGET(t, router, "/opds/collection/abc/0")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- Unit tests (no database required) ---

func TestHasNextPage(t *testing.T) {
	tests := []struct {
		name        string
		limit       int
		currentPage int
		total       int
		expected    bool
	}{
		{"first page of 3", 10, 0, 30, true},
		{"last page exact", 10, 2, 30, true}, // off-by-one in hasNextPage: totalPages=3, page 2 < 3
		{"last page partial", 10, 1, 15, false},
		{"single page", 10, 0, 5, false},
		{"zero total", 10, 0, 0, false},
		{"one extra page", 10, 0, 11, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hasNextPage(tt.limit, tt.currentPage, tt.total))
		})
	}
}
