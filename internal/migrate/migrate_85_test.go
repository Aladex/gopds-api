package migrate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-pg/pg/v10"
)

// Migration 85 lets a triage batch be stopped. It locks only
// author_review_triage_batch, NOWAIT, so a worker writing its progress makes
// it fail at once, and the writers of every other author table never hold it
// back. Batches that exist stay as they are; a batch can then be stopped,
// by someone, and only the new columns and status change.
func TestMigration85AppliesBesideEveryWriterAndOverExistingBatches(t *testing.T) {
	db, _, _ := scratchDatabase(t)
	ctx := context.Background()
	before, through := migrationsBefore(t, "85-")
	if _, err := Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 84: %v", err)
	}
	var batch int64
	if _, err := db.QueryOne(pg.Scan(&batch), `INSERT INTO author_review_triage_batch
		(rule_version, status, created_by_user_id) VALUES ('triage-v1', 'applying', 7) RETURNING id`); err != nil {
		t.Fatalf("seeding a batch under 84: %v", err)
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`UPDATE author_review_triage_batch SET items_done = 1 WHERE id = ?`, batch); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
		t.Fatal("migration 85 applied while a worker held a batch")
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 85 waited %v for the batches instead of failing at once", time.Since(started))
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}

	writer, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Exec(`LOCK TABLE author_review_triage_item, contributor_manual_override,
		contributor_review_item, book_contributor_credit_selection, contributor_normalization_result
		IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("migration 85 beside the writers: %v", err)
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "85-") {
		t.Fatalf("applied %v, want 85 alone", result.Applied)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}

	var untouched int
	if _, err = db.QueryOne(pg.Scan(&untouched), `SELECT count(*) FROM author_review_triage_batch
		WHERE id = ? AND status = 'applying' AND stop_requested_at IS NULL AND stop_requested_by_user_id IS NULL`,
		batch); err != nil {
		t.Fatal(err)
	}
	if untouched != 1 {
		t.Fatal("the existing batch changed under 85")
	}
	if _, err = db.Exec(`UPDATE author_review_triage_batch
		SET status = 'stopped', stop_requested_at = now() WHERE id = ?`, batch); err == nil {
		t.Error("a stop with no administrator was stored")
	}
	if _, err = db.Exec(`UPDATE author_review_triage_batch
		SET status = 'stopped', stop_requested_at = now(), stop_requested_by_user_id = 13 WHERE id = ?`,
		batch); err != nil {
		t.Fatalf("stopping a batch written before 85: %v", err)
	}
	if _, err = db.Exec(`UPDATE author_review_triage_batch SET rule_version = 'triage-v2' WHERE id = ?`,
		batch); err == nil {
		t.Error("the batch's plan columns are still immutable")
	}
	var triggers int
	if _, err = db.QueryOne(pg.Scan(&triggers), `SELECT count(*) FROM pg_trigger
		WHERE NOT tgisinternal AND tgrelid = 'author_review_triage_batch'::regclass`); err != nil {
		t.Fatal(err)
	}
	if triggers != 1 {
		t.Errorf("migration 85 left %d triggers on the batches, want 1", triggers)
	}
}
