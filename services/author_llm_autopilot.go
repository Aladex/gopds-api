package services

import (
	"context"
	"errors"
	"slices"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The LLM step of the author autopilot (AuthorAutopilot): a full resolve run
// over the open review inputs of the strata the configuration registered
// already, started under the card's limits — the layer on, the resolve pilot
// completed, no participant or endpoint paused, budget left. Never an eval,
// and never a stratum the card's own resolve start has not registered: a
// stratum a newer eval passed waits for the administrator.
//
// What makes a run due is an input of those strata never asked under the
// configuration. The inputs a run asks again — those whose last answer failed
// transiently, or applied on the fingerprint defect — go with the next run
// but make none due on their own, so a failure that repeats starts no run
// after run.

// How the resolve check came out.
const (
	AutopilotResolveStarted      = "started"
	AutopilotResolveLLMOff       = "llm_not_configured"
	AutopilotResolveNoPilot      = "no_resolve_pilot"
	AutopilotResolveLLMPaused    = "llm_paused"
	AutopilotResolveBudget       = "budget_exhausted"
	AutopilotResolveNoRegistered = "no_registered_strata"
	AutopilotResolveNothing      = "nothing_to_resolve"
	// AutopilotResolveEvidence: the evidence or the registrations changed
	// under the start; a later tick plans again.
	AutopilotResolveEvidence = "evidence_changed"
	// AutopilotResolveRunActive: an LLM run started under the start; a
	// later tick waits for it.
	AutopilotResolveRunActive = "llm_run_active"
)

// AutopilotResolveNotes are every note of the resolve check.
func AutopilotResolveNotes() []string {
	return []string{AutopilotResolveStarted, AutopilotResolveLLMOff, AutopilotResolveNoPilot, AutopilotResolveLLMPaused,
		AutopilotResolveBudget, AutopilotResolveNoRegistered, AutopilotResolveNothing, AutopilotResolveEvidence,
		AutopilotResolveRunActive}
}

// AutopilotResolve is what the resolve check did: the run it started, or
// why it started none (Note), with the inputs it found never asked.
// Unregistered are the planned strata it left out because the card has not
// registered them.
type AutopilotResolve struct {
	RunID        int64
	Note         string
	Inputs       int
	Unregistered []string
}

// StartAutopilotResolve starts the autopilot's resolve run, or says why it
// starts none. within runs in the start's transaction once the run is
// written, with the inputs it found never asked (the autopilot records it
// there); an error it returns undoes the
// start and is returned. The caller has checked that no LLM run is active;
// one that starts meanwhile refuses the start (llm_run_active).
func (a *AuthorLLMAdmin) StartAutopilotResolve(
	ctx context.Context, within func(tx *pg.Tx, runID int64, inputs int) error,
) (*AutopilotResolve, error) {
	note := func(n string) (*AutopilotResolve, error) { return &AutopilotResolve{Note: n}, nil }
	if a.disabled != "" {
		return note(AutopilotResolveLLMOff)
	}
	if limit, err := a.autopilotLimit(ctx); err != nil || limit != "" {
		if err != nil {
			return nil, err
		}
		return note(limit)
	}
	ev, plan, err := productionPlan(ctx, a.db, models.AuthorLLMRunResolve, a.version)
	if err != nil {
		return nil, err
	}
	unregistered, err := a.keepRegistered(ctx, &plan)
	if err != nil {
		return nil, err
	}
	if ev == nil || len(plan.Strata) == 0 {
		return &AutopilotResolve{Note: AutopilotResolveNoRegistered, Unregistered: unregistered}, nil
	}
	inputs, err := a.inputsNeverAsked(ctx, &plan)
	if err != nil {
		return nil, err
	}
	if inputs == 0 {
		return &AutopilotResolve{Note: AutopilotResolveNothing, Unregistered: unregistered}, nil
	}
	id, _, err := database.StartAuthorLLMResolveRun(ctx, a.db, &database.AuthorLLMResolveStart{
		Kind: models.AuthorLLMRunResolve, ConfigVersion: a.version, Mode: models.AuthorLLMEvalModeFull, Evidence: ev,
		Settings: plan, RegisteredOnly: true, Within: func(tx *pg.Tx, runID int64) error {
			if within == nil {
				return nil
			}
			return within(tx, runID, inputs)
		}})
	switch {
	case errors.Is(err, database.ErrAuthorLLMActiveRun):
		return note(AutopilotResolveRunActive)
	case errors.Is(err, database.ErrAuthorLLMEvidenceChanged), errors.Is(err, database.ErrAuthorLLMNoEvidence),
		errors.Is(err, database.ErrAuthorLLMRunEvidence), errors.Is(err, database.ErrAuthorLLMNotRegistered):
		return note(AutopilotResolveEvidence)
	case err != nil:
		return nil, err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{Name: AuthorMetadataEventLLMResolveStarted,
		Stage: AuthorMetadataStageRunner, RunID: id, Worker: authorLLMResolveDriverName})
	return &AutopilotResolve{RunID: id, Note: AutopilotResolveStarted, Inputs: inputs, Unregistered: unregistered}, nil
}

// autopilotLimit is the card's limit that keeps the autopilot from
// starting a resolve run, or "" when none does: no completed resolve pilot,
// a paused participant or endpoint, the budget spent.
func (a *AuthorLLMAdmin) autopilotLimit(ctx context.Context) (string, error) {
	completed, err := database.AuthorLLMCompletedRuns(ctx, a.db, a.version)
	if err != nil {
		return "", err
	}
	if !completed[string(models.AuthorLLMRunResolve)+"/"+models.AuthorLLMEvalModePilot] {
		return AutopilotResolveNoPilot, nil
	}
	paused, err := a.llmPaused(ctx)
	if err != nil {
		return "", err
	}
	if paused {
		return AutopilotResolveLLMPaused, nil
	}
	spent, err := database.AuthorLLMTokensSpentAll(ctx, a.db)
	if err != nil {
		return "", err
	}
	if spent >= a.settings.Budget.OneOffTokens {
		return AutopilotResolveBudget, nil
	}
	return "", nil
}

// The merge step of the autopilot (E4e, plan §6): a merge run of the merge
// configuration — the admin's ordinary run, mode full, never an eval —
// over the candidates of the stored census a run would ask (of a
// registered, not withdrawn kind, of a script not blocked, never asked
// under the configuration; a lapsed decision's new candidate among them),
// started under the merge card's limits: a completed merge eval to rest
// on, a live registration, no live veto unanchored, no participant drifted
// or paused, and the run's estimate within what is left of the budget.

// How the merge check came out.
const (
	AutopilotMergeStarted = "started"
	// AutopilotMergeSwitchedOff: the merge step's switch is off.
	AutopilotMergeSwitchedOff = "switched_off"
	// AutopilotMergeBuildDue: a pass is to do first; the candidates are the
	// last completed pass's.
	AutopilotMergeBuildDue = "build_due"
	// AutopilotMergeBuildHeld: the newest pass was held for an administrator
	// (AuthorEntityBuildErrorDecisionsHeld).
	AutopilotMergeBuildHeld    = "build_held"
	AutopilotMergeLLMOff       = "llm_not_configured"
	AutopilotMergeNoEval       = "no_merge_eval"
	AutopilotMergeNoRegistered = "no_registered_kinds"
	AutopilotMergeVetoHeld     = "veto_unanchored"
	AutopilotMergeDriftHeld    = "drift_held"
	AutopilotMergeLLMPaused    = "llm_paused"
	AutopilotMergeNothing      = "nothing_to_merge"
	AutopilotMergeBudget       = "budget_exhausted"
	// AutopilotMergeEvidence: the eval or the registrations changed under
	// the start; a later tick plans again.
	AutopilotMergeEvidence = "evidence_changed"
	// AutopilotMergeRunActive: an LLM run started under the start.
	AutopilotMergeRunActive = "llm_run_active"
)

// AutopilotMergeNotes are every note of the merge check.
func AutopilotMergeNotes() []string {
	return []string{AutopilotMergeStarted, AutopilotMergeSwitchedOff, AutopilotMergeBuildDue, AutopilotMergeBuildHeld,
		AutopilotMergeLLMOff,
		AutopilotMergeNoEval, AutopilotMergeNoRegistered, AutopilotMergeVetoHeld, AutopilotMergeDriftHeld,
		AutopilotMergeLLMPaused, AutopilotMergeNothing, AutopilotMergeBudget, AutopilotMergeEvidence,
		AutopilotMergeRunActive}
}

// AutopilotMerge is what the merge check did: the run it started, or why
// it started none (Note), with the candidates it found due.
type AutopilotMerge struct {
	RunID      int64
	Note       string
	Candidates int
}

// StartAutopilotMerge starts the autopilot's merge run, or says why it
// starts none. within runs in the start's transaction once the run is
// written, with the candidates due (the autopilot records it there); an
// error it returns undoes the start and is returned. The caller has
// checked that no LLM run is active and no pass is to do; a run that
// starts meanwhile refuses the start (llm_run_active).
func (a *AuthorLLMAdmin) StartAutopilotMerge(
	ctx context.Context, within func(tx *pg.Tx, runID int64, candidates int) error,
) (*AutopilotMerge, error) {
	plan, err := a.autopilotMergePlan(ctx)
	if err != nil || plan.note != "" {
		if err != nil {
			return nil, err
		}
		return &AutopilotMerge{Note: plan.note, Candidates: plan.due}, nil
	}
	id, err := startMergeRun(ctx, a.db, &mergeRunStart{version: a.version, mode: models.AuthorLLMEvalModeFull,
		evidence: plan.evidence, registrations: plan.registrations, due: plan.due,
		within: func(tx *pg.Tx, runID int64) error {
			if within == nil {
				return nil
			}
			return within(tx, runID, plan.due)
		}})
	switch {
	case errors.Is(err, database.ErrAuthorLLMActiveRun):
		return &AutopilotMerge{Note: AutopilotMergeRunActive, Candidates: plan.due}, nil
	case errors.Is(err, database.ErrAuthorLLMEvidenceChanged), errors.Is(err, ErrAuthorLLMPrerequisite):
		return &AutopilotMerge{Note: AutopilotMergeEvidence, Candidates: plan.due}, nil
	case err != nil:
		return nil, err
	}
	return &AutopilotMerge{RunID: id, Note: AutopilotMergeStarted, Candidates: plan.due}, nil
}

// autopilotMergePlan is what the merge run would start on: the eval it
// rests on, the live registrations and the candidates due; or the note
// that keeps it from starting.
type autopilotMergePlan struct {
	evidence      *database.AuthorLLMEvalEvidence
	registrations []database.MergeRegistration
	due           int
	note          string
}

// autopilotMergePlan reads the merge run's plan under the merge card's
// limits.
func (a *AuthorLLMAdmin) autopilotMergePlan(ctx context.Context) (*autopilotMergePlan, error) {
	if a.disabled != "" || !a.merge {
		return &autopilotMergePlan{note: AutopilotMergeLLMOff}, nil
	}
	ev, err := database.LatestAuthorLLMMergeEval(ctx, a.db, a.version)
	if err != nil {
		return nil, err
	}
	if ev == nil {
		return &autopilotMergePlan{note: AutopilotMergeNoEval}, nil
	}
	registrations, err := liveMergeRegistrations(ctx, a.db, a.version)
	if err != nil {
		return nil, err
	}
	if len(registrations) == 0 {
		return &autopilotMergePlan{note: AutopilotMergeNoRegistered}, nil
	}
	if held, holdErr := a.mergeHold(ctx); holdErr != nil || held != "" {
		if holdErr != nil {
			return nil, holdErr
		}
		return &autopilotMergePlan{note: held}, nil
	}
	due, err := database.MergeCandidatesDue(ctx, a.db, a.version)
	if err != nil {
		return nil, err
	}
	plan := &autopilotMergePlan{evidence: ev, registrations: registrations, due: len(due)}
	if plan.due == 0 {
		plan.note = AutopilotMergeNothing
		return plan, nil
	}
	spent, err := database.AuthorLLMTokensSpentAll(ctx, a.db)
	if err != nil {
		return nil, err
	}
	if spent+mergeRunEstimate(plan.due) > a.settings.Budget.OneOffTokens {
		plan.note = AutopilotMergeBudget
	}
	return plan, nil
}

// mergeHold is what holds the merge step, or "" when nothing does: a live
// veto unanchored, a participant drifted, a participant or the endpoint
// paused.
func (a *AuthorLLMAdmin) mergeHold(ctx context.Context) (string, error) {
	unanchored, err := database.AuthorEntityVetoUnanchored(ctx, a.db)
	if err != nil {
		return "", err
	}
	if unanchored {
		return AutopilotMergeVetoHeld, nil
	}
	drifted, err := database.MergeApplyDriftHeld(ctx, a.db, a.version, 0)
	if err != nil {
		return "", err
	}
	if drifted {
		return AutopilotMergeDriftHeld, nil
	}
	paused, err := a.llmPaused(ctx)
	if err != nil {
		return "", err
	}
	if paused {
		return AutopilotMergeLLMPaused, nil
	}
	return "", nil
}

// ActiveAuthorLLMRun is the pending, running or paused LLM run of any
// configuration — at most one is (author_llm_run_one_active); 0 when none
// is.
func ActiveAuthorLLMRun(ctx context.Context, db pg.DBI) (int64, error) {
	run, err := database.AuthorLLMRunToShow(ctx, db, "")
	if err != nil || run == nil || !isActiveLLMRun(run.Status) {
		return 0, err
	}
	return run.ID, nil
}

// keepRegistered drops from plan every stratum whose pair the configuration
// has not registered, and returns their names.
func (a *AuthorLLMAdmin) keepRegistered(ctx context.Context, plan *database.AuthorLLMResolveSettings) ([]string, error) {
	registered, err := database.AuthorLLMRegisteredPairs(ctx, a.db, a.version)
	if err != nil {
		return nil, err
	}
	var dropped []string
	for name := range plan.Strata {
		st := plan.Strata[name]
		if !registered[database.AuthorLLMPair(authornorm.DecisionClass(st.Class), st.Script)] {
			dropped = append(dropped, name)
			delete(plan.Strata, name)
		}
	}
	slices.Sort(dropped)
	return dropped, nil
}

// inputsNeverAsked counts the open review inputs of plan's strata the
// configuration has never asked: the enqueue's own population.
func (a *AuthorLLMAdmin) inputsNeverAsked(ctx context.Context, plan *database.AuthorLLMResolveSettings) (int, error) {
	rows, err := database.AuthorLLMEvalPopulation(ctx, a.db)
	if err != nil {
		return 0, err
	}
	strata, _ := classifyPopulation(rows)
	var inputs []database.AuthorLLMReviewInput
	for name, pop := range strata {
		if _, planned := plan.Strata[name]; !planned {
			continue
		}
		for i := range pop {
			inputs = append(inputs, database.AuthorLLMReviewInput{SourceFingerprint: pop[i].row.SourceFingerprint,
				ExtractorVersion: pop[i].row.ExtractorVersion})
		}
	}
	return database.AuthorLLMInputsNeverAsked(ctx, a.db, a.version, models.AuthorLLMPurposeReview, inputs)
}

// llmPaused reports whether a participant or the endpoint of the
// configuration is paused: a run would wait on them.
func (a *AuthorLLMAdmin) llmPaused(ctx context.Context) (bool, error) {
	states, err := database.AuthorLLMProviderStates(ctx, a.db, a.version)
	if err != nil {
		return false, err
	}
	for i := range states {
		if states[i].PausedReason != nil {
			return true, nil
		}
	}
	endpoint, err := database.GetAuthorLLMEndpointState(ctx, a.db, a.version)
	if errors.Is(err, pg.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return endpoint.PausedReason != nil, nil
}
