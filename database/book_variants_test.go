package database

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/bookvariants"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The book variants read model against a real PostgreSQL (migration 83): the
// rebuild of a card, the marks and the walk that keep it, what leaves it.

type variantFixture struct{ *authorSchemaFixture }

// variantSchema is a fixture transaction with the read model emptied of what
// other tests committed and the walk reset.
func variantSchema(t *testing.T) *variantFixture {
	f := withAuthorSchemaTx(t)
	f.exec(`TRUNCATE book_variant_member, book_variant_group, book_variant_dirty`)
	f.exec(`UPDATE book_variant_walk
		SET walk_number = 1, walk_cursor = NULL, walk_read = 0, walk_skipped = 0, finished_at = NULL`)
	return &variantFixture{f}
}

// entity is a live author entity with the name.
func (f *variantFixture) entity(name string) string {
	f.t.Helper()
	id := uuid.New().String()
	f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
		VALUES (?, 'person', ?, ?, ?)`, id, name, name, strings.ToLower(name))
	return id
}

// variantSpec is what a fixture book differs by.
type variantSpec struct {
	lang, doc, version string
	added              time.Time
	series             map[int64]int64
}

func withDoc(doc, version string) func(*variantSpec) {
	return func(s *variantSpec) { s.doc, s.version = doc, version }
}

func numberedIn(series, no int64) func(*variantSpec) {
	return func(s *variantSpec) { s.series[series] = no }
}

// variantBook is a visible book titled so, credited to the entities by one
// line of the author display, with a current snapshot naming its document.
func (f *variantFixture) variantBook(title string, entities []string, opts ...func(*variantSpec)) int64 {
	f.t.Helper()
	spec := variantSpec{lang: "ru", added: time.Now(), series: map[int64]int64{}}
	for _, o := range opts {
		o(&spec)
	}
	id := f.book()
	f.exec(`UPDATE opds_catalog_book SET title = ?, lang = ?, registerdate = ?, approved = true,
		duplicate_hidden = false WHERE id = ?`, title, spec.lang, spec.added, id)
	if len(entities) > 0 {
		f.exec(`INSERT INTO book_author_display
			(book_id, position, display, sort_key, search_key, source, credit_id, entity_ids)
			VALUES (?, 0, 'Автор', 'автор', 'автор', 'layer', ?, ?::uuid[])`, id, id, pg.Array(entities))
	}
	for series, no := range spec.series {
		f.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (?, ?, ?)`, no, id, series)
	}
	// Snapshots are immutable: the document is written with the row.
	f.exec(`INSERT INTO book_metadata_snapshot
			(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name,
			 source_title, source_document_id, source_document_version, is_current)
		VALUES (?, ?, 'extractor-v1', 'live', 'extracted', 'fixture.zip', ?, ?, nullif(?, ''), nullif(?, ''), true)`,
		id, bookMD5(id), fmt.Sprintf("%d.fb2", id), title, spec.doc, spec.version)
	return id
}

// variantTxs stands for the database in the fixture's transaction, as
// summaryTxs does for the author summary.
func (f *variantFixture) txs() summaryTxs { return summaryTxs{f.tx} }

func (f *variantFixture) rebuild(books ...int64) {
	f.t.Helper()
	for _, id := range books {
		require.NoError(f.t, rebuildBookVariants(context.Background(), f.tx, id, 1))
	}
}

// variantMember is a stored row as the tests read it.
type variantMember struct {
	BookID   int64  `pg:"book_id"`
	GroupKey string `pg:"group_key"`
	BaseKey  string `pg:"base_key"`
	Rank     int    `pg:"variant_rank"`
	Latest   bool   `pg:"latest"`
	Walk     int64  `pg:"walk"`
}

func (f *variantFixture) member(book int64) *variantMember {
	f.t.Helper()
	var rows []variantMember
	_, err := f.tx.Query(&rows, `SELECT book_id, group_key::text, base_key, variant_rank, latest, walk
		FROM book_variant_member WHERE book_id = ?`, book)
	require.NoError(f.t, err)
	if len(rows) == 0 {
		return nil
	}
	return &rows[0]
}

// variantCard is a stored card as the tests read it.
type variantCard struct {
	GroupKey string `pg:"group_key"`
	Lang     string `pg:"lang"`
	Files    int    `pg:"files"`
	Kind     string `pg:"kind"`
	Count    int    `pg:"shown_count"`
	Newest   int64  `pg:"newest_book_id"`
}

func (f *variantFixture) card(key string) *variantCard {
	f.t.Helper()
	var rows []variantCard
	_, err := f.tx.Query(&rows, `SELECT group_key::text, lang, files, kind, shown_count, newest_book_id
		FROM book_variant_group WHERE group_key = ?::uuid`, key)
	require.NoError(f.t, err)
	if len(rows) == 0 {
		return nil
	}
	return &rows[0]
}

// cardOf is the card a book's row names, which must exist.
func (f *variantFixture) cardOf(book int64) *variantCard {
	f.t.Helper()
	m := f.member(book)
	require.NotNil(f.t, m, "book %d has no card row", book)
	c := f.card(m.GroupKey)
	require.NotNil(f.t, c, "book %d names card %s, which does not exist", book, m.GroupKey)
	return c
}

func (f *variantFixture) marks() []int64 {
	f.t.Helper()
	var ids []int64
	_, err := f.tx.Query(&ids, `SELECT book_id FROM book_variant_dirty ORDER BY book_id`)
	require.NoError(f.t, err)
	return ids
}

// orphans are member rows naming a card that does not exist, and cards no
// row names; both must stay empty.
func (f *variantFixture) orphans() (members, cards int) {
	f.t.Helper()
	_, err := f.tx.QueryOne(pg.Scan(&members), `SELECT count(*) FROM book_variant_member AS m
		WHERE NOT EXISTS (SELECT 1 FROM book_variant_group AS g WHERE g.group_key = m.group_key)`)
	require.NoError(f.t, err)
	_, err = f.tx.QueryOne(pg.Scan(&cards), `SELECT count(*) FROM book_variant_group AS g
		WHERE NOT EXISTS (SELECT 1 FROM book_variant_member AS m WHERE m.group_key = g.group_key)`)
	require.NoError(f.t, err)
	return members, cards
}

func (f *variantFixture) assertNoOrphans() {
	f.t.Helper()
	members, cards := f.orphans()
	assert.Zero(f.t, members, "member rows naming no card")
	assert.Zero(f.t, cards, "cards no row names")
}

func TestRebuildGroupsThreeFiles(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Джон Толкин")
	a := f.variantBook("Хоббит, или Туда и обратно", []string{e}, withDoc("A", "1.0"))
	b := f.variantBook("Хоббит или туда и обратно [litres]", []string{e}, withDoc("B", "1.0")) //nolint:misspell // a catalog tag
	c := f.variantBook("Хоббит, или Туда и Обратно", []string{e}, withDoc("C", "1.0"))
	other := f.variantBook("Сильмариллион", []string{e}, withDoc("D", "1.0"))

	f.rebuild(a)

	card := f.cardOf(a)
	assert.Equal(t, 3, card.Files)
	assert.Equal(t, "variants", card.Kind)
	assert.Equal(t, 3, card.Count)
	assert.Equal(t, "ru", card.Lang)
	assert.Equal(t, c, card.Newest)
	ranks := []int{}
	for _, id := range []int64{a, b, c} {
		m := f.member(id)
		require.NotNil(t, m)
		assert.Equal(t, f.member(a).GroupKey, m.GroupKey, "book %d is in another card", id)
		assert.True(t, m.Latest)
		ranks = append(ranks, m.Rank)
	}
	slices.Sort(ranks)
	assert.Equal(t, []int{1, 2, 3}, ranks)
	// The partition rebuild took the other title of the entity too.
	assert.Equal(t, "single", f.cardOf(other).Kind)
	f.assertNoOrphans()
}

func TestRebuildMovesBookBetweenGroups(t *testing.T) {
	f := variantSchema(t)
	tolkien, lewis := f.entity("Джон Толкин"), f.entity("Клайв Льюис")
	a := f.variantBook("Хоббит", []string{tolkien}, withDoc("A", "1.0"))
	b := f.variantBook("Хоббит", []string{tolkien}, withDoc("B", "1.0"))
	c := f.variantBook("Хоббит", []string{tolkien}, withDoc("C", "1.0"))
	f.rebuild(a)
	old := f.member(c).GroupKey

	// An entity build gives c's line another entity: c's card is another one.
	f.exec(`UPDATE book_author_display SET entity_ids = ?::uuid[] WHERE book_id = ?`, pg.Array([]string{lewis}), c)
	f.rebuild(c)

	assert.Equal(t, 2, f.card(old).Files, "the old card kept the book that left it")
	moved := f.member(c)
	require.NotNil(t, moved)
	assert.NotEqual(t, old, moved.GroupKey)
	assert.Equal(t, "single", f.cardOf(c).Kind)
	assert.Equal(t, f.member(a).GroupKey, f.member(b).GroupKey)
	assert.Empty(t, f.marks(), "the rebuilt book was marked though it was rebuilt")
	f.assertNoOrphans()
}

func TestRebuildMarksABookAnotherRebuildMoved(t *testing.T) {
	f := variantSchema(t)
	tolkien, lewis := f.entity("Джон Толкин"), f.entity("Клайв Льюис")
	a := f.variantBook("Хоббит", []string{tolkien}, withDoc("A", "1.0"))
	b := f.variantBook("Хоббит", []string{tolkien}, withDoc("B", "1.0"))
	f.rebuild(a)
	f.exec(`UPDATE book_author_display SET entity_ids = ?::uuid[] WHERE book_id = ?`, pg.Array([]string{lewis}), b)

	// a's partition no longer holds b: b leaves, and is marked for its new card.
	f.rebuild(a)

	assert.Equal(t, "single", f.cardOf(a).Kind)
	assert.Nil(t, f.member(b))
	assert.Equal(t, []int64{b}, f.marks())
	f.assertNoOrphans()
}

// drainVariants drains every variant mark.
func (f *variantFixture) drainVariants() {
	f.t.Helper()
	for i := 0; i < 10 && len(f.marks()) > 0; i++ {
		_, err := DrainBookVariantMarks(context.Background(), f.txs(), 100, summaryTimeout, time.Time{})
		require.NoError(f.t, err)
	}
	require.Empty(f.t, f.marks(), "the marks did not drain")
}

// The destination of a move is rebuilt first (review 1-2, blocker 3): B goes
// from Y's card, which it shares with C, to X's, and A's rebuild finds it
// there before B's own mark is drained. Y's card must not keep B.
func TestRebuildDestinationFirstFixesTheOldCard(t *testing.T) {
	f := variantSchema(t)
	x, y := f.entity("Джон Толкин"), f.entity("Клайв Льюис")
	a := f.variantBook("Хоббит", []string{x}, withDoc("A", "1.0"))
	b := f.variantBook("Хоббит", []string{y}, withDoc("B", "1.0"))
	c := f.variantBook("Хоббит", []string{y}, withDoc("C", "1.0"))
	// B is Y's default: it has a cover.
	f.exec(`UPDATE opds_catalog_book SET cover = true WHERE id = ?`, b)
	f.rebuild(a, b)
	old := f.member(c).GroupKey
	require.Equal(t, old, f.member(b).GroupKey)
	require.Equal(t, 1, f.member(b).Rank)
	require.Equal(t, 2, f.card(old).Files)

	// An author rebuild moves B to X and marks it; A's rebuild runs first.
	f.exec(`UPDATE book_author_display SET entity_ids = ?::uuid[] WHERE book_id = ?`, pg.Array([]string{x}), b)
	require.NoError(t, MarkBookVariantsDirty(context.Background(), f.tx, b))
	f.rebuild(a)
	assert.Contains(t, f.marks(), c, "the card B left was not queued")
	f.assertNoOrphans()

	f.drainVariants()

	card := f.card(old)
	require.NotNil(t, card, "C's card is gone")
	assert.Equal(t, 1, card.Files)
	assert.Equal(t, "single", card.Kind)
	assert.Equal(t, 1, card.Count)
	assert.Equal(t, c, card.Newest)
	assert.Equal(t, old, f.member(c).GroupKey)
	assert.Equal(t, 1, f.member(c).Rank)
	assert.Equal(t, f.member(a).GroupKey, f.member(b).GroupKey)
	assert.Equal(t, 2, f.cardOf(a).Files)
	f.assertNoOrphans()
}

// The same with B a card of its own before: its own card must not outlive it.
func TestRebuildDestinationFirstTakesASoloBooksCard(t *testing.T) {
	f := variantSchema(t)
	x := f.entity("Джон Толкин")
	a := f.variantBook("Хоббит", []string{x}, withDoc("A", "1.0"))
	b := f.variantBook("Хоббит", nil, withDoc("B", "1.0"))
	f.rebuild(a, b)
	require.Equal(t, bookvariants.SoloKey(b).String(), f.member(b).GroupKey)

	f.exec(`INSERT INTO book_author_display (book_id, position, display, sort_key, search_key, source, credit_id, entity_ids)
		VALUES (?, 0, 'Автор', 'автор', 'автор', 'layer', ?, ?::uuid[])`, b, b, pg.Array([]string{x}))
	require.NoError(t, MarkBookVariantsDirty(context.Background(), f.tx, b))
	f.rebuild(a)

	assert.Nil(t, f.card(bookvariants.SoloKey(b).String()), "B's own card outlived it")
	f.assertNoOrphans()
	f.drainVariants()
	assert.Equal(t, f.member(a).GroupKey, f.member(b).GroupKey)
	assert.Equal(t, 2, f.cardOf(a).Files)
	f.assertNoOrphans()
}

func TestRebuildDropsHiddenAndDeleted(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Елена Белова")
	a := f.variantBook("Ты дура", []string{e}, withDoc("A", "1.0"))
	b := f.variantBook("Ты дура", []string{e}, withDoc("A", "1.1"))
	c := f.variantBook("Ты дура", []string{e}, withDoc("A", "1.2"))
	f.rebuild(a)
	assert.Equal(t, "revisions", f.cardOf(a).Kind)

	f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, b)
	_, err := deleteBooksWithLayer(f.tx, []int64{c})
	require.NoError(t, err)
	f.rebuild(b, c)

	assert.Nil(t, f.member(b), "a hidden book kept its row")
	assert.Nil(t, f.member(c), "a deleted book kept its row")
	card := f.cardOf(a)
	assert.Equal(t, 1, card.Files)
	assert.Equal(t, "single", card.Kind)
	assert.Equal(t, a, card.Newest)
	f.assertNoOrphans()
}

func TestRebuildSoloBook(t *testing.T) {
	f := variantSchema(t)
	unknown := f.entity("Unknown")
	solo := f.variantBook("Сборник", []string{unknown})
	f.rebuild(solo)

	m := f.member(solo)
	require.NotNil(t, m)
	assert.Equal(t, bookvariants.SoloKey(solo).String(), m.GroupKey)
	assert.Empty(t, m.BaseKey)
	assert.Equal(t, 1, m.Rank)
	assert.Equal(t, "single", f.cardOf(solo).Kind)

	// Unapproved, it leaves, and its card with it.
	f.exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, solo)
	f.rebuild(solo)
	assert.Nil(t, f.member(solo))
	assert.Nil(t, f.card(m.GroupKey))
	f.assertNoOrphans()
}

func TestRebuildSoloBookJoinsACardWhenItGetsAnAuthor(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Джон Толкин")
	a := f.variantBook("Хоббит", []string{e}, withDoc("A", "1.0"))
	b := f.variantBook("Хоббит", nil, withDoc("B", "1.0"))
	f.rebuild(a, b)
	assert.Equal(t, bookvariants.SoloKey(b).String(), f.member(b).GroupKey)

	f.exec(`INSERT INTO book_author_display (book_id, position, display, sort_key, search_key, source, credit_id, entity_ids)
		VALUES (?, 0, 'Автор', 'автор', 'автор', 'layer', ?, ?::uuid[])`, b, b, pg.Array([]string{e}))
	f.rebuild(b)

	assert.Equal(t, f.member(a).GroupKey, f.member(b).GroupKey)
	assert.Nil(t, f.card(bookvariants.SoloKey(b).String()), "the book's own card outlived it")
	f.assertNoOrphans()
}

func TestRebuildSplitsSeriesVolumes(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Фрэнк Герберт")
	one := f.variantBook("Дюна", []string{e}, numberedIn(77, 1))
	two := f.variantBook("Дюна", []string{e}, numberedIn(77, 2))
	loose := f.variantBook("Дюна", []string{e})
	f.rebuild(one)

	keys := map[string]bool{f.member(one).GroupKey: true, f.member(two).GroupKey: true, f.member(loose).GroupKey: true}
	assert.Len(t, keys, 3, "two volumes and an unnumbered file share a card")
	f.assertNoOrphans()
}

func TestDrainAuthorDisplayMarksMarksVariants(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Джон Толкин")
	a := f.variantBook("Хоббит", []string{e})
	f.exec(`TRUNCATE book_variant_dirty`)
	require.NoError(t, MarkAuthorDisplayDirty(context.Background(), f.tx, a))

	f.drain()

	assert.Contains(t, f.marks(), a)
}

func TestDrainRebuildsMarkedBooks(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Джон Толкин")
	a := f.variantBook("Хоббит", []string{e}, withDoc("A", "1.0"))
	b := f.variantBook("Хоббит", []string{e}, withDoc("B", "1.0"))
	require.NoError(t, MarkBookVariantsDirty(context.Background(), f.tx, a, b, a))

	done, err := DrainBookVariantMarks(context.Background(), f.txs(), 10, summaryTimeout, time.Time{})
	require.NoError(t, err)

	assert.Equal(t, 2, done.Books)
	assert.Empty(t, done.Skipped)
	assert.Empty(t, f.marks())
	assert.Equal(t, 2, f.cardOf(a).Files)
}

func TestDrainPassesRefusedBook(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Джон Толкин")
	bad := f.variantBook("Хоббит", []string{e})
	good := f.variantBook("Сильмариллион", []string{f.entity("Кристофер Толкин")})
	// The card row of bad's partition cannot be written: a rank below 1.
	f.exec(`ALTER TABLE book_variant_member DROP CONSTRAINT book_variant_member_rank_check`)
	f.exec(`ALTER TABLE book_variant_member ADD CONSTRAINT book_variant_member_rank_check
		CHECK (variant_rank >= 1 AND book_id <> ?)`, bad)
	require.NoError(t, MarkBookVariantsDirty(context.Background(), f.tx, bad))
	require.NoError(t, MarkBookVariantsDirty(context.Background(), f.tx, good))
	f.exec(`UPDATE book_variant_dirty SET marked_at = marked_at - interval '1 minute' WHERE book_id = ?`, bad)

	done, err := DrainBookVariantMarks(context.Background(), f.txs(), 10, summaryTimeout, time.Time{})
	require.NoError(t, err)

	// A refused book ends the batch: it may have spent the whole timeout.
	assert.Equal(t, 1, done.Books)
	require.Len(t, done.Skipped, 1)
	assert.Equal(t, bad, done.Skipped[0].BookID)
	assert.Equal(t, "23514", done.Skipped[0].SQLState)
	assert.Equal(t, []int64{good}, f.marks(), "the refused mark was not passed")
	assert.Nil(t, f.member(bad))

	done, err = DrainBookVariantMarks(context.Background(), f.txs(), 10, summaryTimeout, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 1, done.Books)
	assert.Empty(t, f.marks())
	assert.NotNil(t, f.member(good))
}

// The walk passes a refused book once and ends its batch there.
func TestWalkPassesRefusedBook(t *testing.T) {
	f := variantSchema(t)
	bad := f.variantBook("Хоббит", []string{f.entity("Джон Толкин")})
	good := f.variantBook("Сильмариллион", []string{f.entity("Кристофер Толкин")})
	f.exec(`ALTER TABLE book_variant_member DROP CONSTRAINT book_variant_member_rank_check`)
	f.exec(`ALTER TABLE book_variant_member ADD CONSTRAINT book_variant_member_rank_check
		CHECK (variant_rank >= 1 AND book_id <> ?)`, bad)
	f.walkFixtureOnly()

	step, err := BookVariantWalkStep(context.Background(), f.txs(), 10, time.Hour, summaryTimeout, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 1, step.Books)
	require.Len(t, step.Skipped, 1)
	assert.Equal(t, bad, step.Skipped[0].BookID)
	assert.Nil(t, f.member(good), "the batch went on past a refused book")

	total := f.walkToTheEnd()
	assert.Equal(t, int64(2), total.Read)
	assert.Equal(t, int64(1), total.Refused)
	assert.Nil(t, f.member(bad))
	assert.NotNil(t, f.member(good))
}

// A batch past its deadline takes no further book, so the author display work
// behind it waits for one book at most (review 1-2, notes).
func TestVariantBatchStopsAtItsDeadline(t *testing.T) {
	f := variantSchema(t)
	books := []int64{
		f.variantBook("Хоббит", []string{f.entity("Джон Толкин")}),
		f.variantBook("Нарния", []string{f.entity("Клайв Льюис")}),
		f.variantBook("Дюна", []string{f.entity("Фрэнк Герберт")}),
	}
	require.NoError(t, MarkBookVariantsDirty(context.Background(), f.tx, books...))
	passed := time.Now().Add(-time.Second)

	drained, err := DrainBookVariantMarks(context.Background(), f.txs(), 10, summaryTimeout, passed)
	require.NoError(t, err)
	assert.Equal(t, 1, drained.Books, "the drain went on past its deadline")
	assert.Len(t, f.marks(), 2)

	f.exec(`TRUNCATE book_variant_member, book_variant_group, book_variant_dirty`)
	f.walkFixtureOnly()
	walked, err := BookVariantWalkStep(context.Background(), f.txs(), 10, time.Hour, summaryTimeout, passed)
	require.NoError(t, err)
	assert.Equal(t, 1, walked.Books, "the walk went on past its deadline")
	assert.False(t, walked.Finished)

	// With time left the batch takes all it is given.
	drained, err = DrainBookVariantMarks(context.Background(), f.txs(), 10, summaryTimeout,
		time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Zero(t, drained.Books)
	walked, err = BookVariantWalkStep(context.Background(), f.txs(), 10, time.Hour, summaryTimeout,
		time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 2, walked.Books)
	assert.True(t, walked.Finished)
}

// walkFixtureOnly starts a walk below the fixture's IDs, so it walks the
// fixture's books and not the catalog the database holds. It starts three
// empty windows below them: the walk has to look past those to find them.
func (f *variantFixture) walkFixtureOnly() {
	f.exec(`UPDATE book_variant_walk SET walk_cursor = ?`, authorSchemaIDBase-1-3*variantWalkWindow)
}

func (f *variantFixture) walkToTheEnd() BookVariantWalkResult {
	f.t.Helper()
	var total BookVariantWalkResult
	for i := 0; i < 100; i++ {
		step, err := BookVariantWalkStep(context.Background(), f.txs(), 2, time.Hour, summaryTimeout, time.Time{})
		require.NoError(f.t, err)
		total.Books += step.Books
		total.Skipped = append(total.Skipped, step.Skipped...)
		if step.Finished {
			total.Finished, total.Read, total.Refused = true, step.Read, step.Refused
			return total
		}
	}
	f.t.Fatal("the walk did not finish")
	return total
}

func TestWalkBackfillsAndFinishes(t *testing.T) {
	f := variantSchema(t)
	tolkien, lewis := f.entity("Джон Толкин"), f.entity("Клайв Льюис")
	books := []int64{
		f.variantBook("Хоббит", []string{tolkien}, withDoc("A", "1.0")),
		f.variantBook("Хоббит", []string{tolkien}, withDoc("B", "1.0")),
		f.variantBook("Нарния", []string{lewis}),
		f.variantBook("unknown", []string{lewis}),
	}
	hidden := f.variantBook("Нарния", []string{lewis})
	f.exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, hidden)
	f.walkFixtureOnly()

	ready, err := BookVariantsReady(context.Background(), f.tx)
	require.NoError(t, err)
	assert.False(t, ready, "ready before the first walk finished")

	total := f.walkToTheEnd()

	assert.True(t, total.Finished)
	for _, id := range books {
		m := f.member(id)
		require.NotNil(t, m, "book %d has no card row after the walk", id)
		assert.Equal(t, int64(1), m.Walk)
	}
	assert.Nil(t, f.member(hidden))
	ready, err = BookVariantsReady(context.Background(), f.tx)
	require.NoError(t, err)
	assert.True(t, ready)
	var number int64
	_, err = f.tx.QueryOne(pg.Scan(&number), `SELECT walk_number FROM book_variant_walk`)
	require.NoError(t, err)
	assert.Equal(t, int64(2), number)
	f.assertNoOrphans()
}

func TestWalkSkipsBooksRebuiltThisWalk(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Джон Толкин")
	f.variantBook("Хоббит", []string{e}, withDoc("A", "1.0"))
	f.variantBook("Хоббит", []string{e}, withDoc("B", "1.0"))
	f.variantBook("Хоббит", []string{e}, withDoc("C", "1.0"))
	f.walkFixtureOnly()

	total := f.walkToTheEnd()

	// The first book's partition stamps all three: the walk takes one.
	assert.Equal(t, int64(1), total.Read)
	assert.Equal(t, 1, total.Books)
}

func TestWalkTakesOutTheRowsOfGoneBooks(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Джон Толкин")
	a := f.variantBook("Хоббит", []string{e}, withDoc("A", "1.0"))
	b := f.variantBook("Хоббит", []string{e}, withDoc("B", "1.0"))
	// Alone in its card: no live book leads the walk to its partition.
	alone := f.variantBook("Нарния", []string{f.entity("Клайв Льюис")})
	solo := f.variantBook("unknown", []string{e})
	f.rebuild(a, alone, solo)
	require.NotNil(t, f.member(alone))
	require.NotNil(t, f.member(solo))
	// Deleted, and their marks lost: only the walk can find them.
	_, err := deleteBooksWithLayer(f.tx, []int64{b, alone, solo})
	require.NoError(t, err)
	f.exec(`TRUNCATE book_variant_dirty`)
	f.exec(`UPDATE book_variant_walk SET walk_number = 2`)
	f.walkFixtureOnly()

	f.walkToTheEnd()

	assert.Nil(t, f.member(b))
	assert.Nil(t, f.member(alone), "a gone book alone in its card kept its row")
	assert.Nil(t, f.member(solo), "a gone book of its own card kept its row")
	assert.Equal(t, "single", f.cardOf(a).Kind)
	f.assertNoOrphans()
}

func TestDirectMarks(t *testing.T) {
	f := variantSchema(t)
	e := f.entity("Джон Толкин")
	series := f.variantBook("Хоббит", []string{e})
	gone := f.variantBook("Сильмариллион", []string{e})

	require.NoError(t, updateBookSeriesFromUpdateRequest(f.tx, series, []models.Series{}))
	_, err := deleteBooksWithLayer(f.tx, []int64{gone})
	require.NoError(t, err)

	assert.Equal(t, []int64{series, gone}, f.marks())
}

// A rescan applies in a transaction of its own and commits: this works on a
// catalog book with its own title, so the apply changes nothing but the mark,
// and takes the mark back.
func TestApplyingARescanMarksTheBook(t *testing.T) {
	requireDatabase(t)
	book := aBook(t, "title <> ''")
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM book_variant_dirty WHERE book_id = ?`, book.ID)
		_, _ = db.Exec(`DELETE FROM book_rescan_pending WHERE book_id = ?`, book.ID)
	})
	_, err := db.Exec(`DELETE FROM book_variant_dirty WHERE book_id = ?`, book.ID)
	require.NoError(t, err)
	require.NoError(t, SaveRescanPending(&models.BookRescanPending{BookID: book.ID, Title: book.Title}))
	yes, no := true, false

	updated, _, err := ApplySelectiveRescanChanges(book.ID, &models.RescanApprovalRequest{
		Action: "approve", UpdateTitle: &yes, UpdateAnnotation: &no, UpdateLang: &no, UpdateDocDate: &no,
		UpdateAuthors: &no, UpdateSeries: &no, UpdateCover: &no, UpdateTags: &no,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"title"}, updated)
	var marked int
	_, err = db.QueryOne(pg.Scan(&marked), `SELECT count(*) FROM book_variant_dirty WHERE book_id = ?`, book.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, marked)
}
