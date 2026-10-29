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

// Migration 68 adds the merge registrations: it locks the decisions, the
// LLM configurations and the eval reports, NOWAIT, so a writer of any of
// them makes it fail at once, and the writers a pass, a scan, K's
// acceptance or a user hold — even exclusively — never hold it back.
func TestMigration68AppliesBesideEveryWriter(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_68_locks_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "69-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() < "68-" {
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 67: %v", err)
	}

	// A writer of any locked table holds the file back: it fails at once
	// and applies whole on the next start.
	for _, table := range []string{"author_entity_merge_decision", "author_llm_config", "author_llm_eval_report"} {
		writer, beginErr := db.Begin()
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, err = writer.Exec(`LOCK TABLE ` + table + ` IN ROW EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
			t.Fatalf("migration 68 applied while a writer held %s", table)
		}
		if time.Since(started) > time.Second {
			t.Errorf("migration 68 waited %v for %s instead of failing at once", time.Since(started), table)
		}
		if err = writer.Rollback(); err != nil {
			t.Fatal(err)
		}
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Exec(`LOCK TABLE author_entity, author_entity_merge_log, author_entity_assignment,
		author_entity_build_run, author_entity_merge_candidate, author_autopilot_mark, auth_user,
		author_acceptance_class, author_llm_run, author_llm_job, book_contributor_credit, opds_catalog_book
		IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("migration 68 beside the writers: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 68 waited %v for the writers", time.Since(started))
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "68-") {
		t.Fatalf("applied %v, want 68 alone", result.Applied)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"author_entity_merge_registration": 3, "author_entity_merge_script_block": 2} {
		var triggers int
		if _, err = db.QueryOne(pg.Scan(&triggers), `SELECT count(*) FROM pg_trigger
			WHERE NOT tgisinternal AND tgrelid = ?::regclass`, table); err != nil {
			t.Fatal(err)
		}
		if triggers != want {
			t.Errorf("migration 68 made %d triggers on %s, want %d", triggers, table, want)
		}
	}
}
