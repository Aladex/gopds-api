package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSetStaticCacheHeaders pins the caching contract for embedded assets.
//
// The entry document must always be revalidated, or a deploy never reaches
// browsers holding an old copy. Bundler output carries a content hash in the
// file name, so it can be cached indefinitely — but only if the rule recognizes
// the directory the bundler actually writes to.
func TestSetStaticCacheHeaders(t *testing.T) {
	const (
		noCache   = "no-cache"
		immutable = "public, max-age=31536000, immutable"
		oneHour   = "public, max-age=3600"
	)

	tests := []struct {
		name     string
		filePath string
		want     string
	}{
		{"root", "/", noCache},
		{"empty", "", noCache},
		{"index", "index.html", noCache},
		{"index with path", "booksdump-frontend/build/index.html", noCache},
		{"hashed js in assets", "/assets/index-DIf6WHZL.js", immutable},
		{"hashed css in assets", "/assets/index-B1VKAR7w.css", immutable},
		{"hashed js in static", "/static/js/main.3ea3fbbb.js", immutable},
		{"manifest", "/manifest.json", oneHour},
		{"favicon", "/favicon.ico", oneHour},
		{"unhashed image", "/logo.png", oneHour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

			setStaticCacheHeaders(c, tt.filePath)

			if got := rec.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control for %q = %q, want %q", tt.filePath, got, tt.want)
			}
		})
	}
}
