package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The LLM block of the admin card: what it shows and what it may start. The
// chain is fixed: check quality (an eval pilot), check everything (a full
// eval), then the production runs — resolve pilot, resolve everything,
// swaps. Each step is enabled only when the one before it completed under
// the current configuration. Swap runs are not built yet: their step is
// shown, never enabled, and refused.

// Errors of the LLM admin; each maps to one closed API code.
var (
	// ErrAuthorLLMNotConfigured: the layer is switched off (no endpoint,
	// invalid participants, or the author-metadata workers are off).
	ErrAuthorLLMNotConfigured = errors.New("services: the LLM layer is not configured")
	// ErrAuthorLLMPrerequisite: the step before this one has not completed
	// under the current configuration.
	ErrAuthorLLMPrerequisite = errors.New("services: the previous LLM step has not completed")
	// ErrAuthorLLMRunKindUnavailable: a run kind the chain marks as not
	// available (every kind is built since phase 7; the API keeps the code).
	ErrAuthorLLMRunKindUnavailable = errors.New("services: this LLM run kind is not available yet")
	// ErrAuthorLLMInvalidRun: an unknown kind or mode.
	ErrAuthorLLMInvalidRun = errors.New("services: unknown LLM run kind or mode")
	// ErrAuthorLLMInvalidSlot: an unknown participant.
	ErrAuthorLLMInvalidSlot = errors.New("services: unknown LLM participant")
	// ErrAuthorLLMRunConfigChanged: the run belongs to an older
	// configuration; no worker of the current one would ever move it, so it
	// cannot be resumed — only closed.
	ErrAuthorLLMRunConfigChanged = errors.New("services: the LLM run belongs to an older configuration")
	// ErrAuthorLLMReportNotFound: the run has no stored report.
	ErrAuthorLLMReportNotFound = errors.New("services: the LLM run has no report")
)

// The chain's steps.
const (
	AuthorLLMStepEvalPilot = "eval_pilot"
	AuthorLLMStepEvalFull  = "eval_full"
	// The stage evals: one fixed set of strata each, an alternative to the
	// full eval for evaluating the review backlog in stages.
	AuthorLLMStepEvalStage0   = "eval_stage0"
	AuthorLLMStepEvalStage1   = "eval_stage1"
	AuthorLLMStepEvalStage2   = "eval_stage2"
	AuthorLLMStepEvalStage3   = "eval_stage3"
	AuthorLLMStepResolvePilot = "resolve_pilot"
	AuthorLLMStepResolveFull  = "resolve_full"
	// The swaps: their own eval (the swap strata alone), then the pilot and
	// the full run.
	AuthorLLMStepSwapEval  = "swap_eval"
	AuthorLLMStepSwapPilot = "swap_pilot"
	AuthorLLMStepSwapFull  = "swap_full"
	// The merge configuration's steps (E4c): the merge pilot, then an eval
	// per kind — swap, initials, bucket, translit (§7).
	AuthorLLMStepMerge0 = models.AuthorLLMEvalModeMerge0
	AuthorLLMStepMerge1 = models.AuthorLLMEvalModeMerge1
	AuthorLLMStepMerge2 = models.AuthorLLMEvalModeMerge2
	AuthorLLMStepMerge3 = models.AuthorLLMEvalModeMerge3
	AuthorLLMStepMerge4 = models.AuthorLLMEvalModeMerge4
	// AuthorLLMStepMergeRun is the merge run (E4d-2): the candidates of
	// the registered kinds no eval asked.
	AuthorLLMStepMergeRun = "merge_run"
)

// Why a step is not enabled.
const (
	AuthorLLMStepNotConfigured = "not_configured"
	AuthorLLMStepNotAvailable  = "not_available"
	AuthorLLMStepPrerequisite  = "prerequisite"
	AuthorLLMStepActiveRun     = "active_run"
)

// AuthorLLMStep is one step of the chain as the card draws it.
type AuthorLLMStep struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	Mode string `json:"mode"`
	// Available is false for a run kind not built yet.
	Available bool `json:"available"`
	Enabled   bool `json:"enabled"`
	// Done: a run of this step completed under the current configuration.
	Done bool `json:"done"`
	// Blocked says why a step is not enabled.
	Blocked string `json:"blocked,omitempty"`
	// Strata are the strata a stage eval measures.
	Strata []string `json:"strata,omitempty"`
	// EstimateTokens is a stage eval's token estimate before it starts: the
	// run's own estimator over the sets it would measure.
	EstimateTokens int64 `json:"estimate_tokens,omitempty"`
}

// AuthorLLMProgress is how far the current stage is: its jobs that ended
// out of all of them.
type AuthorLLMProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// AuthorLLMReportSummary is the one line the card shows of an eval report.
type AuthorLLMReportSummary struct {
	SHA256       string `json:"sha256"`
	StrataTotal  int    `json:"strata_total"`
	StrataPassed int    `json:"strata_passed"`
	// ExcerptHelps counts the passed strata whose decision is the excerpt
	// arm.
	ExcerptHelps int  `json:"excerpt_helps"`
	Registrable  bool `json:"registrable"`
	// Repeats is each participant's changed repeated answers, shown so the
	// share can be watched; it is no gate of its own.
	Repeats []AuthorLLMRepeatChange `json:"repeats"`
	// Version is the version summarized, the latest; Versions every version
	// of the run's report, oldest first.
	Version  int                      `json:"version"`
	Versions []AuthorLLMReportVersion `json:"versions"`
}

// AuthorLLMReportVersion is one version of an eval report.
type AuthorLLMReportVersion struct {
	Version     int       `json:"version"`
	SHA256      string    `json:"sha256"`
	Registrable bool      `json:"registrable"`
	CreatedAt   time.Time `json:"created_at"`
}

// AuthorLLMRepeatChange is how many of a participant's repeated answers
// changed out of all of them.
type AuthorLLMRepeatChange struct {
	Participant string `json:"participant"`
	Changed     int    `json:"changed"`
	Total       int    `json:"total"`
}

// AuthorLLMRunState is a run as the card shows it.
type AuthorLLMRunState struct {
	ID             int64              `json:"id"`
	Kind           string             `json:"kind"`
	Mode           string             `json:"mode"`
	Status         string             `json:"status"`
	Stage          string             `json:"stage,omitempty"`
	PausedReason   string             `json:"paused_reason,omitempty"`
	LastErrorClass string             `json:"last_error_class,omitempty"`
	CurrentConfig  bool               `json:"current_config"`
	Progress       *AuthorLLMProgress `json:"progress,omitempty"`
	// EstimateTokens is the run's estimate (known after its freeze);
	// LimitTokens the spend at which it pauses on its budget.
	EstimateTokens int64 `json:"estimate_tokens"`
	LimitTokens    int64 `json:"limit_tokens"`
	SpentTokens    int64 `json:"spent_tokens"`
	// Cost is the spend in the price's unit, when a price is configured.
	Cost   *float64                `json:"cost,omitempty"`
	Report *AuthorLLMReportSummary `json:"report,omitempty"`
	// Resolve is the one line the card shows of a resolve report.
	Resolve *AuthorLLMResolveSummary `json:"resolve,omitempty"`
}

// AuthorLLMResolveSummary is the one line the card shows of a resolve
// report: the inputs whose answer was applied, of all it decided.
type AuthorLLMResolveSummary struct {
	SHA256  string `json:"sha256"`
	Applied int    `json:"applied"`
	Total   int    `json:"total"`
}

// AuthorLLMParticipantState is a participant and its pause, if any.
type AuthorLLMParticipantState struct {
	Slot         string `json:"slot"`
	Model        string `json:"model"`
	PausedReason string `json:"paused_reason,omitempty"`
}

// AuthorLLMStatus is the whole LLM block.
type AuthorLLMStatus struct {
	Enabled bool `json:"enabled"`
	// Reason says why the layer is off.
	Reason string             `json:"reason,omitempty"`
	Run    *AuthorLLMRunState `json:"run"`
	// OtherActive: a run of the other live configuration (the name
	// normalization's or the merge one's) holds the one active slot; its
	// own block shows it.
	OtherActive          bool                        `json:"other_active,omitempty"`
	Steps                []AuthorLLMStep             `json:"steps"`
	Participants         []AuthorLLMParticipantState `json:"participants"`
	EndpointPausedReason string                      `json:"endpoint_paused_reason,omitempty"`
	// The one-off budget every run shares, and what all runs spent.
	BudgetTokens int64  `json:"budget_tokens"`
	SpentTokens  int64  `json:"spent_tokens"`
	PriceUnit    string `json:"price_unit,omitempty"`
}

// AuthorLLMAdmin serves the LLM block.
type AuthorLLMAdmin struct {
	db       *pg.DB
	version  string
	disabled string
	settings config.AuthorLLMConfig
	// merge is the merge configuration's admin (E4c): its chain is the
	// merge evals, and it starts nothing else. identity is the
	// configuration's (a merge report is recomputed under it); nil for an
	// admin of a layer that is off.
	merge    bool
	identity *llmreq.Identity
}

// NewAuthorLLMAdmin builds the admin of a configuration version; a non-empty
// disabled reason switches every action off.
func NewAuthorLLMAdmin(db *pg.DB, version, disabled string, settings *config.AuthorLLMConfig) *AuthorLLMAdmin {
	return &AuthorLLMAdmin{db: db, version: version, disabled: disabled, settings: *settings}
}

// NewAuthorLLMAdminFromConfig builds the admin of the configuration the
// workers run: the same identity and version, or the reason the layer is off
// (the LLM settings, or the author-metadata workers switched off).
func NewAuthorLLMAdminFromConfig(
	ctx context.Context, db *pg.DB, c *config.AuthorMetadataConfig, shared config.LLMConfig,
) (*AuthorLLMAdmin, error) {
	return newAuthorLLMAdminFromConfig(ctx, db, c, shared, llmreq.NewIdentity)
}

// NewAuthorLLMMergeAdminFromConfig builds the admin of the merge
// configuration (E4c): the merge identity and its version, or the reason
// the layer is off. The name normalization's admin is untouched.
func NewAuthorLLMMergeAdminFromConfig(
	ctx context.Context, db *pg.DB, c *config.AuthorMetadataConfig, shared config.LLMConfig,
) (*AuthorLLMAdmin, error) {
	a, err := newAuthorLLMAdminFromConfig(ctx, db, c, shared, llmreq.NewMergeIdentity)
	if a != nil {
		a.merge = true
	}
	return a, err
}

// NewAuthorLLMMergeAdmin builds the merge configuration's admin of an
// identity and its version, as NewAuthorLLMAdmin builds the name
// normalization's.
func NewAuthorLLMMergeAdmin(
	db *pg.DB, identity *llmreq.Identity, version, disabled string, settings *config.AuthorLLMConfig,
) *AuthorLLMAdmin {
	a := NewAuthorLLMAdmin(db, version, disabled, settings)
	a.merge, a.identity = true, identity
	return a
}

func newAuthorLLMAdminFromConfig(
	ctx context.Context, db *pg.DB, c *config.AuthorMetadataConfig, shared config.LLMConfig,
	newIdentity authorLLMIdentityFunc,
) (*AuthorLLMAdmin, error) {
	if !c.Enabled {
		return NewAuthorLLMAdmin(db, "", AuthorLLMDisabledWorkersOff, &c.LLM), nil
	}
	status := c.LLM.Status(shared)
	if !status.Enabled {
		return NewAuthorLLMAdmin(db, "", status.Reason, &c.LLM), nil
	}
	identity, err := authorLLMIdentity(&c.LLM, shared, newIdentity)
	if err != nil {
		return NewAuthorLLMAdmin(db, "", config.AuthorLLMDisabledModels, &c.LLM), nil
	}
	version, err := database.EnsureAuthorLLMConfig(ctx, db, &identity, c.LLM.ConcurrencyStart)
	if err != nil {
		return nil, err
	}
	a := NewAuthorLLMAdmin(db, version, "", &c.LLM)
	a.identity = &identity
	return a, nil
}

// AuthorLLMDisabledWorkersOff: the author-metadata workers are switched off,
// so no LLM run would ever move.
const AuthorLLMDisabledWorkersOff = "workers_disabled"

// authorLLMChain is the chain's steps in the order the card draws them, each
// with the completed runs (kind/mode) any one of which it follows. The stage
// evals are alternatives to the full eval: each follows the pilot, and a
// resolve run follows whichever of them, or the full eval, is the latest
// (authorLLMResolvable).
var authorLLMChain = []struct {
	key, kind, mode string
	available       bool
	after           []string
}{
	{AuthorLLMStepEvalPilot, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModePilot, true, nil},
	{AuthorLLMStepEvalFull, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeFull, true, []string{evalPilotDone}},
	{AuthorLLMStepEvalStage0, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeStage0, true, []string{evalPilotDone}},
	{AuthorLLMStepEvalStage1, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeStage1, true, []string{evalPilotDone}},
	{AuthorLLMStepEvalStage2, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeStage2, true, []string{evalPilotDone}},
	{AuthorLLMStepEvalStage3, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeStage3, true, []string{evalPilotDone}},
	{AuthorLLMStepResolvePilot, string(models.AuthorLLMRunResolve), models.AuthorLLMEvalModePilot, true,
		[]string{authorLLMResolvable}},
	{AuthorLLMStepResolveFull, string(models.AuthorLLMRunResolve), models.AuthorLLMEvalModeFull, true,
		[]string{string(models.AuthorLLMRunResolve) + "/" + models.AuthorLLMEvalModePilot}},
	{AuthorLLMStepSwapEval, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeSwap, true,
		[]string{string(models.AuthorLLMRunResolve) + "/" + models.AuthorLLMEvalModeFull}},
	{AuthorLLMStepSwapPilot, string(models.AuthorLLMRunSwap), models.AuthorLLMEvalModePilot, true,
		[]string{string(models.AuthorLLMRunEval) + "/" + models.AuthorLLMEvalModeSwap}},
	{AuthorLLMStepSwapFull, string(models.AuthorLLMRunSwap), models.AuthorLLMEvalModeFull, true,
		[]string{string(models.AuthorLLMRunSwap) + "/" + models.AuthorLLMEvalModePilot}},
}

// evalPilotDone is the completed eval pilot.
const evalPilotDone = string(models.AuthorLLMRunEval) + "/" + models.AuthorLLMEvalModePilot

// authorLLMResolvable is the condition a resolve run follows: the latest full
// or stage eval's report registers a stratum.
const authorLLMResolvable = "resolvable"

// Status is the LLM block.
func (a *AuthorLLMAdmin) Status(ctx context.Context) (*AuthorLLMStatus, error) {
	st := &AuthorLLMStatus{Enabled: a.disabled == "", Reason: a.disabled,
		BudgetTokens: a.settings.Budget.OneOffTokens, PriceUnit: a.settings.Price.Unit}
	run, otherActive, err := database.AuthorLLMRunToShowOf(ctx, a.db, a.version, a.merge)
	if err != nil {
		return nil, err
	}
	st.OtherActive = otherActive
	active := otherActive || (run != nil && isActiveLLMRun(run.Status))
	if run != nil {
		if st.Run, err = a.runState(ctx, run); err != nil {
			return nil, err
		}
	}
	if st.SpentTokens, err = database.AuthorLLMTokensSpentAll(ctx, a.db); err != nil {
		return nil, err
	}
	completed := map[string]bool{}
	if st.Enabled {
		if completed, err = database.AuthorLLMCompletedRuns(ctx, a.db, a.version); err != nil {
			return nil, err
		}
	}
	if a.merge {
		st.Steps = authorLLMMergeSteps(st.Enabled, active, completed)
		if !st.Enabled {
			return st, nil
		}
		run, err := a.mergeRunStep(ctx, active, completed)
		if err != nil {
			return nil, err
		}
		st.Steps = append(st.Steps, run)
		return st, a.participants(ctx, st)
	}
	if st.Enabled {
		// The resolve pilot rests on the full and stage evals: evidence that
		// plans no stratum is no ground for it.
		registrable, regErr := a.evalRegistersAStratum(ctx)
		if regErr != nil {
			return nil, regErr
		}
		completed[authorLLMResolvable] = registrable
	}
	if completed[string(models.AuthorLLMRunEval)+"/"+models.AuthorLLMEvalModeSwap] {
		// Likewise the swap pilot rests on the swap eval's report.
		registrable, regErr := a.swapEvalRegistersAStratum(ctx)
		if regErr != nil {
			return nil, regErr
		}
		completed[string(models.AuthorLLMRunEval)+"/"+models.AuthorLLMEvalModeSwap] = registrable
	}
	st.Steps = authorLLMSteps(st.Enabled, active, completed)
	if !st.Enabled {
		return st, nil
	}
	if err := a.stageEstimates(ctx, st.Steps); err != nil {
		return nil, err
	}
	if err := a.participants(ctx, st); err != nil {
		return nil, err
	}
	return st, nil
}

// authorLLMSteps is the chain as the card draws it: each step enabled only
// when the layer is on, the step is built, the step before it completed and
// no run is active.
func authorLLMSteps(enabled, active bool, completed map[string]bool) []AuthorLLMStep {
	steps := make([]AuthorLLMStep, 0, len(authorLLMChain))
	for _, step := range authorLLMChain {
		s := AuthorLLMStep{Key: step.key, Kind: step.kind, Mode: step.mode, Available: step.available,
			Done: completed[step.kind+"/"+step.mode], Strata: AuthorLLMEvalStageStrata(step.mode)}
		switch {
		case !enabled:
			s.Blocked = AuthorLLMStepNotConfigured
		case !step.available:
			s.Blocked = AuthorLLMStepNotAvailable
		case len(step.after) > 0 && !slices.ContainsFunc(step.after, func(k string) bool { return completed[k] }):
			s.Blocked = AuthorLLMStepPrerequisite
		case active:
			s.Blocked = AuthorLLMStepActiveRun
		default:
			s.Enabled = true
		}
		steps = append(steps, s)
	}
	return steps
}

// authorLLMMergeSteps is the merge configuration's chain: the merge pilot
// and the eval of each kind, each enabled when the layer is on and no run
// is active. Their order is the budget's (§7), an operator's decision: no
// step needs another's data.
func authorLLMMergeSteps(enabled, active bool, completed map[string]bool) []AuthorLLMStep {
	steps := make([]AuthorLLMStep, 0, len(mergeEvalModes))
	for _, mode := range []string{AuthorLLMStepMerge0, AuthorLLMStepMerge1, AuthorLLMStepMerge2, AuthorLLMStepMerge3,
		AuthorLLMStepMerge4} {
		s := AuthorLLMStep{Key: mode, Kind: string(models.AuthorLLMRunEval), Mode: mode, Available: true,
			Done: completed[string(models.AuthorLLMRunEval)+"/"+mode]}
		switch {
		case !enabled:
			s.Blocked = AuthorLLMStepNotConfigured
		case active:
			s.Blocked = AuthorLLMStepActiveRun
		default:
			s.Enabled = true
		}
		steps = append(steps, s)
	}
	return steps
}

// mergeRunStep is the merge run's step: enabled when a completed merge eval
// gives it evidence, a kind is registered and not withdrawn, a candidate is
// due and no run is active; its estimate is the run's over the candidates
// due now.
func (a *AuthorLLMAdmin) mergeRunStep(ctx context.Context, active bool, completed map[string]bool) (AuthorLLMStep, error) {
	s := AuthorLLMStep{Key: AuthorLLMStepMergeRun, Kind: string(models.AuthorLLMRunMerge),
		Mode: models.AuthorLLMEvalModeFull, Available: true,
		Done: completed[string(models.AuthorLLMRunMerge)+"/"+models.AuthorLLMEvalModeFull]}
	ev, err := database.LatestAuthorLLMMergeEval(ctx, a.db, a.version)
	if err != nil {
		return s, err
	}
	registrations, err := liveMergeRegistrations(ctx, a.db, a.version)
	if err != nil {
		return s, err
	}
	due, err := database.MergeCandidatesDue(ctx, a.db, a.version)
	if err != nil {
		return s, err
	}
	if len(due) > 0 {
		s.EstimateTokens = mergeRunEstimate(len(due))
	}
	switch {
	case ev == nil || len(registrations) == 0 || len(due) == 0:
		s.Blocked = AuthorLLMStepPrerequisite
	case active:
		s.Blocked = AuthorLLMStepActiveRun
	default:
		s.Enabled = true
	}
	return s, nil
}

// stageEstimates fills each stage eval step's token estimate.
func (a *AuthorLLMAdmin) stageEstimates(ctx context.Context, steps []AuthorLLMStep) error {
	for i := range steps {
		if !models.IsAuthorLLMEvalStageMode(steps[i].Mode) || steps[i].Kind != string(models.AuthorLLMRunEval) {
			continue
		}
		est, err := EstimateAuthorLLMEvalStage(ctx, a.db, steps[i].Mode, DefaultAuthorLLMEvalSizes())
		if err != nil {
			return err
		}
		steps[i].EstimateTokens = est.Tokens
	}
	return nil
}

// participants fills the participants and the endpoint's pause.
func (a *AuthorLLMAdmin) participants(ctx context.Context, st *AuthorLLMStatus) error {
	states, err := database.AuthorLLMProviderStates(ctx, a.db, a.version)
	if err != nil {
		return err
	}
	paused := map[string]string{}
	for i := range states {
		paused[states[i].Slot] = deref(states[i].PausedReason)
	}
	for _, p := range authorLLMParticipants(&a.settings) {
		st.Participants = append(st.Participants, AuthorLLMParticipantState{
			Slot: string(p.Slot), Model: p.Model, PausedReason: paused[string(p.Slot)]})
	}
	endpoint, err := database.GetAuthorLLMEndpointState(ctx, a.db, a.version)
	if errors.Is(err, pg.ErrNoRows) {
		return nil // a configuration no worker has run yet has no endpoint row
	}
	if err != nil {
		return err
	}
	st.EndpointPausedReason = deref(endpoint.PausedReason)
	return nil
}

func isActiveLLMRun(status models.AuthorLLMRunStatus) bool {
	return status == models.AuthorLLMRunPending || status == models.AuthorLLMRunRunning || status == models.AuthorLLMRunPaused
}

// authorLLMStagePurpose is the job purpose an eval stage waits for.
var authorLLMStagePurpose = map[string]models.AuthorLLMJobPurpose{
	evalStageFingerprint: models.AuthorLLMPurposeEvalFingerprint,
	evalStageAnchor:      models.AuthorLLMPurposeEvalAnchor,
	evalStageJudge:       models.AuthorLLMPurposeEvalJudge,
	evalStageCandidates:  models.AuthorLLMPurposeEvalCandidate,
	evalStageRepeat:      models.AuthorLLMPurposeEvalRepeat,
	resolveStageResolve:  models.AuthorLLMPurposeReview,
}

// runState is a run as the card shows it.
func (a *AuthorLLMAdmin) runState(ctx context.Context, run *models.AuthorLLMRun) (*AuthorLLMRunState, error) {
	rs := &AuthorLLMRunState{ID: run.ID, Kind: string(run.Kind), Mode: run.Mode, Status: string(run.Status),
		Stage:        models.AuthorLLMEvalStageOf(run.Mode, deref(run.Stage)),
		PausedReason: deref(run.PausedReason), LastErrorClass: deref(run.LastErrorClass),
		CurrentConfig: run.ConfigVersion == a.version, EstimateTokens: run.EstimateTokens,
		LimitTokens: int64(float64(run.EstimateTokens) * a.settings.Budget.RunOverrun)}
	purpose, ok := authorLLMStagePurpose[rs.Stage]
	if run.Kind == models.AuthorLLMRunSwap && rs.Stage == resolveStageResolve {
		purpose = models.AuthorLLMPurposeSwapCheck
	}
	if run.Kind == models.AuthorLLMRunMerge && rs.Stage == resolveStageResolve {
		purpose = models.AuthorLLMPurposeMergeCheck
	}
	if ok && isActiveLLMRun(run.Status) {
		done, total, err := database.AuthorLLMJobProgress(ctx, a.db, run.ID, purpose)
		if err != nil {
			return nil, err
		}
		rs.Progress = &AuthorLLMProgress{Done: done, Total: total}
	}
	spent, err := database.AuthorLLMTokensSpent(ctx, a.db, run.ID)
	if err != nil {
		return nil, err
	}
	rs.SpentTokens = spent
	if a.settings.Price.Unit != "" {
		bySlot, slotErr := database.AuthorLLMTokensBySlot(ctx, a.db, run.ID)
		if slotErr != nil {
			return nil, slotErr
		}
		price := map[string]float64{
			string(llmreq.SlotProductionA): a.settings.Price.ProductionA,
			string(llmreq.SlotProductionB): a.settings.Price.ProductionB,
			string(llmreq.SlotJudgeA):      a.settings.Price.JudgeA,
			string(llmreq.SlotJudgeB):      a.settings.Price.JudgeB,
		}
		cost := 0.0
		for slot, tokens := range bySlot {
			cost += float64(tokens) * price[slot] / authorLLMPriceTokens
		}
		rs.Cost = &cost
	}
	if run.Kind == models.AuthorLLMRunEval && run.Status == models.AuthorLLMRunCompleted {
		if rs.Report, err = a.reportSummary(ctx, run.ID); err != nil {
			return nil, err
		}
	}
	if (run.Kind == models.AuthorLLMRunResolve || run.Kind == models.AuthorLLMRunSwap ||
		run.Kind == models.AuthorLLMRunMerge) && run.Status == models.AuthorLLMRunCompleted {
		if rs.Resolve, err = resolveSummary(ctx, a.db, run.ID); err != nil {
			return nil, err
		}
	}
	return rs, nil
}

// authorLLMPriceTokens is the token count a price is quoted for.
const authorLLMPriceTokens = 1_000_000

// reportSummary reads the one line of a stored eval report; nil when the
// run has none.
func (a *AuthorLLMAdmin) reportSummary(ctx context.Context, runID int64) (*AuthorLLMReportSummary, error) {
	raw, sha, err := AuthorLLMEvalReportFile(ctx, a.db, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sum, err := summarizeEvalReport(raw, sha)
	if err != nil {
		return nil, err
	}
	versions, err := database.AuthorLLMEvalReportVersions(ctx, a.db, runID)
	if err != nil {
		return nil, err
	}
	sum.Versions = make([]AuthorLLMReportVersion, 0, len(versions))
	for _, v := range versions {
		sum.Version = v.Version
		sum.Versions = append(sum.Versions, AuthorLLMReportVersion{Version: v.Version,
			SHA256: hex.EncodeToString(v.ReportSHA256), Registrable: v.Registrable, CreatedAt: v.CreatedAt})
	}
	return sum, nil
}

// summarizeEvalReport is the card's line of a stored report. The repeat
// change comes from the repeat counts, which every stored report carries.
func summarizeEvalReport(raw []byte, sha string) (*AuthorLLMReportSummary, error) {
	var doc struct {
		Evaluation llmeval.Report `json:"evaluation"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("reading an eval report: %w", err)
	}
	sum := &AuthorLLMReportSummary{SHA256: sha, Registrable: doc.Evaluation.Decision.Registrable,
		StrataTotal: len(doc.Evaluation.Decision.Strata), Repeats: []AuthorLLMRepeatChange{}}
	for _, c := range llmeval.RepeatChanges(doc.Evaluation.Repeat) {
		sum.Repeats = append(sum.Repeats, AuthorLLMRepeatChange{Participant: c.Participant, Changed: c.Changed, Total: c.Total})
	}
	for _, d := range doc.Evaluation.Decision.Strata {
		if d.Passed {
			sum.StrataPassed++
			if d.Arm == llmeval.ArmB {
				sum.ExcerptHelps++
			}
		}
	}
	return sum, nil
}

// Start starts a run of a step's kind and mode; it returns at once, the
// stages run in the background. Dev evals, which the card does not show,
// need no prerequisite.
func (a *AuthorLLMAdmin) Start(ctx context.Context, kind, mode string, actor int64) (*AuthorLLMRunState, error) {
	if a.disabled != "" {
		return nil, ErrAuthorLLMNotConfigured
	}
	if a.merge {
		// The merge configuration starts its evals and its merge run.
		if kind == string(models.AuthorLLMRunMerge) {
			id, err := StartAuthorLLMMergeRun(ctx, a.db, a.version, mode, actor)
			if err != nil {
				return nil, err
			}
			return a.get(ctx, id)
		}
		if kind != string(models.AuthorLLMRunEval) {
			return nil, ErrAuthorLLMInvalidRun
		}
		id, err := StartAuthorLLMMergeEval(ctx, a.db, a.version, mode, &actor)
		if err != nil {
			return nil, err
		}
		return a.get(ctx, id)
	}
	switch kind {
	case string(models.AuthorLLMRunSwap):
		return a.startProduction(ctx, models.AuthorLLMRunSwap, mode, actor)
	case string(models.AuthorLLMRunResolve):
		return a.startProduction(ctx, models.AuthorLLMRunResolve, mode, actor)
	case string(models.AuthorLLMRunEval):
	default:
		return nil, ErrAuthorLLMInvalidRun
	}
	switch mode {
	case models.AuthorLLMEvalModeDev, models.AuthorLLMEvalModePilot:
	case models.AuthorLLMEvalModeFull, models.AuthorLLMEvalModeSwap, models.AuthorLLMEvalModeStage0,
		models.AuthorLLMEvalModeStage1, models.AuthorLLMEvalModeStage2, models.AuthorLLMEvalModeStage3:
		// The full and the stage evals follow the eval pilot; the swap eval
		// follows the full resolve, on either route — the card's chain.
		before := evalPilotDone
		if mode == models.AuthorLLMEvalModeSwap {
			before = string(models.AuthorLLMRunResolve) + "/" + models.AuthorLLMEvalModeFull
		}
		completed, err := database.AuthorLLMCompletedRuns(ctx, a.db, a.version)
		if err != nil {
			return nil, err
		}
		if !completed[before] {
			return nil, ErrAuthorLLMPrerequisite
		}
	default:
		return nil, ErrAuthorLLMInvalidRun
	}
	id, err := StartAuthorLLMEval(ctx, a.db, a.version, mode, nil, &actor)
	if err != nil {
		return nil, err
	}
	return a.get(ctx, id)
}

// evalRegistersAStratum reports whether the configuration's full and stage
// evals register a stratum: whether a resolve run would plan one.
func (a *AuthorLLMAdmin) evalRegistersAStratum(ctx context.Context) (bool, error) {
	_, plan, err := productionPlan(ctx, a.db, models.AuthorLLMRunResolve, a.version)
	if err != nil {
		return false, err
	}
	return len(plan.Strata) > 0, nil
}

// swapEvalRegistersAStratum reports whether the latest swap eval's report
// registers a swap stratum of the configuration.
func (a *AuthorLLMAdmin) swapEvalRegistersAStratum(ctx context.Context) (bool, error) {
	ev, err := database.LatestAuthorLLMSwapEval(ctx, a.db, a.version)
	if err != nil || ev == nil {
		return false, err
	}
	plan, err := PlanAuthorLLMSwap(ev.Report)
	if err != nil {
		return false, err
	}
	return len(plan.Strata) > 0, nil
}

// startProduction starts a resolve or a swap run: the pilot on the eval
// whose report registers a stratum of its kind (the full eval, the swap
// eval), the full run after a completed pilot of the same kind.
func (a *AuthorLLMAdmin) startProduction(
	ctx context.Context, kind models.AuthorLLMRunKind, mode string, actor int64,
) (*AuthorLLMRunState, error) {
	switch mode {
	case models.AuthorLLMEvalModePilot:
	case models.AuthorLLMEvalModeFull:
		completed, err := database.AuthorLLMCompletedRuns(ctx, a.db, a.version)
		if err != nil {
			return nil, err
		}
		if !completed[string(kind)+"/"+models.AuthorLLMEvalModePilot] {
			return nil, ErrAuthorLLMPrerequisite
		}
	default:
		return nil, ErrAuthorLLMInvalidRun
	}
	id, err := startAuthorLLMProduction(ctx, a.db, kind, a.version, mode, actor)
	if err != nil {
		return nil, err
	}
	return a.get(ctx, id)
}

// resolveSummary reads the card's line of a resolve run's report; nil while
// it has none.
func resolveSummary(ctx context.Context, db pg.DBI, runID int64) (*AuthorLLMResolveSummary, error) {
	raw, sha, err := AuthorLLMResolveReportFile(ctx, db, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc AuthorLLMResolveReportDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("reading a resolve report: %w", err)
	}
	sum := &AuthorLLMResolveSummary{SHA256: sha}
	for _, line := range doc.Strata {
		sum.Applied += line.Applied
		sum.Total += line.Sent
	}
	return sum, nil
}

func (a *AuthorLLMAdmin) get(ctx context.Context, id int64) (*AuthorLLMRunState, error) {
	run, err := database.GetAuthorLLMRunByID(ctx, a.db, id)
	if err != nil {
		return nil, err
	}
	return a.runState(ctx, run)
}

// Pause pauses a running run (of any configuration: the active one is
// always the card's to pause).
func (a *AuthorLLMAdmin) Pause(ctx context.Context, id int64) (*AuthorLLMRunState, error) {
	if _, err := database.GetAuthorLLMRunByID(ctx, a.db, id); err != nil {
		return nil, err
	}
	if err := database.PauseAuthorLLMRun(ctx, a.db, id, models.AuthorLLMRunPausedAdmin); err != nil {
		return nil, err
	}
	return a.get(ctx, id)
}

// Resume resumes a paused run of the current configuration. A run of an
// older one is refused: no worker of the current configuration would move
// it, so a "resumed" run would hold the active slot forever. It can be
// closed instead.
func (a *AuthorLLMAdmin) Resume(ctx context.Context, id int64) (*AuthorLLMRunState, error) {
	run, err := database.GetAuthorLLMRunByID(ctx, a.db, id)
	if err != nil {
		return nil, err
	}
	if run.ConfigVersion != a.version {
		return nil, ErrAuthorLLMRunConfigChanged
	}
	if err := database.ResumeAuthorLLMRun(ctx, a.db, id); err != nil {
		return nil, err
	}
	return a.get(ctx, id)
}

// ResumeParticipant lifts a participant's pause.
func (a *AuthorLLMAdmin) ResumeParticipant(ctx context.Context, slot string) error {
	if a.disabled != "" {
		return ErrAuthorLLMNotConfigured
	}
	if !llmreq.Slot(slot).Valid() {
		return ErrAuthorLLMInvalidSlot
	}
	reasoning := false
	for _, p := range authorLLMParticipants(&a.settings) {
		if p.Slot == llmreq.Slot(slot) {
			reasoning = p.ReasoningEffort != ""
		}
	}
	started, err := database.ResumeAuthorLLMParticipantRelearning(ctx, a.db, &database.AuthorLLMResume{
		ConfigVersion: a.version, Slot: llmreq.Slot(slot), Repeats: DefaultAuthorLLMEvalSizes().FingerprintRepeats,
		Reasoning: reasoning})
	if err != nil {
		return err
	}
	if started {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{Name: AuthorMetadataEventLLMRelearnStarted,
			Stage: AuthorMetadataStageRunner})
	}
	return nil
}

// ResumeEndpoint lifts the endpoint's pause.
func (a *AuthorLLMAdmin) ResumeEndpoint(ctx context.Context) error {
	if a.disabled != "" {
		return ErrAuthorLLMNotConfigured
	}
	return database.ResumeAuthorLLMEndpoint(ctx, a.db, a.version)
}

// EvalReport is a stored report and its SHA-256, as downloaded: an eval
// run's given version (0: the latest), or a resolve run's report.
func (a *AuthorLLMAdmin) EvalReport(ctx context.Context, id int64, version int) (report []byte, sha string, err error) {
	run, err := database.GetAuthorLLMRunByID(ctx, a.db, id)
	if err != nil {
		return nil, "", err
	}
	if run.Kind == models.AuthorLLMRunResolve || run.Kind == models.AuthorLLMRunSwap ||
		run.Kind == models.AuthorLLMRunMerge {
		report, sha, err = AuthorLLMResolveReportFile(ctx, a.db, id)
	} else {
		report, sha, err = AuthorLLMEvalReportVersionFile(ctx, a.db, id, version)
	}
	if errors.Is(err, pg.ErrNoRows) {
		return nil, "", ErrAuthorLLMReportNotFound
	}
	return report, sha, err
}

// RecomputeEvalReport computes a completed eval run's report of the
// current configuration again from its stored answers with the current gate
// code and stores it as the next version, unless it says what the latest
// says (RecomputeAuthorLLMEvalReport). It returns the run as the card shows
// it.
func (a *AuthorLLMAdmin) RecomputeEvalReport(ctx context.Context, id int64) (*AuthorLLMRunState, error) {
	if a.disabled != "" {
		return nil, ErrAuthorLLMNotConfigured
	}
	var stored bool
	var err error
	if a.merge {
		_, stored, err = RecomputeAuthorLLMMergeEvalReport(ctx, a.db, a.identity, a.version, id)
	} else {
		_, stored, err = RecomputeAuthorLLMEvalReport(ctx, a.db, a.version, id)
	}
	if errors.Is(err, pg.ErrNoRows) {
		return nil, ErrAuthorLLMReportNotFound
	}
	if err != nil {
		return nil, err
	}
	if stored {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventLLMReportRecomputed, Stage: AuthorMetadataStageRunner, RunID: id})
	}
	return a.get(ctx, id)
}

// Close ends a run for good: one of an older configuration, or a paused
// one. Its charged tokens stay, its open calls are fenced, and the active
// slot is free for the next step.
func (a *AuthorLLMAdmin) Close(ctx context.Context, id, actor int64) (*AuthorLLMRunState, error) {
	var by *int64
	if actor > 0 {
		by = &actor
	}
	if err := database.CloseAuthorLLMRun(ctx, a.db, id, a.version, by); err != nil {
		return nil, err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventLLMRunClosed, Stage: AuthorMetadataStageRunner, RunID: id})
	return a.get(ctx, id)
}

// AdversarialItems are a merge eval run's accepted suite items to
// adjudicate; the name normalization's admin has none.
func (a *AuthorLLMAdmin) AdversarialItems(ctx context.Context, id int64) (*AuthorLLMAdversarialReview, error) {
	if !a.merge {
		return nil, ErrAuthorLLMInvalidRun
	}
	return AuthorLLMAdversarialItems(ctx, a.db, id)
}

// ContestedItems are a merge eval run's contested accepts to adjudicate;
// the name normalization's admin has none.
func (a *AuthorLLMAdmin) ContestedItems(ctx context.Context, id int64) (*AuthorLLMAdversarialReview, error) {
	if !a.merge {
		return nil, ErrAuthorLLMInvalidRun
	}
	return AuthorLLMContestedItems(ctx, a.db, id)
}

// Adjudicate labels an accepted suite item (merge configuration only).
func (a *AuthorLLMAdmin) Adjudicate(ctx context.Context, itemID int64, label, note, by string) error {
	if !a.merge {
		return ErrAuthorLLMInvalidRun
	}
	return AdjudicateAuthorLLMEvalItem(ctx, a.db, itemID, label, note, by)
}
