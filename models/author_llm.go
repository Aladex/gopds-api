package models

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// Closed values of the LLM author layer's tables (migration 29). Each type's
// constants mirror one CHECK constraint; the schema tests compare the two.

// AuthorLLMRunKind is what a run does.
type AuthorLLMRunKind string

const (
	AuthorLLMRunEval    AuthorLLMRunKind = "eval"
	AuthorLLMRunResolve AuthorLLMRunKind = "resolve"
	AuthorLLMRunSwap    AuthorLLMRunKind = "swap"
	// AuthorLLMRunMerge: the merge configuration's resolve-type run (E4d-2,
	// migration 69): its merge_check questions become merge decisions.
	AuthorLLMRunMerge AuthorLLMRunKind = "merge"
)

// AuthorLLMRunStatus is a run's lifecycle state, the metadata runs' set.
type AuthorLLMRunStatus string

const (
	AuthorLLMRunPending        AuthorLLMRunStatus = "pending"
	AuthorLLMRunRunning        AuthorLLMRunStatus = "running"
	AuthorLLMRunPaused         AuthorLLMRunStatus = "paused"
	AuthorLLMRunCompleted      AuthorLLMRunStatus = "completed"
	AuthorLLMRunFailedSystemic AuthorLLMRunStatus = "failed_systemic"
	// AuthorLLMRunClosed: an administrator ended it (a run of an older
	// configuration, or a paused one); it leaves the active slot.
	AuthorLLMRunClosed AuthorLLMRunStatus = "closed"
)

// AuthorLLMRunPauseReason says why a run is paused.
type AuthorLLMRunPauseReason string

const (
	// AuthorLLMRunPausedAdmin: the admin paused it.
	AuthorLLMRunPausedAdmin AuthorLLMRunPauseReason = "admin"
	// AuthorLLMRunPausedBudget: the budget guard stopped it.
	AuthorLLMRunPausedBudget AuthorLLMRunPauseReason = "budget"
)

// AuthorLLMJobPurpose is what a job's answer is for.
type AuthorLLMJobPurpose string

const (
	AuthorLLMPurposeReview        AuthorLLMJobPurpose = "review"
	AuthorLLMPurposeSwapCheck     AuthorLLMJobPurpose = "swap_check"
	AuthorLLMPurposeEvalJudge     AuthorLLMJobPurpose = "eval_judge"
	AuthorLLMPurposeEvalCandidate AuthorLLMJobPurpose = "eval_candidate"
	AuthorLLMPurposeEvalRepeat    AuthorLLMJobPurpose = "eval_repeat"
	AuthorLLMPurposeEvalAnchor    AuthorLLMJobPurpose = "eval_anchor"
	// AuthorLLMPurposeEvalFingerprint: the fixed fingerprint request sent
	// by the eval run's fingerprint stage (migration 31).
	AuthorLLMPurposeEvalFingerprint AuthorLLMJobPurpose = "eval_fingerprint"
	AuthorLLMPurposeCanary          AuthorLLMJobPurpose = "canary"
	// AuthorLLMPurposeMergeCheck: a merge question of the merge
	// configuration (E4c, migration 66), keyed by its candidate's digest.
	// A merge run asks it (E4d-2, migration 69); its verdicts become
	// merge decisions.
	AuthorLLMPurposeMergeCheck AuthorLLMJobPurpose = "merge_check"
)

// IsResolve reports whether the purpose belongs to the resolve side (an input
// fingerprint, a verdict of the production pair).
func (p AuthorLLMJobPurpose) IsResolve() bool {
	return p == AuthorLLMPurposeReview || p == AuthorLLMPurposeSwapCheck || p == AuthorLLMPurposeMergeCheck
}

// AuthorLLMJobStatus is a job's state.
type AuthorLLMJobStatus string

const (
	AuthorLLMJobPending  AuthorLLMJobStatus = "pending"
	AuthorLLMJobClaimed  AuthorLLMJobStatus = "claimed"
	AuthorLLMJobAnswered AuthorLLMJobStatus = "answered"
	// AuthorLLMJobInvalid: the participant's replies stayed invalid after the
	// one allowed retry.
	AuthorLLMJobInvalid AuthorLLMJobStatus = "invalid"
	// AuthorLLMJobFailed: the calls failed until the attempts ran out.
	AuthorLLMJobFailed AuthorLLMJobStatus = "failed"
)

// IsTerminal reports whether the job is done.
func (s AuthorLLMJobStatus) IsTerminal() bool {
	return s == AuthorLLMJobAnswered || s == AuthorLLMJobInvalid || s == AuthorLLMJobFailed
}

// AuthorLLMContextState is what became of a job's excerpt.
type AuthorLLMContextState string

const (
	AuthorLLMContextNone        AuthorLLMContextState = "none"
	AuthorLLMContextAttached    AuthorLLMContextState = "attached"
	AuthorLLMContextIrrelevant  AuthorLLMContextState = "irrelevant"
	AuthorLLMContextUnavailable AuthorLLMContextState = "unavailable"
)

// AuthorLLMCallKind is what a call asks.
type AuthorLLMCallKind string

const (
	AuthorLLMCallJobs        AuthorLLMCallKind = "jobs"
	AuthorLLMCallFingerprint AuthorLLMCallKind = "fingerprint"
)

// AuthorLLMCallOutcome is how a call ended.
type AuthorLLMCallOutcome string

const (
	AuthorLLMCallAnswered       AuthorLLMCallOutcome = "answered"
	AuthorLLMCallHTTPError      AuthorLLMCallOutcome = "http_error"
	AuthorLLMCallTimeout        AuthorLLMCallOutcome = "timeout"
	AuthorLLMCallTransportError AuthorLLMCallOutcome = "transport_error"
	AuthorLLMCallMalformed      AuthorLLMCallOutcome = "malformed"
	AuthorLLMCallAbandoned      AuthorLLMCallOutcome = "abandoned"
)

// AuthorLLMAttemptOutcome is how one job's part of a call ended.
type AuthorLLMAttemptOutcome string

const (
	AuthorLLMAttemptAnswered       AuthorLLMAttemptOutcome = "answered"
	AuthorLLMAttemptInvalid        AuthorLLMAttemptOutcome = "invalid"
	AuthorLLMAttemptModelMismatch  AuthorLLMAttemptOutcome = "model_mismatch"
	AuthorLLMAttemptHTTPError      AuthorLLMAttemptOutcome = "http_error"
	AuthorLLMAttemptTimeout        AuthorLLMAttemptOutcome = "timeout"
	AuthorLLMAttemptTransportError AuthorLLMAttemptOutcome = "transport_error"
	AuthorLLMAttemptMalformed      AuthorLLMAttemptOutcome = "malformed"
	AuthorLLMAttemptAbandoned      AuthorLLMAttemptOutcome = "abandoned"
)

// AuthorLLMPauseReason says why a participant is paused.
type AuthorLLMPauseReason string

const (
	AuthorLLMPausedAuth          AuthorLLMPauseReason = "auth"
	AuthorLLMPausedModelMismatch AuthorLLMPauseReason = "model_mismatch"
	AuthorLLMPausedBadRequest    AuthorLLMPauseReason = "bad_request"
	AuthorLLMPausedDrift         AuthorLLMPauseReason = "drift"
	// AuthorLLMPausedOverReserve: the endpoint reported more tokens than the
	// call's hard bound — it ignored the output ceiling, or its tokenizer is
	// not byte-level. The budget's assumptions no longer hold for it.
	AuthorLLMPausedOverReserve AuthorLLMPauseReason = "over_reserve"
	// AuthorLLMPausedRelearnUnstable: a drifted participant's re-learn did not
	// settle (its calls disagreed, or no output mode worked) — migration 35.
	AuthorLLMPausedRelearnUnstable AuthorLLMPauseReason = "relearn_unstable"
	// AuthorLLMPausedCheckUnavailable: the participant's fingerprint check
	// kept failing transiently past the transient window — migration 40.
	// Its answers stay unconfirmed until a resumed check passes.
	AuthorLLMPausedCheckUnavailable AuthorLLMPauseReason = "check_unavailable"
	// AuthorLLMPausedQuotaExhausted pauses the endpoint, not a participant:
	// every configuration calling it.
	AuthorLLMPausedQuotaExhausted AuthorLLMPauseReason = "quota_exhausted"
)

// AuthorLLMVerdictValue is the production pair's comparison on one input.
type AuthorLLMVerdictValue string

const (
	AuthorLLMVerdictAgree    AuthorLLMVerdictValue = "agree"
	AuthorLLMVerdictDisagree AuthorLLMVerdictValue = "disagree"
	AuthorLLMVerdictInvalid  AuthorLLMVerdictValue = "invalid"
	AuthorLLMVerdictRefused  AuthorLLMVerdictValue = "refused"
	AuthorLLMVerdictFailed   AuthorLLMVerdictValue = "failed"
	// AuthorLLMVerdictHeldDrift: a fingerprint check failed after the
	// answers; the verdict is history and its input is answered again.
	AuthorLLMVerdictHeldDrift AuthorLLMVerdictValue = "held_drift"
)

// AuthorLLMJudgement is the judges' judgement of an answer the production
// pair agreed on (migration 34).
type AuthorLLMJudgement string

const (
	// AuthorLLMJudgementConfirmed: both judges answered it byte-equal to the
	// pair.
	AuthorLLMJudgementConfirmed AuthorLLMJudgement = "confirmed"
	AuthorLLMJudgementDisagree  AuthorLLMJudgement = "disagree"
	AuthorLLMJudgementRefused   AuthorLLMJudgement = "refused"
	AuthorLLMJudgementInvalid   AuthorLLMJudgement = "invalid"
	AuthorLLMJudgementFailed    AuthorLLMJudgement = "failed"
)

// AuthorLLMJudgements lists every judgement the schema accepts.
func AuthorLLMJudgements() []AuthorLLMJudgement {
	return []AuthorLLMJudgement{AuthorLLMJudgementConfirmed, AuthorLLMJudgementDisagree, AuthorLLMJudgementRefused,
		AuthorLLMJudgementInvalid, AuthorLLMJudgementFailed}
}

// AuthorLLMApplyFailed is the outcome of a verdict whose application failed
// for that input alone: its credits were left as they were.
const AuthorLLMApplyFailed = "apply_failed"

// AuthorLLMInputGone is the outcome of a verdict whose input had no current
// credit left when it was applied: a rescan removed it.
const AuthorLLMInputGone = "input_gone"

// The outcomes of a swap verdict that is not applied for want of a swap
// (migration 41): the pair confirms the FB2 fields, or the catalog's role
// statistics do not say what the answer says. The local selection stays.
const (
	AuthorLLMSwapFieldsConfirmed = "swap_fields_confirmed"
	AuthorLLMSwapStatsDisagree   = "swap_stats_disagree"
)

// The outcomes of a merge verdict that inserted no decision (migration 69):
// the pair agreed the two are different, or the compare-and-insert did not
// take it — the candidate is gone from the stored census, a live decision
// of it exists, its kind is not registered or withdrawn, its script blocked,
// or a side is credited on no current book. An applied merge verdict has
// no outcome reason.
const (
	AuthorLLMMergeNotSame           = "merge_not_same"
	AuthorLLMMergeLapsedBeforeApply = "merge_lapsed_before_apply"
	AuthorLLMMergeAlreadyDecided    = "merge_already_decided"
	AuthorLLMMergeUnregistered      = "merge_unregistered"
	AuthorLLMMergeWithdrawn         = "merge_withdrawn"
	AuthorLLMMergeScriptBlocked     = "merge_script_blocked"
	AuthorLLMMergeSideUnrecorded    = "merge_side_unrecorded"
)

// AuthorLLMMergeOutcomes lists the merge outcomes the schema accepts.
func AuthorLLMMergeOutcomes() []string {
	return []string{AuthorLLMMergeNotSame, AuthorLLMMergeLapsedBeforeApply, AuthorLLMMergeAlreadyDecided,
		AuthorLLMMergeUnregistered, AuthorLLMMergeWithdrawn, AuthorLLMMergeScriptBlocked, AuthorLLMMergeSideUnrecorded}
}

// AuthorLLMUndecidedOutcomes are the verdict outcomes that decide nothing
// about the input: the application failed, the input is gone, or the pair or
// the judges failed transiently — the next resolve run asks the input again.
func AuthorLLMUndecidedOutcomes() []string {
	return []string{AuthorLLMApplyFailed, AuthorLLMInputGone, string(UnresolvedLLMFailed),
		string(UnresolvedLLMJudgeFailed)}
}

// AuthorLLMTransientOutcome reports a verdict outcome the next resolve run
// asks again: the pair or the judges failed transiently.
func AuthorLLMTransientOutcome(reason string) bool {
	return reason == string(UnresolvedLLMFailed) || reason == string(UnresolvedLLMJudgeFailed)
}

// AuthorLLMRun is one row of author_llm_run, as the raw queries scan it.
type AuthorLLMRun struct {
	ID              int64              `pg:"id,pk"`
	Kind            AuthorLLMRunKind   `pg:"kind"`
	Mode            string             `pg:"mode"`
	Status          AuthorLLMRunStatus `pg:"status"`
	ConfigVersion   string             `pg:"config_version"`
	Stage           *string            `pg:"stage"`
	EstimateTokens  int64              `pg:"estimate_tokens,use_zero"`
	ExpectedModels  json.RawMessage    `pg:"expected_models,type:jsonb"`
	Settings        json.RawMessage    `pg:"settings,type:jsonb"`
	Reference       json.RawMessage    `pg:"reference,type:jsonb"`
	PausedReason    *string            `pg:"paused_reason"`
	LastErrorClass  *string            `pg:"last_error_class"`
	CreatedByUserID *int64             `pg:"created_by_user_id"`
	ClosedByUserID  *int64             `pg:"closed_by_user_id"`
	CreatedAt       time.Time          `pg:"created_at"`
	UpdatedAt       time.Time          `pg:"updated_at"`
	StartedAt       *time.Time         `pg:"started_at"`
	FinishedAt      *time.Time         `pg:"finished_at"`
}

// AuthorLLMProviderState is one row of author_llm_provider_state, as the raw
// queries scan it.
type AuthorLLMProviderState struct {
	ConfigVersion      string     `pg:"config_version,pk"`
	Slot               string     `pg:"slot,pk"`
	Concurrency        int        `pg:"concurrency"`
	SuccessStreak      int        `pg:"success_streak,use_zero"`
	ThrottledUntil     *time.Time `pg:"throttled_until"`
	PausedReason       *string    `pg:"paused_reason"`
	PausedAt           *time.Time `pg:"paused_at"`
	AnsweredSinceCheck int        `pg:"answered_since_check,use_zero"`
	CheckRunID         *int64     `pg:"check_run_id"`
	LastCheckPassedAt  *time.Time `pg:"last_check_passed_at"`
	CheckSeq           int64      `pg:"check_seq,use_zero"`
	CheckCallID        *int64     `pg:"check_call_id"`
	// CheckFailures counts the participant's consecutive failed checks
	// since CheckFailingSince, the first of them (migration 40).
	CheckFailures     int        `pg:"check_failures,use_zero"`
	CheckFailingSince *time.Time `pg:"check_failing_since"`
	// RejectedItems are the distinct items the provider rejected since the
	// participant's last answered call or bad_request pause.
	RejectedItems []string  `pg:"rejected_items,array"`
	UpdatedAt     time.Time `pg:"updated_at"`
}

// AuthorLLMEvalItemSet is the set a frozen eval item belongs to.
type AuthorLLMEvalItemSet string

const (
	AuthorLLMEvalHoldout     AuthorLLMEvalItemSet = "holdout"
	AuthorLLMEvalDev         AuthorLLMEvalItemSet = "dev"
	AuthorLLMEvalAnchor      AuthorLLMEvalItemSet = "anchor"
	AuthorLLMEvalCanary      AuthorLLMEvalItemSet = "canary"
	AuthorLLMEvalFingerprint AuthorLLMEvalItemSet = "fingerprint"
	// AuthorLLMEvalAdversarial: the merge eval's namesake suite (E4c), each
	// item expected different.
	AuthorLLMEvalAdversarial AuthorLLMEvalItemSet = "adversarial"
)

// The modes of an eval run.
const (
	AuthorLLMEvalModeDev   = "dev"
	AuthorLLMEvalModePilot = "pilot"
	AuthorLLMEvalModeFull  = "full"
	// AuthorLLMEvalModeSwap is an eval run that measures the swap strata
	// alone (migration 41): their registration needs its own measurement,
	// and the other strata are not paid for again.
	AuthorLLMEvalModeSwap = "swap"
	// The stage evals measure one fixed set of strata each (migration 61),
	// so the review backlog is evaluated and resolved in stages without
	// paying again for the strata already registered.
	AuthorLLMEvalModeStage0 = "stage0"
	AuthorLLMEvalModeStage1 = "stage1"
	AuthorLLMEvalModeStage2 = "stage2"
	AuthorLLMEvalModeStage3 = "stage3"
	// The merge evals (E4c, migration 66) measure the merge configuration:
	// merge0 the pilot, merge1..merge4 one pooled kind each.
	AuthorLLMEvalModeMerge  = "merge"
	AuthorLLMEvalModeMerge0 = "merge0"
	AuthorLLMEvalModeMerge1 = "merge1"
	AuthorLLMEvalModeMerge2 = "merge2"
	AuthorLLMEvalModeMerge3 = "merge3"
	AuthorLLMEvalModeMerge4 = "merge4"
)

// AuthorLLMEvalStageModes lists the stage eval modes in order.
func AuthorLLMEvalStageModes() []string {
	return []string{AuthorLLMEvalModeStage0, AuthorLLMEvalModeStage1, AuthorLLMEvalModeStage2, AuthorLLMEvalModeStage3}
}

// IsAuthorLLMEvalStageMode reports a stage eval mode.
func IsAuthorLLMEvalStageMode(mode string) bool {
	return slices.Contains(AuthorLLMEvalStageModes(), mode)
}

// AuthorLLMEvalMergeModes lists the merge eval modes in order.
func AuthorLLMEvalMergeModes() []string {
	return []string{AuthorLLMEvalModeMerge, AuthorLLMEvalModeMerge0, AuthorLLMEvalModeMerge1, AuthorLLMEvalModeMerge2,
		AuthorLLMEvalModeMerge3, AuthorLLMEvalModeMerge4}
}

// IsAuthorLLMEvalMergeMode reports a merge eval mode.
func IsAuthorLLMEvalMergeMode(mode string) bool {
	return slices.Contains(AuthorLLMEvalMergeModes(), mode)
}

// authorLLMEvalScopedStage prefixes the stored stage of a stage eval. A
// binary that does not know stage evals (v0.72.0 and before) steps an eval
// run only through the stage names it knows and passes over any other, so it
// leaves a stage eval to the binary that started it instead of reading it as
// a full one. A merge eval's stage is stored the same way (migration 66).
const authorLLMEvalScopedStage = "scoped_"

// AuthorLLMEvalStoredStage is the stage column value of an eval run of mode
// in stage.
func AuthorLLMEvalStoredStage(mode, stage string) string {
	if IsAuthorLLMEvalStageMode(mode) || IsAuthorLLMEvalMergeMode(mode) {
		return authorLLMEvalScopedStage + stage
	}
	return stage
}

// AuthorLLMEvalStageOf is the stage an eval run of mode is in, read from its
// stage column.
func AuthorLLMEvalStageOf(mode, stored string) string {
	if IsAuthorLLMEvalStageMode(mode) || IsAuthorLLMEvalMergeMode(mode) {
		return strings.TrimPrefix(stored, authorLLMEvalScopedStage)
	}
	return stored
}
