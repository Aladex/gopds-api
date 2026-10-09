package services_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// creditSelection is one author credit of a book's current snapshot with the
// resolution it has.
type creditSelection struct {
	CreditID    int64
	Fingerprint []byte
	State       *string
	OverrideID  *int64
	ResultID    *int64
}

func currentAuthorSelections(t *testing.T, db *pg.DB, bookID int64) []creditSelection {
	t.Helper()
	var rows []creditSelection
	_, err := db.Query(&rows, `SELECT c.id AS credit_id, c.source_fingerprint AS fingerprint,
			sel.state, sel.override_id, sel.result_id
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		LEFT JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id
		WHERE s.book_id = ? AND c.role = 'author' ORDER BY c.position`, bookID)
	require.NoError(t, err)
	return rows
}

// drainLocal runs the local normalization worker until it has nothing left:
// the background stream the server runs on its own.
func drainLocal(t *testing.T, db *pg.DB) {
	t.Helper()
	cfg := services.DefaultAuthorMetadataLocalWorkerConfig()
	worker, err := services.NewAuthorMetadataLocalWorker(db, &cfg)
	require.NoError(t, err)
	for range 20 {
		report, runErr := worker.RunOnce(context.Background())
		require.NoError(t, runErr)
		if report.Claimed == 0 && report.Reconciled == 0 {
			return
		}
	}
	t.Fatal("the local worker never ran out of work")
}

// Amendment 1, the flow Andrey named: an operator corrects a spelling of a
// name for all books (fingerprint scope), deletes the archive's books, and
// rescans the archive. The books come back and the correction applies to them
// again by itself — through the scanner and the background local worker, with
// no operator step.
func TestDeletedArchiveRescanReappliesFingerprintDecisions(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	scanner := services.NewBookScanService(dir, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)
	ids := scanfixture.Ingest(t, dir, time.Now(), scanner.ProcessBook)
	drainLocal(t, db)

	before := currentAuthorSelections(t, db, ids[scanfixture.EntryMultiAuthor])
	require.NotEmpty(t, before)
	fingerprint := before[0].Fingerprint
	var admin int64
	_, err := db.QueryOne(pg.Scan(&admin), `INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', true, 'reingest-admin', 'reingest-admin@fixture.local', now()) RETURNING id`)
	require.NoError(t, err)
	review, err := services.NewAuthorMetadataReviewService(db, services.DefaultAuthorMetadataReviewConfig())
	require.NoError(t, err)
	applied, err := review.ApplyOverride(context.Background(), database.OverrideScope{Fingerprint: fingerprint}, admin,
		&services.AdminCorrection{
			GivenName: "Александр", FamilyName: "Пушкин", DisplayName: "Александр Пушкин",
			SortName: "Пушкин, Александр", SearchKey: "пушкин александр", Script: "Cyrl", Kind: models.NormalizationKind("person"),
		})
	require.NoError(t, err)
	decided := currentAuthorSelections(t, db, ids[scanfixture.EntryMultiAuthor])[0]
	require.NotNil(t, decided.OverrideID)
	override, manual := *decided.OverrideID, *decided.ResultID
	_ = applied

	deleted, err := database.DeleteBooksByArchive(scanfixture.ArchiveName)
	require.NoError(t, err)
	require.Equal(t, len(ids), deleted)

	report, err := scanner.ScanArchive(filepath.Join(dir, scanfixture.ArchiveName))
	require.NoError(t, err)
	require.Equal(t, len(ids), report.BooksProcessed, "the archive's books come back")
	drainLocal(t, db)

	var back int64
	_, err = db.QueryOne(pg.Scan(&back), `SELECT id FROM opds_catalog_book WHERE path = ? AND filename = ?`,
		scanfixture.ArchiveName, scanfixture.EntryMultiAuthor)
	require.NoError(t, err)
	require.NotEqual(t, ids[scanfixture.EntryMultiAuthor], back, "a new book row")
	again := currentAuthorSelections(t, db, back)
	require.NotEmpty(t, again)
	assert.Equal(t, fingerprint, again[0].Fingerprint)
	require.NotNil(t, again[0].State)
	assert.Equal(t, "selected", *again[0].State)
	require.NotNil(t, again[0].OverrideID, "the correction applies to the returning book by itself")
	assert.Equal(t, override, *again[0].OverrideID)
	assert.Equal(t, manual, *again[0].ResultID)
}
