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

// Migration 31 takes its locks up front, NOWAIT, like 28 and 29: a transaction
// that holds one of the LLM tables it alters (here the job queue, which the
// LLM worker writes) makes it fail at once with lock_not_available before it
// holds anything that stays held, and the next start applies it whole.
func TestMigration31FailsFastInsteadOfWaitingForALock(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_31_locks_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "31-" {
			// Every file before 31; later files may need what 31 creates.
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 29: %v", err)
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
		t.Fatalf("migration 31 under a held table: want lock_not_available, got %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 31 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "31-") {
		t.Fatalf("a failed migration 31 left a ledger row")
	}
	var settings int
	if _, err = db.QueryOne(pg.Scan(&settings), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'author_llm_run' AND column_name = 'settings'`); err != nil {
		t.Fatal(err)
	}
	if settings != 0 {
		t.Fatalf("a failed migration 31 left its column")
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 31: %v", err)
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "31-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}
