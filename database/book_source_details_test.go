package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The book card's publisher and ISBN come from the book's current metadata
// snapshot and from nowhere else: a book without one, or with only superseded
// ones, has neither.

// persistSourceDetails writes one snapshot of the book with the given
// publisher and ISBN list and returns its ID; it becomes the current one. The
// card reads no credits, so the snapshot carries none.
func (f *authorSchemaFixture) persistSourceDetails(spec *extractionSpec, publisher *string, isbns []string) int64 {
	f.t.Helper()
	spec.outcome = models.BookMetadataSnapshotExtractedNoAuthor
	in := f.extraction(spec)
	in.SourcePublisher = publisher
	in.SourceISBNs = isbns
	result, err := PersistExtraction(f.tx, in)
	require.NoError(f.t, err)
	return result.SnapshotID
}

func TestBookSourceDetailsReadsOnlyTheCurrentSnapshot(t *testing.T) {
	f := withAuthorSchemaTx(t)
	repo := NewPGBookSourceRepository(f.tx)

	withSnapshot := f.book()
	f.persistSourceDetails(&extractionSpec{book: withSnapshot},
		ptr("  Издательство «Текущее»  "), []string{" 978-5-17-000000-1 ", "978-5-17-000000-2"})

	withoutSnapshot := f.book()

	// Two snapshots, the later one current: only its values may show.
	superseded := f.book()
	f.persistSourceDetails(&extractionSpec{book: superseded},
		ptr("Старое издательство"), []string{"978-0-00-000000-0"})
	f.persistSourceDetails(&extractionSpec{book: superseded, md5: bookMD5(superseded + 1_000)},
		ptr("Новое издательство"), []string{"978-1-11-111111-1"})

	// A current snapshot whose superseding one has no publisher and no ISBN:
	// the older values must not leak through.
	emptied := f.book()
	f.persistSourceDetails(&extractionSpec{book: emptied},
		ptr("Было издательство"), []string{"978-2-22-222222-2"})
	f.persistSourceDetails(&extractionSpec{book: emptied, md5: bookMD5(emptied + 1_000)},
		nil, []string{})

	// The file returns to its first version: the first snapshot is current
	// again and the later one, written after it, is history. Reading rows
	// without the current flag would let the later one win.
	returned := f.book()
	f.persistSourceDetails(&extractionSpec{book: returned},
		ptr("Первое издание"), []string{"978-3-33-333333-3"})
	f.persistSourceDetails(&extractionSpec{book: returned, md5: bookMD5(returned + 1_000)},
		ptr("Второе издание"), []string{"978-4-44-444444-4"})
	f.persistSourceDetails(&extractionSpec{book: returned},
		ptr("Первое издание"), []string{"978-3-33-333333-3"})

	// Only history and no current snapshot: nothing to show, whatever order
	// the rows come back in.
	historyOnly := f.book()
	historic := f.persistSourceDetails(&extractionSpec{book: historyOnly},
		ptr("Историческое издательство"), []string{"978-6-66-666666-6"})
	f.exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE id = ?`, historic)

	// Blank values are no values.
	blank := f.book()
	f.persistSourceDetails(&extractionSpec{book: blank}, ptr("   "), []string{"", "  "})

	got, err := repo.BookSourceDetails(context.Background(),
		[]int64{withSnapshot, withoutSnapshot, superseded, emptied, returned, historyOnly, blank})
	require.NoError(t, err)

	assert.Equal(t, models.BookSourceDetail{
		Publisher: ptr("Издательство «Текущее»"),
		ISBN:      []string{"978-5-17-000000-1", "978-5-17-000000-2"},
	}, got[withSnapshot])
	assert.Equal(t, models.BookSourceDetail{
		Publisher: ptr("Новое издательство"),
		ISBN:      []string{"978-1-11-111111-1"},
	}, got[superseded])
	assert.Equal(t, models.BookSourceDetail{
		Publisher: ptr("Первое издание"),
		ISBN:      []string{"978-3-33-333333-3"},
	}, got[returned])
	assert.Equal(t, models.BookSourceDetail{ISBN: []string{}}, got[emptied])
	assert.NotNil(t, got[emptied].ISBN, "an empty ISBN list is [], never null")
	assert.Equal(t, models.BookSourceDetail{ISBN: []string{}}, got[blank])
	assert.NotContains(t, got, withoutSnapshot, "a book without a snapshot has no entry")
	assert.NotContains(t, got, historyOnly, "a superseded snapshot is never shown")
	assert.Len(t, got, 5)
}

func TestBookSourceDetailsOfNoBooksAsksNothing(t *testing.T) {
	// A nil handle would panic on any query: an empty page must not reach it.
	got, err := NewPGBookSourceRepository(nil).BookSourceDetails(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// persistCredits writes one current snapshot of the book carrying the given
// credits, in the given (XML) order.
func (f *authorSchemaFixture) persistCredits(book int64, credits ...ExtractionCredit) int64 {
	f.t.Helper()
	spec := &extractionSpec{book: book, credits: credits, outcome: models.BookMetadataSnapshotExtractedNoAuthor}
	for i := range credits {
		if credits[i].Role == models.ContributorRoleAuthor {
			spec.outcome = models.BookMetadataSnapshotExtracted
		}
	}
	result, err := PersistExtraction(f.tx, f.extraction(spec))
	require.NoError(f.t, err)
	return result.SnapshotID
}

func TestBookSourceDetailsTranslators(t *testing.T) {
	f := withAuthorSchemaTx(t)
	repo := NewPGBookSourceRepository(f.tx)
	tr := models.ContributorRoleTranslator

	// The credits are written straight in the order positions 2, 0, 1, so
	// that the row order and the position order disagree: Вера (0) is
	// first, Анна (1) second, Борис (2) third.
	team := f.book()
	snapshot := f.persistCredits(team, creditOf(t, models.ContributorRoleAuthor, "Аркадий", "Стругацкий"))
	for _, c := range []struct {
		position    int
		first, last string
	}{{2, "Борис", "Второй"}, {0, "Вера", "Третья"}, {1, "Анна", "Первая"}} {
		f.exec(`INSERT INTO book_contributor_credit
			(snapshot_id, role, position, source_first_name, source_last_name, source_display_name, source_fingerprint)
			VALUES (?, 'translator', ?, ?, ?, ?, sha256(convert_to(?, 'UTF8')))`,
			snapshot, c.position, c.first, c.last, c.first+" "+c.last, c.first+" "+c.last)
	}

	// Two names, one of them credited twice: no third name remains, so no
	// «и др.».
	pair := f.book()
	f.persistCredits(pair,
		creditOf(t, tr, "Нора", "Галь"),
		creditOf(t, tr, "Нора", "Галь"),
		creditOf(t, tr, "Рита", "Райт-Ковалёва"))

	// Three credits, two of them the same name: the duplicate is not a third
	// name either.
	twiceThenOne := f.book()
	f.persistCredits(twiceThenOne,
		creditOf(t, tr, "Нора", "Галь"),
		creditOf(t, tr, "Нора", "Галь"),
		creditOf(t, tr, "Рита", "Райт-Ковалёва"),
		creditOf(t, tr, "Рита", "Райт-Ковалёва"))

	single := f.book()
	f.persistCredits(single, creditOf(t, tr, "Корней", "Чуковский"))

	got, err := repo.BookSourceDetails(context.Background(), []int64{team, pair, twiceThenOne, single})
	require.NoError(t, err)

	assert.Equal(t, []string{"Вера Третья", "Анна Первая"}, got[team].Translators,
		"the first two by position, never the author")
	assert.True(t, got[team].TranslatorsMore, "a third name remains")
	assert.Equal(t, []string{"Нора Галь", "Рита Райт-Ковалёва"}, got[pair].Translators)
	assert.False(t, got[pair].TranslatorsMore, "a duplicate is not a third name")
	assert.Equal(t, []string{"Нора Галь", "Рита Райт-Ковалёва"}, got[twiceThenOne].Translators)
	assert.False(t, got[twiceThenOne].TranslatorsMore)
	assert.Equal(t, []string{"Корней Чуковский"}, got[single].Translators)
	assert.False(t, got[single].TranslatorsMore)
}

func TestBookSourceDetailsNoTranslator(t *testing.T) {
	f := withAuthorSchemaTx(t)
	repo := NewPGBookSourceRepository(f.tx)

	// An author credit is not a translator one.
	authored := f.book()
	f.persistCredits(authored, creditOf(t, models.ContributorRoleAuthor, "Лев", "Толстой"))

	// A translator of a superseded snapshot is not the current one's.
	superseded := f.book()
	f.persistCredits(superseded, creditOf(t, models.ContributorRoleTranslator, "Старый", "Переводчик"))
	f.persistSourceDetails(&extractionSpec{book: superseded, md5: bookMD5(superseded + 1_000)},
		ptr("Издательство"), []string{})

	got, err := repo.BookSourceDetails(context.Background(), []int64{authored, superseded})
	require.NoError(t, err)
	require.Contains(t, got, authored)
	require.Contains(t, got, superseded)
	assert.Nil(t, got[authored].Translators, "nil, so the list response omits the field")
	assert.False(t, got[authored].TranslatorsMore)
	assert.Nil(t, got[superseded].Translators)
}
