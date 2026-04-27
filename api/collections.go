package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"gopds-api/database"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
)

// PublicCollectionsService is the read-only view of curated collections exposed
// to authenticated users. Implementations must never return drafts (is_public=false)
// or UGC (is_curated=false) — that filtering is the implementation's responsibility,
// the handler trusts what it gets.
type PublicCollectionsService interface {
	List(ctx context.Context) ([]models.BookCollection, error)
	Get(ctx context.Context, id int64) (*models.BookCollection, error)
	Books(ctx context.Context, collectionID int64) ([]models.Book, error)
	Covers(ctx context.Context, collectionIDs []int64) (map[int64][]database.CollectionCoverBook, error)
}

// PublicCollectionsHandler binds PublicCollectionsService to gin routes.
type PublicCollectionsHandler struct {
	Svc PublicCollectionsService
}

// Register attaches list + detail endpoints. Caller wraps the group with auth middleware.
func (h *PublicCollectionsHandler) Register(r *gin.RouterGroup) {
	r.GET("", h.list)
	r.GET("/:id", h.get)
}

// publicCollectionDTO is the list-row shape: name + creation timestamp, no admin
// fields. CoverBooks carries up to 4 books of the collection (real ones if the
// library has covers, plus optional placeholders) so the catalog can render a
// 2x2 cover mosaic on each card.
type publicCollectionDTO struct {
	ID         int64                          `json:"id"`
	Name       string                         `json:"name"`
	CreatedAt  time.Time                      `json:"created_at"`
	CoverBooks []database.CollectionCoverBook `json:"cover_books"`
}

// publicCollectionDetailDTO carries the curated book list. We pass through the existing
// models.Book (same shape as elsewhere on the public API) and add no items metadata.
type publicCollectionDetailDTO struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Books     []models.Book `json:"books"`
	CreatedAt time.Time     `json:"created_at"`
}

func (h *PublicCollectionsHandler) list(c *gin.Context) {
	out, err := h.Svc.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ids := make([]int64, 0, len(out))
	for _, col := range out {
		ids = append(ids, col.ID)
	}
	covers, err := h.Svc.Covers(c.Request.Context(), ids)
	if err != nil {
		// Don't fail the whole list because of a cover-strip glitch — just send
		// it without artwork; frontend renders a coverless fallback.
		covers = map[int64][]database.CollectionCoverBook{}
	}

	dtos := make([]publicCollectionDTO, 0, len(out))
	for _, col := range out {
		dtos = append(dtos, publicCollectionDTO{
			ID:         col.ID,
			Name:       col.Name,
			CreatedAt:  col.CreatedAt,
			CoverBooks: covers[col.ID],
		})
	}
	c.JSON(http.StatusOK, dtos)
}

func (h *PublicCollectionsHandler) get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	col, err := h.Svc.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, pg.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	books, err := h.Svc.Books(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if books == nil {
		books = []models.Book{}
	}
	c.JSON(http.StatusOK, publicCollectionDetailDTO{
		ID:        col.ID,
		Name:      col.Name,
		Books:     books,
		CreatedAt: col.CreatedAt,
	})
}
