package services

import (
	"context"
	"errors"
	"strings"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The run service is the admin-facing gate of the extraction pipeline
// (contract 3.9): it validates the mode and selector, canonicalizes the book
// ID selector, enforces the approved-pilot rule of a full run, resolves the
// selector against the catalog and delegates the guarded transitions to the
// repository. Validation and approval failures are typed errors the HTTP layer
// maps to 400/409; nothing is seeded on any refusal.

// ErrFullRunNotApproved marks a full run requested before a pilot_archive run
// of the same extractor and normalizer versions completed and was explicitly
// approved.
var ErrFullRunNotApproved = errors.New("services: a full run requires an approved pilot of the same versions")

// ErrRunVersionMismatch marks a run requested for an extractor or normalizer
// version this build does not run: the work it would persist would carry
// other versions than the run declares.
var ErrRunVersionMismatch = errors.New("services: run versions differ from the versions this build runs")

// StartRunRequest is the closed input of StartRun. The selector fields are
// mode-scoped: BookIDs for smoke, Archive for pilot_archive, neither for full;
// a field of another mode is refused, never dropped. Empty versions mean the
// versions this build runs; any other value must equal them.
type StartRunRequest struct {
	Mode              models.AuthorMetadataRunMode
	BookIDs           []int64
	Archive           string
	ExtractorVersion  string
	NormalizerVersion string
	CreatedByUserID   *int64
}

// AuthorMetadataRunService starts and steers extraction runs.
type AuthorMetadataRunService struct {
	db *pg.DB
}

// NewAuthorMetadataRunService wires the service to the catalog database. Its
// runs are for the versions this build runs: the extractor of
// AuthorMetadataExtractorVersion and the local normalizer of
// authornorm.NormalizerVersion, which the shared mapping stamps on every
// normalization job.
func NewAuthorMetadataRunService(db *pg.DB) *AuthorMetadataRunService {
	return &AuthorMetadataRunService{db: db}
}

// runVersions resolves the request's version pair against the build's.
func runVersions(req *StartRunRequest) (extractor, normalizer string, err error) {
	extractor, normalizer = req.ExtractorVersion, req.NormalizerVersion
	if extractor == "" {
		extractor = AuthorMetadataExtractorVersion
	}
	if normalizer == "" {
		normalizer = authornorm.NormalizerVersion
	}
	if extractor != AuthorMetadataExtractorVersion || normalizer != authornorm.NormalizerVersion {
		return "", "", ErrRunVersionMismatch
	}
	return extractor, normalizer, nil
}

// validateRunRequest refuses a selector field that does not belong to the
// mode before anything is looked up or seeded.
func validateRunRequest(req *StartRunRequest) error {
	switch req.Mode {
	case models.AuthorMetadataRunSmoke:
		if req.Archive != "" {
			return database.ErrInvalidRunSelector
		}
	case models.AuthorMetadataRunPilotArchive:
		if len(req.BookIDs) > 0 || strings.TrimSpace(req.Archive) == "" {
			return database.ErrInvalidRunSelector
		}
	case models.AuthorMetadataRunFull:
		if len(req.BookIDs) > 0 || req.Archive != "" {
			return database.ErrInvalidRunSelector
		}
	default:
		return database.ErrInvalidRunSelector
	}
	return nil
}

// StartRun validates the request, resolves the selector and seeds the run with
// its items. The seeded run starts running: the pending status exists for a
// future two-step admin flow, not for this gate. A full run first checks the
// pilot approval of the exact version pair. A second active run is refused by
// PostgreSQL as database.ErrActiveRunExists.
func (s *AuthorMetadataRunService) StartRun(ctx context.Context, req *StartRunRequest) (*models.AuthorMetadataRun, error) {
	if err := validateRunRequest(req); err != nil {
		return nil, err
	}
	extractor, normalizer, err := runVersions(req)
	if err != nil {
		return nil, err
	}
	run := &models.AuthorMetadataRun{
		Mode:              req.Mode,
		Status:            models.AuthorMetadataRunRunning,
		ExtractorVersion:  extractor,
		NormalizerVersion: normalizer,
		CreatedByUserID:   req.CreatedByUserID,
	}

	var bookIDs []int64
	switch req.Mode {
	case models.AuthorMetadataRunSmoke:
		canonical, err := database.CanonicalRunBookIDs(req.BookIDs)
		if err != nil {
			return nil, err
		}
		existing, err := database.ListExistingBookIDs(ctx, s.db, canonical)
		if err != nil {
			return nil, err
		}
		if len(existing) == 0 {
			return nil, database.ErrInvalidRunSelector
		}
		run.SelectorBookIDs = existing
		bookIDs = existing
	case models.AuthorMetadataRunPilotArchive:
		run.SelectorArchive = &req.Archive
		ids, err := database.ListArchiveBookIDs(ctx, s.db, req.Archive)
		if err != nil {
			return nil, err
		}
		bookIDs = ids
	case models.AuthorMetadataRunFull:
		approved, err := database.HasApprovedPilot(ctx, s.db, extractor, normalizer)
		if err != nil {
			return nil, err
		}
		if !approved {
			return nil, ErrFullRunNotApproved
		}
		ids, err := database.ListCatalogBookIDs(ctx, s.db)
		if err != nil {
			return nil, err
		}
		bookIDs = ids
	default:
		return nil, database.ErrInvalidRunSelector
	}

	if err := database.SeedRun(ctx, s.db, run, bookIDs); err != nil {
		return nil, err
	}
	return run, nil
}

// PauseRun stops new claims of a running run. A repeated pause or a pause of
// a terminal run is database.ErrRunTransitionConflict, never a silent success.
func (s *AuthorMetadataRunService) PauseRun(ctx context.Context, runID int64) error {
	return database.TransitionRun(ctx, s.db, runID, models.AuthorMetadataRunRunning, models.AuthorMetadataRunPaused)
}

// ResumeRun continues a paused run. Resuming anything but a paused run is
// database.ErrRunTransitionConflict.
func (s *AuthorMetadataRunService) ResumeRun(ctx context.Context, runID int64) error {
	return database.TransitionRun(ctx, s.db, runID, models.AuthorMetadataRunPaused, models.AuthorMetadataRunRunning)
}

// CompleteIfSettled completes the run once its extraction is terminal and
// every current author credit of its books is accounted for (see
// database.CompleteRunIfSettled). The extraction worker calls it whenever it
// finds nothing to claim; an administrator may call it at any time. It
// reports whether this call completed the run.
func (s *AuthorMetadataRunService) CompleteIfSettled(ctx context.Context, runID int64) (bool, error) {
	return database.CompleteRunIfSettled(ctx, s.db, runID)
}
