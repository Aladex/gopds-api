package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The administrator's side of the run pipeline (admin API phase 16): the
// run service's guarded start and transitions, plus what the admin reads
// and decides about a run — its three stages, the readiness report, the
// approval a full run needs and the retry of failed rows. A retry reopens
// only rows the configured workers can still claim, so the budgets come
// from the same configuration the workers are built from.

// ErrInvalidAuthorMetadataRunAdmin marks a run admin without its run service,
// database or configuration.
var ErrInvalidAuthorMetadataRunAdmin = errors.New("services: invalid author metadata run admin")

// ErrInvalidRetryClass marks a retry whose class is outside the stage's
// closed set.
var ErrInvalidRetryClass = errors.New("services: the error class is not retryable at this stage")

// Readiness reasons of a run report: closed codes, never free text.
const (
	// RunNotReadyNotCompleted: the run has not completed.
	RunNotReadyNotCompleted = "run_not_completed"
	// RunNotReadyExtractionPending: run items are still pending.
	RunNotReadyExtractionPending = "extraction_pending"
	// RunNotReadyCreditsPending: current author credits are not accounted.
	RunNotReadyCreditsPending = "credits_pending"
	// RunNotReadyFiguresPending: the run's final credit figures are being
	// prepared — none stored yet, or only some computed before it finished —
	// and a refresh is under way; no verdict is drawn until it lands.
	RunNotReadyFiguresPending = "figures_pending"
)

// AuthorMetadataExtractionRetryClasses lists the closed classes an
// extraction retry may name: the per-book failure statuses an item ends in
// (archive_unreadable is both a status and an attempt class, listed once)
// and the attempt classes the extraction worker and the lease layer record
// for a book that can recover. The systemic classes (database_invariant,
// version_mismatch, extractor_misconfigured) end the run failed_systemic,
// which no retry reopens.
func AuthorMetadataExtractionRetryClasses() []AuthorMetadataErrorClass {
	return []AuthorMetadataErrorClass{
		AuthorMetadataErrorClass(models.AuthorMetadataRunItemEntryMissing),
		AuthorMetadataErrorClass(models.AuthorMetadataRunItemInvalidFB2),
		AuthorMetadataErrorClass(models.AuthorMetadataRunItemUnsupportedEncoding),
		AuthorMetadataErrorClass(models.AuthorMetadataRunItemMetadataParseFailed),
		AuthorMetadataErrorClass(models.AuthorMetadataRunItemArchiveMissing),
		AuthorMetadataErrorLeaseExpired,
		AuthorMetadataErrorMaxAttemptsExceeded,
		AuthorMetadataErrorTransientDatabase,
		AuthorMetadataErrorArchiveUnreadable,
		AuthorMetadataErrorExtractionFailed,
	}
}

// AuthorMetadataLocalRetryClasses lists the closed classes a local retry may
// name: the lease classes and the worker's failure classes. The refusals
// (no_author_credit, source_not_canonical, normalizer_version_mismatch) are
// properties of the input that a retry cannot change.
func AuthorMetadataLocalRetryClasses() []AuthorMetadataErrorClass {
	return AuthorMetadataErrorClasses()
}

// AuthorMetadataRunAdmin is the administrator's side of the run pipeline.
type AuthorMetadataRunAdmin struct {
	runs                  *AuthorMetadataRunService
	db                    *pg.DB
	extractionMaxAttempts int
	localMaxAttempts      int

	// liveAggregateItems is the largest run whose credit-scale figures a
	// status read computes itself; a larger run reads them stored.
	liveAggregateItems int
	refreshMu          sync.Mutex
	refreshing         map[int64]bool
	refreshes          sync.WaitGroup
}

// The credit-scale figures of a run (local stream, review backlog, credit
// accounting, coverage, storage) read every current author credit of its
// books. Measured on a copy of the catalog (the task report): about 20 ms
// per thousand items, so a run of up to liveAggregateItems items — a smoke
// run, most pilots — computes them on every read, and a larger one (a full
// run, a pilot on a large archive) reads the figures stored by the last
// refresh. A read that finds them missing or old starts one refresh in the
// background and does not wait for it (see aggregates); the extraction loop
// also stores them when the run completes, so a finished run reads its final
// numbers. Stored figures are display: the report's readiness from them is
// advisory, and an approval accounts the credits exactly
// (database.ApprovePilotRun).
const (
	liveAggregateItems = 20000
	aggregateMaxAge    = 30 * time.Second
	// aggregateRefreshTimeout bounds one background refresh.
	aggregateRefreshTimeout = 5 * time.Minute
	// maxProblemArchives bounds the problem archives a run lists.
	maxProblemArchives = 100
)

// NewAuthorMetadataRunAdmin builds the run admin over the run service and the
// configuration the workers are built from.
func NewAuthorMetadataRunAdmin(
	runs *AuthorMetadataRunService, db *pg.DB, c *config.AuthorMetadataConfig,
) (*AuthorMetadataRunAdmin, error) {
	if runs == nil || db == nil || c == nil {
		return nil, ErrInvalidAuthorMetadataRunAdmin
	}
	a := &AuthorMetadataRunAdmin{
		runs: runs, db: db,
		extractionMaxAttempts: ExtractionWorkerConfigFrom("", c).Retry.MaxAttempts,
		localMaxAttempts:      AuthorMetadataLocalWorkerConfigFrom(c).Retry.MaxAttempts,
		liveAggregateItems:    liveAggregateItems,
		refreshing:            map[int64]bool{},
	}
	if a.extractionMaxAttempts <= 0 || a.localMaxAttempts <= 0 {
		return nil, fmt.Errorf("%w: attempt budgets must be positive", ErrInvalidAuthorMetadataRunAdmin)
	}
	return a, nil
}

// AuthorMetadataRunState is one run as the admin sees it. Its stages,
// credits and coverage are the run's stats (database.AuthorMetadataStatsForRun)
// read once, so the status and the report show the stats' own numbers.
type AuthorMetadataRunState struct {
	Run      models.AuthorMetadataRun
	Approved bool
	Stages   database.RunStageStats
	Credits  database.AuthorMetadataCreditStats
	Coverage database.AuthorMetadataCoverage
	// AggregatesAsOf is when the local, review and credit figures were
	// computed; nil while a catalog-sized run has none stored yet.
	AggregatesAsOf *time.Time
	// Seeding is the seeding progress of a full run that has not started.
	Seeding *RunSeeding
	// AggregatesFresh reports whether the credit-scale figures are the run's
	// final ones to show: computed on this read (a small run), or stored no
	// earlier than the run's finish. Readiness drawn from stored figures is
	// advisory; the approval re-checks.
	AggregatesFresh bool
	// growth is the stored size the run's extraction wrote.
	growth int64
}

// RunSeeding is how far a full run's seeding is: items seeded so far and
// the catalog size at its start.
type RunSeeding struct {
	Seeded int64
	Target int64
}

// ErrArchiveNotFailed marks an archive action on an archive the run did not
// find missing or unreadable.
var ErrArchiveNotFailed = errors.New("services: the run found no problem with this archive")

// AuthorMetadataRunReportState is a run with its readiness verdict.
type AuthorMetadataRunReportState struct {
	AuthorMetadataRunState
	Ready           bool
	NotReadyReasons []string
	Facts           database.RunReportFacts
}

// WithLiveAggregateLimit sets the largest run whose figures a read computes
// itself; a larger run reads them stored.
func (a *AuthorMetadataRunAdmin) WithLiveAggregateLimit(items int) *AuthorMetadataRunAdmin {
	a.liveAggregateItems = items
	return a
}

// state reads one run as the admin sees it. Its extraction numbers are
// always read live (cheap at any size: the item tally and the partial
// indexes); its credit-scale figures are computed for a small run and read
// stored for a large one.
func (a *AuthorMetadataRunAdmin) state(ctx context.Context, run *models.AuthorMetadataRun) (AuthorMetadataRunState, error) {
	st := AuthorMetadataRunState{Run: *run}
	var err error
	if st.Approved, err = database.RunApproved(ctx, a.db, run.ID); err != nil {
		return AuthorMetadataRunState{}, err
	}
	extraction, err := database.RunExtractionStats(ctx, a.db, run.ID)
	if err != nil {
		return AuthorMetadataRunState{}, err
	}
	agg, asOf, fresh, err := a.aggregates(ctx, run)
	if err != nil {
		return AuthorMetadataRunState{}, err
	}
	archive, err := database.RunCurrentArchive(ctx, a.db, run.ID)
	if err != nil {
		return AuthorMetadataRunState{}, err
	}
	stats := database.AuthorMetadataStats{
		Extraction: extraction, Local: agg.Local, Review: agg.Review, Coverage: agg.Coverage, Credits: agg.Credits,
	}
	st.Stages = database.RunStageStatsFrom(&stats, archive)
	st.Credits, st.Coverage, st.AggregatesAsOf, st.growth = agg.Credits, agg.Coverage, asOf, agg.GrowthBytes
	st.AggregatesFresh = fresh
	if run.SeedCursor != nil && run.Status == models.AuthorMetadataRunPending {
		target := int64(0)
		if run.SeedTarget != nil {
			target = int64(*run.SeedTarget)
		}
		st.Seeding = &RunSeeding{Seeded: extraction.Total, Target: target}
	}
	return st, nil
}

// large reports whether the run is too large to compute its figures on a
// read: by its items, or by the catalog it is seeding.
func (a *AuthorMetadataRunAdmin) large(run *models.AuthorMetadataRun) bool {
	size := run.ItemsTotal
	if run.SeedTarget != nil {
		size = max(size, *run.SeedTarget)
	}
	return size > a.liveAggregateItems
}

// aggregates returns the run's credit-scale figures, the time they stand
// for and whether they are the run's final ones: computed now for a small
// run; for a large one the stored figures (the zero figures and a nil time
// before any were stored), final once computed no earlier than the run's
// finish. They are display — a decision (approval, completion) accounts the
// credits exactly when it is taken. A background refresh starts when they
// are missing, older than the finish or older than aggregateMaxAge.
func (a *AuthorMetadataRunAdmin) aggregates(
	ctx context.Context, run *models.AuthorMetadataRun,
) (database.RunAggregates, *time.Time, bool, error) {
	if !a.large(run) {
		agg, at, err := database.ComputeRunAggregates(ctx, a.db, run.ID)
		if err != nil {
			return database.RunAggregates{}, nil, false, err
		}
		return agg, &at, true, nil
	}
	loaded, err := database.LoadRunAggregates(ctx, a.db, run.ID)
	if err != nil {
		return database.RunAggregates{}, nil, false, err
	}
	if loaded == nil {
		a.refreshInBackground(run.ID)
		empty := database.RunAggregates{}
		empty.Credits.Unresolved = map[string]int64{}
		return empty, nil, false, nil
	}
	beforeFinish := run.FinishedAt != nil && loaded.ComputedAt.Before(*run.FinishedAt)
	if beforeFinish || loaded.AgeS > aggregateMaxAge.Seconds() {
		a.refreshInBackground(run.ID)
	}
	at := loaded.ComputedAt
	return loaded.Figures, &at, !beforeFinish, nil
}

// refreshInBackground starts one refresh of the run's figures unless one is
// running in this process already. It outlives the request that asked: the
// next read finds the figures stored.
func (a *AuthorMetadataRunAdmin) refreshInBackground(runID int64) {
	a.refreshMu.Lock()
	if a.refreshing[runID] {
		a.refreshMu.Unlock()
		return
	}
	a.refreshing[runID] = true
	a.refreshes.Add(1)
	a.refreshMu.Unlock()
	go func() {
		defer func() {
			a.refreshMu.Lock()
			delete(a.refreshing, runID)
			a.refreshMu.Unlock()
			a.refreshes.Done()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), aggregateRefreshTimeout)
		defer cancel()
		if err := a.RefreshAggregates(ctx, runID); err != nil {
			LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
				Name: AuthorMetadataEventAggregatesRefreshFailed, Stage: AuthorMetadataStageRunner,
				RunID: runID, SQLState: sqlState(err),
			})
		}
	}()
}

// RefreshAggregates computes the run's figures and stores them.
func (a *AuthorMetadataRunAdmin) RefreshAggregates(ctx context.Context, id int64) error {
	return database.RefreshRunAggregates(ctx, a.db, id)
}

// Wait returns once every background refresh this admin started returned.
func (a *AuthorMetadataRunAdmin) Wait() {
	a.refreshes.Wait()
}

// Get reads one run.
func (a *AuthorMetadataRunAdmin) Get(ctx context.Context, id int64) (AuthorMetadataRunState, error) {
	run, err := database.LoadRun(ctx, a.db, id)
	if err != nil {
		return AuthorMetadataRunState{}, err
	}
	return a.state(ctx, run)
}

// Current reads the run in the single active slot; nil when there is none.
func (a *AuthorMetadataRunAdmin) Current(ctx context.Context) (*AuthorMetadataRunState, error) {
	run, err := database.ActiveRun(ctx, a.db)
	if err != nil || run == nil {
		return nil, err
	}
	st, err := a.state(ctx, run)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// Latest reads the most recent run by id, whatever its status; nil when no
// run exists. It backs GET /runs/latest, which the dashboard consults when
// the active slot is empty, so a completed run stays visible without any
// client-side memory of it.
func (a *AuthorMetadataRunAdmin) Latest(ctx context.Context) (*AuthorMetadataRunState, error) {
	run, err := database.LatestRun(ctx, a.db)
	if err != nil || run == nil {
		return nil, err
	}
	st, err := a.state(ctx, run)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// Report reads a run with its readiness: ready only when the run completed,
// every item is terminal and every current author credit is accounted.
func (a *AuthorMetadataRunAdmin) Report(ctx context.Context, id int64) (AuthorMetadataRunReportState, error) {
	st, err := a.Get(ctx, id)
	if err != nil {
		return AuthorMetadataRunReportState{}, err
	}
	facts, err := database.LoadRunReportFacts(ctx, a.db, id, &st.Coverage)
	if err != nil {
		return AuthorMetadataRunReportState{}, err
	}
	facts.DBGrowthBytes = st.growth
	reasons := []string{}
	if st.Run.Status != models.AuthorMetadataRunCompleted {
		reasons = append(reasons, RunNotReadyNotCompleted)
	}
	if st.Stages.Extraction.Done < st.Stages.Extraction.Total {
		reasons = append(reasons, RunNotReadyExtractionPending)
	}
	switch {
	case !st.AggregatesFresh:
		// Missing or stale figures decide nothing, either way: the
		// verdict waits for the refresh the read started.
		reasons = append(reasons, RunNotReadyFiguresPending)
	case !st.Credits.Settled():
		reasons = append(reasons, RunNotReadyCreditsPending)
	}
	return AuthorMetadataRunReportState{
		AuthorMetadataRunState: st, Ready: len(reasons) == 0, NotReadyReasons: reasons, Facts: facts,
	}, nil
}

// Start validates and seeds a run through the run service, which also
// refuses a selector that selects no catalog book.
func (a *AuthorMetadataRunAdmin) Start(ctx context.Context, req *StartRunRequest) (AuthorMetadataRunState, error) {
	run, err := a.runs.StartRun(ctx, req)
	if err != nil {
		return AuthorMetadataRunState{}, err
	}
	return a.Get(ctx, run.ID)
}

// Archives lists the archives the run found missing or unreadable, the most
// books first.
func (a *AuthorMetadataRunAdmin) Archives(ctx context.Context, id int64) ([]database.RunProblemArchive, error) {
	if _, err := database.LoadRun(ctx, a.db, id); err != nil {
		return nil, err
	}
	return database.RunProblemArchives(ctx, a.db, id, maxProblemArchives)
}

// RetryArchive reopens the run's books of one archive it found missing or
// unreadable — after the file came back — within the extraction budget; see
// database.ReopenRunArchive. An archive with nothing to reopen answers 0.
func (a *AuthorMetadataRunAdmin) RetryArchive(ctx context.Context, id int64, archive string) (int64, error) {
	return database.ReopenRunArchive(ctx, a.db, id, archive, a.extractionMaxAttempts)
}

// DeleteArchive requests the deletion of the catalog records of the books
// of an archive the run found missing or unreadable and returns at once: the
// extraction loop deletes them in batches with the existing archive
// deletion's semantics (the books' author metadata layer and run items go
// with them, fingerprint decisions stay; database.DeleteArchiveBatch). A
// second request while one is pending returns that one. Any other archive is
// ErrArchiveNotFailed: this action cleans up what the pass found, it is not
// a general deletion.
func (a *AuthorMetadataRunAdmin) DeleteArchive(ctx context.Context, id int64, archive string) (database.ArchiveDeletion, error) {
	if _, err := database.LoadRun(ctx, a.db, id); err != nil {
		return database.ArchiveDeletion{}, err
	}
	failed, err := database.RunArchiveFailed(ctx, a.db, id, archive)
	if err != nil {
		return database.ArchiveDeletion{}, err
	}
	if !failed {
		return database.ArchiveDeletion{}, ErrArchiveNotFailed
	}
	return database.RequestArchiveDeletion(ctx, a.db, id, archive, nil)
}

// transition applies a guarded transition to an existing run.
func (a *AuthorMetadataRunAdmin) transition(
	ctx context.Context, id int64, apply func(context.Context, int64) error,
) (AuthorMetadataRunState, error) {
	if _, err := database.LoadRun(ctx, a.db, id); err != nil {
		return AuthorMetadataRunState{}, err
	}
	if err := apply(ctx, id); err != nil {
		return AuthorMetadataRunState{}, err
	}
	return a.Get(ctx, id)
}

// Pause stops new claims of a running run.
func (a *AuthorMetadataRunAdmin) Pause(ctx context.Context, id int64) (AuthorMetadataRunState, error) {
	return a.transition(ctx, id, a.runs.PauseRun)
}

// Resume continues a paused run.
func (a *AuthorMetadataRunAdmin) Resume(ctx context.Context, id int64) (AuthorMetadataRunState, error) {
	return a.transition(ctx, id, a.runs.ResumeRun)
}

// ApproveFull records the admin's approval of a completed pilot.
func (a *AuthorMetadataRunAdmin) ApproveFull(ctx context.Context, id, actorUserID int64) (AuthorMetadataRunState, error) {
	if err := database.ApprovePilotRun(ctx, a.db, id, actorUserID); err != nil {
		return AuthorMetadataRunState{}, err
	}
	return a.Get(ctx, id)
}

// Retry reopens the run's failed rows of the stage that failed with the
// class and that the configured workers can still claim; see
// database.ReopenRunRows.
func (a *AuthorMetadataRunAdmin) Retry(
	ctx context.Context, id int64, stage AuthorMetadataStage, class AuthorMetadataErrorClass,
) (int64, error) {
	var classes []AuthorMetadataErrorClass
	var stream database.RetryStream
	var budget int
	switch stage {
	case AuthorMetadataStageExtraction:
		classes, stream, budget = AuthorMetadataExtractionRetryClasses(), database.RetryExtraction, a.extractionMaxAttempts
	case AuthorMetadataStageLocalNormalization:
		classes, stream, budget = AuthorMetadataLocalRetryClasses(), database.RetryLocal, a.localMaxAttempts
	case AuthorMetadataStageDualWrite, AuthorMetadataStageReview, AuthorMetadataStageRunner,
		AuthorMetadataStageAcceptance:
		// The event stages have no rows a retry could reopen.
		return 0, ErrInvalidRetryClass
	default:
		return 0, ErrInvalidRetryClass
	}
	known := false
	for _, c := range classes {
		known = known || c == class
	}
	if !known {
		return 0, ErrInvalidRetryClass
	}
	return database.ReopenRunRows(ctx, a.db, id, stream, string(class), budget)
}
