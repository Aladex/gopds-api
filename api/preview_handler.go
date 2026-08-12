package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"gopds-api/httputil"
	"gopds-api/internal/converter"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
)

// PreviewService is what the HTTP layer needs from the preview: a table of
// contents, a portion, a picture. Nothing here mentions a cache or a key —
// where the bytes live and how they are addressed is the service's business,
// and a handler that spelled a cache key would be a second authority on the
// format, free to drift from the first.
type PreviewService interface {
	Load(ctx context.Context, bookID int64, isSuperUser bool) ([]byte, error)
	Chunk(ctx context.Context, bookID int64, isSuperUser bool, revision string, index int) ([]byte, error)
	Image(ctx context.Context, bookID int64, isSuperUser bool, revision string, ordinal int) ([]byte, string, error)
}

// PreviewHandler turns the preview service's typed refusals into answers a
// reader and a client can tell apart. It holds nothing else: the book, the
// cache and the revision rules all belong to the service.
type PreviewHandler struct {
	preview PreviewService
}

// NewPreviewHandler creates a new preview handler.
func NewPreviewHandler(preview PreviewService) *PreviewHandler {
	return &PreviewHandler{preview: preview}
}

// SetupPreviewRoutes sets up preview routes.
func SetupPreviewRoutes(r *gin.RouterGroup, preview PreviewService) {
	h := NewPreviewHandler(preview)
	r.GET("/preview/:id", h.GetPreview)
	r.GET("/preview/:id/chunk/:n", h.GetPreviewChunk)
}

// GetPreview returns the preview manifest with the first chunk inline.
func (h *PreviewHandler) GetPreview(c *gin.Context) {
	// Set anti-caching headers immediately
	setNoCacheHeaders(c)

	bookIDStr := c.Param("id")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid book id: %w", err))
		return
	}

	isSuperUser := c.GetBool("is_superuser")

	manifestJSON, err := h.preview.Load(c.Request.Context(), bookID, isSuperUser)
	if err != nil {
		mapPreviewError(c, err)
		return
	}

	var manifest services.PreviewManifest
	if uerr := json.Unmarshal(manifestJSON, &manifest); uerr != nil {
		httputil.NewError(c, http.StatusInternalServerError, fmt.Errorf("failed to unmarshal manifest: %w", uerr))
		return
	}

	// The first portion travels with the table of contents. Two requests
	// would cost either a waterfall or two independent opening states to
	// reconcile on the client, and a cold build has already put every portion
	// in the cache by the time the manifest exists.
	firstChunk, err := h.preview.Chunk(c.Request.Context(), bookID, isSuperUser, manifest.Revision, 0)
	if err != nil {
		mapPreviewError(c, err)
		return
	}

	response := models.PreviewManifestResponse{
		Revision:   manifest.Revision,
		ChunkCount: manifest.ChunkCount,
		TOC:        convertTOC(manifest.TOC),
		Images:     convertImages(manifest.Images),
		FirstChunk: string(firstChunk),
	}

	c.JSON(http.StatusOK, response)
}

// convertTOC converts services.PreviewTOCEntry to models.PreviewTOCEntry.
func convertTOC(toc []services.PreviewTOCEntry) []models.PreviewTOCEntry {
	result := make([]models.PreviewTOCEntry, len(toc))
	for i, entry := range toc {
		result[i] = models.PreviewTOCEntry{
			Title:  entry.Title,
			Depth:  entry.Depth,
			Chunk:  entry.Chunk,
			Anchor: entry.Anchor,
		}
	}
	return result
}

// convertImages converts services.PreviewImageRef to models.PreviewImageRef.
func convertImages(images []services.PreviewImageRef) []models.PreviewImageRef {
	result := make([]models.PreviewImageRef, len(images))
	for i, img := range images {
		result[i] = models.PreviewImageRef{
			Ordinal: img.Ordinal,
			MIME:    img.MIME,
			Bytes:   img.Bytes,
		}
	}
	return result
}

// GetPreviewChunk returns a specific chunk by index.
func (h *PreviewHandler) GetPreviewChunk(c *gin.Context) {
	// Set anti-caching headers immediately
	setNoCacheHeaders(c)

	bookIDStr := c.Param("id")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid book id: %w", err))
		return
	}

	chunkIndexStr := c.Param("n")
	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid chunk index: %w", err))
		return
	}

	revision := c.Query("revision")
	if revision == "" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("revision parameter is required"))
		return
	}

	isSuperUser := c.GetBool("is_superuser")

	// The revision is checked by the service, against the one it would build
	// right now. Checking it here would mean the rule "the revision must
	// agree" lives in two places, and the handler would have to know how a
	// revision is derived to know what it is comparing against.
	chunk, err := h.preview.Chunk(c.Request.Context(), bookID, isSuperUser, revision, chunkIndex)
	if err != nil {
		mapPreviewError(c, err)
		return
	}

	response := models.PreviewChunkResponse{
		Chunk: string(chunk),
	}

	c.JSON(http.StatusOK, response)
}

// setNoCacheHeaders sets headers that prevent caching of the response.
func setNoCacheHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, no-transform, must-revalidate, private")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

// mapPreviewError maps PreviewService errors to HTTP status codes.
func mapPreviewError(c *gin.Context, err error) {
	switch {
	// A book that is not there and a book this reader may not see answer
	// alike on purpose: telling them apart would let anyone probe which
	// hidden books exist by watching the status change.
	case errors.Is(err, services.ErrBookNotFound),
		errors.Is(err, services.ErrBookNotVisible):
		httputil.NewError(c, http.StatusNotFound, err)

	// The book exists and is visible; the preview simply does not read this
	// format. Nothing about the request or the moment will change that.
	case errors.Is(err, services.ErrUnsupportedFormat),
		errors.Is(err, services.ErrEmptyMD5):
		httputil.NewError(c, http.StatusUnsupportedMediaType, err)

	// The book is beyond what the service will spend on one preview. Also
	// permanent for this book, but distinct from "wrong format": the client
	// can say why, and the catalog can be measured for how often it happens.
	// Both spellings are listed on purpose: the service refuses with its own
	// names when it checks a gate itself, and passes the parser's through
	// when the parser stops first. A mapping that knew only one of the two
	// would answer 500 for half the refusals it exists to describe.
	case errors.Is(err, services.ErrFB2TooLarge),
		errors.Is(err, services.ErrTooManyBinaries),
		errors.Is(err, services.ErrTooManyNodes),
		errors.Is(err, services.ErrPreparedImagesTooLarge),
		errors.Is(err, converter.ErrFB2NodeLimit),
		errors.Is(err, converter.ErrFB2BinaryLimit),
		errors.Is(err, converter.ErrPreviewImagesTotalTooLarge):
		httputil.NewError(c, http.StatusRequestEntityTooLarge, err)

	// The portion or picture is gone: past the end of this slicing, or the
	// cached entry expired between the table of contents and this request.
	case errors.Is(err, services.ErrChunkNotFound),
		errors.Is(err, services.ErrImageNotFound):
		httputil.NewError(c, http.StatusNotFound, err)

	// The reader holds a table of contents for a slicing that no longer
	// exists. Not an error in the request and not a missing resource: the
	// resource was there and is gone, which is what Gone says. The client
	// opens the preview again rather than retrying this address.
	case errors.Is(err, services.ErrRevisionStale):
		httputil.NewError(c, http.StatusGone, err)

	// Both of these are the server saying "not now". Retry-After is set
	// before the body, because writing the body commits the header block.
	case errors.Is(err, services.ErrCacheUnavailable):
		c.Header("Retry-After", "60")
		httputil.NewError(c, http.StatusServiceUnavailable, err)
	case errors.Is(err, services.ErrTooManyBuilds):
		c.Header("Retry-After", "10")
		httputil.NewError(c, http.StatusTooManyRequests, err)

	// A reader who left takes the answer with them; no status reaches anyone.
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		_ = c.Error(err)
		c.Abort()

	default:
		httputil.NewError(c, http.StatusInternalServerError, err)
	}
}
