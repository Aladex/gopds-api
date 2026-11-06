package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The author summary (migration 81): what the author search shows of each
// author beside its name, precomputed per entity and language so a page reads
// it by primary key.
//
// Who keeps it. Nothing here runs on its own: AuthorDisplayModelLoop calls it
// when the read model has nothing to do, a small batch and a rest at a time.
//   - A write of the read model marks the entities on both sides of the write
//     (deleteAuthorDisplaySQL, insertAuthorDisplaySQL), which is how a merge, a
//     retirement and a new book's entity get here: all of them reach the read
//     model as a changed assignment, whatever started them.
//   - A drained book marks its entities though its line did not change: the
//     mark of a rescan or an admin edit may be about its title or its cover.
//   - The daily walk is the net, and its first pass is the backfill.
//
// What leaves. A merged entity, a retired one and one with no listed book left
// lose their rows at their next rebuild; a language that lost its last book
// loses its row there too.
//
// One entity, one transaction. The worker's connection does not survive a
// statement timeout: go-pg calls SQLSTATE 57014 a bad connection, so nothing
// more can be said on it, a savepoint's rollback included. An entity therefore
// commits alone, with its mark or its cursor, and the worst it can cost is
// itself. It holds a dirty row for one entity's statements, not a batch's.
//
// Covers. The book's cover flag decides. It matched the disk on all 400 books
// drawn at random, and the search card draws a letter for an image that fails
// to load, so this background work never asks the cover store.

// authorSummaryLangAll is the variant of all languages, the value the search's
// language filter uses for no filter.
const authorSummaryLangAll = ""

// What one summary holds.
const (
	maxSummaryGenres = 3
	maxSummaryWorks  = 4
	maxSummaryCovers = 3
	// maxSummaryNames keeps a page's read bounded: it ranks these per row.
	maxSummaryNames = 24
	// ownAuthorsLimit is how many authors a book may have and still say
	// something of each of them.
	ownAuthorsLimit = 2
)

// authorSummaryBook is one listed book of an entity, as authorSummaryBooksSQL
// reads it.
type authorSummaryBook struct {
	ID           int64     `pg:"id"`
	Title        string    `pg:"title"`
	Lang         string    `pg:"lang"`
	Path         string    `pg:"path"`
	Filename     string    `pg:"filename"`
	Cover        bool      `pg:"cover,use_zero"`
	RegisterDate time.Time `pg:"registerdate"`
	// Authors is how many people the book's line names: a credit of several
	// entities is one row and counts each of them.
	Authors   int `pg:"authors,use_zero"`
	Favorites int `pg:"favorites,use_zero"`
	// Collected: a genre says this is a magazine or an anthology.
	Collected   bool     `pg:"collected,use_zero"`
	GenreIDs    []int64  `pg:"genre_ids,array"`
	GenreTitles []string `pg:"genre_titles,array"`
}

// authorSummaryVariant is one row of the summary, without its entity.
type authorSummaryVariant struct {
	Lang   string
	Genres []string
	Works  []string
	Covers []models.AuthorCover
}

var (
	// summaryTitleTag is what a catalog appends to a title and a reader should
	// not see.
	summaryTitleTag = regexp.MustCompile(`(?i)\s*(?:\[[^\]]*\]|\((?:СИ|ЛП|сборник)\))\s*$`)
	// summaryTitleNote is a closing note of any kind: an edition of a title,
	// not another title.
	summaryTitleNote = regexp.MustCompile(`\s*(?:\[[^\]]*\]|\([^)]*\))\s*$`)
	// collectedTitles are titles of magazines, anthologies and numbered
	// volumes. \b does not see Cyrillic, so words are bounded by hand.
	collectedTitles = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:журнал|альманах|антология|хрестоматия|компиляция|собрание\s+сочинений|` +
			`magazine|anthology|almanac)(?:$|[^\p{L}])`),
		regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:том|т\.|выпуск|вып\.|volume|vol\.|issue)\s*\d`),
		regexp.MustCompile(`№\s*\d`),
		regexp.MustCompile(`(?i)_fb2$`),
	}
)

// looksCollected says whether a title reads as a magazine, an anthology or a
// numbered volume.
func looksCollected(title string) bool {
	for _, re := range collectedTitles {
		if re.MatchString(title) {
			return true
		}
	}
	return false
}

// stripTrailing removes what re matches at the end of title, as often as it
// does, and falls back to title when nothing would be left.
func stripTrailing(re *regexp.Regexp, title string) string {
	out := title
	for {
		next := strings.TrimSpace(re.ReplaceAllString(out, ""))
		if next == out {
			break
		}
		out = next
	}
	if out == "" {
		return title
	}
	return out
}

// shownTitle is a title as a reader sees it: one space between words, no tags.
func shownTitle(title string) string {
	return stripTrailing(summaryTitleTag, strings.Join(strings.Fields(title), " "))
}

// titleKey is what two editions of a title share: its words without notes,
// case, ё or punctuation. It is empty for a title with no words.
func titleKey(title string) string {
	base := stripTrailing(summaryTitleNote, strings.Join(strings.Fields(title), " "))
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r == 'ё':
			b.WriteRune('е')
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// summaryRank orders books the way a pile wants them: magazines and
// anthologies last, then books of one or two authors, the ones readers keep,
// the newest. The ID settles the rest, so a rebuild writes what it read.
type summaryRank struct {
	book      *authorSummaryBook
	collected bool
}

func rankSummaryBooks(books []authorSummaryBook) []summaryRank {
	ranked := make([]summaryRank, len(books))
	for i := range books {
		ranked[i] = summaryRank{book: &books[i], collected: books[i].Collected || looksCollected(books[i].Title)}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.collected != b.collected {
			return b.collected
		}
		if aOwn, bOwn := a.book.Authors <= ownAuthorsLimit, b.book.Authors <= ownAuthorsLimit; aOwn != bOwn {
			return aOwn
		}
		if a.book.Favorites != b.book.Favorites {
			return a.book.Favorites > b.book.Favorites
		}
		if !a.book.RegisterDate.Equal(b.book.RegisterDate) {
			return a.book.RegisterDate.After(b.book.RegisterDate)
		}
		return a.book.ID > b.book.ID
	})
	return ranked
}

// composeAuthorSummaries is the summary of one entity's listed books: the
// variant of all languages first, then one per language, in code order. A book
// with no language is in the first alone.
func composeAuthorSummaries(books []authorSummaryBook) []authorSummaryVariant {
	if len(books) == 0 {
		return nil
	}
	byLang := map[string][]authorSummaryBook{}
	for i := range books {
		if lang := books[i].Lang; lang != authorSummaryLangAll {
			byLang[lang] = append(byLang[lang], books[i])
		}
	}
	langs := make([]string, 0, len(byLang))
	for lang := range byLang {
		langs = append(langs, lang)
	}
	slices.Sort(langs)
	out := make([]authorSummaryVariant, 0, len(langs)+1)
	out = append(out, composeVariant(authorSummaryLangAll, books))
	for _, lang := range langs {
		out = append(out, composeVariant(lang, byLang[lang]))
	}
	return out
}

func composeVariant(lang string, books []authorSummaryBook) authorSummaryVariant {
	ranked := rankSummaryBooks(books)
	return authorSummaryVariant{Lang: lang, Genres: summaryGenres(ranked), Works: summaryWorks(ranked),
		Covers: summaryCovers(ranked)}
}

func summaryWorks(ranked []summaryRank) []string {
	works := []string{}
	titles := map[string]bool{}
	for _, r := range ranked {
		key := titleKey(r.book.Title)
		if key == "" || titles[key] {
			continue
		}
		titles[key] = true
		if works = append(works, shownTitle(r.book.Title)); len(works) == maxSummaryWorks {
			break
		}
	}
	return works
}

// summaryCovers walks the ranking for different titles flagged with a cover. A
// book with no words in its title is a title of its own.
func summaryCovers(ranked []summaryRank) []models.AuthorCover {
	covers := []models.AuthorCover{}
	titles := map[string]bool{}
	for _, r := range ranked {
		key := titleKey(r.book.Title)
		if !r.book.Cover || (key != "" && titles[key]) {
			continue
		}
		titles[key] = true
		covers = append(covers, models.AuthorCover{Path: r.book.Path, Filename: r.book.Filename, Title: shownTitle(r.book.Title)})
		if len(covers) == maxSummaryCovers {
			break
		}
	}
	return covers
}

// summaryGenres are the genres most of the books carry, ties to the lower
// genre ID. Genres are counted by title, once per book: the catalog has two
// genres called «Журнал». Magazines and anthologies do not count unless they
// are all there is: they say what the magazine is about, not the author.
func summaryGenres(ranked []summaryRank) []string {
	type tally struct {
		books int
		id    int64
	}
	count := func(withCollected bool) map[string]*tally {
		tallies := map[string]*tally{}
		for _, r := range ranked {
			if r.collected && !withCollected {
				continue
			}
			inBook := map[string]bool{}
			for i, title := range r.book.GenreTitles {
				if title == "" || i >= len(r.book.GenreIDs) {
					continue
				}
				id := r.book.GenreIDs[i]
				t, ok := tallies[title]
				if !ok {
					t = &tally{id: id}
					tallies[title] = t
				}
				t.id = min(t.id, id)
				if !inBook[title] {
					inBook[title] = true
					t.books++
				}
			}
		}
		return tallies
	}
	tallies := count(false)
	if len(tallies) == 0 {
		tallies = count(true)
	}
	genres := make([]string, 0, len(tallies))
	for title := range tallies {
		genres = append(genres, title)
	}
	sort.Slice(genres, func(i, j int) bool {
		a, b := tallies[genres[i]], tallies[genres[j]]
		if a.books != b.books {
			return a.books > b.books
		}
		return a.id < b.id
	})
	if len(genres) > maxSummaryGenres {
		genres = genres[:maxSummaryGenres]
	}
	return genres
}

// authorSummaryBooksSQL reads the listed books of an entity with what ranks
// them, through the GIN index on entity_ids. The largest entity of the catalog
// has about 1,700 books. The magazine and anthology genres are the catalog's
// own codes; looksCollected covers the titles no genre marks.
const authorSummaryBooksSQL = `
SELECT b.id, b.title, b.lang, b.path, b.filename, coalesce(b.cover, false) AS cover, b.registerdate,
    (SELECT greatest(count(*), sum(coalesce(cardinality(x.entity_ids), 1)))
        FROM book_author_display AS x WHERE x.book_id = b.id) AS authors,
    (SELECT count(*) FROM favorite_books AS f WHERE f.book_id = b.id) AS favorites,
    coalesce(g.collected, false) AS collected, g.ids AS genre_ids, g.titles AS genre_titles
FROM opds_catalog_book AS b
LEFT JOIN LATERAL (
    SELECT array_agg(o.id ORDER BY o.id) AS ids,
        array_agg(coalesce(nullif(o.title, ''), o.genre) ORDER BY o.id) AS titles,
        bool_or(o.genre IN ('periodic', 'журналы', 'collection', 'literature_antology')) AS collected
    FROM opds_catalog_bgenre AS bg
    JOIN opds_catalog_genre AS o ON o.id = bg.genre_id
    WHERE bg.book_id = b.id
) AS g ON true
WHERE b.id = ANY (ARRAY(SELECT d.book_id FROM book_author_display AS d WHERE d.entity_ids @> ARRAY[?::uuid]))
    AND b.approved
    AND NOT b.duplicate_hidden`

// authorSummaryNamesSQL are the names the entity's listed rows give it, the most
// used first: a name it is shown by, or a nickname. A row of several entities is
// a line of several people, no name of any one of them: a magazine's twelve
// authors glued into one credit reach each of the twelve. A name found only on
// a book the listing hides is not published: each arm has the listing's
// predicates, as authorSummaryBooksSQL does.
const authorSummaryNamesSQL = `
SELECT n.name
FROM (
    SELECT d.display AS name FROM book_author_display AS d
    JOIN opds_catalog_book AS b ON b.id = d.book_id
    WHERE d.entity_ids @> ARRAY[?0::uuid] AND cardinality(d.entity_ids) = 1
        AND b.approved AND NOT b.duplicate_hidden
    UNION ALL
    SELECT d.nickname FROM book_author_display AS d
    JOIN opds_catalog_book AS b ON b.id = d.book_id
    WHERE d.entity_ids @> ARRAY[?0::uuid] AND cardinality(d.entity_ids) = 1 AND d.nickname IS NOT NULL
        AND b.approved AND NOT b.duplicate_hidden
) AS n
WHERE n.name <> ''
GROUP BY n.name
ORDER BY count(*) DESC, n.name
LIMIT ?1`

const writeAuthorSummarySQL = `
INSERT INTO author_entity_summary AS s (entity_id, lang, genres, works, covers, names)
VALUES (?, ?, ?::text[], ?::text[], ?::jsonb, ?::text[])
ON CONFLICT (entity_id, lang) DO UPDATE
SET genres = EXCLUDED.genres, works = EXCLUDED.works, covers = EXCLUDED.covers, names = EXCLUDED.names
WHERE (s.genres, s.works, s.covers, s.names) IS DISTINCT FROM
    (EXCLUDED.genres, EXCLUDED.works, EXCLUDED.covers, EXCLUDED.names)`

// AuthorSummarySkip is an entity the summary refused and the SQLSTATE it was
// refused with.
type AuthorSummarySkip struct {
	EntityID uuid.UUID
	SQLState string
}

// severityFatal is the severity of an error that ended the session.
const severityFatal = "FATAL"

// AuthorSummaryDB opens the transactions the summary works in, one per entity.
// *pg.DB is one; a *pg.Tx is not, it would commit on the first entity.
type AuthorSummaryDB interface {
	RunInTransaction(ctx context.Context, fn func(*pg.Tx) error) error
}

// inSummaryTx runs fn in a transaction of its own, each statement under timeout.
func inSummaryTx(ctx context.Context, db AuthorSummaryDB, timeout time.Duration, fn func(*pg.Tx) error) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = ?`, timeout.Milliseconds()); err != nil {
			return err
		}
		return fn(tx)
	})
}

// summaryEntityRefused says whether PostgreSQL refused an entity, whatever the
// reason: its timeout included, because catalog data is what it is and a batch
// retried forever serves no one. A lost connection and a canceled context, whose
// statement comes back as the same 57014, say nothing of the entity.
func summaryEntityRefused(ctx context.Context, err error) bool {
	var refused pg.Error
	return ctx.Err() == nil && errors.As(err, &refused) && refused.Field('V') != severityFatal
}

// rebuildAuthorEntitySummary brings one entity's summary in line with its books.
func rebuildAuthorEntitySummary(ctx context.Context, tx pg.DBI, id uuid.UUID) error {
	var live []bool
	if _, err := tx.QueryContext(ctx, &live, `SELECT merged_into IS NULL FROM author_entity WHERE id = ?`, id); err != nil {
		return err
	}
	var variants []authorSummaryVariant
	if len(live) == 1 && live[0] {
		var books []authorSummaryBook
		if _, err := tx.QueryContext(ctx, &books, authorSummaryBooksSQL, id); err != nil {
			return err
		}
		variants = composeAuthorSummaries(books)
	}
	langs := make([]string, len(variants))
	for i := range variants {
		langs[i] = variants[i].Lang
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM author_entity_summary WHERE entity_id = ? AND NOT lang = ANY (?::text[])`,
		id, pg.Array(langs)); err != nil {
		return err
	}
	if len(variants) == 0 {
		return nil
	}
	names := []string{}
	if _, err := tx.QueryContext(ctx, &names, authorSummaryNamesSQL, id, maxSummaryNames); err != nil {
		return err
	}
	for i := range variants {
		v := &variants[i]
		covers, err := json.Marshal(v.Covers)
		if err != nil {
			return err
		}
		held := []string{}
		if v.Lang == authorSummaryLangAll {
			held = names
		}
		if _, err := tx.ExecContext(ctx, writeAuthorSummarySQL, id, v.Lang, pg.Array(v.Genres), pg.Array(v.Works),
			string(covers), pg.Array(held)); err != nil {
			return err
		}
	}
	return nil
}

// AuthorSummaryWork is what a step of the summary did.
type AuthorSummaryWork struct {
	// Entities is how many entities the step took.
	Entities int
	Skipped  []AuthorSummarySkip
}

// summaryMark is a mark as the drain took it. Generation is the row's xmin, the
// transaction that wrote it: rolling the take back restores that same row, and a
// mark written after the take has another one. marked_at cannot say this: it is
// now(), so two transactions that start in one microsecond share it, and a
// transaction that waited for the read model's lock stamps its mark earlier than
// the rows it follows.
type summaryMark struct {
	EntityID   uuid.UUID `pg:"entity_id,type:uuid"`
	Generation string    `pg:"generation"`
}

// DrainAuthorSummaryMarks rebuilds the oldest marked entities, at most limit,
// each in its own transaction: a mark is gone exactly when its rebuild commits,
// or when the entity was refused and passed. Marks another pod holds are
// skipped, never waited for. A lost connection or a canceled context ends the
// batch; what already committed is reported with the error.
func DrainAuthorSummaryMarks(
	ctx context.Context, db AuthorSummaryDB, limit int, timeout time.Duration,
) (AuthorSummaryWork, error) {
	var out AuthorSummaryWork
	for out.Entities < limit {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var taken []summaryMark
		err := inSummaryTx(ctx, db, timeout, func(tx *pg.Tx) error {
			if _, err := tx.QueryContext(ctx, &taken, `DELETE FROM author_entity_summary_dirty d
				WHERE d.entity_id IN (SELECT entity_id FROM author_entity_summary_dirty ORDER BY marked_at LIMIT 1 FOR UPDATE SKIP LOCKED)
				RETURNING d.entity_id, d.xmin::text AS generation`); err != nil {
				return fmt.Errorf("taking a summary mark: %w", err)
			}
			if len(taken) == 0 {
				return nil
			}
			return rebuildAuthorEntitySummary(ctx, tx, taken[0].EntityID)
		})
		if len(taken) == 0 {
			return out, err
		}
		id := taken[0].EntityID
		out.Entities++
		if err == nil {
			continue
		}
		if !summaryEntityRefused(ctx, err) {
			return out, fmt.Errorf("rebuilding the summary of entity %s: %w", id, err)
		}
		if passErr := passSummaryMark(ctx, db, timeout, taken[0]); passErr != nil {
			return out, passErr
		}
		out.Skipped = append(out.Skipped, AuthorSummarySkip{EntityID: id, SQLState: sqlState(err)})
	}
	return out, nil
}

// passSummaryMark takes the mark of a refused entity, which the rollback put
// back, and only that one: between the rollback and this transaction another
// pod can consume it and a later change can mark the entity again, and that mark
// is still owed a rebuild. It waits for the row rather than skip it: the failed
// connection's locks last until the server sees it go.
func passSummaryMark(ctx context.Context, db AuthorSummaryDB, timeout time.Duration, mark summaryMark) error {
	err := inSummaryTx(ctx, db, timeout, func(tx *pg.Tx) error {
		_, execErr := tx.ExecContext(ctx, `DELETE FROM author_entity_summary_dirty WHERE entity_id = ? AND xmin = ?::xid`,
			mark.EntityID, mark.Generation)
		return execErr
	})
	if err != nil {
		return fmt.Errorf("passing the mark of entity %s: %w", mark.EntityID, err)
	}
	return nil
}

// AuthorSummaryWalkResult is what one step of the walk did. Ran is false when
// no walk was due, or another pod holds it; Finished marks the last step, with
// the walk's totals.
type AuthorSummaryWalkResult struct {
	AuthorSummaryWork
	Ran, Finished bool
	Read, Refused int64
}

// summaryWalkRow is the walk's one row.
type summaryWalkRow struct {
	Cursor  *uuid.UUID `pg:"walk_cursor,type:uuid"`
	Read    int64      `pg:"walk_read,use_zero"`
	Skipped int64      `pg:"walk_skipped,use_zero"`
	Due     bool       `pg:"due,use_zero"`
}

// nextSummaryEntitySQL is the first ID past the cursor that has something to
// walk: an entity, merged ones too, or summary rows that outlived theirs, as the
// read model's walk finds rows of a book that is gone.
const nextSummaryEntitySQL = `
SELECT n.id
FROM (
    (SELECT id FROM author_entity WHERE id > ?0 ORDER BY id LIMIT 1)
    UNION ALL
    (SELECT entity_id FROM author_entity_summary WHERE entity_id > ?0 ORDER BY entity_id LIMIT 1)
) AS n(id)
ORDER BY n.id
LIMIT 1`

// AuthorSummaryWalkStep advances the walk over every entity by at most batch
// entities, each in its own transaction with the cursor: a restart goes on from
// the last one committed, and a refused entity is passed once a walk, however
// often it fails. A walk starts when none runs and none finished within every,
// which makes the first one start by itself after the migration: that one is the
// backfill. It finishes in the step that finds nothing past the cursor. A lost
// connection or a canceled context ends the step; what already committed is
// reported with the error.
func AuthorSummaryWalkStep(
	ctx context.Context, db AuthorSummaryDB, batch int, every, timeout time.Duration,
) (AuthorSummaryWalkResult, error) {
	var out AuthorSummaryWalkResult
	for out.Entities < batch {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var from, id *uuid.UUID
		err := inSummaryTx(ctx, db, timeout, func(tx *pg.Tx) (err error) {
			from, id, err = walkOneSummaryEntity(ctx, tx, every, &out)
			return err
		})
		if id == nil {
			return out, err
		}
		out.Entities++
		if err == nil {
			continue
		}
		if !summaryEntityRefused(ctx, err) {
			return out, fmt.Errorf("walking the summary of entity %s: %w", *id, err)
		}
		if passErr := passSummaryWalkEntity(ctx, db, timeout, *from, *id); passErr != nil {
			return out, passErr
		}
		out.Skipped = append(out.Skipped, AuthorSummarySkip{EntityID: *id, SQLState: sqlState(err)})
	}
	return out, nil
}

// walkOneSummaryEntity moves the walk by one entity inside tx, and finishes it
// when there is none left. It returns where the walk stood and the entity it
// took, nil with no walk due, with another pod on the walk, or with the walk
// finished; a failure after that is the entity's.
func walkOneSummaryEntity(
	ctx context.Context, tx *pg.Tx, every time.Duration, out *AuthorSummaryWalkResult,
) (from, id *uuid.UUID, err error) {
	var rows []summaryWalkRow
	if _, err := tx.QueryContext(ctx, &rows, `SELECT walk_cursor, walk_read, walk_skipped,
			walk_cursor IS NULL AND (finished_at IS NULL OR finished_at <= now() - ?::interval) AS due
		FROM author_entity_summary_walk FOR UPDATE SKIP LOCKED`, every.String()); err != nil {
		return nil, nil, fmt.Errorf("reading the summary walk: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}
	state := rows[0]
	if state.Cursor == nil {
		if !state.Due {
			return nil, nil, nil
		}
		state = summaryWalkRow{Cursor: &uuid.Nil}
	}
	out.Ran = true

	var next []uuid.UUID
	if _, err := tx.QueryContext(ctx, &next, nextSummaryEntitySQL, *state.Cursor); err != nil {
		return nil, nil, fmt.Errorf("reading the walk's next entity: %w", err)
	}
	if len(next) == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE author_entity_summary_walk
			SET walk_cursor = NULL, walk_read = 0, walk_skipped = 0, finished_at = now()`); err != nil {
			return nil, nil, fmt.Errorf("finishing the summary walk: %w", err)
		}
		out.Finished, out.Read, out.Refused = true, state.Read, state.Skipped
		return nil, nil, nil
	}
	if err := rebuildAuthorEntitySummary(ctx, tx, next[0]); err != nil {
		return state.Cursor, &next[0], err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE author_entity_summary_walk SET walk_cursor = ?, walk_read = walk_read + 1`,
		next[0]); err != nil {
		return state.Cursor, &next[0], fmt.Errorf("moving the summary walk: %w", err)
	}
	return state.Cursor, &next[0], nil
}

// passSummaryWalkEntity moves the cursor past a refused entity, from where the
// failed transaction left it. It waits for the walk's row rather than skip it:
// the failed connection's lock lasts until the server sees it go. With the
// cursor moved meanwhile, another pod is already past this entity.
func passSummaryWalkEntity(
	ctx context.Context, db AuthorSummaryDB, timeout time.Duration, from, id uuid.UUID,
) error {
	err := inSummaryTx(ctx, db, timeout, func(tx *pg.Tx) error {
		_, execErr := tx.ExecContext(ctx, `UPDATE author_entity_summary_walk
			SET walk_cursor = ?, walk_read = walk_read + 1, walk_skipped = walk_skipped + 1
			WHERE coalesce(walk_cursor, ?) = ?`, id, uuid.Nil, from)
		return execErr
	})
	if err != nil {
		return fmt.Errorf("passing entity %s in the summary walk: %w", id, err)
	}
	return nil
}

// authorSummariesSQL reads a page of summaries in one statement: each entity's
// row of all languages, which holds its names, with the row of the reader's
// language. The closest name is the one the search matched; it is reported
// only when it is closer than the name the entity is shown by: a word order or
// an initial explains nothing a reader does not see already.
const authorSummariesSQL = `
WITH q AS (SELECT public.search_normalize(?0::text) AS needle)
SELECT a.entity_id, v.genres, v.works, v.covers,
    CASE WHEN m.distance < (SELECT q.needle FROM q) <<-> public.search_normalize(e.display_name)
        THEN m.name END AS matched_name
FROM author_entity_summary AS a
JOIN author_entity AS e ON e.id = a.entity_id
LEFT JOIN author_entity_summary AS v ON v.entity_id = a.entity_id AND v.lang = ?1
LEFT JOIN LATERAL (
    SELECT n.name, (SELECT q.needle FROM q) <<-> public.search_normalize(n.name) AS distance
    FROM unnest(a.names) AS n(name)
    ORDER BY distance, n.name
    LIMIT 1
) AS m ON true
WHERE a.lang = '' AND a.entity_id IN (?2)`

// authorSummaryRow is one row of authorSummariesSQL.
type authorSummaryRow struct {
	EntityID    uuid.UUID            `pg:"entity_id,type:uuid"`
	Genres      []string             `pg:"genres,array"`
	Works       []string             `pg:"works,array"`
	Covers      []models.AuthorCover `pg:"covers,type:jsonb"`
	MatchedName *string              `pg:"matched_name"`
}

// authorSummaries reads the summaries of a page's entities for the reader's
// language. An entity not built yet has no entry: the caller shows it without.
func authorSummaries(
	ctx context.Context, db pg.DBI, query, lang string, ids []uuid.UUID,
) (map[uuid.UUID]*authorSummaryRow, error) {
	out := make(map[uuid.UUID]*authorSummaryRow, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []authorSummaryRow
	if _, err := db.QueryContext(ctx, &rows, authorSummariesSQL, query, lang, pg.In(ids)); err != nil {
		return nil, fmt.Errorf("reading %d author summaries: %w", len(ids), err)
	}
	for i := range rows {
		out[rows[i].EntityID] = &rows[i]
	}
	return out, nil
}
