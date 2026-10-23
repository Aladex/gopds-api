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

	routes := []struct{ method, path string }{{http.MethodGet, entityBase + "/status"}, {http.MethodPost, entityBase + "/build"}}
	for _, route := range routes {
		assert.Equal(t, http.StatusUnauthorized, runsRequest(t, r, route.method, route.path, "").Code)
		req := httptest.NewRequestWithContext(context.Background(), route.method, route.path, strings.NewReader(""))
		req.Header.Set("Authorization", userToken)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	}
	assert.Empty(t, svc.calls, "a request without an administrator reached the service")
	for _, route := range routes {
		req := httptest.NewRequestWithContext(context.Background(), route.method, route.path, strings.NewReader(""))
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
		"units_skipped":0,"changes":0,"attempts":0,"created_at":"0001-01-01T00:00:00Z"},"report":{"entities":7}}}`,
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

func TestAuthorEntityRoutesUnavailableUntilWired(t *testing.T) {
	rec := runsRequest(t, entityRouter(unavailableAuthorEntityAdmin{}), http.MethodGet, entityBase+"/status", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"error":"entity_service_unavailable"}`, rec.Body.String())
}
