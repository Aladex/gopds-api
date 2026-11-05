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
// rest, and CoverExists says whether a cover can be served; nil trusts the
// book's flag.
type AuthorDisplayModelConfig struct {
	Batch        int
	Every        time.Duration
	Poll         time.Duration
	SummaryBatch int
	CoverExists  database.CoverExists
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
	// authorSummaryStatementTimeout gives up on one entity's statements rather
	// than hold a batch; the entity is reported and the walk comes back to it.
	authorSummaryStatementTimeout = "5s"
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
	return c
}

// AuthorDisplayModelLoop keeps the author display read model current
// (migration 30): it rebuilds the books writers marked, a batch per
// transaction, and between drains advances the daily walk that rebuilds
// every book — the first walk is the backfill, later ones count what a write
// path without a mark left behind. With nothing of that left it advances the
// author summary by one batch and rests: the summary follows the read model,
// is never ahead of it, and yields to it. The batch does not count as work,
// which is what paces it to one a poll.
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
	return false, l.advanceSummary(ctx)
}

// advanceSummary does one transaction of the author summary: a batch of its
// marks or, with none, the next step of its walk.
func (l *AuthorDisplayModelLoop) advanceSummary(ctx context.Context) error {
	var drained database.AuthorSummaryWork
	var walked database.AuthorSummaryWalkResult
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = '`+authorSummaryStatementTimeout+`'`); err != nil {
			return err
		}
		var err error
		if drained, err = database.DrainAuthorSummaryMarks(ctx, tx, l.cfg.SummaryBatch, l.cfg.CoverExists); err != nil {
			return err
		}
		if drained.Entities > 0 {
			return nil
		}
		walked, err = database.AuthorSummaryWalkStep(ctx, tx, l.cfg.SummaryBatch, l.cfg.Every, l.cfg.CoverExists)
		return err
	})
	if err != nil {
		return err
	}
	for _, skip := range append(drained.Skipped, walked.Skipped...) {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventSummaryEntitySkipped, Stage: AuthorMetadataStageDisplayModel,
			EntityID: skip.EntityID, SQLState: skip.SQLState,
		})
	}
	if walked.Finished {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventSummaryWalkFinished, Stage: AuthorMetadataStageDisplayModel,
			Count: int(walked.Read),
		})
	}
	return nil
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
