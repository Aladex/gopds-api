package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/middlewares"
	"gopds-api/models"
	"gopds-api/services"
	"gopds-api/sessions"
	"gopds-api/utils"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunService records every call and answers with the configured values.
type fakeRunService struct {
	mu sync.Mutex

	calls      []string
	lastID     int64
	start      *AuthorMetadataRunStart
	actor      int64
	retryStage string
	retryClass string

	run      AuthorMetadataRunView
	current  *AuthorMetadataRunView
	latest   *AuthorMetadataRunView
	report   AuthorMetadataRunReport
	reopened int64
	err      error
}

func (f *fakeRunService) record(call string, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	f.lastID = id
}

func (f *fakeRunService) Start(_ context.Context, start AuthorMetadataRunStart, actorUserID int64) (AuthorMetadataRunView, error) {
	f.record("start", 0)
	f.start, f.actor = &start, actorUserID
	return f.run, f.err
}

func (f *fakeRunService) Current(context.Context) (*AuthorMetadataRunView, error) {
	f.record("current", 0)
	return f.current, f.err
}

func (f *fakeRunService) Latest(context.Context) (*AuthorMetadataRunView, error) {
	f.record("latest", 0)
	return f.latest, f.err
}

func (f *fakeRunService) Get(_ context.Context, id int64) (AuthorMetadataRunView, error) {
	f.record("get", id)
	return f.run, f.err
}

func (f *fakeRunService) Report(_ context.Context, id int64) (AuthorMetadataRunReport, error) {
	f.record("report", id)
	return f.report, f.err
}

func (f *fakeRunService) Pause(_ context.Context, id int64) (AuthorMetadataRunView, error) {
	f.record("pause", id)
	return f.run, f.err
}

func (f *fakeRunService) Resume(_ context.Context, id int64) (AuthorMetadataRunView, error) {
	f.record("resume", id)
	return f.run, f.err
}

func (f *fakeRunService) ApproveFull(_ context.Context, id, actorUserID int64) (AuthorMetadataRunView, error) {
	f.record("approve-full", id)
	f.actor = actorUserID
	return f.run, f.err
}

func (f *fakeRunService) Retry(_ context.Context, id int64, stage, errorClass string) (int64, error) {
	f.record("retry", id)
	f.retryStage, f.retryClass = stage, errorClass
	return f.reopened, f.err
}

func (f *fakeRunService) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// adminActorID is the user the stub identity middleware authenticates.
const adminActorID int64 = 7

// runsRouter mounts the run routes the way the admin group does, behind a
// stub identity that sets what AdminMiddleware sets for an administrator.
func runsRouter(svc AuthorMetadataRunService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorMetadataRunRoutes(admin.Group("/author-metadata"), svc)
	return r
}

func runsRequest(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const runsBase = "/api/admin/author-metadata/runs"

// sampleRun is a fully populated run in a non-UTC zone, so the tests see
// every field and the UTC normalization.
func sampleRun(now time.Time) AuthorMetadataRunView {
	zone := time.FixedZone("UTC+3", 3*60*60)
	created := now.Add(-2 * time.Hour).In(zone)
	started := now.Add(-time.Hour).In(zone)
	archive := "fb2-000001-000100.zip"
	lastError := "archive_unreadable"
	return AuthorMetadataRunView{
		ID: 42, Mode: "pilot_archive", Status: "running",
		ExtractorVersion: "fb2-metadata-v1", NormalizerVersion: "authornorm-local-v1",
		CreatedAt: created, StartedAt: &started, LastErrorClass: &lastError,
		Stages: AuthorMetadataRunStages{
			Extraction: AuthorMetadataExtractionStage{
				Total: 100, Done: 60, Pending: 38, Leased: 2, OldestPendingAgeS: 95,
				ByStatus: AuthorMetadataExtractionByStatus{
					Extracted: 50, ExtractedNoAuthor: 3, AlreadyCurrent: 2, EntryMissing: 1,
					InvalidFB2: 1, UnsupportedEncoding: 1, MetadataParseFailed: 2,
				},
				CurrentArchive: &archive, ItemsPerMinute: 12.5,
			},
			Local:  AuthorMetadataLocalStage{Total: 80, Done: 70, Pending: 6, Leased: 1, Failed: 3, OldestPendingAgeS: 40},
			Review: AuthorMetadataReviewStage{Open: 9, Closed: 4},
		},
		Credits: AuthorMetadataCreditCounts{
			Selected: 0, Invalid: 2, Review: 9, Pending: 7,
			Unresolved: map[string]int64{"policy_not_registered": 51, "normalizer_failed": 1},
		},
	}
}

// sampleRunJSON is sampleRun as the contract serializes it.
func sampleRunJSON(now time.Time) string {
	stamp := func(ts time.Time) string { return ts.UTC().Format(time.RFC3339Nano) }
	return fmt.Sprintf(`{
		"id": 42, "mode": "pilot_archive", "status": "running",
		"extractor_version": "fb2-metadata-v1", "normalizer_version": "authornorm-local-v1",
		"created_at": %q, "started_at": %q, "extraction_completed_at": null, "completed_at": null,
		"last_error_class": "archive_unreadable", "approved_for_full": false,
		"stages": {
			"extraction": {"total": 100, "done": 60, "pending": 38, "leased": 2, "oldest_pending_age_s": 95,
				"by_status": {"extracted": 50, "extracted_no_author": 3, "already_current": 2, "entry_missing": 1,
					"invalid_fb2": 1, "unsupported_encoding": 1, "metadata_parse_failed": 2},
				"current_archive": "fb2-000001-000100.zip", "items_per_minute": 12.5},
			"local": {"total": 80, "done": 70, "pending": 6, "leased": 1, "failed": 3, "oldest_pending_age_s": 40},
			"review": {"open": 9, "closed": 4}
		},
		"credits": {"selected": 0, "invalid": 2, "review": 9, "pending": 7,
			"unresolved": {"policy_not_registered": 51, "normalizer_failed": 1}}
	}`, stamp(now.Add(-2*time.Hour)), stamp(now.Add(-time.Hour)))
}

// --- RED 1: route and auth contract through the real AdminMiddleware ---

// adminSession installs an in-memory session store and a signing key and
// returns a valid access token for a user with the given admin flag.
func adminSession(t *testing.T, superuser bool) string {
	t.Helper()
	token, _, err := utils.CreateTokenPair(models.User{ID: adminActorID, Login: "operator", IsSuperUser: superuser})
	require.NoError(t, err)
	require.NoError(t, sessions.SetSessionKey(context.Background(), models.LoggedInUser{User: "operator", Token: &token}))
	return token
}

func installSessionStore(t *testing.T) {
	t.Helper()
	store, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(store.Close)
	client := redis.NewClient(&redis.Options{Addr: store.Addr()})
	sessions.SetRedisConnections(client, client)
	t.Cleanup(func() {
		sessions.SetRedisConnections(nil, nil)
		_ = client.Close()
	})
	previousKey := viper.GetString("sessions.key")
	viper.Set("sessions.key", "runs-route-test-key")
	t.Cleanup(func() { viper.Set("sessions.key", previousKey) })
}

// productionAdminRouter mounts SetupAdminRoutes behind the real
// AdminMiddleware exactly as cmd/gopds does, with the run service wiring
// point answering with the fake.
func productionAdminRouter(t *testing.T, svc AuthorMetadataRunService) *gin.Engine {
	t.Helper()
	previous := authorMetadataRunService
	authorMetadataRunService = func() AuthorMetadataRunService { return svc }
	t.Cleanup(func() { authorMetadataRunService = previous })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupAdminRoutes(r.Group("/api").Group("/admin", middlewares.AdminMiddleware()))
	return r
}

var everyRunRoute = []struct{ method, path, body string }{
	{http.MethodPost, runsBase, `{"mode":"smoke","book_ids":[1]}`},
	{http.MethodGet, runsBase + "/current", ""},
	{http.MethodGet, runsBase + "/latest", ""},
	{http.MethodGet, runsBase + "/5", ""},
	{http.MethodGet, runsBase + "/5/report", ""},
	{http.MethodPost, runsBase + "/5/pause", ""},
	{http.MethodPost, runsBase + "/5/resume", ""},
	{http.MethodPost, runsBase + "/5/approve-full", ""},
	{http.MethodPost, runsBase + "/5/retry", `{"stage":"local","error_class":"normalizer_failed"}`},
}

func TestAuthorMetadataRunRoutesRequireAnAdministrator(t *testing.T) {
	installSessionStore(t)
	svc := &fakeRunService{run: sampleRun(time.Now()), report: AuthorMetadataRunReport{AuthorMetadataRunView: sampleRun(time.Now())}}
	r := productionAdminRouter(t, svc)
	userToken := adminSession(t, false)
	adminToken := adminSession(t, true)

	for _, route := range everyRunRoute {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			noToken := runsRequest(t, r, route.method, route.path, route.body)
			assert.Equal(t, http.StatusUnauthorized, noToken.Code)

			req := httptest.NewRequestWithContext(context.Background(), route.method, route.path, strings.NewReader(route.body))
			req.Header.Set("Authorization", userToken)
			nonAdmin := httptest.NewRecorder()
			r.ServeHTTP(nonAdmin, req)
			assert.Equal(t, http.StatusNotFound, nonAdmin.Code, "security by 404 for a non-admin")
			assert.JSONEq(t, `{"error":"not_admin"}`, nonAdmin.Body.String())
		})
	}
	assert.Empty(t, svc.callList(), "no request without an administrator reached the service")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, runsBase+"/current", http.NoBody)
	req.Header.Set("Authorization", adminToken)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"current"}, svc.callList())
}

// Until the phase-8 run service is wired, the routes exist and answer with a
// closed 500 code instead of panicking or disappearing.
func TestAuthorMetadataRunServiceWiringPointIsUnavailableUntilWired(t *testing.T) {
	for _, path := range []string{"/current", "/latest"} {
		t.Run("GET "+path, func(t *testing.T) {
			rec := runsRequest(t, runsRouter(authorMetadataRunService()), http.MethodGet, runsBase+path, "")
			assert.Equal(t, http.StatusInternalServerError, rec.Code)
			assert.JSONEq(t, `{"error":"run_service_unavailable"}`, rec.Body.String())
		})
	}
}

// --- RED 2 and 9: start ---

func TestStartAuthorMetadataRunValidation(t *testing.T) {
	tenThousand := make([]string, 10000)
	for i := range tenThousand {
		tenThousand[i] = strconv.Itoa(i + 1)
	}
	tooMany := append(append([]string(nil), tenThousand...), "10001")
	cases := []struct {
		name, body, code string
	}{
		{"not json", `{`, "invalid_request"},
		{"unknown field", `{"mode":"smoke","book_ids":[1],"extra":1}`, "invalid_request"},
		{"book id not an integer", `{"mode":"smoke","book_ids":[1.5]}`, "invalid_request"},
		{"book id as string", `{"mode":"smoke","book_ids":["1"]}`, "invalid_request"},
		{"missing mode", `{"book_ids":[1]}`, "invalid_mode"},
		{"unknown mode", `{"mode":"everything","book_ids":[1]}`, "invalid_mode"},
		{"smoke without selector", `{"mode":"smoke"}`, "invalid_selector"},
		{"smoke with both selectors", `{"mode":"smoke","book_ids":[1],"archive":"a.zip"}`, "invalid_selector"},
		{"pilot without selector", `{"mode":"pilot_archive"}`, "invalid_selector"},
		{"empty book id list is no selector", `{"mode":"smoke","book_ids":[]}`, "invalid_selector"},
		{"full with book ids", `{"mode":"full","book_ids":[1]}`, "invalid_selector"},
		{"full with archive", `{"mode":"full","archive":"a.zip"}`, "invalid_selector"},
		{"full with an empty book id list", `{"mode":"full","book_ids":[]}`, "invalid_selector"},
		{"zero book id", `{"mode":"smoke","book_ids":[1,0]}`, "invalid_book_id"},
		{"negative book id", `{"mode":"smoke","book_ids":[-3]}`, "invalid_book_id"},
		{"duplicate book id", `{"mode":"smoke","book_ids":[4,5,4]}`, "duplicate_book_id"},
		{"10001 book ids", `{"mode":"smoke","book_ids":[` + strings.Join(tooMany, ",") + `]}`, "too_many_book_ids"},
		{"empty archive", `{"mode":"pilot_archive","archive":""}`, "invalid_archive"},
		{"blank archive", `{"mode":"pilot_archive","archive":"  "}`, "invalid_archive"},
		{"archive with a path", `{"mode":"pilot_archive","archive":"../x.zip"}`, "invalid_archive"},
		{"archive in a directory", `{"mode":"pilot_archive","archive":"dir/x.zip"}`, "invalid_archive"},
		{"archive dot dot", `{"mode":"pilot_archive","archive":".."}`, "invalid_archive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &fakeRunService{}
			rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase, c.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.JSONEq(t, `{"error":"`+c.code+`"}`, rec.Body.String())
			assert.Empty(t, svc.callList(), "an invalid request never reaches the service")
		})
	}

	t.Run("an empty object is a missing mode, not a malformed body", func(t *testing.T) {
		rec := runsRequest(t, runsRouter(&fakeRunService{}), http.MethodPost, runsBase, `{}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.JSONEq(t, `{"error":"invalid_mode"}`, rec.Body.String())
	})

	t.Run("10000 book ids are accepted", func(t *testing.T) {
		svc := &fakeRunService{run: sampleRun(time.Now())}
		rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase,
			`{"mode":"smoke","book_ids":[`+strings.Join(tenThousand, ",")+`]}`)
		assert.Equal(t, http.StatusCreated, rec.Code)
		require.NotNil(t, svc.start)
		assert.Len(t, svc.start.BookIDs, 10000)
	})
}

func TestStartAuthorMetadataRunPassesTheSelectorAndActor(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name, body string
		want       AuthorMetadataRunStart
	}{
		{"smoke by book ids", `{"mode":"smoke","book_ids":[3,1,2]}`,
			AuthorMetadataRunStart{Mode: "smoke", BookIDs: []int64{3, 1, 2}}},
		{"smoke by archive", `{"mode":"smoke","archive":"a.zip"}`,
			AuthorMetadataRunStart{Mode: "smoke", Archive: strPtr("a.zip")}},
		{"pilot by archive", `{"mode":"pilot_archive","archive":"fb2-1-2.zip"}`,
			AuthorMetadataRunStart{Mode: "pilot_archive", Archive: strPtr("fb2-1-2.zip")}},
		{"pilot by book ids", `{"mode":"pilot_archive","book_ids":[9]}`,
			AuthorMetadataRunStart{Mode: "pilot_archive", BookIDs: []int64{9}}},
		{"full", `{"mode":"full"}`, AuthorMetadataRunStart{Mode: "full"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &fakeRunService{run: sampleRun(now)}
			rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase, c.body)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			assert.JSONEq(t, `{"run":`+sampleRunJSON(now)+`}`, rec.Body.String())
			require.NotNil(t, svc.start)
			assert.Equal(t, c.want, *svc.start)
			assert.Equal(t, adminActorID, svc.actor)
		})
	}
}

func strPtr(s string) *string { return &s }

// RED 2 and 9: the service's refusals map to the contract's 409 codes.
func TestStartAuthorMetadataRunConflicts(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{ErrAuthorMetadataActiveRunExists, "active_run_exists"},
		{ErrAuthorMetadataFullRunNotApproved, "full_run_not_approved"},
		{fmt.Errorf("pilot report versions differ: %w", ErrAuthorMetadataFullRunNotApproved), "full_run_not_approved"},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			svc := &fakeRunService{err: c.err}
			rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase, `{"mode":"full"}`)
			assert.Equal(t, http.StatusConflict, rec.Code)
			assert.JSONEq(t, `{"error":"`+c.code+`"}`, rec.Body.String())
		})
	}
}

// --- RED 3: current ---

func TestCurrentAuthorMetadataRun(t *testing.T) {
	t.Run("no run is null", func(t *testing.T) {
		rec := runsRequest(t, runsRouter(&fakeRunService{}), http.MethodGet, runsBase+"/current", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, `{"run":null}`, rec.Body.String())
	})
	t.Run("the active run", func(t *testing.T) {
		now := time.Now()
		run := sampleRun(now)
		rec := runsRequest(t, runsRouter(&fakeRunService{current: &run}), http.MethodGet, runsBase+"/current", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"run":`+sampleRunJSON(now)+`}`, rec.Body.String())
	})
}

// --- RED (review UX task A): latest ---

func TestLatestAuthorMetadataRun(t *testing.T) {
	t.Run("no run is null", func(t *testing.T) {
		svc := &fakeRunService{}
		rec := runsRequest(t, runsRouter(svc), http.MethodGet, runsBase+"/latest", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, `{"run":null}`, rec.Body.String())
		assert.Equal(t, []string{"latest"}, svc.callList())
	})
	t.Run("the most recent run, terminal statuses included", func(t *testing.T) {
		now := time.Now()
		run := sampleRun(now)
		run.Status = "completed"
		wantJSON := strings.Replace(sampleRunJSON(now), `"status": "running"`, `"status": "completed"`, 1)
		rec := runsRequest(t, runsRouter(&fakeRunService{latest: &run}), http.MethodGet, runsBase+"/latest", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"run":`+wantJSON+`}`, rec.Body.String())
	})
	t.Run("a service failure is a closed code, never free text", func(t *testing.T) {
		svc := &fakeRunService{err: fmt.Errorf("private: sql: connection refused")}
		rec := runsRequest(t, runsRouter(svc), http.MethodGet, runsBase+"/latest", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.JSONEq(t, `{"error":"internal_error"}`, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "connection refused")
	})
	t.Run("latest is not parsed as a run id", func(t *testing.T) {
		// The static segment must win over the :id param route: the word
		// "latest" is not a canonical decimal and must not answer invalid_id.
		svc := &fakeRunService{}
		rec := runsRequest(t, runsRouter(svc), http.MethodGet, runsBase+"/latest", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []string{"latest"}, svc.callList())
	})
}

// --- RED 6: the status shape ---

// The run carries three stage blocks (extraction, local, review — no LLM
// stage, scope amendment A6), every terminal extraction status even at zero,
// queue age, throughput and the unresolved breakdown; timestamps are UTC.
func TestGetAuthorMetadataRunStatusShape(t *testing.T) {
	now := time.Now()
	svc := &fakeRunService{run: sampleRun(now)}
	rec := runsRequest(t, runsRouter(svc), http.MethodGet, runsBase+"/42", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"run":`+sampleRunJSON(now)+`}`, rec.Body.String())
	assert.Equal(t, int64(42), svc.lastID)
	assert.NotContains(t, rec.Body.String(), "+03:00", "timestamps are UTC")
	for _, absent := range []string{"llm", "token", "cost"} {
		assert.NotContains(t, strings.ToLower(rec.Body.String()), absent)
	}
}

// Zero values serialize as zeros, empty collections as [] / {}, absent
// timestamps as null — never omitted, never null for a collection.
func TestAuthorMetadataRunZeroValuesKeepTheShape(t *testing.T) {
	now := time.Now()
	svc := &fakeRunService{
		run:    AuthorMetadataRunView{ID: 1, Mode: "smoke", Status: "pending", CreatedAt: now},
		report: AuthorMetadataRunReport{AuthorMetadataRunView: AuthorMetadataRunView{ID: 1, Mode: "smoke", Status: "pending", CreatedAt: now}},
	}
	stage := `"stages": {
		"extraction": {"total": 0, "done": 0, "pending": 0, "leased": 0, "oldest_pending_age_s": 0,
			"by_status": {"extracted": 0, "extracted_no_author": 0, "already_current": 0, "entry_missing": 0,
				"invalid_fb2": 0, "unsupported_encoding": 0, "metadata_parse_failed": 0},
			"current_archive": null, "items_per_minute": 0},
		"local": {"total": 0, "done": 0, "pending": 0, "leased": 0, "failed": 0, "oldest_pending_age_s": 0},
		"review": {"open": 0, "closed": 0}},
		"credits": {"selected": 0, "invalid": 0, "review": 0, "pending": 0, "unresolved": {}}`
	run := fmt.Sprintf(`"id": 1, "mode": "smoke", "status": "pending", "extractor_version": "", "normalizer_version": "",
		"created_at": %q, "started_at": null, "extraction_completed_at": null, "completed_at": null,
		"last_error_class": null, "approved_for_full": false, `, now.UTC().Format(time.RFC3339Nano)) + stage

	rec := runsRequest(t, runsRouter(svc), http.MethodGet, runsBase+"/1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"run":{`+run+`}}`, rec.Body.String())

	rec = runsRequest(t, runsRouter(svc), http.MethodGet, runsBase+"/1/report", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"report":{`+run+`, "ready": false, "not_ready_reasons": [], "duration_s": 0,
		"db_growth_bytes": 0, "by_class": {}, "by_script": {}}}`, rec.Body.String())
}

// --- RED 7: report ---

// The report is the run plus the readiness verdict; the review backlog
// (stages.review.open) and the hidden pending count (credits.pending) are
// explicit fields, never folded into another number.
func TestAuthorMetadataRunReport(t *testing.T) {
	now := time.Now()
	svc := &fakeRunService{report: AuthorMetadataRunReport{
		AuthorMetadataRunView: sampleRun(now),
		Ready:                 false,
		NotReadyReasons:       []string{"extraction_not_terminal", "credits_pending"},
		DurationS:             3600,
		DBGrowthBytes:         1 << 20,
		ByClass:               map[string]int64{"full_name": 40, "initials": 9},
		ByScript:              map[string]int64{"Cyrl": 45, "Latn": 4},
	}}
	rec := runsRequest(t, runsRouter(svc), http.MethodGet, runsBase+"/42/report", "")

	require.Equal(t, http.StatusOK, rec.Code)
	run := sampleRunJSON(now)
	report := strings.TrimSuffix(strings.TrimSpace(run), "}") + `,
		"ready": false, "not_ready_reasons": ["extraction_not_terminal", "credits_pending"], "duration_s": 3600,
		"db_growth_bytes": 1048576, "by_class": {"full_name": 40, "initials": 9}, "by_script": {"Cyrl": 45, "Latn": 4}}`
	assert.JSONEq(t, `{"report":`+report+`}`, rec.Body.String())

	var decoded struct {
		Report struct {
			Stages struct {
				Review struct{ Open int64 } `json:"review"`
			} `json:"stages"`
			Credits struct{ Pending int64 } `json:"credits"`
		} `json:"report"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &decoded))
	assert.Equal(t, int64(9), decoded.Report.Stages.Review.Open, "review backlog")
	assert.Equal(t, int64(7), decoded.Report.Credits.Pending, "hidden pending count")
	assert.Equal(t, int64(42), svc.lastID)
}

// --- RED 4: pause and resume ---

func TestPauseAndResumeAuthorMetadataRun(t *testing.T) {
	for _, action := range []string{"pause", "resume"} {
		t.Run(action+" valid transition", func(t *testing.T) {
			now := time.Now()
			svc := &fakeRunService{run: sampleRun(now)}
			rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase+"/42/"+action, "")
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.JSONEq(t, `{"run":`+sampleRunJSON(now)+`}`, rec.Body.String())
			assert.Equal(t, []string{action}, svc.callList())
			assert.Equal(t, int64(42), svc.lastID)
		})
		t.Run(action+" duplicate or terminal", func(t *testing.T) {
			rec := runsRequest(t, runsRouter(&fakeRunService{err: ErrAuthorMetadataInvalidTransition}),
				http.MethodPost, runsBase+"/42/"+action, "")
			assert.Equal(t, http.StatusConflict, rec.Code)
			assert.JSONEq(t, `{"error":"invalid_transition"}`, rec.Body.String())
		})
		t.Run(action+" missing run", func(t *testing.T) {
			rec := runsRequest(t, runsRouter(&fakeRunService{err: ErrAuthorMetadataRunNotFound}),
				http.MethodPost, runsBase+"/42/"+action, "")
			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.JSONEq(t, `{"error":"run_not_found"}`, rec.Body.String())
		})
	}
}

func TestGetAndReportOfAMissingRunAre404(t *testing.T) {
	for _, path := range []string{runsBase + "/9", runsBase + "/9/report"} {
		rec := runsRequest(t, runsRouter(&fakeRunService{err: ErrAuthorMetadataRunNotFound}), http.MethodGet, path, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
		assert.JSONEq(t, `{"error":"run_not_found"}`, rec.Body.String(), path)
	}
}

// --- RED 8: approve-full ---

func TestApproveFullAuthorMetadataRun(t *testing.T) {
	t.Run("a completed pilot is approved by the acting administrator", func(t *testing.T) {
		now := time.Now()
		approved := sampleRun(now)
		approved.ApprovedForFull = true
		svc := &fakeRunService{run: approved}
		rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase+"/42/approve-full", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"approved_for_full":true`)
		assert.Equal(t, int64(42), svc.lastID)
		assert.Equal(t, adminActorID, svc.actor, "the admin actor is recorded")
	})
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrAuthorMetadataNotACompletedPilot, http.StatusConflict, "not_a_completed_pilot"},
		{ErrAuthorMetadataAlreadyApproved, http.StatusConflict, "already_approved"},
		{ErrAuthorMetadataRunNotFound, http.StatusNotFound, "run_not_found"},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			rec := runsRequest(t, runsRouter(&fakeRunService{err: c.err}), http.MethodPost, runsBase+"/42/approve-full", "")
			assert.Equal(t, c.status, rec.Code)
			assert.JSONEq(t, `{"error":"`+c.code+`"}`, rec.Body.String())
		})
	}
}

// --- RED 5: retry ---

func TestRetryAuthorMetadataRunAcceptsOnlyClosedClasses(t *testing.T) {
	// The contract's closed lists (plans/briefs/authors-api-contract.md).
	extraction := []string{
		"entry_missing", "invalid_fb2", "unsupported_encoding", "metadata_parse_failed",
		"lease_expired", "max_attempts_exceeded", "transient_database", "archive_unreadable", "extraction_failed",
	}
	local := []string{"transient_database", "normalizer_failed", "lease_expired", "max_attempts_exceeded"}
	accepted := map[string][]string{"extraction": extraction, "local": local}
	for stage, classes := range accepted {
		for _, class := range classes {
			t.Run(stage+" "+class, func(t *testing.T) {
				svc := &fakeRunService{reopened: 17}
				rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase+"/42/retry",
					`{"stage":"`+stage+`","error_class":"`+class+`"}`)
				assert.Equal(t, http.StatusAccepted, rec.Code)
				assert.Equal(t, `{"reopened":17}`, rec.Body.String())
				assert.Equal(t, stage, svc.retryStage)
				assert.Equal(t, class, svc.retryClass)
				assert.Equal(t, int64(42), svc.lastID)
			})
		}
	}

	rejected := []struct{ name, body, code string }{
		{"no body", ``, "invalid_request"},
		{"unknown field", `{"stage":"local","error_class":"normalizer_failed","all":true}`, "invalid_request"},
		{"llm stage", `{"stage":"llm","error_class":"transient_database"}`, "invalid_stage"},
		{"review stage", `{"stage":"review","error_class":"transient_database"}`, "invalid_stage"},
		{"missing stage", `{"error_class":"transient_database"}`, "invalid_stage"},
		{"free text class", `{"stage":"local","error_class":"connection refused"}`, "invalid_error_class"},
		{"missing class", `{"stage":"local"}`, "invalid_error_class"},
		{"extraction-only class at the local stage", `{"stage":"local","error_class":"invalid_fb2"}`, "invalid_error_class"},
		{"local-only class at the extraction stage", `{"stage":"extraction","error_class":"normalizer_failed"}`, "invalid_error_class"},
		{"a success status is not an error class", `{"stage":"extraction","error_class":"extracted"}`, "invalid_error_class"},
		{"case matters", `{"stage":"local","error_class":"Normalizer_Failed"}`, "invalid_error_class"},
		{"a systemic class ends the run, no retry", `{"stage":"extraction","error_class":"database_invariant"}`,
			"invalid_error_class"},
		{"version mismatch is systemic", `{"stage":"extraction","error_class":"version_mismatch"}`, "invalid_error_class"},
		{"a misconfigured extractor is systemic", `{"stage":"extraction","error_class":"extractor_misconfigured"}`,
			"invalid_error_class"},
		{"a refused input cannot be retried", `{"stage":"local","error_class":"no_author_credit"}`, "invalid_error_class"},
	}
	for _, c := range rejected {
		t.Run(c.name, func(t *testing.T) {
			svc := &fakeRunService{reopened: 1}
			rec := runsRequest(t, runsRouter(svc), http.MethodPost, runsBase+"/42/retry", c.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.JSONEq(t, `{"error":"`+c.code+`"}`, rec.Body.String())
			assert.Empty(t, svc.callList())
		})
	}

	t.Run("nothing to reopen is still an exact count", func(t *testing.T) {
		rec := runsRequest(t, runsRouter(&fakeRunService{}), http.MethodPost, runsBase+"/42/retry",
			`{"stage":"local","error_class":"normalizer_failed"}`)
		assert.Equal(t, http.StatusAccepted, rec.Code)
		assert.Equal(t, `{"reopened":0}`, rec.Body.String())
	})
	t.Run("missing run", func(t *testing.T) {
		rec := runsRequest(t, runsRouter(&fakeRunService{err: ErrAuthorMetadataRunNotFound}), http.MethodPost,
			runsBase+"/42/retry", `{"stage":"local","error_class":"normalizer_failed"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.JSONEq(t, `{"error":"run_not_found"}`, rec.Body.String())
	})
}

// --- RED 10: no free text ---

// Whatever text a service error carries — parser messages, source names,
// database detail — the response is a closed code.
func TestAuthorMetadataRunErrorsNeverEchoFreeText(t *testing.T) {
	secret := "Секретный Писатель: XML syntax error on line 3"
	errs := []error{
		errors.New(secret),
		fmt.Errorf("persisting %s: %w", secret, errors.New("ERROR #23505 duplicate key")),
		fmt.Errorf("%s: %w", secret, ErrAuthorMetadataInvalidTransition),
	}
	for i, svcErr := range errs {
		for _, route := range everyRunRoute {
			t.Run(fmt.Sprintf("%d %s %s", i, route.method, route.path), func(t *testing.T) {
				rec := runsRequest(t, runsRouter(&fakeRunService{err: svcErr}), route.method, route.path, route.body)
				assert.NotContains(t, rec.Body.String(), "Секретный")
				assert.NotContains(t, rec.Body.String(), "syntax")
				assert.NotContains(t, rec.Body.String(), "23505")
				var body map[string]string
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Contains(t, []string{"internal_error", "invalid_transition"}, body["error"])
			})
		}
	}
}

// --- RED 11: IDs ---

func TestAuthorMetadataRunIDsMustBePositiveIntegers(t *testing.T) {
	for _, id := range []string{"0", "-1", "abc", "1.5", "9223372036854775808", "1e3", " 1", "0x10", "+5", "007"} {
		for _, suffix := range []string{"", "/report", "/pause", "/resume", "/approve-full", "/retry"} {
			method := http.MethodPost
			if suffix == "" || suffix == "/report" {
				method = http.MethodGet
			}
			body := ""
			if suffix == "/retry" {
				body = `{"stage":"local","error_class":"normalizer_failed"}`
			}
			t.Run(id+suffix, func(t *testing.T) {
				svc := &fakeRunService{}
				path := runsBase + "/" + strings.ReplaceAll(id, " ", "%20") + suffix
				rec := runsRequest(t, runsRouter(svc), method, path, body)
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				assert.JSONEq(t, `{"error":"invalid_id"}`, rec.Body.String())
				assert.Empty(t, svc.callList())
			})
		}
	}
}

// --- strict request bodies (review round 1) ---

// A body is exactly one JSON object with exactly the contract's field names,
// each at most once, no null anywhere (request fields are never nullable, so
// a null field or element is a value of the wrong type), and nothing after
// it but whitespace. Everything else is invalid_request and never reaches the
// service.
func TestAuthorMetadataRunRequestBodiesAreStrict(t *testing.T) {
	const (
		startOK = `{"mode":"smoke","book_ids":[1]}`
		retryOK = `{"stage":"local","error_class":"normalizer_failed"}`
	)
	type probe struct{ name, body string }
	shared := func(ok string) []probe {
		return []probe{
			{"trailing closing brace", ok + `}`},
			{"trailing closing bracket", ok + `]`},
			{"second object", ok + ok},
			{"trailing garbage", ok + ` x`},
			{"trailing comma", ok + `,`},
			{"top-level null", `null`},
			{"top-level array", `[` + ok + `]`},
			{"top-level string", `"` + strings.ReplaceAll(ok, `"`, `\"`) + `"`},
			{"top-level number", `1`},
			{"empty body", ``},
			{"whitespace only", " \n\t"},
		}
	}
	routes := []struct {
		path   string
		ok     string
		probes []probe
	}{
		{runsBase, startOK, append(shared(startOK),
			probe{"null book id element", `{"mode":"smoke","book_ids":[null]}`},
			probe{"null among book ids", `{"mode":"smoke","book_ids":[1,null]}`},
			probe{"null mode", `{"mode":null,"book_ids":[1]}`},
			probe{"null archive next to book ids", `{"mode":"smoke","book_ids":[1],"archive":null}`},
			probe{"null archive alone", `{"mode":"pilot_archive","archive":null}`},
			probe{"null book ids on full", `{"mode":"full","book_ids":null}`},
			probe{"mode of the wrong type", `{"mode":5}`},
			probe{"archive of the wrong type", `{"mode":"smoke","archive":["a.zip"]}`},
			probe{"book ids of the wrong type", `{"mode":"smoke","book_ids":"1"}`},
			probe{"field name in another case", `{"MODE":"smoke","book_ids":[1]}`},
			probe{"repeated field", `{"mode":"full","mode":"smoke","book_ids":[1]}`},
		)},
		{runsBase + "/42/retry", retryOK, append(shared(retryOK),
			probe{"null stage", `{"stage":null,"error_class":"normalizer_failed"}`},
			probe{"null error class", `{"stage":"local","error_class":null}`},
			probe{"stage of the wrong type", `{"stage":1,"error_class":"normalizer_failed"}`},
			probe{"field name in another case", `{"Stage":"local","error_class":"normalizer_failed"}`},
			probe{"repeated field", `{"stage":"extraction","stage":"local","error_class":"normalizer_failed"}`},
		)},
	}
	for _, route := range routes {
		for _, p := range route.probes {
			t.Run(route.path+" "+p.name, func(t *testing.T) {
				svc := &fakeRunService{run: sampleRun(time.Now())}
				rec := runsRequest(t, runsRouter(svc), http.MethodPost, route.path, p.body)
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				assert.JSONEq(t, `{"error":"invalid_request"}`, rec.Body.String())
				assert.Empty(t, svc.callList(), "a malformed body never reaches the service")
			})
		}
		t.Run(route.path+" trailing whitespace is still one object", func(t *testing.T) {
			svc := &fakeRunService{run: sampleRun(time.Now())}
			rec := runsRequest(t, runsRouter(svc), http.MethodPost, route.path, " \n"+route.ok+" \n\t")
			assert.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
			assert.Len(t, svc.callList(), 1)
		})
	}
}

// Every timestamp pointer, populated in a non-UTC zone, is rendered in UTC in
// both the run and the report.
func TestAuthorMetadataRunAllTimestampsAreUTC(t *testing.T) {
	now := time.Now()
	zone := time.FixedZone("UTC-5", -5*60*60)
	run := sampleRun(now)
	extractionDone := now.Add(-30 * time.Minute).In(zone)
	completed := now.Add(-10 * time.Minute).In(zone)
	run.ExtractionCompletedAt, run.CompletedAt = &extractionDone, &completed
	svc := &fakeRunService{run: run, report: AuthorMetadataRunReport{AuthorMetadataRunView: run}}
	want := map[string]string{
		"created_at":              now.Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
		"started_at":              now.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"extraction_completed_at": extractionDone.UTC().Format(time.RFC3339Nano),
		"completed_at":            completed.UTC().Format(time.RFC3339Nano),
	}
	for path, key := range map[string]string{runsBase + "/42": "run", runsBase + "/42/report": "report"} {
		rec := runsRequest(t, runsRouter(svc), http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		for field, stamp := range want {
			assert.Equal(t, stamp, body[key][field], path+" "+field)
		}
	}
}

// checkRequestObject is the structural half of decodeStrict and holds the
// whole "exactly one object" rule by itself — not only together with the
// typed decode after it.
func TestCheckRequestObject(t *testing.T) {
	fields := []string{"mode", "book_ids"}
	refused := []string{
		`{"mode":"smoke"}}`, `{"mode":"smoke"}]`, `{"mode":"smoke"}{"mode":"smoke"}`, `{"mode":"smoke"} x`,
		`{"mode":"smoke"},`, `null`, `[{"mode":"smoke"}]`, `"x"`, `1`, `true`, ``, `   `, `{`, `{"mode":"smoke"`,
		`{"mode":null}`, `{"book_ids":[1,null]}`, `{"MODE":"smoke"}`, `{"mode":"a","mode":"b"}`, `{"other":1}`,
	}
	for _, body := range refused {
		assert.ErrorIs(t, checkRequestObject([]byte(body), fields), runRequestError{codeInvalidRequest}, "%q", body)
	}
	for _, body := range []string{`{}`, ` {"mode":"smoke","book_ids":[1,2]} ` + "\n", `{"mode":5}`, `{"book_ids":"x"}`} {
		assert.NoError(t, checkRequestObject([]byte(body), fields), "%q (types are the typed decode's job)", body)
	}
}

// The strict decoder's field set is exactly the request structs' json tags.
func TestRunRequestFieldNames(t *testing.T) {
	assert.Equal(t, []string{"mode", "book_ids", "archive"}, jsonFieldNames(&startRunRequest{}))
	assert.Equal(t, []string{"stage", "error_class"}, jsonFieldNames(&retryRunRequest{}))
}

// The handlers keep no list of their own: the accepted classes are exactly
// the run admin's published lists, built from the pipeline's constants.
func TestRetryErrorClassesAreThePublishedLists(t *testing.T) {
	for stage, published := range map[string][]services.AuthorMetadataErrorClass{
		retryStageExtraction: services.AuthorMetadataExtractionRetryClasses(),
		retryStageLocal:      services.AuthorMetadataLocalRetryClasses(),
	} {
		want := map[string]bool{}
		for _, class := range published {
			want[string(class)] = true
		}
		assert.Equal(t, want, retryErrorClasses[stage], stage)
	}
}

// Every error the services run admin returns maps to its closed code; the
// cause stays wrapped and never reaches the response.
func TestRunServiceErrorsMapToContractCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{database.ErrRunNotFound, http.StatusNotFound, codeRunNotFound},
		{database.ErrActiveRunExists, http.StatusConflict, codeActiveRunExists},
		{services.ErrFullRunNotApproved, http.StatusConflict, codeFullRunNotApproved},
		{database.ErrRunTransitionConflict, http.StatusConflict, codeInvalidTransition},
		{database.ErrNotACompletedPilot, http.StatusConflict, codeNotACompletedPilot},
		{database.ErrPilotAlreadyApproved, http.StatusConflict, codeAlreadyApproved},
		{database.ErrInvalidRunSelector, http.StatusBadRequest, codeInvalidSelector},
		{database.ErrInvalidRunBookIDs, http.StatusBadRequest, codeInvalidBookID},
		{services.ErrInvalidRetryClass, http.StatusBadRequest, codeInvalidErrorClass},
		{services.ErrRunVersionMismatch, http.StatusInternalServerError, codeInternalError},
		{errors.New("ERROR #57014 canceling statement"), http.StatusInternalServerError, codeInternalError},
	}
	for _, c := range cases {
		t.Run(c.code+" "+c.err.Error(), func(t *testing.T) {
			wrapped := fmt.Errorf("deep in the service: %w", c.err)
			adapted := adaptRunError(wrapped)
			require.ErrorIs(t, adapted, c.err, "the cause stays wrapped")
			status, code := runErrorStatus(adapted)
			assert.Equal(t, c.status, status)
			assert.Equal(t, c.code, code)

			rec := runsRequest(t, runsRouter(&fakeRunService{err: adapted}), http.MethodGet, runsBase+"/42", "")
			assert.Equal(t, c.status, rec.Code)
			assert.JSONEq(t, `{"error":"`+c.code+`"}`, rec.Body.String())
		})
	}
}
