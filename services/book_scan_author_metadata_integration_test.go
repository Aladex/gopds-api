package services_test

import (
	"crypto/md5" // #nosec G501 -- the scan path's duplicate fingerprint, pinned here, not a security use
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
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
