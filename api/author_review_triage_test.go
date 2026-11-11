package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gopds-api/database"
	"gopds-api/middlewares"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The review triage routes: admin only, the dry run's start behind CSRF, a
// 409 while a batch works, 503 until wired, and the audit sample as TSV.

type fakeTriage struct {
	mu    sync.Mutex
	calls []string
	actor int64
	err   error
	rows  []database.TriageAuditRow
}

func (f *fakeTriage) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeTriage) StartDryRun(_ context.Context, actor int64) (*database.TriageBatch, error) {
	f.record("start")
	f.actor = actor
	if f.err != nil {
		return nil, f.err
	}
	return &database.TriageBatch{ID: 3, Status: database.TriageBatchPlanning, AppliedSteps: []int16{}}, nil
}

func (f *fakeTriage) Batches(context.Context) ([]database.TriageBatch, error) {
	f.record("batches")
	return []database.TriageBatch{{ID: 3, Status: database.TriageBatchPlanned}}, f.err
}

func (f *fakeTriage) Batch(_ context.Context, id int64) (*database.TriageBatchView, error) {
	f.record("batch")
	if f.err != nil {
		return nil, f.err
	}
	return &database.TriageBatchView{Batch: database.TriageBatch{ID: id}, Cells: []database.TriageCell{}}, nil
}

func (f *fakeTriage) AuditSample(context.Context, int64) ([]database.TriageAuditRow, error) {
	f.record("audit")
	return f.rows, f.err
}

func (f *fakeTriage) Apply(_ context.Context, _, actor int64) error {
	f.record("apply")
	f.actor = actor
	return f.err
}

func (f *fakeTriage) Undo(_ context.Context, _, actor int64, step int16) (int, error) {
	f.record(fmt.Sprintf("undo %d", step))
	f.actor = actor
	return 4, f.err
}

func (f *fakeTriage) Stop(_ context.Context, id, actor int64) (*database.TriageBatch, error) {
	f.record("stop")
	f.actor = actor
	if f.err != nil {
		return nil, f.err
	}
	return &database.TriageBatch{ID: id, Status: database.TriageBatchStopped, AppliedSteps: []int16{}}, nil
}

const triageBase = "/api/admin/author-metadata/triage"

func triageRouter(svc AuthorReviewTriageService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorReviewTriageRoutes(admin.Group("/author-metadata/triage"),
		func() AuthorReviewTriageService { return svc })
	return r
}

func triageGet(r http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestTriageRoutesNeedAdmin(t *testing.T) {
	installSessionStore(t)
	svc := &fakeTriage{}
	previous := authorReviewTriageService
	authorReviewTriageService = func() AuthorReviewTriageService { return svc }
	t.Cleanup(func() { authorReviewTriageService = previous })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupAdminRoutes(r.Group("/api").Group("/admin", middlewares.AdminMiddleware()))
	send := func(method, path, token string, csrf bool) int {
		body := ""
		if strings.HasSuffix(path, "/undo") {
			body = `{"step":0}`
		}
		req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		if csrf {
			withCSRF(req, "t4")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	routes := []struct {
		method, path string
		ok           int
	}{
		{http.MethodGet, triageBase + "/batches", http.StatusOK},
		{http.MethodPost, triageBase + "/batches", http.StatusAccepted},
		{http.MethodGet, triageBase + "/batches/3", http.StatusOK},
		{http.MethodGet, triageBase + "/batches/3/audit-sample", http.StatusOK},
		{http.MethodPost, triageBase + "/batches/3/apply", http.StatusAccepted},
		{http.MethodPost, triageBase + "/batches/3/undo", http.StatusAccepted},
		{http.MethodPost, triageBase + "/batches/3/stop", http.StatusOK},
	}
	user := adminSession(t, false)
	for _, route := range routes {
		assert.Equal(t, http.StatusUnauthorized, send(route.method, route.path, "", true), route.path)
		assert.Equal(t, http.StatusNotFound, send(route.method, route.path, user, true), route.path,
			"a non-admin sees no admin area")
	}
	assert.Empty(t, svc.calls, "a non-admin reached the service")
	admin := adminSession(t, true)
	for _, path := range []string{"/batches", "/batches/3/apply", "/batches/3/undo", "/batches/3/stop"} {
		assert.Equal(t, http.StatusForbidden, send(http.MethodPost, triageBase+path, admin, false),
			"%s checks the CSRF pair", path)
	}
	assert.Empty(t, svc.calls)
	for _, route := range routes {
		assert.Equal(t, route.ok, send(route.method, route.path, admin, true), route.path)
	}
	assert.Equal(t, adminActorID, svc.actor)
}

func TestTriageStartAndItsConflict(t *testing.T) {
	svc := &fakeTriage{}
	r := triageRouter(svc)
	rec := csrfRequest(t, r, http.MethodPost, triageBase+"/batches", "")
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.JSONEq(t, `{"batch":{"id":3,"rule_version":"","status":"planning","created_by_user_id":0,
		"created_at":"0001-01-01T00:00:00Z","applied_steps":[],"items_total":0,"items_done":0}}`, rec.Body.String())

	svc.err = database.ErrTriageActiveBatch
	rec = csrfRequest(t, r, http.MethodPost, triageBase+"/batches", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"error":"active_run_exists"}`, rec.Body.String())

	svc.err = database.ErrTriageBatchNotFound
	rec = triageGet(r, triageBase+"/batches/9")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"error":"batch_not_found"}`, rec.Body.String())

	rec = triageGet(r, triageBase+"/batches/x")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error":"invalid_id"}`, rec.Body.String())
}

func TestTriageIsUnavailableUntilWired(t *testing.T) {
	r := triageRouter(unavailableReviewTriage{})
	rec := triageGet(r, triageBase+"/batches")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"error":"triage_service_unavailable"}`, rec.Body.String())
}

func TestTriageAuditSampleIsTSV(t *testing.T) {
	svc := &fakeTriage{rows: []database.TriageAuditRow{
		{ReviewItemID: 11, Pool: "sample", Step: 1, Rule: "four_model", Class: "mononym", Credits: 3,
			Source: []string{"Ива\tн", "", "Пет\nров", "", "Иван Петров"}, Before: []string{"display=a\tb"},
			After: []string{"given=Иван", "family=Петров"}, Action: "edited",
			Evidence: json.RawMessage(`{"run_id":4}`)},
		{ReviewItemID: 12, Pool: "reserve", Step: 7, Rule: "keep_handle", Class: "mononym", Credits: 1,
			Source: []string{"", "", "Zorro"}},
	}}
	rec := triageGet(triageRouter(svc), triageBase+"/batches/5/audit-sample")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `attachment; filename="triage-5-audit.tsv"`, rec.Header().Get("Content-Disposition"))
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/tab-separated-values"))
	lines := strings.Split(strings.TrimSuffix(rec.Body.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "item_id\tpool\tstep\trule\tclass\tcredits\tsrc_first\tsrc_middle\tsrc_last\tsrc_nick\t"+
		"src_display\tbefore\tafter\taction\tevidence\tverdict\tnote", lines[0])
	for _, line := range lines {
		assert.Len(t, strings.Split(line, "\t"), 17, line)
	}
	assert.Equal(t, []string{"11", "sample", "1", "four_model", "mononym", "3", "Ива н", "", "Пет ров", "",
		"Иван Петров", "display=a b", "given=Иван; family=Петров", "edited", `{"run_id":4}`, "", ""},
		strings.Split(lines[1], "\t"))
}

func TestTriageApplyAndUndoAnswers(t *testing.T) {
	svc := &fakeTriage{}
	r := triageRouter(svc)
	rec := csrfRequest(t, r, http.MethodPost, triageBase+"/batches/3/apply", "")
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, adminActorID, svc.actor)

	rec = csrfRequest(t, r, http.MethodPost, triageBase+"/batches/3/undo", `{"step":3}`)
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.JSONEq(t, `{"marked":4}`, rec.Body.String())
	assert.Equal(t, "undo 3", svc.calls[len(svc.calls)-1])

	for _, body := range []string{``, `{}`, `{"step":-1}`, `{"step":9}`, `{"step":"1"}`} {
		rec = csrfRequest(t, r, http.MethodPost, triageBase+"/batches/3/undo", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.JSONEq(t, `{"error":"invalid_request"}`, rec.Body.String(), body)
	}
	assert.Len(t, svc.calls, 2, "a bad undo reaches no service")

	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{database.ErrTriageNothingAudited, http.StatusConflict, "no_audited_step"},
		{database.ErrTriageRuleVersion, http.StatusConflict, "rule_version"},
		{database.ErrTriageBatchState, http.StatusConflict, "batch_state"},
		{database.ErrTriageActiveBatch, http.StatusConflict, "active_run_exists"},
		{database.ErrTriageBatchNotFound, http.StatusNotFound, "batch_not_found"},
		{errors.New("boom"), http.StatusInternalServerError, "internal_error"},
	} {
		svc.err = c.err
		rec = csrfRequest(t, r, http.MethodPost, triageBase+"/batches/3/apply", "")
		assert.Equal(t, c.status, rec.Code, c.code)
		assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, c.code), rec.Body.String())
	}
	svc.err = database.ErrTriageBatchState
	rec = csrfRequest(t, r, http.MethodPost, triageBase+"/batches/3/undo", `{"step":0}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestTriageStopAnswers(t *testing.T) {
	svc := &fakeTriage{}
	r := triageRouter(svc)
	rec := csrfRequest(t, r, http.MethodPost, triageBase+"/batches/3/stop", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, adminActorID, svc.actor)
	var got struct {
		Batch database.TriageBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, int64(3), got.Batch.ID)
	assert.Equal(t, database.TriageBatchStopped, got.Batch.Status)

	rec = csrfRequest(t, r, http.MethodPost, triageBase+"/batches/x/stop", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, []string{"stop"}, svc.calls, "a bad ID reaches no service")
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{database.ErrTriageBatchState, http.StatusConflict, "batch_state"},
		{database.ErrTriageBatchNotFound, http.StatusNotFound, "batch_not_found"},
		{errors.New("boom"), http.StatusInternalServerError, "internal_error"},
	} {
		svc.err = c.err
		rec = csrfRequest(t, r, http.MethodPost, triageBase+"/batches/3/stop", "")
		assert.Equal(t, c.status, rec.Code, c.code)
		assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, c.code), rec.Body.String())
	}
}
