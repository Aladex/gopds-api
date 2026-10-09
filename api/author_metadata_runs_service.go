package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"
)

// The adapter between the runs handlers and the services run admin (phase 8's
// run service underneath): it shapes the contract's Run and Report objects
// and turns every service error into one of the identities runErrorStatus
// maps to a closed code. An error it does not know stays as it is and answers
// internal_error.

// runServiceAdapter serves the runs handlers from the services run admin.
type runServiceAdapter struct {
	admin *services.AuthorMetadataRunAdmin
}

// NewAuthorMetadataRunService adapts the run admin to the handlers.
func NewAuthorMetadataRunService(admin *services.AuthorMetadataRunAdmin) AuthorMetadataRunService {
	return &runServiceAdapter{admin: admin}
}

// SetAuthorMetadataRunService installs the run service SetupAdminRoutes
// mounts. The server calls it once, before the routes are set up.
func SetAuthorMetadataRunService(svc AuthorMetadataRunService) {
	authorMetadataRunService = func() AuthorMetadataRunService { return svc }
}

// runServiceErrors maps the service layer's errors to the handlers'.
var runServiceErrors = []struct{ from, to error }{
	{database.ErrRunNotFound, ErrAuthorMetadataRunNotFound},
	{database.ErrActiveRunExists, ErrAuthorMetadataActiveRunExists},
	{services.ErrFullRunNotApproved, ErrAuthorMetadataFullRunNotApproved},
	{database.ErrRunTransitionConflict, ErrAuthorMetadataInvalidTransition},
	{database.ErrNotACompletedPilot, ErrAuthorMetadataNotACompletedPilot},
	{database.ErrPilotAlreadyApproved, ErrAuthorMetadataAlreadyApproved},
	{database.ErrInvalidRunSelector, ErrAuthorMetadataInvalidSelector},
	{database.ErrInvalidRunBookIDs, ErrAuthorMetadataInvalidBookIDs},
	{services.ErrInvalidRetryClass, ErrAuthorMetadataInvalidErrorClass},
}

func adaptRunError(err error) error {
	for _, m := range runServiceErrors {
		if errors.Is(err, m.from) {
			return fmt.Errorf("%w: %w", m.to, err)
		}
	}
	return err
}

func (a *runServiceAdapter) Start(
	ctx context.Context, start AuthorMetadataRunStart, actorUserID int64,
) (AuthorMetadataRunView, error) {
	req := &services.StartRunRequest{
		Mode: models.AuthorMetadataRunMode(start.Mode), BookIDs: start.BookIDs, CreatedByUserID: &actorUserID,
	}
	if start.Archive != nil {
		req.Archive = *start.Archive
	}
	st, err := a.admin.Start(ctx, req)
	if err != nil {
		return AuthorMetadataRunView{}, adaptRunError(err)
	}
	return runView(&st), nil
}

func (a *runServiceAdapter) Current(ctx context.Context) (*AuthorMetadataRunView, error) {
	st, err := a.admin.Current(ctx)
	if err != nil {
		return nil, adaptRunError(err)
	}
	if st == nil {
		return nil, nil
	}
	view := runView(st)
	return &view, nil
}

func (a *runServiceAdapter) Get(ctx context.Context, id int64) (AuthorMetadataRunView, error) {
	st, err := a.admin.Get(ctx, id)
	if err != nil {
		return AuthorMetadataRunView{}, adaptRunError(err)
	}
	return runView(&st), nil
}

func (a *runServiceAdapter) Report(ctx context.Context, id int64) (AuthorMetadataRunReport, error) {
	st, err := a.admin.Report(ctx, id)
	if err != nil {
		return AuthorMetadataRunReport{}, adaptRunError(err)
	}
	return AuthorMetadataRunReport{
		AuthorMetadataRunView: runView(&st.AuthorMetadataRunState),
		Ready:                 st.Ready,
		NotReadyReasons:       st.NotReadyReasons,
		DurationS:             st.Facts.DurationS,
		DBGrowthBytes:         st.Facts.DBGrowthBytes,
		ByClass:               st.Facts.ByClass,
		ByScript:              st.Facts.ByScript,
	}, nil
}

func (a *runServiceAdapter) Pause(ctx context.Context, id int64) (AuthorMetadataRunView, error) {
	st, err := a.admin.Pause(ctx, id)
	if err != nil {
		return AuthorMetadataRunView{}, adaptRunError(err)
	}
	return runView(&st), nil
}

func (a *runServiceAdapter) Resume(ctx context.Context, id int64) (AuthorMetadataRunView, error) {
	st, err := a.admin.Resume(ctx, id)
	if err != nil {
		return AuthorMetadataRunView{}, adaptRunError(err)
	}
	return runView(&st), nil
}

func (a *runServiceAdapter) ApproveFull(ctx context.Context, id, actorUserID int64) (AuthorMetadataRunView, error) {
	st, err := a.admin.ApproveFull(ctx, id, actorUserID)
	if err != nil {
		return AuthorMetadataRunView{}, adaptRunError(err)
	}
	return runView(&st), nil
}

// retryStages maps the contract's retry stages to the pipeline's.
var retryStages = map[string]services.AuthorMetadataStage{
	retryStageExtraction: services.AuthorMetadataStageExtraction,
	retryStageLocal:      services.AuthorMetadataStageLocalNormalization,
}

func (a *runServiceAdapter) Retry(ctx context.Context, id int64, stage, errorClass string) (int64, error) {
	pipelineStage, ok := retryStages[stage]
	if !ok {
		return 0, ErrAuthorMetadataInvalidErrorClass
	}
	n, err := a.admin.Retry(ctx, id, pipelineStage, services.AuthorMetadataErrorClass(errorClass))
	if err != nil {
		return 0, adaptRunError(err)
	}
	return n, nil
}

// runView shapes the contract's Run object.
func runView(st *services.AuthorMetadataRunState) AuthorMetadataRunView {
	run := &st.Run
	ex := &st.Stages.Extraction
	unresolved := make(map[string]int64, len(st.Credits.Unresolved))
	for reason, n := range st.Credits.Unresolved {
		unresolved[reason] = n
	}
	return AuthorMetadataRunView{
		ID: run.ID, Mode: string(run.Mode), Status: string(run.Status),
		ExtractorVersion: run.ExtractorVersion, NormalizerVersion: run.NormalizerVersion,
		CreatedAt: run.CreatedAt, StartedAt: run.StartedAt, ExtractionCompletedAt: run.ExtractionCompletedAt,
		CompletedAt: completedAt(run), LastErrorClass: run.LastErrorClass, ApprovedForFull: st.Approved,
		Stages: AuthorMetadataRunStages{
			Extraction: AuthorMetadataExtractionStage{
				Total: ex.Total, Done: ex.Done, Pending: ex.Pending, Leased: ex.Leased,
				OldestPendingAgeS: ex.OldestPendingAgeS,
				ByStatus: AuthorMetadataExtractionByStatus{
					Extracted:           ex.ByStatus[models.AuthorMetadataRunItemExtracted],
					ExtractedNoAuthor:   ex.ByStatus[models.AuthorMetadataRunItemExtractedNoAuthor],
					AlreadyCurrent:      ex.ByStatus[models.AuthorMetadataRunItemAlreadyCurrent],
					EntryMissing:        ex.ByStatus[models.AuthorMetadataRunItemEntryMissing],
					InvalidFB2:          ex.ByStatus[models.AuthorMetadataRunItemInvalidFB2],
					UnsupportedEncoding: ex.ByStatus[models.AuthorMetadataRunItemUnsupportedEncoding],
					MetadataParseFailed: ex.ByStatus[models.AuthorMetadataRunItemMetadataParseFailed],
				},
				CurrentArchive: ex.CurrentArchive, ItemsPerMinute: ex.ItemsPerMinute,
			},
			Local: AuthorMetadataLocalStage{
				Total: st.Stages.Local.Total, Done: st.Stages.Local.Done, Pending: st.Stages.Local.Pending,
				Leased: st.Stages.Local.Leased, Failed: st.Stages.Local.Failed,
				OldestPendingAgeS: st.Stages.Local.OldestPendingAgeS,
			},
			Review: AuthorMetadataReviewStage{Open: st.Stages.Review.Open, Closed: st.Stages.Review.Closed},
		},
		Credits: AuthorMetadataCreditCounts{
			Selected: st.Credits.Selected, Invalid: st.Credits.Invalid,
			Review: st.Credits.Review, Pending: st.Credits.Pending, Unresolved: unresolved,
		},
	}
}

// completedAt is the contract's completed_at: when a completed run finished.
// A run that ended failed_systemic has a finish time but never completed.
func completedAt(run *models.AuthorMetadataRun) *time.Time {
	if run.Status != models.AuthorMetadataRunCompleted {
		return nil
	}
	return run.FinishedAt
}
