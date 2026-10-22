package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
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
		VALUES ('3', 'structured_person', 'Cyrl', 'normalizer-v1', ?, 'internal/authornorm/policy_evidence/fixture.json',
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
		VALUES (?, ?, 'selected', ?, 'automatic', '3')`, credit, fp, result)
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

// A worker that stops halfway leaves its lease to run out; the next worker
// plans again over what is stored and finishes the pass, which ends where an
// uninterrupted one would: a further pass writes nothing.
func TestAnInterruptedEntityBuildIsFinishedByTheNextWorker(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedBucket()
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	cfg := services.AuthorEntityBuildConfig{Batch: 1, Lease: 50 * time.Millisecond}
	first := services.NewAuthorEntityBuildLoop(c.db, cfg)
	for range 2 { // the plan and one unit
		_, err = first.RunOnce(ctx)
		require.NoError(t, err)
	}
	latest, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
	require.NoError(t, err)
	require.Equal(t, 1, latest.UnitsDone)
	require.Less(t, latest.UnitsDone, latest.UnitsTotal)

	next := services.NewAuthorEntityBuildLoop(c.db, cfg)
	worked, err := next.RunOnce(ctx)
	require.NoError(t, err)
	require.False(t, worked, "the first worker's lease still holds the pass")
	time.Sleep(100 * time.Millisecond)
	done := runToCompletion(t, next, c.db, run.ID)
	assert.Equal(t, done.UnitsTotal, done.UnitsDone)

	var entities int
	_, err = c.db.QueryOne(pg.Scan(&entities), `SELECT count(*) FROM author_entity WHERE merged_into IS NULL`)
	require.NoError(t, err)
	assert.Equal(t, 3, entities, "no unit was written twice under two IDs")

	again, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(t, err)
	assert.Zero(t, runToCompletion(t, next, c.db, again.ID).Changes)
}
