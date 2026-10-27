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

// Migration 64 creates tables that reference only each other and triggers on
// them alone: it takes no lock on an existing table, so the writers a pass
// or a scan holds — even exclusively — never hold it back.
func TestMigration64AppliesBesideEveryWriter(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_64_locks_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "65-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() < "64-" {
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 63: %v", err)
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
	started := time.Now()
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("migration 64 beside the writers: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 64 waited %v for the writers", time.Since(started))
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "64-") {
		t.Fatalf("applied %v, want 64 alone", result.Applied)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	var triggers int
	if _, err = db.QueryOne(pg.Scan(&triggers), `SELECT count(*) FROM pg_trigger
		WHERE NOT tgisinternal AND tgrelid::regclass::text LIKE 'author_entity_merge_%'
			AND tgrelid::regclass::text NOT LIKE 'author_entity_merge_candidate%'`); err != nil {
		t.Fatal(err)
	}
	if triggers != 8 {
		t.Errorf("migration 64 made %d triggers, want 8", triggers)
	}
}
