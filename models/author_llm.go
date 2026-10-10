package models

import (
	"encoding/json"
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
)

// AuthorLLMRunStatus is a run's lifecycle state, the metadata runs' set.
type AuthorLLMRunStatus string

const (
	AuthorLLMRunPending        AuthorLLMRunStatus = "pending"
	AuthorLLMRunRunning        AuthorLLMRunStatus = "running"
	AuthorLLMRunPaused         AuthorLLMRunStatus = "paused"
	AuthorLLMRunCompleted      AuthorLLMRunStatus = "completed"
	AuthorLLMRunFailedSystemic AuthorLLMRunStatus = "failed_systemic"
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
	AuthorLLMPurposeCanary        AuthorLLMJobPurpose = "canary"
)

// IsResolve reports whether the purpose belongs to the resolve side (an input
// fingerprint, a verdict of the production pair).
func (p AuthorLLMJobPurpose) IsResolve() bool {
	return p == AuthorLLMPurposeReview || p == AuthorLLMPurposeSwapCheck
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
	Reference       json.RawMessage    `pg:"reference,type:jsonb"`
	PausedReason    *string            `pg:"paused_reason"`
	LastErrorClass  *string            `pg:"last_error_class"`
	CreatedByUserID *int64             `pg:"created_by_user_id"`
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
	UpdatedAt          time.Time  `pg:"updated_at"`
}
