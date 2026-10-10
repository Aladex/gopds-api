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

// Migration 28 never waits for a table lock while it holds another: an
// application writer that holds one of its tables (a review action on the
// review items) makes it fail at once, before it holds anything the writer
// may need next — no lock cycle, no deadlock victim among the writers — and
// the next start retries it whole.
func TestMigration28FailsFastInsteadOfWaitingForALock(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_28_locks_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + scratch + " WITH (FORCE)") })
	db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: scratch})
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	// Everything up to 27.
	before := fstest.MapFS{}
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") || strings.HasPrefix(e.Name(), "28-") {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 27: %v", err)
	}

	// A review action in flight holds the review items.
	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`LOCK TABLE contributor_review_item IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, err = Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	elapsed := time.Since(started)
	var pgErr pg.Error
	if err == nil || !errors.As(err, &pgErr) || pgErr.Field('C') != "55P03" {
		t.Fatalf("migration 28 under a held table: want lock_not_available, got %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 28 waited %v for a lock instead of failing at once", elapsed)
	}
	// The writer goes on: the run tables the migration would lock are free.
	if _, err = writer.Exec(`SET LOCAL lock_timeout = '1s'; LOCK TABLE author_metadata_run_item IN ROW EXCLUSIVE MODE`); err != nil {
		t.Errorf("the writer could not continue: %v", err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "28-") {
		t.Fatalf("a failed migration 28 left a ledger row")
	}

	// The retry applies it whole.
	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 28: %v", err)
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "28-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}
