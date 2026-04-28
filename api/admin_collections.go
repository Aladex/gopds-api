package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
)

// CuratedCollectionsAdmin is the service-layer view of admin curated-collection
// operations. The admin handler depends on this interface (not on database
// directly) so its httptest cases can mock it without a real database.
type CuratedCollectionsAdmin interface {
	StartImport(ctx context.Context, params services.ImportParams) (collectionID int64, err error)

	List(ctx context.Context, page, pageSize int) ([]models.BookCollection, int, error)
	Get(ctx context.Context, id int64) (*models.BookCollection, error)

	ListItems(ctx context.Context, collectionID int64, statusFilter string, page, pageSize int) (items []models.BookCollectionItem, total int, err error)

	Resolve(ctx context.Context, itemID, bookID int64, decidedByUserID *int64) error
	Ignore(ctx context.Context, itemID int64) error
	AutoResolveAmbiguous(ctx context.Context, collectionID int64, decidedByUserID *int64) (int, error)
	LLMResolveAmbiguous(ctx context.Context, collectionID int64, decidedByUserID *int64) (int, error)

	Update(ctx context.Context, id int64, patch database.CuratedCollectionPatch) error
	Delete(ctx context.Context, id int64) error
}

// CuratedCollectionsHandler binds CuratedCollectionsAdmin to gin routes.
type CuratedCollectionsHandler struct {
	Svc CuratedCollectionsAdmin
}

// Register attaches all curated-collection admin endpoints to the given group.
// Caller is expected to have already wrapped the group with admin middleware.
func (h *CuratedCollectionsHandler) Register(r *gin.RouterGroup) {
	r.POST("", h.create)
	r.GET("", h.list)
	r.GET("/:id", h.get)
	r.GET("/:id/status", h.status)
	r.GET("/:id/items", h.items)
	r.PATCH("/:id", h.patch)
	r.DELETE("/:id", h.delete)
	r.POST("/:id/items/:itemID/resolve", h.resolve)
	r.POST("/:id/items/:itemID/ignore", h.ignore)
	r.POST("/:id/auto-resolve", h.autoResolve)
	r.POST("/:id/llm-resolve", h.llmResolve)
}

// --- DTOs ---

type curatedImportRequest struct {
	Name      string                 `json:"name" binding:"required"`
	SourceURL string                 `json:"source_url"`
	Items     []services.ImportItem  `json:"items" binding:"required,min=1,dive"`
}

type curatedImportResponse struct {
	CollectionID int64  `json:"collection_id"`
	Status       string `json:"status"`
}

type curatedStatusResponse struct {
	Status      string                       `json:"status"`
	ImportError string                       `json:"import_error,omitempty"`
	Stats       models.CollectionImportStats `json:"stats"`
}

type curatedItemsResponse struct {
	Items    []models.BookCollectionItem `json:"items"`
	Total    int                         `json:"total"`
	Page     int                         `json:"page"`
	PageSize int                         `json:"page_size"`
}

type curatedListResponse struct {
	Rows     []models.BookCollection `json:"rows"`
	Total    int                     `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
}

type curatedPatchRequest struct {
	Name      *string `json:"name,omitempty"`
	IsPublic  *bool   `json:"is_public,omitempty"`
	SourceURL *string `json:"source_url,omitempty"`
}

type curatedResolveRequest struct {
	BookID int64 `json:"book_id" binding:"required"`
}

// --- Handlers ---

func (h *CuratedCollectionsHandler) create(c *gin.Context) {
	var req curatedImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id, err := h.Svc.StartImport(c.Request.Context(), services.ImportParams{
		Name:      req.Name,
		SourceURL: req.SourceURL,
		Items:     req.Items,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, curatedImportResponse{
		CollectionID: id,
		Status:       models.ImportStatusImporting,
	})
}

func (h *CuratedCollectionsHandler) list(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	out, total, err := h.Svc.List(c.Request.Context(), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if out == nil {
		out = []models.BookCollection{}
	}
	c.JSON(http.StatusOK, curatedListResponse{
		Rows:     out,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

func (h *CuratedCollectionsHandler) get(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	col, err := h.Svc.Get(c.Request.Context(), id)
	if err != nil {
		respondCollectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, col)
}

func (h *CuratedCollectionsHandler) status(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	col, err := h.Svc.Get(c.Request.Context(), id)
	if err != nil {
		respondCollectionError(c, err)
		return
	}
	resp := curatedStatusResponse{
		Status:      col.ImportStatus,
		ImportError: col.ImportError,
	}
	if col.ImportStats != nil {
		resp.Stats = *col.ImportStats
	}
	c.JSON(http.StatusOK, resp)
}

func (h *CuratedCollectionsHandler) items(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	statusFilter := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))

	items, total, err := h.Svc.ListItems(c.Request.Context(), id, statusFilter, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []models.BookCollectionItem{}
	}
	c.JSON(http.StatusOK, curatedItemsResponse{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

func (h *CuratedCollectionsHandler) patch(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	var req curatedPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	patch := database.CuratedCollectionPatch{
		Name:      req.Name,
		IsPublic:  req.IsPublic,
		SourceURL: req.SourceURL,
	}
	if err := h.Svc.Update(c.Request.Context(), id, patch); err != nil {
		respondCollectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *CuratedCollectionsHandler) delete(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	if err := h.Svc.Delete(c.Request.Context(), id); err != nil {
		respondCollectionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *CuratedCollectionsHandler) resolve(c *gin.Context) {
	itemID, ok := parseInt64Param(c, "itemID")
	if !ok {
		return
	}
	var req curatedResolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var decidedBy *int64
	if v, exists := c.Get("user_id"); exists {
		if uid, ok := v.(int64); ok {
			decidedBy = &uid
		}
	}
	if err := h.Svc.Resolve(c.Request.Context(), itemID, req.BookID, decidedBy); err != nil {
		respondCollectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *CuratedCollectionsHandler) ignore(c *gin.Context) {
	itemID, ok := parseInt64Param(c, "itemID")
	if !ok {
		return
	}
	if err := h.Svc.Ignore(c.Request.Context(), itemID); err != nil {
		respondCollectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *CuratedCollectionsHandler) autoResolve(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	var decidedBy *int64
	if v, exists := c.Get("user_id"); exists {
		if uid, ok := v.(int64); ok {
			decidedBy = &uid
		}
	}
	resolved, err := h.Svc.AutoResolveAmbiguous(c.Request.Context(), id, decidedBy)
	if err != nil {
		respondCollectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"resolved": resolved})
}

func (h *CuratedCollectionsHandler) llmResolve(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	var decidedBy *int64
	if v, exists := c.Get("user_id"); exists {
		if uid, ok := v.(int64); ok {
			decidedBy = &uid
		}
	}
	// LLM resolution iterates one OpenAI call per ambiguous item — for a
	// collection with hundreds of items this easily exceeds the proxy /
	// axios timeout. Run it in the background with a detached context so
	// client disconnects do not cancel mid-loop database writes.
	go func(cid int64, decidedBy *int64) {
		_, err := h.Svc.LLMResolveAmbiguous(context.Background(), cid, decidedBy)
		if err != nil {
			// Logged inside the service; nothing else to do here.
			_ = err
		}
	}(id, decidedBy)
	c.JSON(http.StatusAccepted, gin.H{"status": "started"})
}

// parseInt64Param reads an int64 path parameter and writes 400 if it is malformed.
func parseInt64Param(c *gin.Context, name string) (int64, bool) {
	raw := c.Param(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + name})
		return 0, false
	}
	return id, true
}

// respondCollectionError maps repo errors to HTTP responses: pg.ErrNoRows → 404,
// everything else → 500.
func respondCollectionError(c *gin.Context, err error) {
	if errors.Is(err, pg.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
