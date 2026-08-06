package database

import (
	"fmt"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchFixture proves the rollback fixture: rows it seeds are visible
// through the transaction it hands out, and none of them survives the test.
func TestSearchFixture(t *testing.T) {
	requireDatabase(t)

	t.Run("seeded rows are visible through the fixture DBI", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			var books int
			_, err := f.tx.QueryOne(pg.Scan(&books),
				`SELECT count(*) FROM opds_catalog_book WHERE id >= ?`, searchFixtureIDBase)
			require.NoError(t, err)
			// 510 equal-title regression rows plus the individually seeded ones.
			assert.GreaterOrEqual(t, books, 511)

			var authors int
			_, err = f.tx.QueryOne(pg.Scan(&authors),
				`SELECT count(*) FROM opds_catalog_author WHERE id >= ?`, searchFixtureIDBase)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, authors, 3)

			var seriesLinks int
			_, err = f.tx.QueryOne(pg.Scan(&seriesLinks), `
				SELECT count(*) FROM opds_catalog_bseries bs
				JOIN opds_catalog_book b ON b.id = bs.book_id
				WHERE b.id >= ?`, searchFixtureIDBase)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, seriesLinks, 1)

			var favs int
			_, err = f.tx.QueryOne(pg.Scan(&favs), `
				SELECT count(*) FROM favorite_books fb
				JOIN opds_catalog_book b ON b.id = fb.book_id
				WHERE b.id >= ?`, searchFixtureIDBase)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, favs, 1)

			var curatedItems int
			_, err = f.tx.QueryOne(pg.Scan(&curatedItems), `
				SELECT count(*) FROM book_collection_items ci
				JOIN book_collections c ON c.id = ci.collection_id
				WHERE c.id >= ?`, searchFixtureIDBase)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, curatedItems, 1)
		})
	})

	t.Run("rollback removes every seeded row", func(t *testing.T) {
		for _, table := range []string{
			"opds_catalog_book",
			"opds_catalog_author",
			"opds_catalog_series",
			"opds_catalog_genre",
			"auth_user",
			"book_collections",
		} {
			var count int
			_, err := db.QueryOne(pg.Scan(&count),
				`SELECT count(*) FROM `+table+` WHERE id >= ?`, searchFixtureIDBase)
			require.NoError(t, err, table)
			assert.Zero(t, count, table)
		}
	})
}

// searchFixtureIDBase starts the fixture-owned ID range: far above every real
// catalogue ID, far below the int4 ceiling, and identical across runs because
// rollback always removes the previous batch.
const searchFixtureIDBase int64 = 2_147_000_000

// searchFixture seeds a small deterministic catalogue inside a transaction
// that is rolled back when the test ends. Every row carries an ID from the
// fixture range, so seeded data never collides with the restored dump and
// never leaks between runs. Rows are written through the fixture's own
// transaction only — never through the package-global handle — which is what
// makes the rollback complete.
type searchFixture struct {
	t      *testing.T
	tx     *pg.Tx
	now    time.Time
	nextID int64

	BookIDs       map[string]int64
	AuthorIDs     map[string]int64
	SeriesIDs     map[string]int64
	GenreIDs      map[string]int64
	UserIDs       map[string]int64
	CollectionIDs map[string]int64
}

// fixtureBook is one book to seed. Authors, Series and Genres carry fixture
// IDs to link through the junction tables.
type fixtureBook struct {
	Title    string
	Lang     string
	Approved bool
	Hidden   bool
	Authors  []int64
	Series   []int64
	Genres   []int64
}

// withSearchFixture seeds the standard search catalogue and runs fn with it.
// The transaction rolls back when the calling test finishes.
func withSearchFixture(t *testing.T, fn func(*searchFixture)) {
	t.Helper()
	requireDatabase(t)

	tx, err := db.Begin()
	require.NoError(t, err, "beginning the fixture transaction")
	t.Cleanup(func() {
		_ = tx.Rollback()
		// Code under test committing the fixture transaction turns this
		// rollback into a no-op and settles fixture rows into the real
		// catalogue — go-pg v10 (*Tx).RunInTransaction did exactly that.
		// Check the leak directly instead of trusting the rollback.
		for _, table := range []string{
			"opds_catalog_book",
			"opds_catalog_author",
			"auth_user",
		} {
			var leaked int
			_, err := db.QueryOne(pg.Scan(&leaked),
				`SELECT count(*) FROM `+table+` WHERE id >= ?`, searchFixtureIDBase)
			require.NoError(t, err, table)
			assert.Zero(t, leaked, "fixture rows leaked past the rollback into "+table)
		}
	})

	f := &searchFixture{
		t:             t,
		tx:            tx,
		now:           time.Now(),
		nextID:        searchFixtureIDBase,
		BookIDs:       map[string]int64{},
		AuthorIDs:     map[string]int64{},
		SeriesIDs:     map[string]int64{},
		GenreIDs:      map[string]int64{},
		UserIDs:       map[string]int64{},
		CollectionIDs: map[string]int64{},
	}
	seedSearchCatalog(f)
	fn(f)
}

func (f *searchFixture) id() int64 {
	id := f.nextID
	f.nextID++
	return id
}

func (f *searchFixture) exec(query string, args ...interface{}) {
	f.t.Helper()
	_, err := f.tx.Exec(query, args...)
	require.NoError(f.t, err, query)
}

// Author seeds one author row and remembers it under key.
func (f *searchFixture) Author(key, fullName string) int64 {
	f.t.Helper()
	id := f.id()
	f.exec(`INSERT INTO opds_catalog_author (id, full_name) VALUES (?, ?)`, id, fullName)
	f.AuthorIDs[key] = id
	return id
}

// Series seeds one series row and remembers it under key.
func (f *searchFixture) Series(key, ser string) int64 {
	f.t.Helper()
	id := f.id()
	f.exec(`INSERT INTO opds_catalog_series (id, ser, lang_code) VALUES (?, ?, 0)`, id, ser)
	f.SeriesIDs[key] = id
	return id
}

// Genre seeds one genre row and remembers it under key.
func (f *searchFixture) Genre(key, title string) int64 {
	f.t.Helper()
	id := f.id()
	f.exec(`INSERT INTO opds_catalog_genre (id, genre, title) VALUES (?, ?, ?)`, id, "searchfixture-"+key, title)
	f.GenreIDs[key] = id
	return id
}

// User seeds one reader and remembers it under key.
func (f *searchFixture) User(key, username string) int64 {
	f.t.Helper()
	id := f.id()
	f.exec(`INSERT INTO auth_user (id, password, is_superuser, username, email, date_joined)
		VALUES (?, '', false, ?, ?, ?)`, id, username, username+"@fixture.local", f.now)
	f.UserIDs[key] = id
	return id
}

// Book seeds one book row plus its author, series and genre junctions, and
// remembers the book under key.
//
// The default language is the synthetic code "fx", which no real catalogue
// row can carry: the integration database is the restored production dump, so
// a search scoped to Language "fx" sees fixture rows and nothing else. Seed
// rows with another Lang (like the "en" row) to exercise the filter itself.
func (f *searchFixture) Book(key string, b fixtureBook) int64 {
	f.t.Helper()
	id := f.id()
	lang := b.Lang
	if lang == "" {
		lang = "fx"
	}
	f.exec(`INSERT INTO opds_catalog_book
		(id, filename, path, format, registerdate, docdate, lang, title, annotation,
		 cover, approved, md5, duplicate_hidden)
		VALUES (?, ?, ?, 'fb2', ?, ?, ?, ?, '', false, ?, '', ?)`,
		id, fmt.Sprintf("%d.fb2", id), fmt.Sprintf("fixture/%d.fb2", id),
		f.now, f.now.Format("2006-01-02"), lang, b.Title, b.Approved, b.Hidden)
	for _, authorID := range b.Authors {
		f.exec(`INSERT INTO opds_catalog_bauthor (id, author_id, book_id) VALUES (?, ?, ?)`,
			f.id(), authorID, id)
	}
	for _, seriesID := range b.Series {
		f.exec(`INSERT INTO opds_catalog_bseries (id, ser_no, ser_id, book_id) VALUES (?, 1, ?, ?)`,
			f.id(), seriesID, id)
	}
	for _, genreID := range b.Genres {
		f.exec(`INSERT INTO opds_catalog_bgenre (id, genre_id, book_id) VALUES (?, ?, ?)`,
			f.id(), genreID, id)
	}
	f.BookIDs[key] = id
	return id
}

// Favorite marks bookID as a favorite of userID.
func (f *searchFixture) Favorite(userID, bookID int64) {
	f.t.Helper()
	f.exec(`INSERT INTO favorite_books (id, user_id, book_id) VALUES (?, ?, ?)`,
		f.id(), userID, bookID)
}

// Collection seeds a book collection and remembers it under key.
func (f *searchFixture) Collection(key string, userID *int64, name string, public, curated bool) int64 {
	f.t.Helper()
	id := f.id()
	f.exec(`INSERT INTO book_collections (id, user_id, name, is_public, is_curated, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, userID, name, public, curated, f.now, f.now)
	f.CollectionIDs[key] = id
	return id
}

// CollectionBook adds bookID to a user collection at position.
func (f *searchFixture) CollectionBook(collectionID, bookID int64, position int) {
	f.t.Helper()
	f.exec(`INSERT INTO book_collection_books (id, book_collection_id, book_id, position, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, f.id(), collectionID, bookID, position, f.now, f.now)
}

// CuratedItem adds one item row to a curated collection.
func (f *searchFixture) CuratedItem(collectionID int64, bookID *int64, externalTitle, status string, position int) {
	f.t.Helper()
	f.exec(`INSERT INTO book_collection_items
		(id, collection_id, book_id, external_title, match_status, position, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.id(), collectionID, bookID, externalTitle, status, position, f.now, f.now)
}

// seedSearchCatalog builds the catalogue every search test shares: one row
// per ranking signal, normalization edge cases, duplicate titles across
// authors, invisible rows, scoped rows, short titles, and a block of 510
// equal-title books that used to overflow the old candidate cap.
func seedSearchCatalog(f *searchFixture) {
	tolstoy := f.Author("tolstoy", "Толстой Лев")
	bulgakov := f.Author("bulgakov", "Булгаков Михаил")
	kozlov := f.Author("kozlov", "Козлов Сергей")
	other := f.Author("other", "Другой Автор")

	reader := f.User("reader", "searchfixture_reader")

	greatSeries := f.Series("great", "Великая серия")
	roman := f.Genre("roman", "Роман")

	// Ranking lanes: exact, prefix, all-words, substring and typo forms of the
	// same query.
	f.Book("exact", fixtureBook{Title: "Война и мир", Approved: true, Authors: []int64{tolstoy}})
	f.Book("prefix", fixtureBook{Title: "Война и мир: полное издание", Approved: true, Authors: []int64{tolstoy}})
	f.Book("allWords", fixtureBook{Title: "Мир и война", Approved: true, Authors: []int64{other}})
	f.Book("substring", fixtureBook{Title: "Читаем Война и мир вместе", Approved: true, Authors: []int64{other}})
	f.Book("typo", fixtureBook{Title: "Вайна и мир", Approved: true, Authors: []int64{other}})
	// Adjacent-letter transposition of this one-word title scores 0.333
	// against it: inside the old 0.3 trigram floor, outside the book-search
	// floor of 0.5.
	f.Book("transposition", fixtureBook{Title: "Океан", Approved: true, Authors: []int64{other}})

	// Normalization: е/ё, dashes, quotes, decomposed Unicode, numero sign and
	// repeated whitespace.
	f.Book("yo", fixtureBook{Title: "Ёжик в тумане", Approved: true, Authors: []int64{kozlov}})
	f.Book("dash", fixtureBook{Title: "Война—и–мир", Approved: true, Authors: []int64{tolstoy}})
	f.Book("quotes", fixtureBook{Title: "«Мастер» 'и' Маргарита", Approved: true, Authors: []int64{bulgakov}})
	f.Book("decomposed", fixtureBook{Title: "Café", Approved: true, Authors: []int64{other}})
	f.Book("numero", fixtureBook{Title: "Книга № 2", Approved: true, Authors: []int64{other}})
	f.Book("spaces", fixtureBook{Title: "  Ёжик   в тумане  ", Approved: true, Authors: []int64{kozlov}})

	// The same visible title owned by two different authors must stay two books.
	f.Book("dupA", fixtureBook{Title: "Сто лет одиночества", Approved: true, Authors: []int64{tolstoy}})
	f.Book("dupB", fixtureBook{Title: "Сто лет одиночества", Approved: true, Authors: []int64{bulgakov}})

	// Invisible rows: hidden duplicate, unapproved, and a second synthetic
	// language ("fy") the default reader does not browse. A real code like
	// "en" would leak production-dump rows into language-filter assertions.
	f.Book("hidden", fixtureBook{Title: "Война и мир", Approved: true, Hidden: true, Authors: []int64{tolstoy}})
	f.Book("unapproved", fixtureBook{Title: "Война и мир", Approved: false, Authors: []int64{tolstoy}})
	f.Book("english", fixtureBook{Title: "Война и мир", Lang: "fy", Approved: true, Authors: []int64{tolstoy}})

	// One- and two-character titles.
	f.Book("oneChar", fixtureBook{Title: "Я", Approved: true, Authors: []int64{other}})
	f.Book("twoChar", fixtureBook{Title: "Он", Approved: true, Authors: []int64{other}})

	// Scoped rows: an exact-titled book reachable through each scope kind.
	f.Book("seriesBook", fixtureBook{Title: "Война и мир", Approved: true, Authors: []int64{tolstoy}, Series: []int64{greatSeries}})
	f.Book("genreBook", fixtureBook{Title: "Война и мир", Approved: true, Authors: []int64{tolstoy}, Genres: []int64{roman}})
	f.Book("collectionBook", fixtureBook{Title: "Война и мир", Approved: true, Authors: []int64{tolstoy}})
	userShelf := f.Collection("shelf", &reader, "Полка читателя", true, false)
	f.CollectionBook(userShelf, f.BookIDs["collectionBook"], 1)
	f.Book("curatedBook", fixtureBook{Title: "Война и мир", Approved: true, Authors: []int64{tolstoy}})
	curated := f.Collection("curated", nil, "Кураторская подборка", true, true)
	curatedBookID := f.BookIDs["curatedBook"]
	f.CuratedItem(curated, &curatedBookID, "Война и мир", models.MatchStatusAutoMatched, 1)
	f.Book("curatedIgnored", fixtureBook{Title: "Война и мир", Approved: true, Authors: []int64{tolstoy}})
	ignoredID := f.BookIDs["curatedIgnored"]
	f.CuratedItem(curated, &ignoredID, "Война и мир", models.MatchStatusIgnored, 2)
	f.Book("favBook", fixtureBook{Title: "Война и мир", Approved: true, Authors: []int64{tolstoy}})
	f.Favorite(reader, f.BookIDs["favBook"])

	// The former candidate-cap regression: more than 500 visible books sharing
	// one title, so a capped candidate window silently drops exact matches.
	for i := 0; i < 510; i++ {
		f.Book(fmt.Sprintf("rasskazy%03d", i), fixtureBook{
			Title:    "Рассказы",
			Approved: true,
			Authors:  []int64{other},
		})
	}
}
