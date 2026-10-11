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

// Migration 35 takes every table it alters up front, NOWAIT, and the runs in
// the mode its new foreign key needs: a transaction holding either — the LLM
// worker on the participants' state or the runs — makes it fail at once with
// lock_not_available, and the next start applies it whole.
func TestMigration35FailsFastInsteadOfWaitingForALock(t *testing.T) {
	for _, table := range []string{"author_llm_provider_state", "author_llm_run", "author_llm_participant"} {
		t.Run(table, func(t *testing.T) { migration35UnderAHeldTable(t, table) })
	}
}

func migration35UnderAHeldTable(t *testing.T, table string) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_35_locks_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "35-" {
			// Every file before 35.
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 34: %v", err)
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`LOCK TABLE ` + table + ` IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	elapsed := time.Since(started)
	var pgErr pg.Error
	if err == nil || !errors.As(err, &pgErr) || pgErr.Field('C') != "55P03" {
		t.Fatalf("migration 35 under a held %s: want lock_not_available, got %v", table, err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 35 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "35-") {
		t.Fatalf("a failed migration 35 left a ledger row")
	}
	var exists bool
	if _, err = db.QueryOne(pg.Scan(&exists), `SELECT to_regclass('public.author_llm_relearn') IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("a failed migration 35 left its table")
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 35: %v", err)
	}
	if len(result.Applied) == 0 || !strings.HasPrefix(result.Applied[0], "35-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}
