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

	//nolint:depguard // the test raises the logger to its most verbose level
	"github.com/sirupsen/logrus"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const apiCanary = "zqxcanary"

var errAPICanary = errors.New("provider said Zqxcanary Title by Zqxcanaryov failed")

// RED 8: whatever text a service error carries, the response is its closed
// code, and nothing of the text reaches the body or a log.
func TestAuthorMetadataRunErrorsAreClosedCodes(t *testing.T) {
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	previous := logging.GetLogger().GetLevel()
	logging.GetLogger().SetLevel(logrus.TraceLevel)
	t.Cleanup(func() { logging.GetLogger().SetLevel(previous) })

	errs := []struct {
		err    error
		status int
		code   string
	}{
		{errAPICanary, http.StatusInternalServerError, codeInternalError},
		{fmt.Errorf("%w: %w", ErrAuthorMetadataActiveRunExists, errAPICanary), http.StatusConflict, codeActiveRunExists},
		{fmt.Errorf("%w: %w", ErrAuthorMetadataRunNotFound, errAPICanary), http.StatusNotFound, codeRunNotFound},
	}
	requests := []struct{ method, path, body string }{
		{http.MethodPost, "/api/admin/author-metadata/runs", `{"mode":"smoke","book_ids":[1]}`},
		{http.MethodGet, "/api/admin/author-metadata/runs/current", ""},
		{http.MethodGet, "/api/admin/author-metadata/runs/7", ""},
		{http.MethodGet, "/api/admin/author-metadata/runs/7/report", ""},
		{http.MethodPost, "/api/admin/author-metadata/runs/7/pause", ""},
		{http.MethodPost, "/api/admin/author-metadata/runs/7/resume", ""},
		{http.MethodPost, "/api/admin/author-metadata/runs/7/approve-full", ""},
		{http.MethodPost, "/api/admin/author-metadata/runs/7/retry", `{"stage":"extraction","error_class":"entry_missing"}`},
	}
	for _, e := range errs {
		for _, req := range requests {
			t.Run(fmt.Sprintf("%s %s -> %s", req.method, req.path, e.code), func(t *testing.T) {
				rec := runsRequest(t, runsRouter(&fakeRunService{err: e.err}), req.method, req.path, req.body)
				assert.Equal(t, e.status, rec.Code)
				var body map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Equal(t, map[string]interface{}{jsonKeyError: e.code}, body)
				assert.NotContains(t, strings.ToLower(rec.Body.String()), apiCanary)
			})
		}
	}
	for _, entry := range hook.AllEntries() {
		line, err := entry.String()
		require.NoError(t, err)
		assert.NotContains(t, strings.ToLower(line+fmt.Sprint(entry.Data)), apiCanary)
	}
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
