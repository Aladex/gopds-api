package logging

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// A handler attaches the cause of a refusal with c.Error and answers the
// reader with a fixed sentence, so nothing internal leaks. That arrangement
// is only half of a contract: the other half is that the cause reaches the
// log. It did not — the request logger never read the error list, and a bare
// 503 in the log could equally mean a dead cache, a build out of time, or a
// server already building its fill.
//
// This test holds the half that has no other witness. It matters more than
// usual: preview metrics are deliberately out of scope, so the log is the
// only place these refusals are visible at all.
func TestGinrusLogger_RecordsAttachedCauses(t *testing.T) {
	var captured bytes.Buffer
	previous := logger.Out
	logger.SetOutput(&captured)
	defer logger.SetOutput(previous)

	const cause = "dial tcp 10.28.0.4:6379: connect: connection refused"

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinrusLogger())
	r.GET("/thing", func(c *gin.Context) {
		_ = c.Error(errors.New(cause))
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "storage is unavailable"})
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/thing", http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	line := captured.String()
	if !strings.Contains(line, cause) {
		t.Errorf("the log line does not carry the attached cause.\nline: %s", line)
	}
	// And the reader still learns nothing: the public answer is unchanged by
	// anything this test does.
	if strings.Contains(rec.Body.String(), cause) {
		t.Errorf("the answer carries the internal cause: %s", rec.Body.String())
	}
}

// A request that refuses nothing must not grow an empty errors field: a log
// line that always carries the key teaches a reader to ignore it.
func TestGinrusLogger_QuietWhenNothingFailed(t *testing.T) {
	var captured bytes.Buffer
	previous := logger.Out
	logger.SetOutput(&captured)
	defer logger.SetOutput(previous)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinrusLogger())
	r.GET("/thing", func(c *gin.Context) { c.String(http.StatusOK, "fine") })

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/thing", http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(captured.String(), "errors") {
		t.Errorf("a successful request carries an errors field: %s", captured.String())
	}
}
