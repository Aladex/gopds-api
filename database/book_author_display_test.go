package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The page query behind the author line: the legacy links in link order and
// the author credits of the current snapshot in file order, each showing its
// selected name or the file's spelling.

// legacyAuthorOf links a new catalog author to the book and returns its ID.
func (f *authorSchemaFixture) legacyAuthorOf(book int64, name string) int64 {
	f.t.Helper()
	id := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES (?) RETURNING id`, name)
	f.linkLegacyAuthor(book, id)
	return id
}

func (f *authorSchemaFixture) linkLegacyAuthor(book, author int64) {
	f.t.Helper()
	f.exec(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?)`, book, author)
}

// displayCredit writes a credit of the snapshot spelled as the file spells it
// and returns its ID; fill makes its fingerprint distinct.
func (f *authorSchemaFixture) displayCredit(snapshot int64, role string, position int, name string, fill byte) int64 {
	f.t.Helper()
	return f.returningID(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_display_name, source_fingerprint)
		VALUES (?, ?, ?, ?, ?) RETURNING id`, snapshot, role, position, name, fingerprint(32, fill))
}

// selectAs gives the credit an automatic selection of a result showing name.
// The pair it rests on is registered once per fixture by the caller.
func (f *authorSchemaFixture) selectAs(credit int64, fill byte, name string) {
	f.t.Helper()
	row := autoRow(fingerprint(32, fill), "structured_person", "normalizer-v1")
	row.display = &name
	result := f.result(row)
	f.exec(selectionInsert, credit, fingerprint(32, fill), "selected", result, "automatic", nil, "3", nil, nil)
}

func TestBookAuthorDisplayReadsTheCurrentCreditsInFileOrder(t *testing.T) {
	f := withAuthorSchemaTx(t)
	f.registerPair("3", "structured_person", "normalizer-v1")
	repo := NewPGBookSourceRepository(f.tx)

	// Two authors, linked in the legacy catalog in the opposite order, and
	// credited in the file with the position-1 credit written first.
	both := f.book()
	petrov := f.legacyAuthorOf(both, "Петров Иван")
	sidorova := f.legacyAuthorOf(both, "Сидорова Анна")
	f.linkLegacyAuthor(both, petrov) // a duplicate link is one author
	snap := f.snapshot(&snapshotSpec{book: both})
	second := f.displayCredit(snap, "author", 1, "ИВАН ПЕТРОВИЧ ПЕТРОВ", 0x11)
	first := f.displayCredit(snap, "author", 0, "СИДОРОВА Анна", 0x12)
	f.selectAs(first, 0x12, "Анна Сидорова")
	f.exec(selectionInsert, second, fingerprint(32, 0x11), "review", nil, nil, nil, nil, nil, nil)
	f.displayCredit(snap, "translator", 2, "Переводчик Тот", 0x13)

	// A verdict of "not a person" still shows the name as the file has it.
	invalid := f.book()
	snap = f.snapshot(&snapshotSpec{book: invalid})
	credit := f.displayCredit(snap, "author", 0, "Коллектив авторов", 0x21)
	result := f.result(invalidRow(fingerprint(32, 0x21)))
	f.exec(selectionInsert, credit, fingerprint(32, 0x21), "invalid", result, nil, nil, nil, nil, nil)

	// Only the current snapshot speaks.
	superseded := f.book()
	newer := f.legacyAuthorOf(superseded, "Новиков Олег")
	old := f.snapshot(&snapshotSpec{book: superseded, notCurrent: true})
	f.displayCredit(old, "author", 0, "Старый Автор", 0x31)
	cur := f.snapshot(&snapshotSpec{book: superseded, md5: bookMD5(superseded + 1_000)})
	f.displayCredit(cur, "author", 0, "Олег Новиков", 0x32)

	// No snapshot: the legacy authors, in link order.
	noSnapshot := f.book()
	b := f.legacyAuthorOf(noSnapshot, "Борисов Борис")
	a := f.legacyAuthorOf(noSnapshot, "Алексеев Алексей")

	// A snapshot naming only a translator has no author credits.
	translatorOnly := f.book()
	kept := f.legacyAuthorOf(translatorOnly, "Иванов Иван")
	snap = f.snapshot(&snapshotSpec{book: translatorOnly})
	f.displayCredit(snap, "translator", 0, "Иван Иванов", 0x41)

	nothing := f.book()

	got, err := repo.BookAuthorDisplay(context.Background(),
		[]int64{both, invalid, superseded, noSnapshot, translatorOnly, nothing})
	require.NoError(t, err)

	assert.Equal(t, layerLine(linked("Анна Сидорова", sidorova), linked("Иван Петрович Петров", petrov)), got[both])
	assert.Equal(t, layerLine(unlinked("Коллектив авторов")), got[invalid])
	assert.Equal(t, layerLine(linked("Олег Новиков", newer)), got[superseded])
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits, linked("Борисов Борис", b), linked("Алексеев Алексей", a)),
		got[noSnapshot])
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits, linked("Иванов Иван", kept)), got[translatorOnly])
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits), got[nothing])
	assert.Len(t, got, 6)

	empty, err := repo.BookAuthorDisplay(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// The report reads the catalog in ID order, a window at a time, and says
// where the next window starts until the catalog is read to its end.
func TestCompareAuthorDisplayWalksTheCatalogInWindows(t *testing.T) {
	f := withAuthorSchemaTx(t)
	f.registerPair("3", "structured_person", "normalizer-v1")

	same := f.book()
	f.legacyAuthorOf(same, "Иванов Иван")
	snap := f.snapshot(&snapshotSpec{book: same})
	f.selectAs(f.displayCredit(snap, "author", 0, "ИВАНОВ Иван", 0x51), 0x51, "Иван Иванов")

	edited := f.book()
	f.legacyAuthorOf(edited, "Исправленный Автор")
	snap = f.snapshot(&snapshotSpec{book: edited})
	f.displayCredit(snap, "author", 0, "Лев Толстой", 0x52)

	noSnapshot := f.book()
	f.legacyAuthorOf(noSnapshot, "Гоголь Николай")

	first, err := CompareAuthorDisplay(context.Background(), f.tx, same-1, 2)
	require.NoError(t, err)
	assert.Equal(t, same-1, first.AfterID)
	assert.Equal(t, 2, first.Books)
	require.NotNil(t, first.NextAfterID)
	assert.Equal(t, edited, *first.NextAfterID)
	assert.Equal(t, []int64{same}, first.Categories[ReportWordsSame].Samples)
	assert.Equal(t, []int64{edited}, first.Categories[ReportDisplayLegacyUnmatched].Samples)

	rest, err := CompareAuthorDisplay(context.Background(), f.tx, *first.NextAfterID, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, rest.Books, "nothing in the catalog lies above the fixture's books")
	assert.Nil(t, rest.NextAfterID)
	assert.Equal(t, []int64{noSnapshot}, rest.Categories[ReportDisplayLegacyNoCredits].Samples)

	// A window that ends exactly at the last book is the end of the walk:
	// it points nowhere, rather than at an empty window after it.
	exact, err := CompareAuthorDisplay(context.Background(), f.tx, edited, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, exact.Books)
	assert.Nil(t, exact.NextAfterID)

	// The same holds when the window is a whole number of read batches.
	last := f.book()
	for range reportBatch - 1 {
		last = f.book()
	}
	batch, err := CompareAuthorDisplay(context.Background(), f.tx, noSnapshot, reportBatch)
	require.NoError(t, err)
	assert.Equal(t, reportBatch, batch.Books)
	assert.Nil(t, batch.NextAfterID, "the window ended on book %d, the last one", last)
}
