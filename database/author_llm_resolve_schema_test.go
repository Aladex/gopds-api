package database

import (
	"testing"
)

// Migration 34: the LLM resolve run's closed values. A review item the
// service decides closes as automatic without an admin, and only that
// resolution may; the selection takes the LLM's unresolved reasons; an llm
// result is one per normalization key; a resolve report is immutable.

func TestResolveClosesAReviewItemWithoutAnAdmin(t *testing.T) {
	f := withAuthorSchemaTx(t)
	actor := f.user()
	fp := fingerprint(32, 0x71)
	proposal := f.result(autoRow(fp, "initials", "normalizer-v1"))
	const closeItem = `UPDATE contributor_review_item
		SET status = 'closed', resolution = ?, resolved_by_user_id = ?, finished_at = now() WHERE id = ?`

	sf := f.on(t)
	item := sf.returningID(reviewInsert, nil, fp, fp, "ambiguous_decision", "initials", proposal)
	sf.accept(closeItem, "automatic", nil, item)
	sf.reject(sqlstateCheck, "contributor_review_item_closed_check", closeItem, "automatic", actor, item)
	sf.reject(sqlstateCheck, "contributor_review_item_closed_check", closeItem, "accepted", nil, item)
	sf.accept(closeItem, "retried", nil, item)
	sf.accept(closeItem, "accepted", actor, item)
}

func TestSelectionTakesTheLLMReasons(t *testing.T) {
	f := withAuthorSchemaTx(t)
	fp := fingerprint(32, 0x72)
	credit := f.authorCredit(fp)
	proposal := f.result(autoRow(fp, "initials", "normalizer-v1"))
	sf := f.on(t)
	for _, reason := range []string{"llm_disagreement", "llm_refused", "llm_invalid", "llm_failed",
		"llm_judge_disagreement", "llm_judge_refused", "llm_judge_invalid", "llm_judge_failed", "llm_not_registered",
		"llm_context_unavailable"} {
		sf.accept(selectionInsert, credit, fp, "unresolved", proposal, nil, nil, nil, reason, nil)
	}
	for _, bad := range []string{"llm_other", "apply_failed", "LLM_REFUSED"} {
		sf.reject(sqlstateCheck, "book_contributor_credit_selection_reason_check",
			selectionInsert, credit, fp, "unresolved", proposal, nil, nil, nil, bad, nil)
	}
}

func TestOneLLMResultPerKey(t *testing.T) {
	f := withAuthorSchemaTx(t)
	fp := fingerprint(32, 0x73)
	llm := func() *resultRow {
		r := autoRow(fp, "llm.initials", "llm1-"+hex64('a'))
		r.method = "llm"
		r.key = keyFor(string(fp), "extractor-v1", *r.normalizer)
		return r
	}
	sf := f.on(t)
	sf.result(llm())
	sf.reject(sqlstateUnique, "contributor_normalization_result_one_llm_per_key", resultInsert, llm().params()...)
	// A local result of the same key is another method.
	local := llm()
	local.method = "rules"
	sf.result(local)
}

func TestResolveReportIsImmutable(t *testing.T) {
	f := withAuthorSchemaTx(t)
	sf := f.on(t)
	version := "llm1-" + hex64('b')
	sf.exec(`INSERT INTO author_llm_config (version, base_url, identity, prompt_sha256, schema_sha256,
		validator_version, tokenizer_version, context_version)
		VALUES (?, 'http://llm.local/v1', '{}', ?, ?, 'v', 't', 'c')`, version, hex64('c'), hex64('d'))
	run := sf.returningID(`INSERT INTO author_llm_run (kind, mode, config_version) VALUES ('eval', 'dev', ?)
		RETURNING id`, version)
	report := sf.returningID(`INSERT INTO author_llm_resolve_report (run_id, report, report_sha256)
		VALUES (?, '{}', sha256('x')) RETURNING id`, run)
	sf.reject(sqlstateRestrict, "immutable", `UPDATE author_llm_resolve_report SET report = '{"a":1}' WHERE id = ?`, report)
	sf.reject(sqlstateRestrict, "immutable", `DELETE FROM author_llm_resolve_report WHERE id = ?`, report)
}

func hex64(c byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = c
	}
	return string(out)
}
