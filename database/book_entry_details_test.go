package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a page of book entries shows beyond the rows the lists already have:
// the translators each file credits, read from the current snapshot, and the
// visible files of its card, read live over the catalog.

// translatorsOf credits the book's current snapshot with the translators, in
// the given order.
func (f *cardsFixture) translatorsOf(book int64, names ...string) {
	f.t.Helper()
	var snapshot int64
	_, err := f.tx.QueryOne(pg.Scan(&snapshot),
		`SELECT id FROM book_metadata_snapshot WHERE book_id = ? AND is_current`, book)
	require.NoError(f.t, err)
	for i, name := range names {
		f.exec(`INSERT INTO book_contributor_credit
			(snapshot_id, role, position, source_display_name, source_fingerprint)
			VALUES (?, 'translator', ?, ?, sha256(convert_to(?, 'UTF8')))`, snapshot, i, name, name)
	}
}

func TestBookEntryDetailsOfNoBooksAsksNothing(t *testing.T) {
	// A nil handle would panic on any query: an empty page must not reach it.
	got, err := NewPGBookEntries(nil).BookEntryDetails(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestBookEntryDetailsReadsTranslatorsAndCardCounts(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Лем")
	files := f.group3(entity, "Солярис")
	solo := f.variantBook("Непобедимый", []string{entity})
	f.translatorsOf(files[0], "Джан Брунеро", "Светлана Васильева")
	f.translatorsOf(files[1], "Нора Галь", "Рита Райт-Ковалёва", "Корней Чуковский")
	f.translatorsOf(solo, "Ольга Славникова")
	f.commit()

	got, err := NewPGBookEntries(f.scratch).BookEntryDetails(context.Background(),
		[]int64{files[0], files[1], files[2], solo})
	require.NoError(t, err)

	assert.Equal(t, models.BookEntryDetail{Translators: []string{"Джан Брунеро", "Светлана Васильева"},
		VariantCount: 3}, got[files[0]])
	assert.Equal(t, models.BookEntryDetail{Translators: []string{"Нора Галь", "Рита Райт-Ковалёва"},
		TranslatorsMore: true, VariantCount: 3}, got[files[1]],
		"the third translator remains, said by More")
	assert.Equal(t, models.BookEntryDetail{VariantCount: 3}, got[files[2]], "no credits, no translators")
	assert.Equal(t, models.BookEntryDetail{Translators: []string{"Ольга Славникова"}, VariantCount: 1},
		got[solo], "a card of one counts its only file")
}

func TestBookEntryDetailsCountsOnlyVisibleFiles(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Лем")
	files := f.group3(entity, "Солярис")
	f.commit()

	// Hidden after the card was built: the stored row still counts it.
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, files[2])
	f.commit()

	got, err := NewPGBookEntries(f.scratch).BookEntryDetails(context.Background(),
		[]int64{files[0], files[1], files[2]})
	require.NoError(t, err)
	assert.Equal(t, 2, got[files[0]].VariantCount, "the hidden file counts nowhere")
	assert.Equal(t, 2, got[files[1]].VariantCount)
	assert.Equal(t, 2, got[files[2]].VariantCount,
		"the count is of the card's visible files, whoever of it was asked")
}

func TestBookEntryDetailsCountsABookWithoutACardRow(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Лем")
	// Visible, never rebuilt: no read-model row, a card of its own.
	lone := f.variantBook("Магелланово облако", []string{entity})
	f.commit()

	got, err := NewPGBookEntries(f.scratch).BookEntryDetails(context.Background(), []int64{lone})
	require.NoError(t, err)
	assert.Equal(t, models.BookEntryDetail{VariantCount: 1}, got[lone])
}

func TestBookEntryDetailsReadsTheTranslatorsOfTheCurrentSnapshotOnly(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Лем")
	book := f.variantBook("Эдем", []string{entity})
	f.translatorsOf(book, "Старый Переводчик")
	f.commit()

	// A second snapshot becomes the current one and credits no one.
	f.exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE book_id = ?`, book)
	f.exec(`INSERT INTO book_metadata_snapshot
			(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name,
			 source_title, source_document_id, source_document_version, is_current)
		VALUES (?, ?, 'extractor-v1', 'live', 'extracted', 'fixture.zip', '42.fb2', 'Эдем', NULL, NULL, true)`,
		book, bookMD5(book+1_000))
	f.commit()

	got, err := NewPGBookEntries(f.scratch).BookEntryDetails(context.Background(), []int64{book})
	require.NoError(t, err)
	assert.Equal(t, models.BookEntryDetail{VariantCount: 1}, got[book],
		"a superseded snapshot's translator is not the file's")
}
