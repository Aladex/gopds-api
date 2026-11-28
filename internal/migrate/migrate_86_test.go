package migrate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-pg/pg/v10"
)

// Migration 86 adds the short-name function and the live entities' index on
// it. It takes author_entity in SHARE mode, NOWAIT, so a build writing
// entities makes it fail at once; readers do not hold it back. It writes no
// row: an entity of 85 comes out as it went in, and the index answers the
// namesake lookup by short name.
func TestMigration86AppliesBesideReadersAndWritesNoEntity(t *testing.T) {
	db, _, _ := scratchDatabase(t)
	ctx := context.Background()
	before, through := migrationsBefore(t, "86-")
	if _, err := Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 85: %v", err)
	}
	const entity = "0193b7e0-0000-7000-8000-000000000086"
	if _, err := db.Exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
		VALUES (?, 'person', 'Михаил Павлович Игнатов', 'Игнатов, Михаил Павлович', 'михаил павлович игнатов')`,
		entity); err != nil {
		t.Fatalf("seeding an entity under 85: %v", err)
	}
	var stamped time.Time
	if _, err := db.QueryOne(pg.Scan(&stamped), `SELECT updated_at FROM author_entity WHERE id = ?`, entity); err != nil {
		t.Fatal(err)
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`UPDATE author_entity SET sort_name = sort_name WHERE id = ?`, entity); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
		t.Fatal("migration 86 applied while a build held an entity")
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 86 waited %v for the entities instead of failing at once", time.Since(started))
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}

	reader, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Rollback() })
	if _, err = reader.Exec(`LOCK TABLE author_entity IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("migration 86 beside a reader: %v", err)
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "86-") {
		t.Fatalf("applied %v, want 86 alone", result.Applied)
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}

	var name, short string
	var after time.Time
	if _, err = db.QueryOne(pg.Scan(&name, &after, &short), `SELECT display_name, updated_at,
		public.author_short_name(display_name) FROM author_entity WHERE id = ?`, entity); err != nil {
		t.Fatal(err)
	}
	if name != "Михаил Павлович Игнатов" || !after.Equal(stamped) {
		t.Errorf("the entity changed under 86: %q, updated %v (was %v)", name, after, stamped)
	}
	if short != "Михаил Игнатов" {
		t.Errorf("author_short_name = %q, want «Михаил Игнатов»", short)
	}

	// The namesake lookup's predicate is the index's: with sequential scans
	// off, the plan reads the index.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err = tx.Exec(`SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Line string `pg:"QUERY PLAN"`
	}
	if _, err = tx.Query(&rows, `EXPLAIN SELECT id FROM author_entity AS o
		WHERE public.author_short_name(o.display_name) = 'Михаил Игнатов' AND o.merged_into IS NULL`); err != nil {
		t.Fatal(err)
	}
	plan := make([]string, len(rows))
	for i, r := range rows {
		plan[i] = r.Line
	}
	if !strings.Contains(strings.Join(plan, "\n"), "author_entity_short_name_idx") {
		t.Errorf("the namesake lookup does not read the short-name index:\n%s", strings.Join(plan, "\n"))
	}
}
