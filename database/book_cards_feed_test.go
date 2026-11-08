package database

import (
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The feed lists cards by their newest file. A new file of an old book lifts
// its card to the top, opened on that file and marked «новый вариант» or
// «новая редакция»; files of one day mark nothing. Visibility is checked at
// read time, as on the scoped lists.

//nolint:gocritic // the list takes its filters by value
func (f *cardsFixture) feed(filters models.BookFilters) ([]models.Book, []CardEntry, int) {
	f.t.Helper()
	return f.cards(0, filters)
}

func TestFeedCardsOrderByNewestFile(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Герберт")
	old1 := f.variantBook("Дюна", []string{entity}, withDoc("dune-a", "1.0"), addedAgo(30))
	f.variantBook("Дюна", []string{entity}, withDoc("dune-b", "1.0"), addedAgo(29))
	single := f.variantBook("Мессия Дюны", []string{entity}, addedAgo(5))
	f.rebuild(old1, single)
	f.commit()

	books, _, total := f.feed(models.BookFilters{})
	require.Equal(t, 2, total)
	assert.Equal(t, single, books[0].ID, "before the new file the single book is newer")

	fresh := f.variantBook("Дюна", []string{entity}, withDoc("dune-c", "1.0"), addedAgo(1))
	// The old file has a cover and stays the default: the card opens on the
	// new file for its mark, not for its rank.
	f.exec(`UPDATE opds_catalog_book SET cover = true WHERE id = ?`, old1)
	f.rebuild(fresh)
	require.Equal(t, 1, f.member(old1).Rank)
	f.commit()

	books, entries, total := f.feed(models.BookFilters{})
	assert.Equal(t, 2, total)
	assert.Equal(t, []int64{fresh, single}, cardIDs(books), "the card rises, opened on its new file")
	require.NotNil(t, entries[0].Variants)
	assert.Equal(t, models.BookVariantReasonNewVariant, entries[0].Variants.Reason)
	assert.Equal(t, 3, entries[0].Variants.Count)
	assert.Nil(t, entries[1].Variants)
}

func TestFeedCardsNewRevision(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Белова")
	f.variantBook("Ты дура", []string{entity}, withDoc("samizdat", "1.0"), addedAgo(20))
	old := f.variantBook("Ты дура", []string{entity}, withDoc("other", "1.0"), addedAgo(19))
	fresh := f.variantBook("Ты дура", []string{entity}, withDoc("samizdat", "2.0"), addedAgo(1))
	f.rebuild(old)
	f.commit()

	books, entries, _ := f.feed(models.BookFilters{})
	assert.Equal(t, []int64{fresh}, cardIDs(books))
	require.NotNil(t, entries[0].Variants)
	assert.Equal(t, models.BookVariantReasonNewRevision, entries[0].Variants.Reason)
}

func TestFeedCardsSameDayNoMark(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Лем")
	files := f.group3(entity, "Солярис")
	// group3 spreads its files over three days; bring them to one.
	f.exec(`UPDATE opds_catalog_book SET registerdate = now() WHERE id IN (?, ?, ?)`, files[0], files[1], files[2])
	// The oldest file has a cover: the default is not the newest file.
	oldest := min(files[0], files[1], files[2])
	f.exec(`UPDATE opds_catalog_book SET cover = true WHERE id = ?`, oldest)
	f.rebuild(files[0])
	f.commit()

	books, entries, _ := f.feed(models.BookFilters{})
	require.Len(t, books, 1)
	require.NotNil(t, entries[0].Variants)
	assert.Empty(t, entries[0].Variants.Reason)
	assert.Equal(t, oldest, books[0].ID, "the card opens on its default, not on its newest file")
	assert.Equal(t, 1, f.member(books[0].ID).Rank)
}

func TestFeedCardsLanguage(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Азимов")
	ru := f.group3(entity, "Я, робот")
	en := f.variantBook("I, Robot", []string{entity}, func(s *variantSpec) { s.lang = "en" })
	f.rebuild(en)
	f.commit()

	books, _, total := f.feed(models.BookFilters{Lang: "en"})
	assert.Equal(t, 1, total)
	assert.Equal(t, []int64{en}, cardIDs(books))
	books, _, total = f.feed(models.BookFilters{Lang: "ru"})
	assert.Equal(t, 1, total)
	assert.Equal(t, []int64{ru[0]}, cardIDs(books))
	_, _, total = f.feed(models.BookFilters{Lang: AllLanguages})
	assert.Equal(t, 2, total)
}

func TestFeedCardsTotalCountsGroups(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Каттнер")
	for _, title := range []string{"Первая", "Вторая", "Третья"} {
		a := f.variantBook(title, []string{entity}, withDoc(title+"-1", "1.0"), addedAgo(2))
		f.variantBook(title, []string{entity}, withDoc(title+"-2", "1.0"), addedAgo(2))
		f.rebuild(a)
	}
	single := f.variantBook("Одна", []string{entity})
	f.rebuild(single)
	f.commit()

	seen := map[int64]bool{}
	for offset := 0; offset < 6; offset += 2 {
		books, _, total := f.feed(models.BookFilters{Limit: 2, Offset: offset})
		assert.Equal(t, 4, total)
		for i := range books {
			assert.False(t, seen[books[i].ID], "a card is listed once")
			seen[books[i].ID] = true
		}
	}
	assert.Len(t, seen, 4)
}

// A visible book the read model has no row for yet (a fresh upload before the
// drain) is in the feed as a card of its own, by its id.
func TestFeedCardsBookWithoutRow(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Брэдбери")
	files := f.group3(entity, "Вино из одуванчиков")
	fresh := f.variantBook("451 градус", []string{entity})
	f.commit()

	books, entries, total := f.feed(models.BookFilters{})
	assert.Equal(t, 2, total)
	assert.Equal(t, fresh, books[0].ID)
	assert.Nil(t, entries[0].Variants)
	assert.Contains(t, []int64{files[0], files[1], files[2]}, books[1].ID)
}

// The marked new file hidden after the card was built: the card opens on its
// best visible file, without a mark, and counts what is visible.
func TestFeedCardsHiddenNewestFallsBack(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Желязны")
	old1 := f.variantBook("Амбер", []string{entity}, withDoc("amber-a", "1.0"), addedAgo(30))
	old2 := f.variantBook("Амбер", []string{entity}, withDoc("amber-b", "1.0"), addedAgo(29))
	fresh := f.variantBook("Амбер", []string{entity}, withDoc("amber-c", "1.0"), addedAgo(1))
	f.rebuild(old1)
	f.commit()
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, fresh)
	f.commit()

	books, entries, total := f.feed(models.BookFilters{})
	assert.Equal(t, 1, total)
	require.Len(t, books, 1)
	assert.Contains(t, []int64{old1, old2}, books[0].ID)
	require.NotNil(t, entries[0].Variants)
	assert.Empty(t, entries[0].Variants.Reason, "the mark belongs to the hidden file")
	assert.Equal(t, 2, entries[0].Variants.Count)
}

// Hidden between the page's selection and its read: the replacement chain of
// the scoped lists applies to the feed too.
func TestFeedCardsHiddenBetweenSelectionAndRead(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Шекли")
	files := f.group3(entity, "Обмен разумов")
	f.commit()

	f.onceBetweenSelectAndRead(func() { f.hideNow("approved", files[0]) })
	afterCardsReplaced = func() {
		afterCardsReplaced = nil
		f.hideNow("duplicate_hidden", files[1])
	}
	t.Cleanup(func() { afterCardsReplaced = nil })

	books, entries, _ := f.feed(models.BookFilters{})
	assert.Equal(t, []int64{files[2]}, cardIDs(books))
	assert.Nil(t, entries[0].Variants)
}
