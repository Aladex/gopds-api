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

// Migration 40 takes the participants' state up front, NOWAIT: a transaction
// holding it — the LLM worker claiming or settling a call — makes it fail at
// once with lock_not_available, and the next start applies it whole.
func TestMigration40FailsFastInsteadOfWaitingForALock(t *testing.T) {
	const table = "author_llm_provider_state"
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_40_locks_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "40-" {
			// Every file before 40.
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 39: %v", err)
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
		t.Fatalf("migration 40 under a held %s: want lock_not_available, got %v", table, err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 40 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "40-") {
		t.Fatalf("a failed migration 40 left a ledger row")
	}
	var columns int
	if _, err = db.QueryOne(pg.Scan(&columns), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'author_llm_provider_state' AND column_name IN ('check_failures', 'check_failing_since')`); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatalf("a failed migration 40 left its columns")
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 40: %v", err)
	}
	if len(result.Applied) == 0 || !strings.HasPrefix(result.Applied[0], "40-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
	var check string
	if _, err = db.QueryOne(pg.Scan(&check), `SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conname = 'author_llm_provider_state_pause_check'`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(check, "check_unavailable") {
		t.Fatalf("the retried migration 40 left the old pause check: %s", check)
	}
}
