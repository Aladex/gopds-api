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
//  3. all_words_match — the title consists of the needle's words, in any
//     order, each as a whole word or word prefix (both directions checked,
//     so a longer title that merely contains the needle is not here);
//  4. substring_match — the needle occurs inside the normalized title;
//  5. word_score — word_similarity of the needle against the title;
//  6. trigram_score — plain trigram similarity for typos.
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
    -- instead of computing the normalized title for the whole catalogue.
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
    SELECT v.id, v.norm_title, 32
    FROM visible v
    WHERE (SELECT q.exact_id FROM q) = 0
        AND (SELECT q.rune_count FROM q) >= 3
        AND v.norm_title LIKE '%' || split_part((SELECT q.needle FROM q), ' ', 1) || '%'
        AND NOT EXISTS (
            SELECT 1 FROM unnest(string_to_array((SELECT q.needle FROM q), ' ')) AS nw(word)
            WHERE NOT EXISTS (
                SELECT 1 FROM unnest(string_to_array(v.norm_title, ' ')) AS tw(word)
                WHERE tw.word LIKE nw.word || '%'))
        AND NOT EXISTS (
            SELECT 1 FROM unnest(string_to_array(v.norm_title, ' ')) AS tw(word)
            WHERE NOT EXISTS (
                SELECT 1 FROM unnest(string_to_array((SELECT q.needle FROM q), ' ')) AS nw(word)
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
    -- Only all_words_match comes from the lane bitmask; recomputing the
    -- word-coverage anti-joins per candidate was the dominant cost here.
    -- Every other signal stays a pure function of the candidate row —
    -- including the raw similarity scores, because the %> / % lanes are
    -- strict while the admission floors below are inclusive, so a gated
    -- score would move the boundary.
    SELECT
        c.id,
        (c.norm_title = q.needle) AS exact_match,
        (c.norm_title LIKE q.needle || '%') AS prefix_match,
        (c.lanes & 32 <> 0) AS all_words_match,
        (strpos(c.norm_title, q.needle) > 0) AS substring_match,
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
        OR s.exact_match OR s.prefix_match OR s.all_words_match OR s.substring_match
        OR s.word_score >= 0.60
        OR s.trigram_score >= 0.30
),
ranked AS (
    SELECT a.id,
        row_number() OVER (
            ORDER BY
                a.exact_match DESC,
                a.prefix_match DESC,
                a.all_words_match DESC,
                a.substring_match DESC,
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
// loading query carries no visibility filter and the catalogue scanner deletes
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
func (r *PGSearchRepository) SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	page := models.BookSearchPage{Limit: req.Limit, Offset: req.Offset}

	var rows []searchBookRow
	_, err := r.db.QueryContext(ctx, &rows, bookSearchSQL,
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
	if err != nil {
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

// markCallerFavorites sets Fav on the page books the caller favorited. The
// global FavoriteCount is part of the hydration query; Fav is caller-specific
// and is resolved here, for the page IDs only.
func (r *PGSearchRepository) markCallerFavorites(ctx context.Context, userID int64, books []models.Book) error {
	if userID == 0 || len(books) == 0 {
		return nil
	}
	ids := make([]int64, len(books))
	for i, b := range books {
		ids[i] = b.ID
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
