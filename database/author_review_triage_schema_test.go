package database

// Review triage, Task 1: the two triage tables keep their plan. A batch
// changes only in its life-cycle columns, an item only in its outcome
// columns; neither is ever deleted, and one batch at a time is active.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const triageBatchInsert = `INSERT INTO author_review_triage_batch (rule_version, status, created_by_user_id)
	VALUES ('triage-v1', ?, ?) RETURNING id`

const triageItemInsert = `INSERT INTO author_review_triage_item
	(batch_id, review_item_id, source_fingerprint, decision_class, credits, proposal_result_id,
	 step, rule, action, planned, evidence, exclusion, outcome)
	VALUES (?, ?, ?, 'initials', 3, 11, ?, ?, ?, ?, ?, ?, ?) RETURNING id`

func TestTriageTablesGuardTheirPlan(t *testing.T) {
	base := withScratchTx(t)
	admin := base.user()
	fp := fingerprint(32, 0x61)
	batch := base.returningID(triageBatchInsert, "planning", admin)

	planned := base.returningID(triageItemInsert, batch, 101, fp,
		1, "four_model", "accepted", `{"display":"Имя Фамилия"}`, `{"run_id":7}`, nil, "planned")
	excluded := base.returningID(triageItemInsert, batch, 102, fp, nil, nil, nil, nil, nil, "pinned", "excluded")

	t.Run("the plan of an item is frozen", func(t *testing.T) {
		f := base.on(t)
		for _, update := range []string{
			`UPDATE author_review_triage_item SET step = 2 WHERE id = ?`,
			`UPDATE author_review_triage_item SET planned = '{"display":"Другое Имя"}' WHERE id = ?`,
			`UPDATE author_review_triage_item SET action = 'edited' WHERE id = ?`,
			`UPDATE author_review_triage_item SET rule = 'rules:case' WHERE id = ?`,
			`UPDATE author_review_triage_item SET evidence = '{}' WHERE id = ?`,
			`UPDATE author_review_triage_item SET review_item_id = 103 WHERE id = ?`,
			`UPDATE author_review_triage_item SET proposal_result_id = 12 WHERE id = ?`,
			`UPDATE author_review_triage_item SET credits = 4 WHERE id = ?`,
			`UPDATE author_review_triage_item SET batch_id = batch_id + 1 WHERE id = ?`,
		} {
			f.reject(sqlstateRestrict, "immutable", update, planned)
		}
		f.reject(sqlstateRestrict, "immutable", `UPDATE author_review_triage_item SET exclusion = 'pinned',
			outcome = 'excluded' WHERE id = ?`, planned)
	})

	t.Run("its outcome columns change", func(t *testing.T) {
		f := base.on(t)
		f.accept(`UPDATE author_review_triage_item SET outcome = 'applied', result_id = 5, override_id = 6,
			decided_at = now() WHERE id = ?`, planned)
		f.accept(`UPDATE author_review_triage_item SET outcome = 'failed', sqlstate = '40P01' WHERE id = ?`, planned)
		f.accept(`UPDATE author_review_triage_item SET undo_requested_at = now(), revoked_at = now(),
			revoked_by_user_id = ?, undo_outcome = 'restored' WHERE id = ?`, admin, planned)
	})

	t.Run("nothing is deleted", func(t *testing.T) {
		f := base.on(t)
		f.reject(sqlstateRestrict, "immutable", `DELETE FROM author_review_triage_item WHERE id = ?`, planned)
		f.reject(sqlstateRestrict, "immutable", `DELETE FROM author_review_triage_item WHERE id = ?`, excluded)
		f.reject(sqlstateRestrict, "immutable", `DELETE FROM author_review_triage_batch WHERE id = ?`, batch)
	})

	t.Run("an item's shape", func(t *testing.T) {
		f := base.on(t)
		// A step comes with its action, an exclusion with its outcome.
		f.reject(sqlstateCheck, "author_review_triage_item_step_action_check", triageItemInsert,
			batch, 201, fp, 1, "four_model", nil, nil, nil, nil, "planned")
		f.reject(sqlstateCheck, "author_review_triage_item_step_action_check", triageItemInsert,
			batch, 202, fp, nil, nil, "accepted", nil, nil, nil, "planned")
		f.reject(sqlstateCheck, "author_review_triage_item_exclusion_check", triageItemInsert,
			batch, 203, fp, nil, nil, nil, nil, nil, nil, "excluded")
		f.reject(sqlstateCheck, "author_review_triage_item_exclusion_check", triageItemInsert,
			batch, 204, fp, nil, nil, nil, nil, nil, "pinned", "left")
		f.reject(sqlstateCheck, "author_review_triage_item_step_check", triageItemInsert,
			batch, 205, fp, 9, "x", "accepted", nil, nil, nil, "planned")
		f.reject(sqlstateCheck, "author_review_triage_item_action_check", triageItemInsert,
			batch, 206, fp, 1, "x", "retried", nil, nil, nil, "planned")
		f.reject(sqlstateCheck, "author_review_triage_item_outcome_check", triageItemInsert,
			batch, 207, fp, nil, nil, nil, nil, nil, nil, "done")
		f.reject(sqlstateCheck, "author_review_triage_item_fingerprint_check", triageItemInsert,
			batch, 208, fingerprint(16, 0x61), nil, nil, nil, nil, nil, nil, "left")
		f.reject(sqlstateUnique, "author_review_triage_item_batch_review_item_key", triageItemInsert,
			batch, 101, fp, nil, nil, nil, nil, nil, nil, "left")
		f.reject(sqlstateCheck, "author_review_triage_item_undo_outcome_check",
			`UPDATE author_review_triage_item SET undo_outcome = 'undone' WHERE id = ?`, planned)
		f.accept(triageItemInsert, batch, 209, fp, nil, nil, nil, nil, nil, nil, "left")
	})

	t.Run("a batch changes in its life cycle only", func(t *testing.T) {
		f := base.on(t)
		for _, update := range []string{
			`UPDATE author_review_triage_batch SET rule_version = 'triage-v2' WHERE id = ?`,
			`UPDATE author_review_triage_batch SET created_by_user_id = created_by_user_id + 1 WHERE id = ?`,
			`UPDATE author_review_triage_batch SET created_at = now() - interval '1 day' WHERE id = ?`,
		} {
			f.reject(sqlstateRestrict, "immutable", update, batch)
		}
		f.accept(`UPDATE author_review_triage_batch SET status = 'planned', planned_at = now(), counts = '{}',
			items_total = 2, items_done = 1, lease_owner = gen_random_uuid(), lease_until = now(),
			apply_requested_by_user_id = ?, apply_requested_at = now(), applied_steps = '{1,3}', audit = '[]',
			undo_requested_by_user_id = ?, undo_requested_at = now(), finished_at = now(), failure = 'x'
			WHERE id = ?`, admin, admin, batch)
		f.reject(sqlstateCheck, "author_review_triage_batch_status_check",
			`UPDATE author_review_triage_batch SET status = 'done' WHERE id = ?`, batch)
		f.reject(sqlstateCheck, "author_review_triage_batch_actor_check", triageBatchInsert, "planned", 0)
	})

	t.Run("one batch is active at a time", func(t *testing.T) {
		f := base.on(t)
		f.exec(`UPDATE author_review_triage_batch SET status = 'applying' WHERE id = ?`, batch)
		for _, status := range []string{"planning", "applying", "undoing"} {
			f.reject(sqlstateUnique, "author_review_triage_batch_one_active", triageBatchInsert, status, admin)
		}
		for _, status := range []string{"planned", "applied", "failed", "superseded"} {
			f.accept(triageBatchInsert, status, admin)
		}
		f.exec(`UPDATE author_review_triage_batch SET status = 'applied' WHERE id = ?`, batch)
		f.accept(triageBatchInsert, "planning", admin)
	})

	assert.Equal(t, 2, base.count(`SELECT count(*) FROM author_review_triage_item WHERE batch_id = ?`, batch))
}
