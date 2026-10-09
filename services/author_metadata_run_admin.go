package services

import (
	"context"
	"errors"
	"fmt"

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
)

// AuthorMetadataExtractionRetryClasses lists the closed classes an
// extraction retry may name: the per-book failure statuses an item ends in
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
}

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
	}
	if a.extractionMaxAttempts <= 0 || a.localMaxAttempts <= 0 {
		return nil, fmt.Errorf("%w: attempt budgets must be positive", ErrInvalidAuthorMetadataRunAdmin)
	}
	return a, nil
}

// AuthorMetadataRunState is one run as the admin sees it.
type AuthorMetadataRunState struct {
	Run      models.AuthorMetadataRun
	Approved bool
	Stages   database.RunStageStats
	Credits  database.CreditAccounting
}

// AuthorMetadataRunReportState is a run with its readiness verdict.
type AuthorMetadataRunReportState struct {
	AuthorMetadataRunState
	Ready           bool
	NotReadyReasons []string
	Facts           database.RunReportFacts
}

func (a *AuthorMetadataRunAdmin) state(ctx context.Context, run *models.AuthorMetadataRun) (AuthorMetadataRunState, error) {
	st := AuthorMetadataRunState{Run: *run}
	var err error
	if st.Approved, err = database.RunApproved(ctx, a.db, run.ID); err != nil {
		return AuthorMetadataRunState{}, err
	}
	if st.Stages, err = database.LoadRunStageStats(ctx, a.db, run); err != nil {
		return AuthorMetadataRunState{}, err
	}
	if st.Credits, err = database.AuthorCreditAccountingForRun(ctx, a.db, run.ID); err != nil {
		return AuthorMetadataRunState{}, err
	}
	return st, nil
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

// Report reads a run with its readiness: ready only when the run completed,
// every item is terminal and every current author credit is accounted.
func (a *AuthorMetadataRunAdmin) Report(ctx context.Context, id int64) (AuthorMetadataRunReportState, error) {
	st, err := a.Get(ctx, id)
	if err != nil {
		return AuthorMetadataRunReportState{}, err
	}
	facts, err := database.LoadRunReportFacts(ctx, a.db, id)
	if err != nil {
		return AuthorMetadataRunReportState{}, err
	}
	reasons := []string{}
	if st.Run.Status != models.AuthorMetadataRunCompleted {
		reasons = append(reasons, RunNotReadyNotCompleted)
	}
	if st.Stages.Extraction.Done < st.Stages.Extraction.Total {
		reasons = append(reasons, RunNotReadyExtractionPending)
	}
	if !st.Credits.Settled() {
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
	return a.state(ctx, run)
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
