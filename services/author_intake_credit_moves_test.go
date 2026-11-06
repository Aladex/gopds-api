package services_test

import (
	"archive/zip"
	"context"
	"crypto/md5" // #nosec G501 -- the catalog's own duplicate fingerprint
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// creditMoveState is everything a hand move consists of: the credit row and
// the assignment that names the new card.
type creditMoveState struct {
	Credits     int
	Assignments int
	Snapshot    int64
}

func (f *moveFixture) moveState(credit int64) creditMoveState {
	f.c.t.Helper()
	var st creditMoveState
	_, err := f.c.db.QueryOne(pg.Scan(&st.Credits, &st.Assignments, &st.Snapshot), `SELECT
		(SELECT count(*) FROM book_contributor_credit WHERE id = ?0),
		(SELECT count(*) FROM author_entity_credit_assignment WHERE credit_id = ?0),
		(SELECT coalesce(max(snapshot_id), 0) FROM book_contributor_credit WHERE id = ?0)`, credit)
	require.NoError(f.c.t, err)
	return st
}

// giveBookContent makes the book of a credit the catalog's record of content:
// its MD5 becomes that of the bytes, and its archive entry is returned.
func (f *moveFixture) giveBookContent(credit int64, content []byte) (archive, entry string) {
	f.c.t.Helper()
	sum := md5.Sum(content) // #nosec G401 -- the catalog's own duplicate fingerprint
	f.c.exec(`UPDATE opds_catalog_book SET md5 = ? WHERE id = (SELECT s.book_id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE c.id = ?)`, hex.EncodeToString(sum[:]), credit)
	_, err := f.c.db.QueryOne(pg.Scan(&archive, &entry), `SELECT b.path, b.filename FROM opds_catalog_book b
		JOIN book_metadata_snapshot s ON s.book_id = b.id JOIN book_contributor_credit c ON c.snapshot_id = s.id
		WHERE c.id = ?`, credit)
	require.NoError(f.c.t, err)
	return archive, entry
}

// writeZip writes entries to dir/name.
func writeZip(t *testing.T, dir, name string, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// #nosec G304 -- a test file in the test's own directory
	file, err := os.Create(path)
	require.NoError(t, err)
	w := zip.NewWriter(file)
	for entry, content := range entries {
		out, createErr := w.Create(entry)
		require.NoError(t, createErr)
		_, writeErr := out.Write(content)
		require.NoError(t, writeErr)
	}
	require.NoError(t, w.Close())
	require.NoError(t, file.Close())
	return path
}

// intakeScanner is the scanner as production has it: skip_duplicates on.
func intakeScanner(t *testing.T, dir string) *services.BookScanService {
	t.Helper()
	return services.NewBookScanService(dir, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)
}

// A new archive cannot reach an old book: even when it carries the very bytes
// of a book a hand move sits on, the book is a different archive's and is left
// alone, so the move, its credit and its snapshot stay. The pass after the
// scan keeps them too.
func TestANewArchiveLeavesAHandMoveAlone(t *testing.T) {
	f := newMoveFixture(t)
	ctx := context.Background()
	card, err := f.admin.MoveCredits(ctx, &database.CreditMove{Credits: f.credits[:1],
		NewName: "Алексей Константинович Толстой"})
	require.NoError(t, err)
	books := scanfixture.Books(time.Now())
	same := books[scanfixture.EntryMultiAuthor]
	f.giveBookContent(f.credits[0], same)
	before := f.moveState(f.credits[0])
	require.Equal(t, 1, before.Credits)
	require.Positive(t, before.Assignments)

	dir := t.TempDir()
	path := writeZip(t, dir, "fb2-new.zip", map[string][]byte{"same.fb2": same,
		scanfixture.EntryLatin: books[scanfixture.EntryLatin]})
	report, err := intakeScanner(t, dir).ScanArchive(path)
	require.NoError(t, err)
	require.Equal(t, 1, report.BooksSkipped, "the copy of the old book was skipped as a duplicate")
	require.Equal(t, 1, report.BooksProcessed, "the new book went in")
	require.Empty(t, report.Errors)

	assert.Equal(t, before, f.moveState(f.credits[0]))
	assert.Equal(t, card.String(), f.entityOfCredit(f.credits[0]))

	f.c.build()
	assert.Equal(t, before, f.moveState(f.credits[0]))
	assert.Equal(t, card.String(), f.entityOfCredit(f.credits[0]))
}

// The scan a crash makes the job repeat: a second pass over an archive finds
// its books unchanged and keeps their snapshots and credit rows, the rows hand
// moves hang from (a moved credit dies only with its row, by FK cascade).
func TestARepeatedScanKeepsTheCreditRows(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	books := scanfixture.Books(time.Now())
	path := writeZip(t, dir, "fb2-new.zip", map[string][]byte{scanfixture.EntryLatin: books[scanfixture.EntryLatin]})
	scanner := intakeScanner(t, dir)
	report, err := scanner.ScanArchive(path)
	require.NoError(t, err)
	require.Equal(t, 1, report.BooksProcessed)

	rows := func() []int64 {
		var ids []int64
		_, queryErr := db.Query(&ids, `SELECT c.id FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE s.is_current ORDER BY c.id`)
		require.NoError(t, queryErr)
		return ids
	}
	before := rows()
	require.NotEmpty(t, before)

	report, err = scanner.ScanArchive(path)
	require.NoError(t, err)
	require.Equal(t, 1, report.BooksSkipped, "the book is its own duplicate on the second pass")
	assert.Equal(t, before, rows())
}
