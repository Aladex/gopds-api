package database

import (
	"context"
	"errors"
	"fmt"
	"math"

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

// ExtractionStageStats is the extraction stream of one run in the contract's
// shape.
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
	Total             int64
	Done              int64
	Pending           int64
	Leased            int64
	Failed            int64
	OldestPendingAgeS int64
}

// ReviewStageStats is the review backlog of a run's inputs.
type ReviewStageStats struct {
	Open   int64
	Closed int64
}

// RunStageStats are the three streams of one run (scope amendment A6).
type RunStageStats struct {
	Extraction ExtractionStageStats
	Local      LocalStageStats
	Review     ReviewStageStats
}

// secondsPerMinute converts the stats' throughput into the contract's unit.
const secondsPerMinute = 60

// RunStageStatsFrom shapes the run's aggregates (AuthorMetadataStatsForRun)
// as the contract's stages: the status shows the same numbers the stats
// read, by the same definitions — a live lease, the run's version-pinned
// inputs. Only the archive being extracted is not an aggregate; the caller
// reads it with RunCurrentArchive.
func RunStageStatsFrom(stats *AuthorMetadataStats, currentArchive *string) RunStageStats {
	ex := &stats.Extraction
	byStatus := make(map[models.AuthorMetadataRunItemStatus]int64, len(ex.ByStatus))
	for status, n := range ex.ByStatus {
		byStatus[models.AuthorMetadataRunItemStatus(status)] = n
	}
	local := &stats.Local
	return RunStageStats{
		Extraction: ExtractionStageStats{
			Total: ex.Total, Done: ex.Terminal, Pending: ex.Pending, Leased: ex.Leased,
			OldestPendingAgeS: wholeSeconds(ex.OldestPendingAgeS), ByStatus: byStatus,
			CurrentArchive: currentArchive, ItemsPerMinute: ex.ItemsPerSecond * secondsPerMinute,
		},
		Local: LocalStageStats{
			Total: local.Pending + local.Leased + local.Completed + local.Failed,
			Done:  local.Completed, Pending: local.Pending, Leased: local.Leased, Failed: local.Failed,
			OldestPendingAgeS: wholeSeconds(local.OldestPendingAgeS),
		},
		Review: ReviewStageStats{Open: stats.Review.Open, Closed: stats.Review.Closed},
	}
}

// wholeSeconds is an age in the contract's whole seconds.
func wholeSeconds(s float64) int64 { return int64(math.Floor(s)) }

// RunCurrentArchive reads the archive of the run's item leased most
// recently, nil when nothing is leased.
func RunCurrentArchive(ctx context.Context, db pg.DBI, runID int64) (*string, error) {
	var archive struct {
		Path *string
	}
	_, err := db.QueryOneContext(ctx, &archive, `SELECT (SELECT b.path FROM author_metadata_run_item l
		JOIN opds_catalog_book b ON b.id = l.book_id
		WHERE l.run_id = ? AND l.status = 'pending' AND l.lease_expires_at > clock_timestamp()
		ORDER BY l.lease_expires_at DESC, l.id DESC LIMIT 1) AS path`, runID)
	if err != nil {
		return nil, fmt.Errorf("reading the current archive: %w", err)
	}
	return archive.Path, nil
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
	// decision class and the script of the result their resolution rests on:
	// the run's coverage (AuthorMetadataCoverage.ResultClasses).
	ByClass  map[string]int64
	ByScript map[string]int64
}

const runGrowthSQL = `
SELECT coalesce((SELECT sum(pg_column_size(s.*)) FROM book_metadata_snapshot s WHERE s.run_id = ?0), 0)
	+ coalesce((SELECT sum(pg_column_size(c.*)) FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.run_id = ?0), 0) AS growth,
	coalesce((SELECT floor(extract(epoch FROM coalesce(r.finished_at, clock_timestamp()) - r.started_at))::bigint
		FROM author_metadata_run r WHERE r.id = ?0), 0) AS duration_s`

// LoadRunReportFacts reads the report figures of a run; the result classes
// come from the run's coverage, read with its stats.
func LoadRunReportFacts(ctx context.Context, db pg.DBI, runID int64, coverage *AuthorMetadataCoverage) (RunReportFacts, error) {
	var head struct {
		Growth    int64 `pg:"growth"`
		DurationS int64 `pg:"duration_s"`
	}
	if _, err := db.QueryOneContext(ctx, &head, runGrowthSQL, runID); err != nil {
		return RunReportFacts{}, fmt.Errorf("measuring the run: %w", err)
	}
	facts := RunReportFacts{DurationS: max(head.DurationS, 0), DBGrowthBytes: head.Growth}
	facts.ByClass, facts.ByScript = coverage.ResultClasses()
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

// reopenLocalSQL reopens the failed local jobs of the run's inputs — the
// stats' run_jobs, so a retry reopens exactly the jobs the status counts:
// the run's sources under its extractor and normalizer versions — that
// failed with the class on any attempt and that the workers' budget still
// allows a claim of. ?0 run, ?1 class, ?2 budget.
const reopenLocalSQL = runJobsCTE + `
UPDATE contributor_normalization_job j
SET status = 'pending', last_error_class = NULL, lease_owner = NULL, lease_expires_at = NULL,
	next_attempt_at = clock_timestamp(), finished_at = NULL
WHERE j.id IN (SELECT id FROM run_jobs) AND j.status = 'failed' AND j.attempt_count < ?2
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
			res, err = tx.ExecContext(ctx, reopenLocalSQL, runID, errorClass, maxAttempts)
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
