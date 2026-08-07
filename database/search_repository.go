package database

import (
	"context"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// PGSearchRepository owns the lexical search SQL: normalization, candidate
// lanes, ranking, deduplication and exact totals all happen in PostgreSQL.
// The pg.DBI boundary lets production share the pool while tests run inside
// the rollback fixture transaction.
type PGSearchRepository struct {
	db pg.DBI
}

// NewPGSearchRepository wires the repository to a database handle.
func NewPGSearchRepository(db pg.DBI) *PGSearchRepository {
	return &PGSearchRepository{db: db}
}

// bookSearchSQL ranks candidates lexicographically and returns one page of
// IDs together with the exact pre-pagination total and the query correlation
// hash. Lanes, from strongest to weakest:
//
//  1. exact_match — the normalized title equals the needle;
//  2. prefix_match — the normalized title starts with the needle;
//  3. word_set_match — the title is the needle's words and nothing else, in
//     any order, each as a whole word or word prefix;
//  4. substring_match — the needle occurs inside the normalized title;
//  5. all_words_match — every needle word has a title word starting with it,
//     the title saying more around them;
//  6. word_score — word_similarity of the needle against the title;
//  7. trigram_score — plain trigram similarity for typos.
//
// The fuzzy lanes (5 and 6) and the word-coverage lane fire only from three
// runes up, so one- and two-rune manual queries stay exact/prefix/substring.
// Candidate generation: the substring-family lanes (exact, prefix,
// substring) share one WHERE over the NOT MATERIALIZED visible set, so the
// planner collapses the OR into a single BitmapOr over the title indexes
// and fetches heap pages once — their bitmaps overlap heavily, so the
// union is cheap. The two similarity lanes (%>, %) and the word-coverage
// lane keep their own UNION ALL legs: merging the similarity lanes into
// the OR would make the bitmap recheck evaluate every branch's copy of
// the normalization expression for tens of thousands of lossy trigram
// candidates, which costs more than a second heap pass, and the word-
// coverage anti-joins inside an OR would collapse the whole clause into
// a seq scan. The needle travels as InitPlan parameters from the one-row
// q CTE; a cross join against q would turn the same terms into join
// filters and defeat the indexes. The word-coverage lane reports through
// a lane bitmask, so the per-candidate signal pass never re-runs the
// anti-joins. The explicit floors 0.60/0.30 are applied to the computed
// scores, so no threshold hides in a Go constant or a session mutation.
// An ExactBookID pin narrows the candidate set to that one visible book
// and bypasses the textual lanes. Every candidate is one row of the book
// table, which makes dedupe by book.id structural. LIMIT/OFFSET apply
// only to the fully ranked page; meta carries the total over the whole
// admitted set and always produces a row, even with zero candidates.
const bookSearchSQL = `
WITH q AS (
    SELECT
        ?::text AS raw,
        public.search_normalize(?::text) AS needle,
        public.search_normalize(NULLIF(?::text, '')) AS author_needle,
        char_length(public.search_normalize(?::text)) AS rune_count,
        md5(public.search_normalize(?::text)) AS query_hash,
        ?::bigint AS exact_id
),
visible AS NOT MATERIALIZED (
    -- Visibility and scope filters, nothing textual. NOT MATERIALIZED so
    -- every reference inlines the filters into that leg's own index scan
    -- instead of computing the normalized title for the whole catalog.
    SELECT b.id, public.search_normalize(b.title) AS norm_title
    FROM opds_catalog_book b
    WHERE b.approved = (NOT ?::bool)
        AND (NOT b.duplicate_hidden OR ?::bool)
        AND (?::text = '' OR ?::text = 'all' OR b.lang = ?::text)
        AND (NOT ?::bool OR EXISTS (
            SELECT 1 FROM favorite_books fb
            WHERE fb.book_id = b.id AND fb.user_id = ?))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM opds_catalog_bauthor ba
            WHERE ba.book_id = b.id AND ba.author_id = ?))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM opds_catalog_bseries bs
            WHERE bs.book_id = b.id AND bs.ser_id = ?))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM opds_catalog_bgenre bg
            WHERE bg.book_id = b.id AND bg.genre_id = ?))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM book_collection_books bcb
            WHERE bcb.book_id = b.id AND bcb.book_collection_id = ?))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM book_collection_items ci
            JOIN book_collections c ON c.id = ci.collection_id
            WHERE ci.book_id = b.id AND ci.collection_id = ?
                AND c.is_curated AND c.is_public
                -- Membership mirrors models.MatchStatusAutoMatched and
                -- models.MatchStatusManual; anything else (ignored,
                -- pending) is not membership.
                AND ci.match_status IN ('auto_matched', 'manual')))
        -- AuthorQuery narrows the same request: exact/prefix at any length,
        -- the index-served word-similarity lane from three runes up. It
        -- never falls back to a Go-side author pass.
        AND ((SELECT q.author_needle FROM q) IS NULL OR EXISTS (
            SELECT 1 FROM opds_catalog_bauthor ba
            JOIN opds_catalog_author a ON a.id = ba.author_id
            WHERE ba.book_id = b.id
                AND (public.search_normalize(a.full_name) = (SELECT q.author_needle FROM q)
                    OR public.search_normalize(a.full_name) LIKE (SELECT q.author_needle FROM q) || '%'
                    OR (char_length((SELECT q.author_needle FROM q)) >= 3
                        AND public.search_normalize(a.full_name) %> (SELECT q.author_needle FROM q)))))
),
anchor AS (
    -- The longest needle word, used as the word-coverage lane's index qual.
    --
    -- The lane used to anchor on the first word, and Russian titles start with
    -- prepositions constantly: for "в тумане" the prefilter became
    -- LIKE '%в%', which matches nearly every book, so the planner dropped to a
    -- sequential scan over 266 640 rows and ran the anti-join per row —
    -- 3.9 seconds end to end, against 0.26 for the worst query in the reviewed
    -- corpus, which contains no such needle. Coverage requires every needle
    -- word to prefix some title word, so any word may serve as the qual and
    -- the most selective one should. Ties break on the word itself, so the
    -- choice is deterministic.
    SELECT w FROM unnest(string_to_array((SELECT q.needle FROM q), ' ')) AS t(w)
    ORDER BY char_length(w) DESC, w
    LIMIT 1
),
clean AS (
    -- The three substring-family lanes share one WHERE, so the planner
    -- collapses the OR into a single BitmapOr and touches the heap once
    -- for all of them. Their bitmaps overlap heavily (a prefix hit is
    -- almost always a substring hit too), so the union costs little.
    -- The needle reaches the quals as InitPlan parameters from the
    -- one-row q CTE — pseudoconstants that keep index quals valid,
    -- unlike a cross join against q.
    SELECT v.id, v.norm_title
    FROM visible v
    WHERE (SELECT q.exact_id FROM q) = 0
        AND (SELECT q.needle FROM q) <> ''
        AND (v.norm_title = (SELECT q.needle FROM q)
            OR v.norm_title LIKE (SELECT q.needle FROM q) || '%'
            OR v.norm_title LIKE '%' || (SELECT q.needle FROM q) || '%')
),
lane_hits AS (
    -- One row per (lane, book) hit. The two similarity lanes stay OUT of
    -- the shared OR on purpose: their trigram bitmaps are lossy and huge
    -- (tens of thousands of TIDs for a common word), and a merged recheck
    -- would evaluate every OR branch — each carrying its own copy of the
    -- normalization expression — for every one of those heap rows, which
    -- costs more than fetching the overlapping heap pages a second time.
    -- Kept as separate legs, each rechecks only its own operator. The
    -- word-coverage leg likewise keeps its anti-joins out of any OR; it
    -- carries bit 32 so signaled never re-runs the anti-joins per
    -- candidate. Its first-word prefilter is logically implied by the
    -- two-way coverage (every needle word is a prefix of some title word,
    -- hence occurs in the title), so the leg and the flag it replaces are
    -- equivalent; the prefilter only gives the GIN index a qual to start
    -- from. string_to_array on a single space splits exactly like the
    -- previous regexp_split_to_table(pattern ' '), including empty words
    -- on repeated spaces, without the regex engine.
    SELECT c.id, c.norm_title, 0::int AS lanes FROM clean c
    UNION ALL
    SELECT v.id, v.norm_title, 0
    FROM visible v
    WHERE (SELECT q.exact_id FROM q) = 0
        AND (SELECT q.rune_count FROM q) >= 3
        AND v.norm_title %> (SELECT q.needle FROM q)
    UNION ALL
    SELECT v.id, v.norm_title, 0
    FROM visible v
    WHERE (SELECT q.exact_id FROM q) = 0
        AND (SELECT q.rune_count FROM q) >= 3
        AND v.norm_title % (SELECT q.needle FROM q)
    UNION ALL
    -- Word coverage, admitting on the weaker of the two readings and
    -- reporting which one held.
    --
    -- Bit 32 is coverage: every word the reader typed has a title word
    -- starting with it. Bit 64 adds the reverse — every title word is covered
    -- too — which makes the title the reader's words and nothing else.
    --
    -- Admitting on set equality alone, as this lane first did, made it nearly
    -- unreachable: "гарри поттер" is set-equal to none of the 224 Harry Potter
    -- books, because each of them says something more. Titles that spread the
    -- query's words out were then not admitted by anything at all — "Тайна
    -- старого заброшенного маяка" scores word_similarity 0.500 for "тайна
    -- маяка", under the 0.6 the fuzzy lane demands.
    --
    -- The two readings rank differently, so both are kept: set equality
    -- outranks an interior substring, plain coverage sits below it. Otherwise
    -- coverage would swallow the substring tier — a title containing the
    -- needle contiguously covers its words by construction.
    --
    -- The LIKE prefilter stays: coverage means every query word prefixes some
    -- title word, so it is implied whichever word is used, and it gives the
    -- GIN index an entry qual instead of a per-row anti-join over the whole
    -- catalog. Which word is used decides whether that actually happens — see
    -- the anchor CTE.
    SELECT v.id, v.norm_title,
        CASE WHEN NOT EXISTS (
            SELECT 1 FROM unnest(string_to_array(v.norm_title, ' ')) AS tw(word)
            WHERE NOT EXISTS (
                SELECT 1 FROM unnest(string_to_array((SELECT q.needle FROM q), ' ')) AS nw(word)
                WHERE tw.word LIKE nw.word || '%'))
        THEN 96 ELSE 32 END
    FROM visible v
    WHERE (SELECT q.exact_id FROM q) = 0
        AND (SELECT q.rune_count FROM q) >= 3
        -- Single-word needles skip the lane entirely. Coverage then means one
        -- title word starts with the needle, which already makes the title
        -- contain it, so the substring lane has the row; and set equality then
        -- means every title word starts with it, which makes the title a
        -- prefix match, ranked higher still. The lane would add no row and no
        -- tier, only an anti-join over every "рассказы"-class candidate.
        AND strpos((SELECT q.needle FROM q), ' ') > 0
        -- Under three characters no trigram index can serve the qual, and the
        -- lane would seq-scan again; the fuzzy lanes still cover such needles.
        AND char_length((SELECT w FROM anchor)) >= 3
        AND v.norm_title LIKE '%' || (SELECT w FROM anchor) || '%'
        AND NOT EXISTS (
            SELECT 1 FROM unnest(string_to_array((SELECT q.needle FROM q), ' ')) AS nw(word)
            WHERE NOT EXISTS (
                SELECT 1 FROM unnest(string_to_array(v.norm_title, ' ')) AS tw(word)
                WHERE tw.word LIKE nw.word || '%'))
    UNION ALL
    -- A pinned exact ID bypasses the textual lanes: it is a navigation
    -- request, not a text filter. The one-time gate leaves the textual
    -- lanes unexecuted, and the id equality is a primary key lookup.
    SELECT v.id, v.norm_title, 0
    FROM visible v
    WHERE (SELECT q.exact_id FROM q) > 0
        AND v.id = (SELECT q.exact_id FROM q)
),
candidates AS (
    -- GROUP BY replaces the old UNION dedupe: every candidate is one row
    -- of the book table, lane bits accumulate through bit_or.
    SELECT l.id, l.norm_title, bit_or(l.lanes) AS lanes
    FROM lane_hits l
    GROUP BY l.id, l.norm_title
),
signaled AS (
    -- Only the two word-coverage signals come from the lane bitmask;
    -- recomputing their anti-joins per candidate was the dominant cost here.
    -- Every other signal stays a pure function of the candidate row —
    -- including the raw similarity scores, because the %> / % lanes are
    -- strict while the admission floors below are inclusive, so a gated
    -- score would move the boundary.
    SELECT
        c.id,
        (c.norm_title = q.needle) AS exact_match,
        (c.norm_title LIKE q.needle || '%') AS prefix_match,
        (c.lanes & 64 <> 0) AS word_set_match,
        (strpos(c.norm_title, q.needle) > 0) AS substring_match,
        (c.lanes & 32 <> 0) AS all_words_match,
        CASE WHEN q.rune_count >= 3 THEN word_similarity(q.needle, c.norm_title)
             ELSE 0::real END AS word_score,
        CASE WHEN q.rune_count >= 3 THEN similarity(c.norm_title, q.needle)
             ELSE 0::real END AS trigram_score,
        NULLIF(strpos(c.norm_title, q.needle), 0) AS match_position,
        abs(char_length(c.norm_title) - char_length(q.needle)) AS length_delta
    FROM candidates c CROSS JOIN q
),
admitted AS (
    SELECT s.*,
        (SELECT count(*) FROM favorite_books fb WHERE fb.book_id = s.id) AS favorite_count
    FROM signaled s CROSS JOIN q
    -- A pinned exact ID is admitted unconditionally; textual candidates must
    -- carry at least one signal or clear a score floor.
    WHERE q.exact_id > 0
        OR s.exact_match OR s.prefix_match
        OR s.word_set_match OR s.substring_match OR s.all_words_match
        OR s.word_score >= 0.60
        OR s.trigram_score >= 0.30
),
ranked AS (
    SELECT a.id,
        row_number() OVER (
            ORDER BY
                a.exact_match DESC,
                a.prefix_match DESC,
                a.word_set_match DESC,
                a.substring_match DESC,
                a.all_words_match DESC,
                a.word_score DESC,
                a.trigram_score DESC,
                a.match_position ASC NULLS LAST,
                a.length_delta ASC,
                a.favorite_count DESC,
                a.id ASC
        ) AS pos
    FROM admitted a
),
page AS (
    SELECT r.id, r.pos
    FROM ranked r
    ORDER BY r.pos
    LIMIT ? OFFSET ?
),
meta AS (
    SELECT
        (SELECT count(*) FROM admitted) AS total,
        (SELECT q.query_hash FROM q) AS query_hash
)
SELECT p.id, m.total, m.query_hash
FROM meta m
LEFT JOIN page p ON true
ORDER BY p.pos
`

// searchBookRow is one row of bookSearchSQL: a page entry, or the metadata
// row with a NULL id when the page is empty.
type searchBookRow struct {
	ID        *int64 `pg:"id"`
	Total     int    `pg:"total"`
	QueryHash string `pg:"query_hash"`
}

// orderByIDs puts loaded rows back into the order the ranking gave their ids.
//
// A page is two queries — one ranks the ids, one loads the rows — and the
// second returns them in whatever order the planner liked, so the rank has to
// be reapplied here.
//
// It walks the ids rather than the rows because the two need not agree: the
// loading query carries no visibility filter and the catalog scanner deletes
// books, so a row asked for in the first query can be gone by the second.
// Walking the ids skips what vanished; walking the rows and indexing by
// position, as this did, wrote past the end of a slice sized by the smaller of
// the two and panicked the request.
//
// Total is left as the database reported it. It counts what matched at the
// moment the page was ranked, which is the only moment either number describes.
func orderByIDs(ids []int64, books []models.Book) []models.Book {
	at := make(map[int64]int, len(books))
	for i := range books {
		at[books[i].ID] = i
	}

	ordered := make([]models.Book, 0, len(ids))
	for _, id := range ids {
		if i, ok := at[id]; ok {
			ordered = append(ordered, books[i])
		}
	}
	return ordered
}

// SearchBooks returns one ranked page and the exact pre-pagination total.
//
//nolint:gocritic // the repository port takes the request by value; this implements it
func (r *PGSearchRepository) SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	page := models.BookSearchPage{Limit: req.Limit, Offset: req.Offset}

	var rows []searchBookRow
	query := func(q pg.DBI) error {
		_, err := q.QueryContext(ctx, &rows, bookSearchSQL,
			req.Query, req.Query, req.AuthorQuery, req.Query, req.Query, req.ExactBookID,
			req.Unapproved, req.IncludeHidden,
			req.Language, req.Language, req.Language,
			req.Favorites, req.UserID,
			req.AuthorID, req.AuthorID,
			req.SeriesID, req.SeriesID,
			req.GenreID, req.GenreID,
			req.CollectionID, req.CollectionID,
			req.CuratedCollectionID, req.CuratedCollectionID,
			req.Limit, req.Offset)
		return err
	}
	if err := r.queryWithBookThreshold(ctx, query); err != nil {
		return page, preferContextError(ctx, err)
	}

	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		page.Total = row.Total
		page.QueryHash = row.QueryHash
		if row.ID != nil {
			ids = append(ids, *row.ID)
		}
	}

	if len(ids) > 0 {
		var books []models.Book
		if err := r.db.ModelContext(ctx, &books).
			Relation("Authors").
			Relation("Series").
			Relation("Genres").
			ColumnExpr("book.*, (SELECT COUNT(*) FROM favorite_books WHERE book_id = book.id) AS favorite_count").
			Where("book.id IN (?)", pg.In(ids)).
			Select(); err != nil {
			return page, preferContextError(ctx, err)
		}
		page.Books = orderByIDs(ids, books)
		if err := r.markCallerFavorites(ctx, req.UserID, page.Books); err != nil {
			return page, preferContextError(ctx, err)
		}
		populateSeriesNumbersWithDB(r.db, page.Books)
	}

	return page, nil
}

// queryWithBookThreshold runs fn inside one transaction with the book-search
// trigram floor raised to 0.5 for that transaction only. At the pg_trgm
// default 0.3 the lossy GIN bitmap pulls tens of thousands of heap rows for a
// common word; SET LOCAL keeps the raise inside this statement's transaction —
// the same pooled connections serve the author search, whose % lane must stay
// at 0.3 to keep abbreviated names reachable.
//
// When the repository already sits inside a transaction (the test fixture
// hands one out) SET LOCAL applies to it directly. Wrapping that case in
// RunInTransaction would be wrong: go-pg v10 has no savepoints, and
// (*Tx).RunInTransaction ends with COMMIT on the outer transaction — that
// once committed fixture rows into the real catalog.
func (r *PGSearchRepository) queryWithBookThreshold(ctx context.Context, fn func(q pg.DBI) error) error {
	if tx, ok := r.db.(*pg.Tx); ok {
		if _, err := tx.ExecContext(ctx, "SET LOCAL pg_trgm.similarity_threshold = 0.5"); err != nil {
			return err
		}
		return fn(tx)
	}
	return r.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, "SET LOCAL pg_trgm.similarity_threshold = 0.5"); err != nil {
			return err
		}
		return fn(tx)
	})
}

// markCallerFavorites sets Fav on the page books the caller favorited. The
// global FavoriteCount is part of the hydration query; Fav is caller-specific
// and is resolved here, for the page IDs only.
func (r *PGSearchRepository) markCallerFavorites(ctx context.Context, userID int64, books []models.Book) error {
	if userID == 0 || len(books) == 0 {
		return nil
	}
	ids := make([]int64, len(books))
	for i := range books {
		ids[i] = books[i].ID
	}
	var favIDs []int64
	if _, err := r.db.QueryContext(ctx, &favIDs,
		`SELECT book_id FROM favorite_books WHERE user_id = ? AND book_id IN (?)`,
		userID, pg.In(ids)); err != nil {
		return err
	}
	faved := make(map[int64]struct{}, len(favIDs))
	for _, id := range favIDs {
		faved[id] = struct{}{}
	}
	for i := range books {
		_, books[i].Fav = faved[books[i].ID]
	}
	return nil
}

// preferContextError reports err unless the caller's context explains the
// failure. The driver surfaces an in-flight cancellation as SQLSTATE 57014,
// never as context.Canceled, and cancellation is part of the repository
// contract — adapters must be able to errors.Is it either way.
func preferContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// authorSearchRepositorySQL finds authors by name and counts the books each
// one would actually open — the repository port of the GetAuthors query,
// moved from lower(full_name) to search_normalize(full_name) so searches
// inherit the canonical normalization (ё/е, punctuation, NFC) and reach
// idx_author_full_name_search_norm_trgm.
//
// The count has to agree with the list behind it or it misleads: every
// filter here is one the book list applies too — approved, not a hidden
// duplicate, and the reader's books language. An author whose books are all
// filtered away counts zero, and the inner join then drops them from the
// results, because a row that leads to an empty list is a dead end offered
// as a choice.
//
// Candidates come from two operators because neither finds the other's
// names: % compares whole strings and punishes a surname-only query for the
// given names it did not type, while %> asks how well the query matches some
// run of words within the name and misses loose fuzzy matches. Both stay at
// the pg_trgm session floor — unlike the book lane, which runs at 0.5 —
// because abbreviated names ("Толкин Дж." at 0.360) live between the two
// floors.
//
// Ordering runs normalized word distance, then size, then id. Whole-string
// distance ranks by length as much as by likeness and buried the largest
// Tolstoy under one-book namesakes; word distance ties on the surname, so
// the question becomes which of these people the reader meant — answered by
// how many books each one holds. Ties break on id last, or paging repeats
// and skips rows.
//
// meta always produces a row, so an empty page still carries the exact total
// and the correlation hash for logging.
const authorSearchRepositorySQL = `
WITH q AS (
    SELECT public.search_normalize(?::text) AS needle,
        md5(public.search_normalize(?::text)) AS query_hash
),
matched AS (
    SELECT a.id, a.full_name
    FROM opds_catalog_author AS a
    WHERE public.search_normalize(a.full_name) % (SELECT q.needle FROM q)
        OR public.search_normalize(a.full_name) %> (SELECT q.needle FROM q)
),
counted AS (
    SELECT m.id, m.full_name, count(b.id) AS books_count
    FROM matched AS m
    JOIN opds_catalog_bauthor AS ba ON ba.author_id = m.id
    JOIN opds_catalog_book AS b ON b.id = ba.book_id
        AND b.approved
        AND NOT b.duplicate_hidden
        AND (? = '' OR b.lang = ?)
    GROUP BY m.id, m.full_name
),
page AS (
    SELECT c.id, c.full_name, c.books_count,
        row_number() OVER (
            ORDER BY (SELECT q.needle FROM q) <<-> public.search_normalize(c.full_name) ASC,
                c.books_count DESC,
                c.id ASC
        ) AS pos
    FROM counted c
    ORDER BY pos
    LIMIT ? OFFSET ?
),
meta AS (
    SELECT
        (SELECT count(*) FROM counted) AS total,
        (SELECT q.query_hash FROM q) AS query_hash
)
SELECT p.id, p.full_name, p.books_count, m.total, m.query_hash
FROM meta m
LEFT JOIN page p ON true
ORDER BY p.pos
`

// searchAuthorRow is one row of authorSearchRepositorySQL: a page entry, or
// the metadata row with a NULL id when the page is empty.
type searchAuthorRow struct {
	ID         *int64 `pg:"id"`
	FullName   string `pg:"full_name"`
	BooksCount int    `pg:"books_count"`
	Total      int    `pg:"total"`
	QueryHash  string `pg:"query_hash"`
}

// SearchAuthors returns one page of authors ranked by name distance plus the
// exact pre-pagination total.
func (r *PGSearchRepository) SearchAuthors(ctx context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	page := models.AuthorSearchPage{Limit: req.Limit, Offset: req.Offset}

	var rows []searchAuthorRow
	if _, err := r.db.QueryContext(ctx, &rows, authorSearchRepositorySQL,
		req.Query, req.Query,
		req.Language, req.Language,
		req.Limit, req.Offset); err != nil {
		return page, preferContextError(ctx, err)
	}

	for _, row := range rows {
		page.Total = row.Total
		page.QueryHash = row.QueryHash
		if row.ID != nil {
			page.Authors = append(page.Authors, models.Author{
				ID:         *row.ID,
				FullName:   row.FullName,
				BooksCount: row.BooksCount,
			})
		}
	}
	return page, nil
}

// Autocomplete geometry: a single kind answers up to defaultSuggestionLimit
// rows, while the combined picker halves the same budget between a book shelf
// and an author shelf so neither lane can shout the other down. The halves are
// computed rather than named — a constant pair beside the budget is a second
// source of truth, and it was already wrong for every budget but the default.
const (
	defaultSuggestionLimit = 15
	suggestionLaneBook     = 1
	suggestionLaneAuthor   = 2

	// laneCount is how many shelves the combined budget is split between, and
	// bookLaneRoundUp gives books the odd row.
	laneCount       = 2
	bookLaneRoundUp = 1

	// The suggestion kinds as the picker labels them.
	suggestionTypeAuthor = "author"
	suggestionTypeBook   = "book"
)

// suggestionSQL drives the autocomplete picker: the same normalization,
// visibility and language semantics as the search paths, but a compact list
// meant to be chosen from, not browsed. Book lanes mirror SearchBooks —
// exact/prefix/substring share one WHERE so the planner collapses the OR into
// a single BitmapOr over the title indexes, while %> and % keep their own
// UNION ALL legs — with every lane gated at three runes, because a shorter
// prefix is the reader still typing, not a fuzzy query. The author lane is
// substring LIKE plus %> only: the whole statement runs under the
// book-search SET LOCAL floor of 0.5, and the % operator would inherit it,
// silently dropping abbreviated names ("Толкин Дж." scores 0.360); word
// similarity follows the separate word_similarity_threshold GUC, which the
// book threshold does not touch.
//
// Same-title reimports collapse before the limit, not after: books dedupe by
// (normalized title, language), so five hundred copies of one title fill one
// slot — and so do fifteen different writers who each called a book "Война".
// The author was once part of the key, which made those fifteen fifteen rows;
// they looked like a choice and were not, because picking any of them runs the
// same search for the same words. The row carries how many books share the
// title instead, which is what the reader is about to see. Language stays in
// the key: two editions of one title in two languages are genuinely two
// choices. Authors with no visible book
// under the reader's language drop out through the inner join, because a
// suggestion that opens an empty list is a dead end offered as a choice.
//
// Both shelves rank strongest signal first with id as the final tiebreak, so
// two runs of the same keystrokes produce the same picker. meta always
// produces a row, so an empty picker still carries the correlation hash.
const suggestionSQL = `
WITH q AS (
    SELECT public.search_normalize(?::text) AS needle,
        char_length(public.search_normalize(?::text)) AS rune_count,
        md5(public.search_normalize(?::text)) AS query_hash
),
visible_books AS NOT MATERIALIZED (
    -- The same scopes the book search honors, for the same reason: a picker
    -- offering titles from the whole catalog to a reader standing in their
    -- favorites offers books that list cannot show. Every row here leads to
    -- a search that stays in the list, so the rows come from it too. Written
    -- as correlated EXISTS, like the search, so an unknown id matches nothing
    -- rather than widening back to everything.
    SELECT b.id, b.lang, public.search_normalize(b.title) AS norm_title
    FROM opds_catalog_book AS b
    WHERE b.approved
        AND NOT b.duplicate_hidden
        AND (?::text = '' OR ?::text = 'all' OR b.lang = ?::text)
        AND (NOT ?::bool OR EXISTS (
            SELECT 1 FROM favorite_books AS fb
            WHERE fb.book_id = b.id AND fb.user_id = ?
        ))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM opds_catalog_bauthor AS ba
            WHERE ba.book_id = b.id AND ba.author_id = ?
        ))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM opds_catalog_bseries AS bs
            WHERE bs.book_id = b.id AND bs.ser_id = ?
        ))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM opds_catalog_bgenre AS bg
            WHERE bg.book_id = b.id AND bg.genre_id = ?
        ))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM book_collection_books AS bcb
            WHERE bcb.book_id = b.id AND bcb.book_collection_id = ?
        ))
        AND (? = 0 OR EXISTS (
            SELECT 1 FROM book_collection_items AS ci
            JOIN book_collections AS c ON c.id = ci.collection_id
            WHERE ci.book_id = b.id AND ci.collection_id = ?
                AND c.is_curated AND c.is_public
                AND ci.match_status IN ('auto_matched', 'manual')
        ))
),
book_hits AS (
    SELECT v.id, v.lang, v.norm_title
    FROM visible_books AS v
    WHERE ? IN ('all', 'title')
        AND (SELECT q.rune_count FROM q) >= 3
        AND (v.norm_title = (SELECT q.needle FROM q)
            OR v.norm_title LIKE (SELECT q.needle FROM q) || '%'
            OR v.norm_title LIKE '%' || (SELECT q.needle FROM q) || '%')
    UNION ALL
    SELECT v.id, v.lang, v.norm_title
    FROM visible_books AS v
    WHERE ? IN ('all', 'title')
        AND (SELECT q.rune_count FROM q) >= 3
        AND v.norm_title %> (SELECT q.needle FROM q)
    UNION ALL
    SELECT v.id, v.lang, v.norm_title
    FROM visible_books AS v
    WHERE ? IN ('all', 'title')
        AND (SELECT q.rune_count FROM q) >= 3
        AND v.norm_title % (SELECT q.needle FROM q)
),
book_candidates AS (
    SELECT h.id, h.lang, h.norm_title
    FROM book_hits AS h
    GROUP BY h.id, h.lang, h.norm_title
),
book_signaled AS (
    SELECT c.id, c.lang, c.norm_title,
        (c.norm_title = q.needle) AS exact_match,
        (c.norm_title LIKE q.needle || '%') AS prefix_match,
        NULLIF(strpos(c.norm_title, q.needle), 0) AS match_position,
        word_similarity(q.needle, c.norm_title) AS word_score,
        similarity(c.norm_title, q.needle) AS trigram_score
    FROM book_candidates AS c
    CROSS JOIN q
),
book_ranked AS (
    SELECT s.id, s.lang, s.norm_title,
        row_number() OVER (
            ORDER BY s.exact_match DESC, s.prefix_match DESC,
                s.match_position ASC NULLS LAST,
                s.word_score DESC, s.trigram_score DESC, s.id ASC
        ) AS pos
    FROM book_signaled AS s
    WHERE s.exact_match
        OR s.prefix_match
        OR s.match_position IS NOT NULL
        OR s.word_score >= 0.60
        OR s.trigram_score >= 0.50
),
book_deduped AS (
    -- One row per title per language, whoever wrote it.
    --
    -- The author used to be part of the key, so fifteen writers with a book
    -- called "Война" filled the picker with fifteen identical-looking rows.
    -- Picking any of them runs the same search — the picker chooses words, not
    -- a copy — so the repetition offered a choice that did not exist. What
    -- distinguishes the row now is how many books carry the title, which is
    -- what the reader is about to be shown.
    SELECT r.id, r.norm_title, r.pos,
        count(*) OVER (PARTITION BY r.norm_title, r.lang) AS copies,
        row_number() OVER (
            PARTITION BY r.norm_title, r.lang
            ORDER BY r.pos
        ) AS copy_pos
    FROM book_ranked AS r
),
books_page AS (
    SELECT d.id, b.title, NULL::text AS secondary, d.copies, d.pos
    FROM book_deduped AS d
    JOIN opds_catalog_book AS b ON b.id = d.id
    WHERE d.copy_pos = 1
    ORDER BY d.pos
    LIMIT ?
),
author_matched AS (
    SELECT a.id, a.full_name, public.search_normalize(a.full_name) AS norm_name
    FROM opds_catalog_author AS a
    WHERE ? IN ('all', 'author')
        AND (SELECT q.rune_count FROM q) >= 3
        AND (public.search_normalize(a.full_name) LIKE '%' || (SELECT q.needle FROM q) || '%'
            OR public.search_normalize(a.full_name) %> (SELECT q.needle FROM q))
),
author_counted AS (
    SELECT m.id, m.full_name, m.norm_name, count(b.id) AS books_count
    FROM author_matched AS m
    JOIN opds_catalog_bauthor AS ba ON ba.author_id = m.id
    JOIN opds_catalog_book AS b ON b.id = ba.book_id
        AND b.approved
        AND NOT b.duplicate_hidden
        AND (?::text = '' OR ?::text = 'all' OR b.lang = ?::text)
    GROUP BY m.id, m.full_name, m.norm_name
),
authors_page AS (
    SELECT c.id, c.full_name, c.books_count,
        row_number() OVER (
            ORDER BY (c.norm_name = (SELECT q.needle FROM q)) DESC,
                (c.norm_name LIKE (SELECT q.needle FROM q) || '%') DESC,
                (SELECT q.needle FROM q) <<-> c.norm_name ASC,
                c.books_count DESC, c.id ASC
        ) AS pos
    FROM author_counted AS c
    ORDER BY pos
    LIMIT ?
),
combined AS (
    SELECT 1 AS lane, bp.pos, bp.id, bp.title AS value, bp.secondary, bp.copies AS books_count
    FROM books_page AS bp
    UNION ALL
    SELECT 2 AS lane, ap.pos, ap.id, ap.full_name, NULL::text, ap.books_count
    FROM authors_page AS ap
),
meta AS (
    SELECT (SELECT q.query_hash FROM q) AS query_hash
)
SELECT c.lane, c.pos, c.id, c.value, c.secondary, c.books_count, m.query_hash
FROM meta AS m
LEFT JOIN combined AS c ON true
ORDER BY c.lane, c.pos
`

// suggestionRow is one row of suggestionSQL: a picker entry, or the metadata
// row with NULL entry fields when the picker is empty.
type suggestionRow struct {
	Lane       *int    `pg:"lane"`
	Pos        *int64  `pg:"pos"`
	ID         *int64  `pg:"id"`
	Value      *string `pg:"value"`
	Secondary  *string `pg:"secondary"`
	BooksCount *int    `pg:"books_count"`
	QueryHash  string  `pg:"query_hash"`
}

// suggestionLaneLimits splits the picker budget between the book and author
// lanes. The combined kind ignores the caller's limit and takes the fixed
// shelf sizes; a single kind answers up to its own limit.
//
//nolint:gocritic // named results document which lane each number is
func suggestionLaneLimits(kind models.SuggestionKind, limit int) (books, authors int) {
	if limit <= 0 {
		limit = defaultSuggestionLimit
	}
	switch kind {
	case models.SuggestionBook:
		return limit, 0
	case models.SuggestionAuthor:
		return 0, limit
	case models.SuggestionAll:
		fallthrough
	default: //nolint:gocritic // the explicit SuggestionAll case documents the exhaustive set
		// Split rather than fixed shelves: the two used to be constants that
		// ignored the budget, so a picker asked for five suggestions answered
		// with fifteen. Rounding up gives books the odd row, which is where the
		// default fifteen gets its eight and seven — the geometry the picker
		// was built around is the formula's own answer, not a second copy of it.
		books = (limit + bookLaneRoundUp) / laneCount
		return books, limit - books
	}
}

// Suggestions returns the autocomplete picker for one prefix. The statement
// runs inside the book-search threshold transaction so the % lane keeps its
// 0.5 floor; the author lane deliberately avoids % and is unaffected.
func (r *PGSearchRepository) Suggestions(ctx context.Context, req models.SuggestionRequest) (models.SuggestionResult, error) {
	result := models.SuggestionResult{Suggestions: []models.AutocompleteSuggestion{}}
	bookLimit, authorLimit := suggestionLaneLimits(req.Kind, req.Limit)
	kind := string(req.Kind)

	var rows []suggestionRow
	query := func(q pg.DBI) error {
		_, err := q.QueryContext(ctx, &rows, suggestionSQL,
			req.Query, req.Query, req.Query,
			req.Language, req.Language, req.Language,
			req.Favorites, req.UserID,
			req.AuthorID, req.AuthorID,
			req.SeriesID, req.SeriesID,
			req.GenreID, req.GenreID,
			req.CollectionID, req.CollectionID,
			req.CuratedCollectionID, req.CuratedCollectionID,
			kind, kind, kind,
			bookLimit,
			kind,
			req.Language, req.Language, req.Language,
			authorLimit)
		return err
	}
	if err := r.queryWithBookThreshold(ctx, query); err != nil {
		return result, preferContextError(ctx, err)
	}

	for _, row := range rows {
		result.QueryHash = row.QueryHash
		if row.ID == nil || row.Lane == nil {
			continue
		}
		suggestion := models.AutocompleteSuggestion{ID: *row.ID}
		if row.Value != nil {
			suggestion.Value = *row.Value
		}
		if row.Secondary != nil {
			suggestion.Secondary = *row.Secondary
		}
		if row.BooksCount != nil {
			suggestion.BooksCount = *row.BooksCount
		}
		if *row.Lane == suggestionLaneAuthor {
			suggestion.Type = suggestionTypeAuthor
		} else {
			suggestion.Type = suggestionTypeBook
		}
		result.Suggestions = append(result.Suggestions, suggestion)
	}
	return result, nil
}
