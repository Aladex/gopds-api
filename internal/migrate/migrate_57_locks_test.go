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

// Migration 57 takes the read model, the assignments, the entity names and
// the build runs up front, NOWAIT: the read-model worker or a build holding
// any of them makes it fail at once with lock_not_available, and the next
// start applies it whole.
func TestMigration57FailsFastInsteadOfWaitingForALock(t *testing.T) {
	for _, table := range []string{"book_author_display", "author_entity_assignment", "author_entity_credit_assignment",
		"author_entity_name", "author_entity_build_run"} {
		t.Run(table, func(t *testing.T) { migration57UnderAHeldTable(t, table) })
	}
}

func migration57UnderAHeldTable(t *testing.T, table string) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_57_locks_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "57-" {
			// Every file before 57.
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 56: %v", err)
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
		t.Fatalf("migration 57 under a held %s: want lock_not_available, got %v", table, err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 57 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "57-") {
		t.Fatalf("a failed migration 57 left a ledger row")
	}
	var exists bool
	if _, err = db.QueryOne(pg.Scan(&exists), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'book_author_display' AND column_name = 'entity_ids')`); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("a failed migration 57 left its column")
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 57: %v", err)
	}
	if len(result.Applied) == 0 || !strings.HasPrefix(result.Applied[0], "57-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}
