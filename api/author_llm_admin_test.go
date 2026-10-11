package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gopds-api/database"
	"gopds-api/middlewares"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The LLM routes of the card: admin only, instant start, 409 on an illegal
// transition with a closed code, and the report downloaded byte for byte.

type fakeLLMAdmin struct {
	mu     sync.Mutex
	calls  []string
	kind   string
	mode   string
	actor  int64
	id     int64
	slot   string
	err    error
	report []byte
	sha    string
}

func (f *fakeLLMAdmin) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeLLMAdmin) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeLLMAdmin) run() *services.AuthorLLMRunState {
	return &services.AuthorLLMRunState{ID: 12, Kind: "eval", Mode: "pilot", Status: "running", Stage: "freeze"}
}

func (f *fakeLLMAdmin) Status(context.Context) (*services.AuthorLLMStatus, error) {
	f.record("status")
	if f.err != nil {
		return nil, f.err
	}
	return &services.AuthorLLMStatus{Enabled: true, Run: f.run(),
		Steps: []services.AuthorLLMStep{{Key: "eval_pilot", Kind: "eval", Mode: "pilot", Available: true, Enabled: true}}}, nil
}

func (f *fakeLLMAdmin) Start(_ context.Context, kind, mode string, actor int64) (*services.AuthorLLMRunState, error) {
	f.record("start")
	f.kind, f.mode, f.actor = kind, mode, actor
	if f.err != nil {
		return nil, f.err
	}
	return f.run(), nil
}

func (f *fakeLLMAdmin) Pause(_ context.Context, id int64) (*services.AuthorLLMRunState, error) {
	f.record("pause")
	f.id = id
	if f.err != nil {
		return nil, f.err
	}
	return f.run(), nil
}

func (f *fakeLLMAdmin) Resume(_ context.Context, id int64) (*services.AuthorLLMRunState, error) {
	f.record("resume")
	f.id = id
	if f.err != nil {
		return nil, f.err
	}
	return f.run(), nil
}

func (f *fakeLLMAdmin) Close(_ context.Context, id, actor int64) (*services.AuthorLLMRunState, error) {
	f.record("close")
	f.id, f.actor = id, actor
	if f.err != nil {
		return nil, f.err
	}
	return f.run(), nil
}

func (f *fakeLLMAdmin) ResumeParticipant(_ context.Context, slot string) error {
	f.record("resume_participant")
	f.slot = slot
	return f.err
}

func (f *fakeLLMAdmin) ResumeEndpoint(context.Context) error {
	f.record("resume_endpoint")
	return f.err
}

func (f *fakeLLMAdmin) EvalReport(_ context.Context, id int64) (report []byte, sha string, err error) {
	f.record("report")
	f.id = id
	if f.err != nil {
		return nil, "", f.err
	}
	return f.report, f.sha, nil
}

const llmBase = "/api/admin/author-metadata/llm"

// llmRouter mounts the LLM routes behind the stub administrator.
func llmRouter(svc AuthorLLMAdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorLLMRoutes(admin.Group("/author-metadata/llm"), svc)
	return r
}

var everyLLMRoute = []struct{ method, path, body string }{
	{http.MethodGet, llmBase + "/status", ""},
	{http.MethodPost, llmBase + "/runs", `{"kind":"eval","mode":"pilot"}`},
	{http.MethodPost, llmBase + "/runs/5/pause", ""},
	{http.MethodPost, llmBase + "/runs/5/resume", ""},
	{http.MethodPost, llmBase + "/runs/5/close", ""},
	{http.MethodGet, llmBase + "/eval-report/5", ""},
	{http.MethodPost, llmBase + "/participants/judge_a/resume", ""},
	{http.MethodPost, llmBase + "/endpoint/resume", ""},
}

func TestAuthorLLMRoutesRequireAnAdministrator(t *testing.T) {
	installSessionStore(t)
	svc := &fakeLLMAdmin{report: []byte(`{}`), sha: "00"}
	previous := authorLLMAdminService
	authorLLMAdminService = func() AuthorLLMAdminService { return svc }
	t.Cleanup(func() { authorLLMAdminService = previous })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupAdminRoutes(r.Group("/api").Group("/admin", middlewares.AdminMiddleware()))
	userToken := adminSession(t, false)
	adminToken := adminSession(t, true)

	for _, route := range everyLLMRoute {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			assert.Equal(t, http.StatusUnauthorized, runsRequest(t, r, route.method, route.path, route.body).Code)

			req := httptest.NewRequestWithContext(context.Background(), route.method, route.path, strings.NewReader(route.body))
			req.Header.Set("Authorization", userToken)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.JSONEq(t, `{"error":"not_admin"}`, rec.Body.String())
		})
	}
	assert.Empty(t, svc.callList(), "a request without an administrator reached the service")

	for _, route := range everyLLMRoute {
		req := httptest.NewRequestWithContext(context.Background(), route.method, route.path, strings.NewReader(route.body))
		req.Header.Set("Authorization", adminToken)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Less(t, rec.Code, 300, "%s %s: %s", route.method, route.path, rec.Body.String())
	}
	assert.Len(t, svc.callList(), len(everyLLMRoute))
}

func TestAuthorLLMRoutesUnavailableUntilWired(t *testing.T) {
	rec := runsRequest(t, llmRouter(unavailableAuthorLLMAdmin{}), http.MethodGet, llmBase+"/status", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"error":"llm_service_unavailable"}`, rec.Body.String())
}

func TestAuthorLLMStatusAndStart(t *testing.T) {
	svc := &fakeLLMAdmin{}
	r := llmRouter(svc)

	rec := runsRequest(t, r, http.MethodGet, llmBase+"/status", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var status struct {
		Status services.AuthorLLMStatus `json:"status"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	assert.True(t, status.Status.Enabled)
	assert.Equal(t, int64(12), status.Status.Run.ID)
	assert.Equal(t, "eval_pilot", status.Status.Steps[0].Key)

	rec = runsRequest(t, r, http.MethodPost, llmBase+"/runs", `{"kind":"eval","mode":"pilot"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"run":{"id":12,"kind":"eval","mode":"pilot","status":"running","stage":"freeze",
		"current_config":false,"estimate_tokens":0,"limit_tokens":0,"spent_tokens":0}}`, rec.Body.String())
	assert.Equal(t, "eval", svc.kind)
	assert.Equal(t, "pilot", svc.mode)
	assert.Equal(t, adminActorID, svc.actor)

	for _, body := range []string{``, `{}`, `{"kind":"eval"}`, `{"mode":"pilot"}`, `{"kind":"eval","mode":"pilot","x":1}`,
		`{"kind":null,"mode":"pilot"}`, `[]`, `{"kind":"eval","mode":"pilot"}{}`} {
		rec = runsRequest(t, r, http.MethodPost, llmBase+"/runs", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.JSONEq(t, `{"error":"invalid_request"}`, rec.Body.String(), body)
	}
	assert.Len(t, svc.callList(), 2, "an invalid body reached the service")
}

func TestAuthorLLMErrorsAreClosedCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{services.ErrAuthorLLMNotConfigured, http.StatusConflict, "llm_not_configured"},
		{services.ErrAuthorLLMPrerequisite, http.StatusConflict, "prerequisite_incomplete"},
		{services.ErrAuthorLLMRunKindUnavailable, http.StatusConflict, "run_kind_unavailable"},
		{fmt.Errorf("creating: %w", database.ErrAuthorLLMActiveRun), http.StatusConflict, "active_run_exists"},
		{database.ErrAuthorLLMRunState, http.StatusConflict, "invalid_transition"},
		{services.ErrAuthorLLMRunConfigChanged, http.StatusConflict, "run_config_changed"},
		{database.ErrAuthorLLMRunNotFound, http.StatusNotFound, "run_not_found"},
		{services.ErrAuthorLLMReportNotFound, http.StatusNotFound, "report_not_found"},
		{services.ErrAuthorLLMInvalidRun, http.StatusBadRequest, "invalid_run"},
		{services.ErrAuthorLLMInvalidSlot, http.StatusBadRequest, "invalid_slot"},
		{fmt.Errorf("pg: secret detail of a failure"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := llmRouter(&fakeLLMAdmin{err: tc.err})
			for _, route := range everyLLMRoute {
				rec := runsRequest(t, r, route.method, route.path, route.body)
				assert.Equal(t, tc.status, rec.Code, route.path)
				assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, tc.code), rec.Body.String(), route.path)
			}
		})
	}
}

func TestAuthorLLMRunActionsAndIDs(t *testing.T) {
	svc := &fakeLLMAdmin{}
	r := llmRouter(svc)
	rec := runsRequest(t, r, http.MethodPost, llmBase+"/runs/34/pause", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(34), svc.id)
	rec = runsRequest(t, r, http.MethodPost, llmBase+"/runs/35/resume", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(35), svc.id)
	assert.Contains(t, rec.Body.String(), `"run":{`)
	rec = runsRequest(t, r, http.MethodPost, llmBase+"/runs/36/close", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(36), svc.id)
	assert.Equal(t, adminActorID, svc.actor, "the closing administrator is passed on")

	for _, path := range []string{"/runs/0/pause", "/runs/-1/resume", "/runs/x/pause", "/runs/01/pause", "/eval-report/abc"} {
		method := http.MethodPost
		if strings.HasPrefix(path, "/eval-report") {
			method = http.MethodGet
		}
		rec = runsRequest(t, r, method, llmBase+path, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, path)
		assert.JSONEq(t, `{"error":"invalid_id"}`, rec.Body.String(), path)
	}
	assert.Len(t, svc.callList(), 3)

	rec = runsRequest(t, r, http.MethodPost, llmBase+"/participants/judge_b/resume", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "judge_b", svc.slot)
	rec = runsRequest(t, r, http.MethodPost, llmBase+"/endpoint/resume", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestAuthorLLMEvalReportDownload(t *testing.T) {
	body := []byte(`{"run_id": 5, "evaluation": {"decision": {"registrable": false}}}`)
	svc := &fakeLLMAdmin{report: body, sha: "ab12"}
	rec := runsRequest(t, llmRouter(svc), http.MethodGet, llmBase+"/eval-report/5", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, body, rec.Body.Bytes(), "the stored bytes, unchanged: their SHA is the report's")
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="author-llm-eval-5.json"`, rec.Header().Get("Content-Disposition"))
	assert.Equal(t, "ab12", rec.Header().Get("X-Report-SHA256"))
	assert.Equal(t, int64(5), svc.id)
}
