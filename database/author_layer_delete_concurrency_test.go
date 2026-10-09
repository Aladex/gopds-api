package database

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/require"
)

// Deleting a book's author layer runs concurrently with the source writers
// (ingest, fix scan, rescans, the extraction worker), the acceptance pass and
// review actions. These cases pin the lock protocol that keeps them apart:
//
//   - deletion locks its books, then the layer exclusively, then its credits in
//     ascending ID order, and only then decides which fingerprints are orphaned;
//   - a source writer locks its book first, then holds the layer shared while
//     it adds credits;
//   - a review action locks credits in ascending ID order before any review
//     item, which deletion now follows too.
//
// Each case drives a real interleaving with scratch-only triggers that park a
// transaction on an advisory "gate" lock the test holds; production SQL is
// never replaced.

const (
	deletionGateKey = 7654321
	writerGateKey   = 7654322
)

// holdGate takes the gate lock in a transaction of its own and installs a
// trigger that parks every statement firing it until the test commits the
// returned transaction.
func holdGate(t *testing.T, s *pg.DB, key int, trigger string) *pg.Tx {
	t.Helper()
	gate, err := s.Begin()
	require.NoError(t, err)
	_, err = gate.Exec(`SELECT pg_advisory_xact_lock(?)`, key)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gate.Rollback() })
	name := "gate_" + strings.ReplaceAll(strings.ToLower(trigger), " ", "_")
	_, err = s.Exec(`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_advisory_xact_lock(` + strconv.Itoa(key) + `); RETURN coalesce(NEW, OLD); END $$;
		CREATE TRIGGER ` + name + ` ` + trigger + ` FOR EACH ROW EXECUTE FUNCTION ` + name + `()`)
	require.NoError(t, err)
	return gate
}

// waitBlocked waits until a backend running a statement that contains
// fragment waits for a lock (a row, an advisory or a gate lock alike).
func waitBlocked(t *testing.T, s *pg.DB, fragment string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var n int
		_, err := s.QueryOne(pg.Scan(&n), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type IN ('Lock', 'Advisory')
			  AND query LIKE ?`, "%"+fragment+"%")
		return err == nil && n > 0
	}, 10*time.Second, 10*time.Millisecond, "nothing running %q ever waited for a lock", fragment)
}

func deleteLayerAsync(s *pg.DB, book int64) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := s.Exec(`SELECT author_layer_delete_books(ARRAY[?]::bigint[])`, book)
		done <- err
	}()
	return done
}

func receive(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(20 * time.Second):
		t.Fatalf("%s never finished", what)
		return nil
	}
}

// sharedFingerprintBook commits book A with an author credit of the
// structured source, a fingerprint override (with its manual result) and an
// open fingerprint review item on that source, and returns the book, the
// source and its fingerprint.
func sharedFingerprintBook(t *testing.T, s *pg.DB) (
	f *authorSchemaFixture, book int64, v authornorm.SourceValue, fp []byte,
) {
	t.Helper()
	storeDB = s
	t.Cleanup(func() { storeDB = nil })
	f = withStoreTx(t)
	admin := f.user()
	v = structuredSource(t)
	book, _ = f.persistBook(authorCredit(v))
	r := normalizedAs(t, v, authornorm.ClassStructuredPerson)
	result := f.storeResult(&r)
	manual := f.manualResultFor(r.SourceFingerprint[:], admin)
	f.fingerprintOverride(r.SourceFingerprint[:], manual, admin)
	f.returningID(reviewInsert, nil, r.SourceFingerprint[:], r.SourceFingerprint[:],
		"ambiguous_decision", "structured_person", result)
	require.NoError(t, f.tx.Commit())
	return f, book, v, r.SourceFingerprint[:]
}

// fingerprintReviews counts the review items scoped to the fingerprint: what
// the deletion's orphan decision governs now that fingerprint overrides are
// never deleted (amendment 1).
func fingerprintReviews(t *testing.T, s *pg.DB, fp []byte) int {
	t.Helper()
	var n int
	_, err := s.QueryOne(pg.Scan(&n), `SELECT count(*) FROM contributor_review_item WHERE scope_fingerprint = ?`, fp)
	require.NoError(t, err)
	return n
}

func overridesOf(t *testing.T, s *pg.DB, fp []byte) int {
	t.Helper()
	var n int
	_, err := s.QueryOne(pg.Scan(&n), `SELECT count(*) FROM contributor_manual_override WHERE scope_fingerprint = ?`, fp)
	require.NoError(t, err)
	return n
}

func creditExists(t *testing.T, s *pg.DB, book int64) int {
	t.Helper()
	var n int
	_, err := s.QueryOne(pg.Scan(&n), `SELECT count(*) FROM book_contributor_credit c
		JOIN book_metadata_snapshot x ON x.id = c.snapshot_id WHERE x.book_id = ?`, book)
	require.NoError(t, err)
	return n
}

// A writer that already added a credit of the fingerprint, uncommitted, when
// the deletion starts: the deletion waits for it, then sees the credit, so the
// fingerprint is not orphaned and its open review item stays.
func TestLayerDeletionWaitsForAnInFlightWriterOfTheFingerprint(t *testing.T) {
	s := jobsDB(t)
	f, book, v, fp := sharedFingerprintBook(t, s)

	writer, err := s.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Rollback() })
	other := &authorSchemaFixture{t: t, tx: writer, next: f.next}
	kept, _ := other.persistBook(authorCredit(v))

	deleted := deleteLayerAsync(s, book)
	select {
	case err = <-deleted:
		// The deletion did not wait: it can only have decided on the
		// fingerprint without the writer's credit.
		require.NoError(t, writer.Commit())
		require.NoError(t, err)
		require.Equal(t, 1, fingerprintReviews(t, s, fp), "the deletion removed the review of a spelling an in-flight book carries")
		return
	case <-time.After(500 * time.Millisecond):
	}
	waitBlocked(t, s, "author_layer_delete_books")
	require.NoError(t, writer.Commit())
	require.NoError(t, receive(t, deleted, "the deletion"))

	require.Equal(t, 1, creditExists(t, s, kept))
	require.Equal(t, 1, fingerprintReviews(t, s, fp), "the kept book's fingerprint review survives")
	require.Equal(t, 1, overridesOf(t, s, fp))
	require.Zero(t, creditExists(t, s, book))
}

// The reviewer's interleaving: the deletion is parked after it decided which
// fingerprints are orphaned; a writer of the same fingerprint starts then. It
// must not commit a credit until the deletion has committed, so the outcome is
// the serial order "deletion, then the new book": the open review of the
// orphaned spelling went with the last book, and the fingerprint decision
// (amendment 1) is there for the new book.
func TestLayerDeletionHoldsOffAWriterThatStartsDuringIt(t *testing.T) {
	s := jobsDB(t)
	f, book, v, fp := sharedFingerprintBook(t, s)
	gate := holdGate(t, s, deletionGateKey, "AFTER DELETE ON contributor_review_item")

	deleted := deleteLayerAsync(s, book)
	waitBlocked(t, s, "author_layer_delete_books")

	writer, err := s.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Rollback() })
	other := &authorSchemaFixture{t: t, tx: writer, next: f.next}
	kept := other.book()
	in := other.extraction(&extractionSpec{
		book: kept, extractor: resolverExtractor, normalizer: authornorm.NormalizerVersion,
		credits: []ExtractionCredit{authorCredit(v)},
	})
	written := make(chan error, 1)
	go func() {
		if _, persistErr := PersistExtraction(writer, in); persistErr != nil {
			written <- persistErr
			return
		}
		written <- writer.Commit()
	}()

	require.Never(t, func() bool { return len(written) > 0 }, 500*time.Millisecond, 25*time.Millisecond,
		"a writer of the fingerprint committed while the deletion was deciding on it")
	require.NoError(t, gate.Commit())
	require.NoError(t, receive(t, deleted, "the deletion"))
	require.NoError(t, receive(t, written, "the writer"))

	require.Equal(t, 1, creditExists(t, s, kept))
	require.Zero(t, fingerprintReviews(t, s, fp), "serial order: the open review went with the last book")
	require.Equal(t, 1, overridesOf(t, s, fp), "the fingerprint decision outlives the books")
}

// readyRelease parks a review transaction right after it locked its first
// credit: ExecContext is the one call the review makes for that lock.
type readyRelease struct {
	*pg.Tx
	ready, release chan struct{}
	fired          bool
}

func (g *readyRelease) ExecContext(c context.Context, q interface{}, p ...interface{}) (pg.Result, error) {
	r, err := g.Tx.ExecContext(c, q, p...)
	if sql, ok := q.(string); ok && strings.Contains(sql, "FOR NO KEY UPDATE") && !g.fired && err == nil {
		g.fired = true
		close(g.ready)
		<-g.release
	}
	return r, err
}

// The reviewer's lock-order probe: a real review action holds its credit, the
// deletion of that credit's book starts. Deletion takes credits before review
// items, as every review path does, so it waits instead of locking the review
// row first; both finish and nobody deadlocks.
func TestLayerDeletionAndAReviewActionTakeCreditsFirst(t *testing.T) {
	s := jobsDB(t)
	credit, _, item := seedAmbiguousInput(t, s)
	var book int64
	_, err := s.QueryOne(pg.Scan(&book), `SELECT x.book_id FROM book_metadata_snapshot x
		JOIN book_contributor_credit c ON c.snapshot_id = x.id WHERE c.id = ?`, credit)
	require.NoError(t, err)
	gate := holdGate(t, s, deletionGateKey, "AFTER DELETE ON contributor_review_item")

	tx, err := s.Begin()
	require.NoError(t, err)
	review := &readyRelease{Tx: tx, ready: make(chan struct{}), release: make(chan struct{})}
	decided := make(chan error, 1)
	go func() {
		if _, actErr := ApplyReviewAction(context.Background(), review, item, 1,
			ReviewDecision{Action: ReviewLeaveUnresolved}); actErr != nil {
			_ = tx.Rollback()
			decided <- actErr
			return
		}
		decided <- tx.Commit()
	}()
	select {
	case <-review.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the review never locked its credit")
	}

	deleted := deleteLayerAsync(s, book)
	waitBlocked(t, s, "author_layer_delete_books")
	close(review.release)
	require.NoError(t, gate.Commit())

	require.NoError(t, receive(t, decided, "the review action"))
	require.NoError(t, receive(t, deleted, "the deletion"), "the deletion must not deadlock with a review")
	require.Zero(t, creditExists(t, s, book))
}

// A source writer refreshing the very book being deleted: the writer un-marks
// the book's current snapshot before it inserts the new one, so it would hold
// that snapshot while waiting for the book the deletion locked, while the
// deletion waits for the snapshot. The writer locks its book first; the
// deletion waits for it, then deletes the refreshed layer too.
func TestLayerDeletionOfABookBeingRefreshed(t *testing.T) {
	s := jobsDB(t)
	storeDB = s
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	book, _ := f.persistBook(authorCredit(structuredSource(t)))
	require.NoError(t, f.tx.Commit())
	gate := holdGate(t, s, writerGateKey, "BEFORE INSERT ON book_metadata_snapshot")

	writer, err := s.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Rollback() })
	refresher := &authorSchemaFixture{t: t, tx: writer, next: f.next}
	in := refresher.extraction(&extractionSpec{
		book: book, md5: bookMD5(book + 1), extractor: resolverExtractor, normalizer: authornorm.NormalizerVersion,
		credits: []ExtractionCredit{authorCredit(initialsSource(t))},
	})
	written := make(chan error, 1)
	go func() {
		if _, persistErr := PersistExtraction(writer, in); persistErr != nil {
			_ = writer.Rollback()
			written <- persistErr
			return
		}
		written <- writer.Commit()
	}()
	waitBlocked(t, s, "INSERT INTO book_metadata_snapshot")

	deleted := deleteLayerAsync(s, book)
	waitBlocked(t, s, "author_layer_delete_books")
	require.NoError(t, gate.Commit())

	require.NoError(t, receive(t, written, "the refresh"))
	require.NoError(t, receive(t, deleted, "the deletion"), "the deletion must not deadlock with a refresh")
	var snapshots int
	_, err = s.QueryOne(pg.Scan(&snapshots), `SELECT count(*) FROM book_metadata_snapshot WHERE book_id = ?`, book)
	require.NoError(t, err)
	require.Zero(t, snapshots, "the refreshed snapshot went with the book's layer")
}

// The reviewer's bypass probe, kept: a deletion that fails part-way and is
// rolled back to a savepoint leaves the bypass off in the transaction and on
// the pooled connection.
func TestLayerDeletionBypassRollsBackAndDoesNotLeak(t *testing.T) {
	s := jobsDB(t)
	storeDB = s
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	book, _ := f.persistBook(authorCredit(initialsSource(t)))
	r := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
	rid := f.storeResult(&r)
	f.returningID(reviewInsert, nil, r.SourceFingerprint[:], r.SourceFingerprint[:], "ambiguous_decision", "initials", rid)
	f.exec(`CREATE FUNCTION fail_review_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected failure'; END $$;
		CREATE TRIGGER fail_review_delete AFTER DELETE ON contributor_review_item
		FOR EACH ROW EXECUTE FUNCTION fail_review_delete()`)
	f.exec(`SAVEPOINT outside_deletion`)
	_, err := f.tx.Exec(`SELECT author_layer_delete_books(ARRAY[?]::bigint[])`, book)
	require.Error(t, err)
	f.exec(`ROLLBACK TO SAVEPOINT outside_deletion`)
	var value string
	_, err = f.tx.QueryOne(pg.Scan(&value), `SELECT coalesce(current_setting('gopds.author_layer_delete', true), '')`)
	require.NoError(t, err)
	require.NotEqual(t, "on", value)
	f.reject(sqlstateRestrict, "immutable", `DELETE FROM book_contributor_credit`)
	require.NoError(t, f.tx.Rollback())

	s.Options().PoolSize = 1
	_, err = s.QueryOne(pg.Scan(&value), `SELECT coalesce(current_setting('gopds.author_layer_delete', true), '')`)
	require.NoError(t, err)
	require.NotEqual(t, "on", value)
}

// The deletion's SQL spells the layer lock's numbers; they must stay the
// writers' numbers.
func TestLayerLockNumbersMatchTheMigration(t *testing.T) {
	sql, err := os.ReadFile("../database_migrations/26-author-layer-deleted-with-books.sql")
	require.NoError(t, err)
	require.Contains(t, string(sql), "pg_advisory_xact_lock("+strconv.Itoa(int(authorLayerLockClass))+", "+
		strconv.Itoa(int(authorLayerLockKey))+")")
}

// A fix scan updates a book, then rewrites its author links. The archive
// deletion locks its books before it deletes any link, so it waits for the
// fix scan instead of holding the links the fix scan needs while waiting for
// the book.
func TestArchiveDeletionWaitsForAFixScanOfItsBook(t *testing.T) {
	s := jobsDB(t)
	storeDB = s
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	book, _ := f.persistBook(authorCredit(structuredSource(t)))
	author := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES ('Фикстура') RETURNING id`)
	f.exec(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?)`, book, author)
	require.NoError(t, f.tx.Commit())

	fix, err := s.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = fix.Rollback() })
	_, err = fix.Exec(`UPDATE opds_catalog_book SET title = 'fixed' WHERE id = ?`, book)
	require.NoError(t, err)

	deleted := make(chan error, 1)
	go func() {
		tx, beginErr := s.Begin()
		if beginErr != nil {
			deleted <- beginErr
			return
		}
		if _, delErr := deleteArchiveBooks(tx, "fixture.zip"); delErr != nil {
			_ = tx.Rollback()
			deleted <- delErr
			return
		}
		deleted <- tx.Commit()
	}()
	waitBlocked(t, s, "opds_catalog_book")

	_, err = fix.Exec(`DELETE FROM opds_catalog_bauthor WHERE book_id = ?`, book)
	require.NoError(t, err, "the fix scan must not deadlock with the archive deletion")
	require.NoError(t, fix.Commit())
	require.NoError(t, receive(t, deleted, "the archive deletion"))
	var books int
	_, err = s.QueryOne(pg.Scan(&books), `SELECT count(*) FROM opds_catalog_book WHERE id = ?`, book)
	require.NoError(t, err)
	require.Zero(t, books)
}
