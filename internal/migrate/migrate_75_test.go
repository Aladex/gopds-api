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

// Migration 75 adds the five mixed-script kinds and mode merge7 on data
// shaped as prod's: a candidate, its sample row, a frozen card, a
// registration and a decision of every kind there is, and a merge6 run.
// Nothing already there changes; a row of each mixed kind is admitted in
// each table — bucket_mixed's of a book, the others of entities — and a
// merge7 eval run in the runs; an unknown kind or mode, and a book item of
// another kind, are still refused. A build holding the candidates, or a
// run's worker the runs, makes the file fail at once, not wait.
func TestMigration75AdmitsTheMixedScriptKinds(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_75_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "76-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() < "75-" {
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 74: %v", err)
	}

	exec := func(query string, params ...interface{}) error {
		_, execErr := db.Exec(query, params...)
		return execErr
	}
	must := func(query string, params ...interface{}) {
		t.Helper()
		if execErr := exec(query, params...); execErr != nil {
			t.Fatalf("%s: %v", query, execErr)
		}
	}
	must(`INSERT INTO author_llm_config (version, base_url, identity, prompt_sha256, schema_sha256,
		validator_version, tokenizer_version, context_version)
		VALUES (?, 'http://llm.local/v1', '{"task":"merge"}', ?, ?, 'v', 't', 'c')`,
		"llm1-"+strings.Repeat("a", 64), strings.Repeat("c", 64), strings.Repeat("d", 64))
	must(`INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		SELECT 'eval', 'merge6', 'completed', version, now() FROM author_llm_config`)
	must(`INSERT INTO author_llm_eval_report (run_id, config_version, report, report_sha256)
		SELECT id, config_version, '{}', sha256('report') FROM author_llm_run`)
	// One row of a kind in every table the kind is checked in.
	insertItem := func(kind string, book bool) error {
		item, credit := "entity", "NULL::bigint"
		if book {
			item, credit = "book", "7"
		}
		digest := fmt.Sprintf("sha256(convert_to('%s', 'UTF8'))", kind)
		for _, q := range []string{
			`INSERT INTO author_entity_merge_candidate (digest, kind, script, item_kind, side_a_entity, side_b_entity,
				credit_id, credit_ordinal, members_sha256, input_sha256, first_build_run_id)
				VALUES (` + digest + `, ?0, 'Cyrl', ?1, gen_random_uuid(), gen_random_uuid(), ` + credit + `,
				` + strings.Replace(credit, "7", "0", 1) + `::smallint, ` + digest + `, ` + digest + `, 1)`,
			`INSERT INTO author_entity_merge_candidate_sample (build_run_id, kind, script, reason, members_sha256,
				entity_a, entity_b, go_keys_a, go_keys_b, digest)
				VALUES (1, ?0, 'Cyrl', 'candidate', ` + digest + `, gen_random_uuid(), gen_random_uuid(), '{}', '{}',
				` + digest + `)`,
			`INSERT INTO author_llm_merge_input (digest, kind, script, input) VALUES (` + digest + `, ?0, 'Cyrl', '{}')`,
			`INSERT INTO author_entity_merge_registration (config_version, kind, evidence_report_id,
				evidence_report_sha256, gate) SELECT config_version, ?0, id, report_sha256, 'judge_consensus'
				FROM author_llm_eval_report`,
			`INSERT INTO author_entity_merge_decision (digest, config_version, kind, script, item_kind, credit_id,
				credit_ordinal, members_sha256, input_sha256, outcome, source, registration_id)
				SELECT ` + digest + `, config_version, kind, 'Cyrl', ?1, ` + credit + `,
				` + strings.Replace(credit, "7", "0", 1) + `::smallint, ` + digest + `, ` + digest + `, 'same', 'model', id
				FROM author_entity_merge_registration WHERE kind = ?0`,
		} {
			if insertErr := exec(q, kind, item); insertErr != nil {
				return insertErr
			}
		}
		return nil
	}
	insertKind := func(kind string) error {
		return insertItem(kind, kind == "bucket" || kind == "bucket_mixed")
	}
	for _, kind := range []string{"swap", "translit", "initials", "bucket", "variant", "initials_hub"} {
		if err = insertKind(kind); err != nil {
			t.Fatalf("a %s row before 75: %v", kind, err)
		}
	}
	mixed := []string{"bucket_mixed", "swap_mixed", "initials_mixed", "translit_mixed", "variant_mixed"}
	for _, kind := range mixed {
		if insertKind(kind) == nil {
			t.Fatalf("74 admitted a %s candidate", kind)
		}
	}
	snapshot := func(table string) string {
		t.Helper()
		var out string
		if _, snapErr := db.QueryOne(pg.Scan(&out), `SELECT coalesce(string_agg(to_jsonb(x)::text, ',' ORDER BY x::text), '')
			FROM `+table+` x`); snapErr != nil {
			t.Fatal(snapErr)
		}
		return out
	}
	tables := []string{"author_entity_merge_candidate", "author_entity_merge_candidate_sample", "author_llm_merge_input",
		"author_entity_merge_registration", "author_entity_merge_decision", "author_llm_run"}
	was := map[string]string{}
	for _, table := range tables {
		was[table] = snapshot(table)
	}

	for _, held := range []string{"author_entity_merge_candidate", "author_llm_run"} {
		worker, beginErr := db.Begin()
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, err = worker.Exec(`SELECT count(*) FROM ` + held); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
			t.Fatalf("migration 75 applied while a worker read %s", held)
		}
		if time.Since(started) > time.Second {
			t.Errorf("migration 75 waited %v for a worker on %s instead of failing at once", time.Since(started), held)
		}
		if err = worker.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("applying 75: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != "75-author-entity-merge-mixed-script.sql" {
		t.Fatalf("applied %v, want 75 alone", result.Applied)
	}
	for _, table := range tables {
		if got := snapshot(table); got != was[table] {
			t.Errorf("75 changed %s", table)
		}
	}

	for _, kind := range mixed {
		if err = insertKind(kind); err != nil {
			t.Fatalf("a %s row after 75: %v", kind, err)
		}
	}
	if insertKind("merge") == nil {
		t.Error("75 admitted an unknown kind")
	}
	if insertItem("translit_mixed", true) == nil {
		t.Error("75 admitted a book item of a kind not bucket")
	}
	if insertItem("bucket_mixed", false) == nil {
		t.Error("75 admitted a bucket_mixed item of entities")
	}
	must(`INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		SELECT 'eval', 'merge7', 'completed', version, now() FROM author_llm_config`)
	if exec(`INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		SELECT 'eval', 'merge8', 'completed', version, now() FROM author_llm_config`) == nil {
		t.Error("75 admitted an unknown mode")
	}
}
