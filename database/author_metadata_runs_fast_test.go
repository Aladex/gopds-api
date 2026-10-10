package database

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A full run starts without its items and is seeded in bounded batches by
// its own loop; item counts by status come from a tally the item triggers
// maintain; a run's broken archives are listed, retried and deleted per
// archive; and the credit-scale figures are stored with their time.

func fullSeedingRun() *models.AuthorMetadataRun {
	return &models.AuthorMetadataRun{
		Mode:              models.AuthorMetadataRunFull,
		ExtractorVersion:  "extractor-v1",
		NormalizerVersion: "normalizer-v1",
	}
}

// archiveBook adds a committed book in the named archive.
func archiveBook(t *testing.T, scratch *pg.DB, archive string) int64 {
	t.Helper()
	id := raceBookNext.Add(1)
	_, err := scratch.Exec(`INSERT INTO opds_catalog_book
		(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES (?, ?, ?, 'fb2', now(), '', 'ru', 'fast fixture', '', ?)`,
		id, fmt.Sprintf("%d.fb2", id), archive, bookMD5(id))
	require.NoError(t, err)
	return id
}

type seededRun struct {
	Status     models.AuthorMetadataRunStatus
	ItemsTotal int
	SeedCursor *int64
	SeedTarget *int
	StartedAt  *time.Time
	Items      int
	Distinct   int
}

func readSeededRun(t *testing.T, db pg.DBI, runID int64) seededRun {
	t.Helper()
	var got seededRun
	_, err := db.QueryOne(&got, `
		SELECT r.status, r.items_total, r.seed_cursor, r.seed_target, r.started_at,
			(SELECT count(*) FROM author_metadata_run_item i WHERE i.run_id = r.id) AS items,
			(SELECT count(DISTINCT book_id) FROM author_metadata_run_item i WHERE i.run_id = r.id) AS distinct
		FROM author_metadata_run r WHERE r.id = ?`, runID)
	require.NoError(t, err)
	return got
}

// A full run is created pending with no item, holding the active slot from
// the start: a second start, seeding or not, is refused by PostgreSQL.
func TestCreateSeedingRunHoldsTheSlot(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	for range 3 {
		archiveBook(t, scratch, "a.zip")
	}

	run := fullSeedingRun()
	require.NoError(t, CreateSeedingRun(ctx, scratch, run))
	require.NotZero(t, run.ID)
	got := readSeededRun(t, scratch, run.ID)
	assert.Equal(t, models.AuthorMetadataRunPending, got.Status)
	assert.Zero(t, got.Items, "the start inserts no item")
	require.NotNil(t, got.SeedCursor)
	assert.Zero(t, *got.SeedCursor)
	require.NotNil(t, got.SeedTarget)
	assert.Equal(t, 3, *got.SeedTarget, "the target is the catalog at the start")
	assert.Nil(t, got.StartedAt, "a run that is still being seeded has not started")

	require.ErrorIs(t, CreateSeedingRun(ctx, scratch, fullSeedingRun()), ErrActiveRunExists,
		"a second full run is refused while the first one is being seeded")
	smoke := runSeed()
	smoke.SelectorBookIDs = []int64{raceBookNext.Load()}
	require.ErrorIs(t, SeedRun(ctx, scratch, smoke, smoke.SelectorBookIDs), ErrActiveRunExists)
}

// Concurrent starts of a full run: exactly one wins the slot.
func TestCreateSeedingRunConcurrentStarts(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	archiveBook(t, scratch, "a.zip")

	const starters = 8
	var wg sync.WaitGroup
	errs := make([]error, starters)
	for i := range starters {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = CreateSeedingRun(ctx, scratch, fullSeedingRun())
		}(i)
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
			continue
		}
		require.ErrorIs(t, err, ErrActiveRunExists)
	}
	assert.Equal(t, 1, won)
	var runs int
	_, err := scratch.QueryOne(pg.Scan(&runs), `SELECT count(*) FROM author_metadata_run`)
	require.NoError(t, err)
	assert.Equal(t, 1, runs)
}

// Seeding goes in bounded batches, each moving the cursor with its items, and
// the last batch starts the run.
func TestSeedRunBatchSeedsInBatchesAndStarts(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	var books []int64
	for range 5 {
		books = append(books, archiveBook(t, scratch, "a.zip"))
	}
	run := fullSeedingRun()
	require.NoError(t, CreateSeedingRun(ctx, scratch, run))

	var steps [][2]int
	for range 10 {
		n, done, err := SeedRunBatch(ctx, scratch, run.ID, 2)
		require.NoError(t, err)
		steps = append(steps, [2]int{n, boolInt(done)})
		if done {
			break
		}
	}
	assert.Equal(t, [][2]int{{2, 0}, {2, 0}, {1, 1}}, steps)

	got := readSeededRun(t, scratch, run.ID)
	assert.Equal(t, models.AuthorMetadataRunRunning, got.Status, "the last batch starts the run")
	assert.NotNil(t, got.StartedAt)
	assert.Nil(t, got.SeedCursor, "a seeded run has no cursor")
	assert.Equal(t, 5, got.ItemsTotal)
	assert.Equal(t, 5, got.Items)
	assert.Equal(t, 5, got.Distinct)

	var itemBooks []int64
	_, err := scratch.Query(&itemBooks, `SELECT book_id FROM author_metadata_run_item WHERE run_id = ? ORDER BY book_id`, run.ID)
	require.NoError(t, err)
	assert.Equal(t, books, itemBooks)

	_, _, err = SeedRunBatch(ctx, scratch, run.ID, 2)
	require.ErrorIs(t, err, ErrRunTransitionConflict, "a started run has nothing left to seed")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// A batch that died before its commit left nothing behind, and two seeders at
// once (a restart overlapping the old process, a second pod) never seed a
// book twice: every book has exactly one item and the total is exact.
func TestSeedRunBatchIsRestartSafe(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	const books = 23
	for range books {
		archiveBook(t, scratch, "a.zip")
	}
	run := fullSeedingRun()
	require.NoError(t, CreateSeedingRun(ctx, scratch, run))

	// The process dies inside its first batch: the transaction rolls back.
	tx, err := scratch.Begin()
	require.NoError(t, err)
	n, _, err := SeedRunBatch(ctx, tx, run.ID, 4)
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.NoError(t, tx.Rollback())
	got := readSeededRun(t, scratch, run.ID)
	assert.Zero(t, got.Items)
	require.NotNil(t, got.SeedCursor)
	assert.Zero(t, *got.SeedCursor, "the cursor rolled back with the items")

	// Two seeders race to the end.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				_, done, seedErr := SeedRunBatch(ctx, scratch, run.ID, 3)
				if seedErr != nil {
					if seedErr != ErrRunTransitionConflict {
						errs[i] = seedErr
					}
					return
				}
				if done {
					return
				}
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	got = readSeededRun(t, scratch, run.ID)
	assert.Equal(t, models.AuthorMetadataRunRunning, got.Status)
	assert.Equal(t, books, got.Items)
	assert.Equal(t, books, got.Distinct, "no book is seeded twice")
	assert.Equal(t, books, got.ItemsTotal, "the total counts every book once")

	counts, err := RunItemCounts(ctx, scratch, run.ID)
	require.NoError(t, err)
	assert.Equal(t, map[models.AuthorMetadataRunItemStatus]int64{models.AuthorMetadataRunItemPending: books}, counts)
}

// exactCounts counts the items themselves.
func exactCounts(t *testing.T, db pg.DBI, runID int64) map[models.AuthorMetadataRunItemStatus]int64 {
	t.Helper()
	var rows []struct {
		Status models.AuthorMetadataRunItemStatus
		N      int64
	}
	_, err := db.Query(&rows, `SELECT status, count(*) AS n FROM author_metadata_run_item WHERE run_id = ? GROUP BY 1`, runID)
	require.NoError(t, err)
	out := map[models.AuthorMetadataRunItemStatus]int64{}
	for _, r := range rows {
		out[r.Status] = r.N
	}
	return out
}

// The tally follows every path that changes an item — seeding, a claim, a
// completion, a failure to the end of its attempts, a retry, deleting books
// with their layer — and folding it changes no sum.
func TestRunItemTallyFollowsTheItems(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	keep := []int64{archiveBook(t, scratch, "keep.zip"), archiveBook(t, scratch, "keep.zip"),
		archiveBook(t, scratch, "keep.zip")}
	gone := []int64{archiveBook(t, scratch, "gone.zip"), archiveBook(t, scratch, "gone.zip")}
	ids := append(append([]int64{}, keep...), gone...)
	run := runSeed()
	run.SelectorBookIDs = ids
	require.NoError(t, SeedRun(ctx, scratch, run, ids))

	check := func(step string) {
		t.Helper()
		got, err := RunItemCounts(ctx, scratch, run.ID)
		require.NoError(t, err)
		assert.Equal(t, exactCounts(t, scratch, run.ID), got, "after %s", step)
	}
	check("seeding")

	owner := NewLeaseOwner()
	claims, err := ClaimExtractionItems(ctx, scratch, owner, LeaseClaimOptions{Limit: 5, Lease: time.Minute, MaxAttempts: 1})
	require.NoError(t, err)
	require.Len(t, claims, 5)
	check("a claim")

	require.NoError(t, CompleteExtractionItem(ctx, scratch, claims[0].ID, owner, models.AuthorMetadataRunItemEntryMissing, nil))
	require.NoError(t, CompleteExtractionItem(ctx, scratch, claims[1].ID, owner, models.AuthorMetadataRunItemArchiveMissing, nil))
	exhausted, err := FailExtractionItem(ctx, scratch, claims[2].ID, owner, LeaseFailure{
		ErrorClass: "transient_database", RetryAfter: time.Millisecond, MaxAttempts: 1})
	require.NoError(t, err)
	require.True(t, exhausted)
	for range 3 {
		_, err = RecordExtractionItemTerminal(ctx, scratch, run.ID)
		require.NoError(t, err)
	}
	check("completion and exhaustion")

	reopened, err := ReopenRunRows(ctx, scratch, run.ID, RetryExtraction, "entry_missing", 3)
	require.NoError(t, err)
	require.EqualValues(t, 1, reopened)
	check("a retry")

	_, err = DeleteArchiveBooks(ctx, scratch, "gone.zip")
	require.NoError(t, err)
	var deleted int
	_, err = scratch.QueryOne(pg.Scan(&deleted), `SELECT count(*) FROM opds_catalog_book WHERE path = 'gone.zip'`)
	require.NoError(t, err)
	require.Zero(t, deleted, "the archive's books are gone")
	check("deleting books with their layer")

	var rowsBefore int
	_, err = scratch.QueryOne(pg.Scan(&rowsBefore), `SELECT count(*) FROM author_metadata_run_item_tally WHERE run_id = ?`, run.ID)
	require.NoError(t, err)
	require.NoError(t, CompactRunItemTally(ctx, scratch, run.ID))
	check("folding the tally")
	var rowsAfter int
	_, err = scratch.QueryOne(pg.Scan(&rowsAfter), `SELECT count(*) FROM author_metadata_run_item_tally WHERE run_id = ?`, run.ID)
	require.NoError(t, err)
	assert.Less(t, rowsAfter, rowsBefore, "folding leaves fewer rows")
	assert.LessOrEqual(t, rowsAfter, len(exactCounts(t, scratch, run.ID)), "one row per status at most")
}

// A run's broken archives: listed with their books and reason, retried per
// archive within the attempt budget (a completed run goes back to running),
// and deleted with the existing archive path only when the pass found them
// broken.
func TestRunProblemArchives(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	missing := []int64{archiveBook(t, scratch, "missing.zip"), archiveBook(t, scratch, "missing.zip")}
	broken := archiveBook(t, scratch, "broken.zip")
	good := archiveBook(t, scratch, "good.zip")
	ids := []int64{missing[0], missing[1], broken, good}
	run := runSeed()
	run.SelectorBookIDs = ids
	require.NoError(t, SeedRun(ctx, scratch, run, ids))

	owner := NewLeaseOwner()
	claims, err := ClaimExtractionItems(ctx, scratch, owner, LeaseClaimOptions{Limit: 4, Lease: time.Minute, MaxAttempts: 3})
	require.NoError(t, err)
	require.Len(t, claims, 4)
	targets, err := LoadExtractionTargets(ctx, scratch, []int64{claims[0].ID, claims[1].ID, claims[2].ID, claims[3].ID})
	require.NoError(t, err)
	for _, target := range targets {
		status := models.AuthorMetadataRunItemEntryMissing
		switch target.ArchivePath {
		case "missing.zip":
			status = models.AuthorMetadataRunItemArchiveMissing
		case "broken.zip":
			status = models.AuthorMetadataRunItemArchiveUnreadable
		}
		require.NoError(t, CompleteExtractionItem(ctx, scratch, target.ItemID, owner, status, nil))
		_, err = RecordExtractionItemTerminal(ctx, scratch, run.ID)
		require.NoError(t, err)
	}

	archives, err := RunProblemArchives(ctx, scratch, run.ID, 50)
	require.NoError(t, err)
	assert.Equal(t, []RunProblemArchive{
		{Archive: "missing.zip", Reason: models.AuthorMetadataRunItemArchiveMissing, Books: 2},
		{Archive: "broken.zip", Reason: models.AuthorMetadataRunItemArchiveUnreadable, Books: 1},
	}, archives, "most books first; a per-book failure is not an archive problem")

	failed, err := RunArchiveFailed(ctx, scratch, run.ID, "missing.zip")
	require.NoError(t, err)
	assert.True(t, failed)
	failed, err = RunArchiveFailed(ctx, scratch, run.ID, "good.zip")
	require.NoError(t, err)
	assert.False(t, failed, "an archive whose books only failed one by one is not a broken archive")

	// The run completes; retrying one archive reopens exactly its books and
	// takes the run back to running.
	_, err = scratch.Exec(`UPDATE author_metadata_run SET status = 'completed', finished_at = clock_timestamp() WHERE id = ?`, run.ID)
	require.NoError(t, err)
	reopened, err := ReopenRunArchive(ctx, scratch, run.ID, "broken.zip", 3)
	require.NoError(t, err)
	assert.EqualValues(t, 1, reopened)
	got := readSeededRun(t, scratch, run.ID)
	assert.Equal(t, models.AuthorMetadataRunRunning, got.Status)
	var status string
	_, err = scratch.QueryOne(pg.Scan(&status), `SELECT status FROM author_metadata_run_item WHERE run_id = ? AND book_id = ?`, run.ID, broken)
	require.NoError(t, err)
	assert.Equal(t, "pending", status)
	reopened, err = ReopenRunArchive(ctx, scratch, run.ID, "missing.zip", 1)
	require.NoError(t, err)
	assert.Zero(t, reopened, "an item that used the whole budget does not reopen")

	deleted, err := DeleteArchiveBooks(ctx, scratch, "missing.zip")
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)
	archives, err = RunProblemArchives(ctx, scratch, run.ID, 50)
	require.NoError(t, err)
	assert.Empty(t, archives)
	counts, err := RunItemCounts(ctx, scratch, run.ID)
	require.NoError(t, err)
	assert.Equal(t, exactCounts(t, scratch, run.ID), counts)
}

// The stored figures: computed once with the time they stand for, read back
// as they were, and never replaced by an older computation.
func TestRunAggregatesRoundTrip(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	book := archiveBook(t, scratch, "a.zip")
	run := runSeed()
	run.SelectorBookIDs = []int64{book}
	require.NoError(t, SeedRun(ctx, scratch, run, []int64{book}))

	loaded, err := LoadRunAggregates(ctx, scratch, run.ID)
	require.NoError(t, err)
	assert.Nil(t, loaded, "nothing is stored before the first computation")

	agg, computedAt, err := ComputeRunAggregates(ctx, scratch, run.ID)
	require.NoError(t, err)
	require.False(t, computedAt.IsZero())
	stats, err := AuthorMetadataStatsForRun(ctx, scratch, run.ID)
	require.NoError(t, err)
	assert.Equal(t, stats.Local, agg.Local)
	assert.Equal(t, stats.Review, agg.Review)
	assert.Equal(t, stats.Credits, agg.Credits)

	agg.Credits.Pending = 7
	require.NoError(t, StoreRunAggregates(ctx, scratch, run.ID, &agg, computedAt))
	older := agg
	older.Credits.Pending = 99
	require.NoError(t, StoreRunAggregates(ctx, scratch, run.ID, &older, computedAt.Add(-time.Minute)))

	loaded, err = LoadRunAggregates(ctx, scratch, run.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.GreaterOrEqual(t, loaded.AgeS, 0.0)
	assert.True(t, loaded.ComputedAt.Equal(computedAt))
	assert.EqualValues(t, 7, loaded.Figures.Credits.Pending, "an older computation never replaces a newer one")
	assert.NotNil(t, loaded.Figures.Credits.Unresolved)
}

// The scanning section's error list shows the books of a broken archive
// with the per-book status as their class, next to the other failures.
func TestScanFailuresListArchiveStatuses(t *testing.T) {
	storeDB = jobsDB(t)
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	run := f.run(&runSpec{status: "running"})
	missing, unreadable := f.book(), f.book()
	f.exec(`INSERT INTO author_metadata_run_item (run_id, book_id, status, attempt_count, finished_at)
		VALUES (?, ?, 'archive_missing', 1, now())`, run, missing)
	f.exec(`INSERT INTO author_metadata_run_item (run_id, book_id, status, attempt_count, finished_at)
		VALUES (?, ?, 'archive_unreadable', 1, now())`, run, unreadable)

	failures, err := AuthorMetadataScanFailures(context.Background(), f.tx, 10)
	require.NoError(t, err)
	classes := map[string]string{}
	for _, failure := range failures {
		classes[failure.Entry] = failure.Class
	}
	assert.Equal(t, map[string]string{
		fmt.Sprintf("%d.fb2", missing):    "archive_missing",
		fmt.Sprintf("%d.fb2", unreadable): "archive_unreadable",
	}, classes)
	assert.Contains(t, AuthorMetadataScanFailureClasses(), "archive_missing")
}

// Truncating the run items (an operator's reset, a test's) empties the tally
// with them: no count outlives the rows it counted.
func TestRunItemTallyFollowsATruncate(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	book := archiveBook(t, scratch, "a.zip")
	run := runSeed()
	run.SelectorBookIDs = []int64{book}
	require.NoError(t, SeedRun(ctx, scratch, run, []int64{book}))

	_, err := scratch.Exec(`TRUNCATE author_metadata_run_item_attempt, author_metadata_run_item`)
	require.NoError(t, err)
	counts, err := RunItemCounts(ctx, scratch, run.ID)
	require.NoError(t, err)
	assert.Empty(t, counts)
}

// subPlanRelations collects the relations scanned anywhere below a SubPlan of
// an EXPLAIN (FORMAT JSON) plan: the shape that runs once per outer row.
func subPlanRelations(node map[string]any, inSubPlan bool, out map[string]bool) {
	if rel, ok := node["Parent Relationship"].(string); ok && rel == "SubPlan" {
		inSubPlan = true
	}
	if name, ok := node["Relation Name"].(string); ok && inSubPlan {
		out[name] = true
	}
	if children, ok := node["Plans"].([]any); ok {
		for _, child := range children {
			if m, ok := child.(map[string]any); ok {
				subPlanRelations(m, inSubPlan, out)
			}
		}
	}
}

func explainSubPlanRelations(t *testing.T, db pg.DBI, query string, params ...any) map[string]bool {
	t.Helper()
	var raw string
	_, err := db.QueryOne(pg.Scan(&raw), "EXPLAIN (FORMAT JSON) "+query, params...)
	require.NoError(t, err)
	var plans []map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &plans))
	out := map[string]bool{}
	subPlanRelations(plans[0]["Plan"].(map[string]any), false, out)
	return out
}

// The reads over a run's credits — the accounting run completion waits for,
// and the local, review and coverage figures — reach the run's items and the
// pending local jobs through joins, never through a subquery the planner can
// run once per credit: at catalog size such a SubPlan that misses work_mem is
// quadratic (prod's run 3 never completed on it).
func TestRunCreditReadsPlanAsJoins(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	book := archiveBook(t, scratch, "a.zip")
	run := runSeed()
	run.SelectorBookIDs = []int64{book}
	require.NoError(t, SeedRun(ctx, scratch, run, []int64{book}))

	for name, query := range map[string]string{
		"run accounting":     accountingRunScope + accountingVerdictsSQL,
		"catalog accounting": accountingCatalogScope + accountingVerdictsSQL,
		"local figures":      runJobsCTE + ` SELECT count(*) FROM run_jobs`,
		"review figures":     runReviewCTE + ` SELECT count(*) FROM run_review`,
		"coverage":           coverageSQL,
	} {
		params := []any{run.ID}
		if name == "catalog accounting" {
			params = nil
		}
		rels := explainSubPlanRelations(t, scratch, query, params...)
		for _, table := range []string{"author_metadata_run_item", "contributor_normalization_job",
			"book_metadata_snapshot", "book_contributor_credit"} {
			assert.False(t, rels[table], "%s scans %s inside a SubPlan", name, table)
		}
	}
}

// Completing a run accounts its credits without holding the run row: an
// administrator's pause during a long accounting goes through at once, and
// the completion then sees the run changed and leaves it alone.
func TestCompleteRunIfSettledHoldsNoRunLockWhileAccounting(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	book := archiveBook(t, scratch, "a.zip")
	run := runSeed()
	run.SelectorBookIDs = []int64{book}
	require.NoError(t, SeedRun(ctx, scratch, run, []int64{book}))
	owner := NewLeaseOwner()
	claims, err := ClaimExtractionItems(ctx, scratch, owner, LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 3})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	require.NoError(t, CompleteExtractionItem(ctx, scratch, claims[0].ID, owner, models.AuthorMetadataRunItemEntryMissing, nil))
	extracted, err := RecordExtractionItemTerminal(ctx, scratch, run.ID)
	require.NoError(t, err)
	require.True(t, extracted)

	// A long accounting: the table it reads is held by another transaction.
	blocker, err := scratch.Begin()
	require.NoError(t, err)
	_, err = blocker.Exec(`LOCK TABLE book_contributor_credit_selection IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)

	type outcome struct {
		completed bool
		err       error
	}
	settled := make(chan outcome, 1)
	go func() {
		completed, settleErr := CompleteRunIfSettled(ctx, scratch, run.ID)
		settled <- outcome{completed, settleErr}
	}()
	require.Eventually(t, func() bool {
		var waiting int
		_, qerr := scratch.QueryOne(pg.Scan(&waiting), `SELECT count(*) FROM pg_locks l
			JOIN pg_class c ON c.oid = l.relation
			WHERE NOT l.granted AND c.relname = 'book_contributor_credit_selection'`)
		return qerr == nil && waiting > 0
	}, 10*time.Second, 20*time.Millisecond, "the accounting is waiting")

	pauseCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	require.NoError(t, TransitionRun(pauseCtx, scratch, run.ID, models.AuthorMetadataRunRunning, models.AuthorMetadataRunPaused),
		"a pause does not wait for the accounting")

	require.NoError(t, blocker.Rollback())
	got := <-settled
	require.NoError(t, got.err)
	assert.False(t, got.completed, "the run changed under the accounting: no completion")
	assert.Equal(t, models.AuthorMetadataRunPaused, readSeededRun(t, scratch, run.ID).Status)

	// Resumed; a write to the run that keeps it running (a retry reopening
	// rows writes the row the same way) also voids the accounting in flight.
	require.NoError(t, TransitionRun(ctx, scratch, run.ID, models.AuthorMetadataRunPaused, models.AuthorMetadataRunRunning))
	blocker, err = scratch.Begin()
	require.NoError(t, err)
	_, err = blocker.Exec(`LOCK TABLE book_contributor_credit_selection IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)
	go func() {
		completed, settleErr := CompleteRunIfSettled(ctx, scratch, run.ID)
		settled <- outcome{completed, settleErr}
	}()
	require.Eventually(t, func() bool {
		var waiting int
		_, qerr := scratch.QueryOne(pg.Scan(&waiting), `SELECT count(*) FROM pg_locks l
			JOIN pg_class c ON c.oid = l.relation
			WHERE NOT l.granted AND c.relname = 'book_contributor_credit_selection'`)
		return qerr == nil && waiting > 0
	}, 10*time.Second, 20*time.Millisecond)
	_, err = scratch.Exec(`UPDATE author_metadata_run SET items_total = items_total WHERE id = ?`, run.ID)
	require.NoError(t, err)
	require.NoError(t, blocker.Rollback())
	got = <-settled
	require.NoError(t, got.err)
	assert.False(t, got.completed, "the row the accounting judged is not the row any more")
	assert.Equal(t, models.AuthorMetadataRunRunning, readSeededRun(t, scratch, run.ID).Status)

	// Accounted on an unchanged row, it completes exactly once.
	results := make(chan bool, 2)
	for range 2 {
		go func() {
			completed, settleErr := CompleteRunIfSettled(ctx, scratch, run.ID)
			assert.NoError(t, settleErr)
			results <- completed
		}()
	}
	first, second := <-results, <-results
	assert.True(t, first != second, "exactly one of two concurrent completions completes")
	assert.Equal(t, models.AuthorMetadataRunCompleted, readSeededRun(t, scratch, run.ID).Status)
}

func readDeletion(t *testing.T, db pg.DBI, id int64) ArchiveDeletion {
	t.Helper()
	var d ArchiveDeletion
	_, err := db.QueryOne(&d, `SELECT id, run_id, archive, books_total, books_deleted, status
		FROM author_metadata_archive_deletion WHERE id = ?`, id)
	require.NoError(t, err)
	return d
}

func archiveBooks(t *testing.T, db pg.DBI, archive string) int {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM opds_catalog_book WHERE path = ?`, archive)
	require.NoError(t, err)
	return n
}

// Deleting an archive's book records is a durable request worked off in
// bounded batches: the request records the intent and returns, a second
// request for the same archive is the same deletion, a batch that dies
// before its commit changes nothing, and the deletion ends once the
// archive's books are gone — the books of other archives untouched.
func TestArchiveDeletionRunsInBatches(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	var ids []int64
	for range 5 {
		ids = append(ids, archiveBook(t, scratch, "gone.zip"))
	}
	keep := archiveBook(t, scratch, "keep.zip")
	run := runSeed()
	run.SelectorBookIDs = append(append([]int64{}, ids...), keep)
	require.NoError(t, SeedRun(ctx, scratch, run, run.SelectorBookIDs))

	first, err := RequestArchiveDeletion(ctx, scratch, run.ID, "gone.zip", nil)
	require.NoError(t, err)
	require.NotZero(t, first.ID)
	assert.Equal(t, ArchiveDeletion{ID: first.ID, RunID: run.ID, Archive: "gone.zip", BooksTotal: 5, Status: "pending"}, first)
	assert.Equal(t, 5, archiveBooks(t, scratch, "gone.zip"), "the request deletes nothing itself")
	again, err := RequestArchiveDeletion(ctx, scratch, run.ID, "gone.zip", nil)
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID, "a second request is the same deletion")

	// A batch that dies before its commit leaves everything as it was.
	tx, err := scratch.Begin()
	require.NoError(t, err)
	n, err := deleteArchiveBatch(ctx, tx, 2)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.NoError(t, tx.Rollback())
	assert.Zero(t, readDeletion(t, scratch, first.ID).BooksDeleted)
	assert.Equal(t, 5, archiveBooks(t, scratch, "gone.zip"))

	var steps []int
	for range 6 {
		n, err = DeleteArchiveBatch(ctx, scratch, 2)
		require.NoError(t, err)
		steps = append(steps, n)
		if n == 0 {
			break
		}
	}
	assert.Equal(t, []int{2, 2, 1, 0}, steps)
	done := readDeletion(t, scratch, first.ID)
	assert.Equal(t, "done", done.Status)
	assert.EqualValues(t, 5, done.BooksDeleted)
	assert.Zero(t, archiveBooks(t, scratch, "gone.zip"))
	assert.Equal(t, 1, archiveBooks(t, scratch, "keep.zip"))
	counts, err := RunItemCounts(ctx, scratch, run.ID)
	require.NoError(t, err)
	assert.Equal(t, exactCounts(t, scratch, run.ID), counts)

	next, err := RequestArchiveDeletion(ctx, scratch, run.ID, "gone.zip", nil)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, next.ID, "a finished deletion does not absorb a new request")
}

// Two workers at once share the work and never delete a book twice.
func TestArchiveDeletionConcurrentWorkers(t *testing.T) {
	scratch := raceDB(t)
	ctx := context.Background()
	var ids []int64
	for range 9 {
		ids = append(ids, archiveBook(t, scratch, "gone.zip"))
	}
	run := runSeed()
	run.SelectorBookIDs = ids
	require.NoError(t, SeedRun(ctx, scratch, run, ids))
	d, err := RequestArchiveDeletion(ctx, scratch, run.ID, "gone.zip", nil)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				n, batchErr := DeleteArchiveBatch(ctx, scratch, 2)
				if batchErr != nil || n == 0 {
					errs[i] = batchErr
					return
				}
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	got := readDeletion(t, scratch, d.ID)
	assert.Equal(t, "done", got.Status)
	assert.EqualValues(t, 9, got.BooksDeleted, "each book counted once")
	assert.Zero(t, archiveBooks(t, scratch, "gone.zip"))
}
