package services

import (
	"errors"
	"time"

	"gopds-api/database"
)

// Types shared by the author metadata workers: the leased stages, the closed
// error classes a worker may record, and the retry policy that turns a failed
// attempt into the delay before the next one. The lease mechanics themselves
// live in database/author_metadata_jobs.go.

// AuthorMetadataStage is one leased stream of the pipeline. There is no LLM
// stage (scope amendment A1).
type AuthorMetadataStage string

const (
	AuthorMetadataStageExtraction         AuthorMetadataStage = "extraction"
	AuthorMetadataStageLocalNormalization AuthorMetadataStage = "local_normalization"
)

// AuthorMetadataStages lists every leased stage.
func AuthorMetadataStages() []AuthorMetadataStage {
	return []AuthorMetadataStage{AuthorMetadataStageExtraction, AuthorMetadataStageLocalNormalization}
}

// LeaseStream is the database stream the stage leases from.
func (s AuthorMetadataStage) LeaseStream() database.LeaseStream {
	switch s {
	case AuthorMetadataStageExtraction:
		return database.LeaseStreamExtraction
	case AuthorMetadataStageLocalNormalization:
		return database.LeaseStreamLocalNormalization
	}
	return ""
}

// AuthorMetadataErrorClass is a closed error class: what a worker stores,
// logs and reports instead of free error text.
type AuthorMetadataErrorClass string

const (
	// AuthorMetadataErrorTransientDatabase is a database error worth retrying.
	AuthorMetadataErrorTransientDatabase AuthorMetadataErrorClass = "transient_database"
	// AuthorMetadataErrorNormalizerFailed is a local normalizer failure.
	AuthorMetadataErrorNormalizerFailed AuthorMetadataErrorClass = "normalizer_failed"
	// AuthorMetadataErrorLeaseExpired closes an attempt whose lease ran out.
	AuthorMetadataErrorLeaseExpired AuthorMetadataErrorClass = database.LeaseErrorLeaseExpired
	// AuthorMetadataErrorMaxAttemptsExceeded closes a row's last attempt.
	AuthorMetadataErrorMaxAttemptsExceeded AuthorMetadataErrorClass = database.LeaseErrorMaxAttemptsExceeded
)

// AuthorMetadataErrorClasses lists every closed error class.
func AuthorMetadataErrorClasses() []AuthorMetadataErrorClass {
	return []AuthorMetadataErrorClass{
		AuthorMetadataErrorTransientDatabase, AuthorMetadataErrorNormalizerFailed,
		AuthorMetadataErrorLeaseExpired, AuthorMetadataErrorMaxAttemptsExceeded,
	}
}

// ErrInvalidAuthorMetadataRetryPolicy marks a policy with a non-positive
// limit, a cap below the base delay or no jitter source.
var ErrInvalidAuthorMetadataRetryPolicy = errors.New("services: invalid author metadata retry policy")

// backoffFactor is how much the retry window grows per failed attempt.
const backoffFactor = 2

// AuthorMetadataRetryPolicy is exponential backoff with injected jitter.
type AuthorMetadataRetryPolicy struct {
	// MaxAttempts is how many attempts a row gets in total.
	MaxAttempts int
	// BaseDelay is the window for the first retry; it doubles per attempt.
	BaseDelay time.Duration
	// MaxDelay caps the window.
	MaxDelay time.Duration
	// Jitter returns a duration in [0, limit]; injected so schedules are
	// reproducible in tests. Out-of-range values are clamped.
	Jitter func(limit time.Duration) time.Duration
}

// Validate refuses a policy that would retry forever or never wait.
func (p AuthorMetadataRetryPolicy) Validate() error {
	if p.MaxAttempts <= 0 || p.BaseDelay <= 0 || p.MaxDelay < p.BaseDelay || p.Jitter == nil {
		return ErrInvalidAuthorMetadataRetryPolicy
	}
	return nil
}

// Delay is the wait after attempt failed: a window of BaseDelay doubled per
// earlier attempt, capped at MaxDelay, of which the upper half is jittered.
func (p AuthorMetadataRetryPolicy) Delay(attempt int) time.Duration {
	window := p.BaseDelay
	// Doubling stops at the cap, which also keeps a large attempt number from
	// overflowing the shift.
	for i := 1; i < attempt && window < p.MaxDelay; i++ {
		window *= backoffFactor
	}
	if window > p.MaxDelay {
		window = p.MaxDelay
	}
	half := window / backoffFactor
	jitter := p.Jitter(window - half)
	if jitter < 0 {
		jitter = 0
	}
	if jitter > window-half {
		jitter = window - half
	}
	return half + jitter
}

// Failure describes the failed attempt for the lease layer.
func (p AuthorMetadataRetryPolicy) Failure(class AuthorMetadataErrorClass, attempt int) database.LeaseFailure {
	return database.LeaseFailure{
		ErrorClass:  string(class),
		RetryAfter:  p.Delay(attempt),
		MaxAttempts: p.MaxAttempts,
	}
}
