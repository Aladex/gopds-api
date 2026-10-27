package main

import (
	"context"
	"errors"
	"io/fs"
	"math/rand/v2"
	"strings"
	"time"

	migrations "gopds-api/database_migrations"
	"gopds-api/internal/migrate"
	"gopds-api/logging"

	"github.com/go-pg/pg/v10"
)

// schemaMigrationFiles is the migration set a server start applies: the one
// compiled into the binary. A variable only so a test process can swap it.
var schemaMigrationFiles = migrations.FS()

// startupMigrationTimeout bounds the wait for migrate.LockKey plus the
// migrations. It matches cmd/migrate: long enough to build the whole schema on an empty
// database, short enough that a wedged start gives up instead of hanging.
const startupMigrationTimeout = 10 * time.Minute

// A migration that takes a busy table NOWAIT fails with lock_not_available
// while a writer holds it; a start waits and tries again, from
// migrationLockRetryFirst doubling to migrationLockRetryMax with jitter,
// until its deadline. Variables only so a test can make them short and see
// each retry.
var (
	migrationLockRetryFirst = 2 * time.Second
	migrationLockRetryMax   = 30 * time.Second
	migrationLockRetryHook  func(attempt int)
)

// The closed vocabulary of the schema log fields.
const (
	logFieldCount    = "count"
	logFieldFiles    = "files"
	logFieldFile     = "file"
	logFieldSQLState = "sqlstate"
	logFieldAttempt  = "attempt"
	logFieldWaitMS   = "wait_ms"
)

// schemaStartup reports what a start did to the schema.
type schemaStartup struct {
	// Applied names the migrations this start executed, in order.
	Applied []string
	// Baselined names migrations recorded without being executed, on a
	// database that predates the ledger.
	Baselined []string
	// Pending names what a start with auto_migrate off left waiting.
	Pending []string
}

// prepareSchema is the start's schema step: it runs before any other
// database user and reports whether the start may go on.
func prepareSchema(db *pg.DB, autoMigrate bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), startupMigrationTimeout)
	defer cancel()
	_, err := migrateSchemaOnStartup(ctx, db, schemaMigrationFiles, autoMigrate)
	return err == nil
}

// migrateSchemaOnStartup brings the schema up to date from files, or, with
// autoMigrate off, only reports what is pending and changes nothing.
//
// Every outcome is logged here as a closed event: file names, counts and a
// SQLSTATE, never statement text or the server's message.
func migrateSchemaOnStartup(ctx context.Context, db *pg.DB, files fs.FS, autoMigrate bool) (schemaStartup, error) {
	var report schemaStartup
	if !autoMigrate {
		toRecord, toApply, err := migrate.Pending(ctx, db, files, migrations.Dir, migrate.AppBaseline())
		if err != nil {
			logSchemaMigrationFailure("", err)
			return report, err
		}
		report.Pending = append(report.Pending, toRecord...)
		report.Pending = append(report.Pending, toApply...)
		if len(report.Pending) == 0 {
			logging.Info("schema up to date")
			return report, nil
		}
		logging.WithFields(fileListFields(report.Pending)).Warn("schema_migrations_pending")
		return report, nil
	}

	// The same lock cmd/migrate takes: a second replica, or an operator's
	// manual run, waits here and then finds nothing pending. A file that
	// finds a table held (lock_not_available) left nothing behind and is
	// tried again until the start's deadline; the old replica keeps serving
	// meanwhile, since this one is not ready before its schema is.
	err := runWithLockRetries(ctx, func() error {
		result, runErr := migrate.RunLocked(ctx, db, files, migrations.Dir, migrate.AppBaseline())
		report.Applied = append(report.Applied, result.Applied...)
		report.Baselined = append(report.Baselined, result.Baselined...)
		return runErr
	})
	if err != nil {
		var failed *migrate.FileError
		file := ""
		if errors.As(err, &failed) {
			file = failed.Name
		}
		logSchemaMigrationFailure(file, err)
		return report, err
	}

	if len(report.Baselined) > 0 {
		logging.WithFields(fileListFields(report.Baselined)).Info("schema_migrations_baselined")
	}
	if len(report.Applied) > 0 {
		logging.WithFields(fileListFields(report.Applied)).Info("schema_migrations_applied")
	} else if len(report.Baselined) == 0 {
		logging.Info("schema up to date")
	}
	return report, nil
}

// sqlStateLockNotAvailable is a NOWAIT lock or a lock_timeout that found
// the table held. Each retry waits migrationLockBackoffFactor times the
// last, split into migrationLockJitterParts: one fixed, the rest random.
const (
	sqlStateLockNotAvailable   = "55P03"
	migrationLockBackoffFactor = 2
	migrationLockJitterParts   = 2
)

// runWithLockRetries runs attempt until it succeeds, fails with anything but
// lock_not_available, or ctx ends while it waits: then the last lock error
// is the start's. Each retry is logged as a closed event.
func runWithLockRetries(ctx context.Context, attempt func() error) error {
	wait := migrationLockRetryFirst
	for n := 1; ; n++ {
		err := attempt()
		if err == nil || sqlStateOf(err) != sqlStateLockNotAvailable {
			return err
		}
		// Half the wait fixed, half random, so two starts do not retry in step.
		half := wait / migrationLockJitterParts
		pause := half + rand.N(half+1) // #nosec G404 -- a retry's jitter, not a secret
		file := ""
		var failed *migrate.FileError
		if errors.As(err, &failed) {
			file = failed.Name
		}
		logging.WithFields(map[string]any{logFieldFile: file, logFieldSQLState: sqlStateLockNotAvailable,
			logFieldAttempt: n, logFieldWaitMS: pause.Milliseconds()}).Warn("schema_migration_lock_retry")
		if migrationLockRetryHook != nil {
			migrationLockRetryHook(n)
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(pause):
		}
		wait = min(migrationLockBackoffFactor*wait, migrationLockRetryMax)
	}
}

// fileListFields renders a list of migrations as log fields.
func fileListFields(names []string) map[string]any {
	return map[string]any{logFieldCount: len(names), logFieldFiles: strings.Join(names, ",")}
}

// logSchemaMigrationFailure writes the closed failure event.
func logSchemaMigrationFailure(file string, err error) {
	logging.WithFields(map[string]any{
		logFieldFile:     file,
		logFieldSQLState: sqlStateOf(err),
	}).Error("schema_migration_failed")
}

// sqlStateOf extracts the server's SQLSTATE, or "" for a failure that never
// reached the server.
func sqlStateOf(err error) string {
	var pgErr pg.Error
	if errors.As(err, &pgErr) {
		return pgErr.Field('C')
	}
	return ""
}
