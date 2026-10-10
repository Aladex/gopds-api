package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// What keeps a catalog-sized run cheap to start and to watch (migration 28).
//
// A full run starts without its items: CreateSeedingRun records it pending
// with a seed cursor and takes the single active slot at once, and the run's
// own extraction loop seeds it with SeedRunBatch — each batch inserts the
// next books after the cursor and moves the cursor in the same transaction,
// so a batch is either all there or not at all, and the last one starts the
// run. Item counts by status come from the tally the item triggers maintain
// (RunItemCounts), never from counting the items. The credit-scale figures
// of a run are stored with the time they stand for (StoreRunAggregates).

// CreateSeedingRun records a full run that its extraction loop will seed:
// pending, no item yet, the cursor before the first book and the catalog's
// size as the target of the seeding progress. The run takes the single
// active slot here, so a second start is ErrActiveRunExists from the unique
// index while this one is still being seeded.
func CreateSeedingRun(ctx context.Context, db pg.DBI, run *models.AuthorMetadataRun) error {
	if run == nil || run.Mode != models.AuthorMetadataRunFull ||
		run.SelectorArchive != nil || len(run.SelectorBookIDs) > 0 {
		return ErrInvalidRunSelector
	}
	for _, version := range []string{run.ExtractorVersion, run.NormalizerVersion} {
		if strings.TrimSpace(version) == "" {
			return authornorm.ErrEmptyVersion
		}
	}
	var cursor int64
	var target int
	_, err := db.QueryOneContext(ctx, pg.Scan(&run.ID, &cursor, &target), `
		INSERT INTO author_metadata_run
			(mode, status, extractor_version, normalizer_version, created_by_user_id, seed_cursor, seed_target)
		VALUES ('full', 'pending', ?, ?, ?, 0, (SELECT count(*) FROM opds_catalog_book))
		RETURNING id, seed_cursor, seed_target`,
		run.ExtractorVersion, run.NormalizerVersion, run.CreatedByUserID)
	if err != nil {
		return translateRunSeedError(err)
	}
	run.Status = models.AuthorMetadataRunPending
	run.SeedCursor, run.SeedTarget = &cursor, &target
	return nil
}

// SeedRunBatch seeds up to limit more books of a full run being seeded: the
// books after the cursor, in ID order. The run row is locked first, so two
// seeders take turns, and the items, the total and the cursor commit
// together — a batch that dies before its commit leaves the run as it was,
// and no book is seeded twice. A batch shorter than limit is the last one: it
// clears the cursor and starts the run. A run that is not being seeded is
// ErrRunTransitionConflict. On a *pg.DB the batch opens its own transaction;
// a caller's transaction is joined as-is. It reports how many items it added
// and whether the run is seeded now.
func SeedRunBatch(ctx context.Context, db pg.DBI, runID int64, limit int) (seeded int, done bool, err error) {
	if limit <= 0 {
		return 0, false, fmt.Errorf("%w: seed batch size", ErrInvalidLeaseOptions)
	}
	if pool, ok := db.(*pg.DB); ok {
		err = pool.RunInTransaction(ctx, func(tx *pg.Tx) error {
			var txErr error
			seeded, done, txErr = seedRunBatch(ctx, tx, runID, limit)
			return txErr
		})
		return seeded, done, err
	}
	return seedRunBatch(ctx, db, runID, limit)
}

func seedRunBatch(ctx context.Context, db pg.DBI, runID int64, limit int) (seeded int, done bool, err error) {
	var cursor int64
	_, err = db.QueryOneContext(ctx, pg.Scan(&cursor), `
		SELECT seed_cursor FROM author_metadata_run
		WHERE id = ? AND status = 'pending' AND seed_cursor IS NOT NULL
		FOR UPDATE`, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return 0, false, ErrRunTransitionConflict
	}
	if err != nil {
		return 0, false, fmt.Errorf("locking the seeding run: %w", err)
	}
	// The books are locked like any item insert's foreign key would lock
	// them, before the insert: a book an archive deletion holds is waited
	// for and then skipped, never a failed batch.
	var last int64
	_, err = db.QueryOneContext(ctx, pg.Scan(&seeded, &last), `
		WITH batch AS (
			SELECT id FROM opds_catalog_book WHERE id > ?1 ORDER BY id LIMIT ?2 FOR KEY SHARE
		), seeded AS (
			INSERT INTO author_metadata_run_item (run_id, book_id)
			SELECT ?0, id FROM batch ORDER BY id
			RETURNING book_id
		)
		SELECT count(*), coalesce(max(book_id), ?1) FROM seeded`, runID, cursor, limit)
	if err != nil {
		return 0, false, fmt.Errorf("seeding a batch of run items: %w", err)
	}
	done = seeded < limit
	if done {
		_, err = db.ExecContext(ctx, `
			UPDATE author_metadata_run
			SET items_total = items_total + ?1, seed_cursor = NULL, status = 'running',
				started_at = clock_timestamp()
			WHERE id = ?0`, runID, seeded)
	} else {
		_, err = db.ExecContext(ctx, `
			UPDATE author_metadata_run SET items_total = items_total + ?1, seed_cursor = ?2
			WHERE id = ?0`, runID, seeded, last)
	}
	if err != nil {
		return 0, false, fmt.Errorf("advancing the seed cursor: %w", err)
	}
	return seeded, done, nil
}

// RunItemCounts reads the run's items by status from the tally: the item
// triggers add a signed delta for every statement that inserts, changes or
// deletes items, so the sums are the item rows' own counts in any snapshot,
// without reading the items. A status with no item is absent.
func RunItemCounts(ctx context.Context, db pg.DBI, runID int64) (map[models.AuthorMetadataRunItemStatus]int64, error) {
	var rows []struct {
		Status models.AuthorMetadataRunItemStatus
		Items  int64
	}
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT status, sum(items) AS items FROM author_metadata_run_item_tally
		WHERE run_id = ? GROUP BY status HAVING sum(items) <> 0`, runID); err != nil {
		return nil, fmt.Errorf("reading the run's item counts: %w", err)
	}
	counts := make(map[models.AuthorMetadataRunItemStatus]int64, len(rows))
	for _, r := range rows {
		counts[r.Status] = r.Items
	}
	return counts, nil
}

// tallyLockClass is the advisory lock class of folding a run's tally
// ('tlly'), in the two-key form; the second key is the run.
const tallyLockClass int32 = 0x746c6c79

// CompactRunItemTally folds the run's tally into one row per status. The
// sums do not change: the rows it deletes are the rows it adds up, and a
// delta committed meanwhile is not in its snapshot, so it stays. One folder
// at a time per run (an advisory try-lock); a second one skips.
func CompactRunItemTally(ctx context.Context, db pg.DBI, runID int64) error {
	_, err := db.ExecContext(ctx, `
		WITH locked AS MATERIALIZED (
			SELECT pg_try_advisory_xact_lock(?1, (?0 % 2147483647)::int) AS ok
		), folded AS (
			DELETE FROM author_metadata_run_item_tally t
			WHERE t.run_id = ?0 AND (SELECT ok FROM locked)
			RETURNING t.status, t.items
		)
		INSERT INTO author_metadata_run_item_tally (run_id, status, items)
		SELECT ?0, status, sum(items) FROM folded GROUP BY status HAVING sum(items) <> 0`,
		runID, tallyLockClass)
	if err != nil {
		return fmt.Errorf("folding the run's item tally: %w", err)
	}
	return nil
}

// archiveFailureStatuses are the per-book statuses of a broken archive.
var archiveFailureStatuses = []string{
	string(models.AuthorMetadataRunItemArchiveMissing),
	string(models.AuthorMetadataRunItemArchiveUnreadable),
}

// RunProblemArchive is one archive the run found missing or unreadable: its
// file name as the catalog stores it, the reason, and how many of the
// run's books it holds.
type RunProblemArchive struct {
	Archive string
	Reason  models.AuthorMetadataRunItemStatus
	Books   int64
	// Deletion is the pending deletion of the archive's book records, if one
	// was requested; nil otherwise.
	Deletion *ArchiveDeletionProgress `pg:"-"`
}

// ArchiveDeletionProgress is how far a pending archive deletion is.
type ArchiveDeletionProgress struct {
	Deleted int64
	Total   int64
}

// RunProblemArchives lists up to limit archives whose books the run ended
// archive_missing or archive_unreadable, the most books first. A book that
// failed on its own (entry_missing, invalid_fb2, ...) is not an archive
// problem.
func RunProblemArchives(ctx context.Context, db pg.DBI, runID int64, limit int) ([]RunProblemArchive, error) {
	archives := []RunProblemArchive{}
	if _, err := db.QueryContext(ctx, &archives, `
		SELECT b.path AS archive, i.status AS reason, count(*) AS books
		FROM author_metadata_run_item i
		JOIN opds_catalog_book b ON b.id = i.book_id
		WHERE i.run_id = ? AND i.status IN (?)
		GROUP BY 1, 2
		ORDER BY 3 DESC, 1, 2
		LIMIT ?`, runID, pg.In(archiveFailureStatuses), limit); err != nil {
		return nil, fmt.Errorf("listing the run's problem archives: %w", err)
	}
	if len(archives) == 0 {
		return archives, nil
	}
	names := make([]string, 0, len(archives))
	for _, a := range archives {
		names = append(names, a.Archive)
	}
	var pending []ArchiveDeletion
	if _, err := db.QueryContext(ctx, &pending, `SELECT `+archiveDeletionColumns+`
		FROM author_metadata_archive_deletion WHERE status = 'pending' AND archive IN (?)`, pg.In(names)); err != nil {
		return nil, fmt.Errorf("reading the pending archive deletions: %w", err)
	}
	for i := range archives {
		for _, d := range pending {
			if d.Archive == archives[i].Archive {
				archives[i].Deletion = &ArchiveDeletionProgress{Deleted: d.BooksDeleted, Total: d.BooksTotal}
			}
		}
	}
	return archives, nil
}

// RunArchiveFailed reports whether the run ended any book of the archive
// archive_missing or archive_unreadable.
func RunArchiveFailed(ctx context.Context, db pg.DBI, runID int64, archive string) (bool, error) {
	var failed bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&failed), `
		SELECT EXISTS (SELECT 1 FROM author_metadata_run_item i
			JOIN opds_catalog_book b ON b.id = i.book_id
			WHERE i.run_id = ? AND i.status IN (?) AND b.path = ?)`,
		runID, pg.In(archiveFailureStatuses), archive)
	if err != nil {
		return false, fmt.Errorf("checking the run's archive: %w", err)
	}
	return failed, nil
}

// reopenArchiveSQL reopens the run's books of one archive that ended
// archive_missing or archive_unreadable and that the workers' budget still
// allows a claim of. ?0 run, ?1 archive, ?2 budget, ?3 the statuses.
const reopenArchiveSQL = `UPDATE author_metadata_run_item i
SET status = 'pending', lease_owner = NULL, lease_expires_at = NULL,
	next_attempt_at = clock_timestamp(), finished_at = NULL
WHERE i.run_id = ?0 AND i.status IN (?3) AND i.attempt_count < ?2
	AND i.book_id IN (SELECT id FROM opds_catalog_book WHERE path = ?1)`

// ReopenRunArchive reopens the run's books of one broken archive — after the
// file came back, say — by the rules of ReopenRunRows: only items the budget
// still allows a claim of, a completed run back to running, and nothing
// changed when nothing reopens.
func ReopenRunArchive(ctx context.Context, db *pg.DB, runID int64, archive string, maxAttempts int) (int64, error) {
	if maxAttempts <= 0 {
		return 0, fmt.Errorf("%w: retry budget", ErrInvalidLeaseFailure)
	}
	return reopenRunRowsWith(ctx, db, runID, RetryExtraction, func(tx *pg.Tx) (pg.Result, error) {
		return tx.ExecContext(ctx, reopenArchiveSQL, runID, archive, maxAttempts, pg.In(archiveFailureStatuses))
	})
}

// DeleteArchiveBooks deletes the archive's books with everything that hangs
// off them — their author metadata layer and run items among them — in one
// transaction: the same deletion the scanning section's archive reset makes.
// Fingerprint decisions stay (migration 26). It reports the books deleted.
func DeleteArchiveBooks(ctx context.Context, db *pg.DB, archive string) (int, error) {
	var deleted int
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var txErr error
		deleted, txErr = deleteArchiveBooks(tx, archive)
		return txErr
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// RunAggregates are the credit-scale figures of a run: its local stream,
// review backlog, coverage and credit accounting, and the storage its
// extraction wrote. They read every current author credit of the run's
// books, which for a catalog-sized run is too much for a status read; such
// a run reads them stored, with the time they stand for.
type RunAggregates struct {
	Local       AuthorMetadataLocalStats  `json:"local"`
	Review      AuthorMetadataReviewStats `json:"review"`
	Coverage    AuthorMetadataCoverage    `json:"coverage"`
	Credits     AuthorMetadataCreditStats `json:"credits"`
	GrowthBytes int64                     `json:"growth_bytes"`
}

// StoredRunAggregates are a run's stored figures as a status read finds
// them: the figures, when they were computed and their age on the database
// clock. They are for display; a decision accounts the credits itself.
type StoredRunAggregates struct {
	Figures    RunAggregates
	ComputedAt time.Time
	AgeS       float64
}

// withMaps makes every map of the figures non-nil, so a figure read back
// from storage answers like a computed one.
func (a *RunAggregates) withMaps() {
	for _, m := range []*map[string]int64{
		&a.Local.ErrorClasses, &a.Review.ByResolution, &a.Credits.Unresolved,
		&a.Coverage.ByMethod, &a.Coverage.ByKind, &a.Coverage.ByScript, &a.Coverage.ByDecisionClass,
	} {
		if *m == nil {
			*m = map[string]int64{}
		}
	}
}

// ComputeRunAggregates computes the run's credit-scale figures and returns
// them with the database time taken before the first of them was read. On a
// *pg.DB they are computed in one REPEATABLE READ, read-only transaction, so
// the figures are one consistent picture of the data; they are computed
// without parallel plans (serialPlans).
func ComputeRunAggregates(ctx context.Context, db pg.DBI, runID int64) (RunAggregates, time.Time, error) {
	var agg RunAggregates
	var at time.Time
	compute := func(conn pg.DBI) error {
		if _, err := conn.ExecContext(ctx, `SET LOCAL max_parallel_workers_per_gather = 0`); err != nil {
			return fmt.Errorf("planning serially: %w", err)
		}
		if _, err := conn.QueryOneContext(ctx, pg.Scan(&at), `SELECT clock_timestamp()`); err != nil {
			return fmt.Errorf("reading the database clock: %w", err)
		}
		var err error
		if agg, err = runAggregates(ctx, conn, runID); err != nil {
			return err
		}
		var growth struct {
			Growth    int64 `pg:"growth"`
			DurationS int64 `pg:"duration_s"`
		}
		if _, err := conn.QueryOneContext(ctx, &growth, runGrowthSQL, runID); err != nil {
			return fmt.Errorf("measuring the run: %w", err)
		}
		agg.GrowthBytes = growth.Growth
		return nil
	}
	var err error
	if pool, ok := db.(*pg.DB); ok {
		err = pool.RunInTransaction(ctx, func(tx *pg.Tx) error {
			if _, setErr := tx.ExecContext(ctx,
				`SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY`); setErr != nil {
				return fmt.Errorf("opening the figures snapshot: %w", setErr)
			}
			return compute(tx)
		})
	} else {
		err = compute(db)
	}
	if err != nil {
		return RunAggregates{}, time.Time{}, err
	}
	return agg, at.UTC(), nil
}

// serialPlans runs fn with parallel query off for its transaction. A
// catalog-sized aggregate planned in parallel builds its hash tables in
// dynamic shared memory, and a PostgreSQL in a container with the default
// 64 MB /dev/shm refuses that (SQLSTATE 53100, "could not resize shared
// memory segment") — the error the status reads of a full run ended in. A
// serial plan spills to temporary files instead. On a *pg.DB fn runs in a
// transaction of its own; a caller's transaction is joined, and the setting
// lasts until it ends.
func serialPlans(ctx context.Context, db pg.DBI, fn func(pg.DBI) error) error {
	const serial = `SET LOCAL max_parallel_workers_per_gather = 0`
	if pool, ok := db.(*pg.DB); ok {
		return pool.RunInTransaction(ctx, func(tx *pg.Tx) error {
			if _, err := tx.ExecContext(ctx, serial); err != nil {
				return fmt.Errorf("planning serially: %w", err)
			}
			return fn(tx)
		})
	}
	if _, err := db.ExecContext(ctx, serial); err != nil {
		return fmt.Errorf("planning serially: %w", err)
	}
	return fn(db)
}

// runAggregates computes the figures every run status shows beyond its
// extraction stream.
func runAggregates(ctx context.Context, db pg.DBI, runID int64) (RunAggregates, error) {
	var agg RunAggregates
	var err error
	if agg.Local, err = localStats(ctx, db, runID); err != nil {
		return RunAggregates{}, err
	}
	if agg.Review, err = reviewStats(ctx, db, runID); err != nil {
		return RunAggregates{}, err
	}
	if agg.Coverage, err = coverageStats(ctx, db, runID); err != nil {
		return RunAggregates{}, err
	}
	accounting, err := AuthorCreditAccountingForRun(ctx, db, runID)
	if err != nil {
		return RunAggregates{}, err
	}
	agg.Credits = AuthorMetadataCreditStats{
		Total: int64(accounting.Credits), Selected: int64(accounting.Selected), Invalid: int64(accounting.Invalid),
		Review: int64(accounting.Review), Pending: int64(accounting.Pending), Unresolved: map[string]int64{},
	}
	for reason, n := range accounting.Unresolved {
		agg.Credits.Unresolved[string(reason)] = int64(n)
	}
	agg.withMaps()
	return agg, nil
}

// StoreRunAggregates keeps the run's figures computed at computedAt, unless
// figures computed later are already stored: two computations racing leave
// the newer one.
func StoreRunAggregates(ctx context.Context, db pg.DBI, runID int64, agg *RunAggregates, computedAt time.Time) error {
	figures, err := json.Marshal(agg)
	if err != nil {
		return fmt.Errorf("encoding the run's figures: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO author_metadata_run_aggregate (run_id, computed_at, figures)
		VALUES (?, ?, ?)
		ON CONFLICT (run_id) DO UPDATE SET computed_at = EXCLUDED.computed_at, figures = EXCLUDED.figures
		WHERE author_metadata_run_aggregate.computed_at < EXCLUDED.computed_at`,
		runID, computedAt, string(figures)); err != nil {
		return fmt.Errorf("storing the run's figures: %w", err)
	}
	return nil
}

// LoadRunAggregates reads the run's stored figures with their time and
// their age on the database clock; nil when none were stored yet.
func LoadRunAggregates(ctx context.Context, db pg.DBI, runID int64) (*StoredRunAggregates, error) {
	var row struct {
		ComputedAt time.Time `pg:"computed_at"`
		AgeS       float64   `pg:"age_s"`
		Figures    string    `pg:"figures"`
	}
	_, err := db.QueryOneContext(ctx, &row, `
		SELECT computed_at, greatest(0, extract(epoch FROM clock_timestamp() - computed_at))::float8 AS age_s,
			figures::text AS figures
		FROM author_metadata_run_aggregate WHERE run_id = ?`, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the run's figures: %w", err)
	}
	stored := &StoredRunAggregates{ComputedAt: row.ComputedAt.UTC(), AgeS: row.AgeS}
	if err := json.Unmarshal([]byte(row.Figures), &stored.Figures); err != nil {
		return nil, fmt.Errorf("decoding the run's figures: %w", err)
	}
	stored.Figures.withMaps()
	return stored, nil
}

// RefreshRunAggregates computes the run's figures and stores them.
func RefreshRunAggregates(ctx context.Context, db pg.DBI, runID int64) error {
	agg, at, err := ComputeRunAggregates(ctx, db, runID)
	if err != nil {
		return err
	}
	return StoreRunAggregates(ctx, db, runID, &agg, at)
}

// OtherArchivePaths names up to limit archives of the catalog other than
// except, in name order: the witnesses the extraction worker tries when an
// archive does not open, to tell a broken archive from a gone volume.
func OtherArchivePaths(ctx context.Context, db pg.DBI, except string, limit int) ([]string, error) {
	var paths []string
	if _, err := db.QueryContext(ctx, &paths, `
		SELECT DISTINCT path FROM opds_catalog_book WHERE path <> ? ORDER BY path LIMIT ?`,
		except, limit); err != nil {
		return nil, fmt.Errorf("listing other archives: %w", err)
	}
	return paths, nil
}

// ArchiveDeletion is one request to delete the catalog records of an
// archive's books: how many books the archive held when it was asked for,
// how many are gone, and whether it is still pending.
type ArchiveDeletion struct {
	ID           int64
	RunID        int64
	Archive      string
	BooksTotal   int64
	BooksDeleted int64
	Status       string
}

// Archive deletion statuses.
const (
	ArchiveDeletionPending = "pending"
	ArchiveDeletionDone    = "done"
)

const archiveDeletionColumns = `id, run_id, archive, books_total, books_deleted, status`

// RequestArchiveDeletion records the request to delete the archive's book
// records and returns it at once; the extraction loop works it off
// (DeleteArchiveBatch). An archive with a pending request returns that one:
// a second click changes nothing.
func RequestArchiveDeletion(
	ctx context.Context, db pg.DBI, runID int64, archive string, actor *int64,
) (ArchiveDeletion, error) {
	var d ArchiveDeletion
	for range 2 {
		res, err := db.QueryOneContext(ctx, &d, `
			INSERT INTO author_metadata_archive_deletion (run_id, archive, books_total, requested_by_user_id)
			VALUES (?0, ?1, (SELECT count(*) FROM opds_catalog_book WHERE path = ?1), ?2)
			ON CONFLICT (archive) WHERE status = 'pending' DO NOTHING
			RETURNING `+archiveDeletionColumns, runID, archive, actor)
		if err == nil && res.RowsReturned() == 1 {
			return d, nil
		}
		if err != nil && !errors.Is(err, pg.ErrNoRows) {
			return ArchiveDeletion{}, fmt.Errorf("requesting the archive deletion: %w", err)
		}
		_, err = db.QueryOneContext(ctx, &d, `SELECT `+archiveDeletionColumns+`
			FROM author_metadata_archive_deletion WHERE archive = ? AND status = 'pending'`, archive)
		if err == nil {
			return d, nil
		}
		if !errors.Is(err, pg.ErrNoRows) {
			return ArchiveDeletion{}, fmt.Errorf("reading the archive deletion: %w", err)
		}
		// The pending request finished between the two statements: ask again.
	}
	return ArchiveDeletion{}, fmt.Errorf("requesting the archive deletion: %w", ErrRunTransitionConflict)
}

// DeleteArchiveBatch deletes up to limit books of the oldest pending archive
// deletion, in one transaction: the request row is locked (another worker
// skips it), the next books of the archive in ID order are locked and
// deleted with everything that hangs off them — the archive deletion's own
// semantics (deleteBooksWithLayer), fingerprint decisions kept — and the
// progress is counted with them. A batch that dies before its commit leaves
// the books and the progress as they were; a batch shorter than limit ends
// the request. It reports how many books it deleted; 0 when nothing is
// pending.
func DeleteArchiveBatch(ctx context.Context, db *pg.DB, limit int) (int, error) {
	var deleted int
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var txErr error
		deleted, txErr = deleteArchiveBatch(ctx, tx, limit)
		return txErr
	})
	return deleted, err
}

func deleteArchiveBatch(ctx context.Context, db pg.DBI, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("%w: archive deletion batch size", ErrInvalidLeaseOptions)
	}
	var request struct {
		ID      int64
		Archive string
	}
	_, err := db.QueryOneContext(ctx, &request, `
		SELECT id, archive FROM author_metadata_archive_deletion
		WHERE status = 'pending' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`)
	if errors.Is(err, pg.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("claiming an archive deletion: %w", err)
	}
	var ids []int64
	if _, err = db.QueryContext(ctx, &ids, `
		SELECT id FROM opds_catalog_book WHERE path = ? ORDER BY id LIMIT ? FOR UPDATE`,
		request.Archive, limit); err != nil {
		return 0, fmt.Errorf("locking the archive's books: %w", err)
	}
	deleted, err := deleteBooksWithLayer(db, ids)
	if err != nil {
		return 0, err
	}
	if _, err = db.ExecContext(ctx, `
		UPDATE author_metadata_archive_deletion
		SET books_deleted = books_deleted + ?1,
			status = CASE WHEN ?2 THEN 'done' ELSE status END,
			finished_at = CASE WHEN ?2 THEN clock_timestamp() ELSE finished_at END
		WHERE id = ?0`, request.ID, deleted, len(ids) < limit); err != nil {
		return 0, fmt.Errorf("counting the archive deletion: %w", err)
	}
	return deleted, nil
}
