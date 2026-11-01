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

// Migration 73 adds the variant kind and mode merge5 on data shaped as
// prod's: a candidate, its sample row, a frozen card, a registration and a
// decision of every kind there is, and a merge4 run. Nothing already there
// changes; a variant row is admitted in each table and a merge5 eval run
// in the runs, and an unknown kind or mode is still refused; an extract
// inserted published marks a build. A run's worker holding the runs makes
// the file fail at once, not wait.
func TestMigration73AdmitsTheVariantKind(t *testing.T) {
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	scratch := fmt.Sprintf("migrate_73_test_%d", time.Now().UnixNano())
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
		if !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "74-" {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		through["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() < "73-" {
			before["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if _, err = Run(ctx, db, before, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 72: %v", err)
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
		SELECT 'eval', 'merge4', 'completed', version, now() FROM author_llm_config`)
	must(`INSERT INTO author_llm_eval_report (run_id, config_version, report, report_sha256)
		SELECT id, config_version, '{}', sha256('report') FROM author_llm_run`)
	// One row of a kind in every table the kind is checked in.
	insertKind := func(kind string) error {
		item, credit := "entity", "NULL::bigint"
		if kind == "bucket" {
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
	for _, kind := range []string{"swap", "translit", "initials", "bucket"} {
		if err = insertKind(kind); err != nil {
			t.Fatalf("a %s row before 73: %v", kind, err)
		}
	}
	if insertKind("variant") == nil {
		t.Fatal("72 admitted a variant candidate")
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

	worker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Exec(`SELECT count(*) FROM author_llm_run`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = Run(ctx, db, through, "database_migrations", AppBaseline()); err == nil {
		t.Fatal("migration 73 applied while a worker read the runs")
	}
	if time.Since(started) > time.Second {
		t.Errorf("migration 73 waited %v for a worker instead of failing at once", time.Since(started))
	}
	if err = worker.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, db, through, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("applying 73: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != "73-author-entity-merge-variant.sql" {
		t.Fatalf("applied %v, want 73 alone", result.Applied)
	}
	for _, table := range tables {
		if got := snapshot(table); got != was[table] {
			t.Errorf("73 changed %s", table)
		}
	}

	if err = insertKind("variant"); err != nil {
		t.Fatalf("a variant row after 73: %v", err)
	}
	if insertKind("merge") == nil {
		t.Error("73 admitted an unknown kind")
	}
	must(`INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		SELECT 'eval', 'merge5', 'completed', version, now() FROM author_llm_config`)
	if exec(`INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		SELECT 'eval', 'merge6', 'completed', version, now() FROM author_llm_config`) == nil {
		t.Error("73 admitted an unknown mode")
	}

	marks := func() int {
		t.Helper()
		var n int
		if _, countErr := db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM author_autopilot_mark`); countErr != nil {
			t.Fatal(countErr)
		}
		return n
	}
	n := marks()
	must(`INSERT INTO wikidata_extract (source, status, content_id, manifest) VALUES ('fixture', 'fetching', NULL, '{}')`)
	if marks() != n {
		t.Error("an extract still fetching marked a build")
	}
	must(`INSERT INTO wikidata_extract (source, status, content_id, manifest)
		VALUES ('fixture', 'published', 'wd-0123456789abcdef', '{}')`)
	if marks() != n+1 {
		t.Error("an extract inserted published marked no build")
	}
}
