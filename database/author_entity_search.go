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
// whose books are all filtered away is left out. Nothing a reader sees calls
// it yet (design E, phase E2): E3 switches the surfaces.
const entitySearchSQL = `
WITH q AS (SELECT public.search_normalize(?::text) AS needle),
hits AS (
    SELECT n.entity_id, min((SELECT q.needle FROM q) <<-> n.search_key) AS distance
    FROM author_entity_name AS n
    WHERE n.search_key % (SELECT q.needle FROM q)
        OR n.search_key %> (SELECT q.needle FROM q)
    GROUP BY n.entity_id
),
counted AS (
    -- Per candidate, through the GIN index on entity_ids: a join over all
    -- the rows is misestimated and hashes the whole book table.
    SELECT h.entity_id, h.distance, books.n AS books
    FROM hits AS h
    CROSS JOIN LATERAL (
        SELECT count(DISTINCT d.book_id) AS n
        FROM book_author_display AS d
        JOIN opds_catalog_book AS b ON b.id = d.book_id
            AND b.approved
            AND NOT b.duplicate_hidden
            AND (? = '' OR b.lang = ?)
        WHERE d.entity_ids @> ARRAY[h.entity_id]) AS books
    WHERE books.n > 0
        AND EXISTS (SELECT 1 FROM author_entity AS e WHERE e.id = h.entity_id AND e.merged_into IS NULL)
),
page AS (
    SELECT c.entity_id, c.books, c.distance FROM counted AS c
    ORDER BY c.distance, c.books DESC, c.entity_id
    LIMIT ? OFFSET ?
)
SELECT p.entity_id AS id, e.display_name, e.kind, p.books
FROM page AS p
JOIN author_entity AS e ON e.id = p.entity_id
ORDER BY p.distance, p.books DESC, p.entity_id`

// AuthorEntityHit is one entity a search found.
type AuthorEntityHit struct {
	ID          uuid.UUID `pg:"id,type:uuid"`
	DisplayName string    `pg:"display_name"`
	Kind        string    `pg:"kind"`
	Books       int       `pg:"books,use_zero"`
}

// SearchAuthorEntities is one page of the entities whose names match the
// query, closest first.
func SearchAuthorEntities(
	ctx context.Context, db pg.DBI, query, language string, limit, offset int,
) ([]AuthorEntityHit, error) {
	var hits []AuthorEntityHit
	if _, err := db.QueryContext(ctx, &hits, entitySearchSQL, query, language, language, limit, offset); err != nil {
		return nil, fmt.Errorf("searching the author entities: %w", err)
	}
	return hits, nil
}
