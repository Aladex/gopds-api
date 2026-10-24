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

// Migration 62 takes the selections up front, NOWAIT, for its triggers: a
// writer holding them makes it fail at once, leaving nothing, and the next
// start applies it whole. The binary before it writing anything else —
// snapshots, local jobs, LLM runs, entity passes — does not hold it back.
// It starts the autopilot on, with one mark: the selections made before its
// triggers are a pass to do.
func TestMigration62FailsFastAndAppliesBesideOtherWriters(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_62_locks_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + scratch + " WITH (FORCE)") })
	db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: scratch})
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	before := fstest.MapFS{}
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "62-" {
			// Every file before 62.
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 61: %v", err)
	}

	selections, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = selections.Exec(`LOCK TABLE book_contributor_credit_selection IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	elapsed := time.Since(started)
	var pgErr pg.Error
	if err == nil || !errors.As(err, &pgErr) || pgErr.Field('C') != "55P03" {
		t.Fatalf("migration 62 under a held selection table: want lock_not_available, got %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 62 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = selections.Rollback(); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if _, err = db.QueryOne(pg.Scan(&exists), `SELECT to_regclass('public.author_autopilot') IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("a failed migration 62 left its table")
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Exec(`LOCK TABLE book_metadata_snapshot, book_contributor_credit, contributor_normalization_job,
		author_llm_run, author_entity_build_run, author_acceptance_class, auth_user IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 62 beside the old binary's other writes: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 62 waited %v for the old binary's other writes", time.Since(started))
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "62-") {
		t.Fatalf("applied %v, want 62 alone", result.Applied)
	}
	var state struct {
		Enabled bool `pg:"enabled"`
		Rows    int  `pg:"rows"`
	}
	if _, err = db.QueryOne(&state, `SELECT bool_and(enabled) AS enabled, count(*) AS rows FROM author_autopilot`); err != nil {
		t.Fatal(err)
	}
	if state.Rows != 1 || !state.Enabled {
		t.Errorf("the autopilot starts as %+v, want one row, on", state)
	}
	var marks int
	if _, err = db.QueryOne(pg.Scan(&marks), `SELECT count(*) FROM author_autopilot_mark WHERE build_run_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	if marks != 1 {
		t.Errorf("migration 62 seeded %d marks, want one: the selections made before it are to be built", marks)
	}
	if _, err = db.Exec(`INSERT INTO author_autopilot DEFAULT VALUES`); err == nil {
		t.Error("a second autopilot row was accepted")
	}
}
