package services

import (
	"bytes"
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
	return planAuthorLLMRun(report, models.AuthorLLMRunResolve)
}

// PlanAuthorLLMSwap reads a stored eval report of mode swap into a swap
// run's plan: its passed (swap, script) strata. A swap stratum the report
// measured and did not pass — swap.latn has too few candidates to reach the
// gate — is listed as not passed and never registered.
func PlanAuthorLLMSwap(report []byte) (database.AuthorLLMResolveSettings, error) {
	return planAuthorLLMRun(report, models.AuthorLLMRunSwap)
}

// planAuthorLLMRun plans a production run of kind from an eval report: a
// swap run only the swap strata, a resolve run every other.
func planAuthorLLMRun(report []byte, kind models.AuthorLLMRunKind) (database.AuthorLLMResolveSettings, error) {
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
		if isSwapStratum(d.Stratum) != (kind == models.AuthorLLMRunSwap) {
			continue
		}
		if !d.Passed {
			if kind == models.AuthorLLMRunSwap {
				plan.NotPassed = append(plan.NotPassed, d.Stratum)
			}
			continue
		}
		class, script, ok := registrablePair(d.Stratum)
		if !ok {
			plan.NotRegistrable = append(plan.NotRegistrable, d.Stratum)
			continue
		}
		batch := max(d.ItemsPerRequest, 1)
		armA := doc.Evaluation.Strata[d.Stratum].Combos[llmeval.ArmA+strconv.Itoa(batch)]
		plan.Strata[d.Stratum] = database.AuthorLLMResolveStratum{Class: string(class), Script: script, Arm: d.Arm,
			ItemsPerRequest: batch, Tiers: d.Tiers, ArmAPassed: armA.Passed, ArmATiers: armA.AllowedTiers}
	}
	slices.Sort(plan.NotRegistrable)
	slices.Sort(plan.NotPassed)
	return plan, nil
}

// isSwapStratum reports a (swap, script) stratum.
func isSwapStratum(stratum string) bool {
	return strings.HasPrefix(stratum, string(authornorm.ClassSwap)+".")
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
	if class != authornorm.ClassSwap && authornorm.LLMClass(class).Validate() != nil {
		return "", "", false
	}
	return class, script, true
}

// StartAuthorLLMResolve starts a resolve run of the configuration on its
// latest full eval: the registration of its passed strata happens in the
// same transaction. Without a completed full eval whose report registers a
// stratum it refuses with ErrAuthorLLMPrerequisite.
func StartAuthorLLMResolve(ctx context.Context, db *pg.DB, version, mode string, admin int64) (int64, error) {
	return startAuthorLLMProduction(ctx, db, models.AuthorLLMRunResolve, version, mode, admin)
}

// StartAuthorLLMSwap starts a swap run of the configuration on its latest
// eval of mode swap: the registration of its passed swap strata happens in
// the same transaction. Without one whose report registers a swap stratum it
// refuses with ErrAuthorLLMPrerequisite.
func StartAuthorLLMSwap(ctx context.Context, db *pg.DB, version, mode string, admin int64) (int64, error) {
	return startAuthorLLMProduction(ctx, db, models.AuthorLLMRunSwap, version, mode, admin)
}

func startAuthorLLMProduction(
	ctx context.Context, db *pg.DB, kind models.AuthorLLMRunKind, version, mode string, admin int64,
) (int64, error) {
	ev, err := database.LatestAuthorLLMEvidence(ctx, db, version, kind)
	if err != nil {
		return 0, err
	}
	if ev == nil {
		return 0, ErrAuthorLLMPrerequisite
	}
	plan, err := planAuthorLLMRun(ev.Report, kind)
	if err != nil {
		return 0, err
	}
	if len(plan.Strata) == 0 {
		return 0, ErrAuthorLLMPrerequisite
	}
	plan.ReportID, plan.ReportSHA256 = ev.ReportID, hex.EncodeToString(ev.ReportSHA256)
	if resolveStartPlannedHook != nil {
		resolveStartPlannedHook()
	}
	id, registered, err := database.StartAuthorLLMResolveRun(ctx, db, &database.AuthorLLMResolveStart{
		Kind: kind, ConfigVersion: version, Mode: mode, Evidence: ev, Settings: plan, CreatedBy: admin})
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

// resolveEnqueuePlannedHook is called, when set, once an enqueue has planned
// what it asks and before it writes anything: a test seam to pause the run
// or race a second driver there.
var resolveEnqueuePlannedHook func()

// resolveStartPlannedHook is called, when set, between a resolve start's
// evidence read and its transaction: a test seam to publish a report there.
var resolveStartPlannedHook func()

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
	ID int64
	// Kind is resolve or swap: the swap run runs on the same machinery.
	Kind      models.AuthorLLMRunKind
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
	run := &resolveRun{ID: row.ID, Kind: row.Kind, Mode: row.Mode}
	if row.Stage != nil {
		run.Stage = *row.Stage
	}
	if err = json.Unmarshal(row.Settings, &run.Plan); err != nil {
		return nil, fmt.Errorf("reading a resolve run's plan: %w", err)
	}
	if len(row.Reference) > 0 {
		if err = json.Unmarshal(row.Reference, &run.Reference); err != nil {
			return nil, fmt.Errorf("reading a resolve run's reference: %w", err)
		}
	}
	learned, err := database.AuthorLLMLearnedReferences(ctx, d.db, run.ID)
	if err != nil {
		return nil, err
	}
	for slot, ref := range learned {
		if run.Reference == nil {
			run.Reference = map[string]database.AuthorLLMFingerprintReference{}
		}
		run.Reference[slot] = ref
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
		enqueue := d.enqueue
		if run.Kind == models.AuthorLLMRunSwap {
			enqueue = d.enqueueSwap
		}
		if err = enqueue(ctx, run); err == nil {
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
	// The inputs an earlier run left unresolved on a transient failure are
	// planned with the open review: the run reopens only those it asks.
	transient, err := database.AuthorLLMTransientInputs(ctx, d.db, d.version)
	if err != nil {
		return err
	}
	proposals := map[string]int64{}
	transientRows := make([]database.AuthorLLMPopulationRow, 0, len(transient))
	for i := range transient {
		transientRows = append(transientRows, transient[i].AuthorLLMPopulationRow)
		proposals[inputKey(transient[i].SourceFingerprint, transient[i].ExtractorVersion)] = transient[i].ProposalResultID
	}
	reopenStrata, _ := classifyPopulation(transientRows)
	for name, pop := range reopenStrata {
		strata[name] = append(strata[name], pop...)
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
	if resolveEnqueuePlannedHook != nil {
		resolveEnqueuePlannedHook()
	}
	asked := items[:0]
	for i := range items {
		it := &items[i]
		proposal, reopen := proposals[inputKey(it.row.SourceFingerprint, it.row.ExtractorVersion)]
		if reopen {
			ok, reopenErr := d.reopenTransient(ctx, run, it.row, proposal)
			if reopenErr != nil || !ok {
				if errors.Is(reopenErr, errResolveRunNotRunning) {
					return nil
				}
				if reopenErr != nil {
					return reopenErr
				}
				continue
			}
		}
		asked = append(asked, *it)
	}
	if err := d.enqueueJobs(ctx, run, asked); err != nil {
		return err
	}
	return d.reopenHeld(ctx, run)
}

// enqueueJobs enqueues the production pair's jobs of the inputs the run
// asks.
func (d *AuthorLLMResolveDriver) enqueueJobs(ctx context.Context, run *resolveRun, asked []populationItem) error {
	for start := 0; start < len(asked); start += resolveEnqueueChunk {
		chunk := asked[start:min(start+resolveEnqueueChunk, len(asked))]
		pair := []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB}
		jobs := make([]database.AuthorLLMNewJob, 0, len(pair)*len(chunk))
		for i := range chunk {
			it := &chunk[i]
			plan := run.Plan.Strata[it.stratum]
			again := fingerprintDefect(it)
			for _, slot := range pair {
				jobs = append(jobs, database.AuthorLLMNewJob{ConfigVersion: d.version, Slot: slot,
					Purpose: run.purpose(), SourceFingerprint: it.row.SourceFingerprint,
					ExtractorVersion: it.row.ExtractorVersion, Arm: plan.Arm, BatchSize: plan.ItemsPerRequest,
					OutputMode: run.mode(slot), Input: it.input, WithContext: plan.Arm == llmeval.ArmB,
					AskAgainApplyFailed: again})
			}
		}
		if _, err := database.EnqueueAuthorLLMReviewJobs(ctx, d.db, run.ID, jobs); err != nil {
			return err
		}
	}
	return nil
}

// reopenHeld gives the inputs the particle rule holds their review back
// where an earlier binary took it: run 17 closed its V8 rejections with the
// credits unresolved llm_invalid, and runs 15 and 16 selected results whose
// reply dropped the particle. The resolver puts the credits back in review
// on the local proposal and opens the review item; the input is not asked
// again. Each reopen runs under the run's lock — a paused or closed run
// reopens nothing — and the input's, and checks again that the input is
// still eligible (an admin may have decided it since). It runs after the
// jobs are enqueued, so a reopened input takes no place in the run's sample.
func (d *AuthorLLMResolveDriver) reopenHeld(ctx context.Context, run *resolveRun) error {
	held, err := database.AuthorLLMHeldInputs(ctx, d.db)
	if err != nil {
		return err
	}
	for i := range held {
		h := &held[i]
		out := &database.AutomaticOutcome{ResultID: h.ProposalResultID, DecisionClass: authornorm.DecisionClass(h.DecisionClass),
			LLM: true, Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview}}
		err = d.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			running, lockErr := database.LockAuthorLLMRunRunning(ctx, tx, run.ID)
			if lockErr != nil {
				return lockErr
			}
			if !running {
				return errResolveRunNotRunning
			}
			if lockErr := database.LockNormalizationInput(ctx, tx, h.SourceFingerprint, h.ExtractorVersion); lockErr != nil {
				return lockErr
			}
			eligible, checkErr := database.AuthorLLMHeldInputEligible(ctx, tx, h.SourceFingerprint, h.ExtractorVersion)
			if checkErr != nil || !eligible {
				return checkErr
			}
			_, resolveErr := database.ResolveCredits(ctx, tx, h.SourceFingerprint, h.ExtractorVersion, out)
			return resolveErr
		})
		if errors.Is(err, errResolveRunNotRunning) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// inputKey names an input in a map.
func inputKey(fingerprint []byte, extractorVersion string) string {
	return string(fingerprint) + "\x00" + extractorVersion
}

// fingerprintDefect reports an input whose request fields do not rebuild
// its fingerprint: an apply_failed verdict of it is the defect run 15 hit
// (20 inputs, 23503) before the result took the verdict's fingerprint, and
// it is asked again. An apply_failed of any other input stays final.
func fingerprintDefect(it *populationItem) bool {
	in, err := llmres.NewInput(it.input.Fields)
	if err != nil {
		return false
	}
	fp := in.SourceFingerprint()
	return !bytes.Equal(fp[:], it.row.SourceFingerprint)
}

// errResolveRunNotRunning stops an enqueue whose run was paused or closed
// meanwhile: it writes nothing more, and a resumed run enqueues again.
var errResolveRunNotRunning = errors.New("services: the resolve run is not running")

// reopenTransient gives an input an earlier run left unresolved on a
// transient failure its review back, so this run asks it again: the resolver
// puts its credits back in review on the local proposal and opens a review
// item for it. It runs under the run's lock — a paused or closed run reopens
// nothing (errResolveRunNotRunning) — and the input's, and checks again that
// the input is still eligible (an admin may have decided it since); false
// when it is not.
func (d *AuthorLLMResolveDriver) reopenTransient(
	ctx context.Context, run *resolveRun, row *database.AuthorLLMPopulationRow, proposal int64,
) (bool, error) {
	out := &database.AutomaticOutcome{ResultID: proposal, DecisionClass: authornorm.DecisionClass(row.DecisionClass),
		LLM: true, Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview}}
	reopened := false
	err := d.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		running, err := database.LockAuthorLLMRunRunning(ctx, tx, run.ID)
		if err != nil {
			return err
		}
		if !running {
			return errResolveRunNotRunning
		}
		if lockErr := database.LockNormalizationInput(ctx, tx, row.SourceFingerprint, row.ExtractorVersion); lockErr != nil {
			return lockErr
		}
		eligible, err := database.AuthorLLMTransientInputEligible(ctx, tx, d.version, row.SourceFingerprint,
			row.ExtractorVersion)
		if err != nil || !eligible {
			return err
		}
		if _, err = database.ResolveCredits(ctx, tx, row.SourceFingerprint, row.ExtractorVersion, out); err != nil {
			return err
		}
		reopened = true
		return nil
	})
	return reopened, err
}

// purpose is the purpose of the run's jobs.
func (r *resolveRun) purpose() models.AuthorLLMJobPurpose {
	if r.Kind == models.AuthorLLMRunSwap {
		return models.AuthorLLMPurposeSwapCheck
	}
	return models.AuthorLLMPurposeReview
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
	verdicts, err := database.AuthorLLMVerdictsToApply(ctx, d.db, run.ID, run.Kind, resolveApplyBatch)
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
	// The report counts it under its stratum, when the input still reads.
	stratum := ""
	_ = isolateItem(func() error {
		var input llmreq.ItemInput
		if json.Unmarshal(v.Input, &input) == nil {
			stratum, _ = verdictStratum(v, &input)
		}
		return nil
	})
	return d.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, settleErr := database.SettleAuthorLLMVerdict(ctx, tx, v.ID,
			&database.AuthorLLMVerdictApplication{Reason: &reason, Stratum: stratum})
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
		credits, err := database.CurrentAuthorCredits(ctx, tx, v.SourceFingerprint, v.ExtractorVersion)
		if err != nil {
			return err
		}
		if credits == 0 {
			// A rescan removed the input after the verdict was confirmed:
			// nothing to apply it to.
			stratum, _ := verdictStratum(v, &input)
			gone := models.AuthorLLMInputGone
			_, err = database.SettleAuthorLLMVerdict(ctx, tx, v.ID,
				&database.AuthorLLMVerdictApplication{Reason: &gone, Stratum: stratum})
			return err
		}
		settlement, outcome, err := d.decide(ctx, tx, run, v, &input)
		if err != nil {
			return err
		}
		if settlement.KeepReview || (settlement.Reason != nil && models.AuthorLLMTransientOutcome(*settlement.Reason)) ||
			(run.Kind == models.AuthorLLMRunSwap && settlement.ResultID == nil) {
			// A transient failure decides nothing: the review stays open
			// and the credits as they are, and the next run asks again.
			// Nor does a verdict the particle rule holds, which stays
			// final: the input is left to manual review. A swap that is
			// not applied changes nothing: the local selection stays.
			_, err = database.SettleAuthorLLMVerdict(ctx, tx, v.ID, settlement)
			return err
		}
		if outcome != nil {
			if _, err = database.ResolveCredits(ctx, tx, v.SourceFingerprint, v.ExtractorVersion, outcome); err != nil {
				return err
			}
		}
		if run.Kind != models.AuthorLLMRunSwap {
			if _, err = database.CloseAutomaticReviewItems(ctx, tx, v.SourceFingerprint, settlement.ResultID); err != nil {
				return err
			}
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
		return s, &database.AutomaticOutcome{ResultID: *v.ProposalResultID, DecisionClass: base, LLM: true,
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeUnresolved, Reason: authornorm.UnresolvedReason(r)}}, nil
	}
	if v.ParticleHeld() {
		// V8 refused the pair's or a judge's replies, or refuses the
		// agreed reply now: settled for the report, left to manual review.
		reason := string(models.UnresolvedLLMInvalid)
		if verdictReason(v) == models.UnresolvedLLMJudgeInvalid {
			reason = string(models.UnresolvedLLMJudgeInvalid)
		}
		return &database.AuthorLLMVerdictApplication{Reason: &reason, Stratum: stratum, KeepReview: true}, nil, nil
	}
	if reason := verdictReason(v); reason != "" {
		return unresolved(reason)
	}
	if run.Kind == models.AuthorLLMRunSwap && deref(v.Tier) == string(llmres.TierConfirm) {
		// The pair and both judges confirm the FB2 fields: no swap.
		return unresolved(models.UnresolvedReason(models.AuthorLLMSwapFieldsConfirmed))
	}
	plan, planned := run.Plan.Strata[stratum]
	if !planned || v.Tier == nil || !slices.Contains(plan.Tiers, *v.Tier) ||
		deref(v.Arm) != plan.Arm || v.BatchSize != plan.ItemsPerRequest {
		return unresolved(models.UnresolvedLLMNotRegistered)
	}
	if !armAFallbackHolds(&plan, v) {
		return unresolved(models.UnresolvedLLMContextUnavailable)
	}
	result, ok := assembleVerdict(v, input, d.version, base)
	if !ok {
		return unresolved(models.UnresolvedLLMInvalid)
	}
	if string(result.Script) != plan.Script {
		return unresolved(models.UnresolvedLLMNotRegistered)
	}
	if run.Kind == models.AuthorLLMRunSwap {
		agree, agreeErr := d.swapStatsAgree(ctx, tx, run, v, input)
		if agreeErr != nil {
			return nil, nil, agreeErr
		}
		if !agree {
			return unresolved(models.UnresolvedReason(models.AuthorLLMSwapStatsDisagree))
		}
	}
	policy, err := database.AuthorLLMRegistration(ctx, tx, d.version, result.DecisionClass, plan.Script)
	if err != nil {
		return nil, nil, err
	}
	policyVersion, convErr := strconv.Atoi(policy)
	if policy == "" || convErr != nil {
		return unresolved(models.UnresolvedLLMNotRegistered)
	}
	if run.Kind == models.AuthorLLMRunSwap {
		// An automatic selection replaces another only under a policy
		// version no lower than the one it was made under.
		current, policyErr := database.AuthorLLMSelectedPolicy(ctx, tx, v.SourceFingerprint, v.ExtractorVersion)
		if policyErr != nil {
			return nil, nil, policyErr
		}
		if policyVersion < current {
			return unresolved(models.UnresolvedLLMNotRegistered)
		}
	}
	id, err := database.InsertLLMResult(ctx, tx, &result)
	if err != nil {
		return nil, nil, err
	}
	return &database.AuthorLLMVerdictApplication{ResultID: &id, PolicyVersion: &policy, Stratum: stratum},
		&database.AutomaticOutcome{ResultID: id, DecisionClass: result.DecisionClass, LLM: true,
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeSelected, PolicyVersion: policyVersion}}, nil
}

// armAFallbackHolds reports whether the answer stands under the arm it was
// actually given in. On a stratum registered on arm B, a production
// participant that could not read the excerpt answered arm A's request —
// either participant, each job keeps its own context state — and the answer
// then needs arm A to have passed at that batch size on its own tier.
func armAFallbackHolds(plan *database.AuthorLLMResolveStratum, v *database.AuthorLLMVerdictToApply) bool {
	unavailable := string(models.AuthorLLMContextUnavailable)
	if plan.Arm != llmeval.ArmB || (v.ContextStateA != unavailable && v.ContextStateB != unavailable) {
		return true
	}
	return plan.ArmAPassed && v.Tier != nil && slices.Contains(plan.ArmATiers, *v.Tier)
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
	// The result is of the input the verdict judged, whose identity is its
	// stored fingerprint. The request's fields are a view of the credit that
	// need not hash back to it: an empty FB2 element is left out of them,
	// and the fingerprint's display keeps the FB2 element order (run 15:
	// 20 results the credits' selections could not point at, 23503).
	if len(v.SourceFingerprint) != len(out.Result.SourceFingerprint) {
		return authornorm.Result{}, false
	}
	copy(out.Result.SourceFingerprint[:], v.SourceFingerprint)
	if out.Result.NormalizationKey, err = authornorm.NormalizationKey(out.Result.SourceFingerprint, v.ExtractorVersion,
		version); err != nil {
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
	// NotPassed are the swap strata the swap eval measured and did not pass:
	// reported, never registered.
	NotPassed []string         `json:"not_passed,omitempty"`
	Skipped   map[string]int   `json:"skipped"`
	Tokens    map[string]int64 `json:"tokens"`
	// FingerprintRelearns is the history of the participants re-learned
	// after a drift during the run.
	FingerprintRelearns []database.AuthorLLMRelearnRecord `json:"fingerprint_relearns"`
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
		Strata: map[string]*AuthorLLMResolveCount{}, NotRegistrable: run.Plan.NotRegistrable,
		NotPassed: run.Plan.NotPassed, Skipped: map[string]int{}}
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
	if doc.FingerprintRelearns, err = database.AuthorLLMRelearns(ctx, d.db, run.ID); err != nil {
		return err
	}
	if doc.FingerprintRelearns == nil {
		doc.FingerprintRelearns = []database.AuthorLLMRelearnRecord{}
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
