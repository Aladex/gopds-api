package database

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopds-api/internal/bookvariants"

	"github.com/go-pg/pg/v10"
)

// The book variants read model (migration 83): which files of the catalog are
// one card on the site, and how the files inside a card rank. The rule is
// internal/bookvariants; this keeps what it decided.
//
// The unit of a rebuild is a partition: the books of one entity in one
// language. Every book of a base key has the base's first entity, so the
// partition of that entity holds the whole card, and a rebuild of it sees
// every file it ranks. A book that is a card of its own has no partition.
//
// Who keeps it. Nothing here runs on its own: AuthorDisplayModelLoop calls it
// when the read model has nothing to do, a small batch and a rest at a time.
//   - The author display drain marks every book it drains, which is how a new
//     scan, a rescan of the authors, an admin edit, a cover, a hide or approve
//     and an author entity build get here.
//   - A rescan, a series edit, a duplicate hide and a deletion mark their books
//     directly: none of them reaches the author display.
//   - The daily walk is the net, and its first pass is the backfill.
//
// One book, one transaction, as the author summary does it (see
// author_entity_summary.go): a refused book is passed and logged by its ID,
// never retried forever.

// variantBooksSQL reads books with what the rule reads of them; the caller
// appends the WHERE clause. visible is what readers see: only those group.
const variantBooksSQL = `
SELECT b.id, coalesce(b.lang, '') AS lang, coalesce(b.title, '') AS title, b.registerdate,
    coalesce(b.docdate, '') AS docdate, coalesce(b.cover, false) AS cover, coalesce(b.annotation, '') <> '' AS annotation,
    coalesce(s.source_document_id, '') AS document_id, coalesce(s.source_document_version, '') AS doc_version,
    coalesce(trim(s.source_publisher), '') <> '' AS publisher, coalesce(trim(s.source_year), '') <> '' AS year,
    (SELECT array_agg(DISTINCT x::text ORDER BY x::text)
        FROM book_author_display AS d, unnest(d.entity_ids) AS x WHERE d.book_id = b.id) AS entity_ids,
    EXISTS (SELECT 1 FROM book_author_display AS d
        WHERE d.book_id = b.id AND coalesce(cardinality(d.entity_ids), 0) = 0) AS unlinked,
    (SELECT array_agg(e.display_name) FROM author_entity AS e
        WHERE e.id IN (SELECT unnest(d.entity_ids) FROM book_author_display AS d WHERE d.book_id = b.id)) AS entity_names,
    (SELECT array_agg(bs.ser_id::text || ':' || bs.ser_no::text)
        FROM opds_catalog_bseries AS bs WHERE bs.book_id = b.id AND bs.ser_no > 0) AS series,
    coalesce(b.approved, false) AND NOT coalesce(b.duplicate_hidden, false) AS visible
FROM opds_catalog_book AS b
LEFT JOIN book_metadata_snapshot AS s ON s.book_id = b.id AND s.is_current
`

// variantBookRow is one row of variantBooksSQL.
type variantBookRow struct {
	ID           int64     `pg:"id"`
	Lang         string    `pg:"lang"`
	Title        string    `pg:"title"`
	RegisterDate time.Time `pg:"registerdate"`
	DocDate      string    `pg:"docdate"`
	Cover        bool      `pg:"cover"`
	Annotation   bool      `pg:"annotation"`
	DocumentID   string    `pg:"document_id"`
	DocVersion   string    `pg:"doc_version"`
	Publisher    bool      `pg:"publisher"`
	Year         bool      `pg:"year"`
	EntityIDs    []string  `pg:"entity_ids,array"`
	Unlinked     bool      `pg:"unlinked"`
	EntityNames  []string  `pg:"entity_names,array"`
	Series       []string  `pg:"series,array"`
	Visible      bool      `pg:"visible"`
}

// input is the row as the rule reads it: numbered series only, entity IDs
// distinct and sorted.
func (r *variantBookRow) input() bookvariants.Input {
	ids := slices.Clone(r.EntityIDs)
	slices.Sort(ids)
	in := bookvariants.Input{
		BookID: r.ID, Lang: r.Lang, Title: r.Title, EntityIDs: slices.Compact(ids), Unlinked: r.Unlinked,
		EntityNames: r.EntityNames, Series: map[int64]int64{}, DocumentID: r.DocumentID,
		DocVersion: r.DocVersion, DocDate: r.DocDate, RegisterDate: r.RegisterDate,
		Cover: r.Cover, Annotation: r.Annotation, Publisher: r.Publisher, Year: r.Year,
	}
	for _, s := range r.Series {
		series, no, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		id, err1 := strconv.ParseInt(series, 10, 64)
		n, err2 := strconv.ParseInt(no, 10, 64)
		if err1 == nil && err2 == nil && n > 0 {
			in.Series[id] = n
		}
	}
	return in
}

// variantPartition is the entity and the language a base is built in.
type variantPartition struct {
	Entity string
	Lang   string
}

// partitionOf is the partition of a book with a base key: its first entity
// and its language as the base spells it.
func partitionOf(in *bookvariants.Input) variantPartition {
	return variantPartition{Entity: in.EntityIDs[0], Lang: strings.ToLower(strings.TrimSpace(in.Lang))}
}

// storedVariantMember is what a rebuild reads of a book's stored row.
type storedVariantMember struct {
	BookID     int64   `pg:"book_id"`
	GroupKey   string  `pg:"group_key"`
	BaseKey    string  `pg:"base_key"`
	PartEntity *string `pg:"part_entity"`
	PartLang   *string `pg:"part_lang"`
}

func (m *storedVariantMember) partition() (variantPartition, bool) {
	if m.PartEntity == nil || m.PartLang == nil {
		return variantPartition{}, false
	}
	return variantPartition{Entity: *m.PartEntity, Lang: *m.PartLang}, true
}

// rebuildBookVariants brings the card of one book in line with the catalog:
// the partition the book is in now and the one its stored row names, or its
// own card. A book that is gone or hidden loses its row. walk is stamped on
// every row written.
func rebuildBookVariants(ctx context.Context, tx pg.DBI, bookID, walk int64) error {
	var rows []variantBookRow
	if _, err := tx.QueryContext(ctx, &rows, variantBooksSQL+`WHERE b.id = ?`, bookID); err != nil {
		return fmt.Errorf("reading book %d: %w", bookID, err)
	}
	var stored []storedVariantMember
	if _, err := tx.QueryContext(ctx, &stored, `SELECT book_id, group_key::text, base_key, part_entity::text, part_lang
		FROM book_variant_member WHERE book_id = ?`, bookID); err != nil {
		return fmt.Errorf("reading the card of book %d: %w", bookID, err)
	}

	parts, solo := bookVariantTargets(rows, stored)
	// The partitions first: they take the book's old row out of its card and
	// the card's key with it when the book was its last file.
	for _, p := range parts {
		if err := rebuildVariantPartition(ctx, tx, p, bookID, walk); err != nil {
			return err
		}
	}
	if solo != nil {
		if err := writeSoloVariant(ctx, tx, solo, walk); err != nil {
			return err
		}
	}
	if len(stored) == 0 {
		return nil
	}
	var gone []int64
	if solo == nil && len(parts) == 0 {
		// Gone or hidden, and a card of its own: nothing else holds its row.
		gone = []int64{bookID}
	}
	// The card the stored row named goes when no row names it any more.
	return deleteVariantMembers(ctx, tx, gone, []string{stored[0].GroupKey})
}

// bookVariantTargets is what a rebuild of a book takes: the partition it is
// in now and the one its stored row names, or the book itself when it is a
// card of its own. A book that is gone or hidden is in no partition.
func bookVariantTargets(rows []variantBookRow, stored []storedVariantMember) ([]variantPartition, *bookvariants.Input) {
	var parts []variantPartition
	var solo *bookvariants.Input
	if len(rows) == 1 && rows[0].Visible {
		in := rows[0].input()
		if bookvariants.BaseKey(&in) != "" {
			parts = append(parts, partitionOf(&in))
		} else {
			solo = &in
		}
	}
	if len(stored) == 1 {
		if p, ok := stored[0].partition(); ok && !slices.Contains(parts, p) {
			parts = append(parts, p)
		}
	}
	return parts, solo
}

// variantPartitionLockClass namespaces the partition locks
// (pg_advisory_xact_lock(class, key)): a drain and a walk on two pods never
// rebuild one partition at once.
const variantPartitionLockClass int32 = 0x6276 // "bv"

// variantPartitionBooksSQL are the visible books of a partition, found through
// the GIN index on entity_ids that the author summary reads too. The largest
// entity of the catalog has about 1,700 books.
const variantPartitionBooksSQL = variantBooksSQL + `
WHERE b.id = ANY (ARRAY(SELECT d.book_id FROM book_author_display AS d WHERE d.entity_ids @> ARRAY[?::uuid]))
    AND b.approved AND NOT b.duplicate_hidden AND lower(trim(coalesce(b.lang, ''))) = ?`

// rebuildVariantPartition rebuilds every card of one partition: it reads the
// partition's visible books, keeps those whose base belongs to it, builds the
// cards and writes what changed. A stored row the partition no longer holds is
// deleted, and its book marked when it still exists, so its new card is built
// by the next drain; self is the book the caller rebuilds itself, not marked.
func rebuildVariantPartition(ctx context.Context, tx pg.DBI, p variantPartition, self, walk int64) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?, hashtext(?))`,
		variantPartitionLockClass, p.Entity+"\x1f"+p.Lang); err != nil {
		return err
	}
	var rows []variantBookRow
	if _, err := tx.QueryContext(ctx, &rows, variantPartitionBooksSQL, p.Entity, p.Lang); err != nil {
		return fmt.Errorf("reading partition %s/%s: %w", p.Entity, p.Lang, err)
	}
	groups := partitionGroups(rows, p)

	var stored []storedVariantMember
	if _, err := tx.QueryContext(ctx, &stored, `SELECT book_id, group_key::text FROM book_variant_member
		WHERE part_entity = ?::uuid AND part_lang = ?`, p.Entity, p.Lang); err != nil {
		return fmt.Errorf("reading the cards of partition %s/%s: %w", p.Entity, p.Lang, err)
	}
	left, oldKeys := leftVariantMembers(stored, groups)
	if err := deleteVariantMembers(ctx, tx, left, oldKeys); err != nil {
		return err
	}
	if len(left) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO book_variant_dirty (book_id)
			SELECT b.id FROM opds_catalog_book AS b WHERE b.id = ANY (?::bigint[]) AND b.id <> ?
			ON CONFLICT (book_id) DO NOTHING`, pg.Array(left), self); err != nil {
			return fmt.Errorf("marking the books partition %s/%s let go: %w", p.Entity, p.Lang, err)
		}
	}
	return writeVariantGroups(ctx, tx, groups, &p, p.Lang, walk)
}

// partitionGroups builds the cards of the books whose base belongs to the
// partition.
func partitionGroups(rows []variantBookRow, p variantPartition) []bookvariants.Group {
	bases := map[string][]bookvariants.Input{}
	for i := range rows {
		in := rows[i].input()
		base := bookvariants.BaseKey(&in)
		if base == "" || partitionOf(&in) != p {
			continue
		}
		bases[base] = append(bases[base], in)
	}
	var groups []bookvariants.Group
	for base, books := range bases {
		groups = append(groups, bookvariants.Build(base, books)...)
	}
	return groups
}

// leftVariantMembers are the stored books the built cards no longer hold, and
// the stored card keys they no longer have.
func leftVariantMembers(stored []storedVariantMember, groups []bookvariants.Group) (left []int64, oldKeys []string) {
	built := map[int64]bool{}
	keys := map[string]bool{}
	for i := range groups {
		keys[groups[i].Key.String()] = true
		for _, m := range groups[i].Members {
			built[m.BookID] = true
		}
	}
	for _, m := range stored {
		if !built[m.BookID] {
			left = append(left, m.BookID)
		}
		if !keys[m.GroupKey] {
			oldKeys = append(oldKeys, m.GroupKey)
		}
	}
	return left, oldKeys
}

// writeSoloVariant writes a book that is a card of its own.
func writeSoloVariant(ctx context.Context, tx pg.DBI, in *bookvariants.Input, walk int64) error {
	return writeVariantGroups(ctx, tx, bookvariants.Build("", []bookvariants.Input{*in}), nil,
		strings.ToLower(strings.TrimSpace(in.Lang)), walk)
}

// deleteVariantMembers deletes the rows of books and the cards whose keys no
// row names any more.
func deleteVariantMembers(ctx context.Context, tx pg.DBI, books []int64, keys []string) error {
	if len(books) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM book_variant_member WHERE book_id = ANY (?::bigint[])`,
			pg.Array(books)); err != nil {
			return fmt.Errorf("deleting %d card rows: %w", len(books), err)
		}
	}
	if len(keys) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM book_variant_group AS g
			WHERE g.group_key = ANY (?::uuid[])
				AND NOT EXISTS (SELECT 1 FROM book_variant_member AS m WHERE m.group_key = g.group_key)`,
			pg.Array(keys)); err != nil {
			return fmt.Errorf("deleting %d cards: %w", len(keys), err)
		}
	}
	return nil
}

// writeVariantMembersSQL writes members column by column, only those that
// differ or that this walk has not stamped yet.
const writeVariantMembersSQL = `
INSERT INTO book_variant_member AS m
    (book_id, group_key, base_key, part_entity, part_lang, lineage, latest, variant_rank, tag, walk)
SELECT b, g::uuid, base, ?::uuid, ?::text, l, lt, r, tg, ?::bigint
FROM unnest(?::bigint[], ?::text[], ?::text[], ?::text[], ?::boolean[], ?::integer[], ?::text[])
    AS t(b, g, base, l, lt, r, tg)
ON CONFLICT (book_id) DO UPDATE
SET group_key = EXCLUDED.group_key, base_key = EXCLUDED.base_key, part_entity = EXCLUDED.part_entity,
    part_lang = EXCLUDED.part_lang, lineage = EXCLUDED.lineage, latest = EXCLUDED.latest,
    variant_rank = EXCLUDED.variant_rank, tag = EXCLUDED.tag, walk = greatest(m.walk, EXCLUDED.walk)
WHERE (m.group_key, m.base_key, m.part_entity, m.part_lang, m.lineage, m.latest, m.variant_rank, m.tag)
        IS DISTINCT FROM (EXCLUDED.group_key, EXCLUDED.base_key, EXCLUDED.part_entity, EXCLUDED.part_lang,
            EXCLUDED.lineage, EXCLUDED.latest, EXCLUDED.variant_rank, EXCLUDED.tag)
    OR m.walk < EXCLUDED.walk`

const writeVariantGroupsSQL = `
INSERT INTO book_variant_group AS g (group_key, lang, files, kind, shown_count, newest_book_id, mark)
SELECT k::uuid, l, f, kd, c, n, mk
FROM unnest(?::text[], ?::text[], ?::integer[], ?::text[], ?::integer[], ?::bigint[], ?::text[])
    AS t(k, l, f, kd, c, n, mk)
ON CONFLICT (group_key) DO UPDATE
SET lang = EXCLUDED.lang, files = EXCLUDED.files, kind = EXCLUDED.kind, shown_count = EXCLUDED.shown_count,
    newest_book_id = EXCLUDED.newest_book_id, mark = EXCLUDED.mark
WHERE (g.lang, g.files, g.kind, g.shown_count, g.newest_book_id, g.mark)
    IS DISTINCT FROM (EXCLUDED.lang, EXCLUDED.files, EXCLUDED.kind, EXCLUDED.shown_count,
        EXCLUDED.newest_book_id, EXCLUDED.mark)`

// writeVariantGroups writes cards and their members. p is the partition they
// were built in, nil for a card of its own; lang is the cards' language.
func writeVariantGroups(
	ctx context.Context, tx pg.DBI, groups []bookvariants.Group, p *variantPartition, lang string, walk int64,
) error {
	if len(groups) == 0 {
		return nil
	}
	var m variantRows
	var g variantGroupRows
	for i := range groups {
		grp := &groups[i]
		key := grp.Key.String()
		for _, member := range grp.Members {
			m.books = append(m.books, member.BookID)
			m.keys = append(m.keys, key)
			m.bases = append(m.bases, grp.Base)
			m.lineages = append(m.lineages, member.Lineage)
			m.latest = append(m.latest, member.Latest)
			m.ranks = append(m.ranks, member.VariantRank)
			m.tags = append(m.tags, member.Tag)
		}
		g.keys = append(g.keys, key)
		g.langs = append(g.langs, lang)
		g.files = append(g.files, len(grp.Members))
		g.kinds = append(g.kinds, string(grp.Kind))
		g.counts = append(g.counts, grp.Count)
		g.newest = append(g.newest, grp.NewestBookID)
		g.marks = append(g.marks, string(grp.Mark))
	}
	var partEntity, partLang *string
	if p != nil {
		partEntity, partLang = &p.Entity, &p.Lang
	}
	if _, err := tx.ExecContext(ctx, writeVariantMembersSQL, partEntity, partLang, walk,
		pg.Array(m.books), pg.Array(m.keys), pg.Array(m.bases), pg.Array(m.lineages), pg.Array(m.latest),
		pg.Array(m.ranks), pg.Array(m.tags)); err != nil {
		return fmt.Errorf("writing %d card rows: %w", len(m.books), err)
	}
	if _, err := tx.ExecContext(ctx, writeVariantGroupsSQL, pg.Array(g.keys), pg.Array(g.langs), pg.Array(g.files),
		pg.Array(g.kinds), pg.Array(g.counts), pg.Array(g.newest), pg.Array(g.marks)); err != nil {
		return fmt.Errorf("writing %d cards: %w", len(g.keys), err)
	}
	return nil
}

// variantRows are member rows column by column.
type variantRows struct {
	books                 []int64
	keys, bases, lineages []string
	latest                []bool
	ranks                 []int
	tags                  []string
}

// variantGroupRows are card rows column by column.
type variantGroupRows struct {
	keys, langs, kinds, marks []string
	files, counts             []int
	newest                    []int64
}

// MarkBookVariantsDirty queues books whose card may have changed, in the
// caller's transaction: the mark commits with the change or not at all.
func MarkBookVariantsDirty(ctx context.Context, conn pg.DBI, bookIDs ...int64) error {
	if len(bookIDs) == 0 {
		return nil
	}
	_, err := conn.ExecContext(ctx, `INSERT INTO book_variant_dirty (book_id)
		SELECT DISTINCT unnest(?::bigint[]) ON CONFLICT (book_id) DO NOTHING`, pg.Array(bookIDs))
	return err
}

// BookVariantSkip is a book the read model refused and the SQLSTATE it was
// refused with.
type BookVariantSkip struct {
	BookID   int64
	SQLState string
}

// BookVariantWork is what a step of the read model did.
type BookVariantWork struct {
	// Books is how many books the step took.
	Books   int
	Skipped []BookVariantSkip
}

// variantMark is a mark as the drain took it; Generation is the row's xmin,
// as summaryMark explains.
type variantMark struct {
	BookID     int64  `pg:"book_id"`
	Generation string `pg:"generation"`
}

// currentVariantWalk is the walk a rebuild stamps.
func currentVariantWalk(ctx context.Context, tx pg.DBI) (int64, error) {
	var walk int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&walk), `SELECT walk_number FROM book_variant_walk`); err != nil {
		return 0, fmt.Errorf("reading the variant walk: %w", err)
	}
	return walk, nil
}

// DrainBookVariantMarks rebuilds the oldest marked books, at most limit, each
// in its own transaction: a mark is gone exactly when its rebuild commits, or
// when the book was refused and passed. Marks another pod holds are skipped,
// never waited for. A lost connection or a canceled context ends the batch;
// what already committed is reported with the error.
func DrainBookVariantMarks(
	ctx context.Context, db AuthorSummaryDB, limit int, timeout time.Duration,
) (BookVariantWork, error) {
	var out BookVariantWork
	for out.Books < limit {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var taken []variantMark
		err := inSummaryTx(ctx, db, timeout, func(tx *pg.Tx) error {
			if _, err := tx.QueryContext(ctx, &taken, `DELETE FROM book_variant_dirty d
				WHERE d.book_id IN (SELECT book_id FROM book_variant_dirty ORDER BY marked_at LIMIT 1 FOR UPDATE SKIP LOCKED)
				RETURNING d.book_id, d.xmin::text AS generation`); err != nil {
				return fmt.Errorf("taking a variant mark: %w", err)
			}
			if len(taken) == 0 {
				return nil
			}
			walk, err := currentVariantWalk(ctx, tx)
			if err != nil {
				return err
			}
			return rebuildBookVariants(ctx, tx, taken[0].BookID, walk)
		})
		if len(taken) == 0 {
			return out, err
		}
		id := taken[0].BookID
		out.Books++
		if err == nil {
			continue
		}
		if !summaryEntityRefused(ctx, err) {
			return out, fmt.Errorf("rebuilding the card of book %d: %w", id, err)
		}
		if passErr := passVariantMark(ctx, db, timeout, taken[0]); passErr != nil {
			return out, passErr
		}
		out.Skipped = append(out.Skipped, BookVariantSkip{BookID: id, SQLState: sqlState(err)})
	}
	return out, nil
}

// passVariantMark takes the mark of a refused book, which the rollback put
// back, and only that one, as passSummaryMark does.
func passVariantMark(ctx context.Context, db AuthorSummaryDB, timeout time.Duration, mark variantMark) error {
	err := inSummaryTx(ctx, db, timeout, func(tx *pg.Tx) error {
		_, execErr := tx.ExecContext(ctx, `DELETE FROM book_variant_dirty WHERE book_id = ? AND xmin = ?::xid`,
			mark.BookID, mark.Generation)
		return execErr
	})
	if err != nil {
		return fmt.Errorf("passing the variant mark of book %d: %w", mark.BookID, err)
	}
	return nil
}

// BookVariantWalkResult is what one step of the walk did. Ran is false when
// no walk was due, or another pod holds it; Finished marks the last step, with
// the walk's totals.
type BookVariantWalkResult struct {
	BookVariantWork
	Ran, Finished bool
	Read, Refused int64
}

// variantWalkRow is the walk's one row.
type variantWalkRow struct {
	Number  int64  `pg:"walk_number,use_zero"`
	Cursor  *int64 `pg:"walk_cursor"`
	Read    int64  `pg:"walk_read,use_zero"`
	Skipped int64  `pg:"walk_skipped,use_zero"`
	Due     bool   `pg:"due,use_zero"`
}

// variantWalkWindow is how many IDs one look for the walk's next book covers.
// Past the cursor most books are stamped already — a rebuild stamps its whole
// partition — so an open-ended look reads all of them, every book; a window
// bounds that, and the walk as a whole reads each ID about once.
const variantWalkWindow = 5000

// nextVariantBookSQL is the first book in the window (?0, ?2] that this walk
// has not rebuilt yet: a book of the catalog without a row of this walk — a
// rebuild stamps every row of the partition it took, so the rest of a card is
// passed — or a row of this walk's predecessors whose book is gone. The window
// is repeated on the rows: a range does not carry across the join.
const nextVariantBookSQL = `
SELECT n.id
FROM (
    (SELECT b.id FROM opds_catalog_book AS b
        WHERE b.id > ?0 AND b.id <= ?2
            AND NOT EXISTS (SELECT 1 FROM book_variant_member AS m
                WHERE m.book_id = b.id AND m.book_id > ?0 AND m.book_id <= ?2 AND m.walk = ?1)
        ORDER BY b.id LIMIT 1)
    UNION ALL
    (SELECT m.book_id FROM book_variant_member AS m
        WHERE m.book_id > ?0 AND m.book_id <= ?2 AND m.walk <> ?1
        ORDER BY m.book_id LIMIT 1)
) AS n(id)
ORDER BY n.id
LIMIT 1`

// nextVariantBook looks for the walk's next book one window at a time, up to
// the last ID of the catalog or of the rows; false means the walk is through.
func nextVariantBook(ctx context.Context, tx *pg.Tx, cursor, walk int64) (next int64, found bool, err error) {
	var last int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&last), `SELECT greatest(
			(SELECT max(id) FROM opds_catalog_book), (SELECT max(book_id) FROM book_variant_member), 0)`); err != nil {
		return 0, false, fmt.Errorf("reading the walk's last book: %w", err)
	}
	for from := cursor; from < last; from += variantWalkWindow {
		var ids []int64
		if _, err := tx.QueryContext(ctx, &ids, nextVariantBookSQL, from, walk, from+variantWalkWindow); err != nil {
			return 0, false, fmt.Errorf("reading the walk's next book: %w", err)
		}
		if len(ids) > 0 {
			return ids[0], true, nil
		}
	}
	return 0, false, nil
}

// BookVariantWalkStep advances the walk over every book by at most batch
// books, each in its own transaction with the cursor: a restart goes on from
// the last one committed, and a refused book is passed once a walk. A walk
// starts when none runs and none finished within every, which makes the first
// one start by itself after the migration: that one is the backfill. It
// finishes in the step that finds nothing past the cursor.
func BookVariantWalkStep(
	ctx context.Context, db AuthorSummaryDB, batch int, every, timeout time.Duration,
) (BookVariantWalkResult, error) {
	var out BookVariantWalkResult
	for out.Books < batch {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var from, id *int64
		err := inSummaryTx(ctx, db, timeout, func(tx *pg.Tx) (err error) {
			from, id, err = walkOneVariantBook(ctx, tx, every, &out)
			return err
		})
		if id == nil {
			return out, err
		}
		out.Books++
		if err == nil {
			continue
		}
		if !summaryEntityRefused(ctx, err) {
			return out, fmt.Errorf("walking the card of book %d: %w", *id, err)
		}
		if passErr := passVariantWalkBook(ctx, db, timeout, *from, *id); passErr != nil {
			return out, passErr
		}
		out.Skipped = append(out.Skipped, BookVariantSkip{BookID: *id, SQLState: sqlState(err)})
	}
	return out, nil
}

// walkOneVariantBook moves the walk by one book inside tx, and finishes it
// when there is none left. It returns where the walk stood and the book it
// took, nil with no walk due, with another pod on the walk, or with the walk
// finished; a failure after that is the book's.
func walkOneVariantBook(
	ctx context.Context, tx *pg.Tx, every time.Duration, out *BookVariantWalkResult,
) (from, id *int64, err error) {
	var rows []variantWalkRow
	if _, err := tx.QueryContext(ctx, &rows, `SELECT walk_number, walk_cursor, walk_read, walk_skipped,
			walk_cursor IS NULL AND (finished_at IS NULL OR finished_at <= now() - ?::interval) AS due
		FROM book_variant_walk FOR UPDATE SKIP LOCKED`, every.String()); err != nil {
		return nil, nil, fmt.Errorf("reading the variant walk: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}
	state := rows[0]
	if state.Cursor == nil {
		if !state.Due {
			return nil, nil, nil
		}
		zero := int64(0)
		state.Cursor, state.Read, state.Skipped = &zero, 0, 0
	}
	out.Ran = true

	next, found, lookErr := nextVariantBook(ctx, tx, *state.Cursor, state.Number)
	if lookErr != nil {
		return nil, nil, lookErr
	}
	if !found {
		if _, err := tx.ExecContext(ctx, `UPDATE book_variant_walk
			SET walk_cursor = NULL, walk_read = 0, walk_skipped = 0, walk_number = walk_number + 1,
				finished_at = now()`); err != nil {
			return nil, nil, fmt.Errorf("finishing the variant walk: %w", err)
		}
		out.Finished, out.Read, out.Refused = true, state.Read, state.Skipped
		return nil, nil, nil
	}
	if err := rebuildBookVariants(ctx, tx, next, state.Number); err != nil {
		return state.Cursor, &next, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE book_variant_walk SET walk_cursor = ?, walk_read = ?`,
		next, state.Read+1); err != nil {
		return state.Cursor, &next, fmt.Errorf("moving the variant walk: %w", err)
	}
	return state.Cursor, &next, nil
}

// passVariantWalkBook moves the cursor past a refused book, from where the
// failed transaction left it, as passSummaryWalkEntity does.
func passVariantWalkBook(ctx context.Context, db AuthorSummaryDB, timeout time.Duration, from, id int64) error {
	err := inSummaryTx(ctx, db, timeout, func(tx *pg.Tx) error {
		_, execErr := tx.ExecContext(ctx, `UPDATE book_variant_walk
			SET walk_cursor = ?, walk_read = walk_read + 1, walk_skipped = walk_skipped + 1
			WHERE coalesce(walk_cursor, 0) = ?`, id, from)
		return execErr
	})
	if err != nil {
		return fmt.Errorf("passing book %d in the variant walk: %w", id, err)
	}
	return nil
}

// BookVariantsReady says whether the first walk has finished: until then the
// read model is partial and the site must not read cards from it.
func BookVariantsReady(ctx context.Context, db pg.DBI) (bool, error) {
	var ready bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&ready), `SELECT finished_at IS NOT NULL FROM book_variant_walk`)
	if errors.Is(err, pg.ErrNoRows) {
		return false, nil
	}
	return ready, err
}

// markBookVariantsOfBooks marks drained books for the variants read model,
// whatever the author display rebuild found: the mark may be about a title, a
// language, a cover or a visibility that the line does not show.
func markBookVariantsOfBooks(ctx context.Context, tx pg.DBI, books []int64) error {
	return MarkBookVariantsDirty(ctx, tx, books...)
}
