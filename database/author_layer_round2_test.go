package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/require"
)

// Fix round 2 of task E: the archive deletion's target set, the active run's
// accounting after its items go, and fingerprint decisions that outlive the
// books they were made on (amendment 1).

// pauseAfterGenreLinks parks the archive deletion right after it deleted the
// genre links: the last legacy statement before its layer and book deletes.
type pauseAfterGenreLinks struct {
	*pg.Tx
	ready, release chan struct{}
}

func (g *pauseAfterGenreLinks) Exec(q interface{}, p ...interface{}) (pg.Result, error) {
	r, err := g.Tx.Exec(q, p...)
	if sql, ok := q.(string); ok && strings.HasPrefix(sql, "DELETE FROM opds_catalog_bgenre") && err == nil {
		close(g.ready)
		<-g.release
	}
	return r, err
}

// The reviewer's B9 probe: a book of the same archive committed while the
// deletion runs is not part of it. The deletion deletes exactly the books it
// locked, so it never reaches a book whose links it did not clean up.
func TestArchiveDeletionTargetsOnlyTheBooksItLocked(t *testing.T) {
	s := jobsDB(t)
	storeDB = s
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	old, _ := f.persistBook(authorCredit(structuredSource(t)))
	genre := f.returningID(`INSERT INTO opds_catalog_genre (genre) VALUES ('fixture_genre') RETURNING id`)
	require.NoError(t, f.tx.Commit())

	deleter, err := s.Begin()
	require.NoError(t, err)
	paused := &pauseAfterGenreLinks{Tx: deleter, ready: make(chan struct{}), release: make(chan struct{})}
	type outcome struct {
		n   int
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		n, delErr := deleteArchiveBooks(paused, "fixture.zip")
		if delErr != nil {
			_ = deleter.Rollback()
			done <- outcome{0, delErr}
			return
		}
		done <- outcome{n, deleter.Commit()}
	}()
	select {
	case <-paused.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the deletion never finished its legacy cleanup")
	}

	tx, err := s.Begin()
	require.NoError(t, err)
	writer := &authorSchemaFixture{t: t, tx: tx, next: f.next}
	added, _ := writer.persistBook(authorCredit(initialsSource(t)))
	writer.exec(`INSERT INTO opds_catalog_bgenre (book_id, genre_id) VALUES (?, ?)`, added, genre)
	require.NoError(t, tx.Commit())
	close(paused.release)

	got := <-done
	require.NoError(t, got.err, "a book added meanwhile must not enter the deletion")
	require.Equal(t, 1, got.n)
	var left []int64
	_, err = s.Query(&left, `SELECT id FROM opds_catalog_book WHERE path = 'fixture.zip' ORDER BY id`)
	require.NoError(t, err)
	require.Equal(t, []int64{added}, left)
	require.Equal(t, 1, creditExists(t, s, added), "the added book keeps its layer")
	require.Zero(t, creditExists(t, s, old))
}

type runCounters struct {
	Status   string
	Total    int
	Terminal int
	Items    int
	Stamped  bool
}

func readRunCounters(t *testing.T, s *pg.DB, run int64) runCounters {
	t.Helper()
	var c runCounters
	_, err := s.QueryOne(&c, `SELECT status, items_total AS total, items_terminal AS terminal,
			(SELECT count(*) FROM author_metadata_run_item WHERE run_id = r.id) AS items,
			extraction_completed_at IS NOT NULL AS stamped
		FROM author_metadata_run r WHERE id = ?`, run)
	require.NoError(t, err)
	return c
}

func seedRunningSmoke(t *testing.T, s *pg.DB, books ...int64) int64 {
	t.Helper()
	run := &models.AuthorMetadataRun{
		Mode: models.AuthorMetadataRunSmoke, Status: models.AuthorMetadataRunRunning,
		ExtractorVersion: resolverExtractor, NormalizerVersion: "authornorm-local-v1", SelectorBookIDs: books,
	}
	require.NoError(t, SeedRun(context.Background(), s, run, books))
	return run.ID
}

func deleteArchive(t *testing.T, s *pg.DB, archive string) {
	t.Helper()
	tx, err := s.Begin()
	require.NoError(t, err)
	_, err = deleteArchiveBooks(tx, archive)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

// The reviewer's B10 probe: every book of a running run is deleted while its
// item is still pending. The run's own reconciliation — what the extraction
// worker runs on every idle poll — accounts for the deletion, so the run
// completes instead of holding the one active slot forever.
func TestDeletingPendingBooksLetsTheActiveRunSettle(t *testing.T) {
	s := jobsDB(t)
	storeDB = s
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	book, _ := f.persistBook(authorCredit(structuredSource(t)))
	require.NoError(t, f.tx.Commit())
	ctx := context.Background()
	run := seedRunningSmoke(t, s, book)

	deleteArchive(t, s, "fixture.zip")
	extracted, err := ReconcileRunExtraction(ctx, s, run)
	require.NoError(t, err)
	require.True(t, extracted)
	completed, err := CompleteRunIfSettled(ctx, s, run)
	require.NoError(t, err)
	require.True(t, completed, "a run whose books were all deleted must not keep the active slot")
	c := readRunCounters(t, s, run)
	require.Equal(t, runCounters{Status: "completed", Total: 0, Terminal: 0, Items: 0, Stamped: true}, c)
}

// Deleting a terminal item and a pending one of the same running run, in
// separate archives: after each deletion the counters describe the items that
// remain, and the run settles only once nothing remains pending.
func TestDeletingTerminalAndPendingItemsKeepsTheRunCountersTrue(t *testing.T) {
	s := jobsDB(t)
	storeDB = s
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	done, _ := f.persistBook(authorCredit(structuredSource(t)))
	waiting, _ := f.persistBook(authorCredit(initialsSource(t)))
	f.exec(`UPDATE opds_catalog_book SET path = 'other.zip' WHERE id = ?`, waiting)
	require.NoError(t, f.tx.Commit())
	ctx := context.Background()
	run := seedRunningSmoke(t, s, done, waiting)
	_, err := s.Exec(`UPDATE author_metadata_run_item SET status = 'invalid_fb2', finished_at = now()
		WHERE run_id = ? AND book_id = ?`, run, done)
	require.NoError(t, err)
	_, err = ReconcileRunExtraction(ctx, s, run)
	require.NoError(t, err)

	deleteArchive(t, s, "fixture.zip")
	extracted, err := ReconcileRunExtraction(ctx, s, run)
	require.NoError(t, err)
	require.False(t, extracted, "the waiting book is still pending")
	require.Equal(t, runCounters{Status: "running", Total: 1, Terminal: 0, Items: 1}, readRunCounters(t, s, run))

	deleteArchive(t, s, "other.zip")
	extracted, err = ReconcileRunExtraction(ctx, s, run)
	require.NoError(t, err)
	require.True(t, extracted)
	completed, err := CompleteRunIfSettled(ctx, s, run)
	require.NoError(t, err)
	require.True(t, completed)
}

// A finished run keeps its counters as the record of what it processed; its
// item rows go with their books.
func TestDeletingBooksOfAFinishedRunKeepsItsRecord(t *testing.T) {
	s := jobsDB(t)
	storeDB = s
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	book, _ := f.persistBook(authorCredit(structuredSource(t)))
	require.NoError(t, f.tx.Commit())
	ctx := context.Background()
	run := seedRunningSmoke(t, s, book)
	_, err := s.Exec(`UPDATE author_metadata_run_item SET status = 'invalid_fb2', finished_at = now() WHERE run_id = ?`, run)
	require.NoError(t, err)
	_, err = ReconcileRunExtraction(ctx, s, run)
	require.NoError(t, err)
	// Its credits are not the point here: the run is recorded as finished.
	_, err = s.Exec(`UPDATE author_metadata_run SET status = 'completed', finished_at = clock_timestamp() WHERE id = ?`, run)
	require.NoError(t, err)

	deleteArchive(t, s, "fixture.zip")
	_, err = ReconcileRunExtraction(ctx, s, run)
	require.ErrorIs(t, err, ErrRunTransitionConflict, "a finished run is not reconciled")
	require.Equal(t, runCounters{Status: "completed", Total: 1, Terminal: 1, Items: 0, Stamped: true},
		readRunCounters(t, s, run))
}

func manualResults(t *testing.T, s *pg.DB) int {
	t.Helper()
	var n int
	_, err := s.QueryOne(pg.Scan(&n), `SELECT count(*) FROM contributor_normalization_result WHERE method = 'manual'`)
	require.NoError(t, err)
	return n
}

// Amendment 1 and the reviewer's reingest probe: a fingerprint decision is
// about a spelling of a name, not about a book. Deleting the last book that
// carries the spelling leaves the decision and its manual result in place,
// dormant; a book bringing the spelling back finds it again. Open review
// items of the spelling go with the books.
func TestFingerprintDecisionsOutliveTheirBooks(t *testing.T) {
	s := jobsDB(t)
	f, _, v, fp := sharedFingerprintBook(t, s)
	require.Equal(t, 1, overridesOf(t, s, fp))
	manualBefore := manualResults(t, s)

	deleteArchive(t, s, "fixture.zip")
	require.Equal(t, 1, overridesOf(t, s, fp), "the fingerprint decision outlives its books")
	require.Equal(t, manualBefore, manualResults(t, s), "with the manual result it cites")
	var reviews int
	_, err := s.QueryOne(pg.Scan(&reviews), `SELECT count(*) FROM contributor_review_item WHERE scope_fingerprint = ?`, fp)
	require.NoError(t, err)
	require.Zero(t, reviews, "an open review of a spelling no book carries goes")

	tx, err := s.Begin()
	require.NoError(t, err)
	other := &authorSchemaFixture{t: t, tx: tx, next: f.next}
	back, _ := other.persistBook(authorCredit(v))
	require.NoError(t, tx.Commit())
	require.Equal(t, 1, creditExists(t, s, back))
	override, err := ActiveOverrideForFingerprint(s, fp)
	require.NoError(t, err)
	require.NotNil(t, override, "the returning spelling finds its decision")
}
