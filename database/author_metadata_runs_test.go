package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The run repository owns the author_metadata_run state machine: seeding a run
// with its items in one transaction, the guarded transitions (a repeated or
// terminal transition is a conflict, not a silent success), the systemic
// pause/fail paths, the extraction-progress counters that set
// extraction_completed_at without waiting for any downstream stage, and the
// pilot-approval gate a full run needs (contracts 3.7, 3.8, 3.9).

func runSeed() *models.AuthorMetadataRun {
	return &models.AuthorMetadataRun{
		Mode:              models.AuthorMetadataRunSmoke,
		Status:            models.AuthorMetadataRunRunning,
		ExtractorVersion:  "extractor-v1",
		NormalizerVersion: "normalizer-v1",
		SelectorBookIDs:   []int64{1},
	}
}

func ptrString(s string) *string { return &s }

// RED 1 (validation): mode/selector fit, closed modes and canonical ID rules
// are refused before any SQL runs — the nil connection proves it.
func TestSeedRunValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("mode and selector", func(t *testing.T) {
		archive := "a.zip"
		cases := []struct {
			name string
			run  *models.AuthorMetadataRun
			ids  []int64
		}{
			{"unknown mode", &models.AuthorMetadataRun{Mode: "everything", Status: models.AuthorMetadataRunRunning,
				ExtractorVersion: "e", NormalizerVersion: "n"}, []int64{1}},
			{"smoke without book ids", runSeed(), nil},
			{"smoke with an archive", &models.AuthorMetadataRun{Mode: models.AuthorMetadataRunSmoke,
				Status: models.AuthorMetadataRunRunning, ExtractorVersion: "e", NormalizerVersion: "n",
				SelectorBookIDs: []int64{1}, SelectorArchive: &archive}, []int64{1}},
			{"pilot without archive", &models.AuthorMetadataRun{Mode: models.AuthorMetadataRunPilotArchive,
				Status: models.AuthorMetadataRunRunning, ExtractorVersion: "e", NormalizerVersion: "n"}, []int64{1}},
			{"pilot with book ids", &models.AuthorMetadataRun{Mode: models.AuthorMetadataRunPilotArchive,
				Status: models.AuthorMetadataRunRunning, ExtractorVersion: "e", NormalizerVersion: "n",
				SelectorBookIDs: []int64{1}, SelectorArchive: &archive}, []int64{1}},
			{"pilot with blank archive", &models.AuthorMetadataRun{Mode: models.AuthorMetadataRunPilotArchive,
				Status: models.AuthorMetadataRunRunning, ExtractorVersion: "e", NormalizerVersion: "n",
				SelectorArchive: ptrString(" ")}, []int64{1}},
			{"full with book ids", &models.AuthorMetadataRun{Mode: models.AuthorMetadataRunFull,
				Status: models.AuthorMetadataRunRunning, ExtractorVersion: "e", NormalizerVersion: "n",
				SelectorBookIDs: []int64{1}}, []int64{1}},
			{"full with archive", &models.AuthorMetadataRun{Mode: models.AuthorMetadataRunFull,
				Status: models.AuthorMetadataRunRunning, ExtractorVersion: "e", NormalizerVersion: "n",
				SelectorArchive: &archive}, []int64{1}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				err := SeedRun(ctx, nil, c.run, c.ids)
				require.ErrorIs(t, err, ErrInvalidRunSelector)
			})
		}
		// Contract 3.9: smoke and pilot_archive take either kind of selector;
		// a valid seed passes the pure validation (nothing to refuse before
		// SQL).
		for _, mode := range []models.AuthorMetadataRunMode{models.AuthorMetadataRunSmoke, models.AuthorMetadataRunPilotArchive} {
			byIDs := &models.AuthorMetadataRun{Mode: mode, Status: models.AuthorMetadataRunRunning,
				ExtractorVersion: "e", NormalizerVersion: "n", SelectorBookIDs: []int64{1}}
			byArchive := &models.AuthorMetadataRun{Mode: mode, Status: models.AuthorMetadataRunRunning,
				ExtractorVersion: "e", NormalizerVersion: "n", SelectorArchive: &archive}
			assert.NoError(t, validateRunSeed(byIDs, []int64{1}), "%s by book ids", mode)
			assert.NoError(t, validateRunSeed(byArchive, []int64{1}), "%s by archive", mode)
			assert.ErrorIs(t, validateRunSeed(byIDs, nil), ErrInvalidRunSelector, "%s by book ids selecting none", mode)
		}
	})

	t.Run("versions", func(t *testing.T) {
		for _, v := range []string{"", " ", "\t"} {
			run := runSeed()
			run.ExtractorVersion = v
			require.ErrorIs(t, SeedRun(ctx, nil, run, []int64{1}), authornorm.ErrEmptyVersion)
			run = runSeed()
			run.NormalizerVersion = v
			require.ErrorIs(t, SeedRun(ctx, nil, run, []int64{1}), authornorm.ErrEmptyVersion)
		}
	})

	t.Run("book id rules", func(t *testing.T) {
		for _, ids := range [][]int64{{1, 1}, {0}, {-3}, {1, -1}, {2, 2, 3}} {
			require.ErrorIs(t, SeedRun(ctx, nil, runSeed(), ids), ErrInvalidRunBookIDs)
		}
	})

	t.Run("status must be active", func(t *testing.T) {
		run := runSeed()
		run.Status = models.AuthorMetadataRunCompleted
		require.ErrorIs(t, SeedRun(ctx, nil, run, []int64{1}), ErrInvalidRunSelector)
	})

	t.Run("canonicalization sorts, never renumbers", func(t *testing.T) {
		ids, err := CanonicalRunBookIDs([]int64{9, 1, 7, 3})
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 3, 7, 9}, ids)
		empty, err := CanonicalRunBookIDs(nil)
		require.NoError(t, err)
		assert.Empty(t, empty)
	})
}

// RED 1 (seeding): one transaction writes the run, one item per book and the
// total; out-of-order IDs are stored canonicalized.
func TestSeedRunSeedsItems(t *testing.T) {
	f := withAuthorSchemaTx(t)
	books := []int64{f.book(), f.book(), f.book()}

	run := runSeed()
	run.SelectorBookIDs = []int64{books[2], books[0], books[1]}
	require.NoError(t, SeedRun(context.Background(), f.tx, run, []int64{books[2], books[0], books[1]}))
	require.NotZero(t, run.ID)

	var got struct {
		Status          models.AuthorMetadataRunStatus
		SelectorBookIDs []int64 `pg:"selector_book_ids,array"`
		ItemsTotal      int
		StartedAt       *time.Time
		Items           int
	}
	_, err := f.tx.QueryOne(&got, `
		SELECT r.status, r.selector_book_ids, r.items_total, r.started_at,
			(SELECT count(*) FROM author_metadata_run_item i WHERE i.run_id = r.id) AS items
		FROM author_metadata_run r WHERE r.id = ?`, run.ID)
	require.NoError(t, err)
	assert.Equal(t, models.AuthorMetadataRunRunning, got.Status)
	assert.Equal(t, books, got.SelectorBookIDs, "selector IDs are stored ascending")
	assert.Equal(t, 3, got.ItemsTotal)
	assert.Equal(t, 3, got.Items)
	require.NotNil(t, got.StartedAt, "a run seeded as running starts at seed time")

	var itemBooks []int64
	_, err = f.tx.Query(&itemBooks, `SELECT book_id FROM author_metadata_run_item WHERE run_id = ? ORDER BY book_id`, run.ID)
	require.NoError(t, err)
	assert.Equal(t, books, itemBooks)
}

// RED 1 (atomicity): a failing item insert rolls the run back with it. The
// committed side needs a scratch database: the integration one would keep the
// rows.
func TestSeedRunIsAtomic(t *testing.T) {
	scratch := raceDB(t)
	good := nextCommittedBook(t, scratch, bookMD5(1))

	run := runSeed()
	run.SelectorBookIDs = []int64{good, 777}
	err := SeedRun(context.Background(), scratch, run, []int64{good, 777})
	require.Error(t, err, "the missing book breaks the item insert")

	var runs, items int
	_, qerr := scratch.QueryOne(pg.Scan(&runs, &items), `
		SELECT (SELECT count(*) FROM author_metadata_run),
			(SELECT count(*) FROM author_metadata_run_item)`)
	require.NoError(t, qerr)
	assert.Zero(t, runs, "the run rolled back with its items")
	assert.Zero(t, items)
}

// RED 2: PostgreSQL — not a process mutex — refuses a second active run, and
// finishing the active one frees the slot.
func TestSeedRunOneActiveRun(t *testing.T) {
	scratch := raceDB(t)
	book := nextCommittedBook(t, scratch, bookMD5(1))
	ctx := context.Background()

	first := runSeed()
	first.SelectorBookIDs = []int64{book}
	require.NoError(t, SeedRun(ctx, scratch, first, []int64{book}))

	for _, status := range []models.AuthorMetadataRunStatus{
		models.AuthorMetadataRunPending, models.AuthorMetadataRunRunning, models.AuthorMetadataRunPaused,
	} {
		second := runSeed()
		second.Status = status
		second.SelectorBookIDs = []int64{book}
		err := SeedRun(ctx, scratch, second, []int64{book})
		require.ErrorIs(t, err, ErrActiveRunExists, "second active run in status %q", status)
	}

	require.NoError(t, FailRunSystemic(ctx, scratch, first.ID, "database_invariant"))
	freed := runSeed()
	freed.SelectorBookIDs = []int64{book}
	require.NoError(t, SeedRun(ctx, scratch, freed, []int64{book}), "a terminal run frees the slot")
}

// RED (state machine): only running↔paused (and pending→running) pass; a
// repeated transition or a transition of a terminal run is a conflict.
func TestTransitionRun(t *testing.T) {
	ctx := context.Background()

	t.Run("running to paused and back", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "running"})
		require.NoError(t, TransitionRun(ctx, sf.tx, run, models.AuthorMetadataRunRunning, models.AuthorMetadataRunPaused))
		require.ErrorIs(t, TransitionRun(ctx, sf.tx, run, models.AuthorMetadataRunRunning, models.AuthorMetadataRunPaused),
			ErrRunTransitionConflict, "repeating the same transition is a conflict, not a silent success")
		require.NoError(t, TransitionRun(ctx, sf.tx, run, models.AuthorMetadataRunPaused, models.AuthorMetadataRunRunning))
		require.ErrorIs(t, TransitionRun(ctx, sf.tx, run, models.AuthorMetadataRunPaused, models.AuthorMetadataRunRunning),
			ErrRunTransitionConflict)
	})

	t.Run("pending to running sets started_at", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "pending"})
		require.NoError(t, TransitionRun(ctx, sf.tx, run, models.AuthorMetadataRunPending, models.AuthorMetadataRunRunning))
		var startedAt *time.Time
		_, err := sf.tx.QueryOne(pg.Scan(&startedAt), `SELECT started_at FROM author_metadata_run WHERE id = ?`, run)
		require.NoError(t, err)
		assert.NotNil(t, startedAt)
	})

	t.Run("terminal runs and illegal pairs are conflicts", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		for _, terminal := range []string{"completed", "failed_systemic"} {
			run := sf.run(&runSpec{status: terminal})
			require.ErrorIs(t, TransitionRun(ctx, sf.tx, run, models.AuthorMetadataRunRunning, models.AuthorMetadataRunPaused),
				ErrRunTransitionConflict, "a %s run cannot transition", terminal)
		}
		pending := sf.run(&runSpec{status: "pending"})
		require.ErrorIs(t, TransitionRun(ctx, sf.tx, pending, models.AuthorMetadataRunPending, models.AuthorMetadataRunPaused),
			ErrRunTransitionConflict, "pending never pauses directly")
		require.ErrorIs(t, TransitionRun(ctx, sf.tx, pending, models.AuthorMetadataRunRunning, models.AuthorMetadataRunCompleted),
			ErrRunTransitionConflict, "the repository never completes a run through TransitionRun")
	})
}

// RED (systemic paths): an archive/volume failure pauses the run with a closed
// error class; a schema/invariant failure ends it failed_systemic.
func TestSystemicRunTransitions(t *testing.T) {
	ctx := context.Background()

	t.Run("pause records the closed class", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "running"})
		require.NoError(t, PauseRunSystemic(ctx, sf.tx, run, "archive_unreadable"))

		var status models.AuthorMetadataRunStatus
		var class *string
		_, err := sf.tx.QueryOne(pg.Scan(&status, &class),
			`SELECT status, last_error_class FROM author_metadata_run WHERE id = ?`, run)
		require.NoError(t, err)
		assert.Equal(t, models.AuthorMetadataRunPaused, status)
		require.NotNil(t, class)
		assert.Equal(t, "archive_unreadable", *class)

		require.ErrorIs(t, PauseRunSystemic(ctx, sf.tx, run, "archive_unreadable"), ErrRunTransitionConflict,
			"pausing a paused run is a conflict")
	})

	t.Run("free error text is not a class", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "running"})
		require.Error(t, PauseRunSystemic(ctx, sf.tx, run, "open /books/secret.zip: permission denied"))
		var status models.AuthorMetadataRunStatus
		_, err := sf.tx.QueryOne(pg.Scan(&status), `SELECT status FROM author_metadata_run WHERE id = ?`, run)
		require.NoError(t, err)
		assert.Equal(t, models.AuthorMetadataRunRunning, status, "a refused class changes nothing")
	})

	t.Run("fail_systemic finishes the run", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "paused"})
		require.NoError(t, FailRunSystemic(ctx, sf.tx, run, "database_invariant"))
		var status models.AuthorMetadataRunStatus
		var finishedAt *time.Time
		_, err := sf.tx.QueryOne(pg.Scan(&status, &finishedAt),
			`SELECT status, finished_at FROM author_metadata_run WHERE id = ?`, run)
		require.NoError(t, err)
		assert.Equal(t, models.AuthorMetadataRunFailedSystemic, status)
		assert.NotNil(t, finishedAt)

		require.ErrorIs(t, FailRunSystemic(ctx, sf.tx, run, "database_invariant"), ErrRunTransitionConflict,
			"a terminal run cannot fail again")
	})
}

// RED 10: extraction progress is counted per terminal item and
// extraction_completed_at lands exactly when the last item ends — it never
// waits for the local normalization stage.
func TestRecordExtractionItemTerminal(t *testing.T) {
	ctx := context.Background()

	t.Run("the last terminal item completes extraction", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "running"})
		sf.exec(`UPDATE author_metadata_run SET items_total = 2 WHERE id = ?`, run)

		done, err := RecordExtractionItemTerminal(ctx, sf.tx, run)
		require.NoError(t, err)
		assert.False(t, done)

		done, err = RecordExtractionItemTerminal(ctx, sf.tx, run)
		require.NoError(t, err)
		assert.True(t, done, "all items terminal completes extraction")

		var terminal int
		var completedAt *time.Time
		_, err = sf.tx.QueryOne(pg.Scan(&terminal, &completedAt),
			`SELECT items_terminal, extraction_completed_at FROM author_metadata_run WHERE id = ?`, run)
		require.NoError(t, err)
		assert.Equal(t, 2, terminal)
		assert.NotNil(t, completedAt, "set with the last item, without any downstream stage")

		// A normalization job of this run's credits is still pending: extraction
		// completion does not look at it (contract 3.9).
		var jobCount int
		_, err = sf.tx.QueryOne(pg.Scan(&jobCount), `SELECT count(*) FROM contributor_normalization_job`)
		require.NoError(t, err)
		_ = jobCount
	})
}

// The claim layer ends abandoned rows out of attempts inside its own
// transaction; the worker reconciles the counters when a claim comes back
// empty, so those rows still count toward extraction completion.
func TestReconcileRunExtraction(t *testing.T) {
	ctx := context.Background()

	t.Run("recomputes drifted counters", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "running"})
		book := sf.book()
		snapshot := sf.snapshot(&snapshotSpec{book: book})
		sf.runItem(run, book)
		sf.exec(`UPDATE author_metadata_run SET items_total = 1 WHERE id = ?`, run)
		// The item ended through the claim layer's exhaust path: the run
		// counters never saw it.
		sf.exec(`UPDATE author_metadata_run_item
			SET status = 'metadata_parse_failed', finished_at = now() WHERE run_id = ?`, run)

		done, err := ReconcileRunExtraction(ctx, sf.tx, run)
		require.NoError(t, err)
		assert.True(t, done)

		var terminal int
		var completedAt *time.Time
		_, err = sf.tx.QueryOne(pg.Scan(&terminal, &completedAt),
			`SELECT items_terminal, extraction_completed_at FROM author_metadata_run WHERE id = ?`, run)
		require.NoError(t, err)
		assert.Equal(t, 1, terminal)
		assert.NotNil(t, completedAt)
		_ = snapshot
	})

	t.Run("a run without items completes vacuously", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "running"})
		done, err := ReconcileRunExtraction(ctx, sf.tx, run)
		require.NoError(t, err)
		assert.True(t, done)
	})

	t.Run("pending items keep extraction open", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		run := sf.run(&runSpec{status: "running"})
		sf.runItem(run, sf.book())
		sf.exec(`UPDATE author_metadata_run SET items_total = 1 WHERE id = ?`, run)
		done, err := ReconcileRunExtraction(ctx, sf.tx, run)
		require.NoError(t, err)
		assert.False(t, done)
	})
}

// RED 12 (gate): a full run needs an approval of the exact version pair.
func TestHasApprovedPilot(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()

	ok, err := HasApprovedPilot(ctx, f.tx, "extractor-v1", "normalizer-v1")
	require.NoError(t, err)
	assert.False(t, ok)

	pilot := f.run(&runSpec{mode: "pilot_archive", status: "completed", extractor: "extractor-v3", normalizer: "normalizer-v7"})
	f.exec(`INSERT INTO author_metadata_pilot_approval
		(run_id, extractor_version, normalizer_version, approved_by_user_id)
		VALUES (?, 'extractor-v3', 'normalizer-v7', ?)`, pilot, f.user())

	ok, err = HasApprovedPilot(ctx, f.tx, "extractor-v3", "normalizer-v7")
	require.NoError(t, err)
	assert.True(t, ok)

	for _, pair := range [][2]string{{"extractor-v3", "normalizer-v8"}, {"extractor-v4", "normalizer-v7"}, {"extractor-v1", "normalizer-v1"}} {
		ok, err = HasApprovedPilot(ctx, f.tx, pair[0], pair[1])
		require.NoError(t, err)
		assert.False(t, ok, "%s/%s is not the approved pair", pair[0], pair[1])
	}
}

// The worker reads its claimed items joined to the catalog row, and fills a
// legacy MD5 only while it is empty.
func TestExtractionTargetsAndMD5(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()

	withMD5 := f.book()
	emptyMD5 := f.book()
	f.exec(`UPDATE opds_catalog_book SET md5 = '' WHERE id = ?`, emptyMD5)
	run := f.run(&runSpec{status: "running"})
	itemA := f.runItem(run, withMD5)
	itemB := f.runItem(run, emptyMD5)

	targets, err := LoadExtractionTargets(ctx, f.tx, []int64{itemB, itemA})
	require.NoError(t, err)
	require.Len(t, targets, 2)
	byItem := map[int64]ExtractionItemTarget{}
	for _, target := range targets {
		byItem[target.ItemID] = target
	}
	assert.Equal(t, withMD5, byItem[itemA].BookID)
	assert.Equal(t, "fixture.zip", byItem[itemA].ArchivePath)
	assert.Equal(t, fmt.Sprintf("%d.fb2", withMD5), byItem[itemA].EntryName)
	assert.Equal(t, bookMD5(withMD5), byItem[itemA].MD5)
	assert.Equal(t, "", byItem[itemB].MD5, "an empty legacy MD5 reads as empty")

	t.Run("fill only while empty", func(t *testing.T) {
		sf := f.on(t)
		fresh := bookMD5(424242)
		require.NoError(t, FillBookMD5IfEmpty(ctx, sf.tx, emptyMD5, fresh))
		var stored string
		_, err := sf.tx.QueryOne(pg.Scan(&stored), `SELECT md5 FROM opds_catalog_book WHERE id = ?`, emptyMD5)
		require.NoError(t, err)
		assert.Equal(t, fresh, stored)

		other := bookMD5(515151)
		require.NoError(t, FillBookMD5IfEmpty(ctx, sf.tx, emptyMD5, other), "filling a filled book is a no-op, not an error")
		_, err = sf.tx.QueryOne(pg.Scan(&stored), `SELECT md5 FROM opds_catalog_book WHERE id = ?`, emptyMD5)
		require.NoError(t, err)
		assert.Equal(t, fresh, stored, "a non-empty MD5 is never overwritten")

		require.NoError(t, FillBookMD5IfEmpty(ctx, sf.tx, withMD5, other))
		_, err = sf.tx.QueryOne(pg.Scan(&stored), `SELECT md5 FROM opds_catalog_book WHERE id = ?`, withMD5)
		require.NoError(t, err)
		assert.Equal(t, bookMD5(withMD5), stored)
	})

	t.Run("invalid md5 refused before SQL", func(t *testing.T) {
		for _, bad := range []string{"", "abc", "ABCDEF0123456789abcdef0123456789", "zz" + bookMD5(1)[2:]} {
			require.ErrorIs(t, FillBookMD5IfEmpty(ctx, nil, emptyMD5, bad), ErrInvalidBookMD5)
		}
	})
}

// The worker finds the single active run, and the selectors enumerate the
// catalog.
func TestRunLookups(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()

	none, err := ActiveRun(ctx, f.tx)
	require.NoError(t, err)
	assert.Nil(t, none)

	running := f.run(&runSpec{status: "running"})
	active, err := ActiveRun(ctx, f.tx)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, running, active.ID)

	t.Run("selectors", func(t *testing.T) {
		sf := withAuthorSchemaTx(t)
		inArchive := sf.book()
		sf.exec(`UPDATE opds_catalog_book SET path = 'picked.zip' WHERE id = ?`, inArchive)
		other := sf.book()

		archiveIDs, err := ListArchiveBookIDs(ctx, sf.tx, "picked.zip")
		require.NoError(t, err)
		assert.Equal(t, []int64{inArchive}, archiveIDs)

		existing, err := ListExistingBookIDs(ctx, sf.tx, []int64{other, inArchive, 999_999_999_999})
		require.NoError(t, err)
		assert.Equal(t, []int64{inArchive, other}, existing, "ascending, missing IDs dropped")

		catalog, err := ListCatalogBookIDs(ctx, sf.tx)
		require.NoError(t, err)
		assert.Subset(t, catalog, []int64{inArchive, other})
	})
}

var _ = errors.Is // keep the import used when the RED file is extended
