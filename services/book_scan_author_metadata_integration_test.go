package services_test

import (
	"archive/zip"
	"bytes"
	"crypto/md5" // #nosec G501 -- the scan path's duplicate fingerprint, pinned here, not a security use
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Part A of phase 9: characterization of what the new-book scan path writes to
// the legacy reader model today. These pins must hold unchanged once ProcessBook
// also writes the author metadata source layer in the same transaction.

// legacyBook is one opds_catalog_book row as ProcessBook leaves it, apart from
// the ID and the register date, which are checked separately.
type legacyBook struct {
	Filename        string
	Path            string
	Format          string
	Docdate         string
	Lang            string
	Title           string
	Annotation      string
	Cover           bool
	Approved        bool
	Md5             string
	DuplicateHidden bool
	DuplicateOfID   *int64
}

// legacyLinks are the book's junction rows in insertion order.
type legacyLinks struct {
	Authors []string
	Series  []legacySeries
	Genres  []legacyGenre
}

type legacySeries struct {
	Ser   string
	SerNo int
}

type legacyGenre struct {
	Genre string
	Title string
}

// newCharacterizationScanner builds the scanner the way api/book_scan.go does
// with language detection on and OpenAI off. The LLM service is nil: without
// an API key the production one also returns the genre tag as its title, and
// a test must not reach the network.
func newCharacterizationScanner(t *testing.T) *services.BookScanService {
	t.Helper()
	return services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
}

func readLegacyBook(t *testing.T, db *pg.DB, id int64) (legacyBook, time.Time) {
	t.Helper()
	var row struct {
		legacyBook
		Registerdate time.Time
	}
	_, err := db.QueryOne(&row, `
		SELECT filename, path, format, docdate, lang, title, annotation, cover, approved, md5,
			duplicate_hidden, duplicate_of_id, registerdate
		FROM opds_catalog_book WHERE id = ?`, id)
	require.NoError(t, err)
	return row.legacyBook, row.Registerdate
}

func readLegacyLinks(t *testing.T, db *pg.DB, id int64) legacyLinks {
	t.Helper()
	var links legacyLinks
	_, err := db.Query(&links.Authors, `
		SELECT a.full_name FROM opds_catalog_bauthor ba
		JOIN opds_catalog_author a ON a.id = ba.author_id
		WHERE ba.book_id = ? ORDER BY ba.id`, id)
	require.NoError(t, err)
	_, err = db.Query(&links.Series, `
		SELECT s.ser, bs.ser_no FROM opds_catalog_bseries bs
		JOIN opds_catalog_series s ON s.id = bs.ser_id
		WHERE bs.book_id = ? ORDER BY bs.id`, id)
	require.NoError(t, err)
	_, err = db.Query(&links.Genres, `
		SELECT g.genre, g.title FROM opds_catalog_bgenre bg
		JOIN opds_catalog_genre g ON g.id = bg.genre_id
		WHERE bg.book_id = ? ORDER BY bg.id`, id)
	require.NoError(t, err)
	return links
}

func fixtureMD5(now time.Time, entry string) string {
	// #nosec G401 -- recomputing the scan path's duplicate fingerprint
	sum := md5.Sum(scanfixture.Books(now)[entry])
	return hex.EncodeToString(sum[:])
}

// TestScanCharacterizationLegacyRows pins the legacy rows ProcessBook writes
// for each characterization book: the book itself, its authors in link order
// with the legacy "Last First" case rule and the 'Автор неизвестен' fallback,
// its series and number, its genres, and the detected book.lang.
func TestScanCharacterizationLegacyRows(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := newCharacterizationScanner(t)
	now := time.Now()
	year := strconv.Itoa(now.Year())

	before := time.Now()
	ids := scanfixture.Ingest(t, t.TempDir(), now, scanner.ProcessBook)
	after := time.Now()
	require.Len(t, ids, 3)

	cases := []struct {
		entry string
		book  legacyBook
		links legacyLinks
	}{
		{
			entry: scanfixture.EntryMultiAuthor,
			book: legacyBook{
				Filename: scanfixture.EntryMultiAuthor, Path: scanfixture.ArchiveName, Format: "fb2",
				Docdate: year, Lang: "ru", Title: "война и МИР", Annotation: "Роман-эпопея о войне и мире.",
				Approved: true, Md5: fixtureMD5(now, scanfixture.EntryMultiAuthor),
			},
			links: legacyLinks{
				// The legacy parser pairs first and last names by index across
				// authors and re-cases only mostly-upper tokens: the second pair
				// is the third author's first name with the second's last name,
				// the nickname-only author is dropped.
				Authors: []string{"Толстой лев", "пушкин Анна"},
				Series:  []legacySeries{{Ser: "Эпопея", SerNo: 2}},
				Genres:  []legacyGenre{{Genre: "prose_classic", Title: "prose_classic"}, {Genre: "prose_history", Title: "prose_history"}},
			},
		},
		{
			entry: scanfixture.EntryNoAuthor,
			book: legacyBook{
				Filename: scanfixture.EntryNoAuthor, Path: scanfixture.ArchiveName, Format: "fb2",
				Docdate: year, Lang: "ru", Title: "Сборник без автора",
				Approved: true, Md5: fixtureMD5(now, scanfixture.EntryNoAuthor),
			},
			links: legacyLinks{
				Authors: []string{"Автор неизвестен"},
				Genres:  []legacyGenre{{Genre: "antology", Title: "antology"}},
			},
		},
		{
			entry: scanfixture.EntryLatin,
			book: legacyBook{
				Filename: scanfixture.EntryLatin, Path: scanfixture.ArchiveName, Format: "fb2",
				Docdate: year, Lang: "en", Title: "the GREAT book",
				Approved: true, Md5: fixtureMD5(now, scanfixture.EntryLatin),
			},
			links: legacyLinks{
				Authors: []string{"O'brien jOHN", "толстой ЛЕВ"},
				Series:  []legacySeries{{Ser: "Saga", SerNo: 0}},
				Genres:  []legacyGenre{{Genre: "sf", Title: "sf"}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.entry, func(t *testing.T) {
			book, registered := readLegacyBook(t, db, ids[c.entry])
			assert.Equal(t, c.book, book)
			assert.False(t, registered.Before(before.Add(-time.Second)) || registered.After(after.Add(time.Second)),
				"register date %v is the ingestion time", registered)
			assert.Equal(t, c.links, readLegacyLinks(t, db, ids[c.entry]))
		})
	}

	t.Run("authors are shared by exact full name only", func(t *testing.T) {
		var names []string
		_, err := db.Query(&names, `SELECT full_name FROM opds_catalog_author ORDER BY id`)
		require.NoError(t, err)
		// "Толстой лев" and "толстой ЛЕВ" stay two author rows: get-or-create
		// matches the full name exactly.
		assert.Equal(t, []string{"Толстой лев", "пушкин Анна", "Автор неизвестен", "O'brien jOHN", "толстой ЛЕВ"}, names)
	})
}

// Part B of phase 9: the transactional dual write. ProcessBook writes the
// legacy rows and the live source snapshot, its credits and its local jobs in
// one commit, from the same FB2 bytes; the Part A pins above stay unchanged.

// sourceSnapshot is one stored snapshot as the backfill and the normalizer
// read it.
type sourceSnapshot struct {
	BookID           int64
	BookMD5          string
	ExtractorVersion string
	Origin           string
	RunID            *int64
	Outcome          string
	ArchivePath      string
	EntryName        string
	IsCurrent        bool
	SourceTitle      *string
	SourceLang       *string
	SourceSrcLang    *string
	DocumentID       *string
	DocumentVersion  *string
	Sequences        string
}

// sourceCredit is one stored credit.
type sourceCredit struct {
	Role         string
	Position     int
	First        *string
	Middle       *string
	Last         *string
	Nickname     *string
	Display      string
	QualityFlags []string `pg:",array"`
}

func readSourceSnapshots(t *testing.T, db *pg.DB, bookID int64) []sourceSnapshot {
	t.Helper()
	var rows []sourceSnapshot
	_, err := db.Query(&rows, `SELECT book_id, book_md5, extractor_version, origin, run_id, outcome,
			archive_path, entry_name, is_current, source_title, source_lang, source_src_lang,
			source_document_id AS document_id, source_document_version AS document_version,
			source_sequences::text AS sequences
		FROM book_metadata_snapshot WHERE book_id = ? ORDER BY id`, bookID)
	require.NoError(t, err)
	return rows
}

func readSourceCredits(t *testing.T, db *pg.DB, bookID int64) []sourceCredit {
	t.Helper()
	var rows []sourceCredit
	_, err := db.Query(&rows, `SELECT c.role, c.position, c.source_first_name AS first,
			c.source_middle_name AS middle, c.source_last_name AS last, c.source_nickname AS nickname,
			c.source_display_name AS display, c.quality_flags
		FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE s.book_id = ? ORDER BY c.role, c.position`, bookID)
	require.NoError(t, err)
	return rows
}

func sp(s string) *string { return &s }

func scalarCount(t *testing.T, db *pg.DB, query string, params ...interface{}) int {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), query, params...)
	require.NoError(t, err)
	return n
}

// Plan RED 1, 3, 4 and 5: one ProcessBook call leaves the legacy rows pinned
// above and the live source rows — raw order, case, middle names, nicknames
// and translators, extracted_no_author for the book without an author, the
// declared language kept in the snapshot while book.lang keeps the detector's
// result.
func TestScanDualWriteSourceRows(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := newCharacterizationScanner(t)
	now := time.Now()
	ids := scanfixture.Ingest(t, t.TempDir(), now, scanner.ProcessBook)

	none := []string{}
	cases := []struct {
		entry    string
		snapshot sourceSnapshot
		credits  []sourceCredit
		jobs     int
	}{
		{
			entry: scanfixture.EntryMultiAuthor,
			snapshot: sourceSnapshot{
				Outcome: "extracted", SourceTitle: sp("война  и   МИР"), SourceLang: sp("ru"), SourceSrcLang: sp("fr"),
				DocumentID: sp("characterization-multi"), DocumentVersion: sp("1.0"),
				Sequences: `[{"name": "Эпопея", "index": 0, "number": "2", "parent": -1}]`,
			},
			credits: []sourceCredit{
				{Role: "author", Position: 0, First: sp("лев"), Middle: sp("Николаевич"), Last: sp("ТОЛСТОЙ"),
					Display: "лев Николаевич ТОЛСТОЙ", QualityFlags: none},
				{Role: "author", Position: 1, Last: sp("пушкин"), Display: "пушкин", QualityFlags: none},
				{Role: "author", Position: 2, First: sp("Анна"), Display: "Анна", QualityFlags: none},
				{Role: "author", Position: 3, Nickname: sp("Аноним"), Display: "Аноним", QualityFlags: none},
				{Role: "translator", Position: 0, First: sp("Пётр"), Last: sp("Переводчиков"),
					Display: "Пётр Переводчиков", QualityFlags: none},
			},
			jobs: 4,
		},
		{
			// The legacy side keeps 'Автор неизвестен' (pinned above); the
			// source side records the truth: no author, the translator only,
			// and never the document-info author.
			entry: scanfixture.EntryNoAuthor,
			snapshot: sourceSnapshot{
				Outcome: "extracted_no_author", SourceTitle: sp("Сборник без автора"), SourceLang: sp("ru"),
				DocumentID: sp("characterization-no-author"), Sequences: `[]`,
			},
			credits: []sourceCredit{
				{Role: "translator", Position: 0, First: sp("Мария"), Last: sp("Переводова"),
					Display: "Мария Переводова", QualityFlags: none},
			},
		},
		{
			// Declared ru, detected en: the snapshot keeps the declaration,
			// book.lang (pinned above) keeps the detector's en.
			entry: scanfixture.EntryLatin,
			snapshot: sourceSnapshot{
				Outcome: "extracted", SourceTitle: sp("the GREAT book"), SourceLang: sp("ru"),
				DocumentID: sp("characterization-latin"),
				Sequences:  `[{"name": "Saga", "index": 0, "number": "first", "parent": -1}]`,
			},
			credits: []sourceCredit{
				{Role: "author", Position: 0, First: sp("jOHN"), Last: sp("o'BRIEN"), Display: "jOHN o'BRIEN", QualityFlags: none},
				{Role: "author", Position: 1, First: sp("ЛЕВ"), Last: sp("толстой"), Display: "ЛЕВ толстой", QualityFlags: none},
			},
			jobs: 2,
		},
	}
	for _, c := range cases {
		t.Run(c.entry, func(t *testing.T) {
			id := ids[c.entry]
			want := c.snapshot
			want.BookID, want.BookMD5 = id, fixtureMD5(now, c.entry)
			want.ExtractorVersion, want.Origin, want.RunID = services.AuthorMetadataExtractorVersion, "live", nil
			want.ArchivePath, want.EntryName, want.IsCurrent = scanfixture.ArchiveName, c.entry, true

			assert.Equal(t, []sourceSnapshot{want}, readSourceSnapshots(t, db, id))
			assert.Equal(t, c.credits, readSourceCredits(t, db, id))

			book, _ := readLegacyBook(t, db, id)
			assert.Equal(t, book.Md5, want.BookMD5, "one file identity on both sides")
		})
	}

	t.Run("one local job per distinct author key, none for translators", func(t *testing.T) {
		assert.Equal(t, 6, scalarCount(t, db, `SELECT count(*) FROM contributor_normalization_job`))
		assert.Equal(t, 6, scalarCount(t, db, `SELECT count(DISTINCT c.source_fingerprint)
			FROM book_contributor_credit c WHERE c.role = 'author'`))
	})
	t.Run("every snapshot names an ingested book", func(t *testing.T) {
		assert.Equal(t, 0, scalarCount(t, db, `SELECT count(*) FROM book_metadata_snapshot
			WHERE book_id NOT IN (?, ?, ?)`, ids[scanfixture.EntryMultiAuthor], ids[scanfixture.EntryNoAuthor],
			ids[scanfixture.EntryLatin]))
	})
}

// openFixtureEntry writes the characterization archive and returns one entry.
func openFixtureEntry(t *testing.T, now time.Time, entry string) *zip.File {
	t.Helper()
	reader, err := zip.OpenReader(scanfixture.WriteArchive(t, t.TempDir(), now))
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	for _, f := range reader.File {
		if f.Name == entry {
			return f
		}
	}
	t.Fatalf("fixture entry %s not found", entry)
	return nil
}

// catalogTables are every table a new book can add rows to, legacy and source.
var catalogTables = []string{
	"opds_catalog_book", "opds_catalog_author", "opds_catalog_bauthor", "opds_catalog_series",
	"opds_catalog_bseries", "opds_catalog_genre", "opds_catalog_bgenre",
	"book_metadata_snapshot", "book_contributor_credit", "contributor_normalization_job",
}

func catalogCounts(t *testing.T, db *pg.DB) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(catalogTables))
	for _, table := range catalogTables {
		counts[table] = scalarCount(t, db, "SELECT count(*) FROM "+table)
	}
	return counts
}

// Plan RED 2 and the plan's mutation: a failure in any source insert, after
// the legacy book, authors, series and genres were written in the same
// transaction, leaves zero rows on both sides. The failure is injected in
// the database itself, so the real repository and transaction run.
func TestScanDualWriteIsAtomic(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := newCharacterizationScanner(t)
	now := time.Now()

	_, err := db.Exec(`CREATE FUNCTION inject_source_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected source failure'; END $$`)
	require.NoError(t, err)

	for _, table := range []string{"book_metadata_snapshot", "book_contributor_credit", "contributor_normalization_job"} {
		t.Run(table, func(t *testing.T) {
			_, err := db.Exec(`CREATE TRIGGER inject_source_failure BEFORE INSERT ON ` + table +
				` FOR EACH ROW EXECUTE FUNCTION inject_source_failure()`)
			require.NoError(t, err)
			t.Cleanup(func() {
				_, dropErr := db.Exec(`DROP TRIGGER inject_source_failure ON ` + table)
				require.NoError(t, dropErr)
			})
			before := catalogCounts(t, db)

			id, err := scanner.ProcessBook(openFixtureEntry(t, now, scanfixture.EntryMultiAuthor), scanfixture.ArchiveName)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "injected source failure")
			assert.Zero(t, id)
			assert.Equal(t, before, catalogCounts(t, db), "no legacy and no source rows survive")
		})
	}

	t.Run("the same book ingests once the failure is gone", func(t *testing.T) {
		id, err := scanner.ProcessBook(openFixtureEntry(t, now, scanfixture.EntryMultiAuthor), scanfixture.ArchiveName)
		require.NoError(t, err)
		assert.Len(t, readSourceSnapshots(t, db, id), 1)
		assert.Equal(t, []string{"Толстой лев", "пушкин Анна"}, readLegacyLinks(t, db, id).Authors)
	})
}

// oversizedDescriptionFB2 is a book the legacy parser accepts whose
// description exceeds the extractor's metadata byte limit: a custom-info
// block, which the legacy parser never reads, larger than
// AuthorMetadataMaxBytes.
func oversizedDescriptionFB2(now time.Time) []byte {
	line := strings.Repeat("служебные данные ", 64) + "\n"
	padding := strings.Repeat(line, int(services.AuthorMetadataMaxBytes)/len(line)+1)
	return []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<genre>prose</genre>
<author><first-name>Секретный</first-name><last-name>Писатель</last-name></author>
<book-title>Скрытое название</book-title>
<lang>ru</lang>
</title-info>
<document-info><date>` + strconv.Itoa(now.Year()) + `</date><id>oversized</id></document-info>
<custom-info info-type="padding">` + padding + `</custom-info>
</description>
<body><section><p>Короткий текст книги на русском языке для определения языка.</p></section></body>
</FictionBook>
`)
}

func singleEntryArchive(t *testing.T, name string, content []byte) *zip.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "single.zip")
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	w, err := zw.Create(name)
	require.NoError(t, err)
	_, err = w.Write(content)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	reader, err := zip.OpenReader(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	return reader.File[0]
}

// captureLogs redirects the shared logger into a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logger := logging.GetLogger()
	previous := logger.Out
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(previous) })
	return &buf
}

// Decision recorded in the report: when only the extraction fails, the legacy
// book ingests exactly as before, without a snapshot; the log names the book
// ID and the closed class, never a name or a title.
func TestScanDualWriteSkipsOnlyAFailedExtraction(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := newCharacterizationScanner(t)
	logs := captureLogs(t)

	id, err := scanner.ProcessBook(singleEntryArchive(t, "oversized.fb2", oversizedDescriptionFB2(time.Now())), "oversized.zip")

	require.NoError(t, err)
	require.NotZero(t, id)
	assert.Equal(t, []string{"Писатель Секретный"}, readLegacyLinks(t, db, id).Authors)
	assert.Empty(t, readSourceSnapshots(t, db, id))

	var skipped []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, string(services.AuthorMetadataEventSourceSkipped)) {
			skipped = append(skipped, line)
		}
	}
	require.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "level=warning")
	assert.Contains(t, skipped[0], "book_id="+strconv.FormatInt(id, 10))
	assert.Contains(t, skipped[0], "status="+string(services.AuthorMetadataParseFailed))
	for _, secret := range []string{"Секретный", "Писатель", "Скрытое", "oversized"} {
		assert.NotContains(t, skipped[0], secret)
	}
}

// Plan RED 6: an administrator's edit of the legacy authors re-links the
// book's legacy authors and leaves the source snapshot and its credits as
// they were.
func TestScanDualWriteSourceSurvivesAdminAuthorEdit(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := newCharacterizationScanner(t)
	ids := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)
	id := ids[scanfixture.EntryMultiAuthor]
	snapshotsBefore := readSourceSnapshots(t, db, id)
	creditsBefore := readSourceCredits(t, db, id)
	require.Len(t, snapshotsBefore, 1, "precondition: the dual write stored the source side")
	require.Len(t, creditsBefore, 5)

	_, err := database.UpdateBook(models.BookUpdateRequest{ID: id, Authors: []models.Author{{FullName: "Исправленный Автор"}}})
	require.NoError(t, err)

	assert.Equal(t, []string{"Исправленный Автор"}, readLegacyLinks(t, db, id).Authors)
	assert.Equal(t, snapshotsBefore, readSourceSnapshots(t, db, id))
	assert.Equal(t, creditsBefore, readSourceCredits(t, db, id))
}
