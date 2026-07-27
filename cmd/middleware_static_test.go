package main

import (
	assets "gopds-api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newStaticTestContext builds a gin context for a bare GET request.
func newStaticTestContext(target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c, rec
}

// TestServeStaticFilesMiddlewareServesIndexAtRoot pins the behavior that "/" is
// answered directly from the embedded SPA entry point, with revalidating cache
// headers, and that the middleware chain stops there.
func TestServeStaticFilesMiddlewareServesIndexAtRoot(t *testing.T) {
	c, rec := newStaticTestContext("/")

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

	c, rec := newStaticTestContext("/api/books/list")

	serveStaticFilesMiddleware(NewHTTPFS(assets.Assets))(c)

	if c.IsAborted() {
		t.Fatal("middleware aborted the chain for a non-static path")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("middleware wrote %d bytes for a non-static path, want 0", rec.Body.Len())
	}
}
