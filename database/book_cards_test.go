package database

import (
	"context"
	"testing"

	"gopds-api/internal/bookvariants"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The grouped list's own rules: what a card shows, where it sits, what it
// counts, and the variants and the stars of one card.

// star favorites the book for the user; stars are ordered by insertion.
func (f *cardsFixture) star(user, book int64) {
	f.t.Helper()
	f.exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?)`, user, book)
}

//nolint:gocritic // the list takes its filters by value
func (f *cardsFixture) cards(userID int64, filters models.BookFilters) ([]models.Book, []CardEntry, int) {
	f.t.Helper()
	if filters.Limit == 0 {
		filters.Limit = 10
	}
	books, entries, total, err := GetBookCards(context.Background(), userID, filters)
	require.NoError(f.t, err)
	require.Len(f.t, entries, len(books))
	for i := range books {
		require.Equal(f.t, books[i].ID, entries[i].BookID, "entries are index-aligned with the books")
	}
	return books, entries, total
}

func TestBookCardsSkipInvisibleMember(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Лем")
	files := f.group3(entity, "Солярис")
	f.commit()

	// Hidden after the rebuild: the stored row still says it is rank 1.
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, files[0])
	f.commit()

	books, entries, total := f.cards(0, models.BookFilters{AuthorEntity: uuidOf(t, entity)})
	assert.Equal(t, 1, total)
	assert.Equal(t, []int64{files[1]}, cardIDs(books), "the card opens on the next file")
	require.NotNil(t, entries[0].Variants)
	assert.Equal(t, 2, entries[0].Variants.Count, "the count drops with the hidden file")
	assert.Empty(t, entries[0].Variants.Reason, "the best visible file is the default")

	// Unapproved the same way, then the last visible file stands alone.
	f.exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, files[1])
	f.commit()
	books, entries, total = f.cards(0, models.BookFilters{AuthorEntity: uuidOf(t, entity)})
	assert.Equal(t, 1, total)
	assert.Equal(t, []int64{files[2]}, cardIDs(books))
	assert.Nil(t, entries[0].Variants, "one visible file is a single book")

	// Deleted outright: nothing left, and no hidden file shows up instead.
	// Deleted the way an archive deletion does it; its card row stays until
	// the drain takes it out.
	_, err := deleteBooksWithLayer(f.tx, []int64{files[2]})
	require.NoError(t, err)
	f.exec(`DELETE FROM book_variant_dirty`)
	f.commit()
	books, _, total = f.cards(0, models.BookFilters{AuthorEntity: uuidOf(t, entity)})
	assert.Equal(t, 0, total)
	assert.Empty(t, books)
}

func TestBookCardsFavoritesOneEntryAtFirstPosition(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Брэдбери")
	files := f.group3(entity, "Вино из одуванчиков")
	other := f.entity("Азимов")
	x := f.variantBook("Конец вечности", []string{other})
	f.rebuild(x)
	user := f.user()
	a, b := files[1], files[2]
	// Newest star first: A = 1, X = 2, B = 3.
	f.star(user, b)
	f.star(user, x)
	f.star(user, a)
	f.commit()

	books, entries, total := f.cards(user, models.BookFilters{Fav: true})
	assert.Equal(t, 2, total, "totals count cards")
	assert.Equal(t, []int64{a, x}, cardIDs(books), "the card sits at its first position, on its best saved file")
	require.NotNil(t, entries[0].Variants)
	assert.Equal(t, models.BookVariantReasonSaved, entries[0].Variants.Reason, "A is not the default")
	assert.True(t, entries[0].Fav)
	assert.True(t, books[0].Fav)
	assert.Nil(t, entries[1].Variants)

	// The default hidden, the saved A is the best visible file: no reason.
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, files[0])
	f.commit()
	books, entries, _ = f.cards(user, models.BookFilters{Fav: true})
	assert.Equal(t, []int64{a, x}, cardIDs(books))
	assert.Empty(t, entries[0].Variants.Reason, "A is the default once rank 1 is hidden")
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = false WHERE id = ?`, files[0])

	// Saving the default too: the card shows it, with no reason.
	f.star(user, files[0])
	f.commit()
	books, entries, _ = f.cards(user, models.BookFilters{Fav: true})
	assert.Equal(t, []int64{files[0], x}, cardIDs(books))
	assert.Empty(t, entries[0].Variants.Reason)
}

func TestBookCardsCollectionKeepsCuratedFile(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Толкин")
	files := f.group3(entity, "Хоббит")
	collection := f.id()
	f.exec(`INSERT INTO book_collections (id, name, is_public, is_curated) VALUES (?, 'Сказки', true, true)`, collection)
	f.exec(`INSERT INTO book_collection_items (collection_id, book_id, external_title, match_status, position)
		VALUES (?, ?, 'Хоббит', ?, 1)`, collection, files[2], models.MatchStatusManual)
	userCollection := f.id()
	owner := f.user()
	f.exec(`INSERT INTO book_collections (id, user_id, name, is_public, is_curated) VALUES (?, ?, 'Моя', false, false)`,
		userCollection, owner)
	f.exec(`INSERT INTO book_collection_books (book_collection_id, book_id, position) VALUES (?, ?, 1)`,
		userCollection, files[1])
	f.commit()

	books, entries, total := f.cards(0, models.BookFilters{CuratedCollection: collection})
	assert.Equal(t, 1, total)
	assert.Equal(t, []int64{files[2]}, cardIDs(books), "a curated pick of one file stays that file")
	require.NotNil(t, entries[0].Variants)
	assert.Equal(t, models.BookVariantReasonSaved, entries[0].Variants.Reason)
	assert.Equal(t, 3, entries[0].Variants.Count)

	books, entries, _ = f.cards(owner, models.BookFilters{Collection: userCollection})
	assert.Equal(t, []int64{files[1]}, cardIDs(books))
	assert.Equal(t, models.BookVariantReasonSaved, entries[0].Variants.Reason)
}

func TestBookCardsPopularCountsPerGroup(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Гаррисон")
	files := f.group3(entity, "Неукротимая планета")
	other := f.entity("Шекли")
	single := f.variantBook("Обмен разумов", []string{other})
	f.rebuild(single)
	u1, u2, u3 := f.user(), f.user(), f.user()
	// Two files of the group, two users each, three users in all.
	f.star(u1, files[0])
	f.star(u2, files[0])
	f.star(u2, files[1])
	f.star(u3, files[1])
	// The single book: two users, more than any one file of the group has.
	f.star(u1, single)
	f.star(u3, single)
	f.commit()

	books, entries, total := f.cards(u1, models.BookFilters{UsersFavorites: true})
	assert.Equal(t, 2, total)
	assert.Equal(t, []int64{files[0], single}, cardIDs(books), "three users outrank two")
	assert.Equal(t, 3, entries[0].FavoriteCount)
	assert.Equal(t, 3, books[0].FavoriteCount)
	assert.Equal(t, 2, entries[1].FavoriteCount)
	assert.True(t, entries[0].Fav, "u1 starred a file of the card")
}

// Readers, not stars: two readers who each starred two files of a card are
// two, as many as a newer book two readers starred; the newer comes first.
func TestBookCardsPopularCountsReadersNotStars(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Азимов")
	files := f.group3(entity, "Основание")
	single := f.variantBook("Сами боги", []string{entity})
	f.rebuild(single)
	u1, u2 := f.user(), f.user()
	for _, s := range []struct{ user, book int64 }{
		{u1, files[0]}, {u1, files[1]}, {u2, files[0]}, {u2, files[1]}, {u1, single}, {u2, single},
	} {
		f.star(s.user, s.book)
	}
	f.commit()

	books, entries, _ := f.cards(0, models.BookFilters{UsersFavorites: true})
	assert.Equal(t, []int64{single, files[0]}, cardIDs(books), "a tie in readers puts the newest first")
	assert.Equal(t, 2, entries[1].FavoriteCount)
}

func TestBookCardsPaginationCountsGroups(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Каттнер")
	for _, title := range []string{"Первая", "Вторая", "Третья", "Четвёртая", "Пятая"} {
		a := f.variantBook(title, []string{entity}, withDoc(title+"-1", "1.0"), addedAgo(2))
		f.variantBook(title, []string{entity}, withDoc(title+"-2", "1.0"), addedAgo(1))
		f.rebuild(a)
	}
	f.commit()

	seen := map[int64]int{}
	pages := 0
	for offset := 0; ; offset += 2 {
		books, entries, total := f.cards(0, models.BookFilters{AuthorEntity: uuidOf(t, entity), Limit: 2, Offset: offset})
		assert.Equal(t, 5, total)
		if len(books) == 0 {
			break
		}
		pages++
		for i := range books {
			seen[books[i].ID]++
			require.NotNil(t, entries[i].Variants)
			assert.Equal(t, 2, entries[i].Variants.Count)
		}
	}
	assert.Equal(t, 3, pages)
	assert.Len(t, seen, 5)
	for id, n := range seen {
		assert.Equal(t, 1, n, "book %d listed once", id)
	}
}

func TestBookCardsBeforeBackfill(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Саймак")
	files := f.group3(entity, "Город")
	// No rebuild: no member row at all.
	lone := f.variantBook("Заповедник гоблинов", []string{entity})
	f.commit()

	books, entries, total := f.cards(0, models.BookFilters{AuthorEntity: uuidOf(t, entity)})
	assert.Equal(t, 2, total)
	assert.Equal(t, []int64{lone, files[0]}, cardIDs(books), "a book without a row is its own entry")
	assert.Nil(t, entries[0].Variants)
	assert.NotNil(t, entries[1].Variants)
}

func TestUnfavBookGroupRemovesEveryFile(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Беляев")
	files := f.group3(entity, "Человек-амфибия")
	unrelated := f.variantBook("Голова профессора Доуэля", []string{entity})
	f.rebuild(unrelated)
	user, other := f.user(), f.user()
	f.star(user, files[0])
	f.star(user, files[2])
	f.star(user, unrelated)
	f.star(other, files[0])
	f.commit()

	haveFavs, err := UnfavBookGroup(user, files[1])
	require.NoError(t, err)
	assert.True(t, haveFavs)

	var left []int64
	_, err = f.scratch.Query(&left, `SELECT book_id FROM favorite_books WHERE user_id = ? ORDER BY book_id`, user)
	require.NoError(t, err)
	assert.Equal(t, []int64{unrelated}, left, "both starred files of the card are gone, the unrelated star stays")
	n, err := f.scratch.Model((*models.UserToBook)(nil)).Where("user_id = ?", other).Count()
	require.NoError(t, err)
	assert.Equal(t, 1, n, "another reader's star is untouched")

	// A book that is a card of its own unstars itself only.
	haveFavs, err = UnfavBookGroup(user, unrelated)
	require.NoError(t, err)
	assert.False(t, haveFavs)

	_, err = UnfavBookGroup(user, f.id())
	assert.ErrorIs(t, err, pg.ErrNoRows, "an unknown book is an error, as for a single unstar")
}

func TestBookVariantsOfOrdersByRank(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Стругацкие")
	title := "Понедельник начинается в субботу"
	// The oldest file has a cover, so it ranks first: the rank order is
	// neither the id order nor its reverse.
	oldest := f.variantBook(title, []string{entity}, withDoc("pnvs-a", "1.0"), addedAgo(3))
	f.exec(`UPDATE opds_catalog_book SET cover = true WHERE id = ?`, oldest)
	f.variantBook(title, []string{entity}, withDoc("pnvs-b", "1.0"), addedAgo(2))
	f.variantBook(title, []string{entity}, withDoc("pnvs-c", "1.0"), addedAgo(1))
	f.rebuild(oldest)
	var files [3]int64
	_, err := f.tx.Query(pg.Scan(&files[0], &files[1], &files[2]), `
		SELECT (array_agg(book_id ORDER BY variant_rank))[1], (array_agg(book_id ORDER BY variant_rank))[2],
			(array_agg(book_id ORDER BY variant_rank))[3]
		FROM book_variant_member WHERE group_key = (SELECT group_key FROM book_variant_member WHERE book_id = ?)`, oldest)
	require.NoError(t, err)
	require.Equal(t, oldest, files[0])
	require.Greater(t, files[1], files[2], "the newest uncovered file ranks second")
	user := f.user()
	f.star(user, files[2])
	f.commit()

	books, variants, err := BookVariantsOf(context.Background(), files[2], user)
	require.NoError(t, err)
	assert.Equal(t, []int64{files[0], files[1], files[2]}, cardIDs(books))
	require.Len(t, variants, 3)
	key := f.member(files[0]).GroupKey
	for i, v := range variants {
		assert.Equal(t, books[i].ID, v.BookID)
		assert.Equal(t, i+1, v.Rank)
		assert.Equal(t, key, v.GroupKey.String())
		assert.Equal(t, i == 0, v.Default)
		assert.True(t, v.Latest)
		assert.Equal(t, "1.0", v.DocVersion)
	}
	assert.True(t, books[2].Fav, "the caller's star is on its file")
	assert.False(t, books[0].Fav)

	// A book with no row yet is a card of one.
	lone := f.variantBook("Отель «У погибшего альпиниста»", []string{entity})
	f.commit()
	books, variants, err = BookVariantsOf(context.Background(), lone, 0)
	require.NoError(t, err)
	assert.Equal(t, []int64{lone}, cardIDs(books))
	assert.Equal(t, bookvariants.SoloKey(lone), variants[0].GroupKey)
	assert.True(t, variants[0].Default)
}

func TestBookVariantsOfHidesInvisible(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Ефремов")
	files := f.group3(entity, "Туманность Андромеды")
	f.commit()
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, files[0])
	f.exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, files[2])
	f.commit()

	books, variants, err := BookVariantsOf(context.Background(), files[1], 0)
	require.NoError(t, err)
	assert.Equal(t, []int64{files[1]}, cardIDs(books))
	assert.True(t, variants[0].Default, "the best visible file is the default")

	for _, hidden := range []int64{files[0], files[2], f.id()} {
		_, _, err = BookVariantsOf(context.Background(), hidden, 0)
		assert.ErrorIs(t, err, pg.ErrNoRows, "book %d is not visible", hidden)
	}
}

// The junction scopes narrow the grouped list by subquery: a genre, a series
// and a legacy author each list the card once, and a scope with no book lists
// nothing rather than the whole catalog.
func TestBookCardsJunctionScopes(t *testing.T) {
	f := cardsDB(t)
	entity := f.entity("Желязны")
	files := f.group3(entity, "Хроники Амбера")
	other := f.variantBook("Князь света", []string{entity})
	f.rebuild(other)
	genre := f.returningID(`INSERT INTO opds_catalog_genre (genre, title) VALUES ('sf', 'Фантастика') RETURNING id`)
	series := f.returningID(`INSERT INTO opds_catalog_series (ser, lang_code) VALUES ('Амбер', 0) RETURNING id`)
	author := f.returningID(`INSERT INTO opds_catalog_author (full_name) VALUES ('Желязны Роджер') RETURNING id`)
	for _, b := range files {
		f.exec(`INSERT INTO opds_catalog_bgenre (book_id, genre_id) VALUES (?, ?)`, b, genre)
		f.exec(`INSERT INTO opds_catalog_bseries (book_id, ser_id, ser_no) VALUES (?, ?, 0)`, b, series)
		f.exec(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?)`, b, author)
	}
	f.exec(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?)`, other, author)
	empty := f.returningID(`INSERT INTO opds_catalog_genre (genre, title) VALUES ('poetry', 'Поэзия') RETURNING id`)
	// A genre that holds the card's second file only.
	partial := f.returningID(`INSERT INTO opds_catalog_genre (genre, title) VALUES ('adv', 'Приключения') RETURNING id`)
	f.exec(`INSERT INTO opds_catalog_bgenre (book_id, genre_id) VALUES (?, ?)`, files[1], partial)
	f.commit()

	// The card shows its best file in the genre; a genre saves nothing, so
	// there is no reason, and the count is still the card's.
	books, entries, _ := f.cards(0, models.BookFilters{Genre: int(partial)})
	assert.Equal(t, []int64{files[1]}, cardIDs(books))
	require.NotNil(t, entries[0].Variants)
	assert.Empty(t, entries[0].Variants.Reason, "only a saved list says saved")
	assert.Equal(t, 3, entries[0].Variants.Count)

	for _, tc := range []struct {
		name    string
		filters models.BookFilters
		want    []int64
	}{
		{"genre", models.BookFilters{Genre: int(genre)}, []int64{files[0]}},
		{"series", models.BookFilters{Series: int(series)}, []int64{files[0]}},
		{"legacy author", models.BookFilters{Author: int(author)}, []int64{other, files[0]}},
		{"empty genre", models.BookFilters{Genre: int(empty)}, []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			books, _, total := f.cards(0, tc.filters)
			assert.Equal(t, tc.want, cardIDs(books))
			assert.Equal(t, len(tc.want), total)
		})
	}
}
