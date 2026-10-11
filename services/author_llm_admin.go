package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
// the current configuration. Resolve and swap runs are not built yet: their
// steps are shown, never enabled, and refused.

// Errors of the LLM admin; each maps to one closed API code.
var (
	// ErrAuthorLLMNotConfigured: the layer is switched off (no endpoint,
	// invalid participants, or the author-metadata workers are off).
	ErrAuthorLLMNotConfigured = errors.New("services: the LLM layer is not configured")
	// ErrAuthorLLMPrerequisite: the step before this one has not completed
	// under the current configuration.
	ErrAuthorLLMPrerequisite = errors.New("services: the previous LLM step has not completed")
	// ErrAuthorLLMRunKindUnavailable: resolve and swap runs are not built yet.
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
	AuthorLLMStepEvalPilot    = "eval_pilot"
	AuthorLLMStepEvalFull     = "eval_full"
	AuthorLLMStepResolvePilot = "resolve_pilot"
	AuthorLLMStepResolveFull  = "resolve_full"
	AuthorLLMStepSwap         = "swap"
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
	Reason               string                      `json:"reason,omitempty"`
	Run                  *AuthorLLMRunState          `json:"run"`
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
	if !c.Enabled {
		return NewAuthorLLMAdmin(db, "", AuthorLLMDisabledWorkersOff, &c.LLM), nil
	}
	status := c.LLM.Status(shared)
	if !status.Enabled {
		return NewAuthorLLMAdmin(db, "", status.Reason, &c.LLM), nil
	}
	identity, err := llmreq.NewIdentity(shared.BaseURL, authorLLMParticipants(&c.LLM), llmreq.OutputLimits{
		Field: c.LLM.OutputLimitField, PerCall: c.LLM.MaxOutput.PerCallTokens, PerItem: c.LLM.MaxOutput.PerItemTokens,
	})
	if err != nil {
		return NewAuthorLLMAdmin(db, "", config.AuthorLLMDisabledModels, &c.LLM), nil
	}
	version, err := database.EnsureAuthorLLMConfig(ctx, db, &identity, c.LLM.ConcurrencyStart)
	if err != nil {
		return nil, err
	}
	return NewAuthorLLMAdmin(db, version, "", &c.LLM), nil
}

// AuthorLLMDisabledWorkersOff: the author-metadata workers are switched off,
// so no LLM run would ever move.
const AuthorLLMDisabledWorkersOff = "workers_disabled"

// authorLLMChain is the chain's steps in order; a step's prerequisite is the
// step before it. Only the eval steps are built.
var authorLLMChain = []struct {
	key, kind, mode string
	available       bool
}{
	{AuthorLLMStepEvalPilot, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModePilot, true},
	{AuthorLLMStepEvalFull, string(models.AuthorLLMRunEval), models.AuthorLLMEvalModeFull, true},
	{AuthorLLMStepResolvePilot, string(models.AuthorLLMRunResolve), models.AuthorLLMEvalModePilot, false},
	{AuthorLLMStepResolveFull, string(models.AuthorLLMRunResolve), models.AuthorLLMEvalModeFull, false},
	{AuthorLLMStepSwap, string(models.AuthorLLMRunSwap), models.AuthorLLMEvalModeFull, false},
}

// Status is the LLM block.
func (a *AuthorLLMAdmin) Status(ctx context.Context) (*AuthorLLMStatus, error) {
	st := &AuthorLLMStatus{Enabled: a.disabled == "", Reason: a.disabled,
		BudgetTokens: a.settings.Budget.OneOffTokens, PriceUnit: a.settings.Price.Unit}
	run, err := database.AuthorLLMRunToShow(ctx, a.db, a.version)
	if err != nil {
		return nil, err
	}
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
	st.Steps = authorLLMSteps(st.Enabled, run != nil && isActiveLLMRun(run.Status), completed)
	if !st.Enabled {
		return st, nil
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
	for i, step := range authorLLMChain {
		s := AuthorLLMStep{Key: step.key, Kind: step.kind, Mode: step.mode, Available: step.available,
			Done: completed[step.kind+"/"+step.mode]}
		switch {
		case !enabled:
			s.Blocked = AuthorLLMStepNotConfigured
		case !step.available:
			s.Blocked = AuthorLLMStepNotAvailable
		case i > 0 && !completed[authorLLMChain[i-1].kind+"/"+authorLLMChain[i-1].mode]:
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
}

// runState is a run as the card shows it.
func (a *AuthorLLMAdmin) runState(ctx context.Context, run *models.AuthorLLMRun) (*AuthorLLMRunState, error) {
	rs := &AuthorLLMRunState{ID: run.ID, Kind: string(run.Kind), Mode: run.Mode, Status: string(run.Status),
		Stage: deref(run.Stage), PausedReason: deref(run.PausedReason), LastErrorClass: deref(run.LastErrorClass),
		CurrentConfig: run.ConfigVersion == a.version, EstimateTokens: run.EstimateTokens,
		LimitTokens: int64(float64(run.EstimateTokens) * a.settings.Budget.RunOverrun)}
	if purpose, ok := authorLLMStagePurpose[rs.Stage]; ok && isActiveLLMRun(run.Status) {
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
	return summarizeEvalReport(raw, sha)
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
	switch kind {
	case string(models.AuthorLLMRunResolve), string(models.AuthorLLMRunSwap):
		return nil, ErrAuthorLLMRunKindUnavailable
	case string(models.AuthorLLMRunEval):
	default:
		return nil, ErrAuthorLLMInvalidRun
	}
	switch mode {
	case models.AuthorLLMEvalModeDev, models.AuthorLLMEvalModePilot:
	case models.AuthorLLMEvalModeFull:
		completed, err := database.AuthorLLMCompletedRuns(ctx, a.db, a.version)
		if err != nil {
			return nil, err
		}
		if !completed[kind+"/"+models.AuthorLLMEvalModePilot] {
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
	return database.ResumeAuthorLLMParticipant(ctx, a.db, a.version, llmreq.Slot(slot))
}

// ResumeEndpoint lifts the endpoint's pause.
func (a *AuthorLLMAdmin) ResumeEndpoint(ctx context.Context) error {
	if a.disabled != "" {
		return ErrAuthorLLMNotConfigured
	}
	return database.ResumeAuthorLLMEndpoint(ctx, a.db, a.version)
}

// EvalReport is a stored eval report and its SHA-256, as downloaded.
func (a *AuthorLLMAdmin) EvalReport(ctx context.Context, id int64) (report []byte, sha string, err error) {
	if _, err = database.GetAuthorLLMRunByID(ctx, a.db, id); err != nil {
		return nil, "", err
	}
	report, sha, err = AuthorLLMEvalReportFile(ctx, a.db, id)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, "", ErrAuthorLLMReportNotFound
	}
	return report, sha, err
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
