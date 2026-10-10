package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The comparison report is served a bounded window at a time: a request for
// the whole catalog would outlast the server's write timeout, so the
// window has a default and a ceiling, and the answer says where the next one
// starts.

type fakeCompare struct {
	afterID int64
	books   int
	calls   int
	err     error
}

func (f *fakeCompare) compare(_ context.Context, afterID int64, books int) (*database.AuthorDisplayReport, error) {
	f.calls++
	f.afterID, f.books = afterID, books
	if f.err != nil {
		return nil, f.err
	}
	next := afterID + int64(books)
	return &database.AuthorDisplayReport{AfterID: afterID, NextAfterID: &next, Books: books}, nil
}

func newReportRouter(f *fakeCompare) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/report", authorDisplayReport(f.compare))
	return r
}

func TestAuthorDisplayReportWindow(t *testing.T) {
	f := &fakeCompare{}
	r := newReportRouter(f)

	rec := doJSON(t, r, http.MethodGet, "/report", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, int64(0), f.afterID)
	assert.Equal(t, authorDisplayReportDefaultBooks, f.books)

	rec = doJSON(t, r, http.MethodGet, "/report?after_id=500&books=7", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, int64(500), f.afterID)
	assert.Equal(t, 7, f.books)
	assert.JSONEq(t, `507`, string(listedField(t, rec.Body.Bytes(), "next_after_id")))

	rec = doJSON(t, r, http.MethodGet, "/report?books=999999999", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, authorDisplayReportMaxBooks, f.books, "the window never exceeds its ceiling")
}

func TestAuthorDisplayReportRefusesABadWindow(t *testing.T) {
	for _, query := range []string{"?after_id=-1", "?books=0", "?books=-5", "?after_id=x", "?books=many"} {
		f := &fakeCompare{}
		rec := doJSON(t, newReportRouter(f), http.MethodGet, "/report"+query, nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code, query)
		assert.Zero(t, f.calls, query)
	}
}

func TestAuthorDisplayReportFailureIsAServerError(t *testing.T) {
	rec := doJSON(t, newReportRouter(&fakeCompare{err: errors.New("down")}), http.MethodGet, "/report", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// listedField decodes one top-level field of a JSON object, raw.
func listedField(t *testing.T, body []byte, field string) json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &object))
	return object[field]
}
