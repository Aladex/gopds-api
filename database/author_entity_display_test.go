package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The read model's entity links (migration 57, design E phase E2): every
// layer row carries the entities its credit names, every assignment write
// marks the books it touches, the walk catches a missed mark, and the
// entity names count the rows that carry them.

// entityIDsOf is a book's rows' entity IDs in line order, as text.
func (f *authorSchemaFixture) entityIDsOf(book int64) [][]string {
	f.t.Helper()
	var rows []struct {
		EntityIDs []string `pg:"entity_ids,array"`
	}
	_, err := f.tx.Query(&rows, `SELECT coalesce(entity_ids::text[], '{}') AS entity_ids
		FROM book_author_display WHERE book_id = ? ORDER BY position`, book)
	require.NoError(f.t, err)
	out := make([][]string, len(rows))
	for i := range rows {
		out[i] = rows[i].EntityIDs
	}
	return out
}

// drain rebuilds every marked book.
func (f *authorSchemaFixture) drain() AuthorDisplayDrain {
	f.t.Helper()
	d, err := DrainAuthorDisplayMarks(context.Background(), f.tx, 100000)
	require.NoError(f.t, err)
	return d
}

// entityNames are an entity's name variants with their books.
func (f *authorSchemaFixture) entityNames(id uuid.UUID) map[string]int {
	f.t.Helper()
	var rows []struct {
		SearchKey string `pg:"search_key"`
		Books     int    `pg:"books,use_zero"`
	}
	_, err := f.tx.Query(&rows, `SELECT search_key, books FROM author_entity_name WHERE entity_id = ?`, id)
	require.NoError(f.t, err)
	out := map[string]int{}
	for _, r := range rows {
		out[r.SearchKey] = r.Books
	}
	return out
}

func (f *authorSchemaFixture) clearEntityMarks() {
	f.t.Helper()
	f.exec(`DELETE FROM book_author_display_dirty`)
}

// A build pass marks the books of every credit whose assignment it wrote;
// draining the marks gives each layer row its entity: the fingerprint's, or
// the credit's own decision over it (a book moved out of a bucket).
func TestTheBuildMarksItsBooksAndTheRowsCarryTheirEntities(t *testing.T) {
	f := entitySchema(t)
	full, s1 := f.entityBook("Мост", 601)
	moved, s2 := f.entityBook("Река", 601)
	stays, s3 := f.entityBook("Лес")
	f.entityCredit(s1, 0x61, "Иванов", "Алексей", "Викторович")
	f.entityCredit(s2, 0x62, "Иванов", "Алексей")
	stayed := f.entityCredit(s3, 0x62, "Иванов", "Алексей")
	f.clearMarks()
	f.rebuild(full, moved, stays)
	assert.Equal(t, [][]string{{}}, f.entityIDsOf(full), "no entity before the build")

	p, _, _ := f.pass()
	assert.Subset(t, f.marks(), []int64{full, moved, stays}, "the build marked every book")
	f.drain()
	entity := p.EntityOf(f.creditIDByFingerprint(s1, fingerprint(32, 0x61))).String()
	bucket := p.EntityOf(stayed).String()
	require.NotEqual(t, entity, bucket)
	assert.Equal(t, [][]string{{entity}}, f.entityIDsOf(full))
	assert.Equal(t, [][]string{{entity}}, f.entityIDsOf(moved), "the credit's decision over its fingerprint's")
	assert.Equal(t, [][]string{{bucket}}, f.entityIDsOf(stays), "the fingerprint's entity")
	assert.Zero(t, f.drain().Differed, "a second drain finds nothing to change")
}

// Several people in one credit (§6 item 5): the credit links to each of
// them in segment order only when every segment has an entity.
func TestACreditOfSeveralPeopleLinksOnlyWhenEverySegmentHasAnEntity(t *testing.T) {
	f := entitySchema(t)
	book, snap := f.entityBook("Трудно быть богом")
	credit := f.entityCredit(snap, 0x71, "Стругацкие", "Аркадий и Борис")
	arkady, boris := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, e := range []struct {
		id   uuid.UUID
		name string
	}{{arkady, "Аркадий Стругацкий"}, {boris, "Борис Стругацкий"}} {
		f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key) VALUES (?, 'person', ?, ?, ?)`,
			e.id, e.name, e.name, e.name)
	}
	f.exec(`INSERT INTO author_entity_assignment (source_fingerprint, extractor_version, ordinal, entity_id, method, rule)
		VALUES (?, 'extractor-v1', 0, ?, 'deterministic', 'D1'), (?, 'extractor-v1', 1, ?, 'deterministic', 'D1')`,
		fingerprint(32, 0x71), arkady, fingerprint(32, 0x71), boris)
	f.drain()
	assert.Equal(t, [][]string{{arkady.String(), boris.String()}}, f.entityIDsOf(book))

	// One segment without an entity unlinks the whole credit.
	user := f.user()
	f.exec(`INSERT INTO author_entity_credit_assignment (credit_id, ordinal, entity_id, method, rule, created_by_user_id)
		VALUES (?, 1, NULL, 'manual', 'manual', ?)`, credit, user)
	f.drain()
	assert.Equal(t, [][]string{{}}, f.entityIDsOf(book))

	// So does a gap in the segments.
	f.exec(`DELETE FROM author_entity_credit_assignment WHERE credit_id = ?`, credit)
	f.exec(`DELETE FROM author_entity_assignment WHERE source_fingerprint = ? AND ordinal = 0`, fingerprint(32, 0x71))
	f.drain()
	assert.Equal(t, [][]string{{}}, f.entityIDsOf(book))
}

// A book shown from its legacy line links to no entity: its rows are not
// the layer's.
func TestALegacyLineLinksToNoEntity(t *testing.T) {
	f := entitySchema(t)
	book, snap := f.entityBook("Старое")
	f.legacyAuthorOf(book, "Сидоров Пётр Ильич")
	f.entityCredit(snap, 0x72, "Петров", "Иван", "Сергеевич")
	f.pass()
	f.drain()
	rows := f.modelRows(book)
	require.NotEmpty(t, rows)
	require.Equal(t, "legacy", rows[0].Source)
	for _, ids := range f.entityIDsOf(book) {
		assert.Empty(t, ids)
	}
}

// Every write of an assignment marks the books it touches: an insert, an
// update and a delete of a fingerprint row, and of a credit row.
func TestEveryAssignmentWriteMarksItsBooks(t *testing.T) {
	f := entitySchema(t)
	book, snap := f.entityBook("Мост")
	credit := f.entityCredit(snap, 0x73, "Петров", "Иван", "Сергеевич")
	id, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, e := range []uuid.UUID{id, other} {
		f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
			VALUES (?, 'person', 'Иван Петров', 'Петров', 'иван петров')`, e)
	}
	steps := []struct {
		name, query string
		args        []interface{}
	}{
		{"fingerprint insert", `INSERT INTO author_entity_assignment (source_fingerprint, extractor_version, entity_id, method, rule)
			VALUES (?, 'extractor-v1', ?, 'deterministic', 'D1')`, []interface{}{fingerprint(32, 0x73), id}},
		{"fingerprint update", `UPDATE author_entity_assignment SET entity_id = ? WHERE source_fingerprint = ?`,
			[]interface{}{other, fingerprint(32, 0x73)}},
		{"fingerprint delete", `DELETE FROM author_entity_assignment WHERE source_fingerprint = ?`,
			[]interface{}{fingerprint(32, 0x73)}},
		{"credit insert", `INSERT INTO author_entity_credit_assignment (credit_id, entity_id, method, rule)
			VALUES (?, ?, 'evidence', 'D3:title')`, []interface{}{credit, id}},
		{"credit update", `UPDATE author_entity_credit_assignment SET entity_id = ? WHERE credit_id = ?`,
			[]interface{}{other, credit}},
		{"credit delete", `DELETE FROM author_entity_credit_assignment WHERE credit_id = ?`, []interface{}{credit}},
	}
	for _, s := range steps {
		f.clearEntityMarks()
		f.exec(s.query, s.args...)
		assert.Equal(t, []int64{book}, distinctIDs(f.marks()), s.name)
	}
}

// A write that forgot its mark is caught by the walk: the row's entities
// are rebuilt and the walk counts the book as differing.
func TestTheWalkCatchesAMissedEntityMark(t *testing.T) {
	f := entitySchema(t)
	book, snap := f.entityBook("Мост")
	f.entityCredit(snap, 0x74, "Петров", "Иван", "Сергеевич")
	p, _, _ := f.pass()
	f.drain()
	want := f.entityIDsOf(book)
	require.NotEmpty(t, want[0])
	// A writer that bypasses the triggers: the row's link is lost, no mark.
	f.exec(`ALTER TABLE book_author_display DISABLE TRIGGER USER`)
	f.exec(`UPDATE book_author_display SET entity_ids = NULL WHERE book_id = ?`, book)
	f.exec(`ALTER TABLE book_author_display ENABLE TRIGGER USER`)
	f.clearEntityMarks()
	f.exec(`UPDATE book_author_display_reconcile SET walk_cursor = NULL, finished_at = NULL`)
	walked := false
	for range 1000 {
		step, err := ReconcileAuthorDisplayStep(context.Background(), f.tx, 100000, time.Hour)
		require.NoError(t, err)
		if step.Finished {
			walked = true
			assert.Positive(t, step.WalkDiffered)
			break
		}
	}
	require.True(t, walked)
	assert.Equal(t, want, f.entityIDsOf(book))
	_ = p
}

// A book the model refuses is skipped alone: the other books of the drain
// get their entities.
func TestABookTheModelRefusesDoesNotStopTheEntityLinks(t *testing.T) {
	f := entitySchema(t)
	bad, s1 := f.entityBook("Мост")
	good, s2 := f.entityBook("Река")
	f.entityCredit(s1, 0x75, "Петров", "Иван", "Сергеевич")
	f.entityCredit(s2, 0x76, "Сидоров", "Пётр", "Ильич")
	f.poison(bad)
	f.pass()
	d := f.drain()
	require.Len(t, d.Skipped, 1)
	assert.Equal(t, bad, d.Skipped[0].BookID)
	assert.NotEmpty(t, f.entityIDsOf(good)[0])
}

// The entity names count the rows that carry the entity — the row's own
// name and the nickname of its credit — and follow every rebuild: a name no
// row carries any more goes, so a retired entity ends with none; the walk
// recounts a drifted index.
func TestTheEntityNamesFollowTheRows(t *testing.T) {
	f := entitySchema(t)
	_, s1 := f.entityBook("Мост")
	_, s2 := f.entityBook("Река")
	c1 := f.entityCredit(s1, 0x77, "Петров", "Иван", "Сергеевич")
	f.entityCredit(s2, 0x77, "Петров", "Иван", "Сергеевич")
	f.exec(`ALTER TABLE contributor_normalization_result DISABLE TRIGGER USER`)
	f.exec(`UPDATE contributor_normalization_result SET nickname = 'Капитан' WHERE source_fingerprint = ?`, fingerprint(32, 0x77))
	f.exec(`ALTER TABLE contributor_normalization_result ENABLE TRIGGER USER`)
	p, _, _ := f.pass()
	f.drain()
	id := p.EntityOf(c1)
	assert.Equal(t, map[string]int{f.searchKey("Иван Сергеевич Петров"): 2, f.searchKey("Капитан"): 2}, f.entityNames(id))
	assert.Zero(t, f.count(`SELECT count(*) FROM author_entity_name n WHERE NOT EXISTS (
		SELECT 1 FROM book_author_display d WHERE n.entity_id = ANY (d.entity_ids))`),
		"names only for entities that rows carry")

	// The entity is retired: its fingerprint row goes, the rows lose it, and
	// so do the names.
	f.exec(`DELETE FROM author_entity_assignment WHERE source_fingerprint = ?`, fingerprint(32, 0x77))
	f.drain()
	assert.Empty(t, f.entityNames(id), "a retired entity ends with no names")

	// A drifted index is recounted by the walk.
	f.exec(`INSERT INTO author_entity_name (entity_id, search_key, books) VALUES (?, 'stray', 3)`, id)
	n, err := recountAuthorEntityNames(context.Background(), f.tx)
	require.NoError(t, err)
	assert.Positive(t, n)
	assert.Empty(t, f.entityNames(id))
}

// The entity search finds an entity by any of its names — a nickname too —
// ranks the closest first, counts only the books the list would show, and
// never returns an entity no row carries.
func TestTheEntitySearchFindsEntitiesByTheirNames(t *testing.T) {
	f := entitySchema(t)
	b1, s1 := f.entityBook("Мост")
	_, s2 := f.entityBook("Река")
	_, s3 := f.entityBook("Лес")
	c1 := f.entityCredit(s1, 0x78, "Толстойский", "Лев", "Николаевич")
	f.entityCredit(s2, 0x78, "Толстойский", "Лев", "Николаевич")
	other := f.entityCredit(s3, 0x79, "Толстойская", "Анна")
	f.exec(`ALTER TABLE contributor_normalization_result DISABLE TRIGGER USER`)
	f.exec(`UPDATE contributor_normalization_result SET nickname = 'Великий Старец' WHERE source_fingerprint = ?`,
		fingerprint(32, 0x78))
	f.exec(`ALTER TABLE contributor_normalization_result ENABLE TRIGGER USER`)
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	p, _, _ := f.pass()
	f.drain()
	ctx := context.Background()

	page, err := SearchAuthorEntities(ctx, f.tx, "Лев Толстойский", "", 10, 0)
	require.NoError(t, err)
	hits := page.Hits
	require.NotEmpty(t, hits)
	assert.Equal(t, p.EntityOf(c1), hits[0].ID)
	assert.Equal(t, 2, hits[0].Books)

	page, err = SearchAuthorEntities(ctx, f.tx, "Великий Старец", "", 10, 0)
	require.NoError(t, err)
	hits = page.Hits
	require.NotEmpty(t, hits)
	assert.Equal(t, p.EntityOf(c1), hits[0].ID, "found by the nickname")

	// A book the list hides is not counted; an entity left with none is not
	// offered at all.
	f.exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, b1)
	page, err = SearchAuthorEntities(ctx, f.tx, "Лев Толстойский", "", 10, 0)
	require.NoError(t, err)
	hits = page.Hits
	require.NotEmpty(t, hits)
	assert.Equal(t, 1, hits[0].Books)
	var annaBook int64
	_, err = f.tx.QueryOne(pg.Scan(&annaBook), `SELECT book_id FROM book_metadata_snapshot WHERE id = ?`, s3)
	require.NoError(t, err)
	f.exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, annaBook)
	page, err = SearchAuthorEntities(ctx, f.tx, "Анна Толстойская", "", 10, 0)
	require.NoError(t, err)
	hits = page.Hits
	for _, h := range hits {
		assert.NotEqual(t, p.EntityOf(other), h.ID, "an entity whose books are all hidden")
	}
	f.exec(`DELETE FROM author_entity_assignment WHERE source_fingerprint = ?`, fingerprint(32, 0x79))
	f.drain()
	page, err = SearchAuthorEntities(ctx, f.tx, "Анна Толстойская", "", 10, 0)
	require.NoError(t, err)
	hits = page.Hits
	for _, h := range hits {
		assert.NotEqual(t, p.EntityOf(other), h.ID, "an entity no row carries")
	}
}

// The entity search ranks first and counts only what the page needs, yet
// pages exactly as counting every candidate would: six entities tie on
// distance (one surname, six given names) with 1-6 books, two match farther,
// one holds only hidden books, one only books in another language and one
// seven books in the reader's language and three in another. Every page,
// also one that ends inside the tie or starts past it, is the slice of the
// full order, the books are those in the reader's language, and the total
// counts only the entities with a book the list shows.
func TestTheEntitySearchPagesAsIfItCountedEveryCandidate(t *testing.T) {
	f := entitySchema(t)
	var fill byte = 0x80
	entity := func(family, given string, books int) int64 {
		var credit int64
		for i := range books {
			_, s := f.entityBook(fmt.Sprintf("%s %s %d", family, given, i))
			credit = f.entityCredit(s, fill, family, given)
		}
		fill++
		return credit
	}
	tied := []int64{
		entity("Сидоровский", "Пётр", 3), entity("Сидоровский", "Иван", 6), entity("Сидоровский", "Олег", 1),
		entity("Сидоровский", "Глеб", 5), entity("Сидоровский", "Юрий", 2), entity("Сидоровский", "Лев", 4),
	}
	far := []int64{entity("Сидоровскийский", "Пётр", 2), entity("Сидоровскийский", "Иван", 1)}
	hidden := entity("Сидоровский", "Марк", 7)
	english := entity("Сидоровский", "Карл", 8)
	mixed := entity("Сидоровский", "Марат", 10)
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	p, _, _ := f.pass()
	f.drain()
	f.exec(`UPDATE opds_catalog_book b SET approved = false FROM book_author_display d
		WHERE d.book_id = b.id AND ? = ANY (d.entity_ids)`, p.EntityOf(hidden))
	f.exec(`UPDATE opds_catalog_book b SET lang = 'en' FROM book_author_display d
		WHERE d.book_id = b.id AND ? = ANY (d.entity_ids)`, p.EntityOf(english))
	f.exec(`UPDATE opds_catalog_book SET lang = 'en' WHERE id IN (SELECT b.id FROM opds_catalog_book b
		JOIN book_author_display d ON d.book_id = b.id AND ? = ANY (d.entity_ids) ORDER BY b.id LIMIT 3)`,
		p.EntityOf(mixed))

	// The tie orders by books in the reader's language: Марат 7, Иван 6,
	// Глеб 5, Лев 4, Пётр 3, Юрий 2, Олег 1; the farther pair by books after it.
	var want []uuid.UUID
	for _, c := range []int64{mixed, tied[1], tied[3], tied[5], tied[0], tied[4], tied[2], far[0], far[1]} {
		want = append(want, p.EntityOf(c))
	}
	ctx := context.Background()
	for _, pg := range []struct{ limit, offset int }{{20, 0}, {3, 0}, {2, 2}, {4, 4}, {2, 6}, {1, 7}, {5, 8}, {3, 20}} {
		page, err := SearchAuthorEntities(ctx, f.tx, "Сидоровский", "ru", pg.limit, pg.offset)
		require.NoError(t, err)
		assert.Equal(t, len(want), page.Total, "limit %d offset %d: the total", pg.limit, pg.offset)
		got := []uuid.UUID{}
		for _, h := range page.Hits {
			got = append(got, h.ID)
		}
		end := min(pg.offset+pg.limit, len(want))
		assert.Equal(t, want[min(pg.offset, end):end], got, "limit %d offset %d", pg.limit, pg.offset)
		if pg.offset == 0 {
			assert.Equal(t, 7, page.Hits[0].Books, "the reader's language only")
		}
	}
	page, err := SearchAuthorEntities(ctx, f.tx, "Сидоровский", "", 3, 0)
	require.NoError(t, err)
	assert.Equal(t, len(want)+1, page.Total, "without a language the other language's entity counts")
	require.NotEmpty(t, page.Hits)
	require.Len(t, page.Hits, 3)
	assert.Equal(t, p.EntityOf(mixed), page.Hits[0].ID)
	assert.Equal(t, 10, page.Hits[0].Books, "every language")
	assert.Equal(t, p.EntityOf(english), page.Hits[1].ID, "with its eight books")
	assert.Equal(t, 8, page.Hits[1].Books)
}

// A new book the scan adds with a credit of a fingerprint the build already
// assigned links to that entity as soon as its scan mark is drained — before
// any new build (design E, §4.3).
func TestANewBookOfAKnownFingerprintLinksAtOnce(t *testing.T) {
	f := entitySchema(t)
	_, s1 := f.entityBook("Мост")
	known := f.entityCredit(s1, 0x7a, "Петров", "Иван", "Сергеевич")
	p, _, _ := f.pass()
	f.drain()
	book, snap := f.entityBook("Новая книга")
	f.exec(`INSERT INTO book_contributor_credit (snapshot_id, role, position, source_first_name, source_last_name,
		source_display_name, source_fingerprint) VALUES (?, 'author', 0, 'Иван', 'Петров', 'Иван Петров', ?)`,
		snap, fingerprint(32, 0x7a))
	require.NoError(t, MarkAuthorDisplayDirty(context.Background(), f.tx, book))
	f.drain()
	assert.Equal(t, [][]string{{p.EntityOf(known).String()}}, f.entityIDsOf(book))
}

// Review E2 r1: a name's books are distinct books — a book whose two credits
// resolve to the same entity and name counts once.
func TestAnEntityNameCountsABookOnce(t *testing.T) {
	f := entitySchema(t)
	book, snap := f.entityBook("Двойная")
	first := f.entityCredit(snap, 0x7c, "Петров", "Иван", "Сергеевич")
	second := f.returningID(`INSERT INTO book_contributor_credit (snapshot_id, role, position, source_first_name,
		source_last_name, source_display_name, source_fingerprint) VALUES (?, 'author', 1, 'Иван', 'Петров',
		'Иван Сергеевич Петров', ?) RETURNING id`, snap, fingerprint(32, 0x7c))
	var result int64
	_, err := f.tx.QueryOne(pg.Scan(&result), `SELECT result_id FROM book_contributor_credit_selection WHERE credit_id = ?`, first)
	require.NoError(t, err)
	f.exec(selectionInsert, second, fingerprint(32, 0x7c), "selected", result, "automatic", nil, "4", nil, nil)
	p, _, _ := f.pass()
	f.drain()
	require.Len(t, f.entityIDsOf(book), 2)
	assert.Equal(t, map[string]int{f.searchKey("Иван Сергеевич Петров"): 1}, f.entityNames(p.EntityOf(first)))
	n, err := recountAuthorEntityNames(context.Background(), f.tx)
	require.NoError(t, err)
	assert.Zero(t, n, "the walk's recount agrees")
}
