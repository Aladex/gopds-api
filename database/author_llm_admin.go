package database

import (
	"context"
	"errors"
	"fmt"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The reads the admin card's LLM block is drawn from: which run to show, how
// far its stage is, what it spent, and the participants' pauses. Every one is
// a short read; none takes a lock.

// ErrAuthorLLMRunNotFound marks a run id with no run.
var ErrAuthorLLMRunNotFound = errors.New("database: LLM run not found")

// AuthorLLMRunToShow is the run the card shows: the active run (running or
// paused, of any configuration — there is one at a time), else the latest
// run of the configuration. Nil when there is none.
func AuthorLLMRunToShow(ctx context.Context, db pg.DBI, version string) (*models.AuthorLLMRun, error) {
	var run models.AuthorLLMRun
	_, err := db.QueryOneContext(ctx, &run, `
		SELECT * FROM author_llm_run
		WHERE status IN ('pending', 'running', 'paused') OR config_version = ?0
		ORDER BY status IN ('pending', 'running', 'paused') DESC, id DESC
		LIMIT 1`, version)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the LLM run to show: %w", err)
	}
	return &run, nil
}

// AuthorLLMRunToShowOf is AuthorLLMRunToShow for one of the two live
// configurations (E4c): the active run of a configuration of the same task
// — the name normalization's (merge false), whatever its version, or the
// merge one's — else the latest of the version; and whether a run of the
// other task is active (it holds the one active slot, its own block shows
// it).
func AuthorLLMRunToShowOf(
	ctx context.Context, db pg.DBI, version string, merge bool,
) (run *models.AuthorLLMRun, otherActive bool, err error) {
	var row models.AuthorLLMRun
	_, err = db.QueryOneContext(ctx, &row, `
		SELECT r.* FROM author_llm_run r JOIN author_llm_config c ON c.version = r.config_version
		WHERE (r.status IN ('pending', 'running', 'paused') AND (coalesce(c.identity->>'task', '') = 'merge') = ?1)
			OR r.config_version = ?0
		ORDER BY r.status IN ('pending', 'running', 'paused') DESC, r.id DESC
		LIMIT 1`, version, merge)
	switch {
	case errors.Is(err, pg.ErrNoRows):
	case err != nil:
		return nil, false, fmt.Errorf("reading the LLM run to show: %w", err)
	default:
		run = &row
	}
	if _, err = db.QueryOneContext(ctx, pg.Scan(&otherActive), `
		SELECT EXISTS (SELECT 1 FROM author_llm_run r JOIN author_llm_config c ON c.version = r.config_version
			WHERE r.status IN ('pending', 'running', 'paused') AND (coalesce(c.identity->>'task', '') = 'merge') <> ?0)`,
		merge); err != nil {
		return nil, false, fmt.Errorf("looking for the other configuration's run: %w", err)
	}
	return run, otherActive, nil
}

// GetAuthorLLMRunByID reads one run; ErrAuthorLLMRunNotFound when there is
// none.
func GetAuthorLLMRunByID(ctx context.Context, db pg.DBI, id int64) (*models.AuthorLLMRun, error) {
	run, err := GetAuthorLLMRun(ctx, db, id)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, ErrAuthorLLMRunNotFound
	}
	return run, err
}

// AuthorLLMCompletedRuns lists the kind/mode pairs of the configuration that
// have a completed run ("eval/pilot", "eval/full", ...).
func AuthorLLMCompletedRuns(ctx context.Context, db pg.DBI, version string) (map[string]bool, error) {
	var pairs []string
	if _, err := db.QueryContext(ctx, &pairs, `
		SELECT DISTINCT kind || '/' || mode FROM author_llm_run
		WHERE config_version = ?0 AND status = 'completed'`, version); err != nil {
		return nil, fmt.Errorf("reading the completed LLM runs: %w", err)
	}
	out := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		out[p] = true
	}
	return out, nil
}

// AuthorLLMJobProgress counts a run's jobs of one purpose: all of them, and
// those that ended (answered, invalid or failed).
func AuthorLLMJobProgress(ctx context.Context, db pg.DBI, runID int64, purpose models.AuthorLLMJobPurpose) (done, total int, err error) {
	if _, err = db.QueryOneContext(ctx, pg.Scan(&done, &total), `
		SELECT count(*) FILTER (WHERE status IN ('answered', 'invalid', 'failed')), count(*)
		FROM author_llm_job WHERE run_id = ?0 AND purpose = ?1`, runID, string(purpose)); err != nil {
		return 0, 0, fmt.Errorf("counting an LLM run's jobs: %w", err)
	}
	return done, total, nil
}

// AuthorLLMTokensBySlot is what each participant's calls of a run spent: the
// settled tokens, or the reserve of a call not settled yet.
func AuthorLLMTokensBySlot(ctx context.Context, db pg.DBI, runID int64) (map[string]int64, error) {
	var rows []struct {
		Slot   string `pg:"slot"`
		Tokens int64  `pg:"tokens"`
	}
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT slot, coalesce(sum(coalesce(settled_tokens, reserved_tokens)), 0) AS tokens
		FROM author_llm_call WHERE run_id = ?0 GROUP BY slot`, runID); err != nil {
		return nil, fmt.Errorf("reading an LLM run's tokens by participant: %w", err)
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Slot] = r.Tokens
	}
	return out, nil
}

// AuthorLLMTokensSpentAll is what every run spent together: the figure the
// one-off budget bounds.
func AuthorLLMTokensSpentAll(ctx context.Context, db pg.DBI) (int64, error) {
	var spent int64
	if _, err := db.QueryOneContext(ctx, pg.Scan(&spent),
		`SELECT coalesce(sum(tokens), 0) FROM author_llm_token_tally`); err != nil {
		return 0, fmt.Errorf("reading the LLM tokens spent: %w", err)
	}
	return spent, nil
}

// AuthorLLMProviderStates lists the participants' shared states of a
// configuration.
func AuthorLLMProviderStates(ctx context.Context, db pg.DBI, version string) ([]models.AuthorLLMProviderState, error) {
	var states []models.AuthorLLMProviderState
	if _, err := db.QueryContext(ctx, &states,
		`SELECT * FROM author_llm_provider_state WHERE config_version = ?0 ORDER BY slot`, version); err != nil {
		return nil, fmt.Errorf("reading the LLM participants' states: %w", err)
	}
	return states, nil
}

// authorLLMRunClosed is the error class of a call its run's closing ended.
const authorLLMRunClosed = "run_closed"

// CloseAuthorLLMRun ends an active run for good, as the administrator actor:
// one of a configuration other than current (no worker of the current one
// would ever move it), or a paused one. It follows the lock order: the claim
// lock, then the run's row, then its open calls. Every open call is closed as
// abandoned and keeps its reserve as spent — what the run was charged does
// not change — and its lease owner is cleared, so a late reply settles
// nothing (ErrAuthorLLMLeaseLost). The calls' claimed jobs go back to pending
// without a failure: they are no fault of theirs. The run leaves the active
// slot. A run that may not be closed is ErrAuthorLLMRunState.
func CloseAuthorLLMRun(ctx context.Context, db *pg.DB, id int64, current string, actor *int64) error {
	return RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		now, err := lockAuthorLLM(ctx, tx, nil)
		if err != nil {
			return err
		}
		locked, err := lockAuthorLLMRuns(ctx, tx, authorLLMRunWrite, false, []int64{id})
		if err != nil {
			return err
		}
		if len(locked) == 0 {
			return ErrAuthorLLMRunNotFound
		}
		var run struct {
			Status        string `pg:"status"`
			ConfigVersion string `pg:"config_version"`
		}
		if _, err = tx.QueryOneContext(ctx, &run,
			`SELECT status, config_version FROM author_llm_run WHERE id = ?0`, id); err != nil {
			return fmt.Errorf("reading an LLM run: %w", err)
		}
		active := run.Status == string(models.AuthorLLMRunPending) || run.Status == string(models.AuthorLLMRunRunning) ||
			run.Status == string(models.AuthorLLMRunPaused)
		if !active || (run.ConfigVersion == current && run.Status != string(models.AuthorLLMRunPaused)) {
			return ErrAuthorLLMRunState
		}
		if _, err = tx.ExecContext(ctx, `
			WITH open AS (
				SELECT id FROM author_llm_call WHERE run_id = ?0 AND finished_at IS NULL FOR UPDATE
			), closed AS (
				UPDATE author_llm_call c
				SET finished_at = ?1, outcome = 'abandoned', error_class = ?2, settled_tokens = c.reserved_tokens,
					lease_owner = NULL, lease_expires_at = NULL
				FROM open o WHERE c.id = o.id
				RETURNING c.id
			), attempts AS (
				UPDATE author_llm_attempt a SET finished_at = ?1, outcome = 'abandoned'
				FROM closed WHERE a.call_id = closed.id AND a.finished_at IS NULL
				RETURNING a.job_id
			), checks AS (
				UPDATE author_llm_provider_state p SET check_call_id = NULL
				FROM closed WHERE p.check_call_id = closed.id
				RETURNING p.slot
			)
			UPDATE author_llm_job j SET call_id = NULL, status = 'pending'
			FROM attempts WHERE j.id = attempts.job_id AND j.status = 'claimed'`,
			id, now, authorLLMRunClosed); err != nil {
			return fmt.Errorf("closing an LLM run's calls: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE author_llm_run SET status = 'closed', paused_reason = NULL, finished_at = now(),
				closed_by_user_id = ?1
			WHERE id = ?0`, id, actor); err != nil {
			return fmt.Errorf("closing an LLM run: %w", err)
		}
		return nil
	})
}
