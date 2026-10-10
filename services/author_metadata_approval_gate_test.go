package services

import (
	"context"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedPilot builds a completed one-book pilot over the review fixture's
// book — one author credit in review, its job completed — and an admin that
// reads its figures stored (live limit 0, the branch of a pilot above the
// limit). It returns the run, the open review item and the admin.
func gatedPilot(t *testing.T) (f *reviewFixture, runID, item, actor int64, admin *AuthorMetadataRunAdmin) {
	t.Helper()
	f = newReviewFixture(t)
	resetPipeline(t, f.s)
	item, credit := f.seedItem("first", "A.", "last", "Pushkin")
	f.commit()

	var snapshot, book int64
	_, err := f.s.QueryOne(pg.Scan(&snapshot, &book), `SELECT s.id, s.book_id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE c.id = ?`, credit)
	require.NoError(t, err)
	_, err = f.s.QueryOne(pg.Scan(&runID), `INSERT INTO author_metadata_run
		(mode, status, extractor_version, normalizer_version, selector_archive, items_total, items_terminal,
		 started_at, extraction_completed_at, finished_at)
		VALUES ('pilot_archive', 'completed', ?, ?, 'fixture.zip', 1, 1,
			clock_timestamp(), clock_timestamp(), clock_timestamp())
		RETURNING id`, reviewExtractor, authornorm.NormalizerVersion)
	require.NoError(t, err)
	_, err = f.s.Exec(`INSERT INTO author_metadata_run_item (run_id, book_id, status, snapshot_id, attempt_count, finished_at)
		VALUES (?, ?, 'extracted', ?, 1, clock_timestamp())`, runID, book, snapshot)
	require.NoError(t, err)
	actor = 7

	c := config.AuthorMetadataConfig{
		MetadataMaxBytes:   config.AuthorMetadataMaxBytes,
		Extraction:         config.AuthorMetadataStageConfig{Concurrency: 1, ClaimSize: 10, Lease: time.Minute, MaxAttempts: 3},
		LocalNormalization: config.AuthorMetadataStageConfig{Concurrency: 1, ClaimSize: 10, Lease: time.Minute, MaxAttempts: 3},
	}
	admin, err = NewAuthorMetadataRunAdmin(NewAuthorMetadataRunService(f.s), f.s, &c)
	require.NoError(t, err)
	admin.WithLiveAggregateLimit(0)
	t.Cleanup(admin.Wait)

	require.NoError(t, admin.RefreshAggregates(context.Background(), runID))
	report, err := admin.Report(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, report.Ready, "settled after the refresh: %v", report.NotReadyReasons)
	return f, runID, item, actor, admin
}

// The approval of a pilot is decided by the exact accounting at the moment
// of the action, never by the stored figures: a credit that became pending
// after the last refresh — a review retry, a reset of the resolutions — is
// refused even while the stored figures still read settled.
func TestApprovalRechecksTheCreditsAtActionTime(t *testing.T) {
	t.Run("review retry", func(t *testing.T) {
		f, runID, item, actor, admin := gatedPilot(t)
		ctx := context.Background()
		_, err := reviewService(t, f.s, DefaultAuthorMetadataReviewConfig()).Retry(ctx, item, actor)
		require.NoError(t, err)

		_, err = admin.ApproveFull(ctx, runID, actor)
		require.ErrorIs(t, err, database.ErrNotACompletedPilot, "a pending credit refuses the approval")
		approved, err := database.RunApproved(ctx, f.s, runID)
		require.NoError(t, err)
		assert.False(t, approved)
	})
	t.Run("truncated resolutions", func(t *testing.T) {
		f, runID, _, actor, admin := gatedPilot(t)
		ctx := context.Background()
		_, err := f.s.Exec(`TRUNCATE book_contributor_credit_selection_audit, book_contributor_credit_selection`)
		require.NoError(t, err)

		_, err = admin.ApproveFull(ctx, runID, actor)
		require.ErrorIs(t, err, database.ErrNotACompletedPilot)
	})
	t.Run("settled pilot is approved", func(t *testing.T) {
		f, runID, _, actor, admin := gatedPilot(t)
		_, err := admin.ApproveFull(context.Background(), runID, actor)
		require.NoError(t, err)
		approved, err := database.RunApproved(context.Background(), f.s, runID)
		require.NoError(t, err)
		assert.True(t, approved)
	})
}
