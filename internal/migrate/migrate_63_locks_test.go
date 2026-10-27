package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

// Migration 63 takes every table it puts a mark trigger on up front, NOWAIT:
// a scan holding the books makes it fail at once, leaving nothing, and the
// next start applies it whole. Writers of the tables it does not touch —
// the selections, the entity and LLM runs — do not hold it back.
func TestMigration63FailsFastAndAppliesBesideOtherWriters(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_63_locks_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "64-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() < "63-" {
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 62: %v", err)
	}

	for _, held := range []string{"opds_catalog_book", "opds_catalog_bgenre", "book_metadata_snapshot",
		"author_entity_assignment", "wikidata_extract"} {
		scan, beginErr := db.Begin()
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, err = scan.Exec(`LOCK TABLE ` + held + ` IN ROW EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		_, err = Run(ctx, db, through, "database_migrations", AppBaseline())
		elapsed := time.Since(started)
		var pgErr pg.Error
		if err == nil || !errors.As(err, &pgErr) || pgErr.Field('C') != "55P03" {
			t.Fatalf("migration 63 under a held %s: want lock_not_available, got %v", held, err)
		}
		if elapsed > time.Second {
			t.Errorf("migration 63 waited %v for %s instead of failing at once", elapsed, held)
		}
		if err = scan.Rollback(); err != nil {
			t.Fatal(err)
		}
		var exists bool
		if _, err = db.QueryOne(pg.Scan(&exists), `SELECT to_regclass('public.author_entity_merge_candidate') IS NOT NULL
			OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgname LIKE '%entity_input%')`); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("a failed migration 63 under %s left its table or a trigger", held)
		}
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Exec(`LOCK TABLE book_contributor_credit_selection, book_contributor_credit, author_llm_run,
		author_entity_build_run, author_entity, auth_user IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 63 beside the other writers: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 63 waited %v for the other writers", time.Since(started))
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "63-") {
		t.Fatalf("applied %v, want 63 alone", result.Applied)
	}
	var triggers int
	if _, err = db.QueryOne(pg.Scan(&triggers), `SELECT count(*) FROM pg_trigger WHERE tgname LIKE '%entity_input%'`); err != nil {
		t.Fatal(err)
	}
	if triggers != 29 {
		t.Errorf("migration 63 made %d mark triggers, want 29", triggers)
	}
}
