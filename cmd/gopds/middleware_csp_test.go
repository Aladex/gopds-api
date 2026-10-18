package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// The Send-to-Kindle setup dialog plays a how-to video from /videos/. The
// policy denies everything it does not name, so a policy without media-src
// leaves the player empty with nothing but a console line to say why.
func TestContentSecurityPolicyAllowsSameOriginMedia(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(securityHeadersMiddleware())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	router.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "media-src 'self'") {
		t.Errorf("the policy does not allow same-origin media: %q", csp)
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("the policy no longer denies by default: %q", csp)
	}
}
