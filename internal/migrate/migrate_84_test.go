package migrate

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-pg/pg/v10"
)

// migrationsBefore returns every migration on disk below the file named
// prefix (e.g. "84-"), and the same set through it.
func migrationsBefore(t *testing.T, prefix string) (before, through fstest.MapFS) {
	t.Helper()
	before, through = fstest.MapFS{}, fstest.MapFS{}
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		switch {
		case strings.HasPrefix(e.Name(), prefix):
			found = true
			through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		case e.Name() < prefix:
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
			through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if !found {
		t.Fatalf("no migration %s on disk", prefix)
	}
	return before, through
}

// Migration 84 makes overrides revocable and adds the triage tables. It
// locks only contributor_manual_override, NOWAIT, so a writer of the
// overrides makes it fail at once, and the writers of every other author
// table never hold it back. Overrides that exist stay as they are: unrevoked,
// active, and revocable once.
func TestMigration84AppliesBesideEveryWriterAndOverExistingOverrides(t *testing.T) {
	db, _, _ := scratchDatabase(t)
	ctx := context.Background()
	before, through := migrationsBefore(t, "84-")
	if _, err := Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 83: %v", err)
	}

	fp := bytes.Repeat([]byte{0x84}, 32)
	var override int64
	if _, err := db.QueryOne(pg.Scan(&override), `WITH r AS (
			INSERT INTO contributor_normalization_result
				(source_fingerprint, result_schema_version, method, kind, status, created_by_user_id)
			VALUES (?0, 'manual-v1', 'manual', 'person', 'unresolved', 7)
			RETURNING id)
		INSERT INTO contributor_manual_override (scope_fingerprint, source_fingerprint, result_id, created_by_user_id)
		SELECT ?0, ?0, r.id, 7 FROM r RETURNING id`, fp); err != nil {
		t.Fatalf("seeding an override under 83: %v", err)
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`LOCK TABLE contributor_manual_override IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
		t.Fatal("migration 84 applied while a writer held contributor_manual_override")
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 84 waited %v for the overrides instead of failing at once", time.Since(started))
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}

	writer, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Exec(`LOCK TABLE book_contributor_credit_selection, book_contributor_credit_selection_audit,
		book_contributor_credit, book_metadata_snapshot, contributor_review_item, contributor_normalization_job,
		author_entity_assignment, author_entity, auth_user, opds_catalog_book
		IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	// Replacing the selection check compiles its body, which reads the row
	// type of the results: a writer of the results (ROW EXCLUSIVE, what every
	// path that adds one holds) does not hold that back.
	if _, err = writer.Exec(`LOCK TABLE contributor_normalization_result IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("migration 84 beside the writers: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 84 waited %v for the writers", time.Since(started))
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "84-") {
		t.Fatalf("applied %v, want 84 alone", result.Applied)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}

	var unrevoked int
	if _, err = db.QueryOne(pg.Scan(&unrevoked), `SELECT count(*) FROM contributor_manual_override
		WHERE id = ? AND revoked_at IS NULL AND revoked_by_user_id IS NULL`, override); err != nil {
		t.Fatal(err)
	}
	if unrevoked != 1 {
		t.Fatalf("the existing override is not unrevoked after 84")
	}
	var validated bool
	if _, err = db.QueryOne(pg.Scan(&validated), `SELECT convalidated FROM pg_constraint
		WHERE conname = 'contributor_manual_override_revoke_check'`); err != nil {
		t.Fatal(err)
	}
	if !validated {
		t.Error("the revoke check is not validated over the existing rows")
	}
	if _, err = db.Exec(`UPDATE contributor_manual_override SET revoked_at = now(), revoked_by_user_id = 7
		WHERE id = ?`, override); err != nil {
		t.Fatalf("revoking an override written before 84: %v", err)
	}
	if _, err = db.Exec(`UPDATE contributor_manual_override SET revoked_at = NULL, revoked_by_user_id = NULL
		WHERE id = ?`, override); err == nil {
		t.Error("a revoke written after 84 was taken back")
	}

	for table, want := range map[string]int{
		"contributor_manual_override": 2, "author_review_triage_batch": 1, "author_review_triage_item": 1,
	} {
		var triggers int
		if _, err = db.QueryOne(pg.Scan(&triggers), `SELECT count(*) FROM pg_trigger
			WHERE NOT tgisinternal AND tgrelid = ?::regclass`, table); err != nil {
			t.Fatal(err)
		}
		if triggers != want {
			t.Errorf("migration 84 left %d triggers on %s, want %d", triggers, table, want)
		}
	}
}
