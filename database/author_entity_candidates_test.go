package database

import (
	"context"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The merge candidates' card fields (migration 63, plan E4a §4.3, §4.10).

// Every (table, columns) a card field is declared to read has a trigger that
// marks a build on insert, on delete and on an update of those columns: a
// statement trigger of any update, or a row trigger whose column list holds
// them. A source added without its trigger fails here.
func TestMergeCardSourcesMarked(t *testing.T) {
	requireDatabase(t)
	type trig struct {
		Table    string   `pg:"tbl"`
		Type     int      `pg:"tgtype"`
		Columns  []string `pg:"cols,array"`
		Function string   `pg:"fn"`
	}
	var rows []trig
	_, err := db.Query(&rows, `
		SELECT c.relname AS tbl, t.tgtype, p.proname AS fn,
			coalesce((SELECT array_agg(a.attname) FROM pg_attribute a
				WHERE a.attrelid = t.tgrelid AND a.attnum = ANY(t.tgattr)), '{}') AS cols
		FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_proc p ON p.oid = t.tgfoid
		WHERE NOT t.tgisinternal AND p.proname IN ('author_autopilot_mark', 'author_entity_input_mark')`)
	require.NoError(t, err)
	// The tgtype bits of a trigger's events.
	const insert, del, update = 1 << 2, 1 << 3, 1 << 4
	covers := func(table string, event int, column string) bool {
		for _, r := range rows {
			if r.Table != table || r.Type&event == 0 {
				continue
			}
			if event != update || len(r.Columns) == 0 || column == "" {
				return true
			}
			for _, c := range r.Columns {
				if c == column {
					return true
				}
			}
		}
		return false
	}
	for field, sources := range authorentity.MergeCardSources {
		for _, s := range sources {
			assert.True(t, covers(s.Table, insert, ""), "%s: %s marks no insert", field, s.Table)
			assert.True(t, covers(s.Table, del, ""), "%s: %s marks no delete", field, s.Table)
			for _, col := range s.Columns {
				assert.True(t, covers(s.Table, update, col), "%s: an update of %s.%s marks nothing", field, s.Table, col)
			}
		}
	}
}

// The card reads the source year of the current snapshot over the docdate,
// the genre codes and series names in order, and the annotation cut; a genre
// title is not on it, a genre code is.
func TestTheCardFieldsTheBuildReads(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	book, plain := f.book(), f.book()
	f.exec(`UPDATE opds_catalog_book SET docdate = '1999', lang = 'en', annotation = ? WHERE id = ?`,
		strings.Repeat("я", 400), book)
	f.exec(`UPDATE opds_catalog_book SET docdate = '2001' WHERE id = ?`, plain)
	f.exec(`INSERT INTO book_metadata_snapshot (book_id, book_md5, extractor_version, origin, outcome, archive_path,
		entry_name, source_year) VALUES (?, ?, 'extractor-v1', 'live', 'extracted', 'fixture.zip', 'b.fb2', '1977')`,
		book, bookMD5(book))
	genre, other := f.id(), f.id()
	f.exec(`INSERT INTO opds_catalog_genre (id, genre, title) VALUES (?, 'sf_horror', 'Ужасы'), (?, 'detective', 'Детектив')`,
		genre, other)
	f.exec(`INSERT INTO opds_catalog_bgenre (genre_id, book_id) VALUES (?, ?), (?, ?)`, genre, book, other, book)
	series := f.id()
	f.exec(`INSERT INTO opds_catalog_series (id, ser, lang_code) VALUES (?, 'Тёмная башня', 1)`, series)
	f.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (1, ?, ?)`, book, series)

	read := func() map[int64]authorentity.CardBookFields {
		got, err := LoadMergeCardBooks(ctx, f.tx, []int64{book, plain})
		require.NoError(t, err)
		return got
	}
	got := read()
	assert.Equal(t, authorentity.CardBookFields{Year: "1977", Lang: "en", Genres: []string{"detective", "sf_horror"},
		Series: []string{"Тёмная башня"}, Annotation: strings.Repeat("я", 300)}, got[book])
	assert.Equal(t, "2001", got[plain].Year, "no snapshot: the docdate")

	f.exec(`UPDATE opds_catalog_genre SET title = 'Хоррор' WHERE id = ?`, genre)
	assert.Equal(t, got, read(), "a genre title is on the card")
	f.exec(`UPDATE opds_catalog_genre SET genre = 'sf_horror_classic' WHERE id = ?`, genre)
	assert.Equal(t, []string{"detective", "sf_horror_classic"}, read()[book].Genres)
}
