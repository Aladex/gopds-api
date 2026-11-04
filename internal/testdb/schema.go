package testdb

import (
	"context"
	"fmt"

	"github.com/go-pg/pg/v10"
)

// The catalog base tables of the production database come from a restored
// dump, not from database_migrations, and the two differ by one column:
// opds_catalog_genre.title (found by diffing information_schema of a fresh
// migration against the restored catalog). models.Genre reads it and
// database.UpdateBookTags writes it, so a test on a freshly migrated database
// fails with `column "title" of relation "opds_catalog_genre" does not exist`
// wherever a genre is touched.
//
// Until a migration creates it, every integration suite brings its database
// to the production shape with ProductionShape, right after migrating or
// connecting. On a database restored from the dump it changes nothing.

// productionOnlyDDL is what the production catalog has and the migration
// files do not create.
const productionOnlyDDL = `ALTER TABLE public.opds_catalog_genre
	ADD COLUMN IF NOT EXISTS title character varying NOT NULL DEFAULT ''`

// ProductionShape adds the production-only columns to a migrated database.
// It reads the catalog first and alters only when a column is missing, so
// a database that already has them takes no lock: suites of several packages
// share one configured database and run at the same time.
func ProductionShape(ctx context.Context, db pg.DBI) error {
	var missing bool
	if _, err := db.QueryOneContext(ctx, pg.Scan(&missing), `SELECT NOT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'opds_catalog_genre' AND column_name = 'title')`); err != nil {
		return fmt.Errorf("reading the genre columns: %w", err)
	}
	if !missing {
		return nil
	}
	if _, err := db.ExecContext(ctx, productionOnlyDDL); err != nil {
		return fmt.Errorf("adding the production-only genre title column: %w", err)
	}
	return nil
}
