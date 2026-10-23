package database

import (
	"context"
	"fmt"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// entitySearchSQL finds author entities by any of their name variants
// (author_entity_name, the read-model worker's) and counts the books each
// one would open, with the filters the book list applies: approved, not a
// hidden duplicate, the reader's language. Candidates come from both
// trigram operators, as in the legacy author search (authorSearchRepositorySQL);
// an entity ranks by its closest name, then by books, then by ID. An entity
// whose books are all filtered away is left out, and the total counts the
// entities that remain.
//
// Rank, then count. A broad query matches thousands of entities ('ан':
// 9,861 on the catalog), and counting the books of each one was most of the
// query. The total only needs to know that an entity has a visible book, a
// probe that stops at the first one (visible). The page needs the counts
// only of the entities it can be drawn from: distance comes first in the
// order, so those are the entities no farther than the last one the page
// reaches in distance order (cut), ties included — books then order them
// exactly as counting them all would. Each lookup goes per entity through
// the GIN index on entity_ids: a join over all the rows is misestimated and
// hashes the whole book table, and one overlap scan with every candidate is
// slower still.
//
// The CTEs are materialized so that each probe runs once per entity. Liveness
// is checked after grouping: checked per name row, it turns into a loop over
// every entity. Both probes of visible are scalar subqueries, never joins: the
// planner estimates the GIN probe at a few dozen survivors of thousands, and
// given a join it chose a nested loop of every candidate against every entity
// (ninety seconds for 'ан' once the tables had statistics). A scalar subquery
// cannot be flattened, so each stays one index lookup per candidate.
const entitySearchSQL = `
WITH q AS (SELECT public.search_normalize(?0::text) AS needle),
named AS MATERIALIZED (
    SELECT n.entity_id, min((SELECT q.needle FROM q) <<-> n.search_key) AS distance
    FROM author_entity_name AS n
    WHERE n.search_key % (SELECT q.needle FROM q)
        OR n.search_key %> (SELECT q.needle FROM q)
    GROUP BY n.entity_id
),
visible AS MATERIALIZED (
    SELECT h.entity_id, h.distance
    FROM named AS h
    WHERE (SELECT e.merged_into IS NULL FROM author_entity AS e WHERE e.id = h.entity_id)
        AND (SELECT true
            FROM opds_catalog_book AS b
            WHERE b.id = ANY (ARRAY(
                    SELECT d.book_id FROM book_author_display AS d WHERE d.entity_ids @> ARRAY[h.entity_id]))
                AND b.approved
                AND NOT b.duplicate_hidden
                AND (?1 = '' OR b.lang = ?1)
            LIMIT 1)
),
cut AS (
    SELECT v.distance FROM visible AS v
    ORDER BY v.distance
    LIMIT 1 OFFSET greatest(?2 + ?3 - 1, 0)
),
counted AS (
    SELECT v.entity_id, v.distance, books.n AS books
    FROM visible AS v
    CROSS JOIN LATERAL (
        SELECT count(DISTINCT d.book_id) AS n
        FROM book_author_display AS d
        JOIN opds_catalog_book AS b ON b.id = d.book_id
            AND b.approved
            AND NOT b.duplicate_hidden
            AND (?1 = '' OR b.lang = ?1)
        WHERE d.entity_ids @> ARRAY[v.entity_id]) AS books
    WHERE v.distance <= coalesce((SELECT c.distance FROM cut AS c), 'infinity')
),
page AS (
    SELECT c.entity_id, c.books, c.distance FROM counted AS c
    ORDER BY c.distance, c.books DESC, c.entity_id
    LIMIT ?2 OFFSET ?3
)
SELECT p.entity_id AS id, e.display_name, e.kind, p.books, (SELECT count(*) FROM visible) AS total,
    (SELECT md5(q.needle) FROM q) AS query_hash
FROM (SELECT 1) AS meta
LEFT JOIN page AS p ON true
LEFT JOIN author_entity AS e ON e.id = p.entity_id
ORDER BY p.distance, p.books DESC, p.entity_id`

// AuthorEntityHit is one entity a search found.
type AuthorEntityHit struct {
	ID          uuid.UUID `pg:"id,type:uuid"`
	DisplayName string    `pg:"display_name"`
	Kind        string    `pg:"kind"`
	Books       int       `pg:"books,use_zero"`
}

// AuthorEntityPage is one page of an entity search and how many entities
// the search found in all; QueryHash correlates log lines without the query,
// as the other searches' does.
type AuthorEntityPage struct {
	Hits      []AuthorEntityHit
	Total     int
	QueryHash string
}

// entitySearchRow is one row of entitySearchSQL: a hit, or a row with a nil
// ID that carries only the total when the page is empty.
type entitySearchRow struct {
	ID          *uuid.UUID `pg:"id,type:uuid"`
	DisplayName string     `pg:"display_name"`
	Kind        string     `pg:"kind"`
	Books       int        `pg:"books,use_zero"`
	Total       int        `pg:"total,use_zero"`
	QueryHash   string     `pg:"query_hash"`
}

// SearchAuthorEntities is one page of the entities whose names match the
// query, closest first, and their total.
func SearchAuthorEntities(
	ctx context.Context, db pg.DBI, query, language string, limit, offset int,
) (AuthorEntityPage, error) {
	var rows []entitySearchRow
	if _, err := db.QueryContext(ctx, &rows, entitySearchSQL, query, language, limit, offset); err != nil {
		return AuthorEntityPage{}, fmt.Errorf("searching the author entities: %w", err)
	}
	var page AuthorEntityPage
	for _, r := range rows {
		page.Total, page.QueryHash = r.Total, r.QueryHash
		if r.ID != nil {
			page.Hits = append(page.Hits, AuthorEntityHit{ID: *r.ID, DisplayName: r.DisplayName, Kind: r.Kind, Books: r.Books})
		}
	}
	return page, nil
}

// entitySuggestionSQL is the author shelf of the autocomplete picker under
// the entity identity: the legacy shelf's rules (suggestionSQL) over the
// entities' names — a name containing the prefix or word-similar to it, one
// row per entity, an entity with no listed book in the reader's language
// left out — ranked exact name first, then a name starting with the prefix,
// then word distance, books and ID.
//
// It ranks before it counts, as entitySearchSQL does, and a shelf has no
// total, so it does not even probe every candidate: cut walks them in rank
// order and stops at the last row the shelf can show, and only the entities
// no later than that one are counted — ties included, since books order
// them. A common word ties thousands at distance 0 ('александр': 4,463 of
// 8,447), and those are counted all the same; the walk spares probing the
// rest. The OFFSET 0 keeps the walk's filter above its sort.
const entitySuggestionSQL = `
WITH q AS (SELECT public.search_normalize(?0::text) AS needle),
named AS MATERIALIZED (
    SELECT n.entity_id,
        NOT bool_or(n.search_key = (SELECT q.needle FROM q)) AS not_exact,
        NOT bool_or(n.search_key LIKE (SELECT q.needle FROM q) || '%') AS not_prefix,
        min((SELECT q.needle FROM q) <<-> n.search_key) AS distance
    FROM author_entity_name AS n
    WHERE n.search_key LIKE '%' || (SELECT q.needle FROM q) || '%'
        OR n.search_key %> (SELECT q.needle FROM q)
    GROUP BY n.entity_id
),
cut AS (
    SELECT o.not_exact, o.not_prefix, o.distance
    FROM (SELECT h.* FROM named AS h ORDER BY h.not_exact, h.not_prefix, h.distance OFFSET 0) AS o
    WHERE (SELECT e.merged_into IS NULL FROM author_entity AS e WHERE e.id = o.entity_id)
        AND (SELECT true
            FROM opds_catalog_book AS b
            WHERE b.id = ANY (ARRAY(
                    SELECT d.book_id FROM book_author_display AS d WHERE d.entity_ids @> ARRAY[o.entity_id]))
                AND b.approved
                AND NOT b.duplicate_hidden
                AND (?1 IN ('', 'all') OR b.lang = ?1)
            LIMIT 1)
    LIMIT 1 OFFSET greatest(?2 - 1, 0)
),
counted AS (
    SELECT h.entity_id, h.not_exact, h.not_prefix, h.distance, books.n AS books
    FROM named AS h
    CROSS JOIN LATERAL (
        SELECT count(DISTINCT d.book_id) AS n
        FROM book_author_display AS d
        JOIN opds_catalog_book AS b ON b.id = d.book_id
            AND b.approved
            AND NOT b.duplicate_hidden
            AND (?1 IN ('', 'all') OR b.lang = ?1)
        WHERE d.entity_ids @> ARRAY[h.entity_id]) AS books
    WHERE (NOT EXISTS (SELECT 1 FROM cut)
            OR (h.not_exact, h.not_prefix, h.distance) <= (SELECT c.not_exact, c.not_prefix, c.distance FROM cut AS c))
        AND books.n > 0
        AND (SELECT e.merged_into IS NULL FROM author_entity AS e WHERE e.id = h.entity_id)
)
SELECT c.entity_id AS id, e.display_name, e.kind, c.books
FROM counted AS c
JOIN author_entity AS e ON e.id = c.entity_id
ORDER BY c.not_exact, c.not_prefix, c.distance, c.books DESC, c.entity_id
LIMIT ?2`

// SuggestAuthorEntities is the author shelf of the picker under the entity
// identity: at most limit entities for the prefix.
func SuggestAuthorEntities(ctx context.Context, db pg.DBI, query, language string, limit int) ([]AuthorEntityHit, error) {
	var hits []AuthorEntityHit
	if limit <= 0 {
		return hits, nil
	}
	if _, err := db.QueryContext(ctx, &hits, entitySuggestionSQL, query, language, limit); err != nil {
		return nil, fmt.Errorf("suggesting author entities: %w", err)
	}
	return hits, nil
}
