package services

// The statements the authornorm-local-v1 binary (commit 3150c8c) runs on the
// local queue, rendered from its database/author_metadata_jobs.go with its
// local lease spec: the claim (claimLeases) and the exhaustion sweep that
// precedes it (endExhausted); and its run-admin retry of local jobs. That binary keeps running next to the new one
// in a rolling update and again after a rollback, and it cannot be changed:
// these strings are what the new queue must stay invisible to.

// v1LocalClaimSQL: ?0 max attempts, ?1 limit, ?2 owner, ?3 lease in
// microseconds, ?4 the clock (NULL: now).
const v1LocalClaimSQL = `
		WITH clock AS MATERIALIZED (
			SELECT coalesce(?4::timestamptz, clock_timestamp()) AS at), ready AS (
			SELECT j.id FROM contributor_normalization_job j, clock c
			WHERE j.status = 'pending'
				AND j.next_attempt_at <= c.at
				AND (j.lease_expires_at IS NULL OR j.lease_expires_at <= c.at)
				AND j.attempt_count < ?0
				AND NOT EXISTS (SELECT 1 FROM author_metadata_run WHERE status = 'paused')
			ORDER BY j.next_attempt_at, j.id
			LIMIT ?1
			FOR UPDATE OF j SKIP LOCKED
		), abandoned AS (
			UPDATE contributor_normalization_job_attempt a
			SET finished_at = c.at, error_class = 'lease_expired'
			FROM ready r, clock c
			WHERE a.job_id = r.id AND a.finished_at IS NULL
			RETURNING a.id
		), claimed AS (
			UPDATE contributor_normalization_job j
			SET lease_owner = ?2::uuid, lease_expires_at = c.at + ?3 * interval '1 microsecond',
				attempt_count = j.attempt_count + 1
			FROM ready r, clock c
			WHERE j.id = r.id
			RETURNING j.id, j.attempt_count, j.lease_expires_at, j.next_attempt_at
		), opened AS (
			INSERT INTO contributor_normalization_job_attempt (job_id, attempt_no, lease_owner, started_at)
			SELECT cl.id, cl.attempt_count, ?2::uuid, c.at FROM claimed cl, clock c
			RETURNING job_id
		)
		SELECT cl.id, cl.attempt_count AS attempt_no, cl.lease_expires_at
		FROM claimed cl
		ORDER BY cl.next_attempt_at, cl.id`

// v1LocalExhaustSQL: ?0 max attempts, ?1 the clock (NULL: now).
const v1LocalExhaustSQL = `
		WITH clock AS MATERIALIZED (
			SELECT coalesce(?1::timestamptz, clock_timestamp()) AS at), exhausted AS (
			UPDATE contributor_normalization_job j
			SET status = 'failed', last_error_class = 'max_attempts_exceeded', lease_owner = NULL, lease_expires_at = NULL, finished_at = c.at
			FROM clock c
			WHERE j.id IN (
				SELECT j.id FROM contributor_normalization_job j, clock c
				WHERE j.status = 'pending'
					AND j.attempt_count >= ?0
					AND (j.lease_expires_at IS NULL OR j.lease_expires_at <= c.at)
					AND NOT EXISTS (SELECT 1 FROM author_metadata_run WHERE status = 'paused')
				FOR UPDATE OF j SKIP LOCKED)
			RETURNING j.id
		)
		UPDATE contributor_normalization_job_attempt a
		SET finished_at = c.at, outcome = 'failed', error_class = 'max_attempts_exceeded'
		FROM exhausted e, clock c
		WHERE a.job_id = e.id AND a.finished_at IS NULL`

// v1ReopenLocalSQL is the v1 binary's run-admin retry of the local stream
// (reopenLocalSQL with its runJobsCTE, database/author_metadata_run_admin.go
// and author_metadata_stats.go at 3150c8c): it reopens a run's failed jobs of
// the run's own normalizer version as pending. ?0 run, ?1 class, ?2 budget.
const v1ReopenLocalSQL = `WITH run_credits AS (
	SELECT c.id, c.source_fingerprint, s.extractor_version
	FROM author_metadata_run r
	JOIN author_metadata_run_item ri ON ri.run_id = r.id
	JOIN book_metadata_snapshot s ON s.book_id = ri.book_id AND s.is_current
		AND s.extractor_version = r.extractor_version
	JOIN book_contributor_credit c ON c.snapshot_id = s.id AND c.role = 'author'
	WHERE r.id = ?0), run_jobs AS (
	SELECT j.* FROM contributor_normalization_job j
	WHERE j.normalizer_version = (SELECT normalizer_version FROM author_metadata_run WHERE id = ?0)
		AND EXISTS (SELECT 1 FROM run_credits rc
			WHERE rc.source_fingerprint = j.source_fingerprint AND rc.extractor_version = j.extractor_version))
UPDATE contributor_normalization_job j
SET status = 'pending', last_error_class = NULL, lease_owner = NULL, lease_expires_at = NULL,
	next_attempt_at = clock_timestamp(), finished_at = NULL
WHERE j.id IN (SELECT id FROM run_jobs) AND j.status = 'failed' AND j.attempt_count < ?2
	AND (j.last_error_class = ?1 OR EXISTS (SELECT 1 FROM contributor_normalization_job_attempt a
		WHERE a.job_id = j.id AND a.error_class = ?1))`
