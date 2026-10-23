package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The author entity build pass (design E, phase E1) driven by its worker
// over a scratch catalog: it completes with a report, a second pass writes
// nothing, and a pass a worker left halfway is finished by the next one.

// entityCatalog seeds books with selected author credits.
type entityCatalog struct {
	t    *testing.T
	db   *pg.DB
	next int64
}

func newEntityCatalog(t *testing.T) *entityCatalog {
	t.Helper()
	db := scanfixture.ScratchDB(t)
	c := &entityCatalog{t: t, db: db, next: 1000}
	c.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES ('4', 'structured_person', 'Cyrl', 'normalizer-v1', ?, 'internal/authornorm/policy_evidence/fixture.json',
		 'shipped')`, bytes.Repeat([]byte{0xee}, 32))
	return c
}

func (c *entityCatalog) exec(query string, params ...interface{}) {
	c.t.Helper()
	_, err := c.db.Exec(query, params...)
	require.NoError(c.t, err, query)
}

// book adds a book with a current snapshot and returns the snapshot.
func (c *entityCatalog) book(title string, series ...int64) int64 {
	c.t.Helper()
	c.next++
	id := c.next
	c.exec(`INSERT INTO opds_catalog_book (id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES (?, ?, 'fixture.zip', 'fb2', now(), '', 'ru', ?, '', ?)`, id, fmt.Sprintf("%d.fb2", id), title,
		fmt.Sprintf("%032x", id))
	for _, s := range series {
		c.exec(`INSERT INTO opds_catalog_series (id, ser, lang_code) VALUES (?, ?, 1) ON CONFLICT (id) DO NOTHING`,
			s, fmt.Sprintf("series %d", s))
		c.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (0, ?, ?)`, id, s)
	}
	var snap int64
	_, err := c.db.QueryOne(pg.Scan(&snap), `INSERT INTO book_metadata_snapshot
		(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name)
		VALUES (?, ?, 'extractor-v1', 'live', 'extracted', 'fixture.zip', ?) RETURNING id`,
		id, fmt.Sprintf("%032x", id), fmt.Sprintf("%d.fb2", id))
	require.NoError(c.t, err)
	return snap
}

// credit adds a selected author credit of the snapshot; fill names its
// fingerprint, one result per fingerprint.
func (c *entityCatalog) credit(snap int64, fill byte, family, given string, additional ...string) {
	c.t.Helper()
	fp := bytes.Repeat([]byte{fill}, 32)
	display := strings.Join(append(append([]string{given}, additional...), family), " ")
	var credit, result int64
	_, err := c.db.QueryOne(pg.Scan(&credit), `INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_last_name, source_display_name, source_fingerprint)
		VALUES (?, 'author', 0, ?, ?, ?, ?) RETURNING id`, snap, given, family, display, fp)
	require.NoError(c.t, err)
	_, err = c.db.Query(pg.Scan(&result), `SELECT id FROM contributor_normalization_result WHERE source_fingerprint = ?`, fp)
	require.NoError(c.t, err)
	if result == 0 {
		key := bytes.Repeat([]byte{fill ^ 0xff}, 32)
		_, err = c.db.QueryOne(pg.Scan(&result), `INSERT INTO contributor_normalization_result
			(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
			 method, kind, status, decision_class, family_name, given_name, additional_names,
			 display_name, sort_name, search_key, script)
			VALUES (?, ?, 'extractor-v1', 'normalizer-v1', 'result-v1', 'rules', 'person', 'normalized',
			 'structured_person', ?, ?, coalesce(?::text[], '{}'), ?, ?, ?, 'Cyrl') RETURNING id`,
			fp, key, family, given, pg.Array(additional), display, display, authornorm.SearchKey(display))
		require.NoError(c.t, err)
	}
	c.exec(`INSERT INTO book_contributor_credit_selection
		(credit_id, source_fingerprint, state, result_id, basis, policy_version)
		VALUES (?, ?, 'selected', ?, 'automatic', '4')`, credit, fp, result)
}

// seedBucket is a full name, a short-name book that moves to it by series
// and one that stays in the bucket, and an unrelated author.
func (c *entityCatalog) seedBucket() {
	s1 := c.book("Сердце пармы", 501)
	s2 := c.book("Золото бунта", 501)
	s3 := c.book("Психология")
	s4 := c.book("Война и мир")
	c.credit(s1, 0x51, "Иванов", "Алексей", "Викторович")
	c.credit(s2, 0x52, "Иванов", "Алексей")
	c.credit(s3, 0x52, "Иванов", "Алексей")
	c.credit(s4, 0x53, "Толстой", "Лев", "Николаевич")
}

// runToCompletion steps the loop until the pass completes.
func runToCompletion(t *testing.T, loop *services.AuthorEntityBuildLoop, db *pg.DB, run int64) *database.AuthorEntityBuildRun {
	t.Helper()
	ctx := context.Background()
	for range 100 {
		_, err := loop.RunOnce(ctx)
		require.NoError(t, err)
		latest, _, err := database.LatestAuthorEntityBuilds(ctx, db)
		require.NoError(t, err)
		if latest.ID == run && latest.Status == database.AuthorEntityRunCompleted {
			return latest
		}
	}
	t.Fatal("the pass did not complete")
	return nil
}

func TestTheEntityBuildCompletesWithAReportAndASecondPassWritesNothing(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedBucket()
	ctx := context.Background()
	admin := services.NewAuthorEntityAdmin(c.db)
	run, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	_, err = admin.Start(ctx, 0)
	require.ErrorIs(t, err, database.ErrAuthorEntityBuildActive)

	loop := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{Batch: 1})
	done := runToCompletion(t, loop, c.db, run.ID)
	assert.Equal(t, done.UnitsTotal, done.UnitsDone)
	assert.Positive(t, done.Changes)

	st, err := admin.Status(ctx)
	require.NoError(t, err)
	var rep services.AuthorEntityBuildReport
	require.NoError(t, json.Unmarshal(st.Report, &rep))
	assert.Equal(t, 3, rep.Entities, "the full name, the bucket and Tolstoy")
	assert.Equal(t, 1, rep.Buckets.Moved)
	assert.Equal(t, 1, rep.Buckets.MovedBy["series"])
	assert.Equal(t, 1, rep.Buckets.Remaining)
	assert.Equal(t, int(done.Changes), rep.Changes)
	assert.Nil(t, st.Run.Report, "the report is shown once, beside the run")

	again, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	second := runToCompletion(t, loop, c.db, again.ID)
	assert.Zero(t, second.Changes, "a second pass over the same catalog writes nothing")
	assert.Zero(t, second.UnitsTotal)
}

// liveEntities counts the stored live entities.
func liveEntities(t *testing.T, db *pg.DB) int {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM author_entity WHERE merged_into IS NULL`)
	require.NoError(t, err)
	return n
}

// A pass is written in one transaction: a crash after some of its units
// leaves nothing behind — no entity, no assignment, the run still running —
// and the next worker, once the lease is gone, plans again from scratch and
// ends where an uninterrupted pass ends.
func TestACrashedEntityBuildLeavesNothingAndIsDoneAgain(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedBucket()
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	cfg := services.AuthorEntityBuildConfig{Batch: 1, Lease: 50 * time.Millisecond}
	crash := errors.New("the worker died")
	batches := 0
	restore := services.SetAuthorEntityHooks(nil, func(step string) error {
		batches++
		if step == "batch" && batches == 2 {
			return crash
		}
		return nil
	})
	_, err = services.NewAuthorEntityBuildLoop(c.db, cfg).RunOnce(ctx)
	restore()
	require.ErrorIs(t, err, crash)
	assert.Zero(t, liveEntities(t, c.db), "the units written before the crash rolled back")
	latest, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, database.AuthorEntityRunRunning, latest.Status)

	next := services.NewAuthorEntityBuildLoop(c.db, cfg)
	worked, err := next.RunOnce(ctx)
	require.NoError(t, err)
	require.False(t, worked, "the crashed worker's lease still holds the pass")
	time.Sleep(100 * time.Millisecond)
	done := runToCompletion(t, next, c.db, run.ID)
	assert.Equal(t, 2, done.Attempts)
	assert.Equal(t, done.UnitsTotal, done.UnitsDone)
	assert.Equal(t, 3, liveEntities(t, c.db))

	whole := newEntityCatalog(t)
	whole.seedBucket()
	wholeRun, err := services.NewAuthorEntityAdmin(whole.db).Start(ctx, 0)
	require.NoError(t, err)
	uninterrupted := runToCompletion(t, services.NewAuthorEntityBuildLoop(whole.db, cfg), whole.db, wholeRun.ID)
	assert.Equal(t, uninterrupted.Changes, done.Changes, "the pass that landed wrote what an uninterrupted one writes")
	assert.Equal(t, uninterrupted.UnitsTotal, done.UnitsTotal)
}

// A worker whose lease another took between its plan and its write writes
// nothing (review E2 r2, finding 1: a stale completion).
func TestAStaleWorkerWritesNothing(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedBucket()
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	cfg := services.AuthorEntityBuildConfig{Batch: 1, Lease: time.Minute}
	takeover := func() {
		c.exec(`UPDATE author_entity_build_run SET lease_expires_at = now() - interval '1 second' WHERE id = ?`, run.ID)
		claimed, claimErr := database.ClaimAuthorEntityBuild(ctx, c.db, uuid.New(), time.Minute)
		require.NoError(t, claimErr)
		require.NotNil(t, claimed)
	}
	batches := 0
	restore := services.SetAuthorEntityHooks(takeover, func(string) error { batches++; return nil })
	_, err = services.NewAuthorEntityBuildLoop(c.db, cfg).RunOnce(ctx)
	restore()
	require.ErrorIs(t, err, database.ErrAuthorEntityLeaseLost)
	assert.Zero(t, batches, "the stale worker is stopped before its first write")
	assert.Zero(t, liveEntities(t, c.db))
	latest, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	assert.Equal(t, database.AuthorEntityRunRunning, latest.Status, "the new owner's pass is untouched")
}

// A completion that fails takes the whole pass with it: nothing written, the
// run still running, the next attempt lands it (review E2 r2, finding 1: a
// failed completion).
func TestAFailedCompletionTakesThePassWithIt(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedBucket()
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	cfg := services.AuthorEntityBuildConfig{Batch: 1, Lease: 50 * time.Millisecond}
	failed := errors.New("the completion failed")
	restore := services.SetAuthorEntityHooks(nil, func(step string) error {
		if step == "complete" {
			return failed
		}
		return nil
	})
	loop := services.NewAuthorEntityBuildLoop(c.db, cfg)
	_, err = loop.RunOnce(ctx)
	restore()
	require.ErrorIs(t, err, failed)
	assert.Zero(t, liveEntities(t, c.db))
	latest, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, database.AuthorEntityRunRunning, latest.Status)
	time.Sleep(100 * time.Millisecond)
	runToCompletion(t, loop, c.db, run.ID)
	assert.Equal(t, 3, liveEntities(t, c.db))
}

// entityOf is the entity the fingerprint rows give a credit's
// fingerprint, or the credit's own row.
func entityOf(t *testing.T, db *pg.DB, fill byte, book int64) string {
	t.Helper()
	var id string
	_, err := db.QueryOne(pg.Scan(&id), `SELECT coalesce(
		(SELECT ca.entity_id::text FROM author_entity_credit_assignment ca JOIN book_contributor_credit c ON c.id = ca.credit_id
		 JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE c.source_fingerprint = ? AND s.book_id = ?),
		(SELECT a.entity_id::text FROM author_entity_assignment a WHERE a.source_fingerprint = ?), '')`,
		bytes.Repeat([]byte{fill}, 32), book, bytes.Repeat([]byte{fill}, 32))
	require.NoError(t, err)
	return id
}

// splitAndMerge is the review E2 r2 fixture: X a three-book short-name
// bucket, Y a five-book full name, Z a four-book other author, built once.
// evolve then makes Z's result read as the short name and puts Y0, X0 and
// X1 in one series: the whole plan keeps Y's ID for the full group, gives
// the bucket Z's ID and merges X into Y.
type splitAndMerge struct {
	c             *entityCatalog
	x, y, z       []int64 // snapshots
	idX, idY, idZ string
}

func newSplitAndMerge(t *testing.T) *splitAndMerge {
	c := newEntityCatalog(t)
	f := &splitAndMerge{c: c}
	for i := range 3 {
		f.x = append(f.x, c.book(fmt.Sprintf("X%d", i)))
	}
	for i := range 5 {
		f.y = append(f.y, c.book(fmt.Sprintf("Y%d", i)))
	}
	for i := range 4 {
		f.z = append(f.z, c.book(fmt.Sprintf("Z%d", i)))
	}
	for _, s := range f.x {
		c.credit(s, 0x81, "Иванов", "Алексей")
	}
	for _, s := range f.y {
		c.credit(s, 0x82, "Иванов", "Алексей", "Викторович")
	}
	for _, s := range f.z {
		c.credit(s, 0x83, "Петров", "Пётр", "Ильич")
	}
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	runToCompletion(t, services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{}), c.db, run.ID)
	f.idX, f.idY, f.idZ = entityOf(t, c.db, 0x81, c.bookOf(f.x[0])), entityOf(t, c.db, 0x82, c.bookOf(f.y[0])),
		entityOf(t, c.db, 0x83, c.bookOf(f.z[0]))
	return f
}

func (f *splitAndMerge) evolve() {
	c := f.c
	c.exec(`ALTER TABLE contributor_normalization_result DISABLE TRIGGER USER`)
	c.exec(`UPDATE contributor_normalization_result SET family_name = 'Иванов', given_name = 'Алексей',
		additional_names = '{}', display_name = 'Алексей Иванов', sort_name = 'Алексей Иванов', search_key = 'алексей иванов'
		WHERE source_fingerprint = ?`, bytes.Repeat([]byte{0x83}, 32))
	c.exec(`ALTER TABLE contributor_normalization_result ENABLE TRIGGER USER`)
	c.exec(`INSERT INTO opds_catalog_series (id, ser, lang_code) VALUES (777, 'series 777', 1)`)
	for _, s := range []int64{f.y[0], f.x[0], f.x[1]} {
		c.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (0, ?, 777)`, c.bookOf(s))
	}
}

// converged checks the whole plan's end: the full group keeps Y's ID, the
// bucket has Z's, X went into Y, and the pass that did it merged one entity
// and orphaned none.
func (f *splitAndMerge) converged(t *testing.T, report json.RawMessage, movedSnap, bucketSnap int64) {
	t.Helper()
	c := f.c
	assert.Equal(t, f.idY, entityOf(t, c.db, 0x82, c.bookOf(f.y[0])), "the full group keeps Y's ID")
	assert.Equal(t, f.idY, entityOf(t, c.db, 0x81, c.bookOf(movedSnap)), "a moved X book is Y's")
	assert.Equal(t, f.idZ, entityOf(t, c.db, 0x81, c.bookOf(bucketSnap)), "the bucket takes Z's ID")
	var merged string
	_, err := c.db.QueryOne(pg.Scan(&merged), `SELECT coalesce(merged_into::text, '') FROM author_entity WHERE id = ?`, f.idX)
	require.NoError(t, err)
	assert.Equal(t, f.idY, merged, "X went into Y, as in the whole plan")
	if report != nil {
		var rep struct {
			IDs struct{ Merged, Orphaned int } `json:"ids"`
		}
		require.NoError(t, json.Unmarshal(report, &rep))
		assert.Equal(t, 1, rep.IDs.Merged)
		assert.Zero(t, rep.IDs.Orphaned)
	}
}

// bookOf is a snapshot's book.
func (c *entityCatalog) bookOf(snap int64) int64 {
	c.t.Helper()
	var id int64
	_, err := c.db.QueryOne(pg.Scan(&id), `SELECT book_id FROM book_metadata_snapshot WHERE id = ?`, snap)
	require.NoError(c.t, err)
	return id
}

// storedRows are the stored rows of the entities, one line each, sorted:
// the entity rows, their fingerprint and credit rows and their merges.
func storedRows(t *testing.T, db *pg.DB, ids ...string) string {
	t.Helper()
	var out string
	_, err := db.QueryOne(pg.Scan(&out), `SELECT coalesce(string_agg(line, E'\n' ORDER BY line), '') FROM (
		SELECT 'entity ' || id || ' ' || display_name || ' ' || coalesce(merged_into::text, '-') AS line
		FROM author_entity WHERE id::text = ANY (?0)
		UNION ALL SELECT 'fp ' || encode(source_fingerprint, 'hex') || ' ' || ordinal || ' ' || entity_id || ' ' || rule
		FROM author_entity_assignment WHERE entity_id::text = ANY (?0)
		UNION ALL SELECT 'credit ' || credit_id || ' ' || ordinal || ' ' || coalesce(entity_id::text, '-') || ' ' || rule
		FROM author_entity_credit_assignment WHERE entity_id::text = ANY (?0)
		UNION ALL SELECT 'merge ' || from_id || ' ' || to_id FROM author_entity_merge_log
		WHERE from_id::text = ANY (?0) OR to_id::text = ANY (?0)) x`, pg.Array(ids))
	require.NoError(t, err)
	return out
}

// passReport runs a new pass to completion and returns its report.
func passReport(t *testing.T, c *entityCatalog, loop *services.AuthorEntityBuildLoop) json.RawMessage {
	t.Helper()
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	runToCompletion(t, loop, c.db, run.ID)
	_, completed, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, run.ID, completed.ID)
	return completed.Report
}

// Review E2 r2, finding 2: a split and a merge at once, crashed after the
// first batch: everything rolls back, and the next attempt does exactly the
// whole plan.
func TestASplitAndMergeCrashedHalfwayEndsAsTheWholePlan(t *testing.T) {
	f := newSplitAndMerge(t)
	f.evolve()
	ctx := context.Background()
	loop := services.NewAuthorEntityBuildLoop(f.c.db, services.AuthorEntityBuildConfig{Batch: 1, Lease: 50 * time.Millisecond})
	second, err := services.NewAuthorEntityAdmin(f.c.db).Start(ctx, 0)
	require.NoError(t, err)
	crash := errors.New("the worker died")
	restore := services.SetAuthorEntityHooks(nil, func(step string) error {
		if step == "batch" {
			return crash
		}
		return nil
	})
	_, err = loop.RunOnce(ctx)
	restore()
	require.ErrorIs(t, err, crash)
	time.Sleep(100 * time.Millisecond)
	runToCompletion(t, loop, f.c.db, second.ID)
	f.converged(t, nil, f.x[0], f.x[2])
}

// Review E2 r3: a scan deletes the layer of a moved X book between the plan
// and its write. Y's unit then refers to a credit that is gone and is
// refused (23503); the bucket's unit, which would move X's fingerprint to
// Z, is refused with it — the store keeps every row of X, Y and Z as it
// was, while an unrelated new author lands. The next pass ends where a
// whole plan after the deletion ends: X merged into Y, nothing orphaned.
func TestASplitAndMergeRefusedByAScanIsDoneWholeNextTime(t *testing.T) {
	f := newSplitAndMerge(t)
	f.evolve()
	c := f.c
	c.credit(c.book("Чужая"), 0x84, "Сидоров", "Пётр", "Ильич")
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	before := storedRows(t, c.db, f.idX, f.idY, f.idZ)
	restore := services.SetAuthorEntityHooks(func() {
		c.exec(`SELECT author_layer_delete_books(ARRAY[?]::bigint[])`, c.bookOf(f.x[0]))
	}, nil)
	loop := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{})
	_, err = loop.RunOnce(ctx)
	restore()
	require.NoError(t, err)
	done, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, run.ID, done.ID)
	require.Equal(t, database.AuthorEntityRunCompleted, done.Status)
	assert.GreaterOrEqual(t, done.UnitsSkipped, 2, "the split and the merge were refused together")
	assert.Equal(t, before, storedRows(t, c.db, f.idX, f.idY, f.idZ), "nothing of X, Y or Z changed")
	assert.Equal(t, 4, liveEntities(t, c.db), "the unrelated author landed")

	f.converged(t, passReport(t, c, loop), f.x[1], f.x[2])
}

// The same with no scan: the database refuses Y's unit (a row it writes is
// held past the lock timeout). The bucket's unit goes with it, the store is
// as before, and the next pass merges X into Y.
func TestASplitAndMergeRefusedByAHeldRowIsDoneWholeNextTime(t *testing.T) {
	f := newSplitAndMerge(t)
	f.evolve()
	c := f.c
	c.credit(c.book("Чужая"), 0x84, "Сидоров", "Пётр", "Ильич")
	ctx := context.Background()
	_, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	before := storedRows(t, c.db, f.idX, f.idY, f.idZ)
	var moved int64 // the credit of a moved X book: Y's unit writes its decision
	_, err = c.db.QueryOne(pg.Scan(&moved), `SELECT id FROM book_contributor_credit WHERE snapshot_id = ?`, f.x[1])
	require.NoError(t, err)
	review, err := c.db.Begin()
	require.NoError(t, err)
	_, err = review.Exec(`SELECT 1 FROM book_contributor_credit WHERE id = ? FOR UPDATE`, moved)
	require.NoError(t, err)
	loop := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{LockTimeout: 200 * time.Millisecond})
	_, err = loop.RunOnce(ctx)
	require.NoError(t, review.Rollback())
	require.NoError(t, err)
	done, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, database.AuthorEntityRunCompleted, done.Status)
	assert.GreaterOrEqual(t, done.UnitsSkipped, 2, "the split and the merge were refused together")
	assert.Equal(t, before, storedRows(t, c.db, f.idX, f.idY, f.idZ), "nothing of X, Y or Z changed")
	assert.Equal(t, 4, liveEntities(t, c.db), "the unrelated author landed")

	f.converged(t, passReport(t, c, loop), f.x[0], f.x[2])
}

// Review E2 r3, the r1 split: a three-book short-name bucket and a one-book
// full name; a series moves two bucket books to the full name. The database
// refuses the remainder's unit (its fingerprint row is held): the full
// group's unit, which takes the bucket's ID and absorbs the old full name,
// is refused with it — so the remainder book is never linked to the full
// group under the bucket's ID. The store is as before; the next pass ends
// where the whole plan ends.
func TestARefusedSplitLeavesTheRemainderAlone(t *testing.T) {
	c := newEntityCatalog(t)
	short := []int64{c.book("Одна"), c.book("Две"), c.book("Три")}
	full := c.book("Своя")
	for _, s := range short {
		c.credit(s, 0x71, "Иванов", "Алексей")
	}
	c.credit(full, 0x72, "Иванов", "Алексей", "Викторович")
	ctx := context.Background()
	loop := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{LockTimeout: 200 * time.Millisecond})
	passReport(t, c, loop)
	bucket, fullID := entityOf(t, c.db, 0x71, c.bookOf(short[2])), entityOf(t, c.db, 0x72, c.bookOf(full))
	c.exec(`INSERT INTO opds_catalog_series (id, ser, lang_code) VALUES (778, 'series 778', 1)`)
	for _, s := range []int64{full, short[0], short[1]} {
		c.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (0, ?, 778)`, c.bookOf(s))
	}
	c.credit(c.book("Чужая"), 0x73, "Сидоров", "Пётр", "Ильич")
	before := storedRows(t, c.db, bucket, fullID)

	_, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	held, err := c.db.Begin()
	require.NoError(t, err)
	_, err = held.Exec(`SELECT 1 FROM author_entity_assignment WHERE source_fingerprint = ? FOR UPDATE`,
		bytes.Repeat([]byte{0x71}, 32))
	require.NoError(t, err)
	_, err = loop.RunOnce(ctx)
	require.NoError(t, held.Rollback())
	require.NoError(t, err)
	done, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, database.AuthorEntityRunCompleted, done.Status)
	assert.GreaterOrEqual(t, done.UnitsSkipped, 2, "the split was refused whole")
	assert.Equal(t, before, storedRows(t, c.db, bucket, fullID), "nothing of the bucket or the full name changed")
	assert.Equal(t, bucket, entityOf(t, c.db, 0x71, c.bookOf(short[2])), "the remainder book keeps its bucket")
	assert.Equal(t, 3, liveEntities(t, c.db), "the unrelated author landed")

	rep := passReport(t, c, loop)
	assert.Equal(t, bucket, entityOf(t, c.db, 0x72, c.bookOf(full)), "the full group takes the bucket's ID")
	assert.Equal(t, bucket, entityOf(t, c.db, 0x71, c.bookOf(short[0])))
	remainder := entityOf(t, c.db, 0x71, c.bookOf(short[2]))
	assert.NotContains(t, []string{"", bucket, fullID}, remainder, "the remainder gets a new ID")
	var merged string
	_, err = c.db.QueryOne(pg.Scan(&merged), `SELECT coalesce(merged_into::text, '') FROM author_entity WHERE id = ?`, fullID)
	require.NoError(t, err)
	assert.Equal(t, bucket, merged, "the old full name went into the bucket's ID")
	var ids struct {
		IDs struct{ Merged, Orphaned int } `json:"ids"`
	}
	require.NoError(t, json.Unmarshal(rep, &ids))
	assert.Equal(t, 1, ids.IDs.Merged)
	assert.Zero(t, ids.IDs.Orphaned)
}

// A scan that replaces and deletes credits while a pass is planned and
// applied leaves stale rows behind for a moment; the next pass and the read
// model's worker bring everything back in line with the credits: no
// assignment names a fingerprint or a credit no current selected credit
// has, every row carries its entity, and the name index needs no recount.
func TestAScanDuringABuildIsReconciledByTheNextBuildAndTheReadModel(t *testing.T) {
	c := newEntityCatalog(t)
	ctx := context.Background()
	s1 := c.book("Сердце пармы", 502)
	s2 := c.book("Золото бунта", 502)
	s3 := c.book("Психология")
	c.credit(s1, 0x61, "Иванов", "Алексей", "Викторович")
	c.credit(s2, 0x62, "Иванов", "Алексей")
	c.credit(s3, 0x63, "Петров", "Иван", "Сергеевич")
	admin := services.NewAuthorEntityAdmin(c.db)
	run, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	loop := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{Batch: 1})

	// The scan, between the plan and its writes: book 2 gets a new file with
	// another credit, book 3 loses its layer; both are marked as a scan does.
	var book2, book3 int64
	_, err = c.db.QueryOne(pg.Scan(&book2), `SELECT book_id FROM book_metadata_snapshot WHERE id = ?`, s2)
	require.NoError(t, err)
	_, err = c.db.QueryOne(pg.Scan(&book3), `SELECT book_id FROM book_metadata_snapshot WHERE id = ?`, s3)
	require.NoError(t, err)
	scan := func() {
		c.exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE id = ?`, s2)
		var s2b int64
		_, scanErr := c.db.QueryOne(pg.Scan(&s2b), `INSERT INTO book_metadata_snapshot
			(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name)
			VALUES (?, ?, 'extractor-v1', 'live', 'extracted', 'fixture.zip', 'v2.fb2') RETURNING id`,
			book2, fmt.Sprintf("%032x", book2+1000000))
		require.NoError(t, scanErr)
		c.credit(s2b, 0x64, "Сидоров", "Пётр", "Ильич")
		c.exec(`SELECT author_layer_delete_books(ARRAY[?]::bigint[])`, book3)
		require.NoError(t, database.MarkAuthorDisplayDirty(ctx, c.db, book2, book3))
	}
	restore := services.SetAuthorEntityHooks(scan, nil)
	_, err = loop.RunOnce(ctx)
	restore()
	require.NoError(t, err)

	done, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, run.ID, done.ID)
	require.Equal(t, database.AuthorEntityRunCompleted, done.Status)
	next, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	runToCompletion(t, loop, c.db, next.ID)
	require.NoError(t, c.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, drainErr := database.DrainAuthorDisplayMarks(ctx, tx, 100000)
		return drainErr
	}))

	count := func(query string, params ...interface{}) int {
		var n int
		_, countErr := c.db.QueryOne(pg.Scan(&n), query, params...)
		require.NoError(t, countErr)
		return n
	}
	assert.Zero(t, count(`SELECT count(*) FROM author_entity_assignment a WHERE NOT EXISTS (
		SELECT 1 FROM book_contributor_credit cr JOIN book_metadata_snapshot s ON s.id = cr.snapshot_id AND s.is_current
		JOIN book_contributor_credit_selection sel ON sel.credit_id = cr.id AND sel.state = 'selected'
		WHERE cr.source_fingerprint = a.source_fingerprint)`), "no fingerprint row without a current credit")
	assert.Zero(t, count(`SELECT count(*) FROM author_entity_credit_assignment a WHERE NOT EXISTS (
		SELECT 1 FROM book_contributor_credit cr JOIN book_metadata_snapshot s ON s.id = cr.snapshot_id AND s.is_current
		WHERE cr.id = a.credit_id)`), "no credit row for a credit of an old file")
	assert.Equal(t, 1, count(`SELECT count(*) FROM book_author_display WHERE book_id = ? AND entity_ids IS NOT NULL`, book2),
		"the new credit carries its entity")
	assert.Zero(t, count(`SELECT count(*) FROM book_author_display WHERE book_id = ? AND entity_ids IS NOT NULL`, book3))

	// A walk over the model now finds nothing to change, in rows or names.
	c.exec(`UPDATE book_author_display_reconcile SET walk_cursor = NULL, finished_at = NULL`)
	var step database.AuthorDisplayReconcileStep
	for !step.Finished {
		require.NoError(t, c.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			var stepErr error
			step, stepErr = database.ReconcileAuthorDisplayStep(ctx, tx, 1000, time.Hour)
			return stepErr
		}))
	}
	assert.Zero(t, step.WalkDiffered, "every row was already right")
	assert.Zero(t, step.NamesDiffered, "the name indexes were already right")
}

// While a pass's transaction runs, no other worker can claim it, even when
// its lease runs out meanwhile: the run row is locked.
func TestAPassInItsTransactionCannotBeClaimed(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedBucket()
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	cfg := services.AuthorEntityBuildConfig{Batch: 1, Lease: 50 * time.Millisecond}
	var stolen *database.AuthorEntityBuildRun
	tried := false
	restore := services.SetAuthorEntityHooks(nil, func(step string) error {
		if step != "batch" || tried {
			return nil
		}
		tried = true
		time.Sleep(100 * time.Millisecond) // the lease runs out
		var claimErr error
		stolen, claimErr = database.ClaimAuthorEntityBuild(ctx, c.db, uuid.New(), time.Minute)
		return claimErr
	})
	_, err = services.NewAuthorEntityBuildLoop(c.db, cfg).RunOnce(ctx)
	restore()
	require.NoError(t, err)
	assert.Nil(t, stolen, "the pass was claimed while its transaction ran")
	latest, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	assert.Equal(t, run.ID, latest.ID)
	assert.Equal(t, database.AuthorEntityRunCompleted, latest.Status)
}

// A scan that deletes books' layers while the pass's transaction runs waits
// for the pass to commit — the pass holds the author layer lock shared —
// instead of racing it or deadlocking with it on credit rows: both succeed,
// the pass refuses no unit, and the next pass removes what the deleted
// credits left behind.
func TestABookDeletionDuringTheBuildWaitsForIt(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedBucket()
	ctx := context.Background()
	admin := services.NewAuthorEntityAdmin(c.db)
	run, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	var books []int64
	_, err = c.db.Query(&books, `SELECT id FROM opds_catalog_book ORDER BY id`)
	require.NoError(t, err)
	gone := books[2:] // the bucket's own book and the unrelated author's

	deleted := make(chan error, 1)
	started, early := false, false
	restore := services.SetAuthorEntityHooks(nil, func(step string) error {
		if step != "batch" || started {
			return nil
		}
		started = true
		go func() {
			deleted <- c.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
				_, delErr := tx.Exec(`SELECT author_layer_delete_books(?::bigint[])`, pg.Array(gone))
				return delErr
			})
		}()
		select {
		case delErr := <-deleted:
			early = true
			deleted <- delErr
		case <-time.After(300 * time.Millisecond):
		}
		return nil
	})
	loop := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{Batch: 1})
	_, err = loop.RunOnce(ctx)
	restore()
	require.NoError(t, err)
	require.True(t, started)
	require.NoError(t, <-deleted, "the deletion succeeded after the pass")
	assert.False(t, early, "the deletion went through while the pass's transaction ran")

	done, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, run.ID, done.ID)
	require.Equal(t, database.AuthorEntityRunCompleted, done.Status)
	assert.Zero(t, done.UnitsSkipped, "no unit was refused")

	next, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	runToCompletion(t, loop, c.db, next.ID)
	var stale int
	_, err = c.db.QueryOne(pg.Scan(&stale), `SELECT count(*) FROM author_entity_assignment a WHERE NOT EXISTS (
		SELECT 1 FROM book_contributor_credit cr WHERE cr.source_fingerprint = a.source_fingerprint)`)
	require.NoError(t, err)
	assert.Zero(t, stale, "no fingerprint row outlives its credits")
}

// A unit whose row another writer holds past the lock timeout — here a
// credit a review locks while the pass writes that credit's own decision —
// is skipped with the units it depends on (the full name's unit takes a
// book from the bucket's, so both go); the unrelated author lands, and the
// next pass writes the component once the row is free.
func TestAUnitWhoseRowIsHeldIsSkippedWithItsComponent(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedBucket()
	ctx := context.Background()
	admin := services.NewAuthorEntityAdmin(c.db)
	run, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	var moved int64 // the short-name credit a series moves to the full name
	_, err = c.db.QueryOne(pg.Scan(&moved), `SELECT c.id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id JOIN opds_catalog_book b ON b.id = s.book_id
		WHERE b.title = 'Золото бунта'`)
	require.NoError(t, err)
	review, err := c.db.Begin()
	require.NoError(t, err)
	_, err = review.Exec(`SELECT 1 FROM book_contributor_credit WHERE id = ? FOR UPDATE`, moved)
	require.NoError(t, err)

	loop := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{LockTimeout: 200 * time.Millisecond})
	_, err = loop.RunOnce(ctx)
	require.NoError(t, review.Rollback())
	require.NoError(t, err)
	done, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, run.ID, done.ID)
	require.Equal(t, database.AuthorEntityRunCompleted, done.Status)
	assert.Equal(t, 2, done.UnitsSkipped, "the held unit's component was skipped")
	assert.Equal(t, 1, liveEntities(t, c.db), "the unrelated author landed")

	next, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	assert.Zero(t, runToCompletion(t, loop, c.db, next.ID).UnitsSkipped)
	assert.Equal(t, 3, liveEntities(t, c.db))
}
