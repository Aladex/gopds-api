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

// Migration 33 makes exactly one more column of author_llm_job mutable —
// output_mode — and leaves the table itself alone: no rewrite, every other
// identity column still refused by the trigger.
func TestMigration33OnlyMakesOutputModeMutable(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_33_whitelist_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "33-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 32: %v", err)
	}
	read := func() (heap uint32, args string) {
		t.Helper()
		if _, readErr := db.QueryOne(pg.Scan(&heap, &args), `
			SELECT c.relfilenode, encode(tg.tgargs, 'escape')
			FROM pg_class c JOIN pg_trigger tg ON tg.tgrelid = c.oid
			WHERE c.relname = 'author_llm_job' AND tg.tgname = 'author_llm_job_identity'`); readErr != nil {
			t.Fatal(readErr)
		}
		return heap, args
	}
	heapBefore, argsBefore := read()
	if _, err = Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("applying 33: %v", err)
	}
	heapAfter, argsAfter := read()
	if heapAfter != heapBefore {
		t.Errorf("migration 33 rewrote author_llm_job (relfilenode %d → %d)", heapBefore, heapAfter)
	}
	// tgargs holds the arguments NUL-separated; escape encoding shows the
	// separators as \000.
	columns := func(args string) []string {
		return strings.Split(strings.TrimSuffix(args, `\000`), `\000`)
	}
	want := append(columns(argsBefore), "output_mode")
	got := columns(argsAfter)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("mutable columns after 33 = %v, want %v", got, want)
	}
}
