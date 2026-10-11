package database

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/go-pg/pg/v10"
)

// The LLM lock order. Every transaction that touches an LLM run and the rows
// of its work takes its locks in this order and never waits for an earlier
// lock while it holds a later one:
//
//  1. the claim lock (pg_advisory_xact_lock(authorLLMClaimLock)) — claims and
//     settles only, so no two of them overlap;
//  2. the run rows, in id order, through lockAuthorLLMRuns — taken before any
//     row of the run's work, in the mode the transaction needs:
//     - authorLLMRunWrite (FOR NO KEY UPDATE): it changes the run's row — the
//     claim, which may pause the run on its budget, and the report store;
//     - authorLLMRunHold (FOR SHARE): it writes the run's work and needs the
//     run's status to stay as read until it commits — the stage enqueue,
//     which a pause then waits for, so a stage gets all its jobs or none;
//     - authorLLMRunRef (FOR KEY SHARE): it writes rows of the run's work and
//     never the run — the settle; this conflicts with no status change.
//     A one-statement change of a run (pause, resume, complete, fail, stage,
//     learned references, estimate) locks only that row: it is at step 2
//     and waits for nothing after it;
//  3. participant and endpoint state, calls, attempts, jobs, verdicts — under
//     the claim lock in whatever order the claim or settle needs (the claim
//     lock keeps any two such transactions apart); outside it only
//     one-statement writers touch them (a job's excerpt record, a
//     participant's resume) and the enqueues, which insert new job rows;
//  4. the token tally, written by the calls' trigger and folded on its own.
//
// The cycle this order removes: an enqueue holding its run SHARE waits on a
// job row (a duplicate INSERT conflicts with a job the worker is updating),
// while the worker, holding that job, waits on the run row to pause it. With
// the run row first, the worker locks the run before it touches a job — and
// skips a run an enqueue holds, rather than wait on it under the claim lock.

// authorLLMTxAttempts bounds the attempts of an LLM transaction that
// PostgreSQL aborted as a deadlock victim (40P01) or a serialization failure
// (40001). The lock order (lockAuthorLLMRuns) is what keeps deadlocks out;
// the retry is only a safety net, and every retry is counted.
const authorLLMTxAttempts = 3

// The SQLSTATEs of a transaction PostgreSQL aborted for its concurrency.
const (
	sqlStateDeadlock      = "40P01"
	sqlStateSerialization = "40001"
)

var authorLLMTxRetries atomic.Int64

// AuthorLLMTxRetries is how many LLM transactions were retried after a
// deadlock or a serialization failure since the process started.
func AuthorLLMTxRetries() int64 { return authorLLMTxRetries.Load() }

// RunAuthorLLMTx runs fn in a transaction, again (a bounded number of times)
// when PostgreSQL aborted it as a deadlock victim or a serialization failure:
// the abort rolled everything back, so fn starts over from nothing.
func RunAuthorLLMTx(ctx context.Context, db *pg.DB, fn func(*pg.Tx) error) error {
	for attempt := 1; ; attempt++ {
		err := db.RunInTransaction(ctx, fn)
		if attempt == authorLLMTxAttempts || !retryableAuthorLLMTxError(err) {
			return err
		}
		authorLLMTxRetries.Add(1)
	}
}

func retryableAuthorLLMTxError(err error) bool {
	code := pgErrorCode(err)
	return code == sqlStateDeadlock || code == sqlStateSerialization
}

// authorLLMRunLock is the row-lock mode a transaction takes on a run.
type authorLLMRunLock string

// The run lock modes, by what the transaction does to the run.
const (
	authorLLMRunWrite authorLLMRunLock = "NO KEY UPDATE"
	authorLLMRunHold  authorLLMRunLock = "SHARE"
	authorLLMRunRef   authorLLMRunLock = "KEY SHARE"
)

// lockAuthorLLMRuns is step 2 of the lock order: it locks the runs' rows in
// id order and returns the ids it locked — all of them, or with skipLocked
// those no other transaction holds in a conflicting mode.
func lockAuthorLLMRuns(
	ctx context.Context, tx *pg.Tx, mode authorLLMRunLock, skipLocked bool, ids []int64,
) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query := `SELECT id FROM author_llm_run WHERE id IN (?) ORDER BY id FOR ` + string(mode)
	if skipLocked {
		query += ` SKIP LOCKED`
	}
	var locked []int64
	if _, err := tx.QueryContext(ctx, &locked, query, pg.In(ids)); err != nil {
		return nil, fmt.Errorf("locking LLM runs: %w", err)
	}
	return locked, nil
}
