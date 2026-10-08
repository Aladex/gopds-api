package database

import (
	"crypto/sha256"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the normalization pipeline schema (migration 24) against a
// real PostgreSQL, on top of the source schema the author metadata tests
// cover. They reuse that file's fixture: one rolled-back transaction per test,
// savepoint-wrapped reject/accept probes.

var normalizationTables = []string{
	"author_acceptance_class",
	"contributor_normalization_result",
	"contributor_manual_override",
	"book_contributor_credit_selection",
	"book_contributor_credit_selection_audit",
	"contributor_normalization_job",
	"contributor_normalization_job_attempt",
	"contributor_review_item",
}

var normalizationIndexes = []string{
	"book_contributor_credit_id_fingerprint_role_key",
	"author_acceptance_class_key",
	"contributor_normalization_result_one_local_per_key",
	"contributor_normalization_result_id_fingerprint_key",
	"contributor_normalization_result_id_fingerprint_method_key",
	"contributor_normalization_result_id_key_key",
	"contributor_normalization_result_fingerprint_idx",
	"contributor_manual_override_id_result_key",
	"contributor_manual_override_credit_idx",
	"contributor_manual_override_fingerprint_idx",
	"book_contributor_credit_selection_pkey",
	"book_contributor_credit_selection_state_idx",
	"book_contributor_credit_selection_audit_credit_idx",
	"contributor_normalization_job_key",
	"contributor_normalization_job_claim_idx",
	"contributor_normalization_job_attempt_no_key",
	"contributor_review_item_one_open_per_credit",
	"contributor_review_item_one_open_per_fingerprint",
	"contributor_review_item_open_page_idx",
}

// keyFor stands in for a normalization key: 32 bytes derived from the parts.
// The real derivation belongs to internal/authornorm; the schema only needs
// distinct 32-byte values.
func keyFor(parts ...string) []byte {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return sum[:]
}

// resultRow is one contributor_normalization_result insert, every column a
// field so a probe can change exactly one of them.
type resultRow struct {
	fp, key                        []byte
	extractor, normalizer, schema  *string
	method, kind, status           string
	class, display, search, script *string
	actor                          *int64
	additionalNames, qualityFlags  interface{}
}

const resultInsert = `INSERT INTO contributor_normalization_result
	(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
	 method, kind, status, decision_class, display_name, search_key, script, created_by_user_id,
	 additional_names, quality_flags)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, coalesce(?::text[], '{}'), coalesce(?::text[], '{}'))
	RETURNING id`

func (r *resultRow) params() []interface{} {
	return []interface{}{r.fp, r.key, r.extractor, r.normalizer, r.schema,
		r.method, r.kind, r.status, r.class, r.display, r.search, r.script, r.actor,
		r.additionalNames, r.qualityFlags}
}

// autoRow is a valid automatic (rules) result for fp under extractor-v1.
func autoRow(fp []byte, class, normalizer string) *resultRow {
	return &resultRow{
		fp: fp, key: keyFor(string(fp), "extractor-v1", normalizer),
		extractor: ptr("extractor-v1"), normalizer: ptr(normalizer), schema: ptr("result-v1"),
		method: "rules", kind: "person", status: "normalized",
		class: ptr(class), display: ptr("Имя Фамилия"), search: ptr("имя фамилия"), script: ptr("Cyrl"),
	}
}

// manualRow is a valid manual result for fp written by actor.
func manualRow(fp []byte, actor int64) *resultRow {
	return &resultRow{
		fp: fp, schema: ptr("result-v1"),
		method: "manual", kind: "person", status: "normalized",
		display: ptr("Имя Фамилия"), search: ptr("имя фамилия"), script: ptr("Cyrl"),
		actor: &actor,
	}
}

func invalidRow(fp []byte) *resultRow {
	r := autoRow(fp, "malformed", "normalizer-invalid")
	r.kind, r.status, r.display, r.search, r.script = "malformed", "invalid", nil, nil, nil
	return r
}

func (f *authorSchemaFixture) result(r *resultRow) int64 {
	f.t.Helper()
	return f.returningID(resultInsert, r.params()...)
}

// authorCredit inserts an author credit with the given fingerprint into a
// fresh current snapshot (extractor-v1) of a fresh book.
func (f *authorSchemaFixture) authorCredit(fp []byte) int64 {
	f.t.Helper()
	return f.creditWith(f.snapshot(&snapshotSpec{book: f.book()}), "author", fp)
}

// creditWith inserts the first credit of a role into snapshot.
func (f *authorSchemaFixture) creditWith(snapshot int64, role string, fp []byte) int64 {
	f.t.Helper()
	return f.returningID(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_display_name, source_fingerprint)
		VALUES (?, ?, 0, 'Имя Фамилия', ?) RETURNING id`, snapshot, role, fp)
}

const acceptanceInsert = `INSERT INTO author_acceptance_class
	(policy_version, decision_class, config_version, evidence_report_sha256, registered_by_user_id)
	VALUES (?, ?, ?, ?, ?)`

const overrideInsert = `INSERT INTO contributor_manual_override
	(scope_credit_id, scope_fingerprint, source_fingerprint, result_id, created_by_user_id)
	VALUES (?, ?, ?, ?, ?) RETURNING id`

const selectionInsert = `INSERT INTO book_contributor_credit_selection
	(credit_id, source_fingerprint, state, result_id, basis, override_id, policy_version,
	 unresolved_reason, decided_by_user_id)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

const reviewInsert = `INSERT INTO contributor_review_item
	(scope_credit_id, scope_fingerprint, source_fingerprint, reason, decision_class, proposal_result_id)
	VALUES (?, ?, ?, ?, ?, ?) RETURNING id`

const jobInsert = `INSERT INTO contributor_normalization_job
	(normalization_key, source_fingerprint, extractor_version, normalizer_version)
	VALUES (?, ?, 'extractor-v1', 'normalizer-v1') RETURNING id`

// RED 1: the relations and the named indexes exist, and the acceptance
// policy ships empty.
func TestNormalizationSchemaRelationsAndIndexes(t *testing.T) {
	f := withAuthorSchemaTx(t)

	var tables []string
	_, err := f.tx.Query(&tables, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN (?)`, pg.In(normalizationTables))
	require.NoError(t, err)
	assert.ElementsMatch(t, normalizationTables, tables)

	var indexes []string
	_, err = f.tx.Query(&indexes, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = 'public' AND indexname IN (?)`, pg.In(normalizationIndexes))
	require.NoError(t, err)
	assert.ElementsMatch(t, normalizationIndexes, indexes)

	// No LLM-stream relations: amendment A1.
	var llm []string
	_, err = f.tx.Query(&llm, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND (table_name LIKE '%llm%' OR table_name LIKE '%batch%')`)
	require.NoError(t, err)
	assert.Empty(t, llm)
}

// RED 1: result fields, enums, versions and provenance are checked.
func TestNormalizationResultChecks(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	fp := fingerprint(32, 0x11)

	t.Run("closed enums", func(t *testing.T) {
		sf := f.on(t)
		for _, m := range []string{"structured", "rules", "llm"} {
			r := autoRow(fp, "plain", "normalizer-"+m)
			r.method = m
			sf.accept(resultInsert, r.params()...)
		}
		sf.accept(resultInsert, manualRow(fp, actor).params()...)
		for _, k := range []string{"person", "collective", "unknown"} {
			r := autoRow(fp, "plain", "normalizer-"+k)
			r.kind = k
			sf.accept(resultInsert, r.params()...)
		}
		for _, s := range []string{"normalized", "unresolved", "invalid"} {
			r := autoRow(fp, "plain", "normalizer-"+s)
			r.status = s
			sf.accept(resultInsert, r.params()...)
		}
		for _, bad := range []string{"", "other", "Rules", "manual "} {
			r := autoRow(fp, "plain", "normalizer-x")
			r.method = bad
			sf.reject(sqlstateCheck, "contributor_normalization_result_method_check", resultInsert, r.params()...)
			r = autoRow(fp, "plain", "normalizer-x")
			r.kind = bad
			sf.reject(sqlstateCheck, "contributor_normalization_result_kind_check", resultInsert, r.params()...)
			r = autoRow(fp, "plain", "normalizer-x")
			r.status = bad
			sf.reject(sqlstateCheck, "contributor_normalization_result_status_check", resultInsert, r.params()...)
		}
	})

	t.Run("malformed is invalid", func(t *testing.T) {
		sf := f.on(t)
		r := invalidRow(fp)
		sf.accept(resultInsert, r.params()...)
		r.status = "unresolved"
		sf.reject(sqlstateCheck, "contributor_normalization_result_malformed_check", resultInsert, r.params()...)
	})

	t.Run("automatic provenance", func(t *testing.T) {
		sf := f.on(t)
		const check = "contributor_normalization_result_provenance_check"
		for name, mutate := range map[string]func(r *resultRow){
			"no key":            func(r *resultRow) { r.key = nil },
			"short key":         func(r *resultRow) { r.key = fingerprint(31, 1) },
			"no extractor":      func(r *resultRow) { r.extractor = nil },
			"blank normalizer":  func(r *resultRow) { r.normalizer = ptr(" ") },
			"padded normalizer": func(r *resultRow) { r.normalizer = ptr("normalizer-v1 ") },
			"no decision class": func(r *resultRow) { r.class = nil },
			"an actor":          func(r *resultRow) { r.actor = &actor },
		} {
			r := autoRow(fp, "plain", "normalizer-v1")
			mutate(r)
			t.Log(name)
			sf.reject(sqlstateCheck, check, resultInsert, r.params()...)
		}
	})

	t.Run("manual provenance", func(t *testing.T) {
		sf := f.on(t)
		const check = "contributor_normalization_result_provenance_check"
		for name, mutate := range map[string]func(r *resultRow){
			"no actor":     func(r *resultRow) { r.actor = nil },
			"zero actor":   func(r *resultRow) { zero := int64(0); r.actor = &zero },
			"a key":        func(r *resultRow) { r.key = keyFor("manual") },
			"an extractor": func(r *resultRow) { r.extractor = ptr("extractor-v1") },
			"a normalizer": func(r *resultRow) { r.normalizer = ptr("normalizer-v1") },
		} {
			r := manualRow(fp, actor)
			mutate(r)
			t.Log(name)
			sf.reject(sqlstateCheck, check, resultInsert, r.params()...)
		}
		// A manual result may carry a decision class (classify) or none.
		r := manualRow(fp, actor)
		r.class = ptr("collective")
		sf.accept(resultInsert, r.params()...)
	})

	t.Run("shape", func(t *testing.T) {
		sf := f.on(t)
		cases := []struct {
			check  string
			mutate func(r *resultRow)
		}{
			{"contributor_normalization_result_fingerprint_check", func(r *resultRow) { r.fp = fingerprint(33, 1) }},
			{"contributor_normalization_result_schema_version_check", func(r *resultRow) { r.schema = ptr("") }},
			{"contributor_normalization_result_decision_class_check", func(r *resultRow) { r.class = ptr("Initials") }},
			{"contributor_normalization_result_decision_class_check", func(r *resultRow) { r.class = ptr("two words") }},
			{"contributor_normalization_result_script_check", func(r *resultRow) { r.script = ptr("cyrl") }},
			{"contributor_normalization_result_script_check", func(r *resultRow) { r.script = ptr("Cyrillic") }},
			{"contributor_normalization_result_normalized_check", func(r *resultRow) { r.display = nil }},
			{"contributor_normalization_result_normalized_check", func(r *resultRow) { r.search = ptr("") }},
			{"contributor_normalization_result_arrays_check", func(r *resultRow) { r.additionalNames = pg.Array([]*string{nil}) }},
			{"contributor_normalization_result_arrays_check", func(r *resultRow) { r.qualityFlags = pg.Array([]*string{nil}) }},
		}
		for _, c := range cases {
			r := autoRow(fp, "plain", "normalizer-v1")
			c.mutate(r)
			sf.reject(sqlstateCheck, c.check, resultInsert, r.params()...)
		}
		for _, script := range []string{"Latn", "Cyrl", "Grek", "mixed"} {
			r := autoRow(fp, "plain", "normalizer-"+script)
			r.script = &script
			sf.accept(resultInsert, r.params()...)
		}
		// An unresolved result need not have a display name.
		r := autoRow(fp, "initials", "normalizer-v1")
		r.status, r.display, r.search = "unresolved", nil, nil
		r.additionalNames = pg.Array([]string{"Иванович"})
		sf.accept(resultInsert, r.params()...)
	})
}

// RED 2: one local result per normalization key, shared by every credit.
func TestNormalizationResultOneLocalPerKey(t *testing.T) {
	f := withAuthorSchemaTx(t)
	fp := fingerprint(32, 0x22)
	first := autoRow(fp, "plain", "normalizer-v1")
	f.result(first)

	for _, method := range []string{"rules", "structured"} {
		r := autoRow(fp, "other", "normalizer-v1")
		r.method = method
		f.reject(sqlstateUnique, "contributor_normalization_result_one_local_per_key", resultInsert, r.params()...)
	}
	// A new normalizer version is a new key and a new result.
	f.result(autoRow(fp, "plain", "normalizer-v2"))
	// Manual results are not keyed and may repeat.
	actor := f.user()
	f.result(manualRow(fp, actor))
	f.result(manualRow(fp, actor))
}

// RED 10: results, overrides, acceptance classes and the audit are immutable.
func TestNormalizationHistoryIsImmutable(t *testing.T) {
	requireDatabase(t)

	t.Run("automatic result", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		r := autoRow(fingerprint(32, 0x33), "plain", "normalizer-v1")
		r.additionalNames = pg.Array([]string{"A"})
		f.requireImmutable("contributor_normalization_result", f.result(r))
	})

	t.Run("manual override", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		actor := f.user()
		fp := fingerprint(32, 0x34)
		result := f.result(manualRow(fp, actor))
		override := f.returningID(overrideInsert, nil, fp, fp, result, actor)
		f.requireImmutable("contributor_manual_override", override)
	})

	t.Run("acceptance class", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		id := f.returningID(acceptanceInsert+" RETURNING id",
			"policy-1", "plain", "normalizer-v1", fingerprint(32, 9), f.user())
		f.requireImmutable("author_acceptance_class", id)
	})

	t.Run("selection audit", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		fp := fingerprint(32, 0x35)
		credit := f.authorCredit(fp)
		f.exec(selectionInsert, credit, fp, "review", nil, nil, nil, nil, nil, nil)
		audit := f.returningID(`SELECT id FROM book_contributor_credit_selection_audit WHERE credit_id = ?`, credit)
		f.requireImmutable("book_contributor_credit_selection_audit", audit)
	})

	t.Run("trigger names and protected column sets", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		var rows []struct {
			RelName      string
			TriggerName  string
			FunctionName string
			TriggerArgs  string
		}
		_, err := f.tx.Query(&rows, `
			SELECT c.relname AS rel_name, t.tgname AS trigger_name, p.proname AS function_name,
				replace(encode(t.tgargs, 'escape'), '\000', ',') AS trigger_args
			FROM pg_trigger t
			JOIN pg_class c ON c.oid = t.tgrelid
			JOIN pg_proc p ON p.oid = t.tgfoid
			WHERE NOT t.tgisinternal AND c.relname IN (?)`, pg.In(normalizationTables))
		require.NoError(t, err)

		got := map[string]string{}
		for _, r := range rows {
			got[r.TriggerName] = r.FunctionName + "(" + strings.TrimSuffix(r.TriggerArgs, ",") + ")"
		}
		assert.Equal(t, map[string]string{
			"author_acceptance_class_immutable":             "author_metadata_reject_mutation()",
			"contributor_normalization_result_immutable":    "author_metadata_reject_mutation()",
			"contributor_manual_override_immutable":         "author_metadata_reject_mutation()",
			"book_contributor_credit_selection_consistency": "book_contributor_credit_selection_check()",
			"book_contributor_credit_selection_identity": "author_metadata_reject_mutation(" +
				"state,result_id,basis,override_id,policy_version,unresolved_reason,decided_by_user_id,decided_at)",
			"contributor_normalization_job_identity": "author_metadata_reject_mutation(" +
				"status,result_id,last_error_class,lease_owner,lease_expires_at,attempt_count,next_attempt_at," +
				"updated_at,finished_at)",
			"book_contributor_credit_selection_audited":         "book_contributor_credit_selection_record()",
			"book_contributor_credit_selection_audit_immutable": "author_metadata_reject_mutation()",
			"contributor_normalization_job_updated_at":          "update_updated_at_column()",
			"contributor_normalization_job_attempt_immutable": "author_metadata_reject_mutation(" +
				"finished_at,outcome,error_class)",
			"contributor_normalization_job_attempt_closed": "author_metadata_reject_finished()",
			"contributor_review_item_immutable": "author_metadata_reject_mutation(" +
				"status,resolution,resolution_result_id,resolved_by_user_id,finished_at)",
			"contributor_review_item_closed": "author_metadata_reject_finished()",
		}, got)
	})
}

// RED 4: a manual override has exactly one scope, names the source it is
// about and points at a manual result of that source.
func TestManualOverrideScope(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	fp := fingerprint(32, 0x44)
	other := fingerprint(32, 0x45)
	credit := f.authorCredit(fp)
	manual := f.result(manualRow(fp, actor))
	automatic := f.result(autoRow(fp, "plain", "normalizer-v1"))
	otherManual := f.result(manualRow(other, actor))

	t.Run("exactly one scope", func(t *testing.T) {
		sf := f.on(t)
		const check = "contributor_manual_override_scope_check"
		sf.reject(sqlstateCheck, check, overrideInsert, nil, nil, fp, manual, actor)
		sf.reject(sqlstateCheck, check, overrideInsert, credit, fp, fp, manual, actor)
		sf.accept(overrideInsert, credit, nil, fp, manual, actor)
		sf.accept(overrideInsert, nil, fp, fp, manual, actor)
	})

	t.Run("credit ID is positive and the credit's own fingerprint", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "contributor_manual_override_credit_check", overrideInsert, 0, nil, fp, manual, actor)
		sf.reject(sqlstateCheck, "contributor_manual_override_credit_check", overrideInsert, -1, nil, fp, manual, actor)
		sf.reject(sqlstateForeignKey, "contributor_manual_override_credit_fkey",
			overrideInsert, credit, nil, other, otherManual, actor)
	})

	t.Run("fingerprint scope is the source", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "contributor_manual_override_fingerprint_check",
			overrideInsert, nil, other, fp, manual, actor)
		sf.reject(sqlstateCheck, "contributor_manual_override_fingerprint_check",
			overrideInsert, nil, fingerprint(31, 1), fingerprint(31, 1), manual, actor)
	})

	t.Run("only author credits", func(t *testing.T) {
		sf := f.on(t)
		snapshot := sf.snapshot(&snapshotSpec{book: sf.book()})
		translator := sf.creditWith(snapshot, "translator", fp)
		sf.reject(sqlstateForeignKey, "contributor_manual_override_credit_fkey",
			overrideInsert, translator, nil, fp, manual, actor)
	})

	t.Run("a manual result of the same source", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateForeignKey, "contributor_manual_override_result_fkey",
			overrideInsert, credit, nil, fp, automatic, actor)
		sf.reject(sqlstateForeignKey, "contributor_manual_override_result_fkey",
			overrideInsert, nil, fp, fp, otherManual, actor)
		sf.reject(sqlstateCheck, "contributor_manual_override_actor_check",
			overrideInsert, nil, fp, fp, manual, 0)
	})

	t.Run("an edit is a second override, the first stays", func(t *testing.T) {
		sf := f.on(t)
		first := sf.returningID(overrideInsert, nil, fp, fp, manual, actor)
		edited := sf.result(manualRow(fp, actor))
		second := sf.returningID(overrideInsert, nil, fp, fp, edited, actor)
		var results []int64
		_, err := sf.tx.Query(&results, `SELECT result_id FROM contributor_manual_override
			WHERE id IN (?, ?) ORDER BY id`, first, second)
		require.NoError(t, err)
		assert.Equal(t, []int64{manual, edited}, results)
	})
}

// RED 3 and A2/A3: the resolution of an author credit expresses selected,
// invalid, unresolved(reason) and open review, and points only at results of
// the same normalization input.
func TestCreditSelectionStates(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	fp := fingerprint(32, 0x55)
	credit := f.authorCredit(fp)
	plain := f.result(autoRow(fp, "plain", "normalizer-v1"))
	initials := f.result(autoRow(fp, "initials", "normalizer-v2"))
	invalid := f.result(invalidRow(fp))

	// First, while nothing is registered yet.
	t.Run("an empty policy selects nothing", func(t *testing.T) {
		sf := f.on(t)
		var registered int
		_, err := sf.tx.QueryOne(pg.Scan(&registered), `SELECT count(*) FROM author_acceptance_class`)
		require.NoError(t, err)
		require.Zero(t, registered, "the acceptance policy ships empty")
		sf.reject(sqlstateCheck, "not registered in the acceptance policy",
			selectionInsert, credit, fp, "selected", plain, "automatic", nil, "policy-1", nil, nil)
	})

	t.Run("every accounting state", func(t *testing.T) {
		sf := f.on(t)
		sf.exec(acceptanceInsert, "policy-1", "plain", "normalizer-v1", fingerprint(32, 7), actor)
		sf.accept(selectionInsert, credit, fp, "selected", plain, "automatic", nil, "policy-1", nil, nil)
		sf.accept(selectionInsert, credit, fp, "unresolved", initials, nil, nil, nil, "policy_not_registered", nil)
		sf.accept(selectionInsert, credit, fp, "unresolved", nil, nil, nil, nil, "normalizer_failed", nil)
		sf.accept(selectionInsert, credit, fp, "unresolved", nil, nil, nil, nil, "review_left_unresolved", actor)
		sf.accept(selectionInsert, credit, fp, "review", initials, nil, nil, nil, nil, nil)
		sf.accept(selectionInsert, credit, fp, "review", nil, nil, nil, nil, nil, nil)
	})

	t.Run("invalid rests on an invalid result", func(t *testing.T) {
		sf := f.on(t)
		invalidCredit := sf.authorCredit(fp)
		sf.accept(selectionInsert, invalidCredit, fp, "invalid", invalid, nil, nil, nil, nil, nil)
		sf.reject(sqlstateCheck, "an invalid credit must rest on an invalid result",
			selectionInsert, invalidCredit, fp, "invalid", plain, nil, nil, nil, nil, nil)
	})

	t.Run("state shapes", func(t *testing.T) {
		sf := f.on(t)
		const shape = "book_contributor_credit_selection_shape_check"
		const basis = "book_contributor_credit_selection_basis_shape_check"
		sf.reject(sqlstateCheck, shape, selectionInsert, credit, fp, "selected", nil, "automatic", nil, "policy-1", nil, nil)
		sf.reject(sqlstateCheck, shape, selectionInsert, credit, fp, "selected", plain, nil, nil, nil, nil, nil)
		sf.reject(sqlstateCheck, shape, selectionInsert, credit, fp, "invalid", nil, nil, nil, nil, nil, nil)
		sf.reject(sqlstateCheck, shape, selectionInsert, credit, fp, "unresolved", plain, nil, nil, nil, nil, nil)
		sf.reject(sqlstateCheck, shape, selectionInsert, credit, fp, "review", nil, nil, nil, nil, "normalizer_failed", nil)
		sf.reject(sqlstateCheck, shape, selectionInsert, credit, fp, "review", plain, "automatic", nil, "policy-1", nil, nil)
		sf.reject(sqlstateCheck, basis, selectionInsert, credit, fp, "selected", plain, "automatic", nil, nil, nil, nil)
		sf.reject(sqlstateCheck, basis, selectionInsert, credit, fp, "selected", plain, "credit_override", nil, nil, nil, nil)
		sf.reject(sqlstateCheck, basis, selectionInsert, credit, fp, "unresolved", nil, nil, nil, "policy-1",
			"policy_not_registered", nil)
		for _, bad := range []string{"", "pending", "Selected"} {
			sf.reject(sqlstateCheck, "book_contributor_credit_selection_state_check",
				selectionInsert, credit, fp, bad, nil, nil, nil, nil, nil, nil)
		}
		sf.reject(sqlstateCheck, "book_contributor_credit_selection_basis_check",
			selectionInsert, credit, fp, "selected", plain, "llm", nil, nil, nil, nil)
		sf.reject(sqlstateCheck, "book_contributor_credit_selection_reason_check",
			selectionInsert, credit, fp, "unresolved", nil, nil, nil, nil, "timeout: provider said …", nil)
		sf.reject(sqlstateCheck, "book_contributor_credit_selection_actor_check",
			selectionInsert, credit, fp, "review", nil, nil, nil, nil, nil, 0)
	})

	t.Run("automatic selection only for a registered class of that configuration", func(t *testing.T) {
		sf := f.on(t)
		const notRegistered = "not registered in the acceptance policy"
		// Registered under another normalizer configuration.
		sf.exec(acceptanceInsert, "policy-2", "plain", "normalizer-v9", fingerprint(32, 7), actor)
		sf.reject(sqlstateCheck, notRegistered,
			selectionInsert, credit, fp, "selected", plain, "automatic", nil, "policy-2", nil, nil)
		// Registered class, other policy version.
		sf.exec(acceptanceInsert, "policy-3", "plain", "normalizer-v1", fingerprint(32, 7), actor)
		sf.reject(sqlstateCheck, notRegistered,
			selectionInsert, credit, fp, "selected", plain, "automatic", nil, "policy-4", nil, nil)
		// Other class.
		sf.reject(sqlstateCheck, notRegistered,
			selectionInsert, credit, fp, "selected", initials, "automatic", nil, "policy-3", nil, nil)
		sf.accept(selectionInsert, credit, fp, "selected", plain, "automatic", nil, "policy-3", nil, nil)
		// An invalid result is never selected, registered or not.
		sf.exec(acceptanceInsert, "policy-3", "malformed", "normalizer-v1", fingerprint(32, 7), actor)
		sf.reject(sqlstateCheck, "an invalid result cannot be selected",
			selectionInsert, credit, fp, "selected", invalid, "automatic", nil, "policy-3", nil, nil)
	})

	t.Run("the same normalization input", func(t *testing.T) {
		sf := f.on(t)
		otherFP := fingerprint(32, 0x57)
		otherResult := sf.result(autoRow(otherFP, "plain", "normalizer-v1"))
		sf.reject(sqlstateForeignKey, "book_contributor_credit_selection_result_fkey",
			selectionInsert, credit, fp, "review", otherResult, nil, nil, nil, nil, nil)
		// The fingerprint column cannot be faked to match another result.
		sf.reject(sqlstateForeignKey, "book_contributor_credit_selection_credit_fkey",
			selectionInsert, credit, otherFP, "review", otherResult, nil, nil, nil, nil, nil)
		// Same fingerprint, other extractor version: a different input.
		r := autoRow(fp, "plain", "normalizer-v1")
		r.extractor, r.key = ptr("extractor-v2"), keyFor(string(fp), "extractor-v2", "normalizer-v1")
		otherExtractor := sf.result(r)
		sf.reject(sqlstateCheck, "extractor version differs",
			selectionInsert, credit, fp, "review", otherExtractor, nil, nil, nil, nil, nil)
	})

	t.Run("only author credits", func(t *testing.T) {
		sf := f.on(t)
		snapshot := sf.snapshot(&snapshotSpec{book: sf.book()})
		translator := sf.creditWith(snapshot, "translator", fp)
		sf.reject(sqlstateForeignKey, "book_contributor_credit_selection_credit_fkey",
			selectionInsert, translator, fp, "review", nil, nil, nil, nil, nil, nil)
	})

	t.Run("override selection", func(t *testing.T) {
		sf := f.on(t)
		manual := sf.result(manualRow(fp, actor))
		creditOverride := sf.returningID(overrideInsert, credit, nil, fp, manual, actor)
		fingerprintOverride := sf.returningID(overrideInsert, nil, fp, fp, manual, actor)
		neighbor := sf.authorCredit(fp)
		neighborOverride := sf.returningID(overrideInsert, neighbor, nil, fp, manual, actor)

		sf.accept(selectionInsert, credit, fp, "selected", manual, "credit_override", creditOverride, nil, nil, actor)
		sf.accept(selectionInsert, credit, fp, "selected", manual, "fingerprint_override", fingerprintOverride, nil, nil, actor)
		sf.reject(sqlstateCheck, "a credit override applies only to its own credit",
			selectionInsert, credit, fp, "selected", manual, "credit_override", neighborOverride, nil, nil, actor)
		sf.reject(sqlstateCheck, "needs a fingerprint-scoped override",
			selectionInsert, credit, fp, "selected", manual, "fingerprint_override", creditOverride, nil, nil, actor)
		// The selected result must be the override's own result.
		sf.reject(sqlstateForeignKey, "book_contributor_credit_selection_override_fkey",
			selectionInsert, credit, fp, "selected", plain, "credit_override", creditOverride, nil, nil, actor)
		// A manual result is reached through its override, never as automatic.
		sf.exec(acceptanceInsert, "policy-5", "plain", "normalizer-v1", fingerprint(32, 7), actor)
		sf.reject(sqlstateCheck, "selected through its override",
			selectionInsert, credit, fp, "selected", manual, "automatic", nil, "policy-5", nil, nil)
	})

	t.Run("resolutions are not deleted", func(t *testing.T) {
		sf := f.on(t)
		sf.exec(selectionInsert, credit, fp, "review", nil, nil, nil, nil, nil, nil)
		sf.reject(sqlstateRestrict, "immutable", `DELETE FROM book_contributor_credit_selection WHERE credit_id = ?`, credit)
	})
}

// RED 3: every resolution change is appended to the audit, by the database.
func TestCreditSelectionAudit(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	fp := fingerprint(32, 0x66)
	credit := f.authorCredit(fp)
	proposal := f.result(autoRow(fp, "initials", "normalizer-v1"))
	manual := f.result(manualRow(fp, actor))
	override := f.returningID(overrideInsert, credit, nil, fp, manual, actor)

	f.exec(selectionInsert, credit, fp, "review", proposal, nil, nil, nil, nil, nil)
	// A no-op update is not a change.
	f.exec(`UPDATE book_contributor_credit_selection SET state = state WHERE credit_id = ?`, credit)
	f.exec(`UPDATE book_contributor_credit_selection
		SET state = 'selected', result_id = ?, basis = 'credit_override', override_id = ?, decided_by_user_id = ?
		WHERE credit_id = ?`, manual, override, actor, credit)

	var audit []struct {
		PreviousState    *string
		PreviousResultID *int64
		State            string
		ResultID         *int64
		Basis            *string
		OverrideID       *int64
		DecidedByUserID  *int64
	}
	_, err := f.tx.Query(&audit, `SELECT previous_state, previous_result_id, state, result_id, basis,
			override_id, decided_by_user_id
		FROM book_contributor_credit_selection_audit WHERE credit_id = ? ORDER BY id`, credit)
	require.NoError(t, err)
	require.Len(t, audit, 2)

	assert.Nil(t, audit[0].PreviousState)
	assert.Equal(t, "review", audit[0].State)
	assert.Equal(t, &proposal, audit[0].ResultID)
	assert.Nil(t, audit[0].DecidedByUserID, "a pipeline decision has no actor")

	assert.Equal(t, ptr("review"), audit[1].PreviousState)
	assert.Equal(t, &proposal, audit[1].PreviousResultID)
	assert.Equal(t, "selected", audit[1].State)
	assert.Equal(t, &manual, audit[1].ResultID)
	assert.Equal(t, ptr("credit_override"), audit[1].Basis)
	assert.Equal(t, &override, audit[1].OverrideID)
	assert.Equal(t, &actor, audit[1].DecidedByUserID)
}

// RED 2 and RED 5: one job per key; lease, counter and next-attempt rules are
// the extraction items' rules under the same column names.
func TestNormalizationJobChecks(t *testing.T) {
	f := withAuthorSchemaTx(t)
	fp := fingerprint(32, 0x77)
	key := keyFor(string(fp), "extractor-v1", "normalizer-v1")
	job := f.returningID(jobInsert, key, fp)

	t.Run("one job per normalization key", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateUnique, "contributor_normalization_job_key", jobInsert, key, fp)
		sf.accept(jobInsert, keyFor(string(fp), "extractor-v1", "normalizer-v2"), fp)
	})

	t.Run("same lease columns as extraction items", func(t *testing.T) {
		sf := f.on(t)
		jobColumns := tableColumns(t, sf.tx, "contributor_normalization_job")
		itemColumns := tableColumns(t, sf.tx, "author_metadata_run_item")
		for _, c := range []string{"status", "lease_owner", "lease_expires_at", "attempt_count", "next_attempt_at", "finished_at"} {
			assert.Equal(t, itemColumns[c], jobColumns[c], c)
			assert.NotEmpty(t, jobColumns[c], c)
		}
	})

	t.Run("lease pair, terminal lease, counters", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "contributor_normalization_job_lease_pair_check",
			`UPDATE contributor_normalization_job SET lease_owner = ? WHERE id = ?`, ownerToken, job)
		sf.reject(sqlstateCheck, "contributor_normalization_job_lease_pair_check",
			`UPDATE contributor_normalization_job SET lease_expires_at = now() + interval '1 minute' WHERE id = ?`, job)
		sf.accept(`UPDATE contributor_normalization_job
			SET lease_owner = ?, lease_expires_at = now() + interval '1 minute' WHERE id = ?`, ownerToken, job)
		sf.reject(sqlstateCheck, "contributor_normalization_job_lease_terminal_check",
			`UPDATE contributor_normalization_job
			SET status = 'failed', last_error_class = 'normalizer_panic', finished_at = now(),
				lease_owner = ?, lease_expires_at = now() + interval '1 minute' WHERE id = ?`, ownerToken, job)
		sf.reject(sqlstateCheck, "contributor_normalization_job_attempt_count_check",
			`UPDATE contributor_normalization_job SET attempt_count = -1 WHERE id = ?`, job)
		sf.accept(`UPDATE contributor_normalization_job
			SET attempt_count = 3, next_attempt_at = now() + interval '5 minutes' WHERE id = ?`, job)
	})

	t.Run("status and outcome", func(t *testing.T) {
		sf := f.on(t)
		own := sf.result(autoRow(fp, "plain", "normalizer-v1"))
		foreign := sf.result(autoRow(fp, "plain", "normalizer-v3"))
		for _, bad := range []string{"", "running", "Completed"} {
			sf.reject(sqlstateCheck, "contributor_normalization_job_status_check",
				`UPDATE contributor_normalization_job SET status = ?, finished_at = now() WHERE id = ?`, bad, job)
		}
		sf.reject(sqlstateCheck, "contributor_normalization_job_outcome_check",
			`UPDATE contributor_normalization_job SET status = 'completed', finished_at = now() WHERE id = ?`, job)
		sf.reject(sqlstateCheck, "contributor_normalization_job_outcome_check",
			`UPDATE contributor_normalization_job SET status = 'failed', finished_at = now() WHERE id = ?`, job)
		sf.reject(sqlstateCheck, "contributor_normalization_job_outcome_check",
			`UPDATE contributor_normalization_job SET result_id = ? WHERE id = ?`, own, job)
		sf.reject(sqlstateCheck, "contributor_normalization_job_finished_check",
			`UPDATE contributor_normalization_job SET status = 'completed', result_id = ? WHERE id = ?`, own, job)
		sf.reject(sqlstateCheck, "contributor_normalization_job_error_class_check",
			`UPDATE contributor_normalization_job SET status = 'failed', finished_at = now(),
				last_error_class = 'panic: index out of range' WHERE id = ?`, job)
		// The result must be the result of the job's own key.
		sf.reject(sqlstateForeignKey, "contributor_normalization_job_result_fkey",
			`UPDATE contributor_normalization_job SET status = 'completed', finished_at = now(), result_id = ? WHERE id = ?`,
			foreign, job)
		sf.accept(`UPDATE contributor_normalization_job SET status = 'completed', finished_at = now(), result_id = ? WHERE id = ?`,
			own, job)
		sf.accept(`UPDATE contributor_normalization_job SET status = 'failed', finished_at = now(),
			last_error_class = 'normalizer_failed' WHERE id = ?`, job)
	})

	t.Run("key, fingerprint and versions", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "contributor_normalization_job_key_check", jobInsert, fingerprint(31, 1), fp)
		sf.reject(sqlstateCheck, "contributor_normalization_job_key_check", jobInsert, keyFor("x"), fingerprint(16, 1))
		sf.reject(sqlstateCheck, "contributor_normalization_job_versions_check",
			`INSERT INTO contributor_normalization_job (normalization_key, source_fingerprint, extractor_version, normalizer_version)
			VALUES (?, ?, 'extractor-v1', ' normalizer-v1')`, keyFor("y"), fp)
	})

	t.Run("attempts append and finish once", func(t *testing.T) {
		sf := f.on(t)
		const insert = `INSERT INTO contributor_normalization_job_attempt (job_id, attempt_no, lease_owner)
			VALUES (?, ?, ?) RETURNING id`
		sf.reject(sqlstateCheck, "contributor_normalization_job_attempt_no_check", insert, job, 0, ownerToken)
		first := sf.returningID(insert, job, 1, ownerToken)
		sf.reject(sqlstateUnique, "contributor_normalization_job_attempt_no_key", insert, job, 1, ownerToken)
		sf.reject(sqlstateCheck, "contributor_normalization_job_attempt_finished_check",
			`UPDATE contributor_normalization_job_attempt SET finished_at = now() WHERE id = ?`, first)
		sf.reject(sqlstateCheck, "contributor_normalization_job_attempt_outcome_check",
			`UPDATE contributor_normalization_job_attempt SET finished_at = now(), outcome = 'selected' WHERE id = ?`, first)
		sf.requireImmutable("contributor_normalization_job_attempt", first, "finished_at", "outcome", "error_class")
		sf.exec(`UPDATE contributor_normalization_job_attempt
			SET finished_at = now(), outcome = 'failed', error_class = 'normalizer_failed' WHERE id = ?`, first)
		sf.reject(sqlstateRestrict, "immutable",
			`UPDATE contributor_normalization_job_attempt SET outcome = 'completed', error_class = NULL WHERE id = ?`, first)
		sf.returningID(insert, job, 2, ownerToken)
	})
}

// RED 2: at most one open review item per scope and reason; closing it frees
// the slot and freezes it.
func TestReviewItemQueue(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	fp := fingerprint(32, 0x88)
	credit := f.authorCredit(fp)
	proposal := f.result(autoRow(fp, "initials", "normalizer-v1"))
	const closeItem = `UPDATE contributor_review_item
		SET status = 'closed', resolution = ?, resolved_by_user_id = ?, finished_at = now() WHERE id = ?`

	t.Run("one open item per credit and reason", func(t *testing.T) {
		sf := f.on(t)
		item := sf.returningID(reviewInsert, credit, nil, fp, "ambiguous_decision", "initials", proposal)
		sf.reject(sqlstateUnique, "contributor_review_item_one_open_per_credit",
			reviewInsert, credit, nil, fp, "ambiguous_decision", "initials", nil)
		sf.accept(reviewInsert, credit, nil, fp, "incompatible_manual_schema", nil, nil)
		// A fingerprint-scoped item is a different scope.
		sf.accept(reviewInsert, nil, fp, fp, "ambiguous_decision", "initials", proposal)
		sf.exec(closeItem, "accepted", actor, item)
		sf.accept(reviewInsert, credit, nil, fp, "ambiguous_decision", "initials", proposal)
	})

	t.Run("one open item per fingerprint and reason", func(t *testing.T) {
		sf := f.on(t)
		sf.returningID(reviewInsert, nil, fp, fp, "ambiguous_decision", nil, nil)
		sf.reject(sqlstateUnique, "contributor_review_item_one_open_per_fingerprint",
			reviewInsert, nil, fp, fp, "ambiguous_decision", nil, nil)
	})

	t.Run("scope", func(t *testing.T) {
		sf := f.on(t)
		sf.reject(sqlstateCheck, "contributor_review_item_scope_check", reviewInsert, nil, nil, fp, "ambiguous_decision", nil, nil)
		sf.reject(sqlstateCheck, "contributor_review_item_scope_check", reviewInsert, credit, fp, fp, "ambiguous_decision", nil, nil)
		sf.reject(sqlstateCheck, "contributor_review_item_credit_check", reviewInsert, -3, nil, fp, "ambiguous_decision", nil, nil)
		sf.reject(sqlstateCheck, "contributor_review_item_fingerprint_check",
			reviewInsert, nil, fingerprint(32, 1), fp, "ambiguous_decision", nil, nil)
		snapshot := sf.snapshot(&snapshotSpec{book: sf.book()})
		translator := sf.creditWith(snapshot, "translator", fp)
		sf.reject(sqlstateForeignKey, "contributor_review_item_credit_fkey",
			reviewInsert, translator, nil, fp, "ambiguous_decision", nil, nil)
		other := sf.result(autoRow(fingerprint(32, 0x89), "initials", "normalizer-v1"))
		sf.reject(sqlstateForeignKey, "contributor_review_item_proposal_fkey",
			reviewInsert, credit, nil, fp, "incompatible_manual_schema", nil, other)
	})

	t.Run("closed enums", func(t *testing.T) {
		sf := f.on(t)
		for _, bad := range []string{"", "llm_failed", "Ambiguous_decision"} {
			sf.reject(sqlstateCheck, "contributor_review_item_reason_check", reviewInsert, credit, nil, fp, bad, nil, nil)
		}
		sf.reject(sqlstateCheck, "contributor_review_item_decision_class_check",
			reviewInsert, credit, nil, fp, "ambiguous_decision", "Initials!", nil)
		item := sf.returningID(reviewInsert, nil, fp, fp, "incompatible_manual_schema", nil, nil)
		sf.reject(sqlstateCheck, "contributor_review_item_status_check",
			`UPDATE contributor_review_item
			SET status = 'resolved', resolution = 'accepted', resolved_by_user_id = ?, finished_at = now()
			WHERE id = ?`, actor, item)
		sf.reject(sqlstateCheck, "contributor_review_item_resolution_check", closeItem, "merged", actor, item)
		for _, ok := range []string{"accepted", "edited", "classified", "left_unresolved", "retried"} {
			sf.accept(closeItem, ok, actor, item)
		}
	})

	t.Run("open and closed shapes", func(t *testing.T) {
		sf := f.on(t)
		const check = "contributor_review_item_closed_check"
		item := sf.returningID(reviewInsert, sf.authorCredit(fp), nil, fp, "ambiguous_decision", nil, nil)
		sf.reject(sqlstateCheck, check, `UPDATE contributor_review_item SET resolution = 'accepted' WHERE id = ?`, item)
		sf.reject(sqlstateCheck, check, `UPDATE contributor_review_item SET status = 'closed' WHERE id = ?`, item)
		sf.reject(sqlstateCheck, check, closeItem, "accepted", nil, item)
		sf.reject(sqlstateCheck, check, closeItem, "edited", 0, item)
		sf.reject(sqlstateCheck, check, `UPDATE contributor_review_item
			SET status = 'closed', resolution = 'accepted', resolved_by_user_id = ?,
				finished_at = created_at - interval '1 minute' WHERE id = ?`, actor, item)
		// A retry hands the item back to the normalizer; no admin decided it.
		sf.accept(closeItem, "retried", nil, item)
	})

	t.Run("protected columns while open; frozen once closed", func(t *testing.T) {
		sf := f.on(t)
		item := sf.returningID(reviewInsert, sf.authorCredit(fp), nil, fp, "incompatible_manual_schema", "initials", proposal)
		sf.requireImmutable("contributor_review_item", item,
			"status", "resolution", "resolution_result_id", "resolved_by_user_id", "finished_at")
		manual := sf.result(manualRow(fp, actor))
		sf.exec(`UPDATE contributor_review_item
			SET status = 'closed', resolution = 'edited', resolution_result_id = ?, resolved_by_user_id = ?,
				finished_at = now() WHERE id = ?`, manual, actor, item)
		sf.reject(sqlstateRestrict, "immutable",
			`UPDATE contributor_review_item SET status = 'open', resolution = NULL, resolution_result_id = NULL,
				resolved_by_user_id = NULL, finished_at = NULL WHERE id = ?`, item)
		sf.reject(sqlstateRestrict, "immutable",
			`UPDATE contributor_review_item SET resolution = 'accepted' WHERE id = ?`, item)
	})
}

// RED 8 and A2: an acceptance class names its policy version, decision
// class, configuration and the evidence report's hash.
func TestAcceptanceClassChecks(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	evidence := fingerprint(32, 7)

	f.exec(acceptanceInsert, "policy-1", "plain", "normalizer-v1", evidence, actor)
	f.reject(sqlstateUnique, "author_acceptance_class_key",
		acceptanceInsert, "policy-1", "plain", "normalizer-v2", evidence, actor)
	f.accept(acceptanceInsert, "policy-2", "plain", "normalizer-v1", evidence, actor)

	for _, n := range []int{0, 20, 31, 33} {
		f.reject(sqlstateCheck, "author_acceptance_class_evidence_check",
			acceptanceInsert, "policy-9", "plain", "normalizer-v1", fingerprint(n, 7), actor)
	}
	f.reject(sqlstateCheck, "author_acceptance_class_policy_version_check",
		acceptanceInsert, "", "plain", "normalizer-v1", evidence, actor)
	f.reject(sqlstateCheck, "author_acceptance_class_decision_class_check",
		acceptanceInsert, "policy-9", "Plain", "normalizer-v1", evidence, actor)
	f.reject(sqlstateCheck, "author_acceptance_class_config_version_check",
		acceptanceInsert, "policy-9", "plain", "normalizer-v1\n", evidence, actor)
	f.reject(sqlstateCheck, "author_acceptance_class_actor_check",
		acceptanceInsert, "policy-9", "plain", "normalizer-v1", evidence, 0)
}

// Deleting never cascades through the pipeline's history.
func TestNormalizationForeignKeysDoNotCascade(t *testing.T) {
	f := withAuthorSchemaTx(t)
	var rows []struct {
		Name string
		Rule string
	}
	_, err := f.tx.Query(&rows, `
		SELECT conname AS name, confdeltype AS rule FROM pg_constraint
		WHERE contype = 'f' AND conrelid::regclass::text IN (?)`, pg.In(normalizationTables))
	require.NoError(t, err)

	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Rule
	}
	assert.Equal(t, map[string]string{
		"contributor_manual_override_credit_fkey":                         "r",
		"contributor_manual_override_result_fkey":                         "r",
		"book_contributor_credit_selection_credit_fkey":                   "r",
		"book_contributor_credit_selection_result_fkey":                   "r",
		"book_contributor_credit_selection_override_fkey":                 "r",
		"book_contributor_credit_selection_audit_credit_id_fkey":          "r",
		"book_contributor_credit_selection_audit_previous_result_id_fkey": "r",
		"book_contributor_credit_selection_audit_result_id_fkey":          "r",
		"book_contributor_credit_selection_audit_override_id_fkey":        "r",
		"contributor_normalization_job_result_fkey":                       "r",
		"contributor_normalization_job_attempt_job_id_fkey":               "r",
		"contributor_review_item_credit_fkey":                             "r",
		"contributor_review_item_proposal_fkey":                           "r",
		"contributor_review_item_resolution_result_fkey":                  "r",
	}, got)
}

// The pipeline's ORM models name exactly the schema's columns.
func TestNormalizationModelsMatchSchema(t *testing.T) {
	f := withAuthorSchemaTx(t)

	for _, model := range []interface{}{
		(*models.AuthorAcceptanceClass)(nil),
		(*models.ContributorNormalizationResult)(nil),
		(*models.ContributorManualOverride)(nil),
		(*models.BookContributorCreditSelection)(nil),
		(*models.BookContributorCreditSelectionAudit)(nil),
		(*models.ContributorNormalizationJob)(nil),
		(*models.ContributorNormalizationJobAttempt)(nil),
		(*models.ContributorReviewItem)(nil),
	} {
		table := orm.GetTable(reflect.TypeOf(model).Elem())
		name := strings.Trim(string(table.SQLName), `"`)
		t.Run(name, func(t *testing.T) {
			var fields []string
			for _, field := range table.Fields {
				fields = append(fields, field.SQLName)
			}
			var columns []string
			for column := range tableColumns(t, f.tx, name) {
				columns = append(columns, column)
			}
			assert.ElementsMatch(t, columns, fields)
		})
	}
}

// The model enum constants are exactly the values the CHECK constraints
// accept, as the schema tests above pin them.
func TestNormalizationModelEnumsMatchSchema(t *testing.T) {
	strs := func(values ...string) []string { return values }

	assert.ElementsMatch(t, strs("structured", "rules", "llm", "manual"), enumStrings(models.NormalizationMethods()))
	assert.ElementsMatch(t, strs("person", "collective", "unknown", "malformed"), enumStrings(models.NormalizationKinds()))
	assert.ElementsMatch(t, strs("normalized", "unresolved", "invalid"), enumStrings(models.NormalizationStatuses()))
	assert.ElementsMatch(t, strs("selected", "invalid", "unresolved", "review"), enumStrings(models.CreditSelectionStates()))
	assert.ElementsMatch(t, strs("automatic", "fingerprint_override", "credit_override"),
		enumStrings(models.CreditSelectionBases()))
	assert.ElementsMatch(t, strs("policy_not_registered", "normalizer_failed", "review_left_unresolved"),
		enumStrings(models.UnresolvedReasons()))
	assert.ElementsMatch(t, strs("pending", "completed", "failed"), enumStrings(models.NormalizationJobStatuses()))
	assert.ElementsMatch(t, strs("ambiguous_decision", "incompatible_manual_schema"), enumStrings(models.ReviewReasons()))
	assert.ElementsMatch(t, strs("open", "closed"), enumStrings(models.ReviewStatuses()))
	assert.ElementsMatch(t, strs("accepted", "edited", "classified", "left_unresolved", "retried"),
		enumStrings(models.ReviewResolutions()))

	for _, s := range models.NormalizationJobStatuses() {
		assert.Equal(t, s != models.NormalizationJobPending, s.IsTerminal(), string(s))
	}
	for _, unknown := range []string{"", "running", "Completed"} {
		assert.False(t, models.NormalizationJobStatus(unknown).IsTerminal(), "%q", unknown)
	}
}

// Every pipeline model round-trips through go-pg, including the constant
// role/method columns the composite foreign keys rely on.
func TestNormalizationModelsRoundTrip(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	fp := fingerprint(32, 0x99)
	credit := f.authorCredit(fp)
	key := keyFor(string(fp), "extractor-v1", "normalizer-v1")

	class := &models.AuthorAcceptanceClass{
		PolicyVersion:        "policy-1",
		DecisionClass:        "plain",
		ConfigVersion:        "normalizer-v1",
		EvidenceReportSHA256: fingerprint(32, 3),
		RegisteredByUserID:   actor,
	}
	_, err := f.tx.Model(class).Returning("*").Insert()
	require.NoError(t, err)
	assert.False(t, class.RegisteredAt.IsZero())

	automatic := &models.ContributorNormalizationResult{
		SourceFingerprint:   fp,
		NormalizationKey:    key,
		ExtractorVersion:    ptr("extractor-v1"),
		NormalizerVersion:   ptr("normalizer-v1"),
		ResultSchemaVersion: "result-v1",
		Method:              models.NormalizationRules,
		Kind:                models.NormalizationPerson,
		Status:              models.NormalizationNormalized,
		DecisionClass:       ptr("plain"),
		GivenName:           ptr("Имя"),
		AdditionalNames:     []string{"Отчество"},
		FamilyName:          ptr("Фамилия"),
		DisplayName:         ptr("Имя Отчество Фамилия"),
		SortName:            ptr("Фамилия, Имя Отчество"),
		SearchKey:           ptr("имя отчество фамилия"),
		Script:              ptr("Cyrl"),
		QualityFlags:        []string{},
	}
	_, err = f.tx.Model(automatic).Returning("*").Insert()
	require.NoError(t, err)
	var gotResult models.ContributorNormalizationResult
	require.NoError(t, f.tx.Model(&gotResult).Where("id = ?", automatic.ID).Select())
	assert.Equal(t, key, gotResult.NormalizationKey)
	assert.Equal(t, []string{"Отчество"}, gotResult.AdditionalNames)
	assert.Nil(t, gotResult.Nickname)
	assert.Nil(t, gotResult.CreatedByUserID)

	manual := &models.ContributorNormalizationResult{
		SourceFingerprint:   fp,
		ResultSchemaVersion: "result-v1",
		Method:              models.NormalizationManual,
		Kind:                models.NormalizationCollective,
		Status:              models.NormalizationNormalized,
		DisplayName:         ptr("Коллектив"),
		SearchKey:           ptr("коллектив"),
		CreatedByUserID:     &actor,
	}
	_, err = f.tx.Model(manual).Returning("*").Insert()
	require.NoError(t, err)
	assert.Empty(t, manual.AdditionalNames)

	override := &models.ContributorManualOverride{
		ScopeCreditID:     &credit,
		SourceFingerprint: fp,
		ResultID:          manual.ID,
		CreatedByUserID:   actor,
	}
	_, err = f.tx.Model(override).Returning("*").Insert()
	require.NoError(t, err)
	assert.Equal(t, models.ContributorRoleAuthor, override.CreditRole)
	assert.Equal(t, models.NormalizationManual, override.ResultMethod)

	selection := &models.BookContributorCreditSelection{
		CreditID:          credit,
		SourceFingerprint: fp,
		State:             models.CreditSelectionSelected,
		ResultID:          &automatic.ID,
		Basis:             ptr(models.CreditSelectionAutomatic),
		PolicyVersion:     ptr("policy-1"),
	}
	_, err = f.tx.Model(selection).Returning("*").Insert()
	require.NoError(t, err)
	assert.Equal(t, models.ContributorRoleAuthor, selection.CreditRole)

	selection.State = models.CreditSelectionSelected
	selection.ResultID = &manual.ID
	selection.Basis = ptr(models.CreditSelectionCreditOverride)
	selection.OverrideID = &override.ID
	selection.PolicyVersion = nil
	selection.DecidedByUserID = &actor
	_, err = f.tx.Model(selection).WherePK().Update()
	require.NoError(t, err, "a full-model update of a resolution")

	var audit []models.BookContributorCreditSelectionAudit
	require.NoError(t, f.tx.Model(&audit).Where("credit_id = ?", credit).Order("id").Select())
	require.Len(t, audit, 2)
	assert.Equal(t, ptr(models.CreditSelectionSelected), audit[1].PreviousState)
	assert.Equal(t, &manual.ID, audit[1].ResultID)
	assert.Equal(t, ptr(models.CreditSelectionCreditOverride), audit[1].Basis)

	job := &models.ContributorNormalizationJob{
		NormalizationKey:  key,
		SourceFingerprint: fp,
		ExtractorVersion:  "extractor-v1",
		NormalizerVersion: "normalizer-v1",
	}
	_, err = f.tx.Model(job).Returning("*").Insert()
	require.NoError(t, err)
	assert.Equal(t, models.NormalizationJobPending, job.Status)
	assert.Zero(t, job.AttemptCount)

	attempt := &models.ContributorNormalizationJobAttempt{JobID: job.ID, AttemptNo: 1, LeaseOwner: ownerToken}
	_, err = f.tx.Model(attempt).Returning("*").Insert()
	require.NoError(t, err)

	job.Status = models.NormalizationJobCompleted
	job.ResultID = &automatic.ID
	job.FinishedAt = ptr(time.Now())
	_, err = f.tx.Model(job).WherePK().Update()
	require.NoError(t, err, "a full-model update of a finished job")

	item := &models.ContributorReviewItem{
		ScopeFingerprint:  fp,
		SourceFingerprint: fp,
		Reason:            models.ReviewAmbiguousDecision,
		DecisionClass:     ptr("initials"),
		ProposalResultID:  &automatic.ID,
	}
	_, err = f.tx.Model(item).Returning("*").Insert()
	require.NoError(t, err)
	assert.Equal(t, models.ReviewOpen, item.Status)
	assert.Nil(t, item.ScopeCreditID)

	item.Status = models.ReviewClosed
	item.Resolution = ptr(models.ReviewAccepted)
	item.ResolvedByUserID = &actor
	item.FinishedAt = ptr(time.Now())
	_, err = f.tx.Model(item).WherePK().Update()
	require.NoError(t, err, "a full-model update closing a review item")
}

// Fix round 1 (review B1): a resolution belongs to its credit for good. Only
// the decision may change; moving the row to another credit would drop the
// first credit's accounting without an audit transition and give the second a
// fabricated predecessor in its audit.
func TestCreditSelectionIdentityIsFrozen(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	fp := fingerprint(32, 0xa1)
	// Two author credits with the same fingerprint, each in its own snapshot:
	// the composite foreign keys accept either one.
	a := f.authorCredit(fp)
	b := f.authorCredit(fp)
	f.exec(selectionInsert, a, fp, "unresolved", nil, nil, nil, nil, "normalizer_failed", nil)

	f.reject(sqlstateRestrict, "immutable",
		`UPDATE book_contributor_credit_selection SET credit_id = ? WHERE credit_id = ?`, b, a)

	counts := func() (resolutionsA, auditA, resolutionsB, auditB int) {
		t.Helper()
		_, err := f.tx.QueryOne(pg.Scan(&resolutionsA, &auditA, &resolutionsB, &auditB), `
			SELECT (SELECT count(*) FROM book_contributor_credit_selection WHERE credit_id = ?),
				(SELECT count(*) FROM book_contributor_credit_selection_audit WHERE credit_id = ?),
				(SELECT count(*) FROM book_contributor_credit_selection WHERE credit_id = ?),
				(SELECT count(*) FROM book_contributor_credit_selection_audit WHERE credit_id = ?)`, a, a, b, b)
		require.NoError(t, err)
		return
	}
	ra, aa, rb, ab := counts()
	assert.Equal(t, []int{1, 1, 0, 0}, []int{ra, aa, rb, ab},
		"A keeps its resolution and audit; B gets neither")

	t.Run("every identity column is frozen", func(t *testing.T) {
		sf := f.on(t)
		sf.requireFrozen("book_contributor_credit_selection", "credit_id", a,
			"state", "result_id", "basis", "override_id", "policy_version",
			"unresolved_reason", "decided_by_user_id", "decided_at")
	})

	t.Run("the decision still changes and is audited truthfully", func(t *testing.T) {
		sf := f.on(t)
		sf.exec(`UPDATE book_contributor_credit_selection
			SET state = 'unresolved', unresolved_reason = 'review_left_unresolved', decided_by_user_id = ?
			WHERE credit_id = ?`, actor, a)
		var audit []struct {
			CreditID      int64
			PreviousState *string
			State         string
		}
		_, err := sf.tx.Query(&audit, `SELECT credit_id, previous_state, state
			FROM book_contributor_credit_selection_audit WHERE credit_id IN (?, ?) ORDER BY id`, a, b)
		require.NoError(t, err)
		require.Len(t, audit, 2)
		assert.Equal(t, a, audit[1].CreditID)
		assert.Equal(t, ptr("unresolved"), audit[1].PreviousState)
	})
}

// Fix round 1: a queue row keeps the input it was created for. Moving a local
// job to another key, or a run item to another run or book, would silently
// reassign work and its history.
func TestQueueIdentityIsFrozen(t *testing.T) {
	t.Run("local job", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		fp := fingerprint(32, 0xa2)
		job := f.returningID(jobInsert, keyFor(string(fp), "extractor-v1", "normalizer-v1"), fp)
		f.requireFrozen("contributor_normalization_job", "id", job,
			"status", "result_id", "last_error_class", "lease_owner", "lease_expires_at",
			"attempt_count", "next_attempt_at", "updated_at", "finished_at")
	})

	t.Run("run item", func(t *testing.T) {
		f := withAuthorSchemaTx(t)
		item := f.runItem(f.run(&runSpec{}), f.book())
		f.requireFrozen("author_metadata_run_item", "id", item,
			"status", "snapshot_id", "lease_owner", "lease_expires_at",
			"attempt_count", "next_attempt_at", "updated_at", "finished_at")
	})
}
