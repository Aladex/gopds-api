package services

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The merge run (E4d-2, plan §4.4, §6): a resolve-type run of the merge
// configuration. Its start rests on the configuration's latest merge eval
// (the model ids and fingerprint references its checks hold the pair to)
// and registers nothing. Its driver asks the production pair about every
// candidate due — of the stored census, of a registered, not withdrawn
// kind, of a script not blocked in it, never asked under the merge
// configuration — with the card frozen in author_llm_merge_input; the
// settle asks both judges where the pair agreed. A verdict is inserted as a
// decision only when the pair agreed same and both judges confirmed it
// (the production rule of the apply from an eval), through the one
// compare-and-insert (database.InsertAuthorEntityMergeDecisions): under the
// build start lock, with no build active, no veto unanchored and no
// participant drifted, against the stored census and the live
// registration. The build that follows merges the pairs.

// authorLLMMergeRunDriverName is the merge run driver's log label.
const authorLLMMergeRunDriverName = "author_llm_merge_run"

// Why the merge run's enqueue left a candidate due out (author_llm_eval_skip
// reasons): the plan read now no longer has it (the catalog moved since the
// build; the next build lapses or renews it), or the card frozen under its
// digest does not hash to the digest's input hash.
const (
	mergeRunSkipMoved        = "candidate_moved"
	mergeRunSkipCardMismatch = "card_mismatch"
)

// mergeRunApplyBatch is how many verdicts one step settles.
const mergeRunApplyBatch = 500

// mergeRunEstimate is a merge run's token estimate: every participant
// answers every candidate (the judges only where the pair agrees, so this
// is the bound), at the merge eval's measured cost per answer, and the
// fingerprint checks at its cost per run.
func mergeRunEstimate(candidates int) int64 {
	return int64(candidates)*evalParticipants*mergeTokensPerAnswer + mergeTokensPerRunOverall
}

// StartAuthorLLMMergeRun starts a merge run of the merge configuration. It
// refuses with ErrAuthorLLMPrerequisite without a completed merge eval to
// rest on, a live registration, or a candidate due.
func StartAuthorLLMMergeRun(ctx context.Context, db *pg.DB, version, mode string, admin int64) (int64, error) {
	if mode != models.AuthorLLMEvalModeFull {
		return 0, ErrAuthorLLMInvalidRun
	}
	ev, err := database.LatestAuthorLLMMergeEval(ctx, db, version)
	if err != nil {
		return 0, err
	}
	registrations, err := liveMergeRegistrations(ctx, db, version)
	if err != nil {
		return 0, err
	}
	due, err := database.MergeCandidatesDue(ctx, db, version)
	if err != nil {
		return 0, err
	}
	if ev == nil || len(registrations) == 0 || len(due) == 0 {
		return 0, ErrAuthorLLMPrerequisite
	}
	ids := make([]int64, 0, len(registrations))
	for i := range registrations {
		ids = append(ids, registrations[i].ID)
	}
	id, err := database.StartAuthorLLMMergeRun(ctx, db, &database.AuthorLLMMergeRunStart{
		ConfigVersion: version, Mode: mode, Evidence: ev, EstimateTokens: mergeRunEstimate(len(due)),
		Settings: database.AuthorLLMMergeRunSettings{EvidenceRunID: ev.RunID, EvidenceReportID: ev.ReportID,
			EvidenceSHA256: hex.EncodeToString(ev.ReportSHA256), Registrations: ids},
		CreatedBy: admin})
	if errors.Is(err, database.ErrAuthorLLMNoEvidence) || errors.Is(err, database.ErrAuthorLLMRunEvidence) {
		return 0, ErrAuthorLLMPrerequisite
	}
	if err != nil {
		return 0, err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{Name: AuthorMetadataEventLLMMergeRunStarted,
		Stage: AuthorMetadataStageRunner, RunID: id, Count: len(due), Worker: authorLLMMergeRunDriverName})
	return id, nil
}

// liveMergeRegistrations are the configuration's registrations not
// withdrawn.
func liveMergeRegistrations(ctx context.Context, db pg.DBI, version string) ([]database.MergeRegistration, error) {
	all, err := database.MergeRegistrations(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []database.MergeRegistration
	for i := range all {
		if all[i].ConfigVersion == version && all[i].WithdrawnAt == nil {
			out = append(out, all[i])
		}
	}
	return out, nil
}

// AuthorLLMMergeRunDriver moves the merge configuration's running merge
// run through its stages: enqueue, resolve, report.
type AuthorLLMMergeRunDriver struct {
	db      *pg.DB
	version string
	poll    time.Duration
}

// NewAuthorLLMMergeRunDriver builds the merge run driver of a merge
// configuration.
func NewAuthorLLMMergeRunDriver(db *pg.DB, version string, poll time.Duration) *AuthorLLMMergeRunDriver {
	if poll <= 0 {
		poll = authorLLMPollInterval
	}
	return &AuthorLLMMergeRunDriver{db: db, version: version, poll: poll}
}

// MergeRunDriver is the merge run driver of the worker's configuration.
func (w *AuthorLLMWorker) MergeRunDriver() *AuthorLLMMergeRunDriver {
	return NewAuthorLLMMergeRunDriver(w.db, w.cfg.ConfigVersion, w.cfg.PollInterval)
}

// Name is the driver's log label.
func (d *AuthorLLMMergeRunDriver) Name() string { return authorLLMMergeRunDriverName }

// Run steps the running merge run until ctx is canceled.
func (d *AuthorLLMMergeRunDriver) Run(ctx context.Context) error {
	return pollLoop(ctx, authorLLMMergeRunDriverName, d.poll, d.Step)
}

// mergeRun is the running merge run as the driver reads it.
type mergeRun struct {
	resolveRun
	Settings database.AuthorLLMMergeRunSettings
}

func (d *AuthorLLMMergeRunDriver) running(ctx context.Context) (*mergeRun, error) {
	row, err := database.RunningAuthorLLMMergeRun(ctx, d.db, d.version)
	if err != nil || row == nil {
		return nil, err
	}
	run := &mergeRun{resolveRun: resolveRun{ID: row.ID, Kind: row.Kind, Mode: row.Mode}}
	if row.Stage != nil {
		run.Stage = *row.Stage
	}
	if err = json.Unmarshal(row.Settings, &run.Settings); err != nil {
		return nil, fmt.Errorf("reading a merge run's settings: %w", err)
	}
	if run.Reference, err = productionReference(ctx, d.db, run.ID, row.Reference); err != nil {
		return nil, err
	}
	return run, nil
}

// Step does the running merge run's current stage as far as it can now and
// moves it on when the stage is done. It reports whether it moved a stage.
func (d *AuthorLLMMergeRunDriver) Step(ctx context.Context) (bool, error) {
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
			Worker: authorLLMMergeRunDriverName, SQLState: AuthorMetadataSQLState(err)})
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

// skip records a candidate the enqueue left out.
func (d *AuthorLLMMergeRunDriver) skip(ctx context.Context, run *mergeRun, reason string, digest []byte) error {
	return database.RecordAuthorLLMEvalSkip(ctx, d.db, run.ID, &database.AuthorLLMEvalSkip{Stage: resolveSkipStage,
		Reason: reason, SourceFingerprint: digest})
}

// authorLLMMergeEnqueueHook, a test seam, runs after the due read and the
// card freeze of each chunk, before its questions are inserted.
var authorLLMMergeEnqueueHook func()

// enqueue asks the production pair about every candidate due. The cards
// come from the entity plan read now in one snapshot, as the merge eval
// reads them: a due candidate the plan no longer has is left out (moved),
// and so is one whose input is over the bound. Each card is frozen under
// its digest, and the question is asked only when the frozen card — what
// the model will be sent — hashes to the digest's input hash.
func (d *AuthorLLMMergeRunDriver) enqueue(ctx context.Context, run *mergeRun) error {
	due, err := database.MergeCandidatesDue(ctx, d.db, d.version)
	if err != nil || len(due) == 0 {
		return err
	}
	byDigest := make(map[string]*database.MergeCandidateDue, len(due))
	for i := range due {
		byDigest[string(due[i].Digest)] = &due[i]
	}
	cards, err := d.dueCards(ctx, run, due, byDigest)
	if err != nil {
		return err
	}
	modes := map[llmreq.Slot]string{llmreq.SlotProductionA: run.mode(llmreq.SlotProductionA),
		llmreq.SlotProductionB: run.mode(llmreq.SlotProductionB)}
	for start := 0; start < len(cards); start += resolveEnqueueChunk {
		chunk := cards[start:min(start+resolveEnqueueChunk, len(cards))]
		frozen, err := database.FreezeAuthorLLMMergeInputs(ctx, d.db, chunk)
		if err != nil {
			return err
		}
		ask := make([]database.MergeCandidateDue, 0, len(chunk))
		for i := range chunk {
			digest := chunk[i].Digest
			if !mergeCardHashes(frozen[string(digest)], byDigest[string(digest)].InputSHA256) {
				if err := d.skip(ctx, run, mergeRunSkipCardMismatch, digest); err != nil {
					return err
				}
				continue
			}
			ask = append(ask, *byDigest[string(digest)])
		}
		if authorLLMMergeEnqueueHook != nil {
			authorLLMMergeEnqueueHook()
		}
		if _, err := database.EnqueueAuthorLLMMergeJobs(ctx, d.db, run.ID, d.version, ask, modes); err != nil {
			return err
		}
	}
	return nil
}

// dueCards are the cards of the candidates due, from the entity plan read
// now; the due candidates the plan does not have, or whose input is over
// the bound, are recorded and left out.
func (d *AuthorLLMMergeRunDriver) dueCards(
	ctx context.Context, run *mergeRun, due []database.MergeCandidateDue,
	byDigest map[string]*database.MergeCandidateDue,
) ([]database.AuthorLLMMergeCard, error) {
	r, err := NewAuthorEntityBuildLoop(d.db, AuthorEntityBuildConfig{}).readPlan(ctx)
	if err != nil {
		return nil, err
	}
	census := r.census
	if census == "" {
		census = sealCensus(r.plan, r.cards)
	}
	if census != "" || !r.plan.MergeCandidatesSealed() {
		return nil, errMergeEvalCensus
	}
	var cards []database.AuthorLLMMergeCard
	planned := map[string]bool{}
	cands := r.plan.MergeCandidates()
	for i := range cands {
		c := &cands[i]
		if byDigest[string(c.Digest[:])] == nil {
			continue
		}
		planned[string(c.Digest[:])] = true
		input := r.plan.MergeInput(i)
		if len(input) > llmreq.MergeItemMaxBytes {
			if err := d.skip(ctx, run, evalSkipInputTooLong, c.Digest[:]); err != nil {
				return nil, err
			}
			continue
		}
		cards = append(cards, database.AuthorLLMMergeCard{Digest: bytes.Clone(c.Digest[:]), Kind: c.Kind,
			Script: c.Script, Input: bytes.Clone(input)})
	}
	for i := range due {
		if !planned[string(due[i].Digest)] {
			if err := d.skip(ctx, run, mergeRunSkipMoved, due[i].Digest); err != nil {
				return nil, err
			}
		}
	}
	return cards, nil
}

// mergeCardHashes reports a frozen card that reads as the worker reads it
// and hashes, rendered again, to want: the input hash of the candidate the
// decision will be compared with.
func mergeCardHashes(frozen json.RawMessage, want []byte) bool {
	var in authorentity.MergeInput
	dec := json.NewDecoder(bytes.NewReader(frozen))
	dec.DisallowUnknownFields()
	if len(frozen) == 0 || dec.Decode(&in) != nil || in.CardVersion != authorentity.MergeCardVersion {
		return false
	}
	sum := authorentity.MergeInputSHA256(&in.A, &in.B)
	return bytes.Equal(sum[:], want)
}

// mergeVerdictReason is why a confirmed merge verdict is not inserted as a
// decision; empty when the production rule accepts it: the pair agreed,
// on same, and both judges confirmed that answer.
func mergeVerdictReason(v *database.AuthorLLMMergeVerdict) string {
	switch models.AuthorLLMVerdictValue(v.Verdict) {
	case models.AuthorLLMVerdictAgree:
	case models.AuthorLLMVerdictDisagree:
		return string(models.UnresolvedLLMDisagreement)
	case models.AuthorLLMVerdictRefused:
		return string(models.UnresolvedLLMRefused)
	case models.AuthorLLMVerdictFailed:
		return string(models.UnresolvedLLMFailed)
	case models.AuthorLLMVerdictInvalid, models.AuthorLLMVerdictHeldDrift:
		// A held_drift verdict is never listed (AuthorLLMMergeVerdicts).
		return string(models.UnresolvedLLMInvalid)
	default:
		return string(models.UnresolvedLLMInvalid)
	}
	if hex.EncodeToString(v.Canonical) != mergeSameCanonical {
		return models.AuthorLLMMergeNotSame
	}
	if v.Judgement == nil || v.JudgeA == nil || v.JudgeB == nil {
		return string(models.UnresolvedLLMJudgeFailed)
	}
	switch models.AuthorLLMJudgement(*v.Judgement) {
	case models.AuthorLLMJudgementConfirmed:
		return ""
	case models.AuthorLLMJudgementRefused:
		return string(models.UnresolvedLLMJudgeRefused)
	case models.AuthorLLMJudgementInvalid:
		return string(models.UnresolvedLLMJudgeInvalid)
	case models.AuthorLLMJudgementFailed:
		return string(models.UnresolvedLLMJudgeFailed)
	case models.AuthorLLMJudgementDisagree:
	}
	return string(models.UnresolvedLLMJudgeDisagreement)
}

// mergeInsertReasons are the verdict outcomes of what the insert did.
var mergeInsertReasons = map[string]string{
	database.MergeInsertOutcomeInserted:          "",
	database.MergeInsertOutcomeLapsedBeforeApply: models.AuthorLLMMergeLapsedBeforeApply,
	database.MergeInsertOutcomeAlreadyDecided:    models.AuthorLLMMergeAlreadyDecided,
	database.MergeInsertOutcomeSideUnrecorded:    models.AuthorLLMMergeSideUnrecorded,
	database.MergeWithheldUnregistered:           models.AuthorLLMMergeUnregistered,
	database.MergeWithheldWithdrawn:              models.AuthorLLMMergeWithdrawn,
	database.MergeWithheldScriptBlocked:          models.AuthorLLMMergeScriptBlocked,
}

// mergeInsertHeld reports an insert refusal the run waits out: a build
// running, a veto unanchored, no build yet, a participant drifted.
func mergeInsertHeld(err error) bool {
	return errors.Is(err, database.ErrAuthorEntityBuildActive) ||
		errors.Is(err, database.ErrAuthorEntityVetoUnanchored) ||
		errors.Is(err, database.ErrAuthorEntityNoCompletedBuild) || errors.Is(err, database.ErrMergeApplyDriftHeld)
}

// resolve settles the run's confirmed verdicts: those the production rule
// does not accept with their reason, the rest through the compare-and-insert,
// each settled in the transaction of its chunk by what the insert did with
// it. A refused insert leaves its verdicts for the next step. Once nothing
// is left to answer, judge, confirm or settle, the stage is done.
func (d *AuthorLLMMergeRunDriver) resolve(ctx context.Context, run *mergeRun) (string, error) {
	verdicts, err := database.AuthorLLMMergeVerdicts(ctx, d.db, run.ID, mergeRunApplyBatch)
	if err != nil {
		return "", err
	}
	reports, err := d.registrationReports(ctx)
	if err != nil {
		return "", err
	}
	pending := map[[32]byte]*database.AuthorLLMMergeVerdict{}
	var accepted []database.MergeVerdict
	for i := range verdicts {
		v := &verdicts[i]
		if reason := mergeVerdictReason(v); reason != "" {
			if settleErr := database.SettleAuthorLLMMergeVerdict(ctx, d.db, v.ID, reason,
				mergeStratum(v.Kind, v.Script)); settleErr != nil {
				return "", settleErr
			}
			continue
		}
		mv := database.MergeVerdict{ConfigVersion: d.version, Kind: v.Kind, ReportID: reports[v.Kind],
			VerdictIDs: []int64{v.JobA, v.JobB, *v.JudgeA, *v.JudgeB}}
		if copy(mv.Digest[:], v.Digest) != len(mv.Digest) {
			continue
		}
		pending[mv.Digest] = v
		accepted = append(accepted, mv)
	}
	if held, insertErr := d.insert(ctx, run, accepted, pending); insertErr != nil || held {
		return "", insertErr
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

// insert inserts the accepted verdicts' decisions through the
// compare-and-insert and settles each verdict, in its chunk's transaction,
// by what the insert did with it. held reports a refusal the run waits out;
// its verdicts stay for the next step.
func (d *AuthorLLMMergeRunDriver) insert(
	ctx context.Context, run *mergeRun, accepted []database.MergeVerdict,
	pending map[[32]byte]*database.AuthorLLMMergeVerdict,
) (held bool, err error) {
	if len(accepted) == 0 {
		return false, nil
	}
	settle := func(ctx context.Context, tx *pg.Tx, outcomes map[[32]byte]string) error {
		for digest, class := range outcomes {
			v := pending[digest]
			if v == nil {
				continue
			}
			reason, ok := mergeInsertReasons[class]
			if !ok {
				return fmt.Errorf("services: unknown merge insert outcome %q", class)
			}
			if settleErr := database.SettleAuthorLLMMergeVerdict(ctx, tx, v.ID, reason,
				mergeStratum(v.Kind, v.Script)); settleErr != nil {
				return settleErr
			}
		}
		return nil
	}
	rep, err := database.InsertAuthorEntityMergeDecisions(ctx, d.db, accepted, &database.MergeInsertOptions{
		DriftConfig: d.version, DriftRunID: run.ID, Settle: settle})
	if mergeInsertHeld(err) {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventLLMMergeRunHeld, Stage: AuthorMetadataStageRunner, RunID: run.ID,
			Count: rep.Refused, Status: rep.RefusedReason, Worker: authorLLMMergeRunDriverName})
		return true, nil
	}
	return false, err
}

// registrationReports are the evidence reports of the configuration's
// registrations by kind, withdrawn or not: a decision cites the report its
// kind was registered from (the insert itself refuses a withdrawn kind).
func (d *AuthorLLMMergeRunDriver) registrationReports(ctx context.Context) (map[string]int64, error) {
	all, err := database.MergeRegistrations(ctx, d.db)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for i := range all {
		if all[i].ConfigVersion == d.version {
			out[all[i].Kind] = all[i].EvidenceReportID
		}
	}
	return out, nil
}

// AuthorLLMMergeRunReportDoc is a merge run's stored report: what it asked
// and applied per stratum (kind.script), why the rest was not applied, the
// candidates it left out, and the tokens per participant. Identifiers and
// closed values only — no name, no card.
type AuthorLLMMergeRunReportDoc struct {
	RunID            int64                             `json:"run_id"`
	Mode             string                            `json:"mode"`
	ConfigVersion    string                            `json:"config_version"`
	EvidenceRunID    int64                             `json:"evidence_run_id"`
	EvidenceReportID int64                             `json:"evidence_report_id"`
	EvidenceSHA      string                            `json:"evidence_report_sha256"`
	Registrations    []int64                           `json:"registrations"`
	Strata           map[string]*AuthorLLMResolveCount `json:"strata"`
	Skipped          map[string]int                    `json:"skipped"`
	Tokens           map[string]int64                  `json:"tokens"`
	// FingerprintRelearns is the history of the participants re-learned
	// after a drift during the run.
	FingerprintRelearns []database.AuthorLLMRelearnRecord `json:"fingerprint_relearns"`
}

// report writes the run's report and completes the run.
func (d *AuthorLLMMergeRunDriver) report(ctx context.Context, run *mergeRun) error {
	counts, err := database.AuthorLLMMergeRunCounts(ctx, d.db, run.ID)
	if err != nil {
		return err
	}
	doc := AuthorLLMMergeRunReportDoc{RunID: run.ID, Mode: run.Mode, ConfigVersion: d.version,
		EvidenceRunID: run.Settings.EvidenceRunID, EvidenceReportID: run.Settings.EvidenceReportID,
		EvidenceSHA: run.Settings.EvidenceSHA256, Registrations: run.Settings.Registrations,
		Strata: map[string]*AuthorLLMResolveCount{}, Skipped: map[string]int{}}
	for _, c := range counts {
		line, ok := doc.Strata[c.Stratum]
		if !ok {
			line = &AuthorLLMResolveCount{NotApplied: map[string]int{}}
			doc.Strata[c.Stratum] = line
		}
		line.Sent += c.Count
		if c.Reason == nil {
			line.Applied += c.Count
		} else {
			line.NotApplied[*c.Reason] += c.Count
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
