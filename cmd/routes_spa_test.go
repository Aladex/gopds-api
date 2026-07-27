package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	assets "gopds-api"

	"github.com/gin-gonic/gin"
)

// clientRoutes mirrors the routes declared in the SPA router
// (booksdump-frontend/src/app/routes/*.tsx). Deep-linking to any of them must reach
// the SPA rather than a 404, because routing happens in the browser.
var clientRoutes = []string{
	"/",
	"/login",
	"/registration",
	"/forgot-password",
	"/change-password/some-token",
	"/activate/some-token",
	"/books/page/1",
	"/books/favorite/1",
	"/books/users/favorites/1",
	"/books/find/author/42/1",
	"/books/find/category/42/1",
	"/books/find/genre/42/1",
	"/books/find/title/dune/1",
	"/authors/herbert/1",
	"/collections",
	"/collections/page/1",
	"/collections/7/page/1",
	"/catalog",
	"/admin",
	"/admin/users",
	"/404",
	"/this-route-does-not-exist",
}

// servicePrefixes are answered by the backend itself, so an unknown path under
// them is a genuine 404 and must never be masked by the SPA fallback.
var servicePrefixes = []string{
	"/api/nope",
	"/opds/nope",
	"/files/nope",
	"/telegram/nope",
}

// newSPATestRouter builds an engine carrying only the SPA fallback, which is
// what the NoRoute handler does in production once every real route missed.
func newSPATestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.NoRoute(spaFallbackHandler(NewHTTPFS(assets.Assets)))
	return engine
}

func doSPARequest(t *testing.T, target, accept string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	newSPATestRouter().ServeHTTP(rec, req)
	return rec
}

// TestSPAFallbackServesIndexForClientRoutes pins that a browser can deep-link or
// reload on any client route and still get the application.
func TestSPAFallbackServesIndexForClientRoutes(t *testing.T) {
	for _, route := range clientRoutes {
		t.Run(route, func(t *testing.T) {
			rec := doSPARequest(t, route, "text/html")

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Fatalf("Content-Type = %q, want a text/html prefix", ct)
			}
			if rec.Body.Len() == 0 {
				t.Fatal("empty body")
			}
		})
	}
}

// TestSPAFallbackReturnsJSONForServicePrefixes pins that backend namespaces keep
// reporting missing resources instead of returning an HTML page.
func TestSPAFallbackReturnsJSONForServicePrefixes(t *testing.T) {
	for _, route := range servicePrefixes {
		t.Run(route, func(t *testing.T) {
			rec := doSPARequest(t, route, "text/html")

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type = %q, want an application/json prefix", ct)
			}
		})
	}
}

// TestSPAFallbackRespectsJSONAccept pins that non-browser clients get a 404 they
// can parse, rather than the SPA shell.
func TestSPAFallbackRespectsJSONAccept(t *testing.T) {
	rec := doSPARequest(t, "/books/page/1", "application/json")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want an application/json prefix", ct)
	}
}

// TestSPAFallbackPrefersHTMLWhenBothAccepted pins the browser case: Accept lists
// JSON alongside HTML, and the SPA must still win.
func TestSPAFallbackPrefersHTMLWhenBothAccepted(t *testing.T) {
	rec := doSPARequest(t, "/books/page/1", "text/html,application/json")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
