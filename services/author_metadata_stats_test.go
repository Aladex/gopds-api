package services

// Phase 20, RED 3/4/6/8: the run aggregates reconcile with the rows the real
// workers wrote, and every dimension is a closed value.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorMetadataStatsReconcileWithRows(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	dir := t.TempDir()
	privacyArchive(t, dir, "fixture.zip", map[string]string{"canary.fb2": canaryFB2, "broken.fb2": brokenFB2})
	run := f.privacyRun("fixture.zip", "canary.fb2", "broken.fb2", "absent.fb2")
	drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
	// A book outside the run, with its own credit, job and result: the run's
	// aggregates must not count it.
	f.persistBook(author(tolstoy(t)))
	f.drain(f.worker(nil))

	review, err := NewAuthorMetadataReviewService(s, DefaultAuthorMetadataReviewConfig())
	require.NoError(t, err)
	page, err := review.List(ctx, ReviewListOptions{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "both canary authors are ambiguous: a nickname with a name, and initials")
	_, err = review.LeaveUnresolved(ctx, page.Items[0].ID, 1)
	require.NoError(t, err)

	stats, err := database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)

	// Extraction reconciles with the run items and the run counters.
	assert.Equal(t, int64(f.count(`SELECT count(*) FROM author_metadata_run_item WHERE run_id = ?`, run.ID)), stats.Extraction.Total)
	assert.Equal(t, int64(3), stats.Extraction.Total)
	var byStatus int64
	for _, n := range stats.Extraction.ByStatus {
		byStatus += n
	}
	assert.Equal(t, stats.Extraction.Terminal, byStatus)
	assert.Equal(t, int64(f.count(`SELECT items_terminal FROM author_metadata_run WHERE id = ?`, run.ID)), stats.Extraction.Terminal)
	assert.Equal(t, map[string]int64{
		"extracted": 1, "metadata_parse_failed": 1, "entry_missing": 1,
		"extracted_no_author": 0, "already_current": 0, "invalid_fb2": 0, "unsupported_encoding": 0,
	}, stats.Extraction.ByStatus)
	assert.Zero(t, stats.Extraction.Pending)
	assert.Positive(t, stats.Extraction.ItemsPerSecond)
	var classes int64
	for _, n := range stats.Extraction.ErrorClasses {
		classes += n
	}
	assert.Equal(t, int64(f.count(`SELECT count(*) FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id
		WHERE i.run_id = ? AND a.error_class IS NOT NULL`, run.ID)), classes, "error classes reconcile with the attempts")

	// Local stream: the run's inputs and their jobs.
	assert.Equal(t, 3, f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'completed'`))
	assert.Equal(t, int64(2), stats.Local.Completed, "only the run's inputs")
	assert.Zero(t, stats.Local.Pending)

	// Credits: every current author credit is in exactly one bucket.
	credits := int64(f.count(`SELECT count(*) FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE s.is_current AND c.role = 'author'
			AND s.book_id IN (SELECT book_id FROM author_metadata_run_item WHERE run_id = ?)`, run.ID))
	assert.Equal(t, credits, stats.Credits.Total)
	assert.Equal(t, int64(2), stats.Credits.Total)
	sum := stats.Credits.Selected + stats.Credits.Invalid + stats.Credits.Review + stats.Credits.Pending
	for _, n := range stats.Credits.Unresolved {
		sum += n
	}
	assert.Equal(t, stats.Credits.Total, sum)
	assert.Equal(t, map[string]int64{"review_left_unresolved": 1}, stats.Credits.Unresolved)
	assert.Equal(t, int64(1), stats.Credits.Review)

	// Coverage counts the resolved credits by their result.
	resolved := int64(f.count(`SELECT count(*) FROM book_contributor_credit_selection sel
		JOIN book_contributor_credit c ON c.id = sel.credit_id
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE sel.result_id IS NOT NULL AND s.book_id IN (SELECT book_id FROM author_metadata_run_item WHERE run_id = ?)`, run.ID))
	assert.Equal(t, int64(1), resolved, "the credit left unresolved rests on no result")
	for name, dim := range map[string]map[string]int64{
		"method": stats.Coverage.ByMethod, "kind": stats.Coverage.ByKind,
		"script": stats.Coverage.ByScript, "decision class": stats.Coverage.ByDecisionClass,
	} {
		var total int64
		for _, n := range dim {
			total += n
		}
		assert.Equal(t, resolved, total, name)
	}

	// Review: one item left unresolved by the admin, one still open.
	assert.Equal(t, int64(f.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'`)), stats.Review.Open)
	assert.Equal(t, int64(1), stats.Review.Open)
	assert.Positive(t, stats.Review.OldestOpenAgeS)
	assert.Equal(t, int64(1), stats.Review.Closed)
	assert.Equal(t, map[string]int64{"left_unresolved": 1}, stats.Review.ByResolution)

	assertClosedDimensions(t, &stats)
}

// assertClosedDimensions is RED 6: every map key is a closed value, and the
// serialized report carries no identifier, fingerprint or name.
func assertClosedDimensions(t *testing.T, stats *database.AuthorMetadataStats) {
	t.Helper()
	closed := func(values ...string) map[string]bool {
		set := map[string]bool{}
		for _, v := range values {
			set[v] = true
		}
		return set
	}
	var statuses, methods, kinds, classes, reasons, resolutions []string
	for _, v := range models.AuthorMetadataRunItemTerminalStatuses() {
		statuses = append(statuses, string(v))
	}
	for _, v := range models.NormalizationMethods() {
		methods = append(methods, string(v))
	}
	for _, v := range models.NormalizationKinds() {
		kinds = append(kinds, string(v))
	}
	for _, v := range authornorm.DecisionClasses() {
		classes = append(classes, string(v))
	}
	for _, v := range models.UnresolvedReasons() {
		reasons = append(reasons, string(v))
	}
	for _, v := range models.ReviewResolutions() {
		resolutions = append(resolutions, string(v))
	}
	// Membership, not shape: a key of the right shape can still carry a name
	// or an identifier.
	errorClasses := closed(append(database.AuthorMetadataAttemptErrorClasses(), database.AuthorMetadataOtherValue)...)
	// Scripts are the ISO 15924 table of authornorm (Script.Validate is a
	// table lookup), plus none and the fallback.
	scripts := map[string]bool{"none": true, database.AuthorMetadataOtherValue: true}
	for key := range stats.Coverage.ByScript {
		if authornorm.Script(key).Validate() == nil {
			scripts[key] = true
		}
	}
	for _, check := range []struct {
		name    string
		dim     map[string]int64
		allowed map[string]bool
	}{
		{"extraction status", stats.Extraction.ByStatus, closed(statuses...)},
		{"extraction error class", stats.Extraction.ErrorClasses, errorClasses},
		{"local error class", stats.Local.ErrorClasses, errorClasses},
		{"method", stats.Coverage.ByMethod, closed(methods...)},
		{"kind", stats.Coverage.ByKind, closed(kinds...)},
		{"script", stats.Coverage.ByScript, scripts},
		{"decision class", stats.Coverage.ByDecisionClass, closed(append(classes, "manual", database.AuthorMetadataOtherValue)...)},
		{"unresolved reason", stats.Credits.Unresolved, closed(reasons...)},
		{"review resolution", stats.Review.ByResolution, closed(resolutions...)},
	} {
		require.NotNil(t, check.dim, check.name)
		for key := range check.dim {
			assert.True(t, check.allowed[key], "%s %q is not a closed value", check.name, key)
		}
	}
	raw, err := json.Marshal(stats)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(raw)), canary)
	assert.NotRegexp(t, `[0-9a-f]{64}`, string(raw), "no fingerprint")
}

// Fix round 1 (review B2): an error class outside the pipeline's closed set —
// the lease API takes any class of the right shape — is counted as "other";
// the class itself never becomes an aggregate key.
func TestStatsErrorClassesAreTheClosedSet(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	run := f.privacyRun("x.zip", "x.fb2")
	owner := database.NewLeaseOwner()
	claims, err := database.ClaimExtractionItems(ctx, s, owner, database.LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 3})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	_, err = database.FailExtractionItem(ctx, s, claims[0].ID, owner, database.LeaseFailure{
		ErrorClass: "zqxcanary_person_123456", RetryAfter: time.Millisecond, MaxAttempts: 3,
	})
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	claims, err = database.ClaimExtractionItems(ctx, s, owner, database.LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 3})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	_, err = database.FailExtractionItem(ctx, s, claims[0].ID, owner, database.LeaseFailure{
		ErrorClass: string(AuthorMetadataErrorTransientDatabase), RetryAfter: time.Minute, MaxAttempts: 3,
	})
	require.NoError(t, err)

	stats, err := database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"other": 1, "transient_database": 1}, stats.Extraction.ErrorClasses)
	assertClosedDimensions(t, &stats)
}

// Fix round 1 (review B3): another normalizer version's jobs for the same
// sources are other inputs; they change nothing in this run's local counts.
func TestStatsLocalStreamIsTheRunsNormalizerVersion(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	dir := t.TempDir()
	privacyArchive(t, dir, "fixture.zip", map[string]string{"canary.fb2": canaryFB2})
	run := f.privacyRun("fixture.zip", "canary.fb2")
	drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
	f.drain(f.worker(nil))
	before, err := database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), before.Local.Completed)

	var inputs []struct {
		Fingerprint      []byte
		ExtractorVersion string
	}
	_, err = s.Query(&inputs, `SELECT source_fingerprint AS fingerprint, extractor_version FROM contributor_normalization_job`)
	require.NoError(t, err)
	for _, in := range inputs {
		var fp [32]byte
		copy(fp[:], in.Fingerprint)
		key, keyErr := authornorm.NormalizationKey(fp, in.ExtractorVersion, "newer-normalizer")
		require.NoError(t, keyErr)
		f.exec(`INSERT INTO contributor_normalization_job (normalization_key, source_fingerprint, extractor_version, normalizer_version)
			VALUES (?, ?, ?, 'newer-normalizer')`, key[:], fp[:], in.ExtractorVersion)
	}
	failed := f.count(`SELECT count(*) FROM contributor_normalization_job WHERE normalizer_version = 'newer-normalizer'`)
	require.Equal(t, 2, failed)

	after, err := database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assert.Equal(t, before.Local, after.Local)
}

// Fix round 1 (review B3, extractor half): a book of the run whose current
// snapshot is now another extractor version brings jobs of another input;
// they are not the run's queue.
func TestStatsLocalStreamIsTheRunsExtractorVersion(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	dir := t.TempDir()
	privacyArchive(t, dir, "fixture.zip", map[string]string{"canary.fb2": canaryFB2})
	run := f.privacyRun("fixture.zip", "canary.fb2")
	drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
	f.drain(f.worker(nil))

	var book int64
	_, err := s.QueryOne(pg.Scan(&book), `SELECT book_id FROM author_metadata_run_item WHERE run_id = ?`, run.ID)
	require.NoError(t, err)
	require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, persistErr := database.PersistExtraction(tx, &database.ExtractionInput{
			BookID: book, BookMD5: strings.Repeat("c", 32), ExtractorVersion: "other-extractor",
			NormalizerVersion: authornorm.NormalizerVersion, Origin: models.BookMetadataSnapshotLive,
			Outcome: models.BookMetadataSnapshotExtracted, ArchivePath: "fixture.zip", EntryName: "canary.fb2",
			Credits: []database.ExtractionCredit{author(tolstoy(t)), author(chekhov(t))},
		})
		return persistErr
	}))
	require.Equal(t, 2, f.count(`SELECT count(*) FROM contributor_normalization_job
		WHERE status = 'pending' AND extractor_version = 'other-extractor'`))

	stats, err := database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assert.Zero(t, stats.Local.Pending, "another extractor version's jobs are not the run's queue")
	assert.Zero(t, stats.Local.Leased)
	assert.Empty(t, stats.Coverage.ByDecisionClass, "nor are its credits the run's coverage")
}

// The schema checks a result's script only for its shape; a schema-valid
// code outside the ISO 15924 table is counted as "other", never as its own
// coverage key.
func TestStatsCoverageScriptIsTheClosedSet(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	dir := t.TempDir()
	privacyArchive(t, dir, "fixture.zip", map[string]string{"canary.fb2": canaryFB2})
	run := f.privacyRun("fixture.zip", "canary.fb2")
	drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
	f.drain(f.worker(nil))

	var credit int64
	var fp []byte
	_, err := s.QueryOne(pg.Scan(&credit, &fp), `SELECT c.id, c.source_fingerprint FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.is_current AND c.role = 'author' ORDER BY c.id LIMIT 1`)
	require.NoError(t, err)
	var result int64
	_, err = s.QueryOne(pg.Scan(&result), `INSERT INTO contributor_normalization_result
		(source_fingerprint, result_schema_version, method, kind, status, display_name, search_key, script, created_by_user_id)
		VALUES (?, 'manual-v1', 'manual', 'person', 'normalized', 'Manual', 'manual', 'Qwer', 1) RETURNING id`, fp)
	require.NoError(t, err)
	f.exec(`INSERT INTO contributor_manual_override (scope_credit_id, source_fingerprint, result_id, created_by_user_id)
		VALUES (?, ?, ?, 1)`, credit, fp, result)
	require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return database.ResolveCreditSelection(ctx, tx, credit, nil)
	}))

	stats, err := database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.Coverage.ByScript[database.AuthorMetadataOtherValue])
	assert.NotContains(t, stats.Coverage.ByScript, "Qwer")
	assertClosedDimensions(t, &stats)
}

// Integration with phase 16 Part 2: leased is a live lease, the same
// definition the runs API shows, and pending excludes it. A lease that ran
// out is pending again — any worker may claim the row — whatever owner it
// still names.
func TestStatsLeasedIsALiveLease(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	dir := t.TempDir()
	privacyArchive(t, dir, "fixture.zip", map[string]string{"canary.fb2": canaryFB2})
	run := f.privacyRun("fixture.zip", "canary.fb2", "absent.fb2", "absent-2.fb2")
	owner := database.NewLeaseOwner()

	claims, err := database.ClaimExtractionItems(ctx, s, owner, database.LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 3})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	stats, err := database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.Extraction.Pending, "the leased item is not pending")
	assert.Equal(t, int64(1), stats.Extraction.Leased)

	f.exec(`UPDATE author_metadata_run_item SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id = ?`, claims[0].ID)
	stats, err = database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.Extraction.Pending, "an expired lease is pending again")
	assert.Zero(t, stats.Extraction.Leased, "an expired lease is not a lease, whatever owner it names")

	drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
	jobs := int64(f.count(`SELECT count(*) FROM contributor_normalization_job WHERE status = 'pending'`))
	require.Positive(t, jobs)
	local, err := database.ClaimLocalNormalizationJobs(ctx, s, owner, database.LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 3})
	require.NoError(t, err)
	require.Len(t, local, 1)
	stats, err = database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assert.Equal(t, jobs-1, stats.Local.Pending, "the leased job is not pending")
	assert.Equal(t, int64(1), stats.Local.Leased)

	f.exec(`UPDATE contributor_normalization_job SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id = ?`, local[0].ID)
	stats, err = database.AuthorMetadataStatsForRun(ctx, s, run.ID)
	require.NoError(t, err)
	assert.Equal(t, jobs, stats.Local.Pending, "an expired lease is pending again")
	assert.Zero(t, stats.Local.Leased)
}

// Integration fix round 1: the classes the extraction worker pauses or ends
// a run with are exactly the run error classes the writers accept and the
// API shows.
func TestSystemicClassesAreTheRunErrorClasses(t *testing.T) {
	systemic := []string{
		string(AuthorMetadataErrorArchiveUnreadable), string(AuthorMetadataErrorDatabaseInvariant),
		string(AuthorMetadataErrorVersionMismatch), string(AuthorMetadataErrorExtractorMisconfigured),
	}
	assert.ElementsMatch(t, systemic, database.AuthorMetadataRunErrorClasses())
}
