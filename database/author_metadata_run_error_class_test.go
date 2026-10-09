package database

// Phase 20 integration fix round 1: a run's last_error_class is one of the
// closed run error classes — the systemic classes that pause a run or end it
// failed_systemic. Matching the schema's shape is not membership: a
// name-shaped value such as zqxcanary_person_123456 passes the shape and is
// refused by every writer, without its text in the error.

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runClassCanary = "zqxcanary_person_123456"

func runStatusAndClass(t *testing.T, sf *authorSchemaFixture, run int64) (status models.AuthorMetadataRunStatus, class *string) {
	t.Helper()
	_, err := sf.tx.QueryOne(pg.Scan(&status, &class),
		`SELECT status, last_error_class FROM author_metadata_run WHERE id = ?`, run)
	require.NoError(t, err)
	return status, class
}

func TestSystemicWritersTakeOnlyRunErrorClasses(t *testing.T) {
	ctx := context.Background()
	writers := []struct {
		name  string
		write func(context.Context, pg.DBI, int64, string) error
	}{
		{"PauseRunSystemic", PauseRunSystemic},
		{"FailRunSystemic", FailRunSystemic},
	}
	// Each refused class passes the schema's shape check.
	refused := []string{runClassCanary, "transient_database", "normalizer_failed", "lease_expired"}
	for _, w := range writers {
		for _, class := range refused {
			t.Run(w.name+" refuses "+class, func(t *testing.T) {
				sf := withAuthorSchemaTx(t)
				run := sf.run(&runSpec{status: "running"})
				err := w.write(ctx, sf.tx, run, class)
				require.ErrorIs(t, err, ErrInvalidLeaseFailure)
				assert.NotContains(t, err.Error(), class, "the refused value is not echoed")
				status, stored := runStatusAndClass(t, sf, run)
				assert.Equal(t, models.AuthorMetadataRunRunning, status, "a refused class changes nothing")
				assert.Nil(t, stored)
			})
		}
	}

	// Known controls: the class each writer is called with in production.
	t.Run("PauseRunSystemic takes archive_unreadable", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "running"})
		require.NoError(t, PauseRunSystemic(ctx, sf.tx, run, "archive_unreadable"))
		status, stored := runStatusAndClass(t, sf, run)
		assert.Equal(t, models.AuthorMetadataRunPaused, status)
		require.NotNil(t, stored)
		assert.Equal(t, "archive_unreadable", *stored)
	})
	for _, class := range []string{"database_invariant", "version_mismatch", "extractor_misconfigured"} {
		t.Run("FailRunSystemic takes "+class, func(t *testing.T) {
			sf := withAuthorSchemaTx(t)
			run := sf.run(&runSpec{status: "running"})
			require.NoError(t, FailRunSystemic(ctx, sf.tx, run, class))
			status, stored := runStatusAndClass(t, sf, run)
			assert.Equal(t, models.AuthorMetadataRunFailedSystemic, status)
			require.NotNil(t, stored)
			assert.Equal(t, class, *stored)
		})
	}
}

// The response projection: none stays none, each run error class is itself,
// anything else stored — a name-shaped value, a lease or worker class, an
// empty string — is "other".
func TestClosedRunErrorClass(t *testing.T) {
	assert.Nil(t, ClosedRunErrorClass(nil))
	for _, class := range AuthorMetadataRunErrorClasses() {
		got := ClosedRunErrorClass(&class)
		require.NotNil(t, got)
		assert.Equal(t, class, *got)
	}
	for _, stored := range []string{runClassCanary, "transient_database", "lease_expired", "", "Archive_Unreadable"} {
		got := ClosedRunErrorClass(&stored)
		require.NotNil(t, got, "%q", stored)
		assert.Equal(t, AuthorMetadataOtherValue, *got, "%q", stored)
	}
	assert.ElementsMatch(t, []string{"archive_unreadable", "database_invariant", "version_mismatch", "extractor_misconfigured"},
		AuthorMetadataRunErrorClasses())
}
