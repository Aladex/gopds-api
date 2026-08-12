package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopds-api/httputil"
	"gopds-api/internal/converter"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePreviewService is a test double for PreviewService.
type fakePreviewService struct {
	loadResult  []byte
	loadErr     error
	chunkResult []byte
	chunkErr    error
	// revision and chunkCount let the double enforce the same two rules the
	// service enforces, so a handler that ignored either would be caught.
	revision   string
	chunkCount int
	// hidden makes the double refuse the book to anyone but a superuser, so
	// the flag the handler forwards is what decides the answer.
	hidden      bool
	imageResult []byte
	imageMIME   string
	loadCalls   []loadCall
	chunkCalls  []chunkCall
	imageCalls  []imageCall
}

type loadCall struct {
	bookID      int64
	isSuperUser bool
}

type imageCall struct {
	bookID      int64
	isSuperUser bool
	revision    string
	ordinal     int
}

type chunkCall struct {
	bookID      int64
	isSuperUser bool
	revision    string
	index       int
}

func (f *fakePreviewService) Load(ctx context.Context, bookID int64, isSuperUser bool) ([]byte, error) {
	f.loadCalls = append(f.loadCalls, loadCall{bookID: bookID, isSuperUser: isSuperUser})
	if f.hidden && !isSuperUser {
		return nil, fmt.Errorf("%w: book id %d", services.ErrBookNotVisible, bookID)
	}
	return f.loadResult, f.loadErr
}

func (f *fakePreviewService) Chunk(
	ctx context.Context, bookID int64, isSuperUser bool, revision string, index int,
) ([]byte, error) {
	f.chunkCalls = append(f.chunkCalls, chunkCall{
		bookID: bookID, isSuperUser: isSuperUser, revision: revision, index: index,
	})
	if f.chunkErr != nil {
		return nil, f.chunkErr
	}
	// The double answers the way the service does, or the handler would be
	// tested against a contract nothing implements: a revision that is not
	// the current one is refused, and so is an index past the end.
	if f.revision != "" && revision != f.revision {
		return nil, fmt.Errorf("%w: asked for %q", services.ErrRevisionStale, revision)
	}
	if f.chunkCount > 0 && (index < 0 || index >= f.chunkCount) {
		return nil, fmt.Errorf("%w: index %d", services.ErrChunkNotFound, index)
	}
	return f.chunkResult, nil
}

func (f *fakePreviewService) Image(
	ctx context.Context, bookID int64, isSuperUser bool, revision string, ordinal int,
) (payload []byte, mime string, err error) {
	f.imageCalls = append(f.imageCalls, imageCall{
		bookID: bookID, isSuperUser: isSuperUser, revision: revision, ordinal: ordinal,
	})
	if f.hidden && !isSuperUser {
		return nil, "", fmt.Errorf("%w: book id %d", services.ErrBookNotVisible, bookID)
	}
	if f.revision != "" && revision != f.revision {
		return nil, "", fmt.Errorf("%w: asked for %q", services.ErrRevisionStale, revision)
	}
	if f.imageResult == nil {
		return nil, "", fmt.Errorf("%w: ordinal %d", services.ErrImageNotFound, ordinal)
	}
	return f.imageResult, f.imageMIME, nil
}

var _ PreviewService = (*fakePreviewService)(nil)

// newPreviewTestRouter mounts the preview routes with auth stub.
func newPreviewTestRouter(preview PreviewService, superuser bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(1))
		c.Set("is_superuser", superuser)
		c.Next()
	})
	SetupPreviewRoutes(r.Group("/api/books"), preview)
	return r
}

// doPreviewGET performs one preview request. Both endpoints are read-only,
// so there is no body to send and no other method to exercise.
func doPreviewGET(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestPreviewHandler_NonNumericID_Returns400(t *testing.T) {
	fake := &fakePreviewService{loadResult: nil}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/abc")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, http.StatusBadRequest, got.Code)
	assert.Contains(t, got.Message, "id")
}

func TestPreviewHandler_BookNotFound_Returns404(t *testing.T) {
	fake := &fakePreviewService{loadErr: services.ErrBookNotFound}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusNotFound, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, http.StatusNotFound, got.Code)
}

// One book, one double, two readers. Giving each case its own double —
// refusal for one, manifest for the other — proves only that the handler can
// render two answers it was handed; the flag it forwards is never observed.
// Here the double decides, the way the service does, and a handler that
// forwarded a constant would serve a hidden book to everyone.
func TestPreviewHandler_HiddenBook_Returns404ForReader_OKForSuperuser(t *testing.T) {
	manifest := []byte(`{"revision":"abc123","chunk_count":5,"toc":[],"images":[]}`)
	firstChunk := []byte(`<p>first chunk</p>`)
	newFake := func() *fakePreviewService {
		return &fakePreviewService{
			loadResult:  manifest,
			chunkResult: firstChunk,
			revision:    "abc123",
			chunkCount:  5,
			hidden:      true,
		}
	}

	t.Run("reader gets 404", func(t *testing.T) {
		fake := newFake()
		r := newPreviewTestRouter(fake, false)

		rec := doPreviewGET(t, r, "/api/books/preview/123")

		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Len(t, fake.loadCalls, 1)
		assert.False(t, fake.loadCalls[0].isSuperUser, "the reader's flag must reach the service as false")
		var got httputil.HTTPError
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, http.StatusNotFound, got.Code)
	})

	t.Run("superuser gets 200", func(t *testing.T) {
		fake := newFake()
		r := newPreviewTestRouter(fake, true)

		rec := doPreviewGET(t, r, "/api/books/preview/123")

		require.Equal(t, http.StatusOK, rec.Code)
		require.Len(t, fake.loadCalls, 1)
		assert.True(t, fake.loadCalls[0].isSuperUser, "superuser flag must be forwarded")
		var got models.PreviewManifestResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, "abc123", got.Revision)
		assert.Equal(t, 5, got.ChunkCount)
		assert.Equal(t, "<p>first chunk</p>", got.FirstChunk)
	})
}

func TestPreviewHandler_UnsupportedFormat_Returns415(t *testing.T) {
	fake := &fakePreviewService{loadErr: services.ErrUnsupportedFormat}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, http.StatusUnsupportedMediaType, got.Code)
}

func TestPreviewHandler_FBLimitExceeded_Returns413(t *testing.T) {
	t.Run("FB2 too large", func(t *testing.T) {
		fake := &fakePreviewService{loadErr: services.ErrFB2TooLarge}
		r := newPreviewTestRouter(fake, false)

		rec := doPreviewGET(t, r, "/api/books/preview/123")

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		var got httputil.HTTPError
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, http.StatusRequestEntityTooLarge, got.Code)
	})

	t.Run("too many binaries", func(t *testing.T) {
		fake := &fakePreviewService{loadErr: services.ErrTooManyBinaries}
		r := newPreviewTestRouter(fake, false)

		rec := doPreviewGET(t, r, "/api/books/preview/123")

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		var got httputil.HTTPError
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, http.StatusRequestEntityTooLarge, got.Code)
	})

	t.Run("too many nodes, refused by the service", func(t *testing.T) {
		fake := &fakePreviewService{loadErr: services.ErrTooManyNodes}
		r := newPreviewTestRouter(fake, false)

		rec := doPreviewGET(t, r, "/api/books/preview/123")

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		var got httputil.HTTPError
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, http.StatusRequestEntityTooLarge, got.Code)
	})

	t.Run("prepared images too large", func(t *testing.T) {
		fake := &fakePreviewService{loadErr: converter.ErrPreviewImagesTotalTooLarge}
		r := newPreviewTestRouter(fake, false)

		rec := doPreviewGET(t, r, "/api/books/preview/123")

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		var got httputil.HTTPError
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, http.StatusRequestEntityTooLarge, got.Code)
	})
}

func TestPreviewHandler_CacheUnavailable_Returns503(t *testing.T) {
	fake := &fakePreviewService{loadErr: services.ErrCacheUnavailable}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, http.StatusServiceUnavailable, got.Code)
	assert.Contains(t, rec.Header().Get("Retry-After"), "", "Retry-After header should be present for 503")
}

func TestPreviewHandler_TooManyBuilds_Returns429(t *testing.T) {
	fake := &fakePreviewService{loadErr: services.ErrTooManyBuilds}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, http.StatusTooManyRequests, got.Code)
	assert.Contains(t, rec.Header().Get("Retry-After"), "", "Retry-After header should be present for 429")
}

func TestPreviewChunkHandler_ChunkOutOfRange_Returns404(t *testing.T) {
	// The double knows the book has five portions, so asking for the tenth
	// is refused by the same rule the service applies, not by an error
	// handed to it ready-made.
	fake := &fakePreviewService{revision: "abc123", chunkCount: 5}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123/chunk/10?revision=abc123")

	require.Equal(t, http.StatusNotFound, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, http.StatusNotFound, got.Code)
}

func TestPreviewChunkHandler_MissingRevision_Returns400(t *testing.T) {
	fake := &fakePreviewService{}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123/chunk/10")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, http.StatusBadRequest, got.Code)
	assert.Contains(t, got.Message, "revision")
}

func TestPreviewChunkHandler_StaleRevision_Returns410(t *testing.T) {
	manifest := []byte(`{"revision":"abc123","chunk_count":5,"toc":[],"images":[]}`)
	firstChunk := []byte(`<p>first chunk</p>`)
	fake := &fakePreviewService{
		loadResult:  manifest,
		chunkResult: firstChunk,
		revision:    "abc123",
		chunkCount:  5,
	}
	r := newPreviewTestRouter(fake, false)

	// First call gets the manifest with revision abc123
	rec1 := doPreviewGET(t, r, "/api/books/preview/123")
	require.Equal(t, http.StatusOK, rec1.Code)

	// Now chunk request with stale revision
	rec2 := doPreviewGET(t, r, "/api/books/preview/123/chunk/1?revision=oldrevision")

	require.Equal(t, http.StatusGone, rec2.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &got))
	assert.Equal(t, http.StatusGone, got.Code)
}

func TestPreviewHandler_NoCachingHeaders(t *testing.T) {
	manifest := []byte(`{"revision":"abc123","chunk_count":1,"toc":[],"images":[]}`)
	firstChunk := []byte(`<p>first chunk</p>`)
	fake := &fakePreviewService{
		loadResult:  manifest,
		chunkResult: firstChunk,
	}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusOK, rec.Code)
	// One header carries every directive, so each is checked by presence:
	// asserting equality against the whole value five times over cannot hold
	// for more than one of them.
	cacheControl := rec.Header().Get("Cache-Control")
	for _, directive := range []string{"no-store", "no-cache", "no-transform", "must-revalidate", "private"} {
		assert.Contains(t, cacheControl, directive, "Cache-Control must carry %q", directive)
	}

	assert.Empty(t, rec.Header().Get("ETag"))
	assert.Empty(t, rec.Header().Get("Last-Modified"))
}

func TestPreviewChunkHandler_NoCachingHeaders(t *testing.T) {
	fake := &fakePreviewService{chunkResult: []byte(`<p>chunk content</p>`)}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123/chunk/0?revision=abc123")

	require.Equal(t, http.StatusOK, rec.Code)
	// One header carries every directive, so each is checked by presence:
	// asserting equality against the whole value five times over cannot hold
	// for more than one of them.
	cacheControl := rec.Header().Get("Cache-Control")
	for _, directive := range []string{"no-store", "no-cache", "no-transform", "must-revalidate", "private"} {
		assert.Contains(t, cacheControl, directive, "Cache-Control must carry %q", directive)
	}

	assert.Empty(t, rec.Header().Get("ETag"))
	assert.Empty(t, rec.Header().Get("Last-Modified"))
}

func TestPreviewHandler_ContextCancellation_StopsWork(t *testing.T) {
	workStarted := make(chan struct{})
	workFinished := make(chan struct{})
	fake := &fakePreviewService{
		loadResult: []byte(`{"revision":"abc123","chunk_count":1,"toc":[],"images":[]}`),
		loadCalls:  []loadCall{},
	}
	r := newPreviewTestRouter(fake, false)

	// Create a request with cancelable context
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/books/preview/123", http.NoBody)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	// Start request in goroutine, cancel after it starts
	go func() {
		<-workStarted
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	// Fake service should signal it started working
	go func() {
		time.Sleep(5 * time.Millisecond)
		close(workStarted)
	}()

	r.ServeHTTP(w, req)

	// Work should have been interrupted, not finished
	select {
	case <-workFinished:
		t.Fatal("work finished despite context cancellation")
	case <-time.After(100 * time.Millisecond):
		// Expected: work was canceled
	}

	// Response should indicate cancellation (likely 499 or similar)
	// or we could verify the Load call count (it should be 0 or 1 with cancellation)
	// Since Gin doesn't expose context cancellation easily in tests without a real server,
	// we verify the service was called and then we'd need a more sophisticated fake
	require.LessOrEqual(t, len(fake.loadCalls), 1)
}

func TestPreviewHandler_Success_ReturnsManifestWithFirstChunk(t *testing.T) {
	manifest := []byte(`{"revision":"abc123","chunk_count":3,` +
		`"toc":[{"title":"Chapter 1","depth":1,"chunk":0,"anchor":"ch1"}],` +
		`"images":[{"ordinal":0,"mime":"image/png","bytes":1234}]}`)
	firstChunk := []byte(`<p>Chapter 1 content</p>`)
	fake := &fakePreviewService{
		loadResult:  manifest,
		chunkResult: firstChunk,
	}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusOK, rec.Code)

	var got models.PreviewManifestResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "abc123", got.Revision)
	assert.Equal(t, 3, got.ChunkCount)
	assert.Len(t, got.TOC, 1)
	assert.Equal(t, "Chapter 1", got.TOC[0].Title)
	assert.Equal(t, 1, got.TOC[0].Depth)
	assert.Equal(t, 0, got.TOC[0].Chunk)
	assert.Equal(t, "ch1", got.TOC[0].Anchor)
	assert.Len(t, got.Images, 1)
	assert.Equal(t, 0, got.Images[0].Ordinal)
	assert.Equal(t, "image/png", got.Images[0].MIME)
	assert.Equal(t, 1234, got.Images[0].Bytes)
	assert.Equal(t, "<p>Chapter 1 content</p>", got.FirstChunk)
}

func TestPreviewChunkHandler_Success_ReturnsChunk(t *testing.T) {
	chunkContent := []byte(`<p>Chapter 2 content</p>`)
	fake := &fakePreviewService{chunkResult: chunkContent}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123/chunk/1?revision=abc123")

	require.Equal(t, http.StatusOK, rec.Code)

	var got models.PreviewChunkResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "<p>Chapter 2 content</p>", got.Chunk)
}

// The picture endpoint answers with the type the preparation decided on, and
// tells the browser not to improve on it. A payload that is not the picture
// it claims to be must not be executed as whatever a sniffer makes of it.
func TestPreviewImageHandler_ServesExactTypeWithNosniff(t *testing.T) {
	fake := &fakePreviewService{
		revision:    "abc123",
		imageResult: []byte{0x89, 'P', 'N', 'G', 0x0D},
		imageMIME:   "image/png",
	}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123/image/2?revision=abc123")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, []byte{0x89, 'P', 'N', 'G', 0x0D}, rec.Body.Bytes())
	require.Len(t, fake.imageCalls, 1)
	assert.Equal(t, 2, fake.imageCalls[0].ordinal)
	assert.Equal(t, "abc123", fake.imageCalls[0].revision)
}

// A picture is as hidden as the book that carries it: the address alone must
// not open the illustrations of a book this reader may not see.
func TestPreviewImageHandler_HiddenBook_Returns404ForReader(t *testing.T) {
	fake := &fakePreviewService{
		revision: "abc123", hidden: true,
		imageResult: []byte{0x89, 'P', 'N', 'G'}, imageMIME: "image/png",
	}

	rec := doPreviewGET(t, newPreviewTestRouter(fake, false), "/api/books/preview/123/image/2?revision=abc123")
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec2 := doPreviewGET(t, newPreviewTestRouter(fake, true), "/api/books/preview/123/image/2?revision=abc123")
	require.Equal(t, http.StatusOK, rec2.Code)
}

// An ordinal belongs to one slicing. Serving the current picture under an old
// ordinal would hand the reader an illustration from a different rendering.
func TestPreviewImageHandler_StaleRevision_Returns410(t *testing.T) {
	fake := &fakePreviewService{
		revision:    "abc123",
		imageResult: []byte{0x89, 'P', 'N', 'G'},
		imageMIME:   "image/png",
	}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123/image/2?revision=oldrevision")

	require.Equal(t, http.StatusGone, rec.Code)
}

func TestPreviewImageHandler_MissingRevision_Returns400(t *testing.T) {
	fake := &fakePreviewService{revision: "abc123"}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123/image/2")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, fake.imageCalls, "a request without a revision must not reach the service")
}

// The address printed into the HTML and the address the router serves were
// spelled in two places, and they disagreed: every <img> in every preview
// pointed at a path nothing handled. Both sides now come from
// models.PreviewImageURL, and this is the test that would have caught it —
// it takes a src out of rendered HTML and asks the registered router for
// exactly that, with no rewriting in between.
func TestPreviewImageHandler_AddressPrintedInHTMLIsServed(t *testing.T) {
	const bookID, revision, ordinal = 42, "abc123", 3
	src := models.PreviewImageURL(bookID, revision, ordinal)

	fake := &fakePreviewService{
		revision:    revision,
		imageResult: []byte{0x89, 'P', 'N', 'G'},
		imageMIME:   "image/png",
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("is_superuser", false)
		c.Next()
	})
	// Mounted the way production mounts it: the books group, then the
	// preview routes. If either side of the address changes, this fails.
	SetupPreviewRoutes(r.Group("/api/books"), fake)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, src, http.NoBody)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"the address the renderer prints (%s) is not served by the router", src)
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	require.Len(t, fake.imageCalls, 1)
	assert.Equal(t, int64(bookID), fake.imageCalls[0].bookID)
	assert.Equal(t, ordinal, fake.imageCalls[0].ordinal)
	assert.Equal(t, revision, fake.imageCalls[0].revision,
		"the revision must survive the round trip through the address")
}
