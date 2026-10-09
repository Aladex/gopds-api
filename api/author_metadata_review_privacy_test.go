package api

// Phase 20 over the phase-17 review API: whatever names, linked titles or
// error text pass through the review routes, the log carries none of them —
// on success and on every error path, behind the server's own request
// logger, at the most verbose level.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/logging"

	"github.com/gin-gonic/gin"
	//nolint:depguard // the test raises the logger to its most verbose level
	"github.com/sirupsen/logrus"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// traceHook captures every log entry at Trace level for one test.
func traceHook(t *testing.T) *logrustest.Hook {
	t.Helper()
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	previous := logging.GetLogger().GetLevel()
	logging.GetLogger().SetLevel(logrus.TraceLevel)
	t.Cleanup(func() { logging.GetLogger().SetLevel(previous) })
	return hook
}

// assertNoCanaryLogged fails on any entry whose line or fields carry the
// canary, case-insensitively.
func assertNoCanaryLogged(t *testing.T, hook *logrustest.Hook) {
	t.Helper()
	for _, entry := range hook.AllEntries() {
		line, err := entry.String()
		require.NoError(t, err)
		assert.NotContains(t, strings.ToLower(line+fmt.Sprint(entry.Data)), apiCanary)
	}
}

// loggedReviewRouter mounts the review routes behind the server's request
// logger, which writes every handler's c.Errors into the log.
func loggedReviewRouter(svc AuthorMetadataReviewService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(logging.GinrusLogger())
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorMetadataReviewRoutes(admin.Group("/author-metadata"), svc)
	return r
}

// canaryReviewDetail carries the canary in every name and in the linked
// title.
func canaryReviewDetail(now time.Time) AuthorMetadataReviewDetail {
	d := sampleReviewDetail(now)
	d.DisplayName = "Z. Zqxcanaryov"
	d.Source.First, d.Source.Last = ptrReview("Zqxcanary"), ptrReview("Zqxcanaryov")
	d.Source.Display = "Zqxcanary Zqxcanaryov"
	d.Proposal.GivenName, d.Proposal.FamilyName = ptrReview("Zqxcanary"), ptrReview("Zqxcanaryov")
	d.Proposal.DisplayName, d.Proposal.SearchKey = "Zqxcanary Zqxcanaryov", "zqxcanary zqxcanaryov"
	d.LinkedBooks = []AuthorMetadataReviewLinkedBook{{ID: 5, Title: "Zqxcanary Title"}}
	return d
}

// reviewCanaryRequests is every review route, with the canary in each body
// that takes names.
var reviewCanaryRequests = []struct{ method, path, body string }{
	{http.MethodGet, reviewBase + "?status=open", ""},
	{http.MethodGet, reviewBase + "/31", ""},
	{http.MethodPost, reviewBase + "/31/accept", `{"scope":"fingerprint"}`},
	{http.MethodPost, reviewBase + "/31/edit", `{"scope":"fingerprint","result":{"given_name":"Zqxcanary",
		"family_name":"Zqxcanaryov","nickname":null,"display_name":"Zqxcanary Zqxcanaryov","sort_name":null,"kind":"person"}}`},
	{http.MethodPost, reviewBase + "/31/classify", `{"scope":"fingerprint","kind":"collective"}`},
	{http.MethodPost, reviewBase + "/31/unresolved", `{"scope":"fingerprint"}`},
	{http.MethodPost, reviewBase + "/31/retry", ""},
}

func TestReviewAPISuccessLogsNoNamesOrTitles(t *testing.T) {
	hook := traceHook(t)
	detail := canaryReviewDetail(time.Now().UTC())
	f := &fakeReviewService{detail: detail, page: AuthorMetadataReviewListPage{
		Items: []AuthorMetadataReviewListItem{detail.AuthorMetadataReviewListItem},
		Next:  &AuthorMetadataReviewCursor{CreatedAt: detail.CreatedAt, ID: detail.ID},
	}}
	r := loggedReviewRouter(f)
	for _, req := range reviewCanaryRequests {
		rec := reviewRequest(t, r, req.method, req.path, req.body)
		require.Equal(t, http.StatusOK, rec.Code, "%s %s", req.method, req.path)
	}
	// The detail does answer with the canary: it is the one response where
	// names and titles belong.
	rec := reviewRequest(t, r, http.MethodGet, reviewBase+"/31", "")
	require.Contains(t, strings.ToLower(rec.Body.String()), apiCanary)

	require.NotEmpty(t, hook.AllEntries(), "the request logger ran")
	assertNoCanaryLogged(t, hook)
}

func TestReviewAPIErrorsLogNoRawErrors(t *testing.T) {
	hook := traceHook(t)
	detail := canaryReviewDetail(time.Now().UTC())
	for _, e := range []struct {
		err    error
		status int
		code   string
	}{
		{errAPICanary, http.StatusInternalServerError, codeInternalError},
		{fmt.Errorf("%w: %w", ErrAuthorMetadataReviewNotFound, errAPICanary), http.StatusNotFound, codeReviewNotFound},
		{fmt.Errorf("%w: %w", ErrAuthorMetadataReviewConflict, errAPICanary), http.StatusConflict, codeReviewConflict},
		{fmt.Errorf("%w: %w", ErrAuthorMetadataReviewScopeMismatch, errAPICanary), http.StatusConflict, codeScopeMismatch},
		{fmt.Errorf("%w: %w", ErrAuthorMetadataReviewRetryExhausted, errAPICanary), http.StatusConflict, codeRetryExhausted},
	} {
		r := loggedReviewRouter(&fakeReviewService{detail: detail, err: e.err})
		for _, req := range reviewCanaryRequests {
			t.Run(fmt.Sprintf("%s %s -> %s", req.method, req.path, e.code), func(t *testing.T) {
				rec := reviewRequest(t, r, req.method, req.path, req.body)
				assert.Equal(t, e.status, rec.Code)
				var body map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Equal(t, map[string]interface{}{jsonKeyError: e.code}, body)
			})
		}
	}

	// Validation refusals of bodies that carry names answer a closed code too.
	r := loggedReviewRouter(&fakeReviewService{detail: detail})
	for _, body := range []string{
		`{"scope":"Zqxcanary","result":{"display_name":"Zqxcanary","kind":"person"}}`,
		`{"scope":"credit","result":{"display_name":"Zqxcanary","kind":"Zqxcanary"}}`,
		`{"scope":"credit","result":{"display_name":"Zqxcanary","kind":"person","zqxcanary":1}}`,
		`{"scope":"credit","result":{"display_name":"Zqxcanary","kind":"person"}} zqxcanary`,
	} {
		rec := reviewRequest(t, r, http.MethodPost, reviewBase+"/31/edit", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.NotContains(t, strings.ToLower(rec.Body.String()), apiCanary, body)
	}
	rec := reviewRequest(t, r, http.MethodGet, reviewBase+"?status=open&cursor=zqxcanary", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.NotContains(t, strings.ToLower(rec.Body.String()), apiCanary)

	require.NotEmpty(t, hook.AllEntries(), "the request logger ran")
	assertNoCanaryLogged(t, hook)
}

// The same contract through the concrete adapter and the real phase-11
// service on a scratch database: the seeded names and the linked title
// reach the detail response and no log entry, whichever way each action
// ends.
func TestReviewAPIAdapterLogsNoNamesOrTitles(t *testing.T) {
	db := reviewAPIDB(t)
	previousWiring := authorMetadataReviewService
	t.Cleanup(func() { authorMetadataReviewService = previousWiring })
	serverConfig := config.AuthorMetadataConfig{}
	serverConfig.LocalNormalization.MaxAttempts = 5
	require.NoError(t, SetAuthorMetadataReviewService(db, &serverConfig))
	seedOpenReviewItem(t, db, nameValueOf(t, "Z.", "Zqxcanaryov"), "Zqxcanary Title")

	hook := traceHook(t)
	r := loggedReviewRouter(authorMetadataReviewService())
	request := func(method, path, body string) int {
		req, err := http.NewRequestWithContext(context.Background(), method, path, readerOrNull(body))
		require.NoError(t, err)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if path == reviewBase+"/1" && rec.Code == http.StatusOK {
			assert.Contains(t, strings.ToLower(rec.Body.String()), apiCanary, "the detail shows the names")
		}
		return rec.Code
	}

	require.Equal(t, http.StatusOK, request(http.MethodGet, reviewBase+"?status=open", ""))
	require.Equal(t, http.StatusOK, request(http.MethodGet, reviewBase+"/1", ""))
	require.Equal(t, http.StatusConflict, request(http.MethodPost, reviewBase+"/1/accept", `{"scope":"credit"}`))
	require.Equal(t, http.StatusOK, request(http.MethodPost, reviewBase+"/1/edit", `{"scope":"fingerprint",
		"result":{"given_name":"Zqxcanary","family_name":"Zqxcanaryov","nickname":null,
		"display_name":"Zqxcanary Zqxcanaryov","sort_name":null,"kind":"person"}}`))
	for _, action := range []struct{ path, body string }{
		{"/1/accept", `{"scope":"fingerprint"}`},
		{"/1/classify", `{"scope":"fingerprint","kind":"collective"}`},
		{"/1/unresolved", `{"scope":"fingerprint"}`},
		{"/1/retry", ""},
	} {
		code := request(http.MethodPost, reviewBase+action.path, action.body)
		assert.Contains(t, []int{http.StatusConflict, http.StatusBadRequest}, code, action.path)
	}
	require.Equal(t, http.StatusNotFound, request(http.MethodGet, reviewBase+"/999", ""))
	require.Equal(t, http.StatusOK, request(http.MethodGet, reviewBase+"?status=closed", ""))

	require.NotEmpty(t, hook.AllEntries(), "the request logger ran")
	assertNoCanaryLogged(t, hook)
}
