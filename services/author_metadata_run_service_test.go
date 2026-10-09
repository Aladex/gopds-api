package services_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The run service is the admin-facing gate of the extraction pipeline
// (contract 3.9): it validates the mode and selector, canonicalizes the book
// IDs, enforces the approved-pilot rule of a full run, and owns the
// pause/resume transitions. The repository underneath owns the SQL; these
// tests pin the composition against a scratch database, since a seeded run
// must be committed to be real.

const (
	runServiceExtractor  = services.AuthorMetadataExtractorVersion
	runServiceNormalizer = authornorm.NormalizerVersion
)

func seedCatalogBook(t *testing.T, db *pg.DB, id int64, path, filename string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO opds_catalog_book
		(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES (?, ?, ?, 'fb2', now(), '', 'ru', 'run service fixture', '', ?)`,
		id, filename, path, fmt.Sprintf("%032x", id))
	require.NoError(t, err)
}

func startRequest(mode models.AuthorMetadataRunMode) *services.StartRunRequest {
	return &services.StartRunRequest{
		Mode:              mode,
		ExtractorVersion:  runServiceExtractor,
		NormalizerVersion: runServiceNormalizer,
	}
}

func runItems(t *testing.T, db *pg.DB, runID int64) []int64 {
	t.Helper()
	var books []int64
	_, err := db.Query(&books, `SELECT book_id FROM author_metadata_run_item WHERE run_id = ? ORDER BY book_id`, runID)
	require.NoError(t, err)
	return books
}

// RED 1: a smoke start validates and canonicalizes the selector and seeds one
// item per book in the same transaction.
func TestStartRunSmoke(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	svc := services.NewAuthorMetadataRunService(db)

	for i, id := range []int64{11, 22, 33} {
		seedCatalogBook(t, db, id, "smoke.zip", fmt.Sprintf("b%d.fb2", i))
	}

	t.Run("happy: canonical order, one item per book, actor recorded", func(t *testing.T) {
		_, err := db.Exec(`INSERT INTO auth_user (id, password, is_superuser, username, email, date_joined)
			VALUES (7, '', true, 'run-service-admin', 'run-service-admin@fixture.local', now())`)
		require.NoError(t, err)
		req := startRequest(models.AuthorMetadataRunSmoke)
		req.BookIDs = []int64{33, 11, 22}
		actor := int64(7)
		req.CreatedByUserID = &actor

		run, err := svc.StartRun(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, run)
		assert.Equal(t, models.AuthorMetadataRunSmoke, run.Mode)
		assert.Equal(t, models.AuthorMetadataRunRunning, run.Status)
		assert.Equal(t, []int64{11, 22, 33}, run.SelectorBookIDs, "out-of-order IDs are stored ascending")

		assert.Equal(t, []int64{11, 22, 33}, runItems(t, db, run.ID))

		var total int
		var startedAt *bool
		var createdBy *int64
		_, err = db.QueryOne(pg.Scan(&total, &startedAt, &createdBy), `
			SELECT items_total, started_at IS NOT NULL, created_by_user_id
			FROM author_metadata_run WHERE id = ?`, run.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		require.NotNil(t, startedAt)
		assert.True(t, *startedAt, "a started run stamps started_at")
		require.NotNil(t, createdBy)
		assert.Equal(t, actor, *createdBy)
	})

	finishActiveRun(t, db)
}

func finishActiveRun(t *testing.T, db *pg.DB) {
	t.Helper()
	_, err := db.Exec(`UPDATE author_metadata_run
		SET status = 'completed', extraction_completed_at = now(), finished_at = now()
		WHERE status IN ('pending', 'running', 'paused')`)
	require.NoError(t, err)
}

// RED 1 (rejects): duplicate, zero, negative and empty selectors are client
// errors; nothing is seeded.
func TestStartRunRejectsBadSelectors(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	svc := services.NewAuthorMetadataRunService(db)
	seedCatalogBook(t, db, 11, "smoke.zip", "b.fb2")

	for _, ids := range [][]int64{{11, 11}, {0}, {-1}, {11, -2}} {
		req := startRequest(models.AuthorMetadataRunSmoke)
		req.BookIDs = ids
		_, err := svc.StartRun(ctx, req)
		require.ErrorIs(t, err, database.ErrInvalidRunBookIDs, "IDs %v", ids)
	}

	req := startRequest(models.AuthorMetadataRunSmoke)
	_, err := svc.StartRun(ctx, req)
	require.ErrorIs(t, err, database.ErrInvalidRunSelector, "smoke without IDs")

	req = startRequest("everything")
	req.BookIDs = []int64{11}
	_, err = svc.StartRun(ctx, req)
	require.ErrorIs(t, err, database.ErrInvalidRunSelector, "a mode outside the closed set")

	req = startRequest(models.AuthorMetadataRunSmoke)
	req.BookIDs = []int64{11}
	req.ExtractorVersion = " "
	_, err = svc.StartRun(ctx, req)
	require.Error(t, err, "a blank version")

	var runs int
	_, err = db.QueryOne(pg.Scan(&runs), `SELECT count(*) FROM author_metadata_run`)
	require.NoError(t, err)
	assert.Zero(t, runs, "a refused start seeds nothing")
}

// RED 2: starting a second active run is refused by PostgreSQL itself, not by
// an in-process check.
func TestStartRunOneActive(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	svc := services.NewAuthorMetadataRunService(db)
	seedCatalogBook(t, db, 11, "smoke.zip", "b.fb2")

	req := startRequest(models.AuthorMetadataRunSmoke)
	req.BookIDs = []int64{11}
	_, err := svc.StartRun(ctx, req)
	require.NoError(t, err)

	_, err = svc.StartRun(ctx, req)
	require.ErrorIs(t, err, database.ErrActiveRunExists)

	finishActiveRun(t, db)
	_, err = svc.StartRun(ctx, req)
	require.NoError(t, err, "a terminal run frees the slot")
}

// RED 1 (pilot): the archive selector enumerates the archive's books.
func TestStartRunPilotArchive(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	svc := services.NewAuthorMetadataRunService(db)
	seedCatalogBook(t, db, 11, "pilot.zip", "a.fb2")
	seedCatalogBook(t, db, 22, "pilot.zip", "b.fb2")
	seedCatalogBook(t, db, 33, "other.zip", "c.fb2")

	req := startRequest(models.AuthorMetadataRunPilotArchive)
	req.Archive = "pilot.zip"
	run, err := svc.StartRun(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, run.SelectorArchive)
	assert.Equal(t, "pilot.zip", *run.SelectorArchive)
	assert.Empty(t, run.SelectorBookIDs)
	assert.Equal(t, []int64{11, 22}, runItems(t, db, run.ID))
	finishActiveRun(t, db)
}

// Plan contract 3.9: smoke and pilot_archive each take exactly one selector
// of either kind. The selector the request named is the one stored; the
// items are the books it selects.
func TestStartRunAcceptsEitherSelectorForSmokeAndPilot(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	svc := services.NewAuthorMetadataRunService(db)
	seedCatalogBook(t, db, 11, "pilot.zip", "a.fb2")
	seedCatalogBook(t, db, 22, "pilot.zip", "b.fb2")
	seedCatalogBook(t, db, 33, "other.zip", "c.fb2")

	for _, mode := range []models.AuthorMetadataRunMode{models.AuthorMetadataRunSmoke, models.AuthorMetadataRunPilotArchive} {
		t.Run(string(mode)+" by archive", func(t *testing.T) {
			req := startRequest(mode)
			req.Archive = "pilot.zip"
			run, err := svc.StartRun(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, run.SelectorArchive)
			assert.Equal(t, "pilot.zip", *run.SelectorArchive)
			assert.Empty(t, run.SelectorBookIDs)
			assert.Equal(t, []int64{11, 22}, runItems(t, db, run.ID))
			finishActiveRun(t, db)
		})
		t.Run(string(mode)+" by book ids", func(t *testing.T) {
			req := startRequest(mode)
			req.BookIDs = []int64{33, 11, 999}
			run, err := svc.StartRun(ctx, req)
			require.NoError(t, err)
			assert.Nil(t, run.SelectorArchive)
			assert.Equal(t, []int64{11, 33}, run.SelectorBookIDs, "existing IDs, ascending")
			assert.Equal(t, []int64{11, 33}, runItems(t, db, run.ID))
			finishActiveRun(t, db)
		})
		t.Run(string(mode)+" with both selectors or none", func(t *testing.T) {
			req := startRequest(mode)
			req.BookIDs, req.Archive = []int64{11}, "pilot.zip"
			_, err := svc.StartRun(ctx, req)
			require.ErrorIs(t, err, database.ErrInvalidRunSelector)
			_, err = svc.StartRun(ctx, startRequest(mode))
			require.ErrorIs(t, err, database.ErrInvalidRunSelector)
			req = startRequest(mode)
			req.Archive = " "
			_, err = svc.StartRun(ctx, req)
			require.ErrorIs(t, err, database.ErrInvalidRunSelector, "a blank archive")
		})
		t.Run(string(mode)+" selecting no catalog book", func(t *testing.T) {
			req := startRequest(mode)
			req.Archive = "absent.zip"
			_, err := svc.StartRun(ctx, req)
			require.ErrorIs(t, err, database.ErrInvalidRunSelector)
			req = startRequest(mode)
			req.BookIDs = []int64{999}
			_, err = svc.StartRun(ctx, req)
			require.ErrorIs(t, err, database.ErrInvalidRunSelector)
		})
	}
	var runs int
	_, err := db.QueryOne(pg.Scan(&runs), `SELECT count(*) FROM author_metadata_run WHERE status <> 'completed'`)
	require.NoError(t, err)
	assert.Zero(t, runs, "every refused start seeded nothing")
}

// RED 12: a full run is refused until a pilot_archive run of the same versions
// completed and was explicitly approved.
func TestStartRunFullRequiresApprovedPilot(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	svc := services.NewAuthorMetadataRunService(db)
	seedCatalogBook(t, db, 11, "a.zip", "a.fb2")
	seedCatalogBook(t, db, 22, "b.zip", "b.fb2")

	approve := func(extractor, normalizer string) {
		t.Helper()
		var pilot int64
		_, err := db.QueryOne(pg.Scan(&pilot), `
			INSERT INTO author_metadata_run
				(mode, status, extractor_version, normalizer_version, selector_archive,
				 extraction_completed_at, finished_at)
			VALUES ('pilot_archive', 'completed', ?, ?, 'pilot.zip', now(), now())
			RETURNING id`, extractor, normalizer)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO author_metadata_pilot_approval
			(run_id, extractor_version, normalizer_version, approved_by_user_id)
			VALUES (?, ?, ?, 1)`, pilot, extractor, normalizer)
		require.NoError(t, err)
	}

	t.Run("no approval", func(t *testing.T) {
		_, err := svc.StartRun(ctx, startRequest(models.AuthorMetadataRunFull))
		require.ErrorIs(t, err, services.ErrFullRunNotApproved)
	})

	t.Run("approval of other versions does not count", func(t *testing.T) {
		approve("extractor-v0", runServiceNormalizer)
		approve(runServiceExtractor, "normalizer-v0")
		_, err := svc.StartRun(ctx, startRequest(models.AuthorMetadataRunFull))
		require.ErrorIs(t, err, services.ErrFullRunNotApproved)
	})

	t.Run("approved pilot of the same versions seeds the whole catalog", func(t *testing.T) {
		approve(runServiceExtractor, runServiceNormalizer)
		run, err := svc.StartRun(ctx, startRequest(models.AuthorMetadataRunFull))
		require.NoError(t, err)
		assert.Equal(t, models.AuthorMetadataRunFull, run.Mode)
		assert.Empty(t, run.SelectorBookIDs)
		assert.Nil(t, run.SelectorArchive)
		assert.Equal(t, []int64{11, 22}, runItems(t, db, run.ID))
	})

	t.Run("a full run with a selector is a client error", func(t *testing.T) {
		finishActiveRun(t, db)
		req := startRequest(models.AuthorMetadataRunFull)
		req.BookIDs = []int64{11}
		_, err := svc.StartRun(ctx, req)
		require.ErrorIs(t, err, database.ErrInvalidRunSelector)
	})
}

// RED (state machine, 3.9): pause and resume are guarded; a repeated
// transition or a transition of a terminal run is a conflict, and claims stop
// while paused (the claim layer reads the run status, proven here end to end).
func TestRunServicePauseResume(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	svc := services.NewAuthorMetadataRunService(db)
	seedCatalogBook(t, db, 11, "smoke.zip", "b.fb2")

	req := startRequest(models.AuthorMetadataRunSmoke)
	req.BookIDs = []int64{11}
	run, err := svc.StartRun(ctx, req)
	require.NoError(t, err)

	require.ErrorIs(t, svc.ResumeRun(ctx, run.ID), database.ErrRunTransitionConflict,
		"resuming a running run is a conflict")

	require.NoError(t, svc.PauseRun(ctx, run.ID))
	require.ErrorIs(t, svc.PauseRun(ctx, run.ID), database.ErrRunTransitionConflict,
		"repeating pause is a conflict, not a silent success")

	claims, err := database.ClaimExtractionItems(ctx, db, database.NewLeaseOwner(), database.LeaseClaimOptions{
		Limit: 1, Lease: time.Minute, MaxAttempts: 3,
	})
	require.NoError(t, err)
	assert.Empty(t, claims, "a paused run gives no claims")

	require.NoError(t, svc.ResumeRun(ctx, run.ID))
	claims, err = database.ClaimExtractionItems(ctx, db, database.NewLeaseOwner(), database.LeaseClaimOptions{
		Limit: 1, Lease: time.Minute, MaxAttempts: 3,
	})
	require.NoError(t, err)
	assert.Len(t, claims, 1, "resume makes the items claimable again")

	finishActiveRun(t, db)
	require.ErrorIs(t, svc.PauseRun(ctx, run.ID), database.ErrRunTransitionConflict,
		"a terminal run cannot transition")
}
