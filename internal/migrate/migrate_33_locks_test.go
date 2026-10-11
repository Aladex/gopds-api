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

// Migration 33 takes author_llm_job up front, NOWAIT, like 28-32: a
// transaction that holds it (the LLM worker or the eval driver) makes it fail
// at once with lock_not_available, and the next start applies it whole.
func TestMigration33FailsFastInsteadOfWaitingForALock(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_33_locks_test_%d", time.Now().UnixNano())
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
			// Every file before 33.
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

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`LOCK TABLE author_llm_job IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, err = Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	elapsed := time.Since(started)
	var pgErr pg.Error
	if err == nil || !errors.As(err, &pgErr) || pgErr.Field('C') != "55P03" {
		t.Fatalf("migration 33 under a held table: want lock_not_available, got %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 33 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "33-") {
		t.Fatalf("a failed migration 33 left a ledger row")
	}
	var mutable bool
	if _, err = db.QueryOne(pg.Scan(&mutable), `SELECT pg_get_triggerdef(oid) LIKE '%output_mode%'
		FROM pg_trigger WHERE tgname = 'author_llm_job_identity'`); err != nil {
		t.Fatal(err)
	}
	if mutable {
		t.Fatalf("a failed migration 33 left its trigger")
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 33: %v", err)
	}
	// The retry applies 33 first; any later file follows in the same run.
	if len(result.Applied) == 0 || !strings.HasPrefix(result.Applied[0], "33-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}
