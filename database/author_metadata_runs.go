package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The author_metadata_run state machine (contracts 3.7, 3.8, 3.9). The
// repository seeds a run with its items in one transaction, guards the
// transitions so a repeated or terminal transition is a conflict instead of a
// silent success, and counts extraction progress without ever looking at the
// downstream stages: extraction_completed_at lands when the last run item
// reaches a terminal status, however many normalization jobs are still open.
//
// One active run at a time is enforced by PostgreSQL (the partial unique index
// author_metadata_run_one_active), never by an in-process mutex: a second pod
// starting a run gets ErrActiveRunExists from the database itself.

var (
	// ErrInvalidRunSelector marks a mode/selector combination the contract
	// forbids: smoke or pilot_archive without exactly one selector (book IDs
	// or an archive) or selecting no book, full with any selector, a mode
	// outside the closed set, or a seed in a non-active status.
	ErrInvalidRunSelector = errors.New("database: run selector does not fit the mode")
	// ErrInvalidRunBookIDs marks empty, zero, negative or duplicate book IDs.
	// Out-of-order IDs are valid and canonicalized ascending.
	ErrInvalidRunBookIDs = errors.New("database: run book IDs must be positive and unique")
	// ErrActiveRunExists marks a second active run; the partial unique index
	// refused it.
	ErrActiveRunExists = errors.New("database: another author metadata run is already active")
	// ErrRunTransitionConflict marks a transition against the run's current
	// status: a repeated transition, a transition of a terminal run, or a run
	// that no longer exists. The HTTP layer maps it to 409.
	ErrRunTransitionConflict = errors.New("database: run transition conflicts with its current status")
	// ErrInvalidBookMD5 marks an MD5 that is not 32 lowercase hex digits.
	ErrInvalidBookMD5 = errors.New("database: book MD5 must be 32 lowercase hex digits")
)

// oneActiveRunIndex is the partial unique index that enforces the single
// active run.
const oneActiveRunIndex = "author_metadata_run_one_active"

// bookMD5Pattern is the closed shape of a book MD5.
var bookMD5Pattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidBookMD5 reports whether s is a well-formed book MD5 (32 lowercase hex).
func ValidBookMD5(s string) bool { return bookMD5Pattern.MatchString(s) }

// CanonicalRunBookIDs validates the selector IDs and returns them sorted
// ascending. Duplicates, zeros and negatives are refused — they are a client
// error, not something to silently repair; out-of-order IDs are valid input.
func CanonicalRunBookIDs(ids []int64) ([]int64, error) {
	out := make([]int64, len(ids))
	copy(out, ids)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	for i, id := range out {
		if id <= 0 || (i > 0 && out[i-1] == id) {
			return nil, ErrInvalidRunBookIDs
		}
	}
	return out, nil
}

// validateRunSeed is the pure half of SeedRun: every rule fails before any SQL
// runs, so an invalid seed never touches the connection.
func validateRunSeed(run *models.AuthorMetadataRun, bookIDs []int64) error {
	if run == nil || !run.Status.IsActive() {
		return ErrInvalidRunSelector
	}
	if err := validateRunSelector(run, bookIDs); err != nil {
		return err
	}
	for _, version := range []string{run.ExtractorVersion, run.NormalizerVersion} {
		if strings.TrimSpace(version) == "" {
			return authornorm.ErrEmptyVersion
		}
	}
	return nil
}

// validateRunSelector checks the selector shape (contract 3.9): smoke and
// pilot_archive store exactly one selector of either kind and seed at least
// one item; full stores none.
func validateRunSelector(run *models.AuthorMetadataRun, bookIDs []int64) error {
	switch run.Mode {
	case models.AuthorMetadataRunSmoke, models.AuthorMetadataRunPilotArchive:
		hasIDs, hasArchive := len(run.SelectorBookIDs) > 0, run.SelectorArchive != nil
		if hasIDs == hasArchive || len(bookIDs) == 0 ||
			(hasArchive && strings.TrimSpace(*run.SelectorArchive) == "") {
			return ErrInvalidRunSelector
		}
	case models.AuthorMetadataRunFull:
		// The catalog IDs the service resolved are items to seed, never a
		// stored selector: both selector columns stay empty for full.
		if run.SelectorArchive != nil || len(run.SelectorBookIDs) > 0 {
			return ErrInvalidRunSelector
		}
	default:
		return ErrInvalidRunSelector
	}
	return nil
}

// SeedRun inserts the run and one pending item per book and records the total,
// in one transaction: a run never exists without exactly its items. The book
// IDs must already be canonicalized. On a *pg.DB the seed opens its own
// transaction; a caller's transaction (or connection) is joined as-is. A
// second active run comes back as ErrActiveRunExists from the unique index.
func SeedRun(ctx context.Context, db pg.DBI, run *models.AuthorMetadataRun, bookIDs []int64) error {
	if err := validateRunSeed(run, bookIDs); err != nil {
		return err
	}
	canonical, err := CanonicalRunBookIDs(bookIDs)
	if err != nil {
		return err
	}
	if pool, ok := db.(*pg.DB); ok {
		return pool.RunInTransaction(ctx, func(tx *pg.Tx) error {
			return seedRun(ctx, tx, run, canonical)
		})
	}
	return seedRun(ctx, db, run, canonical)
}

func seedRun(ctx context.Context, db pg.DBI, run *models.AuthorMetadataRun, bookIDs []int64) error {
	// A book ID selector is stored as the canonical IDs seeded; an archive
	// or full run stores none.
	selectorIDs := bookIDs
	if len(run.SelectorBookIDs) == 0 {
		selectorIDs = nil
	}
	_, err := db.QueryOneContext(ctx, pg.Scan(&run.ID), `
		INSERT INTO author_metadata_run
			(mode, status, extractor_version, normalizer_version,
			 selector_book_ids, selector_archive, created_by_user_id, started_at, items_total)
		VALUES (?, ?, ?, ?, ?, ?, ?,
			CASE WHEN ? = 'running' THEN clock_timestamp() END, ?)
		RETURNING id`,
		run.Mode, run.Status, run.ExtractorVersion, run.NormalizerVersion,
		pg.Array(selectorIDs), run.SelectorArchive, run.CreatedByUserID,
		string(run.Status), len(bookIDs))
	if err != nil {
		return translateRunSeedError(err)
	}
	if len(bookIDs) > 0 {
		if _, err = db.ExecContext(ctx, `
			INSERT INTO author_metadata_run_item (run_id, book_id)
			SELECT ?, book FROM unnest(?::bigint[]) AS book`, run.ID, pg.Array(bookIDs)); err != nil {
			return fmt.Errorf("seeding the run items: %w", err)
		}
	}
	return nil
}

func translateRunSeedError(err error) error {
	var pgErr pg.Error
	if errors.As(err, &pgErr) && pgErr.Field('C') == sqlstateUniqueViolation && pgErr.Field('n') == oneActiveRunIndex {
		return ErrActiveRunExists
	}
	return fmt.Errorf("seeding the run: %w", err)
}

// legalTransitions is the closed set of guarded transitions. Completion and
// failure are not here: completion belongs to the pipeline accounting, and
// FailRunSystemic has its own guarded statement.
var legalTransitions = map[[2]models.AuthorMetadataRunStatus]bool{
	{models.AuthorMetadataRunPending, models.AuthorMetadataRunRunning}: true,
	{models.AuthorMetadataRunRunning, models.AuthorMetadataRunPaused}:  true,
	{models.AuthorMetadataRunPaused, models.AuthorMetadataRunRunning}:  true,
}

// TransitionRun moves a run between active statuses. The guard is the current
// status in the WHERE clause: a repeated transition, a transition of a
// terminal run or a transition someone else already made affects no row and is
// ErrRunTransitionConflict, never a silent success (contract 3.9). Starting a
// pending run stamps started_at on the database clock.
func TransitionRun(ctx context.Context, db pg.DBI, id int64, from, to models.AuthorMetadataRunStatus) error {
	if !legalTransitions[[2]models.AuthorMetadataRunStatus{from, to}] {
		return ErrRunTransitionConflict
	}
	res, err := db.ExecContext(ctx, `
		UPDATE author_metadata_run
		SET status = ?,
			started_at = CASE WHEN ? = 'pending' AND ? = 'running' THEN clock_timestamp() ELSE started_at END
		WHERE id = ? AND status = ?`,
		to, from, to, id, from)
	if err != nil {
		return fmt.Errorf("transitioning the run: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrRunTransitionConflict
	}
	return nil
}

// AuthorMetadataRunErrorClasses is the closed vocabulary of a run's
// last_error_class: the systemic class that pauses a run (an unreadable
// archive or volume) and the classes that end it failed_systemic. The
// schema checks the column only for its shape, and a shape is not a
// vocabulary — a name-shaped value passes it — so the writers check
// membership. services keeps the constants and a test pins them to this list.
func AuthorMetadataRunErrorClasses() []string {
	return []string{classArchiveUnreadable, classDatabaseInvariant, classVersionMismatch, classExtractorMisconfigured}
}

// ValidRunErrorClass reports whether class is a run error class.
func ValidRunErrorClass(class string) bool {
	for _, known := range AuthorMetadataRunErrorClasses() {
		if class == known {
			return true
		}
	}
	return false
}

// ClosedRunErrorClass projects a stored last_error_class onto the run error
// classes for a response: none stays none, a known class is itself, and any
// other stored value — the schema accepts the shape — is
// AuthorMetadataOtherValue, never its own text.
func ClosedRunErrorClass(class *string) *string {
	if class == nil {
		return nil
	}
	closed := AuthorMetadataOtherValue
	if ValidRunErrorClass(*class) {
		closed = *class
	}
	return &closed
}

// errNotARunErrorClass refuses a systemic write whose class is outside the
// run error classes; the value itself is not repeated.
var errNotARunErrorClass = fmt.Errorf("%w: not a run error class", ErrInvalidLeaseFailure)

// PauseRunSystemic pauses a running run because the archive or volume failed:
// the items keep no mass per-book status (contract 3.3), the run records the
// closed error class, and claims stop until a resume. Pausing anything but a
// running run is a conflict — the worker that lost the race to pause simply
// stops.
func PauseRunSystemic(ctx context.Context, db pg.DBI, id int64, class string) error {
	if !ValidRunErrorClass(class) {
		return errNotARunErrorClass
	}
	res, err := db.ExecContext(ctx, `
		UPDATE author_metadata_run SET status = 'paused', last_error_class = ?
		WHERE id = ? AND status = 'running'`, class, id)
	if err != nil {
		return fmt.Errorf("pausing the run: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrRunTransitionConflict
	}
	return nil
}

// FailRunSystemic ends an active run as failed_systemic: a database schema or
// invariant error means the run cannot make progress (contract 3.3). The
// terminal status frees the single active-run slot.
func FailRunSystemic(ctx context.Context, db pg.DBI, id int64, class string) error {
	if !ValidRunErrorClass(class) {
		return errNotARunErrorClass
	}
	res, err := db.ExecContext(ctx, `
		UPDATE author_metadata_run
		SET status = 'failed_systemic', last_error_class = ?, finished_at = clock_timestamp()
		WHERE id = ? AND status IN ('pending', 'running', 'paused')`, class, id)
	if err != nil {
		return fmt.Errorf("failing the run: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrRunTransitionConflict
	}
	return nil
}

// RecordExtractionItemTerminal counts one more terminal item of the run and,
// when the count reaches the total, stamps extraction_completed_at on the
// database clock. It runs inside the item's completion transaction, so the
// counter never drifts from the item states the transaction commits. The stamp
// does not wait for any downstream stage (contract 3.9). It reports whether
// extraction is complete after this item.
func RecordExtractionItemTerminal(ctx context.Context, db pg.DBI, runID int64) (bool, error) {
	var done bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&done), `
		UPDATE author_metadata_run
		SET items_terminal = items_terminal + 1,
			extraction_completed_at = CASE WHEN items_terminal + 1 >= items_total
				THEN coalesce(extraction_completed_at, clock_timestamp())
				ELSE extraction_completed_at END
		WHERE id = ?
		RETURNING items_terminal >= items_total`, runID)
	if err != nil {
		return false, fmt.Errorf("counting the terminal item: %w", err)
	}
	return done, nil
}

// ReconcileRunExtraction recomputes the counters from the item rows. The
// claim layer ends abandoned rows out of attempts inside its own transaction,
// where no per-item completion runs; the worker reconciles when a claim comes
// back empty, so those rows still count and extraction completion is still
// stamped. The total is recounted too: a run seeds every item in one
// transaction, so its rows are its total until deleting books removes some of
// them (migration 26) — the deletion leaves the run row alone, because the
// extraction worker (item, then run) and a retry (run, then items) lock the
// two in opposite orders, and this is where the run learns of it. A run
// without items completes vacuously. Terminal runs are left untouched: their
// counters stay the record of what they processed.
func ReconcileRunExtraction(ctx context.Context, db pg.DBI, runID int64) (bool, error) {
	var done bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&done), `
		WITH counted AS (
			SELECT count(*) AS total, count(*) FILTER (WHERE status <> 'pending') AS n
			FROM author_metadata_run_item WHERE run_id = ?)
		UPDATE author_metadata_run r
		SET items_total = counted.total,
			items_terminal = counted.n,
			extraction_completed_at = CASE WHEN counted.n >= counted.total
				THEN coalesce(r.extraction_completed_at, clock_timestamp())
				ELSE r.extraction_completed_at END
		FROM counted
		WHERE r.id = ? AND r.status IN ('pending', 'running', 'paused')
		RETURNING r.items_terminal >= r.items_total`, runID, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return false, ErrRunTransitionConflict
	}
	if err != nil {
		return false, fmt.Errorf("reconciling the run counters: %w", err)
	}
	return done, nil
}

// HasApprovedPilot reports whether a completed pilot_archive run of exactly
// these versions was explicitly approved (contract 3.9): the gate a full run
// must pass before it seeds.
func HasApprovedPilot(ctx context.Context, db pg.DBI, extractorVersion, normalizerVersion string) (bool, error) {
	var approved bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&approved), `
		SELECT EXISTS (SELECT 1 FROM author_metadata_pilot_approval
			WHERE extractor_version = ? AND normalizer_version = ?)`,
		extractorVersion, normalizerVersion)
	if err != nil {
		return false, fmt.Errorf("checking the pilot approval: %w", err)
	}
	return approved, nil
}

// ExtractionItemTarget is one claimed item joined to its catalog row: where
// the entry lives and the MD5 the catalog already knows (empty when the legacy
// row has none).
type ExtractionItemTarget struct {
	ItemID int64
	// RunID is the run the item belongs to. A worker processes the item for
	// this run, never for a run it happened to hold before the claim.
	RunID       int64
	BookID      int64
	ArchivePath string
	EntryName   string
	MD5         string
}

// LoadExtractionTargets reads the catalog locations of claimed items. The
// caller owns claim order; the result preserves the requested ID order.
func LoadExtractionTargets(ctx context.Context, db pg.DBI, itemIDs []int64) ([]ExtractionItemTarget, error) {
	if len(itemIDs) == 0 {
		return nil, nil
	}
	var rows []ExtractionItemTarget
	_, err := db.QueryContext(ctx, &rows, `
		SELECT i.id AS item_id, i.run_id, i.book_id, b.path AS archive_path, b.filename AS entry_name,
			coalesce(b.md5, '') AS md5
		FROM author_metadata_run_item i
		JOIN opds_catalog_book b ON b.id = i.book_id
		WHERE i.id IN (?)`, pg.In(itemIDs))
	if err != nil {
		return nil, fmt.Errorf("loading the extraction targets: %w", err)
	}
	byItem := make(map[int64]ExtractionItemTarget, len(rows))
	for _, row := range rows {
		byItem[row.ItemID] = row
	}
	ordered := make([]ExtractionItemTarget, 0, len(rows))
	for _, id := range itemIDs {
		if row, ok := byItem[id]; ok {
			ordered = append(ordered, row)
		}
	}
	return ordered, nil
}

// FillBookMD5IfEmpty backfills a legacy book's MD5 only while the column is
// empty: a concurrent live ingestion that already set it always wins, and a
// non-empty value is never overwritten (contract: backfill computes, never
// rewrites). It runs inside the extraction's own transaction.
func FillBookMD5IfEmpty(ctx context.Context, db pg.DBI, bookID int64, md5 string) error {
	if !bookMD5Pattern.MatchString(md5) {
		return ErrInvalidBookMD5
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE opds_catalog_book SET md5 = ? WHERE id = ? AND (md5 IS NULL OR md5 = '')`, md5, bookID); err != nil {
		return fmt.Errorf("backfilling the book MD5: %w", err)
	}
	return nil
}

// ActiveRun returns the run occupying the single active slot, or nil when no
// run is active.
func ActiveRun(ctx context.Context, db pg.DBI) (*models.AuthorMetadataRun, error) {
	run := &models.AuthorMetadataRun{}
	err := db.ModelContext(ctx, run).
		Where("status IN ('pending', 'running', 'paused')").
		Limit(1).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading the active run: %w", err)
	}
	return run, nil
}

// LatestRun returns the most recent run by id, whatever its status, or nil
// when no run exists. The dashboard asks for it once the active slot is
// empty, so a completed run stays visible from the database alone — no
// client-side memory involved.
func LatestRun(ctx context.Context, db pg.DBI) (*models.AuthorMetadataRun, error) {
	run := &models.AuthorMetadataRun{}
	err := db.ModelContext(ctx, run).
		Order("id DESC").
		Limit(1).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading the latest run: %w", err)
	}
	return run, nil
}

// ListCatalogBookIDs enumerates the whole catalog, ascending: the full-mode
// selector.
func ListCatalogBookIDs(ctx context.Context, db pg.DBI) ([]int64, error) {
	var ids []int64
	if _, err := db.QueryContext(ctx, &ids, `SELECT id FROM opds_catalog_book ORDER BY id`); err != nil {
		return nil, fmt.Errorf("listing the catalog books: %w", err)
	}
	return ids, nil
}

// ListArchiveBookIDs enumerates the books of one archive, ascending: the
// pilot_archive selector.
func ListArchiveBookIDs(ctx context.Context, db pg.DBI, archive string) ([]int64, error) {
	var ids []int64
	if _, err := db.QueryContext(ctx, &ids,
		`SELECT id FROM opds_catalog_book WHERE path = ? ORDER BY id`, archive); err != nil {
		return nil, fmt.Errorf("listing the archive books: %w", err)
	}
	return ids, nil
}

// ListExistingBookIDs filters the selector to the books that exist, ascending.
// An ID the catalog does not have is dropped: the smoke selector names books
// an administrator picked, and a deleted book is not an error.
func ListExistingBookIDs(ctx context.Context, db pg.DBI, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var existing []int64
	if _, err := db.QueryContext(ctx, &existing,
		`SELECT id FROM opds_catalog_book WHERE id IN (?) ORDER BY id`, pg.In(ids)); err != nil {
		return nil, fmt.Errorf("filtering the selector books: %w", err)
	}
	return existing, nil
}

// CompleteRunIfSettled completes a running run once both of its stages are
// done (contract 3.9, amendment A3): every run item is terminal
// (extraction_completed_at is stamped) and every current author credit of
// the run's books is accounted — selected, invalid, unresolved with a closed
// reason or in open review, with no local job of its input still pending
// (AuthorCreditAccountingForRun). An open review backlog does not hold the
// run. A paused run is left to the administrator, and a run that is not
// active is never touched. It reports whether this call completed the run.
//
// The run row is locked for the check and the write, so two callers cannot
// both complete it and nothing completes a run whose status changed under
// the check. On a *pg.DB the check opens its own transaction; a caller's
// transaction is joined as-is.
func CompleteRunIfSettled(ctx context.Context, db pg.DBI, runID int64) (bool, error) {
	if pool, ok := db.(*pg.DB); ok {
		var done bool
		err := pool.RunInTransaction(ctx, func(tx *pg.Tx) error {
			var txErr error
			done, txErr = completeRunIfSettled(ctx, tx, runID)
			return txErr
		})
		return done, err
	}
	return completeRunIfSettled(ctx, db, runID)
}

func completeRunIfSettled(ctx context.Context, db pg.DBI, runID int64) (bool, error) {
	var run struct {
		Status    models.AuthorMetadataRunStatus
		Extracted bool
	}
	_, err := db.QueryOneContext(ctx, &run, `
		SELECT status, extraction_completed_at IS NOT NULL AS extracted
		FROM author_metadata_run WHERE id = ? FOR UPDATE`, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return false, ErrRunTransitionConflict
	}
	if err != nil {
		return false, fmt.Errorf("locking the run: %w", err)
	}
	if run.Status != models.AuthorMetadataRunRunning || !run.Extracted {
		return false, nil
	}
	accounting, err := AuthorCreditAccountingForRun(ctx, db, runID)
	if err != nil {
		return false, err
	}
	if !accounting.Settled() {
		return false, nil
	}
	if _, err = db.ExecContext(ctx, `
		UPDATE author_metadata_run SET status = 'completed', finished_at = clock_timestamp()
		WHERE id = ? AND status = 'running'`, runID); err != nil {
		return false, fmt.Errorf("completing the run: %w", err)
	}
	return true, nil
}

// LoadRuns reads the runs with the given IDs, keyed by ID.
func LoadRuns(ctx context.Context, db pg.DBI, ids []int64) (map[int64]*models.AuthorMetadataRun, error) {
	runs := map[int64]*models.AuthorMetadataRun{}
	if len(ids) == 0 {
		return runs, nil
	}
	var rows []models.AuthorMetadataRun
	if err := db.ModelContext(ctx, &rows).Where("id IN (?)", pg.In(ids)).Select(); err != nil {
		return nil, fmt.Errorf("loading the runs: %w", err)
	}
	for i := range rows {
		runs[rows[i].ID] = &rows[i]
	}
	return runs, nil
}
