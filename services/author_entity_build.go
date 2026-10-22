package services

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The author entity build (design E, phase E1): a background pass an
// administrator starts from the «Сканирование» card. It reads the selected
// author credits and what the previous pass stored, plans the entities
// deterministically (internal/authorentity) and writes the difference a
// batch of units per transaction, recording the progress in the same
// transaction. A restart plans again over what is stored — the units
// already written have nothing left to write — so the pass resumes where it
// stopped and a second pass over the same data writes nothing. Nothing a
// reader sees reads the entities yet (shadow).

// AuthorEntityBuildConfig bounds the pass: units per transaction, the
// worker's lease on the pass and its rest when there is nothing to do.
type AuthorEntityBuildConfig struct {
	Batch int
	Lease time.Duration
	Poll  time.Duration
}

const (
	defaultAuthorEntityBatch = 500
	defaultAuthorEntityLease = 5 * time.Minute
	defaultAuthorEntityPoll  = 5 * time.Second
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
	return c
}

// AuthorEntityBuildLoop runs the pass an administrator started.
type AuthorEntityBuildLoop struct {
	db    *pg.DB
	cfg   AuthorEntityBuildConfig
	owner uuid.UUID
	// plan is the pass being applied: planned once per run in this
	// process, planned again after a restart.
	plan *authorEntityPlan
}

// authorEntityPlan is a planned pass with its place in the apply.
type authorEntityPlan struct {
	runID   int64
	plan    *authorentity.Plan
	next    int
	skipped int
	changes int64
	timings AuthorEntityBuildTimings
	started time.Time
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
	UnselectedCredits int                      `json:"unselected_credits"`
	UnitsSkipped      int                      `json:"units_skipped"`
	Timings           AuthorEntityBuildTimings `json:"timings"`
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

// RunOnce does one step of the active pass: the plan, a batch of units, or
// the report. It reports whether there was a pass to step.
func (l *AuthorEntityBuildLoop) RunOnce(ctx context.Context) (bool, error) {
	run, err := database.ClaimAuthorEntityBuild(ctx, l.db, l.owner, l.cfg.Lease)
	if err != nil || run == nil {
		l.plan = nil
		return false, err
	}
	if l.plan == nil || l.plan.runID != run.ID {
		return true, l.planRun(ctx, run)
	}
	if l.plan.next < len(l.plan.plan.Units) {
		return true, l.applyBatch(ctx, run)
	}
	return true, l.complete(ctx, run)
}

// planRun reads and plans the pass.
func (l *AuthorEntityBuildLoop) planRun(ctx context.Context, run *database.AuthorEntityBuildRun) error {
	if err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return database.RecordAuthorEntityProgress(ctx, tx, run.ID, l.owner, l.cfg.Lease,
			database.AuthorEntityProgress{Stage: database.AuthorEntityStagePlan})
	}); err != nil {
		return err
	}
	started := time.Now()
	in, err := database.LoadAuthorEntityInput(ctx, l.db)
	if err != nil {
		return err
	}
	old, err := database.LoadAuthorEntityState(ctx, l.db)
	if err != nil {
		return err
	}
	in.Old = old
	loaded := time.Now()
	plan := authorentity.Build(in)
	planned := time.Now()
	l.plan = &authorEntityPlan{runID: run.ID, plan: plan, started: planned, timings: AuthorEntityBuildTimings{
		LoadMS: loaded.Sub(started).Milliseconds(), PlanMS: planned.Sub(loaded).Milliseconds()}}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityBuildPlanned, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
		Count: len(plan.Units), Worker: authorEntityBuildName,
	})
	return l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return database.RecordAuthorEntityProgress(ctx, tx, run.ID, l.owner, l.cfg.Lease, l.progress())
	})
}

func (l *AuthorEntityBuildLoop) progress() database.AuthorEntityProgress {
	return database.AuthorEntityProgress{Stage: database.AuthorEntityStageApply, UnitsTotal: len(l.plan.plan.Units),
		UnitsDone: l.plan.next, UnitsSkipped: l.plan.skipped, Changes: l.plan.changes}
}

// applyBatch writes the next batch of units with the progress, in one
// transaction.
func (l *AuthorEntityBuildLoop) applyBatch(ctx context.Context, run *database.AuthorEntityBuildRun) error {
	p := l.plan
	end := min(p.next+l.cfg.Batch, len(p.plan.Units))
	var changes int
	var skipped []database.AuthorEntityUnitSkip
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var err error
		changes, skipped, err = database.ApplyAuthorEntityUnits(ctx, tx, run.ID, p.plan.Units[p.next:end])
		if err != nil {
			return err
		}
		next := database.AuthorEntityProgress{Stage: database.AuthorEntityStageApply, UnitsTotal: len(p.plan.Units),
			UnitsDone: end, UnitsSkipped: p.skipped + len(skipped), Changes: p.changes + int64(changes)}
		return database.RecordAuthorEntityProgress(ctx, tx, run.ID, l.owner, l.cfg.Lease, next)
	})
	if err != nil {
		if errors.Is(err, database.ErrAuthorEntityLeaseLost) {
			l.plan = nil
		}
		return err
	}
	for _, sk := range skipped {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventEntityBuildUnitSkipped, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
			Fingerprint: sk.Fingerprint, SQLState: sk.SQLState, Worker: authorEntityBuildName,
		})
	}
	p.next, p.skipped, p.changes = end, p.skipped+len(skipped), p.changes+int64(changes)
	return nil
}

// complete stores the report and completes the pass.
func (l *AuthorEntityBuildLoop) complete(ctx context.Context, run *database.AuthorEntityBuildRun) error {
	p := l.plan
	unselected, err := database.CountUnselectedAuthorCredits(ctx, l.db)
	if err != nil {
		return err
	}
	p.timings.ApplyMS = time.Since(p.started).Milliseconds()
	rep := AuthorEntityBuildReport{Report: p.plan.Report, UnselectedCredits: unselected, UnitsSkipped: p.skipped,
		Timings: p.timings}
	rep.Changes = int(p.changes)
	raw, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	if completeErr := database.CompleteAuthorEntityBuild(ctx, l.db, run.ID, l.owner, raw); completeErr != nil {
		return completeErr
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityBuildCompleted, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
		Count: int(p.changes), Worker: authorEntityBuildName,
	})
	l.plan = nil
	return nil
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
	Report json.RawMessage                `json:"report"`
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
