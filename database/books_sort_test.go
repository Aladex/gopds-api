package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ordinary list sorts by author on request: by the sort key of each
// book's first name in the author display read model, compared without case
// and with ё read as е; books the model has no line for come last, and books
// with one key keep the newest first. Without the request the order is the
// one it always was.
func TestGetBooksSortsByTheFirstAuthorOnRequest(t *testing.T) {
	scratch := jobsDB(t)
	// The production-only genre title column the list reads (see
	// scanfixture.ScratchDB): the migration files do not create it.
	_, err := scratch.Exec(`ALTER TABLE opds_catalog_genre ADD COLUMN IF NOT EXISTS title varchar NOT NULL DEFAULT ''`)
	require.NoError(t, err)
	previous := db
	SetDB(scratch)
	t.Cleanup(func() { SetDB(previous) })
	ctx := context.Background()

	book := func(title string, authors ...string) int64 {
		t.Helper()
		var id int64
		_, err := scratch.QueryOne(pg.Scan(&id), `INSERT INTO opds_catalog_book
			(filename, path, format, title, annotation, lang, registerdate, docdate, approved, cover, md5, duplicate_hidden)
			VALUES (?, 'sort-test.zip', 'fb2', ?, '', 'ru', now(), '', true, false, md5(?), false) RETURNING id`,
			title+".fb2", title, title)
		require.NoError(t, err)
		for _, name := range authors {
			var author int64
			_, err = scratch.QueryOne(pg.Scan(&author),
				`INSERT INTO opds_catalog_author (full_name) VALUES (?) RETURNING id`, name)
			require.NoError(t, err)
			_, err = scratch.Exec(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?)`, id, author)
			require.NoError(t, err)
		}
		return id
	}
	yakovlev := book("Первая", "Яковлев Яков")
	abramov := book("Вторая", "Абрамов Абрам", "Яковлев Яков")
	unknown := book("Третья")
	yolkin := book("Четвёртая", "Ёлкин Пётр")
	elkin := book("Пятая", "Елкин Петр")
	require.NoError(t, scratch.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, err := RebuildAuthorDisplay(ctx, tx, []int64{yakovlev, abramov, unknown, yolkin, elkin})
		return err
	}))

	ids := func(filters models.BookFilters) []int64 {
		t.Helper()
		books, count, err := GetBooks(1, filters)
		require.NoError(t, err)
		assert.Equal(t, 5, count)
		out := make([]int64, len(books))
		for i := range books {
			out[i] = books[i].ID
		}
		return out
	}
	assert.Equal(t, []int64{abramov, elkin, yolkin, yakovlev, unknown},
		ids(models.BookFilters{Limit: 10, Sort: models.BookSortAuthor}),
		"Абрамов, then the two Елкин (one key; newest first), Яковлев, and the book with no author last")
	assert.Equal(t, []int64{elkin, yolkin, unknown, abramov, yakovlev}, ids(models.BookFilters{Limit: 10}),
		"the default order is the newest first, as ever")
	assert.Equal(t, []int64{elkin, yolkin}, ids(models.BookFilters{Limit: 2, Offset: 1, Sort: models.BookSortAuthor})[:2],
		"paging walks the same order")
}
