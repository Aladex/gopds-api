package scanfixture

import (
	"context"
	"testing"

	"gopds-api/database"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// Entities are the author entities LinkEntities gives the characterization
// books (design E): one for each of two credits of the multi-author book,
// two for its "Аноним" credit as if it named two people (§6 item 5), and a
// namesake of the first with no books, which earns it its label.
// The entities' display names that recur in the list below.
const (
	entityTolstoy = "Лев Николаевич Толстой"
	entityPushkin = "Пушкин"
	sharedAnonym  = "Аноним"
	entityAnonym1 = "Аноним Первый"
	entityAnonym2 = "Аноним Второй"
)

var Entities = []struct {
	Name, Display string
	// Credit is the name the book's author line shows for the credit the
	// entity is assigned to; empty for an entity no credit names.
	Credit string
}{
	{"Толстой", entityTolstoy, "лев Николаевич Толстой"},
	{"Толстой-тёзка", entityTolstoy, ""},
	{entityPushkin, entityPushkin, "пушкин"},
	{entityAnonym1, entityAnonym1, sharedAnonym},
	{entityAnonym2, entityAnonym2, sharedAnonym},
}

// LinkEntities makes Entities, assigns them to the ingested books' credits
// and rebuilds the read model, so every line links its names to them; it
// returns the entities' UUIDs by name.
func LinkEntities(t *testing.T, db *pg.DB) map[string]uuid.UUID {
	t.Helper()
	ctx := context.Background()
	rebuild := func() {
		t.Helper()
		if err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			var books []int64
			if _, err := tx.Query(&books, `SELECT id FROM opds_catalog_book`); err != nil {
				return err
			}
			_, err := database.RebuildAuthorDisplay(ctx, tx, books)
			return err
		}); err != nil {
			t.Fatalf("rebuilding the read model: %v", err)
		}
	}
	// The credits are found by the names the line shows.
	rebuild()
	ids := map[string]uuid.UUID{}
	ordinal := map[string]int{}
	for _, e := range Entities {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		ids[e.Name] = id
		if _, err = db.Exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
			VALUES (?, 'person', ?, ?, public.search_normalize(?))`, id, e.Display, e.Display, e.Display); err != nil {
			t.Fatalf("making entity %s: %v", e.Name, err)
		}
		if e.Credit == "" {
			continue
		}
		res, err := db.Exec(`INSERT INTO author_entity_assignment
			(source_fingerprint, extractor_version, ordinal, entity_id, method, rule)
			SELECT DISTINCT c.source_fingerprint, s.extractor_version, ?::smallint, ?::uuid, 'deterministic', 'D1'
			FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			JOIN book_author_display d ON d.credit_id = c.id
			WHERE c.role = 'author' AND d.display = ?`, ordinal[e.Credit], id, e.Credit)
		if err != nil || res.RowsAffected() == 0 {
			t.Fatalf("assigning entity %s to the credit %q: %v", e.Name, e.Credit, err)
		}
		ordinal[e.Credit]++
	}
	// The rebuild links the lines and counts the entities' names.
	rebuild()
	return ids
}
