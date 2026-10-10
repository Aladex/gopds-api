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
type AuthorDisplayModelConfig struct {
	Batch int
	Every time.Duration
	Poll  time.Duration
}

// Defaults: a batch rebuilds in tens of milliseconds, a walk over the
// catalog takes well under a minute, and the walk is daily.
const (
	defaultAuthorDisplayBatch = 1000
	defaultAuthorDisplayEvery = 24 * time.Hour
	defaultAuthorDisplayPoll  = 2 * time.Second
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
	return c
}

// AuthorDisplayModelLoop keeps the author display read model current
// (migration 30): it rebuilds the books writers marked, a batch per
// transaction, and between drains advances the daily walk that rebuilds
// every book — the first walk is the backfill, later ones count what a write
// path without a mark left behind.
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
		return true, nil
	}
	var step database.AuthorDisplayReconcileStep
	if err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var err error
		step, err = database.ReconcileAuthorDisplayStep(ctx, tx, l.cfg.Batch, l.cfg.Every)
		return err
	}); err != nil {
		return false, err
	}
	if step.Finished {
		// Counts only: how many books the walk found its model rows
		// different from their line — zero when every writer marked.
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventDisplayWalkFinished, Stage: AuthorMetadataStageDisplayModel,
			Count: int(step.WalkDiffered),
		})
	}
	return drained.Marks > 0 || step.Ran, nil
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
