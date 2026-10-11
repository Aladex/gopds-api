package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The resolve run (design K, phase 6). Its start registers the pair of every
// stratum the latest full eval report passed (decision R1, option «в»: in the
// start's transaction). Its driver then asks the production pair about the
// open review inputs of those strata — the pilot a hash-ordered sample, the
// full run the rest — the settle asks both judges about every answer the
// pair agrees on, and the driver applies each confirmed verdict to the
// input's credits. An answer is applied only when the pair agrees and both
// judges confirm it (the run-4 rule), in a registered stratum, class, script
// and tier; everything else ends unresolved with a closed reason. Either way
// the review item is closed by the service. A manual decision is never
// overwritten: the resolver's precedence keeps overrides and admin decisions.

// AuthorLLMResolveSizes are the resolve run's sizes.
type AuthorLLMResolveSizes struct {
	// Pilot is how many inputs the pilot asks about.
	Pilot int
}

// defaultResolvePilot is the design's pilot: 500 inputs.
const defaultResolvePilot = 500

// DefaultAuthorLLMResolveSizes are the design's sizes.
func DefaultAuthorLLMResolveSizes() AuthorLLMResolveSizes {
	return AuthorLLMResolveSizes{Pilot: defaultResolvePilot}
}

// The resolve run's stages.
const (
	resolveStageEnqueue = "enqueue"
	resolveStageResolve = "resolve"
	resolveStageReport  = "report"
)

// resolveSkipStage is the skip record's stage of an input the resolve run
// cannot send.
const resolveSkipStage = "enqueue"

// resolveEnqueueChunk is how many inputs one insert enqueues.
const resolveEnqueueChunk = 500

// resolveApplyBatch is how many verdicts one step applies.
const resolveApplyBatch = 200

// PlanAuthorLLMResolve reads a stored eval report into a resolve run's plan:
// every stratum the report passed whose pair can be registered (its class is
// an ambiguous local class and its script a script of its own), and the
// passed strata that cannot (homoglyph, or no single script). A report that
// is not registrable plans nothing.
func PlanAuthorLLMResolve(report []byte) (database.AuthorLLMResolveSettings, error) {
	var doc struct {
		Evaluation llmeval.Report `json:"evaluation"`
	}
	plan := database.AuthorLLMResolveSettings{Strata: map[string]database.AuthorLLMResolveStratum{},
		NotRegistrable: []string{}}
	if err := json.Unmarshal(report, &doc); err != nil {
		return plan, fmt.Errorf("reading an eval report: %w", err)
	}
	if !doc.Evaluation.Decision.Registrable {
		return plan, nil
	}
	for _, d := range doc.Evaluation.Decision.Strata {
		if !d.Passed {
			continue
		}
		class, script, ok := registrablePair(d.Stratum)
		if !ok {
			plan.NotRegistrable = append(plan.NotRegistrable, d.Stratum)
			continue
		}
		combos := doc.Evaluation.Strata[d.Stratum].Combos
		plan.Strata[d.Stratum] = database.AuthorLLMResolveStratum{Class: string(class), Script: script, Arm: d.Arm,
			ItemsPerRequest: max(d.ItemsPerRequest, 1), Tiers: d.Tiers,
			ArmAPassed: combos[llmeval.ArmA+strconv.Itoa(max(d.ItemsPerRequest, 1))].Passed}
	}
	slices.Sort(plan.NotRegistrable)
	return plan, nil
}

// registrablePair splits a stratum "<class>.<script>" into the local class
// and the ISO 15924 script its pair is registered for.
func registrablePair(stratum string) (authornorm.DecisionClass, string, bool) {
	cut := strings.LastIndexByte(stratum, '.')
	if cut <= 0 {
		return "", "", false
	}
	class := authornorm.DecisionClass(stratum[:cut])
	raw := stratum[cut+1:]
	if raw == "" {
		return "", "", false
	}
	runes := []rune(raw)
	script := string(unicode.ToUpper(runes[0])) + strings.ToLower(string(runes[1:]))
	if s := authornorm.Script(script); s == authornorm.ScriptMixed || s.Validate() != nil {
		return "", "", false
	}
	if authornorm.LLMClass(class).Validate() != nil {
		return "", "", false
	}
	return class, script, true
}

// StartAuthorLLMResolve starts a resolve run of the configuration on its
// latest full eval: the registration of its passed strata happens in the
// same transaction. Without a completed full eval whose report registers a
// stratum it refuses with ErrAuthorLLMPrerequisite.
func StartAuthorLLMResolve(ctx context.Context, db *pg.DB, version, mode string, admin int64) (int64, error) {
	ev, err := database.LatestAuthorLLMFullEval(ctx, db, version)
	if err != nil {
		return 0, err
	}
	if ev == nil {
		return 0, ErrAuthorLLMPrerequisite
	}
	plan, err := PlanAuthorLLMResolve(ev.Report)
	if err != nil {
		return 0, err
	}
	if len(plan.Strata) == 0 {
		return 0, ErrAuthorLLMPrerequisite
	}
	plan.ReportID, plan.ReportSHA256 = ev.ReportID, hex.EncodeToString(ev.ReportSHA256)
	id, registered, err := database.StartAuthorLLMResolveRun(ctx, db, &database.AuthorLLMResolveStart{
		ConfigVersion: version, Mode: mode, Evidence: ev, Settings: plan, CreatedBy: admin})
	if errors.Is(err, database.ErrAuthorLLMNoEvidence) || errors.Is(err, database.ErrAuthorLLMRunEvidence) {
		return 0, ErrAuthorLLMPrerequisite
	}
	if err != nil {
		return 0, err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{Name: AuthorMetadataEventLLMResolveStarted,
		Stage: AuthorMetadataStageRunner, RunID: id, Count: registered, Worker: authorLLMResolveDriverName})
	return id, nil
}

// AuthorLLMResolveDriver moves the configuration's running resolve run
// through its stages.
type AuthorLLMResolveDriver struct {
	db       *pg.DB
	identity llmreq.Identity
	version  string
	sizes    AuthorLLMResolveSizes
	poll     time.Duration
}

// NewAuthorLLMResolveDriver builds the driver of a configuration.
func NewAuthorLLMResolveDriver(
	db *pg.DB, identity *llmreq.Identity, version string, sizes AuthorLLMResolveSizes, poll time.Duration,
) *AuthorLLMResolveDriver {
	if poll <= 0 {
		poll = authorLLMPollInterval
	}
	return &AuthorLLMResolveDriver{db: db, identity: *identity, version: version, sizes: sizes, poll: poll}
}

// ResolveDriver is the resolve driver of the worker's configuration, with
// the design's sizes.
func (w *AuthorLLMWorker) ResolveDriver() *AuthorLLMResolveDriver {
	return NewAuthorLLMResolveDriver(w.db, &w.cfg.Identity, w.cfg.ConfigVersion, DefaultAuthorLLMResolveSizes(),
		w.cfg.PollInterval)
}

// authorLLMResolveDriverName is the driver's log label.
const authorLLMResolveDriverName = "author_llm_resolve"

// Name is the driver's log label.
func (d *AuthorLLMResolveDriver) Name() string { return authorLLMResolveDriverName }

// Run steps the running resolve run until ctx is canceled.
func (d *AuthorLLMResolveDriver) Run(ctx context.Context) error {
	return pollLoop(ctx, authorLLMResolveDriverName, d.poll, d.Step)
}

// resolveRun is the running resolve run as the driver reads it.
type resolveRun struct {
	ID        int64
	Mode      string
	Stage     string
	Plan      database.AuthorLLMResolveSettings
	Reference map[string]database.AuthorLLMFingerprintReference
}

func (d *AuthorLLMResolveDriver) running(ctx context.Context) (*resolveRun, error) {
	row, err := database.RunningAuthorLLMResolveRun(ctx, d.db, d.version)
	if err != nil || row == nil {
		return nil, err
	}
	run := &resolveRun{ID: row.ID, Mode: row.Mode}
	if row.Stage != nil {
		run.Stage = *row.Stage
	}
	if err := json.Unmarshal(row.Settings, &run.Plan); err != nil {
		return nil, fmt.Errorf("reading a resolve run's plan: %w", err)
	}
	if len(row.Reference) > 0 {
		if err := json.Unmarshal(row.Reference, &run.Reference); err != nil {
			return nil, fmt.Errorf("reading a resolve run's reference: %w", err)
		}
	}
	return run, nil
}

// Step does the running resolve run's current stage as far as it can now
// and moves it on when the stage is done. It reports whether it moved a
// stage.
func (d *AuthorLLMResolveDriver) Step(ctx context.Context) (bool, error) {
	run, err := d.running(ctx)
	if err != nil || run == nil {
		return false, err
	}
	var next string
	switch run.Stage {
	case resolveStageEnqueue:
		if err = d.enqueue(ctx, run); err == nil {
			next = resolveStageResolve
		}
	case resolveStageResolve:
		next, err = d.resolve(ctx, run)
	case resolveStageReport:
		return false, d.report(ctx, run)
	default:
		return false, nil
	}
	if err != nil {
		if !stageImpossible(err) {
			return false, err
		}
		LogAuthorMetadataEvent(AuthorMetadataEventError, &AuthorMetadataEvent{
			Name: AuthorMetadataEventLLMEvalRunFailed, Stage: AuthorMetadataStageRunner, RunID: run.ID,
			Worker: authorLLMResolveDriverName, SQLState: AuthorMetadataSQLState(err)})
		if failErr := database.FailAuthorLLMRun(ctx, d.db, run.ID, authorLLMEvalStageInvalid); failErr != nil {
			return false, errors.Join(err, failErr)
		}
		return false, nil
	}
	if next == "" {
		return false, nil
	}
	return database.SetAuthorLLMRunStage(ctx, d.db, run.ID, run.Stage, next)
}

// enqueue asks the production pair about the open review inputs of the
// planned strata: the pilot about a hash-ordered sample, the full run about
// the rest. An input that cannot be sent is recorded as skipped; one row is
// never more than its own skip.
func (d *AuthorLLMResolveDriver) enqueue(ctx context.Context, run *resolveRun) error {
	rows, err := database.AuthorLLMEvalPopulation(ctx, d.db)
	if err != nil {
		return err
	}
	strata, skipped := classifyPopulation(rows)
	for i := range skipped {
		skipped[i].Stage = resolveSkipStage
		if err := database.RecordAuthorLLMEvalSkip(ctx, d.db, run.ID, &skipped[i]); err != nil {
			return err
		}
	}
	var items []populationItem
	for name, pop := range strata {
		if _, planned := run.Plan.Strata[name]; planned {
			items = append(items, pop...)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return string(evalOrder(items[i].row.SourceFingerprint)) < string(evalOrder(items[j].row.SourceFingerprint))
	})
	if run.Mode == models.AuthorLLMEvalModePilot && len(items) > d.sizes.Pilot {
		items = items[:d.sizes.Pilot]
	}
	for start := 0; start < len(items); start += resolveEnqueueChunk {
		chunk := items[start:min(start+resolveEnqueueChunk, len(items))]
		pair := []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB}
		jobs := make([]database.AuthorLLMNewJob, 0, len(pair)*len(chunk))
		for i := range chunk {
			it := &chunk[i]
			plan := run.Plan.Strata[it.stratum]
			for _, slot := range pair {
				jobs = append(jobs, database.AuthorLLMNewJob{ConfigVersion: d.version, Slot: slot,
					Purpose: models.AuthorLLMPurposeReview, SourceFingerprint: it.row.SourceFingerprint,
					ExtractorVersion: it.row.ExtractorVersion, BatchSize: plan.ItemsPerRequest,
					OutputMode: run.mode(slot), Input: it.input, WithContext: plan.Arm == llmeval.ArmB})
			}
		}
		if _, err := database.EnqueueAuthorLLMReviewJobs(ctx, d.db, run.ID, jobs); err != nil {
			return err
		}
	}
	return nil
}

// mode is a participant's negotiated output mode, as the eval learned it.
func (r *resolveRun) mode(slot llmreq.Slot) string {
	if ref, ok := r.Reference[string(slot)]; ok && ref.OutputMode != "" {
		return ref.OutputMode
	}
	return database.AuthorLLMDefaultOutputMode
}

// resolve applies the run's confirmed verdicts; once nothing is left to
// answer, judge, confirm or apply, the stage is done.
func (d *AuthorLLMResolveDriver) resolve(ctx context.Context, run *resolveRun) (string, error) {
	verdicts, err := database.AuthorLLMVerdictsToApply(ctx, d.db, run.ID, resolveApplyBatch)
	if err != nil {
		return "", err
	}
	for i := range verdicts {
		if applyErr := d.applyIsolated(ctx, run, &verdicts[i]); applyErr != nil {
			return "", applyErr
		}
	}
	progress, err := database.AuthorLLMResolveRunProgress(ctx, d.db, run.ID)
	if err != nil {
		return "", err
	}
	if progress.Done() {
		return resolveStageReport, nil
	}
	return "", nil
}

// applyIsolated applies one verdict; a panic or an error the database raises
// for that input's data settles it as apply_failed, its credits left as they
// were, and the rest go on. Any other error (the connection) is returned and
// the verdict is tried again on the next step.
func (d *AuthorLLMResolveDriver) applyIsolated(ctx context.Context, run *resolveRun, v *database.AuthorLLMVerdictToApply) error {
	err := isolateItem(func() error { return d.apply(ctx, run, v) })
	if err == nil || (!errors.Is(err, errAuthorLLMItemPanic) && !stageImpossible(err)) {
		return err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{Name: AuthorMetadataEventLLMResolveApplyFailed,
		Stage: AuthorMetadataStageRunner, RunID: run.ID, ItemID: v.ID, Worker: authorLLMResolveDriverName,
		SQLState: AuthorMetadataSQLState(err)})
	reason := models.AuthorLLMApplyFailed
	return d.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, settleErr := database.SettleAuthorLLMVerdict(ctx, tx, v.ID, &database.AuthorLLMVerdictApplication{Reason: &reason})
		return settleErr
	})
}

// applyHook is called with each verdict's input before it is applied; a
// test makes it panic for one input.
var applyHook func(input *llmreq.ItemInput)

// apply decides one confirmed verdict and applies it to the input's credits,
// in one transaction under the input's lock: the selected LLM result or the
// unresolved reason through the resolver (which keeps every manual
// decision), the review item closed by the service, the verdict settled.
func (d *AuthorLLMResolveDriver) apply(ctx context.Context, run *resolveRun, v *database.AuthorLLMVerdictToApply) error {
	var input llmreq.ItemInput
	if err := json.Unmarshal(v.Input, &input); err != nil {
		return fmt.Errorf("reading a verdict's input: %w", err)
	}
	if applyHook != nil {
		applyHook(&input)
	}
	return d.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := database.LockNormalizationInput(ctx, tx, v.SourceFingerprint, v.ExtractorVersion); err != nil {
			return err
		}
		settlement, outcome, err := d.decide(ctx, tx, run, v, &input)
		if err != nil {
			return err
		}
		if outcome != nil {
			if _, err = database.ResolveCredits(ctx, tx, v.SourceFingerprint, v.ExtractorVersion, outcome); err != nil {
				return err
			}
		}
		if _, err = database.CloseAutomaticReviewItems(ctx, tx, v.SourceFingerprint, settlement.ResultID); err != nil {
			return err
		}
		_, err = database.SettleAuthorLLMVerdict(ctx, tx, v.ID, settlement)
		return err
	})
}

// decide is a verdict's application: the LLM result to select, or the
// reason the input stays unresolved, and the resolver's outcome for it (nil
// when the input has no local proposal to leave unresolved on).
func (d *AuthorLLMResolveDriver) decide(
	ctx context.Context, tx *pg.Tx, run *resolveRun, v *database.AuthorLLMVerdictToApply, input *llmreq.ItemInput,
) (*database.AuthorLLMVerdictApplication, *database.AutomaticOutcome, error) {
	stratum, base := verdictStratum(v, input)
	unresolved := func(reason models.UnresolvedReason) (*database.AuthorLLMVerdictApplication, *database.AutomaticOutcome, error) {
		r := string(reason)
		s := &database.AuthorLLMVerdictApplication{Reason: &r, Stratum: stratum}
		if v.ProposalResultID == nil {
			return s, nil, nil
		}
		return s, &database.AutomaticOutcome{ResultID: *v.ProposalResultID, DecisionClass: base,
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeUnresolved, Reason: authornorm.UnresolvedReason(r)}}, nil
	}
	if reason := verdictReason(v); reason != "" {
		return unresolved(reason)
	}
	plan, planned := run.Plan.Strata[stratum]
	if !planned || v.Tier == nil || !slices.Contains(plan.Tiers, *v.Tier) {
		return unresolved(models.UnresolvedLLMNotRegistered)
	}
	if plan.Arm == llmeval.ArmB && v.ContextState == string(models.AuthorLLMContextUnavailable) && !plan.ArmAPassed {
		return unresolved(models.UnresolvedLLMContextUnavailable)
	}
	result, ok := assembleVerdict(v, input, d.version, base)
	if !ok {
		return unresolved(models.UnresolvedLLMInvalid)
	}
	if string(result.Script) != plan.Script {
		return unresolved(models.UnresolvedLLMNotRegistered)
	}
	policy, err := database.AuthorLLMRegistration(ctx, tx, d.version, result.DecisionClass, plan.Script)
	if err != nil {
		return nil, nil, err
	}
	policyVersion, convErr := strconv.Atoi(policy)
	if policy == "" || convErr != nil {
		return unresolved(models.UnresolvedLLMNotRegistered)
	}
	id, err := database.InsertLLMResult(ctx, tx, &result)
	if err != nil {
		return nil, nil, err
	}
	return &database.AuthorLLMVerdictApplication{ResultID: &id, PolicyVersion: &policy, Stratum: stratum},
		&database.AutomaticOutcome{ResultID: id, DecisionClass: result.DecisionClass,
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeSelected, PolicyVersion: policyVersion}}, nil
}

// verdictStratum is the input's stratum, as the eval named it, and its local
// class.
func verdictStratum(v *database.AuthorLLMVerdictToApply, input *llmreq.ItemInput) (string, authornorm.DecisionClass) {
	if v.DecisionClass == nil {
		return "", ""
	}
	tokens, err := input.Tokens()
	if err != nil {
		return "", authornorm.DecisionClass(*v.DecisionClass)
	}
	row := database.AuthorLLMPopulationRow{DecisionClass: *v.DecisionClass, Script: v.ProposalScript}
	return stratumOf(&row, tokens), authornorm.DecisionClass(*v.DecisionClass)
}

// verdictReason is why a verdict cannot be applied whatever the plan: the
// pair did not agree on an answer, or the judges did not confirm it.
func verdictReason(v *database.AuthorLLMVerdictToApply) models.UnresolvedReason {
	switch models.AuthorLLMVerdictValue(v.Verdict) {
	case models.AuthorLLMVerdictDisagree:
		return models.UnresolvedLLMDisagreement
	case models.AuthorLLMVerdictRefused:
		return models.UnresolvedLLMRefused
	case models.AuthorLLMVerdictInvalid:
		return models.UnresolvedLLMInvalid
	case models.AuthorLLMVerdictFailed:
		return models.UnresolvedLLMFailed
	case models.AuthorLLMVerdictHeldDrift:
		// Never applied: AuthorLLMVerdictsToApply leaves them out.
		return models.UnresolvedLLMInvalid
	case models.AuthorLLMVerdictAgree:
	default:
		return models.UnresolvedLLMInvalid
	}
	if v.Judgement == nil {
		return models.UnresolvedLLMJudgeFailed
	}
	switch models.AuthorLLMJudgement(*v.Judgement) {
	case models.AuthorLLMJudgementConfirmed:
		return ""
	case models.AuthorLLMJudgementRefused:
		return models.UnresolvedLLMJudgeRefused
	case models.AuthorLLMJudgementInvalid:
		return models.UnresolvedLLMJudgeInvalid
	case models.AuthorLLMJudgementFailed:
		return models.UnresolvedLLMJudgeFailed
	case models.AuthorLLMJudgementDisagree:
	}
	return models.UnresolvedLLMJudgeDisagreement
}

// assembleVerdict builds the LLM result of the pair's agreed reply; ok is
// false when the stored reply does not assemble (it never should: it passed
// the validators when it was answered).
func assembleVerdict(
	v *database.AuthorLLMVerdictToApply, input *llmreq.ItemInput, version string, base authornorm.DecisionClass,
) (authornorm.Result, bool) {
	in, err := llmres.NewInput(input.Fields)
	if err != nil {
		return authornorm.Result{}, false
	}
	rep, err := llmres.ParseStoredReply(v.Reply)
	if err != nil {
		return authornorm.Result{}, false
	}
	out, err := llmres.Assemble(&in, &rep, v.ExtractorVersion, version, authornorm.LLMClass(base))
	if err != nil {
		return authornorm.Result{}, false
	}
	for _, f := range v.QualityFlags {
		out.Result.QualityFlags = append(out.Result.QualityFlags, authornorm.QualityFlag(f))
	}
	return out.Result, out.Result.Validate() == nil
}

// AuthorLLMResolveReportDoc is a resolve run's stored report: what it asked
// and applied per stratum, why the rest was not applied, the inputs it could
// not send, and the tokens per participant. Identifiers and closed values
// only — no name, no excerpt.
type AuthorLLMResolveReportDoc struct {
	RunID          int64                             `json:"run_id"`
	Mode           string                            `json:"mode"`
	ConfigVersion  string                            `json:"config_version"`
	EvalReportID   int64                             `json:"eval_report_id"`
	EvalReportSHA  string                            `json:"eval_report_sha256"`
	Strata         map[string]*AuthorLLMResolveCount `json:"strata"`
	NotRegistrable []string                          `json:"not_registrable"`
	Skipped        map[string]int                    `json:"skipped"`
	Tokens         map[string]int64                  `json:"tokens"`
}

// AuthorLLMResolveCount is one stratum's line of the report.
type AuthorLLMResolveCount struct {
	Sent       int            `json:"sent"`
	Applied    int            `json:"applied"`
	NotApplied map[string]int `json:"not_applied"`
}

// report writes the run's report and completes the run.
func (d *AuthorLLMResolveDriver) report(ctx context.Context, run *resolveRun) error {
	counts, err := database.AuthorLLMResolveCounts(ctx, d.db, run.ID)
	if err != nil {
		return err
	}
	doc := AuthorLLMResolveReportDoc{RunID: run.ID, Mode: run.Mode, ConfigVersion: d.version,
		EvalReportID: run.Plan.ReportID, EvalReportSHA: run.Plan.ReportSHA256,
		Strata: map[string]*AuthorLLMResolveCount{}, NotRegistrable: run.Plan.NotRegistrable, Skipped: map[string]int{}}
	for _, c := range counts {
		line, ok := doc.Strata[c.Stratum]
		if !ok {
			line = &AuthorLLMResolveCount{NotApplied: map[string]int{}}
			doc.Strata[c.Stratum] = line
		}
		line.Sent += c.Count
		if c.Applied {
			line.Applied += c.Count
		} else {
			line.NotApplied[deref(c.Reason)] += c.Count
		}
	}
	skips, err := database.AuthorLLMEvalSkips(ctx, d.db, run.ID, nil)
	if err != nil {
		return err
	}
	for _, s := range skips {
		doc.Skipped[s.Reason]++
	}
	if doc.Tokens, err = database.AuthorLLMTokensBySlot(ctx, d.db, run.ID); err != nil {
		return err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return database.StoreAuthorLLMResolveReport(ctx, d.db, run.ID, raw)
}

// AuthorLLMResolveReportFile is a resolve run's stored report and the hex
// digest of exactly those bytes.
func AuthorLLMResolveReportFile(ctx context.Context, db pg.DBI, runID int64) (report []byte, sha string, err error) {
	r, err := database.GetAuthorLLMResolveReport(ctx, db, runID)
	if err != nil {
		return nil, "", err
	}
	return []byte(r.Report), hex.EncodeToString(r.ReportSHA256), nil
}

// llmOutcomeOf is the resolver's outcome of an input the LLM layer decided:
// its selected result, or unresolved on the local proposal with the LLM's
// reason. nil when the stored decision cannot be read back.
func llmOutcomeOf(o *database.AuthorLLMOutcome, local authornorm.DecisionClass, localResult int64) *database.AutomaticOutcome {
	if o.ResultID != nil {
		policy, err := strconv.Atoi(deref(o.PolicyVersion))
		if err != nil || o.DecisionClass == nil {
			return nil
		}
		return &database.AutomaticOutcome{ResultID: *o.ResultID, DecisionClass: authornorm.DecisionClass(*o.DecisionClass),
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeSelected, PolicyVersion: policy}}
	}
	if o.Reason == nil {
		return nil
	}
	return &database.AutomaticOutcome{ResultID: localResult, DecisionClass: local,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeUnresolved, Reason: authornorm.UnresolvedReason(*o.Reason)}}
}
