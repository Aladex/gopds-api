package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

// migrateTo41 creates a scratch database and applies every migration before
// 42 to it.
func migrateTo41(t *testing.T) *pg.DB {
	t.Helper()
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_42_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + scratch + " WITH (FORCE)") })
	db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: scratch})
	t.Cleanup(func() { _ = db.Close() })

	before := fstest.MapFS{}
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "42-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(context.Background(), db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 41: %v", err)
	}
	return db
}

// Migration 42 takes auth_user up front, NOWAIT: a login stamping last_login
// makes it fail at once with lock_not_available rather than queue every other
// login behind the table rewrite, and the next start applies it whole.
func TestMigration42FailsFastInsteadOfWaitingForALock(t *testing.T) {
	db := migrateTo41(t)
	ctx := context.Background()

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`LOCK TABLE auth_user IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	elapsed := time.Since(started)
	var pgErr pg.Error
	if err == nil || !errors.As(err, &pgErr) || pgErr.Field('C') != "55P03" {
		t.Fatalf("migration 42 under a held auth_user: want lock_not_available, got %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 42 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "42-") {
		t.Fatalf("a failed migration 42 left a ledger row")
	}
	var exists bool
	if _, err = db.QueryOne(pg.Scan(&exists), `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		               WHERE table_name = 'auth_user' AND column_name = 'kindle_sender_id')`); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("a failed migration 42 left its column")
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 42: %v", err)
	}
	if len(result.Applied) == 0 || !strings.HasPrefix(result.Applied[0], "42-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}

// Every reader who existed before the migration gets a sender id of their own,
// and so does every reader created after it; both buttons start shown and the
// Kindle address starts empty.
func TestMigration42GivesEveryReaderTheirOwnSender(t *testing.T) {
	db := migrateTo41(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := db.Exec(`
			INSERT INTO auth_user (password, username, email, date_joined, is_superuser)
			VALUES ('x', ?, ?, now(), false)`, fmt.Sprintf("before%d", i), fmt.Sprintf("before%d@example.test", i)); err != nil {
			t.Fatalf("seeding a reader: %v", err)
		}
	}

	if _, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("applying 42: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO auth_user (password, username, email, date_joined, is_superuser)
		VALUES ('x', 'after', 'after@example.test', now(), false)`); err != nil {
		t.Fatalf("creating a reader after the migration: %v", err)
	}

	var rows []struct {
		Username           string
		KindleEmail        string
		KindleSenderID     string
		ShowKindleButton   bool
		ShowTelegramButton bool
	}
	if _, err := db.Query(&rows, `
		SELECT username, kindle_email, kindle_sender_id::text AS kindle_sender_id,
		       show_kindle_button, show_telegram_button
		FROM auth_user ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("read %d readers, want 4", len(rows))
	}
	seen := map[string]string{}
	for _, row := range rows {
		if row.KindleSenderID == "" {
			t.Errorf("%s has no sender id", row.Username)
		}
		if other, dup := seen[row.KindleSenderID]; dup {
			t.Errorf("%s and %s share the sender id %s", row.Username, other, row.KindleSenderID)
		}
		seen[row.KindleSenderID] = row.Username
		if row.KindleEmail != "" || !row.ShowKindleButton || !row.ShowTelegramButton {
			t.Errorf("%s starts with %+v, want no Kindle address and both buttons shown", row.Username, row)
		}
	}

	if _, err := db.Exec(`
		UPDATE auth_user
		SET kindle_sender_id = (SELECT kindle_sender_id FROM auth_user WHERE username = 'after')
		WHERE username = 'before0'`); err == nil {
		t.Error("two readers were allowed the same sender id")
	}
}
