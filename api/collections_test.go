package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminFieldsForbiddenInList is the set of keys that must NEVER appear in the public
// list payload. Caught by the test even if someone later adds them by accident in the DTO.
var adminFieldsForbiddenInList = []string{
	"source_url", "import_status", "import_error", "imported_at", "import_stats",
	"is_curated", "is_public", "user_id",
}

// adminFieldsForbiddenInDetail extends the list-level forbiddens with item-level admin metadata.
var adminFieldsForbiddenInDetail = append([]string{}, adminFieldsForbiddenInList...)

// fakePublicSvc is an in-memory PublicCollectionsService for httptest.
type fakePublicSvc struct {
	listResp  []models.BookCollection
	listErr   error
	getResp   *models.BookCollection
	getErr    error
	booksResp []models.Book
	booksErr  error

	getCalls   []int64
	booksCalls []int64
}

func (f *fakePublicSvc) List(ctx context.Context) ([]models.BookCollection, error) {
	return f.listResp, f.listErr
}
func (f *fakePublicSvc) Get(ctx context.Context, id int64) (*models.BookCollection, error) {
	f.getCalls = append(f.getCalls, id)
	return f.getResp, f.getErr
}
func (f *fakePublicSvc) Books(ctx context.Context, collectionID int64) ([]models.Book, error) {
	f.booksCalls = append(f.booksCalls, collectionID)
	return f.booksResp, f.booksErr
}
func (f *fakePublicSvc) Covers(ctx context.Context, ids []int64) (map[int64][]database.CollectionCoverBook, error) {
	return map[int64][]database.CollectionCoverBook{}, nil
}

func newPublicTestRouter(svc PublicCollectionsService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &PublicCollectionsHandler{Svc: svc}
	g := r.Group("/api/collections")
	h.Register(g)
	return r
}

func TestPublicCollections_List_Success(t *testing.T) {
	svc := &fakePublicSvc{listResp: []models.BookCollection{
		{ID: 1, Name: "A", IsCurated: true, IsPublic: true, CreatedAt: time.Now()},
		{ID: 2, Name: "B", IsCurated: true, IsPublic: true, CreatedAt: time.Now()},
	}}
	r := newPublicTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/collections", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var got []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 2)
	for _, item := range got {
		assert.Contains(t, item, "id")
		assert.Contains(t, item, "name")
		for _, key := range adminFieldsForbiddenInList {
			assert.NotContains(t, item, key, "list payload must not expose %s", key)
		}
	}
}

func TestPublicCollections_List_Empty(t *testing.T) {
	svc := &fakePublicSvc{listResp: nil}
	r := newPublicTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/collections", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []map[string]any{}, got, "must serialize empty list as [], not null")
}

func TestPublicCollections_Get_Success(t *testing.T) {
	svc := &fakePublicSvc{
		getResp: &models.BookCollection{
			ID:        7,
			Name:      "Sel",
			IsCurated: true,
			IsPublic:  true,
			CreatedAt: time.Now(),
		},
		booksResp: []models.Book{
			{ID: 100, Title: "A"},
			{ID: 200, Title: "B"},
		},
	}
	r := newPublicTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/collections/7", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	assert.Equal(t, float64(7), got["id"])
	assert.Equal(t, "Sel", got["name"])
	require.Contains(t, got, "books")
	books, ok := got["books"].([]any)
	require.True(t, ok)
	assert.Len(t, books, 2)

	for _, key := range adminFieldsForbiddenInDetail {
		assert.NotContains(t, got, key, "detail payload must not expose collection-level %s", key)
	}

	require.Len(t, svc.getCalls, 1)
	assert.Equal(t, int64(7), svc.getCalls[0])
	require.Len(t, svc.booksCalls, 1)
	assert.Equal(t, int64(7), svc.booksCalls[0])
}

func TestPublicCollections_Get_NotFoundForDraft(t *testing.T) {
	// Service returns ErrNoRows for non-public / non-curated.
	svc := &fakePublicSvc{getErr: pg.ErrNoRows}
	r := newPublicTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/collections/99", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, svc.booksCalls, "books must not be loaded if the collection is hidden")
}

func TestPublicCollections_Get_BadID(t *testing.T) {
	svc := &fakePublicSvc{}
	r := newPublicTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/collections/not-a-number", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, svc.getCalls)
}
