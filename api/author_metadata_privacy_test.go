package api

// Phase 20, RED 7/8/9 for the admin runs API: an error response is a closed
// code and nothing else, a status or report carries aggregates and IDs but no
// source text or raw audit, and no new component pushes WebSocket events.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gopds-api/logging"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const apiCanary = "zqxcanary"

var errAPICanary = errors.New("provider said Zqxcanary Title by Zqxcanaryov failed")

// loggedRunsRouter mounts the runs routes behind the server's request
// logger, which writes every handler's c.Errors into the log.
func loggedRunsRouter(svc AuthorMetadataRunService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(logging.GinrusLogger())
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorMetadataRunRoutes(admin.Group("/author-metadata"), svc)
	return r
}

// runsCanaryRequests is every runs route; the selectors and the retry class
// carry the canary where a body takes text.
var runsCanaryRequests = []struct{ method, path, body string }{
	{http.MethodPost, "/api/admin/author-metadata/runs", `{"mode":"smoke","book_ids":[1]}`},
	{http.MethodPost, "/api/admin/author-metadata/runs", `{"mode":"pilot_archive","archive":"zqxcanary-title.zip"}`},
	{http.MethodGet, "/api/admin/author-metadata/runs/current", ""},
	{http.MethodGet, "/api/admin/author-metadata/runs/7", ""},
	{http.MethodGet, "/api/admin/author-metadata/runs/7/report", ""},
	{http.MethodPost, "/api/admin/author-metadata/runs/7/pause", ""},
	{http.MethodPost, "/api/admin/author-metadata/runs/7/resume", ""},
	{http.MethodPost, "/api/admin/author-metadata/runs/7/approve-full", ""},
	{http.MethodPost, "/api/admin/author-metadata/runs/7/retry", `{"stage":"extraction","error_class":"entry_missing"}`},
}

// Integration with phase 16 Part 2: every runs route answers its success
// behind the request logger, and none of the text a request body carries is
// logged.
func TestAuthorMetadataRunSuccessLogsNoRequestText(t *testing.T) {
	hook := traceHook(t)
	now := time.Now()
	run := sampleRun(now)
	svc := &fakeRunService{run: run, current: &run, report: AuthorMetadataRunReport{
		AuthorMetadataRunView: run, NotReadyReasons: []string{}, ByClass: map[string]int64{}, ByScript: map[string]int64{},
	}, reopened: 1}
	r := loggedRunsRouter(svc)
	for _, req := range runsCanaryRequests {
		rec := runsRequest(t, r, req.method, req.path, req.body)
		assert.Less(t, rec.Code, http.StatusBadRequest, "%s %s: %s", req.method, req.path, rec.Body.String())
	}
	assert.Equal(t, "zqxcanary-title.zip", *svc.start.Archive, "the canary reached the service")
	require.NotEmpty(t, hook.AllEntries(), "the request logger ran")
	assertNoCanaryLogged(t, hook)
}

// RED 8: whatever text a service error carries, the response is its closed
// code, and nothing of the text reaches the body or a log — behind the
// server's request logger.
func TestAuthorMetadataRunErrorsAreClosedCodes(t *testing.T) {
	hook := traceHook(t)

	errs := []struct {
		err    error
		status int
		code   string
	}{
		{errAPICanary, http.StatusInternalServerError, codeInternalError},
		{fmt.Errorf("%w: %w", ErrAuthorMetadataActiveRunExists, errAPICanary), http.StatusConflict, codeActiveRunExists},
		{fmt.Errorf("%w: %w", ErrAuthorMetadataRunNotFound, errAPICanary), http.StatusNotFound, codeRunNotFound},
	}
	for _, e := range errs {
		for _, req := range runsCanaryRequests {
			t.Run(fmt.Sprintf("%s %s -> %s", req.method, req.path, e.code), func(t *testing.T) {
				rec := runsRequest(t, loggedRunsRouter(&fakeRunService{err: e.err}), req.method, req.path, req.body)
				assert.Equal(t, e.status, rec.Code)
				var body map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Equal(t, map[string]interface{}{jsonKeyError: e.code}, body)
				assert.NotContains(t, strings.ToLower(rec.Body.String()), apiCanary)
			})
		}
	}
	// Validation refusals of bodies that carry the canary answer a closed
	// code too.
	r := loggedRunsRouter(&fakeRunService{})
	for _, body := range []string{
		`{"mode":"zqxcanary"}`,
		`{"mode":"pilot_archive","archive":"zqxcanary.zip","book_ids":[1]}`,
		`{"mode":"smoke","book_ids":[1],"zqxcanary":1}`,
	} {
		rec := runsRequest(t, r, http.MethodPost, "/api/admin/author-metadata/runs", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.NotContains(t, strings.ToLower(rec.Body.String()), apiCanary, body)
	}
	rec := runsRequest(t, r, http.MethodPost, "/api/admin/author-metadata/runs/7/retry",
		`{"stage":"local","error_class":"zqxcanary"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.NotContains(t, strings.ToLower(rec.Body.String()), apiCanary)

	require.NotEmpty(t, hook.AllEntries(), "the request logger ran")
	assertNoCanaryLogged(t, hook)
}

// jsonKeys collects every object key of a JSON document, dotted by path.
func jsonKeys(prefix string, v interface{}, into map[string]bool) {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, child := range x {
			into[prefix+k] = true
			jsonKeys(prefix+k+".", child, into)
		}
	case []interface{}:
		for _, child := range x {
			jsonKeys(prefix, child, into)
		}
	}
}

// RED 7: the run status and the report are aggregates, IDs, versions and
// closed values. A key outside this list — raw audit, a source name, a title,
// a fingerprint — fails the test.
func TestAuthorMetadataRunStatusAndReportCarryNoRawAudit(t *testing.T) {
	now := time.Now()
	run := sampleRun(now)
	report := AuthorMetadataRunReport{
		AuthorMetadataRunView: run, Ready: true, NotReadyReasons: []string{}, DurationS: 1,
		ByClass: map[string]int64{"initials": 1}, ByScript: map[string]int64{"Cyrl": 1},
	}
	svc := &fakeRunService{run: run, current: &run, report: report}
	keys := map[string]bool{}
	for _, path := range []string{
		"/api/admin/author-metadata/runs/current", "/api/admin/author-metadata/runs/7", "/api/admin/author-metadata/runs/7/report",
	} {
		rec := runsRequest(t, runsRouter(svc), http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, rec.Code, path)
		var doc interface{}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
		jsonKeys("", doc, keys)
	}
	var got []string
	for k := range keys {
		// Values of the closed-class maps are keyed by class, not by field.
		if strings.HasPrefix(k, "report.by_class.") || strings.HasPrefix(k, "report.by_script.") ||
			strings.Contains(k, ".unresolved.") {
			continue
		}
		got = append(got, k)
	}
	sort.Strings(got)
	for _, k := range got {
		for _, forbidden := range []string{"name", "title", "fingerprint", "audit", "raw", "provenance", "annotation"} {
			assert.NotContains(t, k, forbidden, "status/report key %q", k)
		}
	}
	assert.Contains(t, got, "run.stages.extraction.by_status.extracted")
	assert.Contains(t, got, "report.credits.unresolved")
}

// RED 9: no new author-metadata component sends WebSocket events. When one
// starts to, its events must carry only aggregates, IDs and closed classes;
// this guard makes that a reviewed decision instead of a silent addition.
func TestAuthorMetadataComponentsSendNoWebSocketEvents(t *testing.T) {
	var files []string
	for _, pattern := range []string{
		"../services/author_metadata_*.go", "../database/author_metadata_*.go",
		"../database/author_normalization_*.go", "author_metadata_*.go", "../cmd/gopds/init_author_metadata.go",
	} {
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err)
		files = append(files, matches...)
	}
	require.NotEmpty(t, files)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		require.NoError(t, err)
		lower := strings.ToLower(string(src))
		for _, ws := range []string{"websocket", "broadcast", "sendtoadmins"} {
			assert.NotContains(t, lower, ws, "%s sends a WebSocket event", file)
		}
	}
}

// Integration fix round 1: last_error_class is projected onto the closed run
// error classes on every response that carries a run. A stored value outside
// them — name-shaped text passes the schema's shape — answers "other"; a
// known class is itself; none stays null. The key is unchanged.
func TestAuthorMetadataRunLastErrorClassIsClosed(t *testing.T) {
	routes := []struct{ method, path, body, key string }{
		{http.MethodPost, "/api/admin/author-metadata/runs", `{"mode":"smoke","book_ids":[1]}`, jsonKeyRun},
		{http.MethodGet, "/api/admin/author-metadata/runs/current", "", jsonKeyRun},
		{http.MethodGet, "/api/admin/author-metadata/runs/7", "", jsonKeyRun},
		{http.MethodGet, "/api/admin/author-metadata/runs/7/report", "", "report"},
		{http.MethodPost, "/api/admin/author-metadata/runs/7/pause", "", jsonKeyRun},
		{http.MethodPost, "/api/admin/author-metadata/runs/7/resume", "", jsonKeyRun},
		{http.MethodPost, "/api/admin/author-metadata/runs/7/approve-full", "", jsonKeyRun},
	}
	canary := "zqxcanary_person_123456"
	known := "archive_unreadable"
	for _, tc := range []struct {
		name   string
		stored *string
		want   interface{}
	}{
		{"name-shaped value", &canary, "other"},
		{"known class", &known, known},
		{"none", nil, nil},
	} {
		run := sampleRun(time.Now())
		run.LastErrorClass = tc.stored
		svc := &fakeRunService{run: run, current: &run, report: AuthorMetadataRunReport{
			AuthorMetadataRunView: run, NotReadyReasons: []string{},
		}}
		for _, route := range routes {
			rec := runsRequest(t, runsRouter(svc), route.method, route.path, route.body)
			require.Less(t, rec.Code, http.StatusBadRequest, "%s %s: %s", route.method, route.path, rec.Body.String())
			assert.NotContains(t, strings.ToLower(rec.Body.String()), apiCanary, "%s %s", tc.name, route.path)
			var body map[string]map[string]interface{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			value, present := body[route.key]["last_error_class"]
			require.True(t, present, "the key stays: %s %s", tc.name, route.path)
			assert.Equal(t, tc.want, value, "%s %s", tc.name, route.path)
		}
	}
}
