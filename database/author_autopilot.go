package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The author autopilot's storage (migration 62): its one row — the switch,
// the ticking pod's lease, what the last tick found and did, a failed
// tick's backoff — and the selection marks, which say an entity pass is to
// do. Every time is the caller's clock, so a test moves it; the pods' clocks
// are the cluster's.

// AuthorAutopilotState is the autopilot's row.
type AuthorAutopilotState struct {
	Enabled        bool       `pg:"enabled,use_zero" json:"enabled"`
	LeaseOwner     *uuid.UUID `pg:"lease_owner,type:uuid" json:"-"`
	LeaseExpiresAt *time.Time `pg:"lease_expires_at" json:"-"`
	// State is what held or moved the last tick (AuthorAutopilotState*),
	// StateRunID the run it waited for or started.
	State      *string `pg:"state" json:"state,omitempty"`
	StateRunID *int64  `pg:"state_run_id" json:"state_run_id,omitempty"`
	// ResolveNote is how the last resolve check came out, ResolveInputs the
	// open inputs it found to ask.
	ResolveNote   *string `pg:"resolve_note" json:"resolve_note,omitempty"`
	ResolveInputs int     `pg:"resolve_inputs,use_zero" json:"resolve_inputs"`
	// LastAction is the last run it started: resolve or build.
	LastAction      *string    `pg:"last_action" json:"last_action,omitempty"`
	LastActionRunID *int64     `pg:"last_action_run_id" json:"last_action_run_id,omitempty"`
	LastActionAt    *time.Time `pg:"last_action_at" json:"last_action_at,omitempty"`
	// Failures counts the ticks that failed in a row; the next one waits
	// until NextAttemptAt.
	Failures       int        `pg:"failures,use_zero" json:"failures"`
	LastErrorClass *string    `pg:"last_error_class" json:"last_error_class,omitempty"`
	NextAttemptAt  *time.Time `pg:"next_attempt_at" json:"next_attempt_at,omitempty"`
	UpdatedAt      time.Time  `pg:"updated_at" json:"updated_at"`
}

const authorAutopilotColumns = `enabled, lease_owner, lease_expires_at, state, state_run_id, resolve_note,
	resolve_inputs, last_action, last_action_run_id, last_action_at, failures, last_error_class, next_attempt_at,
	updated_at`

// What the last tick found.
const (
	// AuthorAutopilotStateNotQuiet: a snapshot was written within the quiet
	// period.
	AuthorAutopilotStateNotQuiet = "not_quiet"
	// AuthorAutopilotStateNormalizing: local normalization jobs wait.
	AuthorAutopilotStateNormalizing = "normalizing"
	// AuthorAutopilotStateLLMRunActive and EntityBuildActive: a run is
	// active (StateRunID), whoever started it; the autopilot does nothing.
	AuthorAutopilotStateLLMRunActive      = "llm_run_active"
	AuthorAutopilotStateEntityBuildActive = "entity_build_active"
	// AuthorAutopilotStateResolveStarted and BuildStarted: the tick started
	// that run (StateRunID).
	AuthorAutopilotStateResolveStarted = "resolve_started"
	AuthorAutopilotStateBuildStarted   = "build_started"
	// AuthorAutopilotStateIdle: nothing to do.
	AuthorAutopilotStateIdle = "idle"
)

// AuthorAutopilotStates are every state a tick records.
func AuthorAutopilotStates() []string {
	return []string{AuthorAutopilotStateNotQuiet, AuthorAutopilotStateNormalizing, AuthorAutopilotStateLLMRunActive,
		AuthorAutopilotStateEntityBuildActive, AuthorAutopilotStateResolveStarted, AuthorAutopilotStateBuildStarted,
		AuthorAutopilotStateIdle}
}

// The runs the autopilot starts.
const (
	AuthorAutopilotActionResolve = "resolve"
	AuthorAutopilotActionBuild   = "build"
)

// ErrAuthorAutopilotLeaseLost is a write of a pod whose lease ran out or
// another pod took: nothing is written.
var ErrAuthorAutopilotLeaseLost = errors.New("database: the author autopilot lease was lost")

// ErrAuthorAutopilotOff is a write of a pod after the autopilot was switched
// off: nothing is written, and a run started in the same transaction is
// undone with it.
var ErrAuthorAutopilotOff = errors.New("database: the author autopilot is switched off")

// GetAuthorAutopilot reads the autopilot's row.
func GetAuthorAutopilot(ctx context.Context, db pg.DBI) (*AuthorAutopilotState, error) {
	var st AuthorAutopilotState
	if _, err := db.QueryOneContext(ctx, &st, `SELECT `+authorAutopilotColumns+` FROM public.author_autopilot`); err != nil {
		return nil, fmt.Errorf("reading the author autopilot: %w", err)
	}
	return &st, nil
}

// SetAuthorAutopilotEnabled switches the autopilot on or off.
func SetAuthorAutopilotEnabled(ctx context.Context, db pg.DBI, enabled bool, actor int64) error {
	var by interface{}
	if actor > 0 {
		by = actor
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE public.author_autopilot SET enabled = ?, updated_by_user_id = ?, updated_at = now()`,
		enabled, by); err != nil {
		return fmt.Errorf("switching the author autopilot: %w", err)
	}
	return nil
}

// ClaimAuthorAutopilot takes the autopilot's lease for owner until now +
// lease: a free one, one that ran out, or one owner holds already. Another
// pod's live lease is nil.
func ClaimAuthorAutopilot(
	ctx context.Context, db pg.DBI, owner uuid.UUID, lease time.Duration, now time.Time,
) (*AuthorAutopilotState, error) {
	var rows []AuthorAutopilotState
	if _, err := db.QueryContext(ctx, &rows, `
		UPDATE public.author_autopilot SET lease_owner = ?0, lease_expires_at = ?1::timestamptz + ?2 * interval '1 millisecond'
		WHERE lease_owner IS NULL OR lease_expires_at <= ?1 OR lease_owner = ?0
		RETURNING `+authorAutopilotColumns, owner, now, lease.Milliseconds()); err != nil {
		return nil, fmt.Errorf("claiming the author autopilot: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// LockAuthorAutopilot locks the autopilot's row in the caller's transaction
// for owner and renews its lease: a pod whose lease ran out by now, or
// another pod took, gets ErrAuthorAutopilotLeaseLost, and any pod once the
// autopilot is switched off ErrAuthorAutopilotOff. While the row is locked no
// other pod claims it and the switch waits, so every write a pod makes under
// it is the lease holder's, made while the autopilot is on: a switch that
// committed first refuses it, one that comes later waits for it.
func LockAuthorAutopilot(ctx context.Context, tx pg.DBI, owner uuid.UUID, lease time.Duration, now time.Time) error {
	var enabled []bool
	if _, err := tx.QueryContext(ctx, &enabled, `
		UPDATE public.author_autopilot SET lease_expires_at = ?2::timestamptz + ?1 * interval '1 millisecond'
		WHERE lease_owner = ?0 AND lease_expires_at > ?2
		RETURNING enabled`, owner, lease.Milliseconds(), now); err != nil {
		return fmt.Errorf("locking the author autopilot: %w", err)
	}
	if len(enabled) == 0 {
		return ErrAuthorAutopilotLeaseLost
	}
	if !enabled[0] {
		return ErrAuthorAutopilotOff
	}
	return nil
}

// AuthorAutopilotTick is what one tick found, as the row records it.
type AuthorAutopilotTick struct {
	State         string
	StateRunID    int64
	ResolveNote   string
	ResolveInputs int
}

// RecordAuthorAutopilotTick records what a tick found, in the caller's
// transaction (under LockAuthorAutopilot), and clears a failure streak; a
// tick that found what the last one did writes nothing. It reports whether
// the row changed.
func RecordAuthorAutopilotTick(ctx context.Context, tx pg.DBI, t *AuthorAutopilotTick, now time.Time) (bool, error) {
	var runID interface{}
	if t.StateRunID != 0 {
		runID = t.StateRunID
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE public.author_autopilot SET state = ?0, state_run_id = ?1, resolve_note = nullif(?2, ''),
			resolve_inputs = ?3, failures = 0, last_error_class = NULL, next_attempt_at = NULL, updated_at = ?4
		WHERE (state, state_run_id, resolve_note, resolve_inputs, failures)
			IS DISTINCT FROM (?0::text, ?1::bigint, nullif(?2, ''), ?3::int, 0)`,
		t.State, runID, t.ResolveNote, t.ResolveInputs, now)
	if err != nil {
		return false, fmt.Errorf("recording the author autopilot's tick: %w", err)
	}
	return res.RowsAffected() == 1, nil
}

// RecordAuthorAutopilotAction records the run a tick started, in the
// transaction that started it (under LockAuthorAutopilot).
func RecordAuthorAutopilotAction(ctx context.Context, tx pg.DBI, action string, runID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE public.author_autopilot SET last_action = ?0, last_action_run_id = ?1, last_action_at = ?2`,
		action, runID, now); err != nil {
		return fmt.Errorf("recording the author autopilot's action: %w", err)
	}
	return nil
}

// RecordAuthorAutopilotFailure records a failed tick, in the caller's
// transaction (under LockAuthorAutopilot): its class, and when the next one
// may run. It returns the failures in a row.
func RecordAuthorAutopilotFailure(ctx context.Context, tx pg.DBI, class string, next time.Time) (int, error) {
	var failures int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&failures), `
		UPDATE public.author_autopilot SET failures = failures + 1, last_error_class = ?, next_attempt_at = ?
		RETURNING failures`, class, next); err != nil {
		return 0, fmt.Errorf("recording an author autopilot failure: %w", err)
	}
	return failures, nil
}

// AuthorAutopilotBuildDebt is what the newest completed passes owe: Units
// the newest one skipped (rows another writer held), Passes how many
// completed passes in a row skipped units, the newest first, and when the
// newest finished. A pass that skipped nothing owes nothing: every pass plans
// the whole catalog again.
type AuthorAutopilotBuildDebt struct {
	Units    int        `pg:"units,use_zero"`
	Passes   int        `pg:"passes,use_zero"`
	Finished *time.Time `pg:"finished"`
}

// authorAutopilotDebtWindow bounds how many passes the streak counts.
const authorAutopilotDebtWindow = 16

// ReadAuthorAutopilotBuildDebt reads what the newest completed passes owe,
// whoever started them.
func ReadAuthorAutopilotBuildDebt(ctx context.Context, db pg.DBI) (*AuthorAutopilotBuildDebt, error) {
	var debt AuthorAutopilotBuildDebt
	if _, err := db.QueryOneContext(ctx, &debt, `
		WITH recent AS (
			SELECT units_skipped, finished_at, row_number() OVER (ORDER BY id DESC) AS n
			FROM (SELECT id, units_skipped, finished_at FROM public.author_entity_build_run
				WHERE status = 'completed' ORDER BY id DESC LIMIT ?) r)
		SELECT coalesce((SELECT units_skipped FROM recent WHERE n = 1), 0) AS units,
			coalesce((SELECT min(n) - 1 FROM recent WHERE units_skipped = 0),
				(SELECT count(*) FROM recent)) AS passes,
			(SELECT finished_at FROM recent WHERE n = 1) AS finished`, authorAutopilotDebtWindow); err != nil {
		return nil, fmt.Errorf("reading what the entity passes owe: %w", err)
	}
	return &debt, nil
}

// AuthorAutopilotQuiet is the quiet gate's view: when the newest snapshot
// was written, and the local normalization jobs still waiting.
type AuthorAutopilotQuiet struct {
	NewestSnapshotAt *time.Time `pg:"newest_at"`
	// WaitingJobs counts the waiting jobs of the normalizer version, up to
	// AuthorAutopilotWaitingCap.
	WaitingJobs int `pg:"waiting,use_zero"`
}

// AuthorAutopilotWaitingCap bounds the count of waiting jobs: the card
// needs only "none" or "many".
const AuthorAutopilotWaitingCap = 10000

// ReadAuthorAutopilotQuiet reads the quiet gate's view: the newest
// snapshot is the one of the highest id, read through the primary key (a
// max over created_at would scan the whole table every tick); the gate only
// debounces, so the order of commits does not matter to it. The waiting
// jobs are those of normalizerVersion, queued or claimed: a job of an older
// version waits for nothing (the re-normalization queues its input again
// under this one).
func ReadAuthorAutopilotQuiet(ctx context.Context, db pg.DBI, normalizerVersion string) (*AuthorAutopilotQuiet, error) {
	var q AuthorAutopilotQuiet
	if _, err := db.QueryOneContext(ctx, &q, `
		SELECT (SELECT created_at FROM public.book_metadata_snapshot ORDER BY id DESC LIMIT 1) AS newest_at,
			(SELECT count(*) FROM (
				SELECT 1 FROM public.contributor_normalization_job
				WHERE status IN ('pending', 'queued') AND normalizer_version = ?0
				LIMIT ?1) w) AS waiting`,
		normalizerVersion, AuthorAutopilotWaitingCap); err != nil {
		return nil, fmt.Errorf("reading whether the scan is quiet: %w", err)
	}
	return &q, nil
}

// SettleAuthorAutopilotMarks drops the marks of the passes that completed
// and gives back those of the passes that failed: what a failed pass took is
// still to do.
func SettleAuthorAutopilotMarks(ctx context.Context, db pg.DBI) error {
	if _, err := db.ExecContext(ctx, `
		DELETE FROM public.author_autopilot_mark m USING public.author_entity_build_run b
		WHERE m.build_run_id = b.id AND b.status = 'completed'`); err != nil {
		return fmt.Errorf("dropping the done autopilot marks: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE public.author_autopilot_mark m SET build_run_id = NULL
		WHERE m.build_run_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM public.author_entity_build_run b
			WHERE b.id = m.build_run_id AND b.status IN ('pending', 'running', 'completed'))`); err != nil {
		return fmt.Errorf("giving back the autopilot marks of failed passes: %w", err)
	}
	return nil
}

// AuthorAutopilotBuildNeeded reports whether the selections changed since
// the last entity pass started: a mark no pass took.
func AuthorAutopilotBuildNeeded(ctx context.Context, db pg.DBI) (bool, error) {
	var needed bool
	if _, err := db.QueryOneContext(ctx, pg.Scan(&needed), `
		SELECT EXISTS (SELECT 1 FROM public.author_autopilot_mark WHERE build_run_id IS NULL)`); err != nil {
		return false, fmt.Errorf("reading whether an entity pass is to do: %w", err)
	}
	return needed, nil
}

// ActiveAuthorEntityBuild is the pending or running build pass; 0 when none
// is.
func ActiveAuthorEntityBuild(ctx context.Context, db pg.DBI) (int64, error) {
	var ids []int64
	if _, err := db.QueryContext(ctx, &ids, `
		SELECT id FROM public.author_entity_build_run WHERE status IN ('pending', 'running') ORDER BY id LIMIT 1`); err != nil {
		return 0, fmt.Errorf("reading the active author entity build: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	return ids[0], nil
}
