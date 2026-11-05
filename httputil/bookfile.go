package httputil

import (
	"errors"
	"net/http"

	"gopds-api/internal/archivestore"

	"github.com/gin-gonic/gin"
)

// storageRetryAfter is how long a client is told to wait, in seconds, when
// object storage cannot deliver a book.
const storageRetryAfter = "30"

// BookFileError answers a failure to reach a book's archive: 404 when the file
// exists nowhere, 503 when object storage cannot deliver it right now, 500
// otherwise.
func BookFileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, archivestore.ErrNotFound):
		NewError(c, http.StatusNotFound, errors.New("book file not found"))
	case errors.Is(err, archivestore.ErrUnavailable):
		c.Header("Retry-After", storageRetryAfter)
		NewError(c, http.StatusServiceUnavailable, errors.New("book_storage_unavailable"))
	default:
		NewError(c, http.StatusInternalServerError, err)
	}
}
