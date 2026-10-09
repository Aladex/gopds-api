package services_test

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task E2: every path that re-reads a book refreshes its author metadata
// layer through the same writer as ingest — an unchanged book is
// already_current, a changed one gets a new current snapshot and jobs, and an
// extraction failure never fails the legacy operation.

// writeEntries writes a zip archive with the given entries into dir.
func writeEntries(t *testing.T, dir, name string, entries map[string][]byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for entry, content := range entries {
		w, createErr := zw.Create(entry)
		require.NoError(t, createErr)
		_, err = w.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
}

// dropLayer removes the books' author metadata layer, as for books ingested
// before the layer existed.
func dropLayer(t *testing.T, db *pg.DB, ids ...int64) {
	t.Helper()
	_, err := db.Exec(`SELECT author_layer_delete_books(?::bigint[])`, pg.Array(ids))
	require.NoError(t, err)
	for _, id := range ids {
		require.Empty(t, readSourceSnapshots(t, db, id))
	}
}

func idsOf(m map[string]int64) []int64 {
	ids := make([]int64, 0, len(m))
	for _, id := range m {
		ids = append(ids, id)
	}
	return ids
}

func currentSnapshots(t *testing.T, db *pg.DB, id int64) (all, current int) {
	t.Helper()
	snaps := readSourceSnapshots(t, db, id)
	for i := range snaps {
		all++
		if snaps[i].IsCurrent {
			current++
		}
	}
	return all, current
}

func jobCount(t *testing.T, db *pg.DB) int {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM contributor_normalization_job`)
	require.NoError(t, err)
	return n
}

func TestFixScanRefreshesTheAuthorLayer(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	now := time.Now()
	ids := scanfixture.Ingest(t, dir, now, newCharacterizationScanner(t).ProcessBook)
	dropLayer(t, db, idsOf(ids)...)
	fix := services.NewFixScanService(dir, t.TempDir(), nil, nil)

	report, err := fix.RunFixScan(context.Background(), 1)
	require.NoError(t, err)
	require.Zero(t, report.ErrorCount, "%+v", report.Errors)
	assert.Equal(t, len(ids), report.UpdatedBooks)
	for entry, id := range ids {
		snaps := readSourceSnapshots(t, db, id)
		require.Len(t, snaps, 1, entry)
		assert.True(t, snaps[0].IsCurrent)
		assert.Equal(t, "live", snaps[0].Origin)
		assert.Equal(t, fixtureMD5(now, entry), snaps[0].BookMD5)
	}
	assert.Positive(t, jobCount(t, db))

	// Nothing changed: already current, no second snapshot.
	jobs := jobCount(t, db)
	_, err = fix.RunFixScan(context.Background(), 1)
	require.NoError(t, err)
	for _, id := range ids {
		all, current := currentSnapshots(t, db, id)
		assert.Equal(t, 1, all)
		assert.Equal(t, 1, current)
	}
	assert.Equal(t, jobs, jobCount(t, db))

	// The file changed on disk: a new current snapshot, the old one kept.
	books := scanfixture.Books(now)
	books[scanfixture.EntryMultiAuthor] = []byte(strings.Replace(string(books[scanfixture.EntryMultiAuthor]),
		"<author><last-name>пушкин</last-name></author>",
		"<author><last-name>пушкин</last-name></author><author><first-name>Иван</first-name><last-name>Новиков</last-name></author>", 1))
	writeEntries(t, dir, scanfixture.ArchiveName, books)
	_, err = fix.RunFixScan(context.Background(), 1)
	require.NoError(t, err)
	all, current := currentSnapshots(t, db, ids[scanfixture.EntryMultiAuthor])
	assert.Equal(t, 2, all)
	assert.Equal(t, 1, current)
	assert.Greater(t, jobCount(t, db), jobs, "the new source is queued")
	credits := readSourceCredits(t, db, ids[scanfixture.EntryMultiAuthor])
	found := false
	for _, c := range credits {
		found = found || (c.Last != nil && *c.Last == "Новиков")
	}
	assert.True(t, found, "the current snapshot carries the new author")
}

// The extraction fails on the document, the legacy update still happens, and
// the failure is reported by its closed class only.
func TestFixScanKeepsTheLegacyUpdateWhenExtractionFails(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	content := oversizedDescriptionFB2(time.Now())
	writeEntries(t, dir, "oversized.zip", map[string][]byte{"oversized.fb2": content})
	scanner := newCharacterizationScanner(t)
	id, err := scanner.ProcessBook(singleEntryArchive(t, "oversized.fb2", content), "oversized.zip")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE opds_catalog_book SET title = 'stale' WHERE id = ?`, id)
	require.NoError(t, err)

	report, err := services.NewFixScanService(dir, t.TempDir(), nil, nil).RunFixScan(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.UpdatedBooks, "the legacy update went through")
	book, _ := readLegacyBook(t, db, id)
	assert.Equal(t, "Скрытое название", book.Title)
	assert.Empty(t, readSourceSnapshots(t, db, id))
	require.Len(t, report.Errors, 1)
	assert.Equal(t, "author_metadata: "+string(services.AuthorMetadataParseFailed), report.Errors[0].Error)
	assert.Equal(t, id, report.Errors[0].BookID)
	for _, secret := range []string{"Секретный", "Писатель", "Скрытое"} {
		assert.NotContains(t, report.Errors[0].Error, secret)
	}
}

func TestApprovedRescanRefreshesTheAuthorLayer(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	now := time.Now()
	ids := scanfixture.Ingest(t, dir, now, newCharacterizationScanner(t).ProcessBook)
	approved, rejected := ids[scanfixture.EntryMultiAuthor], ids[scanfixture.EntryLatin]
	dropLayer(t, db, approved, rejected)
	rescan := services.NewRescanService(dir, t.TempDir(), nil)

	_, err := rescan.RescanBookPreview(approved, 0)
	require.NoError(t, err)
	assert.Empty(t, readSourceSnapshots(t, db, approved), "a preview writes nothing")
	_, err = rescan.ApproveRescan(approved, nil)
	require.NoError(t, err)
	snaps := readSourceSnapshots(t, db, approved)
	require.Len(t, snaps, 1)
	assert.True(t, snaps[0].IsCurrent)
	assert.Equal(t, fixtureMD5(now, scanfixture.EntryMultiAuthor), snaps[0].BookMD5)

	_, err = rescan.RescanBookPreview(rejected, 0)
	require.NoError(t, err)
	_, err = rescan.RejectRescan(rejected)
	require.NoError(t, err)
	assert.Empty(t, readSourceSnapshots(t, db, rejected), "a rejected rescan writes nothing")

	// Approving again with nothing changed: already current.
	_, err = rescan.RescanBookPreview(approved, 0)
	require.NoError(t, err)
	_, err = rescan.ApproveRescan(approved, nil)
	require.NoError(t, err)
	assert.Len(t, readSourceSnapshots(t, db, approved), 1)
}

// An archive rescan reads every entry; an unchanged book of that archive is
// skipped as a duplicate on the legacy side, and its author layer is
// refreshed from the bytes the scan read.
func TestArchiveRescanRefreshesItsUnchangedBooks(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	now := time.Now()
	scanner := services.NewBookScanService(dir, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)
	ids := scanfixture.Ingest(t, dir, now, scanner.ProcessBook)
	dropLayer(t, db, idsOf(ids)...)

	report, err := scanner.ScanArchive(filepath.Join(dir, scanfixture.ArchiveName))
	require.NoError(t, err)
	assert.Equal(t, len(ids), report.BooksSkipped, "no legacy book is added")
	for entry, id := range ids {
		snaps := readSourceSnapshots(t, db, id)
		require.Len(t, snaps, 1, entry)
		assert.True(t, snaps[0].IsCurrent)
	}
	_, err = scanner.ScanArchive(filepath.Join(dir, scanfixture.ArchiveName))
	require.NoError(t, err)
	for _, id := range ids {
		assert.Len(t, readSourceSnapshots(t, db, id), 1, "already current")
	}
}

// Review B8 and the reviewer's probe, kept: every reread path keeps its legacy
// outcome when the source write fails, writes no partial snapshot, and reports
// the closed class — the approved rescan through its response, for the scan
// errors list.
func TestRescansKeepTheLegacyOutcomeWhenTheSourceWriteFails(t *testing.T) {
	for _, mode := range []string{"fix", "archive", "approved"} {
		t.Run(mode, func(t *testing.T) {
			db := scanfixture.ScratchDB(t)
			dir := t.TempDir()
			scanner := newCharacterizationScanner(t)
			ids := scanfixture.Ingest(t, dir, time.Now(), scanner.ProcessBook)
			dropLayer(t, db, idsOf(ids)...)
			id := ids[scanfixture.EntryMultiAuthor]
			_, err := db.Exec(`UPDATE opds_catalog_book SET title = 'stale' WHERE id = ?`, id)
			require.NoError(t, err)
			rescan := services.NewRescanService(dir, t.TempDir(), nil)
			if mode == "approved" {
				_, err = rescan.RescanBookPreview(id, 0)
				require.NoError(t, err)
			}
			_, err = db.Exec(`CREATE FUNCTION fail_source_insert() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'privacy_canary_name' USING ERRCODE = 'XX001'; END $$;
				CREATE TRIGGER fail_source_insert BEFORE INSERT ON book_metadata_snapshot
				FOR EACH ROW EXECUTE FUNCTION fail_source_insert()`)
			require.NoError(t, err)

			switch mode {
			case "fix":
				r, fixErr := services.NewFixScanService(dir, t.TempDir(), nil, nil).RunFixScan(context.Background(), 1)
				require.NoError(t, fixErr)
				require.Equal(t, len(ids), r.UpdatedBooks)
				require.NotEmpty(t, r.Errors)
				assert.Equal(t, "author_metadata: author_metadata_unavailable", r.Errors[0].Error)
			case "archive":
				s := services.NewBookScanService(dir, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)
				r, scanErr := s.ScanArchive(filepath.Join(dir, scanfixture.ArchiveName))
				require.NoError(t, scanErr)
				require.Equal(t, len(ids), r.BooksSkipped)
				require.NotEmpty(t, r.Errors)
				assert.Equal(t, "author_metadata: author_metadata_unavailable", r.Errors[0].Error)
			case "approved":
				resp, approveErr := rescan.ApproveRescan(id, nil)
				require.NoError(t, approveErr)
				require.NotNil(t, resp.AuthorMetadataFailure)
				assert.Equal(t, services.AuthorMetadataRefreshUnavailable, resp.AuthorMetadataFailure.Class)
				assert.Equal(t, scanfixture.EntryMultiAuthor, resp.AuthorMetadataFailure.Entry)
			}
			assert.Empty(t, readSourceSnapshots(t, db, id))
			if mode != "archive" {
				book, _ := readLegacyBook(t, db, id)
				assert.NotEqual(t, "stale", book.Title)
			}
		})
	}
}

// The reviewer's probe, kept: a changed file approved after its archive was
// removed refreshes from the bytes the preview kept — no second archive read.
func TestChangedApprovedRescanNeedsNoSecondArchiveRead(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	dir := t.TempDir()
	now := time.Now()
	ids := scanfixture.Ingest(t, dir, now, newCharacterizationScanner(t).ProcessBook)
	id := ids[scanfixture.EntryMultiAuthor]
	books := scanfixture.Books(now)
	books[scanfixture.EntryMultiAuthor] = append(books[scanfixture.EntryMultiAuthor], []byte("\n ")...)
	writeEntries(t, dir, scanfixture.ArchiveName, books)
	rescan := services.NewRescanService(dir, t.TempDir(), nil)
	_, err := rescan.RescanBookPreview(id, 0)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, scanfixture.ArchiveName)))

	_, err = rescan.ApproveRescan(id, nil)
	require.NoError(t, err)
	all, current := currentSnapshots(t, db, id)
	assert.Equal(t, 2, all)
	assert.Equal(t, 1, current)
}
