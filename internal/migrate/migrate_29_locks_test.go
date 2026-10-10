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

// Migration 29 takes its locks up front, NOWAIT, like 28: a transaction that
// holds one of the existing tables it alters or references (here the
// acceptance policy, which the selection trigger reads) makes it fail at once
// with lock_not_available before it holds anything, and the next start applies
// it whole.
func TestMigration29FailsFastInsteadOfWaitingForALock(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_29_locks_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + scratch + " WITH (FORCE)") })
	db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: scratch})
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	// Everything up to 28; 29 and whatever follows it come with the retry.
	before := fstest.MapFS{}
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatal(err)
	}
	later := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if number, _, _ := strings.Cut(e.Name(), "-"); len(number) == 2 && number >= "29" {
			later++
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 28: %v", err)
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`LOCK TABLE author_acceptance_class IN ROW SHARE MODE`); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, err = Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	elapsed := time.Since(started)
	var pgErr pg.Error
	if err == nil || !errors.As(err, &pgErr) || pgErr.Field('C') != "55P03" {
		t.Fatalf("migration 29 under a held table: want lock_not_available, got %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 29 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "29-") {
		t.Fatalf("a failed migration 29 left a ledger row")
	}
	var tables int
	if _, err = db.QueryOne(pg.Scan(&tables), `SELECT count(*) FROM pg_tables WHERE tablename LIKE 'author_llm_%'`); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("a failed migration 29 left %d tables", tables)
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 29: %v", err)
	}
	if len(result.Applied) != later || !strings.HasPrefix(result.Applied[0], "29-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}
