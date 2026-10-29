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
	"github.com/google/uuid"
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

func (f *fakeEntityAdmin) MergeDecisions(context.Context) (*database.MergeDecisionsView, error) {
	f.record("merges")
	if f.err != nil {
		return nil, f.err
	}
	state := "applied"
	return &database.MergeDecisionsView{Decisions: []database.MergeDecisionView{{ID: 9, Kind: "swap", Script: "Cyrl",
			ItemKind: "entity", Outcome: "same", State: &state, SideA: database.MergeSideView{DisplayName: "Стивен Кинг",
				Books: 2}, SideB: database.MergeSideView{DisplayName: "Кинг Стивен", Books: 1}}}, DecisionsTotal: 1,
			Vetoes: []database.MergeVetoView{}, Registrations: []database.MergeRegistration{{ID: 4, ConfigVersion: "llm1-m",
				Kind: "swap", EvidenceReportID: 31, Gate: "judge_consensus", BlockedScripts: []string{"Latn"}, RunID: 33}}},
		nil
}

func (f *fakeEntityAdmin) RevokeMergeDecision(_ context.Context, id, actor int64) (int64, error) {
	f.record(fmt.Sprintf("revoke %d", id))
	f.actor = actor
	return 10, f.err
}

func (f *fakeEntityAdmin) RetireMergeVeto(_ context.Context, id, actor int64) error {
	f.record(fmt.Sprintf("retire %d", id))
	f.actor = actor
	return f.err
}

func (f *fakeEntityAdmin) ReanchorMergeVeto(_ context.Context, id int64, a, b uuid.UUID, actor int64) (int64, error) {
	f.record(fmt.Sprintf("reanchor %d %s %s", id, a, b))
	f.actor = actor
	return 12, f.err
}

func (f *fakeEntityAdmin) RegisterMergeKind(_ context.Context, run int64, kind string, actor int64) (int64, error) {
	f.record(fmt.Sprintf("register %d %s", run, kind))
	f.actor = actor
	return 4, f.err
}

func (f *fakeEntityAdmin) WithdrawMergeRegistration(_ context.Context, id, actor int64) error {
	f.record(fmt.Sprintf("withdraw %d", id))
	f.actor = actor
	return f.err
}

func (f *fakeEntityAdmin) BlockMergeScript(_ context.Context, id int64, script string, actor int64) error {
	f.record(fmt.Sprintf("block %d %s", id, script))
	f.actor = actor
	return f.err
}

func (f *fakeEntityAdmin) ApplyMergeEval(_ context.Context, id, actor int64) (*services.AuthorEntityMergeApplyReport, error) {
	f.record(fmt.Sprintf("apply %d", id))
	f.actor = actor
	if f.err != nil {
		return nil, f.err
	}
	return &services.AuthorEntityMergeApplyReport{RegistrationID: id, Kind: "swap", RunID: 33, ReportID: 31,
		Items: 1081, Accepted: 900, MergeInsertReport: database.MergeInsertReport{Inserted: 890, LapsedBeforeApply: 10,
			BuildRunID: 40}}, nil
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
		{http.MethodPost, entityBase + "/autopilot", `{"enabled":false}`}, {http.MethodGet, entityBase + "/merges", ""},
		{http.MethodPost, entityBase + "/merges/decisions/9/revoke", ""},
		{http.MethodPost, entityBase + "/merges/vetoes/3/retire", ""},
		{http.MethodPost, entityBase + "/merges/vetoes/3/reanchor",
			`{"side_a":"01a17dc0-a8b4-7706-b3db-57951f632b68","side_b":"01a17dc0-a91f-737c-9c41-3ed188ad408f"}`},
		{http.MethodPost, entityBase + "/merges/registrations", `{"run_id":33,"kind":"swap"}`},
		{http.MethodPost, entityBase + "/merges/registrations/4/withdraw", ""},
		{http.MethodPost, entityBase + "/merges/registrations/4/block", `{"script":"Latn"}`},
		{http.MethodPost, entityBase + "/merges/registrations/4/apply", ""}}
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

// The model merges: the block, the revoke by the administrator, a veto's
// retire and re-anchor, each refused before the service when malformed and
// answered with its closed code when the database refuses it.
func TestTheModelMergeRoutes(t *testing.T) {
	svc := &fakeEntityAdmin{}
	r := entityRouter(svc)
	rec := runsRequest(t, r, http.MethodGet, entityBase+"/merges", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"merges":{"decisions":[{"id":9,"kind":"swap","script":"Cyrl","item_kind":"entity",
		"outcome":"same","created_at":"0001-01-01T00:00:00Z","state":"applied",
		"side_a":{"display_name":"Стивен Кинг","books":2},"side_b":{"display_name":"Кинг Стивен","books":1}}],
		"decisions_total":1,"vetoes":[],"unanchored":false,"registrations":[{"id":4,"config_version":"llm1-m",
		"kind":"swap","evidence_report_id":31,"gate":"judge_consensus","registered_at":"0001-01-01T00:00:00Z",
		"blocked_scripts":["Latn"],"run_id":33}]}}`, rec.Body.String())

	rec = runsRequest(t, r, http.MethodPost, entityBase+"/merges/decisions/9/revoke", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"revoke_id":10}`, rec.Body.String())
	assert.Equal(t, adminActorID, svc.actor)
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/merges/vetoes/3/retire", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"veto_id":3}`, rec.Body.String())
	a, b := uuid.New(), uuid.New()
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/merges/vetoes/3/reanchor",
		fmt.Sprintf(`{"side_a":%q,"side_b":%q}`, a, b))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"veto_id":12}`, rec.Body.String())
	assert.Equal(t, []string{"merges", "revoke 9", "retire 3", fmt.Sprintf("reanchor 3 %s %s", a, b)}, svc.calls)

	svc.calls = nil
	for _, bad := range []struct{ path, body string }{
		{"/merges/decisions/0/revoke", ""}, {"/merges/decisions/x/revoke", ""}, {"/merges/vetoes/-1/retire", ""},
		{"/merges/vetoes/3/reanchor", ""}, {"/merges/vetoes/3/reanchor", `{"side_a":"not-a-uuid"}`},
		{"/merges/vetoes/3/reanchor", fmt.Sprintf(`{"side_a":%q}`, a)},
	} {
		rec = runsRequest(t, r, http.MethodPost, entityBase+bad.path, bad.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, bad.path+" "+bad.body)
		assert.JSONEq(t, `{"error":"invalid_request"}`, rec.Body.String())
	}
	assert.Empty(t, svc.calls, "a malformed request reached the service")

	for _, tc := range []struct {
		err  error
		path string
		code int
		want string
	}{
		{database.ErrMergeDecisionNotLive, "/merges/decisions/9/revoke", http.StatusConflict, "decision_not_live"},
		{database.ErrMergeVetoSideUnrecorded, "/merges/decisions/9/revoke", http.StatusConflict, "veto_side_unrecorded"},
		{database.ErrMergeVetoNotLive, "/merges/vetoes/3/retire", http.StatusConflict, "veto_not_live"},
		{database.ErrMergeVetoSides, "/merges/vetoes/3/reanchor", http.StatusUnprocessableEntity, "veto_sides_invalid"},
	} {
		svc.err = tc.err
		rec = runsRequest(t, r, http.MethodPost, entityBase+tc.path, fmt.Sprintf(`{"side_a":%q,"side_b":%q}`, a, b))
		assert.Equal(t, tc.code, rec.Code, tc.want)
		assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, tc.want), rec.Body.String())
	}
}

// The registration routes (E4d): register a kind from a run, withdraw it,
// block a script, apply its eval. Malformed requests never reach the
// service; refusals map to closed codes.
func TestTheMergeRegistrationRoutes(t *testing.T) {
	svc := &fakeEntityAdmin{}
	r := entityRouter(svc)
	rec := runsRequest(t, r, http.MethodPost, entityBase+"/merges/registrations", `{"run_id":33,"kind":"swap"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.JSONEq(t, `{"registration_id":4}`, rec.Body.String())
	assert.Equal(t, adminActorID, svc.actor)
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/merges/registrations/4/withdraw", "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/merges/registrations/4/block", `{"script":"Cyrl-Latn"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec = runsRequest(t, r, http.MethodPost, entityBase+"/merges/registrations/4/apply", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"apply":{"registration_id":4,"kind":"swap","run_id":33,"report_id":31,"items":1081,
		"accepted":900,"inserted":890,"lapsed_before_apply":10,"already_decided":0,"build_run_id":40}}`,
		rec.Body.String())
	assert.Equal(t, []string{"register 33 swap", "withdraw 4", "block 4 Cyrl-Latn", "apply 4"}, svc.calls)

	svc.calls = nil
	for _, bad := range []struct{ path, body string }{
		{"/merges/registrations", ""}, {"/merges/registrations", `{"run_id":33}`},
		{"/merges/registrations", `{"run_id":0,"kind":"swap"}`}, {"/merges/registrations", `{"run_id":33,"kind":"other"}`},
		{"/merges/registrations", `{"run_id":33,"kind":"swap","gate":"pair_agreement"}`},
		{"/merges/registrations", `{"run_id":33,"kind":"swap","kind":"bucket"}`},
		{"/merges/registrations/0/withdraw", ""}, {"/merges/registrations/x/apply", ""},
		{"/merges/registrations/4/block", `{"script":"Grek"}`}, {"/merges/registrations/4/block", `{}`},
		{"/merges/registrations/4/block", `{"Script":"Latn"}`},
	} {
		rec = runsRequest(t, r, http.MethodPost, entityBase+bad.path, bad.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, bad.path+" "+bad.body)
	}
	assert.Empty(t, svc.calls, "a malformed request reached the service")

	for _, tc := range []struct {
		err        error
		path, body string
		code       int
		want       string
	}{
		{services.ErrAuthorEntityMergeReportInvalid, "/merges/registrations", `{"run_id":33,"kind":"swap"}`,
			http.StatusUnprocessableEntity, "merge_report_invalid"},
		{services.ErrAuthorEntityMergeGate, "/merges/registrations", `{"run_id":33,"kind":"swap"}`,
			http.StatusUnprocessableEntity, "merge_gate_invalid"},
		{services.ErrAuthorEntityMergeNotPassed, "/merges/registrations", `{"run_id":33,"kind":"swap"}`,
			http.StatusConflict, "merge_kind_not_passed"},
		{database.ErrMergeRegistrationExists, "/merges/registrations", `{"run_id":33,"kind":"swap"}`,
			http.StatusConflict, "merge_kind_registered"},
		{database.ErrMergeRegistrationNotLive, "/merges/registrations/4/withdraw", "", http.StatusConflict,
			"registration_not_live"},
		{services.ErrAuthorEntityMergeNotRegistered, "/merges/registrations/4/apply", "", http.StatusConflict,
			"registration_not_live"},
		{services.ErrAuthorEntityMergeDriftHeld, "/merges/registrations/4/apply", "", http.StatusConflict,
			"merge_apply_drift_held"},
		{database.ErrAuthorEntityVetoUnanchored, "/merges/registrations/4/apply", "", http.StatusConflict,
			"veto_unanchored"},
		{database.ErrAuthorEntityNoCompletedBuild, "/merges/registrations/4/apply", "", http.StatusConflict,
			"no_completed_build"},
		{database.ErrAuthorEntityBuildActive, "/merges/registrations/4/apply", "", http.StatusConflict,
			"active_run_exists"},
	} {
		svc.err = tc.err
		rec = runsRequest(t, r, http.MethodPost, entityBase+tc.path, tc.body)
		assert.Equal(t, tc.code, rec.Code, tc.want)
		assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, tc.want), rec.Body.String())
	}
}

// E4b-3b review (P2): a re-anchor body is read strictly — a repeated,
// unknown, case-variant or null field and trailing content are refused
// before the service.
func TestTheReanchorBodyIsStrict(t *testing.T) {
	svc := &fakeEntityAdmin{}
	r := entityRouter(svc)
	a, b := uuid.New(), uuid.New()
	for _, body := range []string{
		fmt.Sprintf(`{"side_a":%q,"side_a":%q,"side_b":%q}`, a, b, b),
		fmt.Sprintf(`{"side_a":%q,"side_b":%q,"note":"x"}`, a, b),
		fmt.Sprintf(`{"Side_A":%q,"side_b":%q}`, a, b),
		fmt.Sprintf(`{"side_a":null,"side_b":%q}`, b),
		fmt.Sprintf(`{"side_a":%q,"side_b":%q} {}`, a, b),
	} {
		rec := runsRequest(t, r, http.MethodPost, entityBase+"/merges/vetoes/3/reanchor", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
	assert.Empty(t, svc.calls, "an ambiguous body reached the service")
	rec := runsRequest(t, r, http.MethodPost, entityBase+"/merges/vetoes/3/reanchor",
		fmt.Sprintf(`{"side_a":%q,"side_b":%q}`, a, b))
	require.Equal(t, http.StatusOK, rec.Code)
}
