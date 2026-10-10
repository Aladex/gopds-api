package database

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// authorDisplayRow is one row of the read model book_author_display: one
// name of a book's author line, in line order (migration 30).
type authorDisplayRow struct {
	BookID         int64
	Position       int
	Display        string
	SortKey        string
	Source         string
	LegacyAuthorID *int64
	CreditID       *int64
	// ExtendsLegacy: a name from the layer, linked to a legacy author, with a
	// word that author's catalog name lacks.
	ExtendsLegacy bool
}

// authorDisplayRows is the read model of one book: its author line as
// BookAuthorDisplay decides it, a row per name, each with its sort key.
func authorDisplayRows(bookID int64, s *bookAuthorSources) []authorDisplayRow {
	line := resolveAuthorDisplay(s)
	rows := make([]authorDisplayRow, len(line.Authors))
	for i, a := range line.Authors {
		row := authorDisplayRow{
			BookID: bookID, Position: i, Display: a.Name,
			Source: string(line.Source), LegacyAuthorID: a.LegacyAuthorID,
		}
		if line.Source == models.AuthorDisplayLayer {
			c := s.credits[i]
			id := c.ID
			row.CreditID = &id
			row.SortKey = foldSortKey(creditSortKey(&c, a.Name))
			if a.LegacyAuthorID != nil {
				row.ExtendsLegacy = !wordsWithin(nameWords(a.Name), nameWords(legacyNameOf(s.legacy, *a.LegacyAuthorID)))
			}
		} else {
			row.SortKey = foldSortKey(a.Name)
		}
		rows[i] = row
	}
	return rows
}

func legacyNameOf(legacy []legacyAuthor, id int64) string {
	for _, a := range legacy {
		if a.ID == id {
			return a.Name
		}
	}
	return ""
}

// modelRowsOf is the read model of one book: its line's rows, or — for a book
// whose line names no one — a single row that names no one; nothing for a
// book that is gone.
func modelRowsOf(bookID int64, s *bookAuthorSources, exists bool) []authorDisplayRow {
	if !exists {
		return nil
	}
	if rows := authorDisplayRows(bookID, s); len(rows) > 0 {
		return rows
	}
	return []authorDisplayRow{{BookID: bookID, Source: authorDisplaySourceNone}}
}

// authorDisplaySourceNone is the source of the row of a book whose line names
// no one: no name, no sort key, no search key worth a match.
const authorDisplaySourceNone = "none"

// creditSortKey is what a credit sorts by: the selected result's sort name;
// else "last, first middle" when the file gives a last name; else the name
// shown.
func creditSortKey(c *creditName, shown string) string {
	if c.Selected && strings.TrimSpace(c.SortName) != "" {
		return c.SortName
	}
	last := strings.TrimSpace(c.Last)
	if last == "" {
		return shown
	}
	given := strings.Join(strings.Fields(c.First+" "+c.Middle), " ")
	if given == "" {
		return last
	}
	return last + ", " + given
}

// foldSortKey makes sort keys compare without case and with ё read as е,
// the way every author comparison here does.
func foldSortKey(key string) string {
	key = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "ё", "е")
	return strings.Join(strings.Fields(key), " ")
}

// authorDisplayModelLockKey serializes every rebuild of the read model, one
// transaction at a time (pg_advisory_xact_lock). A rebuild from marks and a
// rebuild by the walk then never interleave: whichever runs later reads the
// layer later, and a change committed after a walk read it keeps its mark in
// the queue — the walk takes no marks — so it is rebuilt again.
const authorDisplayModelLockKey int64 = 0x617574686f72_6d // "author" + "m"

func lockAuthorDisplayModel(ctx context.Context, tx pg.DBI) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, authorDisplayModelLockKey)
	return err
}

// MarkAuthorDisplayDirty queues books whose author line may have changed, in
// the caller's transaction: the mark commits with the change or not at all.
func MarkAuthorDisplayDirty(ctx context.Context, conn pg.DBI, bookIDs ...int64) error {
	if len(bookIDs) == 0 {
		return nil
	}
	_, err := conn.ExecContext(ctx, `INSERT INTO book_author_display_dirty (book_id)
		SELECT unnest(?::bigint[])`, pg.Array(bookIDs))
	return err
}

// creditMarks gathers the credits a write changed, to mark them for the read
// model in one statement when the write is done: a resolution of thousands
// of credits pays one INSERT for its marks, and one that changed nothing
// pays none. The worker finds each credit's book when it drains the mark.
type creditMarks []int64

func (m *creditMarks) add(ids ...int64) { *m = append(*m, ids...) }

func (m creditMarks) flush(ctx context.Context, conn pg.DBI) error {
	if len(m) == 0 {
		return nil
	}
	_, err := conn.ExecContext(ctx, `INSERT INTO book_author_display_dirty (credit_id)
		SELECT unnest(?::bigint[])`, pg.Array([]int64(m)))
	return err
}

// storedAuthorDisplayRow is a read-model row as stored, with whether its
// search key still follows from its name.
type storedAuthorDisplayRow struct {
	BookID         int64  `pg:"book_id"`
	Position       int    `pg:"position"`
	Display        string `pg:"display"`
	SortKey        string `pg:"sort_key"`
	Source         string `pg:"source"`
	LegacyAuthorID *int64 `pg:"legacy_author_id"`
	CreditID       *int64 `pg:"credit_id"`
	ExtendsLegacy  bool   `pg:"extends_legacy"`
	KeyFresh       bool   `pg:"key_fresh"`
}

func (s *storedAuthorDisplayRow) matches(r *authorDisplayRow) bool {
	return s.KeyFresh && s.Position == r.Position && s.Display == r.Display && s.SortKey == r.SortKey &&
		s.Source == r.Source && equalID(s.LegacyAuthorID, r.LegacyAuthorID) && equalID(s.CreditID, r.CreditID) &&
		s.ExtendsLegacy == r.ExtendsLegacy
}

func equalID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// deleteAuthorDisplaySQL deletes the books' rows and takes the names among
// them that extend their legacy author off the name index's counts; it
// returns the legacy authors whose counts it lowered.
const deleteAuthorDisplaySQL = `WITH gone AS (
	DELETE FROM book_author_display WHERE book_id IN (?)
	RETURNING legacy_author_id, search_key, extends_legacy
), names AS (
	SELECT legacy_author_id, search_key, count(*)::int AS books FROM gone WHERE extends_legacy GROUP BY 1, 2
)
UPDATE book_author_display_name n SET books = n.books - names.books
FROM names WHERE n.legacy_author_id = names.legacy_author_id AND n.search_key = names.search_key
RETURNING n.legacy_author_id`

// insertAuthorDisplaySQL writes rows column by column; 0 stands for a NULL
// ID, which no legacy author or credit has, and an empty sort key for the
// NULL of a line that names no one. The search key is computed here,
// by the normalization every search uses, and the names among the rows that
// extend their legacy author are counted into the name index.
const insertAuthorDisplaySQL = `WITH added AS (
	INSERT INTO book_author_display
		(book_id, position, display, sort_key, search_key, source, legacy_author_id, credit_id, extends_legacy)
	SELECT b, p, d, nullif(s, ''), public.search_normalize(d), src, nullif(l, 0), nullif(c, 0), e
	FROM unnest(?::bigint[], ?::smallint[], ?::text[], ?::text[], ?::text[], ?::bigint[], ?::bigint[], ?::boolean[])
		AS t(b, p, d, s, src, l, c, e)
	RETURNING legacy_author_id, search_key, extends_legacy
)
INSERT INTO book_author_display_name (legacy_author_id, search_key, books)
SELECT legacy_author_id, search_key, count(*) FROM added WHERE extends_legacy GROUP BY 1, 2
ON CONFLICT (legacy_author_id, search_key) DO UPDATE
SET books = book_author_display_name.books + EXCLUDED.books`

// RebuildAuthorDisplay brings the read model of the books in line with their
// author lines and returns how many books it had to rewrite. A book whose
// rows already match is not written; a book that no longer exists loses its
// rows. It must run inside a transaction, and takes the model's lock there.
func RebuildAuthorDisplay(ctx context.Context, tx pg.DBI, bookIDs []int64) (int, error) {
	if len(bookIDs) == 0 {
		return 0, nil
	}
	if err := lockAuthorDisplayModel(ctx, tx); err != nil {
		return 0, err
	}
	sources, err := loadBookAuthorSources(ctx, tx, bookIDs)
	if err != nil {
		return 0, err
	}
	exists, err := existingBooks(ctx, tx, bookIDs)
	if err != nil {
		return 0, err
	}
	have, err := storedAuthorDisplay(ctx, tx, bookIDs)
	if err != nil {
		return 0, err
	}

	var w authorDisplayWrite
	for _, id := range bookIDs {
		want := modelRowsOf(id, sources[id], exists[id])
		if sameAuthorDisplayRows(have[id], want) {
			continue
		}
		w.changed = append(w.changed, id)
		w.cols.add(want)
	}
	if applyErr := w.apply(ctx, tx); applyErr != nil {
		return 0, applyErr
	}
	return len(w.changed), nil
}

// existingBooks tells which of the books are in the catalog.
func existingBooks(ctx context.Context, tx pg.DBI, bookIDs []int64) (map[int64]bool, error) {
	var existing []int64
	if _, err := tx.QueryContext(ctx, &existing, `SELECT id FROM opds_catalog_book WHERE id IN (?)`,
		pg.In(bookIDs)); err != nil {
		return nil, err
	}
	exists := make(map[int64]bool, len(existing))
	for _, id := range existing {
		exists[id] = true
	}
	return exists, nil
}

// storedAuthorDisplay reads the books' rows as the model holds them now.
func storedAuthorDisplay(ctx context.Context, tx pg.DBI, bookIDs []int64) (map[int64][]storedAuthorDisplayRow, error) {
	var stored []storedAuthorDisplayRow
	if _, err := tx.QueryContext(ctx, &stored, `SELECT book_id, position, display,
			coalesce(sort_key, '') AS sort_key, source, legacy_author_id, credit_id, extends_legacy,
			search_key = public.search_normalize(display) AS key_fresh
		FROM book_author_display WHERE book_id IN (?) ORDER BY book_id, position`, pg.In(bookIDs)); err != nil {
		return nil, err
	}
	have := make(map[int64][]storedAuthorDisplayRow, len(bookIDs))
	for i := range stored {
		have[stored[i].BookID] = append(have[stored[i].BookID], stored[i])
	}
	return have, nil
}

// authorDisplayWrite is the rewrite of the books whose rows differ: their new
// rows.
type authorDisplayWrite struct {
	changed []int64
	cols    authorDisplayColumns
}

func (w *authorDisplayWrite) apply(ctx context.Context, tx pg.DBI) error {
	if len(w.changed) == 0 {
		return nil
	}
	var lowered []int64
	if _, err := tx.QueryContext(ctx, &lowered, deleteAuthorDisplaySQL, pg.In(w.changed)); err != nil {
		return err
	}
	if c := &w.cols; len(c.book) > 0 {
		if _, err := tx.ExecContext(ctx, insertAuthorDisplaySQL, pg.Array(c.book), pg.Array(c.position),
			pg.Array(c.display), pg.Array(c.sortKey), pg.Array(c.source),
			pg.Array(c.legacy), pg.Array(c.credit), pg.Array(c.extends)); err != nil {
			return err
		}
	}
	return dropUncarriedNames(ctx, tx, lowered)
}

// dropUncarriedNames removes from the name index the names of these legacy
// authors that no book carries any more.
func dropUncarriedNames(ctx context.Context, tx pg.DBI, authors []int64) error {
	if len(authors) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM book_author_display_name
		WHERE books = 0 AND legacy_author_id IN (?)`, pg.In(distinctIDs(authors)))
	return err
}

// deleteAuthorDisplayOf takes deleted books out of the read model inside the
// caller's transaction: their rows, and from the name index the names only
// they carried. It holds the model's lock, like every rebuild, so a rebuild
// either finishes before the deletion or reads the books after it — gone —
// and never writes rows or an alias back for them.
func deleteAuthorDisplayOf(ctx context.Context, tx pg.DBI, bookIDs []int64) error {
	if len(bookIDs) == 0 {
		return nil
	}
	if err := lockAuthorDisplayModel(ctx, tx); err != nil {
		return err
	}
	var lowered []int64
	if _, err := tx.QueryContext(ctx, &lowered, deleteAuthorDisplaySQL, pg.In(bookIDs)); err != nil {
		return err
	}
	return dropUncarriedNames(ctx, tx, lowered)
}

func sameAuthorDisplayRows(have []storedAuthorDisplayRow, want []authorDisplayRow) bool {
	if len(have) != len(want) {
		return false
	}
	for i := range want {
		if !have[i].matches(&want[i]) {
			return false
		}
	}
	return true
}

// authorDisplayColumns gathers rows column by column for one INSERT.
type authorDisplayColumns struct {
	book, legacy, credit     []int64
	position                 []int
	display, sortKey, source []string
	extends                  []bool
}

func (c *authorDisplayColumns) add(rows []authorDisplayRow) {
	for i := range rows {
		r := &rows[i]
		c.book = append(c.book, r.BookID)
		c.position = append(c.position, r.Position)
		c.display = append(c.display, r.Display)
		c.sortKey = append(c.sortKey, r.SortKey)
		c.source = append(c.source, r.Source)
		c.legacy = append(c.legacy, idOrZero(r.LegacyAuthorID))
		c.credit = append(c.credit, idOrZero(r.CreditID))
		c.extends = append(c.extends, r.ExtendsLegacy)
	}
}

func idOrZero(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// AuthorDisplayDrain is what one drain of the mark queue did: the marks it
// took, the distinct books they named, and how many of those it rewrote.
type AuthorDisplayDrain struct {
	Marks, Books, Differed int
}

// DrainAuthorDisplayMarks takes the oldest marks, at most limit, and
// rebuilds the books they name, inside the caller's transaction: the marks
// are gone exactly when the rebuild commits. Marks another drain holds are
// skipped, never waited for.
func DrainAuthorDisplayMarks(ctx context.Context, tx pg.DBI, limit int) (AuthorDisplayDrain, error) {
	if err := lockAuthorDisplayModel(ctx, tx); err != nil {
		return AuthorDisplayDrain{}, err
	}
	// A mark names a book, or a credit whose book is looked up here; a
	// credit gone with its book names nothing left to rebuild.
	// (0 stands for such a credit: no book has it as its ID.)
	var marked []int64
	if _, err := tx.QueryContext(ctx, &marked, `DELETE FROM book_author_display_dirty d
		WHERE d.id IN (SELECT id FROM book_author_display_dirty ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED)
		RETURNING coalesce(d.book_id, (SELECT s.book_id FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id WHERE c.id = d.credit_id), 0)`, limit); err != nil {
		return AuthorDisplayDrain{}, err
	}
	named := make([]int64, 0, len(marked))
	for _, id := range marked {
		if id != 0 {
			named = append(named, id)
		}
	}
	books := distinctIDs(named)
	differed, err := RebuildAuthorDisplay(ctx, tx, books)
	if err != nil {
		return AuthorDisplayDrain{}, err
	}
	return AuthorDisplayDrain{Marks: len(marked), Books: len(books), Differed: differed}, nil
}

func distinctIDs(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

// recountAuthorDisplayNames brings the name index in line with the model —
// every name that extends its legacy author, with its number of books — and
// returns how many index rows it had to correct.
func recountAuthorDisplayNames(ctx context.Context, tx pg.DBI) (int, error) {
	const want = `SELECT legacy_author_id, search_key, count(*)::int AS books
		FROM book_author_display WHERE extends_legacy GROUP BY 1, 2`
	stray, err := tx.ExecContext(ctx, `WITH want AS (`+want+`)
		DELETE FROM book_author_display_name n
		WHERE NOT EXISTS (SELECT 1 FROM want w
			WHERE w.legacy_author_id = n.legacy_author_id AND w.search_key = n.search_key)`)
	if err != nil {
		return 0, err
	}
	fixed, err := tx.ExecContext(ctx, `INSERT INTO book_author_display_name (legacy_author_id, search_key, books)
		`+want+`
		ON CONFLICT (legacy_author_id, search_key) DO UPDATE SET books = EXCLUDED.books
		WHERE book_author_display_name.books <> EXCLUDED.books`)
	if err != nil {
		return 0, err
	}
	return stray.RowsAffected() + fixed.RowsAffected(), nil
}

// AuthorDisplayReconcileStep is what one step of the daily walk did. Ran is
// false when no walk was due; Started marks the step that began a walk and
// Finished the one that ended it, with the walk's totals.
type AuthorDisplayReconcileStep struct {
	Ran, Started, Finished bool
	Books, Differed        int
	WalkRead, WalkDiffered int64
	// NamesDiffered is how many rows of the name index the finished walk
	// corrected.
	NamesDiffered int
}

// reconcileRow is the walk's one row.
type reconcileRow struct {
	WalkCursor   *int64 `pg:"walk_cursor"`
	WalkDiffered int64  `pg:"walk_differed"`
	WalkRead     int64  `pg:"walk_read"`
	Due          bool   `pg:"due"`
}

// ReconcileAuthorDisplayStep advances the walk that rebuilds every book by
// one batch, inside the caller's transaction. A walk starts when none runs
// and none finished within every; the first one fills the model. Each step
// rebuilds the next batch of books in ID order and drops rows whose book is
// gone; the last records when the walk ended, how many books it read and how
// many it found different — zero on a model every writer kept marked.
func ReconcileAuthorDisplayStep(ctx context.Context, tx pg.DBI, batch int, every time.Duration) (AuthorDisplayReconcileStep, error) {
	var out AuthorDisplayReconcileStep
	var state reconcileRow
	if _, err := tx.QueryOneContext(ctx, &state, `SELECT walk_cursor, walk_differed, walk_read,
			walk_cursor IS NULL AND (finished_at IS NULL OR finished_at <= now() - ?::interval) AS due
		FROM book_author_display_reconcile FOR UPDATE`, every.String()); err != nil {
		return out, err
	}
	if state.WalkCursor == nil {
		if !state.Due {
			return out, nil
		}
		zero := int64(0)
		state = reconcileRow{WalkCursor: &zero}
		out.Started = true
	}
	out.Ran = true
	cursor := *state.WalkCursor

	var ids []int64
	if _, err := tx.QueryContext(ctx, &ids, `SELECT id FROM opds_catalog_book WHERE id > ? ORDER BY id LIMIT ?`,
		cursor, batch); err != nil {
		return out, err
	}
	out.Finished = len(ids) < batch
	// Rows of books that are gone, between the cursor and the batch's end —
	// or beyond the cursor at all once the catalog is read to its end.
	upper := int64(math.MaxInt64)
	if !out.Finished {
		upper = ids[len(ids)-1]
	}
	var gone []int64
	if _, err := tx.QueryContext(ctx, &gone, `SELECT DISTINCT d.book_id FROM book_author_display d
		WHERE d.book_id > ? AND d.book_id <= ?
			AND NOT EXISTS (SELECT 1 FROM opds_catalog_book b WHERE b.id = d.book_id)`, cursor, upper); err != nil {
		return out, err
	}
	differed, err := RebuildAuthorDisplay(ctx, tx, append(slices.Clone(ids), gone...))
	if err != nil {
		return out, err
	}
	out.Books, out.Differed = len(ids), differed
	out.WalkRead, out.WalkDiffered = state.WalkRead+int64(len(ids)), state.WalkDiffered+int64(differed)

	if out.Finished {
		if out.NamesDiffered, err = recountAuthorDisplayNames(ctx, tx); err != nil {
			return out, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE book_author_display_reconcile SET walk_cursor = NULL,
			walk_started = NULL, walk_differed = 0, walk_read = 0, finished_at = now(),
			books_differed = ?, books_read = ?, names_differed = ?`, out.WalkDiffered, out.WalkRead, out.NamesDiffered)
		return out, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE book_author_display_reconcile SET walk_cursor = ?,
		walk_started = CASE WHEN ? THEN now() ELSE walk_started END, walk_differed = ?, walk_read = ?`,
		upper, out.Started, out.WalkDiffered, out.WalkRead)
	return out, err
}
