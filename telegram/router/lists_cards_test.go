package router

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot's scoped lists list cards, as the site's grouped list of the same
// scope: one row per card, the same count and pages.

// listFixture is a catalog of cards by one author, in a scratch database
// that is the package handle.
type listFixture struct {
	t      *testing.T
	db     *pg.DB
	entity uuid.UUID
	author int64
	series int64
	user   int64
	// lang is the language of the files written next; "" is Russian.
	lang string
}

func newListFixture(t *testing.T) *listFixture {
	t.Helper()
	db := scanfixture.ScratchDB(t)
	f := &listFixture{t: t, db: db, entity: uuid.New()}
	f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
		VALUES (?, 'person', 'Станислав Лем', 'Лем Станислав', public.search_normalize('Станислав Лем'))`, f.entity)
	f.author = f.scalar(`INSERT INTO opds_catalog_author (full_name) VALUES ('Лем Станислав') RETURNING id`)
	f.series = f.scalar(`INSERT INTO opds_catalog_series (ser, lang_code) VALUES ('Звёздные дневники', 1) RETURNING id`)
	f.user = f.scalar(`INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', false, 'lists-reader', 'lists-reader@fixture.local', now()) RETURNING id`)
	return f
}

// otherAuthor makes the files written next another author's.
func (f *listFixture) otherAuthor() {
	f.t.Helper()
	f.entity = uuid.New()
	f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
		VALUES (?, 'person', 'Аркадий Стругацкий', 'Стругацкий Аркадий', public.search_normalize('Аркадий Стругацкий'))`, f.entity)
	f.author = f.scalar(`INSERT INTO opds_catalog_author (full_name) VALUES ('Стругацкий Аркадий') RETURNING id`)
}

func (f *listFixture) exec(query string, args ...any) {
	f.t.Helper()
	_, err := f.db.Exec(query, args...)
	require.NoError(f.t, err)
}

func (f *listFixture) scalar(query string, args ...any) int64 {
	f.t.Helper()
	var id int64
	_, err := f.db.QueryOne(pg.Scan(&id), query, args...)
	require.NoError(f.t, err)
	return id
}

// file is a visible book titled so by the fixture's author, its own
// document, numbered serNo in the fixture's series (0: not in it).
func (f *listFixture) file(title, doc string, serNo int64) int64 {
	f.t.Helper()
	lang := f.lang
	if lang == "" {
		lang = "ru"
	}
	id := f.scalar(`INSERT INTO opds_catalog_book
		(filename, path, format, title, annotation, lang, registerdate, docdate, approved, cover, md5, duplicate_hidden)
		VALUES (?, 'lists.zip', 'fb2', ?, '', ?, ?, '', true, false, md5(?), false) RETURNING id`,
		doc+".fb2", title, lang, time.Now().Add(-24*time.Hour), doc)
	f.scalar(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?) RETURNING id`, id, f.author)
	f.exec(`INSERT INTO book_author_display (book_id, position, display, sort_key, search_key, source, credit_id, entity_ids)
		VALUES (?, 0, 'Станислав Лем', 'лем станислав', 'станислав лем', 'layer', ?, ARRAY[?::uuid])`, id, id, f.entity)
	f.exec(`INSERT INTO book_metadata_snapshot
			(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name,
			 source_title, source_document_id, source_document_version, is_current)
		SELECT id, md5, 'extractor-v1', 'live', 'extracted', path, filename, title, ?, '1.0', true
		FROM opds_catalog_book WHERE id = ?`, doc, id)
	if serNo > 0 {
		f.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (?, ?, ?)`, serNo, id, f.series)
	}
	return id
}

// card is n files of one title, each its own document.
func (f *listFixture) card(title string, n int, serNo int64) []int64 {
	f.t.Helper()
	out := make([]int64, n)
	for i := range out {
		out[i] = f.file(title, fmt.Sprintf("%s-%d-%d", title, i, time.Now().UnixNano()), serNo)
	}
	return out
}

// walk runs the card read model's walk to its end: the bot lists cards.
func (f *listFixture) walk() {
	f.t.Helper()
	for step := 0; step < 200; step++ {
		res, err := database.BookVariantWalkStep(context.Background(), f.db, 50, time.Hour, 5*time.Second, time.Time{})
		require.NoError(f.t, err)
		if res.Finished {
			return
		}
	}
	f.t.Fatal("the walk did not finish")
}

// byRank is the card's files by rank.
func (f *listFixture) byRank(files []int64) []int64 {
	f.t.Helper()
	var out []int64
	_, err := f.db.Query(&out, `SELECT book_id FROM book_variant_member WHERE book_id IN (?) ORDER BY variant_rank`, pg.In(files))
	require.NoError(f.t, err)
	require.Len(f.t, out, len(files))
	return out
}

func (f *listFixture) bot() *Bot { return &Bot{Binding: &models.TelegramBinding{UserID: f.user}} }

func pageIDs(page *bookPage) []int64 {
	out := make([]int64, len(page.list.Books))
	for i := range page.list.Books {
		out[i] = page.list.Books[i].ID
	}
	return out
}

func cardIDs(books []models.Book) []int64 {
	out := make([]int64, len(books))
	for i := range books {
		out[i] = books[i].ID
	}
	return out
}

// The bot's four scoped lists are the site's grouped list of the same scope
// under the bot's own order (authors by title, a series by number) and
// language: the same cards in the same order on every page, shown on the
// same files, saying the same of them, and counted alike.
func TestBotListsMatchTheSiteCards(t *testing.T) {
	f := newListFixture(t)
	solaris := f.card("Солярис", 3, 1)
	eden := f.card("Эдем", 2, 2)
	lone := f.file("Насморк", "nasmork", 0)
	for i := 0; i < 6; i++ {
		f.file(fmt.Sprintf("Рассказ %d", i), fmt.Sprintf("story-%d", i), 0)
	}
	f.lang = "en"
	english := f.card("Solaris", 2, 0)
	f.walk()
	ranked := f.byRank(solaris)
	f.exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?), (?, ?), (?, ?), (?, ?)`,
		f.user, ranked[2], f.user, lone, f.user, ranked[0], f.user, english[0])
	curated := f.scalar(`INSERT INTO book_collections (name, is_public, is_curated) VALUES ('Подборка', true, true) RETURNING id`)
	for pos, book := range []int64{eden[0], ranked[1], lone, english[1]} {
		f.exec(`INSERT INTO book_collection_items (collection_id, book_id, external_title, match_status, position)
			VALUES (?, ?, 'x', ?, ?)`, curated, book, models.MatchStatusAutoMatched, pos)
	}
	var handle int64
	_, err := f.db.QueryOne(pg.Scan(&handle), `INSERT INTO telegram_author_entity (entity_id) VALUES (?) RETURNING id`, f.entity)
	require.NoError(t, err)
	ctx := context.Background()

	byTitle, bySeries := models.BookSortTitle, models.BookSortSeries
	cases := []struct {
		name     string
		entities bool
		view     models.TelegramView
		filters  models.BookFilters
		total    int
	}{
		{"author, all languages", false, models.TelegramView{Kind: ViewAuthorBooks, AuthorID: f.author},
			models.BookFilters{Author: int(f.author), Sort: byTitle}, 10},
		{"author, Russian", false, models.TelegramView{Kind: ViewAuthorBooks, AuthorID: f.author, Lang: "ru"},
			models.BookFilters{Author: int(f.author), Sort: byTitle, Lang: "ru"}, 9},
		{"author, English", false, models.TelegramView{Kind: ViewAuthorBooks, AuthorID: f.author, Lang: "en"},
			models.BookFilters{Author: int(f.author), Sort: byTitle, Lang: "en"}, 1},
		{"author entity, Russian", true, models.TelegramView{Kind: ViewAuthorBooks, AuthorID: handle, Lang: "ru"},
			models.BookFilters{AuthorEntity: f.entity, Sort: byTitle, Lang: "ru"}, 9},
		{"series", false, models.TelegramView{Kind: ViewSeriesBooks, SeriesID: f.series},
			models.BookFilters{Series: int(f.series), Sort: bySeries}, 2},
		{"collection", false, models.TelegramView{Kind: ViewCollectionBooks, CollectionID: curated},
			models.BookFilters{CuratedCollection: curated}, 4},
		{"collection, Russian", false, models.TelegramView{Kind: ViewCollectionBooks, CollectionID: curated, Lang: "ru"},
			models.BookFilters{CuratedCollection: curated, Lang: "ru"}, 3},
		// The favorites are listed in every language, whatever the view says.
		{"favorites", false, models.TelegramView{Kind: ViewFavorites, Favorites: true, Lang: "ru"},
			models.BookFilters{Fav: true}, 3},
	}
	for _, tc := range cases {
		r := &Router{cfg: Config{Cards: true, Entities: tc.entities}}
		for page := 0; page < 2; page++ {
			name := fmt.Sprintf("%s, page %d", tc.name, page+1)
			got, err := r.loadList(ctx, f.bot(), &tc.view, page)
			require.NoError(t, err, name)
			want := tc.filters
			want.Limit, want.Offset = 5, page*5
			books, entries, total, err := database.GetBookCards(ctx, f.user, want)
			require.NoError(t, err, name)
			assert.Equal(t, tc.total, got.list.Total, name)
			assert.Equal(t, total, got.list.Total, "%s: the site's count", name)
			assert.Equal(t, cardIDs(books), pageIDs(got), "%s: the same cards in the same order, on the same files", name)
			require.Len(t, got.list.Variants, len(got.list.Books), name)
			for i := range entries {
				assert.Equal(t, entries[i].Variants, got.list.Variants[i], "%s: row %d says what the site's says", name, i)
			}
		}
	}
}

// Favorites and a collection list the saved file of a card, not its
// default.
func TestBotListsShowTheSavedFile(t *testing.T) {
	f := newListFixture(t)
	solaris := f.card("Солярис", 3, 0)
	f.walk()
	ranked := f.byRank(solaris)
	f.exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?)`, f.user, ranked[2])
	curated := f.scalar(`INSERT INTO book_collections (name, is_public, is_curated) VALUES ('Подборка', true, true) RETURNING id`)
	f.exec(`INSERT INTO book_collection_items (collection_id, book_id, external_title, match_status, position)
		VALUES (?, ?, 'x', ?, 0)`, curated, ranked[1], models.MatchStatusAutoMatched)
	r := &Router{cfg: Config{Cards: true}}
	ctx := context.Background()

	page, err := r.loadList(ctx, f.bot(), &models.TelegramView{Kind: ViewFavorites, Favorites: true}, 0)
	require.NoError(t, err)
	assert.Equal(t, []int64{ranked[2]}, pageIDs(page), "the starred file")
	assert.Equal(t, models.BookVariantReasonSaved, page.list.Variants[0].Reason)

	page, err = r.loadList(ctx, f.bot(), &models.TelegramView{Kind: ViewCollectionBooks, CollectionID: curated}, 0)
	require.NoError(t, err)
	assert.Equal(t, []int64{ranked[1]}, pageIDs(page), "the curated file")
}

// Before the read model's first walk the bot lists files, as the site does.
func TestBotListsPerFileBeforeTheWalk(t *testing.T) {
	f := newListFixture(t)
	f.card("Солярис", 3, 0)
	f.walk()
	f.exec(`UPDATE book_variant_walk SET finished_at = NULL`)
	r := &Router{cfg: Config{Cards: true}}
	page, err := r.loadList(context.Background(), f.bot(), &models.TelegramView{Kind: ViewAuthorBooks, AuthorID: f.author}, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, page.list.Total)
	assert.Empty(t, page.list.Variants)
}

// A series keeps its order by number, cards and all: 1, 2, then the
// unnumbered.
func TestBotSeriesKeepsItsOrder(t *testing.T) {
	f := newListFixture(t)
	two := f.card("Эдем", 2, 2)
	one := f.card("Солярис", 3, 1)
	lone := f.file("Насморк", "nasmork", 0)
	f.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (0, ?, ?)`, lone, f.series)
	f.walk()
	r := &Router{cfg: Config{Cards: true}}
	page, err := r.loadList(context.Background(), f.bot(), &models.TelegramView{Kind: ViewSeriesBooks, SeriesID: f.series}, 0)
	require.NoError(t, err)
	assert.Equal(t, []int64{f.byRank(one)[0], f.byRank(two)[0], lone}, pageIDs(page))
}

// A page of cards reads the same statements as a page of single books: the
// cards' files are read in batches.
func TestBotListReadsDoNotGrowWithCards(t *testing.T) {
	f := newListFixture(t)
	for i := 0; i < 5; i++ {
		f.card(fmt.Sprintf("Карточка %d", i), 3, 0)
	}
	cardsBy := f.author
	f.otherAuthor()
	for i := 0; i < 5; i++ {
		f.file(fmt.Sprintf("Одна %d", i), fmt.Sprintf("one-%d", i), 0)
	}
	singlesBy := f.author
	f.walk()
	var n atomic.Int64
	f.db.AddQueryHook(countHook{n: &n})
	r := &Router{cfg: Config{Cards: true}}
	count := func(author int64) int {
		view := &models.TelegramView{Kind: ViewAuthorBooks, AuthorID: author}
		_, err := r.loadList(context.Background(), f.bot(), view, 0) // the readiness is read once
		require.NoError(t, err)
		n.Store(0)
		page, err := r.loadList(context.Background(), f.bot(), view, 0)
		require.NoError(t, err)
		require.Equal(t, 5, page.list.Total)
		return int(n.Load())
	}
	cards, single := count(cardsBy), count(singlesBy)
	t.Logf("statements of a page: 5 cards of 3 files %d, 5 single books %d", cards, single)
	assert.Equal(t, single, cards)
}

type countHook struct{ n *atomic.Int64 }

func (h countHook) BeforeQuery(ctx context.Context, _ *pg.QueryEvent) (context.Context, error) {
	h.n.Add(1)
	return ctx, nil
}

func (countHook) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

// The inline picker with no query lists the favorites as the panel does: one
// entry per card, on the saved file, saying what the card holds.
func TestInlineFavoritesOneEntryPerCard(t *testing.T) {
	f := newListFixture(t)
	solaris := f.card("Солярис", 3, 0)
	f.walk()
	ranked := f.byRank(solaris)
	f.exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?), (?, ?)`, f.user, ranked[2], f.user, ranked[1])
	r := &Router{cfg: Config{Cards: true}}
	books, variants, total, err := r.inlineBooks(context.Background(), f.bot(), "", 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, books, 1)
	assert.Equal(t, ranked[1], books[0].ID, "the best-ranked saved file")
	require.NotNil(t, variants[books[0].ID])
	assert.Equal(t, 3, variants[books[0].ID].Count)
}

// A search in the favorites lists the saved file of a card, once.
func TestFavoritesSearchShowsTheSavedFile(t *testing.T) {
	f := newListFixture(t)
	solaris := f.card("Солярис", 3, 0)
	f.walk()
	ranked := f.byRank(solaris)
	f.exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?)`, f.user, ranked[2])
	r := &Router{cfg: Config{Cards: true, SearchTimeout: 5 * time.Second,
		Search: services.NewSearchService(database.NewPGSearchRepositoryFor(f.db, false))}}
	page, err := r.loadList(context.Background(), f.bot(), &models.TelegramView{Kind: ViewFavoritesSearch, Query: "солярис"}, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, page.list.Total)
	assert.Equal(t, []int64{ranked[2]}, pageIDs(page))
	require.Len(t, page.list.Variants, 1)
	require.NotNil(t, page.list.Variants[0])
	assert.Equal(t, 3, page.list.Variants[0].Count)
}
