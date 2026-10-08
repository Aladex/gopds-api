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

	// The author metadata source layer (23-author-metadata-source.sql): its
	// tables, the two partial unique indexes that carry its invariants, and
	// the immutability guard.
	var sourceTables []string
	if _, queryErr := db.Query(&sourceTables, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN (
			'author_metadata_run',
			'author_metadata_run_item',
			'author_metadata_run_item_attempt',
			'author_metadata_pilot_approval',
			'book_metadata_snapshot',
			'book_contributor_credit')`); queryErr != nil {
		t.Fatalf("checking author metadata tables: %v", queryErr)
	}
	if len(sourceTables) != 6 {
		t.Errorf("found author metadata tables %v, want all 6", sourceTables)
	}

	var partialUnique []string
	if _, queryErr := db.Query(&partialUnique, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname IN ('author_metadata_run_one_active', 'book_metadata_snapshot_one_current')
			AND indexdef LIKE 'CREATE UNIQUE INDEX%WHERE%'`); queryErr != nil {
		t.Fatalf("checking author metadata partial unique indexes: %v", queryErr)
	}
	if len(partialUnique) != 2 {
		t.Errorf("found partial unique indexes %v, want the active-run and current-snapshot ones", partialUnique)
	}

	var guards int
	if _, queryErr := db.QueryOne(pg.Scan(&guards), `
		SELECT count(*) FROM pg_trigger t
		JOIN pg_proc p ON p.oid = t.tgfoid
		WHERE NOT t.tgisinternal AND p.proname = 'author_metadata_reject_mutation'
			AND t.tgrelid::regclass::text IN (
				'book_metadata_snapshot', 'book_contributor_credit',
				'author_metadata_run_item_attempt', 'author_metadata_pilot_approval')`); queryErr != nil {
		t.Fatalf("checking author metadata immutability triggers: %v", queryErr)
	}
	if guards != 4 {
		t.Errorf("found %d immutability triggers, want 4 (snapshot, credit, attempt, pilot approval)", guards)
	}

	// The normalization pipeline (24-author-normalization-pipeline.sql): its
	// tables, the one-local-result-per-key and one-open-review indexes, the
	// resolution consistency trigger, and an acceptance policy that ships
	// empty — no migration registers a class.
	var pipelineTables []string
	if _, queryErr := db.Query(&pipelineTables, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN (
			'author_acceptance_class',
			'contributor_normalization_result',
			'contributor_manual_override',
			'book_contributor_credit_selection',
			'book_contributor_credit_selection_audit',
			'contributor_normalization_job',
			'contributor_normalization_job_attempt',
			'contributor_review_item')`); queryErr != nil {
		t.Fatalf("checking normalization pipeline tables: %v", queryErr)
	}
	if len(pipelineTables) != 8 {
		t.Errorf("found normalization pipeline tables %v, want all 8", pipelineTables)
	}

	var pipelineIndexes []string
	if _, queryErr := db.Query(&pipelineIndexes, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname IN (
				'contributor_normalization_result_one_local_per_key',
				'contributor_review_item_one_open_per_credit',
				'contributor_review_item_one_open_per_fingerprint')
			AND indexdef LIKE 'CREATE UNIQUE INDEX%WHERE%'`); queryErr != nil {
		t.Fatalf("checking normalization pipeline partial unique indexes: %v", queryErr)
	}
	if len(pipelineIndexes) != 3 {
		t.Errorf("found partial unique indexes %v, want the local-result and two open-review ones", pipelineIndexes)
	}

	var consistency, policyClasses int
	if _, queryErr := db.QueryOne(pg.Scan(&consistency, &policyClasses), `
		SELECT (SELECT count(*) FROM pg_trigger
				WHERE tgname = 'book_contributor_credit_selection_consistency' AND tgconstraint <> 0),
			(SELECT count(*) FROM author_acceptance_class)`); queryErr != nil {
		t.Fatalf("checking the resolution trigger and the acceptance policy: %v", queryErr)
	}
	if consistency != 1 {
		t.Errorf("found %d resolution consistency constraint triggers, want 1", consistency)
	}
	if policyClasses != 0 {
		t.Errorf("a fresh database holds %d acceptance classes, want an empty policy", policyClasses)
	}

	second, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("re-running the migrations: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Errorf("second run applied %d migrations, want none", len(second.Applied))
	}
}
