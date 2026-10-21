package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	assets "gopds-api"

	"github.com/gin-gonic/gin"
)

// newStaticTestContext builds a gin context for a bare GET request.
func newStaticTestContext(t *testing.T, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	return c, rec
}

// TestServeStaticFilesMiddlewareServesIndexAtRoot pins the behavior that "/" is
// answered directly from the embedded SPA entry point, with revalidating cache
// headers, and that the middleware chain stops there.
func TestServeStaticFilesMiddlewareServesIndexAtRoot(t *testing.T) {
	c, rec := newStaticTestContext(t, "/")

	serveStaticFilesMiddleware(NewHTTPFS(assets.Assets))(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want a text/html prefix", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want %q", cc, "no-cache")
	}
	if !c.IsAborted() {
		t.Fatal("middleware served index.html but did not abort the chain")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("middleware wrote an empty body for /")
	}
}

// TestServeStaticFilesMiddlewarePassesThroughUnknownPath pins the behavior that
// non-static paths fall through to the following handlers untouched, so API and
// OPDS routes keep working.
func TestServeStaticFilesMiddlewarePassesThroughUnknownPath(t *testing.T) {
	distFolders = nil
	t.Cleanup(func() { distFolders = nil })

	c, rec := newStaticTestContext(t, "/api/books/list")

	serveStaticFilesMiddleware(NewHTTPFS(assets.Assets))(c)

	if c.IsAborted() {
		t.Fatal("middleware aborted the chain for a non-static path")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("middleware wrote %d bytes for a non-static path, want 0", rec.Body.Len())
	}
}

// TestServeStaticFilesMiddlewareServesWebMAsVideo pins the media types of the
// reading-room recordings. Go's own table maps .webm to audio/webm (and the
// production image has no /etc/mime.types to say otherwise); a browser asked
// to play an audio/webm resource in a <video> may refuse or misjudge it.
func TestServeStaticFilesMiddlewareServesWebMAsVideo(t *testing.T) {
	distFolders = []string{"/auth-room/"}
	t.Cleanup(func() { distFolders = nil })
	files := fstest.MapFS{
		"booksdump-frontend/build/auth-room/desktop.webm": {Data: []byte("\x1a\x45\xdf\xa3webm")},
		"booksdump-frontend/build/auth-room/desktop.mp4":  {Data: []byte("\x00\x00\x00\x18ftypmp42")},
		"booksdump-frontend/build/auth-room/desktop.webp": {Data: []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")},
	}

	for target, want := range map[string]string{
		"/auth-room/desktop.webm": "video/webm",
		"/auth-room/desktop.mp4":  "video/mp4",
		"/auth-room/desktop.webp": "image/webp",
	} {
		c, rec := newStaticTestContext(t, target)

		serveStaticFilesMiddleware(http.FS(files))(c)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d", target, rec.Code, http.StatusOK)
		}
		if ct := rec.Header().Get("Content-Type"); ct != want {
			t.Errorf("%s: Content-Type = %q, want %q", target, ct, want)
		}
	}
}

// TestServeStaticFilesMiddlewareServesWebMRanges keeps byte ranges working for
// the recording with its type set: browsers fetch video in ranges.
func TestServeStaticFilesMiddlewareServesWebMRanges(t *testing.T) {
	distFolders = []string{"/auth-room/"}
	t.Cleanup(func() { distFolders = nil })
	files := fstest.MapFS{
		"booksdump-frontend/build/auth-room/desktop.webm": {Data: []byte("0123456789")},
	}
	c, rec := newStaticTestContext(t, "/auth-room/desktop.webm")
	c.Request.Header.Set("Range", "bytes=2-4")

	serveStaticFilesMiddleware(http.FS(files))(c)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
	}
	if got := rec.Body.String(); got != "234" {
		t.Fatalf("body = %q, want %q", got, "234")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/webm" {
		t.Fatalf("Content-Type = %q, want %q", ct, "video/webm")
	}
}
