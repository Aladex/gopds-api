package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// raiseSQLState aborts tx with the given SQLSTATE, as PostgreSQL does to a
// deadlock victim.
func raiseSQLState(tx *pg.Tx, code string) error {
	_, err := tx.Exec(fmt.Sprintf(`DO $$ BEGIN RAISE EXCEPTION 'injected' USING ERRCODE = '%s'; END $$`, code))
	return err
}

// The retry is a bounded safety net for deadlock victims and serialization
// failures only; every retry is counted.
func TestRunAuthorLLMTxRetriesDeadlockVictimsOnly(t *testing.T) {
	requireDatabase(t)
	ctx := context.Background()

	for _, code := range []string{sqlStateDeadlock, sqlStateSerialization} {
		before := AuthorLLMTxRetries()
		attempts := 0
		err := RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
			attempts++
			if attempts == 1 {
				return raiseSQLState(tx, code)
			}
			return nil
		})
		require.NoError(t, err, code)
		assert.Equal(t, 2, attempts, code)
		assert.Equal(t, before+1, AuthorLLMTxRetries(), code)
	}

	// Bounded: a transaction that keeps losing gives up with the error.
	attempts := 0
	err := RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		attempts++
		return raiseSQLState(tx, sqlStateDeadlock)
	})
	assert.Equal(t, sqlStateDeadlock, pgErrorCode(err))
	assert.Equal(t, authorLLMTxAttempts, attempts)

	// Any other error is not retried.
	attempts = 0
	err = RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		attempts++
		return raiseSQLState(tx, "23505")
	})
	assert.Equal(t, "23505", pgErrorCode(err))
	assert.Equal(t, 1, attempts)
}
