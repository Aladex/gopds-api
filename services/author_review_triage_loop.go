package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authortriage"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// AuthorReviewTriageConfig paces the triage worker. Chunk items are decided
// between two pauses of Pause; Lease is how long a claim holds the batch;
// Poll is the rest when there is nothing to do; LockTimeout and
// StatementTimeout bound each statement of one item's transaction.
// PlanLease is the lease while a dry run plans, and PlanStatementTimeout
// bounds each read of the plan.
type AuthorReviewTriageConfig struct {
	Chunk                int
	Pause                time.Duration
	Lease                time.Duration
	Poll                 time.Duration
	LockTimeout          time.Duration
	StatementTimeout     time.Duration
	PlanLease            time.Duration
	PlanStatementTimeout time.Duration
}

// The defaults of AuthorReviewTriageConfig.
const (
	defaultTriageChunk                = 50
	defaultTriagePause                = time.Second
	defaultTriageLease                = 2 * time.Minute
	defaultTriagePoll                 = 5 * time.Second
	defaultTriageLockTimeout          = 5 * time.Second
	defaultTriageStatementTimeout     = 30 * time.Second
	defaultTriagePlanLease            = 15 * time.Minute
	defaultTriagePlanStatementTimeout = authorEntityReadStatementTimeout
)

func (c AuthorReviewTriageConfig) withDefaults() AuthorReviewTriageConfig {
	if c.Chunk <= 0 {
		c.Chunk = defaultTriageChunk
	}
	if c.Pause <= 0 {
		c.Pause = defaultTriagePause
	}
	if c.Lease <= 0 {
		c.Lease = defaultTriageLease
	}
	if c.Poll <= 0 {
		c.Poll = defaultTriagePoll
	}
	if c.LockTimeout <= 0 {
		c.LockTimeout = defaultTriageLockTimeout
	}
	if c.StatementTimeout <= 0 {
		c.StatementTimeout = defaultTriageStatementTimeout
	}
	if c.PlanLease <= 0 {
		c.PlanLease = defaultTriagePlanLease
	}
	if c.PlanStatementTimeout <= 0 {
		c.PlanStatementTimeout = defaultTriagePlanStatementTimeout
	}
	return c
}

// AuthorReviewTriageLoop is the triage's background worker: it plans a dry
// run, and applies and undoes a plan an administrator asked for. One batch
// works at a time, under a lease, so a second replica or a restart takes it
// over where it stopped.
type AuthorReviewTriageLoop struct {
	db     *pg.DB
	review *AuthorMetadataReviewService
	cfg    AuthorReviewTriageConfig
	owner  uuid.UUID
}

// NewAuthorReviewTriageLoop builds the worker; zero fields take the defaults.
func NewAuthorReviewTriageLoop(
	db *pg.DB, review *AuthorMetadataReviewService, cfg AuthorReviewTriageConfig,
) *AuthorReviewTriageLoop {
	return &AuthorReviewTriageLoop{db: db, review: review, cfg: cfg.withDefaults(), owner: uuid.New()}
}

// authorReviewTriageName is the worker's log label.
const authorReviewTriageName = string(AuthorMetadataStageReviewTriage)

// Name is the worker's log label.
func (l *AuthorReviewTriageLoop) Name() string { return authorReviewTriageName }

// Run works until ctx is canceled: step after step while a batch works, a
// poll interval of rest when there is none or a step failed.
func (l *AuthorReviewTriageLoop) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		worked, err := l.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
				Name: AuthorMetadataEventTriageFailed, Stage: AuthorMetadataStageReviewTriage,
				SQLState: sqlState(err), Worker: authorReviewTriageName,
			})
		}
		if worked && err == nil {
			continue
		}
		if !sleepCtx(ctx, l.cfg.Poll) {
			break
		}
	}
	return nil
}

// sleepCtx rests d, or less when ctx ends; false when it ended.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// RunOnce takes the active batch and does one step of it: the whole plan of
// a planning batch, one chunk of an apply or an undo. It reports whether it
// worked; false while an entity build runs (the worker rests a poll).
func (l *AuthorReviewTriageLoop) RunOnce(ctx context.Context) (bool, error) {
	batch, err := database.ClaimTriageBatch(ctx, l.db, l.owner, l.cfg.Lease)
	if err != nil || batch == nil {
		return false, err
	}
	switch batch.Status {
	case database.TriageBatchPlanning:
		return true, l.plan(ctx, batch)
	case database.TriageBatchApplying, database.TriageBatchUndoing:
		return l.work(ctx, batch)
	}
	return false, nil
}

// Failure classes of a batch: which part failed, never what it read.
const (
	triageFailurePlanRead  = "plan_read"
	triageFailurePlanStore = "plan_store"
)

// plan builds the dry run's plan in one read-only snapshot and stores it.
// A failure ends the batch as failed, with its class only; a shutdown leaves
// it for the next worker.
func (l *AuthorReviewTriageLoop) plan(ctx context.Context, batch *database.TriageBatch) error {
	if err := database.RenewTriageLease(ctx, l.db, batch.ID, l.owner, l.cfg.PlanLease); err != nil {
		return err
	}
	items, counts, err := l.readPlan(ctx)
	if err != nil {
		return l.failPlan(ctx, batch.ID, triageFailurePlanRead, err)
	}
	if err = database.StoreTriagePlan(ctx, l.db, batch.ID, l.owner, items, counts); err != nil {
		if errors.Is(err, database.ErrTriageLeaseLost) || errors.Is(err, database.ErrTriageBatchState) {
			return err
		}
		return l.failPlan(ctx, batch.ID, triageFailurePlanStore, err)
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventTriagePlanned, Stage: AuthorMetadataStageReviewTriage, RunID: batch.ID,
		Count: len(items), Worker: authorReviewTriageName,
	})
	return nil
}

// readPlan builds the plan in one repeatable-read, read-only transaction.
func (l *AuthorReviewTriageLoop) readPlan(ctx context.Context) ([]database.TriagePlanItem, []byte, error) {
	var items []database.TriagePlanItem
	var counts []byte
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY`); err != nil {
			return fmt.Errorf("making the plan's snapshot: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL statement_timeout = %d`,
			l.cfg.PlanStatementTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("bounding the plan's reads: %w", err)
		}
		var err error
		items, counts, err = BuildTriagePlan(ctx, tx)
		return err
	})
	return items, counts, err
}

// failPlan ends the batch as failed unless the worker is shutting down.
func (l *AuthorReviewTriageLoop) failPlan(ctx context.Context, batchID int64, class string, cause error) error {
	if ctx.Err() != nil {
		return cause
	}
	failure := class
	if code := sqlState(cause); code != "" {
		failure += " " + closedSQLState(code)
	}
	if err := database.FailTriageBatch(ctx, l.db, batchID, l.owner, failure); err != nil {
		return err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventError, &AuthorMetadataEvent{
		Name: AuthorMetadataEventTriageFailed, Stage: AuthorMetadataStageReviewTriage, RunID: batchID,
		SQLState: sqlState(cause), Worker: authorReviewTriageName,
	})
	return cause
}

// work decides or undoes one chunk of the batch, then rests a pause; with
// nothing left the batch is applied. A build of the author entities reads
// the credits a decision moves and refuses a pass whose input moved, so
// while one runs nothing is decided. Every item checks in its own
// transaction that the batch still works: one an administrator stopped ends
// the chunk there, also for the items already read. A long chunk records its
// progress, which renews the lease, every third of a lease.
func (l *AuthorReviewTriageLoop) work(ctx context.Context, batch *database.TriageBatch) (bool, error) {
	build, err := database.ActiveAuthorEntityBuild(ctx, l.db)
	if err != nil {
		return false, err
	}
	if build != 0 {
		return false, database.RenewTriageLease(ctx, l.db, batch.ID, l.owner, l.cfg.Lease)
	}
	undo := batch.Status == database.TriageBatchUndoing
	next := database.NextTriageApplyItems
	if undo {
		next = database.NextTriageUndoItems
	}
	items, err := next(ctx, l.db, batch.ID, l.cfg.Chunk)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return true, database.FinishTriageBatch(ctx, l.db, batch.ID, l.owner)
	}
	done := 0
	recorded := time.Now()
	for i := range items {
		if ctx.Err() != nil {
			break
		}
		if undo {
			err = l.undoItem(ctx, batch, &items[i])
		} else {
			err = l.applyItem(ctx, batch, &items[i])
		}
		if errors.Is(err, database.ErrTriageStopped) {
			LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
				Name: AuthorMetadataEventTriageStopped, Stage: AuthorMetadataStageReviewTriage, RunID: batch.ID,
				Worker: authorReviewTriageName,
			})
			return false, nil
		}
		if err != nil {
			return false, err
		}
		done++
		if time.Since(recorded) >= l.cfg.Lease/3 {
			if pErr := database.ProgressTriageBatch(ctx, l.db, batch.ID, l.owner, l.cfg.Lease, done); pErr != nil {
				return false, pErr
			}
			done, recorded = 0, time.Now()
		}
	}
	if pErr := database.ProgressTriageBatch(ctx, l.db, batch.ID, l.owner, l.cfg.Lease, done); pErr != nil {
		return false, pErr
	}
	sleepCtx(ctx, l.cfg.Pause)
	return true, nil
}

// errTriagePinned: the fingerprint was pinned by hand after the dry run.
var errTriagePinned = errors.New("services: the fingerprint is pinned by hand")

// errTriageBatchUnchecked: an item's transaction could not read its batch;
// the item is left as it is and the chunk ends.
var errTriageBatchUnchecked = errors.New("services: the triage batch could not be checked")

// begin bounds every statement of one item's transaction, then checks that
// the batch is still in status and holds it there until the transaction
// ends (database.CheckTriageBatch).
func (l *AuthorReviewTriageLoop) begin(ctx context.Context, tx *pg.Tx, batchID int64, status string) error {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL lock_timeout = %d; SET LOCAL statement_timeout = %d`,
		l.cfg.LockTimeout.Milliseconds(), l.cfg.StatementTimeout.Milliseconds())); err != nil {
		return fmt.Errorf("%w: %w", errTriageBatchUnchecked, err)
	}
	err := database.CheckTriageBatch(ctx, tx, batchID, status)
	if err != nil && !errors.Is(err, database.ErrTriageStopped) {
		return fmt.Errorf("%w: %w", errTriageBatchUnchecked, err)
	}
	return err
}

// applyItem decides one planned item in its own transaction through the
// review service, as the administrator who asked for the apply, and logs
// it. An item that changed since the dry run is skipped: decided or retried
// by hand (conflict), another proposal or a new pin (skipped_changed). Any
// other error fails only this item. A stopped batch, a batch that cannot be
// checked or a shutdown leave the item planned and stop the chunk.
func (l *AuthorReviewTriageLoop) applyItem(ctx context.Context, batch *database.TriageBatch, item *database.TriageWorkItem) error {
	decision, err := triageDecision(item)
	if err == nil {
		_, err = l.review.DecideWithin(ctx, item.ReviewItemID, deref(batch.ApplyRequestedByUserID), decision, DecideHooks{
			Before: func(tx *pg.Tx) error {
				if bErr := l.begin(ctx, tx, batch.ID, database.TriageBatchApplying); bErr != nil {
					return bErr
				}
				pinned, pErr := database.FingerprintPinned(ctx, tx, item.Fingerprint)
				if pErr != nil {
					return pErr
				}
				if pinned {
					return errTriagePinned
				}
				return nil
			},
			After: func(tx *pg.Tx, rep database.ReviewReport) error {
				return database.RecordTriageApplied(ctx, tx, item.ID, rep)
			},
		})
	}
	outcome := database.TriageOutcomeApplied
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, database.ErrTriageStopped), errors.Is(err, errTriageBatchUnchecked):
		return err
	case errors.Is(err, database.ErrTriageItemTaken):
		return nil
	case errors.Is(err, database.ErrReviewConflict), errors.Is(err, database.ErrReviewNotFound):
		outcome = database.TriageOutcomeConflict
	case errors.Is(err, database.ErrReviewProposalChanged), errors.Is(err, errTriagePinned):
		outcome = database.TriageOutcomeSkippedChanged
	default:
		outcome = database.TriageOutcomeFailed
	}
	if outcome != database.TriageOutcomeApplied {
		if rErr := database.RecordTriageOutcome(ctx, l.db, item.ID, outcome, sqlState(err)); rErr != nil {
			return rErr
		}
	}
	l.logItem(batch.ID, item, outcome, err)
	return nil
}

// triageDecision is the review decision of a planned item.
func triageDecision(item *database.TriageWorkItem) (database.ReviewDecision, error) {
	if item.ProposalResultID == nil {
		return database.ReviewDecision{}, database.ErrReviewProposalChanged
	}
	var after authortriage.Name
	if err := json.Unmarshal(item.Planned, &after); err != nil {
		return database.ReviewDecision{}, fmt.Errorf("services: reading the planned name: %w", err)
	}
	d := database.ReviewDecision{Action: database.ReviewAction(item.Action), ExpectProposalResultID: *item.ProposalResultID}
	switch d.Action {
	case database.ReviewAccept, database.ReviewLeaveUnresolved, database.ReviewRetry:
		// An accept keeps the proposal; DecideWithin refuses the other two.
	case database.ReviewClassify:
		d.Kind = models.NormalizationKind(after.Kind)
	case database.ReviewEdit:
		d.Correction = &database.ManualCorrection{GivenName: after.Given, AdditionalName: after.Additional,
			FamilyName: after.Family, Nickname: after.Nickname, Prefix: after.Prefix, Suffix: after.Suffix,
			DisplayName: after.Display, SortName: after.Sort, Kind: models.NormalizationKind(after.Kind)}
	}
	return d, nil
}

// undoItem revokes the override an item's decision wrote and puts its
// fingerprint's credits back in review on the item's proposal, in one
// transaction with its log row. An override that is gone (the book was
// deleted with it, or it was revoked) is gone; credits read under another
// extractor version since block it until the next undo request; anything
// else fails only this item. A stopped batch, one that cannot be checked or
// a shutdown leave the item as it is and stop the chunk.
func (l *AuthorReviewTriageLoop) undoItem(ctx context.Context, batch *database.TriageBatch, item *database.TriageWorkItem) error {
	actor := deref(batch.UndoRequestedByUserID)
	var outcome string
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := l.begin(ctx, tx, batch.ID, database.TriageBatchUndoing); err != nil {
			return err
		}
		if item.OverrideID == nil {
			return database.ErrOverrideNotFound
		}
		if item.ProposalResultID == nil {
			return database.ErrInvalidAutomaticOutcome
		}
		rep, err := database.RevokeOverrideAndRestore(ctx, tx, *item.OverrideID, actor, *item.ProposalResultID,
			authornorm.DecisionClass(item.Class))
		if err != nil {
			return err
		}
		outcome = triageUndoOutcome(rep)
		return database.RecordTriageUndo(ctx, tx, item.ID, actor, outcome)
	})
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, database.ErrTriageStopped), errors.Is(err, errTriageBatchUnchecked):
		return err
	case errors.Is(err, database.ErrTriageItemTaken):
		return nil
	case errors.Is(err, database.ErrOverrideNotFound), errors.Is(err, database.ErrOverrideRevoked):
		outcome = database.TriageUndoGone
	case errors.Is(err, database.ErrRestoreExtractorMoved):
		outcome = database.TriageUndoBlocked
	default:
		outcome = database.TriageUndoFailed
	}
	if err != nil {
		if rErr := database.RecordTriageUndo(ctx, l.db, item.ID, actor, outcome); rErr != nil {
			return rErr
		}
	}
	l.logItem(batch.ID, item, outcome, err)
	return nil
}

// triageUndoOutcome names what a revoke with restore did. Every credit held
// by a newer override is superseded; otherwise the item was restored, also
// when some credit stays on a newer override (Reopened and Superseded), and
// when the book is gone and no credit was left to restore.
func triageUndoOutcome(rep database.RestoreReport) string {
	if rep.Superseded && !rep.Reopened {
		return database.TriageUndoSuperseded
	}
	return database.TriageUndoRestored
}

// logItem logs one item's outcome: identifiers and closed values only.
func (l *AuthorReviewTriageLoop) logItem(batchID int64, item *database.TriageWorkItem, outcome string, err error) {
	level := AuthorMetadataEventInfo
	if err != nil {
		level = AuthorMetadataEventWarn
	}
	LogAuthorMetadataEvent(level, &AuthorMetadataEvent{
		Name: AuthorMetadataEventTriageItem, Stage: AuthorMetadataStageReviewTriage, RunID: batchID,
		ReviewItemID: item.ReviewItemID, Status: outcome, SQLState: sqlState(err), Worker: authorReviewTriageName,
	})
}
