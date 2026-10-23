package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The author entity build (design E, phase E1): a background pass an
// administrator starts from the «Сканирование» card. It reads the selected
// author credits and what the previous pass stored, plans the entities
// deterministically (internal/authorentity) and writes the whole plan in
// ONE transaction — batches of units under savepoints, a refused batch
// again component by component (the units of a split or a merge depend on
// each other and land or are skipped together), so what the database
// refuses is skipped alone and planned again by the next pass —
// with the report and the run's completion. A pass either lands whole or
// not at all: a crash, a lost lease or a failed write rolls everything
// back, and the next attempt plans again from what is stored, as if the
// pass had never started. Readers keep their snapshot meanwhile; a scan
// that writes credits during the pass is caught up by the read model's
// marks and walk and by the next pass, and one that deletes a book's layer
// waits for the pass to commit (the author layer lock, held shared). A
// second pass over the same data writes nothing. Nothing a reader sees
// reads the entities yet (shadow).

// AuthorEntityBuildConfig bounds the pass: units per savepoint batch (of
// whole components), the worker's lease on the pass, its rest when there is
// nothing to do and how long a write waits for a row another writer holds
// before its component is skipped.
type AuthorEntityBuildConfig struct {
	Batch       int
	Lease       time.Duration
	Poll        time.Duration
	LockTimeout time.Duration
}

const (
	defaultAuthorEntityBatch = 500
	defaultAuthorEntityLease = 5 * time.Minute
	defaultAuthorEntityPoll  = 5 * time.Second
	// A row is waited for a little, never for long.
	defaultAuthorEntityLockTimeout = 5 * time.Second
)

func (c AuthorEntityBuildConfig) withDefaults() AuthorEntityBuildConfig {
	if c.Batch <= 0 {
		c.Batch = defaultAuthorEntityBatch
	}
	if c.Lease <= 0 {
		c.Lease = defaultAuthorEntityLease
	}
	if c.Poll <= 0 {
		c.Poll = defaultAuthorEntityPoll
	}
	if c.LockTimeout <= 0 {
		c.LockTimeout = defaultAuthorEntityLockTimeout
	}
	return c
}

// AuthorEntityBuildLoop runs the pass an administrator started.
type AuthorEntityBuildLoop struct {
	db    *pg.DB
	cfg   AuthorEntityBuildConfig
	owner uuid.UUID
}

// authorEntityPlan is a planned pass and what its apply wrote.
type authorEntityPlan struct {
	runID    int64
	plan     *authorentity.Plan
	skipped  []database.AuthorEntityUnitSkip
	changes  int64
	attempts int
	timings  AuthorEntityBuildTimings
}

// AuthorEntityBuildTimings are the pass's durations in milliseconds.
type AuthorEntityBuildTimings struct {
	LoadMS  int64 `json:"load_ms"`
	PlanMS  int64 `json:"plan_ms"`
	ApplyMS int64 `json:"apply_ms"`
}

// AuthorEntityBuildReport is the stored report: the plan's counts, the
// credits E1 gives no entity because they are not selected, the units the
// database refused and the durations.
type AuthorEntityBuildReport struct {
	authorentity.Report
	UnselectedCredits int `json:"unselected_credits"`
	// UnitsSkipped are the units the database refused; Attempts is how many
	// times the pass was planned (an attempt that did not land wrote
	// nothing).
	UnitsSkipped int                      `json:"units_skipped"`
	Attempts     int                      `json:"attempts"`
	Timings      AuthorEntityBuildTimings `json:"timings"`
}

// NewAuthorEntityBuildLoop builds the worker; zero fields take the defaults.
func NewAuthorEntityBuildLoop(db *pg.DB, cfg AuthorEntityBuildConfig) *AuthorEntityBuildLoop {
	return &AuthorEntityBuildLoop{db: db, cfg: cfg.withDefaults(), owner: uuid.New()}
}

// authorEntityBuildName is the worker's log label.
const authorEntityBuildName = string(AuthorMetadataStageEntityBuild)

// Name is the worker's log label.
func (l *AuthorEntityBuildLoop) Name() string { return authorEntityBuildName }

// Run works until ctx is canceled: step after step while a pass is active,
// a poll interval of rest when there is none or a step failed.
func (l *AuthorEntityBuildLoop) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		worked, err := l.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
				Name: AuthorMetadataEventEntityBuildStepFailed, Stage: AuthorMetadataStageEntityBuild,
				SQLState: AuthorMetadataSQLState(err), Worker: authorEntityBuildName,
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

// Test seams: authorEntityBeforeApplyHook runs between the plan and its
// transaction; authorEntityApplyHook runs inside the transaction after each
// batch ("batch") and before the completion ("complete") — an error it
// returns fails the pass there.
var (
	authorEntityBeforeApplyHook func()
	authorEntityApplyHook       func(step string) error
)

// No statement of the apply transaction runs away.
const authorEntityStatementTimeout = "5min"

// RunOnce does one attempt of the active pass: plans it and writes it whole,
// or writes nothing. It reports whether there was a pass to attempt.
func (l *AuthorEntityBuildLoop) RunOnce(ctx context.Context) (bool, error) {
	run, err := database.ClaimAuthorEntityBuild(ctx, l.db, l.owner, l.cfg.Lease)
	if err != nil || run == nil {
		return false, err
	}
	p, err := l.planRun(ctx, run)
	if err != nil {
		return true, err
	}
	if authorEntityBeforeApplyHook != nil {
		authorEntityBeforeApplyHook()
	}
	if err := l.apply(ctx, run, p); err != nil {
		return true, err
	}
	for _, sk := range p.skipped {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventEntityBuildUnitSkipped, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
			Fingerprint: sk.Fingerprint, SQLState: sk.SQLState, Worker: authorEntityBuildName,
		})
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityBuildCompleted, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
		Count: int(p.changes), Worker: authorEntityBuildName,
	})
	return true, nil
}

// planRun reads and plans the pass, one more attempt of it.
func (l *AuthorEntityBuildLoop) planRun(ctx context.Context, run *database.AuthorEntityBuildRun) (*authorEntityPlan, error) {
	attempt, err := database.BeginAuthorEntityAttempt(ctx, l.db, run.ID, l.owner, l.cfg.Lease)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	in, err := database.LoadAuthorEntityInput(ctx, l.db)
	if err != nil {
		return nil, err
	}
	if in.Old, err = database.LoadAuthorEntityState(ctx, l.db); err != nil {
		return nil, err
	}
	loaded := time.Now()
	plan := authorentity.Build(in)
	planned := time.Now()
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityBuildPlanned, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
		Count: len(plan.Units), Worker: authorEntityBuildName,
	})
	// The card shows the units to write while the transaction runs.
	if err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return database.RecordAuthorEntityProgress(ctx, tx, run.ID, l.owner, l.cfg.Lease,
			database.AuthorEntityProgress{Stage: database.AuthorEntityStageApply, UnitsTotal: len(plan.Units)})
	}); err != nil {
		return nil, err
	}
	return &authorEntityPlan{runID: run.ID, plan: plan, attempts: attempt.Attempts, timings: AuthorEntityBuildTimings{
		LoadMS: loaded.Sub(started).Milliseconds(), PlanMS: planned.Sub(loaded).Milliseconds()}}, nil
}

// apply writes the whole plan, the report and the completion in one
// transaction, which first locks the run as its lease owner: a worker whose
// lease another took writes nothing, and while the transaction runs no one
// else can claim the pass. It holds the author layer shared throughout: a
// scan deleting a book's layer waits for it to commit.
func (l *AuthorEntityBuildLoop) apply(ctx context.Context, run *database.AuthorEntityBuildRun, p *authorEntityPlan) error {
	started := time.Now()
	return l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', ?, true)`,
			authorEntityStatementTimeout); err != nil {
			return err
		}
		// The layer lock may wait for a book deletion in flight; the lock
		// timeout bounds only the row waits after it.
		if err := database.LockAuthorEntityBuild(ctx, tx, run.ID, l.owner); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT set_config('lock_timeout', ?, true)`,
			fmt.Sprintf("%dms", l.cfg.LockTimeout.Milliseconds())); err != nil {
			return err
		}
		for _, batch := range componentBatches(p.plan.Components(), l.cfg.Batch) {
			changes, skipped, err := database.ApplyAuthorEntityUnits(ctx, tx, run.ID, batch)
			if err != nil {
				return err
			}
			p.changes += int64(changes)
			p.skipped = append(p.skipped, skipped...)
			if authorEntityApplyHook != nil {
				if err := authorEntityApplyHook("batch"); err != nil {
					return err
				}
			}
		}
		return l.complete(ctx, tx, run, p, started)
	})
}

// componentBatches cut the components into batches of about size units,
// never splitting a component.
func componentBatches(components [][]authorentity.Unit, size int) [][][]authorentity.Unit {
	var out [][][]authorentity.Unit
	var batch [][]authorentity.Unit
	units := 0
	for _, comp := range components {
		batch = append(batch, comp)
		if units += len(comp); units >= size {
			out = append(out, batch)
			batch, units = nil, 0
		}
	}
	if len(batch) > 0 {
		out = append(out, batch)
	}
	return out
}

// complete stores the report and completes the pass in the apply's
// transaction.
func (l *AuthorEntityBuildLoop) complete(
	ctx context.Context, tx *pg.Tx, run *database.AuthorEntityBuildRun, p *authorEntityPlan, started time.Time,
) error {
	unselected, err := database.CountUnselectedAuthorCredits(ctx, tx)
	if err != nil {
		return err
	}
	p.timings.ApplyMS = time.Since(started).Milliseconds()
	rep := AuthorEntityBuildReport{Report: p.plan.Report, UnselectedCredits: unselected, UnitsSkipped: len(p.skipped),
		Attempts: p.attempts, Timings: p.timings}
	rep.Changes = int(p.changes)
	raw, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	if authorEntityApplyHook != nil {
		if err := authorEntityApplyHook("complete"); err != nil {
			return err
		}
	}
	units := len(p.plan.Units)
	return database.CompleteAuthorEntityBuild(ctx, tx, run.ID, l.owner, database.AuthorEntityProgress{
		Stage: database.AuthorEntityStageReport, UnitsTotal: units, UnitsDone: units,
		UnitsSkipped: len(p.skipped), Changes: p.changes}, raw)
}

// AuthorEntityAdmin is the entity step of the «Сканирование» card: its
// status and the start of a pass.
type AuthorEntityAdmin struct {
	db *pg.DB
}

// NewAuthorEntityAdmin builds the admin.
func NewAuthorEntityAdmin(db *pg.DB) *AuthorEntityAdmin { return &AuthorEntityAdmin{db: db} }

// AuthorEntityStatus is what the card shows: the newest pass with its
// progress, and the report of the newest completed one.
type AuthorEntityStatus struct {
	Run    *database.AuthorEntityBuildRun `json:"run"`
	Report json.RawMessage                `json:"report" swaggertype:"object"`
}

// Status reads the card's status.
func (a *AuthorEntityAdmin) Status(ctx context.Context) (*AuthorEntityStatus, error) {
	latest, completed, err := database.LatestAuthorEntityBuilds(ctx, a.db)
	if err != nil {
		return nil, err
	}
	st := &AuthorEntityStatus{Run: latest}
	if completed != nil {
		st.Report = completed.Report
	}
	if st.Run != nil {
		// The card reads the report once, beside the run.
		run := *st.Run
		run.Report = nil
		st.Run = &run
	}
	return st, nil
}

// Start records a new pass; the worker picks it up.
func (a *AuthorEntityAdmin) Start(ctx context.Context, actor int64) (*database.AuthorEntityBuildRun, error) {
	run, err := database.StartAuthorEntityBuild(ctx, a.db, actor)
	if err != nil {
		return nil, err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityBuildStarted, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
	})
	return run, nil
}
