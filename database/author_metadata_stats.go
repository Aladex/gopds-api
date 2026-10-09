package database

import (
	"context"
	"errors"
	"fmt"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The durable aggregates of one author metadata run (plan phase 20, RED 3):
// what the admin status and report read instead of any in-process registry.
// Every number comes from the pipeline's own rows at the moment of the call.
// Every dimension is a closed value — an item status, an error class, a
// method, kind, script or decision class, a review resolution or an
// unresolved reason — never an ID, a fingerprint or a name, and an empty
// stream reports zeros: no NaN, nothing negative, no null map. There is no
// LLM stream, so no token or cost figure (scope amendment A1).

// ErrRunNotFound marks a run ID with no row.
var ErrRunNotFound = errors.New("database: author metadata run not found")

// AuthorMetadataOtherValue is the one dimension value every value outside a
// closed set is counted under. The schema accepts any error class of the
// right shape, and a shape is not a vocabulary: an unknown class could carry
// a name or an identifier, so it never becomes a key of its own.
const AuthorMetadataOtherValue = "other"

// AuthorMetadataAttemptErrorClasses is the closed vocabulary of error classes
// the pipeline records on attempts: the lease layer's own two and every class
// the extraction and local workers define (services keeps its constants and a
// test pins them to this list).
func AuthorMetadataAttemptErrorClasses() []string {
	return []string{
		LeaseErrorLeaseExpired, LeaseErrorMaxAttemptsExceeded,
		classTransientDatabase, classNormalizerFailed,
		classNoAuthorCredit, classSourceNotCanonical, classNormalizerVersionMismatch,
		classArchiveUnreadable, classDatabaseInvariant, classVersionMismatch,
		classExtractorMisconfigured, classExtractionFailed,
	}
}

// The worker classes of the vocabulary, spelled as services defines them.
const (
	classTransientDatabase         = "transient_database"
	classNormalizerFailed          = "normalizer_failed"
	classNoAuthorCredit            = "no_author_credit"
	classSourceNotCanonical        = "source_not_canonical"
	classNormalizerVersionMismatch = "normalizer_version_mismatch"
	classArchiveUnreadable         = "archive_unreadable"
	classDatabaseInvariant         = "database_invariant"
	classVersionMismatch           = "version_mismatch"
	classExtractorMisconfigured    = "extractor_misconfigured"
	classExtractionFailed          = "extraction_failed"
)

func closedErrorClass(class string) string {
	for _, known := range AuthorMetadataAttemptErrorClasses() {
		if class == known {
			return class
		}
	}
	return AuthorMetadataOtherValue
}

// Coverage values outside the domain's sets.
const (
	// coverageNoScript is a result without a script (a manual result).
	coverageNoScript = "none"
	// coverageManual is a result without a decision class (a manual result).
	coverageManual = "manual"
	coverageMixed  = "mixed"
)

func closedScript(script string) string {
	if script == coverageNoScript || script == coverageMixed || authornorm.Script(script).Validate() == nil {
		return script
	}
	return AuthorMetadataOtherValue
}

func closedDecisionClass(class string) string {
	if class == coverageManual || authornorm.DecisionClass(class).Validate() == nil {
		return class
	}
	return AuthorMetadataOtherValue
}

// AuthorMetadataStats is the aggregate view of one run.
type AuthorMetadataStats struct {
	Extraction AuthorMetadataExtractionStats `json:"extraction"`
	Local      AuthorMetadataLocalStats      `json:"local"`
	Review     AuthorMetadataReviewStats     `json:"review"`
	Coverage   AuthorMetadataCoverage        `json:"coverage"`
	Credits    AuthorMetadataCreditStats     `json:"credits"`
}

// AuthorMetadataExtractionStats is the extraction stream of the run.
type AuthorMetadataExtractionStats struct {
	Total    int64 `json:"total"`
	Pending  int64 `json:"pending"`
	Leased   int64 `json:"leased"`
	Terminal int64 `json:"terminal"`
	// OldestPendingAgeS is the age of the oldest pending item, 0 without one.
	OldestPendingAgeS float64 `json:"oldest_pending_age_s"`
	// ByStatus has every terminal status, zero or not.
	ByStatus map[string]int64 `json:"by_status"`
	// ErrorClasses counts the closed classes recorded on attempts.
	ErrorClasses map[string]int64 `json:"error_classes"`
	// DurationS runs from the run's start to extraction completion, or to now
	// while extraction goes on; 0 before the run started.
	DurationS      float64 `json:"duration_s"`
	ItemsPerSecond float64 `json:"items_per_second"`
}

// AuthorMetadataLocalStats is the local normalization stream of the run's
// inputs: the jobs of the current author credits of its books.
type AuthorMetadataLocalStats struct {
	Pending           int64            `json:"pending"`
	Leased            int64            `json:"leased"`
	Completed         int64            `json:"completed"`
	Failed            int64            `json:"failed"`
	OldestPendingAgeS float64          `json:"oldest_pending_age_s" pg:"oldest_pending_age_s"`
	ErrorClasses      map[string]int64 `json:"error_classes" pg:"-"`
}

// AuthorMetadataReviewStats is the review queue of the run's credits.
type AuthorMetadataReviewStats struct {
	Open           int64            `json:"open"`
	Closed         int64            `json:"closed"`
	OldestOpenAgeS float64          `json:"oldest_open_age_s" pg:"oldest_open_age_s"`
	ByResolution   map[string]int64 `json:"by_resolution" pg:"-"`
}

// AuthorMetadataCoverage counts the run's resolved credits by the method,
// kind, script and decision class of the result they rest on.
type AuthorMetadataCoverage struct {
	ByMethod        map[string]int64 `json:"by_method"`
	ByKind          map[string]int64 `json:"by_kind"`
	ByScript        map[string]int64 `json:"by_script"`
	ByDecisionClass map[string]int64 `json:"by_decision_class"`
}

// AuthorMetadataCreditStats is the accounting of the run's current author
// credits (AuthorCreditAccountingForRun): accepted, in review, invalid,
// unresolved by closed reason, and not yet accounted.
type AuthorMetadataCreditStats struct {
	Total      int64            `json:"total"`
	Selected   int64            `json:"selected"`
	Invalid    int64            `json:"invalid"`
	Review     int64            `json:"review"`
	Pending    int64            `json:"pending"`
	Unresolved map[string]int64 `json:"unresolved"`
}

// runAuthorRowsCTE names the current author credits of the run's books (?0)
// whose snapshot is the run's extractor version: another version's snapshot
// is another input.
const runAuthorRowsCTE = `run_credits AS (
	SELECT c.id, c.source_fingerprint, s.extractor_version
	FROM book_contributor_credit c
	JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
	WHERE s.is_current AND c.role = 'author'
		AND s.extractor_version = (SELECT extractor_version FROM author_metadata_run WHERE id = ?0)
		AND s.book_id IN (SELECT book_id FROM author_metadata_run_item WHERE run_id = ?0))`

// AuthorMetadataStatsForRun reads the aggregates of one run.
func AuthorMetadataStatsForRun(ctx context.Context, db pg.DBI, runID int64) (AuthorMetadataStats, error) {
	stats := AuthorMetadataStats{}
	var err error
	if stats.Extraction, err = extractionStats(ctx, db, runID); err != nil {
		return AuthorMetadataStats{}, err
	}
	if stats.Local, err = localStats(ctx, db, runID); err != nil {
		return AuthorMetadataStats{}, err
	}
	if stats.Review, err = reviewStats(ctx, db, runID); err != nil {
		return AuthorMetadataStats{}, err
	}
	if stats.Coverage, err = coverageStats(ctx, db, runID); err != nil {
		return AuthorMetadataStats{}, err
	}
	accounting, err := AuthorCreditAccountingForRun(ctx, db, runID)
	if err != nil {
		return AuthorMetadataStats{}, err
	}
	stats.Credits = AuthorMetadataCreditStats{
		Total: int64(accounting.Credits), Selected: int64(accounting.Selected), Invalid: int64(accounting.Invalid),
		Review: int64(accounting.Review), Pending: int64(accounting.Pending), Unresolved: map[string]int64{},
	}
	for reason, n := range accounting.Unresolved {
		stats.Credits.Unresolved[string(reason)] = int64(n)
	}
	return stats, nil
}

// closedCounts projects grouped counts onto a closed set: every key passes
// through closed, and the keys it collapses add up under its fallback.
func closedCounts(counts map[string]int64, closed func(string) string) map[string]int64 {
	out := make(map[string]int64, len(counts))
	for key, n := range counts {
		out[closed(key)] += n
	}
	return out
}

// counted is one (key, count) row of a grouped aggregate.
type counted struct {
	Key   string
	Count int64
}

func groupedCounts(ctx context.Context, db pg.DBI, query string, params ...interface{}) (map[string]int64, error) {
	var rows []counted
	if _, err := db.QueryContext(ctx, &rows, query, params...); err != nil {
		return nil, fmt.Errorf("reading run aggregates: %w", err)
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Key] += r.Count
	}
	return out, nil
}

func extractionStats(ctx context.Context, db pg.DBI, runID int64) (AuthorMetadataExtractionStats, error) {
	var run struct {
		Total     int64
		Pending   int64
		Leased    int64
		Terminal  int64
		OldestAge float64
		Duration  float64
	}
	_, err := db.QueryOneContext(ctx, &run, `
		SELECT
			(SELECT count(*) FROM author_metadata_run_item WHERE run_id = r.id) AS total,
			(SELECT count(*) FROM author_metadata_run_item WHERE run_id = r.id AND status = 'pending') AS pending,
			(SELECT count(*) FROM author_metadata_run_item
				WHERE run_id = r.id AND status = 'pending' AND lease_owner IS NOT NULL) AS leased,
			(SELECT count(*) FROM author_metadata_run_item WHERE run_id = r.id AND status <> 'pending') AS terminal,
			greatest(0, coalesce(extract(epoch FROM clock_timestamp() - (
				SELECT min(created_at) FROM author_metadata_run_item
				WHERE run_id = r.id AND status = 'pending')), 0))::float8 AS oldest_age,
			greatest(0, coalesce(extract(epoch FROM
				coalesce(r.extraction_completed_at, clock_timestamp()) - r.started_at), 0))::float8 AS duration
		FROM author_metadata_run r WHERE r.id = ?`, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return AuthorMetadataExtractionStats{}, ErrRunNotFound
	}
	if err != nil {
		return AuthorMetadataExtractionStats{}, fmt.Errorf("reading the run: %w", err)
	}
	stats := AuthorMetadataExtractionStats{
		Total: run.Total, Pending: run.Pending, Leased: run.Leased, Terminal: run.Terminal,
		OldestPendingAgeS: run.OldestAge, DurationS: run.Duration, ByStatus: map[string]int64{},
	}
	if run.Duration > 0 {
		stats.ItemsPerSecond = float64(run.Terminal) / run.Duration
	}
	for _, status := range models.AuthorMetadataRunItemTerminalStatuses() {
		stats.ByStatus[string(status)] = 0
	}
	byStatus, err := groupedCounts(ctx, db, `SELECT status AS key, count(*) AS count
		FROM author_metadata_run_item WHERE run_id = ? AND status <> 'pending' GROUP BY 1`, runID)
	if err != nil {
		return AuthorMetadataExtractionStats{}, err
	}
	for status, n := range byStatus {
		stats.ByStatus[status] = n
	}
	classes, err := groupedCounts(ctx, db, `SELECT a.error_class AS key, count(*) AS count
		FROM author_metadata_run_item_attempt a
		JOIN author_metadata_run_item i ON i.id = a.run_item_id
		WHERE i.run_id = ? AND a.error_class IS NOT NULL GROUP BY 1`, runID)
	stats.ErrorClasses = closedCounts(classes, closedErrorClass)
	return stats, err
}

// runJobsCTE names the local jobs of the run's inputs: the run's sources
// under the run's extractor and normalizer versions. A job of another
// normalizer version has another normalization key (contract 3.4) — another
// input, not this run's work.
const runJobsCTE = `WITH ` + runAuthorRowsCTE + `, run_jobs AS (
	SELECT j.* FROM contributor_normalization_job j
	WHERE j.normalizer_version = (SELECT normalizer_version FROM author_metadata_run WHERE id = ?0)
		AND EXISTS (SELECT 1 FROM run_credits rc
			WHERE rc.source_fingerprint = j.source_fingerprint AND rc.extractor_version = j.extractor_version))`

func localStats(ctx context.Context, db pg.DBI, runID int64) (AuthorMetadataLocalStats, error) {
	var stats AuthorMetadataLocalStats
	_, err := db.QueryOneContext(ctx, &stats, runJobsCTE+`
		SELECT
			count(*) FILTER (WHERE status = 'pending') AS pending,
			count(*) FILTER (WHERE status = 'pending' AND lease_owner IS NOT NULL) AS leased,
			count(*) FILTER (WHERE status = 'completed') AS completed,
			count(*) FILTER (WHERE status = 'failed') AS failed,
			greatest(0, coalesce(extract(epoch FROM clock_timestamp() - min(created_at) FILTER (WHERE status = 'pending')), 0))::float8
				AS oldest_pending_age_s
		FROM run_jobs`, runID)
	if err != nil {
		return AuthorMetadataLocalStats{}, fmt.Errorf("reading the local stream: %w", err)
	}
	classes, err := groupedCounts(ctx, db, runJobsCTE+`
		SELECT a.error_class AS key, count(*) AS count
		FROM contributor_normalization_job_attempt a JOIN run_jobs j ON j.id = a.job_id
		WHERE a.error_class IS NOT NULL GROUP BY 1`, runID)
	stats.ErrorClasses = closedCounts(classes, closedErrorClass)
	return stats, err
}

// runReviewCTE names the review items of the run's credits: scoped to one of
// its credits or to the fingerprint of one.
const runReviewCTE = `WITH ` + runAuthorRowsCTE + `, run_review AS (
	SELECT i.* FROM contributor_review_item i
	WHERE i.scope_credit_id IN (SELECT id FROM run_credits)
		OR i.scope_fingerprint IN (SELECT source_fingerprint FROM run_credits))`

func reviewStats(ctx context.Context, db pg.DBI, runID int64) (AuthorMetadataReviewStats, error) {
	var stats AuthorMetadataReviewStats
	_, err := db.QueryOneContext(ctx, &stats, runReviewCTE+`
		SELECT
			count(*) FILTER (WHERE status = 'open') AS open,
			count(*) FILTER (WHERE status = 'closed') AS closed,
			greatest(0, coalesce(extract(epoch FROM clock_timestamp() - min(created_at) FILTER (WHERE status = 'open')), 0))::float8
				AS oldest_open_age_s
		FROM run_review`, runID)
	if err != nil {
		return AuthorMetadataReviewStats{}, fmt.Errorf("reading the review queue: %w", err)
	}
	stats.ByResolution, err = groupedCounts(ctx, db, runReviewCTE+`
		SELECT resolution AS key, count(*) AS count FROM run_review WHERE resolution IS NOT NULL GROUP BY 1`, runID)
	return stats, err
}

func coverageStats(ctx context.Context, db pg.DBI, runID int64) (AuthorMetadataCoverage, error) {
	resolved := `WITH ` + runAuthorRowsCTE + `
		SELECT %s AS key, count(*) AS count
		FROM run_credits rc
		JOIN book_contributor_credit_selection sel ON sel.credit_id = rc.id
		JOIN contributor_normalization_result r ON r.id = sel.result_id
		GROUP BY 1`
	var cov AuthorMetadataCoverage
	var err error
	for _, dim := range []struct {
		column string
		into   *map[string]int64
	}{
		{"r.method", &cov.ByMethod},
		{"r.kind", &cov.ByKind},
		{"coalesce(r.script, 'none')", &cov.ByScript},
		{"coalesce(r.decision_class, 'manual')", &cov.ByDecisionClass},
	} {
		if *dim.into, err = groupedCounts(ctx, db, fmt.Sprintf(resolved, dim.column), runID); err != nil {
			return AuthorMetadataCoverage{}, err
		}
	}
	// Script and decision class are checked only for their shape by the
	// schema; project them onto the domain's sets like the error classes.
	cov.ByScript = closedCounts(cov.ByScript, closedScript)
	cov.ByDecisionClass = closedCounts(cov.ByDecisionClass, closedDecisionClass)
	return cov, nil
}
