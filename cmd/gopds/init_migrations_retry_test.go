package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopds-api/internal/migrate"
	"gopds-api/logging"

	"github.com/go-pg/pg/v10"
	logrustest "github.com/sirupsen/logrus/hooks/test" //nolint:depguard // asserting on emitted log output needs logrus' own test hook
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A migration that takes a busy table NOWAIT (the 28–63 pattern) fails with
// lock_not_available while a writer holds it. A start retries that, in the
// same process, with a bounded backoff; any other failure stops it at once.

// lockedFiles are a table and a migration that takes it NOWAIT.
func lockedFiles() map[string]string {
	return map[string]string{
		"01-table.sql": "CREATE TABLE retry_busy (id int);",
		"02-locks.sql": "SET LOCAL lock_timeout = '5s';\n" +
			"LOCK TABLE retry_busy IN SHARE ROW EXCLUSIVE MODE NOWAIT;\n" +
			"CREATE TABLE retry_after (id int);",
	}
}

// fastRetries makes the backoff a test's size until the test ends.
func fastRetries(t *testing.T, onRetry func(attempt int)) {
	t.Helper()
	first, maxWait, hook := migrationLockRetryFirst, migrationLockRetryMax, migrationLockRetryHook
	migrationLockRetryFirst, migrationLockRetryMax, migrationLockRetryHook = 10*time.Millisecond, 40*time.Millisecond, onRetry
	t.Cleanup(func() { migrationLockRetryFirst, migrationLockRetryMax, migrationLockRetryHook = first, maxWait, hook })
}

// holdTable takes ROW EXCLUSIVE on table in a transaction of its own; the
// returned function ends it (idempotent).
func holdTable(t *testing.T, db *pg.DB, table string) (release func()) {
	t.Helper()
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(`LOCK TABLE ` + table + ` IN ROW EXCLUSIVE MODE`)
	require.NoError(t, err)
	released := false
	release = func() {
		if !released {
			released = true
			_ = tx.Rollback()
		}
	}
	t.Cleanup(release)
	return release
}

// The first attempt finds the table held and fails 55P03; the writer lets
// go; the same start retries and applies the file, logging the retry.
func TestStartupRetriesALockNotAvailableInTheSameProcess(t *testing.T) {
	db, _, _ := emptyScratchDB(t)
	files := lockedFiles()
	_, err := migrateSchemaOnStartup(context.Background(), db, sqlFS(map[string]string{"01-table.sql": files["01-table.sql"]}), true)
	require.NoError(t, err)
	release := holdTable(t, db, "retry_busy")
	retries := 0
	fastRetries(t, func(attempt int) {
		retries = attempt
		release()
	})
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	res, err := migrateSchemaOnStartup(context.Background(), db, sqlFS(files), true)
	require.NoError(t, err)
	assert.Equal(t, []string{"02-locks.sql"}, res.Applied)
	assert.Equal(t, 1, retries)
	assert.True(t, relationExists(t, db, "retry_after"))
	var retried bool
	for _, e := range hook.AllEntries() {
		assertNoSQLText(t, e)
		if e.Message == "schema_migration_lock_retry" {
			retried = true
			assert.Equal(t, "02-locks.sql", e.Data["file"])
			assert.Equal(t, "55P03", e.Data["sqlstate"])
			assert.Equal(t, 1, e.Data["attempt"])
		}
	}
	assert.True(t, retried, "the retry is not logged")
}

// A table held for longer than the start may wait: the retries stop at the
// start's deadline and the start fails with the lock error.
func TestStartupGivesUpALockHeldPastItsDeadline(t *testing.T) {
	db, _, _ := emptyScratchDB(t)
	files := lockedFiles()
	_, err := migrateSchemaOnStartup(context.Background(), db, sqlFS(map[string]string{"01-table.sql": files["01-table.sql"]}), true)
	require.NoError(t, err)
	holdTable(t, db, "retry_busy")
	retries := 0
	fastRetries(t, func(attempt int) { retries = attempt })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err = migrateSchemaOnStartup(ctx, db, sqlFS(files), true)
	var failed *migrate.FileError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, "55P03", sqlStateOf(err))
	assert.Greater(t, retries, 1, "it retried")
	assert.Less(t, time.Since(started), 2*time.Second, "it waited past its deadline")
	assert.False(t, relationExists(t, db, "retry_after"))
}

// Any other failure is no lock to wait for: it stops the start at once.
func TestStartupDoesNotRetryAnotherFailure(t *testing.T) {
	db, _, _ := emptyScratchDB(t)
	retries := 0
	fastRetries(t, func(attempt int) { retries = attempt })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := migrateSchemaOnStartup(ctx, db, failingFiles(), true)
	var failed *migrate.FileError
	require.True(t, errors.As(err, &failed))
	assert.Equal(t, "22012", sqlStateOf(err))
	assert.Zero(t, retries)
}
