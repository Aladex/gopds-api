package services_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A merge eval's report is registrable when its run-wide gates hold (the
// bug on prod: run 33's census passed swap, nothing else failed, and the
// report still said registrable false). The fixture is too small for the
// run-wide evidence (200 repeats per participant), so the test runs merge1
// for real and then stores more answers: one swap census item every
// participant answered same, asked again, and reproduced, frozen again
// under new fingerprints with copies of its jobs, attempts and calls. The
// report is then built from the stored answers by the recompute.

// cloneMergeItems stores n copies of a swap census item of run whose every
// answer is same and that every participant was asked again, each with its
// jobs answered as the original's; it returns the original's id.
func cloneMergeItems(t *testing.T, db *pg.DB, run int64, n int) int64 {
	t.Helper()
	var template int64
	_, err := db.QueryOne(pg.Scan(&template), `
		SELECT i.id FROM author_llm_eval_item i
		JOIN author_llm_job j ON j.eval_item_id = i.id AND j.run_id = ?0
		LEFT JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.item_set = 'holdout' AND i.stratum LIKE 'swap.%'
		GROUP BY i.id
		HAVING count(*) FILTER (WHERE j.purpose = 'eval_repeat') = 4
			AND bool_and(j.status = 'answered' AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8')))
		ORDER BY i.id LIMIT 1`, run)
	require.NoError(t, err, "a swap census item answered same and repeated by all")
	var jobs []struct {
		ID        int64 `pg:"id"`
		AttemptID int64 `pg:"answer_attempt_id"`
	}
	_, err = db.Query(&jobs, `SELECT id, answer_attempt_id FROM author_llm_job WHERE run_id = ? AND eval_item_id = ?`,
		run, template)
	require.NoError(t, err)
	for k := range n {
		var item int64
		_, err = db.QueryOne(pg.Scan(&item), `
			INSERT INTO author_llm_eval_item (set_version, item_set, stratum, source_fingerprint, extractor_version, input, weight)
			SELECT set_version, item_set, stratum, sha256(convert_to(?, 'UTF8')), extractor_version, input, weight
			FROM author_llm_eval_item WHERE id = ? RETURNING id`, fmt.Sprintf("clone %d", k), template)
		require.NoError(t, err)
		for _, j := range jobs {
			var job int64
			_, err = db.QueryOne(pg.Scan(&job), `
				INSERT INTO author_llm_job (config_version, slot, run_id, purpose, eval_item_id, arm, repeat_no, batch_size,
					output_mode, input, with_context, context_state)
				SELECT config_version, slot, run_id, purpose, ?, arm, repeat_no, batch_size, output_mode, input,
					with_context, context_state
				FROM author_llm_job WHERE id = ? RETURNING id`, item, j.ID)
			require.NoError(t, err)
			_, err = db.Exec(`
				WITH c AS (
					INSERT INTO author_llm_call (config_version, slot, run_id, kind, items, output_mode, request_sha256,
						prompt_bound_bytes, output_ceiling, reserved_tokens, settled_tokens, created_at, finished_at, outcome,
						response_model, usage, prompt_tokens, completion_tokens, reasoning_tokens)
					SELECT c.config_version, c.slot, c.run_id, c.kind, c.items, c.output_mode, c.request_sha256,
						c.prompt_bound_bytes, c.output_ceiling, c.reserved_tokens, c.settled_tokens, c.created_at,
						c.finished_at, c.outcome, c.response_model, c.usage, c.prompt_tokens, c.completion_tokens,
						c.reasoning_tokens
					FROM author_llm_attempt a JOIN author_llm_call c ON c.id = a.call_id WHERE a.id = ?1
					RETURNING id),
				a AS (
					INSERT INTO author_llm_attempt (job_id, attempt_no, call_id, item_key, created_at, finished_at, outcome,
						validator_class, reply, canonical_sha256, tier, refused, provider_code)
					SELECT ?0, 1, c.id, a.item_key, a.created_at, a.finished_at, a.outcome, a.validator_class, a.reply,
						a.canonical_sha256, a.tier, a.refused, a.provider_code
					FROM author_llm_attempt a, c WHERE a.id = ?1
					RETURNING id)
				UPDATE author_llm_job SET status = 'answered', answer_attempt_id = a.id, attempt_count = 1,
					finished_at = now()
				FROM a WHERE author_llm_job.id = ?0`, job, j.AttemptID)
			require.NoError(t, err)
		}
	}
	return template
}

// registrableReport is the part of a merge report the test reads.
type registrableReport struct {
	Evaluation struct {
		Decision struct {
			Registrable bool     `json:"registrable"`
			Reasons     []string `json:"reasons"`
			Strata      []struct {
				Stratum string `json:"stratum"`
				Rule    string `json:"rule"`
				Passed  bool   `json:"passed"`
				Reason  string `json:"reason"`
			} `json:"strata"`
		} `json:"decision"`
	} `json:"evaluation"`
}

// A merge1 census that passed swap, with every run-wide gate held, is
// registrable: the report built from the stored answers says so, and the
// registration accepts it.
func TestAPassingCensusReportIsRegistrable(t *testing.T) {
	e, m, _, _ := stageEnv(t)
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	admin := services.NewAuthorEntityAdmin(e.db)

	// The fixture alone decides nothing: too few repeats.
	latest, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)
	var r registrableReport
	require.NoError(t, json.Unmarshal([]byte(latest.Report), &r))
	require.NotEmpty(t, r.Evaluation.Decision.Reasons)
	assert.False(t, r.Evaluation.Decision.Registrable)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed)

	cloneMergeItems(t, e.db, run, 200)
	_, stored, err := services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	require.True(t, stored)
	// The census passes once its audit sample is labeled (migration 70).
	audit, err := services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	require.NotEmpty(t, audit.Items)
	for _, it := range audit.Items {
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, it.EvalItemID, database.AdjudicationSame, "",
			"coordinator"))
	}
	next, stored, err := services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	require.True(t, stored)
	r = registrableReport{}
	require.NoError(t, json.Unmarshal([]byte(next.Report), &r))
	require.Empty(t, r.Evaluation.Decision.Reasons, "every run-wide gate holds")
	require.Len(t, r.Evaluation.Decision.Strata, 1)
	swap := r.Evaluation.Decision.Strata[0]
	require.True(t, swap.Passed, swap.Reason)
	assert.Equal(t, "census_audit", swap.Rule)
	assert.True(t, r.Evaluation.Decision.Registrable, "a passing census report backs a registration")

	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)
}
