package api

import (
	"context"
	"encoding/json"
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

// The entity step of the card: admin only, an instant start, 409 when a pass
// is active, and the status with the newest report.

type fakeEntityAdmin struct {
	mu    sync.Mutex
	calls []string
	actor int64
	err   error
	// enabled is the autopilot switch the last call set.
	enabled *bool
}

func (f *fakeEntityAdmin) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeEntityAdmin) Status(context.Context) (*services.AuthorEntityStatus, error) {
	f.record("status")
	if f.err != nil {
		return nil, f.err
	}
	stage := database.AuthorEntityStageApply
	return &services.AuthorEntityStatus{
		Run:    &database.AuthorEntityBuildRun{ID: 3, Status: database.AuthorEntityRunRunning, Stage: &stage, UnitsTotal: 10, UnitsDone: 4},
		Report: json.RawMessage(`{"entities":7}`),
	}, nil
}

func (f *fakeEntityAdmin) Start(_ context.Context, actor int64) (*database.AuthorEntityBuildRun, error) {
	f.record("start")
	f.actor = actor
	if f.err != nil {
		return nil, f.err
	}
	return &database.AuthorEntityBuildRun{ID: 4, Status: database.AuthorEntityRunPending}, nil
}

func (f *fakeEntityAdmin) StartWikidataFetch(_ context.Context, actor int64) (*database.WikidataExtract, error) {
	f.record("wikidata")
	f.actor = actor
	if f.err != nil {
		return nil, f.err
	}
	return &database.WikidataExtract{ID: 5, Status: database.WikidataFetching, ChunksTotal: 28}, nil
}

func (f *fakeEntityAdmin) SetAutopilot(_ context.Context, enabled bool, actor int64) (*services.AuthorAutopilotStatus, error) {
	f.record("autopilot")
	f.actor = actor
	if f.err != nil {
		return nil, f.err
	}
	f.enabled = &enabled
	return &services.AuthorAutopilotStatus{AuthorAutopilotState: &database.AuthorAutopilotState{Enabled: enabled},
		QuietSeconds: 1800}, nil
}

const entityBase = "/api/admin/author-metadata/entities"

func entityRouter(svc AuthorEntityAdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorEntityRoutes(admin.Group("/author-metadata/entities"), func() AuthorEntityAdminService { return svc })
	return r
}

func TestAuthorEntityRoutesRequireAnAdministrator(t *testing.T) {
	installSessionStore(t)
	svc := &fakeEntityAdmin{}
	previous := authorEntityAdminService
	authorEntityAdminService = func() AuthorEntityAdminService { return svc }
	t.Cleanup(func() { authorEntityAdminService = previous })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupAdminRoutes(r.Group("/api").Group("/admin", middlewares.AdminMiddleware()))
	userToken := adminSession(t, false)
	adminToken := adminSession(t, true)

	routes := []struct{ method, path, body string }{{http.MethodGet, entityBase + "/status", ""},
		{http.MethodPost, entityBase + "/build", ""}, {http.MethodPost, entityBase + "/wikidata", ""},
		{http.MethodPost, entityBase + "/autopilot", `{"enabled":false}`}}
	for _, route := range routes {
		assert.Equal(t, http.StatusUnauthorized, runsRequest(t, r, route.method, route.path, route.body).Code)
		req := httptest.NewRequestWithContext(context.Background(), route.method, route.path, strings.NewReader(route.body))
		req.Header.Set("Authorization", userToken)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	}
	assert.Empty(t, svc.calls, "a request without an administrator reached the service")
	for _, route := range routes {
		req := httptest.NewRequestWithContext(context.Background(), route.method, route.path, strings.NewReader(route.body))
		req.Header.Set("Authorization", adminToken)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Less(t, rec.Code, 300, "%s %s: %s", route.method, route.path, rec.Body.String())
	}
	assert.Len(t, svc.calls, len(routes))
}

func TestAuthorEntityStatusAndStart(t *testing.T) {
	svc := &fakeEntityAdmin{}
	r := entityRouter(svc)

	rec := runsRequest(t, r, http.MethodGet, entityBase+"/status", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":{"run":{"id":3,"status":"running","stage":"apply","units_total":10,"units_done":4,
		"units_skipped":0,"changes":0,"attempts":0,"created_at":"0001-01-01T00:00:00Z"},"report":{"entities":7},
		"wikidata":null,"autopilot":null}}`,
		rec.Body.String())

	rec = runsRequest(t, r, http.MethodPost, entityBase+"/build", "")
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, adminActorID, svc.actor)
	assert.Contains(t, rec.Body.String(), `"status":"pending"`)

	svc.err = database.ErrAuthorEntityBuildActive
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/build", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"error":"active_run_exists"}`, rec.Body.String())
}

func TestTheWikidataFetchStartsAndOneAtATime(t *testing.T) {
	svc := &fakeEntityAdmin{}
	r := entityRouter(svc)
	rec := runsRequest(t, r, http.MethodPost, entityBase+"/wikidata", "")
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, adminActorID, svc.actor)
	var body struct {
		Extract struct {
			ID          int64  `json:"id"`
			Status      string `json:"status"`
			ChunksTotal int    `json:"chunks_total"`
		} `json:"extract"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(5), body.Extract.ID)
	assert.Equal(t, database.WikidataFetching, body.Extract.Status)
	assert.Equal(t, 28, body.Extract.ChunksTotal)

	svc.err = database.ErrWikidataFetchActive
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/wikidata", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"error":"active_run_exists"}`, rec.Body.String())
}

// The autopilot's switch takes an explicit on or off, by the administrator;
// anything else is refused before the service.
func TestTheAutopilotSwitch(t *testing.T) {
	svc := &fakeEntityAdmin{}
	r := entityRouter(svc)
	for _, body := range []string{"", `{}`, `{"enabled":"yes"}`, `not json`} {
		rec := runsRequest(t, r, http.MethodPost, entityBase+"/autopilot", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.JSONEq(t, `{"error":"invalid_request"}`, rec.Body.String(), body)
	}
	assert.Empty(t, svc.calls, "a malformed switch reached the service")

	rec := runsRequest(t, r, http.MethodPost, entityBase+"/autopilot", `{"enabled":false}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, svc.enabled)
	assert.False(t, *svc.enabled)
	assert.Equal(t, adminActorID, svc.actor)
	assert.JSONEq(t, `{"autopilot":{"enabled":false,"resolve_inputs":0,"failures":0,"updated_at":"0001-01-01T00:00:00Z",
		"quiet_seconds":1800,"waiting_jobs":0,"build_pending":false,"skipped_units":0}}`, rec.Body.String())
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/autopilot", `{"enabled":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, *svc.enabled)

	svc.err = errAuthorEntityServiceUnavailable
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/autopilot", `{"enabled":true}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAuthorEntityRoutesUnavailableUntilWired(t *testing.T) {
	rec := runsRequest(t, entityRouter(unavailableAuthorEntityAdmin{}), http.MethodGet, entityBase+"/status", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"error":"entity_service_unavailable"}`, rec.Body.String())
}
