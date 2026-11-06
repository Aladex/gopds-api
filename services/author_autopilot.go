package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The author autopilot (migration 62) is a level-triggered reconciler: every
// tick it looks at the state of the author layers and does at most one
// thing, so new books go through them without a button.
//
//  1. While the scan is not quiet — a snapshot written within the quiet
//     period, or local normalization jobs still waiting — it does nothing.
//  2. While any LLM run or entity pass is active, whoever started it, it
//     does nothing: it never overlaps a run, and an administrator may start
//     anything at any time.
//  3. When the registered strata have open review inputs never asked, it
//     starts one full resolve run (AuthorLLMAdmin.StartAutopilotResolve,
//     under the card's limits; never an eval, never a new registration).
//  4. Otherwise, when the selections changed since the last entity pass
//     started (an untaken mark, migration 62), or the newest completed pass
//     skipped units (a row another writer held) and its retry is due, it
//     starts an entity pass. The retry waits a backoff that doubles with
//     every completed pass in a row that skipped units, so a refusal that
//     persists costs a pass now and then, not one a minute. Passes that
//     failed their census in a row (census_failed: their marks were given
//     back) are spaced out the same way; any other failure is built again
//     on the next quiet tick. A pass refused because it would take applied
//     decisions apart on a not-same answer (applied_decisions_held) is
//     not retried: every pass and merge run waits for an administrator's
//     pass.
//  5. Otherwise, when its merge step is switched on (migration 76, off by
//     default), it starts one merge run of the merge configuration over the
//     candidates due (AuthorLLMAdmin.StartAutopilotMerge, under the merge
//     card's limits; never an eval). It does so only with no pass to do —
//     the candidates are the last completed pass's — and the decisions the
//     run inserts mark the pass after it.
//
// Correctness is convergence, not one action per event: whatever a tick
// misses — a commit that lands late or during a pass, a run an administrator
// started — a later tick sees in the state. A run that races the
// autopilot's own start is harmless: the next tick evaluates again, the pass
// is idempotent and a resolve run asks only open inputs. On the first quiet
// tick after the deploy the registered strata's backlog of inputs never
// asked is resolved: that is the backlog the autopilot is for, capped by the
// budget guard.
//
// Every pod runs the worker; a lease on the autopilot row lets one tick at a
// time, and every write is made under that row's lock, which re-reads the
// switch: a switch-off that commits while a tick is planning refuses its
// write, and a run that tick started is undone with it. A failed tick is
// retried after a backoff that doubles; the card shows it.

// AuthorAutopilotConfig bounds the worker: the quiet period after the newest
// snapshot, its lease, the rest between ticks, the backoff of a failed tick
// and of a pass's skipped units, and its clock.
type AuthorAutopilotConfig struct {
	Quiet          time.Duration
	Lease          time.Duration
	Poll           time.Duration
	Backoff        time.Duration
	MaxBackoff     time.Duration
	DebtBackoff    time.Duration
	MaxDebtBackoff time.Duration
	// CensusBackoff and MaxCensusBackoff space the passes after passes that
	// failed their census in a row.
	CensusBackoff    time.Duration
	MaxCensusBackoff time.Duration
	Now              func() time.Time
}

const (
	// DefaultAuthorAutopilotQuiet is the quiet period of the configuration's
	// default.
	DefaultAuthorAutopilotQuiet      = 30 * time.Minute
	defaultAuthorAutopilotLease      = 2 * time.Minute
	defaultAuthorAutopilotPoll       = time.Minute
	defaultAuthorAutopilotBackoff    = time.Minute
	defaultAuthorAutopilotMaxBackoff = 30 * time.Minute
	// A held row is usually free again within minutes; one held for hours
	// is retried a few times a day.
	defaultAuthorAutopilotDebtBackoff    = 5 * time.Minute
	defaultAuthorAutopilotMaxDebtBackoff = 6 * time.Hour
	// A census that failed for a passing read error passes on the next
	// pass; one that keeps failing is a defect, retried a few times a day.
	defaultAuthorAutopilotCensusBackoff    = 5 * time.Minute
	defaultAuthorAutopilotMaxCensusBackoff = 6 * time.Hour
)

func (c AuthorAutopilotConfig) withDefaults() AuthorAutopilotConfig {
	if c.Quiet <= 0 {
		c.Quiet = DefaultAuthorAutopilotQuiet
	}
	if c.Lease <= 0 {
		c.Lease = defaultAuthorAutopilotLease
	}
	if c.Poll <= 0 {
		c.Poll = defaultAuthorAutopilotPoll
	}
	if c.Backoff <= 0 {
		c.Backoff = defaultAuthorAutopilotBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = defaultAuthorAutopilotMaxBackoff
	}
	if c.DebtBackoff <= 0 {
		c.DebtBackoff = defaultAuthorAutopilotDebtBackoff
	}
	if c.MaxDebtBackoff <= 0 {
		c.MaxDebtBackoff = defaultAuthorAutopilotMaxDebtBackoff
	}
	if c.CensusBackoff <= 0 {
		c.CensusBackoff = defaultAuthorAutopilotCensusBackoff
	}
	if c.MaxCensusBackoff <= 0 {
		c.MaxCensusBackoff = defaultAuthorAutopilotMaxCensusBackoff
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// AuthorAutopilotLLM gives the autopilot the LLM block's admin of the
// configuration the workers run, or the error that kept it from being built
// (the tick fails and is retried).
type AuthorAutopilotLLM func(ctx context.Context) (*AuthorLLMAdmin, error)

// AuthorAutopilot is the autopilot's worker.
type AuthorAutopilot struct {
	db  *pg.DB
	llm AuthorAutopilotLLM
	// merge gives the merge configuration's admin; nil: no merge step.
	merge AuthorAutopilotLLM
	cfg   AuthorAutopilotConfig
	owner uuid.UUID
	// heldLoggedAt is when the hold was last announced.
	heldLoggedAt time.Time
}

// NewAuthorAutopilot builds the worker; zero fields take the defaults.
func NewAuthorAutopilot(db *pg.DB, llm AuthorAutopilotLLM, cfg AuthorAutopilotConfig) *AuthorAutopilot {
	return &AuthorAutopilot{db: db, llm: llm, cfg: cfg.withDefaults(), owner: uuid.New()}
}

// WithMerge gives the worker the merge configuration's admin, for its
// merge step.
func (l *AuthorAutopilot) WithMerge(merge AuthorAutopilotLLM) *AuthorAutopilot {
	l.merge = merge
	return l
}

// authorAutopilotName is the worker's log label.
const authorAutopilotName = string(AuthorMetadataStageAutopilot)

// Name is the worker's log label.
func (l *AuthorAutopilot) Name() string { return authorAutopilotName }

// Run ticks until ctx is canceled: a poll interval of rest after each tick.
func (l *AuthorAutopilot) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if _, err := l.RunOnce(ctx); err != nil && ctx.Err() == nil {
			LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
				Name: AuthorMetadataEventAutopilotStepFailed, Stage: AuthorMetadataStageAutopilot,
				SQLState: AuthorMetadataSQLState(err), Worker: authorAutopilotName,
			})
		}
		select {
		case <-ctx.Done():
		case <-time.After(l.cfg.Poll):
		}
	}
	return nil
}

// authorAutopilotHook, when set, runs before a tick starts a run, with what
// it starts ("resolve", "build"); an error it returns fails the tick there
// (a test seam).
var authorAutopilotHook func(action string) error

// RunOnce takes the lease and ticks once. It reports whether the tick
// started a run. A failed tick is recorded with its backoff, and its error
// returned.
func (l *AuthorAutopilot) RunOnce(ctx context.Context) (bool, error) {
	now := l.cfg.Now()
	st, err := database.ClaimAuthorAutopilot(ctx, l.db, l.owner, l.cfg.Lease, now)
	if err != nil || st == nil || !st.Enabled {
		return false, err
	}
	if st.NextAttemptAt != nil && st.NextAttemptAt.After(now) {
		return false, nil
	}
	started, err := l.tick(ctx, st, now)
	if fenced(err) {
		// Another pod holds the lease now, or the autopilot was switched off:
		// nothing was written.
		return false, nil
	}
	if err != nil {
		return false, errors.Join(err, l.fail(ctx, st, err, now))
	}
	return started, nil
}

// fenced reports an error that means another pod holds the lease, the
// autopilot was switched off, or its merge step (for a merge start): the
// write was refused, and nothing failed.
func fenced(err error) bool {
	return errors.Is(err, database.ErrAuthorAutopilotLeaseLost) || errors.Is(err, database.ErrAuthorAutopilotOff) ||
		errors.Is(err, database.ErrAuthorAutopilotMergeOff)
}

// tick looks at the state and does at most one thing.
func (l *AuthorAutopilot) tick(ctx context.Context, st *database.AuthorAutopilotState, now time.Time) (bool, error) {
	if err := database.SettleAuthorAutopilotMarks(ctx, l.db); err != nil {
		return false, err
	}
	q, err := database.ReadAuthorAutopilotQuiet(ctx, l.db, authornorm.NormalizerVersion)
	if err != nil {
		return false, err
	}
	switch {
	case q.WaitingJobs > 0:
		return false, l.record(ctx, &database.AuthorAutopilotTick{State: database.AuthorAutopilotStateNormalizing}, now)
	case q.NewestSnapshotAt != nil && q.NewestSnapshotAt.Add(l.cfg.Quiet).After(now):
		return false, l.record(ctx, &database.AuthorAutopilotTick{State: database.AuthorAutopilotStateNotQuiet}, now)
	}
	if active, held, activeErr := l.activeRun(ctx); activeErr != nil || held {
		if activeErr != nil {
			return false, activeErr
		}
		return false, l.record(ctx, active, now)
	}
	resolved, err := l.resolve(ctx, now)
	if err != nil || resolved.RunID != 0 {
		return resolved.RunID != 0, err
	}
	found := &database.AuthorAutopilotTick{State: database.AuthorAutopilotStateIdle, ResolveNote: resolved.Note,
		ResolveInputs: resolved.Inputs}
	if resolved.Note == AutopilotResolveRunActive {
		found.State = database.AuthorAutopilotStateLLMRunActive
		return false, l.record(ctx, found, now)
	}
	hold, err := database.ReadAuthorAutopilotDecisionsHeld(ctx, l.db, AuthorEntityBuildErrorDecisionsHeld)
	if err != nil {
		return false, err
	}
	if hold.RunID != 0 {
		found.State, found.StateRunID, found.MergeNote = database.AuthorAutopilotStateBuildHeld, hold.RunID,
			AutopilotMergeBuildHeld
		l.announceHold(hold, now)
		return false, l.record(ctx, found, now)
	}
	needed, held, err := l.buildNeeded(ctx, now)
	switch {
	case err != nil:
		return false, err
	case held != 0:
		found.State, found.StateRunID, found.MergeNote = database.AuthorAutopilotStateBuildBackoff, held,
			AutopilotMergeBuildDue
		return false, l.record(ctx, found, now)
	case needed:
		return l.build(ctx, found, now)
	}
	return l.mergeStep(ctx, st, found, now)
}

// announceHold logs the hold now and then for as long as it lasts: state
// changes are logged once, and a line that scrolled past the alert's window
// must not leave new books waiting in silence.
func (l *AuthorAutopilot) announceHold(hold *database.AuthorAutopilotDecisionsHeld, now time.Time) {
	if now.Sub(l.heldLoggedAt) < authorAutopilotHoldLogEvery {
		return
	}
	l.heldLoggedAt = now
	LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
		Name: AuthorMetadataEventAutopilotHeld, Stage: AuthorMetadataStageAutopilot, ItemID: hold.RunID,
		Count: len(hold.Decisions), Worker: authorAutopilotName,
	})
}

// buildNeeded reports whether a pass is to do now: the selections changed
// since the last one started, or the newest completed one skipped units and
// their retry is due. A pass to do after passes that failed their census in
// a row waits its backoff: held is then the newest of them.
func (l *AuthorAutopilot) buildNeeded(ctx context.Context, now time.Time) (needed bool, held int64, err error) {
	changed, err := database.AuthorAutopilotBuildNeeded(ctx, l.db)
	if err != nil {
		return false, 0, err
	}
	if changed {
		census, censusErr := database.ReadAuthorAutopilotCensusFailures(ctx, l.db, AuthorEntityBuildErrorCensus)
		if censusErr != nil {
			return false, 0, censusErr
		}
		if due := l.cfg.censusDue(census); due != nil && due.After(now) {
			return false, census.RunID, nil
		}
		return true, 0, nil
	}
	debt, err := database.ReadAuthorAutopilotBuildDebt(ctx, l.db)
	if err != nil {
		return false, 0, err
	}
	due := l.cfg.debtDue(debt)
	return due != nil && !due.After(now), 0, nil
}

// debtDue is when the retry of the units the newest completed pass skipped
// is due: after it finished, a backoff that doubles with every completed
// pass in a row that skipped units; nil when nothing is owed.
func (c *AuthorAutopilotConfig) debtDue(debt *database.AuthorAutopilotBuildDebt) *time.Time {
	if debt.Units == 0 {
		return nil
	}
	return doublingDue(debt.Finished, debt.Passes, c.DebtBackoff, c.MaxDebtBackoff)
}

// censusDue is when the next pass is due after passes that failed their
// census in a row: after the newest finished, a backoff that doubles with
// every one of them; nil when the newest pass did not fail its census.
func (c *AuthorAutopilotConfig) censusDue(f *database.AuthorAutopilotCensusFailures) *time.Time {
	if f.Passes == 0 {
		return nil
	}
	return doublingDue(f.Finished, f.Passes, c.CensusBackoff, c.MaxCensusBackoff)
}

// doublingDue is finished plus a backoff of base doubled with each of
// passes after the first, at most ceiling; nil without a finish.
func doublingDue(finished *time.Time, passes int, base, ceiling time.Duration) *time.Time {
	if finished == nil {
		return nil
	}
	d := base
	for i := 1; i < passes && d < ceiling; i++ {
		d *= 2
	}
	due := finished.Add(min(d, ceiling))
	return &due
}

// activeRun is the tick a run that is active holds, whoever started it.
func (l *AuthorAutopilot) activeRun(ctx context.Context) (*database.AuthorAutopilotTick, bool, error) {
	llmRun, err := ActiveAuthorLLMRun(ctx, l.db)
	if err != nil {
		return nil, false, err
	}
	if llmRun != 0 {
		return &database.AuthorAutopilotTick{State: database.AuthorAutopilotStateLLMRunActive, StateRunID: llmRun}, true, nil
	}
	build, err := database.ActiveAuthorEntityBuild(ctx, l.db)
	if err != nil {
		return nil, false, err
	}
	if build != 0 {
		return &database.AuthorAutopilotTick{State: database.AuthorAutopilotStateEntityBuildActive, StateRunID: build},
			true, nil
	}
	return nil, false, nil
}

// record writes what a tick found, under the lease, and logs a change.
func (l *AuthorAutopilot) record(ctx context.Context, t *database.AuthorAutopilotTick, now time.Time) error {
	changed := false
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := database.LockAuthorAutopilot(ctx, tx, l.owner, l.cfg.Lease, now); err != nil {
			return err
		}
		var err error
		changed, err = database.RecordAuthorAutopilotTick(ctx, tx, t, now)
		return err
	})
	if err == nil && changed {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventAutopilotState, Stage: AuthorMetadataStageAutopilot, Status: t.State,
			ItemID: t.StateRunID, Worker: authorAutopilotName,
		})
	}
	return err
}

// authorAutopilotHoldLogEvery spaces the hold's log line; the alert's window
// is twice this.
const authorAutopilotHoldLogEvery = time.Hour

// The tick seam's names of what it starts.
const (
	authorAutopilotHookResolve = "resolve"
	authorAutopilotHookBuild   = "build"
	authorAutopilotHookMerge   = "merge"
)

// resolve starts the resolve run when one is due, recording it in the
// start's transaction, or says why it started none.
func (l *AuthorAutopilot) resolve(ctx context.Context, now time.Time) (*AutopilotResolve, error) {
	if authorAutopilotHook != nil {
		if err := authorAutopilotHook(authorAutopilotHookResolve); err != nil {
			return &AutopilotResolve{}, err
		}
	}
	admin, err := l.llm(ctx)
	if err != nil {
		return &AutopilotResolve{}, err
	}
	res, err := admin.StartAutopilotResolve(ctx, func(tx *pg.Tx, runID int64, inputs int) error {
		if lockErr := database.LockAuthorAutopilot(ctx, tx, l.owner, l.cfg.Lease, now); lockErr != nil {
			return lockErr
		}
		if actionErr := database.RecordAuthorAutopilotAction(ctx, tx, database.AuthorAutopilotActionResolve, runID,
			now); actionErr != nil {
			return actionErr
		}
		_, recordErr := database.RecordAuthorAutopilotTick(ctx, tx, &database.AuthorAutopilotTick{
			State: database.AuthorAutopilotStateResolveStarted, StateRunID: runID, ResolveNote: AutopilotResolveStarted,
			ResolveInputs: inputs}, now)
		return recordErr
	})
	if err != nil {
		return &AutopilotResolve{}, err
	}
	if res.RunID != 0 {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventAutopilotResolveStarted, Stage: AuthorMetadataStageAutopilot, RunID: res.RunID,
			Count: res.Inputs, Worker: authorAutopilotName,
		})
	}
	return res, nil
}

// mergeStep starts the merge run when the merge step is switched on and a
// run is due, recording it in the start's transaction (under the lock,
// which re-reads both switches), or records why it started none.
func (l *AuthorAutopilot) mergeStep(
	ctx context.Context, st *database.AuthorAutopilotState, found *database.AuthorAutopilotTick, now time.Time,
) (bool, error) {
	if l.merge == nil || !st.MergeEnabled {
		found.MergeNote = AutopilotMergeSwitchedOff
		return false, l.record(ctx, found, now)
	}
	if authorAutopilotHook != nil {
		if err := authorAutopilotHook(authorAutopilotHookMerge); err != nil {
			return false, err
		}
	}
	admin, err := l.merge(ctx)
	if err != nil {
		return false, err
	}
	res, err := admin.StartAutopilotMerge(ctx, func(tx *pg.Tx, runID int64, candidates int) error {
		if lockErr := database.LockAuthorAutopilotMerge(ctx, tx, l.owner, l.cfg.Lease, now); lockErr != nil {
			return lockErr
		}
		if actionErr := database.RecordAuthorAutopilotAction(ctx, tx, database.AuthorAutopilotActionMerge, runID,
			now); actionErr != nil {
			return actionErr
		}
		_, recordErr := database.RecordAuthorAutopilotTick(ctx, tx, &database.AuthorAutopilotTick{
			State: database.AuthorAutopilotStateMergeStarted, StateRunID: runID, ResolveNote: found.ResolveNote,
			ResolveInputs: found.ResolveInputs, MergeNote: AutopilotMergeStarted, MergeCandidates: candidates}, now)
		return recordErr
	})
	if err != nil {
		return false, err
	}
	if res.RunID != 0 {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventAutopilotMergeStarted, Stage: AuthorMetadataStageAutopilot, RunID: res.RunID,
			Count: res.Candidates, Worker: authorAutopilotName,
		})
		return true, nil
	}
	found.MergeNote, found.MergeCandidates = res.Note, res.Candidates
	if res.Note == AutopilotMergeRunActive {
		found.State = database.AuthorAutopilotStateLLMRunActive
	}
	return false, l.record(ctx, found, now)
}

// build starts an entity pass, recording it in the same transaction; the
// pass takes the marks visible then. One an administrator started meanwhile
// is waited for.
func (l *AuthorAutopilot) build(ctx context.Context, found *database.AuthorAutopilotTick, now time.Time) (bool, error) {
	if authorAutopilotHook != nil {
		if err := authorAutopilotHook(authorAutopilotHookBuild); err != nil {
			return false, err
		}
	}
	var run *database.AuthorEntityBuildRun
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := database.LockAuthorAutopilot(ctx, tx, l.owner, l.cfg.Lease, now); err != nil {
			return err
		}
		var err error
		if run, err = database.StartAuthorEntityBuild(ctx, tx, 0); err != nil {
			return err
		}
		if actionErr := database.RecordAuthorAutopilotAction(ctx, tx, database.AuthorAutopilotActionBuild, run.ID,
			now); actionErr != nil {
			return actionErr
		}
		_, err = database.RecordAuthorAutopilotTick(ctx, tx, &database.AuthorAutopilotTick{
			State: database.AuthorAutopilotStateBuildStarted, StateRunID: run.ID, ResolveNote: found.ResolveNote,
			ResolveInputs: found.ResolveInputs}, now)
		return err
	})
	if errors.Is(err, database.ErrAuthorEntityBuildActive) {
		found.State = database.AuthorAutopilotStateEntityBuildActive
		return false, l.record(ctx, found, now)
	}
	if err != nil {
		return false, err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityBuildStarted, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
		Worker: authorAutopilotName,
	})
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventAutopilotBuildStarted, Stage: AuthorMetadataStageAutopilot, RunID: run.ID,
		Worker: authorAutopilotName,
	})
	return true, nil
}

// fail records a failed tick: its class, and the time of the next one after
// a backoff that doubles with every failure in a row.
func (l *AuthorAutopilot) fail(ctx context.Context, st *database.AuthorAutopilotState, cause error, now time.Time) error {
	next := now.Add(l.backoff(st.Failures + 1))
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := database.LockAuthorAutopilot(ctx, tx, l.owner, l.cfg.Lease, now); err != nil {
			return err
		}
		_, err := database.RecordAuthorAutopilotFailure(ctx, tx, autopilotFailureClass(cause), next)
		return err
	})
	if fenced(err) {
		return nil
	}
	return err
}

// backoff is the wait after the given number of failures in a row: the
// base, doubled with each further failure, at most the ceiling.
func (l *AuthorAutopilot) backoff(failures int) time.Duration {
	d := l.cfg.Backoff
	for i := 1; i < failures && d < l.cfg.MaxBackoff; i++ {
		d *= 2
	}
	return min(d, l.cfg.MaxBackoff)
}

// autopilotFailureClass is the closed class a failure is recorded with: its
// SQLSTATE, or internal.
func autopilotFailureClass(err error) string {
	code := sqlState(err)
	if code == "" || closedSQLState(code) == authorMetadataUnknownValue {
		return "internal"
	}
	return "sqlstate_" + strings.ToLower(closedSQLState(code))
}

// AuthorAutopilotStatus is the autopilot's block of the card: the row (the
// switch, what the last tick found and did, a failure), the quiet period,
// where the scan stands and whether a pass is to do.
type AuthorAutopilotStatus struct {
	*database.AuthorAutopilotState
	QuietSeconds int64 `json:"quiet_seconds"`
	// QuietUntil is when the newest snapshot is quiet long enough, while it
	// is not yet; WaitingJobs the local normalization jobs still waiting
	// (capped).
	QuietUntil  *time.Time `json:"quiet_until,omitempty"`
	WaitingJobs int        `json:"waiting_jobs"`
	// BuildPending: the selections changed since the last pass started.
	BuildPending bool `json:"build_pending"`
	// SkippedUnits are the units the newest completed pass skipped, whose
	// retry is due at SkippedRetryAt.
	SkippedUnits   int        `json:"skipped_units"`
	SkippedRetryAt *time.Time `json:"skipped_retry_at,omitempty"`
	// CensusFailures are the newest passes that failed their census in a
	// row; the next pass is due at CensusRetryAt.
	CensusFailures int        `json:"census_failures"`
	CensusRetryAt  *time.Time `json:"census_retry_at,omitempty"`
	// HeldDecisions are the applied decisions the newest pass would have
	// taken apart, when it was held for an administrator.
	HeldDecisions []int64 `json:"held_decisions,omitempty"`
}

// ReadAuthorAutopilotStatus reads the card's block, with the configuration's
// default backoff of skipped units.
func ReadAuthorAutopilotStatus(ctx context.Context, db pg.DBI, quiet time.Duration) (*AuthorAutopilotStatus, error) {
	st, err := database.GetAuthorAutopilot(ctx, db)
	if err != nil {
		return nil, err
	}
	q, err := database.ReadAuthorAutopilotQuiet(ctx, db, authornorm.NormalizerVersion)
	if err != nil {
		return nil, err
	}
	pending, err := database.AuthorAutopilotBuildNeeded(ctx, db)
	if err != nil {
		return nil, err
	}
	debt, err := database.ReadAuthorAutopilotBuildDebt(ctx, db)
	if err != nil {
		return nil, err
	}
	census, err := database.ReadAuthorAutopilotCensusFailures(ctx, db, AuthorEntityBuildErrorCensus)
	if err != nil {
		return nil, err
	}
	hold, err := database.ReadAuthorAutopilotDecisionsHeld(ctx, db, AuthorEntityBuildErrorDecisionsHeld)
	if err != nil {
		return nil, err
	}
	defaults := AuthorAutopilotConfig{}.withDefaults()
	out := &AuthorAutopilotStatus{AuthorAutopilotState: st, QuietSeconds: int64(quiet / time.Second),
		WaitingJobs: q.WaitingJobs, BuildPending: pending, SkippedUnits: debt.Units,
		SkippedRetryAt: defaults.debtDue(debt), CensusFailures: census.Passes, CensusRetryAt: defaults.censusDue(census),
		HeldDecisions: hold.Decisions}
	if q.NewestSnapshotAt != nil {
		if until := q.NewestSnapshotAt.Add(quiet); until.After(time.Now()) {
			out.QuietUntil = &until
		}
	}
	return out, nil
}

// Switch states of the autopilot's log: the autopilot's, and its merge
// step's.
const (
	autopilotSwitchedOn       = "on"
	autopilotSwitchedOff      = "off"
	autopilotMergeSwitchedOn  = "merge_on"
	autopilotMergeSwitchedOff = "merge_off"
)

// SetAuthorAutopilot switches the autopilot on or off.
func SetAuthorAutopilot(ctx context.Context, db pg.DBI, enabled bool, actor int64) error {
	if err := database.SetAuthorAutopilotEnabled(ctx, db, enabled, actor); err != nil {
		return err
	}
	status := autopilotSwitchedOff
	if enabled {
		status = autopilotSwitchedOn
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventAutopilotSwitched, Stage: AuthorMetadataStageAutopilot, Status: status,
	})
	return nil
}

// SetAuthorAutopilotMerge switches the autopilot's merge step on or off. A
// merge run it started already goes on: switching off stops the next.
func SetAuthorAutopilotMerge(ctx context.Context, db pg.DBI, enabled bool, actor int64) error {
	if err := database.SetAuthorAutopilotMergeEnabled(ctx, db, enabled, actor); err != nil {
		return err
	}
	status := autopilotMergeSwitchedOff
	if enabled {
		status = autopilotMergeSwitchedOn
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventAutopilotSwitched, Stage: AuthorMetadataStageAutopilot, Status: status,
	})
	return nil
}

// AuthorAutopilotStatuses are the closed values the autopilot logs as a
// status: what a tick found, and the switches.
func AuthorAutopilotStatuses() []string {
	return append(database.AuthorAutopilotStates(), autopilotSwitchedOn, autopilotSwitchedOff,
		autopilotMergeSwitchedOn, autopilotMergeSwitchedOff)
}
