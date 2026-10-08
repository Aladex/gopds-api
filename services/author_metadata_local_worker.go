package services

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/logging"

	"github.com/go-pg/pg/v10"
)

// The local normalization worker (plan phase 10): claim local jobs, load the
// canonical source each one stands for, normalize it in pure code, decide it
// against the acceptance policy, and persist the result, the job completion
// and the resolution of every applicable credit in one transaction after the
// computation. Where a non-selected credit goes is scope amendment A3; the
// precedence of overrides over the automatic outcome lives in the database
// resolver, which this worker only feeds. There is no next stage to enqueue:
// no LLM stream exists (A1).
//
// Logs carry IDs, versions, decision classes and closed error classes only,
// never a name or other source text (contract 3.14).

// Closed classes the local worker records when it refuses or fails a job.
const (
	// AuthorMetadataErrorNoAuthorCredit refuses a job no author credit stands
	// behind, such as one seeded for a translator: translators never enter
	// the local stream.
	AuthorMetadataErrorNoAuthorCredit AuthorMetadataErrorClass = "no_author_credit"
	// AuthorMetadataErrorSourceNotCanonical refuses a job whose stored source
	// does not rebuild into the canonical value of the job's fingerprint.
	AuthorMetadataErrorSourceNotCanonical AuthorMetadataErrorClass = "source_not_canonical"
	// AuthorMetadataErrorNormalizerVersionMismatch refuses a job of another
	// normalizer version than the worker runs, or a result whose version or
	// key is not the job's.
	AuthorMetadataErrorNormalizerVersionMismatch AuthorMetadataErrorClass = "normalizer_version_mismatch"
)

// AuthorMetadataLocalWorkerErrorClasses lists every class the local worker
// may record on an attempt, besides the ones the lease layer writes.
func AuthorMetadataLocalWorkerErrorClasses() []AuthorMetadataErrorClass {
	return []AuthorMetadataErrorClass{
		AuthorMetadataErrorTransientDatabase, AuthorMetadataErrorNormalizerFailed,
		AuthorMetadataErrorNoAuthorCredit, AuthorMetadataErrorSourceNotCanonical,
		AuthorMetadataErrorNormalizerVersionMismatch,
	}
}

// ErrInvalidAuthorMetadataLocalWorkerConfig marks a configuration that would
// claim without bound, never decide, or normalize with nothing.
var ErrInvalidAuthorMetadataLocalWorkerConfig = errors.New("services: invalid author metadata local worker config")

// LocalNormalizer is the pure normalization step; authornorm.Normalize in
// production.
type LocalNormalizer func(v authornorm.SourceValue, extractorVersion string) (authornorm.Result, error)

// AuthorMetadataLocalWorkerConfig configures one local worker.
type AuthorMetadataLocalWorkerConfig struct {
	// ClaimLimit is how many jobs one RunOnce claims.
	ClaimLimit int
	// Lease is how long a claimed job stays the worker's.
	Lease time.Duration
	// Retry turns a failed attempt into the delay before the next one and
	// bounds the attempts.
	Retry AuthorMetadataRetryPolicy
	// PolicyVersion is the acceptance policy version decisions are made under.
	PolicyVersion int
	// NormalizerVersion is the exact version Normalize produces; jobs of any
	// other version are refused.
	NormalizerVersion string
	Normalize         LocalNormalizer
}

// Validate refuses a configuration with a non-positive limit, lease or
// policy version, an invalid retry policy, or no normalizer.
func (c *AuthorMetadataLocalWorkerConfig) Validate() error {
	if c.ClaimLimit <= 0 || c.Lease <= 0 || c.PolicyVersion <= 0 ||
		strings.TrimSpace(c.NormalizerVersion) == "" || c.Normalize == nil {
		return ErrInvalidAuthorMetadataLocalWorkerConfig
	}
	if err := c.Retry.Validate(); err != nil {
		return errors.Join(ErrInvalidAuthorMetadataLocalWorkerConfig, err)
	}
	return nil
}

// Production defaults until phase 15 makes them configurable.
const (
	defaultLocalClaimLimit  = 100
	defaultLocalLease       = time.Minute
	defaultLocalMaxAttempts = 5
	defaultLocalBaseDelay   = 5 * time.Second
	defaultLocalMaxDelay    = 5 * time.Minute
	defaultPolicyVersion    = 1
)

// DefaultAuthorMetadataLocalWorkerConfig is the shipped configuration: the
// local normalizer under the empty production policy version.
func DefaultAuthorMetadataLocalWorkerConfig() AuthorMetadataLocalWorkerConfig {
	return AuthorMetadataLocalWorkerConfig{
		ClaimLimit: defaultLocalClaimLimit,
		Lease:      defaultLocalLease,
		Retry: AuthorMetadataRetryPolicy{
			MaxAttempts: defaultLocalMaxAttempts,
			BaseDelay:   defaultLocalBaseDelay,
			MaxDelay:    defaultLocalMaxDelay,
			Jitter:      uniformJitter,
		},
		PolicyVersion:     defaultPolicyVersion,
		NormalizerVersion: authornorm.NormalizerVersion,
		Normalize:         authornorm.Normalize,
	}
}

// uniformJitter spreads retries; it is scheduling noise, not a secret.
func uniformJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(limit) + 1)) // #nosec G404 -- retry jitter needs no cryptographic randomness
}

// AuthorMetadataLocalWorker runs the local normalization stream. Replicas
// may run concurrently: claims are disjoint, results converge per key, and a
// late owner's completion is fenced off.
type AuthorMetadataLocalWorker struct {
	db    *pg.DB
	cfg   AuthorMetadataLocalWorkerConfig
	owner string
}

// NewAuthorMetadataLocalWorker validates the configuration and takes a fresh
// lease owner token.
func NewAuthorMetadataLocalWorker(db *pg.DB, cfg *AuthorMetadataLocalWorkerConfig) (*AuthorMetadataLocalWorker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &AuthorMetadataLocalWorker{db: db, cfg: *cfg, owner: database.NewLeaseOwner()}, nil
}

// LocalBatchReport counts what one RunOnce did.
type LocalBatchReport struct {
	Claimed   int
	Completed int
	// Failed counts attempts recorded as failed, retried or final.
	Failed int
	// LeaseLost counts jobs another owner took over before completion.
	LeaseLost int
	// Outcomes counts completed jobs by policy outcome.
	Outcomes map[authornorm.Outcome]int
	// Settled counts inputs of failed jobs whose credits were accounted.
	Settled int
	// Reconciled counts inputs of completed jobs whose late credits were
	// resolved from the stored result.
	Reconciled int
}

// RunOnce loads the policy, claims one batch, processes every claimed job and
// accounts the credits of jobs that ended failed. An unloadable policy stops
// the batch before any claim: no decision is made without it.
func (w *AuthorMetadataLocalWorker) RunOnce(ctx context.Context) (LocalBatchReport, error) {
	report := LocalBatchReport{Outcomes: map[authornorm.Outcome]int{}}
	policy, err := database.LoadAcceptancePolicy(ctx, w.db, w.cfg.PolicyVersion, w.cfg.NormalizerVersion)
	if err != nil {
		return report, err
	}
	claims, err := database.ClaimLocalNormalizationJobs(ctx, w.db, w.owner, database.LeaseClaimOptions{
		Limit: w.cfg.ClaimLimit, Lease: w.cfg.Lease, MaxAttempts: w.cfg.Retry.MaxAttempts,
	})
	if err != nil {
		return report, err
	}
	report.Claimed = len(claims)
	for i := range claims {
		if processErr := w.process(ctx, &policy, claims[i], &report); processErr != nil {
			return report, processErr
		}
	}
	report.Settled, err = w.settleFailed(ctx)
	if err != nil {
		return report, err
	}
	report.Reconciled, err = w.reconcile(ctx, &policy)
	return report, err
}

// settleFailed accounts the credits of inputs whose local job ended failed:
// unresolved with normalizer_failed unless an override decides them. Each
// input is settled in a transaction of its own, which holds the credit locks
// from the override read to the selection write (database.ResolutionTx).
func (w *AuthorMetadataLocalWorker) settleFailed(ctx context.Context) (int, error) {
	inputs, err := database.FailedInputsToSettle(ctx, w.db, w.cfg.NormalizerVersion, w.cfg.ClaimLimit)
	if err != nil {
		return 0, err
	}
	for i := range inputs {
		err = w.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			_, resolveErr := database.ResolveCredits(ctx, tx, inputs[i].SourceFingerprint, inputs[i].ExtractorVersion,
				&database.AutomaticOutcome{Failed: true})
			return resolveErr
		})
		if err != nil {
			return i, err
		}
	}
	return len(inputs), nil
}

// reconcile resolves credits that joined an input after its job completed.
// The key is queued once, so a later credit with the same input brings no
// job; it is decided from the key's stored result under the current policy,
// without normalizing again and without a second result. Doing this in the
// local stream rather than when the credit is persisted keeps the extraction
// transaction free of policy and resolver work, and covers backfill and live
// writes alike.
func (w *AuthorMetadataLocalWorker) reconcile(ctx context.Context, policy *authornorm.AcceptancePolicy) (int, error) {
	jobs, err := database.CompletedInputsToReconcile(ctx, w.db, w.cfg.NormalizerVersion, w.cfg.ClaimLimit)
	if err != nil {
		return 0, err
	}
	reconciled := 0
	for _, job := range jobs {
		in, loadErr := database.LoadLocalNormalizationInput(ctx, w.db, job)
		if loadErr != nil {
			return reconciled, loadErr
		}
		if in.ResultID == nil {
			continue
		}
		stored, loadErr := database.LoadLocalResult(ctx, w.db, *in.ResultID)
		if loadErr != nil {
			return reconciled, loadErr
		}
		out, decideErr := reconciledOutcome(policy, &in, &stored, *in.ResultID)
		if decideErr != nil {
			// The credits stay unaccounted, and visible as pending, rather
			// than resolved without a valid decision.
			logging.Warnf("author local job %d: stored result cannot be decided again", job)
			continue
		}
		err = w.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			_, resolveErr := database.ResolveCredits(ctx, tx, in.SourceFingerprint, in.ExtractorVersion, &out)
			return resolveErr
		})
		if err != nil {
			return reconciled, err
		}
		reconciled++
	}
	return reconciled, nil
}

// reconciledOutcome decides a stored result for the credits of its input. A
// credit with the duplicate_component flag that joined after the result was
// computed without it makes the whole input ambiguous: every credit goes to
// review with that class, the stored parse kept only as the proposal, instead
// of inheriting a decision made for the plain reading.
func reconciledOutcome(
	policy *authornorm.AcceptancePolicy,
	in *database.LocalNormalizationInput,
	stored *authornorm.Result,
	resultID int64,
) (database.AutomaticOutcome, error) {
	if in.DuplicateComponent && !slices.Contains(stored.QualityFlags, authornorm.FlagDuplicateComponent) {
		return database.AutomaticOutcome{
			ResultID:      resultID,
			DecisionClass: authornorm.ClassDuplicateComponent,
			Decision:      authornorm.Decision{Outcome: authornorm.OutcomeReview, PolicyVersion: policy.Version()},
		}, nil
	}
	d, err := policy.Decide(stored)
	if err != nil {
		return database.AutomaticOutcome{}, err
	}
	return database.AutomaticOutcome{ResultID: resultID, DecisionClass: stored.DecisionClass, Decision: d}, nil
}

// localVerdict is the pure part of one job: the result and its decision, or
// the closed class the job fails with.
type localVerdict struct {
	result   authornorm.Result
	decision authornorm.Decision
	failure  AuthorMetadataErrorClass
}

// compute rebuilds the canonical source, normalizes it and decides it. It
// touches no database.
func (w *AuthorMetadataLocalWorker) compute(policy *authornorm.AcceptancePolicy, in *database.LocalNormalizationInput) localVerdict {
	if !in.HasAuthorCredit {
		return localVerdict{failure: AuthorMetadataErrorNoAuthorCredit}
	}
	source, err := authornorm.RestoreSourceValue(in.First, in.Middle, in.Last, in.Nickname,
		in.DisplayName, in.DuplicateComponent)
	if err != nil {
		return localVerdict{failure: AuthorMetadataErrorSourceNotCanonical}
	}
	fingerprint := authornorm.SourceFingerprint(source)
	if !bytes.Equal(fingerprint[:], in.SourceFingerprint) {
		return localVerdict{failure: AuthorMetadataErrorSourceNotCanonical}
	}
	result, err := w.cfg.Normalize(source, in.ExtractorVersion)
	if err != nil {
		return localVerdict{failure: AuthorMetadataErrorNormalizerFailed}
	}
	if !bytes.Equal(result.NormalizationKey[:], in.NormalizationKey) {
		return localVerdict{failure: AuthorMetadataErrorNormalizerVersionMismatch}
	}
	decision, err := policy.Decide(&result)
	switch {
	case errors.Is(err, authornorm.ErrNormalizerVersionMismatch):
		return localVerdict{failure: AuthorMetadataErrorNormalizerVersionMismatch}
	case err != nil:
		return localVerdict{failure: AuthorMetadataErrorNormalizerFailed}
	}
	return localVerdict{result: result, decision: decision}
}

// process runs one claimed job to a recorded completion or failure. A lost
// lease is not an error of the batch: the new owner finishes the job.
func (w *AuthorMetadataLocalWorker) process(
	ctx context.Context,
	policy *authornorm.AcceptancePolicy,
	claim database.LeaseClaim,
	report *LocalBatchReport,
) error {
	in, err := database.LoadLocalNormalizationInput(ctx, w.db, claim.ID)
	if err != nil {
		return w.fail(ctx, claim, AuthorMetadataErrorTransientDatabase, report)
	}
	verdict := w.compute(policy, &in)
	if verdict.failure != "" {
		return w.fail(ctx, claim, verdict.failure, report)
	}

	err = w.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, recordErr := database.RecordLocalNormalization(ctx, tx, claim.ID, w.owner, &verdict.result, verdict.decision)
		return recordErr
	})
	switch {
	case errors.Is(err, authornorm.ErrLeaseLost):
		report.LeaseLost++
		logging.Warnf("author local job %d: lease lost, result discarded", claim.ID)
		return nil
	case err != nil:
		return w.fail(ctx, claim, AuthorMetadataErrorTransientDatabase, report)
	}
	report.Completed++
	report.Outcomes[verdict.decision.Outcome]++
	return nil
}

// fail records a failed attempt. A job that ended with it is accounted by
// SettleFailedLocalJobs at the end of the batch, the same path that accounts
// a job whose last attempt was abandoned.
func (w *AuthorMetadataLocalWorker) fail(
	ctx context.Context,
	claim database.LeaseClaim,
	class AuthorMetadataErrorClass,
	report *LocalBatchReport,
) error {
	_, err := database.FailLocalNormalizationJob(ctx, w.db, claim.ID, w.owner, w.cfg.Retry.Failure(class, claim.AttemptNo))
	if errors.Is(err, authornorm.ErrLeaseLost) {
		report.LeaseLost++
		return nil
	}
	if err != nil {
		return err
	}
	report.Failed++
	logging.Warnf("author local job %d: attempt %d failed with %s", claim.ID, claim.AttemptNo, class)
	return nil
}
