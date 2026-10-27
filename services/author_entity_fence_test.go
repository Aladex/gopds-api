package services_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The commit-time freshness fence (plan §4.9, E4b-2): a pass that applies a
// model decision plans in one REPEATABLE READ snapshot and, before it
// completes, refuses to commit when an input changed after that snapshot.
// The writer in each test is a second database session.

// fencedCatalog is the swap pair with a same decision on its candidate and
// an unrelated author, all built: its next pass applies the decision, so it
// is fenced.
type fencedCatalog struct {
	*entityCatalog
	small    byte
	decision int64
	// big and small are the pair's entities before the decision.
	bigID, smallID string
	// other is the unrelated author's book, an input no candidate reads.
	other int64
}

func newFencedCatalog(t *testing.T) *fencedCatalog {
	t.Helper()
	c := newEntityCatalog(t)
	f := &fencedCatalog{entityCatalog: c, small: c.seedCandidates()}
	snap := c.book("Война и мир")
	c.credit(snap, 0x6a, "Толстой", "Лев", "Николаевич")
	f.other = c.bookOf(snap)
	c.build()
	f.bigID, f.smallID = c.entityOf(0x61), c.entityOf(f.small)
	f.decision = c.decideSwap()
	return f
}

// start records a pass and returns it with a worker.
func (f *fencedCatalog) start(cfg services.AuthorEntityBuildConfig) (int64, *services.AuthorEntityBuildLoop) {
	f.t.Helper()
	run, err := services.NewAuthorEntityAdmin(f.db).Start(context.Background(), 0)
	require.NoError(f.t, err)
	return run.ID, services.NewAuthorEntityBuildLoop(f.db, cfg)
}

// attempt runs one attempt of the pass.
func (f *fencedCatalog) attempt(loop *services.AuthorEntityBuildLoop) {
	f.t.Helper()
	worked, err := loop.RunOnce(context.Background())
	require.NoError(f.t, err)
	require.True(f.t, worked)
}

// run is a pass as stored.
func (f *fencedCatalog) run(id int64) *database.AuthorEntityBuildRun {
	f.t.Helper()
	var r database.AuthorEntityBuildRun
	_, err := f.db.QueryOne(&r, `SELECT status, attempts, last_error_class FROM author_entity_build_run WHERE id = ?`, id)
	require.NoError(f.t, err)
	return &r
}

// layout is what a pass writes: the assignments, entities, merge log,
// candidates, decision states and revivals.
func (f *fencedCatalog) layout() string {
	f.t.Helper()
	var out string
	for _, table := range []string{"author_entity", "author_entity_assignment", "author_entity_credit_assignment",
		"author_entity_merge_log", "author_entity_merge_member", "author_entity_merge_candidate",
		"author_entity_merge_decision_state", "author_entity_revival"} {
		var sum string
		_, err := f.db.QueryOne(pg.Scan(&sum), `SELECT coalesce(md5(string_agg(t::text, ',' ORDER BY t::text)), '')
			FROM `+table+` t`)
		require.NoError(f.t, err, table)
		out += table + "=" + sum + "\n"
	}
	return out
}

// state is the decision's stored state and reason.
func (f *fencedCatalog) state() string {
	f.t.Helper()
	var state, reason string
	_, err := f.db.QueryOne(pg.Scan(&state, &reason), `SELECT state, coalesce(reason, '')
		FROM author_entity_merge_decision_state WHERE decision_id = ?`, f.decision)
	require.NoError(f.t, err)
	if reason == "" {
		return state
	}
	return state + "/" + reason
}

// refused runs one attempt with hooks and checks it committed nothing: the
// layout is as it was and the pass still runs.
func (f *fencedCatalog) refused(run int64, loop *services.AuthorEntityBuildLoop, beforeApply func(),
	apply func(step string) error) {
	f.t.Helper()
	// What the attempt may not change is the layout once the writer of the
	// before-apply hook committed: that writer's own rows are its change.
	before := f.layout()
	if beforeApply != nil {
		hook := beforeApply
		beforeApply = func() {
			hook()
			before = f.layout()
		}
	}
	func() {
		defer services.SetAuthorEntityHooks(beforeApply, apply)()
		f.attempt(loop)
	}()
	assert.Equal(f.t, before, f.layout(), "a refused attempt wrote")
	assert.Equal(f.t, database.AuthorEntityRunRunning, f.run(run).Status)
}

// completes runs the pass's next attempt and checks it completed.
func (f *fencedCatalog) completes(run int64, loop *services.AuthorEntityBuildLoop) {
	f.t.Helper()
	f.attempt(loop)
	assert.Equal(f.t, database.AuthorEntityRunCompleted, f.run(run).Status)
	require.NoError(f.t, database.SettleAuthorAutopilotMarks(context.Background(), f.db))
}

// retitle edits a book's title, a card input, in its own transaction.
func (f *fencedCatalog) retitle(book int64) {
	f.t.Helper()
	f.exec(`UPDATE opds_catalog_book SET title = title || ' [ред.]' WHERE id = ?`, book)
}

// bookOfFill is the book of a fingerprint's first credit.
func (f *fencedCatalog) bookOfFill(fill byte) int64 {
	f.t.Helper()
	var book int64
	_, err := f.db.QueryOne(pg.Scan(&book), `SELECT s.book_id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE c.source_fingerprint = ? ORDER BY c.id LIMIT 1`,
		bytes.Repeat([]byte{fill}, 32))
	require.NoError(f.t, err)
	return book
}

// flipSnapshot replaces the current snapshot of the small side's book by
// one whose credits are not selected yet.
func (f *fencedCatalog) flipSnapshot() {
	f.t.Helper()
	book := f.bookOfFill(f.small)
	require.NoError(f.t, f.db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		if _, err := tx.Exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE book_id = ? AND is_current`,
			book); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO book_metadata_snapshot (book_id, book_md5, extractor_version, origin, outcome,
				archive_path, entry_name)
			SELECT book_id, book_md5, 'extractor-v2', origin, outcome, archive_path, entry_name
			FROM book_metadata_snapshot WHERE book_id = ? AND NOT is_current`, book)
		return err
	}))
}

// markInFlight opens a writer that marked an input and has not committed.
func (f *fencedCatalog) markInFlight() *pg.Tx {
	f.t.Helper()
	tx, err := f.db.Begin()
	require.NoError(f.t, err)
	_, err = tx.Exec(`UPDATE opds_catalog_book SET title = title || ' [ред.]' WHERE id = ?`, f.other)
	require.NoError(f.t, err)
	return tx
}

// fenceLocks counts the locks on the mark table that are held (granted) or
// waited for.
func (f *fencedCatalog) fenceLocks(granted bool) int {
	f.t.Helper()
	var n int
	_, err := f.db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM pg_locks
		WHERE relation = 'author_autopilot_mark'::regclass AND mode = 'ShareLock' AND granted = ?`, granted)
	require.NoError(f.t, err)
	return n
}

// waitFor polls cond until it holds, false when the deadline passes first.
func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// Tests 1–3 and 5: a change committed between the plan and the first write
// stops the attempt; the next attempt, from a new snapshot, sees it.
func TestFenceSelectedResultChange(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	f.refused(run, loop, func() {
		fp := bytes.Repeat([]byte{f.small}, 32)
		var result int64
		_, err := f.db.QueryOne(pg.Scan(&result), `INSERT INTO contributor_normalization_result
				(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
				 method, kind, status, decision_class, family_name, given_name, additional_names, display_name,
				 sort_name, search_key, script)
			SELECT source_fingerprint, decode(repeat('5a', 32), 'hex'), extractor_version, normalizer_version,
				result_schema_version, method, kind, status, decision_class, family_name, 'КИНГ', additional_names,
				'Стивен КИНГ', sort_name, search_key, script
			FROM contributor_normalization_result WHERE source_fingerprint = ? RETURNING id`, fp)
		require.NoError(t, err)
		f.exec(`UPDATE book_contributor_credit_selection SET result_id = ? WHERE source_fingerprint = ?`, result, fp)
	}, nil)
	f.completes(run, loop)
	assert.Equal(t, "lapsed/input_changed", f.state())
	assert.Equal(t, f.smallID, f.entityOf(f.small))
}

func TestFenceSampledBookChange(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	f.refused(run, loop, func() { f.retitle(f.bookOfFill(f.small)) }, nil)
	f.completes(run, loop)
	assert.Equal(t, "lapsed/input_changed", f.state())
	assert.Equal(t, f.smallID, f.entityOf(f.small))
}

func TestFenceDecisionRevoke(t *testing.T) {
	f := newFencedCatalog(t)
	f.build()
	require.Equal(t, f.bigID, f.mergedInto(f.smallID))
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	f.refused(run, loop, func() {
		_, err := database.RevokeMergeDecision(context.Background(), f.db, f.decision, 0)
		require.NoError(t, err)
	}, nil)
	assert.Equal(t, f.bigID, f.mergedInto(f.smallID), "the refused attempt split the pair")
	f.completes(run, loop)
	assert.Equal(t, f.smallID, f.entityOf(f.small))
	assert.Equal(t, f.bigID, f.entityOf(0x61))
	assert.Empty(t, f.mergedInto(f.smallID))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_entity_revival WHERE outcome = 'exact'`))
}

// The flip lands either after the plan or between the plan's first load and
// the next one: the snapshot is the plan's first statement, so both stop it.
func TestFenceSnapshotFlip(t *testing.T) {
	for _, at := range []string{"before_apply", "after_first_load"} {
		t.Run(at, func(t *testing.T) {
			f := newFencedCatalog(t)
			run, loop := f.start(services.AuthorEntityBuildConfig{})
			if at == "after_first_load" {
				flipped := false
				restore := services.SetAuthorEntityLoadHook(func(step string) {
					if step == services.AuthorEntityLoadInput && !flipped {
						flipped = true
						f.flipSnapshot()
					}
				})
				defer restore()
				f.refused(run, loop, nil, nil)
				require.True(t, flipped)
			} else {
				f.refused(run, loop, f.flipSnapshot, nil)
			}
			f.completes(run, loop)
			assert.Equal(t, "lapsed/members_changed", f.state())
		})
	}
}

// Test 6: a writer that commits while the units are written is caught at
// the fence.
func TestFenceWriterDuringApply(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	wrote := false
	f.refused(run, loop, nil, func(step string) error {
		if step != "batch" || wrote {
			return nil
		}
		wrote = true
		// A fence that already holds the mark table would hold this writer
		// until the pass commits, and the pass waits for this hook.
		return f.db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
			if _, err := tx.Exec(`SET LOCAL lock_timeout = '2s'`); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE opds_catalog_book SET title = title || ' [ред.]' WHERE id = ?`, f.other)
			return err
		})
	})
	require.True(t, wrote)
	f.completes(run, loop)
	assert.Equal(t, "applied", f.state())
	assert.Equal(t, f.bigID, f.entityOf(f.small))
}

// Test 7: a writer that marked and has not committed when the fence runs is
// waited for; its commit refuses the attempt, its rollback lets it land.
func TestFenceWaitsForInFlightWriter(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			f := newFencedCatalog(t)
			run, loop := f.start(services.AuthorEntityBuildConfig{})
			waited := false
			finished := make(chan struct{})
			started := false
			hook := func(step string) error {
				if step != "fence" {
					return nil
				}
				writer := f.markInFlight()
				started = true
				go func() {
					defer close(finished)
					waited = waitFor(func() bool { return f.fenceLocks(false) == 1 })
					if commit {
						assert.NoError(t, writer.Commit())
					} else {
						assert.NoError(t, writer.Rollback())
					}
				}()
				return nil
			}
			if commit {
				f.refused(run, loop, nil, hook)
				if started {
					<-finished
				}
				require.True(t, waited, "the fence did not wait for the writer")
				f.completes(run, loop)
			} else {
				restore := services.SetAuthorEntityHooks(nil, hook)
				defer restore()
				f.completes(run, loop)
				if started {
					<-finished
				}
				require.True(t, waited, "the fence did not wait for the writer")
			}
			assert.Equal(t, "applied", f.state())
			assert.Equal(t, f.bigID, f.entityOf(f.small))
			attempts := 1
			if commit {
				attempts = 2
			}
			assert.Equal(t, attempts, f.run(run).Attempts)
		})
	}
}

// Test 8: a writer that starts after the fence's lock waits for the pass,
// commits after it, and leaves its mark for the next pass; the pass applied
// the decision, consistent at its commit.
func TestWriterAfterFenceLeavesMark(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	done := make(chan error, 1)
	restore := services.SetAuthorEntityHooks(nil, func(step string) error {
		if step != "complete" {
			return nil
		}
		assert.Equal(t, 1, f.fenceLocks(true), "the fence holds the mark table")
		go func() {
			_, err := f.db.Exec(`UPDATE opds_catalog_book SET title = title || ' [ред.]' WHERE id = ?`, f.other)
			done <- err
		}()
		if !waitFor(f.writerWaits) {
			return errors.New("the writer did not wait for the pass")
		}
		return nil
	})
	defer restore()
	f.attempt(loop)
	require.NoError(t, <-done)
	assert.Equal(t, database.AuthorEntityRunCompleted, f.run(run).Status)
	require.NoError(t, database.SettleAuthorAutopilotMarks(context.Background(), f.db))
	assert.Equal(t, "applied", f.state())
	assert.Equal(t, f.bigID, f.entityOf(f.small))
	due, err := database.AuthorAutopilotBuildNeeded(context.Background(), f.db)
	require.NoError(t, err)
	assert.True(t, due, "the writer's mark is a pass to do")
}

// writerWaits reports a session waiting for a lock on the mark table.
func (f *fencedCatalog) writerWaits() bool {
	f.t.Helper()
	var n int
	_, err := f.db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM pg_locks
		WHERE relation = 'author_autopilot_mark'::regclass AND NOT granted`)
	require.NoError(f.t, err)
	return n > 0
}

// Test 9: a pass that applies no decision takes no lock and commits despite
// a concurrent change, as before the fence.
func TestUnfencedPlanUnchanged(t *testing.T) {
	c := newEntityCatalog(t)
	small := c.seedCandidates()
	c.build()
	f := &fencedCatalog{entityCatalog: c, small: small}
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	locks := -1
	restore := services.SetAuthorEntityHooks(func() { f.retitle(f.bookOfFill(small)) }, func(step string) error {
		if step == "complete" {
			locks = f.fenceLocks(true)
		}
		return nil
	})
	defer restore()
	f.attempt(loop)
	assert.Zero(t, locks, "an unfenced pass took the fence's lock")
	got := f.run(run)
	assert.Equal(t, database.AuthorEntityRunCompleted, got.Status)
	assert.Equal(t, 1, got.Attempts)
	rep := c.reportOf(run)
	assert.Zero(t, rep.FenceRefusals)
	assert.Nil(t, rep.Model)
}

// Test 10: three refusals in a row fail the pass input_moved, and its marks
// are given back.
func TestFenceRefusedThriceInputMoved(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	before := f.layout()
	restore := services.SetAuthorEntityHooks(func() { f.retitle(f.other) }, nil)
	defer restore()
	for i := range 3 {
		f.attempt(loop)
		if i < 2 {
			assert.Equal(t, database.AuthorEntityRunRunning, f.run(run).Status, "refusal %d", i+1)
		}
	}
	restore()
	got := f.run(run)
	assert.Equal(t, database.AuthorEntityRunFailed, got.Status)
	require.NotNil(t, got.LastErrorClass)
	assert.Equal(t, services.AuthorEntityBuildErrorInputMoved, *got.LastErrorClass)
	assert.Equal(t, 3, got.Attempts)
	assert.Equal(t, before, f.layout())
	require.NoError(t, database.SettleAuthorAutopilotMarks(context.Background(), f.db))
	assert.Zero(t, f.count(fmt.Sprintf(`SELECT count(*) FROM author_autopilot_mark WHERE build_run_id = %d`, run)),
		"the failed pass kept its marks")
	assert.Positive(t, f.marks())

	f.build()
	assert.Equal(t, "applied", f.state())
}

// A refusal and then a landing attempt: the report counts the refusal.
func TestALandedPassReportsItsRefusals(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	f.refused(run, loop, func() { f.retitle(f.other) }, nil)
	f.completes(run, loop)
	rep := f.reportOf(run)
	assert.Equal(t, 1, rep.FenceRefusals)
	assert.Equal(t, 2, rep.Attempts)

	// The same worker counts the next pass's refusals from zero.
	next, _ := f.start(services.AuthorEntityBuildConfig{})
	f.refused(next, loop, func() { f.retitle(f.other) }, nil)
	f.completes(next, loop)
	assert.Equal(t, 1, f.reportOf(next).FenceRefusals)
}

// Lapses and revivals are not fenced (§4.9): a pass whose decision lapses,
// or which revives a revoked merge, lands despite a concurrent change.
func TestALapseOrRevivalIsNotFenced(t *testing.T) {
	for _, tc := range []string{"lapse", "revival"} {
		t.Run(tc, func(t *testing.T) {
			f := newFencedCatalog(t)
			if tc == "lapse" {
				f.retitle(f.bookOfFill(f.small))
			} else {
				f.build()
				_, err := database.RevokeMergeDecision(context.Background(), f.db, f.decision, 0)
				require.NoError(t, err)
			}
			run, loop := f.start(services.AuthorEntityBuildConfig{})
			defer services.SetAuthorEntityHooks(func() { f.retitle(f.other) }, nil)()
			f.completes(run, loop)
			assert.Equal(t, 1, f.run(run).Attempts)
			assert.Equal(t, f.smallID, f.entityOf(f.small))
			if tc == "lapse" {
				assert.Equal(t, "lapsed/input_changed", f.state())
			} else {
				assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_entity_revival WHERE outcome = 'exact'`))
			}
		})
	}
}

// Test 11: a writer holding its mark past the lock timeout fails the
// attempt with nothing written; the next attempt lands.
func TestFenceLockTimeout(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{LockTimeout: 200 * time.Millisecond})
	var writer *pg.Tx
	f.refused(run, loop, nil, func(step string) error {
		if step == "fence" && writer == nil {
			writer = f.markInFlight()
		}
		return nil
	})
	require.NotNil(t, writer)
	require.NoError(t, writer.Rollback())
	f.completes(run, loop)
	assert.Equal(t, "applied", f.state())
	assert.Equal(t, 1, f.reportOf(run).FenceRefusals)
}

// Test 12: a Wikidata publication and a manual row are inputs too.
func TestFenceWikidataPublish(t *testing.T) {
	f := newFencedCatalog(t)
	f.exec(`INSERT INTO wikidata_extract (source) VALUES ('fixture')`)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	f.refused(run, loop, func() {
		f.exec(`UPDATE wikidata_extract SET status = 'published', content_id = 'wd-0123456789abcdef', manifest = '{}'
			WHERE status = 'fetching'`)
	}, nil)
}

func TestFenceManualRow(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	f.refused(run, loop, func() {
		f.exec(`UPDATE author_entity_assignment SET method = 'manual', rule = 'manual' WHERE source_fingerprint = ?`,
			bytes.Repeat([]byte{0x6a}, 32))
	}, nil)
	f.completes(run, loop)
	assert.Equal(t, "applied", f.state())
}

// Test 13: a change committed after the pass started but before its
// snapshot is in the plan; the fence accepts it.
func TestChangeBeforeSnapshotAccepted(t *testing.T) {
	f := newFencedCatalog(t)
	run, loop := f.start(services.AuthorEntityBuildConfig{})
	f.retitle(f.other)
	require.Positive(t, f.marks(), "the change left an untaken mark")
	f.completes(run, loop)
	assert.Equal(t, "applied", f.state())
	assert.Equal(t, 1, f.run(run).Attempts)
	assert.Zero(t, f.reportOf(run).FenceRefusals)
}

// The plan reads one snapshot, fenced or not: a card input edited after the
// plan's first load is not in the pass's census, which keeps its
// candidates; the next pass sees the edit.
func TestThePlanReadsOneSnapshot(t *testing.T) {
	c := newEntityCatalog(t)
	small := c.seedCandidates()
	c.build()
	f := &fencedCatalog{entityCatalog: c, small: small}
	book := f.bookOfFill(small)
	edited := false
	restore := services.SetAuthorEntityLoadHook(func(step string) {
		if step == services.AuthorEntityLoadInput && !edited {
			edited = true
			f.retitle(book)
		}
	})
	defer restore()
	rep := c.build()
	require.True(t, edited)
	assert.Equal(t, database.MergeCandidateDiff{Kept: 2}, rep.CandidateDiff, "the census saw an edit after the snapshot")
	restore()
	assert.Equal(t, 1, c.build().CandidateDiff.Inserted)
}

// The plan's read transaction bounds its statements and its lock waits
// (E4b-2 review P3): 90 s a statement, the worker's lock timeout a wait.
func TestThePlanReadIsBounded(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedCandidates()
	var statement, lock, isolation string
	defer services.SetAuthorEntityReadHook(func(tx *pg.Tx) {
		_, err := tx.QueryOne(pg.Scan(&statement, &lock, &isolation), `SELECT current_setting('statement_timeout'),
			current_setting('lock_timeout'), current_setting('transaction_isolation')`)
		require.NoError(t, err)
	})()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(context.Background(), 0)
	require.NoError(t, err)
	runToCompletion(t, services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{
		LockTimeout: 3 * time.Second}), c.db, run.ID)
	assert.Equal(t, "90s", statement)
	assert.Equal(t, "3s", lock)
	assert.Equal(t, "repeatable read", isolation)
}
