package database

import (
	"context"
	"fmt"
	"testing"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task E1: deleting books deletes their author metadata layer, in the same
// transaction, and nothing else; outside that path the layer stays
// immutable. The cases run on a scratch database (withStoreTx), so the
// counts see only these fixtures.

func TestAuthorLayerOfDeletedBooks(t *testing.T) {
	storeDB = jobsDB(t)
	t.Cleanup(func() { storeDB = nil })
	t.Run("DeletingAnArchiveDeletesItsBooksLayer", deletingAnArchiveDeletesItsBooksLayer)
	t.Run("TheLayerStaysImmutableOutsideTheDeletion", theLayerStaysImmutableOutsideTheDeletion)
}

// layerRows counts every row of a book's layer.
func (f *authorSchemaFixture) layerRows(book int64) map[string]int {
	f.t.Helper()
	credits := `(SELECT c.id FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE s.book_id = ?)`
	return map[string]int{
		"snapshots":  f.count(`SELECT count(*) FROM book_metadata_snapshot WHERE book_id = ?`, book),
		"credits":    f.count(`SELECT count(*) FROM book_contributor_credit WHERE id IN `+credits, book),
		"selections": f.count(`SELECT count(*) FROM book_contributor_credit_selection WHERE credit_id IN `+credits, book),
		"audit":      f.count(`SELECT count(*) FROM book_contributor_credit_selection_audit WHERE credit_id IN `+credits, book),
		"overrides":  f.count(`SELECT count(*) FROM contributor_manual_override WHERE scope_credit_id IN `+credits, book),
		"reviews":    f.count(`SELECT count(*) FROM contributor_review_item WHERE scope_credit_id IN `+credits, book),
		"run items":  f.count(`SELECT count(*) FROM author_metadata_run_item WHERE book_id = ?`, book),
		"attempts": f.count(`SELECT count(*) FROM author_metadata_run_item_attempt a
			JOIN author_metadata_run_item i ON i.id = a.run_item_id WHERE i.book_id = ?`, book),
	}
}

// unreferencedManualResults counts manual results nothing points at.
func (f *authorSchemaFixture) unreferencedManualResults() int {
	return f.count(`SELECT count(*) FROM contributor_normalization_result r
		WHERE r.method = 'manual'
			AND NOT EXISTS (SELECT 1 FROM contributor_manual_override o WHERE o.result_id = r.id)
			AND NOT EXISTS (SELECT 1 FROM contributor_review_item i WHERE r.id IN (i.proposal_result_id, i.resolution_result_id))
			AND NOT EXISTS (SELECT 1 FROM book_contributor_credit_selection s WHERE s.result_id = r.id)
			AND NOT EXISTS (SELECT 1 FROM book_contributor_credit_selection_audit a
				WHERE r.id IN (a.result_id, a.previous_result_id))`)
}

// The deleted archive's book had two file versions, a selection and its
// audit, a credit override over a manual result, an ambiguous source behind
// an open fingerprint review item, and a run item with an attempt. Another
// archive's book shares one of its sources, under a fingerprint override.
func deletingAnArchiveDeletesItsBooksLayer(t *testing.T) {
	f := withStoreTx(t)
	admin := f.user()
	run := f.run(&runSpec{})

	deleted, deletedIDs := f.persistBook(authorCredit(structuredSource(t)), authorCredit(initialsSource(t)))
	f.persistVersion(deleted, "ffffffffffffffffffffffffffffffff",
		authorCredit(structuredSource(t)), authorCredit(initialsSource(t)))
	kept, keptIDs := f.persistBook(authorCredit(structuredSource(t)))
	f.exec(`UPDATE opds_catalog_book SET path = 'other.zip' WHERE id = ?`, kept)
	// A genre link, as every scanned book with tags has one.
	genre := f.returningID(`INSERT INTO opds_catalog_genre (genre) VALUES ('fixture_genre') RETURNING id`)
	f.exec(`INSERT INTO opds_catalog_bgenre (genre_id, book_id) VALUES (?, ?), (?, ?)`, genre, deleted, genre, kept)

	structured := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
	initials := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
	structuredID, initialsID := f.storeResult(&structured), f.storeResult(&initials)
	sfp, ifp := structured.SourceFingerprint[:], initials.SourceFingerprint[:]
	byFingerprint := f.manualResultFor(sfp, admin)
	f.fingerprintOverride(sfp, byFingerprint, admin)
	byCredit := f.manualResultFor(sfp, admin)
	f.creditOverride(deletedIDs[0], byCredit, sfp, admin)
	f.resolve(&structured, decide(t, emptyPolicy(t, f), &structured, structuredID))
	f.resolve(&initials, decide(t, emptyPolicy(t, f), &initials, initialsID))
	require.Len(t, f.openReviewItems(initials.SourceFingerprint), 1)
	// A credit-scoped item too, on the superseded file version's credit.
	f.returningID(reviewInsert, deletedIDs[1], nil, ifp, "ambiguous_decision", "initials", initialsID)
	item := f.runItem(run, deleted)
	f.exec(`INSERT INTO author_metadata_run_item_attempt (run_item_id, attempt_no, lease_owner, finished_at, outcome)
		VALUES (?, 1, ?, now(), 'invalid_fb2')`, item, ownerToken)
	f.runItem(run, kept)
	keptBefore := f.layerRows(kept)
	keptSelection := f.selection(keptIDs[0])
	require.Equal(t, models.CreditSelectionFingerprintOverride, *keptSelection.Basis)
	jobs := f.count(`SELECT count(*) FROM contributor_normalization_job`)
	for name, n := range f.layerRows(deleted) {
		require.Positive(t, n, "the fixture has no %s for the deleted book", name)
	}

	n, err := deleteArchiveBooks(f.tx, "fixture.zip")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Zero(t, f.count(`SELECT count(*) FROM opds_catalog_book WHERE id = ?`, deleted))
	// The genre link's foreign key is checked at commit: settle it here.
	f.exec(`SET CONSTRAINTS ALL IMMEDIATE`)
	assert.Zero(t, f.count(`SELECT count(*) FROM opds_catalog_bgenre WHERE book_id = ?`, deleted))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM opds_catalog_bgenre WHERE book_id = ?`, kept))

	for name, rows := range f.layerRows(deleted) {
		assert.Zero(t, rows, "%s of the deleted book remain", name)
	}
	assert.Empty(t, f.openReviewItems(initials.SourceFingerprint), "no other credit has that source: its review item goes")
	assert.Zero(t, f.count(`SELECT count(*) FROM contributor_normalization_result WHERE id = ?`, byCredit),
		"the credit override's manual result goes with it")
	assert.Zero(t, f.unreferencedManualResults())

	assert.Equal(t, keptBefore, f.layerRows(kept), "the other archive's book keeps its layer")
	assert.Equal(t, keptSelection, f.selection(keptIDs[0]), "and its selection")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_manual_override WHERE scope_fingerprint = ?`, sfp),
		"its source is still in use: the fingerprint override stays")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_result WHERE id = ?`, byFingerprint))
	// Results and jobs are keyed by normalization input and shared: they stay,
	// referenced or not, for a book that brings the same source back.
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM contributor_normalization_result WHERE id IN (?, ?)`,
		structuredID, initialsID))
	assert.Equal(t, jobs, f.count(`SELECT count(*) FROM contributor_normalization_job`))
}

// Outside the deletion the layer refuses UPDATE and DELETE as before, also
// later in the very transaction that deleted books.
func theLayerStaysImmutableOutsideTheDeletion(t *testing.T) {
	f := withStoreTx(t)
	_, deletedIDs := f.persistBook(authorCredit(structuredSource(t)))
	kept, keptIDs := f.persistBook(authorCredit(initialsSource(t)))
	f.exec(`UPDATE opds_catalog_book SET path = 'other.zip' WHERE id = ?`, kept)

	f.reject(sqlstateRestrict, "immutable", `DELETE FROM book_contributor_credit WHERE id = ?`, keptIDs[0])
	f.reject(sqlstateRestrict, "immutable", `UPDATE book_contributor_credit SET position = 7 WHERE id = ?`, keptIDs[0])

	_, err := deleteArchiveBooks(f.tx, "fixture.zip")
	require.NoError(t, err)
	assert.Zero(t, f.count(`SELECT count(*) FROM book_contributor_credit WHERE id = ?`, deletedIDs[0]))

	var setting string
	_, err = f.tx.QueryOne(pg.Scan(&setting), `SELECT coalesce(current_setting('gopds.author_layer_delete', true), '')`)
	require.NoError(t, err)
	assert.NotEqual(t, "on", setting, "the bypass ends with the deletion")
	f.reject(sqlstateRestrict, "immutable", `DELETE FROM book_contributor_credit WHERE id = ?`, keptIDs[0])
	f.reject(sqlstateRestrict, "immutable",
		`DELETE FROM book_metadata_snapshot WHERE book_id = ?`, kept)
	f.reject(sqlstateRestrict, "immutable", `UPDATE book_contributor_credit SET position = 7 WHERE id = ?`, keptIDs[0])
	// The exception is DELETE on a book's layer only: never an UPDATE, and
	// never the policy tables, even while it is on.
	f.registerClass("2", "structured_person", authornorm.NormalizerVersion)
	f.exec(`SELECT set_config('gopds.author_layer_delete', 'on', true)`)
	f.reject(sqlstateRestrict, "immutable", `UPDATE book_contributor_credit SET position = 7 WHERE id = ?`, keptIDs[0])
	f.reject(sqlstateRestrict, "immutable", `DELETE FROM author_acceptance_class`)
}

// Task E3: the backfill run's per-book failures — failure statuses and items
// still pending after a failed attempt — appear in the scan errors list with
// their archive, entry and closed class, from the newest run only.
func TestAuthorMetadataScanFailures(t *testing.T) {
	storeDB = jobsDB(t)
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	older := f.run(&runSpec{status: "completed"})
	run := f.run(&runSpec{status: "running"})
	failed, retried, fine, old := f.book(), f.book(), f.book(), f.book()
	f.exec(`INSERT INTO author_metadata_run_item (run_id, book_id, status, attempt_count, finished_at)
		VALUES (?, ?, 'invalid_fb2', 1, now())`, run, failed)
	item := f.returningID(`INSERT INTO author_metadata_run_item (run_id, book_id, status, attempt_count)
		VALUES (?, ?, 'pending', 1) RETURNING id`, run, retried)
	f.exec(`INSERT INTO author_metadata_run_item_attempt (run_item_id, attempt_no, lease_owner, finished_at, error_class)
		VALUES (?, 1, ?, now(), 'lease_expired')`, item, ownerToken)
	f.runItem(run, fine)
	f.exec(`INSERT INTO author_metadata_run_item (run_id, book_id, status, attempt_count, finished_at)
		VALUES (?, ?, 'entry_missing', 1, now())`, older, old)

	failures, err := AuthorMetadataScanFailures(context.Background(), f.tx, 10)
	require.NoError(t, err)
	require.Len(t, failures, 2)
	byEntry := map[string]AuthorMetadataScanFailure{}
	for _, failure := range failures {
		assert.Equal(t, "fixture.zip", failure.Archive)
		byEntry[failure.Entry] = failure
	}
	assert.Equal(t, "invalid_fb2", byEntry[fmt.Sprintf("%d.fb2", failed)].Class)
	assert.Equal(t, "lease_expired", byEntry[fmt.Sprintf("%d.fb2", retried)].Class)

	limited, err := AuthorMetadataScanFailures(context.Background(), f.tx, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

// Review B3: an attempt class outside the closed vocabulary — a historical
// writer, a hand edit — never reaches the list as such: the schema only checks
// the token's shape, so the read closes it.
func TestAuthorMetadataScanFailureClassesAreClosed(t *testing.T) {
	storeDB = jobsDB(t)
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	run := f.run(&runSpec{})
	item := f.runItem(run, f.book())
	f.exec(`INSERT INTO author_metadata_run_item_attempt (run_item_id, attempt_no, lease_owner, finished_at, error_class)
		VALUES (?, 1, ?, now(), 'privacy_canary_name')`, item, ownerToken)

	rows, err := AuthorMetadataScanFailures(context.Background(), f.tx, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "extraction_failed", rows[0].Class)
	assert.NotContains(t, fmt.Sprint(rows), "privacy_canary_name")

	for _, class := range AuthorMetadataScanFailureClasses() {
		assert.Equal(t, class, ClosedAuthorMetadataScanClass(class))
	}
	assert.Equal(t, "extraction_failed", ClosedAuthorMetadataScanClass(""))
}
