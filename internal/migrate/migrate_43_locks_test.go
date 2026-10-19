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

// migrateTo42 creates a scratch database and applies every migration before
// 43 to it.
func migrateTo42(t *testing.T) *pg.DB {
	t.Helper()
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_43_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "43-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, err = Run(context.Background(), db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 42: %v", err)
	}
	return db
}

// Migration 43 references auth_user from four new tables, which takes SHARE
// ROW EXCLUSIVE on it. It takes that up front, NOWAIT: a login stamping
// last_login makes it fail at once rather than queue every other login behind
// it, and the next start applies it whole.
func TestMigration43FailsFastInsteadOfWaitingForALock(t *testing.T) {
	db := migrateTo42(t)
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
		t.Fatalf("migration 43 under a held auth_user: want lock_not_available, got %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("migration 43 waited %v for a lock instead of failing at once", elapsed)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, db); strings.HasPrefix(got[len(got)-1], "43-") {
		t.Fatalf("a failed migration 43 left a ledger row")
	}
	var exists bool
	if _, err = db.QueryOne(pg.Scan(&exists), `SELECT to_regclass('public.telegram_bot') IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("a failed migration 43 left its tables")
	}

	result, err := Run(ctx, db, os.DirFS("../.."), "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("retrying migration 43: %v", err)
	}
	if len(result.Applied) == 0 || !strings.HasPrefix(result.Applied[0], "43-") {
		t.Fatalf("the retry applied %v", result.Applied)
	}
}

// The binding rules live in the schema, not only in the code: one active
// binding per account, per bot and per Telegram user; an inbox row is either
// pending under a lease or finished without one.
func TestMigration43EnforcesTheBindingRules(t *testing.T) {
	db := migrateTo42(t)
	if _, err := Run(context.Background(), db, os.DirFS("../.."), "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("applying 43: %v", err)
	}
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	mustFail := func(why, query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err == nil {
			t.Fatalf("%s: the schema accepted it", why)
		}
	}
	mustExec(`INSERT INTO auth_user (password, username, email, date_joined, is_superuser)
		VALUES ('x', 'a', 'a@example.test', now(), false), ('x', 'b', 'b@example.test', now(), false)`)
	mustExec(`INSERT INTO telegram_bot (tg_bot_id, kind, webhook_path, webhook_secret_enc, nav_key_enc)
		VALUES (11, 'manual', gen_random_uuid(), '\x00', '\x00'), (12, 'manual', gen_random_uuid(), '\x00', '\x00')`)
	mustExec(`INSERT INTO telegram_binding (user_id, bot_ref, tg_user_id, generation, status)
		SELECT (SELECT id FROM auth_user WHERE username = 'a'), (SELECT id FROM telegram_bot WHERE tg_bot_id = 11), 5000000000, 1, 'active'`)

	mustFail("a second active binding of the account", `INSERT INTO telegram_binding (user_id, bot_ref, tg_user_id, generation, status)
		SELECT (SELECT id FROM auth_user WHERE username = 'a'), (SELECT id FROM telegram_bot WHERE tg_bot_id = 12), 1, 1, 'active'`)
	mustFail("a second active binding of the bot", `INSERT INTO telegram_binding (user_id, bot_ref, tg_user_id, generation, status)
		SELECT (SELECT id FROM auth_user WHERE username = 'b'), (SELECT id FROM telegram_bot WHERE tg_bot_id = 11), 2, 2, 'active'`)
	mustFail("a second active binding of the Telegram user", `INSERT INTO telegram_binding (user_id, bot_ref, tg_user_id, generation, status)
		SELECT (SELECT id FROM auth_user WHERE username = 'b'), (SELECT id FROM telegram_bot WHERE tg_bot_id = 12), 5000000000, 1, 'active'`)
	mustFail("a revoked binding without its time", `INSERT INTO telegram_binding (user_id, bot_ref, tg_user_id, generation, status)
		SELECT (SELECT id FROM auth_user WHERE username = 'b'), (SELECT id FROM telegram_bot WHERE tg_bot_id = 12), 3, 1, 'revoked'`)
	mustFail("a generation past 16 bits", `INSERT INTO telegram_binding (user_id, bot_ref, tg_user_id, generation, status)
		SELECT (SELECT id FROM auth_user WHERE username = 'b'), (SELECT id FROM telegram_bot WHERE tg_bot_id = 12), 3, 65536, 'active'`)
	mustFail("a finished inbox row still leased", `INSERT INTO telegram_update_inbox
		(bot_ref, update_id, kind, state, lease_owner, lease_expires_at, finished_at)
		SELECT id, 1, 'message', 'done', gen_random_uuid(), now(), now() FROM telegram_bot WHERE tg_bot_id = 11`)
	mustFail("a token hash without a token", `UPDATE telegram_bot SET token_sha256 = '\x01' WHERE tg_bot_id = 11`)
}
