package database

import (
	"context"
	"testing"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The read model book_author_display: rebuilt from the layer and the legacy
// catalog for the books a writer marked, and for every book by the daily walk
// that catches a write path which forgot its mark.

// modelRow is a read-model row as stored, search key included.
type modelRow struct {
	BookID         int64  `pg:"book_id"`
	Position       int    `pg:"position"`
	Display        string `pg:"display"`
	SortKey        string `pg:"sort_key"`
	SearchKey      string `pg:"search_key"`
	Source         string `pg:"source"`
	LegacyAuthorID *int64 `pg:"legacy_author_id"`
	CreditID       *int64 `pg:"credit_id"`
}

func (f *authorSchemaFixture) modelRows(book int64) []modelRow {
	f.t.Helper()
	var rows []modelRow
	_, err := f.tx.Query(&rows, `SELECT book_id, position, display, sort_key, search_key, source, legacy_author_id, credit_id
		FROM book_author_display WHERE book_id = ? ORDER BY position`, book)
	require.NoError(f.t, err)
	return rows
}

func (f *authorSchemaFixture) searchKey(name string) string {
	f.t.Helper()
	var key string
	_, err := f.tx.QueryOne(pg.Scan(&key), `SELECT public.search_normalize(?)`, name)
	require.NoError(f.t, err)
	return key
}

func (f *authorSchemaFixture) marks() []int64 {
	f.t.Helper()
	var books []int64
	_, err := f.tx.Query(&books, `SELECT coalesce(d.book_id, (SELECT s.book_id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE c.id = d.credit_id))
		FROM book_author_display_dirty d ORDER BY d.id`)
	require.NoError(f.t, err)
	return books
}

func (f *authorSchemaFixture) rebuild(books ...int64) int {
	f.t.Helper()
	n, err := RebuildAuthorDisplay(context.Background(), f.tx, books)
	require.NoError(f.t, err)
	return n
}

func TestRebuildAuthorDisplayRewritesOnlyTheBooksWhoseLineChanged(t *testing.T) {
	f := withAuthorSchemaTx(t)
	f.registerPair("3", "structured_person", "normalizer-v1")

	layered := f.book()
	petrov := f.legacyAuthorOf(layered, "Петров Иван")
	snap := f.snapshot(&snapshotSpec{book: layered})
	credit := f.displayCredit(snap, "author", 0, "Иван Петров", 0x61)
	legacyOnly := f.book()
	gogol := f.legacyAuthorOf(legacyOnly, "Гоголь Николай")

	assert.Equal(t, 2, f.rebuild(layered, legacyOnly))
	assert.Equal(t, []modelRow{{BookID: layered, Position: 0, Display: "Иван Петров", SortKey: "иван петров",
		SearchKey: f.searchKey("Иван Петров"), Source: "layer", LegacyAuthorID: &petrov, CreditID: &credit}},
		f.modelRows(layered))
	assert.Equal(t, []modelRow{{BookID: legacyOnly, Position: 0, Display: "Гоголь Николай", SortKey: "гоголь николай",
		SearchKey: f.searchKey("Гоголь Николай"), Source: "legacy", LegacyAuthorID: &gogol}},
		f.modelRows(legacyOnly))

	assert.Equal(t, 0, f.rebuild(layered, legacyOnly), "nothing changed, nothing written")

	// A selection changes the name shown: that book alone is rewritten.
	f.selectAs(credit, 0x61, "Иван Ильич Петров")
	assert.Equal(t, 1, f.rebuild(layered, legacyOnly))
	rows := f.modelRows(layered)
	require.Len(t, rows, 1)
	assert.Equal(t, "Иван Ильич Петров", rows[0].Display)
	assert.Equal(t, f.searchKey("Иван Ильич Петров"), rows[0].SearchKey)

	// A search key that no longer follows from the name is rewritten too.
	f.exec(`UPDATE book_author_display SET search_key = 'stale' WHERE book_id = ?`, legacyOnly)
	assert.Equal(t, 1, f.rebuild(legacyOnly))
	assert.Equal(t, f.searchKey("Гоголь Николай"), f.modelRows(legacyOnly)[0].SearchKey)

	// Rows that outlived their book go.
	ghost := legacyOnly + 1_000_000
	f.exec(`INSERT INTO book_author_display (book_id, position, display, sort_key, search_key, source, legacy_author_id)
		VALUES (?, 0, 'x', 'x', 'x', 'legacy', 1)`, ghost)
	assert.Equal(t, 1, f.rebuild(ghost))
	assert.Empty(t, f.modelRows(ghost))
}

func TestDrainingMarksRebuildsEachMarkedBookOnce(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	a, b, c := f.book(), f.book(), f.book()
	for _, book := range []int64{a, b, c} {
		f.legacyAuthorOf(book, "Автор Книги")
	}
	require.NoError(t, MarkAuthorDisplayDirty(ctx, f.tx, a, b))
	require.NoError(t, MarkAuthorDisplayDirty(ctx, f.tx, a))
	require.NoError(t, MarkAuthorDisplayDirty(ctx, f.tx, c))

	// The oldest marks first, as many as the batch takes.
	got, err := DrainAuthorDisplayMarks(ctx, f.tx, 3)
	require.NoError(t, err)
	assert.Equal(t, AuthorDisplayDrain{Marks: 3, Books: 2, Differed: 2}, got)
	assert.Equal(t, []int64{c}, f.marks())
	assert.Len(t, f.modelRows(a), 1)
	assert.Len(t, f.modelRows(b), 1)
	assert.Empty(t, f.modelRows(c))

	got, err = DrainAuthorDisplayMarks(ctx, f.tx, 3)
	require.NoError(t, err)
	assert.Equal(t, AuthorDisplayDrain{Marks: 1, Books: 1, Differed: 1}, got)

	got, err = DrainAuthorDisplayMarks(ctx, f.tx, 3)
	require.NoError(t, err)
	assert.Equal(t, AuthorDisplayDrain{}, got)
	assert.Empty(t, f.marks())

	// A selection marks its credit; the drain finds the credit's book. A
	// credit gone with its book leaves a mark with nothing to rebuild.
	credited := f.book()
	snap := f.snapshot(&snapshotSpec{book: credited})
	credit := f.displayCredit(snap, "author", 0, "Иван Петров", 0x71)
	f.exec(`INSERT INTO book_author_display_dirty (credit_id) VALUES (?), (-1)`, credit)
	got, err = DrainAuthorDisplayMarks(ctx, f.tx, 3)
	require.NoError(t, err)
	assert.Equal(t, AuthorDisplayDrain{Marks: 2, Books: 1, Differed: 1}, got)
	assert.Equal(t, "Иван Петров", f.modelRows(credited)[0].Display)
}

// reconcileState is the one row of the walk, read in the fixture.
type reconcileState struct {
	WalkCursor    *int64     `pg:"walk_cursor"`
	FinishedAt    *time.Time `pg:"finished_at"`
	BooksDiffered *int64     `pg:"books_differed"`
	BooksRead     *int64     `pg:"books_read"`
}

func (f *authorSchemaFixture) reconcileState() reconcileState {
	f.t.Helper()
	var s reconcileState
	_, err := f.tx.QueryOne(&s, `SELECT walk_cursor, finished_at, books_differed, books_read
		FROM book_author_display_reconcile`)
	require.NoError(f.t, err)
	return s
}

func TestTheDailyWalkCatchesAMissedMarkAndRecordsWhatDiffered(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	step := func() AuthorDisplayReconcileStep {
		t.Helper()
		s, err := ReconcileAuthorDisplayStep(ctx, f.tx, 2, 24*time.Hour)
		require.NoError(t, err)
		return s
	}

	inPlace := f.book()
	f.legacyAuthorOf(inPlace, "Верный Автор")
	missed := f.book()
	f.legacyAuthorOf(missed, "Старое Имя")
	third := f.book()
	f.legacyAuthorOf(third, "Третий Автор")
	f.rebuild(inPlace, missed)
	// A write path that forgot its mark: the catalog changed, the model did
	// not, and no mark says so.
	f.exec(`UPDATE opds_catalog_author SET full_name = 'Новое Имя'
		WHERE id = (SELECT author_id FROM opds_catalog_bauthor WHERE book_id = ?)`, missed)
	ghost := third + 1_000_000
	f.exec(`INSERT INTO book_author_display (book_id, position, display, sort_key, search_key, source, legacy_author_id)
		VALUES (?, 0, 'x', 'x', 'x', 'legacy', 1)`, ghost)

	// A walk is due (none ever finished); this one starts just below the
	// fixture's books, so it reads them and nothing else of the catalog.
	f.exec(`UPDATE book_author_display_reconcile SET walk_cursor = ?, walk_started = now(),
		walk_differed = 0, walk_read = 0, finished_at = NULL`, inPlace-1)

	first := step()
	assert.True(t, first.Ran)
	assert.False(t, first.Finished)
	assert.Equal(t, 2, first.Books)
	assert.Equal(t, 1, first.Differed, "the missed change")
	assert.Equal(t, "Новое Имя", f.modelRows(missed)[0].Display)

	last := step()
	assert.True(t, last.Finished)
	assert.Equal(t, 2, last.Differed, "the third book, never built, and the ghost")
	assert.Empty(t, f.modelRows(ghost))
	assert.Len(t, f.modelRows(third), 1)

	state := f.reconcileState()
	assert.Nil(t, state.WalkCursor)
	require.NotNil(t, state.FinishedAt)
	assert.Equal(t, int64(3), *state.BooksDiffered)
	assert.Equal(t, int64(3), *state.BooksRead)

	// The next walk waits a day.
	assert.False(t, step().Ran)
	f.exec(`UPDATE book_author_display_reconcile SET finished_at = now() - interval '25 hours'`)
	next := step()
	assert.True(t, next.Ran)
	assert.True(t, next.Started)
}

// nameIndex is book_author_display_name: each linked name that says more than
// its legacy author's, with the number of books whose line carries it — what
// the author search and the picker read instead of a row per book.
func (f *authorSchemaFixture) nameIndex(author int64) map[string]int {
	f.t.Helper()
	var rows []struct {
		SearchKey string `pg:"search_key"`
		Books     int    `pg:"books"`
	}
	_, err := f.tx.Query(&rows, `SELECT search_key, books FROM book_author_display_name
		WHERE legacy_author_id = ?`, author)
	require.NoError(f.t, err)
	out := map[string]int{}
	for _, r := range rows {
		out[r.SearchKey] = r.Books
	}
	return out
}

func TestRebuildKeepsTheNameIndexOfNamesThatSayMore(t *testing.T) {
	f := withAuthorSchemaTx(t)
	f.registerPair("3", "structured_person", "normalizer-v1")
	author := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES ('Петров Иван') RETURNING id`)
	book := func(name string, fill byte) (int64, int64) {
		b := f.book()
		f.linkLegacyAuthor(b, author)
		snap := f.snapshot(&snapshotSpec{book: b})
		return b, f.displayCredit(snap, "author", 0, name, fill)
	}
	a, creditA := book("Иван Ильич Петров", 0x81)
	b, _ := book("Иван Ильич Петров", 0x82)
	c, _ := book("Иван Петров", 0x83)
	patronymic := f.searchKey("Иван Ильич Петров")

	f.rebuild(a, b, c)
	assert.Equal(t, map[string]int{patronymic: 2}, f.nameIndex(author),
		"two books carry the patronymic; the reordered catalog name says nothing new")

	// A selection drops the patronymic from one book: the count follows.
	f.selectAs(creditA, 0x81, "Иван Петров")
	f.rebuild(a)
	assert.Equal(t, map[string]int{patronymic: 1}, f.nameIndex(author))

	// The last book carrying it goes: so does the name.
	f.exec(`DELETE FROM opds_catalog_bauthor WHERE book_id = ?`, b)
	f.exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE book_id = ?`, b)
	f.rebuild(b)
	assert.Empty(t, f.nameIndex(author))
}

func TestTheDailyWalkRestoresTheNameIndex(t *testing.T) {
	f := withAuthorSchemaTx(t)
	author := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES ('Сидоров Олег') RETURNING id`)
	b := f.book()
	f.linkLegacyAuthor(b, author)
	snap := f.snapshot(&snapshotSpec{book: b})
	f.displayCredit(snap, "author", 0, "Олег Петрович Сидоров", 0x91)
	f.rebuild(b)
	want := f.nameIndex(author)
	require.Len(t, want, 1)

	// Drift no rebuild would see: a count off, a name nothing carries.
	f.exec(`UPDATE book_author_display_name SET books = 7 WHERE legacy_author_id = ?`, author)
	f.exec(`INSERT INTO book_author_display_name (legacy_author_id, search_key, books) VALUES (?, 'призрак', 3)`, author)
	f.exec(`UPDATE book_author_display_reconcile SET walk_cursor = ?, walk_started = now(),
		walk_differed = 0, walk_read = 0, finished_at = NULL`, b-1)

	step, err := ReconcileAuthorDisplayStep(context.Background(), f.tx, 10, 24*time.Hour)
	require.NoError(t, err)
	require.True(t, step.Finished)
	assert.Equal(t, want, f.nameIndex(author))
	assert.Equal(t, 2, step.NamesDiffered, "the wrong count and the stray name")
	var recorded int64
	_, err = f.tx.QueryOne(pg.Scan(&recorded), `SELECT names_differed FROM book_author_display_reconcile`)
	require.NoError(t, err)
	assert.Equal(t, int64(2), recorded)
}
