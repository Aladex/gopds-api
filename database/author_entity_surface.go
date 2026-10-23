package database

import (
	"context"
	"errors"
	"fmt"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
	"github.com/google/uuid"
)

// ErrAuthorEntityNotFound is an entity ID that names no live entity: one
// that never existed, or one merged into another. A merged entity's page is
// gone, not redirected (design E, §4.2).
var ErrAuthorEntityNotFound = errors.New("database: no such author entity")

// dominantGenreSQL is the genre most of an entity's listed books (approved,
// not a hidden duplicate, any language) carry, ties to the lower genre ID;
// no row when its books carry no genre. %s is the entity's ID. The genre's
// title is a production-only column (see scanfixture.ScratchDB), read
// through to_jsonb so the statement runs on a schema without it; the code
// stands in for a missing or empty title.
const dominantGenreSQL = `
        SELECT g.id, coalesce(nullif(to_jsonb(g) ->> 'title', ''), g.genre) AS title
        FROM book_author_display AS d
        JOIN opds_catalog_book AS b ON b.id = d.book_id AND b.approved AND NOT b.duplicate_hidden
        JOIN opds_catalog_bgenre AS bg ON bg.book_id = d.book_id
        JOIN opds_catalog_genre AS g ON g.id = bg.genre_id
        WHERE d.entity_ids @> ARRAY[%s]
        GROUP BY g.id
        ORDER BY count(DISTINCT d.book_id) DESC, g.id
        LIMIT 1`

// liveAuthorEntitiesSQL reads live entities with their namesake labels
// (models.AuthorEntity, design E §1.1, §6 item 3a). An entity is labeled
// only when another live entity carries the same display name: by its
// dominant genre when no such namesake has the same one, else by the number
// of its listed books.
var liveAuthorEntitiesSQL = `
SELECT e.id, e.display_name AS full_name,
    CASE WHEN NOT EXISTS (
            SELECT 1 FROM author_entity AS o
            WHERE o.display_name = e.display_name AND o.merged_into IS NULL AND o.id <> e.id
                AND (SELECT og.id FROM (` + fmt.Sprintf(dominantGenreSQL, "o.id") + `) AS og) = mine.id)
        THEN mine.title END AS label,
    (SELECT count(DISTINCT d.book_id)
        FROM book_author_display AS d
        JOIN opds_catalog_book AS b ON b.id = d.book_id AND b.approved AND NOT b.duplicate_hidden
        WHERE d.entity_ids @> ARRAY[e.id]) AS books
FROM author_entity AS e
LEFT JOIN LATERAL (` + fmt.Sprintf(dominantGenreSQL, "e.id") + `) AS mine ON true
WHERE e.id IN (?) AND e.merged_into IS NULL
    AND EXISTS (
        SELECT 1 FROM author_entity AS o
        WHERE o.display_name = e.display_name AND o.merged_into IS NULL AND o.id <> e.id)
UNION ALL
SELECT e.id, e.display_name, NULL, NULL
FROM author_entity AS e
WHERE e.id IN (?) AND e.merged_into IS NULL
    AND NOT EXISTS (
        SELECT 1 FROM author_entity AS o
        WHERE o.display_name = e.display_name AND o.merged_into IS NULL AND o.id <> e.id)`

// authorEntityRow is one row of liveAuthorEntitiesSQL; Books is set for a
// namesake only.
type authorEntityRow struct {
	ID       uuid.UUID `pg:"id,type:uuid"`
	FullName string    `pg:"full_name"`
	Label    *string   `pg:"label"`
	Books    *int      `pg:"books"`
}

// LiveAuthorEntities returns the live entities among ids, labeled; an ID of
// no live entity has no entry.
func LiveAuthorEntities(ctx context.Context, db pg.DBI, ids []uuid.UUID) (map[uuid.UUID]models.AuthorEntity, error) {
	out := map[uuid.UUID]models.AuthorEntity{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []authorEntityRow
	if _, err := db.QueryContext(ctx, &rows, liveAuthorEntitiesSQL, pg.In(ids), pg.In(ids)); err != nil {
		return nil, fmt.Errorf("reading %d author entities: %w", len(ids), err)
	}
	for _, r := range rows {
		e := models.AuthorEntity{ID: r.ID, FullName: r.FullName}
		switch {
		case r.Label != nil:
			e.Label = *r.Label
		case r.Books != nil:
			e.LabelBooks = *r.Books
		}
		out[r.ID] = e
	}
	return out, nil
}

// AuthorEntityByID returns one live entity, labeled, or
// ErrAuthorEntityNotFound.
func AuthorEntityByID(ctx context.Context, db pg.DBI, id uuid.UUID) (models.AuthorEntity, error) {
	if id == uuid.Nil {
		return models.AuthorEntity{}, ErrAuthorEntityNotFound
	}
	found, err := LiveAuthorEntities(ctx, db, []uuid.UUID{id})
	if err != nil {
		return models.AuthorEntity{}, err
	}
	e, ok := found[id]
	if !ok {
		return models.AuthorEntity{}, ErrAuthorEntityNotFound
	}
	return e, nil
}

// EntityLines turns a page of author lines into the entity identity's: each
// name links to the live entities it names (models.WithEntities), read for
// the whole page at once.
func EntityLines(ctx context.Context, db pg.DBI, lines [][]models.AuthorDisplay) ([][]models.AuthorDisplay, error) {
	var ids []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, line := range lines {
		for _, a := range line {
			for _, raw := range a.EntityIDs {
				if id, err := uuid.Parse(raw); err == nil && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}
	entities, err := LiveAuthorEntities(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	out := make([][]models.AuthorDisplay, len(lines))
	for i, line := range lines {
		out[i] = models.WithEntities(line, entities)
	}
	return out, nil
}

// narrowByAuthorEntity restricts the list to the books whose author line
// names the entity, through the GIN index on entity_ids. As with a junction
// scope (narrowByJunction), no such book matches nothing.
func narrowByAuthorEntity(query *orm.Query, entity uuid.UUID) (*orm.Query, error) {
	if entity == uuid.Nil {
		return query, nil
	}
	var bookIDs []int64
	if _, err := db.Query(&bookIDs, `SELECT DISTINCT book_id FROM book_author_display
		WHERE entity_ids @> ARRAY[?::uuid]`, entity); err != nil {
		return nil, fmt.Errorf("resolving the books of author entity %s: %w", entity, err)
	}
	if len(bookIDs) == 0 {
		return query.Where("1 = 0"), nil
	}
	return query.WhereIn("book.id IN (?)", bookIDs), nil
}

// EntityLines turns a page of author lines into the entity identity's
// (services.AuthorEntityLinks).
func (r *PGBookSourceRepository) EntityLines(
	ctx context.Context, lines [][]models.AuthorDisplay,
) ([][]models.AuthorDisplay, error) {
	return EntityLines(ctx, r.db, lines)
}

// AuthorEntity returns one live entity, labeled, or ErrAuthorEntityNotFound.
func (r *PGBookSourceRepository) AuthorEntity(ctx context.Context, id uuid.UUID) (models.AuthorEntity, error) {
	return AuthorEntityByID(ctx, r.db, id)
}
