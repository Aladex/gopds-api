package main

// Phase 20 integration with phase 16 Part 2: the runs API reads its stage
// numbers, credits and coverage from database.AuthorMetadataStatsForRun, so
// the run status, the report and the stats are one definition — a live
// lease, the run's version-pinned inputs — and the retry of local jobs takes
// the same inputs. RED 7/8 run again on the real adapter: the status and the
// report carry aggregates only, and every error is a closed code that leaves
// nothing of its text in the response or the log.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"gopds-api/api"
	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"

	//nolint:depguard // the test raises the logger to its most verbose level
	"github.com/sirupsen/logrus"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runsCanary = "zqxcanary"

// otherVersionInput gives a book of the run a current snapshot of another
// extractor version, with one canary-named author credit whose local job
// failed and whose fingerprint has an open review item: an input of the
// book, not of the run.
func (f *runsAPIFixture) otherVersionInput(book int64) {
	f.t.Helper()
	var runVersion string
	_, err := f.db.QueryOne(pg.Scan(&runVersion), `SELECT extractor_version FROM author_metadata_run ORDER BY id DESC LIMIT 1`)
	require.NoError(f.t, err)
	other := runVersion + "-previous"
	source, err := authornorm.NewSourceValue([]authornorm.SourceComponent{
		{Kind: authornorm.ComponentFirst, Value: "Zqxcanary"},
		{Kind: authornorm.ComponentLast, Value: "Zqxcanaryov"},
	})
	require.NoError(f.t, err)
	require.NoError(f.t, f.db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		_, persistErr := database.PersistExtraction(tx, &database.ExtractionInput{
			BookID: book, BookMD5: fmt.Sprintf("%032x", book), ExtractorVersion: other,
			NormalizerVersion: authornorm.NormalizerVersion, Origin: models.BookMetadataSnapshotLive,
			Outcome: models.BookMetadataSnapshotExtracted, ArchivePath: "errors.zip", EntryName: "multi_contributor.fb2",
			Credits: []database.ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: source}},
		})
		return persistErr
	}))
	require.Equal(f.t, 1, f.failEveryLocalJob(2), "the other version's one job failed")
	_, err = f.db.Exec(`INSERT INTO contributor_review_item (scope_fingerprint, source_fingerprint, reason)
		SELECT source_fingerprint, source_fingerprint, 'ambiguous_decision' FROM contributor_normalization_job
		WHERE extractor_version = ?`, other)
	require.NoError(f.t, err)
}

// The status counts only the run's own inputs: a book of the run whose
// current snapshot is another extractor version brings jobs and review items
// of another input.
func TestRunsAPIStagesAreTheRunsExtractorVersion(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	books := f.archiveBooks()
	id := f.start(pilotBody("errors.zip"))
	f.otherVersionInput(books["errors.zip/multi_contributor.fb2"])

	code, out := f.call(http.MethodGet, runPath(id, ""), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	stages := field(t, out, "run", "stages")
	for _, key := range []string{"total", "done", "pending", "leased", "failed"} {
		assert.Zero(t, number(t, stages, "local", key), "local %s: another version's job is not the run's", key)
	}
	assert.Zero(t, number(t, stages, "review", "open"), "nor is its review item")
}

// The retry of local jobs reopens the run's inputs only, by the same
// definition the status shows.
func TestRunsAPILocalRetryLeavesAnotherExtractorVersionsJobs(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	books := f.archiveBooks()
	id := f.start(pilotBody("errors.zip"))
	f.otherVersionInput(books["errors.zip/multi_contributor.fb2"])

	code, out := f.call(http.MethodPost, runPath(id, "retry"), `{"stage":"local","error_class":"normalizer_failed"}`)
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	assert.Equal(t, map[string]any{"reopened": 0.0}, out, "another version's failed job is not the run's to reopen")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'failed'`))
}

// assertRunMatchesStats compares the contract's Run object with the stats of
// the same run read right after it.
func assertRunMatchesStats(t *testing.T, f *runsAPIFixture, id int64, run any) {
	t.Helper()
	stats, err := database.AuthorMetadataStatsForRun(context.Background(), f.db, id)
	require.NoError(t, err)
	ex := field(t, run, "stages", "extraction")
	assert.Equal(t, stats.Extraction.Total, number(t, ex, "total"))
	assert.Equal(t, stats.Extraction.Terminal, number(t, ex, "done"))
	assert.Equal(t, stats.Extraction.Pending, number(t, ex, "pending"))
	assert.Equal(t, stats.Extraction.Leased, number(t, ex, "leased"))
	for status, n := range stats.Extraction.ByStatus {
		assert.Equal(t, n, number(t, ex, "by_status", status), status)
	}
	local := field(t, run, "stages", "local")
	assert.Equal(t, stats.Local.Pending+stats.Local.Leased+stats.Local.Completed+stats.Local.Failed, number(t, local, "total"))
	assert.Equal(t, stats.Local.Completed, number(t, local, "done"))
	assert.Equal(t, stats.Local.Pending, number(t, local, "pending"))
	assert.Equal(t, stats.Local.Leased, number(t, local, "leased"))
	assert.Equal(t, stats.Local.Failed, number(t, local, "failed"))
	assert.Equal(t, stats.Review.Open, number(t, run, "stages", "review", "open"))
	assert.Equal(t, stats.Review.Closed, number(t, run, "stages", "review", "closed"))
	credits := field(t, run, "credits")
	assert.Equal(t, stats.Credits.Selected, number(t, credits, "selected"))
	assert.Equal(t, stats.Credits.Invalid, number(t, credits, "invalid"))
	assert.Equal(t, stats.Credits.Review, number(t, credits, "review"))
	assert.Equal(t, stats.Credits.Pending, number(t, credits, "pending"))
	for reason, n := range stats.Credits.Unresolved {
		assert.Equal(t, n, number(t, credits, "unresolved", reason), reason)
	}
}

// The status and the report are the stats of the run, in the contract's
// shape: at every step of a real run the numbers are the ones
// AuthorMetadataStatsForRun reads.
func TestRunsAPIStatusAndReportAreTheRunStats(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	f.archiveBooks()
	id := f.start(pilotBody("errors.zip"))

	claims, err := database.ClaimExtractionItems(context.Background(), f.db, database.NewLeaseOwner(),
		database.LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 5})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	code, out := f.call(http.MethodGet, runPath(id, ""), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assertRunMatchesStats(t, f, id, field(t, out, "run"))
	_, err = f.db.Exec(`UPDATE author_metadata_run_item SET lease_expires_at = clock_timestamp() - interval '1 second'
		WHERE id = ?`, claims[0].ID)
	require.NoError(t, err)

	f.extract()
	code, out = f.call(http.MethodGet, runPath(id, ""), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assertRunMatchesStats(t, f, id, field(t, out, "run"))

	f.drain(id)
	code, out = f.call(http.MethodGet, runPath(id, "report"), "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	report := field(t, out, "report")
	assertRunMatchesStats(t, f, id, report)
	stats, err := database.AuthorMetadataStatsForRun(context.Background(), f.db, id)
	require.NoError(t, err)
	// Extraction completed, so its duration and throughput are fixed.
	assert.InDelta(t, stats.Extraction.ItemsPerSecond*60, field(t, report, "stages", "extraction", "items_per_minute"), 1e-9)
	assert.Equal(t, int64(math.Floor(stats.Extraction.OldestPendingAgeS)), number(t, report, "stages", "extraction", "oldest_pending_age_s"))
	// by_class and by_script are the coverage of the credits resting on a
	// result with a decision class and a script.
	wantClass := map[string]any{}
	for class, n := range stats.Coverage.ByDecisionClass {
		if class != "manual" {
			wantClass[class] = float64(n)
		}
	}
	wantScript := map[string]any{}
	for script, n := range stats.Coverage.ByScript {
		if script != "none" {
			wantScript[script] = float64(n)
		}
	}
	require.NotEmpty(t, wantClass)
	assert.Equal(t, wantClass, field(t, report, "by_class"))
	assert.Equal(t, wantScript, field(t, report, "by_script"))
}

// loggedRunsRouter mounts the real runs API behind the server's request
// logger, which writes every handler's c.Errors into the log.
func (f *runsAPIFixture) loggedRunsRouter() {
	f.t.Helper()
	svc, err := newAuthorMetadataRunsAPI(f.db, &f.cfg)
	require.NoError(f.t, err)
	r := gin.New()
	r.Use(logging.GinrusLogger())
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("user_id", f.actor)
		c.Next()
	})
	api.SetupAuthorMetadataRunRoutes(admin.Group("/author-metadata"), svc)
	f.router = r
}

// runsTraceHook captures every log entry at Trace level for one test.
func runsTraceHook(t *testing.T) *logrustest.Hook {
	t.Helper()
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	previous := logging.GetLogger().GetLevel()
	logging.GetLogger().SetLevel(logrus.TraceLevel)
	t.Cleanup(func() { logging.GetLogger().SetLevel(previous) })
	return hook
}

func assertNoRunsCanaryLogged(t *testing.T, hook *logrustest.Hook) {
	t.Helper()
	require.NotEmpty(t, hook.AllEntries(), "the request logger ran")
	for _, entry := range hook.AllEntries() {
		line, err := entry.String()
		require.NoError(t, err)
		assert.NotContains(t, strings.ToLower(line+fmt.Sprint(entry.Data)), runsCanary)
	}
}

// runKeys collects every object key of a JSON value, dotted by path.
func runKeys(prefix string, v any, into map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			into[prefix+k] = true
			runKeys(prefix+k+".", child, into)
		}
	case []any:
		for _, child := range x {
			runKeys(prefix, child, into)
		}
	}
}

// RED 7 on the real adapter: over a catalog whose titles and authors carry
// the canary, the run status and the report of a real run are aggregates,
// IDs, versions and closed values — no name, title, fingerprint or raw audit
// in a key or a value — and the run's whole life logs none of it.
func TestRunsAPIRealStatusAndReportCarryNoSourceText(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	books := f.archiveBooks()
	_, err := f.db.Exec(`UPDATE opds_catalog_book SET title = 'Zqxcanary Title'`)
	require.NoError(t, err)
	f.loggedRunsRouter()
	hook := runsTraceHook(t)

	id := f.start(pilotBody("errors.zip"))
	f.otherVersionInput(books["errors.zip/multi_contributor.fb2"])
	keys := map[string]bool{}
	read := func() {
		for _, path := range []string{runsAPIBase + "/current", runPath(id, ""), runPath(id, "report")} {
			rec := f.raw(http.MethodGet, path, "")
			require.Equal(t, http.StatusOK, rec.Code, "%s %s", path, rec.Body.String())
			assert.NotContains(t, strings.ToLower(rec.Body.String()), runsCanary, path)
			var out map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
			runKeys("", out, keys)
		}
	}
	read()
	f.extract()
	read()
	f.drain(id)
	read()

	var got []string
	for k := range keys {
		// The closed maps are keyed by value, not by field.
		if strings.Contains(k, ".by_class.") || strings.Contains(k, ".by_script.") || strings.Contains(k, ".unresolved.") {
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
	assert.Contains(t, got, "report.by_class")
	assertNoRunsCanaryLogged(t, hook)
}

// raw sends one request and returns the recorder.
func (f *runsAPIFixture) raw(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// RED 8 on the real adapter: a database failure whose text carries the
// canary — a trigger raising it on the run's own rows — answers
// internal_error and nothing else, and so do the closed refusals; no log
// entry carries the canary.
func TestRunsAPIRealErrorsAreClosedCodes(t *testing.T) {
	f := newRunsAPIFixture(t, nil)
	books := f.archiveBooks()
	f.loggedRunsRouter()
	hook := runsTraceHook(t)

	id := f.start(pilotBody("errors.zip"))
	// Closed refusals of the real service.
	assertRunError(t, f, runPath(id+100, "pause"), "", http.StatusNotFound, "run_not_found")
	assertRunError(t, f, runPath(id, "resume"), "", http.StatusConflict, "invalid_transition")
	assertRunError(t, f, runsAPIBase, pilotBody("zqxcanary.zip"), http.StatusBadRequest, "invalid_selector")
	assertRunError(t, f, runsAPIBase, pilotBody("happy.zip"), http.StatusConflict, "active_run_exists")
	assertRunError(t, f, runPath(id, "retry"), `{"stage":"local","error_class":"zqxcanary"}`,
		http.StatusBadRequest, "invalid_error_class")

	// Database failures that quote the canary: a trigger on the run's own
	// row, raised by the pause's update and by the next start's insert.
	_, err := f.db.Exec(`CREATE FUNCTION privacy_runs_canary() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'Zqxcanary Title by Zqxcanaryov'; END $$`)
	require.NoError(t, err)
	_, err = f.db.Exec(`CREATE TRIGGER privacy_runs_canary BEFORE UPDATE ON author_metadata_run
		FOR EACH ROW EXECUTE FUNCTION privacy_runs_canary()`)
	require.NoError(t, err)
	assertRunError(t, f, runPath(id, "pause"), "", http.StatusInternalServerError, "internal_error")
	_, err = f.db.Exec(`DROP TRIGGER privacy_runs_canary ON author_metadata_run`)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE author_metadata_run SET status = 'failed_systemic', finished_at = now(),
		last_error_class = 'database_invariant' WHERE id = ?`, id)
	require.NoError(t, err)
	_, err = f.db.Exec(`CREATE TRIGGER privacy_runs_canary_insert BEFORE INSERT ON author_metadata_run
		FOR EACH ROW EXECUTE FUNCTION privacy_runs_canary()`)
	require.NoError(t, err)
	assertRunError(t, f, runsAPIBase, smokeBody(books["happy.zip/multi_contributor.fb2"]),
		http.StatusInternalServerError, "internal_error")

	assertNoRunsCanaryLogged(t, hook)
}

// Integration fix round 1, the reviewer's probe on the real adapter: a
// name-shaped class passes the schema's shape, so the public systemic
// writers refuse it, and a stored one — written past them — answers "other"
// on /runs/current, /runs/:id and the report. A known class is shown as
// itself; a run without one shows null.
func TestRunsAPILastErrorClassIsClosed(t *testing.T) {
	const canary = "zqxcanary_person_123456"
	f := newRunsAPIFixture(t, nil)
	f.archiveBooks()
	f.loggedRunsRouter()
	hook := runsTraceHook(t)
	ctx := context.Background()
	id := f.start(pilotBody("errors.zip"))

	shown := func(paths ...string) []any {
		var values []any
		for _, path := range paths {
			rec := f.raw(http.MethodGet, path, "")
			require.Equal(t, http.StatusOK, rec.Code, "%s %s", path, rec.Body.String())
			assert.NotContains(t, strings.ToLower(rec.Body.String()), runsCanary, path)
			var out map[string]map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
			for _, object := range out {
				value, present := object["last_error_class"]
				require.True(t, present, "the key stays: %s", path)
				values = append(values, value)
			}
		}
		return values
	}
	all := []string{runsAPIBase + "/current", runPath(id, ""), runPath(id, "report")}

	assert.Equal(t, []any{nil, nil, nil}, shown(all...), "no class yet")

	require.Error(t, database.PauseRunSystemic(ctx, f.db, id, canary), "the public writer refuses it")
	require.Error(t, database.FailRunSystemic(ctx, f.db, id, canary))
	assert.Equal(t, "running", f.status(id))

	require.NoError(t, database.PauseRunSystemic(ctx, f.db, id, "archive_unreadable"))
	assert.Equal(t, []any{"archive_unreadable", "archive_unreadable", "archive_unreadable"}, shown(all...))

	// A stored value outside the vocabulary — the schema accepts the shape.
	_, err := f.db.Exec(`UPDATE author_metadata_run SET last_error_class = ? WHERE id = ?`, canary, id)
	require.NoError(t, err)
	assert.Equal(t, []any{"other", "other", "other"}, shown(all...))

	_, err = f.db.Exec(`UPDATE author_metadata_run SET last_error_class = NULL WHERE id = ?`, id)
	require.NoError(t, err)
	require.NoError(t, database.FailRunSystemic(ctx, f.db, id, "database_invariant"))
	assert.Equal(t, []any{"database_invariant", "database_invariant"}, shown(runPath(id, ""), runPath(id, "report")))

	assertNoRunsCanaryLogged(t, hook)
}
