package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	migrations "gopds-api/database_migrations"
	"gopds-api/internal/migrate"
	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

func names(t *testing.T, fsys fs.FS, dir string) []string {
	t.Helper()
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// Without -dir the command runs the set compiled into the binary, so it works
// from any directory, an image included.
func TestMigrationSetDefaultsToTheEmbeddedFiles(t *testing.T) {
	t.Chdir(t.TempDir()) // nothing on disk to fall back to

	files := migrationSet("")
	got := names(t, files, migrations.Dir)
	if len(got) == 0 {
		t.Fatal("the default set is empty")
	}
	if _, err := fs.Stat(files, filepath.Join(migrations.Dir, "01-initial.sql")); err != nil {
		t.Errorf("the default set lacks 01-initial.sql: %v", err)
	}
}

// -dir runs exactly what is in that directory.
func TestMigrationSetReadsANamedDirectory(t *testing.T) {
	disk := t.TempDir()
	if err := os.WriteFile(filepath.Join(disk, "01-only.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := names(t, migrationSet(disk), migrations.Dir)
	if len(got) != 1 || got[0] != "01-only.sql" {
		t.Errorf("migrationSet(%q) = %v, want [01-only.sql]", disk, got)
	}
}

// The command takes the same lock as a starting server: while a server holds
// it mid-migration, the command queues on it and runs nothing, then finds the
// work done. Without it both would run the same file at once.
func TestApplyQueuesBehindAServerHoldingTheMigrationLock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	cfg, ok := testdb.Configured()
	if !ok {
		t.Skip(testdb.SkipReason)
	}
	admin, err := testdb.Connect(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("cmd_migrate_lock_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); err != nil {
			t.Errorf("dropping the scratch database: %v", err)
		}
	})
	connect := func() *pg.DB {
		db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name})
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	// The "server": the lock held on a session of its own.
	server := connect().Conn()
	t.Cleanup(func() { _ = server.Close() })
	if _, err := server.Exec(`SELECT pg_advisory_lock(?)`, migrate.LockKey); err != nil {
		t.Fatalf("holding the migration lock: %v", err)
	}

	observer := connect()
	files := fstest.MapFS{"01-one.sql": &fstest.MapFile{Data: []byte("CREATE TABLE cli_one (id int);")}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := apply(ctx, connect(), files)
		done <- err
	}()

	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if _, err := observer.QueryOne(pg.Scan(&waiting), `
			SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted AND objsubid = 1
			  AND database = (SELECT oid FROM pg_database WHERE datname = ?)
			  AND classid::bigint = (?::bigint >> 32)
			  AND objid::bigint = (?::bigint & 4294967295)`, name, migrate.LockKey, migrate.LockKey); err != nil {
			t.Fatalf("reading pg_locks: %v", err)
		}
		if waiting == 1 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the command migrated while a server held the lock (err %v)", err)
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never queued on the migration lock")
		}
	}

	if _, err := server.Exec(`SELECT pg_advisory_unlock(?)`, migrate.LockKey); err != nil {
		t.Fatalf("releasing the migration lock: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the command never finished after the lock was released")
	}
}
