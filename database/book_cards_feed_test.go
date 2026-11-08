package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"

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
	// The scan marks its card with the insert, as it does in production.
	require.NoError(t, MarkBookVariantsDirty(context.Background(), f.tx, fresh))
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

// rebuildCommitted rebuilds the book's card in a transaction of its own and
// drains its mark, as the worker does, committed on the scratch database.
func (f *cardsFixture) rebuildCommitted(book int64) {
	f.t.Helper()
	require.NoError(f.t, f.scratch.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		if err := rebuildBookVariants(context.Background(), tx, book, 1); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM book_variant_dirty WHERE book_id = ?`, book)
		return err
	}))
}

// freshAndCard is a card of two old files and a new file of the same book the
// read model has no row for yet: on the feed, a fresh book above the card. It
// returns the new file.
func (f *cardsFixture) freshAndCard() (fresh int64) {
	f.t.Helper()
	entity := f.entity("Гаррисон")
	old := f.variantBook("Стальная крыса", []string{entity}, withDoc("rat-a", "1.0"), addedAgo(30))
	f.variantBook("Стальная крыса", []string{entity}, withDoc("rat-b", "1.0"), addedAgo(29))
	f.rebuild(old)
	fresh = f.variantBook("Стальная крыса", []string{entity}, withDoc("rat-c", "1.0"), addedAgo(1))
	require.NoError(f.t, MarkBookVariantsDirty(context.Background(), f.tx, fresh))
	f.commit()
	return fresh
}

// The fresh book's rebuild commits between the page's selection and its read:
// the book joins the card that is on the same page. One entry, the card's,
// at the fresh book's earlier place, read as it is now.
func TestFeedFreshJoinsCardOnPage(t *testing.T) {
	f := cardsDB(t)
	fresh := f.freshAndCard()

	f.onceBetweenSelectAndRead(func() { f.rebuildCommitted(fresh) })
	books, entries, _ := f.feed(models.BookFilters{})
	require.Len(t, books, 1, "the file is listed once, inside its card")
	assert.Equal(t, fresh, books[0].ID)
	require.NotNil(t, entries[0].Variants)
	assert.Equal(t, 3, entries[0].Variants.Count)
	assert.Equal(t, models.BookVariantReasonNewVariant, entries[0].Variants.Reason)
}

// The same with the card on the next page: the fresh book becomes the card in
// its place, and the next page does not list the card again.
func TestFeedFreshJoinsCardOnNextPage(t *testing.T) {
	f := cardsDB(t)
	fresh := f.freshAndCard()

	f.onceBetweenSelectAndRead(func() { f.rebuildCommitted(fresh) })
	books, entries, _ := f.feed(models.BookFilters{Limit: 1})
	assert.Equal(t, []int64{fresh}, cardIDs(books))
	require.NotNil(t, entries[0].Variants, "the fresh book became its card")
	assert.Equal(t, 3, entries[0].Variants.Count)

	books, _, total := f.feed(models.BookFilters{Limit: 1, Offset: 1})
	assert.Empty(t, books, "the card is not listed twice across pages")
	assert.Equal(t, 1, total)
}

// A book shown again is in the feed at once, before any worker drains the
// author line: the un-hide queues its card in the same transaction.
func TestFeedReShownBeforeDrain(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Саймак")
	older := f.variantBook("Город", []string{entity})
	newer := f.variantBook("Пересадочная станция", []string{entity})
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, older)
	f.rebuild(older, newer)
	f.exec(`DELETE FROM book_variant_dirty`)
	f.commit()
	require.Nil(t, f.member(older), "hidden, it has no card")

	shown := false
	_, err := UpdateBook(models.BookUpdateRequest{ID: older, DuplicateHidden: &shown})
	require.NoError(t, err)

	books, _, total := f.feed(models.BookFilters{})
	assert.Equal(t, 2, total)
	assert.ElementsMatch(t, []int64{older, newer}, cardIDs(books))
}

// An upload that commits after a higher id already has its card: the lower id
// is found through its mark, not through the highest id.
func TestFeedLowerIDUploadBehindHigher(t *testing.T) {
	f := cardsDB(t)
	// Two authors: the higher id's rebuild must not reach the lower one.
	lower := f.variantBook("Цивилизация статуса", []string{f.entity("Шекли")})
	higher := f.variantBook("Обмен разумов", []string{f.entity("Брэдбери")})
	f.rebuild(higher)
	f.exec(`DELETE FROM book_variant_dirty`)
	require.NoError(t, MarkBookVariantsDirty(context.Background(), f.tx, lower))
	f.commit()
	require.Nil(t, f.member(lower))

	books, _, total := f.feed(models.BookFilters{})
	assert.Equal(t, 2, total)
	assert.Equal(t, []int64{higher, lower}, cardIDs(books))
}

// A file that moved into another card on the page mid-request is listed once:
// replacements never take a file the page already shows.
func TestFeedReplacementSkipsAFileShownOnAnotherCard(t *testing.T) {
	f := cardsDB(t)
	e1, e2 := f.entity("Первый"), f.entity("Второй")
	x1 := f.variantBook("Бета", []string{e1}, withDoc("beta-1", "1.0"), addedAgo(30))
	// «Альфа» has no mark (one day): it picks its file at read time.
	f.variantBook("Альфа", []string{e2}, withDoc("alpha-1", "1.0"), addedAgo(20))
	y2 := f.variantBook("Альфа", []string{e2}, withDoc("alpha-2", "1.0"), addedAgo(20))
	x2 := f.variantBook("Бета", []string{e1}, withDoc("beta-2", "1.0"), addedAgo(1))
	f.rebuild(x1, y2)
	f.commit()

	// The card of «Бета» is marked and shows x2; the card of «Альфа» picks
	// its file at read time. In between, x2 becomes the best file of «Альфа».
	f.onceBetweenSelectAndRead(func() {
		_, err := f.scratch.Exec(`UPDATE book_author_display SET entity_ids = ARRAY[?::uuid] WHERE book_id = ?`, e2, x2)
		require.NoError(t, err)
		_, err = f.scratch.Exec(`UPDATE opds_catalog_book SET title = 'Альфа', cover = true WHERE id = ?`, x2)
		require.NoError(t, err)
		f.rebuildCommitted(x2)
	})
	books, _, _ := f.feed(models.BookFilters{})
	seen := map[int64]int{}
	for i := range books {
		seen[books[i].ID]++
	}
	assert.Equal(t, 1, seen[x2], "x2 is listed once")
	assert.Len(t, books, 2)
}
