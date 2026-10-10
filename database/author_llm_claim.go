package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The claim of the LLM worker: one short transaction that decides whether a
// participant may send one more request now, and if so writes the request's
// call row (with its token reserve) and its jobs' attempts before anything is
// sent. Every replica claims under one transaction-scoped advisory lock, so
// the concurrency windows and the budget are counted exactly across replicas;
// the claim holds no lock once it returns, and the HTTP request runs outside
// any transaction.

// authorLLMClaimLock is the advisory lock key of the LLM claims and settles.
const authorLLMClaimLock int64 = 0x4c4c4d434c41494d

// authorLLMLeaseExpired is the error class of a call whose lease ran out.
const authorLLMLeaseExpired = "lease_expired"

// ErrAuthorLLMLeaseLost marks a settle or an excerpt update of a call that is
// no longer the owner's: its lease ran out and the call was closed as
// abandoned.
var ErrAuthorLLMLeaseLost = errors.New("database: the LLM call's lease was lost")

// AuthorLLMClaimOptions are the limits of one claim.
type AuthorLLMClaimOptions struct {
	ConfigVersion string
	Slot          llmreq.Slot
	// Owner is a lease token from NewLeaseOwner.
	Owner string
	// Lease is the lease of a call carrying n items: longer than its timeout.
	Lease func(items int) time.Duration
	// ConcurrencyTotal bounds the open calls at the endpoint, of every
	// configuration that calls it.
	ConcurrencyTotal int
	// FingerprintEvery is how many answered jobs pass between two checks.
	FingerprintEvery int
	// MaxAttempts is how many failed calls a job gets.
	MaxAttempts int
	// Output is the output ceiling each request carries; with the prompt's
	// byte bound it is the call's reserve (llmreq.OutputLimits.Reserve).
	Output llmreq.OutputLimits
	// The budget: every run together, and a run against its own estimate.
	OneOffTokens int64
	RunOverrun   float64
	// At overrides the operation clock in tests; nil is the server clock.
	At *time.Time
}

func (o *AuthorLLMClaimOptions) validate() error {
	switch {
	case !o.Slot.Valid():
		return fmt.Errorf("database: unknown LLM slot %q", o.Slot)
	case !validOwner(o.Owner):
		return ErrInvalidLeaseOwner
	case o.Lease == nil || o.ConcurrencyTotal < 1 || o.FingerprintEvery < 1 || o.MaxAttempts < 1 ||
		o.Output.PerItem < 1 || o.Output.PerCall < 0 || o.OneOffTokens < 1 || o.RunOverrun < 1:
		return ErrInvalidLeaseOptions
	}
	return nil
}

// AuthorLLMClaimRefusal says why a claim took nothing.
type AuthorLLMClaimRefusal string

const (
	AuthorLLMClaimIdle           AuthorLLMClaimRefusal = "idle"
	AuthorLLMClaimPaused         AuthorLLMClaimRefusal = "participant_paused"
	AuthorLLMClaimEndpointPaused AuthorLLMClaimRefusal = "endpoint_paused"
	AuthorLLMClaimThrottled      AuthorLLMClaimRefusal = "throttled"
	AuthorLLMClaimConcurrency    AuthorLLMClaimRefusal = "concurrency"
	// AuthorLLMClaimCheckInFlight: the participant's due check is in flight;
	// no job goes before it settles, and no second check goes with it.
	AuthorLLMClaimCheckInFlight AuthorLLMClaimRefusal = "check_in_flight"
	AuthorLLMClaimBudgetRun     AuthorLLMClaimRefusal = "budget_run"
	AuthorLLMClaimBudgetOneOff  AuthorLLMClaimRefusal = "budget_one_off"
)

// AuthorLLMClaimedJob is one job of a claimed call.
type AuthorLLMClaimedJob struct {
	JobID             int64                        `pg:"id"`
	AttemptID         int64                        `pg:"attempt_id"`
	AttemptNo         int                          `pg:"attempt_count"`
	ItemKey           string                       `pg:"-"`
	Purpose           models.AuthorLLMJobPurpose   `pg:"purpose"`
	Input             json.RawMessage              `pg:"input,type:jsonb"`
	WithContext       bool                         `pg:"with_context,use_zero"`
	ContextState      models.AuthorLLMContextState `pg:"context_state"`
	ContextBookID     *int64                       `pg:"context_book_id"`
	ContextSHA256     []byte                       `pg:"context_sha256"`
	SourceFingerprint []byte                       `pg:"source_fingerprint"`
	ExtractorVersion  *string                      `pg:"extractor_version"`
	EvalItemID        *int64                       `pg:"eval_item_id"`
	EvalBookID        *int64                       `pg:"eval_book_id"`
	EvalBookMD5       *string                      `pg:"eval_book_md5"`
	EvalContextSHA256 []byte                       `pg:"eval_context_sha256"`
	EvalFingerprint   []byte                       `pg:"eval_fingerprint"`
}

// AuthorLLMClaim is one claimed call: a batch of jobs, or the fixed
// fingerprint request of a due check.
type AuthorLLMClaim struct {
	CallID     int64
	Kind       models.AuthorLLMCallKind
	RunID      int64
	OutputMode string
	// PromptBoundBytes is the most bytes of messages and schema the request
	// may carry (llmreq.RequestBytes); OutputCeiling is the output ceiling it
	// must carry. Together they are the call's reserve.
	PromptBoundBytes int
	OutputCeiling    int
	LeaseExpiresAt   time.Time
	// Now is the operation clock the claim ran at.
	Now  time.Time
	Jobs []AuthorLLMClaimedJob
	// ExpectedModels is the V0 list of the participant; nil only while an
	// eval run learns.
	ExpectedModels []string
	// Reference is the fingerprint reference of the participant under the
	// run; nil for a participant the run does not check (an eval run).
	Reference *AuthorLLMFingerprintReference
}

// claimRun is the active running run of a configuration, as a claim needs it.
type claimRun struct {
	ID             int64           `pg:"id"`
	EstimateTokens int64           `pg:"estimate_tokens"`
	ExpectedModels json.RawMessage `pg:"expected_models"`
	Reference      json.RawMessage `pg:"reference"`
}

func (r *claimRun) expected(slot llmreq.Slot) ([]string, error) {
	if r == nil || len(r.ExpectedModels) == 0 {
		return nil, nil
	}
	var bySlot map[string][]string
	if err := json.Unmarshal(r.ExpectedModels, &bySlot); err != nil {
		return nil, fmt.Errorf("reading a run's expected models: %w", err)
	}
	ids, ok := bySlot[string(slot)]
	if !ok {
		// A run that learned ids for other slots but not this one accepts
		// nothing: an empty list, not "learning".
		return []string{}, nil
	}
	return ids, nil
}

func (r *claimRun) reference(slot llmreq.Slot) (*AuthorLLMFingerprintReference, error) {
	if r == nil || len(r.Reference) == 0 {
		return nil, nil
	}
	var bySlot map[string]AuthorLLMFingerprintReference
	if err := json.Unmarshal(r.Reference, &bySlot); err != nil {
		return nil, fmt.Errorf("reading a run's fingerprint reference: %w", err)
	}
	ref, ok := bySlot[string(slot)]
	if !ok {
		return nil, nil
	}
	return &ref, nil
}

// ClaimAuthorLLMCall claims one call for a participant, or says why not.
func ClaimAuthorLLMCall(ctx context.Context, db *pg.DB, opts *AuthorLLMClaimOptions) (*AuthorLLMClaim, AuthorLLMClaimRefusal, error) {
	if err := opts.validate(); err != nil {
		return nil, "", err
	}
	var claim *AuthorLLMClaim
	var refusal AuthorLLMClaimRefusal
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var err error
		claim, refusal, err = claimAuthorLLMCall(ctx, tx, opts)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return claim, refusal, nil
}

// lockAuthorLLM takes the claim lock and reads the operation clock.
func lockAuthorLLM(ctx context.Context, tx *pg.Tx, at *time.Time) (time.Time, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, authorLLMClaimLock); err != nil {
		return time.Time{}, fmt.Errorf("taking the LLM claim lock: %w", err)
	}
	var now time.Time
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&now), `SELECT coalesce(?::timestamptz, clock_timestamp())`, at); err != nil {
		return time.Time{}, fmt.Errorf("reading the clock: %w", err)
	}
	return now, nil
}

func claimAuthorLLMCall(ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions) (*AuthorLLMClaim, AuthorLLMClaimRefusal, error) {
	now, err := lockAuthorLLM(ctx, tx, opts.At)
	if err != nil {
		return nil, "", err
	}
	if abandonErr := abandonExpiredAuthorLLMCalls(ctx, tx, now, opts.MaxAttempts); abandonErr != nil {
		return nil, "", abandonErr
	}
	st, refusal, err := authorLLMParticipantGate(ctx, tx, opts, now)
	if err != nil || refusal != "" {
		return nil, refusal, err
	}
	// Every job belongs to a run: without a running run of this
	// configuration there is nothing to claim.
	run, err := activeClaimRun(ctx, tx, opts.ConfigVersion)
	if err != nil || run == nil {
		return nil, AuthorLLMClaimIdle, err
	}
	ref, expected, err := run.checks(opts.Slot)
	if err != nil {
		return nil, "", err
	}

	// A due fingerprint check goes before any job of the run: at the run's
	// start, and after every FingerprintEvery answers. One check is in flight
	// at a time; while it is, the participant waits.
	if ref != nil && checkDue(st, run.ID, opts.FingerprintEvery) {
		if st.CheckCallID != nil {
			return nil, AuthorLLMClaimCheckInFlight, nil
		}
		return claimAuthorLLMFingerprint(ctx, tx, opts, now, run, ref, expected)
	}

	return claimAuthorLLMJobsOrTail(ctx, tx, opts, now, st, run, ref, expected)
}

// claimAuthorLLMJobsOrTail claims the next batch of the run, or, with
// nothing left to answer, the check of the unchecked tail: answers since the
// last passed check stay conditional until a check passes.
func claimAuthorLLMJobsOrTail(
	ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions, now time.Time, st *models.AuthorLLMProviderState,
	run *claimRun, ref *AuthorLLMFingerprintReference, expected []string,
) (*AuthorLLMClaim, AuthorLLMClaimRefusal, error) {
	jobIDs, err := selectAuthorLLMBatch(ctx, tx, opts, now, run.ID)
	if err != nil {
		return nil, "", err
	}
	if len(jobIDs) > 0 {
		return claimAuthorLLMJobs(ctx, tx, opts, now, run, jobIDs, ref, expected)
	}
	if ref != nil && st.CheckCallID == nil {
		tail, tailErr := authorLLMTailUnchecked(ctx, tx, opts, run.ID, st.LastCheckPassedAt)
		if tailErr != nil {
			return nil, "", tailErr
		}
		if tail {
			return claimAuthorLLMFingerprint(ctx, tx, opts, now, run, ref, expected)
		}
	}
	return nil, AuthorLLMClaimIdle, nil
}

// authorLLMParticipantGate reads the participant's state under a row lock
// and says whether it may send now: the endpoint not paused, the participant
// not paused nor throttled, inside its AIMD window, and the endpoint — every
// configuration calling it — inside its ceiling.
func authorLLMParticipantGate(
	ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions, now time.Time,
) (*models.AuthorLLMProviderState, AuthorLLMClaimRefusal, error) {
	var endpointPaused bool
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&endpointPaused), `
		SELECT e.paused_reason IS NOT NULL FROM author_llm_endpoint_state e
		JOIN author_llm_config c ON c.base_url = e.base_url WHERE c.version = ?0`, opts.ConfigVersion); err != nil {
		return nil, "", fmt.Errorf("reading the endpoint state: %w", err)
	}
	if endpointPaused {
		return nil, AuthorLLMClaimEndpointPaused, nil
	}
	var st models.AuthorLLMProviderState
	if _, err := tx.QueryOneContext(ctx, &st, `
		SELECT * FROM author_llm_provider_state WHERE config_version = ?0 AND slot = ?1 FOR UPDATE`,
		opts.ConfigVersion, string(opts.Slot)); err != nil {
		return nil, "", fmt.Errorf("reading the participant state: %w", err)
	}
	if st.PausedReason != nil {
		return nil, AuthorLLMClaimPaused, nil
	}
	if st.ThrottledUntil != nil && st.ThrottledUntil.After(now) {
		return nil, AuthorLLMClaimThrottled, nil
	}
	var open, total int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&open, &total), `
		SELECT count(*) FILTER (WHERE c.config_version = ?0 AND c.slot = ?1), count(*)
		FROM author_llm_call c
		WHERE c.finished_at IS NULL AND c.config_version IN (
			SELECT version FROM author_llm_config
			WHERE base_url = (SELECT base_url FROM author_llm_config WHERE version = ?0))`,
		opts.ConfigVersion, string(opts.Slot)); err != nil {
		return nil, "", fmt.Errorf("counting open calls: %w", err)
	}
	if open >= st.Concurrency || total >= opts.ConcurrencyTotal {
		return nil, AuthorLLMClaimConcurrency, nil
	}
	return &st, "", nil
}

// claimAuthorLLMJobs writes the call of a selected batch after the budget
// admitted its reserve: the hard bound of the request.
func claimAuthorLLMJobs(
	ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions, now time.Time, run *claimRun, jobIDs []int64,
	ref *AuthorLLMFingerprintReference, expected []string,
) (*AuthorLLMClaim, AuthorLLMClaimRefusal, error) {
	var head struct {
		OutputMode  string `pg:"output_mode"`
		WithContext int    `pg:"with_context"`
	}
	if _, err := tx.QueryOneContext(ctx, &head, `
		SELECT (SELECT output_mode FROM author_llm_job WHERE id = ?0) AS output_mode,
			(SELECT count(*) FROM author_llm_job WHERE id IN (?1) AND with_context) AS with_context`,
		jobIDs[0], pg.In(jobIDs)); err != nil {
		return nil, "", fmt.Errorf("reading the batch head: %w", err)
	}
	bound := authorLLMCallBound{
		promptBytes: llmreq.RequestBoundBytes(len(jobIDs), head.WithContext),
		ceiling:     opts.Output.Ceiling(len(jobIDs)),
		reserve:     opts.Output.Reserve(len(jobIDs), head.WithContext),
	}
	if refusal, err := authorLLMBudgetRefusal(ctx, tx, opts, run, bound.reserve); err != nil || refusal != "" {
		return nil, refusal, err
	}
	callID, leaseExpires, err := insertAuthorLLMCall(ctx, tx, opts, now, run.ID, models.AuthorLLMCallJobs,
		len(jobIDs), head.OutputMode, &bound, opts.Lease(len(jobIDs)))
	if err != nil {
		return nil, "", err
	}
	jobs, err := openAuthorLLMAttempts(ctx, tx, callID, jobIDs, now)
	if err != nil {
		return nil, "", err
	}
	return &AuthorLLMClaim{
		CallID: callID, Kind: models.AuthorLLMCallJobs, RunID: run.ID, OutputMode: head.OutputMode,
		PromptBoundBytes: bound.promptBytes, OutputCeiling: bound.ceiling,
		LeaseExpiresAt: leaseExpires, Now: now, Jobs: jobs, ExpectedModels: expected, Reference: ref,
	}, "", nil
}

// authorLLMCallBound is the hard bound of one call.
type authorLLMCallBound struct {
	promptBytes int
	ceiling     int
	reserve     int64
	checkSeq    *int64
}

// checks is what the run checks of a participant: its fingerprint reference
// and its V0 list.
func (r *claimRun) checks(slot llmreq.Slot) (*AuthorLLMFingerprintReference, []string, error) {
	ref, err := r.reference(slot)
	if err != nil {
		return nil, nil, err
	}
	expected, err := r.expected(slot)
	if err != nil {
		return nil, nil, err
	}
	return ref, expected, nil
}

// checkDue reports a due fingerprint check: none passed under this run yet,
// or FingerprintEvery answers since the last one.
func checkDue(st *models.AuthorLLMProviderState, runID int64, every int) bool {
	return st.CheckRunID == nil || *st.CheckRunID != runID || st.AnsweredSinceCheck >= every
}

// activeClaimRun is the configuration's running run, if any.
func activeClaimRun(ctx context.Context, tx *pg.Tx, version string) (*claimRun, error) {
	var run claimRun
	_, err := tx.QueryOneContext(ctx, &run, `
		SELECT id, estimate_tokens, expected_models, reference FROM author_llm_run
		WHERE config_version = ?0 AND status = 'running'`, version)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the running LLM run: %w", err)
	}
	return &run, nil
}

// selectAuthorLLMBatch locks the ready jobs of one call: the oldest ready job
// of the participant in the running run and, when its batch
// size allows, more jobs of the same run, purpose, arm, batch size and output
// mode. A job marked retry_single goes alone.
func selectAuthorLLMBatch(ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions, now time.Time, runID int64) ([]int64, error) {
	var head struct {
		ID          int64   `pg:"id"`
		Purpose     string  `pg:"purpose"`
		Arm         *string `pg:"arm"`
		BatchSize   int     `pg:"batch_size"`
		OutputMode  string  `pg:"output_mode"`
		RetrySingle bool    `pg:"retry_single"`
	}
	_, err := tx.QueryOneContext(ctx, &head, `
		SELECT j.id, j.purpose, j.arm, j.batch_size, j.output_mode, j.retry_single
		FROM author_llm_job j
		WHERE j.config_version = ?0 AND j.slot = ?1 AND j.status = 'pending' AND j.next_attempt_at <= ?2
			AND j.run_id = ?3
		ORDER BY j.next_attempt_at, j.id
		LIMIT 1
		FOR UPDATE OF j SKIP LOCKED`,
		opts.ConfigVersion, string(opts.Slot), now, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("selecting the next LLM job: %w", err)
	}
	ids := []int64{head.ID}
	if head.RetrySingle || head.BatchSize <= 1 {
		return ids, nil
	}
	var more []int64
	_, err = tx.QueryContext(ctx, &more, `
		SELECT j.id FROM author_llm_job j
		WHERE j.config_version = ?0 AND j.slot = ?1 AND j.status = 'pending' AND j.next_attempt_at <= ?2
			AND j.run_id = ?9 AND j.purpose = ?3 AND coalesce(j.arm, '') = coalesce(?4, '')
			AND j.batch_size = ?5 AND j.output_mode = ?6 AND NOT j.retry_single AND j.id <> ?7
		ORDER BY j.next_attempt_at, j.id
		LIMIT ?8
		FOR UPDATE OF j SKIP LOCKED`,
		opts.ConfigVersion, string(opts.Slot), now, head.Purpose, head.Arm, head.BatchSize,
		head.OutputMode, head.ID, head.BatchSize-1, runID)
	if err != nil {
		return nil, fmt.Errorf("selecting an LLM batch: %w", err)
	}
	return append(ids, more...), nil
}

// authorLLMTailUnchecked reports whether the participant has answers of the
// run's verdicts newer than its last passed check.
func authorLLMTailUnchecked(ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions, runID int64, lastPass *time.Time) (bool, error) {
	column := `answered_a_at`
	if opts.Slot == llmreq.SlotProductionB {
		column = `answered_b_at`
	} else if opts.Slot != llmreq.SlotProductionA {
		return false, nil
	}
	var exists bool
	_, err := tx.QueryOneContext(ctx, pg.Scan(&exists), `
		SELECT EXISTS (
			SELECT 1 FROM author_llm_verdict v
			WHERE v.config_version = ?0 AND v.run_id = ?1 AND v.confirmed_at IS NULL AND v.verdict <> 'held_drift'
				AND v.`+column+` > coalesce(?2::timestamptz, '-infinity'))`,
		opts.ConfigVersion, runID, lastPass)
	if err != nil {
		return false, fmt.Errorf("looking for unchecked answers: %w", err)
	}
	return exists, nil
}

// authorLLMBudgetRefusal applies the token limits to a new reserve: the run
// against its own estimate, and every run together against the one-off
// total. Either pauses the run (budget).
func authorLLMBudgetRefusal(
	ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions, run *claimRun, reserve int64,
) (AuthorLLMClaimRefusal, error) {
	var spentRun, spentRuns int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&spentRun, &spentRuns), `
		SELECT coalesce(sum(tokens) FILTER (WHERE run_id = ?0), 0), coalesce(sum(tokens), 0)
		FROM author_llm_token_tally`, run.ID); err != nil {
		return "", fmt.Errorf("reading the runs' LLM tokens: %w", err)
	}
	refusal := AuthorLLMClaimRefusal("")
	switch {
	case run.EstimateTokens > 0 && float64(spentRun+reserve) > opts.RunOverrun*float64(run.EstimateTokens):
		refusal = AuthorLLMClaimBudgetRun
	case spentRuns+reserve > opts.OneOffTokens:
		refusal = AuthorLLMClaimBudgetOneOff
	}
	if refusal != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE author_llm_run SET status = 'paused', paused_reason = 'budget'
			WHERE id = ?0 AND status = 'running'`, run.ID); err != nil {
			return "", fmt.Errorf("pausing the run on its budget: %w", err)
		}
	}
	return refusal, nil
}

// insertAuthorLLMCall writes the call row with its bound, reserve and lease.
func insertAuthorLLMCall(
	ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions, now time.Time, runID int64,
	kind models.AuthorLLMCallKind, items int, mode string, bound *authorLLMCallBound, lease time.Duration,
) (int64, time.Time, error) {
	var out struct {
		ID             int64     `pg:"id"`
		LeaseExpiresAt time.Time `pg:"lease_expires_at"`
	}
	_, err := tx.QueryOneContext(ctx, &out, `
		INSERT INTO author_llm_call (config_version, slot, run_id, kind, items, output_mode, prompt_bound_bytes,
			output_ceiling, reserved_tokens, check_seq, lease_owner, lease_expires_at, created_at)
		VALUES (?0, ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10::uuid, ?11::timestamptz + ?12 * interval '1 microsecond', ?11)
		RETURNING id, lease_expires_at`,
		opts.ConfigVersion, string(opts.Slot), runID, string(kind), items, mode, bound.promptBytes, bound.ceiling,
		bound.reserve, bound.checkSeq, opts.Owner, now, micros(lease))
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("recording an LLM call: %w", err)
	}
	return out.ID, out.LeaseExpiresAt, nil
}

// openAuthorLLMAttempts claims the jobs for the call and opens one attempt
// per job, keyed "1".."n" in the call.
func openAuthorLLMAttempts(ctx context.Context, tx *pg.Tx, callID int64, jobIDs []int64, now time.Time) ([]AuthorLLMClaimedJob, error) {
	jobs := make([]AuthorLLMClaimedJob, 0, len(jobIDs))
	for i, id := range jobIDs {
		key := strconv.Itoa(i + 1)
		var job AuthorLLMClaimedJob
		_, err := tx.QueryOneContext(ctx, &job, `
			WITH claimed AS (
				UPDATE author_llm_job SET status = 'claimed', call_id = ?1, attempt_count = attempt_count + 1
				WHERE id = ?0 AND status = 'pending'
				RETURNING *
			), opened AS (
				INSERT INTO author_llm_attempt (job_id, attempt_no, call_id, item_key, created_at)
				SELECT id, attempt_count, ?1, ?2, ?3 FROM claimed
				RETURNING id, job_id
			)
			SELECT c.id, o.id AS attempt_id, c.attempt_count, c.purpose, c.input, c.with_context, c.context_state,
				c.context_book_id, c.context_sha256, c.source_fingerprint, c.extractor_version, c.eval_item_id,
				e.context_book_id AS eval_book_id, e.book_md5 AS eval_book_md5,
				e.context_sha256 AS eval_context_sha256, e.source_fingerprint AS eval_fingerprint
			FROM claimed c
			JOIN opened o ON o.job_id = c.id
			LEFT JOIN author_llm_eval_item e ON e.id = c.eval_item_id`,
			id, callID, key, now)
		if err != nil {
			return nil, fmt.Errorf("claiming an LLM job: %w", err)
		}
		job.ItemKey = key
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// claimAuthorLLMFingerprint claims the fixed fingerprint request of a due
// check. It counts against the run's budget like any call. The check gets the
// participant's next sequence number and becomes its one check in flight:
// only that call's result is applied, and it covers the answers finished
// before now, the moment it is sent.
func claimAuthorLLMFingerprint(
	ctx context.Context, tx *pg.Tx, opts *AuthorLLMClaimOptions, now time.Time,
	run *claimRun, ref *AuthorLLMFingerprintReference, expected []string,
) (*AuthorLLMClaim, AuthorLLMClaimRefusal, error) {
	bound := authorLLMCallBound{
		promptBytes: llmreq.RequestBoundBytes(1, 0),
		ceiling:     opts.Output.Ceiling(1),
		reserve:     opts.Output.Reserve(1, 0),
	}
	if refusal, err := authorLLMBudgetRefusal(ctx, tx, opts, run, bound.reserve); err != nil || refusal != "" {
		return nil, refusal, err
	}
	var seq int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&seq), `
		SELECT check_seq + 1 FROM author_llm_provider_state WHERE config_version = ?0 AND slot = ?1`,
		opts.ConfigVersion, string(opts.Slot)); err != nil {
		return nil, "", fmt.Errorf("numbering a fingerprint check: %w", err)
	}
	bound.checkSeq = &seq
	mode := ref.OutputMode
	if mode == "" {
		mode = AuthorLLMDefaultOutputMode
	}
	callID, leaseExpires, err := insertAuthorLLMCall(ctx, tx, opts, now, run.ID, models.AuthorLLMCallFingerprint,
		1, mode, &bound, opts.Lease(1))
	if err != nil {
		return nil, "", err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE author_llm_provider_state SET check_seq = ?2, check_call_id = ?3
		WHERE config_version = ?0 AND slot = ?1`, opts.ConfigVersion, string(opts.Slot), seq, callID); err != nil {
		return nil, "", fmt.Errorf("recording the check in flight: %w", err)
	}
	return &AuthorLLMClaim{
		CallID: callID, Kind: models.AuthorLLMCallFingerprint, RunID: run.ID, OutputMode: mode,
		PromptBoundBytes: bound.promptBytes, OutputCeiling: bound.ceiling,
		LeaseExpiresAt: leaseExpires, Now: now, ExpectedModels: expected, Reference: ref,
	}, "", nil
}

// abandonExpiredAuthorLLMCalls closes every call whose lease ran out: the
// process that sent it died or hung between the request and its settle. The
// call keeps its reserve — the hard bound of what it could cost — as spent
// (settled = reserved), its attempts are
// closed as abandoned, and its jobs go back to the queue — or fail, when that
// was their last attempt. Jobs that end here get their verdicts.
func abandonExpiredAuthorLLMCalls(ctx context.Context, tx *pg.Tx, now time.Time, maxAttempts int) error {
	var ended []int64
	_, err := tx.QueryContext(ctx, &ended, `
		WITH expired AS (
			SELECT id FROM author_llm_call
			WHERE finished_at IS NULL AND lease_expires_at < ?0
			FOR UPDATE SKIP LOCKED
		), closed AS (
			UPDATE author_llm_call c
			SET finished_at = ?0, outcome = 'abandoned', error_class = ?2, settled_tokens = c.reserved_tokens,
				lease_owner = NULL, lease_expires_at = NULL
			FROM expired e WHERE c.id = e.id
			RETURNING c.id
		), attempts AS (
			UPDATE author_llm_attempt a SET finished_at = ?0, outcome = 'abandoned'
			FROM closed WHERE a.call_id = closed.id AND a.finished_at IS NULL
			RETURNING a.job_id
		), checks AS (
			-- An abandoned check is no longer in flight; the next claim
			-- sends another.
			UPDATE author_llm_provider_state p SET check_call_id = NULL
			FROM closed WHERE p.check_call_id = closed.id
			RETURNING p.slot
		), requeued AS (
			UPDATE author_llm_job j
			SET call_id = NULL, failure_count = j.failure_count + 1, last_error_class = ?2,
				status = CASE WHEN j.failure_count + 1 >= ?1 THEN 'failed' ELSE 'pending' END,
				finished_at = CASE WHEN j.failure_count + 1 >= ?1 THEN ?0::timestamptz END,
				next_attempt_at = ?0
			FROM attempts WHERE j.id = attempts.job_id AND j.status = 'claimed'
			RETURNING j.id, j.status
		)
		SELECT id FROM requeued WHERE status = 'failed'`,
		now, maxAttempts, authorLLMLeaseExpired)
	if err != nil {
		return fmt.Errorf("closing abandoned LLM calls: %w", err)
	}
	return writeAuthorLLMVerdicts(ctx, tx, ended, now)
}
