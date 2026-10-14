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

// Migration 37 takes the eval reports up front, NOWAIT: a transaction
// holding them — the report stage storing one — makes it fail at once with
// lock_not_available, and the next start applies it whole.
func TestMigration37FailsFastInsteadOfWaitingForALock(t *testing.T) {
	const table = "author_llm_eval_report"
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_37_locks_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "37-" {
			// Every file before 37.
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 36: %v", err)
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
		t.Fatalf("migration 37 under a held %s: want lock_not_available, got %v", table, err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 37 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "37-") {
		t.Fatalf("a failed migration 37 left a ledger row")
	}
	var exists bool
	if _, err = db.QueryOne(pg.Scan(&exists), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'author_llm_eval_report' AND column_name = 'version')`); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("a failed migration 37 left its column")
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 37: %v", err)
	}
	if len(result.Applied) == 0 || !strings.HasPrefix(result.Applied[0], "37-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}
