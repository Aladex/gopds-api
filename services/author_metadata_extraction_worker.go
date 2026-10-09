package services

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/parser"
	"gopds-api/internal/safepath"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The extraction worker is the vertical slice of contracts 3.3, 3.7 and 3.9:
// it claims run items of the running run, opens each archive once per batch
// group, extracts metadata (never reading a body past </description> unless a
// legacy MD5 has to be computed streaming), classifies every failure into the
// closed per-book terminal mapping or one of the two systemic paths, and
// persists snapshot, credits, local jobs, item status and run counters in one
// short transaction per item. All archive I/O happens after the claim
// committed, outside any lock.
//
// The default concurrency is 1 (contract 3.8): extractions never overlap
// unless the configuration explicitly asks for more, and a non-positive
// concurrency, claim size, lease or attempt limit is rejected at load instead
// of silently widening into an unbounded mode.

// ErrInvalidExtractionWorkerConfig marks a worker configuration with a
// non-positive limit, a missing extractor or a missing version.
var ErrInvalidExtractionWorkerConfig = errors.New("services: invalid extraction worker configuration")

// ErrArchiveEntryGone marks an entry name the opened archive does not have;
// the item ends entry_missing while the archive's other books continue.
var ErrArchiveEntryGone = errors.New("services: archive entry is missing")

// Closed error classes this worker records. They live here rather than in
// author_metadata_job_types.go because only the extraction stage uses them.
const (
	// AuthorMetadataErrorArchiveUnreadable marks an archive or volume that
	// does not open or stops reading: the run auto-pauses, the item retries.
	AuthorMetadataErrorArchiveUnreadable AuthorMetadataErrorClass = "archive_unreadable"
	// AuthorMetadataErrorDatabaseInvariant marks a schema or invariant error:
	// the run ends failed_systemic instead of retrying forever.
	AuthorMetadataErrorDatabaseInvariant AuthorMetadataErrorClass = "database_invariant"
	// AuthorMetadataErrorVersionMismatch marks a run whose extractor or
	// normalizer version is not the one this worker produces: nothing is
	// persisted under it, and the run ends failed_systemic.
	AuthorMetadataErrorVersionMismatch AuthorMetadataErrorClass = "version_mismatch"
	// AuthorMetadataErrorExtractorMisconfigured marks an extractor that
	// refuses its own configuration: no book can succeed, so the run ends
	// failed_systemic.
	AuthorMetadataErrorExtractorMisconfigured AuthorMetadataErrorClass = "extractor_misconfigured"
	// AuthorMetadataErrorExtractionFailed marks an extraction error of no
	// known kind: neither a document problem nor a source read failure. The
	// book is retried and, out of attempts, ends metadata_parse_failed; the
	// run goes on.
	AuthorMetadataErrorExtractionFailed AuthorMetadataErrorClass = "extraction_failed"
)

// errSourceRead marks a failure of the archive entry stream itself — the
// volume stopped reading — as opposed to anything the extractor concluded
// about the document. sourceReader tags it, so the classification does not
// depend on which I/O error type a reader returns.
type errSourceRead struct{ err error }

func (e *errSourceRead) Error() string {
	return "services: archive entry read failed: " + e.err.Error()
}

func (e *errSourceRead) Unwrap() error { return e.err }

// sourceReader tags every read failure of an entry stream except its end.
type sourceReader struct{ r io.Reader }

func (s sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, &errSourceRead{err: err}
	}
	return n, err
}

// ArchiveReader is one opened archive.
type ArchiveReader interface {
	Close() error
	// OpenEntry opens one entry for reading. A name the archive does not have
	// is ErrArchiveEntryGone; every other failure is a systemic read condition.
	OpenEntry(name string) (io.ReadCloser, error)
}

// ArchiveSource opens archives by absolute path. The production adapter reads
// zips from the archives directory; tests may substitute a deterministic fake.
type ArchiveSource interface {
	Open(ctx context.Context, path string) (ArchiveReader, error)
}

// ZipArchiveSource is the production archive adapter over archive/zip.
type ZipArchiveSource struct{}

// Open opens the zip file; a file that is not a readable zip is a systemic
// archive failure, reported as an ordinary error for the caller to classify.
func (ZipArchiveSource) Open(_ context.Context, path string) (ArchiveReader, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	return &zipArchiveReader{r: r}, nil
}

type zipArchiveReader struct {
	r *zip.ReadCloser
}

func (z *zipArchiveReader) Close() error { return z.r.Close() }

func (z *zipArchiveReader) OpenEntry(name string) (io.ReadCloser, error) {
	for _, f := range z.r.File {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, ErrArchiveEntryGone
}

// MetadataExtractor is the extraction engine; parser.MetadataExtractor
// satisfies it, and tests substitute a barrier or a failing fake.
type MetadataExtractor interface {
	Extract(in parser.ExtractBookInput) (authornorm.SourceMetadata, error)
}

// ExtractionWorkerConfig is the closed configuration of one worker.
type ExtractionWorkerConfig struct {
	// ArchivesDir is the root the catalog's relative archive paths resolve under.
	ArchivesDir string
	// Concurrency bounds how many archive groups extract at once. Default 1.
	Concurrency int
	// ClaimLimit is the most items one claim batch takes.
	ClaimLimit int
	// Lease is how long a claimed item stays this worker's without a heartbeat.
	Lease time.Duration
	// Retry is the backoff policy of failed attempts.
	Retry AuthorMetadataRetryPolicy
	// Extractor is the metadata extraction engine.
	Extractor MetadataExtractor
}

// The normalizer version stamped on snapshots and normalization keys is
// authornorm.NormalizerVersion, applied by the shared mapping helper: the live
// dual write and the backfill must produce the same normalization key for the
// same source, so it is not a worker knob.

// The production defaults of contract 3.8: extraction is single-threaded
// unless configured otherwise, claims are small and retries bounded.
const (
	defaultConcurrency    = 1
	defaultClaimLimit     = 8
	defaultLease          = 2 * time.Minute
	defaultMaxAttempts    = 3
	defaultRetryBaseDelay = 30 * time.Second
	defaultRetryMaxDelay  = 10 * time.Minute
)

// DefaultExtractionWorkerConfig is the production shape: concurrency 1, small
// claim batches and a bounded retry policy.
func DefaultExtractionWorkerConfig(archivesDir string, extractor MetadataExtractor) ExtractionWorkerConfig {
	return ExtractionWorkerConfig{
		ArchivesDir: archivesDir,
		Concurrency: defaultConcurrency,
		ClaimLimit:  defaultClaimLimit,
		Lease:       defaultLease,
		Retry: AuthorMetadataRetryPolicy{
			MaxAttempts: defaultMaxAttempts,
			BaseDelay:   defaultRetryBaseDelay,
			MaxDelay:    defaultRetryMaxDelay,
			Jitter: func(limit time.Duration) time.Duration {
				if limit <= 0 {
					return 0
				}
				// #nosec G404 -- backoff jitter needs no cryptographic randomness
				return time.Duration(rand.Int64N(int64(limit) + 1))
			},
		},
		Extractor: extractor,
	}
}

func (c *ExtractionWorkerConfig) validate() error {
	if c.ArchivesDir == "" || c.Concurrency <= 0 || c.ClaimLimit <= 0 || c.Lease <= 0 || c.Extractor == nil {
		return ErrInvalidExtractionWorkerConfig
	}
	if err := c.Retry.Validate(); err != nil {
		return ErrInvalidExtractionWorkerConfig
	}
	return nil
}

// The worker logs only AuthorMetadataEvent records (author_metadata_observability.go):
// IDs, counts and closed classes, never error text (contract 3.14).

// sqlState is the SQLSTATE of a PostgreSQL error, empty for any other error:
// the only part of an error a log may carry. Error messages can quote row
// data — a constraint or trigger message may contain a source title — so no
// error text is ever logged (contract 3.14).
func sqlState(err error) string {
	var pgErr pg.Error
	if errors.As(err, &pgErr) {
		return pgErr.Field('C')
	}
	return ""
}

// AuthorMetadataExtractionWorker drains the extraction stream of the running
// run until no claimable items remain or the run stops being running.
type AuthorMetadataExtractionWorker struct {
	db       *pg.DB
	archives ArchiveSource
	cfg      ExtractionWorkerConfig
	owner    string
}

// NewAuthorMetadataExtractionWorker validates the configuration and assigns
// this worker its lease owner token.
func NewAuthorMetadataExtractionWorker(
	db *pg.DB,
	archives ArchiveSource,
	cfg *ExtractionWorkerConfig,
) (*AuthorMetadataExtractionWorker, error) {
	if db == nil || archives == nil {
		return nil, ErrInvalidExtractionWorkerConfig
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	resolved := *cfg
	return &AuthorMetadataExtractionWorker{
		db:       db,
		archives: archives,
		cfg:      resolved,
		owner:    database.NewLeaseOwner(),
	}, nil
}

// ProcessAvailable claims and processes items until a claim comes back empty
// (then reconciles the extraction counters and completes every run it
// touched once settled) or a systemic condition stops it. It reports how many
// items reached a terminal status.
//
// Every claimed item is processed for its own run, read again after the
// claim: between two claims another replica or an administrator may complete
// the active run and start the next one, and an item of that next run must
// never be recorded under the run this call started with (contract 3.9).
func (w *AuthorMetadataExtractionWorker) ProcessAvailable(ctx context.Context) (int, error) {
	run, err := database.ActiveRun(ctx, w.db)
	if err != nil {
		return 0, err
	}
	if run == nil || run.Status != models.AuthorMetadataRunRunning {
		return 0, nil
	}

	processed := 0
	touched := map[int64]bool{run.ID: true}
	for {
		claims, err := database.ClaimExtractionItems(ctx, w.db, w.owner, database.LeaseClaimOptions{
			Limit:       w.cfg.ClaimLimit,
			Lease:       w.cfg.Lease,
			MaxAttempts: w.cfg.Retry.MaxAttempts,
		})
		if err != nil {
			return processed, err
		}
		if len(claims) == 0 {
			return processed, w.settleRuns(ctx, touched)
		}

		n, systemic, err := w.processBatch(ctx, claims, touched)
		processed += n
		if err != nil {
			return processed, err
		}
		if systemic {
			// The run is paused now; claims would come back empty anyway.
			return processed, nil
		}
	}
}

// settleRuns runs when nothing is left to claim. The claim layer may have
// ended abandoned rows out of attempts inside its own transaction, so the
// counters are reconciled first; then each run completes once its credits
// are accounted too. The runner polls this worker even while it is idle, so
// a run whose last credits the local stream settles completes on the next
// poll. A run that left the active statuses meanwhile is skipped.
func (w *AuthorMetadataExtractionWorker) settleRuns(ctx context.Context, runs map[int64]bool) error {
	for runID := range runs {
		_, err := database.ReconcileRunExtraction(ctx, w.db, runID)
		if errors.Is(err, database.ErrRunTransitionConflict) {
			continue
		}
		if err != nil {
			return err
		}
		completed, completeErr := database.CompleteRunIfSettled(ctx, w.db, runID)
		if completeErr != nil {
			return completeErr
		}
		if completed {
			LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
				Name: AuthorMetadataEventRunCompleted, Stage: AuthorMetadataStageExtraction, RunID: runID,
			})
		}
	}
	return nil
}

// extractionWorkItem is one claimed item with its catalog location and the
// run it belongs to.
type extractionWorkItem struct {
	claim  database.LeaseClaim
	target database.ExtractionItemTarget
	run    *models.AuthorMetadataRun
}

// processBatch handles one claim batch: items grouped by archive (each archive
// opened once per batch), groups processed with the configured concurrency.
// It reports the number of items that reached a terminal status and whether a
// systemic condition paused the run.
func (w *AuthorMetadataExtractionWorker) processBatch(
	ctx context.Context,
	claims []database.LeaseClaim,
	touched map[int64]bool,
) (processed int, systemic bool, err error) {
	claimByItem := make(map[int64]database.LeaseClaim, len(claims))
	itemIDs := make([]int64, len(claims))
	for i, claim := range claims {
		claimByItem[claim.ID] = claim
		itemIDs[i] = claim.ID
	}
	targets, err := database.LoadExtractionTargets(ctx, w.db, itemIDs)
	if err != nil {
		return 0, false, err
	}
	runIDs := make([]int64, 0, len(targets))
	for _, target := range targets {
		runIDs = append(runIDs, target.RunID)
	}
	runs, err := database.LoadRuns(ctx, w.db, runIDs)
	if err != nil {
		return 0, false, err
	}

	// Group by archive, keeping claim order inside and across groups.
	var order []string
	groups := make(map[string][]*extractionWorkItem)
	for _, target := range targets {
		if _, seen := groups[target.ArchivePath]; !seen {
			order = append(order, target.ArchivePath)
		}
		groups[target.ArchivePath] = append(groups[target.ArchivePath], &extractionWorkItem{
			claim:  claimByItem[target.ItemID],
			target: target,
			run:    runs[target.RunID],
		})
		touched[target.RunID] = true
	}

	var handled [][]bool
	processed, handled, systemic = w.runGroups(ctx, order, groups)
	if !systemic {
		return processed, false, nil
	}

	// A systemic pause abandons the rest of the batch: every claimed item that
	// never ran gets a closed-class retry instead of waiting for its lease to
	// expire, so a resume picks it up immediately.
	for gi, archivePath := range order {
		var remaining []*extractionWorkItem
		for i, item := range groups[archivePath] {
			if !handled[gi][i] {
				remaining = append(remaining, item)
			}
		}
		n, failErr := w.failItems(ctx, remaining, AuthorMetadataErrorArchiveUnreadable)
		if failErr != nil {
			return processed, true, failErr
		}
		processed += n
	}
	return processed, true, nil
}

// runGroups launches one goroutine per archive group under the configured
// concurrency and merges the per-group outcomes. handled mirrors each group's
// items: an item that reached any resolution is marked, so the batch cleanup
// only retries items that never ran.
func (w *AuthorMetadataExtractionWorker) runGroups(
	ctx context.Context,
	order []string,
	groups map[string][]*extractionWorkItem,
) (processed int, handled [][]bool, systemic bool) {
	type groupOutcome struct {
		processed int
		systemic  bool
	}
	outcomes := make([]groupOutcome, len(order))
	handled = make([][]bool, len(order))

	semaphore := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	for gi, archivePath := range order {
		handled[gi] = make([]bool, len(groups[archivePath]))
		wg.Add(1)
		go func(gi int, archivePath string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			outcomes[gi].processed, outcomes[gi].systemic = w.processGroup(
				ctx, archivePath, groups[archivePath], handled[gi])
		}(gi, archivePath)
	}
	wg.Wait()

	for _, outcome := range outcomes {
		processed += outcome.processed
		systemic = systemic || outcome.systemic
	}
	return processed, handled, systemic
}

// processGroup opens one archive once and works through its items in claim
// order. handled mirrors items: an item that reached any resolution (terminal,
// failed with a retry, discarded on a lost lease) is marked, so the batch
// cleanup only retries items that never ran.
func (w *AuthorMetadataExtractionWorker) processGroup(
	ctx context.Context,
	archivePath string,
	items []*extractionWorkItem,
	handled []bool,
) (int, bool) {
	fullPath, err := safepath.Resolve(w.cfg.ArchivesDir, archivePath)
	if err != nil {
		// A catalog path escaping the archives root is a data invariant
		// violation: systemic, never a per-book status.
		w.pauseRunsOf(ctx, items)
		return 0, true
	}
	archive, err := w.archives.Open(ctx, fullPath)
	if err != nil {
		w.pauseRunsOf(ctx, items)
		n, failErr := w.failItems(ctx, items, AuthorMetadataErrorArchiveUnreadable)
		for i := range handled {
			handled[i] = true
		}
		if failErr != nil {
			extractionWriteFailed(nil, 0, "", failErr)
		}
		return n, true
	}
	defer func() { _ = archive.Close() }()

	processed := 0
	for i, item := range items {
		outcome := w.processItem(ctx, archive, item)
		handled[i] = true
		if outcome.terminal {
			processed++
		}
		if outcome.systemic {
			// The remaining items of this archive never ran: they are not
			// marked handled, and the batch cleanup retries them.
			return processed, true
		}
	}
	return processed, false
}

// itemOutcome is how one item's attempt ended.
type itemOutcome struct {
	// terminal: the item reached a closed terminal status (completed or
	// exhausted its attempts).
	terminal bool
	// systemic: the archive or the database failed in a way that pauses or
	// ends the run; the item itself was already failed with a closed class.
	systemic bool
}

// pauseRunsOf pauses the run of every item of a group.
func (w *AuthorMetadataExtractionWorker) pauseRunsOf(ctx context.Context, items []*extractionWorkItem) {
	paused := map[int64]bool{}
	for _, item := range items {
		if !paused[item.run.ID] {
			paused[item.run.ID] = true
			w.pauseSystemic(ctx, item.run.ID)
		}
	}
}

// processItem runs one item for its own run: open the entry, extract,
// classify, persist.
func (w *AuthorMetadataExtractionWorker) processItem(
	ctx context.Context,
	archive ArchiveReader,
	item *extractionWorkItem,
) itemOutcome {
	run := item.run
	if run.NormalizerVersion != authornorm.NormalizerVersion {
		// The shared mapping stamps authornorm.NormalizerVersion on every
		// local job; a run declaring another version would get work it did
		// not authorize. Checked per item, for the item's own run.
		return w.endRun(ctx, run, item, AuthorMetadataErrorVersionMismatch)
	}
	entry, err := archive.OpenEntry(item.target.EntryName)
	if errors.Is(err, ErrArchiveEntryGone) {
		return w.completeTerminalOutcome(ctx, run, item, models.AuthorMetadataRunItemEntryMissing)
	}
	if err != nil {
		return w.sourceFailure(ctx, run, item)
	}

	knownMD5 := ""
	if database.ValidBookMD5(item.target.MD5) {
		knownMD5 = item.target.MD5
	}
	metadata, extractErr := w.cfg.Extractor.Extract(parser.ExtractBookInput{
		Reader:      sourceReader{r: entry},
		BookID:      item.target.BookID,
		ArchivePath: item.target.ArchivePath,
		EntryName:   item.target.EntryName,
		KnownMD5:    knownMD5,
	})
	_ = entry.Close()

	if extractErr != nil {
		// Origin before type: a failure of the entry stream is a source
		// failure whatever it wraps, even an error the per-book classifier
		// would take for a document problem (contract 3.3).
		var sourceErr *errSourceRead
		if errors.As(extractErr, &sourceErr) {
			return w.sourceFailure(ctx, run, item)
		}
		switch status, ok := classifyExtractionError(extractErr); {
		case ok:
			return w.completeTerminalOutcome(ctx, run, item, status)
		case errors.Is(extractErr, parser.ErrInvalidExtractorInput):
			return w.endRun(ctx, run, item, AuthorMetadataErrorExtractorMisconfigured)
		default:
			// Of no known kind: neither a document nor a volume problem.
			// The book is retried and ends metadata_parse_failed once out of
			// attempts; the run goes on.
			n, failErr := w.failItems(ctx, []*extractionWorkItem{item}, AuthorMetadataErrorExtractionFailed)
			if failErr != nil {
				return w.persistenceFailure(ctx, run, item, failErr)
			}
			return itemOutcome{terminal: n > 0}
		}
	}
	if metadata.ExtractorVersion != run.ExtractorVersion {
		return w.endRun(ctx, run, item, AuthorMetadataErrorVersionMismatch)
	}
	return w.persistItem(ctx, run, item, &metadata)
}

// sourceFailure pauses the run for an archive or volume read failure and
// records the item's attempt with the closed class, so its lease is released
// and a resume retries it at once.
func (w *AuthorMetadataExtractionWorker) sourceFailure(
	ctx context.Context,
	run *models.AuthorMetadataRun,
	item *extractionWorkItem,
) itemOutcome {
	w.pauseSystemic(ctx, run.ID)
	if _, err := w.failItems(ctx, []*extractionWorkItem{item}, AuthorMetadataErrorArchiveUnreadable); err != nil {
		extractionWriteFailed(item, run.ID, AuthorMetadataErrorArchiveUnreadable, err)
	}
	return itemOutcome{systemic: true}
}

// endRun ends the run failed_systemic with a closed class that no retry can
// cure, and records the item's attempt with it.
func (w *AuthorMetadataExtractionWorker) endRun(
	ctx context.Context,
	run *models.AuthorMetadataRun,
	item *extractionWorkItem,
	class AuthorMetadataErrorClass,
) itemOutcome {
	w.failRun(ctx, run.ID, class)
	if _, err := w.failItems(ctx, []*extractionWorkItem{item}, class); err != nil {
		extractionWriteFailed(item, run.ID, class, err)
	}
	return itemOutcome{systemic: true}
}

// failRun ends the run failed_systemic with a closed class. Losing the race
// (the run already left the active statuses) is fine.
func (w *AuthorMetadataExtractionWorker) failRun(ctx context.Context, runID int64, class AuthorMetadataErrorClass) {
	err := database.FailRunSystemic(ctx, w.db, runID, string(class))
	switch {
	case err == nil:
		LogAuthorMetadataEvent(AuthorMetadataEventError, &AuthorMetadataEvent{
			Name: AuthorMetadataEventExtractionRunEnded, Stage: AuthorMetadataStageExtraction, RunID: runID, Class: class,
		})
	case !errors.Is(err, database.ErrRunTransitionConflict):
		extractionWriteFailed(nil, runID, class, err)
	}
}

// extractionWriteFailed records that a write of the extraction stream failed:
// identifiers, the closed class it was about and the SQLSTATE — never the
// error text, which can quote row data.
func extractionWriteFailed(item *extractionWorkItem, runID int64, class AuthorMetadataErrorClass, err error) {
	e := &AuthorMetadataEvent{
		Name: AuthorMetadataEventExtractionWriteFailed, Stage: AuthorMetadataStageExtraction,
		RunID: runID, Class: class, SQLState: sqlState(err),
	}
	if item != nil {
		e.ItemID, e.BookID = item.target.ItemID, item.target.BookID
	}
	LogAuthorMetadataEvent(AuthorMetadataEventError, e)
}

// extractionItemCompleted records a per-book terminal status.
func extractionItemCompleted(run *models.AuthorMetadataRun, item *extractionWorkItem, status models.AuthorMetadataRunItemStatus) {
	LogAuthorMetadataEvent(AuthorMetadataEventDebug, &AuthorMetadataEvent{
		Name: AuthorMetadataEventExtractionItemCompleted, Stage: AuthorMetadataStageExtraction,
		RunID: run.ID, ItemID: item.target.ItemID, BookID: item.target.BookID, Status: string(status),
	})
}

// completeTerminalOutcome records a per-book terminal status; a failure to
// record it is classified like any other persistence failure, so a
// rolled-back status is never counted as terminal.
func (w *AuthorMetadataExtractionWorker) completeTerminalOutcome(
	ctx context.Context,
	run *models.AuthorMetadataRun,
	item *extractionWorkItem,
	status models.AuthorMetadataRunItemStatus,
) itemOutcome {
	if err := w.completeTerminal(ctx, run.ID, item, status, nil); err != nil {
		return w.persistenceFailure(ctx, run, item, err)
	}
	extractionItemCompleted(run, item, status)
	return itemOutcome{terminal: true}
}

// classifyExtractionError is the single pure function mapping extractor
// failures to the closed per-book terminal statuses of contract 3.3. An error
// outside the closed set is not a document problem: it is systemic.
func classifyExtractionError(err error) (models.AuthorMetadataRunItemStatus, bool) {
	switch {
	case errors.Is(err, parser.ErrDamagedContent):
		return models.AuthorMetadataRunItemInvalidFB2, true
	case errors.Is(err, parser.ErrUnsupportedCharset),
		errors.Is(err, parser.ErrUndeclaredCharset),
		errors.Is(err, parser.ErrUnsupportedDeclaredCharset):
		return models.AuthorMetadataRunItemUnsupportedEncoding, true
	case errors.Is(err, parser.ErrMetadataLimit),
		errors.Is(err, authornorm.ErrInvalidSourceMetadata):
		// A metadata window over budget, or a document whose metadata has
		// a shape the source contract refuses: one book, never the run.
		return models.AuthorMetadataRunItemMetadataParseFailed, true
	}
	var syntax *xml.SyntaxError
	if errors.As(err, &syntax) {
		// A token error after the recognized FictionBook root.
		return models.AuthorMetadataRunItemMetadataParseFailed, true
	}
	return "", false
}

// persistItem writes one successful extraction: the legacy MD5 backfill (only
// while empty), the snapshot with its credits and local jobs, the item's
// terminal status and the run's progress counter — one transaction, so a book
// is never half-recorded.
func (w *AuthorMetadataExtractionWorker) persistItem(
	ctx context.Context,
	run *models.AuthorMetadataRun,
	item *extractionWorkItem,
	metadata *authornorm.SourceMetadata,
) itemOutcome {
	// The mapping is pure: it runs before the transaction, and a refusal means
	// the extraction violated the shape invariants — a bug or a damaged
	// extractor, never a transient condition.
	input, mapErr := ExtractionInputFromSourceMetadata(metadata, models.BookMetadataSnapshotBackfill, &run.ID)
	if errors.Is(mapErr, authornorm.ErrInvalidSourceMetadata) {
		return w.completeTerminalOutcome(ctx, run, item, models.AuthorMetadataRunItemMetadataParseFailed)
	}
	if mapErr != nil {
		return w.abortInvariant(ctx, run, item, mapErr)
	}

	tx, err := w.db.Begin()
	if err != nil {
		return w.failTransient(ctx, run.ID, item, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if item.target.MD5 == "" {
		if fillErr := database.FillBookMD5IfEmpty(ctx, tx, item.target.BookID, metadata.BookMD5); fillErr != nil {
			return w.abortPersist(ctx, tx, &committed, run, item, fillErr)
		}
	}

	result, err := database.PersistExtraction(tx, input)
	if err != nil {
		return w.abortPersist(ctx, tx, &committed, run, item, err)
	}

	status := models.AuthorMetadataRunItemExtracted
	switch {
	case result.Outcome == database.PersistAlreadyCurrent:
		status = models.AuthorMetadataRunItemAlreadyCurrent
	case metadata.Outcome == authornorm.OutcomeNoAuthor:
		status = models.AuthorMetadataRunItemExtractedNoAuthor
	}
	if completeErr := database.CompleteExtractionItem(ctx, tx, item.target.ItemID, w.owner, status, &result.SnapshotID); completeErr != nil {
		return w.abortPersist(ctx, tx, &committed, run, item, completeErr)
	}
	if _, recordErr := database.RecordExtractionItemTerminal(ctx, tx, run.ID); recordErr != nil {
		return w.abortPersist(ctx, tx, &committed, run, item, recordErr)
	}
	if err := tx.Commit(); err != nil {
		return w.abortPersist(ctx, tx, &committed, run, item, err)
	}
	committed = true
	extractionItemCompleted(run, item, status)
	return itemOutcome{terminal: true}
}

// abortPersist rolls the item transaction back and classifies the failure: a
// lost lease discards the result silently (the item is claimable again), a
// schema or invariant error ends the run failed_systemic, anything else is a
// transient database failure worth a retry.
func (w *AuthorMetadataExtractionWorker) abortPersist(
	ctx context.Context,
	tx *pg.Tx,
	committed *bool,
	run *models.AuthorMetadataRun,
	item *extractionWorkItem,
	cause error,
) itemOutcome {
	_ = tx.Rollback()
	*committed = true // the deferred rollback must not run twice
	return w.persistenceFailure(ctx, run, item, cause)
}

// persistenceFailure classifies a failure to persist an item's outcome — a
// successful extraction or a per-book terminal status alike: a lost lease
// discards it (the item is claimable again, nothing counted), a schema or
// invariant error ends the run failed_systemic, anything else is a transient
// database failure worth a retry.
func (w *AuthorMetadataExtractionWorker) persistenceFailure(
	ctx context.Context,
	run *models.AuthorMetadataRun,
	item *extractionWorkItem,
	cause error,
) itemOutcome {
	if errors.Is(cause, authornorm.ErrLeaseLost) {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventExtractionLeaseLost, Stage: AuthorMetadataStageExtraction,
			RunID: run.ID, ItemID: item.target.ItemID,
		})
		return itemOutcome{}
	}
	if isInvariantFailure(cause) {
		return w.abortInvariant(ctx, run, item, cause)
	}
	return w.failTransient(ctx, run.ID, item, cause)
}

// abortInvariant ends the run failed_systemic and records the item's failure
// with the closed invariant class.
func (w *AuthorMetadataExtractionWorker) abortInvariant(
	ctx context.Context,
	run *models.AuthorMetadataRun,
	item *extractionWorkItem,
	cause error,
) itemOutcome {
	extractionWriteFailed(item, run.ID, AuthorMetadataErrorDatabaseInvariant, cause)
	w.failRun(ctx, run.ID, AuthorMetadataErrorDatabaseInvariant)
	if _, err := w.failItems(ctx, []*extractionWorkItem{item}, AuthorMetadataErrorDatabaseInvariant); err != nil {
		extractionWriteFailed(item, run.ID, AuthorMetadataErrorDatabaseInvariant, err)
	}
	return itemOutcome{systemic: true}
}

// failTransient schedules a retry of the item after a database failure.
func (w *AuthorMetadataExtractionWorker) failTransient(
	ctx context.Context,
	runID int64,
	item *extractionWorkItem,
	cause error,
) itemOutcome {
	extractionWriteFailed(item, runID, AuthorMetadataErrorTransientDatabase, cause)
	n, err := w.failItems(ctx, []*extractionWorkItem{item}, AuthorMetadataErrorTransientDatabase)
	if err != nil {
		extractionWriteFailed(item, runID, AuthorMetadataErrorTransientDatabase, err)
	}
	return itemOutcome{terminal: n > 0}
}

// isInvariantFailure reports whether an error from the persistence path is a
// schema or invariant violation (SQLSTATE 23xxx/42xxx or a typed validation
// refusal) rather than a transient condition.
func isInvariantFailure(err error) bool {
	var pgErr pg.Error
	if errors.As(err, &pgErr) {
		code := pgErr.Field('C')
		if strings.HasPrefix(code, "23") || strings.HasPrefix(code, "42") {
			return true
		}
	}
	for _, typed := range []error{
		database.ErrInvalidExtractionBookID, database.ErrInvalidExtractionRunID,
		database.ErrInvalidExtractionOrigin, database.ErrInvalidExtractionOutcome,
		database.ErrInvalidCreditRole, database.ErrInvalidBookMD5,
		authornorm.ErrEmptyVersion, authornorm.ErrNoNameComponents,
	} {
		if errors.Is(err, typed) {
			return true
		}
	}
	return false
}

// completeTerminal ends an item in a per-book terminal status and counts it
// toward the run's extraction progress, in one transaction.
func (w *AuthorMetadataExtractionWorker) completeTerminal(
	ctx context.Context,
	runID int64,
	item *extractionWorkItem,
	status models.AuthorMetadataRunItemStatus,
	snapshotID *int64,
) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	// A lost lease comes back as authornorm.ErrLeaseLost for the caller to
	// classify: the completion was discarded, nothing is counted.
	if err := database.CompleteExtractionItem(ctx, tx, item.target.ItemID, w.owner, status, snapshotID); err != nil {
		return err
	}
	if _, err := database.RecordExtractionItemTerminal(ctx, tx, runID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// failItems records a failed attempt with a closed class for each item, each
// in its own transaction together with the run progress count when the
// failure exhausted the item's attempts. It reports how many items ran out of
// attempts (the poison-book rule: terminal metadata_parse_failed with class
// max_attempts_exceeded, and the run continues).
func (w *AuthorMetadataExtractionWorker) failItems(
	ctx context.Context,
	items []*extractionWorkItem,
	class AuthorMetadataErrorClass,
) (int, error) {
	exhaustedCount := 0
	for _, item := range items {
		tx, err := w.db.Begin()
		if err != nil {
			return exhaustedCount, err
		}
		exhausted, failErr := database.FailExtractionItem(ctx, tx, item.target.ItemID, w.owner,
			w.cfg.Retry.Failure(class, item.claim.AttemptNo))
		if errors.Is(failErr, authornorm.ErrLeaseLost) {
			_ = tx.Rollback()
			continue
		}
		if failErr != nil {
			_ = tx.Rollback()
			return exhaustedCount, failErr
		}
		if exhausted {
			if _, err := database.RecordExtractionItemTerminal(ctx, tx, item.run.ID); err != nil {
				_ = tx.Rollback()
				return exhaustedCount, err
			}
		}
		if err := tx.Commit(); err != nil {
			return exhaustedCount, err
		}
		status := retryScheduledStatus
		if exhausted {
			exhaustedCount++
			status = string(models.AuthorMetadataRunItemMetadataParseFailed)
		}
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventExtractionItemRetried, Stage: AuthorMetadataStageExtraction,
			RunID: item.run.ID, ItemID: item.target.ItemID, BookID: item.target.BookID,
			AttemptNo: item.claim.AttemptNo, Class: class, Status: status,
		})
	}
	return exhaustedCount, nil
}

// pauseSystemic pauses the run for an archive/volume failure. Losing the race
// to pause (another worker paused it, or an administrator completed it) is
// fine: the run is no longer running either way.
func (w *AuthorMetadataExtractionWorker) pauseSystemic(ctx context.Context, runID int64) {
	err := database.PauseRunSystemic(ctx, w.db, runID, string(AuthorMetadataErrorArchiveUnreadable))
	switch {
	case err == nil:
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventExtractionRunPaused, Stage: AuthorMetadataStageExtraction,
			RunID: runID, Class: AuthorMetadataErrorArchiveUnreadable,
		})
	case !errors.Is(err, database.ErrRunTransitionConflict):
		extractionWriteFailed(nil, runID, AuthorMetadataErrorArchiveUnreadable, err)
	}
}

// The seam to the shared mapping helper is the single call of
// ExtractionInputFromSourceMetadata (services/author_metadata_mapping.go) in
// persistItem above: the same mapping serves the backfill worker and the live
// dual write, so a stored snapshot never depends on which path wrote it.
