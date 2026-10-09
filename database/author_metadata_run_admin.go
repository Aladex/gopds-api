package database

import (
	"context"
	"errors"
	"fmt"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The administrator's side of the run state machine (contract 3.9, admin API
// phase 16): reading one run with the numbers of its three stages, the pilot
// approval a full run needs, and the retry that reopens a run's failed rows.
// Every number is read from the rows themselves, on the database clock.

var (
	// ErrRunNotFound marks a run ID with no row.
	ErrRunNotFound = errors.New("database: author metadata run not found")
	// ErrNotACompletedPilot marks an approval of a run that is not a
	// completed pilot_archive run with at least one item.
	ErrNotACompletedPilot = errors.New("database: only a completed pilot with items can be approved")
	// ErrPilotAlreadyApproved marks a second approval of the same pilot.
	ErrPilotAlreadyApproved = errors.New("database: the pilot is already approved")
)

// pilotApprovalRunKey is the unique constraint of one approval per run.
const pilotApprovalRunKey = "author_metadata_pilot_approval_run_key"

// LoadRun reads one run.
func LoadRun(ctx context.Context, db pg.DBI, id int64) (*models.AuthorMetadataRun, error) {
	run := &models.AuthorMetadataRun{}
	err := db.ModelContext(ctx, run).Where("id = ?", id).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading the run: %w", err)
	}
	return run, nil
}

// RunApproved reports whether the run is an approved pilot.
func RunApproved(ctx context.Context, db pg.DBI, id int64) (bool, error) {
	var approved bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&approved),
		`SELECT EXISTS (SELECT 1 FROM author_metadata_pilot_approval WHERE run_id = ?)`, id)
	if err != nil {
		return false, fmt.Errorf("reading the run's approval: %w", err)
	}
	return approved, nil
}

// ApprovePilotRun records the administrator's approval of a completed pilot,
// with the run's exact versions. The run row is locked for the check, so a
// retry cannot reopen it between the check and the insert; the approval's
// composite foreign key pins the run's mode, status and versions afterwards.
// A pilot with no items extracted nothing and approves nothing.
func ApprovePilotRun(ctx context.Context, db *pg.DB, runID, actorUserID int64) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var run struct {
			Mode              models.AuthorMetadataRunMode
			Status            models.AuthorMetadataRunStatus
			ExtractorVersion  string
			NormalizerVersion string
			ItemsTotal        int
		}
		_, err := tx.QueryOneContext(ctx, &run, `SELECT mode, status, extractor_version, normalizer_version, items_total
			FROM author_metadata_run WHERE id = ? FOR UPDATE`, runID)
		if errors.Is(err, pg.ErrNoRows) {
			return ErrRunNotFound
		}
		if err != nil {
			return fmt.Errorf("locking the run: %w", err)
		}
		if run.Mode != models.AuthorMetadataRunPilotArchive || run.Status != models.AuthorMetadataRunCompleted ||
			run.ItemsTotal == 0 {
			return ErrNotACompletedPilot
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO author_metadata_pilot_approval
			(run_id, extractor_version, normalizer_version, approved_by_user_id)
			VALUES (?, ?, ?, ?)`, runID, run.ExtractorVersion, run.NormalizerVersion, actorUserID)
		var pgErr pg.Error
		if errors.As(err, &pgErr) && pgErr.Field('C') == sqlstateUniqueViolation && pgErr.Field('n') == pilotApprovalRunKey {
			return ErrPilotAlreadyApproved
		}
		if err != nil {
			return fmt.Errorf("approving the pilot: %w", err)
		}
		return nil
	})
}

// ExtractionStageStats is the extraction stream of one run.
type ExtractionStageStats struct {
	Total             int64
	Done              int64
	Pending           int64
	Leased            int64
	OldestPendingAgeS int64
	ByStatus          map[models.AuthorMetadataRunItemStatus]int64
	// CurrentArchive is the archive of the item leased most recently; nil
	// when nothing is leased.
	CurrentArchive *string
	// ItemsPerMinute is the terminal items per minute since the run started,
	// up to the end of extraction.
	ItemsPerMinute float64
}

// LocalStageStats is the local normalization stream of a run's inputs.
type LocalStageStats struct {
	Total             int64 `pg:"total"`
	Done              int64 `pg:"done"`
	Pending           int64 `pg:"pending"`
	Leased            int64 `pg:"leased"`
	Failed            int64 `pg:"failed"`
	OldestPendingAgeS int64 `pg:"oldest_pending_age_s"`
}

// ReviewStageStats is the review backlog of a run's inputs.
type ReviewStageStats struct {
	Open   int64 `pg:"open"`
	Closed int64 `pg:"closed"`
}

// RunStageStats are the three streams of one run (scope amendment A6).
type RunStageStats struct {
	Extraction ExtractionStageStats
	Local      LocalStageStats
	Review     ReviewStageStats
}

// runInputsCTE is the normalization inputs of a run: the fingerprint and
// extractor version of every author credit of the current snapshots of the
// run's books — the credits AuthorCreditAccountingForRun accounts. ?0 is the
// run ID.
const runInputsCTE = `run_credits AS (
	SELECT c.id, c.source_fingerprint, s.extractor_version
	FROM book_contributor_credit c
	JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
	WHERE s.is_current AND c.role = 'author'
		AND s.book_id IN (SELECT book_id FROM author_metadata_run_item WHERE run_id = ?0)
), run_inputs AS (
	SELECT DISTINCT source_fingerprint, extractor_version FROM run_credits
)`

const extractionStatsSQL = `
SELECT count(*) AS total,
	count(*) FILTER (WHERE i.status <> 'pending') AS done,
	count(*) FILTER (WHERE i.status = 'pending'
		AND (i.lease_expires_at IS NULL OR i.lease_expires_at <= clock_timestamp())) AS pending,
	count(*) FILTER (WHERE i.status = 'pending' AND i.lease_expires_at > clock_timestamp()) AS leased,
	coalesce(floor(extract(epoch FROM clock_timestamp()
		- min(i.created_at) FILTER (WHERE i.status = 'pending')))::bigint, 0) AS oldest_pending_age_s,
	(SELECT b.path FROM author_metadata_run_item l JOIN opds_catalog_book b ON b.id = l.book_id
		WHERE l.run_id = ?0 AND l.status = 'pending' AND l.lease_expires_at > clock_timestamp()
		ORDER BY l.lease_expires_at DESC, l.id DESC LIMIT 1) AS current_archive,
	coalesce((SELECT extract(epoch FROM coalesce(r.extraction_completed_at, r.finished_at, clock_timestamp())
		- r.started_at) FROM author_metadata_run r WHERE r.id = ?0), 0) AS elapsed_s
FROM author_metadata_run_item i
WHERE i.run_id = ?0`

const localStatsSQL = `WITH ` + runInputsCTE + `
SELECT count(j.id) AS total,
	count(j.id) FILTER (WHERE j.status = 'completed') AS done,
	count(j.id) FILTER (WHERE j.status = 'pending'
		AND (j.lease_expires_at IS NULL OR j.lease_expires_at <= clock_timestamp())) AS pending,
	count(j.id) FILTER (WHERE j.status = 'pending' AND j.lease_expires_at > clock_timestamp()) AS leased,
	count(j.id) FILTER (WHERE j.status = 'failed') AS failed,
	coalesce(floor(extract(epoch FROM clock_timestamp()
		- min(j.created_at) FILTER (WHERE j.status = 'pending')))::bigint, 0) AS oldest_pending_age_s
FROM run_inputs ri
JOIN contributor_normalization_job j
	ON j.source_fingerprint = ri.source_fingerprint AND j.extractor_version = ri.extractor_version
	AND j.normalizer_version = ?1`

const reviewStatsSQL = `WITH ` + runInputsCTE + `
SELECT count(*) FILTER (WHERE i.status = 'open') AS open,
	count(*) FILTER (WHERE i.status = 'closed') AS closed
FROM contributor_review_item i
WHERE i.scope_fingerprint IN (SELECT source_fingerprint FROM run_inputs)
	OR i.scope_credit_id IN (SELECT id FROM run_credits)`

// secondsPerMinute converts the elapsed seconds into the throughput unit.
const secondsPerMinute = 60

// LoadRunStageStats counts the three streams of a run.
func LoadRunStageStats(ctx context.Context, db pg.DBI, run *models.AuthorMetadataRun) (RunStageStats, error) {
	var stats RunStageStats
	var extraction struct {
		Total             int64   `pg:"total"`
		Done              int64   `pg:"done"`
		Pending           int64   `pg:"pending"`
		Leased            int64   `pg:"leased"`
		OldestPendingAgeS int64   `pg:"oldest_pending_age_s"`
		CurrentArchive    *string `pg:"current_archive"`
		ElapsedS          float64 `pg:"elapsed_s"`
	}
	if _, err := db.QueryOneContext(ctx, &extraction, extractionStatsSQL, run.ID); err != nil {
		return RunStageStats{}, fmt.Errorf("counting the extraction stage: %w", err)
	}
	stats.Extraction = ExtractionStageStats{
		Total: extraction.Total, Done: extraction.Done, Pending: extraction.Pending, Leased: extraction.Leased,
		OldestPendingAgeS: extraction.OldestPendingAgeS, CurrentArchive: extraction.CurrentArchive,
	}
	if extraction.ElapsedS > 0 {
		stats.Extraction.ItemsPerMinute = float64(stats.Extraction.Done) / (extraction.ElapsedS / secondsPerMinute)
	}

	var byStatus []struct {
		Status models.AuthorMetadataRunItemStatus
		N      int64
	}
	if _, err := db.QueryContext(ctx, &byStatus, `SELECT status, count(*) AS n FROM author_metadata_run_item
		WHERE run_id = ? AND status <> 'pending' GROUP BY status`, run.ID); err != nil {
		return RunStageStats{}, fmt.Errorf("counting the extraction statuses: %w", err)
	}
	stats.Extraction.ByStatus = make(map[models.AuthorMetadataRunItemStatus]int64, len(byStatus))
	for _, row := range byStatus {
		stats.Extraction.ByStatus[row.Status] = row.N
	}

	if _, err := db.QueryOneContext(ctx, &stats.Local, localStatsSQL, run.ID, run.NormalizerVersion); err != nil {
		return RunStageStats{}, fmt.Errorf("counting the local stage: %w", err)
	}
	if _, err := db.QueryOneContext(ctx, &stats.Review, reviewStatsSQL, run.ID); err != nil {
		return RunStageStats{}, fmt.Errorf("counting the review stage: %w", err)
	}
	return stats, nil
}

// RunReportFacts are the report figures beyond the stage numbers.
type RunReportFacts struct {
	// DurationS is the run's wall time: started to finished, or to now.
	DurationS int64
	// DBGrowthBytes is the stored size of the snapshot and credit rows the
	// run's extraction wrote — the per-run storage; results and jobs are
	// shared by key across runs.
	DBGrowthBytes int64
	// ByClass and ByScript count the run's current author credits by the
	// decision class and the script of the result their resolution rests on.
	ByClass  map[string]int64
	ByScript map[string]int64
}

const runGrowthSQL = `
SELECT coalesce((SELECT sum(pg_column_size(s.*)) FROM book_metadata_snapshot s WHERE s.run_id = ?0), 0)
	+ coalesce((SELECT sum(pg_column_size(c.*)) FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.run_id = ?0), 0) AS growth,
	coalesce((SELECT floor(extract(epoch FROM coalesce(r.finished_at, clock_timestamp()) - r.started_at))::bigint
		FROM author_metadata_run r WHERE r.id = ?0), 0) AS duration_s`

const runResultGroupsSQL = `WITH ` + runInputsCTE + `
SELECT r.decision_class, r.script, count(*) AS n
FROM run_credits rc
JOIN book_contributor_credit_selection sel ON sel.credit_id = rc.id
JOIN contributor_normalization_result r ON r.id = sel.result_id
GROUP BY 1, 2`

// LoadRunReportFacts reads the report figures of a run.
func LoadRunReportFacts(ctx context.Context, db pg.DBI, runID int64) (RunReportFacts, error) {
	var head struct {
		Growth    int64 `pg:"growth"`
		DurationS int64 `pg:"duration_s"`
	}
	if _, err := db.QueryOneContext(ctx, &head, runGrowthSQL, runID); err != nil {
		return RunReportFacts{}, fmt.Errorf("measuring the run: %w", err)
	}
	facts := RunReportFacts{
		DurationS: max(head.DurationS, 0), DBGrowthBytes: head.Growth,
		ByClass: map[string]int64{}, ByScript: map[string]int64{},
	}
	var groups []struct {
		DecisionClass *string
		Script        *string
		N             int64
	}
	if _, err := db.QueryContext(ctx, &groups, runResultGroupsSQL, runID); err != nil {
		return RunReportFacts{}, fmt.Errorf("grouping the run's results: %w", err)
	}
	for _, g := range groups {
		if g.DecisionClass != nil {
			facts.ByClass[*g.DecisionClass] += g.N
		}
		if g.Script != nil {
			facts.ByScript[*g.Script] += g.N
		}
	}
	return facts, nil
}

// retryRunStatuses are the run statuses a retry may reopen rows of: an
// active run keeps going, a completed one goes back to running.
var retryRunStatuses = map[models.AuthorMetadataRunStatus]bool{
	models.AuthorMetadataRunPending:   true,
	models.AuthorMetadataRunRunning:   true,
	models.AuthorMetadataRunPaused:    true,
	models.AuthorMetadataRunCompleted: true,
}

// reopenExtractionSQL reopens the run's items that ended in a failure — a
// terminal status without a snapshot, the schema's own definition — with the
// class as that status or as the error class of any of their attempts, and
// that the workers' budget still allows a claim of. Attempt identities and
// history stay: only the item's progress columns change. ?0 run, ?1 class,
// ?2 budget.
const reopenExtractionSQL = `UPDATE author_metadata_run_item i
SET status = 'pending', lease_owner = NULL, lease_expires_at = NULL,
	next_attempt_at = clock_timestamp(), finished_at = NULL
WHERE i.run_id = ?0 AND i.status <> 'pending' AND i.snapshot_id IS NULL
	AND i.attempt_count < ?2
	AND (i.status = ?1 OR EXISTS (SELECT 1 FROM author_metadata_run_item_attempt a
		WHERE a.run_item_id = i.id AND a.error_class = ?1))`

// reopenLocalSQL reopens the failed local jobs of the run's inputs and the
// run's normalizer version that failed with the class on any attempt and
// that the workers' budget still allows a claim of. ?0 run, ?1 class,
// ?2 budget, ?3 normalizer version.
const reopenLocalSQL = `WITH ` + runInputsCTE + `
UPDATE contributor_normalization_job j
SET status = 'pending', last_error_class = NULL, lease_owner = NULL, lease_expires_at = NULL,
	next_attempt_at = clock_timestamp(), finished_at = NULL
FROM run_inputs ri
WHERE j.source_fingerprint = ri.source_fingerprint AND j.extractor_version = ri.extractor_version
	AND j.normalizer_version = ?3 AND j.status = 'failed' AND j.attempt_count < ?2
	AND (j.last_error_class = ?1 OR EXISTS (SELECT 1 FROM contributor_normalization_job_attempt a
		WHERE a.job_id = j.id AND a.error_class = ?1))`

// RetryStream selects the rows a retry reopens.
type RetryStream int

const (
	// RetryExtraction reopens run items.
	RetryExtraction RetryStream = iota + 1
	// RetryLocal reopens local normalization jobs of the run's inputs.
	RetryLocal
)

// ReopenRunRows reopens the run's failed rows of one stream that failed with
// errorClass and that a worker with maxAttempts can still claim, in one
// transaction, and reports how many it reopened.
//
// Attempt identities are immutable and the claim compares the same counter
// with the budget, so a row that used every attempt stays as it is — it
// counts zero until the operator raises the budget. When at least one row
// reopens, a completed run goes back to running (extraction_completed_at
// and finished_at cleared, the attempt history kept); the single active slot
// may refuse that with ErrActiveRunExists, and an approved pilot, whose
// approval pins its status, or a run ended failed_systemic is
// ErrRunTransitionConflict. When nothing reopens, nothing changes.
//
// The run row is locked first, so an approval cannot pin it between the
// check and the update. That cannot wait on a worker: the rows a retry
// reopens are terminal, and no worker holds a terminal row — an item a
// worker is ending is still pending in the version the update reads, so the
// update skips it instead of waiting for it.
func ReopenRunRows(
	ctx context.Context, db *pg.DB, runID int64, stream RetryStream, errorClass string, maxAttempts int,
) (int64, error) {
	if !ValidLeaseErrorClass(errorClass) || maxAttempts <= 0 {
		return 0, fmt.Errorf("%w: retry class or budget", ErrInvalidLeaseFailure)
	}
	var reopened int64
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		run := &models.AuthorMetadataRun{}
		err := tx.ModelContext(ctx, run).Where("id = ?", runID).For("UPDATE").Select()
		if errors.Is(err, pg.ErrNoRows) {
			return ErrRunNotFound
		}
		if err != nil {
			return fmt.Errorf("locking the run: %w", err)
		}
		if !retryRunStatuses[run.Status] {
			return ErrRunTransitionConflict
		}
		if run.Status == models.AuthorMetadataRunCompleted {
			approved, approvedErr := RunApproved(ctx, tx, runID)
			if approvedErr != nil {
				return approvedErr
			}
			if approved {
				return ErrRunTransitionConflict
			}
		}

		var res pg.Result
		switch stream {
		case RetryExtraction:
			res, err = tx.ExecContext(ctx, reopenExtractionSQL, runID, errorClass, maxAttempts)
		case RetryLocal:
			res, err = tx.ExecContext(ctx, reopenLocalSQL, runID, errorClass, maxAttempts, run.NormalizerVersion)
		default:
			return fmt.Errorf("%w: unknown retry stream", ErrInvalidLeaseFailure)
		}
		if err != nil {
			return fmt.Errorf("reopening the run's rows: %w", err)
		}
		reopened = int64(res.RowsAffected())
		if reopened == 0 {
			return nil
		}
		return reopenRun(ctx, tx, run, stream, reopened)
	})
	if err != nil {
		return 0, err
	}
	return reopened, nil
}

// reopenRun updates the run after rows reopened: reopened items are no longer
// terminal, so extraction is not complete; a completed run goes back to
// running.
func reopenRun(ctx context.Context, tx *pg.Tx, run *models.AuthorMetadataRun, stream RetryStream, reopened int64) error {
	itemsBack := int64(0)
	if stream == RetryExtraction {
		itemsBack = reopened
	}
	res, err := tx.ExecContext(ctx, `UPDATE author_metadata_run
		SET items_terminal = items_terminal - ?0,
			extraction_completed_at = CASE WHEN ?0 > 0 THEN NULL ELSE extraction_completed_at END,
			status = CASE WHEN status = 'completed' THEN 'running' ELSE status END,
			finished_at = CASE WHEN status = 'completed' THEN NULL ELSE finished_at END
		WHERE id = ?1 AND status = ?2`, itemsBack, run.ID, run.Status)
	var pgErr pg.Error
	if errors.As(err, &pgErr) && pgErr.Field('C') == sqlstateUniqueViolation && pgErr.Field('n') == oneActiveRunIndex {
		return ErrActiveRunExists
	}
	if err != nil {
		return fmt.Errorf("reopening the run: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrRunTransitionConflict
	}
	return nil
}
