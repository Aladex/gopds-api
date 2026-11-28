package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What the surfaces read of author entities under authors.identity = entity
// (design E, phase E3): an entity by its ID, live ones only, with the label
// that tells namesakes apart, and the books an entity's page lists.

// genre links the book to a genre of the code, made on first use.
func (f *authorSchemaFixture) genre(book int64, code string) {
	f.t.Helper()
	f.exec(`INSERT INTO opds_catalog_genre (genre) VALUES (?) ON CONFLICT (genre) DO NOTHING`, code)
	f.exec(`INSERT INTO opds_catalog_bgenre (genre_id, book_id)
		SELECT id, ? FROM opds_catalog_genre WHERE genre = ?`, book, code)
}

// entityWithBooks is an entity of the name whose books carry the genres,
// one book per genre code given.
func (f *authorSchemaFixture) entityWithBooks(fill byte, family, given string, genres ...string) (credit int64, books []int64) {
	f.t.Helper()
	for _, g := range genres {
		book, s := f.entityBook(family + " " + g)
		credit = f.entityCredit(s, fill, family, given)
		f.genre(book, g)
		books = append(books, book)
	}
	return credit, books
}

// Two live entities of one display name are namesakes: each is labeled with
// the genre most of its listed books carry — its title where the catalog has
// one (production's genre table does, the migrations do not), its code
// otherwise — unless a namesake's books are mostly of that genre too: then
// the label is how many listed books it has. A hidden book does not count, a
// unique name has no label, and a merged entity is neither shown nor
// anyone's namesake.
func TestTheEntityLookupLabelsNamesakesByTheirGenre(t *testing.T) {
	f := entitySchema(t)
	kingSF, _ := f.entityWithBooks(0x91, "Кинг", "Стивен", "e3_sf", "e3_sf", "e3_sf", "e3_detective")
	kingDet, detBooks := f.entityWithBooks(0x92, "Кинговский", "Стивен", "e3_detective", "e3_detective",
		"e3_sf", "e3_sf", "e3_sf")
	kingTwin, _ := f.entityWithBooks(0x99, "Кингович", "Стивен", "e3_sf", "e3_sf")
	alone, _ := f.entityWithBooks(0x93, "Одинокий", "Пётр", "e3_sf")
	gone, _ := f.entityWithBooks(0x94, "Ушедший", "Пётр", "e3_sf")
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	f.exec(`UPDATE opds_catalog_genre SET title = 'Фантастика' WHERE genre = 'e3_sf'`)
	p, _, _ := f.pass()
	f.drain()
	// The three sf books of the second Кинг are hidden duplicates: its listed
	// books are the two detectives.
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id IN (?, ?, ?)`,
		detBooks[2], detBooks[3], detBooks[4])
	f.exec(`UPDATE author_entity SET display_name = 'Стивен Кинг' WHERE id IN (?, ?, ?)`,
		p.EntityOf(kingSF), p.EntityOf(kingDet), p.EntityOf(kingTwin))
	f.exec(`UPDATE author_entity SET display_name = 'Пётр Одинокий', merged_into = ? WHERE id = ?`,
		p.EntityOf(alone), p.EntityOf(gone))

	ctx := context.Background()
	ids := []uuid.UUID{p.EntityOf(kingSF), p.EntityOf(kingDet), p.EntityOf(kingTwin), p.EntityOf(alone),
		p.EntityOf(gone), uuid.New()}
	got, err := LiveAuthorEntities(ctx, f.tx, ids)
	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID]models.AuthorEntity{
		// Both mostly science fiction: told apart by their books.
		p.EntityOf(kingSF):   {ID: p.EntityOf(kingSF), FullName: "Стивен Кинг", ShortName: "Стивен Кинг", LabelBooks: 4},
		p.EntityOf(kingTwin): {ID: p.EntityOf(kingTwin), FullName: "Стивен Кинг", ShortName: "Стивен Кинг", LabelBooks: 2},
		p.EntityOf(kingDet): {ID: p.EntityOf(kingDet), FullName: "Стивен Кинг", ShortName: "Стивен Кинг",
			Label: "e3_detective"},
		p.EntityOf(alone): {ID: p.EntityOf(alone), FullName: "Пётр Одинокий", ShortName: "Пётр Одинокий"},
	}, got)

	one, err := AuthorEntityByID(ctx, f.tx, p.EntityOf(kingDet))
	require.NoError(t, err)
	assert.Equal(t, got[p.EntityOf(kingDet)], one)
	for _, id := range []uuid.UUID{p.EntityOf(gone), uuid.New(), uuid.Nil} {
		_, err = AuthorEntityByID(ctx, f.tx, id)
		assert.ErrorIs(t, err, ErrAuthorEntityNotFound, "%s", id)
	}

	none, err := LiveAuthorEntities(ctx, f.tx, nil)
	require.NoError(t, err)
	assert.Empty(t, none)
}

// Namesakes follow the short name (design/author-display, rule 4): two
// entities whose names differ only by a patronymic are both «Михаил Игнатов»
// on a book list, so both are labeled, and so is the one written without a
// patronymic at all; an entity whose short name no other live entity shares
// is not, though its full name has a patronymic too.
func TestNamesakesAreEntitiesOfOneShortName(t *testing.T) {
	f := entitySchema(t)
	pavlovich, _ := f.entityWithBooks(0x81, "Игнатов", "Михаил", "e3_sf", "e3_sf")
	vasilievich, _ := f.entityWithBooks(0x82, "Игнатович", "Михаил", "e3_detective")
	bare, _ := f.entityWithBooks(0x83, "Игнатовский", "Михаил", "e3_sf")
	alone, _ := f.entityWithBooks(0x84, "Семенова", "Мария", "e3_sf")
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	p, _, _ := f.pass()
	f.drain()
	f.exec(`UPDATE author_entity SET display_name = 'Михаил Павлович Игнатов' WHERE id = ?`, p.EntityOf(pavlovich))
	f.exec(`UPDATE author_entity SET display_name = 'Михаил Васильевич Игнатов' WHERE id = ?`,
		p.EntityOf(vasilievich))
	f.exec(`UPDATE author_entity SET display_name = 'Михаил Игнатов' WHERE id = ?`, p.EntityOf(bare))
	f.exec(`UPDATE author_entity SET display_name = 'Мария Васильевна Семенова' WHERE id = ?`, p.EntityOf(alone))

	ids := []uuid.UUID{p.EntityOf(pavlovich), p.EntityOf(vasilievich), p.EntityOf(bare), p.EntityOf(alone)}
	got, err := LiveAuthorEntities(context.Background(), f.tx, ids)
	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID]models.AuthorEntity{
		// Two of science fiction told apart by their books, the detective by its genre.
		p.EntityOf(pavlovich): {ID: p.EntityOf(pavlovich), FullName: "Михаил Павлович Игнатов",
			ShortName: "Михаил Игнатов", LabelBooks: 2},
		p.EntityOf(bare): {ID: p.EntityOf(bare), FullName: "Михаил Игнатов", ShortName: "Михаил Игнатов",
			LabelBooks: 1},
		p.EntityOf(vasilievich): {ID: p.EntityOf(vasilievich), FullName: "Михаил Васильевич Игнатов",
			ShortName: "Михаил Игнатов", Label: "e3_detective"},
		p.EntityOf(alone): {ID: p.EntityOf(alone), FullName: "Мария Васильевна Семенова",
			ShortName: "Мария Семенова"},
	}, got)

	// A merged namesake is no one's namesake.
	f.exec(`UPDATE author_entity SET merged_into = ? WHERE id IN (?, ?)`,
		p.EntityOf(pavlovich), p.EntityOf(vasilievich), p.EntityOf(bare))
	got, err = LiveAuthorEntities(context.Background(), f.tx, ids)
	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID]models.AuthorEntity{
		p.EntityOf(pavlovich): {ID: p.EntityOf(pavlovich), FullName: "Михаил Павлович Игнатов",
			ShortName: "Михаил Игнатов"},
		p.EntityOf(alone): {ID: p.EntityOf(alone), FullName: "Мария Васильевна Семенова",
			ShortName: "Мария Семенова"},
	}, got)
}

// public.author_short_name (migration 86), which the namesake check groups
// by, says what models.ShortAuthorName says, on the cases of the rule and on
// the ways a name can be spaced.
func TestTheShortNameInSQLAgreesWithGo(t *testing.T) {
	f := entitySchema(t)
	names := []string{
		"Владимир Дмитриевич Михайлов", "Мария Васильевна Семенова", "Михаил Павлович Игнатов",
		"Елена Владимировна Хаецкая", "Павло Архипович Загребельний", "Урсула Крёбер Ле Гуин",
		"Джордж Р. Р. Мартин", "Артур Конан Дойл", "Эрнст Теодор Амадей Гофман", "Архимандрит Иоанн (Крестьянкин)",
		"Аркадий и Борис Стругацкие", "Agatha Christie", "Борис Акунин", "Сапфо", "Людмила Никитична Иванова",
		"Вера Ильинична Петрова", "Леся Андріївна Українка", "Оксана Іванівна Забужко", "Владимир Ильич Ленин",
		"Иван Кузьмич Андрощук", "Сергей Никитич Ашихманов", "Алексей Савич Корюков", "Синтия Лейтич Смит",
		"Сергей Милич Рафальский", "ВЛАДИМИР ДМИТРИЕВИЧ МИХАЙЛОВ", "Иван Петрович (Сидоров)", "[Иван] Петрович Сидоров",
		"Иван Бич Петров", "Анна Вич Ли", "Анна Ична Ли", "Mary Ivanovich Smith", "Анна Ahmetovna Ли",
		"Иван Иванович", "Толстой Лев Николаевич", "Анна Петровна Ивановна Смит", "Михаил Евграфович Салтыков-Щедрин",
		"", " ", "  Мария\u00a0Васильевна   Семенова ", "Мария\tВасильевна\nСеменова", "Мария\u3000Васильевна\u2009Семенова",
		"Мария\u200bВасильевна Семенова", "Мария Василье\u0301вна Семенова", "Мария Васильевна\u00ad Семенова",
	}
	for _, name := range names {
		var got string
		_, err := f.tx.QueryOne(pg.Scan(&got), `SELECT public.author_short_name(?)`, name)
		require.NoError(t, err, "%q", name)
		assert.Equal(t, models.ShortAuthorName(name), got, "%q", name)
	}
}

// An entity's books are the books whose author line names it; the entity's
// page lists them like any author's, newest first, with the reader's
// language and the visibility rules.
func TestTheBookListOfAnEntityIsTheBooksNamingIt(t *testing.T) {
	// jobsDB brings the scratch database to the production shape: the list
	// reads the genre title column the migration files do not create.
	scratch := jobsDB(t)
	tx, err := scratch.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: tx, next: &next}
	f.registerPair("4", "structured_person", "normalizer-v1")
	credit, books := f.entityWithBooks(0x95, "Списковый", "Иван", "e3_sf", "e3_sf", "e3_sf")
	_, others := f.entityWithBooks(0x96, "Чужой", "Иван", "e3_sf")
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	p, _, _ := f.pass()
	f.drain()
	f.exec(`UPDATE opds_catalog_book SET lang = 'en' WHERE id = ?`, books[0])
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, books[1])
	require.NoError(t, tx.Commit())

	previous := GetDB()
	SetDB(scratch)
	t.Cleanup(func() { SetDB(previous) })
	list := func(filters models.BookFilters) ([]int64, int) {
		t.Helper()
		got, total, err := GetBooks(0, filters)
		require.NoError(t, err)
		ids := []int64{}
		for i := range got {
			ids = append(ids, got[i].ID)
		}
		return ids, total
	}
	ids, total := list(models.BookFilters{AuthorEntity: p.EntityOf(credit), Limit: 10})
	assert.Equal(t, []int64{books[2], books[0]}, ids, "newest first, the hidden duplicate left out")
	assert.Equal(t, 2, total)
	assert.NotContains(t, ids, others[0])
	ids, total = list(models.BookFilters{AuthorEntity: p.EntityOf(credit), Lang: "ru", Limit: 10})
	assert.Equal(t, []int64{books[2]}, ids, "the reader's language")
	assert.Equal(t, 1, total)
	ids, total = list(models.BookFilters{AuthorEntity: p.EntityOf(credit), Limit: 1, Offset: 1})
	assert.Equal(t, []int64{books[0]}, ids, "paged like any list")
	assert.Equal(t, 2, total)

	ids, total = list(models.BookFilters{AuthorEntity: uuid.New(), Limit: 10})
	assert.Empty(t, ids, "an entity no line names lists nothing, not the catalog")
	assert.Zero(t, total)
}

// The book card's author line carries, for each name, the entities its
// credit names — the same IDs its read-model row stores — and the entity
// identity turns them into links with the entities' names; the legacy line
// carries none.
func TestTheAuthorLineCarriesTheEntitiesOfItsNames(t *testing.T) {
	f := entitySchema(t)
	credit, books := f.entityWithBooks(0x97, "Строчный", "Иван", "e3_sf")
	survivor, _ := f.entityWithBooks(0x98, "Строчнов", "Иван", "e3_sf")
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	p, _, _ := f.pass()
	f.drain()
	ctx := context.Background()

	lines, err := NewPGBookSourceRepository(f.tx).BookAuthorDisplay(ctx, books)
	require.NoError(t, err)
	line := lines[books[0]]
	require.Len(t, line.Authors, 1)
	assert.Equal(t, models.AuthorDisplayLayer, line.Source)
	assert.Equal(t, []string{p.EntityOf(credit).String()}, line.Authors[0].EntityIDs)
	assert.Equal(t, f.entityIDsOf(books[0]), [][]string{line.Authors[0].EntityIDs}, "as the read model stores them")

	linked, err := EntityLines(ctx, f.tx, [][]models.AuthorDisplay{line.Authors})
	require.NoError(t, err)
	assert.Equal(t, []models.AuthorEntity{{ID: p.EntityOf(credit), FullName: "Иван Строчный", ShortName: "Иван Строчный"}},
		linked[0][0].Entities)
	assert.Nil(t, linked[0][0].LegacyAuthorID)

	f.exec(`UPDATE author_entity SET merged_into = ? WHERE id = ?`, p.EntityOf(survivor), p.EntityOf(credit))
	linked, err = EntityLines(ctx, f.tx, [][]models.AuthorDisplay{line.Authors})
	require.NoError(t, err)
	assert.Nil(t, linked[0][0].Entities, "a merged entity is linked to by no one")
}

// entitySearchCatalog is three books for the entity-identity searches: one
// by an entity that also goes by a short name variant, one by a credit that
// names no entity, one by another entity.
type entitySearchCatalog struct {
	tolstoy, other     uuid.UUID
	byEntity, unlinked int64
	byOther            int64
}

func (f *authorSchemaFixture) entitySearchCatalog() entitySearchCatalog {
	f.t.Helper()
	byEntity, s1 := f.entityBook("Война и мирок")
	unlinked, s2 := f.entityBook("Мирок и война")
	byOther, s3 := f.entityBook("Мирок ещё")
	tolstoy := f.entityCredit(s1, 0xa1, "Толстойсков", "Лев", "Николаевич")
	f.entityCredit(s2, 0xa2, "Йодаквистов", "Мастер")
	other := f.entityCredit(s3, 0xa3, "Петровский", "Пётр")
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	p, _, _ := f.pass()
	// The second credit's assignment goes: its name links to no entity.
	f.exec(`DELETE FROM author_entity_assignment WHERE source_fingerprint = ?`, fingerprint(32, 0xa2))
	f.drain()
	// A name variant the entity's other books would give it.
	f.exec(`INSERT INTO author_entity_name (entity_id, search_key, books) VALUES (?, 'великий старцевский', 1)`,
		p.EntityOf(tolstoy))
	return entitySearchCatalog{tolstoy: p.EntityOf(tolstoy), other: p.EntityOf(other),
		byEntity: byEntity, unlinked: unlinked, byOther: byOther}
}

// Under the entity identity a book search by author text matches every name
// of the entities a book's line links to, and the names of the line that
// link to none; scoping a search to an entity keeps its books only.
func TestTheEntitySearchFindsBooksByTheirEntitiesNames(t *testing.T) {
	f := entitySchema(t)
	c := f.entitySearchCatalog()
	ctx := context.Background()
	books := func(repo *PGSearchRepository, req models.BookSearchRequest) []int64 {
		t.Helper()
		req.Query, req.Limit = "мирок", 50
		page, err := repo.SearchBooks(ctx, req)
		require.NoError(t, err)
		ids := []int64{}
		for i := range page.Books {
			ids = append(ids, page.Books[i].ID)
		}
		assert.Equal(t, len(ids), page.Total)
		return ids
	}
	entities, legacy := NewPGSearchRepositoryFor(f.tx, true), NewPGSearchRepository(f.tx)

	assert.Equal(t, []int64{c.byEntity}, books(entities, models.BookSearchRequest{AuthorQuery: "толстойсков"}))
	assert.Equal(t, []int64{c.byEntity}, books(entities, models.BookSearchRequest{AuthorQuery: "великий старцевский"}),
		"a variant of the entity's names")
	assert.Equal(t, []int64{c.unlinked}, books(entities, models.BookSearchRequest{AuthorQuery: "мастер йодаквистов"}),
		"a name that links to no entity")
	assert.Equal(t, []int64{c.byOther}, books(entities, models.BookSearchRequest{AuthorQuery: "петровский"}))
	assert.Empty(t, books(legacy, models.BookSearchRequest{AuthorQuery: "великий старцевский"}),
		"the legacy identity knows no entity names")

	for _, repo := range []*PGSearchRepository{entities, legacy} {
		assert.Equal(t, []int64{c.byEntity}, books(repo, models.BookSearchRequest{AuthorEntityID: c.tolstoy}))
		assert.Empty(t, books(repo, models.BookSearchRequest{AuthorEntityID: uuid.New()}))
	}
}

// Under the entity identity the author search and the picker's author shelf
// offer entities — by any of their names, one row each, with the books they
// open and the namesake label — and no legacy author; the picker's book
// shelf keeps to an entity's books when the reader stands in its list.
func TestTheEntityIdentityOffersEntitiesForAuthors(t *testing.T) {
	f := entitySchema(t)
	c := f.entitySearchCatalog()
	f.genre(c.byEntity, "e3_prose")
	f.genre(c.byOther, "e3_poetry")
	f.exec(`UPDATE author_entity SET display_name = 'Лев Толстойсков' WHERE id IN (?, ?)`, c.tolstoy, c.other)
	ctx := context.Background()
	repo := NewPGSearchRepositoryFor(f.tx, true)

	page, err := repo.SearchAuthors(ctx, models.AuthorSearchRequest{Query: "великий старцевский", Limit: 10})
	require.NoError(t, err)
	assert.Nil(t, page.Authors, "no legacy author")
	require.NotEmpty(t, page.Entities)
	assert.Equal(t, models.AuthorEntity{ID: c.tolstoy, FullName: "Лев Толстойсков", ShortName: "Лев Толстойсков",
		Label: "e3_prose", BooksCount: 1}, page.Entities[0])
	assert.Equal(t, len(page.Entities), page.Total)
	assert.NotEmpty(t, page.QueryHash)

	picked, err := repo.Suggestions(ctx, models.SuggestionRequest{Query: "толстойсков", Kind: models.SuggestionAuthor,
		Limit: 15})
	require.NoError(t, err)
	require.NotEmpty(t, picked.Suggestions)
	assert.Equal(t, models.AutocompleteSuggestion{Value: "Лев Толстойсков", ShortName: "Лев Толстойсков", Type: "author",
		EntityID: c.tolstoy, BooksCount: 1, Label: "e3_prose"}, picked.Suggestions[0])
	for _, s := range picked.Suggestions {
		assert.NotEqual(t, uuid.Nil, s.EntityID, "an author shelf of entities only: %+v", s)
		assert.Zero(t, s.ID)
	}

	all, err := repo.Suggestions(ctx, models.SuggestionRequest{Query: "мирок", Kind: models.SuggestionAll, Limit: 15})
	require.NoError(t, err)
	var titles []int64
	for _, s := range all.Suggestions {
		if s.Type == "book" {
			titles = append(titles, s.ID)
		}
	}
	assert.Len(t, titles, 3, "the book shelf stays")

	scoped, err := repo.Suggestions(ctx, models.SuggestionRequest{Query: "мирок", Kind: models.SuggestionBook,
		Limit: 15, AuthorEntityID: c.tolstoy})
	require.NoError(t, err)
	require.Len(t, scoped.Suggestions, 1)
	assert.Equal(t, c.byEntity, scoped.Suggestions[0].ID)
}

// The picker's shelf ranks before it counts and stops at its last row, yet
// orders the entities tied with that row by their books as if it had counted
// them all: four namesakes of one surname tie on every name signal, and a
// shelf of two is the two with the most books.
func TestTheEntityShelfOrdersTiesByBooks(t *testing.T) {
	f := entitySchema(t)
	var want []uuid.UUID
	credits := map[int]int64{}
	for i, given := range []string{"Иван", "Пётр", "Глеб", "Олег"} {
		genres := make([]string, i+1)
		for j := range genres {
			genres[j] = "e3_sf"
		}
		credits[i+1], _ = f.entityWithBooks(byte(0xb0+i), "Шелфовский", given, genres...)
	}
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	p, _, _ := f.pass()
	f.drain()
	for _, books := range []int{4, 3, 2, 1} {
		want = append(want, p.EntityOf(credits[books]))
	}
	ctx := context.Background()
	for _, limit := range []int{1, 2, 3, 4, 7} {
		hits, err := SuggestAuthorEntities(ctx, f.tx, "шелфовский", "", limit)
		require.NoError(t, err)
		got := []uuid.UUID{}
		for _, h := range hits {
			got = append(got, h.ID)
		}
		assert.Equal(t, want[:min(limit, len(want))], got, "a shelf of %d", limit)
	}
}

// An entity merged into another is not offered by the search, even while
// the rows and names that carried it have not caught up (no redirects:
// design E, §4.2); the total does not count it.
func TestTheEntitySearchLeavesOutAMergedEntity(t *testing.T) {
	f := entitySchema(t)
	merged, _ := f.entityWithBooks(0xc1, "Слитовский", "Иван", "e3_sf", "e3_sf")
	survivor, _ := f.entityWithBooks(0xc2, "Слитовский", "Пётр", "e3_sf")
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	p, _, _ := f.pass()
	f.drain()
	ctx := context.Background()
	page, err := SearchAuthorEntities(ctx, f.tx, "Слитовский", "", 10, 0)
	require.NoError(t, err)
	require.Len(t, page.Hits, 2)

	f.exec(`UPDATE author_entity SET merged_into = ? WHERE id = ?`, p.EntityOf(survivor), p.EntityOf(merged))
	page, err = SearchAuthorEntities(ctx, f.tx, "Слитовский", "", 10, 0)
	require.NoError(t, err)
	require.Len(t, page.Hits, 1)
	assert.Equal(t, p.EntityOf(survivor), page.Hits[0].ID)
	assert.Equal(t, 1, page.Total)
}
