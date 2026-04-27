package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminSvc is an in-memory CuratedCollectionsAdmin for httptest.
type fakeAdminSvc struct {
	startImportCalls   []services.ImportParams
	startImportID      int64
	startImportErr     error
	listResp           []models.BookCollection
	listErr            error
	getResp            *models.BookCollection
	getErr             error
	listItemsCalls     []listItemsCall
	listItemsResp      []models.BookCollectionItem
	listItemsTotal     int
	listItemsErr       error
	resolveCalls       []resolveCall
	resolveErr         error
	ignoreCalls        []int64
	ignoreErr          error
	updateCalls        []updateCall
	updateErr          error
	deleteCalls        []int64
	deleteErr          error
}

type listItemsCall struct {
	collectionID int64
	statusFilter string
	page         int
	pageSize     int
}

type resolveCall struct {
	itemID         int64
	bookID         int64
	decidedBy      *int64
}

type updateCall struct {
	id    int64
	patch database.CuratedCollectionPatch
}

func (f *fakeAdminSvc) StartImport(ctx context.Context, params services.ImportParams) (int64, error) {
	f.startImportCalls = append(f.startImportCalls, params)
	return f.startImportID, f.startImportErr
}
func (f *fakeAdminSvc) List(ctx context.Context) ([]models.BookCollection, error) {
	return f.listResp, f.listErr
}
func (f *fakeAdminSvc) Get(ctx context.Context, id int64) (*models.BookCollection, error) {
	return f.getResp, f.getErr
}
func (f *fakeAdminSvc) ListItems(ctx context.Context, collectionID int64, statusFilter string, page, pageSize int) ([]models.BookCollectionItem, int, error) {
	f.listItemsCalls = append(f.listItemsCalls, listItemsCall{collectionID, statusFilter, page, pageSize})
	return f.listItemsResp, f.listItemsTotal, f.listItemsErr
}
func (f *fakeAdminSvc) Resolve(ctx context.Context, itemID, bookID int64, decidedBy *int64) error {
	f.resolveCalls = append(f.resolveCalls, resolveCall{itemID, bookID, decidedBy})
	return f.resolveErr
}
func (f *fakeAdminSvc) Ignore(ctx context.Context, itemID int64) error {
	f.ignoreCalls = append(f.ignoreCalls, itemID)
	return f.ignoreErr
}
func (f *fakeAdminSvc) Update(ctx context.Context, id int64, patch database.CuratedCollectionPatch) error {
	f.updateCalls = append(f.updateCalls, updateCall{id, patch})
	return f.updateErr
}
func (f *fakeAdminSvc) Delete(ctx context.Context, id int64) error {
	f.deleteCalls = append(f.deleteCalls, id)
	return f.deleteErr
}
func (f *fakeAdminSvc) AutoResolveAmbiguous(ctx context.Context, id int64, decidedBy *int64) (int, error) {
	return 0, nil
}
func (f *fakeAdminSvc) LLMResolveAmbiguous(ctx context.Context, id int64, decidedBy *int64) (int, error) {
	return 0, nil
}

func newAdminTestRouter(svc CuratedCollectionsAdmin) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &CuratedCollectionsHandler{Svc: svc}
	g := r.Group("/api/admin/collections")
	h.Register(g)
	return r
}

func doJSON(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Buffer
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewBuffer(raw)
	} else {
		reader = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// --- Create / Import ---

func TestAdminCollections_Create_RejectsEmptyName(t *testing.T) {
	svc := &fakeAdminSvc{}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodPost, "/api/admin/collections",
		map[string]any{"items": []map[string]any{{"title": "T", "author": "A"}}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, svc.startImportCalls)
}

func TestAdminCollections_Create_RejectsEmptyItems(t *testing.T) {
	svc := &fakeAdminSvc{}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodPost, "/api/admin/collections",
		map[string]any{"name": "X", "items": []any{}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, svc.startImportCalls)
}

func TestAdminCollections_Create_Success(t *testing.T) {
	svc := &fakeAdminSvc{startImportID: 42}
	r := newAdminTestRouter(svc)
	body := map[string]any{
		"name":       "Antiutopias",
		"source_url": "https://example.com/sel/1",
		"items": []map[string]any{
			{"title": "1984", "author": "Orwell"},
			{"title": "Brave New World", "author": "Huxley", "year": 1932},
		},
	}
	rec := doJSON(t, r, http.MethodPost, "/api/admin/collections", body)

	require.Equal(t, http.StatusAccepted, rec.Code, "body=%s", rec.Body.String())
	var resp curatedImportResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, int64(42), resp.CollectionID)
	assert.Equal(t, models.ImportStatusImporting, resp.Status)

	require.Len(t, svc.startImportCalls, 1)
	call := svc.startImportCalls[0]
	assert.Equal(t, "Antiutopias", call.Name)
	assert.Equal(t, "https://example.com/sel/1", call.SourceURL)
	require.Len(t, call.Items, 2)
	assert.Equal(t, 1932, call.Items[1].Year)
}

// --- List ---

func TestAdminCollections_List_Success(t *testing.T) {
	svc := &fakeAdminSvc{listResp: []models.BookCollection{
		{ID: 1, Name: "A", IsCurated: true},
		{ID: 2, Name: "B", IsCurated: true},
	}}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/admin/collections", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var got []models.BookCollection
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got, 2)
}

// --- Get ---

func TestAdminCollections_Get_NotFound(t *testing.T) {
	svc := &fakeAdminSvc{getErr: pg.ErrNoRows}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/admin/collections/99", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminCollections_Get_Success(t *testing.T) {
	svc := &fakeAdminSvc{getResp: &models.BookCollection{ID: 7, Name: "Sel", IsCurated: true}}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/admin/collections/7", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var got models.BookCollection
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, int64(7), got.ID)
}

// --- Status ---

func TestAdminCollections_Status_Success(t *testing.T) {
	stats := models.CollectionImportStats{Matched: 2, Ambiguous: 1}
	svc := &fakeAdminSvc{getResp: &models.BookCollection{
		ID:           7,
		ImportStatus: models.ImportStatusCompleted,
		ImportStats:  &stats,
	}}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/admin/collections/7/status", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp curatedStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, models.ImportStatusCompleted, resp.Status)
	assert.Equal(t, 2, resp.Stats.Matched)
	assert.Equal(t, 1, resp.Stats.Ambiguous)
}

func TestAdminCollections_Status_NotFound(t *testing.T) {
	svc := &fakeAdminSvc{getErr: pg.ErrNoRows}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/admin/collections/99/status", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Items ---

func TestAdminCollections_Items_FilterAndPagination(t *testing.T) {
	svc := &fakeAdminSvc{
		listItemsResp:  []models.BookCollectionItem{{ID: 1}, {ID: 2}},
		listItemsTotal: 12,
	}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodGet, "/api/admin/collections/7/items?status=ambiguous&page=2&page_size=5", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, svc.listItemsCalls, 1)
	call := svc.listItemsCalls[0]
	assert.Equal(t, int64(7), call.collectionID)
	assert.Equal(t, "ambiguous", call.statusFilter)
	assert.Equal(t, 2, call.page)
	assert.Equal(t, 5, call.pageSize)

	var resp curatedItemsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Items, 2)
	assert.Equal(t, 12, resp.Total)
	assert.Equal(t, 2, resp.Page)
	assert.Equal(t, 5, resp.PageSize)
}

// --- Resolve ---

func TestAdminCollections_Resolve_RejectsMissingBookID(t *testing.T) {
	svc := &fakeAdminSvc{}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodPost, "/api/admin/collections/7/items/3/resolve", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, svc.resolveCalls)
}

func TestAdminCollections_Resolve_Success(t *testing.T) {
	svc := &fakeAdminSvc{}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodPost, "/api/admin/collections/7/items/3/resolve",
		map[string]any{"book_id": 100})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, svc.resolveCalls, 1)
	assert.Equal(t, int64(3), svc.resolveCalls[0].itemID)
	assert.Equal(t, int64(100), svc.resolveCalls[0].bookID)
}

// --- Ignore ---

func TestAdminCollections_Ignore_Success(t *testing.T) {
	svc := &fakeAdminSvc{}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodPost, "/api/admin/collections/7/items/3/ignore", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, svc.ignoreCalls, 1)
	assert.Equal(t, int64(3), svc.ignoreCalls[0])
}

// --- Patch ---

func TestAdminCollections_Patch_TogglesPublic(t *testing.T) {
	svc := &fakeAdminSvc{}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodPatch, "/api/admin/collections/7",
		map[string]any{"is_public": true})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, svc.updateCalls, 1)
	require.NotNil(t, svc.updateCalls[0].patch.IsPublic)
	assert.True(t, *svc.updateCalls[0].patch.IsPublic)
	assert.Nil(t, svc.updateCalls[0].patch.Name)
}

func TestAdminCollections_Patch_NotFound(t *testing.T) {
	svc := &fakeAdminSvc{updateErr: pg.ErrNoRows}
	r := newAdminTestRouter(svc)
	name := "X"
	rec := doJSON(t, r, http.MethodPatch, "/api/admin/collections/99",
		map[string]any{"name": name})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Delete ---

func TestAdminCollections_Delete_Success(t *testing.T) {
	svc := &fakeAdminSvc{}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodDelete, "/api/admin/collections/7", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Len(t, svc.deleteCalls, 1)
	assert.Equal(t, int64(7), svc.deleteCalls[0])
}

func TestAdminCollections_Delete_NotFound(t *testing.T) {
	svc := &fakeAdminSvc{deleteErr: pg.ErrNoRows}
	r := newAdminTestRouter(svc)
	rec := doJSON(t, r, http.MethodDelete, "/api/admin/collections/99", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// helper: ensure the strings package stays imported even if test paths shift.
var _ = strings.Contains
