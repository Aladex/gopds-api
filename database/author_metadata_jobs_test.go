package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/internal/migrate"
	"gopds-api/internal/testdb"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lease primitives need what a rolled-back fixture transaction cannot
// give: committed rows, several connections racing for them, and claims that
// commit before the caller goes on. They also claim globally — a local
// normalization claim takes any ready job — so running them against a real
// catalog would lease its jobs. The suite therefore runs in a scratch
// database, migrated from the real files and dropped afterwards, which every
// case empties before it starts.

// jobsDB creates and migrates a scratch database for one test.
func jobsDB(t *testing.T) *pg.DB {
	t.Helper()
	requireDatabase(t)
	cfg, _ := testdb.Configured()

	name := fmt.Sprintf("author_jobs_test_%d", time.Now().UnixNano())
	_, err := db.Exec("CREATE DATABASE " + name)
	require.NoError(t, err, "creating the scratch database")
	// Registered first so it runs last, after every connection below closed.
	// FORCE also ends connections a failed test left behind.
	t.Cleanup(func() {
		if _, dropErr := db.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); dropErr != nil {
			t.Errorf("dropping the scratch database %s: %v", name, dropErr)
		}
	})

	scratch := pg.Connect(&pg.Options{
		Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name, PoolSize: 16,
	})
	t.Cleanup(func() { _ = scratch.Close() })

	_, err = migrate.Run(context.Background(), scratch, os.DirFS(".."), "database_migrations", migrate.AppBaseline())
	require.NoError(t, err, "migrating the scratch database")
	return scratch
}

// TestLeasePrimitives runs the lease suite over one scratch database: creating
// and migrating one costs over a second, so the cases share it and empty it
// between them.
func TestLeasePrimitives(t *testing.T) {
	s := jobsDB(t)
	t.Run("claim rejects invalid options", func(t *testing.T) { leaseClaimRejectsInvalidOptions(t, s) })
	t.Run("concurrent claims are disjoint", func(t *testing.T) { leaseConcurrentClaimsAreDisjoint(t, s) })
	t.Run("claim releases row locks", func(t *testing.T) { leaseClaimReleasesRowLocks(t, s) })
	t.Run("heartbeat", func(t *testing.T) { leaseHeartbeat(t, s) })
	t.Run("expiry boundary", func(t *testing.T) { leaseExpiryBoundary(t, s) })
	t.Run("stale owner cannot finish", func(t *testing.T) { leaseStaleOwnerCannotFinish(t, s) })
	t.Run("crash recovery", func(t *testing.T) { leaseCrashRecovery(t, s) })
	t.Run("pause and resume", func(t *testing.T) { leasePauseAndResume(t, s) })
	t.Run("transient failure schedules retry", func(t *testing.T) { leaseTransientFailureSchedulesRetry(t, s) })
	t.Run("max attempts", func(t *testing.T) { leaseMaxAttempts(t, s) })
	t.Run("claim order and completion order", func(t *testing.T) { leaseClaimOrderAndCompletionOrder(t, s) })
	t.Run("completion arguments", func(t *testing.T) { leaseCompletionArguments(t, s) })
	t.Run("fencing uses the fresh database clock", func(t *testing.T) { leaseFencingUsesFreshClock(t, s) })
	t.Run("missing attempt changes nothing", func(t *testing.T) { leaseMissingAttemptChangesNothing(t, s) })
	t.Run("run continues after a poisoned row", func(t *testing.T) { leaseRunContinuesAfterPoison(t, s) })
}

// leaseCase drives one leased stream through the public, stage-specific API.
type leaseCase struct {
	name         string
	table        string
	attemptTable string
	attemptFK    string
	// seed creates n ready rows whose claim order is their slice order.
	seed      func(t *testing.T, s *pg.DB, n int) []int64
	claim     func(ctx context.Context, s *pg.DB, owner string, opts LeaseClaimOptions) ([]LeaseClaim, error)
	heartbeat func(ctx context.Context, s pg.DBI, id int64, owner string, lease time.Duration) error
	fail      func(ctx context.Context, s pg.DBI, id int64, owner string, f LeaseFailure) (bool, error)
	// complete finishes a claimed row successfully.
	complete func(t *testing.T, s *pg.DB, id int64, owner string) error
	// completeIn is complete through any DBI, the caller's transaction included.
	completeIn func(t *testing.T, db pg.DBI, id int64, owner string) error
	// completeAt is completion at a given operation instant.
	completeAt func(t *testing.T, db pg.DBI, id int64, owner string, at *time.Time) error
	// terminal is the status a successful completion leaves.
	terminal string
	// exhausted is the status the stream's max-attempts path leaves.
	exhausted string
	pause     func(t *testing.T, s *pg.DB)
	resume    func(t *testing.T, s *pg.DB)
	// spec is the private descriptor, for the same-transaction boundary probes.
	spec *leaseSpec
}

func leaseCases() []leaseCase {
	return []leaseCase{
		{
			name: "extraction", table: "author_metadata_run_item",
			attemptTable: "author_metadata_run_item_attempt", attemptFK: "run_item_id",
			seed:      seedExtractionItems,
			claim:     ClaimExtractionItems,
			heartbeat: HeartbeatExtractionItem,
			fail:      FailExtractionItem,
			complete: func(t *testing.T, s *pg.DB, id int64, owner string) error {
				return CompleteExtractionItem(context.Background(), s, id, owner, models.AuthorMetadataRunItemEntryMissing, nil)
			},
			completeIn: func(t *testing.T, db pg.DBI, id int64, owner string) error {
				return CompleteExtractionItem(context.Background(), db, id, owner, models.AuthorMetadataRunItemEntryMissing, nil)
			},
			completeAt: func(t *testing.T, db pg.DBI, id int64, owner string, at *time.Time) error {
				return completeExtraction(context.Background(), db, id, owner, models.AuthorMetadataRunItemEntryMissing, nil, at)
			},
			terminal:  "entry_missing",
			exhausted: "metadata_parse_failed",
			pause: func(t *testing.T, s *pg.DB) {
				_, err := s.Exec(`UPDATE author_metadata_run SET status = 'paused' WHERE status = 'running'`)
				require.NoError(t, err)
			},
			resume: func(t *testing.T, s *pg.DB) {
				_, err := s.Exec(`UPDATE author_metadata_run SET status = 'running' WHERE status = 'paused'`)
				require.NoError(t, err)
			},
			spec: extractionLeaseSpec,
		},
		{
			name: "local normalization", table: "contributor_normalization_job",
			attemptTable: "contributor_normalization_job_attempt", attemptFK: "job_id",
			seed:      seedLocalJobs,
			claim:     ClaimLocalNormalizationJobs,
			heartbeat: HeartbeatLocalNormalizationJob,
			fail:      FailLocalNormalizationJob,
			complete: func(t *testing.T, s *pg.DB, id int64, owner string) error {
				return CompleteLocalNormalizationJob(context.Background(), s, id, owner, resultForJob(t, s, id))
			},
			completeIn: func(t *testing.T, db pg.DBI, id int64, owner string) error {
				return CompleteLocalNormalizationJob(context.Background(), db, id, owner, resultForJob(t, db, id))
			},
			completeAt: func(t *testing.T, db pg.DBI, id int64, owner string, at *time.Time) error {
				return completeLocal(context.Background(), db, id, owner, resultForJob(t, db, id), at)
			},
			terminal:  "completed",
			exhausted: "failed",
			// The local stream has no run of its own: the active run's pause
			// pauses the whole pipeline.
			pause: func(t *testing.T, s *pg.DB) {
				_, err := s.Exec(`INSERT INTO author_metadata_run
					(mode, status, extractor_version, normalizer_version, selector_archive)
					VALUES ('smoke', 'paused', 'extractor-v1', 'normalizer-v1', 'pause.zip')`)
				require.NoError(t, err)
			},
			resume: func(t *testing.T, s *pg.DB) {
				_, err := s.Exec(`UPDATE author_metadata_run SET status = 'running' WHERE status = 'paused'`)
				require.NoError(t, err)
			},
			spec: localLeaseSpec,
		},
	}
}

// eachStream runs fn for both streams over the group's scratch database,
// emptied before each stream.
func eachStream(t *testing.T, s *pg.DB, fn func(t *testing.T, s *pg.DB, c *leaseCase)) {
	t.Helper()
	cases := leaseCases()
	for i := range cases {
		c := &cases[i]
		t.Run(c.name, func(t *testing.T) {
			resetJobsDB(t, s)
			fn(t, s, c)
		})
	}
}

// resetJobsDB empties the queues the lease suite fills. TRUNCATE is not a row
// DELETE, so the immutability guards do not apply; that is fine for a scratch
// database. Seeded books stay: every case seeds fresh ones, and cascading
// into the legacy catalog tables would cost more than the cases themselves.
func resetJobsDB(t *testing.T, s *pg.DB) {
	t.Helper()
	_, err := s.Exec(`TRUNCATE author_metadata_run_item_attempt, author_metadata_run_item,
		contributor_normalization_job_attempt, contributor_normalization_job,
		contributor_normalization_result, author_metadata_run CASCADE`)
	require.NoError(t, err)
}

// seedExtractionItems creates a running run with n pending items whose ready
// times ascend in slice order, so the claim order is the slice order even
// though the IDs run the other way.
func seedExtractionItems(t *testing.T, s *pg.DB, n int) []int64 {
	t.Helper()
	var run int64
	_, err := s.QueryOne(pg.Scan(&run), `
		INSERT INTO author_metadata_run (mode, status, extractor_version, normalizer_version, selector_archive, started_at)
		VALUES ('smoke', 'running', 'extractor-v1', 'normalizer-v1', 'seed.zip', now())
		RETURNING id`)
	require.NoError(t, err)

	ids := make([]int64, n)
	for i := n - 1; i >= 0; i-- {
		var book int64
		_, err = s.QueryOne(pg.Scan(&book), `
			INSERT INTO opds_catalog_book
				(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
			VALUES (?, 'seed.zip', 'fb2', now(), '', 'ru', 'seed', '', '')
			RETURNING id`, fmt.Sprintf("%d.fb2", i))
		require.NoError(t, err)
		_, err = s.QueryOne(pg.Scan(&ids[i]), `
			INSERT INTO author_metadata_run_item (run_id, book_id, next_attempt_at)
			VALUES (?, ?, now() - (? - ?) * interval '1 second')
			RETURNING id`, run, book, n+1, i)
		require.NoError(t, err)
	}
	return ids
}

func seedLocalJobs(t *testing.T, s *pg.DB, n int) []int64 {
	t.Helper()
	ids := make([]int64, n)
	for i := n - 1; i >= 0; i-- {
		fp := keyFor("fingerprint", fmt.Sprint(i))
		_, err := s.QueryOne(pg.Scan(&ids[i]), `
			INSERT INTO contributor_normalization_job
				(normalization_key, source_fingerprint, extractor_version, normalizer_version, next_attempt_at)
			VALUES (?, ?, 'extractor-v1', 'normalizer-v1', now() - (? - ?) * interval '1 second')
			RETURNING id`, keyFor(string(fp), "extractor-v1", "normalizer-v1"), fp, n+1, i)
		require.NoError(t, err)
	}
	return ids
}

// resultForJob returns the automatic result of a job's own key, inserting it
// the first time: a key has one local result however often it is asked for.
func resultForJob(t *testing.T, s pg.DBI, job int64) int64 {
	t.Helper()
	_, err := s.Exec(`
		INSERT INTO contributor_normalization_result
			(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
			 method, kind, status, decision_class, display_name, search_key)
		SELECT source_fingerprint, normalization_key, extractor_version, normalizer_version, 'result-v1',
			'rules', 'person', 'normalized', 'plain', 'Имя', 'имя'
		FROM contributor_normalization_job WHERE id = ?
		ON CONFLICT (normalization_key) WHERE method IN ('structured', 'rules') DO NOTHING`, job)
	require.NoError(t, err)
	var id int64
	_, err = s.QueryOne(pg.Scan(&id), `
		SELECT r.id FROM contributor_normalization_result r
		JOIN contributor_normalization_job j ON j.normalization_key = r.normalization_key
		WHERE j.id = ? AND r.method IN ('structured', 'rules')`, job)
	require.NoError(t, err)
	return id
}

// leaseRow is the lease state of one row as the database sees it, with times
// expressed as seconds relative to the database clock.
type leaseRow struct {
	Status          string
	LeaseOwner      *string
	LeaseInSeconds  *float64
	AttemptCount    int
	ReadyInSeconds  float64
	LastErrorClass  *string
	FinishedPresent bool
}

func (c *leaseCase) row(t *testing.T, s pg.DBI, id int64) leaseRow {
	t.Helper()
	lastError := "NULL"
	if c.table == "contributor_normalization_job" {
		lastError = "last_error_class"
	}
	var r leaseRow
	_, err := s.QueryOne(&r, `
		SELECT status, lease_owner::text AS lease_owner,
			extract(epoch FROM lease_expires_at - now()) AS lease_in_seconds,
			attempt_count, extract(epoch FROM next_attempt_at - now()) AS ready_in_seconds,
			`+lastError+` AS last_error_class, finished_at IS NOT NULL AS finished_present
		FROM `+c.table+` WHERE id = ?`, id)
	require.NoError(t, err)
	return r
}

type attemptRow struct {
	AttemptNo  int
	LeaseOwner string
	Finished   bool
	FinishedAt *time.Time
	Outcome    *string
	ErrorClass *string
}

func (c *leaseCase) attempts(t *testing.T, s pg.DBI, id int64) []attemptRow {
	t.Helper()
	var rows []attemptRow
	_, err := s.Query(&rows, `
		SELECT attempt_no, lease_owner::text AS lease_owner, finished_at IS NOT NULL AS finished,
			finished_at, outcome, error_class
		FROM `+c.attemptTable+` WHERE `+c.attemptFK+` = ? ORDER BY attempt_no`, id)
	require.NoError(t, err)
	return rows
}

// expire moves a row's lease into the past: the database clock has, in
// effect, advanced past it.
func (c *leaseCase) expire(t *testing.T, s *pg.DB, id int64) {
	t.Helper()
	_, err := s.Exec(`UPDATE `+c.table+` SET lease_expires_at = now() - interval '1 second' WHERE id = ?`, id)
	require.NoError(t, err)
}

// expireAtServerInstant reads the server's current clock and makes it the
// row's lease expiry, returning that instant.
func (c *leaseCase) expireAtServerInstant(t *testing.T, s *pg.DB, id int64) time.Time {
	t.Helper()
	var instant time.Time
	_, err := s.QueryOne(pg.Scan(&instant), `
		UPDATE `+c.table+` SET lease_expires_at = clock_timestamp() WHERE id = ? RETURNING lease_expires_at`, id)
	require.NoError(t, err)
	return instant
}

// makeReady moves a scheduled retry's ready time into the past — an hour
// back, ahead of every seeded row, so it is claimed first again.
func (c *leaseCase) makeReady(t *testing.T, s *pg.DB, id int64) {
	t.Helper()
	_, err := s.Exec(`UPDATE `+c.table+` SET next_attempt_at = now() - interval '1 hour' WHERE id = ?`, id)
	require.NoError(t, err)
}

func claimIDs(claims []LeaseClaim) []int64 {
	ids := make([]int64, len(claims))
	for i, c := range claims {
		ids[i] = c.ID
	}
	return ids
}

var defaultClaim = LeaseClaimOptions{Limit: 10, Lease: time.Minute, MaxAttempts: 5}

func withLimit(limit int) LeaseClaimOptions {
	o := defaultClaim
	o.Limit = limit
	return o
}

// RED 1: a claim limit of zero or below is refused before any SQL runs, as
// are the other non-positive settings.
func leaseClaimRejectsInvalidOptions(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		ids := c.seed(t, s, 3)
		owner := NewLeaseOwner()

		for _, limit := range []int{0, -1, -100} {
			claims, err := c.claim(ctx, s, owner, withLimit(limit))
			assert.ErrorIs(t, err, authornorm.ErrInvalidClaimLimit, "limit %d", limit)
			assert.Empty(t, claims)
		}
		for name, opts := range map[string]LeaseClaimOptions{
			"zero lease":        {Limit: 1, Lease: 0, MaxAttempts: 1},
			"negative lease":    {Limit: 1, Lease: -time.Second, MaxAttempts: 1},
			"zero max attempts": {Limit: 1, Lease: time.Minute, MaxAttempts: 0},
		} {
			_, err := c.claim(ctx, s, owner, opts)
			assert.ErrorIs(t, err, ErrInvalidLeaseOptions, name)
		}
		_, err := c.claim(ctx, s, "not-a-token", defaultClaim)
		assert.ErrorIs(t, err, ErrInvalidLeaseOwner)

		for _, id := range ids {
			r := c.row(t, s, id)
			assert.Nil(t, r.LeaseOwner, "row %d leased", id)
			assert.Zero(t, r.AttemptCount, "row %d counted an attempt", id)
			assert.Empty(t, c.attempts(t, s, id), "row %d got an attempt", id)
		}
	})
}

// RED 2: concurrent claimers receive disjoint sets, and a claimer skips rows
// another claim transaction holds instead of waiting for them.
func leaseConcurrentClaimsAreDisjoint(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		t.Run("rows locked by another claim are skipped, not waited for", func(t *testing.T) {
			resetJobsDB(t, s)
			ids := c.seed(t, s, 10)
			tx, err := s.Begin()
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			_, err = tx.Exec(`SELECT id FROM `+c.table+` WHERE id IN (?) FOR UPDATE`, pg.In(ids[:5]))
			require.NoError(t, err)

			// A liveness bound, not a sleep: without SKIP LOCKED the claim
			// blocks on the held rows until this deadline cancels it.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			claims, err := c.claim(ctx, s, NewLeaseOwner(), withLimit(5))
			require.NoError(t, err)
			assert.Equal(t, ids[5:], claimIDs(claims))
		})

		t.Run("racing workers never share a row", func(t *testing.T) {
			resetJobsDB(t, s)
			const rows, workers = 60, 6
			ids := c.seed(t, s, rows)

			var mu sync.Mutex
			seen := map[int64]int{}
			var wg sync.WaitGroup
			start := make(chan struct{})
			errs := make(chan error, workers)
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					owner := NewLeaseOwner()
					<-start
					for {
						claims, err := c.claim(context.Background(), s, owner, withLimit(4))
						if err != nil {
							errs <- err
							return
						}
						if len(claims) == 0 {
							return
						}
						mu.Lock()
						for _, cl := range claims {
							seen[cl.ID]++
						}
						mu.Unlock()
					}
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}

			require.Len(t, seen, rows, "every row claimed")
			for _, id := range ids {
				assert.Equal(t, 1, seen[id], "row %d claimed %d times", id, seen[id])
				assert.Len(t, c.attempts(t, s, id), 1, "row %d attempts", id)
			}
		})
	})
}

// RED 3: the claim has committed and released its row locks when it returns,
// so the worker's I/O runs outside any lock.
func leaseClaimReleasesRowLocks(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		c.seed(t, s, 3)
		claims, err := c.claim(context.Background(), s, NewLeaseOwner(), withLimit(3))
		require.NoError(t, err)
		require.Len(t, claims, 3)

		// The barrier: another transaction locks the claimed rows at once.
		tx, err := s.Begin()
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		var locked int
		_, err = tx.QueryOne(pg.Scan(&locked), `
			SELECT count(*) FROM (SELECT id FROM `+c.table+` WHERE id IN (?) FOR UPDATE NOWAIT) l`,
			pg.In(claimIDs(claims)))
		require.NoError(t, err, "claimed rows are still locked after the claim returned")
		assert.Equal(t, 3, locked)
	})
}

// RED 4: only the current owner extends a lease, and only up to its expiry.
func leaseHeartbeat(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		id := c.seed(t, s, 1)[0]
		owner := NewLeaseOwner()
		_, err := c.claim(ctx, s, owner, LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 5})
		require.NoError(t, err)

		require.NoError(t, c.heartbeat(ctx, s, id, owner, time.Hour))
		r := c.row(t, s, id)
		require.NotNil(t, r.LeaseInSeconds)
		assert.Greater(t, *r.LeaseInSeconds, 59*60.0, "the lease was extended to an hour")

		assert.ErrorIs(t, c.heartbeat(ctx, s, id, NewLeaseOwner(), time.Hour), authornorm.ErrLeaseLost, "foreign owner")
		assert.ErrorIs(t, c.heartbeat(ctx, s, id, owner, 0), ErrInvalidLeaseOptions, "zero lease")

		c.expire(t, s, id)
		assert.ErrorIs(t, c.heartbeat(ctx, s, id, owner, time.Hour), authornorm.ErrLeaseLost, "expired owner")

		t.Run("at the expiry instant the owner still holds it", func(t *testing.T) {
			instant := c.expireAtServerInstant(t, s, id)
			late := instant.Add(time.Microsecond)
			assert.ErrorIs(t, heartbeat(ctx, s, c.spec, id, owner, time.Minute, &late), authornorm.ErrLeaseLost,
				"a microsecond after expiry")
			require.NoError(t, heartbeat(ctx, s, c.spec, id, owner, time.Minute, &instant))
		})
	})
}

// RED 5: a lease that expires exactly at the operation instant is claimable;
// one that expires a microsecond later is not. The instant is read from the
// server and handed to the claim as its operation clock.
func leaseExpiryBoundary(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		id := c.seed(t, s, 1)[0]
		_, err := c.claim(ctx, s, NewLeaseOwner(), withLimit(1))
		require.NoError(t, err)
		instant := c.expireAtServerInstant(t, s, id)

		probe := func(at time.Time) []LeaseClaim {
			t.Helper()
			tx, err := s.Begin()
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			claims, err := claimLeases(ctx, tx, c.spec, NewLeaseOwner(), withLimit(1), &at)
			require.NoError(t, err)
			return claims
		}
		assert.Equal(t, []int64{id}, claimIDs(probe(instant)), "expiry == operation instant is claimable")
		assert.Empty(t, probe(instant.Add(-time.Microsecond)), "a live lease is not claimable")
	})
}

// RED 6: a late first owner cannot complete, fail or extend a row that a
// second owner reclaimed.
func leaseStaleOwnerCannotFinish(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		id := c.seed(t, s, 1)[0]
		first, second := NewLeaseOwner(), NewLeaseOwner()
		_, err := c.claim(ctx, s, first, withLimit(1))
		require.NoError(t, err)
		c.expire(t, s, id)
		claims, err := c.claim(ctx, s, second, withLimit(1))
		require.NoError(t, err)
		require.Equal(t, []int64{id}, claimIDs(claims))

		assert.ErrorIs(t, c.complete(t, s, id, first), authornorm.ErrLeaseLost, "stale complete")
		_, err = c.fail(ctx, s, id, first, LeaseFailure{ErrorClass: "transient_database", RetryAfter: time.Minute, MaxAttempts: 5})
		assert.ErrorIs(t, err, authornorm.ErrLeaseLost, "stale fail")
		assert.ErrorIs(t, c.heartbeat(ctx, s, id, first, time.Hour), authornorm.ErrLeaseLost, "stale heartbeat")

		r := c.row(t, s, id)
		assert.Equal(t, "pending", r.Status, "the stale owner changed the status")
		require.NotNil(t, r.LeaseOwner)
		assert.Equal(t, second, *r.LeaseOwner, "the stale owner took the lease back")
		attempts := c.attempts(t, s, id)
		require.Len(t, attempts, 2)
		assert.Equal(t, ptr("lease_expired"), attempts[0].ErrorClass, "the first owner's attempt is closed as abandoned")
		assert.False(t, attempts[1].Finished, "the second owner's attempt is untouched")

		require.NoError(t, c.complete(t, s, id, second))
		assert.Equal(t, c.terminal, c.row(t, s, id).Status)
	})
}

// RED 7: a worker that crashed without completing loses nothing: once its
// lease runs out, the row is claimable again with a new attempt.
func leaseCrashRecovery(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		id := c.seed(t, s, 1)[0]
		crashed := NewLeaseOwner()
		_, err := c.claim(ctx, s, crashed, withLimit(1))
		require.NoError(t, err)

		claims, err := c.claim(ctx, s, NewLeaseOwner(), withLimit(1))
		require.NoError(t, err)
		assert.Empty(t, claims, "a live lease is not reclaimed")

		c.expire(t, s, id)
		rescuer := NewLeaseOwner()
		claims, err = c.claim(ctx, s, rescuer, withLimit(1))
		require.NoError(t, err)
		require.Equal(t, []int64{id}, claimIDs(claims))
		assert.Equal(t, 2, claims[0].AttemptNo)

		attempts := c.attempts(t, s, id)
		require.Len(t, attempts, 2)
		assert.Equal(t, crashed, attempts[0].LeaseOwner)
		assert.True(t, attempts[0].Finished)
		assert.Equal(t, rescuer, attempts[1].LeaseOwner)
		require.NoError(t, c.complete(t, s, id, rescuer))
	})
}

// RED 8: pause hands out no new claims; resume hands out the same rows.
func leasePauseAndResume(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		ids := c.seed(t, s, 4)
		c.pause(t, s)

		claims, err := c.claim(ctx, s, NewLeaseOwner(), defaultClaim)
		require.NoError(t, err)
		assert.Empty(t, claims, "a paused pipeline handed out a claim")
		for _, id := range ids {
			assert.Zero(t, c.row(t, s, id).AttemptCount)
		}

		c.resume(t, s)
		claims, err = c.claim(ctx, s, NewLeaseOwner(), defaultClaim)
		require.NoError(t, err)
		assert.Equal(t, ids, claimIDs(claims))
	})
}

// RED 9: a transient failure releases the lease, schedules the retry on the
// database clock and appends to the attempt history; the retry never rewrites
// an earlier attempt.
func leaseTransientFailureSchedulesRetry(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		id := c.seed(t, s, 1)[0]
		first := NewLeaseOwner()
		_, err := c.claim(ctx, s, first, withLimit(1))
		require.NoError(t, err)

		exhausted, err := c.fail(ctx, s, id, first,
			LeaseFailure{ErrorClass: "transient_database", RetryAfter: 5 * time.Minute, MaxAttempts: 5})
		require.NoError(t, err)
		assert.False(t, exhausted)

		r := c.row(t, s, id)
		assert.Equal(t, "pending", r.Status)
		assert.Nil(t, r.LeaseOwner, "the lease is released")
		assert.InDelta(t, 300, r.ReadyInSeconds, 10, "retry scheduled five minutes ahead on the database clock")
		if c.table == "contributor_normalization_job" {
			assert.Equal(t, ptr("transient_database"), r.LastErrorClass)
		}

		claims, err := c.claim(ctx, s, NewLeaseOwner(), withLimit(1))
		require.NoError(t, err)
		assert.Empty(t, claims, "a scheduled retry is not ready yet")

		before := c.attempts(t, s, id)
		require.Len(t, before, 1)
		assert.Equal(t, ptr("transient_database"), before[0].ErrorClass)
		assert.Nil(t, before[0].Outcome)

		c.makeReady(t, s, id)
		second := NewLeaseOwner()
		claims, err = c.claim(ctx, s, second, withLimit(1))
		require.NoError(t, err)
		require.Equal(t, []int64{id}, claimIDs(claims))
		require.NoError(t, c.complete(t, s, id, second))

		after := c.attempts(t, s, id)
		require.Len(t, after, 2, "the retry appended an attempt")
		assert.Equal(t, before[0], after[0], "the first attempt is unchanged")
		assert.Equal(t, ptr(c.terminal), after[1].Outcome)

		_, err = c.fail(ctx, s, id, second, LeaseFailure{ErrorClass: "Not A Class", RetryAfter: time.Minute, MaxAttempts: 5})
		assert.ErrorIs(t, err, ErrInvalidLeaseFailure)
		_, err = c.fail(ctx, s, id, second, LeaseFailure{ErrorClass: "transient_database", RetryAfter: 0, MaxAttempts: 5})
		assert.ErrorIs(t, err, ErrInvalidLeaseFailure)
	})
}

// RED 10: running out of attempts ends the row on the stream's closed
// terminal path — through a failure, or through a crash on the last attempt.
func leaseMaxAttempts(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		failure := LeaseFailure{ErrorClass: "transient_database", RetryAfter: time.Minute, MaxAttempts: 2}
		opts := LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 2}

		t.Run("through failures", func(t *testing.T) {
			resetJobsDB(t, s)
			id := c.seed(t, s, 1)[0]
			for attempt := 1; attempt <= 2; attempt++ {
				owner := NewLeaseOwner()
				claims, err := c.claim(ctx, s, owner, opts)
				require.NoError(t, err)
				require.Equal(t, []int64{id}, claimIDs(claims), "attempt %d", attempt)
				exhausted, err := c.fail(ctx, s, id, owner, failure)
				require.NoError(t, err)
				assert.Equal(t, attempt == 2, exhausted, "attempt %d", attempt)
				c.makeReady(t, s, id)
			}

			r := c.row(t, s, id)
			assert.Equal(t, c.exhausted, r.Status)
			assert.True(t, r.FinishedPresent)
			assert.Nil(t, r.LeaseOwner)
			if c.table == "contributor_normalization_job" {
				assert.Equal(t, ptr(LeaseErrorMaxAttemptsExceeded), r.LastErrorClass)
			}
			attempts := c.attempts(t, s, id)
			require.Len(t, attempts, 2)
			assert.Equal(t, ptr("transient_database"), attempts[0].ErrorClass)
			assert.Equal(t, ptr(c.exhausted), attempts[1].Outcome)
			assert.Equal(t, ptr(LeaseErrorMaxAttemptsExceeded), attempts[1].ErrorClass)

			claims, err := c.claim(ctx, s, NewLeaseOwner(), opts)
			require.NoError(t, err)
			assert.Empty(t, claims, "an exhausted row is not claimed again")
		})

		t.Run("through a crash on the last attempt", func(t *testing.T) {
			resetJobsDB(t, s)
			id := c.seed(t, s, 1)[0]
			one := LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 1}
			_, err := c.claim(ctx, s, NewLeaseOwner(), one)
			require.NoError(t, err)
			c.expire(t, s, id)

			claims, err := c.claim(ctx, s, NewLeaseOwner(), one)
			require.NoError(t, err)
			assert.Empty(t, claims)
			r := c.row(t, s, id)
			assert.Equal(t, c.exhausted, r.Status, "no hidden pending row is left behind")
			attempts := c.attempts(t, s, id)
			require.Len(t, attempts, 1)
			assert.Equal(t, ptr(LeaseErrorMaxAttemptsExceeded), attempts[0].ErrorClass)
		})
	})
}

// RED 11: claims come in ready-time order, then ID; the order in which
// claimed rows are completed changes nothing.
func leaseClaimOrderAndCompletionOrder(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		ids := c.seed(t, s, 6)
		owner := NewLeaseOwner()

		first, err := c.claim(ctx, s, owner, withLimit(3))
		require.NoError(t, err)
		rest, err := c.claim(ctx, s, owner, withLimit(10))
		require.NoError(t, err)
		assert.Equal(t, ids[:3], claimIDs(first))
		assert.Equal(t, ids[3:], claimIDs(rest))

		reversed := append(append([]int64{}, ids...), nil...)
		sort.Slice(reversed, func(i, j int) bool { return reversed[i] > reversed[j] })
		for _, id := range reversed {
			require.NoError(t, c.complete(t, s, id, owner))
		}
		for _, id := range ids {
			r := c.row(t, s, id)
			assert.Equal(t, c.terminal, r.Status)
			assert.Len(t, c.attempts(t, s, id), 1)
		}
	})
}

// A stream outside the closed set never reaches SQL.
func TestLeaseSpecIsClosed(t *testing.T) {
	_, err := leaseSpecFor(LeaseStream("author_metadata_run; DROP TABLE opds_catalog_book"))
	assert.ErrorIs(t, err, ErrUnknownLeaseStream)
	for _, stream := range []LeaseStream{LeaseStreamExtraction, LeaseStreamLocalNormalization} {
		spec, err := leaseSpecFor(stream)
		require.NoError(t, err)
		assert.NotEmpty(t, spec.table)
	}
}

// Completion takes only what the stream can end in.
func leaseCompletionArguments(t *testing.T, s *pg.DB) {
	ctx := context.Background()
	resetJobsDB(t, s)
	id := seedExtractionItems(t, s, 1)[0]
	owner := NewLeaseOwner()
	_, err := ClaimExtractionItems(ctx, s, owner, withLimit(1))
	require.NoError(t, err)

	err = CompleteExtractionItem(ctx, s, id, owner, models.AuthorMetadataRunItemPending, nil)
	assert.ErrorIs(t, err, ErrInvalidLeaseCompletion)
	err = CompleteExtractionItem(ctx, s, id, owner, models.AuthorMetadataRunItemStatus("unknown"), nil)
	assert.ErrorIs(t, err, ErrInvalidLeaseCompletion)
	assert.Equal(t, "pending", extractionLeaseCase().row(t, s, id).Status)
	err = CompleteLocalNormalizationJob(ctx, s, id, owner, 0)
	assert.ErrorIs(t, err, ErrInvalidLeaseCompletion)
	assert.True(t, errors.Is(CompleteExtractionItem(ctx, s, id, NewLeaseOwner(),
		models.AuthorMetadataRunItemEntryMissing, nil), authornorm.ErrLeaseLost))
}

func extractionLeaseCase() *leaseCase { return &leaseCases()[0] }

// expiredSinceTxStart opens a transaction and moves the row's lease expiry
// strictly between that transaction's now() and the server's current clock:
// live by the transaction's timestamp, already past by the real clock. Both
// instants come from PostgreSQL; nothing here reads the host clock.
func (c *leaseCase) expiredSinceTxStart(t *testing.T, s *pg.DB, id int64) *pg.Tx {
	t.Helper()
	tx, err := s.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	// Each round trip takes real time; a few microseconds of gap are enough
	// for an expiry strictly inside it. Bounded, and not a correctness proof.
	for i := 0; i < 1000; i++ {
		var gap float64
		_, err = tx.QueryOne(pg.Scan(&gap), `SELECT extract(epoch FROM clock_timestamp() - now()) * 1e6`)
		require.NoError(t, err)
		if gap >= 4 {
			break
		}
	}
	_, err = tx.Exec(`UPDATE `+c.table+`
		SET lease_expires_at = now() + (clock_timestamp() - now()) / 2 WHERE id = ?`, id)
	require.NoError(t, err)

	var liveByTxStart, pastByClock bool
	_, err = tx.QueryOne(pg.Scan(&liveByTxStart, &pastByClock), `
		SELECT lease_expires_at >= now(), lease_expires_at < clock_timestamp()
		FROM `+c.table+` WHERE id = ?`, id)
	require.NoError(t, err)
	require.True(t, liveByTxStart && pastByClock, "precondition: expiry between the transaction start and now")
	return tx
}

// Fix round 1 (review B1): an older caller transaction does not keep a lease
// alive. The fence compares with the database clock at the operation, not
// with the transaction's start.
func leaseFencingUsesFreshClock(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		ids := c.seed(t, s, 5)
		owner := NewLeaseOwner()
		_, err := c.claim(ctx, s, owner, withLimit(5))
		require.NoError(t, err)

		t.Run("heartbeat", func(t *testing.T) {
			tx := c.expiredSinceTxStart(t, s, ids[0])
			assert.ErrorIs(t, c.heartbeat(ctx, tx, ids[0], owner, time.Hour), authornorm.ErrLeaseLost)
		})
		t.Run("complete", func(t *testing.T) {
			tx := c.expiredSinceTxStart(t, s, ids[1])
			assert.ErrorIs(t, c.completeIn(t, tx, ids[1], owner), authornorm.ErrLeaseLost)
			assert.Equal(t, "pending", c.row(t, tx, ids[1]).Status)
		})
		t.Run("complete and fail fence inclusively at the operation instant", func(t *testing.T) {
			more := ids[3:]
			failure := LeaseFailure{ErrorClass: "transient_database", RetryAfter: time.Minute, MaxAttempts: 5}

			instant := c.expireAtServerInstant(t, s, more[0])
			late := instant.Add(time.Microsecond)
			assert.ErrorIs(t, c.completeAt(t, s, more[0], owner, &late), authornorm.ErrLeaseLost)
			require.NoError(t, c.completeAt(t, s, more[0], owner, &instant))

			instant = c.expireAtServerInstant(t, s, more[1])
			late = instant.Add(time.Microsecond)
			_, err := fail(ctx, s, c.spec, more[1], owner, failure, &late)
			assert.ErrorIs(t, err, authornorm.ErrLeaseLost)
			_, err = fail(ctx, s, c.spec, more[1], owner, failure, &instant)
			require.NoError(t, err)
		})
		t.Run("fail", func(t *testing.T) {
			tx := c.expiredSinceTxStart(t, s, ids[2])
			_, err := c.fail(ctx, tx, ids[2], owner,
				LeaseFailure{ErrorClass: "transient_database", RetryAfter: time.Minute, MaxAttempts: 5})
			assert.ErrorIs(t, err, authornorm.ErrLeaseLost)
			r := c.row(t, tx, ids[2])
			require.NotNil(t, r.LeaseOwner, "the expired owner's failure released the lease")
			assert.Equal(t, owner, *r.LeaseOwner)
		})
	})
}

// Fix round 1 (review B2): a leased row whose open attempt is missing is not
// moved at all — not even by a caller without a transaction, where a moved row
// would commit before the error is returned.
func leaseMissingAttemptChangesNothing(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		failure := LeaseFailure{ErrorClass: "transient_database", RetryAfter: time.Minute, MaxAttempts: 5}

		// A schema-valid lease with no attempt row behind it.
		leaseWithoutAttempt := func(t *testing.T, id int64, owner string) leaseRow {
			t.Helper()
			_, err := s.Exec(`UPDATE `+c.table+`
				SET lease_owner = ?, lease_expires_at = now() + interval '1 hour', attempt_count = 1
				WHERE id = ?`, owner, id)
			require.NoError(t, err)
			require.Empty(t, c.attempts(t, s, id))
			return c.row(t, s, id)
		}
		unchanged := func(t *testing.T, db pg.DBI, id int64, before leaseRow) {
			t.Helper()
			after := c.row(t, db, id)
			assert.Equal(t, before.Status, after.Status)
			assert.Equal(t, before.LeaseOwner, after.LeaseOwner)
			assert.Equal(t, before.AttemptCount, after.AttemptCount)
			assert.Equal(t, before.LastErrorClass, after.LastErrorClass)
			assert.False(t, after.FinishedPresent)
			require.NotNil(t, after.LeaseInSeconds, "the lease was cleared")
			assert.InDelta(t, *before.LeaseInSeconds, *after.LeaseInSeconds, 30)
			assert.InDelta(t, before.ReadyInSeconds, after.ReadyInSeconds, 30, "a retry was scheduled")
			assert.Empty(t, c.attempts(t, db, id))
		}

		ids := c.seed(t, s, 4)
		for i, viaTx := range []bool{false, true} {
			name := map[bool]string{false: "without a transaction", true: "inside a transaction"}[viaTx]
			t.Run("complete "+name, func(t *testing.T) {
				id, owner := ids[2*i], NewLeaseOwner()
				before := leaseWithoutAttempt(t, id, owner)
				var db pg.DBI = s
				if viaTx {
					tx, err := s.Begin()
					require.NoError(t, err)
					defer func() { _ = tx.Rollback() }()
					db = tx
				}
				assert.ErrorIs(t, c.completeIn(t, db, id, owner), ErrLeaseAttemptMissing)
				unchanged(t, db, id, before)
			})
			t.Run("fail "+name, func(t *testing.T) {
				id, owner := ids[2*i+1], NewLeaseOwner()
				before := leaseWithoutAttempt(t, id, owner)
				var db pg.DBI = s
				if viaTx {
					tx, err := s.Begin()
					require.NoError(t, err)
					defer func() { _ = tx.Rollback() }()
					db = tx
				}
				_, err := c.fail(ctx, db, id, owner, failure)
				assert.ErrorIs(t, err, ErrLeaseAttemptMissing)
				unchanged(t, db, id, before)
			})
		}
	})
}

// Fix round 1 (review B3): one row running out of attempts ends that row only.
// The other rows stay claimable and, for extraction, the run keeps running.
func leaseRunContinuesAfterPoison(t *testing.T, s *pg.DB) {
	eachStream(t, s, func(t *testing.T, s *pg.DB, c *leaseCase) {
		ctx := context.Background()
		opts := LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 2}
		failure := LeaseFailure{ErrorClass: "transient_database", RetryAfter: time.Minute, MaxAttempts: 2}

		// continues claims next — the claim that, after a crash, ends the
		// poisoned row — and then observes both rows and the run.
		continues := func(t *testing.T, poisoned, healthy int64) {
			t.Helper()
			owner := NewLeaseOwner()
			claims, err := c.claim(ctx, s, owner, opts)
			require.NoError(t, err)
			assert.Equal(t, []int64{healthy}, claimIDs(claims), "the healthy row is still handed out")

			r := c.row(t, s, poisoned)
			assert.Equal(t, c.exhausted, r.Status)
			attempts := c.attempts(t, s, poisoned)
			require.NotEmpty(t, attempts)
			assert.Equal(t, ptr(LeaseErrorMaxAttemptsExceeded), attempts[len(attempts)-1].ErrorClass)
			if c.table == runItemTable {
				var status string
				var lastError *string
				_, err = s.QueryOne(pg.Scan(&status, &lastError), `
					SELECT r.status, r.last_error_class FROM author_metadata_run r
					JOIN author_metadata_run_item i ON i.run_id = r.id WHERE i.id = ?`, healthy)
				require.NoError(t, err)
				assert.Equal(t, "running", status, "one poisoned book does not stop the run")
				assert.Nil(t, lastError)
			}
			require.NoError(t, c.complete(t, s, healthy, owner))
		}

		t.Run("through failures", func(t *testing.T) {
			resetJobsDB(t, s)
			ids := c.seed(t, s, 2)
			for attempt := 1; attempt <= 2; attempt++ {
				owner := NewLeaseOwner()
				claims, err := c.claim(ctx, s, owner, opts)
				require.NoError(t, err)
				require.Equal(t, []int64{ids[0]}, claimIDs(claims))
				_, err = c.fail(ctx, s, ids[0], owner, failure)
				require.NoError(t, err)
				c.makeReady(t, s, ids[0])
			}
			continues(t, ids[0], ids[1])
		})

		t.Run("through a crash on the last attempt", func(t *testing.T) {
			resetJobsDB(t, s)
			ids := c.seed(t, s, 2)
			one := LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 1}
			claims, err := c.claim(ctx, s, NewLeaseOwner(), one)
			require.NoError(t, err)
			require.Equal(t, []int64{ids[0]}, claimIDs(claims))
			c.expire(t, s, ids[0])
			opts = one
			continues(t, ids[0], ids[1])
		})
	})
}
