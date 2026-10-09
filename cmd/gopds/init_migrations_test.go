package main

// Task C: a server start brings the schema up to date itself, under an
// advisory lock, and refuses to serve a schema it could not finish.

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	migrations "gopds-api/database_migrations"
	"gopds-api/internal/migrate"
	"gopds-api/internal/testdb"
	"gopds-api/logging"

	"github.com/go-pg/pg/v10"
	//nolint:depguard // the assertions name logrus levels and entries
	"github.com/sirupsen/logrus"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateLockKey is an advisory lock the tests hold to stop a migration halfway
// through: a gated migration file waits on it inside its own transaction.
const gateLockKey int64 = 0x676174655f6d6967 // "gate_mig"

// lastPreLedger22 is the newest migration production held before this release.
const lastPreLedger22 = "22-author-search-index.sql"

func connectTo(cfg testdb.Config, name string) *pg.DB {
	return pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name})
}

// emptyScratchDB creates a database with nothing in it and drops it after.
func emptyScratchDB(t *testing.T) (*pg.DB, string, testdb.Config) {
	t.Helper()
	admin, cfg := configuredDB(t)
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("startup_migrate_test_%d", time.Now().UnixNano())
	_, err := admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, dropErr := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); dropErr != nil {
			t.Errorf("dropping %s: %v", name, dropErr)
		}
	})
	db := connectTo(cfg, name)
	t.Cleanup(func() { _ = db.Close() })
	return db, name, cfg
}

// diskMigrations lists the migration files in the repository, sorted.
func diskMigrations(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("../../database_migrations")
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// ledger reads schema_migrations, or nil when the table does not exist.
func ledger(t *testing.T, db *pg.DB) []string {
	t.Helper()
	if !relationExists(t, db, "schema_migrations") {
		return nil
	}
	var got []string
	_, err := db.Query(&got, `SELECT version FROM schema_migrations ORDER BY version`)
	require.NoError(t, err)
	return got
}

func relationExists(t *testing.T, db *pg.DB, name string) bool {
	t.Helper()
	var exists bool
	_, err := db.QueryOne(pg.Scan(&exists), `SELECT to_regclass(?) IS NOT NULL`, name)
	require.NoError(t, err)
	return exists
}

// waitingOn counts sessions of database dbName queued for the advisory lock
// key without holding it.
func waitingOn(t *testing.T, db *pg.DB, dbName string, key int64) int {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), `
		SELECT count(*) FROM pg_locks
		WHERE locktype = 'advisory' AND NOT granted AND objsubid = 1
		  AND database = (SELECT oid FROM pg_database WHERE datname = ?)
		  AND classid::bigint = (?::bigint >> 32)
		  AND objid::bigint = (?::bigint & 4294967295)`, dbName, key, key)
	require.NoError(t, err)
	return n
}

// holdGate takes the gate lock on a connection of its own; the returned
// function releases it (idempotent).
func holdGate(t *testing.T, db *pg.DB) (release func()) {
	t.Helper()
	conn := db.Conn()
	_, err := conn.Exec(`SELECT pg_advisory_lock(?)`, gateLockKey)
	require.NoError(t, err)
	released := false
	release = func() {
		if released {
			return
		}
		released = true
		_, _ = conn.Exec(`SELECT pg_advisory_unlock(?)`, gateLockKey)
		_ = conn.Close()
	}
	t.Cleanup(release)
	return release
}

func sqlFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

// A fresh database gets the whole embedded set; the next start finds
// nothing to do.
func TestStartupMigratesFreshDatabaseThenNothing(t *testing.T) {
	db, _, _ := emptyScratchDB(t)
	ctx := context.Background()
	want := diskMigrations(t)

	first, err := migrateSchemaOnStartup(ctx, db, migrations.FS(), true)
	require.NoError(t, err)
	assert.Equal(t, want, first.Applied, "a fresh database runs every migration")
	assert.Empty(t, first.Baselined)
	assert.Equal(t, want, ledger(t, db))

	second, err := migrateSchemaOnStartup(ctx, db, migrations.FS(), true)
	require.NoError(t, err)
	assert.Empty(t, second.Applied, "a second start applies nothing")
	assert.Empty(t, second.Baselined)
	assert.Equal(t, want, ledger(t, db), "the ledger is unchanged by a second start")
}

// ledger22Database builds the production schema as it stood before this
// release — every embedded migration up to and including 22, through the real
// runner — in a scratch database of its own, dropped after the test.
//
// It never templates a shared database: CREATE DATABASE ... TEMPLATE needs
// the source free of other sessions, which the parallel integration gate never
// guarantees for the configured catalog.
func ledger22Database(t *testing.T) *pg.DB {
	t.Helper()
	db, _, _ := emptyScratchDB(t)

	upTo22 := fstest.MapFS{}
	for _, name := range diskMigrations(t) {
		if name > lastPreLedger22 {
			continue
		}
		body, err := fs.ReadFile(migrations.FS(), path.Join(migrations.Dir, name))
		require.NoError(t, err)
		upTo22[name] = &fstest.MapFile{Data: body}
	}
	_, err := migrate.Run(context.Background(), db, upTo22, migrations.Dir, migrate.AppBaseline())
	require.NoError(t, err, "building the ledger-22 schema")

	held := ledger(t, db)
	require.NotEmpty(t, held)
	require.Equal(t, lastPreLedger22, held[len(held)-1], "the fixture stops at 22")
	return db
}

// On a database at ledger 22 (production before this release), a start with
// the switch off reports 23 and 24 and changes nothing; with it on it applies
// exactly those two; the start after that applies nothing.
func TestStartupOnLedger22DatabaseAppliesOnlyTheNewFiles(t *testing.T) {
	db := ledger22Database(t)
	ctx := context.Background()
	before := ledger(t, db)

	disk := diskMigrations(t)
	var want []string
	for _, name := range disk {
		if name > lastPreLedger22 {
			want = append(want, name)
		}
	}
	require.Subset(t, want, []string{"23-author-metadata-source.sql", "24-author-normalization-pipeline.sql"})

	off, err := migrateSchemaOnStartup(ctx, db, migrations.FS(), false)
	require.NoError(t, err)
	assert.Equal(t, want, off.Pending, "with auto_migrate off the start lists what is pending")
	assert.Empty(t, off.Applied)
	assert.Equal(t, before, ledger(t, db), "with auto_migrate off nothing is recorded")
	assert.False(t, relationExists(t, db, "contributor_normalization_job"), "with auto_migrate off nothing runs")

	started := time.Now()
	on, err := migrateSchemaOnStartup(ctx, db, migrations.FS(), true)
	require.NoError(t, err)
	t.Logf("applying %v to the ledger-22 schema took %s", on.Applied, time.Since(started))
	assert.Equal(t, want, on.Applied, "only the files after 22 run")
	assert.Empty(t, on.Baselined, "a database with a ledger is never baselined")
	assert.Equal(t, append(append([]string{}, before...), want...), ledger(t, db))
	assert.True(t, relationExists(t, db, "contributor_normalization_job"), "24 ran")

	again, err := migrateSchemaOnStartup(ctx, db, migrations.FS(), true)
	require.NoError(t, err)
	assert.Empty(t, again.Applied, "the next start applies nothing")
	assert.Empty(t, again.Pending)
}

// auto_migrate off on an empty database: every file is reported, nothing is
// created, not even the ledger.
func TestStartupWithAutoMigrateOffAppliesNothing(t *testing.T) {
	db, _, _ := emptyScratchDB(t)

	got, err := migrateSchemaOnStartup(context.Background(), db, migrations.FS(), false)
	require.NoError(t, err)
	assert.Equal(t, diskMigrations(t), got.Pending)
	assert.Empty(t, got.Applied)
	assert.False(t, relationExists(t, db, "schema_migrations"), "the ledger is not even created")
	assert.False(t, relationExists(t, db, "auth_user"), "no migration ran")
}

type startupOutcome struct {
	res schemaStartup
	err error
}

// Two starters released together: one migrates while the other waits on the
// advisory lock — observed in pg_locks while the first is held mid-file —
// and then finds nothing pending.
func TestStartupConcurrentStartersSerializeOnTheAdvisoryLock(t *testing.T) {
	_, name, cfg := emptyScratchDB(t)
	observer := connectTo(cfg, name)
	t.Cleanup(func() { _ = observer.Close() })
	release := holdGate(t, observer)

	files := sqlFS(map[string]string{
		"01-gate.sql":  fmt.Sprintf("SELECT pg_advisory_xact_lock(%d); CREATE TABLE gated_one (id int);", gateLockKey),
		"02-after.sql": "CREATE TABLE gated_two (id int);",
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	start := make(chan struct{})
	outcomes := make(chan startupOutcome, 2)
	for i := 0; i < 2; i++ {
		starter := connectTo(cfg, name)
		t.Cleanup(func() { _ = starter.Close() })
		go func() {
			<-start
			res, err := migrateSchemaOnStartup(ctx, starter, files, true)
			outcomes <- startupOutcome{res, err}
		}()
	}
	close(start)

	require.Eventually(t, func() bool {
		return waitingOn(t, observer, name, gateLockKey) == 1 &&
			waitingOn(t, observer, name, migrate.LockKey) == 1
	}, 15*time.Second, 20*time.Millisecond,
		"one starter must be inside the gated migration and the other queued on the migration lock")
	select {
	case o := <-outcomes:
		t.Fatalf("a starter finished while the first migration was still held: %+v", o)
	default:
	}
	release()

	var got []startupOutcome
	for i := 0; i < 2; i++ {
		select {
		case o := <-outcomes:
			require.NoError(t, o.err)
			got = append(got, o)
		case <-ctx.Done():
			t.Fatal("the starters never finished")
		}
	}
	sort.Slice(got, func(i, j int) bool { return len(got[i].res.Applied) > len(got[j].res.Applied) })
	assert.Equal(t, []string{"01-gate.sql", "02-after.sql"}, got[0].res.Applied, "one starter applies everything")
	assert.Empty(t, got[1].res.Applied, "the other sees nothing pending")
	assert.Equal(t, []string{"01-gate.sql", "02-after.sql"}, ledger(t, observer))
}

// The same race on the real embedded set and an empty database: both starts
// succeed, the schema is built once.
func TestStartupConcurrentStartersOnFreshDatabase(t *testing.T) {
	_, name, cfg := emptyScratchDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	start := make(chan struct{})
	outcomes := make(chan startupOutcome, 2)
	for i := 0; i < 2; i++ {
		starter := connectTo(cfg, name)
		t.Cleanup(func() { _ = starter.Close() })
		go func() {
			<-start
			res, err := migrateSchemaOnStartup(ctx, starter, migrations.FS(), true)
			outcomes <- startupOutcome{res, err}
		}()
	}
	close(start)

	total := 0
	empty := 0
	for i := 0; i < 2; i++ {
		o := <-outcomes
		require.NoError(t, o.err)
		total += len(o.res.Applied)
		if len(o.res.Applied) == 0 {
			empty++
		}
	}
	assert.Equal(t, len(diskMigrations(t)), total, "every migration ran exactly once")
	assert.Equal(t, 1, empty, "one starter found nothing to do")
}

// failingFiles is a set whose middle file fails.
func failingFiles() fstest.MapFS {
	return sqlFS(map[string]string{
		"01-ok.sql":    "CREATE TABLE child_one (id int);",
		"02-fails.sql": "SELECT 1/0;",
		"03-later.sql": "CREATE TABLE child_three (id int);",
	})
}

// A failing migration stops the start: the error names the file, the files
// after it are not applied, the log carries the file name and SQLSTATE but
// no SQL text or server message.
func TestStartupMigrationFailureStopsAndNamesTheFile(t *testing.T) {
	db, _, _ := emptyScratchDB(t)
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	res, err := migrateSchemaOnStartup(context.Background(), db, failingFiles(), true)
	var failed *migrate.FileError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, "02-fails.sql", failed.Name)
	assert.Equal(t, []string{"01-ok.sql"}, res.Applied)
	assert.Equal(t, []string{"01-ok.sql"}, ledger(t, db), "nothing after the failing file is recorded")
	assert.False(t, relationExists(t, db, "child_three"), "nothing after the failing file runs")

	var entry *logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Message == "schema_migration_failed" {
			entry = e
		}
		assertNoSQLText(t, e)
	}
	require.NotNil(t, entry, "the failure is logged as a closed event")
	assert.Equal(t, logrus.ErrorLevel, entry.Level)
	assert.Equal(t, "02-fails.sql", entry.Data["file"])
	assert.Equal(t, "22012", entry.Data["sqlstate"])
}

// assertNoSQLText fails when a log entry carries statement text or the
// server's message.
func assertNoSQLText(t *testing.T, e *logrus.Entry) {
	t.Helper()
	text := e.Message + fmt.Sprint(e.Data)
	for _, banned := range []string{"SELECT", "CREATE", "division", "pg_advisory"} {
		assert.NotContains(t, text, banned, "log entry %q leaks SQL or server text", e.Message)
	}
}

// The start logs the files it applied and their count, "schema up to date"
// when there were none, and the pending files as a warning when it may not
// migrate.
func TestStartupMigrationLogs(t *testing.T) {
	db, _, _ := emptyScratchDB(t)
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	files := sqlFS(map[string]string{
		"01-one.sql": "CREATE TABLE log_one (id int);",
		"02-two.sql": "CREATE TABLE log_two (id int);",
	})
	ctx := context.Background()

	_, err := migrateSchemaOnStartup(ctx, db, files, false)
	require.NoError(t, err)
	pending := hook.LastEntry()
	require.NotNil(t, pending)
	assert.Equal(t, "schema_migrations_pending", pending.Message)
	assert.Equal(t, logrus.WarnLevel, pending.Level)
	assert.Equal(t, "01-one.sql,02-two.sql", pending.Data["files"])
	assert.Equal(t, 2, pending.Data["count"])

	hook.Reset()
	_, err = migrateSchemaOnStartup(ctx, db, files, true)
	require.NoError(t, err)
	applied := hook.LastEntry()
	require.NotNil(t, applied)
	assert.Equal(t, "schema_migrations_applied", applied.Message)
	assert.Equal(t, logrus.InfoLevel, applied.Level)
	assert.Equal(t, "01-one.sql,02-two.sql", applied.Data["files"])
	assert.Equal(t, 2, applied.Data["count"])

	hook.Reset()
	_, err = migrateSchemaOnStartup(ctx, db, files, true)
	require.NoError(t, err)
	upToDate := hook.LastEntry()
	require.NotNil(t, upToDate)
	assert.Equal(t, "schema up to date", upToDate.Message)

	hook.Reset()
	_, err = migrateSchemaOnStartup(ctx, db, files, false)
	require.NoError(t, err)
	require.NotNil(t, hook.LastEntry())
	assert.Equal(t, "schema up to date", hook.LastEntry().Message, "nothing pending is not a warning")

	for _, e := range hook.AllEntries() {
		assertNoSQLText(t, e)
	}
}

// Helper process for the process-level tests below: the real run() with a
// migration set chosen by the parent.
func TestStartupMigrationChild(t *testing.T) {
	if os.Getenv("GOPDS_STARTUP_CHILD") == "" {
		t.Skip("helper process for the startup migration process tests")
	}
	schemaMigrationFiles = sqlFS(map[string]string{
		"01-ok.sql":        "CREATE TABLE child_one (id int);",
		"02-gate-fail.sql": fmt.Sprintf("SELECT pg_advisory_xact_lock(%d); SELECT 1/0;", gateLockKey),
		"03-later.sql":     "CREATE TABLE child_three (id int);",
	})
	os.Exit(run())
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// startChild runs the helper process against database name; Redis points at
// a closed port, so a start that gets past the schema dies there.
func startChild(t *testing.T, ctx context.Context, cfg testdb.Config, name string, extraEnv ...string) (*exec.Cmd, *strings.Builder, int) {
	t.Helper()
	dir := t.TempDir()
	port := freePort(t)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupMigrationChild$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOPDS_STARTUP_CHILD=1",
		"GOPDS_SECRET_KEY=startup-test-secret",
		"GOPDS_SESSIONS_KEY=startup-test-session",
		"GOPDS_SESSIONS_REFRESH=startup-test-refresh",
		"GOPDS_POSTGRES_DBHOST="+cfg.Host,
		"GOPDS_POSTGRES_DBUSER="+cfg.User,
		"GOPDS_POSTGRES_DBPASS="+cfg.Password,
		"GOPDS_POSTGRES_DBNAME="+name,
		"GOPDS_SERVER_HOST=127.0.0.1",
		"GOPDS_SERVER_PORT="+strconv.Itoa(port),
		"GOPDS_REDIS_HOST=127.0.0.1",
		"GOPDS_REDIS_PORT="+strconv.Itoa(freePort(t)),
		"GOPDS_APP_FILES_PATH="+filepath.Join(dir, "files"),
		"GOPDS_APP_USERS_PATH="+filepath.Join(dir, "users"),
		"GOPDS_APP_POSTERS_PATH="+filepath.Join(dir, "posters"),
		"GOPDS_APP_MOBI_CONVERSION_DIR="+filepath.Join(dir, "mobi"),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out := &strings.Builder{}
	cmd.Stdout = out
	cmd.Stderr = out
	require.NoError(t, cmd.Start())
	return cmd, out, port
}

// The real process: while the second migration is held, nothing listens on
// the server port; when it fails, the process exits 1 on its own, the third
// file never runs, and the log names the file without SQL text.
func TestStartupFailingMigrationExitsBeforeServing(t *testing.T) {
	db, name, cfg := emptyScratchDB(t)
	release := holdGate(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd, out, port := startChild(t, ctx, cfg, name)
	require.Eventually(t, func() bool { return waitingOn(t, db, name, gateLockKey) == 1 },
		30*time.Second, 20*time.Millisecond, "the child never reached the second migration\n%s", out)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 100 * time.Millisecond}
	for i := 0; i < 15; i++ {
		if conn, err := dialer.DialContext(ctx, "tcp", addr); err == nil {
			_ = conn.Close()
			t.Fatal("the server listens while the schema is still being migrated")
		}
		time.Sleep(100 * time.Millisecond)
	}
	release()

	err := cmd.Wait()
	require.NoError(t, ctx.Err(), "the process did not exit on its own\n%s", out)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "the process must exit non-zero\n%s", out)
	assert.Equal(t, 1, exitErr.ExitCode(), "a failed migration ends the start with status 1\n%s", out)

	assert.Equal(t, []string{"01-ok.sql"}, ledger(t, db))
	assert.False(t, relationExists(t, db, "child_three"), "nothing after the failing file runs")
	log := out.String()
	assert.Contains(t, log, "schema_migration_failed")
	assert.Contains(t, log, "02-gate-fail.sql")
	assert.NotContains(t, log, "Server is starting", "the server never starts")
	// initializeAuthorMetadata logs an event whatever the worker switch says;
	// any event means it ran before the schema was finished.
	assert.NotContains(t, log, "author_metadata.", "the author metadata step never runs on a failed schema")
	for _, banned := range []string{"division", "SELECT", "pg_advisory"} {
		assert.NotContains(t, log, banned, "the log leaks SQL or server text")
	}
}

// The real process with GOPDS_DATABASE_AUTO_MIGRATE=false: it never enters a
// migration (the gate stays shut and it does not wait), warns with the
// pending files, and carries on with the start — here, into the Redis it
// cannot reach.
func TestStartupAutoMigrateOffDoesNotMigrate(t *testing.T) {
	db, name, cfg := emptyScratchDB(t)
	holdGate(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd, out, _ := startChild(t, ctx, cfg, name, "GOPDS_DATABASE_AUTO_MIGRATE=false")
	err := cmd.Wait()
	require.NoError(t, ctx.Err(), "the start blocked: it must not run migrations with auto_migrate off\n%s", out)
	require.Error(t, err, "the child is expected to die at the unreachable Redis")

	log := out.String()
	assert.Contains(t, log, "schema_migrations_pending")
	assert.Contains(t, log, "01-ok.sql,02-gate-fail.sql,03-later.sql")
	assert.Contains(t, log, "Failed to connect to Redis", "the start went on past the schema check\n%s", log)
	pendingAt := strings.Index(log, "schema_migrations_pending")
	authorAt := strings.Index(log, "author_metadata.workers_disabled")
	assert.True(t, pendingAt >= 0 && authorAt > pendingAt, "the schema check precedes the author metadata step\n%s", log)
	assert.False(t, relationExists(t, db, "schema_migrations"), "nothing is recorded")
	assert.False(t, relationExists(t, db, "child_one"), "nothing runs")
}
