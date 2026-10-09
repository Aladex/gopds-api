package main

import (
	"context"
	"fmt"
	"time"

	"gopds-api/config"
	"gopds-api/logging"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
)

// authorMetadataShutdownTimeout bounds how long shutdown waits for the author
// metadata workers before the database pool closes regardless. An abandoned
// claim is safe: its lease expires and the next start claims it again.
const authorMetadataShutdownTimeout = 10 * time.Second

// buildAuthorMetadataWorkers assembles every embedded author metadata worker
// from the configuration. This is the one place stages are registered.
func buildAuthorMetadataWorkers(db *pg.DB, c *config.AuthorMetadataConfig) ([]services.AuthorMetadataStageWorker, error) {
	var workers []services.AuthorMetadataStageWorker

	// EXTRACTION STAGE (plan phase 8) — register the extraction worker here
	// once it is merged: c.Extraction.Concurrency loops built from
	// c.Extraction (claim size, lease, max attempts), c.MetadataMaxBytes and
	// c.PollInterval, appended to workers like the local loops below. Nothing
	// else in this file or in the runner changes for it.

	local, err := services.NewAuthorMetadataLocalLoops(db, c)
	if err != nil {
		return nil, fmt.Errorf("building the local normalization workers: %w", err)
	}
	return append(workers, local...), nil
}

// startAuthorMetadataRunner hands the workers to a new runner and starts it
// once the database answers; no worker runs before that.
func startAuthorMetadataRunner(
	ctx context.Context,
	db *pg.DB,
	workers []services.AuthorMetadataStageWorker,
) (*services.AuthorMetadataRunner, error) {
	runner := services.NewAuthorMetadataRunner(workers...)
	ready := func(ctx context.Context) error {
		_, err := db.ExecContext(ctx, `SELECT 1`)
		return err
	}
	if err := runner.Start(ctx, ready); err != nil {
		return nil, err
	}
	return runner, nil
}

// initializeAuthorMetadata starts the workers when the configuration enables
// them. A failure is logged and leaves the server running without them: the
// pipeline is background work, and its jobs wait in the database.
func initializeAuthorMetadata(db *pg.DB, c *config.AuthorMetadataConfig) *services.AuthorMetadataRunner {
	if !c.Enabled {
		logging.Info("Author metadata workers disabled")
		return nil
	}
	workers, err := buildAuthorMetadataWorkers(db, c)
	if err != nil {
		logging.Errorf("Author metadata workers not started: %v", err)
		return nil
	}
	runner, err := startAuthorMetadataRunner(context.Background(), db, workers)
	if err != nil {
		logging.Errorf("Author metadata workers not started: %v", err)
		return nil
	}
	return runner
}

// shutdownAuthorMetadataThenDatabase stops the author metadata workers —
// canceling their claims and database calls, waiting at most timeout — and
// only then closes the database pool.
//
// If the deadline passes while a worker is still running, the pool is left
// open and the error is returned: a canceled goroutine stops only when it
// returns, and closing the pool under it would turn its last call into a
// use-after-close. The process owner then exits; the operating system ends
// the connections with the process, and the jobs the worker held keep only
// their leases, which expire and are claimed again on the next start.
func shutdownAuthorMetadataThenDatabase(runner *services.AuthorMetadataRunner, db *pg.DB, timeout time.Duration) error {
	if runner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := runner.Shutdown(ctx)
		cancel()
		if err != nil {
			logging.Errorf("Author metadata workers: %v; the database pool stays open for them until the process exits", err)
			return err
		}
	}
	closeDatabaseConnection(db)
	return nil
}
