package main

import (
	"context"
	"errors"
	"io/fs"
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

// The closed vocabulary of the schema log fields.
const (
	logFieldCount    = "count"
	logFieldFiles    = "files"
	logFieldFile     = "file"
	logFieldSQLState = "sqlstate"
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
	// manual run, waits here and then finds nothing pending.
	result, err := migrate.RunLocked(ctx, db, files, migrations.Dir, migrate.AppBaseline())
	report.Applied, report.Baselined = result.Applied, result.Baselined
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
