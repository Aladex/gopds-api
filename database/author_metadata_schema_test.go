package database

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the author metadata source schema (migration 23) against a
// real PostgreSQL: every invariant below is enforced by the database itself,
// so a repository bug in a later phase cannot write a row that breaks it.

// authorSchemaIDBase starts the ID range for legacy rows these tests seed:
// above every real book and user, and below the search fixture's range so the
// two never collide. Everything rolls back with the test transaction.
const authorSchemaIDBase int64 = 2_146_000_000

// SQLSTATE codes the assertions name.
const (
	sqlstateCheck      = "23514"
	sqlstateUnique     = "23505"
	sqlstateForeignKey = "23503"
	sqlstateRestrict   = "23001"
)

// authorSchemaFixture is one test transaction plus helpers that seed the
// minimum rows the constraints need.
type authorSchemaFixture struct {
	t    *testing.T
	tx   *pg.Tx
	next *int64
}

func withAuthorSchemaTx(t *testing.T) *authorSchemaFixture {
	t.Helper()
	requireDatabase(t)

	tx, err := db.Begin()
	require.NoError(t, err, "beginning the schema test transaction")
	t.Cleanup(func() { _ = tx.Rollback() })

	next := authorSchemaIDBase
	return &authorSchemaFixture{t: t, tx: tx, next: &next}
}

func (f *authorSchemaFixture) exec(query string, params ...interface{}) {
	f.t.Helper()
	_, err := f.tx.Exec(query, params...)
	require.NoError(f.t, err, query)
}

func (f *authorSchemaFixture) returningID(query string, params ...interface{}) int64 {
	f.t.Helper()
	var id int64
	_, err := f.tx.QueryOne(pg.Scan(&id), query, params...)
	require.NoError(f.t, err, query)
	return id
}

// on rebinds the fixture to a subtest: assertions that stop a test must run
// on that test's own *testing.T.
func (f *authorSchemaFixture) on(t *testing.T) *authorSchemaFixture {
	g := *f
	g.t = t
	return &g
}

// attempt runs one statement inside a savepoint and rolls the savepoint back
// whatever happened, so a refused statement does not abort the transaction and
// an accepted one leaves no row behind.
func (f *authorSchemaFixture) attempt(query string, params ...interface{}) error {
	f.t.Helper()
	const name = "author_schema_attempt"
	f.exec("SAVEPOINT " + name)
	_, err := f.tx.Exec(query, params...)
	f.exec("ROLLBACK TO SAVEPOINT " + name)
	f.exec("RELEASE SAVEPOINT " + name)
	return err
}

// reject requires PostgreSQL to refuse the statement with the given SQLSTATE,
// citing the named constraint (or, for trigger refusals, a message fragment).
func (f *authorSchemaFixture) reject(code, cite, query string, params ...interface{}) {
	f.t.Helper()
	err := f.attempt(query, params...)
	require.Error(f.t, err, "the database returned no error for: %s", query)

	var pgErr pg.Error
	require.True(f.t, errors.As(err, &pgErr), "not a PostgreSQL error: %v", err)
	assert.Equal(f.t, code, pgErr.Field('C'), "SQLSTATE for %s: %v", query, err)
	if cite != "" {
		cited := pgErr.Field('n') == cite || strings.Contains(pgErr.Field('M'), cite)
		assert.True(f.t, cited, "error for %s cites %q, want %q", query, pgErr.Field('n')+" "+pgErr.Field('M'), cite)
	}
}

// accept requires PostgreSQL to take the statement and then discards its rows.
func (f *authorSchemaFixture) accept(query string, params ...interface{}) {
	f.t.Helper()
	require.NoError(f.t, f.attempt(query, params...), query)
}

func (f *authorSchemaFixture) id() int64 {
	id := *f.next
	*f.next++
	return id
}

func (f *authorSchemaFixture) user() int64 {
	f.t.Helper()
	id := f.id()
	f.exec(`INSERT INTO auth_user (id, password, is_superuser, username, email, date_joined)
		VALUES (?, '', true, ?, ?, now())`, id, fmt.Sprintf("author-schema-%d", id),
		fmt.Sprintf("author-schema-%d@fixture.local", id))
	return id
}

func (f *authorSchemaFixture) book() int64 {
	f.t.Helper()
	id := f.id()
	f.exec(`INSERT INTO opds_catalog_book
		(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES (?, ?, 'fixture.zip', 'fb2', now(), '', 'ru', 'fixture', '', ?)`,
		id, fmt.Sprintf("%d.fb2", id), bookMD5(id))
	return id
}

func bookMD5(bookID int64) string { return fmt.Sprintf("%032x", bookID) }

// fingerprint returns n bytes; 32 is the only valid length.
func fingerprint(n int, fill byte) []byte { return bytes.Repeat([]byte{fill}, n) }

type runSpec struct {
	mode, status, extractor, normalizer string
	createdBy                           *int64
}

func (f *authorSchemaFixture) runSQL(r *runSpec) (query string, params []interface{}) {
	if r.mode == "" {
		r.mode = "smoke"
	}
	if r.status == "" {
		r.status = "pending"
	}
	if r.extractor == "" {
		r.extractor = "extractor-v1"
	}
	if r.normalizer == "" {
		r.normalizer = "normalizer-v1"
	}
	var archive *string
	if r.mode != "full" {
		a := "fixture.zip"
		archive = &a
	}
	terminal := r.status == "completed" || r.status == "failed_systemic"
	return `INSERT INTO author_metadata_run
			(mode, status, extractor_version, normalizer_version, selector_archive, created_by_user_id,
			 extraction_completed_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?,
			CASE WHEN ? THEN now() END, CASE WHEN ? THEN now() END)
		RETURNING id`,
		[]interface{}{r.mode, r.status, r.extractor, r.normalizer, archive, r.createdBy,
			r.status == "completed", terminal}
}

func (f *authorSchemaFixture) run(r *runSpec) int64 {
	f.t.Helper()
	query, params := f.runSQL(r)
	return f.returningID(query, params...)
}

type snapshotSpec struct {
	book       int64
	md5        string
	extractor  string
	origin     string
	run        *int64
	outcome    string
	notCurrent bool
}

func (f *authorSchemaFixture) snapshotSQL(s *snapshotSpec) (query string, params []interface{}) {
	if s.md5 == "" {
		s.md5 = bookMD5(s.book)
	}
	if s.extractor == "" {
		s.extractor = "extractor-v1"
	}
	if s.origin == "" {
		s.origin = "live"
	}
	if s.outcome == "" {
		s.outcome = "extracted"
	}
	return `INSERT INTO book_metadata_snapshot
			(book_id, book_md5, extractor_version, origin, run_id, outcome, archive_path, entry_name,
			 source_title, source_isbns, source_sequences, is_current)
		VALUES (?, ?, ?, ?, ?, ?, 'fixture.zip', ?, 'title', '{"978-5"}', '[{"name":"s","number":"1"}]', ?)
		RETURNING id`,
		[]interface{}{s.book, s.md5, s.extractor, s.origin, s.run, s.outcome,
			fmt.Sprintf("%d.fb2", s.book), !s.notCurrent}
}

func (f *authorSchemaFixture) snapshot(s *snapshotSpec) int64 {
	f.t.Helper()
	query, params := f.snapshotSQL(s)
	return f.returningID(query, params...)
}

func creditSQL(snapshot int64, role string, position int) (query string, params []interface{}) {
	return `INSERT INTO book_contributor_credit
			(snapshot_id, role, position, source_first_name, source_last_name, source_display_name,
			 source_fingerprint, quality_flags)
		VALUES (?, ?, ?, 'First', 'Last', 'First Last', ?, '{"flag"}')
		RETURNING id`,
		[]interface{}{snapshot, role, position, fingerprint(32, 0xab)}
}

func (f *authorSchemaFixture) credit(snapshot int64, role string, position int) int64 {
	f.t.Helper()
	query, params := creditSQL(snapshot, role, position)
	return f.returningID(query, params...)
}

func (f *authorSchemaFixture) runItem(run, book int64) int64 {
	f.t.Helper()
	return f.returningID(`INSERT INTO author_metadata_run_item (run_id, book_id) VALUES (?, ?) RETURNING id`, run, book)
}

const ownerToken = "6f1c1c3e-8a0b-4b7e-9d36-3f4f0c6b9a11"

// RED 1: the relations and the named indexes exist.
func TestAuthorMetadataSchemaRelationsAndIndexes(t *testing.T) {
	f := withAuthorSchemaTx(t)

	var tables []string
	_, err := f.tx.Query(&tables, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN (?)
		ORDER BY table_name`, pg.In(authorMetadataTables))
	require.NoError(t, err)
	assert.ElementsMatch(t, authorMetadataTables, tables)

	var indexes []string
	_, err = f.tx.Query(&indexes, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = 'public' AND indexname IN (?)`, pg.In(authorMetadataIndexes))
	require.NoError(t, err)
	assert.ElementsMatch(t, authorMetadataIndexes, indexes)
}

var authorMetadataTables = []string{
	"author_metadata_run",
	"author_metadata_run_item",
	"author_metadata_run_item_attempt",
	"author_metadata_pilot_approval",
	"book_metadata_snapshot",
	"book_contributor_credit",
}

var authorMetadataIndexes = []string{
	"author_metadata_run_one_active",
	"author_metadata_run_approval_key",
	"author_metadata_run_item_run_book_key",
	"author_metadata_run_item_claim_idx",
	"author_metadata_run_item_book_idx",
	"author_metadata_run_item_attempt_no_key",
	"author_metadata_pilot_approval_run_key",
	"author_metadata_pilot_approval_versions_idx",
	"book_metadata_snapshot_key",
	"book_metadata_snapshot_id_book_key",
	"book_metadata_snapshot_one_current",
	"book_metadata_snapshot_run_idx",
	"book_contributor_credit_position_key",
	"book_contributor_credit_fingerprint_idx",
}

// RED 2: at most one active run; paused counts as active.
func TestAuthorMetadataRunOneActive(t *testing.T) {
	requireDatabase(t)

	for _, active := range []string{"pending", "running", "paused"} {
		t.Run(active, func(t *testing.T) {
			f := withAuthorSchemaTx(t)
			first := f.run(&runSpec{status: active})

			for _, second := range []string{"pending", "running", "paused"} {
				query, params := f.runSQL(&runSpec{status: second})
				f.reject(sqlstateUnique, "author_metadata_run_one_active", query, params...)
			}

			// Terminal runs never block, and finishing the active one frees the slot.
			for _, terminal := range []string{"completed", "failed_systemic"} {
				query, params := f.runSQL(&runSpec{status: terminal})
				f.accept(query, params...)
			}
			f.exec(`UPDATE author_metadata_run
				SET status = 'completed', extraction_completed_at = now(), finished_at = now()
				WHERE id = ?`, first)
			f.run(&runSpec{status: "pending"})
		})
	}
}

// RED 3: one item per (run, book).
func TestAuthorMetadataRunItemUniquePerRunAndBook(t *testing.T) {
	f := withAuthorSchemaTx(t)
	run := f.run(&runSpec{})
	other := f.run(&runSpec{status: "completed"})
	book := f.book()

	f.runItem(run, book)
	f.reject(sqlstateUnique, "author_metadata_run_item_run_book_key",
		`INSERT INTO author_metadata_run_item (run_id, book_id) VALUES (?, ?)`, run, book)

	// The same book in another run is a different item.
	f.runItem(other, book)
}

// RED 4: the snapshot key is unique, and a book has at most one current
// snapshot.
func TestBookMetadataSnapshotKeyAndCurrent(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	f.snapshot(&snapshotSpec{book: book})

	t.Run("duplicate key", func(t *testing.T) {
		sf := f.on(t)
		query, params := sf.snapshotSQL(&snapshotSpec{book: book, notCurrent: true})
		sf.reject(sqlstateUnique, "book_metadata_snapshot_key", query, params...)
	})

	t.Run("second current snapshot of the same book", func(t *testing.T) {
		sf := f.on(t)
		query, params := sf.snapshotSQL(&snapshotSpec{book: book, extractor: "extractor-v2"})
		sf.reject(sqlstateUnique, "book_metadata_snapshot_one_current", query, params...)

		query, params = sf.snapshotSQL(&snapshotSpec{book: book, md5: bookMD5(book + 1_000_000)})
		sf.reject(sqlstateUnique, "book_metadata_snapshot_one_current", query, params...)
	})

	t.Run("history and other books are free", func(t *testing.T) {
		sf := f.on(t)
		sf.snapshot(&snapshotSpec{book: book, extractor: "extractor-v2", notCurrent: true})
		sf.snapshot(&snapshotSpec{book: sf.book()})
	})
}

// RED 5: backfill snapshots carry their run; live snapshots carry none.
func TestBookMetadataSnapshotOriginRun(t *testing.T) {
	f := withAuthorSchemaTx(t)
	run := f.run(&runSpec{})

	cases := []struct {
		origin string
		run    *int64
		ok     bool
	}{
		{"backfill", &run, true},
		{"live", nil, true},
		{"backfill", nil, false},
		{"live", &run, false},
	}
	for _, c := range cases {
		query, params := f.snapshotSQL(&snapshotSpec{book: f.book(), origin: c.origin, run: c.run})
		if c.ok {
			f.accept(query, params...)
		} else {
			f.reject(sqlstateCheck, "book_metadata_snapshot_origin_run_check", query, params...)
		}
	}
}

// RED 6: every role, status and outcome column is closed.
func TestAuthorMetadataEnumsAreClosed(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	snapshot := f.snapshot(&snapshotSpec{book: book})
	// A finished run, so no case collides with the active-run index.
	run := f.run(&runSpec{status: "completed"})
	item := f.runItem(run, book)

	rejectedValues := []string{"", "unknown", "Pending", "AUTHOR", "extracted "}

	type enumCase struct {
		name       string
		constraint string
		accepted   []string
		build      func(value string) (string, []interface{})
	}
	terminalItem := []string{"extracted", "extracted_no_author", "already_current",
		"entry_missing", "invalid_fb2", "unsupported_encoding", "metadata_parse_failed"}

	// Every accepted insert is rolled back, so one snapshot key serves them all.
	const snapshotInsert = `INSERT INTO book_metadata_snapshot
		(book_id, book_md5, extractor_version, origin, run_id, outcome, archive_path, entry_name, is_current)
		VALUES (?, ?, 'enum', ?, ?, ?, 'a.zip', 'e.fb2', false)`

	cases := []enumCase{
		{
			name: "run mode", constraint: "author_metadata_run_mode_check",
			accepted: []string{"smoke", "pilot_archive", "full"},
			build: func(v string) (string, []interface{}) {
				return `INSERT INTO author_metadata_run
					(mode, status, extractor_version, normalizer_version, selector_archive,
					 extraction_completed_at, finished_at)
					VALUES (?, 'completed', 'e', 'n', CASE WHEN ? = 'full' THEN NULL ELSE 'a.zip' END, now(), now())`,
					[]interface{}{v, v}
			},
		},
		{
			name: "run status", constraint: "author_metadata_run_status_check",
			accepted: []string{"pending", "running", "paused", "completed", "failed_systemic"},
			build: func(v string) (string, []interface{}) {
				return `INSERT INTO author_metadata_run
					(mode, status, extractor_version, normalizer_version, selector_archive,
					 extraction_completed_at, finished_at)
					VALUES ('smoke', ?, 'e', 'n', 'a.zip', now(),
						CASE WHEN ? IN ('completed', 'failed_systemic') THEN now() END)`,
					[]interface{}{v, v}
			},
		},
		{
			name: "run item status", constraint: "author_metadata_run_item_status_check",
			accepted: append([]string{"pending"}, terminalItem...),
			build: func(v string) (string, []interface{}) {
				return `UPDATE author_metadata_run_item
					SET status = ?,
						snapshot_id = CASE WHEN ? IN ('extracted', 'extracted_no_author', 'already_current')
							THEN ?::bigint END,
						finished_at = CASE WHEN ? <> 'pending' THEN now() END
					WHERE id = ?`, []interface{}{v, v, snapshot, v, item}
			},
		},
		{
			name: "attempt outcome", constraint: "author_metadata_run_item_attempt_outcome_check",
			accepted: terminalItem,
			build: func(v string) (string, []interface{}) {
				return `INSERT INTO author_metadata_run_item_attempt
					(run_item_id, attempt_no, lease_owner, finished_at, outcome)
					VALUES (?, 1, ?, now(), ?)`, []interface{}{item, ownerToken, v}
			},
		},
		{
			name: "snapshot origin", constraint: "book_metadata_snapshot_origin_check",
			accepted: []string{"live", "backfill"},
			build: func(v string) (string, []interface{}) {
				var runID *int64
				if v == "backfill" {
					runID = &run
				}
				return snapshotInsert, []interface{}{book, bookMD5(book), v, runID, "extracted"}
			},
		},
		{
			name: "snapshot outcome", constraint: "book_metadata_snapshot_outcome_check",
			accepted: []string{"extracted", "extracted_no_author"},
			build: func(v string) (string, []interface{}) {
				return snapshotInsert, []interface{}{book, bookMD5(book), "live", nil, v}
			},
		},
		{
			name: "credit role", constraint: "book_contributor_credit_role_check",
			accepted: []string{"author", "translator"},
			build: func(v string) (string, []interface{}) {
				return creditSQL(snapshot, v, 0)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sf := f.on(t)
			for _, v := range c.accepted {
				query, params := c.build(v)
				sf.accept(query, params...)
			}
			for _, v := range rejectedValues {
				query, params := c.build(v)
				sf.reject(sqlstateCheck, c.constraint, query, params...)
			}
		})
	}
}

// RED 7: counters and positions are non-negative, fingerprints are exactly 32
// bytes, a lease is owner and expiry together, and the remaining shape rules.
func TestAuthorMetadataShapeChecks(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	snapshot := f.snapshot(&snapshotSpec{book: book})
	run := f.run(&runSpec{})
	item := f.runItem(run, book)

	t.Run("credit position", func(t *testing.T) {
		sf := f.on(t)
		query, params := creditSQL(snapshot, "author", -1)
		sf.reject(sqlstateCheck, "book_contributor_credit_position_check", query, params...)
		query, params = creditSQL(snapshot, "author", 0)
		sf.accept(query, params...)
	})

	t.Run("fingerprint is exactly 32 bytes", func(t *testing.T) {
		sf := f.on(t)
		insert := `INSERT INTO book_contributor_credit
			(snapshot_id, role, position, source_display_name, source_fingerprint)
			VALUES (?, 'author', 0, 'Name', ?)`
		for _, n := range []int{0, 16, 31, 33, 64} {
			sf.reject(sqlstateCheck, "book_contributor_credit_source_fingerprint_check", insert, snapshot, fingerprint(n, 1))
		}
		sf.accept(insert, snapshot, fingerprint(32, 1))
	})

	t.Run("display name", func(t *testing.T) {
		sf := f.on(t)
		insert := `INSERT INTO book_contributor_credit
			(snapshot_id, role, position, source_display_name, source_fingerprint)
			VALUES (?, 'author', 0, ?, ?)`
		for _, name := range []string{"", " Name", "Name ", "\tName", "Name\n", "\rName"} {
			sf.reject(sqlstateCheck, "book_contributor_credit_display_name_check", insert, snapshot, name, fingerprint(32, 1))
		}
		// Non-XML whitespace is source data, not structure: NBSP stays.
		sf.accept(insert, snapshot, " Name", fingerprint(32, 1))
	})

	t.Run("run counters", func(t *testing.T) {
		sf := f.on(t)
		for _, set := range []string{"items_total = -1", "items_terminal = -1", "items_total = 1, items_terminal = 2"} {
			sf.reject(sqlstateCheck, "author_metadata_run_counters_check",
				`UPDATE author_metadata_run SET `+set+` WHERE id = ?`, run)
		}
		sf.accept(`UPDATE author_metadata_run SET items_total = 2, items_terminal = 2 WHERE id = ?`, run)
	})

	t.Run("run item attempt count", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "author_metadata_run_item_attempt_count_check",
			`UPDATE author_metadata_run_item SET attempt_count = -1 WHERE id = ?`, item)
	})

	t.Run("attempt number starts at one", func(t *testing.T) {
		sf := f.on(t)
		insert := `INSERT INTO author_metadata_run_item_attempt (run_item_id, attempt_no, lease_owner) VALUES (?, ?, ?)`
		sf.reject(sqlstateCheck, "author_metadata_run_item_attempt_no_check", insert, item, 0, ownerToken)
		sf.reject(sqlstateCheck, "author_metadata_run_item_attempt_no_check", insert, item, -1, ownerToken)
		sf.accept(insert, item, 1, ownerToken)
	})

	t.Run("lease owner and expiry are a pair", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "author_metadata_run_item_lease_pair_check",
			`UPDATE author_metadata_run_item SET lease_owner = ? WHERE id = ?`, ownerToken, item)
		sf.reject(sqlstateCheck, "author_metadata_run_item_lease_pair_check",
			`UPDATE author_metadata_run_item SET lease_expires_at = now() + interval '1 minute' WHERE id = ?`, item)
		sf.accept(`UPDATE author_metadata_run_item
			SET lease_owner = ?, lease_expires_at = now() + interval '1 minute' WHERE id = ?`, ownerToken, item)
	})

	t.Run("terminal items hold no lease", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "author_metadata_run_item_lease_terminal_check",
			`UPDATE author_metadata_run_item
			SET status = 'entry_missing', finished_at = now(),
				lease_owner = ?, lease_expires_at = now() + interval '1 minute'
			WHERE id = ?`, ownerToken, item)
	})

	t.Run("item snapshot matches its status and its book", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "author_metadata_run_item_snapshot_check",
			`UPDATE author_metadata_run_item SET status = 'extracted', finished_at = now() WHERE id = ?`, item)
		sf.reject(sqlstateCheck, "author_metadata_run_item_snapshot_check",
			`UPDATE author_metadata_run_item SET status = 'entry_missing', finished_at = now(), snapshot_id = ? WHERE id = ?`,
			snapshot, item)
		sf.reject(sqlstateCheck, "author_metadata_run_item_finished_check",
			`UPDATE author_metadata_run_item SET status = 'entry_missing' WHERE id = ?`, item)
		// A snapshot of another book cannot be this item's result.
		other := sf.snapshot(&snapshotSpec{book: sf.book()})
		sf.reject(sqlstateForeignKey, "author_metadata_run_item_snapshot_fkey",
			`UPDATE author_metadata_run_item SET status = 'extracted', finished_at = now(), snapshot_id = ? WHERE id = ?`,
			other, item)
		sf.accept(`UPDATE author_metadata_run_item SET status = 'already_current', finished_at = now(), snapshot_id = ? WHERE id = ?`,
			snapshot, item)
	})

	t.Run("attempt finish shape", func(t *testing.T) {
		sf := f.on(t)
		insert := `INSERT INTO author_metadata_run_item_attempt
			(run_item_id, attempt_no, lease_owner, started_at, finished_at, outcome, error_class)
			VALUES (?, 9, ?, now(), ?, ?, ?)`
		finished := time.Now().Add(time.Minute)
		before := time.Now().Add(-time.Hour)
		outcome := "invalid_fb2"
		class := "archive_unreadable"
		sf.reject(sqlstateCheck, "author_metadata_run_item_attempt_finished_check", insert, item, ownerToken, nil, outcome, nil)
		sf.reject(sqlstateCheck, "author_metadata_run_item_attempt_finished_check", insert, item, ownerToken, finished, nil, nil)
		sf.reject(sqlstateCheck, "author_metadata_run_item_attempt_finished_check", insert, item, ownerToken, before, outcome, nil)
		sf.reject(sqlstateCheck, "author_metadata_run_item_attempt_error_class_check", insert, item, ownerToken, finished, nil,
			"Archive failed: /books/secret.zip")
		sf.accept(insert, item, ownerToken, finished, nil, class)
		sf.accept(insert, item, ownerToken, finished, outcome, nil)
	})

	t.Run("versions are exact non-empty strings", func(t *testing.T) {
		sf := f.on(t)
		const runInsert = `INSERT INTO author_metadata_run
			(mode, status, extractor_version, normalizer_version, selector_archive, finished_at)
			VALUES ('smoke', 'failed_systemic', ?, ?, 'a.zip', now())`
		const snapshotInsert = `INSERT INTO book_metadata_snapshot
			(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name, is_current)
			VALUES (?, ?, ?, 'live', 'extracted', 'a.zip', 'e.fb2', false)`
		for _, v := range []string{"", " ", " v1", "v1 ", "\tv1", "v1\n"} {
			sf.reject(sqlstateCheck, "author_metadata_run_extractor_version_check", runInsert, v, "n")
			sf.reject(sqlstateCheck, "author_metadata_run_normalizer_version_check", runInsert, "e", v)
			sf.reject(sqlstateCheck, "book_metadata_snapshot_extractor_version_check", snapshotInsert, book, bookMD5(book), v)
		}
		// Inner whitespace is part of an exact version string.
		sf.accept(runInsert, "extractor 1.0", "normalizer 1.0")
	})

	t.Run("snapshot md5 is lowercase hex of 32", func(t *testing.T) {
		sf := f.on(t)
		for _, md5 := range []string{"abc", strings.ToUpper(bookMD5(book + 7)), bookMD5(book) + "0", "zz" + bookMD5(book)[2:]} {
			query, params := sf.snapshotSQL(&snapshotSpec{book: book, md5: md5, notCurrent: true})
			sf.reject(sqlstateCheck, "book_metadata_snapshot_book_md5_check", query, params...)
		}
	})

	t.Run("snapshot repeatable payload shapes", func(t *testing.T) {
		sf := f.on(t)
		insert := `INSERT INTO book_metadata_snapshot
			(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name,
			 source_isbns, source_sequences, quality_flags, xml_provenance, is_current)
			VALUES (?, ?, 'shape', 'live', 'extracted', ?, ?, ?, ?::jsonb, ?, ?::jsonb, false)`
		md5 := bookMD5(book)
		sf.reject(sqlstateCheck, "book_metadata_snapshot_isbns_check", insert, book, md5, "a.zip", "e.fb2",
			pg.Array([]*string{nil}), "[]", pg.Array([]string{}), "{}")
		sf.reject(sqlstateCheck, "book_metadata_snapshot_sequences_check", insert, book, md5, "a.zip", "e.fb2",
			pg.Array([]string{}), `{"name":"s"}`, pg.Array([]string{}), "{}")
		sf.reject(sqlstateCheck, "book_metadata_snapshot_quality_flags_check", insert, book, md5, "a.zip", "e.fb2",
			pg.Array([]string{}), "[]", pg.Array([]*string{nil}), "{}")
		sf.reject(sqlstateCheck, "book_metadata_snapshot_provenance_check", insert, book, md5, "", "e.fb2",
			pg.Array([]string{}), "[]", pg.Array([]string{}), "{}")
		sf.reject(sqlstateCheck, "book_metadata_snapshot_provenance_check", insert, book, md5, "a.zip", "e.fb2",
			pg.Array([]string{}), "[]", pg.Array([]string{}), "[]")
		// An empty ISBN element is a present, empty source value and is kept.
		sf.accept(insert, book, md5, "a.zip", "e.fb2", pg.Array([]string{""}), "[]", pg.Array([]string{}), "{}")
	})

	t.Run("run selector", func(t *testing.T) {
		sf := f.on(t)
		insert := `INSERT INTO author_metadata_run
			(mode, status, extractor_version, normalizer_version, selector_book_ids, selector_archive, finished_at)
			VALUES (?, 'failed_systemic', 'e', 'n', ?, ?, now())`
		ids := pg.Array([]int64{3, 1, 2})
		archive := "a.zip"
		sf.reject(sqlstateCheck, "author_metadata_run_selector_check", insert, "full", ids, nil)
		sf.reject(sqlstateCheck, "author_metadata_run_selector_check", insert, "full", nil, archive)
		sf.reject(sqlstateCheck, "author_metadata_run_selector_check", insert, "smoke", nil, nil)
		sf.reject(sqlstateCheck, "author_metadata_run_selector_check", insert, "pilot_archive", ids, archive)
		sf.reject(sqlstateCheck, "author_metadata_run_selector_book_ids_check", insert, "smoke", pg.Array([]int64{}), nil)
		sf.reject(sqlstateCheck, "author_metadata_run_selector_book_ids_check", insert, "smoke", pg.Array([]int64{1, 0}), nil)
		sf.reject(sqlstateCheck, "author_metadata_run_selector_book_ids_check", insert, "smoke", pg.Array([]int64{-5}), nil)
		sf.reject(sqlstateCheck, "author_metadata_run_selector_book_ids_check", insert, "smoke", pg.Array([]*int64{nil}), nil)
		sf.reject(sqlstateCheck, "author_metadata_run_selector_archive_check", insert, "pilot_archive", nil, " ")
		sf.accept(insert, "smoke", ids, nil)
		sf.accept(insert, "pilot_archive", nil, archive)
		sf.accept(insert, "full", nil, nil)
	})

	t.Run("run lifecycle timestamps and error class", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "author_metadata_run_finished_check",
			`UPDATE author_metadata_run SET status = 'failed_systemic' WHERE id = ?`, run)
		sf.reject(sqlstateCheck, "author_metadata_run_finished_check",
			`UPDATE author_metadata_run SET finished_at = now() WHERE id = ?`, run)
		sf.reject(sqlstateCheck, "author_metadata_run_completed_check",
			`UPDATE author_metadata_run SET status = 'completed', finished_at = now() WHERE id = ?`, run)
		for _, class := range []string{"", "Archive", "archive unreadable", "archive/unreadable", strings.Repeat("a", 65)} {
			sf.reject(sqlstateCheck, "author_metadata_run_error_class_check",
				`UPDATE author_metadata_run SET last_error_class = ? WHERE id = ?`, class, run)
		}
		sf.accept(`UPDATE author_metadata_run SET last_error_class = 'archive_unreadable' WHERE id = ?`, run)
	})
}

// RED 8: one credit per (snapshot, role, position).
func TestBookContributorCreditUniquePosition(t *testing.T) {
	f := withAuthorSchemaTx(t)
	snapshot := f.snapshot(&snapshotSpec{book: f.book()})

	f.credit(snapshot, "author", 0)
	query, params := creditSQL(snapshot, "author", 0)
	f.reject(sqlstateUnique, "book_contributor_credit_position_key", query, params...)

	// Positions are counted per role, and the same source may appear in both.
	f.credit(snapshot, "translator", 0)
	f.credit(snapshot, "author", 1)
}

// tableColumns lists a table's columns from the catalog, so a column added
// later is covered without editing this test.
func tableColumns(t *testing.T, tx *pg.Tx, table string) map[string]string {
	t.Helper()
	var rows []struct {
		ColumnName string
		UdtName    string
	}
	_, err := tx.Query(&rows, `
		SELECT column_name, udt_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = ?`, table)
	require.NoError(t, err)
	require.NotEmpty(t, rows, "table %s has no columns", table)

	columns := make(map[string]string, len(rows))
	for _, r := range rows {
		columns[r.ColumnName] = r.UdtName
	}
	return columns
}

// changedValue is an SQL expression that differs from the column's current
// value, whatever it is, including NULL.
func changedValue(t *testing.T, column, udt string) string {
	t.Helper()
	switch udt {
	case "text", "varchar":
		return fmt.Sprintf("coalesce(%s, '') || 'x'", column)
	case "int2", "int4", "int8":
		return fmt.Sprintf("coalesce(%s, 0) + 1", column)
	case "bool":
		return fmt.Sprintf("NOT coalesce(%s, false)", column)
	case "bytea":
		return fmt.Sprintf(`coalesce(%s, ''::bytea) || '\x00'::bytea`, column)
	case "timestamptz":
		return fmt.Sprintf("coalesce(%s, now()) + interval '1 second'", column)
	case "uuid":
		return fmt.Sprintf("CASE WHEN %s = '%s' THEN gen_random_uuid() ELSE '%s'::uuid END", column, ownerToken, ownerToken)
	case "jsonb":
		return fmt.Sprintf("coalesce(%s, 'null'::jsonb) || '[0]'::jsonb", column)
	case "_text":
		return fmt.Sprintf("array_append(%s, 'x')", column)
	case "_int8":
		return fmt.Sprintf("array_append(%s, 1::bigint)", column)
	}
	t.Fatalf("no changed-value expression for column %s of type %s: extend changedValue", column, udt)
	return ""
}

// requireImmutable updates every column of the row outside mutable one at a
// time and requires the trigger to refuse each, then requires DELETE to be
// refused, then requires each mutable column to change.
func (f *authorSchemaFixture) requireImmutable(table string, id int64, mutable ...string) {
	f.t.Helper()
	f.requireFrozen(table, "id", id, mutable...)
	f.t.Run("delete", func(t *testing.T) {
		f.on(t).reject(sqlstateRestrict, "immutable", fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, table), id)
	})
}

// requireFrozen is the UPDATE half of requireImmutable, for rows identified
// by key that may change in the listed columns only and whose deletion is
// governed elsewhere.
func (f *authorSchemaFixture) requireFrozen(table, key string, id int64, mutable ...string) {
	f.t.Helper()
	allowed := map[string]bool{}
	for _, c := range mutable {
		allowed[c] = true
	}
	columns := tableColumns(f.t, f.tx, table)
	for _, c := range mutable {
		require.Contains(f.t, columns, c, "mutable column %s.%s does not exist", table, c)
	}

	for column, udt := range columns {
		if allowed[column] {
			continue
		}
		f.t.Run("update "+column, func(t *testing.T) {
			f.on(t).reject(sqlstateRestrict, "immutable",
				fmt.Sprintf(`UPDATE %s SET %s = %s WHERE %s = ?`, table, column, changedValue(t, column, udt), key), id)
		})
	}
	// A no-op UPDATE of a protected column is not a change and passes.
	f.accept(fmt.Sprintf(`UPDATE %s SET %s = %s WHERE %s = ?`, table, key, key, key), id)
}

// RED 9: source credits and snapshot payloads cannot be updated or deleted;
// only the snapshot's current marker may change. The protected column set is
// every column the catalog lists, so a column added later is protected by
// default and this test proves it without being edited.
func TestAuthorMetadataSourceIsImmutable(t *testing.T) {
	requireDatabase(t)

	t.Run("credit", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		credit := f.credit(f.snapshot(&snapshotSpec{book: f.book()}), "author", 0)
		f.requireImmutable("book_contributor_credit", credit)
	})

	t.Run("snapshot payload; current marker may change", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		book := f.book()
		snapshot := f.snapshot(&snapshotSpec{book: book})
		f.requireImmutable("book_metadata_snapshot", snapshot, "is_current")

		f.exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE id = ?`, snapshot)
		f.exec(`UPDATE book_metadata_snapshot SET is_current = true WHERE id = ?`, snapshot)
		var current bool
		_, err := f.tx.QueryOne(pg.Scan(&current), `SELECT is_current FROM book_metadata_snapshot WHERE id = ?`, snapshot)
		require.NoError(t, err)
		assert.True(t, current)
	})

	t.Run("pilot approval", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		run := f.run(&runSpec{mode: "pilot_archive", status: "completed"})
		approval := f.returningID(`INSERT INTO author_metadata_pilot_approval
			(run_id, extractor_version, normalizer_version, approved_by_user_id)
			VALUES (?, 'extractor-v1', 'normalizer-v1', ?) RETURNING id`, run, f.user())
		f.requireImmutable("author_metadata_pilot_approval", approval)
	})

	t.Run("finished attempt; an open one may finish once", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		book := f.book()
		item := f.runItem(f.run(&runSpec{}), book)
		attempt := f.returningID(`INSERT INTO author_metadata_run_item_attempt
			(run_item_id, attempt_no, lease_owner) VALUES (?, 1, ?) RETURNING id`, item, ownerToken)

		// While open, only the result columns may change.
		f.requireImmutable("author_metadata_run_item_attempt", attempt, "finished_at", "outcome", "error_class")
		f.exec(`UPDATE author_metadata_run_item_attempt
			SET finished_at = now(), error_class = 'archive_unreadable' WHERE id = ?`, attempt)

		// Once finished, nothing may change.
		f.reject(sqlstateRestrict, "immutable",
			`UPDATE author_metadata_run_item_attempt SET outcome = 'invalid_fb2' WHERE id = ?`, attempt)
		f.reject(sqlstateRestrict, "immutable",
			`UPDATE author_metadata_run_item_attempt SET error_class = 'other' WHERE id = ?`, attempt)
		f.reject(sqlstateRestrict, "immutable",
			`UPDATE author_metadata_run_item_attempt SET finished_at = finished_at + interval '1 second' WHERE id = ?`, attempt)

		// A retry appends; the earlier outcome stays.
		f.exec(`INSERT INTO author_metadata_run_item_attempt
			(run_item_id, attempt_no, lease_owner, finished_at, outcome) VALUES (?, 2, ?, now(), 'extracted_no_author')`,
			item, ownerToken)
		var classes []string
		_, err := f.tx.Query(&classes, `SELECT coalesce(error_class, outcome) FROM author_metadata_run_item_attempt
			WHERE run_item_id = ? ORDER BY attempt_no`, item)
		require.NoError(t, err)
		assert.Equal(t, []string{"archive_unreadable", "extracted_no_author"}, classes)
	})

	t.Run("trigger names and protected column sets", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		var rows []struct {
			RelName      string
			TriggerName  string
			FunctionName string
			TriggerArgs  string
		}
		_, err := f.tx.Query(&rows, `
			SELECT c.relname AS rel_name, t.tgname AS trigger_name, p.proname AS function_name,
				replace(encode(t.tgargs, 'escape'), '\000', ',') AS trigger_args
			FROM pg_trigger t
			JOIN pg_class c ON c.oid = t.tgrelid
			JOIN pg_proc p ON p.oid = t.tgfoid
			WHERE NOT t.tgisinternal AND c.relname IN (?)`, pg.In(authorMetadataTables))
		require.NoError(t, err)

		got := map[string]string{}
		for _, r := range rows {
			got[r.RelName+"."+r.TriggerName] = r.FunctionName + "(" + strings.TrimSuffix(r.TriggerArgs, ",") + ")"
		}
		assert.Equal(t, map[string]string{
			"book_contributor_credit.book_contributor_credit_immutable":               "author_metadata_reject_mutation()",
			"book_metadata_snapshot.book_metadata_snapshot_immutable":                 "author_metadata_reject_mutation(is_current)",
			"author_metadata_pilot_approval.author_metadata_pilot_approval_immutable": "author_metadata_reject_mutation()",
			"author_metadata_run_item_attempt.author_metadata_run_item_attempt_immutable": "author_metadata_reject_mutation(" +
				"finished_at,outcome,error_class)",
			"author_metadata_run_item_attempt.author_metadata_run_item_attempt_closed": "author_metadata_reject_closed_attempt()",
			"author_metadata_run.author_metadata_run_updated_at":                       "update_updated_at_column()",
			"author_metadata_run_item.author_metadata_run_item_updated_at":             "update_updated_at_column()",
			"author_metadata_run_item.author_metadata_run_item_identity": "author_metadata_reject_mutation(" +
				"status,snapshot_id,lease_owner,lease_expires_at,attempt_count,next_attempt_at,updated_at,finished_at)",
		}, got)
	})
}

// RED 10: deleting never cascades through the source history, and the source
// layer never deletes a legacy book.
func TestAuthorMetadataForeignKeysDoNotCascade(t *testing.T) {
	requireDatabase(t)

	t.Run("delete rules", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		var rows []struct {
			Name string
			Rule string
		}
		_, err := f.tx.Query(&rows, `
			SELECT conname AS name, confdeltype AS rule FROM pg_constraint
			WHERE contype = 'f' AND conrelid::regclass::text IN (?)`, pg.In(authorMetadataTables))
		require.NoError(t, err)

		got := map[string]string{}
		for _, r := range rows {
			got[r.Name] = r.Rule
		}
		// r = RESTRICT, n = SET NULL. No CASCADE anywhere.
		assert.Equal(t, map[string]string{
			"author_metadata_run_created_by_user_id_fkey":       "n",
			"author_metadata_run_item_run_id_fkey":              "r",
			"author_metadata_run_item_book_id_fkey":             "r",
			"author_metadata_run_item_snapshot_fkey":            "r",
			"author_metadata_run_item_attempt_run_item_id_fkey": "r",
			"author_metadata_pilot_approval_run_fkey":           "r",
			"book_metadata_snapshot_book_id_fkey":               "r",
			"book_metadata_snapshot_run_id_fkey":                "r",
			"book_contributor_credit_snapshot_id_fkey":          "r",
		}, got)
	})

	t.Run("a legacy book with source history cannot be deleted", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		withSnapshot := f.book()
		f.credit(f.snapshot(&snapshotSpec{book: withSnapshot}), "author", 0)
		f.reject(sqlstateForeignKey, "book_metadata_snapshot_book_id_fkey",
			`DELETE FROM opds_catalog_book WHERE id = ?`, withSnapshot)

		withItem := f.book()
		f.runItem(f.run(&runSpec{}), withItem)
		f.reject(sqlstateForeignKey, "author_metadata_run_item_book_id_fkey",
			`DELETE FROM opds_catalog_book WHERE id = ?`, withItem)

		var books, snapshots, credits int
		_, err := f.tx.QueryOne(pg.Scan(&books, &snapshots, &credits), `
			SELECT (SELECT count(*) FROM opds_catalog_book WHERE id IN (?, ?)),
				(SELECT count(*) FROM book_metadata_snapshot WHERE book_id = ?),
				(SELECT count(*) FROM book_contributor_credit c
					JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.book_id = ?)`,
			withSnapshot, withItem, withSnapshot, withSnapshot)
		require.NoError(t, err)
		assert.Equal(t, []int{2, 1, 1}, []int{books, snapshots, credits})
	})

	t.Run("runs and items with history cannot be deleted", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		book := f.book()
		run := f.run(&runSpec{})
		item := f.runItem(run, book)
		f.reject(sqlstateForeignKey, "author_metadata_run_item_run_id_fkey",
			`DELETE FROM author_metadata_run WHERE id = ?`, run)
		f.exec(`INSERT INTO author_metadata_run_item_attempt (run_item_id, attempt_no, lease_owner) VALUES (?, 1, ?)`,
			item, ownerToken)
		f.reject(sqlstateForeignKey, "author_metadata_run_item_attempt_run_item_id_fkey",
			`DELETE FROM author_metadata_run_item WHERE id = ?`, item)

		backfill := f.run(&runSpec{status: "completed"})
		f.snapshot(&snapshotSpec{book: f.book(), origin: "backfill", run: &backfill})
		f.reject(sqlstateForeignKey, "book_metadata_snapshot_run_id_fkey",
			`DELETE FROM author_metadata_run WHERE id = ?`, backfill)
	})

	t.Run("deleting an admin keeps their runs and approvals", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		admin := f.user()
		pilot := f.run(&runSpec{mode: "pilot_archive", status: "completed", createdBy: &admin})
		f.exec(`INSERT INTO author_metadata_pilot_approval
			(run_id, extractor_version, normalizer_version, approved_by_user_id)
			VALUES (?, 'extractor-v1', 'normalizer-v1', ?)`, pilot, admin)

		f.exec(`DELETE FROM auth_user WHERE id = ?`, admin)

		var createdBy *int64
		var approvedBy int64
		_, err := f.tx.QueryOne(pg.Scan(&createdBy, &approvedBy), `
			SELECT r.created_by_user_id, a.approved_by_user_id
			FROM author_metadata_run r JOIN author_metadata_pilot_approval a ON a.run_id = r.id
			WHERE r.id = ?`, pilot)
		require.NoError(t, err)
		assert.Nil(t, createdBy, "the run forgets the deleted account")
		assert.Equal(t, admin, approvedBy, "the approval keeps who approved it")
	})
}

// RED 11: a pilot approval records actor, time and exact versions; it only
// fits a completed pilot_archive run of the same versions; it is not rewritten
// afterwards, and neither is the run it approved.
func TestAuthorMetadataPilotApproval(t *testing.T) {
	const insert = `INSERT INTO author_metadata_pilot_approval
		(run_id, run_mode, run_status, extractor_version, normalizer_version, approved_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?)`

	f := withAuthorSchemaTx(t)
	admin := f.user()
	pilot := f.run(&runSpec{mode: "pilot_archive", status: "completed", extractor: "extractor-v3", normalizer: "normalizer-v7"})

	t.Run("versions must be the run's", func(t *testing.T) {
		sf := f.on(t)
		for _, v := range [][2]string{
			{"extractor-v4", "normalizer-v7"},
			{"extractor-v3", "normalizer-v8"},
			{"extractor-v3 ", "normalizer-v7"},
		} {
			sf.reject(sqlstateForeignKey, "author_metadata_pilot_approval_run_fkey",
				insert, pilot, "pilot_archive", "completed", v[0], v[1], admin)
		}
	})

	t.Run("only a completed pilot_archive run", func(t *testing.T) {
		sf := f.on(t)
		running := sf.run(&runSpec{mode: "pilot_archive", status: "running"})
		sf.reject(sqlstateForeignKey, "author_metadata_pilot_approval_run_fkey",
			insert, running, "pilot_archive", "completed", "extractor-v1", "normalizer-v1", admin)
		sf.reject(sqlstateCheck, "author_metadata_pilot_approval_run_check",
			insert, running, "pilot_archive", "running", "extractor-v1", "normalizer-v1", admin)

		for _, mode := range []string{"smoke", "full"} {
			done := sf.run(&runSpec{mode: mode, status: "completed"})
			sf.reject(sqlstateForeignKey, "author_metadata_pilot_approval_run_fkey",
				insert, done, "pilot_archive", "completed", "extractor-v1", "normalizer-v1", admin)
			sf.reject(sqlstateCheck, "author_metadata_pilot_approval_run_check",
				insert, done, mode, "completed", "extractor-v1", "normalizer-v1", admin)
		}
		failed := sf.run(&runSpec{mode: "pilot_archive", status: "failed_systemic"})
		sf.reject(sqlstateForeignKey, "author_metadata_pilot_approval_run_fkey",
			insert, failed, "pilot_archive", "completed", "extractor-v1", "normalizer-v1", admin)
	})

	t.Run("records actor, time and versions once", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "author_metadata_pilot_approval_actor_check",
			insert, pilot, "pilot_archive", "completed", "extractor-v3", "normalizer-v7", 0)

		before := time.Now().Add(-time.Minute)
		sf.exec(insert, pilot, "pilot_archive", "completed", "extractor-v3", "normalizer-v7", admin)

		var got struct {
			ExtractorVersion  string
			NormalizerVersion string
			ApprovedByUserID  int64
			ApprovedAt        time.Time
		}
		_, err := sf.tx.QueryOne(&got, `SELECT extractor_version, normalizer_version, approved_by_user_id, approved_at
			FROM author_metadata_pilot_approval WHERE run_id = ?`, pilot)
		require.NoError(t, err)
		assert.Equal(t, "extractor-v3", got.ExtractorVersion)
		assert.Equal(t, "normalizer-v7", got.NormalizerVersion)
		assert.Equal(t, admin, got.ApprovedByUserID)
		assert.True(t, got.ApprovedAt.After(before) && got.ApprovedAt.Before(time.Now().Add(time.Minute)),
			"approved_at %v is not the insert time", got.ApprovedAt)

		sf.reject(sqlstateUnique, "author_metadata_pilot_approval_run_key",
			insert, pilot, "pilot_archive", "completed", "extractor-v3", "normalizer-v7", admin)
	})

	t.Run("an approval does not fit a run of other versions", func(t *testing.T) {
		sf := f.on(t)
		var matches int
		_, err := sf.tx.QueryOne(pg.Scan(&matches), `
			SELECT count(*) FROM author_metadata_pilot_approval
			WHERE extractor_version = ? AND normalizer_version = ?`, "extractor-v3", "normalizer-v8")
		require.NoError(t, err)
		assert.Zero(t, matches)
	})

	t.Run("the approved run is frozen", func(t *testing.T) {
		sf := f.on(t)
		for _, set := range []string{
			"status = 'failed_systemic'",
			"extractor_version = 'extractor-v4'",
			"normalizer_version = 'normalizer-v8'",
			"mode = 'full', selector_archive = NULL",
		} {
			sf.reject(sqlstateForeignKey, "author_metadata_pilot_approval_run_fkey",
				`UPDATE author_metadata_run SET `+set+` WHERE id = ?`, pilot)
		}
		sf.reject(sqlstateForeignKey, "author_metadata_pilot_approval_run_fkey",
			`DELETE FROM author_metadata_run WHERE id = ?`, pilot)
		// Counters are not part of the approval and stay writable.
		sf.accept(`UPDATE author_metadata_run SET items_total = 3 WHERE id = ?`, pilot)
	})
}

// The ORM models name exactly the schema's columns: a model field without a
// column fails every query that selects it, and a column without a field is
// silently dropped on insert.
func TestAuthorMetadataModelsMatchSchema(t *testing.T) {
	f := withAuthorSchemaTx(t)

	for _, model := range []interface{}{
		(*models.AuthorMetadataRun)(nil),
		(*models.AuthorMetadataRunItem)(nil),
		(*models.AuthorMetadataRunItemAttempt)(nil),
		(*models.AuthorMetadataPilotApproval)(nil),
		(*models.BookMetadataSnapshot)(nil),
		(*models.BookContributorCredit)(nil),
	} {
		table := orm.GetTable(reflect.TypeOf(model).Elem())
		name := strings.Trim(string(table.SQLName), `"`)
		t.Run(name, func(t *testing.T) {
			var fields []string
			for _, field := range table.Fields {
				fields = append(fields, field.SQLName)
			}
			var columns []string
			for column := range tableColumns(t, f.tx, name) {
				columns = append(columns, column)
			}
			assert.ElementsMatch(t, columns, fields)
		})
	}
}

// The model enum constants are exactly the values the CHECK constraints
// accept, as TestAuthorMetadataEnumsAreClosed pins them.
func TestAuthorMetadataModelEnumsMatchSchema(t *testing.T) {
	strs := func(values ...string) []string { return values }
	terminal := strs("extracted", "extracted_no_author", "already_current",
		"entry_missing", "invalid_fb2", "unsupported_encoding", "metadata_parse_failed")

	assert.ElementsMatch(t, strs("smoke", "pilot_archive", "full"), enumStrings(models.AuthorMetadataRunModes()))
	assert.ElementsMatch(t, strs("pending", "running", "paused", "completed", "failed_systemic"),
		enumStrings(models.AuthorMetadataRunStatuses()))
	assert.ElementsMatch(t, append(strs("pending"), terminal...), enumStrings(models.AuthorMetadataRunItemStatuses()))
	assert.ElementsMatch(t, terminal, enumStrings(models.AuthorMetadataRunItemTerminalStatuses()))
	assert.ElementsMatch(t, strs("backfill", "live"), enumStrings(models.BookMetadataSnapshotOrigins()))
	assert.ElementsMatch(t, strs("extracted", "extracted_no_author"), enumStrings(models.BookMetadataSnapshotOutcomes()))
	assert.ElementsMatch(t, strs("author", "translator"), enumStrings(models.ContributorRoles()))

	for _, s := range models.AuthorMetadataRunStatuses() {
		active := s == models.AuthorMetadataRunPending || s == models.AuthorMetadataRunRunning ||
			s == models.AuthorMetadataRunPaused
		assert.Equal(t, active, s.IsActive(), string(s))
	}
	for _, s := range models.AuthorMetadataRunItemStatuses() {
		assert.Equal(t, s != models.AuthorMetadataRunItemPending, s.IsTerminal(), string(s))
	}
	// Values the database would refuse are neither active nor terminal.
	for _, unknown := range []string{"", "unknown", "Extracted", "extracted "} {
		assert.False(t, models.AuthorMetadataRunItemStatus(unknown).IsTerminal(), "%q", unknown)
		assert.False(t, models.AuthorMetadataRunStatus(unknown).IsActive(), "%q", unknown)
	}
}

func enumStrings[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

// Every model round-trips through go-pg: what is inserted through the model
// reads back unchanged, including NULL against empty strings, arrays, JSONB
// and the raw fingerprint bytes.
func TestAuthorMetadataModelsRoundTrip(t *testing.T) {
	f := withAuthorSchemaTx(t)
	admin := f.user()
	book := f.book()

	pilot := &models.AuthorMetadataRun{
		Mode:                  models.AuthorMetadataRunPilotArchive,
		Status:                models.AuthorMetadataRunCompleted,
		ExtractorVersion:      "extractor-v1",
		NormalizerVersion:     "normalizer-v1",
		SelectorArchive:       ptr("fixture.zip"),
		ItemsTotal:            1,
		ItemsTerminal:         1,
		CreatedByUserID:       &admin,
		ExtractionCompletedAt: ptr(time.Now()),
		FinishedAt:            ptr(time.Now()),
	}
	_, err := f.tx.Model(pilot).Returning("*").Insert()
	require.NoError(t, err)
	require.NotZero(t, pilot.ID)
	assert.False(t, pilot.CreatedAt.IsZero(), "database default created_at read back")

	smoke := &models.AuthorMetadataRun{
		Mode:              models.AuthorMetadataRunSmoke,
		Status:            models.AuthorMetadataRunPending,
		ExtractorVersion:  "extractor-v1",
		NormalizerVersion: "normalizer-v1",
		SelectorBookIDs:   []int64{book},
	}
	_, err = f.tx.Model(smoke).Returning("*").Insert()
	require.NoError(t, err)
	var gotRun models.AuthorMetadataRun
	require.NoError(t, f.tx.Model(&gotRun).Where("id = ?", smoke.ID).Select())
	assert.Equal(t, []int64{book}, gotRun.SelectorBookIDs)
	assert.Nil(t, gotRun.SelectorArchive)
	assert.Zero(t, gotRun.ItemsTotal)

	snapshot := &models.BookMetadataSnapshot{
		BookID:           book,
		BookMD5:          bookMD5(book),
		ExtractorVersion: "extractor-v1",
		Origin:           models.BookMetadataSnapshotBackfill,
		RunID:            &pilot.ID,
		Outcome:          models.BookMetadataSnapshotExtracted,
		ArchivePath:      "fixture.zip",
		EntryName:        "entry.fb2",
		XMLProvenance:    json.RawMessage(`{"root":"FictionBook"}`),
		SourceTitle:      ptr("Title"),
		SourceLang:       ptr(""),
		SourceISBNs:      []string{"978-5", ""},
		SourceSequences:  json.RawMessage(`[{"name":"S","number":"1"}]`),
		QualityFlags:     []string{},
		IsCurrent:        true,
	}
	_, err = f.tx.Model(snapshot).Returning("*").Insert()
	require.NoError(t, err)
	var gotSnapshot models.BookMetadataSnapshot
	require.NoError(t, f.tx.Model(&gotSnapshot).Where("id = ?", snapshot.ID).Select())
	assert.Equal(t, snapshot.SourceISBNs, gotSnapshot.SourceISBNs)
	assert.Equal(t, ptr(""), gotSnapshot.SourceLang, "a present empty value stays distinct from NULL")
	assert.Nil(t, gotSnapshot.SourceSrcLang)
	assert.JSONEq(t, string(snapshot.SourceSequences), string(gotSnapshot.SourceSequences))
	assert.True(t, gotSnapshot.IsCurrent)
	assert.Equal(t, models.BookMetadataSnapshotBackfill, gotSnapshot.Origin)

	fp := bytes.Repeat([]byte{0x00, 0xff}, 16)
	credit := &models.BookContributorCredit{
		SnapshotID:        snapshot.ID,
		Role:              models.ContributorRoleAuthor,
		Position:          0,
		SourceFirstName:   ptr(""),
		SourceLastName:    ptr("Фамилия"),
		SourceDisplayName: "Фамилия",
		SourceFingerprint: fp,
		QualityFlags:      []string{},
		XMLProvenance:     json.RawMessage(`{}`),
	}
	_, err = f.tx.Model(credit).Returning("*").Insert()
	require.NoError(t, err)
	var gotCredit models.BookContributorCredit
	require.NoError(t, f.tx.Model(&gotCredit).Where("id = ?", credit.ID).Select())
	assert.Equal(t, fp, gotCredit.SourceFingerprint)
	assert.Equal(t, ptr(""), gotCredit.SourceFirstName)
	assert.Nil(t, gotCredit.SourceMiddleName)
	assert.Equal(t, 0, gotCredit.Position)
	assert.Equal(t, models.ContributorRoleAuthor, gotCredit.Role)

	item := &models.AuthorMetadataRunItem{
		RunID:          pilot.ID,
		BookID:         book,
		Status:         models.AuthorMetadataRunItemPending,
		LeaseOwner:     ptr(ownerToken),
		LeaseExpiresAt: ptr(time.Now().Add(time.Minute)),
	}
	_, err = f.tx.Model(item).Returning("*").Insert()
	require.NoError(t, err)
	assert.False(t, item.NextAttemptAt.IsZero(), "database default next_attempt_at read back")
	assert.Zero(t, item.AttemptCount)

	attempt := &models.AuthorMetadataRunItemAttempt{
		RunItemID:  item.ID,
		AttemptNo:  1,
		LeaseOwner: ownerToken,
	}
	_, err = f.tx.Model(attempt).Returning("*").Insert()
	require.NoError(t, err)
	assert.Nil(t, attempt.FinishedAt)
	assert.Nil(t, attempt.Outcome)

	item.Status = models.AuthorMetadataRunItemExtracted
	item.SnapshotID = &snapshot.ID
	item.LeaseOwner, item.LeaseExpiresAt = nil, nil
	item.FinishedAt = ptr(time.Now())
	_, err = f.tx.Model(item).WherePK().Update()
	require.NoError(t, err, "a full-model update of a finished item")

	approval := &models.AuthorMetadataPilotApproval{
		RunID:             pilot.ID,
		ExtractorVersion:  pilot.ExtractorVersion,
		NormalizerVersion: pilot.NormalizerVersion,
		ApprovedByUserID:  admin,
	}
	_, err = f.tx.Model(approval).Returning("*").Insert()
	require.NoError(t, err)
	assert.Equal(t, models.AuthorMetadataRunPilotArchive, approval.RunMode)
	assert.Equal(t, models.AuthorMetadataRunCompleted, approval.RunStatus)
	assert.False(t, approval.ApprovedAt.IsZero())
}

func ptr[T any](v T) *T { return &v }
