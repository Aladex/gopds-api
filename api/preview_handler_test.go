package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopds-api/httputil"
	"gopds-api/internal/converter"
	"gopds-api/internal/parser"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePreviewService is a test double for PreviewService.
type fakePreviewService struct {
	loadResult []byte
	loadErr    error
	// loadFunc, when set, replaces the canned answer: a test that needs to
	// act while the handler is inside the service uses it to get there.
	loadFunc    func(ctx context.Context) ([]byte, error)
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
	if f.loadFunc != nil {
		return f.loadFunc(ctx)
	}
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
	if f.hidden && !isSuperUser {
		return nil, fmt.Errorf("%w: book id %d", services.ErrBookNotVisible, bookID)
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

	t.Run("one block will not fit a portion", func(t *testing.T) {
		// A block that cannot be split under the portion ceiling refuses the
		// book as surely as its size does. Left out of the table it became a
		// 500, telling the client to retry something that will never work.
		fake := &fakePreviewService{loadErr: converter.ErrPreviewBlockTooLarge}
		r := newPreviewTestRouter(fake, false)

		rec := doPreviewGET(t, r, "/api/books/preview/123")

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
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
	assert.Equal(t, "60", rec.Header().Get("Retry-After"), "an unavailable cache must say how long to wait")
}

func TestPreviewHandler_TooManyBuilds_Returns429(t *testing.T) {
	fake := &fakePreviewService{loadErr: services.ErrTooManyBuilds}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, http.StatusTooManyRequests, got.Code)
	assert.Equal(t, "10", rec.Header().Get("Retry-After"), "a busy server must say how long to wait")
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

// A reader who hangs up gets no answer written: the handler stops rather
// than composing a status nobody will read. The service's own build is a
// separate matter and deliberately survives — that is proven in the service
// tests, not here.
//
// The previous version of this test observed nothing: it waited on a channel
// nobody ever closed and signaled from an unrelated goroutine, so it passed
// whatever the handler did.
func TestPreviewHandler_CanceledRequestWritesNothing(t *testing.T) {
	reached := make(chan struct{})
	release := make(chan struct{})
	fake := &fakePreviewService{
		revision:   "abc123",
		chunkCount: 1,
		loadFunc: func(ctx context.Context) ([]byte, error) {
			close(reached)
			<-release
			return nil, ctx.Err()
		},
	}
	r := newPreviewTestRouter(fake, false)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/api/books/preview/123", http.NoBody)
	require.NoError(t, err)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		r.ServeHTTP(rec, req)
		close(done)
	}()

	<-reached // the handler is inside the service
	cancel()  // the reader hangs up
	close(release)
	<-done

	assert.Empty(t, rec.Body.String(), "no body may be written for a reader who left")
}

// A build that ran out of the server's own time is not a reader who left:
// the reader is still waiting and must be told to come back later.
func TestPreviewHandler_BuildDeadline_Returns503(t *testing.T) {
	fake := &fakePreviewService{loadErr: context.DeadlineExceeded}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "30", rec.Header().Get("Retry-After"))
}

// Absent and invisible must be indistinguishable in the answer, body
// included: a difference in the sentence enumerates hidden books just as
// well as a difference in the status.
func TestPreviewHandler_MissingAndHiddenAnswerIdentically(t *testing.T) {
	missing := doPreviewGET(t, newPreviewTestRouter(&fakePreviewService{
		loadErr: services.ErrBookNotFound,
	}, false), "/api/books/preview/123")

	hidden := doPreviewGET(t, newPreviewTestRouter(&fakePreviewService{
		revision: "abc123", hidden: true,
		loadResult: []byte(`{"revision":"abc123","chunk_count":1,"toc":[],"images":[]}`),
	}, false), "/api/books/preview/123")

	require.Equal(t, missing.Code, hidden.Code)
	assert.Equal(t, missing.Body.String(), hidden.Body.String(),
		"the two answers differ, so a prober can tell a hidden book from an absent one")
}

// No internal detail reaches the reader: not a cache address, not a key, not
// the book's fingerprint.
func TestPreviewHandler_RefusalLeaksNoInternals(t *testing.T) {
	internal := errors.New("preview cache: dial tcp 10.28.0.4:6379: connect: connection refused, key preview:v1:9f8e7d:42")
	fake := &fakePreviewService{loadErr: fmt.Errorf("%w: %v", services.ErrCacheUnavailable, internal)}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	for _, secret := range []string{"10.28.0.4", "6379", "9f8e7d", "preview:v1"} {
		assert.NotContains(t, rec.Body.String(), secret, "the answer carries an internal detail")
	}
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
	// A picture belongs to a book someone may not be allowed to see, so it
	// must not settle in a shared cache any more than the text does.
	cacheControl := rec.Header().Get("Cache-Control")
	for _, directive := range []string{"no-store", "private"} {
		assert.Contains(t, cacheControl, directive, "Cache-Control must carry %q", directive)
	}
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

// The text of a hidden book must not be reachable by asking for a portion
// directly. The manifest test stops a reader inside Load, so it says nothing
// about this endpoint: a handler that passed isSuperUser=true here alone
// would have served hidden text with every other test still green.
func TestPreviewChunkHandler_HiddenBook_Returns404ForReader_OKForSuperuser(t *testing.T) {
	newFake := func() *fakePreviewService {
		return &fakePreviewService{
			revision:    "abc123",
			chunkCount:  5,
			chunkResult: []byte(`<p>секретный текст</p>`),
			hidden:      true,
		}
	}

	reader := newFake()
	rec := doPreviewGET(t, newPreviewTestRouter(reader, false),
		"/api/books/preview/123/chunk/1?revision=abc123")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "секретный текст")
	require.Len(t, reader.chunkCalls, 1)
	assert.False(t, reader.chunkCalls[0].isSuperUser,
		"the reader's flag must reach the service as false")

	super := newFake()
	rec2 := doPreviewGET(t, newPreviewTestRouter(super, true),
		"/api/books/preview/123/chunk/1?revision=abc123")
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Len(t, super.chunkCalls, 1)
	assert.True(t, super.chunkCalls[0].isSuperUser)
}

// A cache operation cut short by the build's own deadline matches both
// ErrCacheUnavailable and context.DeadlineExceeded. Whichever the classifier
// asks about first decides the answer, so the composite chain is the form
// that has to be tested — a bare DeadlineExceeded proves nothing about the
// order.
func TestPreviewHandler_DeadlineInsideCacheError_Returns503WithBuildRetry(t *testing.T) {
	composite := fmt.Errorf("%w: %w", services.ErrCacheUnavailable, context.DeadlineExceeded)
	fake := &fakePreviewService{loadErr: composite}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "30", rec.Header().Get("Retry-After"),
		"our own deadline is the more specific cause and must win over the cache")
}

// A manifest that will not parse is our problem, and the reader must learn
// nothing about the shape of our cached bytes. The phrase is pinned exactly,
// not by a list of words that must not appear: swapping one safe-looking
// sentence for another would pass an absence check while changing what every
// client sees.
func TestPreviewHandler_CorruptManifest_AnswersTheGenericPhrase(t *testing.T) {
	fake := &fakePreviewService{loadResult: []byte(`{"revision": broken`)}
	r := newPreviewTestRouter(fake, false)

	rec := doPreviewGET(t, r, "/api/books/preview/123")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var got httputil.HTTPError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "preview is unavailable", got.Message,
		"a corrupt manifest must answer with the same generic sentence as any other internal fault")
	for _, leak := range []string{"unmarshal", "invalid character", "revision"} {
		assert.NotContains(t, rec.Body.String(), leak, "the answer describes our cached bytes")
	}
}

// The exact answer for every typed outcome known at the time of writing:
// status, the sentence the reader receives, and Retry-After. A table rather
// than a case each, because what these rows are worth is precision — an
// earlier version asserted "not 500", which no client can rely on and no
// mutation has to respect.
//
// What this table cannot do is prove completeness. A sentinel added in
// another package will not fail anything here; it will quietly become a
// blanket 500, which is how the last four rounds of review each found one.
// Until the classifier reads from an authoritative registry, the guard
// against that is review, not this test.
func TestPreviewHandler_RefusalTable(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		status     int
		reason     string
		retryAfter string
	}{
		{"book absent", services.ErrBookNotFound, http.StatusNotFound, "book not found", ""},
		{"book hidden", services.ErrBookNotVisible, http.StatusNotFound, "book not found", ""},
		{"file missing from archive", services.ErrArchiveFileNotFound, http.StatusNotFound, "the book file is missing", ""},
		{"portion gone", services.ErrChunkNotFound, http.StatusNotFound, "no such portion", ""},
		{"image gone", services.ErrImageNotFound, http.StatusNotFound, "no such image", ""},
		{"wrong format", services.ErrUnsupportedFormat, http.StatusUnsupportedMediaType, "preview is not available for this format", ""},
		{"not a FictionBook", converter.ErrNotFictionBook, http.StatusUnsupportedMediaType, "this book cannot be read", ""},
		{"damaged content", parser.ErrDamagedContent, http.StatusUnsupportedMediaType, "this book cannot be read", ""},
		{"unsupported charset", parser.ErrUnsupportedCharset, http.StatusUnsupportedMediaType, "this book cannot be read", ""},
		{"undeclared charset", parser.ErrUndeclaredCharset, http.StatusUnsupportedMediaType, "this book cannot be read", ""},
		{
			"declared charset unsupported", parser.ErrUnsupportedDeclaredCharset,
			http.StatusUnsupportedMediaType, "this book cannot be read", "",
		},
		{"file too large", services.ErrFB2TooLarge, http.StatusRequestEntityTooLarge, "this book is too large to preview", ""},
		{"too many pictures", services.ErrTooManyBinaries, http.StatusRequestEntityTooLarge, "this book is too large to preview", ""},
		{"too many nodes", services.ErrTooManyNodes, http.StatusRequestEntityTooLarge, "this book is too large to preview", ""},
		{
			"prepared images too heavy", services.ErrPreparedImagesTooLarge,
			http.StatusRequestEntityTooLarge, "this book is too large to preview", "",
		},
		{"node limit in parser", converter.ErrFB2NodeLimit, http.StatusRequestEntityTooLarge, "this book is too large to preview", ""},
		{"binary limit in parser", converter.ErrFB2BinaryLimit, http.StatusRequestEntityTooLarge, "this book is too large to preview", ""},
		// Reaching this handler at all now means a packing defect: a lone
		// oversized block is allowed through, and only a portion of several
		// blocks over the ceiling raises it. 413 is still the honest answer to
		// a reader — there is no portion to give them — but it is no longer a
		// statement about the book.
		{
			"portion of several blocks over the ceiling", converter.ErrPreviewBlockTooLarge,
			http.StatusRequestEntityTooLarge, "this book is too large to preview", "",
		},
		{
			"prepared images over the budget", converter.ErrPreviewImagesTotalTooLarge,
			http.StatusRequestEntityTooLarge, "this book is too large to preview", "",
		},
		{"nesting too deep", converter.ErrDepthLimit, http.StatusRequestEntityTooLarge, "this book is too large to preview", ""},
		{"revision gone", services.ErrRevisionStale, http.StatusGone, "the preview has been rebuilt, open it again", ""},
		{"build deadline", context.DeadlineExceeded, http.StatusServiceUnavailable, "the preview took too long to build", "30"},
		{"cache down", services.ErrCacheUnavailable, http.StatusServiceUnavailable, "preview storage is unavailable", "60"},
		{"server busy", services.ErrTooManyBuilds, http.StatusTooManyRequests, "too many previews are being built", "10"},
		{"no fingerprint", services.ErrEmptyMD5, http.StatusInternalServerError, "preview is unavailable for this book", ""},
		{"anything else", errors.New("something we did not foresee"), http.StatusInternalServerError, "preview is unavailable", ""},
	}

	for _, tc := range cases {
		// Both forms, because production almost never hands over a bare
		// sentinel: the service wraps what it passes on. A classifier that
		// compared with == instead of errors.Is would answer every wrapped
		// refusal with a blanket 500 while the bare column stayed green.
		forms := map[string]error{
			"bare":    tc.err,
			"wrapped": fmt.Errorf("preview: while doing something: %w", tc.err),
		}
		for form, err := range forms {
			t.Run(tc.name+", "+form, func(t *testing.T) {
				rec := doPreviewGET(t, newPreviewTestRouter(&fakePreviewService{loadErr: err}, false),
					"/api/books/preview/123")

				require.Equal(t, tc.status, rec.Code)
				var got httputil.HTTPError
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				assert.Equal(t, tc.reason, got.Message, "the sentence a reader receives")
				assert.Equal(t, tc.retryAfter, rec.Header().Get("Retry-After"))
			})
		}
	}
}
