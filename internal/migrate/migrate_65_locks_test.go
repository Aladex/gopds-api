package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

// Migration 65 adds the decision books: it locks the decision table alone,
// NOWAIT, so the writers a pass or a scan holds — even exclusively — never
// hold it back, and a writer of decisions makes it fail at once.
func TestMigration65AppliesBesideEveryWriter(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_65_locks_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + scratch + " WITH (FORCE)") })
	db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: scratch})
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	before, through := fstest.MapFS{}, fstest.MapFS{}
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "66-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() < "65-" {
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 64: %v", err)
	}

	// A writer of decisions holds the new table back: the file fails at once
	// and applies whole on the next start.
	decider, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decider.Exec(`LOCK TABLE author_entity_merge_decision IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
		t.Fatal("migration 65 applied while a writer held the decisions")
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 65 waited %v instead of failing at once", time.Since(started))
	}
	if err = decider.Rollback(); err != nil {
		t.Fatal(err)
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Exec(`LOCK TABLE author_entity, author_entity_merge_log, author_entity_assignment,
		author_entity_credit_assignment, author_entity_build_run, author_entity_merge_candidate,
		author_autopilot_mark, auth_user, book_contributor_credit, opds_catalog_book IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("migration 65 beside the writers: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 65 waited %v for the writers", time.Since(started))
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "65-") {
		t.Fatalf("applied %v, want 65 alone", result.Applied)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	var triggers int
	if _, err = db.QueryOne(pg.Scan(&triggers), `SELECT count(*) FROM pg_trigger
		WHERE NOT tgisinternal AND tgrelid = 'author_entity_merge_decision_book'::regclass`); err != nil {
		t.Fatal(err)
	}
	if triggers != 1 {
		t.Errorf("migration 65 made %d triggers, want 1", triggers)
	}
}
