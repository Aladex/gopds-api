package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The settle of an LLM call: one transaction, under the claim lock, that
// closes the call with what the endpoint reported (usage replaces the
// reserve), closes each attempt with its outcome, moves each job on, adjusts
// the participant's shared state (AIMD window, throttle, pauses, fingerprint
// bookkeeping) and writes the verdicts of the inputs it finished.

// Error classes the settle writes besides the client's.
const (
	// AuthorLLMClassModelMismatch: V0 refused the response's model id.
	AuthorLLMClassModelMismatch = "model_mismatch"
	// AuthorLLMClassDrift: a fingerprint check failed.
	AuthorLLMClassDrift = "drift"
	// The client's classes the settle treats specially.
	authorLLMClassRateLimited    = "rate_limited"
	authorLLMClassQuotaExhausted = "quota_exhausted"
	authorLLMClassAuth           = "auth"
	authorLLMClassBadRequest     = "bad_request"
	authorLLMClassCanceled       = "canceled"
)

// The AIMD and V0 window constants of design K.
const (
	// authorLLMAIMDStep is how many answered calls in a row grow the window
	// by one.
	authorLLMAIMDStep = 50
	// authorLLMMismatchWindow and authorLLMMismatchLimit: this many
	// model_mismatch calls among the participant's last calls pause it.
	authorLLMMismatchWindow = 100
	authorLLMMismatchLimit  = 3
	// authorLLMInvalidLimit is how many invalid replies end a job.
	authorLLMInvalidLimit = 2
	// authorLLMMaxContent caps the stored reply text.
	authorLLMMaxContent = 262144
)

// AuthorLLMItemSettlement is the outcome of one attempt of an answered call.
type AuthorLLMItemSettlement struct {
	AttemptID int64
	// Outcome is answered or invalid.
	Outcome models.AuthorLLMAttemptOutcome
	// ValidatorClass is the closed class of an invalid reply.
	ValidatorClass string
	// Reply is the canonical JSON of an answered reply; CanonicalSHA256 its
	// digest. Tier is empty for a refusal (cannot_tell).
	Reply           []byte
	CanonicalSHA256 []byte
	Tier            string
	Refused         bool
}

// AuthorLLMSettlement is what the worker reports of one call.
type AuthorLLMSettlement struct {
	CallID int64
	Owner  string
	// Outcome is answered when the endpoint returned a response at all.
	Outcome models.AuthorLLMCallOutcome
	// ErrorClass is the client's closed class of a failed call.
	ErrorClass    string
	HTTPStatus    int
	Latency       time.Duration
	RetryAfter    time.Duration
	RequestSHA256 []byte
	ResponseModel string
	UsagePresent  bool
	Usage         json.RawMessage
	PromptTokens  int64
	// CompletionTokens and ReasoningTokens as reported.
	CompletionTokens int64
	ReasoningTokens  *int64
	Content          []byte
	// ModelMismatch: V0 refused the response's model id; every item is void.
	ModelMismatch bool
	// Items are the attempts' outcomes of an answered jobs call.
	Items []AuthorLLMItemSettlement
	// CheckPassed is the result of an answered fingerprint call.
	CheckPassed *bool

	MaxAttempts    int
	ConcurrencyMax int
	// BaseBackoff and MaxBackoff shape the retry delay of a failed job.
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	At          *time.Time
}

// AuthorLLMSettleReport counts what a settle did.
type AuthorLLMSettleReport struct {
	Answered int
	Invalid  int
	Requeued int
	Failed   int
	Verdicts int
	// Paused is the pause reason a participant got, if any.
	Paused string
}

// settledCall is the fenced call row.
type settledCall struct {
	ID             int64     `pg:"id"`
	ConfigVersion  string    `pg:"config_version"`
	Slot           string    `pg:"slot"`
	RunID          int64     `pg:"run_id"`
	Kind           string    `pg:"kind"`
	Items          int       `pg:"items"`
	ReservedTokens int64     `pg:"reserved_tokens"`
	CreatedAt      time.Time `pg:"created_at"`
}

// SettleAuthorLLMCall settles a call the owner holds. A call that is not the
// owner's any more (closed as abandoned) is ErrAuthorLLMLeaseLost and changes
// nothing.
func SettleAuthorLLMCall(ctx context.Context, db *pg.DB, s *AuthorLLMSettlement) (*AuthorLLMSettleReport, error) {
	if !validOwner(s.Owner) {
		return nil, ErrInvalidLeaseOwner
	}
	if s.MaxAttempts < 1 || s.ConcurrencyMax < 1 || s.BaseBackoff <= 0 || s.MaxBackoff < s.BaseBackoff {
		return nil, ErrInvalidLeaseOptions
	}
	if s.ErrorClass != "" && !ValidLeaseErrorClass(s.ErrorClass) {
		return nil, ErrInvalidLeaseFailure
	}
	report := &AuthorLLMSettleReport{}
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return settleAuthorLLMCallTx(ctx, tx, s, report)
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// settleAuthorLLMCallTx is the settle's transaction.
func settleAuthorLLMCallTx(ctx context.Context, tx *pg.Tx, s *AuthorLLMSettlement, report *AuthorLLMSettleReport) error {
	now, err := lockAuthorLLM(ctx, tx, s.At)
	if err != nil {
		return err
	}
	var call settledCall
	_, err = tx.QueryOneContext(ctx, &call, `
		SELECT id, config_version, slot, run_id, kind, items, reserved_tokens, created_at FROM author_llm_call
		WHERE id = ?0 AND lease_owner = ?1::uuid AND finished_at IS NULL
		FOR UPDATE`, s.CallID, s.Owner)
	if errors.Is(err, pg.ErrNoRows) {
		return ErrAuthorLLMLeaseLost
	}
	if err != nil {
		return fmt.Errorf("reading the settled call: %w", err)
	}
	overReserve, err := closeAuthorLLMCall(ctx, tx, &call, s, now)
	if err != nil {
		return err
	}
	if call.Kind == string(models.AuthorLLMCallFingerprint) {
		err = settleAuthorLLMFingerprint(ctx, tx, &call, s, now, report)
	} else {
		err = settleAuthorLLMJobs(ctx, tx, &call, s, now, report)
	}
	if err != nil || !overReserve {
		return err
	}
	// The endpoint charged more than the request's hard bound: it
	// ignored the output ceiling, or its tokenizer is not byte-level.
	// The bound no longer holds for this participant, so it stops.
	return pauseAuthorLLMParticipant(ctx, tx, &call, models.AuthorLLMPausedOverReserve, now, report)
}

// storedContent makes the reply text storable: valid UTF-8 without NUL,
// capped at a rune boundary.
func storedContent(raw []byte) *string {
	if len(raw) == 0 {
		return nil
	}
	s := strings.ReplaceAll(strings.ToValidUTF8(string(raw), "�"), "\x00", "")
	if utf8.RuneCountInString(s) > authorLLMMaxContent {
		s = string([]rune(s)[:authorLLMMaxContent])
	}
	return &s
}

// closeAuthorLLMCall closes the call with what the endpoint reported, and
// reports whether that exceeds the reserve.
func closeAuthorLLMCall(ctx context.Context, tx *pg.Tx, call *settledCall, s *AuthorLLMSettlement, now time.Time) (bool, error) {
	settled := call.ReservedTokens
	switch {
	case s.UsagePresent:
		settled = s.PromptTokens + s.CompletionTokens
	case s.Outcome == models.AuthorLLMCallHTTPError:
		// The endpoint refused the request and reported nothing: nothing
		// was generated.
		settled = 0
	}
	errorClass := s.ErrorClass
	if s.ModelMismatch {
		errorClass = AuthorLLMClassModelMismatch
	}
	var usage, model, class, sha interface{}
	if len(s.Usage) > 0 && json.Valid(s.Usage) {
		usage = string(s.Usage)
	}
	if s.ResponseModel != "" {
		model = s.ResponseModel
	}
	if errorClass != "" {
		class = errorClass
	}
	if len(s.RequestSHA256) == sha256.Size {
		sha = s.RequestSHA256
	}
	var status interface{}
	if s.HTTPStatus != 0 {
		status = s.HTTPStatus
	}
	var prompt, completion interface{}
	if s.UsagePresent {
		prompt, completion = s.PromptTokens, s.CompletionTokens
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE author_llm_call
		SET finished_at = ?1, outcome = ?2, error_class = ?3, http_status = ?4, latency_ms = ?5,
			response_model = ?6, usage = ?7::jsonb, prompt_tokens = ?8, completion_tokens = ?9,
			reasoning_tokens = ?10, response_content = ?11, request_sha256 = ?12, settled_tokens = ?13,
			lease_owner = NULL, lease_expires_at = NULL
		WHERE id = ?0`,
		call.ID, now, string(s.Outcome), class, status, s.Latency.Milliseconds(), model, usage, prompt, completion,
		s.ReasoningTokens, storedContent(s.Content), sha, settled)
	if err != nil {
		return false, fmt.Errorf("closing an LLM call: %w", err)
	}
	return settled > call.ReservedTokens, nil
}

// settleAuthorLLMJobs moves the call's jobs on and adjusts the participant.
func settleAuthorLLMJobs(
	ctx context.Context, tx *pg.Tx, call *settledCall, s *AuthorLLMSettlement, now time.Time, report *AuthorLLMSettleReport,
) error {
	var attempts []struct {
		ID    int64 `pg:"id"`
		JobID int64 `pg:"job_id"`
	}
	if _, err := tx.QueryContext(ctx, &attempts,
		`SELECT id, job_id FROM author_llm_attempt WHERE call_id = ?0 AND finished_at IS NULL ORDER BY id`,
		call.ID); err != nil {
		return fmt.Errorf("reading the call's attempts: %w", err)
	}
	items := make(map[int64]*AuthorLLMItemSettlement, len(s.Items))
	for i := range s.Items {
		items[s.Items[i].AttemptID] = &s.Items[i]
	}

	var ended []int64
	answered := 0
	for _, a := range attempts {
		var (
			done bool
			err  error
		)
		switch {
		case s.Outcome != models.AuthorLLMCallAnswered:
			done, err = failAuthorLLMAttempt(ctx, tx, a.ID, a.JobID, s, now, report)
		case s.ModelMismatch:
			err = voidAuthorLLMAttempt(ctx, tx, a.ID, a.JobID, models.AuthorLLMAttemptModelMismatch,
				AuthorLLMClassModelMismatch, now, report)
		default:
			item := items[a.ID]
			if item == nil {
				item = &AuthorLLMItemSettlement{AttemptID: a.ID, Outcome: models.AuthorLLMAttemptInvalid,
					ValidatorClass: "missing"}
			}
			if item.Outcome == models.AuthorLLMAttemptAnswered {
				err = answerAuthorLLMAttempt(ctx, tx, a.ID, a.JobID, item, now)
				done = true
				answered++
				report.Answered++
			} else {
				done, err = invalidAuthorLLMAttempt(ctx, tx, a.ID, a.JobID, item, call.Items > 1, now, report)
			}
		}
		if err != nil {
			return err
		}
		if done {
			ended = append(ended, a.JobID)
		}
	}

	if err := adjustAuthorLLMParticipant(ctx, tx, call, s, now, answered, report); err != nil {
		return err
	}
	return writeAuthorLLMVerdictsReport(ctx, tx, ended, now, report)
}

// closeAuthorLLMAttempt closes one attempt with an outcome.
func closeAuthorLLMAttempt(ctx context.Context, tx *pg.Tx, attemptID int64, outcome models.AuthorLLMAttemptOutcome,
	validatorClass string, now time.Time) error {
	var class interface{}
	if validatorClass != "" {
		class = validatorClass
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE author_llm_attempt SET finished_at = ?1, outcome = ?2, validator_class = ?3 WHERE id = ?0`,
		attemptID, now, string(outcome), class); err != nil {
		return fmt.Errorf("closing an LLM attempt: %w", err)
	}
	return nil
}

func answerAuthorLLMAttempt(ctx context.Context, tx *pg.Tx, attemptID, jobID int64, item *AuthorLLMItemSettlement, now time.Time) error {
	var tier interface{}
	if !item.Refused {
		tier = item.Tier
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE author_llm_attempt
		SET finished_at = ?1, outcome = 'answered', reply = ?2::jsonb, canonical_sha256 = ?3, tier = ?4, refused = ?5
		WHERE id = ?0`,
		attemptID, now, string(item.Reply), item.CanonicalSHA256, tier, item.Refused); err != nil {
		return fmt.Errorf("closing an answered LLM attempt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE author_llm_job
		SET status = 'answered', call_id = NULL, answer_attempt_id = ?1, finished_at = ?2, last_error_class = NULL
		WHERE id = ?0`, jobID, attemptID, now); err != nil {
		return fmt.Errorf("answering an LLM job: %w", err)
	}
	return nil
}

// invalidAuthorLLMAttempt counts an invalid reply: one retry of the same
// model, alone when it came in a batch, then the job ends invalid.
func invalidAuthorLLMAttempt(ctx context.Context, tx *pg.Tx, attemptID, jobID int64, item *AuthorLLMItemSettlement,
	batched bool, now time.Time, report *AuthorLLMSettleReport) (bool, error) {
	class := item.ValidatorClass
	if !ValidLeaseErrorClass(class) {
		class = "invalid_reply"
	}
	if err := closeAuthorLLMAttempt(ctx, tx, attemptID, models.AuthorLLMAttemptInvalid, class, now); err != nil {
		return false, err
	}
	var status string
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&status), `
		UPDATE author_llm_job
		SET invalid_count = invalid_count + 1, call_id = NULL, last_error_class = ?2,
			retry_single = retry_single OR ?3,
			status = CASE WHEN invalid_count + 1 >= ?4 THEN 'invalid' ELSE 'pending' END,
			finished_at = CASE WHEN invalid_count + 1 >= ?4 THEN ?1::timestamptz END,
			next_attempt_at = ?1
		WHERE id = ?0
		RETURNING status`, jobID, now, class, batched, authorLLMInvalidLimit); err != nil {
		return false, fmt.Errorf("recording an invalid LLM reply: %w", err)
	}
	report.Invalid++
	return status == string(models.AuthorLLMJobInvalid), nil
}

// voidAuthorLLMAttempt closes an attempt whose answer is not the job's fault
// nor the job's answer (model_mismatch) and returns the job to the queue
// without counting it.
func voidAuthorLLMAttempt(ctx context.Context, tx *pg.Tx, attemptID, jobID int64, outcome models.AuthorLLMAttemptOutcome,
	class string, now time.Time, report *AuthorLLMSettleReport) error {
	if err := closeAuthorLLMAttempt(ctx, tx, attemptID, outcome, "", now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE author_llm_job SET status = 'pending', call_id = NULL, last_error_class = ?2, next_attempt_at = ?1
		WHERE id = ?0`, jobID, now, class); err != nil {
		return fmt.Errorf("requeueing an LLM job: %w", err)
	}
	report.Requeued++
	return nil
}

// attemptOutcomeOf maps a failed call's outcome onto its attempts.
func attemptOutcomeOf(o models.AuthorLLMCallOutcome) models.AuthorLLMAttemptOutcome {
	switch o {
	case models.AuthorLLMCallTimeout:
		return models.AuthorLLMAttemptTimeout
	case models.AuthorLLMCallTransportError:
		return models.AuthorLLMAttemptTransportError
	case models.AuthorLLMCallMalformed:
		return models.AuthorLLMAttemptMalformed
	case models.AuthorLLMCallAbandoned:
		return models.AuthorLLMAttemptAbandoned
	case models.AuthorLLMCallAnswered, models.AuthorLLMCallHTTPError:
	}
	return models.AuthorLLMAttemptHTTPError
}

// failAuthorLLMAttempt handles an attempt of a call that got no usable
// response. Throttling, auth, bad requests, quota and cancellation are the
// endpoint's or the configuration's state, not the job's: the job goes back
// without a count, after Retry-After when the endpoint sent one. A server
// error, a timeout or a broken transport counts a failure and backs off
// exponentially with jitter, until the attempts run out. A malformed body
// counts like an invalid reply.
func failAuthorLLMAttempt(ctx context.Context, tx *pg.Tx, attemptID, jobID int64, s *AuthorLLMSettlement,
	now time.Time, report *AuthorLLMSettleReport) (bool, error) {
	outcome := attemptOutcomeOf(s.Outcome)
	switch {
	case s.Outcome == models.AuthorLLMCallMalformed:
		return invalidAuthorLLMAttempt(ctx, tx, attemptID, jobID,
			&AuthorLLMItemSettlement{Outcome: models.AuthorLLMAttemptInvalid, ValidatorClass: "malformed_response"},
			true, now, report)
	case isEndpointStateClass(s.ErrorClass):
		if err := closeAuthorLLMAttempt(ctx, tx, attemptID, outcome, "", now); err != nil {
			return false, err
		}
		delay := max(s.RetryAfter, s.BaseBackoff)
		if _, err := tx.ExecContext(ctx, `
			UPDATE author_llm_job SET status = 'pending', call_id = NULL, last_error_class = ?2,
				next_attempt_at = ?1::timestamptz + ?3 * interval '1 microsecond'
			WHERE id = ?0`, jobID, now, s.ErrorClass, micros(delay)); err != nil {
			return false, fmt.Errorf("requeueing an LLM job: %w", err)
		}
		report.Requeued++
		return false, nil
	}
	if err := closeAuthorLLMAttempt(ctx, tx, attemptID, outcome, "", now); err != nil {
		return false, err
	}
	class := s.ErrorClass
	if class == "" {
		class = string(outcome)
	}
	var status string
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&status), `
		UPDATE author_llm_job
		SET failure_count = failure_count + 1, call_id = NULL, last_error_class = ?2,
			status = CASE WHEN failure_count + 1 >= ?3 THEN 'failed' ELSE 'pending' END,
			finished_at = CASE WHEN failure_count + 1 >= ?3 THEN ?1::timestamptz END,
			next_attempt_at = ?1::timestamptz + least(?4 * power(2, failure_count), ?5)
				* (0.8 + random() * 0.4) * interval '1 microsecond'
		WHERE id = ?0
		RETURNING status`,
		jobID, now, class, s.MaxAttempts, micros(s.BaseBackoff), micros(s.MaxBackoff)); err != nil {
		return false, fmt.Errorf("recording a failed LLM call: %w", err)
	}
	if status == string(models.AuthorLLMJobFailed) {
		report.Failed++
		return true, nil
	}
	report.Requeued++
	return false, nil
}

// isEndpointStateClass reports the classes that say nothing about the job.
func isEndpointStateClass(class string) bool {
	switch class {
	case authorLLMClassRateLimited, authorLLMClassQuotaExhausted, authorLLMClassAuth, authorLLMClassBadRequest,
		authorLLMClassCanceled:
		return true
	}
	return false
}

// adjustAuthorLLMParticipant applies a call's outcome to the participant's
// shared state: the AIMD window (+1 per authorLLMAIMDStep answered calls in a
// row, halved by a 429, with its Retry-After as a throttle), the pauses (402
// pauses every participant of the endpoint, 401/403 and 400 the participant,
// repeated model_mismatch the participant), and the answers since the last
// fingerprint check.
func adjustAuthorLLMParticipant(ctx context.Context, tx *pg.Tx, call *settledCall, s *AuthorLLMSettlement,
	now time.Time, answered int, report *AuthorLLMSettleReport) error {
	switch {
	case s.Outcome == models.AuthorLLMCallAnswered && s.ModelMismatch:
		return countAuthorLLMMismatch(ctx, tx, call, now, report)
	case s.Outcome == models.AuthorLLMCallAnswered:
		return execAuthorLLM(ctx, tx, `
			UPDATE author_llm_provider_state
			SET concurrency = CASE WHEN success_streak + 1 >= ?3 THEN least(concurrency + 1, ?4) ELSE concurrency END,
				success_streak = CASE WHEN success_streak + 1 >= ?3 THEN 0 ELSE success_streak + 1 END,
				answered_since_check = answered_since_check + ?2
			WHERE config_version = ?0 AND slot = ?1`,
			call.ConfigVersion, call.Slot, answered, authorLLMAIMDStep, s.ConcurrencyMax)
	}
	switch s.ErrorClass {
	case authorLLMClassRateLimited:
		var until interface{}
		if s.RetryAfter > 0 {
			until = now.Add(s.RetryAfter)
		}
		return execAuthorLLM(ctx, tx, `
			UPDATE author_llm_provider_state
			SET concurrency = greatest(1, concurrency / 2), success_streak = 0,
				throttled_until = coalesce(?2::timestamptz, throttled_until)
			WHERE config_version = ?0 AND slot = ?1`, call.ConfigVersion, call.Slot, until)
	case authorLLMClassQuotaExhausted:
		return pauseAuthorLLMEndpoint(ctx, tx, call, now, report)
	case authorLLMClassAuth:
		return pauseAuthorLLMParticipant(ctx, tx, call, models.AuthorLLMPausedAuth, now, report)
	case authorLLMClassBadRequest:
		return pauseAuthorLLMParticipant(ctx, tx, call, models.AuthorLLMPausedBadRequest, now, report)
	}
	return execAuthorLLM(ctx, tx,
		`UPDATE author_llm_provider_state SET success_streak = 0 WHERE config_version = ?0 AND slot = ?1`,
		call.ConfigVersion, call.Slot)
}

// execAuthorLLM runs one participant-state statement.
func execAuthorLLM(ctx context.Context, tx *pg.Tx, query string, params ...interface{}) error {
	if _, err := tx.ExecContext(ctx, query, params...); err != nil {
		return fmt.Errorf("adjusting an LLM participant: %w", err)
	}
	return nil
}

// pauseAuthorLLMParticipant pauses the call's participant.
func pauseAuthorLLMParticipant(ctx context.Context, tx *pg.Tx, call *settledCall, reason models.AuthorLLMPauseReason,
	now time.Time, report *AuthorLLMSettleReport) error {
	report.Paused = string(reason)
	return execAuthorLLM(ctx, tx, `UPDATE author_llm_provider_state SET paused_reason = ?2, paused_at = ?3
		WHERE config_version = ?0 AND slot = ?1 AND paused_reason IS NULL`,
		call.ConfigVersion, call.Slot, string(reason), now)
}

// pauseAuthorLLMEndpoint pauses the call's endpoint: a quota is the
// endpoint's and its key's, so every configuration calling it stops.
func pauseAuthorLLMEndpoint(ctx context.Context, tx *pg.Tx, call *settledCall, now time.Time, report *AuthorLLMSettleReport) error {
	report.Paused = string(models.AuthorLLMPausedQuotaExhausted)
	return execAuthorLLM(ctx, tx, `UPDATE author_llm_endpoint_state SET paused_reason = ?1, paused_at = ?2
		WHERE base_url = (SELECT base_url FROM author_llm_config WHERE version = ?0) AND paused_reason IS NULL`,
		call.ConfigVersion, string(models.AuthorLLMPausedQuotaExhausted), now)
}

// countAuthorLLMMismatch pauses the participant once authorLLMMismatchLimit
// of its last authorLLMMismatchWindow calls answered under a model id V0
// refused.
func countAuthorLLMMismatch(ctx context.Context, tx *pg.Tx, call *settledCall, now time.Time, report *AuthorLLMSettleReport) error {
	var mismatches int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&mismatches), `
		SELECT count(*) FILTER (WHERE error_class = ?2) FROM (
			SELECT error_class FROM author_llm_call
			WHERE config_version = ?0 AND slot = ?1 AND finished_at IS NOT NULL
			ORDER BY id DESC LIMIT ?3) recent`,
		call.ConfigVersion, call.Slot, AuthorLLMClassModelMismatch, authorLLMMismatchWindow); err != nil {
		return fmt.Errorf("counting model mismatches: %w", err)
	}
	if mismatches >= authorLLMMismatchLimit {
		return pauseAuthorLLMParticipant(ctx, tx, call, models.AuthorLLMPausedModelMismatch, now, report)
	}
	return nil
}

// settleAuthorLLMFingerprint applies a fingerprint check — only the one the
// participant has in flight; a call that is not it (abandoned and replaced)
// changes nothing. Passed: the check covers the answers finished before it
// was sent (its call's creation), which become confirmable; the answers since
// then stay conditional and count toward the next check. Failed: the
// participant is paused (drift), its answers since the last passed check go
// back to the queue and the verdicts resting on them are held. A call that got
// no response leaves the check due; the participant waits out a backoff.
func settleAuthorLLMFingerprint(ctx context.Context, tx *pg.Tx, call *settledCall, s *AuthorLLMSettlement,
	now time.Time, report *AuthorLLMSettleReport) error {
	var current bool
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&current), `
		UPDATE author_llm_provider_state SET check_call_id = NULL
		WHERE config_version = ?0 AND slot = ?1 AND check_call_id = ?2
		RETURNING true`, call.ConfigVersion, call.Slot, call.ID); err != nil && !errors.Is(err, pg.ErrNoRows) {
		return fmt.Errorf("closing the check in flight: %w", err)
	}
	if !current {
		return nil
	}
	if s.Outcome != models.AuthorLLMCallAnswered || s.CheckPassed == nil {
		if err := adjustAuthorLLMParticipant(ctx, tx, call, s, now, 0, report); err != nil {
			return err
		}
		if report.Paused != "" {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE author_llm_provider_state
			SET throttled_until = greatest(coalesce(throttled_until, ?2), ?2::timestamptz + ?3 * interval '1 microsecond')
			WHERE config_version = ?0 AND slot = ?1`,
			call.ConfigVersion, call.Slot, now, micros(max(s.RetryAfter, s.BaseBackoff)))
		if err != nil {
			return fmt.Errorf("delaying a fingerprint check: %w", err)
		}
		return nil
	}
	if *s.CheckPassed {
		if _, err := tx.ExecContext(ctx, `
			UPDATE author_llm_provider_state
			SET check_run_id = ?2, last_check_passed_at = ?3,
				answered_since_check = (
					SELECT count(*) FROM author_llm_job j
					WHERE j.config_version = ?0 AND j.slot = ?1 AND j.run_id = ?2
						AND j.status IN ('answered', 'invalid', 'failed') AND j.finished_at > ?3)
			WHERE config_version = ?0 AND slot = ?1`, call.ConfigVersion, call.Slot, call.RunID, call.CreatedAt); err != nil {
			return fmt.Errorf("recording a passed fingerprint check: %w", err)
		}
		n, err := confirmAuthorLLMVerdicts(ctx, tx, call.ConfigVersion, call.RunID, now)
		report.Verdicts = n
		return err
	}
	return driftAuthorLLMParticipant(ctx, tx, call, now, report)
}

// driftAuthorLLMParticipant pauses a participant whose fingerprint changed and
// voids its unchecked window.
func driftAuthorLLMParticipant(ctx context.Context, tx *pg.Tx, call *settledCall, now time.Time, report *AuthorLLMSettleReport) error {
	var lastPass *time.Time
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&lastPass), `
		SELECT last_check_passed_at FROM author_llm_provider_state WHERE config_version = ?0 AND slot = ?1`,
		call.ConfigVersion, call.Slot); err != nil {
		return fmt.Errorf("reading the last passed check: %w", err)
	}
	var held int
	_, err := tx.QueryOneContext(ctx, pg.Scan(&held), `
		WITH window_jobs AS (
			UPDATE author_llm_job j
			SET status = 'pending', answer_attempt_id = NULL, finished_at = NULL, call_id = NULL,
				invalid_count = 0, failure_count = 0, retry_single = false,
				last_error_class = ?4, next_attempt_at = ?3
			WHERE j.config_version = ?0 AND j.slot = ?1 AND j.run_id = ?2
				AND j.status IN ('answered', 'invalid', 'failed')
				AND j.finished_at > coalesce(?5::timestamptz, '-infinity')
			RETURNING j.id
		), held AS (
			UPDATE author_llm_verdict v SET verdict = 'held_drift'
			WHERE v.confirmed_at IS NULL AND v.verdict <> 'held_drift'
				AND (v.job_a_id IN (SELECT id FROM window_jobs) OR v.job_b_id IN (SELECT id FROM window_jobs))
			RETURNING v.id
		)
		SELECT count(*) FROM held`,
		call.ConfigVersion, call.Slot, call.RunID, now, AuthorLLMClassDrift, lastPass)
	if err != nil {
		return fmt.Errorf("voiding a drifted window: %w", err)
	}
	report.Verdicts = held
	report.Paused = string(models.AuthorLLMPausedDrift)
	// The next claim after the resume checks again before any job.
	if _, err := tx.ExecContext(ctx, `
		UPDATE author_llm_provider_state
		SET paused_reason = 'drift', paused_at = ?2, check_run_id = NULL, answered_since_check = 0
		WHERE config_version = ?0 AND slot = ?1`, call.ConfigVersion, call.Slot, now); err != nil {
		return fmt.Errorf("pausing a drifted participant: %w", err)
	}
	return nil
}

// confirmAuthorLLMVerdicts confirms the run's conditional verdicts whose
// answers both production participants' passed checks cover: each answer
// finished before the participant's last passed check under the run was sent.
func confirmAuthorLLMVerdicts(ctx context.Context, tx *pg.Tx, version string, runID int64, now time.Time) (int, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE author_llm_verdict v SET confirmed_at = ?2
		FROM author_llm_provider_state sa, author_llm_provider_state sb
		WHERE v.config_version = ?0 AND v.run_id = ?1 AND v.confirmed_at IS NULL AND v.verdict <> 'held_drift'
			AND sa.config_version = ?0 AND sa.slot = 'production_a'
			AND sb.config_version = ?0 AND sb.slot = 'production_b'
			AND sa.check_run_id = v.run_id AND sa.last_check_passed_at >= coalesce(v.answered_a_at, '-infinity')
			AND sb.check_run_id = v.run_id AND sb.last_check_passed_at >= coalesce(v.answered_b_at, '-infinity')`,
		version, runID, now)
	if err != nil {
		return 0, fmt.Errorf("confirming LLM verdicts: %w", err)
	}
	return res.RowsAffected(), nil
}

// verdictJob is one side of a verdict.
type verdictJob struct {
	ID                int64      `pg:"id"`
	ConfigVersion     string     `pg:"config_version"`
	Slot              string     `pg:"slot"`
	RunID             int64      `pg:"run_id"`
	Purpose           string     `pg:"purpose"`
	SourceFingerprint []byte     `pg:"source_fingerprint"`
	ExtractorVersion  *string    `pg:"extractor_version"`
	Status            string     `pg:"status"`
	ContextState      string     `pg:"context_state"`
	AttemptID         *int64     `pg:"answer_attempt_id"`
	CanonicalSHA256   []byte     `pg:"canonical_sha256"`
	Refused           *bool      `pg:"refused"`
	Tier              *string    `pg:"tier"`
	AnsweredAt        *time.Time `pg:"answered_at"`
}

const verdictJobColumns = `j.id, j.config_version, j.slot, j.run_id, j.purpose, j.source_fingerprint,
	j.extractor_version, j.status, j.context_state, j.answer_attempt_id, a.canonical_sha256, a.refused, a.tier,
	a.finished_at AS answered_at`

func writeAuthorLLMVerdictsReport(ctx context.Context, tx *pg.Tx, jobIDs []int64, now time.Time, report *AuthorLLMSettleReport) error {
	n, err := writeAuthorLLMVerdictsCount(ctx, tx, jobIDs, now)
	report.Verdicts += n
	return err
}

func writeAuthorLLMVerdicts(ctx context.Context, tx *pg.Tx, jobIDs []int64, now time.Time) error {
	_, err := writeAuthorLLMVerdictsCount(ctx, tx, jobIDs, now)
	return err
}

// writeAuthorLLMVerdictsCount writes the verdict of every resolve input whose
// production pair is now both done: failed if either failed, invalid if
// either stayed invalid, refused if either refused, agree on byte-equal
// canonical replies, disagree otherwise. Every verdict is written
// conditional: it is confirmed only by fingerprint checks of both production
// participants that cover its answers — at once, when earlier checks already
// do.
func writeAuthorLLMVerdictsCount(ctx context.Context, tx *pg.Tx, jobIDs []int64, now time.Time) (int, error) {
	written := 0
	runs := map[int64]string{}
	for _, id := range jobIDs {
		a, b, err := finishedAuthorLLMPair(ctx, tx, id)
		if err != nil {
			return written, err
		}
		if a == nil {
			continue
		}
		n, err := insertAuthorLLMVerdict(ctx, tx, a, b, now)
		if err != nil {
			return written, err
		}
		written += n
		runs[a.RunID] = a.ConfigVersion
	}
	for runID, version := range runs {
		if _, err := confirmAuthorLLMVerdicts(ctx, tx, version, runID, now); err != nil {
			return written, err
		}
	}
	return written, nil
}

// readVerdictJob reads one side of a verdict by a filter on the job j.
func readVerdictJob(ctx context.Context, tx *pg.Tx, job *verdictJob, filter string, params ...interface{}) error {
	_, err := tx.QueryOneContext(ctx, job, `SELECT `+verdictJobColumns+`
		FROM author_llm_job j LEFT JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE `+filter, params...)
	return err
}

// finishedAuthorLLMPair returns the production pair of a finished resolve
// job, A first, when both are done; nil when there is no verdict to write.
func finishedAuthorLLMPair(ctx context.Context, tx *pg.Tx, jobID int64) (a, b *verdictJob, err error) {
	var self verdictJob
	if err = readVerdictJob(ctx, tx, &self, `j.id = ?0`, jobID); err != nil {
		return nil, nil, fmt.Errorf("reading a finished LLM job: %w", err)
	}
	sibling := string(llmreq.SlotProductionB)
	switch {
	case !models.AuthorLLMJobPurpose(self.Purpose).IsResolve():
		return nil, nil, nil
	case self.Slot == string(llmreq.SlotProductionB):
		sibling = string(llmreq.SlotProductionA)
	case self.Slot != string(llmreq.SlotProductionA):
		return nil, nil, nil
	}
	var other verdictJob
	err = readVerdictJob(ctx, tx, &other, `j.source_fingerprint = ?0 AND j.extractor_version = ?1 AND j.purpose = ?2
			AND j.config_version = ?3 AND j.slot = ?4 AND j.eval_item_id IS NULL`,
		self.SourceFingerprint, self.ExtractorVersion, self.Purpose, self.ConfigVersion, sibling)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading the pair's other job: %w", err)
	}
	if !models.AuthorLLMJobStatus(other.Status).IsTerminal() || !models.AuthorLLMJobStatus(self.Status).IsTerminal() {
		return nil, nil, nil
	}
	if self.Slot == string(llmreq.SlotProductionB) {
		return &other, &self, nil
	}
	return &self, &other, nil
}

// insertAuthorLLMVerdict writes the pair's conditional verdict unless the
// input has a current one.
func insertAuthorLLMVerdict(ctx context.Context, tx *pg.Tx, a, b *verdictJob, now time.Time) (int, error) {
	verdict, tier := compareAuthorLLMPair(a, b)
	contextState := a.ContextState
	if contextState == string(models.AuthorLLMContextNone) {
		contextState = b.ContextState
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO author_llm_verdict (config_version, run_id, source_fingerprint, extractor_version, purpose,
			verdict, tier, context_state, job_a_id, job_b_id, attempt_a_id, attempt_b_id, answered_a_at,
			answered_b_at, created_at)
		VALUES (?0, ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14)
		ON CONFLICT (source_fingerprint, extractor_version, purpose, config_version)
			WHERE verdict <> 'held_drift' DO NOTHING`,
		a.ConfigVersion, a.RunID, a.SourceFingerprint, a.ExtractorVersion, a.Purpose, string(verdict), tier,
		contextState, a.ID, b.ID, a.AttemptID, b.AttemptID, a.AnsweredAt, b.AnsweredAt, now)
	if err != nil {
		return 0, fmt.Errorf("writing an LLM verdict: %w", err)
	}
	return res.RowsAffected(), nil
}

// compareAuthorLLMPair is the verdict of two finished jobs.
func compareAuthorLLMPair(a, b *verdictJob) (verdict models.AuthorLLMVerdictValue, tier interface{}) {
	status := func(j *verdictJob) models.AuthorLLMJobStatus { return models.AuthorLLMJobStatus(j.Status) }
	refused := func(j *verdictJob) bool { return j.Refused != nil && *j.Refused }
	switch {
	case status(a) == models.AuthorLLMJobFailed || status(b) == models.AuthorLLMJobFailed:
		return models.AuthorLLMVerdictFailed, nil
	case status(a) == models.AuthorLLMJobInvalid || status(b) == models.AuthorLLMJobInvalid:
		return models.AuthorLLMVerdictInvalid, nil
	case refused(a) || refused(b):
		return models.AuthorLLMVerdictRefused, nil
	case len(a.CanonicalSHA256) == 32 && bytes.Equal(a.CanonicalSHA256, b.CanonicalSHA256) && a.Tier != nil:
		return models.AuthorLLMVerdictAgree, *a.Tier
	}
	return models.AuthorLLMVerdictDisagree, nil
}
