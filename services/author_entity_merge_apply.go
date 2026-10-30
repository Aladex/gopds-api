package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// Registration of a merge stratum and the apply of its eval (E4d, plan
// §4.4, §4.7, Q7). An administrator registers a kind from a merge eval
// report that passed it; the census (or the bucket holdout) of that eval
// already asked every item with all four participants, so its accepted
// items — the pair agreed same and both judges answered same — are
// inserted as decisions through the compare-and-insert, with no new call
// to a model. A withdrawal or a script block undoes the kind's merges at
// the next build.

// Statuses of AuthorMetadataEventEntityMergeAction for registrations.
const (
	authorEntityMergeKindRegistered = "kind_registered"
	authorEntityMergeKindWithdrawn  = "kind_withdrawn"
	authorEntityMergeScriptBlocked  = "script_blocked"
	authorEntityMergeEvalApplied    = "eval_applied"
)

var (
	// ErrAuthorEntityMergeReportInvalid refuses a registration from a run
	// that is not a completed merge eval with a merge report.
	ErrAuthorEntityMergeReportInvalid = errors.New("services: not a completed merge eval with a report")
	// ErrAuthorEntityMergeGate refuses a report gated against anything but
	// judge consensus.
	ErrAuthorEntityMergeGate = errors.New("services: the report's gate is not judge_consensus")
	// ErrAuthorEntityMergeNotPassed refuses a kind the report did not pass,
	// or a report that may back no registration.
	ErrAuthorEntityMergeNotPassed = errors.New("services: the report did not pass the kind")
	// ErrAuthorEntityMergeNotRegistered refuses an apply of a registration
	// that does not exist or is withdrawn.
	ErrAuthorEntityMergeNotRegistered = errors.New("services: the merge registration does not exist or is withdrawn")
	// ErrAuthorEntityMergeDriftHeld holds an apply while a participant of
	// the merge configuration is paused for a fingerprint drift: before the
	// insert, or from the chunk on that saw it (database).
	ErrAuthorEntityMergeDriftHeld = database.ErrMergeApplyDriftHeld
)

// mergeRegistrationReport is what a registration reads of a merge eval
// report.
type mergeRegistrationReport struct {
	Task          string `json:"task"`
	Gate          string `json:"gate"`
	ConfigVersion string `json:"config_version"`
	Evaluation    struct {
		Decision llmeval.Decision `json:"decision"`
	} `json:"evaluation"`
}

// mergeKindPassed checks a merge eval report for a registration of kind: a
// merge report of its configuration, gated against judge consensus, that
// may back a registration and passed the kind.
func mergeKindPassed(report *database.AuthorLLMEvalReport, kind string) error {
	var doc mergeRegistrationReport
	if err := json.Unmarshal([]byte(report.Report), &doc); err != nil {
		return fmt.Errorf("reading a merge eval report: %w", err)
	}
	if doc.Task != llmreq.TaskMerge || doc.ConfigVersion != report.ConfigVersion {
		return ErrAuthorEntityMergeReportInvalid
	}
	if doc.Gate != database.MergeGateJudgeConsensus {
		return ErrAuthorEntityMergeGate
	}
	if !doc.Evaluation.Decision.Registrable {
		return ErrAuthorEntityMergeNotPassed
	}
	for _, s := range doc.Evaluation.Decision.Strata {
		if s.Stratum == kind && s.Passed {
			return nil
		}
	}
	return ErrAuthorEntityMergeNotPassed
}

// RegisterMergeKind registers kind for the merge configuration of a
// completed merge eval run, from the run's latest report, which must have
// passed the kind and still carry the labels it was computed from
// (database.ErrMergeRegistrationEvidenceStale otherwise: recompute it). It
// returns the registration's id.
func (a *AuthorEntityAdmin) RegisterMergeKind(ctx context.Context, runID int64, kind string, actor int64) (int64, error) {
	run, err := database.GetAuthorLLMRunByID(ctx, a.db, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return 0, ErrAuthorEntityMergeReportInvalid
	}
	if err != nil {
		return 0, err
	}
	if run.Kind != models.AuthorLLMRunEval || !models.IsAuthorLLMEvalMergeMode(run.Mode) ||
		run.Status != models.AuthorLLMRunCompleted {
		return 0, ErrAuthorEntityMergeReportInvalid
	}
	report, err := database.GetAuthorLLMEvalReport(ctx, a.db, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return 0, ErrAuthorEntityMergeReportInvalid
	}
	if err != nil {
		return 0, err
	}
	if passErr := mergeKindPassed(report, kind); passErr != nil {
		return 0, passErr
	}
	binding, err := mergeEvidenceBinding(report)
	if err != nil {
		return 0, err
	}
	id, err := database.RegisterMergeKind(ctx, a.db, &database.MergeRegistrationInput{
		ConfigVersion: report.ConfigVersion, Kind: kind, ReportID: report.ID, ReportSHA256: report.ReportSHA256,
		Gate: database.MergeGateJudgeConsensus, ActorID: actor, Adjudications: binding})
	if err == nil {
		logMergeAction(authorEntityMergeKindRegistered, id)
	}
	return id, err
}

// WithdrawMergeRegistration withdraws a registration: from the next build
// on its kind's decisions lapse (withdrawn) and their merges split.
func (a *AuthorEntityAdmin) WithdrawMergeRegistration(ctx context.Context, id, actor int64) error {
	err := database.WithdrawMergeRegistration(ctx, a.db, id, actor)
	if err == nil {
		logMergeAction(authorEntityMergeKindWithdrawn, id)
	}
	return err
}

// BlockMergeScript blocks one script of a registration: from the next
// build on its decisions of that script lapse (script_blocked).
func (a *AuthorEntityAdmin) BlockMergeScript(ctx context.Context, id int64, script string, actor int64) error {
	err := database.BlockMergeScript(ctx, a.db, id, script, actor)
	if err == nil {
		logMergeAction(authorEntityMergeScriptBlocked, id)
	}
	return err
}

// MergeRegistrations lists the registrations, newest first.
func (a *AuthorEntityAdmin) MergeRegistrations(ctx context.Context) ([]database.MergeRegistration, error) {
	return database.MergeRegistrations(ctx, a.db)
}

// AuthorEntityMergeApplyReport is what an apply did: the items of the
// eval asked on the kind's candidates, those the production rule accepted,
// and the insert's counts.
type AuthorEntityMergeApplyReport struct {
	RegistrationID int64  `json:"registration_id"`
	Kind           string `json:"kind"`
	RunID          int64  `json:"run_id"`
	ReportID       int64  `json:"report_id"`
	Items          int    `json:"items"`
	Accepted       int    `json:"accepted"`
	database.MergeInsertReport
}

// mergeVerdictJobs are each item's first answers by participant, as the
// jobs that gave them: the pair's and the judges' (the ids mergeVerdicts'
// words come from).
func mergeVerdictJobs(answers []database.AuthorLLMEvalAnswer) map[int64]map[string]int64 {
	jobs := map[int64]map[string]int64{}
	for i := range answers {
		a := &answers[i]
		if a.RepeatNo != 0 || (a.Purpose != string(models.AuthorLLMPurposeEvalCandidate) &&
			a.Purpose != string(models.AuthorLLMPurposeEvalJudge)) {
			continue
		}
		if jobs[a.EvalItemID] == nil {
			jobs[a.EvalItemID] = map[string]int64{}
		}
		jobs[a.EvalItemID][a.Slot] = a.JobID
	}
	return jobs
}

// mergeApplyAccepted reports an item the production rule accepts: the pair
// agreed same and both judges answered same, first answers.
func mergeApplyAccepted(answers map[string]string) bool {
	for _, slot := range llmreq.Slots() {
		if answers[string(slot)] != llmreq.MergeSame {
			return false
		}
	}
	return true
}

// ApplyMergeEval inserts the decisions of a live registration's eval: every
// census or holdout item of the kind the production rule accepted, compared
// with the latest completed build's candidates (an item whose card changed
// since it was asked is lapsed_before_apply and left to be asked again).
// Each decision cites the four eval jobs that answered same. It is held
// while a participant drifted — checked here and again in every insert
// transaction — and refused while a build is active or a veto is
// unanchored; a refusal after some chunks keeps them, and the report counts
// the rest as refused. Applying again inserts only what is new.
func (a *AuthorEntityAdmin) ApplyMergeEval(
	ctx context.Context, registrationID, actor int64,
) (*AuthorEntityMergeApplyReport, error) {
	reg, err := database.MergeRegistrationByID(ctx, a.db, registrationID)
	if err != nil {
		return nil, err
	}
	if reg == nil || reg.WithdrawnAt != nil {
		return nil, ErrAuthorEntityMergeNotRegistered
	}
	held, err := database.MergeApplyDriftHeld(ctx, a.db, reg.ConfigVersion, reg.RunID)
	if err != nil {
		return nil, err
	}
	if held {
		return nil, ErrAuthorEntityMergeDriftHeld
	}
	items, err := database.MergeApplyItems(ctx, a.db, reg.RunID, reg.Kind)
	if err != nil {
		return nil, err
	}
	answers, err := database.AuthorLLMEvalAnswers(ctx, a.db, reg.RunID)
	if err != nil {
		return nil, err
	}
	verdicts, jobs := mergeVerdicts(answers), mergeVerdictJobs(answers)
	rep := &AuthorEntityMergeApplyReport{RegistrationID: reg.ID, Kind: reg.Kind, RunID: reg.RunID,
		ReportID: reg.EvidenceReportID, Items: len(items)}
	var accepted []database.MergeVerdict
	for i := range items {
		it := &items[i]
		if !mergeApplyAccepted(verdicts[it.ID]) {
			continue
		}
		v := database.MergeVerdict{ConfigVersion: reg.ConfigVersion, Kind: reg.Kind, ReportID: reg.EvidenceReportID}
		if copy(v.Digest[:], it.Fingerprint) != len(v.Digest) {
			continue
		}
		// The four answers that decided it, in slot order: the eval jobs
		// of production_a, production_b, judge_a, judge_b.
		for _, slot := range llmreq.Slots() {
			v.VerdictIDs = append(v.VerdictIDs, jobs[it.ID][string(slot)])
		}
		accepted = append(accepted, v)
	}
	rep.Accepted = len(accepted)
	inserted, err := database.InsertAuthorEntityMergeDecisions(ctx, a.db, accepted, &database.MergeInsertOptions{
		ActorID: actor, DriftConfig: reg.ConfigVersion, DriftRunID: reg.RunID})
	if inserted != nil {
		rep.MergeInsertReport = *inserted
	}
	if err != nil {
		return rep, err
	}
	logMergeAction(authorEntityMergeEvalApplied, reg.ID)
	return rep, nil
}
