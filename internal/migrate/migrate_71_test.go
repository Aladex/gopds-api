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

// Migration 71 lets a withdrawn kind register again, on data shaped as
// prod's: swap and translit live, initials withdrawn with a Latin block,
// decisions under each. Nothing already there changes; a new initials
// registration inserts and takes the withdrawn one's block; a second live
// one of a kind is refused, and the withdrawn row stays history. A reader
// of the registrations makes the file fail at once, not wait.
func TestMigration71RegistersAWithdrawnKindAgain(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_71_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "72-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() < "71-" {
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 70: %v", err)
	}

	exec := func(query string, params ...interface{}) {
		t.Helper()
		if _, execErr := db.Exec(query, params...); execErr != nil {
			t.Fatalf("%s: %v", query, execErr)
		}
	}
	exec(`INSERT INTO author_llm_config (version, base_url, identity, prompt_sha256, schema_sha256,
		validator_version, tokenizer_version, context_version)
		VALUES (?, 'http://llm.local/v1', '{"task":"merge"}', ?, ?, 'v', 't', 'c')`,
		"llm1-"+strings.Repeat("a", 64), strings.Repeat("c", 64), strings.Repeat("d", 64))
	exec(`INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		SELECT 'eval', 'merge1', 'completed', version, now() FROM author_llm_config`)
	exec(`INSERT INTO author_llm_eval_report (run_id, config_version, report, report_sha256)
		SELECT id, config_version, '{}', sha256('report') FROM author_llm_run`)
	for _, kind := range []string{"swap", "translit", "initials"} {
		exec(`INSERT INTO author_entity_merge_registration (config_version, kind, evidence_report_id,
			evidence_report_sha256, gate) SELECT config_version, ?, id, report_sha256, 'judge_consensus'
			FROM author_llm_eval_report`, kind)
	}
	exec(`INSERT INTO author_entity_merge_script_block (registration_id, script)
		SELECT id, 'Latn' FROM author_entity_merge_registration WHERE kind = 'initials'`)
	exec(`UPDATE author_entity_merge_registration SET withdrawn_at = now() WHERE kind = 'initials'`)
	exec(`INSERT INTO author_entity_merge_decision (digest, config_version, kind, script, item_kind, members_sha256,
			input_sha256, outcome, source, registration_id)
		SELECT sha256(convert_to(r.kind || n, 'UTF8')), r.config_version, r.kind, 'Cyrl', 'entity',
			sha256(convert_to('m' || r.kind || n, 'UTF8')), sha256(convert_to('i' || r.kind || n, 'UTF8')),
			'same', 'model', r.id
		FROM author_entity_merge_registration r, generate_series(1, 3) AS n`)
	snapshot := func(table string) string {
		t.Helper()
		var out string
		if _, snapErr := db.QueryOne(pg.Scan(&out), `SELECT coalesce(string_agg(to_jsonb(x)::text, ',' ORDER BY x::text), '')
			FROM `+table+` x`); snapErr != nil {
			t.Fatal(snapErr)
		}
		return out
	}
	tables := []string{"author_entity_merge_registration", "author_entity_merge_script_block",
		"author_entity_merge_decision"}
	was := map[string]string{}
	for _, table := range tables {
		was[table] = snapshot(table)
	}

	reader, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Exec(`SELECT count(*) FROM author_entity_merge_registration`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
		t.Fatal("migration 71 applied while a build read the registrations")
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 71 waited %v for a reader instead of failing at once", time.Since(started))
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("migration 71: %v", err)
	}
	if len(result.Applied) != 1 || !strings.HasPrefix(result.Applied[0], "71-") {
		t.Fatalf("applied %v, want 71 alone", result.Applied)
	}
	for _, table := range tables {
		if got := snapshot(table); got != was[table] {
			t.Errorf("migration 71 changed %s:\n%s\nwant\n%s", table, got, was[table])
		}
	}

	register := func(kind string) (int64, error) {
		var id int64
		_, regErr := db.QueryOne(pg.Scan(&id), `INSERT INTO author_entity_merge_registration (config_version,
			kind, evidence_report_id, evidence_report_sha256, gate)
			SELECT config_version, ?, id, report_sha256, 'judge_consensus' FROM author_llm_eval_report
			RETURNING id`, kind)
		return id, regErr
	}
	code := func(err error) string {
		var pgErr pg.Error
		if errors.As(err, &pgErr) {
			return pgErr.Field('C')
		}
		return fmt.Sprint(err)
	}
	again, err := register("initials")
	if err != nil {
		t.Fatalf("registering a withdrawn kind again: %v", err)
	}
	if again != 4 {
		t.Errorf("the new registration is %d, want 4", again)
	}
	var carried string
	if _, err = db.QueryOne(pg.Scan(&carried), `SELECT coalesce(string_agg(n.script, ',' ORDER BY n.script), '')
		FROM author_entity_merge_script_block n
		JOIN author_entity_merge_script_block o ON o.script = n.script AND o.registration_id = 3
			AND o.blocked_at = n.blocked_at
		WHERE n.registration_id = ?`, again); err != nil {
		t.Fatal(err)
	}
	if carried != "Latn" {
		t.Errorf("the new registration's blocks carried over: %q, want Latn as blocked before", carried)
	}
	for _, kind := range []string{"initials", "swap", "translit"} {
		if _, err = register(kind); code(err) != "23505" {
			t.Errorf("a second live %s registration: %v, want 23505", kind, err)
		}
	}
	if _, err = db.Exec(`DELETE FROM author_entity_merge_registration WHERE id = 3`); code(err) != "23001" {
		t.Errorf("deleting the withdrawn registration: %v, want 23001", err)
	}
	if _, err = db.Exec(`UPDATE author_entity_merge_registration SET withdrawn_at = NULL WHERE id = 3`); code(err) != "23001" {
		t.Errorf("taking the withdrawal back: %v, want 23001", err)
	}
	if got := snapshot("author_entity_merge_decision"); got != was["author_entity_merge_decision"] {
		t.Error("the registration changed a decision row")
	}
}
