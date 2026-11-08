package database

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopds-api/internal/bookvariants"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
	"github.com/google/uuid"
)

// The site's grouped lists: one entry per card (book_variant_member, migration
// 83) instead of one per file. OPDS and the Telegram bot never come here; they
// list files through GetBooks and the search, as they always did.
//
// The read model is only the grouping. What readers may see is read live on
// every request: a file that was hidden, unapproved or deleted after its card
// was built is never listed, never counted and never offered as a variant,
// whatever its stored row still says. A visible book with no row yet (before
// the backfill, or a partition the walk refused) is a card of its own.

// CardEntry is one card of a grouped page, index-aligned with the page's books:
// the file the card is shown on, what it says of its other files, and its
// stars counted over all of its files.
type CardEntry struct {
	BookID int64
	// Variants is set only for a card of more than one visible file.
	Variants *models.BookVariantsInfo
	// Fav says the caller starred any file of the card.
	Fav bool
	// FavoriteCount is the distinct readers who starred any file of it.
	FavoriteCount int
}

// cardsPageSQL pages the scope's cards. The scope (?) selects the visible
// books of the list with their position in its order (pos). A card sits at the
// first position any of its files has in the scope and shows its best-ranked
// file in the scope (the least rank, then position), so a favorites or
// collection list shows the saved file. One aggregate by card, no sort of the
// scope's rows by card: min over the array (rank, pos, id) picks the file.
const cardsPageSQL = `
WITH scoped AS (?),
members AS (
    SELECT s.id, s.pos, coalesce(m.group_key, md5('book:' || s.id)::uuid) AS g, coalesce(m.variant_rank, 1) AS r
    FROM scoped AS s LEFT JOIN book_variant_member AS m ON m.book_id = s.id),
cards AS (
    SELECT g, min(pos) AS gpos, min(ARRAY[r::bigint, pos::bigint, id::bigint]) AS pick
    FROM members GROUP BY g)
SELECT pick[3] AS shown_id, g::text AS group_key, pick[1] AS shown_rank, count(*) OVER () AS total
FROM cards ORDER BY gpos LIMIT ? OFFSET ?`

// cardsPopularSQL pages the cards of the books readers starred: the card
// starred by the most distinct readers over all of its visible files first,
// then the card with the newest visible file. Over all of them, not only the
// files in scope, so the order agrees with the card's favorite count.
const cardsPopularSQL = `
WITH scoped AS (?),
members AS (
    SELECT s.id, s.pos, coalesce(m.group_key, md5('book:' || s.id)::uuid) AS g, coalesce(m.variant_rank, 1) AS r
    FROM scoped AS s LEFT JOIN book_variant_member AS m ON m.book_id = s.id),
cards AS (
    SELECT g, min(ARRAY[r::bigint, pos::bigint, id::bigint]) AS pick
    FROM members GROUP BY g),
files AS (
    SELECT c.g, m.book_id FROM cards AS c JOIN book_variant_member AS m ON m.group_key = c.g
    UNION
    SELECT c.g, c.pick[3] FROM cards AS c),
popular AS (
    SELECT f.g, count(DISTINCT fb.user_id) AS readers, max(f.book_id) AS newest
    FROM files AS f
    JOIN opds_catalog_book AS b ON b.id = f.book_id AND b.approved AND NOT b.duplicate_hidden
    LEFT JOIN favorite_books AS fb ON fb.book_id = f.book_id
    GROUP BY f.g)
SELECT c.pick[3] AS shown_id, c.g::text AS group_key, c.pick[1] AS shown_rank, count(*) OVER () AS total
FROM cards AS c JOIN popular AS p ON p.g = c.g
ORDER BY p.readers DESC, p.newest DESC LIMIT ? OFFSET ?`

// cardsReplacementSQL finds, for cards whose shown file left the visible set
// after the page was selected, the next eligible file in scope: the
// best-ranked file of the card in scope, other than the ones gone. The scope
// is read again here, so it holds what is visible now; the read of the books
// that follows checks it once more.
const cardsReplacementSQL = `
WITH scoped AS (?)
SELECT DISTINCT ON (g) s.id AS shown_id, g::text AS group_key, r AS shown_rank
FROM scoped AS s
LEFT JOIN book_variant_member AS m ON m.book_id = s.id
CROSS JOIN LATERAL (SELECT coalesce(m.group_key, md5('book:' || s.id)::uuid) AS g,
    coalesce(m.variant_rank, 1) AS r) AS k
WHERE g = ANY (?::uuid[]) AND s.id <> ALL (?::bigint[])
ORDER BY g, r, s.pos, s.id`

// afterCardsSelected, when set (tests only), runs between selecting a page's
// files and reading them, where a concurrent hide can land.
var afterCardsSelected func()

type cardRow struct {
	ShownID   int64  `pg:"shown_id"`
	GroupKey  string `pg:"group_key"`
	ShownRank int    `pg:"shown_rank"`
	Total     int    `pg:"total"`
	// Mark and Newest are the feed's: the card's mark and its newest file,
	// which the mark is about. Fresh marks a book the read model had no row
	// for when the page was selected, listed as a card of its own.
	Mark   string `pg:"mark"`
	Newest int64  `pg:"newest"`
	Fresh  bool   `pg:"fresh"`
}

// nextFiles finds the next eligible file of each card named by keys, other
// than the gone files: the list's own idea of where a card may go next.
type nextFiles func(ctx context.Context, conn pg.DBI, keys []string, gone []int64) ([]cardRow, error)

// nextInScope is nextFiles for a scoped list: the card's best-ranked file in
// the scope, read again.
func nextInScope(scoped *orm.Query) nextFiles {
	return func(ctx context.Context, conn pg.DBI, keys []string, gone []int64) ([]cardRow, error) {
		var next []cardRow
		_, err := conn.QueryContext(ctx, &next, cardsReplacementSQL, scoped, pg.Array(keys), pg.Array(gone))
		return next, err
	}
}

// cardFilesSQL reads what a page's cards hold over their visible files: the
// files, the documents among them, the best visible rank, the readers who
// starred any of them and whether the caller is one. A card's files are the
// rows of its key and the shown book itself, which may have no row yet.
const cardFilesSQL = `
WITH page AS (SELECT unnest(?::uuid[]) AS g, unnest(?::bigint[]) AS shown),
files AS (
    SELECT p.g, m.book_id, m.lineage, m.variant_rank
    FROM page AS p JOIN book_variant_member AS m ON m.group_key = p.g
    UNION
    SELECT p.g, p.shown, coalesce(m.lineage, 'book:' || p.shown), coalesce(m.variant_rank, 1)
    FROM page AS p LEFT JOIN book_variant_member AS m ON m.book_id = p.shown AND m.group_key = p.g)
SELECT f.g::text AS group_key, count(DISTINCT f.book_id) AS files, count(DISTINCT f.lineage) AS documents,
    min(f.variant_rank) AS best_rank, count(DISTINCT fb.user_id) AS readers,
    coalesce(bool_or(fb.user_id = ?), false) AS fav
FROM files AS f
JOIN opds_catalog_book AS b ON b.id = f.book_id AND b.approved AND NOT b.duplicate_hidden
LEFT JOIN favorite_books AS fb ON fb.book_id = f.book_id
GROUP BY f.g`

type cardFilesRow struct {
	GroupKey  string `pg:"group_key"`
	Files     int    `pg:"files"`
	Documents int    `pg:"documents"`
	BestRank  int    `pg:"best_rank"`
	Readers   int    `pg:"readers"`
	Fav       bool   `pg:"fav"`
}

// savedScope says the list shows files a reader or a collection saved, so a
// card shown on another file than its default says why.
//
//nolint:gocritic // reads one filter set; the list path passes it by value throughout
func savedScope(filters models.BookFilters) bool {
	return filters.Fav || filters.Collection != 0 || filters.CuratedCollection != 0
}

// GetBookCards lists a scope the way GetBooks does, one entry per card: the
// same filters, visibility and order, with the total and the page counted in
// cards. It is the site's path only, and only for readers' visibility: the
// caller does not ask it for hidden or unapproved books.
//
//nolint:gocritic // reads one filter set; the list path passes it by value throughout
func GetBookCards(ctx context.Context, userID int64, filters models.BookFilters) ([]models.Book, []CardEntry, int, error) {
	if strings.TrimSpace(filters.Title) != "" {
		return nil, nil, 0, ErrTextSearchUnsupported
	}
	if filters.UnApproved || filters.IncludeHidden {
		return nil, nil, 0, errors.New("database: cards list only what readers see")
	}
	if filters.Limit > 100 || filters.Limit == 0 {
		filters.Limit = 100
	}
	var books []models.Book
	var entries []CardEntry
	var total int
	err := inSnapshot(ctx, func(conn pg.DBI) error {
		var err error
		if isFeed(filters) {
			books, entries, total, err = getFeedCards(ctx, conn, userID, filters)
		} else {
			books, entries, total, err = getScopedCards(ctx, conn, userID, filters)
		}
		return err
	})
	if err != nil {
		return nil, nil, 0, err
	}
	return books, entries, total, nil
}

// inSnapshot runs one grouped read in a read-only REPEATABLE READ
// transaction: every statement of it sees the catalog and the read model as
// of its first statement. A rebuild, a scan or a moderation change that
// commits while the request runs is invisible to it, so the page, the fold of
// fresh books, the replacements, the books and the cards' counts all agree.
// One connection per request, held for the request only.
func inSnapshot(ctx context.Context, fn func(conn pg.DBI) error) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY`); err != nil {
			return err
		}
		return fn(tx)
	})
}

// getScopedCards is GetBookCards for a scoped list, in the caller's snapshot.
//
//nolint:gocritic // reads one filter set; the list path passes it by value throughout
func getScopedCards(ctx context.Context, conn pg.DBI, userID int64, filters models.BookFilters) ([]models.Book, []CardEntry, int, error) {
	scoped, err := cardsScope(ctx, filters, userID)
	if err != nil {
		return nil, nil, 0, err
	}
	pageSQL := cardsPageSQL
	if filters.UsersFavorites {
		pageSQL = cardsPopularSQL
	}
	var rows []cardRow
	if _, err = conn.QueryContext(ctx, &rows, pageSQL, scoped, filters.Limit, filters.Offset); err != nil {
		return nil, nil, 0, err
	}
	if len(rows) == 0 {
		// The total rides on the page's rows; past the last page it is read
		// on its own, as a count of the scope's cards.
		total, countErr := countCards(ctx, conn, scoped, filters.Offset)
		return []models.Book{}, []CardEntry{}, total, countErr
	}
	if afterCardsSelected != nil {
		afterCardsSelected()
	}
	books, entries, err := cardEntries(ctx, conn, rows, nextInScope(scoped), savedScope(filters), userID)
	if err != nil {
		return nil, nil, 0, err
	}
	return books, entries, rows[0].Total, nil
}

// cardsScope is the scope's visible books with their position in the list's
// order, as the subquery the card queries read.
//
//nolint:gocritic // reads one filter set; the list path passes it by value throughout
func cardsScope(ctx context.Context, filters models.BookFilters, userID int64) (*orm.Query, error) {
	order, err := listOrder(filters, userID)
	if err != nil {
		return nil, err
	}
	scoped := db.ModelContext(ctx, (*models.Book)(nil)).ColumnExpr("book.id")
	if order.orderBy == newestFirst {
		// The position is the order itself: no window over the scope, which
		// lets the planner run the scope and its join in parallel.
		scoped = scoped.ColumnExpr("-book.id AS pos")
	} else {
		scoped = scoped.ColumnExpr("row_number() OVER (ORDER BY "+order.orderBy+") AS pos", order.orderArgs...)
	}
	scoped, err = applyScopeFilters(scoped, filters, userID, true)
	if err != nil {
		return nil, err
	}
	return order.apply(scoped), nil
}

// cardEntries loads the page's shown books and what each card holds over
// its visible files. A shown file that left the visible set after the page
// was selected gives way to its card's next eligible file in scope, or the
// card leaves the page. saved says the list shows saved files, so a card
// shown on a file other than its best visible one says why.
func cardEntries(
	ctx context.Context, conn pg.DBI, rows []cardRow, next nextFiles, saved bool, userID int64,
) ([]models.Book, []CardEntry, error) {
	books, rows, err := visibleCards(ctx, conn, rows, next, userID)
	if err != nil || len(rows) == 0 {
		return []models.Book{}, []CardEntry{}, err
	}
	keys := make([]string, len(rows))
	ids := make([]int64, len(rows))
	for i := range rows {
		keys[i], ids[i] = rows[i].GroupKey, rows[i].ShownID
	}
	var held []cardFilesRow
	if _, err = conn.QueryContext(ctx, &held, cardFilesSQL, pg.Array(keys), pg.Array(ids), userID); err != nil {
		return nil, nil, err
	}
	byKey := make(map[string]*cardFilesRow, len(held))
	for i := range held {
		byKey[held[i].GroupKey] = &held[i]
	}
	entries := make([]CardEntry, len(books))
	for i := range books {
		row := &rows[i]
		entries[i] = CardEntry{BookID: books[i].ID}
		if card := byKey[row.GroupKey]; card != nil {
			entries[i].Fav, entries[i].FavoriteCount = card.Fav, card.Readers
			entries[i].Variants = variantsInfo(row, card, saved)
		}
		books[i].Fav, books[i].FavoriteCount = entries[i].Fav, entries[i].FavoriteCount
	}
	return books, entries, nil
}

// maxReplacementRounds bounds how often a card's shown file is replaced
// within one request: every round needs another file of the card to vanish
// between two reads, so a few rounds cover any real interleaving, and the
// bound keeps a request from chasing a card that keeps disappearing.
const maxReplacementRounds = 8

// visibleCards reads the page's shown books, visible ones only, and returns
// them with their rows, index-aligned and in page order. A card whose shown
// file is gone is shown on its next eligible file in scope, and that one is
// read in turn; this repeats, every gone file excluded, until each card has a
// visible file or its scope has none left (or the rounds run out). A card
// with no file left is dropped.
func visibleCards(
	ctx context.Context, conn pg.DBI, rows []cardRow, next nextFiles, userID int64,
) ([]models.Book, []cardRow, error) {
	current := slices.Clone(rows)
	ids := make([]int64, len(current))
	for i := range current {
		ids[i] = current[i].ShownID
	}
	books, err := hydrateBooks(ctx, conn, ids, userID)
	if err != nil {
		return nil, nil, err
	}
	found := make(map[int64]*models.Book, len(books))
	for i := range books {
		found[books[i].ID] = &books[i]
	}
	var gone []int64
	exhausted := map[string]bool{}
	for round := 0; round < maxReplacementRounds; round++ {
		more, err := replaceGoneFiles(ctx, conn, current, found, &gone, exhausted, next, userID)
		if err != nil {
			return nil, nil, err
		}
		if !more {
			break
		}
	}
	outBooks := make([]models.Book, 0, len(current))
	outRows := make([]cardRow, 0, len(current))
	listed := make(map[int64]bool, len(current))
	for i := range current {
		// One file, one entry: a later card that still ended on a file the
		// page lists already (two moves racing the request) leaves the page.
		if book := found[current[i].ShownID]; book != nil && !listed[book.ID] {
			listed[book.ID] = true
			outBooks = append(outBooks, *book)
			outRows = append(outRows, current[i])
		}
	}
	return outBooks, outRows, nil
}

// replaceGoneFiles runs one round of replacement: every card whose shown file
// is not found, and whose scope is not known to be exhausted, gets its next
// eligible file, excluding every file seen gone so far (gone grows). It says
// whether there was anything to replace.
func replaceGoneFiles(
	ctx context.Context, conn pg.DBI, current []cardRow, found map[int64]*models.Book, gone *[]int64,
	exhausted map[string]bool, next nextFiles, userID int64,
) (bool, error) {
	var keys []string
	for i := range current {
		if found[current[i].ShownID] == nil && !exhausted[current[i].GroupKey] {
			keys = append(keys, current[i].GroupKey)
			*gone = append(*gone, current[i].ShownID)
		}
	}
	if len(keys) == 0 {
		return false, nil
	}
	// Never a file the page already shows on another card: a file that moved
	// between cards mid-request would otherwise be listed twice.
	exclude := slices.Clone(*gone)
	for i := range current {
		if found[current[i].ShownID] != nil {
			exclude = append(exclude, current[i].ShownID)
		}
	}
	replaced, err := nextCardFiles(ctx, conn, keys, exclude, found, next, userID)
	if err != nil {
		return false, err
	}
	for i := range current {
		if found[current[i].ShownID] != nil || exhausted[current[i].GroupKey] {
			continue
		}
		next := replaced[current[i].GroupKey]
		if next == nil {
			exhausted[current[i].GroupKey] = true
			continue
		}
		current[i].ShownID, current[i].ShownRank = next.ShownID, next.ShownRank
	}
	return true, nil
}

// afterCardsReplaced, when set (tests only), runs between finding the next
// files of cards and reading them, where another concurrent hide can land.
var afterCardsReplaced func()

// nextCardFiles finds the next eligible file in scope of each of the cards
// named by keys, other than the gone files, reads the visible ones into
// found, and returns the files found by card key.
func nextCardFiles(
	ctx context.Context, conn pg.DBI, keys []string, gone []int64, found map[int64]*models.Book, find nextFiles, userID int64,
) (map[string]*cardRow, error) {
	next, err := find(ctx, conn, keys, gone)
	if err != nil {
		return nil, err
	}
	replaced := make(map[string]*cardRow, len(next))
	nextIDs := make([]int64, len(next))
	for i := range next {
		nextIDs[i] = next[i].ShownID
		replaced[next[i].GroupKey] = &next[i]
	}
	if len(nextIDs) == 0 {
		return replaced, nil
	}
	if afterCardsReplaced != nil {
		afterCardsReplaced()
	}
	more, err := hydrateBooks(ctx, conn, nextIDs, userID)
	if err != nil {
		return nil, err
	}
	for i := range more {
		found[more[i].ID] = &more[i]
	}
	return replaced, nil
}

// variantsInfo is what a card says of its files, nil for a single file.
func variantsInfo(row *cardRow, card *cardFilesRow, saved bool) *models.BookVariantsInfo {
	if card.Files <= 1 {
		return nil
	}
	kind, count := models.VariantsKind(card.Files, card.Documents)
	info := &models.BookVariantsInfo{Key: uuid.MustParse(row.GroupKey), Count: count, Kind: kind}
	switch {
	case row.Mark != "" && row.ShownID == row.Newest:
		// The feed's new file, shown: the mark is about it.
		info.Reason = row.Mark
	case saved && row.ShownRank > card.BestRank:
		info.Reason = models.BookVariantReasonSaved
	}
	return info
}

// countCards counts the cards of a scope, for a page past its end.
func countCards(ctx context.Context, conn pg.DBI, scoped *orm.Query, offset int) (int, error) {
	if offset == 0 {
		return 0, nil
	}
	var total int
	_, err := conn.QueryOneContext(ctx, pg.Scan(&total), `
		SELECT count(DISTINCT coalesce(m.group_key, md5('book:' || s.id)::uuid))
		FROM (?) AS s LEFT JOIN book_variant_member AS m ON m.book_id = s.id`, scoped)
	return total, err
}

// hydrateBooks loads the books of a page the way the list shows them, in the
// order of ids, with the caller's stars and the series numbers. It reads only
// what readers see: a book that is gone, hidden or unapproved by now is left
// out, whatever the queries before it selected.
func hydrateBooks(ctx context.Context, conn pg.DBI, ids []int64, userID int64) ([]models.Book, error) {
	var books []models.Book
	if err := conn.ModelContext(ctx, &books).
		Relation("Authors").
		Relation("Users").
		Relation("Series").
		Relation("Genres").
		ColumnExpr("book.*").
		Where("book.id IN (?)", pg.In(ids)).
		Where("book.approved AND NOT book.duplicate_hidden").
		Select(); err != nil {
		return nil, err
	}
	books = orderByIDs(ids, books)
	var favs []int64
	if _, err := conn.QueryContext(ctx, &favs, `SELECT book_id FROM favorite_books WHERE user_id = ? AND book_id IN (?)`,
		userID, pg.In(ids)); err != nil {
		return nil, err
	}
	for i := range books {
		books[i].Fav = isFav(favs, books[i])
	}
	populateSeriesNumbersWithDB(conn, books)
	return books, nil
}

// bookVariantsSQL reads the visible files of a card by rank, with the
// document version of each file's current snapshot.
const bookVariantsSQL = `
SELECT m.book_id, m.lineage, m.latest, m.variant_rank, m.tag, coalesce(s.source_document_version, '') AS doc_version
FROM book_variant_member AS m
JOIN opds_catalog_book AS b ON b.id = m.book_id AND b.approved AND NOT b.duplicate_hidden
LEFT JOIN book_metadata_snapshot AS s ON s.book_id = m.book_id AND s.is_current
WHERE m.group_key = ?
ORDER BY m.variant_rank, m.book_id`

type bookVariantRow struct {
	BookID     int64  `pg:"book_id"`
	Lineage    string `pg:"lineage"`
	Latest     bool   `pg:"latest"`
	Rank       int    `pg:"variant_rank"`
	Tag        string `pg:"tag"`
	DocVersion string `pg:"doc_version"`
}

// BookVariantsOf returns the visible files of the book's card by rank, and
// each file's place in it, index-aligned; the first is the default. A book
// readers cannot see, at the start or by the time its files are read, is
// pg.ErrNoRows; a visible book with no row yet is a card of one.
func BookVariantsOf(ctx context.Context, bookID, userID int64) ([]models.Book, []models.BookVariant, error) {
	var books []models.Book
	var variants []models.BookVariant
	err := inSnapshot(ctx, func(conn pg.DBI) error {
		var err error
		books, variants, err = bookVariantsOf(ctx, conn, bookID, userID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return books, variants, nil
}

// bookVariantsOf is BookVariantsOf in the caller's snapshot.
func bookVariantsOf(ctx context.Context, conn pg.DBI, bookID, userID int64) ([]models.Book, []models.BookVariant, error) {
	var key string
	if _, err := conn.QueryOneContext(ctx, pg.Scan(&key), `
		SELECT coalesce(m.group_key, md5('book:' || b.id)::uuid)::text
		FROM opds_catalog_book AS b LEFT JOIN book_variant_member AS m ON m.book_id = b.id
		WHERE b.id = ? AND b.approved AND NOT b.duplicate_hidden`, bookID); err != nil {
		return nil, nil, err
	}
	groupKey, err := uuid.Parse(key)
	if err != nil {
		return nil, nil, err
	}
	var rows []bookVariantRow
	if _, err = conn.QueryContext(ctx, &rows, bookVariantsSQL, key); err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		// No row yet: the book alone, as the lists show it.
		var version string
		if _, err = conn.QueryContext(ctx, pg.Scan(&version), `SELECT coalesce(source_document_version, '')
			FROM book_metadata_snapshot WHERE book_id = ? AND is_current`, bookID); err != nil {
			return nil, nil, err
		}
		rows = []bookVariantRow{{BookID: bookID, Lineage: "book:" + fmt.Sprint(bookID), Latest: true, Rank: 1,
			DocVersion: version}}
		groupKey = bookvariants.SoloKey(bookID)
	}
	if afterCardsSelected != nil {
		afterCardsSelected()
	}
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = rows[i].BookID
	}
	// Only what is still visible: a file hidden since the rows were read
	// drops out, and the asked-for book gone means the book is not there.
	books, err := hydrateBooks(ctx, conn, ids, userID)
	if err != nil {
		return nil, nil, err
	}
	if !slices.ContainsFunc(books, func(b models.Book) bool { return b.ID == bookID }) {
		return nil, nil, pg.ErrNoRows
	}
	at := make(map[int64]*bookVariantRow, len(rows))
	for i := range rows {
		at[rows[i].BookID] = &rows[i]
	}
	variants := make([]models.BookVariant, len(books))
	for i := range books {
		row := at[books[i].ID]
		variants[i] = models.BookVariant{BookID: row.BookID, GroupKey: groupKey, Lineage: row.Lineage,
			Latest: row.Latest, Rank: row.Rank, Tag: row.Tag, DocVersion: row.DocVersion, Default: i == 0}
	}
	return books, variants, nil
}

// UnfavBookGroup removes the reader's stars from every file of the book's
// card, the book itself included, and says whether any star is left. An
// unknown book is an error, as for FavBook.
func UnfavBookGroup(userID, bookID int64) (bool, error) {
	book := &models.Book{ID: bookID}
	if err := db.Model(book).Column("id").WherePK().Select(); err != nil {
		return false, err
	}
	if _, err := db.Exec(`
		DELETE FROM favorite_books
		WHERE user_id = ? AND book_id IN (
			SELECT ?::bigint
			UNION
			SELECT other.book_id FROM book_variant_member AS m
			JOIN book_variant_member AS other ON other.group_key = m.group_key
			WHERE m.book_id = ?)`, userID, bookID, bookID); err != nil {
		return false, fmt.Errorf("cannot_unfav: %w", err)
	}
	return HaveFavs(userID)
}
