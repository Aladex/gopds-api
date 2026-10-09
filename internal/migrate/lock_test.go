package migrate

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

// scratchDatabase creates an empty database of its own and drops it after.
// The migration lock is per database, so a scratch one keeps these tests from
// queueing behind, or blocking, anything else that migrates.
func scratchDatabase(t *testing.T) (*pg.DB, string, testdb.Config) {
	t.Helper()
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	name := fmt.Sprintf("migrate_lock_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); err != nil {
			t.Errorf("dropping the scratch database: %v", err)
		}
	})
	db := connectScratch(cfg, name)
	t.Cleanup(func() { _ = db.Close() })
	return db, name, cfg
}

func connectScratch(cfg testdb.Config, name string) *pg.DB {
	return pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name})
}

// waitingForLock counts sessions of database name queued for LockKey.
func waitingForLock(t *testing.T, db *pg.DB, name string) int {
	t.Helper()
	var n int
	if _, err := db.QueryOne(pg.Scan(&n), `
		SELECT count(*) FROM pg_locks
		WHERE locktype = 'advisory' AND NOT granted AND objsubid = 1
		  AND database = (SELECT oid FROM pg_database WHERE datname = ?)
		  AND classid::bigint = (?::bigint >> 32)
		  AND objid::bigint = (?::bigint & 4294967295)`, name, LockKey, LockKey); err != nil {
		t.Fatalf("reading pg_locks: %v", err)
	}
	return n
}

// lockIsFree reports whether a session of another pool can take LockKey now;
// it gives the lock straight back when it could.
func lockIsFree(t *testing.T, cfg testdb.Config, name string) bool {
	t.Helper()
	other := connectScratch(cfg, name)
	defer func() { _ = other.Close() }()
	conn := other.Conn()
	defer func() { _ = conn.Close() }()
	var got bool
	if _, err := conn.QueryOne(pg.Scan(&got), `SELECT pg_try_advisory_lock(?)`, LockKey); err != nil {
		t.Fatalf("trying the migration lock: %v", err)
	}
	if got {
		if _, err := conn.Exec(`SELECT pg_advisory_unlock(?)`, LockKey); err != nil {
			t.Fatalf("releasing the probe lock: %v", err)
		}
	}
	return got
}

func tableExists(t *testing.T, db *pg.DB, name string) bool {
	t.Helper()
	var exists bool
	if _, err := db.QueryOne(pg.Scan(&exists), `SELECT to_regclass(?) IS NOT NULL`, name); err != nil {
		t.Fatalf("looking for %s: %v", name, err)
	}
	return exists
}

// RunLocked queues behind whoever holds the migration lock, runs nothing
// while it waits, and migrates once the lock is free.
func TestRunLockedWaitsForTheMigrationLock(t *testing.T) {
	db, name, cfg := scratchDatabase(t)
	holder := connectScratch(cfg, name)
	t.Cleanup(func() { _ = holder.Close() })
	hold := holder.Conn()
	t.Cleanup(func() { _ = hold.Close() })
	if _, err := hold.Exec(`SELECT pg_advisory_lock(?)`, LockKey); err != nil {
		t.Fatalf("holding the migration lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := RunLocked(ctx, db, files(map[string]string{"01-one.sql": "CREATE TABLE locked_one (id int);"}),
			"migrations", Baseline{Established: fresh})
		done <- outcome{res, err}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for waitingForLock(t, holder, name) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("RunLocked never queued on the migration lock")
		}
		select {
		case o := <-done:
			t.Fatalf("RunLocked finished while another session held the lock: %+v", o)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if tableExists(t, holder, "locked_one") || tableExists(t, holder, "schema_migrations") {
		t.Fatal("RunLocked touched the schema before it held the lock")
	}

	if _, err := hold.Exec(`SELECT pg_advisory_unlock(?)`, LockKey); err != nil {
		t.Fatalf("releasing the migration lock: %v", err)
	}
	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("RunLocked: %v", o.err)
		}
		if len(o.res.Applied) != 1 || o.res.Applied[0] != "01-one.sql" {
			t.Fatalf("applied %v, want [01-one.sql]", o.res.Applied)
		}
	case <-ctx.Done():
		t.Fatal("RunLocked never finished after the lock was released")
	}
}

// The lock goes back on every way out of a run, so the next migrator — the
// next replica, or the command — is never left waiting on a finished one.
func TestRunLockedReleasesTheLock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"after success", map[string]string{"01-one.sql": "CREATE TABLE released_one (id int);"}},
		{"after a failing file", map[string]string{"01-one.sql": "SELECT 1/0;"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, name, cfg := scratchDatabase(t)
			_, _ = RunLocked(context.Background(), db, files(tc.files), "migrations", Baseline{Established: fresh})
			if !lockIsFree(t, cfg, name) {
				t.Fatal("the migration lock is still held after RunLocked returned")
			}
		})
	}
}

// A failing file is reported by name, typed, with nothing after it applied.
func TestRunNamesTheFailingFile(t *testing.T) {
	db, _, _ := scratchDatabase(t)

	res, err := Run(context.Background(), db, files(map[string]string{
		"01-ok.sql":    "CREATE TABLE named_one (id int);",
		"02-fails.sql": "SELECT 1/0;",
		"03-later.sql": "CREATE TABLE named_three (id int);",
	}), "migrations", Baseline{Established: fresh})

	var failed *FileError
	if !errors.As(err, &failed) {
		t.Fatalf("Run error = %v, want a *FileError", err)
	}
	if failed.Name != "02-fails.sql" {
		t.Errorf("FileError.Name = %q, want 02-fails.sql", failed.Name)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "01-ok.sql" {
		t.Errorf("applied %v, want [01-ok.sql]", res.Applied)
	}
	if tableExists(t, db, "named_three") {
		t.Error("a file after the failing one ran")
	}
}
