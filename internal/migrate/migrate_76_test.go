package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

// Migration 76 adds the autopilot's merge step on a row shaped as prod's:
// switched on, a tick's state and an action recorded. Every value already
// there stays; the merge step's switch comes in off, its note empty and its
// count zero; the action merge is admitted, an unknown action, a malformed
// note and a negative count are still refused. A tick holding the row makes
// the file fail at once, not wait.
func TestMigration76AddsTheMergeStepSwitchedOff(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_76_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + scratch + " WITH (FORCE)") })
	db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: scratch})
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	before, through := fstest.MapFS{}, fstest.MapFS{}
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "77-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() < "76-" {
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 75: %v", err)
	}
	exec := func(query string, params ...interface{}) error {
		_, execErr := db.Exec(query, params...)
		return execErr
	}
	if err = exec(`UPDATE author_autopilot SET state = 'idle', resolve_note = 'nothing_to_resolve', resolve_inputs = 3,
		last_action = 'build', last_action_run_id = 33, last_action_at = now(), failures = 1,
		last_error_class = 'internal', next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	if exec(`UPDATE author_autopilot SET last_action = 'merge', last_action_run_id = 38`) == nil {
		t.Fatal("75 admitted the action merge")
	}
	columns := `enabled, state, state_run_id, resolve_note, resolve_inputs, last_action, last_action_run_id,
		last_action_at, failures, last_error_class, next_attempt_at, updated_at`
	row := func() string {
		t.Helper()
		var out string
		if _, rowErr := db.QueryOne(pg.Scan(&out), `SELECT to_jsonb(x)::text FROM (SELECT `+columns+`
			FROM author_autopilot) x`); rowErr != nil {
			t.Fatal(rowErr)
		}
		return out
	}
	was := row()

	tick, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tick.Exec(`SELECT enabled FROM author_autopilot FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
		t.Fatal("migration 76 applied while a tick held the row")
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 76 waited %v for a tick instead of failing at once", time.Since(started))
	}
	if err = tick.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("applying 76: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != "76-author-autopilot-merge.sql" {
		t.Fatalf("applied %v, want 76 alone", result.Applied)
	}
	if got := row(); got != was {
		t.Errorf("76 changed the row: %s, was %s", got, was)
	}
	var merge struct {
		Enabled    bool    `pg:"merge_enabled"`
		Note       *string `pg:"merge_note"`
		Candidates int     `pg:"merge_candidates"`
	}
	if _, err = db.QueryOne(&merge, `SELECT merge_enabled, merge_note, merge_candidates FROM author_autopilot`); err != nil {
		t.Fatal(err)
	}
	if merge.Enabled || merge.Note != nil || merge.Candidates != 0 {
		t.Errorf("the merge step came in as %+v, want off, no note, zero", merge)
	}

	if err = exec(`UPDATE author_autopilot SET last_action = 'merge', last_action_run_id = 38,
		merge_note = 'started', merge_candidates = 1250`); err != nil {
		t.Fatalf("a merge action after 76: %v", err)
	}
	for _, refused := range []string{
		`UPDATE author_autopilot SET last_action = 'eval'`,
		`UPDATE author_autopilot SET merge_note = 'Started!'`,
		`UPDATE author_autopilot SET merge_candidates = -1`,
	} {
		if exec(refused) == nil {
			t.Errorf("76 admitted %s", refused)
		}
	}
}
