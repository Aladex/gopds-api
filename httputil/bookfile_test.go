package httputil

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopds-api/internal/archivestore"

	"github.com/gin-gonic/gin"
)

func TestBookFileError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, c := range map[string]struct {
		err        error
		status     int
		retryAfter string
	}{
		"nowhere":     {archivestore.ErrNotFound, http.StatusNotFound, ""},
		"store down":  {fmt.Errorf("reading: %w", archivestore.ErrUnavailable), http.StatusServiceUnavailable, storageRetryAfter},
		"bad archive": {errors.New("zip: not a valid zip file"), http.StatusInternalServerError, ""},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			BookFileError(ctx, c.err)
			if recorder.Code != c.status {
				t.Fatalf("status = %d, want %d", recorder.Code, c.status)
			}
			if got := recorder.Header().Get("Retry-After"); got != c.retryAfter {
				t.Fatalf("Retry-After = %q", got)
			}
		})
	}
}
