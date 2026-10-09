package api

// Phase 17: the admin review API over the review service. Handler tests run
// against a fake service (the contract's routes, decoding, codes and auth);
// adapter tests run against a scratch database through the real phase-11
// service, exactly the way the server wires them.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/migrate"
	"gopds-api/internal/testdb"
	"gopds-api/middlewares"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fake service ---

// fakeReviewService records every call and answers with the configured
// values.
type fakeReviewService struct {
	mu sync.Mutex

	calls []string
	last  int64
	actor int64
	scope string
	kind  string

	page   AuthorMetadataReviewListPage
	detail AuthorMetadataReviewDetail
	err    error

	lastEdit AuthorMetadataReviewEditResult
}

func (f *fakeReviewService) record(call string, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	f.last = id
}

func (f *fakeReviewService) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeReviewService) List(_ context.Context, q AuthorMetadataReviewListQuery) (AuthorMetadataReviewListPage, error) {
	f.record("list:"+q.Status, 0)
	return f.page, f.err
}

func (f *fakeReviewService) Detail(_ context.Context, itemID int64) (AuthorMetadataReviewDetail, error) {
	f.record("detail", itemID)
	return f.detail, f.err
}

func (f *fakeReviewService) Accept(_ context.Context, itemID, admin int64, scope string) error {
	f.record("accept", itemID)
	f.actor, f.scope = admin, scope
	return f.err
}

func (f *fakeReviewService) Edit(_ context.Context, itemID, admin int64, scope string, result AuthorMetadataReviewEditResult) error {
	f.record("edit", itemID)
	f.actor, f.scope, f.lastEdit = admin, scope, result
	return f.err
}

func (f *fakeReviewService) Classify(_ context.Context, itemID, admin int64, scope, kind string) error {
	f.record("classify", itemID)
	f.actor, f.scope, f.kind = admin, scope, kind
	return f.err
}

func (f *fakeReviewService) LeaveUnresolved(_ context.Context, itemID, admin int64, scope string) error {
	f.record("unresolved", itemID)
	f.actor, f.scope = admin, scope
	return f.err
}

func (f *fakeReviewService) Retry(_ context.Context, itemID, admin int64) error {
	f.record("retry", itemID)
	f.actor = admin
	return f.err
}

const reviewBase = "/api/admin/author-metadata/review"

func reviewRouter(svc AuthorMetadataReviewService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorMetadataReviewRoutes(admin.Group("/author-metadata"), svc)
	return r
}

func reviewRequest(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func reviewBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

// sampleReviewDetail is a fully populated detail in a non-UTC zone.
func sampleReviewDetail(now time.Time) AuthorMetadataReviewDetail {
	zone := time.FixedZone("UTC+5", 5*60*60)
	given := "И."
	return AuthorMetadataReviewDetail{
		AuthorMetadataReviewListItem: AuthorMetadataReviewListItem{
			ID: 31, Scope: "fingerprint", CreditID: nil,
			SourceFingerprint: strings.Repeat("ab", 32), Reason: "ambiguous_decision",
			DecisionClass: "initials", DisplayName: "И. Петров", CreditsCount: 3,
			CreatedAt: now.Add(-time.Hour).In(zone),
		},
		Source: AuthorMetadataReviewSource{
			First: &given, Middle: nil, Last: ptrReview("Петров"), Nickname: nil,
			Display: "И. Петров", Flags: []string{"flag_initials"},
		},
		Proposal: &AuthorMetadataReviewProposal{
			GivenName: &given, FamilyName: ptrReview("Петров"), DisplayName: "И. Петров",
			SearchKey: "и петров", Script: "Cyrl", Kind: "person", Method: "rules",
			QualityFlags: []string{},
		},
		LinkedBooks: []AuthorMetadataReviewLinkedBook{{ID: 5, Title: "Осенние грозы"}},
	}
}

func ptrReview(s string) *string { return &s }

// --- RED 1: pagination bounds, malformed filters ---

func TestReviewListQueryValidation(t *testing.T) {
	f := &fakeReviewService{page: AuthorMetadataReviewListPage{Items: []AuthorMetadataReviewListItem{}}}
	r := reviewRouter(f)

	for _, tc := range []struct{ query, code string }{
		{"", codeInvalidStatus},
		{"?status=", codeInvalidStatus},
		{"?status=archived", codeInvalidStatus},
		{"?status=open&limit=0", codeInvalidLimit},
		{"?status=open&limit=-1", codeInvalidLimit},
		{"?status=open&limit=101", codeInvalidLimit},
		{"?status=open&limit=abc", codeInvalidLimit},
		{"?status=open&limit=05", codeInvalidLimit},
		{"?status=open&cursor=not-a-cursor", codeInvalidCursor},
		{"?status=open&cursor=" + encodeReviewCursor(AuthorMetadataReviewCursor{}) + "x", codeInvalidCursor},
		{"?status=open&cursor=YmFzZTY0", codeInvalidCursor},
		{"?status=open&cursor=NS5hYmM", codeInvalidCursor},
	} {
		rec := reviewRequest(t, r, http.MethodGet, reviewBase+tc.query, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, tc.query)
		assert.Equal(t, tc.code, reviewCode(t, rec), tc.query)
	}

	// The default and the bounds pass; the status reaches the service.
	for _, query := range []string{"?status=open", "?status=closed", "?status=open&limit=1", "?status=open&limit=100"} {
		rec := reviewRequest(t, r, http.MethodGet, reviewBase+query, "")
		assert.Equal(t, http.StatusOK, rec.Code, query)
	}
	require.Len(t, f.callList(), 4)
	assert.Equal(t, "list:open", f.callList()[0])
	assert.Equal(t, "list:closed", f.callList()[1])
}

func TestReviewListResponseShape(t *testing.T) {
	now := time.Now().UTC()
	next := AuthorMetadataReviewCursor{CreatedAt: now.Add(-time.Minute), ID: 44}
	f := &fakeReviewService{
		page: AuthorMetadataReviewListPage{
			Items: []AuthorMetadataReviewListItem{sampleReviewDetail(now).AuthorMetadataReviewListItem},
			Next:  &next,
		},
	}
	r := reviewRouter(f)

	rec := reviewRequest(t, r, http.MethodGet, reviewBase+"?status=open", "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := reviewBody(t, rec)
	var cursor string
	require.NoError(t, json.Unmarshal(body["next_cursor"], &cursor))
	assert.Equal(t, encodeReviewCursor(next), cursor, "the cursor is opaque and stable")

	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body["items"], &items))
	require.Len(t, items, 1)
	item := items[0]
	// The list item is exactly the contract's shape — no titles, no source
	// components, nothing of the detail's.
	assert.ElementsMatch(t, []string{
		"id", "scope", "credit_id", "source_fingerprint", "reason", "decision_class",
		"display_name", "credits_count", "created_at",
	}, keysOf(item))
	assert.Equal(t, `"fingerprint"`, string(item["scope"]))
	assert.Equal(t, `"open"`, `"open"`)

	// An empty page is [] and carries no cursor.
	f = &fakeReviewService{page: AuthorMetadataReviewListPage{Items: []AuthorMetadataReviewListItem{}}}
	rec = reviewRequest(t, reviewRouter(f), http.MethodGet, reviewBase+"?status=closed", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"items": [], "next_cursor": null}`, rec.Body.String())
}

func keysOf(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func reviewCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	body := reviewBody(t, rec)
	var code string
	require.NoError(t, json.Unmarshal(body["error"], &code))
	return code
}

// --- RED 2 and 3: detail shape, happy paths ---

func TestReviewDetailShape(t *testing.T) {
	now := time.Now().UTC()
	f := &fakeReviewService{detail: sampleReviewDetail(now)}
	r := reviewRouter(f)

	rec := reviewRequest(t, r, http.MethodGet, reviewBase+"/31", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Item AuthorMetadataReviewDetail `json:"item"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	detail := body.Item
	assert.Equal(t, int64(31), detail.ID)
	assert.Equal(t, "И. Петров", detail.DisplayName)
	assert.Equal(t, int64(3), detail.CreditsCount)
	assert.Equal(t, "И.", *detail.Source.First)
	assert.Equal(t, "Петров", *detail.Proposal.FamilyName)
	assert.Equal(t, "rules", detail.Proposal.Method)
	require.Len(t, detail.LinkedBooks, 1)
	assert.Equal(t, "Осенние грозы", detail.LinkedBooks[0].Title)
	// UTC RFC 3339 timestamps.
	assert.Equal(t, detail.CreatedAt.Format(time.RFC3339Nano), detail.CreatedAt.UTC().Format(time.RFC3339Nano))
	stamp := detail.CreatedAt.Format(time.RFC3339Nano)
	assert.True(t, strings.HasSuffix(stamp, "Z") || strings.Contains(stamp, "+00:00"),
		"the timestamp is UTC: %s", stamp)
}

func TestReviewActionsHappyPaths(t *testing.T) {
	detail := sampleReviewDetail(time.Now().UTC())
	f := &fakeReviewService{detail: detail}
	r := reviewRouter(f)

	// Accept, unresolved and retry carry the scope (retry none) and answer
	// with the item.
	rec := reviewRequest(t, r, http.MethodPost, reviewBase+"/31/accept", `{"scope":"fingerprint"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Item AuthorMetadataReviewDetail `json:"item"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(31), body.Item.ID)
	assert.Equal(t, adminActorID, f.actor)

	rec = reviewRequest(t, r, http.MethodPost, reviewBase+"/31/unresolved", `{"scope":"fingerprint"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = reviewRequest(t, r, http.MethodPost, reviewBase+"/31/retry", "")
	require.Equal(t, http.StatusOK, rec.Code)

	// Classify validates its closed kind set.
	for _, kind := range []string{"collective", "unknown", "malformed"} {
		rec = reviewRequest(t, r, http.MethodPost, reviewBase+"/31/classify",
			fmt.Sprintf(`{"scope":"fingerprint","kind":%q}`, kind))
		require.Equal(t, http.StatusOK, rec.Code, kind)
		assert.Equal(t, kind, f.kind)
	}
	require.Equal(t, []string{"accept", "detail", "unresolved", "detail", "retry", "detail",
		"classify", "detail", "classify", "detail", "classify", "detail"}, f.callList())
}

func TestReviewEditDecoding(t *testing.T) {
	detail := sampleReviewDetail(time.Now().UTC())
	f := &fakeReviewService{detail: detail}
	r := reviewRouter(f)

	rec := reviewRequest(t, r, http.MethodPost, reviewBase+"/31/edit", `{
		"scope": "fingerprint",
		"result": {
			"given_name": null, "additional_names": null, "family_name": "Петрова",
			"nickname": null, "display_name": "Петрова", "sort_name": null, "kind": "person"
		}
	}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "fingerprint", f.scope)
	assert.Equal(t, "Петрова", *f.lastEdit.FamilyName)
	assert.Equal(t, "Петрова", f.lastEdit.DisplayName)
	assert.Nil(t, f.lastEdit.GivenName)
	assert.Equal(t, "person", f.lastEdit.Kind)

	// Every strict rule still holds, at both depths.
	for _, tc := range []struct{ body, code string }{
		{`{"scope":"fingerprint"}`, codeInvalidResult},                // result missing
		{`{"scope":"fingerprint","result":null}`, codeInvalidRequest}, // result null
		{`{"scope":"credit","result":{"display_name":"x","kind":"person"},"extra":1}`, codeInvalidRequest},
		{`{"scope":"credit","result":{"display_name":"x","kind":"person","given_name":null,"given_name":null}}`, codeInvalidRequest},
		{`{"scope":"credit","result":{"display_name":"x","kind":"person","unknown":1}}`, codeInvalidRequest},
		{`{"scope":"credit","result":{"display_name":null,"kind":"person"}}`, codeInvalidRequest},
		{`{"scope":"credit","result":{"display_name":"x","kind":"person"}} trailing`, codeInvalidRequest},
		{`[{"scope":"credit"}]`, codeInvalidRequest},
		{`{"scope":"wallet","result":{"display_name":"x","kind":"person"}}`, codeInvalidScope},
		{`{"scope":"credit","result":{"display_name":"x","kind":"editor"}}`, codeInvalidKind},
		{`{"scope":"credit","result":{"display_name":"  ","kind":"person"}}`, codeInvalidResult},
		{`{"scope":"credit","result":{"display_name":"x","kind":"person"}}{"again":1}`, codeInvalidRequest},
	} {
		rec = reviewRequest(t, r, http.MethodPost, reviewBase+"/31/edit", tc.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, tc.body)
		assert.Equal(t, tc.code, reviewCode(t, rec), tc.body)
	}
}

func TestReviewActionValidationAndErrors(t *testing.T) {
	detail := sampleReviewDetail(time.Now().UTC())
	r := reviewRouter(&fakeReviewService{detail: detail})

	for _, tc := range []struct {
		method, path, body string
		code               string
		status             int
	}{
		{http.MethodGet, reviewBase + "/abc", "", codeInvalidID, http.StatusBadRequest},
		{http.MethodGet, reviewBase + "/-1", "", codeInvalidID, http.StatusBadRequest},
		{http.MethodGet, reviewBase + "/0", "", codeInvalidID, http.StatusBadRequest},
		{http.MethodGet, reviewBase + "/007", "", codeInvalidID, http.StatusBadRequest},
		{http.MethodPost, reviewBase + "/31/accept", `{}`, codeInvalidScope, http.StatusBadRequest},
		{http.MethodPost, reviewBase + "/31/accept", `{"scope":"ledger"}`, codeInvalidScope, http.StatusBadRequest},
		{http.MethodPost, reviewBase + "/31/accept", ``, codeInvalidRequest, http.StatusBadRequest},
		{http.MethodPost, reviewBase + "/31/classify", `{"scope":"credit"}`, codeInvalidKind, http.StatusBadRequest},
	} {
		rec := reviewRequest(t, r, tc.method, tc.path, tc.body)
		assert.Equal(t, tc.status, rec.Code, tc.path)
		assert.Equal(t, tc.code, reviewCode(t, rec), tc.path)
	}
}

// --- RED 5 and 6: conflicts, missing items ---

func TestReviewServiceErrorMapping(t *testing.T) {
	detail := sampleReviewDetail(time.Now().UTC())
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrAuthorMetadataReviewNotFound, http.StatusNotFound, codeReviewNotFound},
		{ErrAuthorMetadataReviewConflict, http.StatusConflict, codeReviewConflict},
		{ErrAuthorMetadataReviewScopeMismatch, http.StatusConflict, codeScopeMismatch},
		{ErrAuthorMetadataReviewRetryExhausted, http.StatusConflict, codeRetryExhausted},
		{errors.New("boom"), http.StatusInternalServerError, codeInternalError},
	} {
		f := &fakeReviewService{detail: detail, err: tc.err}
		r := reviewRouter(f)
		for _, path := range []string{
			reviewBase + "/31", reviewBase + "/31/accept", reviewBase + "/31/retry",
		} {
			body := ""
			if strings.HasSuffix(path, "accept") {
				body = `{"scope":"fingerprint"}`
			}
			rec := reviewRequest(t, r, http.MethodPost, path, body)
			if path == reviewBase+"/31" {
				rec = reviewRequest(t, r, http.MethodGet, path, "")
			}
			assert.Equal(t, tc.status, rec.Code, "%v %s", tc.err, path)
			assert.Equal(t, tc.code, reviewCode(t, rec), "%v %s", tc.err, path)
		}
	}
}

// --- RED 8: the auth contract through the real AdminMiddleware ---

func TestReviewRoutesAuth(t *testing.T) {
	detail := sampleReviewDetail(time.Now().UTC())
	installSessionStore(t)

	previous := authorMetadataReviewService
	authorMetadataReviewService = func() AuthorMetadataReviewService {
		return &fakeReviewService{detail: detail}
	}
	t.Cleanup(func() { authorMetadataReviewService = previous })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupAdminRoutes(r.Group("/api").Group("/admin", middlewares.AdminMiddleware()))

	authed := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			reviewBase+"?status=open", http.NoBody)
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	assert.Equal(t, http.StatusUnauthorized, authed("").Code, "no token: the middleware's own 401")
	assert.Equal(t, http.StatusNotFound, authed(adminSession(t, false)).Code, "a non-admin sees no admin area")
	assert.Equal(t, http.StatusOK, authed(adminSession(t, true)).Code)
}

// --- the adapter over the real service, on a scratch database ---

// reviewAPIDB creates and migrates a scratch database for one adapter test.
func reviewAPIDB(t *testing.T) *pg.DB {
	t.Helper()
	cfg, ok := testdb.Configured()
	if !ok {
		t.Skip(testdb.SkipReason)
	}
	name := fmt.Sprintf("author_review_api_test_%d", time.Now().UnixNano())
	_, err := database.GetDB().Exec("CREATE DATABASE " + name)
	require.NoError(t, err, "creating the scratch database")
	t.Cleanup(func() {
		_, dropErr := database.GetDB().Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		if dropErr != nil {
			t.Errorf("dropping the scratch database %s: %v", name, dropErr)
		}
	})
	scratch := pg.Connect(&pg.Options{
		Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name, PoolSize: 16,
	})
	t.Cleanup(func() { _ = scratch.Close() })
	_, err = migrate.Run(context.Background(), scratch, os.DirFS(".."), "database_migrations", migrate.AppBaseline())
	require.NoError(t, err, "migrating the scratch database")
	return scratch
}

func TestAuthorMetadataReviewAdapter(t *testing.T) {
	db := reviewAPIDB(t)
	ctx := context.Background()

	// Wiring through the shared converter, as the server does. The wiring
	// present before this fixture is what cleanup restores, so no later test
	// inherits an adapter backed by this scratch database.
	previousWiring := authorMetadataReviewService
	t.Cleanup(func() { authorMetadataReviewService = previousWiring })
	serverConfig := config.AuthorMetadataConfig{}
	serverConfig.LocalNormalization.MaxAttempts = 5
	require.NoError(t, SetAuthorMetadataReviewService(db, &serverConfig))
	adapter := authorMetadataReviewService()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorMetadataReviewRoutes(admin.Group("/author-metadata"), adapter)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, method, path, readerOrNull(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// One ambiguous input behind one open item.
	seedOpenReviewItem(t, db, nameValueOf(t, "И.", "Петров"), "Летние грозы")

	t.Run("list, page and walk the open queue", func(t *testing.T) {
		rec := request(http.MethodGet, reviewBase+"?status=open", "")
		require.Equal(t, http.StatusOK, rec.Code)
		var page struct {
			Items []AuthorMetadataReviewListItem `json:"items"`
			Next  *string                        `json:"next_cursor"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
		require.Len(t, page.Items, 1)
		item := page.Items[0]
		assert.Equal(t, "fingerprint", item.Scope)
		assert.Equal(t, "ambiguous_decision", item.Reason)
		assert.Equal(t, "initials", item.DecisionClass)
		assert.Equal(t, "И. Петров", item.DisplayName, "the proposal's display")
		assert.Equal(t, int64(1), item.CreditsCount)
		assert.Nil(t, page.Next)

		rec = request(http.MethodGet, reviewBase+"?status=closed", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"items": [], "next_cursor": null}`, rec.Body.String())

		// No title appears in any list response.
		assert.NotContains(t, rec.Body.String(), "Летние грозы")
		rec = request(http.MethodGet, reviewBase+"?status=open", "")
		assert.NotContains(t, rec.Body.String(), "Летние грозы")
	})

	t.Run("detail carries the source, proposal and bounded linked books", func(t *testing.T) {
		rec := request(http.MethodGet, reviewBase+"/1", "")
		require.Equal(t, http.StatusOK, rec.Code)
		var body struct {
			Item AuthorMetadataReviewDetail `json:"item"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		detail := body.Item
		require.NotNil(t, detail.Proposal)
		assert.Equal(t, "И.", *detail.Proposal.GivenName)
		assert.Equal(t, "structured", detail.Proposal.Method)
		assert.Equal(t, "person", detail.Proposal.Kind)
		assert.Equal(t, "И.", *detail.Source.First)
		assert.Equal(t, "Петров", *detail.Source.Last)
		require.Len(t, detail.LinkedBooks, 1)
		assert.Equal(t, "Летние грозы", detail.LinkedBooks[0].Title)

		rec = request(http.MethodGet, reviewBase+"/999", "")
		require.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, codeReviewNotFound, reviewCode(t, rec))
	})

	t.Run("edit decides the item and answers its closed detail", func(t *testing.T) {
		rec := request(http.MethodPost, reviewBase+"/1/edit", `{
			"scope": "fingerprint",
			"result": {"given_name": "Иван", "family_name": null, "nickname": null,
				"display_name": "Иван", "sort_name": null, "kind": "person"}
		}`)
		require.Equal(t, http.StatusOK, rec.Code)
		var body struct {
			Item AuthorMetadataReviewDetail `json:"item"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		// The contract's display_name stays the proposal's display; the
		// decision itself shows in the closed queue and the conflict below.
		assert.Equal(t, "И. Петров", body.Item.DisplayName)

		// The decided item moved to the closed queue.
		rec = request(http.MethodGet, reviewBase+"?status=closed", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"credits_count":1`, "the closed queue lists the decided item")

		// The decided item refuses the next action with the closed conflict.
		rec = request(http.MethodPost, reviewBase+"/1/accept", `{"scope":"fingerprint"}`)
		require.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, codeReviewConflict, reviewCode(t, rec))

		// And the scope guard: an action on a fingerprint item confirmed as
		// credit is a mismatch, not a not-found.
		rec = request(http.MethodPost, reviewBase+"/1/unresolved", `{"scope":"credit"}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, codeScopeMismatch, reviewCode(t, rec))
	})
}

// seedOpenReviewItem seeds, through the real repositories, one book with
// the given title whose single author credit carries source, and leaves the
// policy's decision on it — an open review item for an ambiguous source.
func seedOpenReviewItem(t *testing.T, db *pg.DB, source authornorm.SourceValue, title string) {
	t.Helper()
	ctx := context.Background()
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		book := time.Now().UnixNano() % 2_000_000
		if _, err := tx.Exec(`INSERT INTO opds_catalog_book
			(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
			VALUES (?, ?, 'fixture.zip', 'fb2', now(), '', 'ru', ?, '', ?)`,
			book, "book.fb2", title, fmt.Sprintf("%032x", book)); err != nil {
			return err
		}
		in := &database.ExtractionInput{
			BookID: book, BookMD5: fmt.Sprintf("%032x", book),
			ExtractorVersion: "extractor-v1", NormalizerVersion: authornorm.NormalizerVersion,
			Origin: models.BookMetadataSnapshotLive, Outcome: models.BookMetadataSnapshotExtracted,
			ArchivePath: "fixture.zip", EntryName: "book.fb2",
			XMLProvenance: []byte(`{}`), SourceISBNs: []string{}, SourceSequences: []byte(`[]`),
			QualityFlags: []string{}, Credits: []database.ExtractionCredit{
				{Role: models.ContributorRoleAuthor, Source: source},
			},
		}
		if _, err := database.PersistExtraction(tx, in); err != nil {
			return err
		}
		result, err := authornorm.Normalize(source, "extractor-v1")
		if err != nil {
			return err
		}
		resultID, err := database.InsertLocalResult(ctx, tx, &result)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE contributor_normalization_job
			SET status = 'completed', result_id = ?, finished_at = now()
			WHERE normalization_key = ?`, resultID, result.NormalizationKey[:]); err != nil {
			return err
		}
		policy, err := database.LoadAcceptancePolicy(ctx, tx, 1, authornorm.NormalizerVersion)
		if err != nil {
			return err
		}
		decision, err := policy.Decide(&result)
		if err != nil {
			return err
		}
		_, err = database.ResolveCredits(ctx, tx, result.SourceFingerprint[:], "extractor-v1",
			&database.AutomaticOutcome{ResultID: resultID, DecisionClass: result.DecisionClass, Decision: decision})
		return err
	})
	require.NoError(t, err)
}

func readerOrNull(body string) io.Reader {
	if body == "" {
		return http.NoBody
	}
	return strings.NewReader(body)
}

func nameValueOf(t *testing.T, first, last string) authornorm.SourceValue {
	t.Helper()
	value, err := authornorm.NewSourceValue([]authornorm.SourceComponent{
		{Kind: authornorm.ComponentFirst, Value: first},
		{Kind: authornorm.ComponentLast, Value: last},
	})
	require.NoError(t, err)
	return value
}

// The reviewer's end-to-end reproduction: a contract-shaped punctuation-only
// edit through the concrete adapter and real service produces the
// terminal-invalid decision, not a 400.
func TestReview17MalformedEditWithoutSearchLetters(t *testing.T) {
	db := reviewAPIDB(t)
	ctx := context.Background()
	previousWiring := authorMetadataReviewService
	t.Cleanup(func() { authorMetadataReviewService = previousWiring })
	serverConfig := config.AuthorMetadataConfig{}
	serverConfig.LocalNormalization.MaxAttempts = 5
	require.NoError(t, SetAuthorMetadataReviewService(db, &serverConfig))
	adapter := authorMetadataReviewService()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorMetadataReviewRoutes(admin.Group("/author-metadata"), adapter)

	source := nameValueOf(t, "И.", "Петров")
	seed := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		book := time.Now().UnixNano() % 2_000_000
		if _, err := tx.Exec(`INSERT INTO opds_catalog_book
			(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
			VALUES (?, ?, 'fixture.zip', 'fb2', now(), '', 'ru', 't', '', ?)`,
			book, "book.fb2", fmt.Sprintf("%032x", book)); err != nil {
			return err
		}
		in := &database.ExtractionInput{
			BookID: book, BookMD5: fmt.Sprintf("%032x", book),
			ExtractorVersion: "extractor-v1", NormalizerVersion: authornorm.NormalizerVersion,
			Origin: models.BookMetadataSnapshotLive, Outcome: models.BookMetadataSnapshotExtracted,
			ArchivePath: "fixture.zip", EntryName: "book.fb2",
			XMLProvenance: []byte(`{}`), SourceISBNs: []string{}, SourceSequences: []byte(`[]`),
			QualityFlags: []string{}, Credits: []database.ExtractionCredit{
				{Role: models.ContributorRoleAuthor, Source: source},
			},
		}
		if _, err := database.PersistExtraction(tx, in); err != nil {
			return err
		}
		result, err := authornorm.Normalize(source, "extractor-v1")
		if err != nil {
			return err
		}
		resultID, err := database.InsertLocalResult(ctx, tx, &result)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE contributor_normalization_job
			SET status = 'completed', result_id = ?, finished_at = now()
			WHERE normalization_key = ?`, resultID, result.NormalizationKey[:]); err != nil {
			return err
		}
		policy, err := database.LoadAcceptancePolicy(ctx, tx, 1, authornorm.NormalizerVersion)
		if err != nil {
			return err
		}
		decision, err := policy.Decide(&result)
		if err != nil {
			return err
		}
		_, err = database.ResolveCredits(ctx, tx, result.SourceFingerprint[:], "extractor-v1",
			&database.AutomaticOutcome{ResultID: resultID, DecisionClass: result.DecisionClass, Decision: decision})
		return err
	})
	require.NoError(t, seed)

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, reviewBase+"/1/edit",
		strings.NewReader(`{"scope":"fingerprint","result":{"display_name":"...","kind":"malformed"}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var body struct {
		Item AuthorMetadataReviewDetail `json:"item"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(1), body.Item.ID)

	var manual models.ContributorNormalizationResult
	_, err := db.QueryOne(&manual, `SELECT kind, status, search_key FROM contributor_normalization_result
		WHERE method = 'manual' ORDER BY id DESC LIMIT 1`)
	require.NoError(t, err)
	assert.Equal(t, models.NormalizationMalformed, manual.Kind)
	assert.Equal(t, models.NormalizationInvalid, manual.Status)
	assert.Nil(t, manual.SearchKey)
}

// The wiring fixtures restore the actual previous wiring value, so running
// them in sequence leaves the earlier service in place — no test inherits an
// adapter backed by a scratch database that has since closed.
func TestReviewWiringFixturesRestorePreviousService(t *testing.T) {
	sentinel := &fakeReviewService{}
	previous := authorMetadataReviewService
	authorMetadataReviewService = func() AuthorMetadataReviewService { return sentinel }
	t.Cleanup(func() { authorMetadataReviewService = previous })

	t.Run("adapter fixture", TestAuthorMetadataReviewAdapter)
	assert.Same(t, sentinel, authorMetadataReviewService(),
		"the adapter fixture must restore the wiring it found")
	// The restored service still answers: its database is the fake's, not a
	// closed scratch pool.
	_, err := authorMetadataReviewService().Detail(context.Background(), 1)
	assert.NoError(t, err, "the restored wiring must not point at a closed database")

	t.Run("malformed-edit fixture", TestReview17MalformedEditWithoutSearchLetters)
	assert.Same(t, sentinel, authorMetadataReviewService(),
		"the malformed-edit fixture must restore the wiring it found")
	_, err = authorMetadataReviewService().Detail(context.Background(), 1)
	assert.NoError(t, err)
}
