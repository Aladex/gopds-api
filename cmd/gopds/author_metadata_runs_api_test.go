package main

// Phase 16 Part 2: the admin runs API over the real run service, mounted the
// way the server mounts it (newAuthorMetadataRunsAPI) on a scratch database,
// with the real workers draining real archives. Covers the plan's transition
// REDs — pause/resume (4), approve-full (8), full needs an approved matching
// pilot (9) — the stage numbers of an actual small run, and retry.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopds-api/api"
	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runsAPIBase = "/api/admin/author-metadata/runs"

// runsAPIFixture is one scratch catalog with the fixture archives on disk, an
// administrator, and the runs routes the server would mount.
type runsAPIFixture struct {
	t      *testing.T
	db     *pg.DB
	dir    string
	cfg    config.AuthorMetadataConfig
	actor  int64
	router *gin.Engine
}

func newRunsAPIFixture(t *testing.T, mod func(*config.AuthorMetadataConfig)) *runsAPIFixture {
	t.Helper()
	f := &runsAPIFixture{t: t, db: scratchDB(t), dir: t.TempDir(), cfg: wiringConfig()}
	if mod != nil {
		mod(&f.cfg)
	}
	for _, archive := range []string{"happy.zip", "errors.zip"} {
		payload, err := os.ReadFile(filepath.Join("..", "..", "services", "testdata", "author_metadata", archive))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(f.dir, archive), payload, 0o600))
	}
	_, err := f.db.QueryOne(pg.Scan(&f.actor), `INSERT INTO auth_user
		(password, is_superuser, username, email, date_joined)
		VALUES ('', true, 'runs-admin', 'runs-admin@fixture.local', now()) RETURNING id`)
	require.NoError(t, err)

	svc, err := newAuthorMetadataRunsAPI(f.db, &f.cfg)
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	f.router = gin.New()
	admin := f.router.Group("/api/admin", func(c *gin.Context) {
		c.Set("user_id", f.actor)
		c.Next()
	})
	api.SetupAuthorMetadataRunRoutes(admin.Group("/author-metadata"), svc)
	return f
}

// book adds one catalog book stored as entry in archive.
func (f *runsAPIFixture) book(archive, entry string) int64 {
	f.t.Helper()
	var id int64
	_, err := f.db.QueryOne(pg.Scan(&id), `INSERT INTO opds_catalog_book
		(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES (?, ?, 'fb2', now(), '', 'ru', 'fixture', '', '') RETURNING id`, entry, archive)
	require.NoError(f.t, err)
	return id
}

// archiveBooks adds every entry of the two fixture archives.
func (f *runsAPIFixture) archiveBooks() map[string]int64 {
	f.t.Helper()
	books := map[string]int64{}
	for _, entry := range []string{"multi_contributor.fb2", "translator_only.fb2"} {
		books["happy.zip/"+entry] = f.book("happy.zip", entry)
	}
	for _, entry := range []string{"multi_contributor.fb2", "foreign_root.fb2", "malformed.fb2",
		"enc_unsupported_declared.fb2"} {
		books["errors.zip/"+entry] = f.book("errors.zip", entry)
	}
	return books
}

// call sends one request and decodes the JSON object it answers.
func (f *runsAPIFixture) call(method, path, body string) (status int, out map[string]any) {
	f.t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	require.NoError(f.t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec.Code, out
}

// start starts a run and returns its ID.
func (f *runsAPIFixture) start(body string) int64 {
	f.t.Helper()
	code, out := f.call(http.MethodPost, runsAPIBase, body)
	require.Equal(f.t, http.StatusCreated, code, "%v", out)
	return int64(field(f.t, out, "run", "id").(float64))
}

// runPath is a run route.
func runPath(id int64, action string) string {
	if action == "" {
		return fmt.Sprintf("%s/%d", runsAPIBase, id)
	}
	return fmt.Sprintf("%s/%d/%s", runsAPIBase, id, action)
}

// field walks nested JSON objects.
func field(t *testing.T, v any, keys ...string) any {
	t.Helper()
	for _, key := range keys {
		object, ok := v.(map[string]any)
		require.True(t, ok, "%v is not an object at %q", v, key)
		v, ok = object[key]
		require.True(t, ok, "no %q in %v", key, object)
	}
	return v
}

func number(t *testing.T, v any, keys ...string) int64 {
	t.Helper()
	n, ok := field(t, v, keys...).(float64)
	require.True(t, ok, "%v is not a number", keys)
	return int64(n)
}

// workers builds the server's workers over the fixture's configuration.
func (f *runsAPIFixture) workers() (*services.AuthorMetadataExtractionLoop, []*services.AuthorMetadataLocalLoop) {
	f.t.Helper()
	built, err := buildAuthorMetadataWorkers(f.db, f.dir, &f.cfg)
	require.NoError(f.t, err)
	var locals []*services.AuthorMetadataLocalLoop
	for _, w := range built[1:] {
		locals = append(locals, w.(*services.AuthorMetadataLocalLoop))
	}
	return built[0].(*services.AuthorMetadataExtractionLoop), locals
}

// extract runs the extraction stream until nothing is left to claim.
func (f *runsAPIFixture) extract() {
	f.t.Helper()
	extraction, _ := f.workers()
	_, err := extraction.RunOnce(context.Background())
	require.NoError(f.t, err)
}

// drain runs both streams until the run completes.
func (f *runsAPIFixture) drain(id int64) {
	f.t.Helper()
	extraction, locals := f.workers()
	ctx := context.Background()
	for range 20 {
		_, err := extraction.RunOnce(ctx)
		require.NoError(f.t, err)
		for _, local := range locals {
			_, err = local.RunOnce(ctx)
			require.NoError(f.t, err)
		}
		if f.status(id) == string(models.AuthorMetadataRunCompleted) {
			return
		}
	}
	f.t.Fatalf("run %d did not complete; status %s", id, f.status(id))
}

func (f *runsAPIFixture) status(id int64) string {
	f.t.Helper()
	var status string
	_, err := f.db.QueryOne(pg.Scan(&status), `SELECT status FROM author_metadata_run WHERE id = ?`, id)
	require.NoError(f.t, err)
	return status
}

func (f *runsAPIFixture) count(query string, params ...interface{}) int {
	f.t.Helper()
	var n int
	_, err := f.db.QueryOne(pg.Scan(&n), query, params...)
	require.NoError(f.t, err)
	return n
}

func smokeBody(ids ...int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return `{"mode":"smoke","book_ids":[` + strings.Join(parts, ",") + `]}`
}

func pilotBody(archive string) string { return `{"mode":"pilot_archive","archive":"` + archive + `"}` }

// assertRunError posts to route and expects the closed error code.
func assertRunError(t *testing.T, f *runsAPIFixture, route, body string, status int, code string) {
	t.Helper()
	got, out := f.call(http.MethodPost, route, body)
	assert.Equal(t, status, got, "POST %s: %v", route, out)
	assert.Equal(t, map[string]any{"error": code}, out, "POST %s", route)
}

// Plan RED 4: pause and resume are the only valid transitions of an active
// run; a repeated or terminal transition is 409, an unknown run 404.
func TestRunsAPIPauseResumeTransitions(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	id := f.start(smokeBody(f.book("happy.zip", "multi_contributor.fb2")))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_metadata_run WHERE id = ? AND created_by_user_id = ?`,
		id, f.actor), "the starting admin is recorded")

	code, out := f.call(http.MethodPost, runPath(id, "pause"), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, "paused", field(t, out, "run", "status"))
	assertRunError(t, f, runPath(id, "pause"), "", http.StatusConflict, "invalid_transition")

	code, out = f.call(http.MethodPost, runPath(id, "resume"), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, "running", field(t, out, "run", "status"))
	assertRunError(t, f, runPath(id, "resume"), "", http.StatusConflict, "invalid_transition")

	for _, action := range []string{"pause", "resume"} {
		assertRunError(t, f, runPath(id+1000, action), "", http.StatusNotFound, "run_not_found")
	}

	f.drain(id)
	for _, action := range []string{"pause", "resume"} {
		assertRunError(t, f, runPath(id, action), "", http.StatusConflict, "invalid_transition")
	}
	assert.Equal(t, "completed", f.status(id), "a refused transition changes nothing")
}

// Plan RED 8: approve-full takes only a completed pilot, records the admin
// and the run's exact versions, and refuses a repeat, a non-pilot and an
// active pilot.
func TestRunsAPIApproveFull(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	books := f.archiveBooks()

	smoke := f.start(smokeBody(books["happy.zip/multi_contributor.fb2"]))
	f.drain(smoke)
	assertRunError(t, f, runPath(smoke, "approve-full"), "", http.StatusConflict, "not_a_completed_pilot")

	pilot := f.start(pilotBody("happy.zip"))
	assertRunError(t, f, runPath(pilot, "approve-full"), "", http.StatusConflict, "not_a_completed_pilot")
	f.drain(pilot)

	code, out := f.call(http.MethodPost, runPath(pilot, "approve-full"), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, true, field(t, out, "run", "approved_for_full"))
	var approval struct {
		RunID             int64
		ApprovedByUserID  int64
		ExtractorVersion  string
		NormalizerVersion string
	}
	_, err := f.db.QueryOne(&approval, `SELECT run_id, approved_by_user_id, extractor_version, normalizer_version
		FROM author_metadata_pilot_approval`)
	require.NoError(t, err)
	assert.Equal(t, pilot, approval.RunID)
	assert.Equal(t, f.actor, approval.ApprovedByUserID, "the approving admin is recorded")
	assert.Equal(t, services.AuthorMetadataExtractorVersion, approval.ExtractorVersion)
	assert.Equal(t, authornorm.NormalizerVersion, approval.NormalizerVersion)

	assertRunError(t, f, runPath(pilot, "approve-full"), "", http.StatusConflict, "already_approved")
	// The approval pins the pilot: a retry cannot move it back to running.
	assertRunError(t, f, runPath(pilot, "retry"), `{"stage":"extraction","error_class":"invalid_fb2"}`,
		http.StatusConflict, "invalid_transition")
	assertRunError(t, f, runPath(pilot+1000, "approve-full"), "", http.StatusNotFound, "run_not_found")

	// A completed pilot that extracted nothing gates nothing.
	var empty int64
	_, err = f.db.QueryOne(pg.Scan(&empty), `INSERT INTO author_metadata_run
		(mode, status, extractor_version, normalizer_version, selector_archive, started_at, extraction_completed_at,
		 finished_at)
		VALUES ('pilot_archive', 'completed', ?, ?, 'empty.zip', now(), now(), now()) RETURNING id`,
		services.AuthorMetadataExtractorVersion, authornorm.NormalizerVersion)
	require.NoError(t, err)
	assertRunError(t, f, runPath(empty, "approve-full"), "", http.StatusConflict, "not_a_completed_pilot")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_metadata_pilot_approval`))

	code, out = f.call(http.MethodGet, runPath(pilot, ""), "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, field(t, out, "run", "approved_for_full"))
	code, out = f.call(http.MethodGet, runPath(smoke, ""), "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, false, field(t, out, "run", "approved_for_full"))
}

// A pilot whose archive holds no catalog book would complete with nothing
// extracted and could then gate a full run: the start refuses it.
func TestRunsAPIPilotOfAnArchiveWithoutBooksIsRefused(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	f.archiveBooks()
	assertRunError(t, f, runsAPIBase, pilotBody("absent.zip"), http.StatusBadRequest, "invalid_selector")
	assertRunError(t, f, runsAPIBase, smokeBody(999_999), http.StatusBadRequest, "invalid_selector")
	assert.Zero(t, f.count(`SELECT count(*) FROM author_metadata_run`))
}

// insertApprovedPilot writes a completed, approved pilot of other versions.
func insertApprovedPilot(t *testing.T, f *runsAPIFixture, extractor, normalizer string) {
	t.Helper()
	var run int64
	_, err := f.db.QueryOne(pg.Scan(&run), `INSERT INTO author_metadata_run
		(mode, status, extractor_version, normalizer_version, selector_archive, items_total, items_terminal,
		 started_at, extraction_completed_at, finished_at)
		VALUES ('pilot_archive', 'completed', ?, ?, 'other.zip', 0, 0, now(), now(), now()) RETURNING id`,
		extractor, normalizer)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO author_metadata_pilot_approval
		(run_id, extractor_version, normalizer_version, approved_by_user_id) VALUES (?, ?, ?, ?)`,
		run, extractor, normalizer, f.actor)
	require.NoError(t, err)
}

// Plan RED 9: a full run needs an approved pilot of exactly its versions.
func TestRunsAPIFullRequiresAnApprovedMatchingPilot(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	f.archiveBooks()
	full := `{"mode":"full"}`

	assertRunError(t, f, runsAPIBase, full, http.StatusConflict, "full_run_not_approved")
	assert.Zero(t, f.count(`SELECT count(*) FROM author_metadata_run`), "a refused start seeds nothing")

	insertApprovedPilot(t, f, services.AuthorMetadataExtractorVersion, "authornorm-local-v0-other")
	insertApprovedPilot(t, f, "fb2-metadata-v0-other", authornorm.NormalizerVersion)
	assertRunError(t, f, runsAPIBase, full, http.StatusConflict, "full_run_not_approved")
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM author_metadata_run`), "mismatched approvals seed nothing")

	// The approval gate is checked before the active slot.
	pilot := f.start(pilotBody("happy.zip"))
	assertRunError(t, f, runsAPIBase, full, http.StatusConflict, "full_run_not_approved")
	assertRunError(t, f, runsAPIBase, pilotBody("errors.zip"), http.StatusConflict, "active_run_exists")
	f.drain(pilot)
	assertRunError(t, f, runsAPIBase, full, http.StatusConflict, "full_run_not_approved")
	code, out := f.call(http.MethodPost, runPath(pilot, "approve-full"), "")
	require.Equal(t, http.StatusOK, code, "%v", out)

	code, out = f.call(http.MethodPost, runsAPIBase, full)
	require.Equal(t, http.StatusCreated, code, "%v", out)
	assert.Equal(t, "full", field(t, out, "run", "mode"))
	assert.Equal(t, "pending", field(t, out, "run", "status"), "the start answers before the seeding")
	assert.Equal(t, int64(0), number(t, out, "run", "seeding", "seeded"))
	assert.Equal(t, int64(6), number(t, out, "run", "seeding", "target"), "the whole catalog")
	id := int64(field(t, out, "run", "id").(float64))
	assertRunError(t, f, runsAPIBase, full, http.StatusConflict, "active_run_exists")

	// The run's own loop seeds it and runs it.
	extraction, _ := f.workers()
	_, err := extraction.RunOnce(context.Background())
	require.NoError(t, err)
	code, out = f.call(http.MethodGet, runPath(id, ""), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, "running", field(t, out, "run", "status"))
	assert.Nil(t, field(t, out, "run", "seeding"))
	assert.Equal(t, int64(6), number(t, out, "run", "stages", "extraction", "total"), "the whole catalog")
}

// The status and the report carry the real numbers of an actual run: the
// errors archive ends one book extracted and three in per-book failures.
func TestRunsAPIReportsRealStageNumbers(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	f.archiveBooks()

	code, out := f.call(http.MethodGet, runsAPIBase+"/current", "")
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, out["run"], "no run yet")

	id := f.start(pilotBody("errors.zip"))
	code, out = f.call(http.MethodGet, runsAPIBase+"/current", "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, float64(id), field(t, out, "run", "id"))
	assert.Equal(t, int64(4), number(t, out, "run", "stages", "extraction", "total"))
	assert.Equal(t, int64(4), number(t, out, "run", "stages", "extraction", "pending"))
	assert.Equal(t, int64(0), number(t, out, "run", "stages", "extraction", "done"))
	assert.Nil(t, field(t, out, "run", "stages", "extraction", "current_archive"), "nothing is leased")

	// One item leased by a worker in flight; the queue has waited a while.
	time.Sleep(1100 * time.Millisecond)
	claims, err := database.ClaimExtractionItems(context.Background(), f.db, database.NewLeaseOwner(),
		database.LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 5})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	code, out = f.call(http.MethodGet, runsAPIBase+"/current", "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, int64(3), number(t, out, "run", "stages", "extraction", "pending"))
	assert.Equal(t, int64(1), number(t, out, "run", "stages", "extraction", "leased"))
	assert.Equal(t, "errors.zip", field(t, out, "run", "stages", "extraction", "current_archive"))
	assert.GreaterOrEqual(t, number(t, out, "run", "stages", "extraction", "oldest_pending_age_s"), int64(1))
	code, out = f.call(http.MethodGet, runPath(id, "report"), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Contains(t, field(t, out, "report", "not_ready_reasons"), "extraction_pending")
	_, err = f.db.Exec(`UPDATE author_metadata_run_item SET lease_expires_at = clock_timestamp() - interval '1 second'
		WHERE id = ?`, claims[0].ID)
	require.NoError(t, err)

	f.extract()
	code, out = f.call(http.MethodGet, runsAPIBase+"/current", "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	extraction := field(t, out, "run", "stages", "extraction")
	assert.Equal(t, int64(4), number(t, extraction, "done"))
	assert.Equal(t, int64(0), number(t, extraction, "pending"))
	assert.Equal(t, int64(0), number(t, extraction, "leased"))
	assert.Equal(t, map[string]any{
		"extracted": 1.0, "extracted_no_author": 0.0, "already_current": 0.0, "entry_missing": 0.0,
		"invalid_fb2": 1.0, "unsupported_encoding": 1.0, "metadata_parse_failed": 1.0,
		"archive_missing": 0.0, "archive_unreadable": 0.0,
	}, field(t, extraction, "by_status"))
	assert.NotNil(t, field(t, out, "run", "extraction_completed_at"))
	assert.Positive(t, field(t, extraction, "items_per_minute"))
	assert.Nil(t, field(t, extraction, "current_archive"))
	jobs := f.count(`SELECT count(*) FROM contributor_normalization_job`)
	require.Positive(t, jobs, "the extracted book queued local jobs")
	assert.Equal(t, int64(jobs), number(t, out, "run", "stages", "local", "total"))
	assert.Equal(t, int64(jobs), number(t, out, "run", "stages", "local", "pending"))
	assert.Equal(t, int64(jobs), number(t, out, "run", "credits", "pending"), "every author credit waits")

	code, out = f.call(http.MethodGet, runPath(id, "report"), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, false, field(t, out, "report", "ready"))
	assert.Contains(t, field(t, out, "report", "not_ready_reasons"), "run_not_completed")
	assert.Contains(t, field(t, out, "report", "not_ready_reasons"), "credits_pending")

	f.drain(id)
	code, out = f.call(http.MethodGet, runsAPIBase+"/current", "")
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, out["run"], "a completed run is no longer current")

	code, out = f.call(http.MethodGet, runPath(id, "report"), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	report := field(t, out, "report")
	assert.Equal(t, "completed", field(t, report, "status"))
	assert.Equal(t, int64(jobs), number(t, report, "stages", "local", "done"))
	assert.Equal(t, int64(0), number(t, report, "stages", "local", "pending"))
	credits := field(t, report, "credits")
	accounted := number(t, credits, "selected") + number(t, credits, "invalid") + number(t, credits, "review")
	for _, n := range field(t, credits, "unresolved").(map[string]any) {
		accounted += int64(n.(float64))
	}
	authors := f.count(`SELECT count(*) FROM book_contributor_credit WHERE role = 'author'`)
	assert.Equal(t, int64(authors), accounted, "every author credit is accounted")
	assert.Equal(t, int64(0), number(t, credits, "pending"))
	assert.Equal(t, int64(f.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'`)),
		number(t, report, "stages", "review", "open"))
	assert.Equal(t, true, field(t, report, "ready"), "%v", field(t, report, "not_ready_reasons"))
	assert.Equal(t, []any{}, field(t, report, "not_ready_reasons"))
	var snapshotBytes, creditBytes int64
	_, err = f.db.QueryOne(pg.Scan(&snapshotBytes, &creditBytes), `SELECT
		(SELECT sum(pg_column_size(s.*)) FROM book_metadata_snapshot s WHERE s.run_id = ?0),
		(SELECT sum(pg_column_size(c.*)) FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.run_id = ?0)`, id)
	require.NoError(t, err)
	require.Positive(t, snapshotBytes)
	require.Positive(t, creditBytes)
	assert.Equal(t, snapshotBytes+creditBytes, number(t, report, "db_growth_bytes"),
		"the run's snapshot and credit rows")
	assert.GreaterOrEqual(t, number(t, report, "duration_s"), int64(0))
	byClass := field(t, report, "by_class").(map[string]any)
	assert.NotEmpty(t, byClass, "credits resting on a result are counted by its decision class")
	byScript := field(t, report, "by_script").(map[string]any)
	assert.NotEmpty(t, byScript)
}

// Retry reopens the run's failed rows of one class the configured workers
// can still claim, and moves a completed run back to running; a run whose
// rows are all exhausted is left as it is.
func TestRunsAPIRetryReopensClaimableRows(t *testing.T) {
	f := newRunsAPIFixture(t, func(c *config.AuthorMetadataConfig) { c.Extraction.MaxAttempts = 2 })
	books := f.archiveBooks()
	id := f.start(pilotBody("errors.zip"))
	f.drain(id)
	retry := func(stage, class string) (int, map[string]any) {
		return f.call(http.MethodPost, runPath(id, "retry"), `{"stage":"`+stage+`","error_class":"`+class+`"}`)
	}

	code, out := retry("extraction", "entry_missing")
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	assert.Equal(t, map[string]any{"reopened": 0.0}, out)
	assert.Equal(t, "completed", f.status(id), "nothing reopened, nothing changed")

	code, out = retry("extraction", "invalid_fb2")
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	assert.Equal(t, map[string]any{"reopened": 1.0}, out)
	code, out = f.call(http.MethodGet, runPath(id, ""), "")
	require.Equal(t, http.StatusOK, code)
	run := field(t, out, "run")
	assert.Equal(t, "running", field(t, run, "status"), "a completed run goes back to running")
	assert.Nil(t, field(t, run, "extraction_completed_at"))
	assert.Nil(t, field(t, run, "completed_at"))
	assert.Equal(t, int64(3), number(t, run, "stages", "extraction", "done"))
	assert.Equal(t, int64(1), number(t, run, "stages", "extraction", "pending"))

	f.drain(id)
	foreign := books["errors.zip/foreign_root.fb2"]
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id WHERE i.run_id = ? AND i.book_id = ?`, id, foreign),
		"the first attempt stays as history")

	// The budget of two attempts is used: nothing is claimable any more.
	code, out = retry("extraction", "invalid_fb2")
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	assert.Equal(t, map[string]any{"reopened": 0.0}, out)
	assert.Equal(t, "completed", f.status(id))

	// A completed run cannot come back while another run holds the slot.
	other := f.start(smokeBody(books["happy.zip/multi_contributor.fb2"]))
	assertRunError(t, f, runPath(id, "retry"), `{"stage":"extraction","error_class":"unsupported_encoding"}`,
		http.StatusConflict, "active_run_exists")
	assert.Equal(t, "completed", f.status(id))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_metadata_run_item
		WHERE run_id = ? AND status = 'unsupported_encoding'`, id), "the refused retry reopened nothing")

	// A run ended failed_systemic is never retried.
	require.NoError(t, database.FailRunSystemic(context.Background(), f.db, other, "database_invariant"))
	code, out = f.call(http.MethodGet, runPath(other, ""), "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "failed_systemic", field(t, out, "run", "status"))
	assert.Nil(t, field(t, out, "run", "completed_at"), "a run that failed never completed")
	assert.Equal(t, "database_invariant", field(t, out, "run", "last_error_class"))
	assertRunError(t, f, runPath(other, "retry"), `{"stage":"extraction","error_class":"invalid_fb2"}`,
		http.StatusConflict, "invalid_transition")
	assertRunError(t, f, runPath(id+1000, "retry"), `{"stage":"extraction","error_class":"invalid_fb2"}`,
		http.StatusNotFound, "run_not_found")
}

// failEveryLocalJob fails every local job with normalizer_failed until each
// ended failed after attempts attempts, and returns how many jobs there are.
func (f *runsAPIFixture) failEveryLocalJob(attempts int) int {
	f.t.Helper()
	ctx := context.Background()
	owner := database.NewLeaseOwner()
	failure := database.LeaseFailure{ErrorClass: "normalizer_failed", RetryAfter: time.Millisecond, MaxAttempts: attempts}
	all := f.count(`SELECT count(*) FROM contributor_normalization_job`)
	require.Eventually(f.t, func() bool {
		claims, err := database.ClaimLocalNormalizationJobs(ctx, f.db, owner,
			database.LeaseClaimOptions{Limit: 10, Lease: time.Minute, MaxAttempts: attempts})
		require.NoError(f.t, err)
		for _, c := range claims {
			_, err = database.FailLocalNormalizationJob(ctx, f.db, c.ID, owner, failure)
			require.NoError(f.t, err)
		}
		return f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'failed'`) == all
	}, 5*time.Second, 5*time.Millisecond)
	return all
}

// A local retry reopens the run's failed local jobs that failed with the
// class and still have attempts under the configured budget.
func TestRunsAPIRetryReopensLocalJobs(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	f.archiveBooks()
	id := f.start(pilotBody("errors.zip"))
	f.extract()

	// Every local job fails twice with normalizer_failed under a budget of
	// two: it ends failed, below the configured budget of five.
	// A failed job of an input outside the run, which the retry must leave.
	seedAuthorJobs(t, f.db, 1)
	all := f.failEveryLocalJob(2)
	jobs := all - 1
	require.Positive(t, jobs)
	f.drain(id)
	code, out := f.call(http.MethodGet, runPath(id, ""), "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(jobs), number(t, out, "run", "stages", "local", "failed"), "only the run's inputs count")
	assert.Equal(t, int64(jobs), number(t, out, "run", "stages", "local", "total"))

	code, out = f.call(http.MethodPost, runPath(id, "retry"), `{"stage":"local","error_class":"transient_database"}`)
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	assert.Equal(t, map[string]any{"reopened": 0.0}, out, "no job failed with that class")

	code, out = f.call(http.MethodPost, runPath(id, "retry"), `{"stage":"local","error_class":"normalizer_failed"}`)
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	assert.Equal(t, map[string]any{"reopened": float64(jobs)}, out)
	assert.Equal(t, "running", f.status(id))
	f.drain(id)
	assert.Equal(t, jobs, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'completed'`),
		"the reopened jobs ran again")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'failed'`),
		"the job of an input outside the run stays failed")
}

// The server mounts the real run service: the wiring point no longer answers
// run_service_unavailable.
func TestAuthorMetadataRunsAPIIsWired(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	code, out := f.call(http.MethodGet, runsAPIBase+"/current", "")
	assert.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, map[string]any{"run": nil}, out)
}

// A retry by an attempt class reopens only the items that ended in a failure
// and saw that class: a book that failed once and was then extracted keeps
// its snapshot.
func TestRunsAPIRetryByAttemptClassReopensOnlyFailures(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	f.archiveBooks()
	id := f.start(pilotBody("errors.zip"))
	ctx := context.Background()

	// Every item's first attempt fails with a transient database error.
	owner := database.NewLeaseOwner()
	claims, err := database.ClaimExtractionItems(ctx, f.db, owner,
		database.LeaseClaimOptions{Limit: 10, Lease: time.Minute, MaxAttempts: 5})
	require.NoError(t, err)
	require.Len(t, claims, 4)
	for _, c := range claims {
		_, err = database.FailExtractionItem(ctx, f.db, c.ID, owner, database.LeaseFailure{
			ErrorClass: "transient_database", RetryAfter: time.Millisecond, MaxAttempts: 5})
		require.NoError(t, err)
	}
	time.Sleep(5 * time.Millisecond)
	f.drain(id)
	require.Equal(t, 1, f.count(`SELECT count(*) FROM author_metadata_run_item WHERE run_id = ? AND status = 'extracted'`, id))

	code, out := f.call(http.MethodPost, runPath(id, "retry"), `{"stage":"extraction","error_class":"transient_database"}`)
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	assert.Equal(t, map[string]any{"reopened": 3.0}, out, "the three failures, not the extracted book")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_metadata_run_item
		WHERE run_id = ? AND status = 'extracted' AND snapshot_id IS NOT NULL`, id))
}

// The server's own wiring call installs the service the admin routes mount.
func TestInitializeAuthorMetadataRunsAPIWiresTheAdminRoutes(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	initializeAuthorMetadataRunsAPI(f.db, &f.cfg)
	router := gin.New()
	api.SetupAdminRoutes(router.Group("/api/admin", func(c *gin.Context) {
		c.Set("user_id", f.actor)
		c.Next()
	}))
	f.router = router
	code, out := f.call(http.MethodGet, runsAPIBase+"/current", "")
	assert.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, map[string]any{"run": nil}, out)
}

// A local job that used the configured budget is not reopened: the retry
// counts zero and the completed run stays completed.
func TestRunsAPIRetryLeavesExhaustedLocalJobs(t *testing.T) {
	f := newRunsAPIFixture(t, func(c *config.AuthorMetadataConfig) { c.LocalNormalization.MaxAttempts = 2 })
	f.archiveBooks()
	id := f.start(pilotBody("errors.zip"))
	f.extract()
	jobs := f.failEveryLocalJob(2)
	f.drain(id)

	code, out := f.call(http.MethodPost, runPath(id, "retry"), `{"stage":"local","error_class":"normalizer_failed"}`)
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	assert.Equal(t, map[string]any{"reopened": 0.0}, out, "both attempts of the budget are used")
	assert.Equal(t, "completed", f.status(id))
	assert.Equal(t, jobs, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'failed'`))
}

// Plan contract 3.9: smoke and pilot_archive each take exactly one selector of
// either kind — book IDs or an archive — and full takes none. Every
// combination goes through the real service and seeds what it selects.
func TestRunsAPIStartAcceptsEitherSelectorForSmokeAndPilot(t *testing.T) {
	cases := []struct {
		name, mode string
		archive    bool
	}{
		{"smoke by book ids", "smoke", false},
		{"smoke by archive", "smoke", true},
		{"pilot by book ids", "pilot_archive", false},
		{"pilot by archive", "pilot_archive", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunsAPIFixture(t, nil)
			books := f.archiveBooks()
			body := fmt.Sprintf(`{"mode":%q,"book_ids":[%d,%d]}`, c.mode,
				books["happy.zip/translator_only.fb2"], books["happy.zip/multi_contributor.fb2"])
			if c.archive {
				body = fmt.Sprintf(`{"mode":%q,"archive":"happy.zip"}`, c.mode)
			}
			code, out := f.call(http.MethodPost, runsAPIBase, body)
			require.Equal(t, http.StatusCreated, code, "%v", out)
			id := int64(field(t, out, "run", "id").(float64))
			assert.Equal(t, c.mode, field(t, out, "run", "mode"))
			assert.Equal(t, int64(2), number(t, out, "run", "stages", "extraction", "total"))

			var stored struct {
				SelectorBookIDs []int64 `pg:"selector_book_ids,array"`
				SelectorArchive *string `pg:"selector_archive"`
			}
			_, err := f.db.QueryOne(&stored, `SELECT selector_book_ids, selector_archive FROM author_metadata_run
				WHERE id = ?`, id)
			require.NoError(t, err)
			if c.archive {
				require.NotNil(t, stored.SelectorArchive)
				assert.Equal(t, "happy.zip", *stored.SelectorArchive)
				assert.Empty(t, stored.SelectorBookIDs)
			} else {
				assert.Nil(t, stored.SelectorArchive)
				assert.Equal(t, []int64{books["happy.zip/multi_contributor.fb2"], books["happy.zip/translator_only.fb2"]},
					stored.SelectorBookIDs, "the selector is stored ascending")
			}
			assert.ElementsMatch(t, []int64{books["happy.zip/multi_contributor.fb2"], books["happy.zip/translator_only.fb2"]},
				f.runBooks(id))

			f.drain(id)
			if c.mode == "pilot_archive" {
				code, out = f.call(http.MethodPost, runPath(id, "approve-full"), "")
				assert.Equal(t, http.StatusOK, code, "a completed pilot of either selector is approvable: %v", out)
			}
		})
	}

	t.Run("a selector that selects no catalog book is refused in every mode", func(t *testing.T) {
		f := newRunsAPIFixture(t, nil)
		f.archiveBooks()
		for _, mode := range []string{"smoke", "pilot_archive"} {
			assertRunError(t, f, runsAPIBase, fmt.Sprintf(`{"mode":%q,"archive":"absent.zip"}`, mode),
				http.StatusBadRequest, "invalid_selector")
			assertRunError(t, f, runsAPIBase, fmt.Sprintf(`{"mode":%q,"book_ids":[999999]}`, mode),
				http.StatusBadRequest, "invalid_selector")
		}
		assert.Zero(t, f.count(`SELECT count(*) FROM author_metadata_run`))
	})
}

// runBooks lists the books a run's items name.
func (f *runsAPIFixture) runBooks(id int64) []int64 {
	f.t.Helper()
	var books []int64
	_, err := f.db.Query(&books, `SELECT book_id FROM author_metadata_run_item WHERE run_id = ? ORDER BY book_id`, id)
	require.NoError(f.t, err)
	return books
}

// Every extraction status counter reads its own status: the fixture ends
// unequal numbers in the statuses, so no two counters can be swapped
// unnoticed.
func TestRunsAPIReportCountsEveryStatusSeparately(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	ids := []int64{
		f.book("errors.zip", "multi_contributor.fb2"),
		f.book("errors.zip", "foreign_root.fb2"),
		f.book("errors.zip", "foreign_root.fb2"),
		f.book("errors.zip", "enc_unsupported_declared.fb2"),
		f.book("errors.zip", "absent-entry.fb2"),
		f.book("errors.zip", "malformed.fb2"),
		f.book("errors.zip", "malformed.fb2"),
		f.book("errors.zip", "malformed.fb2"),
	}
	id := f.start(smokeBody(ids...))
	f.drain(id)
	code, out := f.call(http.MethodGet, runPath(id, ""), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, map[string]any{
		"extracted": 1.0, "extracted_no_author": 0.0, "already_current": 0.0, "entry_missing": 1.0,
		"invalid_fb2": 2.0, "unsupported_encoding": 1.0, "metadata_parse_failed": 3.0,
		"archive_missing": 0.0, "archive_unreadable": 0.0,
	}, field(t, out, "run", "stages", "extraction", "by_status"))

	// A second run: a book already extracted ends already_current, two books
	// without authors extracted_no_author.
	again := f.start(smokeBody(ids[0], f.book("happy.zip", "translator_only.fb2"), f.book("happy.zip", "translator_only.fb2")))
	f.drain(again)
	code, out = f.call(http.MethodGet, runPath(again, ""), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, map[string]any{
		"extracted": 0.0, "extracted_no_author": 2.0, "already_current": 1.0, "entry_missing": 0.0,
		"invalid_fb2": 0.0, "unsupported_encoding": 0.0, "metadata_parse_failed": 0.0,
		"archive_missing": 0.0, "archive_unreadable": 0.0,
	}, field(t, out, "run", "stages", "extraction", "by_status"))
}
