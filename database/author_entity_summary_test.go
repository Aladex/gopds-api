package database

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	logrustest "github.com/sirupsen/logrus/hooks/test" //nolint:depguard // tests assert on the structured log entries
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The author summary against a real PostgreSQL (migration 81): the walk and
// the marks that fill it, what leaves it, and the page read.

// summaryEntity is a live entity with a credited book per title, the read
// model drained, its summary marks cleared.
type summaryEntity struct {
	id    uuid.UUID
	books []int64
}

func (f *authorSchemaFixture) summaryEntity(fill byte, family, given string, titles ...string) summaryEntity {
	f.t.Helper()
	var credit int64
	var books []int64
	for _, title := range titles {
		book, s := f.entityBook(title)
		credit = f.entityCredit(s, fill, family, given)
		books = append(books, book)
	}
	f.exec(`UPDATE opds_catalog_book SET approved = true, cover = true, duplicate_hidden = false WHERE id = ANY (?)`,
		pgInts(books))
	p, _, _ := f.pass()
	f.drain()
	return summaryEntity{id: p.EntityOf(credit), books: books}
}

func pgInts(ids []int64) interface{} {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (f *authorSchemaFixture) summaryMarks() []uuid.UUID {
	f.t.Helper()
	var ids []uuid.UUID
	_, err := f.tx.Query(&ids, `SELECT entity_id FROM author_entity_summary_dirty`)
	require.NoError(f.t, err)
	return ids
}

func (f *authorSchemaFixture) rebuildSummaries(ids ...uuid.UUID) {
	f.t.Helper()
	for _, id := range ids {
		require.NoError(f.t, rebuildAuthorEntitySummary(context.Background(), f.tx, id))
	}
}

// summaryTxs stands for the database in the fixture's one transaction: each of
// the summary's transactions is a savepoint of it, so a refused one takes back
// its own writes and nothing commits. It sees what the steps do, never what a
// connection does: a real timeout is services/author_display_model_loop_test.go.
type summaryTxs struct{ tx *pg.Tx }

func (s summaryTxs) RunInTransaction(ctx context.Context, fn func(*pg.Tx) error) error {
	return underSavepoint(ctx, s.tx, "summary_tx", func() error { return fn(s.tx) })
}

const summaryTimeout = 5 * time.Second

func (f *authorSchemaFixture) drainSummaries(limit int) AuthorSummaryWork {
	f.t.Helper()
	done, err := DrainAuthorSummaryMarks(context.Background(), summaryTxs{f.tx}, limit, summaryTimeout)
	require.NoError(f.t, err)
	return done
}

func (f *authorSchemaFixture) walkSummaries(batch int) AuthorSummaryWalkResult {
	f.t.Helper()
	done, err := AuthorSummaryWalkStep(context.Background(), summaryTxs{f.tx}, batch, time.Hour, summaryTimeout)
	require.NoError(f.t, err)
	return done
}

func (f *authorSchemaFixture) summaryLangs(id uuid.UUID) []string {
	f.t.Helper()
	langs := []string{}
	_, err := f.tx.Query(&langs, `SELECT lang FROM author_entity_summary WHERE entity_id = ? ORDER BY lang`, id)
	require.NoError(f.t, err)
	return langs
}

func (f *authorSchemaFixture) authorsPage(query, lang string) models.AuthorSearchPage {
	f.t.Helper()
	page, err := NewPGSearchRepositoryFor(f.tx, true).SearchAuthors(context.Background(),
		models.AuthorSearchRequest{Query: query, Language: lang, Limit: 10})
	require.NoError(f.t, err)
	return page
}

// resetSummaries empties what other tests committed.
func (f *authorSchemaFixture) resetSummaries() {
	f.t.Helper()
	f.exec(`TRUNCATE author_entity_summary, author_entity_summary_dirty`)
	f.exec(`UPDATE author_entity_summary_walk SET walk_cursor = NULL, walk_read = 0, walk_skipped = 0, finished_at = NULL`)
}

func summarySchema(t *testing.T) *authorSchemaFixture {
	f := entitySchema(t)
	f.resetSummaries()
	return f
}

// The site works from an empty summary: every author found comes back, with
// its kind and empty fields, exactly as many as before anything was built.
func TestAnEmptySummaryLosesNoAuthorAndFailsNoSearch(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xb1, "Лемовский", "Станислав", "Солярис", "Непобедимый")

	page := f.authorsPage("лемовский", "")
	require.Len(t, page.Entities, 1)
	assert.Equal(t, 1, page.Total)
	assert.Equal(t, 2, page.Entities[0].BooksCount)
	got, ok := page.Summaries[e.id]
	require.True(t, ok, "an author without a summary still has an entry")
	assert.Equal(t, models.AuthorSummary{Kind: "person"}, got)
}

// A summary that cannot be read costs the page its covers and titles, never an
// author: the hits were read before it.
func TestASummaryThatCannotBeReadCostsNoAuthor(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xbf, "Лемовский", "Станислав", "Солярис")
	f.exec(`ALTER TABLE author_entity_summary RENAME TO author_entity_summary_away`)
	hook := logrustest.NewLocal(logging.GetLogger())
	page, err := NewPGSearchRepositoryFor(f.tx, true).SearchAuthors(context.Background(),
		models.AuthorSearchRequest{Query: "лемовский", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Entities, 1)
	assert.Equal(t, models.AuthorSummary{Kind: "person"}, page.Summaries[e.id])

	// The log says what happened by a fixed event, never in the driver's words.
	require.Len(t, hook.AllEntries(), 1)
	entry := hook.AllEntries()[0]
	assert.Equal(t, authorSummaryReadFailed, entry.Message)
	assert.Equal(t, logging.Fields{authorSummaryReadHits: 1, authorSummaryReadState: "42P01"}, entry.Data)
}

// Building the summary changes what a page says of an author and nothing of
// which authors it holds, in which order.
func TestTheSummaryChangesNoSearchResult(t *testing.T) {
	f := summarySchema(t)
	one := f.summaryEntity(0xb2, "Лемовский", "Станислав", "Солярис", "Непобедимый", "Эдем")
	two := f.summaryEntity(0xb3, "Лемовский", "Михаил", "Глас господа")
	before := f.authorsPage("лемовский", "")
	f.rebuildSummaries(one.id)
	during := f.authorsPage("лемовский", "")
	assert.Equal(t, before.Entities, during.Entities)
	assert.Equal(t, before.Total, during.Total)
	assert.NotEmpty(t, during.Summaries[one.id].Works)
	assert.Empty(t, during.Summaries[two.id].Works, "half built is a page half filled")
}

func TestAPageReadsTheVariantOfItsLanguage(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xb4, "Лемовский", "Станислав", "Солярис", "Solaris")
	f.exec(`UPDATE opds_catalog_book SET lang = 'en' WHERE id = ?`, e.books[1])
	f.exec(`UPDATE opds_catalog_book SET lang = 'ru' WHERE id = ?`, e.books[0])
	f.exec(`INSERT INTO opds_catalog_genre (genre, title) VALUES ('a3_sf', 'Фантастика') ON CONFLICT (genre) DO NOTHING`)
	f.genre(e.books[0], "a3_sf")
	f.rebuildSummaries(e.id)

	assert.Equal(t, []string{"", "en", "ru"}, f.summaryLangs(e.id))
	assert.ElementsMatch(t, []string{"Солярис", "Solaris"}, f.authorsPage("лемовский", "").Summaries[e.id].Works)
	ru := f.authorsPage("лемовский", "ru").Summaries[e.id]
	assert.Equal(t, []string{"Солярис"}, ru.Works)
	assert.Equal(t, "Солярис", ru.Covers[0].Title)
	assert.NotEmpty(t, ru.Genres)
	en := f.authorsPage("лемовский", "en").Summaries[e.id]
	assert.Equal(t, []string{"Solaris"}, en.Works)
	assert.Empty(t, en.Genres)
}

func TestACoverCarriesWhatItsAddressIsMadeFrom(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xb5, "Лемовский", "Станислав", "Солярис")
	f.exec(`UPDATE opds_catalog_book SET path = 'fb2-1.zip', filename = '5.fb2' WHERE id = ?`, e.books[0])
	f.rebuildSummaries(e.id)
	assert.Equal(t, []models.AuthorCover{{Path: "fb2-1.zip", Filename: "5.fb2", Title: "Солярис"}},
		f.authorsPage("лемовский", "").Summaries[e.id].Covers)
}

// Hidden and unapproved books say nothing about an author, as they do not
// count in the number of its books.
func TestOnlyListedBooksReachTheSummary(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xb6, "Лемовский", "Станислав", "Видимая", "Скрытая", "Неодобренная")
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, e.books[1])
	f.exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, e.books[2])
	f.rebuildSummaries(e.id)
	assert.Equal(t, []string{"Видимая"}, f.authorsPage("лемовский", "").Summaries[e.id].Works)
}

// The signals the order rests on are the database's: how many authors a book
// has, how many readers keep it, and the genres that make it a magazine.
func TestTheSignalsComeFromTheCatalog(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xb7, "Лемовский", "Станислав", "Любимая", "Обычная", "Журнальная", "Коллективная")
	f.exec(`UPDATE opds_catalog_book SET registerdate = now() - interval '1 day' WHERE id IN (?, ?, ?, ?)`,
		e.books[0], e.books[1], e.books[2], e.books[3])
	f.exec(`UPDATE opds_catalog_book SET registerdate = now() WHERE id IN (?, ?)`, e.books[2], e.books[3])
	reader := f.user()
	f.exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?)`, reader, e.books[0])
	f.genre(e.books[2], "periodic")
	// Two more names on the line of the last book: three authors.
	coauthor := f.legacyAuthorOf(e.books[3], "Соавтор")
	f.exec(`INSERT INTO book_author_display (book_id, position, display, sort_key, search_key, source, legacy_author_id)
		SELECT ?0, g, 'Соавтор', 'соавтор', 'соавтор', 'legacy', ?1 FROM generate_series(5, 6) AS g`, e.books[3], coauthor)
	f.rebuildSummaries(e.id)
	assert.Equal(t, []string{"Любимая", "Обычная", "Коллективная", "Журнальная"},
		f.authorsPage("лемовский", "").Summaries[e.id].Works)
}

// A credit of two people counts two authors, though it is one row.
func TestACreditOfTwoEntitiesCountsItsPeople(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xb9, "Лемовский", "Станислав", "Дуэт", "Соло")
	second := uuid.Must(uuid.NewV7())
	third := uuid.Must(uuid.NewV7())
	f.exec(`UPDATE book_author_display SET entity_ids = ARRAY[?::uuid, ?::uuid, ?::uuid] WHERE book_id = ?`,
		e.id, second, third, e.books[0])
	f.exec(`UPDATE opds_catalog_book SET registerdate = now() WHERE id = ?`, e.books[0])
	f.exec(`UPDATE opds_catalog_book SET registerdate = now() - interval '1 day' WHERE id = ?`, e.books[1])
	f.rebuildSummaries(e.id)
	assert.Equal(t, []string{"Соло", "Дуэт"}, f.authorsPage("лемовский", "").Summaries[e.id].Works,
		"three people on one row are three authors")
}

func TestAMatchedNameIsTheClosestNameThatIsNotTheDisplayedOne(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xba, "Лемовский", "Станислав", "Солярис")
	f.exec(`UPDATE author_entity SET display_name = 'Станислав Лемовский' WHERE id = ?`, e.id)
	f.exec(`UPDATE book_author_display SET display = 'Stanislaw Lemowski', nickname = 'Псевдонимов Великий' WHERE book_id = ?`, e.books[0])
	f.exec(`INSERT INTO author_entity_name (entity_id, search_key, books) VALUES
		(?0, public.search_normalize('Stanislaw Lemowski'), 1), (?0, public.search_normalize('Псевдонимов Великий'), 1)`, e.id)
	f.rebuildSummaries(e.id)

	assert.Equal(t, "Stanislaw Lemowski", f.authorsPage("stanislaw lemowski", "").Summaries[e.id].MatchedName)
	assert.Equal(t, "Псевдонимов Великий", f.authorsPage("псевдонимов", "").Summaries[e.id].MatchedName)
}

func TestNoMatchedNameWhenTheDisplayedNameIsTheClosest(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xbb, "Лемовский", "Станислав", "Солярис")
	f.exec(`UPDATE author_entity SET display_name = 'Станислав Лемовский' WHERE id = ?`, e.id)
	f.exec(`UPDATE book_author_display SET display = 'станислав   лемовский' WHERE book_id = ?`, e.books[0])
	f.rebuildSummaries(e.id)
	assert.Empty(t, f.authorsPage("лемовский", "").Summaries[e.id].MatchedName)
}

// Production: a magazine's credit of twelve people is one line naming twelve
// entities, and «александр» reached Лем through it. A line of several people is
// no name of any of them.
func TestACreditOfSeveralPeopleIsNotAMatchedName(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xbd, "Лемовский", "Станислав", "Солярис")
	f.exec(`UPDATE author_entity SET display_name = 'Станислав Лемовский' WHERE id = ?`, e.id)
	f.exec(`UPDATE book_author_display SET display = 'Александр Силецкий, Станислав Лемовский', entity_ids = ARRAY[?::uuid, ?::uuid]
		WHERE book_id = ?`, e.id, uuid.Must(uuid.NewV7()), e.books[0])
	f.exec(`INSERT INTO author_entity_name (entity_id, search_key, books)
		VALUES (?, public.search_normalize('Александр Силецкий, Станислав Лемовский'), 1)`, e.id)
	f.rebuildSummaries(e.id)
	assert.Empty(t, f.authorsPage("александр силецкий", "").Summaries[e.id].MatchedName)
}

// A name no closer to the query than the displayed one explains nothing:
// «Кинг Стивен» beside «Стивен Кинг», «Б. Стругацкий» for «стругацкий».
func TestAVariantNoCloserThanTheDisplayedNameIsNotAMatchedName(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xbe, "Лемовский", "Станислав", "Солярис")
	f.exec(`UPDATE author_entity SET display_name = 'Станислав Лемовский' WHERE id = ?`, e.id)
	f.exec(`UPDATE book_author_display SET display = 'Лемовский Станислав', nickname = 'С. Лемовский' WHERE book_id = ?`, e.books[0])
	f.rebuildSummaries(e.id)
	assert.Empty(t, f.authorsPage("лемовский", "").Summaries[e.id].MatchedName)
}

// hiddenName is an entity with one listed book and one book of its own credited
// under name: until hide takes that book off the listing the name is reported,
// which is what shows the test can see it, and after it the name is gone.
func hiddenName(t *testing.T, fill byte, column, name, hide string) {
	t.Helper()
	f := summarySchema(t)
	e := f.summaryEntity(fill, "Лемовский", "Станислав", "Видимая", "Вторая")
	f.exec(`UPDATE author_entity SET display_name = 'Станислав Лемовский' WHERE id = ?`, e.id)
	f.exec(`UPDATE book_author_display SET `+column+` = ? WHERE book_id = ?`, name, e.books[1])
	f.exec(`INSERT INTO author_entity_name (entity_id, search_key, books) VALUES (?, public.search_normalize(?), 1)`, e.id, name)
	f.rebuildSummaries(e.id)
	require.Equal(t, name, f.authorsPage(name, "").Summaries[e.id].MatchedName)

	f.exec(`UPDATE opds_catalog_book SET `+hide+` WHERE id = ?`, e.books[1])
	f.rebuildSummaries(e.id)
	got := f.authorsPage(name, "")
	require.Len(t, got.Entities, 1, "the search still finds the entity by the name")
	assert.Empty(t, got.Summaries[e.id].MatchedName)
	assert.Equal(t, []string{"Видимая"}, got.Summaries[e.id].Works)
}

const (
	hideDuplicate = "duplicate_hidden = true"
	hideApproval  = "approved = false"
)

func TestADisplayNameOnlyOnAHiddenDuplicateIsNotPublished(t *testing.T) {
	hiddenName(t, 0xe1, "display", "Тайное Имя", hideDuplicate)
}

func TestADisplayNameOnlyOnAnUnapprovedBookIsNotPublished(t *testing.T) {
	hiddenName(t, 0xe2, "display", "Тайное Имя", hideApproval)
}

func TestANicknameOnlyOnAHiddenDuplicateIsNotPublished(t *testing.T) {
	hiddenName(t, 0xe3, "nickname", "Тайное Прозвище", hideDuplicate)
}

func TestANicknameOnlyOnAnUnapprovedBookIsNotPublished(t *testing.T) {
	hiddenName(t, 0xe4, "nickname", "Тайное Прозвище", hideApproval)
}

// A name a listed book also carries stays, whatever a hidden one says.
func TestANameOnAListedBookStaysThoughAHiddenBookHasItToo(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xe5, "Лемовский", "Станислав", "Видимая", "Скрытая")
	f.exec(`UPDATE author_entity SET display_name = 'Станислав Лемовский' WHERE id = ?`, e.id)
	f.exec(`UPDATE book_author_display SET display = 'Общее Имя' WHERE book_id = ANY (?)`, pgInts(e.books))
	f.exec(`INSERT INTO author_entity_name (entity_id, search_key, books) VALUES (?, public.search_normalize('Общее Имя'), 2)`, e.id)
	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, e.books[1])
	f.rebuildSummaries(e.id)
	assert.Equal(t, "Общее Имя", f.authorsPage("общее имя", "").Summaries[e.id].MatchedName)
}

// The matched name does not depend on the language: the search finds an
// entity by any of its names, whatever the filter.
func TestAMatchedNameComesWithALanguageFilter(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xbc, "Лемовский", "Станислав", "Solaris")
	f.exec(`UPDATE author_entity SET display_name = 'Станислав Лемовский' WHERE id = ?`, e.id)
	f.exec(`UPDATE opds_catalog_book SET lang = 'en' WHERE id = ?`, e.books[0])
	f.exec(`UPDATE book_author_display SET display = 'Stanislaw Lemowski' WHERE book_id = ?`, e.books[0])
	f.exec(`INSERT INTO author_entity_name (entity_id, search_key, books) VALUES (?, public.search_normalize('Stanislaw Lemowski'), 1)`, e.id)
	f.rebuildSummaries(e.id)
	assert.Equal(t, "Stanislaw Lemowski", f.authorsPage("stanislaw lemowski", "en").Summaries[e.id].MatchedName)
}

// What the read model writes marks the entities on both sides of the write:
// those a book's line named and those it names now.
func TestARewrittenLineMarksTheEntitiesItLeftAndTheOnesItNames(t *testing.T) {
	f := summarySchema(t)
	before := f.summaryEntity(0xc1, "Прежний", "Пётр", "Книга")
	after := f.summaryEntity(0xc2, "Новый", "Пётр", "Другая")
	f.exec(`DELETE FROM author_entity_summary_dirty`)
	f.exec(`UPDATE author_entity_assignment SET entity_id = ? WHERE entity_id = ?`, after.id, before.id)
	f.drain()
	assert.ElementsMatch(t, []uuid.UUID{before.id, after.id}, f.summaryMarks())
}

func TestADeletedBookMarksItsEntities(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xc3, "Прежний", "Пётр", "Книга")
	f.exec(`DELETE FROM author_entity_summary_dirty`)
	require.NoError(t, deleteAuthorDisplayOf(context.Background(), f.tx, e.books))
	assert.Equal(t, []uuid.UUID{e.id}, f.summaryMarks())
}

// A rescan changes a title or a cover and leaves the author line as it was:
// the book is marked, the drain writes nothing, and the entity is still due.
func TestADrainedBookMarksItsEntityThoughItsLineDidNotChange(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xc4, "Прежний", "Пётр", "Книга")
	f.exec(`DELETE FROM author_entity_summary_dirty`)
	require.NoError(t, MarkAuthorDisplayDirty(context.Background(), f.tx, e.books[0]))
	assert.Zero(t, f.drain().Differed)
	assert.Equal(t, []uuid.UUID{e.id}, f.summaryMarks())
}

// A line the walk finds different is a write like any other: a mark that never
// came is the case the walk exists for.
func TestALineTheDisplayWalkRewritesMarksBothSides(t *testing.T) {
	f := summarySchema(t)
	before := f.summaryEntity(0xd2, "Прежний", "Пётр", "Книга")
	after := f.summaryEntity(0xd3, "Новый", "Пётр", "Другая")
	f.exec(`UPDATE author_entity_assignment SET entity_id = ? WHERE entity_id = ?`, after.id, before.id)
	f.exec(`DELETE FROM book_author_display_dirty`)
	f.exec(`DELETE FROM author_entity_summary_dirty`)
	f.exec(`UPDATE book_author_display_reconcile SET walk_cursor = NULL, finished_at = NULL`)
	for {
		step, err := ReconcileAuthorDisplayStep(context.Background(), f.tx, 1000, time.Hour)
		require.NoError(t, err)
		if step.Finished {
			break
		}
	}
	assert.ElementsMatch(t, []uuid.UUID{before.id, after.id}, f.summaryMarks())
}

// The daily walk reads every book and rewrites none: it must not mark every
// entity of the catalog each day.
func TestTheDisplayWalkMarksOnlyWhatItRewrote(t *testing.T) {
	f := summarySchema(t)
	f.summaryEntity(0xc5, "Прежний", "Пётр", "Книга")
	f.exec(`DELETE FROM author_entity_summary_dirty`)
	f.exec(`UPDATE book_author_display_reconcile SET walk_cursor = NULL, finished_at = NULL`)
	for {
		step, err := ReconcileAuthorDisplayStep(context.Background(), f.tx, 1000, time.Hour)
		require.NoError(t, err)
		if step.Finished {
			break
		}
	}
	assert.Empty(t, f.summaryMarks())
}

func TestDrainingMarksBuildsTheEntityAndTakesTheMark(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xc6, "Прежний", "Пётр", "Книга")
	require.Contains(t, f.summaryMarks(), e.id)
	done := f.drainSummaries(100)
	assert.GreaterOrEqual(t, done.Entities, 1)
	assert.Empty(t, f.summaryMarks())
	assert.Equal(t, []string{"", "ru"}, f.summaryLangs(e.id))
}

// A merged entity leaves: its page is gone. The one it went into has its books.
func TestAMergedEntityLeavesTheSummaryAndItsTargetTakesItsBooks(t *testing.T) {
	f := summarySchema(t)
	from := f.summaryEntity(0xc7, "Слитый", "Пётр", "Первая")
	into := f.summaryEntity(0xc8, "Главный", "Пётр", "Вторая")
	f.rebuildSummaries(from.id, into.id)
	f.exec(`DELETE FROM author_entity_summary_dirty`)

	f.exec(`UPDATE author_entity SET merged_into = ?0 WHERE id = ?1`, into.id, from.id)
	f.exec(`UPDATE author_entity_assignment SET entity_id = ?0 WHERE entity_id = ?1`, into.id, from.id)
	f.drain()
	f.drainSummaries(100)

	assert.Empty(t, f.summaryLangs(from.id))
	assert.ElementsMatch(t, []string{"Первая", "Вторая"}, f.authorsPage("главный", "").Summaries[into.id].Works)
}

// The merge may reach the entity before its books have moved off it: it is
// gone from the summary all the same, whatever its rows still say.
func TestAMergedEntityLeavesThoughItsLinesStillNameIt(t *testing.T) {
	f := summarySchema(t)
	from := f.summaryEntity(0xd4, "Слитый", "Пётр", "Первая")
	into := f.summaryEntity(0xd5, "Главный", "Пётр", "Вторая")
	f.rebuildSummaries(from.id)
	f.exec(`UPDATE author_entity SET merged_into = ? WHERE id = ?`, into.id, from.id)
	f.rebuildSummaries(from.id)
	assert.Empty(t, f.summaryLangs(from.id))
}

// A retired entity is deleted: the marks of its rows take it out, and the
// walk takes out whatever outlived its entity with no mark.
func TestAnEntityWithNoListedBooksLeaves(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xc9, "Прежний", "Пётр", "Книга")
	f.rebuildSummaries(e.id)
	f.exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, e.books[0])
	f.rebuildSummaries(e.id)
	assert.Empty(t, f.summaryLangs(e.id))
}

func TestALanguageTheEntityNoLongerHasLeaves(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xca, "Прежний", "Пётр", "Книга")
	f.exec(`UPDATE opds_catalog_book SET lang = 'de' WHERE id = ?`, e.books[0])
	f.rebuildSummaries(e.id)
	require.Equal(t, []string{"", "de"}, f.summaryLangs(e.id))
	f.exec(`UPDATE opds_catalog_book SET lang = 'fr' WHERE id = ?`, e.books[0])
	f.rebuildSummaries(e.id)
	assert.Equal(t, []string{"", "fr"}, f.summaryLangs(e.id))
}

// Walking the whole table in steps: every entity commits its cursor, so a
// restart continues; the first walk needs no one to start it; rows of an entity
// that is gone leave.
func TestTheWalkFillsTheSummaryAStepAtATimeAndResumes(t *testing.T) {
	f := summarySchema(t)
	a := f.summaryEntity(0xcb, "Первый", "Пётр", "Раз")
	b := f.summaryEntity(0xcc, "Второй", "Пётр", "Два")
	c := f.summaryEntity(0xcd, "Третий", "Пётр", "Три")
	gone := uuid.Must(uuid.NewV7())
	f.exec(`INSERT INTO author_entity_summary (entity_id, lang) VALUES (?, '')`, gone)

	first := f.walkSummaries(2)
	assert.True(t, first.Ran)
	assert.False(t, first.Finished)
	assert.Equal(t, 2, first.Entities)
	var read int
	_, err := f.tx.QueryOne(pg.Scan(&read), `SELECT walk_read FROM author_entity_summary_walk`)
	require.NoError(t, err)
	assert.Equal(t, 2, read, "the cursor and the count are stored: a new process goes on from them")

	second := f.walkSummaries(2)
	assert.False(t, second.Finished, "the last entity of a batch cannot know it is the last")
	last := f.walkSummaries(2)
	assert.Zero(t, last.Entities)
	assert.True(t, last.Finished)
	assert.EqualValues(t, 4, last.Read, "three entities and the rows of the one that is gone")
	for _, e := range []summaryEntity{a, b, c} {
		assert.Equal(t, []string{"", "ru"}, f.summaryLangs(e.id))
	}
	assert.Empty(t, f.summaryLangs(gone), "the entity is gone")

	assert.False(t, f.walkSummaries(2).Ran, "a finished walk rests until every has passed")
}

// A walk rewrites nothing that has not changed: no dead rows, no WAL.
func TestARebuildOfAnUnchangedEntityWritesNothing(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xce, "Прежний", "Пётр", "Книга")
	f.rebuildSummaries(e.id)
	stamp := func() string {
		var ctid string
		_, err := f.tx.QueryOne(pg.Scan(&ctid), `SELECT string_agg(ctid::text, ',' ORDER BY lang)
			FROM author_entity_summary WHERE entity_id = ?`, e.id)
		require.NoError(t, err)
		return ctid
	}
	before := stamp()
	f.rebuildSummaries(e.id)
	assert.Equal(t, before, stamp())
}

// refusedSummary makes the table refuse one entity's rows.
func (f *authorSchemaFixture) refusedSummary(id uuid.UUID) {
	f.t.Helper()
	f.exec(`CREATE FUNCTION public.a3_refuse_summary() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.entity_id = '` + id.String() + `' THEN RAISE EXCEPTION 'refused' USING ERRCODE = '22000'; END IF;
			RETURN NEW;
		END $$`)
	f.exec(`CREATE TRIGGER refuse_summary BEFORE INSERT ON author_entity_summary
		FOR EACH ROW EXECUTE FUNCTION public.a3_refuse_summary()`)
}

// A row the table refuses costs that one entity: the rest of the batch is
// built, the cursor passes it, and it is reported by ID.
func TestARefusedEntityStopsNoWalk(t *testing.T) {
	f := summarySchema(t)
	bad := f.summaryEntity(0xcf, "Плохой", "Пётр", "Раз")
	good := f.summaryEntity(0xd0, "Хороший", "Пётр", "Два")
	f.refusedSummary(bad.id)

	done := f.walkSummaries(10)
	assert.Equal(t, []AuthorSummarySkip{{EntityID: bad.id, SQLState: "22000"}}, done.Skipped)
	assert.True(t, done.Finished)
	assert.EqualValues(t, 2, done.Read)
	assert.EqualValues(t, 1, done.Refused)
	assert.Equal(t, []string{"", "ru"}, f.summaryLangs(good.id))
	assert.Empty(t, f.summaryLangs(bad.id))
}

// The same for a mark: it goes, or the batch would come back to it every rest.
func TestARefusedEntityLosesItsMark(t *testing.T) {
	f := summarySchema(t)
	bad := f.summaryEntity(0xd7, "Плохой", "Пётр", "Раз")
	good := f.summaryEntity(0xd8, "Хороший", "Пётр", "Два")
	f.refusedSummary(bad.id)

	done := f.drainSummaries(10)
	assert.Equal(t, 2, done.Entities)
	assert.Equal(t, []AuthorSummarySkip{{EntityID: bad.id, SQLState: "22000"}}, done.Skipped)
	assert.Empty(t, f.summaryMarks())
	assert.Equal(t, []string{"", "ru"}, f.summaryLangs(good.id))
	assert.Empty(t, f.summaryLangs(bad.id))
}

// A canceled statement comes back as PostgreSQL's own 57014, which reads like a
// refused entity. Its caller going away is no verdict on the entity, and a pass
// sent after it might still get through before the cancellation reaches it.
func TestACanceledCallerRefusesNoEntity(t *testing.T) {
	requireDatabase(t)
	_, err := db.Exec(`SELECT 1 / 0`)
	require.Error(t, err)
	assert.True(t, summaryEntityRefused(context.Background(), err))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, summaryEntityRefused(ctx, err))
}

// fatalPGError is a FATAL as the driver hands it over. A terminated backend in
// tests comes back as a bare EOF, which a lost-connection test in services sees;
// this is the one that arrives with a SQLSTATE like a refusal.
type fatalPGError struct{ error }

func (fatalPGError) Field(field byte) string {
	return map[byte]string{'V': severityFatal, 'C': "57P01"}[field]
}

func (fatalPGError) IntegrityViolation() bool { return false }

func TestAFatalErrorRefusesNoEntity(t *testing.T) {
	var _ pg.Error = fatalPGError{}
	assert.False(t, summaryEntityRefused(context.Background(), fatalPGError{errors.New("terminating connection")}))
}

func TestACanceledContextEndsTheBatchInsteadOfSkippingIt(t *testing.T) {
	f := summarySchema(t)
	e := f.summaryEntity(0xd1, "Прежний", "Пётр", "Книга")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done, err := DrainAuthorSummaryMarks(ctx, summaryTxs{f.tx}, 10, summaryTimeout)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, done.Entities)
	walked, err := AuthorSummaryWalkStep(ctx, summaryTxs{f.tx}, 10, time.Hour, summaryTimeout)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, walked.Ran)
	assert.Contains(t, f.summaryMarks(), e.id)
	assert.Empty(t, f.summaryLangs(e.id))
}
