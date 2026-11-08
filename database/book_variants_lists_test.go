package database

import (
	"context"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The site's grouped lists against the per-file ones every other surface
// keeps. The list functions read the package handle, so these tests run on a
// scratch database: the fixture is written in a transaction on it and
// committed before a list reads it.

// cardsFixture is a variant fixture on a scratch database that is the
// package handle for the test.
type cardsFixture struct {
	*variantFixture
	scratch *pg.DB
}

func cardsDB(t *testing.T) *cardsFixture {
	t.Helper()
	scratch := jobsDB(t)
	previous := db
	SetDB(scratch)
	t.Cleanup(func() { SetDB(previous) })
	next := authorSchemaIDBase
	f := &cardsFixture{variantFixture: &variantFixture{&authorSchemaFixture{t: t, next: &next}}, scratch: scratch}
	f.begin()
	return f
}

func (f *cardsFixture) begin() {
	f.t.Helper()
	tx, err := f.scratch.Begin()
	require.NoError(f.t, err)
	f.tx = tx
	f.t.Cleanup(func() { _ = tx.Rollback() })
}

// commit makes what the fixture wrote visible to the lists and opens the
// next transaction.
func (f *cardsFixture) commit() {
	f.t.Helper()
	require.NoError(f.t, f.tx.Commit())
	f.begin()
}

// addedAgo dates a fixture book days before now.
func addedAgo(days int) func(*variantSpec) {
	return func(s *variantSpec) { s.added = time.Now().Add(-time.Duration(days) * 24 * time.Hour) }
}

// group3 is three files of one card by the entity, each its own document,
// rebuilt; it returns the files by rank.
func (f *cardsFixture) group3(entity, title string) (byRank [3]int64) {
	f.t.Helper()
	books := []int64{
		f.variantBook(title, []string{entity}, withDoc(title+"-a", "1.0"), addedAgo(3)),
		f.variantBook(title, []string{entity}, withDoc(title+"-b", "1.0"), addedAgo(2)),
		f.variantBook(title, []string{entity}, withDoc(title+"-c", "1.0"), addedAgo(1)),
	}
	f.rebuild(books[0])
	for _, b := range books {
		m := f.member(b)
		require.NotNil(f.t, m, "book %d has a card row", b)
		byRank[m.Rank-1] = b
	}
	return byRank
}

func uuidOf(t *testing.T, id string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	return parsed
}

func cardIDs(books []models.Book) []int64 {
	out := make([]int64, len(books))
	for i := range books {
		out[i] = books[i].ID
	}
	return out
}

func TestGetBooksListsEveryFileOfAGroup(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Стругацкие")
	// Older by id than the group, so the group's files come first in the
	// newest-first list.
	single := f.variantBook("Трудно быть богом", []string{entity}, addedAgo(5))
	f.rebuild(single)
	files := f.group3(entity, "Пикник на обочине")
	f.commit()

	books, total, err := GetBooks(0, models.BookFilters{AuthorEntity: uuidOf(t, entity), Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.ElementsMatch(t, []int64{files[0], files[1], files[2], single}, cardIDs(books))
}

func TestGetBookCardsListsOneEntryPerGroup(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Стругацкие")
	// Older by id than the group, so the group's files come first in the
	// newest-first list.
	single := f.variantBook("Трудно быть богом", []string{entity}, addedAgo(5))
	f.rebuild(single)
	files := f.group3(entity, "Пикник на обочине")
	f.commit()

	books, entries, total, err := GetBookCards(context.Background(), 0,
		models.BookFilters{AuthorEntity: uuidOf(t, entity), Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, books, 2)
	require.Len(t, entries, 2)
	// Newest first, by the group's first position: the group's newest file
	// is newer than the single book.
	assert.Equal(t, []int64{files[0], single}, cardIDs(books))
	require.NotNil(t, entries[0].Variants)
	assert.Equal(t, 3, entries[0].Variants.Count)
	assert.Equal(t, models.BookVariantsVariants, entries[0].Variants.Kind)
	assert.Empty(t, entries[0].Variants.Reason, "an author's page shows the default file")
	assert.Equal(t, f.member(files[0]).GroupKey, entries[0].Variants.Key.String())
	assert.Nil(t, entries[1].Variants, "a single book carries nothing")
	assert.Equal(t, files[0], entries[0].BookID)
}

// TestGetBooksScopeOrders pins the per-file order of the scopes whose ORDER BY
// moved into listOrder: the newest star first, a collection's position, a
// curated collection's item position, and the most starred book first.
func TestGetBooksScopeOrders(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Порядок")
	b := make([]int64, 4)
	for i := range b {
		b[i] = f.variantBook("Книга "+string(rune('А'+i)), []string{entity})
	}
	u1, u2, u3 := f.user(), f.user(), f.user()
	for _, s := range []struct{ user, book int64 }{
		{u1, b[2]}, {u1, b[0]}, {u1, b[3]}, // u1: newest star b[3], then b[0], then b[2]
		{u2, b[0]}, {u2, b[2]}, {u3, b[0]}, // b[0] 3 readers, b[2] 2, b[3] 1
	} {
		f.exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?)`, s.user, s.book)
	}
	collection := f.id()
	f.exec(`INSERT INTO book_collections (id, user_id, name) VALUES (?, ?, 'Моя')`, collection, u1)
	for pos, book := range []int64{b[1], b[3], b[0]} {
		f.exec(`INSERT INTO book_collection_books (book_collection_id, book_id, position) VALUES (?, ?, ?)`,
			collection, book, pos)
	}
	curated := f.id()
	f.exec(`INSERT INTO book_collections (id, name, is_public, is_curated) VALUES (?, 'Подборка', true, true)`, curated)
	for pos, book := range []int64{b[3], b[1], b[2]} {
		f.exec(`INSERT INTO book_collection_items (collection_id, book_id, external_title, match_status, position)
			VALUES (?, ?, 'x', ?, ?)`, curated, book, models.MatchStatusAutoMatched, pos)
	}
	f.commit()

	for _, tc := range []struct {
		name    string
		user    int64
		filters models.BookFilters
		want    []int64
	}{
		{"favorites, newest star first", u1, models.BookFilters{Fav: true}, []int64{b[3], b[0], b[2]}},
		{"collection by position", u1, models.BookFilters{Collection: collection}, []int64{b[1], b[3], b[0]}},
		{"curated by item position", 0, models.BookFilters{CuratedCollection: curated}, []int64{b[3], b[1], b[2]}},
		{"popular, most starred first", 0, models.BookFilters{UsersFavorites: true}, []int64{b[0], b[2], b[3]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.filters.Limit = 10
			books, total, err := GetBooks(tc.user, tc.filters)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cardIDs(books))
			assert.Equal(t, len(tc.want), total)
		})
	}
}
