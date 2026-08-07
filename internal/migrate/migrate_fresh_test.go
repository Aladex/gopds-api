package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

// TestRunRealMigrationsOnFreshDatabase runs the whole database_migrations
// directory against a database that has nothing — the path every new
// environment takes. A MapFS of hand-written snippets cannot catch what only
// the real files contain: 01-initial.sql resets the session search_path to
// empty, and later migrations reference extension operator classes that only
// resolve when the runner re-establishes a usable path for each transaction.
func TestRunRealMigrationsOnFreshDatabase(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()

	scratch := fmt.Sprintf("migrate_fresh_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE IF EXISTS " + scratch); err != nil {
			t.Errorf("dropping the scratch database: %v", err)
		}
	})

	db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: scratch})
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("running the real migrations on a fresh database: %v", err)
	}

	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatalf("listing the migrations directory: %v", err)
	}
	files := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files++
		}
	}
	if len(result.Applied) != files {
		t.Errorf("applied %d migrations, directory holds %d", len(result.Applied), files)
	}
	if len(result.Baselined) != 0 {
		t.Errorf("a fresh database baselined %d migrations, want none", len(result.Baselined))
	}

	var fnExists bool
	if _, queryErr := db.QueryOne(pg.Scan(&fnExists), `
		SELECT EXISTS (
			SELECT 1 FROM pg_proc p
			JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'public' AND p.proname = 'search_normalize')`); queryErr != nil {
		t.Fatalf("checking search_normalize: %v", queryErr)
	}
	if !fnExists {
		t.Error("public.search_normalize missing after the full migration run")
	}

	var indexes int
	if _, queryErr := db.QueryOne(pg.Scan(&indexes), `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'public' AND indexname IN (
			'idx_book_title_search_norm_trgm',
			'idx_book_title_search_norm_pattern',
			'idx_author_full_name_search_norm_trgm')`); queryErr != nil {
		t.Fatalf("checking search indexes: %v", queryErr)
	}
	if indexes != 3 {
		t.Errorf("found %d of 3 search expression indexes", indexes)
	}

	second, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("re-running the migrations: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Errorf("second run applied %d migrations, want none", len(second.Applied))
	}
}
