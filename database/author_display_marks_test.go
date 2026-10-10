package database

import (
	"context"
	"testing"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every write that can change a book's author line marks the book for the
// read model in its own transaction: a snapshot, a selection, a review
// decision, an edit of the legacy authors; deleting a book takes its rows.
// A write that changes nothing marks nothing, so a bulk re-resolution of
// thousands of unchanged credits costs no marks.

func (f *authorSchemaFixture) clearMarks() {
	f.t.Helper()
	f.exec(`DELETE FROM book_author_display_dirty`)
}

func (f *authorSchemaFixture) bookOfCredit(credit int64) int64 {
	f.t.Helper()
	return f.returningID(`SELECT s.book_id FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE c.id = ?`, credit)
}

func TestAuthorDisplayMarks(t *testing.T) {
	scratch := jobsDB(t)
	storeDB, reviewDB = scratch, scratch
	t.Cleanup(func() { storeDB, reviewDB = nil, nil })

	t.Run("a selection that changes marks its book, one that changes nothing does not", func(t *testing.T) {
		f := withStoreTx(t)
		book, _ := f.persistBook(authorCredit(structuredSource(t)))
		f.clearMarks()
		r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
		resultID := f.storeResult(&r)
		out := decide(t, registeredPolicy(t, f), &r, resultID)

		f.resolve(&r, out)
		assert.Equal(t, []int64{book}, f.marks())

		f.clearMarks()
		f.resolve(&r, out)
		assert.Empty(t, f.marks(), "the same outcome again writes nothing and marks nothing")
	})

	for _, c := range []struct {
		name     string
		decision ReviewDecision
	}{
		{"accept", ReviewDecision{Action: ReviewAccept}},
		{"leave unresolved", ReviewDecision{Action: ReviewLeaveUnresolved}},
		{"classify as malformed", ReviewDecision{Action: ReviewClassify, Kind: models.NormalizationMalformed}},
	} {
		t.Run("a review decision marks its credits' books: "+c.name, func(t *testing.T) {
			f := withReviewTx(t)
			item, credit, _ := seedReviewItem(t, f)
			f.clearMarks()
			_, err := ApplyReviewAction(context.Background(), f.tx, item, 1, c.decision)
			require.NoError(t, err)
			assert.Equal(t, []int64{f.bookOfCredit(credit)}, f.marks())
		})
	}

	t.Run("the acceptance pass and its undo mark the books they select and put back", func(t *testing.T) {
		f := withStoreTx(t)
		ctx := context.Background()
		waiting, waitingID, credits := f.waitingInput(t, structuredSource(t), 2)
		f.registerClass("2", "structured_person", authornorm.NormalizerVersion)
		policy, err := LoadCurrentAcceptancePolicy(ctx, f.tx, authornorm.NormalizerVersion)
		require.NoError(t, err)
		d, err := policy.Decide(&waiting)
		require.NoError(t, err)
		out := &AutomaticOutcome{ResultID: waitingID, DecisionClass: waiting.DecisionClass, Decision: d}
		candidate := &AcceptanceCandidate{
			SourceFingerprint: waiting.SourceFingerprint[:], ExtractorVersion: waiting.ExtractorVersion, ResultID: waitingID,
		}
		books := []int64{f.bookOfCredit(credits[0]), f.bookOfCredit(credits[1])}
		f.clearMarks()

		_, err = SelectRegisteredCredits(ctx, f.tx, candidate, out, 0, 10)
		require.NoError(t, err)
		assert.Equal(t, books, f.marks())

		f.clearMarks()
		undo := &AutomaticOutcome{ResultID: waitingID, DecisionClass: waiting.DecisionClass, Decision: authornorm.Decision{
			Outcome: authornorm.OutcomeUnresolved, Reason: authornorm.ReasonPolicyNotRegistered, PolicyVersion: 2,
		}}
		_, err = DemoteAcceptanceSelections(ctx, f.tx, candidate, credits, undo)
		require.NoError(t, err)
		assert.Equal(t, books, f.marks())
	})

	t.Run("a new snapshot, and a return to an old one, mark the book", func(t *testing.T) {
		f := withStoreTx(t)
		book, _ := f.persistBook(authorCredit(structuredSource(t)))
		assert.Equal(t, []int64{book}, f.marks())

		f.clearMarks()
		f.persistVersion(book, "ffffffffffffffffffffffffffffffff", authorCredit(initialsSource(t)))
		assert.Equal(t, []int64{book}, f.marks())

		f.clearMarks()
		var md5 string
		_, err := f.tx.QueryOne(pg.Scan(&md5), `SELECT book_md5 FROM book_metadata_snapshot
			WHERE book_id = ? AND NOT is_current`, book)
		require.NoError(t, err)
		f.persistVersion(book, md5, authorCredit(structuredSource(t)))
		assert.Equal(t, []int64{book}, f.marks(), "the superseded version became current again")

		f.clearMarks()
		f.persistVersion(book, md5, authorCredit(structuredSource(t)))
		assert.Empty(t, f.marks(), "already current: nothing changed")
	})

	t.Run("an edit of the legacy authors marks the book", func(t *testing.T) {
		f := withStoreTx(t)
		edited := f.book()
		require.NoError(t, updateBookAuthorsFromUpdateRequest(f.tx, edited, []models.Author{{FullName: "Правка Админа"}}))
		assert.Equal(t, []int64{edited}, f.marks())

		f.clearMarks()
		rescanned := f.book()
		require.NoError(t, UpdateBookAuthors(f.tx, rescanned, []models.RescanAuthor{{Name: "Новое Сканирование"}}))
		assert.Equal(t, []int64{rescanned}, f.marks())
	})

	t.Run("deleting a book deletes its rows", func(t *testing.T) {
		f := withStoreTx(t)
		book := f.book()
		f.legacyAuthorOf(book, "Удаляемый Автор")
		f.rebuild(book)
		require.Len(t, f.modelRows(book), 1)
		_, err := deleteBooksWithLayer(f.tx, []int64{book})
		require.NoError(t, err)
		assert.Empty(t, f.modelRows(book))
	})
}
