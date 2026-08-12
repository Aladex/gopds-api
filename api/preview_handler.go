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
	"gopds-api/internal/parser"
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
	r.GET(models.PreviewImageRoutePattern, h.GetPreviewImage)
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
		// Through the same exit as every other refusal: the cause to the log,
		// a fixed sentence to the reader. Answering with the parse error told
		// the reader what our cached bytes look like and left the cause
		// nowhere.
		mapPreviewError(c, fmt.Errorf("preview: cached manifest is unreadable: %w", uerr))
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

// GetPreviewImage serves one prepared picture. It is a resource of its own
// because the HTML addresses pictures rather than carrying them: a portion
// stays small and cacheable-by-the-client, and a picture nobody scrolls to is
// never fetched.
//
// The same visibility check as the manifest applies — the address alone must
// not open a hidden book's illustrations — and the same revision agreement:
// an ordinal belongs to one slicing, and answering with the current picture
// under an old ordinal would hand the reader someone else's illustration.
func (h *PreviewHandler) GetPreviewImage(c *gin.Context) {
	setNoCacheHeaders(c)

	bookID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid book id: %w", err))
		return
	}
	ordinal, err := strconv.Atoi(c.Param("n"))
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid image ordinal: %w", err))
		return
	}
	revision := c.Query("revision")
	if revision == "" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("revision parameter is required"))
		return
	}

	payload, mime, err := h.preview.Image(c.Request.Context(), bookID, c.GetBool("is_superuser"), revision, ordinal)
	if err != nil {
		mapPreviewError(c, err)
		return
	}

	// The type comes from the bytes the preparation produced, not from what
	// the book claimed, and nosniff keeps a browser from improving on it:
	// a payload that is not the picture it says it is must not be executed
	// as whatever a sniffer decides it looks like.
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, mime, payload)
}

// setNoCacheHeaders sets headers that prevent caching of the response.
func setNoCacheHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, no-transform, must-revalidate, private")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

// mapPreviewError maps PreviewService errors to HTTP status codes.
// reasonBookNotFound is the one sentence both an absent book and a book this
// reader may not see receive. It is a constant because the two must never
// drift apart: any difference between them enumerates hidden books.
const reasonBookNotFound = "book not found"

// previewRefusal is one public answer: a status and the sentence a reader
// gets. The sentence is fixed, never the error's own text — an internal
// message carries cache addresses, key material and the book's fingerprint,
// and "book not found" versus "book is not visible to this reader" tells a
// prober which hidden books exist even when the status is the same.
type previewRefusal struct {
	status int
	reason string
	// retryAfter, when set, is the number of seconds after which the same
	// address is worth trying again. Only temporary refusals carry one.
	retryAfter string
}

// classifyPreviewError is the single place that decides what a refusal means
// to the outside. The HTTP mapping and, later, the refusal metric both read
// it, so a cause cannot be counted as one thing and answered as another.
func classifyPreviewError(err error) previewRefusal {
	switch {
	// Absent and invisible answer identically, status and sentence both:
	// any difference between them is a way to enumerate hidden books.
	case errors.Is(err, services.ErrBookNotFound),
		errors.Is(err, services.ErrBookNotVisible):
		return previewRefusal{status: http.StatusNotFound, reason: reasonBookNotFound}

	// The preview reads FB2 and nothing else. Permanent for this book.
	case errors.Is(err, services.ErrUnsupportedFormat):
		return previewRefusal{status: http.StatusUnsupportedMediaType, reason: "preview is not available for this format"}

	// A catalog row without a fingerprint cannot be keyed, so no preview
	// can be built for it. That is a defect in our data, not a property of
	// the request, and it must not masquerade as a media type problem.
	case errors.Is(err, services.ErrEmptyMD5):
		return previewRefusal{status: http.StatusInternalServerError, reason: "preview is unavailable for this book"}

	// Beyond what the service will spend on one preview: too big, too many
	// pictures, too many nodes, too much prepared weight, or a single block
	// that cannot be split under the portion ceiling. All permanent for the
	// book, and distinct from "wrong format" so a client can say why.
	case errors.Is(err, services.ErrFB2TooLarge),
		errors.Is(err, services.ErrTooManyBinaries),
		errors.Is(err, services.ErrTooManyNodes),
		errors.Is(err, services.ErrPreparedImagesTooLarge),
		errors.Is(err, converter.ErrFB2NodeLimit),
		errors.Is(err, converter.ErrFB2BinaryLimit),
		errors.Is(err, converter.ErrPreviewImagesTotalTooLarge),
		errors.Is(err, converter.ErrPreviewBlockTooLarge),
		errors.Is(err, converter.ErrDepthLimit):
		return previewRefusal{status: http.StatusRequestEntityTooLarge, reason: "this book is too large to preview"}

	// The catalog row exists but the file it names is not in the archive.
	// A different fact from "no such book" and from "the archive is
	// unreadable", and the loader raises it as its own error precisely so
	// this layer can say so.
	case errors.Is(err, services.ErrArchiveFileNotFound):
		return previewRefusal{status: http.StatusNotFound, reason: "the book file is missing"}

	// The portion or picture is gone: past the end of this slicing, or the
	// entry expired between the table of contents and this request.
	case errors.Is(err, services.ErrChunkNotFound):
		return previewRefusal{status: http.StatusNotFound, reason: "no such portion"}
	case errors.Is(err, services.ErrImageNotFound):
		return previewRefusal{status: http.StatusNotFound, reason: "no such image"}

	// The file is not a book we can read: not a FictionBook at all, or
	// damaged past repair. Permanent, and a property of the content rather
	// than a fault of ours — a bare 500 told the client to retry something
	// that will never succeed.
	case errors.Is(err, converter.ErrNotFictionBook),
		errors.Is(err, parser.ErrDamagedContent):
		return previewRefusal{status: http.StatusUnsupportedMediaType, reason: "this book cannot be read"}

	// The reader holds a table of contents for a slicing that no longer
	// exists. The resource was there and is gone, so the client opens the
	// preview again rather than retrying this address.
	case errors.Is(err, services.ErrRevisionStale):
		return previewRefusal{status: http.StatusGone, reason: "the preview has been rebuilt, open it again"}

	// The deadline is tested before the cache, and the order is the whole
	// point: a cache operation cut short by the build's own deadline matches
	// BOTH sentinels, and whichever is asked first decides the answer. Our
	// deadline is the more specific statement — we ran out of the time we
	// gave ourselves — so it wins, and the reader is told to come back in
	// thirty seconds rather than sixty.
	case errors.Is(err, context.DeadlineExceeded):
		return previewRefusal{status: http.StatusServiceUnavailable, reason: "the preview took too long to build", retryAfter: "30"}

	case errors.Is(err, services.ErrCacheUnavailable):
		return previewRefusal{status: http.StatusServiceUnavailable, reason: "preview storage is unavailable", retryAfter: "60"}

	case errors.Is(err, services.ErrTooManyBuilds):
		return previewRefusal{status: http.StatusTooManyRequests, reason: "too many previews are being built", retryAfter: "10"}
	}
	return previewRefusal{status: http.StatusInternalServerError, reason: "preview is unavailable"}
}

// mapPreviewError writes the public answer for a refusal.
func mapPreviewError(c *gin.Context, err error) {
	// A reader who hung up takes the answer with them. This is asked of the
	// request's own context, not of the error: a build that exceeded the
	// server's deadline raises the same context error while the reader is
	// still waiting, and it must get a status rather than silence.
	if c.Request.Context().Err() != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}

	refusal := classifyPreviewError(err)
	if refusal.retryAfter != "" {
		c.Header("Retry-After", refusal.retryAfter)
	}
	// The cause goes to the log, the sentence goes to the reader.
	_ = c.Error(err)
	httputil.NewError(c, refusal.status, errors.New(refusal.reason))
}
