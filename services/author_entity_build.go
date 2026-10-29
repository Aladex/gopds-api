package services

import (
	"context"
	"encoding/json"
	"errors"
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
	// refused counts the fence's refusals in a row of one pass (E4b-2).
	// It lives in the worker: one that restarts, or takes a pass over,
	// counts again from zero, at worst three more attempts.
	refused struct {
		run int64
		n   int
	}
}

// authorEntityPlan is a planned pass and what its apply wrote.
type authorEntityPlan struct {
	runID int64
	plan  *authorentity.Plan
	// census is the class of a census step that failed; empty while none.
	census   string
	skipped  []database.AuthorEntityUnitSkip
	changes  int64
	attempts int
	timings  AuthorEntityBuildTimings
	// decisions is what the pass wrote of its decisions (E4b).
	decisions database.MergeDecisionWrite
	// snapshot is the plan's one snapshot; fenced is a plan that applies a
	// model decision, which commits only if its input did not move after
	// the snapshot (E4b-2, plan §4.9). refusals is how many attempts the
	// fence refused before this one.
	snapshot string
	fenced   bool
	refusals int
}

// AuthorEntityBuildTimings are the pass's durations in milliseconds.
type AuthorEntityBuildTimings struct {
	LoadMS int64 `json:"load_ms"`
	PlanMS int64 `json:"plan_ms"`
	// CardMS is the read of the merge candidates' sample books and the
	// seal of their cards (E4a).
	CardMS int64 `json:"card_ms"`
	// DecideMS is the decision step (E4b), absent when the pass had none.
	DecideMS int64 `json:"decide_ms,omitempty"`
	ApplyMS  int64 `json:"apply_ms"`
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
	// CandidateDiff is what the pass changed in the stored merge
	// candidates; CandidateError the class of the census step that failed.
	CandidateDiff  database.MergeCandidateDiff `json:"merge_candidate_diff"`
	CandidateError string                      `json:"merge_candidate_error,omitempty"`
	// DecisionWrite is what the pass wrote of its decisions' states and
	// revivals (E4b); absent when it wrote none.
	DecisionWrite *database.MergeDecisionWrite `json:"merge_decision_write,omitempty"`
	// FenceRefusals is how many attempts the fence refused before this
	// one landed (E4b-2); absent when none.
	FenceRefusals int `json:"fence_refusals,omitempty"`
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
	// authorEntityWithoutCandidates makes a test's pass the one of the
	// binary before migration 63, which had no candidate tables;
	// authorEntityCensusHook runs at each step of the census, and an error
	// it returns fails that step.
	authorEntityWithoutCandidates bool
	authorEntityCensusHook        func(step string) error
	// authorEntityCensusHoldHook runs as a run's census write starts
	// (step "start") and once it holds the census lock ("locked"); a test
	// holds a worker there.
	authorEntityCensusHoldHook func(runID int64, step string)
	// authorEntityLoadHook runs after each load of the plan's snapshot;
	// authorEntityReadHook once the read transaction has its snapshot.
	authorEntityLoadHook func(step string)
	authorEntityReadHook func(tx *pg.Tx)
)

// The loads of the plan's snapshot, as authorEntityLoadHook names them.
const (
	AuthorEntityLoadInput     = "input"
	AuthorEntityLoadState     = "state"
	AuthorEntityLoadWikidata  = "wikidata"
	AuthorEntityLoadDecisions = "decisions"
	AuthorEntityLoadVetoes    = "vetoes"
	AuthorEntityLoadCards     = "cards"
)

// authorEntityFenceRefused is the status of a refusal that did not fail the
// pass.
const authorEntityFenceRefused = "refused"

// AuthorEntityBuildStatuses are the statuses the entity build's events
// carry: the census's closed classes, the failed census of a pass with
// decisions, and the fence's refusal and its failure.
func AuthorEntityBuildStatuses() []string {
	return []string{AuthorEntityCensusErrorGenerate, AuthorEntityCensusErrorCardLoad, AuthorEntityCensusErrorSeal,
		AuthorEntityCensusErrorWrite, AuthorEntityBuildErrorCensus, authorEntityFenceRefused,
		AuthorEntityBuildErrorInputMoved, AuthorEntityBuildErrorVetoOverlap, authorEntityMergeRevoked,
		authorEntityMergeVetoRetired, authorEntityMergeVetoReanchored}
}

// loaded runs the test hook after a load of the plan's snapshot.
func loaded(step string) {
	if authorEntityLoadHook != nil {
		authorEntityLoadHook(step)
	}
}

// The commit-time fence (E4b-2, plan §4.9). A pass plans from one
// REPEATABLE READ snapshot. One that applies a model decision checks, as
// the last step before its completion, that no input changed after that
// snapshot (database.FenceAuthorEntityBuild); a refused attempt rolls back
// whole and the pass is planned again from a new snapshot. The third
// refusal in a row fails the pass AuthorEntityBuildErrorInputMoved and its
// marks are given back, so the autopilot builds again once the input is
// quiet. A pass that applies no decision is not fenced: it lands as before.
const (
	AuthorEntityBuildErrorInputMoved = "input_moved"
	authorEntityFenceRefusals        = 3
)

// The census of merge candidates (E4a) only observes the pass: it never
// holds it. The candidates are generated and sealed while the pass is
// planned and written in a transaction of their own once the pass has
// committed, so a failing step costs the census alone: the pass lands, the
// stored census stays as the last pass that wrote it left it, the report
// names the step's closed class, and the next pass does the census again.
const (
	// The steps of the census.
	AuthorEntityCensusCardLoad = "card_load"
	AuthorEntityCensusSeal     = "seal"
	AuthorEntityCensusWrite    = "write"
	// The closed classes of a census that did not land.
	AuthorEntityCensusErrorGenerate = authorentity.CandidateErrorGenerate
	AuthorEntityCensusErrorCardLoad = "card_load_failed"
	AuthorEntityCensusErrorSeal     = "seal_failed"
	AuthorEntityCensusErrorWrite    = "write_failed"
	// AuthorEntityCensusErrorSuperseded is a census a newer completed pass
	// made obsolete before it was written: not written, not an error.
	AuthorEntityCensusErrorSuperseded = "superseded"
)

// A pass with merge decisions, or with entities a model union absorbed,
// plans by its sealed census (E4b): without it the pass cannot tell which
// decisions hold, and lapsing them all would split every model merge on a
// passing read error. Such a pass fails closed instead: nothing is written,
// the run fails with AuthorEntityBuildErrorCensus, and its marks are given
// back for the next one. A pass with neither keeps E4a's rule: the census
// never holds it.
const AuthorEntityBuildErrorCensus = "census_failed"

// errAuthorEntityCensusMissing is a pass that needs its census and has none.
var errAuthorEntityCensusMissing = errors.New("the pass has merge decisions and no sealed census")

// censusHold runs the test hook that holds a census write.
func censusHold(runID int64, step string) {
	if authorEntityCensusHoldHook != nil {
		authorEntityCensusHoldHook(runID, step)
	}
}

// censusStep runs the test hook of a census step.
func censusStep(step string) error {
	if authorEntityCensusHook != nil {
		return authorEntityCensusHook(step)
	}
	return nil
}

// authorEntityCensusRecordTimeout bounds the record of a census that failed.
const authorEntityCensusRecordTimeout = 10 * time.Second

// No statement of the apply transaction runs away.
const authorEntityStatementTimeout = "5min"

// authorEntityReadStatementTimeout bounds each statement of the plan's read
// (E4b-2 review P3): the longest, the credits' load, takes ≈ 16 s on prod,
// and the bound stays well below the worker's five-minute lease. A wait for
// a lock is bounded by the worker's lock timeout, below it.
const authorEntityReadStatementTimeout = 90 * time.Second

// RunOnce does one attempt of the active pass: plans it and writes it whole,
// or writes nothing. It reports whether there was a pass to attempt.
func (l *AuthorEntityBuildLoop) RunOnce(ctx context.Context) (bool, error) {
	run, err := database.ClaimAuthorEntityBuild(ctx, l.db, l.owner, l.cfg.Lease)
	if err != nil || run == nil {
		return false, err
	}
	p, err := l.planRun(ctx, run)
	if errors.Is(err, errAuthorEntityCensusMissing) {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventEntityBuildCensusFailed, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
			Status: AuthorEntityBuildErrorCensus, Worker: authorEntityBuildName,
		})
		return true, database.FailAuthorEntityBuild(ctx, l.db, run.ID, l.owner, AuthorEntityBuildErrorCensus)
	}
	if err != nil {
		return true, err
	}
	if overlaps := p.plan.VetoOverlaps(); len(overlaps) > 0 {
		return true, l.refuseOverlap(ctx, run.ID, overlaps)
	}
	if authorEntityBeforeApplyHook != nil {
		authorEntityBeforeApplyHook()
	}
	if err := l.apply(ctx, run, p); errors.Is(err, database.ErrAuthorEntityInputMoved) {
		return true, l.refuse(ctx, run.ID)
	} else if err != nil {
		return true, err
	}
	if !authorEntityWithoutCandidates && p.census == "" {
		l.writeCensus(ctx, run.ID, p)
	}
	// A superseded census is no failure: a newer pass wrote the current one.
	if p.census != "" && p.census != AuthorEntityCensusErrorSuperseded {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventEntityBuildCensusFailed, Stage: AuthorMetadataStageEntityBuild, RunID: run.ID,
			Status: p.census, Worker: authorEntityBuildName,
		})
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

// AuthorEntityBuildErrorVetoOverlap is a pass refused whole because the two
// sides of a live veto share a fingerprint: no pass can keep them apart
// (§4.11, round 5). It stays refused until an administrator retires or
// re-anchors the veto.
const AuthorEntityBuildErrorVetoOverlap = "veto_overlap"

// refuseOverlap fails the pass veto_overlap, its report naming each veto
// and the fingerprints its sides share; nothing of the pass is written.
func (l *AuthorEntityBuildLoop) refuseOverlap(ctx context.Context, runID int64, overlaps []authorentity.VetoOverlap) error {
	raw, err := json.Marshal(map[string][]authorentity.VetoOverlap{"veto_overlaps": overlaps})
	if err != nil {
		return err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityBuildStepFailed, Stage: AuthorMetadataStageEntityBuild, RunID: runID,
		Status: AuthorEntityBuildErrorVetoOverlap, Count: len(overlaps), Worker: authorEntityBuildName,
	})
	return database.FailAuthorEntityBuildReport(ctx, l.db, runID, l.owner, AuthorEntityBuildErrorVetoOverlap, raw)
}

// refuse counts a refused attempt of the pass; the third in a row fails it.
func (l *AuthorEntityBuildLoop) refuse(ctx context.Context, runID int64) error {
	if l.refused.run != runID {
		l.refused.run, l.refused.n = runID, 0
	}
	l.refused.n++
	status := authorEntityFenceRefused
	if l.refused.n >= authorEntityFenceRefusals {
		status = AuthorEntityBuildErrorInputMoved
	}
	LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityBuildInputMoved, Stage: AuthorMetadataStageEntityBuild, RunID: runID,
		Status: status, Count: l.refused.n, Worker: authorEntityBuildName,
	})
	if status == authorEntityFenceRefused {
		return nil
	}
	l.refused.n = 0
	return database.FailAuthorEntityBuild(ctx, l.db, runID, l.owner, AuthorEntityBuildErrorInputMoved)
}

// refusals is how many attempts of the pass the fence refused in a row.
func (l *AuthorEntityBuildLoop) refusals(runID int64) int {
	if l.refused.run != runID {
		return 0
	}
	return l.refused.n
}

// planInput is what the plan read in its one snapshot.
type planInput struct {
	snapshot string
	plan     *authorentity.Plan
	cards    map[int64]authorentity.CardBookFields
	// census is the class of a census step that failed while reading.
	census          string
	loaded, planned time.Time
}

// readPlan reads every input of the pass in one REPEATABLE READ READ ONLY
// transaction whose first statement takes its snapshot (plan §4.9), and
// plans it there: the plan, the census's card books included, is exactly
// what that snapshot shows. The transaction is read only and holds no lock
// a writer takes; it holds back the vacuum horizon while it runs.
func (l *AuthorEntityBuildLoop) readPlan(ctx context.Context) (*planInput, error) {
	r := &planInput{}
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var err error
		if r.snapshot, err = database.SnapshotAuthorEntityPlan(ctx, tx, authorEntityReadStatementTimeout,
			l.cfg.LockTimeout); err != nil {
			return err
		}
		if authorEntityReadHook != nil {
			authorEntityReadHook(tx)
		}
		in, err := database.LoadAuthorEntityInput(ctx, tx)
		if err != nil {
			return err
		}
		loaded(AuthorEntityLoadInput)
		if in.Old, err = database.LoadAuthorEntityState(ctx, tx); err != nil {
			return err
		}
		loaded(AuthorEntityLoadState)
		// The published Wikidata extract is evidence (E4w); none is none.
		if in.Reference, err = database.LoadWikidataReference(ctx, tx, in); err != nil {
			return err
		}
		loaded(AuthorEntityLoadWikidata)
		if !authorEntityWithoutCandidates {
			if in.Decisions, err = database.LoadMergeDecisions(ctx, tx); err != nil {
				return err
			}
			if len(in.Decisions) > 0 {
				notSame, nsErr := database.LoadMergeNotSame(ctx, tx)
				if nsErr != nil {
					return nsErr
				}
				in.NotSame = make(map[[32]byte]bool, len(notSame))
				for _, d := range notSame {
					in.NotSame[d] = true
				}
			}
			loaded(AuthorEntityLoadDecisions)
			if in.Vetoes, in.VetoBooks, err = database.LoadMergeVetoes(ctx, tx); err != nil {
				return err
			}
			loaded(AuthorEntityLoadVetoes)
		}
		r.loaded = time.Now()
		r.plan = authorentity.Build(in)
		r.planned = time.Now()
		if !authorEntityWithoutCandidates {
			r.cards, r.census = l.loadCards(ctx, tx, r.plan)
			loaded(AuthorEntityLoadCards)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// planRun reads and plans the pass, one more attempt of it.
func (l *AuthorEntityBuildLoop) planRun(ctx context.Context, run *database.AuthorEntityBuildRun) (*authorEntityPlan, error) {
	attempt, err := database.BeginAuthorEntityAttempt(ctx, l.db, run.ID, l.owner, l.cfg.Lease)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	r, err := l.readPlan(ctx)
	if err != nil {
		return nil, err
	}
	plan, census := r.plan, r.census
	if !authorEntityWithoutCandidates && census == "" {
		census = sealCensus(plan, r.cards)
	}
	sealed := time.Now()
	decided := sealed
	if plan.NeedsMergeCensus() {
		if census != "" {
			return nil, errAuthorEntityCensusMissing
		}
		if plan, err = plan.ApplyMergeDecisions(); err != nil {
			return nil, errors.Join(errAuthorEntityCensusMissing, err)
		}
		decided = time.Now()
	}
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
	return &authorEntityPlan{runID: run.ID, plan: plan, census: census, attempts: attempt.Attempts,
		snapshot: r.snapshot, fenced: plan.Report.Model != nil && plan.Report.Model.Applied > 0,
		refusals: l.refusals(run.ID), timings: AuthorEntityBuildTimings{
			LoadMS: r.loaded.Sub(started).Milliseconds(), PlanMS: r.planned.Sub(r.loaded).Milliseconds(),
			CardMS: sealed.Sub(r.planned).Milliseconds(), DecideMS: decided.Sub(sealed).Milliseconds()}}, nil
}

// loadCards reads the candidates' sample books in the plan's snapshot; it
// returns the class of the step that failed, empty when none did.
func (l *AuthorEntityBuildLoop) loadCards(
	ctx context.Context, tx pg.DBI, plan *authorentity.Plan,
) (cards map[int64]authorentity.CardBookFields, class string) {
	if class = plan.Report.Candidates.Error; class != "" {
		return nil, class
	}
	cards, err := database.LoadMergeCardBooks(ctx, tx, plan.CardBookIDs())
	if err == nil {
		err = censusStep(AuthorEntityCensusCardLoad)
	}
	if err != nil {
		return nil, AuthorEntityCensusErrorCardLoad
	}
	return cards, ""
}

// sealCensus seals the candidates' cards; it returns the class of the step
// that failed, empty when none did.
func sealCensus(plan *authorentity.Plan, cards map[int64]authorentity.CardBookFields) string {
	err := censusStep(AuthorEntityCensusSeal)
	if err == nil {
		err = plan.SealMergeCandidates(cards)
	}
	if err != nil {
		return AuthorEntityCensusErrorSeal
	}
	return ""
}

// writeCensus writes the sealed census, and its diff into the committed
// pass's report, in a transaction of its own; a failure is recorded as its
// class in the report and leaves the stored census as it was.
func (l *AuthorEntityBuildLoop) writeCensus(ctx context.Context, runID int64, p *authorEntityPlan) {
	censusHold(runID, "start")
	err := l.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', ?, true)`,
			authorEntityStatementTimeout); err != nil {
			return err
		}
		// Census writes are serialized, and one of a pass a later pass has
		// completed after is not written at all.
		if err := database.LockMergeCensus(ctx, tx, runID); err != nil {
			return err
		}
		censusHold(runID, "locked")
		diff, err := database.WriteMergeCandidates(ctx, tx, runID, p.plan)
		if err == nil {
			err = censusStep(AuthorEntityCensusWrite)
		}
		if err != nil {
			return err
		}
		raw, err := json.Marshal(diff)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE public.author_entity_build_run
			SET report = report || jsonb_build_object('merge_candidate_diff', ?::jsonb) WHERE id = ?`, string(raw), runID)
		return err
	})
	if err == nil {
		return
	}
	p.census = AuthorEntityCensusErrorWrite
	if errors.Is(err, database.ErrMergeCensusSuperseded) {
		p.census = AuthorEntityCensusErrorSuperseded
	}
	// The record outlives a caller that is going away: the pass committed.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), authorEntityCensusRecordTimeout)
	defer cancel()
	if _, recordErr := l.db.ExecContext(recordCtx, `UPDATE public.author_entity_build_run
		SET report = report || jsonb_build_object('merge_candidate_error', ?::text) WHERE id = ?`,
		p.census, runID); recordErr != nil {
		LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
			Name: AuthorMetadataEventEntityBuildStepFailed, Stage: AuthorMetadataStageEntityBuild, RunID: runID,
			SQLState: AuthorMetadataSQLState(recordErr), Worker: authorEntityBuildName,
		})
	}
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
		// What the pass did with its decisions lands with the units.
		if len(p.plan.DecisionStates()) > 0 || len(p.plan.Revivals()) > 0 {
			w, err := database.WriteMergeDecisionStates(ctx, tx, run.ID, p.plan)
			if err != nil {
				return err
			}
			p.decisions = w
		}
		// What the pass found of each live veto (E4b-3).
		if len(p.plan.VetoStates()) > 0 {
			n, err := database.WriteMergeVetoStates(ctx, tx, run.ID, p.plan)
			if err != nil {
				return err
			}
			p.decisions.VetoStates = n
		}
		// Last before the completion: whatever the attempt wrote rolls back
		// whole when the fence refuses it.
		if p.fenced {
			if authorEntityApplyHook != nil {
				if err := authorEntityApplyHook("fence"); err != nil {
					return err
				}
			}
			if err := database.FenceAuthorEntityBuild(ctx, tx, p.snapshot); err != nil {
				return err
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
		Attempts: p.attempts, Timings: p.timings, CandidateError: p.census, FenceRefusals: p.refusals}
	if p.decisions != (database.MergeDecisionWrite{}) {
		rep.DecisionWrite = &p.decisions
	}
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
	// quiet is the autopilot's quiet period, as the card shows it.
	quiet time.Duration
}

// NewAuthorEntityAdmin builds the admin.
func NewAuthorEntityAdmin(db *pg.DB) *AuthorEntityAdmin {
	return &AuthorEntityAdmin{db: db, quiet: DefaultAuthorAutopilotQuiet}
}

// WithAutopilotQuiet sets the autopilot's quiet period the card shows; a
// non-positive one keeps the default.
func (a *AuthorEntityAdmin) WithAutopilotQuiet(quiet time.Duration) *AuthorEntityAdmin {
	if quiet > 0 {
		a.quiet = quiet
	}
	return a
}

// AuthorEntityStatus is what the card shows: the newest pass with its
// progress, the report of the newest completed one and the Wikidata
// reference.
type AuthorEntityStatus struct {
	Run    *database.AuthorEntityBuildRun `json:"run"`
	Report json.RawMessage                `json:"report" swaggertype:"object"`
	// Wikidata is the reference the build reads (E4w).
	Wikidata *WikidataStatus `json:"wikidata"`
	// Autopilot is the author autopilot's block.
	Autopilot *AuthorAutopilotStatus `json:"autopilot"`
}

// Status reads the card's status.
func (a *AuthorEntityAdmin) Status(ctx context.Context) (*AuthorEntityStatus, error) {
	latest, completed, err := database.LatestAuthorEntityBuilds(ctx, a.db)
	if err != nil {
		return nil, err
	}
	wd, err := a.WikidataStatus(ctx)
	if err != nil {
		return nil, err
	}
	pilot, err := ReadAuthorAutopilotStatus(ctx, a.db, a.quiet)
	if err != nil {
		return nil, err
	}
	st := &AuthorEntityStatus{Run: latest, Wikidata: wd, Autopilot: pilot}
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

// SetAutopilot switches the autopilot on or off and returns its block.
func (a *AuthorEntityAdmin) SetAutopilot(ctx context.Context, enabled bool, actor int64) (*AuthorAutopilotStatus, error) {
	if err := SetAuthorAutopilot(ctx, a.db, enabled, actor); err != nil {
		return nil, err
	}
	return ReadAuthorAutopilotStatus(ctx, a.db, a.quiet)
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

// The model merges of the card (E4b-3b): what is decided and vetoed, and the
// administrator's three actions on them. Each runs in one transaction and
// marks a build (the decision and veto tables are build input).

// Statuses of AuthorMetadataEventEntityMergeAction.
const (
	authorEntityMergeRevoked        = "revoked"
	authorEntityMergeVetoRetired    = "veto_retired"
	authorEntityMergeVetoReanchored = "veto_reanchored"
)

// MergeDecisions reads the card's merge block.
func (a *AuthorEntityAdmin) MergeDecisions(ctx context.Context) (*database.MergeDecisionsView, error) {
	return database.ReadMergeDecisions(ctx, a.db)
}

// RevokeMergeDecision revokes a live same decision; its veto keeps the pair
// apart from the next pass on. It returns the revoke's id.
func (a *AuthorEntityAdmin) RevokeMergeDecision(ctx context.Context, decisionID, actor int64) (int64, error) {
	id, err := database.RevokeMergeDecision(ctx, a.db, decisionID, actor)
	if err == nil {
		logMergeAction(authorEntityMergeRevoked, decisionID)
	}
	return id, err
}

// RetireMergeVeto retires a live veto: its sides may be joined again.
func (a *AuthorEntityAdmin) RetireMergeVeto(ctx context.Context, vetoID, actor int64) error {
	err := a.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return database.RetireMergeVeto(ctx, tx, vetoID, actor)
	})
	if err == nil {
		logMergeAction(authorEntityMergeVetoRetired, vetoID)
	}
	return err
}

// ReanchorMergeVeto re-anchors a live veto on two current entities and
// returns the new veto's id.
func (a *AuthorEntityAdmin) ReanchorMergeVeto(
	ctx context.Context, vetoID int64, sideA, sideB uuid.UUID, actor int64,
) (int64, error) {
	var id int64
	err := a.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var err error
		id, err = database.ReanchorMergeVeto(ctx, tx, vetoID, sideA, sideB, actor)
		return err
	})
	if err == nil {
		logMergeAction(authorEntityMergeVetoReanchored, vetoID)
	}
	return id, err
}

func logMergeAction(status string, id int64) {
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventEntityMergeAction, Stage: AuthorMetadataStageEntityBuild, ItemID: id, Status: status,
	})
}
