package database

import (
	"context"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deleting books keeps the read model whole: the books' rows go, and so do
// the names in the name index only they carried — an alias the last such
// book took with it no longer finds its author. Deletion runs under the
// model's lock, like every rebuild, so a rebuild cannot rewrite the rows of
// a book being deleted.

func TestDeletingABookTakesItsAliasOutOfTheNameIndex(t *testing.T) {
	f := withAuthorSchemaTx(t)
	author := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES ('Петров Иван') RETURNING id`)
	aliased := f.book()
	f.linkLegacyAuthor(aliased, author)
	snap := f.snapshot(&snapshotSpec{book: aliased})
	f.displayCredit(snap, "author", 0, "Иван Зурабкарлович Петров", 0xa1)
	// Another book keeps the author visible to the author search.
	plain := f.book()
	f.linkLegacyAuthor(plain, author)
	f.exec(`UPDATE opds_catalog_book SET approved = true, duplicate_hidden = false WHERE id IN (?, ?)`, aliased, plain)
	f.rebuild(aliased, plain)
	require.Len(t, f.nameIndex(author), 1)

	repo := NewPGSearchRepository(f.tx)
	found := func() []int64 {
		page, err := repo.SearchAuthors(context.Background(), models.AuthorSearchRequest{Query: "зурабкарлович", Limit: 50})
		require.NoError(t, err)
		return authorIDs(page.Authors)
	}
	require.Contains(t, found(), author, "the alias finds the author while a book carries it")

	_, err := deleteBooksWithLayer(f.tx, []int64{aliased})
	require.NoError(t, err)
	assert.Empty(t, f.modelRows(aliased))
	assert.Empty(t, f.nameIndex(author), "the deleted book's alias left the index")
	assert.NotContains(t, found(), author, "and no longer finds the author")
	assert.Len(t, f.modelRows(plain), 1, "the other book is untouched")
}

// A rebuild that starts while a deletion is in flight waits for it, then
// sees the book gone: it leaves no orphan rows and no stale alias behind.
func TestARebuildWaitsForADeletionOfItsBook(t *testing.T) {
	scratch := jobsDB(t)
	ctx := context.Background()
	// The catalog, committed: the deletion and the rebuild run in their own
	// transactions.
	setup, err := scratch.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: setup, next: &next}
	author := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES ('Петров Иван') RETURNING id`)
	book := f.book()
	f.linkLegacyAuthor(book, author)
	f.displayCredit(f.snapshot(&snapshotSpec{book: book}), "author", 0, "Иван Зурабкарлович Петров", 0xa2)
	require.NoError(t, setup.Commit())
	require.NoError(t, scratch.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, rebuildErr := RebuildAuthorDisplay(ctx, tx, []int64{book})
		return rebuildErr
	}))
	var aliases int
	_, err = scratch.QueryOne(pg.Scan(&aliases), `SELECT count(*) FROM book_author_display_name WHERE legacy_author_id = ?`, author)
	require.NoError(t, err)
	require.Equal(t, 1, aliases)

	deletion, err := scratch.Begin()
	require.NoError(t, err)
	defer func() { _ = deletion.Rollback() }()
	_, err = deleteBooksWithLayer(deletion, []int64{book})
	require.NoError(t, err)

	rebuilt := make(chan error, 1)
	go func() {
		rebuilt <- scratch.RunInTransaction(ctx, func(tx *pg.Tx) error {
			_, rebuildErr := RebuildAuthorDisplay(ctx, tx, []int64{book})
			return rebuildErr
		})
	}()
	select {
	case err = <-rebuilt:
		t.Fatalf("the rebuild ran while the deletion held the book: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, deletion.Commit())
	require.NoError(t, <-rebuilt)

	var rows int
	_, err = scratch.QueryOne(pg.Scan(&rows), `SELECT count(*) FROM book_author_display WHERE book_id = ?`, book)
	require.NoError(t, err)
	assert.Zero(t, rows, "no orphan rows")
	_, err = scratch.QueryOne(pg.Scan(&aliases), `SELECT count(*) FROM book_author_display_name WHERE legacy_author_id = ?`, author)
	require.NoError(t, err)
	assert.Zero(t, aliases, "no stale alias")
}
