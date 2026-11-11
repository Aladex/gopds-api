package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopds-api/database"

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
// a planning batch. It reports whether there was a batch to work on.
func (l *AuthorReviewTriageLoop) RunOnce(ctx context.Context) (bool, error) {
	batch, err := database.ClaimTriageBatch(ctx, l.db, l.owner, l.cfg.Lease)
	if err != nil || batch == nil {
		return false, err
	}
	if batch.Status == database.TriageBatchPlanning {
		return true, l.plan(ctx, batch)
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
