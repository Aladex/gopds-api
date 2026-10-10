package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The problem archives of a run and the two actions on one: GET lists them,
// POST retry reopens one archive's books, POST delete removes the records of
// the books of an archive the run found broken. Every answer is a closed
// shape or a closed code.

func TestAuthorMetadataRunArchivesList(t *testing.T) {
	svc := &fakeRunService{archives: []AuthorMetadataProblemArchive{
		{Archive: "user_books.zip", Books: 120, Reason: "archive_missing",
			Deletion: &AuthorMetadataArchiveDeletionProgress{Deleted: 40, Total: 120}},
		{Archive: "broken.zip", Books: 3, Reason: "archive_unreadable"},
		{Archive: "odd.zip", Books: 1, Reason: "something else"},
	}}
	rec := runsRequest(t, runsRouter(svc), http.MethodGet, runsBase+"/42/archives", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"archives": [
		{"archive": "user_books.zip", "books": 120, "reason": "archive_missing", "deletion": {"deleted": 40, "total": 120}},
		{"archive": "broken.zip", "books": 3, "reason": "archive_unreadable", "deletion": null},
		{"archive": "odd.zip", "books": 1, "reason": "archive_unreadable", "deletion": null}
	]}`, rec.Body.String(), "a reason outside the closed pair reads as unreadable, never as its text")
	assert.Equal(t, []string{"archives"}, svc.callList())
	assert.EqualValues(t, 42, svc.lastID)

	empty := &fakeRunService{}
	rec = runsRequest(t, runsRouter(empty), http.MethodGet, runsBase+"/42/archives", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"archives": []}`, rec.Body.String())

	missing := &fakeRunService{err: ErrAuthorMetadataRunNotFound}
	rec = runsRequest(t, runsRouter(missing), http.MethodGet, runsBase+"/42/archives", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"error": "run_not_found"}`, rec.Body.String())

	rec = runsRequest(t, runsRouter(&fakeRunService{}), http.MethodGet, runsBase+"/0/archives", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error": "invalid_id"}`, rec.Body.String())
}

func TestAuthorMetadataRunArchiveActions(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		svc := &fakeRunService{reopened: 120}
		rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase+"/42/archives/retry", `{"archive": "user_books.zip"}`)
		require.Equal(t, http.StatusAccepted, rec.Code)
		assert.JSONEq(t, `{"reopened": 120}`, rec.Body.String())
		assert.Equal(t, "user_books.zip", svc.archive)
		assert.Equal(t, []string{"retry-archive"}, svc.callList())
	})
	t.Run("delete", func(t *testing.T) {
		svc := &fakeRunService{deletion: AuthorMetadataArchiveDeletion{
			Archive: "user_books.zip", BooksTotal: 120, BooksDeleted: 0, Status: "pending"}}
		rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase+"/42/archives/delete", `{"archive": "user_books.zip"}`)
		require.Equal(t, http.StatusAccepted, rec.Code, "the request is accepted, the deletion runs after it")
		assert.JSONEq(t, `{"deletion": {"archive": "user_books.zip", "books_total": 120, "books_deleted": 0,
			"status": "pending"}}`, rec.Body.String())
		assert.Equal(t, "user_books.zip", svc.archive)
		assert.Equal(t, []string{"delete-archive"}, svc.callList())
	})

	for _, action := range []string{"retry", "delete"} {
		for _, tc := range []struct {
			name, body string
			status     int
			code       string
		}{
			{"empty object", `{}`, http.StatusBadRequest, "invalid_archive"},
			{"empty name", `{"archive": ""}`, http.StatusBadRequest, "invalid_archive"},
			{"a path", `{"archive": "../user_books.zip"}`, http.StatusBadRequest, "invalid_archive"},
			{"padded", `{"archive": " user_books.zip"}`, http.StatusBadRequest, "invalid_archive"},
			{"null", `{"archive": null}`, http.StatusBadRequest, "invalid_request"},
			{"unknown field", `{"archive": "a.zip", "force": true}`, http.StatusBadRequest, "invalid_request"},
			{"not an object", `["a.zip"]`, http.StatusBadRequest, "invalid_request"},
		} {
			t.Run(action+" "+tc.name, func(t *testing.T) {
				svc := &fakeRunService{}
				rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase+"/42/archives/"+action, tc.body)
				assert.Equal(t, tc.status, rec.Code)
				assert.JSONEq(t, fmt.Sprintf(`{"error": %q}`, tc.code), rec.Body.String())
				assert.Empty(t, svc.callList(), "an invalid request reaches no service")
			})
		}
		for _, tc := range []struct {
			err    error
			status int
			code   string
		}{
			{ErrAuthorMetadataArchiveNotFailed, http.StatusConflict, "archive_not_failed"},
			{ErrAuthorMetadataRunNotFound, http.StatusNotFound, "run_not_found"},
			{ErrAuthorMetadataInvalidTransition, http.StatusConflict, "invalid_transition"},
			{ErrAuthorMetadataActiveRunExists, http.StatusConflict, "active_run_exists"},
			{errors.New("pq: the archive user_books.zip is locked"), http.StatusInternalServerError, "internal_error"},
		} {
			t.Run(action+" "+tc.code, func(t *testing.T) {
				svc := &fakeRunService{err: tc.err}
				rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase+"/42/archives/"+action, `{"archive": "user_books.zip"}`)
				assert.Equal(t, tc.status, rec.Code)
				assert.JSONEq(t, fmt.Sprintf(`{"error": %q}`, tc.code), rec.Body.String())
			})
		}
	}
}

// A full run being seeded carries its progress; any other run has null.
func TestAuthorMetadataRunSeedingShape(t *testing.T) {
	now := time.Now()
	run := sampleRun(now)
	run.Status = "pending"
	run.Seeding = &AuthorMetadataRunSeeding{Seeded: 120000, Target: 558609}
	run.AggregatesAsOf = nil
	rec := runsRequest(t, runsRouter(&fakeRunService{run: run}), http.MethodGet, runsBase+"/42", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Run map[string]json.RawMessage `json:"run"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.JSONEq(t, `{"seeded": 120000, "target": 558609}`, string(body.Run["seeding"]))
	assert.JSONEq(t, `null`, string(body.Run["aggregates_as_of"]))
}
