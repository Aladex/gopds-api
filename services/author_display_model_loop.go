package services

import (
	"context"
	"time"

	"gopds-api/database"

	"github.com/go-pg/pg/v10"
)

// AuthorDisplayModelConfig bounds the read-model worker: how many marks or
// books one transaction takes, how long after a finished walk the next one
// starts, and how long the worker rests when there is nothing to do.
// SummaryBatch is how many entities of the author summary it takes in each
// rest, and SummaryTimeout bounds each statement of one entity. VariantBatch
// and VariantTimeout are the same for the book variants read model, one book
// at a time.
type AuthorDisplayModelConfig struct {
	Batch          int
	Every          time.Duration
	Poll           time.Duration
	SummaryBatch   int
	SummaryTimeout time.Duration
	VariantBatch   int
	VariantTimeout time.Duration
}

// Defaults: a batch rebuilds in tens of milliseconds, a walk over the
// catalog takes well under a minute, and the walk is daily.
const (
	defaultAuthorDisplayBatch = 1000
	defaultAuthorDisplayEvery = 24 * time.Hour
	defaultAuthorDisplayPoll  = 2 * time.Second
	// A batch of the summary every poll keeps a walk of the catalog to a few
	// hours of one small query at a time, on a database that serves readers.
	defaultAuthorSummaryBatch = 40
	// defaultAuthorSummaryTimeout gives up on one entity's statements rather
	// than hold a batch; the entity is reported and passed, and the next walk
	// comes back to it.
	defaultAuthorSummaryTimeout = 5 * time.Second
	// The book variants read model goes at the summary's pace: a batch a
	// poll, a book (its partition) a transaction.
	defaultBookVariantBatch   = 40
	defaultBookVariantTimeout = 5 * time.Second
)

func (c AuthorDisplayModelConfig) withDefaults() AuthorDisplayModelConfig {
	if c.Batch <= 0 {
		c.Batch = defaultAuthorDisplayBatch
	}
	if c.Every <= 0 {
		c.Every = defaultAuthorDisplayEvery
	}
	if c.Poll <= 0 {
		c.Poll = defaultAuthorDisplayPoll
	}
	if c.SummaryBatch <= 0 {
		c.SummaryBatch = defaultAuthorSummaryBatch
	}
	if c.SummaryTimeout <= 0 {
		c.SummaryTimeout = defaultAuthorSummaryTimeout
	}
	if c.VariantBatch <= 0 {
		c.VariantBatch = defaultBookVariantBatch
	}
	if c.VariantTimeout <= 0 {
		c.VariantTimeout = defaultBookVariantTimeout
	}
	return c
}

// AuthorDisplayModelLoop keeps the author display read model current
// (migration 30): it rebuilds the books writers marked, a batch per
// transaction, and between drains advances the daily walk that rebuilds
// every book — the first walk is the backfill, later ones count what a write
// path without a mark left behind. With nothing of that left it advances the
// author summary by one batch, then the book variants read model by one, and
// rests: both follow the read model, are never ahead of it, and yield to it.
// Their batches do not count as work, which is what paces them to one a poll.
type AuthorDisplayModelLoop struct {
	db  *pg.DB
	cfg AuthorDisplayModelConfig
}

// NewAuthorDisplayModelLoop builds the worker; zero fields take the defaults.
func NewAuthorDisplayModelLoop(db *pg.DB, cfg AuthorDisplayModelConfig) *AuthorDisplayModelLoop {
	return &AuthorDisplayModelLoop{db: db, cfg: cfg.withDefaults()}
}

// Name is the worker's log label.
func (l *AuthorDisplayModelLoop) Name() string { return string(AuthorMetadataStageDisplayModel) }

// RunOnce does one transaction of work: a batch of marks, or — when the
// queue held less than a batch — the next step of a walk that is due. It
// reports whether there was any work.
func (l *AuthorDisplayModelLoop) RunOnce(ctx context.Context) (bool, error) {
	var drained database.AuthorDisplayDrain
	if err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var err error
		drained, err = database.DrainAuthorDisplayMarks(ctx, tx, l.cfg.Batch)
		return err
	}); err != nil {
		return false, err
	}
	if drained.Marks >= l.cfg.Batch {
		logSkippedAuthorDisplay(drained.Skipped)
		return true, nil
	}
	var step database.AuthorDisplayReconcileStep
	if err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var err error
		step, err = database.ReconcileAuthorDisplayStep(ctx, tx, l.cfg.Batch, l.cfg.Every)
		return err
	}); err != nil {
		// The drain committed: its skipped books are reported all the same.
		logSkippedAuthorDisplay(drained.Skipped)
		return false, err
	}
	// A book both the drain and the walk step refused is reported once.
	logSkippedAuthorDisplay(append(drained.Skipped, step.Skipped...))
	if step.Finished {
		// Counts only: how many books the walk found its model rows
		// different from their line — zero when every writer marked.
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventDisplayWalkFinished, Stage: AuthorMetadataStageDisplayModel,
			Count: int(step.WalkDiffered),
		})
	}
	if drained.Marks > 0 || step.Ran {
		return true, nil
	}
	summaryErr := l.advanceSummary(ctx)
	// The variants do not wait on the summary: a summary that fails keeps
	// failing, and the cards would never be built behind it.
	variantErr := l.advanceVariants(ctx)
	if summaryErr != nil {
		return false, summaryErr
	}
	return false, variantErr
}

// advanceVariants does one batch of the book variants read model, a book to a
// transaction: its marks or, with none, the next step of its walk. The batch
// stops taking books after VariantTimeout of wall clock, or after a refused
// book, so the author display work waits a few seconds at most. What
// committed before an error is reported all the same.
func (l *AuthorDisplayModelLoop) advanceVariants(ctx context.Context) error {
	deadline := time.Now().Add(l.cfg.VariantTimeout)
	drained, err := database.DrainBookVariantMarks(ctx, l.db, l.cfg.VariantBatch, l.cfg.VariantTimeout, deadline)
	skipped := drained.Skipped
	if err == nil && drained.Books == 0 {
		var walked database.BookVariantWalkResult
		walked, err = database.BookVariantWalkStep(ctx, l.db, l.cfg.VariantBatch, l.cfg.Every, l.cfg.VariantTimeout, deadline)
		skipped = append(skipped, walked.Skipped...)
		if walked.Finished {
			LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
				Name: AuthorMetadataEventVariantWalkFinished, Stage: AuthorMetadataStageDisplayModel,
				Count: int(walked.Read),
			})
		}
	}
	logSkippedBookVariants(skipped)
	return err
}

// logSkippedBookVariants records each book the variants read model refused,
// by its ID and the SQLSTATE.
func logSkippedBookVariants(skipped []database.BookVariantSkip) {
	for _, skip := range skipped {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventVariantBookSkipped, Stage: AuthorMetadataStageDisplayModel,
			BookID: skip.BookID, SQLState: skip.SQLState,
		})
	}
}

// advanceSummary does one batch of the author summary, an entity to a
// transaction: its marks or, with none, the next step of its walk. What
// committed before an error is reported all the same.
func (l *AuthorDisplayModelLoop) advanceSummary(ctx context.Context) error {
	drained, err := database.DrainAuthorSummaryMarks(ctx, l.db, l.cfg.SummaryBatch, l.cfg.SummaryTimeout)
	logSkippedAuthorSummaries(drained.Skipped)
	if err != nil || drained.Entities > 0 {
		return err
	}
	walked, err := database.AuthorSummaryWalkStep(ctx, l.db, l.cfg.SummaryBatch, l.cfg.Every, l.cfg.SummaryTimeout)
	logSkippedAuthorSummaries(walked.Skipped)
	if walked.Finished {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventSummaryWalkFinished, Stage: AuthorMetadataStageDisplayModel,
			Count: int(walked.Read),
		})
	}
	return err
}

// logSkippedAuthorSummaries records each entity the summary refused, by its ID
// and the SQLSTATE.
func logSkippedAuthorSummaries(skipped []database.AuthorSummarySkip) {
	for _, skip := range skipped {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventSummaryEntitySkipped, Stage: AuthorMetadataStageDisplayModel,
			EntityID: skip.EntityID, SQLState: skip.SQLState,
		})
	}
}

// logSkippedAuthorDisplay records each book the model refused, once, by its
// ID and the SQLSTATE — never a name.
func logSkippedAuthorDisplay(skipped []database.AuthorDisplaySkip) {
	seen := make(map[int64]bool, len(skipped))
	for _, skip := range skipped {
		if seen[skip.BookID] {
			continue
		}
		seen[skip.BookID] = true
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventDisplayBookSkipped, Stage: AuthorMetadataStageDisplayModel,
			BookID: skip.BookID, SQLState: skip.SQLState,
		})
	}
}

// Run works until ctx is canceled: batch after batch while there is work,
// a poll interval of rest when there is none or a batch failed.
func (l *AuthorDisplayModelLoop) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		worked, err := l.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
				Name: AuthorMetadataEventWorkerBatchFailed, Stage: AuthorMetadataStageDisplayModel,
				Worker: l.Name(), SQLState: AuthorMetadataSQLState(err),
			})
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(l.cfg.Poll):
		}
	}
	return nil
}
