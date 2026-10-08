package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// Lease, claim, heartbeat, fencing and retry primitives for the two durable
// streams of the author metadata pipeline: extraction run items and local
// normalization jobs (contract 3.8). Both tables carry the same lease columns,
// so one private SQL contract serves both; the public functions are
// stage-specific and the stage picks a vetted descriptor, never a table name
// from a caller.
//
// Every time comparison uses the PostgreSQL clock. Owners are opaque random
// tokens. A claim is its own short transaction: it commits before it returns,
// so whatever I/O the worker does next holds no row lock.

// LeaseStream names one leased stream. The set is closed.
type LeaseStream string

const (
	LeaseStreamExtraction         LeaseStream = "extraction"
	LeaseStreamLocalNormalization LeaseStream = "local_normalization"
)

// Error classes the lease layer writes itself.
const (
	// LeaseErrorLeaseExpired closes an attempt whose owner let the lease run
	// out; the row was claimed again.
	LeaseErrorLeaseExpired = "lease_expired"
	// LeaseErrorMaxAttemptsExceeded closes the last attempt of a row that ran
	// out of attempts.
	LeaseErrorMaxAttemptsExceeded = "max_attempts_exceeded"
)

var (
	// ErrUnknownLeaseStream marks a stream outside the closed set.
	ErrUnknownLeaseStream = errors.New("database: unknown lease stream")
	// ErrInvalidLeaseOptions marks a non-positive lease duration or attempt
	// limit. A non-positive claim limit is authornorm.ErrInvalidClaimLimit.
	ErrInvalidLeaseOptions = errors.New("database: lease duration and max attempts must be positive")
	// ErrInvalidLeaseOwner marks an owner that is not a lease token.
	ErrInvalidLeaseOwner = errors.New("database: lease owner is not a token from NewLeaseOwner")
	// ErrInvalidLeaseFailure marks a failure without a closed error class, a
	// positive retry delay or a positive attempt limit.
	ErrInvalidLeaseFailure = errors.New("database: lease failure needs a closed error class, a positive delay and max attempts")
	// ErrInvalidLeaseCompletion marks a completion the stream cannot end in.
	ErrInvalidLeaseCompletion = errors.New("database: invalid lease completion")
	// ErrLeaseAttemptMissing marks a leased row without its open attempt: the
	// lease contract was broken by something other than this layer.
	ErrLeaseAttemptMissing = errors.New("database: leased row has no open attempt")
)

// errorClassPattern is the closed error-class shape the schema enforces.
var errorClassPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidLeaseErrorClass reports whether class has the closed error-class shape.
func ValidLeaseErrorClass(class string) bool {
	return errorClassPattern.MatchString(class)
}

// NewLeaseOwner returns a fresh opaque owner token.
func NewLeaseOwner() string {
	return uuid.NewString()
}

// LeaseClaimOptions bounds one claim.
type LeaseClaimOptions struct {
	// Limit is the most rows one claim takes; it must be positive.
	Limit int
	// Lease is how long the claimed rows stay the owner's without a heartbeat.
	Lease time.Duration
	// MaxAttempts is how many attempts a row gets. A row whose last attempt
	// was abandoned is ended on its stream's terminal path instead of claimed.
	MaxAttempts int
}

// LeaseClaim is one claimed row.
type LeaseClaim struct {
	ID             int64
	AttemptNo      int
	LeaseExpiresAt time.Time
}

// LeaseFailure describes a failed attempt.
type LeaseFailure struct {
	// ErrorClass is a closed class, never free error text.
	ErrorClass string
	// RetryAfter is the delay before the row is ready again, measured from
	// the database clock; the caller's retry policy computes it.
	RetryAfter time.Duration
	// MaxAttempts ends the row on its terminal path once the failed attempt
	// was the last one.
	MaxAttempts int
}

// The leased tables. Identifiers, never values: they are spliced into SQL.
const (
	runItemTable        = "author_metadata_run_item"
	runItemAttemptTable = "author_metadata_run_item_attempt"
	runItemAttemptFK    = "run_item_id"
	localJobTable       = "contributor_normalization_job"
	localJobAttempts    = "contributor_normalization_job_attempt"
	localJobAttemptFK   = "job_id"
)

// leaseSpec is the vetted description of one stream. Every field is a
// constant of this file; none comes from a caller.
type leaseSpec struct {
	stream       LeaseStream
	table        string
	attemptTable string
	attemptFK    string
	// claimable is the stream's pause rule, a predicate over row j.
	claimable string
	// exhaust sets the stream's terminal status for a row out of attempts.
	exhaust string
	// exhaustOutcome is the outcome recorded on that row's last attempt.
	exhaustOutcome string
	// retry sets the stream's extra columns for a scheduled retry, inside
	// fail's statement, where ?4 is the failure's error class.
	retry string
}

var extractionLeaseSpec = &leaseSpec{
	stream:       LeaseStreamExtraction,
	table:        runItemTable,
	attemptTable: runItemAttemptTable,
	attemptFK:    runItemAttemptFK,
	// Only items of a running run: pending has not started, paused stopped.
	claimable:      `j.run_id IN (SELECT id FROM author_metadata_run WHERE status = 'running')`,
	exhaust:        `status = 'metadata_parse_failed'`,
	exhaustOutcome: string(models.AuthorMetadataRunItemMetadataParseFailed),
	retry:          ``,
}

var localLeaseSpec = &leaseSpec{
	stream:       LeaseStreamLocalNormalization,
	table:        localJobTable,
	attemptTable: localJobAttempts,
	attemptFK:    localJobAttemptFK,
	// Local jobs belong to no run; pausing the active run pauses the whole
	// pipeline.
	claimable:      `NOT EXISTS (SELECT 1 FROM author_metadata_run WHERE status = 'paused')`,
	exhaust:        `status = 'failed', last_error_class = '` + LeaseErrorMaxAttemptsExceeded + `'`,
	exhaustOutcome: string(models.NormalizationJobFailed),
	retry:          `, last_error_class = ?4`,
}

func leaseSpecFor(stream LeaseStream) (*leaseSpec, error) {
	switch stream {
	case LeaseStreamExtraction:
		return extractionLeaseSpec, nil
	case LeaseStreamLocalNormalization:
		return localLeaseSpec, nil
	}
	return nil, ErrUnknownLeaseStream
}

// ClaimExtractionItems leases up to opts.Limit ready items of the running run.
func ClaimExtractionItems(ctx context.Context, db *pg.DB, owner string, opts LeaseClaimOptions) ([]LeaseClaim, error) {
	return claim(ctx, db, LeaseStreamExtraction, owner, opts)
}

// ClaimLocalNormalizationJobs leases up to opts.Limit ready local jobs unless
// the pipeline is paused.
func ClaimLocalNormalizationJobs(ctx context.Context, db *pg.DB, owner string, opts LeaseClaimOptions) ([]LeaseClaim, error) {
	return claim(ctx, db, LeaseStreamLocalNormalization, owner, opts)
}

// HeartbeatExtractionItem extends the owner's lease on an item.
func HeartbeatExtractionItem(ctx context.Context, db pg.DBI, id int64, owner string, lease time.Duration) error {
	return heartbeat(ctx, db, extractionLeaseSpec, id, owner, lease, nil)
}

// HeartbeatLocalNormalizationJob extends the owner's lease on a local job.
func HeartbeatLocalNormalizationJob(ctx context.Context, db pg.DBI, id int64, owner string, lease time.Duration) error {
	return heartbeat(ctx, db, localLeaseSpec, id, owner, lease, nil)
}

// FailExtractionItem records a failed attempt of an item: a retry after the
// delay, or, after the last attempt, the terminal metadata_parse_failed with
// class max_attempts_exceeded on that attempt. It reports whether the item
// ran out of attempts.
func FailExtractionItem(ctx context.Context, db pg.DBI, id int64, owner string, f LeaseFailure) (bool, error) {
	return fail(ctx, db, extractionLeaseSpec, id, owner, f, nil)
}

// FailLocalNormalizationJob records a failed attempt of a local job: a retry
// after the delay, or, after the last attempt, status failed with class
// max_attempts_exceeded.
func FailLocalNormalizationJob(ctx context.Context, db pg.DBI, id int64, owner string, f LeaseFailure) (bool, error) {
	return fail(ctx, db, localLeaseSpec, id, owner, f, nil)
}

// CompleteExtractionItem ends an item in a terminal status, with the snapshot
// the successful statuses point at. Pass the transaction that wrote the
// snapshot, so both commit together.
func CompleteExtractionItem(
	ctx context.Context,
	db pg.DBI,
	id int64,
	owner string,
	status models.AuthorMetadataRunItemStatus,
	snapshotID *int64,
) error {
	return completeExtraction(ctx, db, id, owner, status, snapshotID, nil)
}

// CompleteLocalNormalizationJob ends a local job with the result of its key.
// Pass the transaction that wrote the result.
func CompleteLocalNormalizationJob(ctx context.Context, db pg.DBI, id int64, owner string, resultID int64) error {
	return completeLocal(ctx, db, id, owner, resultID, nil)
}

func validOwner(owner string) bool {
	_, err := uuid.Parse(owner)
	return err == nil
}

func micros(d time.Duration) int64 { return d.Microseconds() }

// The operation clock. Every statement below reads the time once, through
// this CTE, and compares and schedules against that one instant. It is
// PostgreSQL's clock_timestamp() — the server's time when the statement runs —
// not now(), which is the start of the caller's transaction: a transaction
// opened before a lease expired must not keep it alive. Tests pass a fixed
// instant read from the server to probe the boundary exactly; production
// passes nil. MATERIALIZED keeps the volatile clock to a single evaluation.
const operationClock = `clock AS MATERIALIZED (
			SELECT coalesce(?%d::timestamptz, clock_timestamp()) AS at)`

func clockCTE(param int) string { return fmt.Sprintf(operationClock, param) }

// Where each statement takes its operation-clock parameter: after its own.
const (
	exhaustClockParam   = 1
	claimClockParam     = 4
	heartbeatClockParam = 3
	failClockParam      = 5
)

// claim validates, then runs the claim as one short transaction.
func claim(ctx context.Context, db *pg.DB, stream LeaseStream, owner string, opts LeaseClaimOptions) ([]LeaseClaim, error) {
	if err := validClaim(owner, opts); err != nil {
		return nil, err
	}
	spec, err := leaseSpecFor(stream)
	if err != nil {
		return nil, err
	}
	var claims []LeaseClaim
	err = db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var claimErr error
		claims, claimErr = claimLeases(ctx, tx, spec, owner, opts, nil)
		return claimErr
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

func validClaim(owner string, opts LeaseClaimOptions) error {
	if opts.Limit <= 0 {
		return authornorm.ErrInvalidClaimLimit
	}
	if opts.Lease <= 0 || opts.MaxAttempts <= 0 {
		return ErrInvalidLeaseOptions
	}
	if !validOwner(owner) {
		return ErrInvalidLeaseOwner
	}
	return nil
}

// claimLeases is the claim inside the caller's transaction, at the operation
// clock (at overrides it in tests). Rows locked by another claim are skipped,
// not waited for. A row whose lease ran out had an abandoned attempt; that
// attempt is closed as lease_expired before the new one opens. Rows out of
// attempts are ended first, so none stays pending unseen.
func claimLeases(
	ctx context.Context,
	tx *pg.Tx,
	spec *leaseSpec,
	owner string,
	opts LeaseClaimOptions,
	at *time.Time,
) ([]LeaseClaim, error) {
	if err := validClaim(owner, opts); err != nil {
		return nil, err
	}
	if err := endExhausted(ctx, tx, spec, opts.MaxAttempts, at); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		WITH %[6]s, ready AS (
			SELECT j.id FROM %[1]s j, clock c
			WHERE j.status = 'pending'
				AND j.next_attempt_at <= c.at
				AND (j.lease_expires_at IS NULL OR j.lease_expires_at <= c.at)
				AND j.attempt_count < ?0
				AND %[3]s
			ORDER BY j.next_attempt_at, j.id
			LIMIT ?1
			FOR UPDATE OF j SKIP LOCKED
		), abandoned AS (
			UPDATE %[2]s a
			SET finished_at = c.at, error_class = '%[5]s'
			FROM ready r, clock c
			WHERE a.%[4]s = r.id AND a.finished_at IS NULL
			RETURNING a.id
		), claimed AS (
			UPDATE %[1]s j
			SET lease_owner = ?2::uuid, lease_expires_at = c.at + ?3 * interval '1 microsecond',
				attempt_count = j.attempt_count + 1
			FROM ready r, clock c
			WHERE j.id = r.id
			RETURNING j.id, j.attempt_count, j.lease_expires_at, j.next_attempt_at
		), opened AS (
			INSERT INTO %[2]s (%[4]s, attempt_no, lease_owner, started_at)
			SELECT cl.id, cl.attempt_count, ?2::uuid, c.at FROM claimed cl, clock c
			RETURNING %[4]s
		)
		SELECT cl.id, cl.attempt_count AS attempt_no, cl.lease_expires_at
		FROM claimed cl
		ORDER BY cl.next_attempt_at, cl.id`,
		spec.table, spec.attemptTable, spec.claimable, spec.attemptFK, LeaseErrorLeaseExpired, clockCTE(claimClockParam))

	var claims []LeaseClaim
	if _, err := tx.QueryContext(ctx, &claims, query,
		opts.MaxAttempts, opts.Limit, owner, micros(opts.Lease), at); err != nil {
		return nil, fmt.Errorf("claiming %s rows: %w", spec.stream, err)
	}
	return claims, nil
}

// endExhausted ends the rows whose last attempt was abandoned — out of
// attempts, lease run out — on the stream's terminal path and closes that
// attempt, so no row stays pending where no claim will ever take it. Rows a
// concurrent claim holds are skipped; the pause rule applies here too.
//
// A row out of attempts with no open attempt (the limit was lowered after its
// last attempt failed and closed) is ended as well: its history is already
// complete, and leaving it pending would hide it.
func endExhausted(ctx context.Context, tx *pg.Tx, spec *leaseSpec, maxAttempts int, at *time.Time) error {
	exhaust := fmt.Sprintf(`
		WITH %[8]s, exhausted AS (
			UPDATE %[1]s j
			SET %[3]s, lease_owner = NULL, lease_expires_at = NULL, finished_at = c.at
			FROM clock c
			WHERE j.id IN (
				SELECT j.id FROM %[1]s j, clock c
				WHERE j.status = 'pending'
					AND j.attempt_count >= ?0
					AND (j.lease_expires_at IS NULL OR j.lease_expires_at <= c.at)
					AND %[7]s
				FOR UPDATE OF j SKIP LOCKED)
			RETURNING j.id
		)
		UPDATE %[2]s a
		SET finished_at = c.at, outcome = '%[5]s', error_class = '%[6]s'
		FROM exhausted e, clock c
		WHERE a.%[4]s = e.id AND a.finished_at IS NULL`,
		spec.table, spec.attemptTable, spec.exhaust, spec.attemptFK, spec.exhaustOutcome, LeaseErrorMaxAttemptsExceeded,
		spec.claimable, clockCTE(exhaustClockParam))
	if _, err := tx.ExecContext(ctx, exhaust, maxAttempts, at); err != nil {
		return fmt.Errorf("ending %s rows out of attempts: %w", spec.stream, err)
	}
	return nil
}

// heartbeat extends a lease held by owner up to and including its expiry
// instant, measured on the operation clock.
func heartbeat(
	ctx context.Context,
	db pg.DBI,
	spec *leaseSpec,
	id int64,
	owner string,
	lease time.Duration,
	at *time.Time,
) error {
	if lease <= 0 {
		return ErrInvalidLeaseOptions
	}
	if !validOwner(owner) {
		return ErrInvalidLeaseOwner
	}
	res, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %[2]s
		UPDATE %[1]s j
		SET lease_expires_at = c.at + ?0 * interval '1 microsecond'
		FROM clock c
		WHERE j.id = ?1 AND j.status = 'pending' AND j.lease_owner = ?2::uuid AND j.lease_expires_at >= c.at`,
		spec.table, clockCTE(heartbeatClockParam)),
		micros(lease), id, owner, at)
	if err != nil {
		return fmt.Errorf("extending a %s lease: %w", spec.stream, err)
	}
	if res.RowsAffected() == 0 {
		return authornorm.ErrLeaseLost
	}
	return nil
}

// leaseHold is the shared head of fail and complete: the operation clock, and
// the fenced row — owner, pending, lease not yet expired on that clock —
// locked, with its open attempt if there is one. The fence sits on the locked
// row itself, so a concurrent reclaim is re-checked after the lock wait. ?0 is
// the row ID, ?1 the owner.
const leaseHold = `%[3]s, held AS (
			SELECT j.id, j.attempt_count, a.id AS attempt_id
			FROM %[1]s j
			CROSS JOIN clock c
			LEFT JOIN %[2]s a
				ON a.%[4]s = j.id AND a.attempt_no = j.attempt_count AND a.finished_at IS NULL
			WHERE j.id = ?0 AND j.status = 'pending' AND j.lease_owner = ?1::uuid AND j.lease_expires_at >= c.at
			FOR UPDATE OF j
		), moving AS (
			SELECT * FROM held WHERE attempt_id IS NOT NULL
		)`

// holdOutcome reads what a fail/complete statement saw: whether the lease was
// held, and whether its open attempt was there. Nothing moves without both.
type holdOutcome struct {
	Held      int
	Attempted int
	Exhausted bool
}

func (o holdOutcome) err() error {
	switch {
	case o.Held == 0:
		return authornorm.ErrLeaseLost
	case o.Attempted == 0:
		return ErrLeaseAttemptMissing
	}
	return nil
}

// fail closes the owner's attempt with the error class and either schedules
// the retry or, after the last attempt, ends the row on the stream's terminal
// path. One statement, so the row and its attempt change together or not at
// all; a held lease without its open attempt changes nothing.
func fail(
	ctx context.Context,
	db pg.DBI,
	spec *leaseSpec,
	id int64,
	owner string,
	f LeaseFailure,
	at *time.Time,
) (bool, error) {
	if !ValidLeaseErrorClass(f.ErrorClass) || f.RetryAfter <= 0 || f.MaxAttempts <= 0 {
		return false, ErrInvalidLeaseFailure
	}
	if !validOwner(owner) {
		return false, ErrInvalidLeaseOwner
	}

	head := fmt.Sprintf(leaseHold, spec.table, spec.attemptTable, clockCTE(failClockParam), spec.attemptFK)
	query := fmt.Sprintf(`
		WITH %[1]s, retried AS (
			UPDATE %[2]s j
			SET lease_owner = NULL, lease_expires_at = NULL,
				next_attempt_at = c.at + ?3 * interval '1 microsecond' %[5]s
			FROM moving m, clock c
			WHERE j.id = m.id AND m.attempt_count < ?2
			RETURNING j.id
		), ended AS (
			UPDATE %[2]s j
			SET %[6]s, lease_owner = NULL, lease_expires_at = NULL, finished_at = c.at
			FROM moving m, clock c
			WHERE j.id = m.id AND m.attempt_count >= ?2
			RETURNING j.id
		), closed AS (
			UPDATE %[3]s a
			SET finished_at = c.at,
				outcome = CASE WHEN m.attempt_count >= ?2 THEN '%[7]s' END,
				error_class = CASE WHEN m.attempt_count >= ?2 THEN '%[8]s' ELSE ?4 END
			FROM moving m, clock c
			WHERE a.id = m.attempt_id
			RETURNING a.id
		)
		SELECT (SELECT count(*) FROM held) AS held,
			(SELECT count(*) FROM moving) AS attempted,
			coalesce((SELECT bool_or(attempt_count >= ?2) FROM moving), false) AS exhausted`,
		head, spec.table, spec.attemptTable, spec.attemptFK,
		spec.retry, spec.exhaust, spec.exhaustOutcome, LeaseErrorMaxAttemptsExceeded)

	var out holdOutcome
	if _, err := db.QueryOneContext(ctx, &out, query,
		id, owner, f.MaxAttempts, micros(f.RetryAfter), f.ErrorClass, at); err != nil {
		return false, fmt.Errorf("recording a failed %s attempt: %w", spec.stream, err)
	}
	if err := out.err(); err != nil {
		return false, err
	}
	return out.Exhausted, nil
}

func completeExtraction(
	ctx context.Context,
	db pg.DBI,
	id int64,
	owner string,
	status models.AuthorMetadataRunItemStatus,
	snapshotID *int64,
	at *time.Time,
) error {
	if !status.IsTerminal() {
		return fmt.Errorf("%w: %q is not a terminal item status", ErrInvalidLeaseCompletion, status)
	}
	return complete(ctx, db, extractionLeaseSpec, id, owner, `status = ?3, snapshot_id = ?4`,
		string(status), []interface{}{string(status), snapshotID}, at)
}

func completeLocal(ctx context.Context, db pg.DBI, id int64, owner string, resultID int64, at *time.Time) error {
	if resultID <= 0 {
		return fmt.Errorf("%w: result ID must be positive", ErrInvalidLeaseCompletion)
	}
	return complete(ctx, db, localLeaseSpec, id, owner, `status = 'completed', result_id = ?3`,
		string(models.NormalizationJobCompleted), []interface{}{resultID}, at)
}

// complete ends a leased row successfully and closes its attempt with the
// outcome, in one statement: both change or neither does. set assigns the
// stream's terminal columns from the stream parameters, which start at ?3
// (?0 is the row, ?1 the owner, ?2 the outcome).
func complete(
	ctx context.Context,
	db pg.DBI,
	spec *leaseSpec,
	id int64,
	owner string,
	set string,
	outcome string,
	params []interface{},
	at *time.Time,
) error {
	if !validOwner(owner) {
		return ErrInvalidLeaseOwner
	}
	clockParam := completeStreamParams + len(params)
	head := fmt.Sprintf(leaseHold, spec.table, spec.attemptTable, clockCTE(clockParam), spec.attemptFK)
	query := fmt.Sprintf(`
		WITH %[1]s, done AS (
			UPDATE %[2]s j
			SET %[5]s, lease_owner = NULL, lease_expires_at = NULL, finished_at = c.at
			FROM moving m, clock c
			WHERE j.id = m.id
			RETURNING j.id
		), closed AS (
			UPDATE %[3]s a
			SET finished_at = c.at, outcome = ?2
			FROM moving m, clock c
			WHERE a.id = m.attempt_id
			RETURNING a.id
		)
		SELECT (SELECT count(*) FROM held) AS held, (SELECT count(*) FROM moving) AS attempted`,
		head, spec.table, spec.attemptTable, spec.attemptFK, set)

	args := append(append([]interface{}{id, owner, outcome}, params...), at)
	var out holdOutcome
	if _, err := db.QueryOneContext(ctx, &out, query, args...); err != nil {
		return fmt.Errorf("completing a %s row: %w", spec.stream, err)
	}
	return out.err()
}

// completeStreamParams is where a completion's stream parameters start.
const completeStreamParams = 3
