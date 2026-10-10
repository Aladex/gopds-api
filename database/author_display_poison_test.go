package database

import (
	"context"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The production shape that stopped the walk (book 389625: one author credit
// whose file spelling is a lone U+00A0): the book is built on its legacy
// line, on every surface the same.
func TestABookWhoseOnlyCreditIsANoBreakSpaceIsBuiltOnItsLegacyLine(t *testing.T) {
	f := withAuthorSchemaTx(t)
	book := f.book()
	author := f.legacyAuthorOf(book, "Петров Иван")
	snap := f.snapshot(&snapshotSpec{book: book})
	f.displayCredit(snap, "author", 0, "\u00a0", 0xb1)
	punct := f.book()
	snap = f.snapshot(&snapshotSpec{book: punct})
	f.displayCredit(snap, "author", 0, "...", 0xb2)
	// The shape that failed: the scan linked the unknown-author placeholder,
	// which the edit protection does not count, so the line came from the
	// layer — a name of one U+00A0, with no key to sort by.
	placeholder := f.book()
	unknown := f.legacyAuthorOf(placeholder, models.UnknownAuthorName)
	snap = f.snapshot(&snapshotSpec{book: placeholder})
	f.displayCredit(snap, "author", 0, "\u00a0", 0xb3)

	lines, err := NewPGBookSourceRepository(f.tx).BookAuthorDisplay(context.Background(), []int64{book, punct, placeholder})
	require.NoError(t, err)
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits, linked("Петров Иван", author)), lines[book])
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits), lines[punct])
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits, linked(models.UnknownAuthorName, unknown)), lines[placeholder])

	assert.Equal(t, 3, f.rebuild(book, punct, placeholder))
	rows := f.modelRows(placeholder)
	require.Len(t, rows, 1)
	assert.Equal(t, "legacy", rows[0].Source)
	rows = f.modelRows(book)
	require.Len(t, rows, 1)
	assert.Equal(t, "legacy", rows[0].Source)
	assert.Equal(t, "петров иван", rows[0].SortKey)
	rows = f.modelRows(punct)
	require.Len(t, rows, 1)
	assert.Equal(t, "none", rows[0].Source)
}

// poison makes the model refuse any row of one book, the way the
// shape check refused book 389625 in production.
func (f *authorSchemaFixture) poison(book int64) {
	f.t.Helper()
	f.exec(`ALTER TABLE book_author_display ADD CONSTRAINT poisoned_book CHECK (book_id <> ` +
		itoa(book) + `) NOT VALID`)
}

func itoa(n int64) string {
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// One book the model refuses does not stop the walk: the batch is retried a
// book at a time, the refused book is skipped and reported by its ID, every
// other book is built, and the cursor moves past it.
func TestTheWalkPassesABookTheModelRefuses(t *testing.T) {
	f := withAuthorSchemaTx(t)
	first, poisoned, last := f.book(), f.book(), f.book()
	for _, b := range []int64{first, poisoned, last} {
		f.legacyAuthorOf(b, "Автор Книги")
	}
	f.poison(poisoned)
	f.exec(`UPDATE book_author_display_reconcile SET walk_cursor = ?, walk_started = now(),
		walk_differed = 0, walk_read = 0, finished_at = NULL`, first-1)

	step, err := ReconcileAuthorDisplayStep(context.Background(), f.tx, 3, 24*time.Hour)
	require.NoError(t, err, "the walk must not stall on one book")
	assert.Equal(t, []AuthorDisplaySkip{{BookID: poisoned, SQLState: "23514"}}, step.Skipped)
	assert.Len(t, f.modelRows(first), 1)
	assert.Len(t, f.modelRows(last), 1)
	assert.Empty(t, f.modelRows(poisoned))
	var cursor *int64
	_, err = f.tx.QueryOne(&struct {
		WalkCursor **int64 `pg:"walk_cursor"`
	}{&cursor}, `SELECT walk_cursor FROM book_author_display_reconcile`)
	require.NoError(t, err)
	if cursor != nil {
		assert.GreaterOrEqual(t, *cursor, last, "the cursor passed the refused book")
	}
}

// The same holds for the mark queue: the refused book's mark is taken, the
// others are built.
func TestTheDrainPassesABookTheModelRefuses(t *testing.T) {
	f := withAuthorSchemaTx(t)
	first, poisoned, last := f.book(), f.book(), f.book()
	for _, b := range []int64{first, poisoned, last} {
		f.legacyAuthorOf(b, "Автор Книги")
	}
	f.poison(poisoned)
	require.NoError(t, MarkAuthorDisplayDirty(context.Background(), f.tx, first, poisoned, last))

	got, err := DrainAuthorDisplayMarks(context.Background(), f.tx, 10)
	require.NoError(t, err)
	assert.Equal(t, AuthorDisplayDrain{Marks: 3, Books: 3, Differed: 2,
		Skipped: []AuthorDisplaySkip{{BookID: poisoned, SQLState: "23514"}}}, got)
	assert.Len(t, f.modelRows(first), 1)
	assert.Len(t, f.modelRows(last), 1)
	assert.Empty(t, f.marks())
}
