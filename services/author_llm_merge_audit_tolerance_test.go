package services_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E4q plan B: merge8's audit samples the first 473 applied items of a kind
// in the run's audit order — rank 473 can be labeled, rank 474 cannot —
// and passes with one labeled different, not two. The apply never inserts
// the item labeled different.
func TestMerge8AuditsFourHundredSeventyThreeAndSkipsTheDifferent(t *testing.T) {
	e := newLLMEnv(t)
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	c.seedStageFixture()
	c.seedMergePersons(orphanFixture, 0xb0)
	c.build()
	require.NotEmpty(t, storedDigests(t, e, authorentity.CandidateOrphan))
	m := e.mergeConfig()
	e.fake.setRespond(mergeStub)
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge8, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)

	var template int64
	_, err = e.db.QueryOne(pg.Scan(&template), `
		SELECT i.id FROM author_llm_eval_item i
		JOIN author_llm_job j ON j.eval_item_id = i.id AND j.run_id = ?0
		LEFT JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.item_set = 'holdout' AND i.stratum LIKE 'orphan.%'
		GROUP BY i.id
		HAVING count(*) FILTER (WHERE j.purpose = 'eval_repeat') = 4
			AND bool_and(j.status = 'answered' AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8')))
		ORDER BY i.id LIMIT 1`, run)
	require.NoError(t, err, "an orphan census item answered same and repeated by all")
	applied := func() []int64 {
		var ids []int64
		_, qErr := e.db.Query(&ids, `
			SELECT j.eval_item_id FROM author_llm_job j
			JOIN author_llm_eval_item i ON i.id = j.eval_item_id AND i.stratum LIKE 'orphan.%'
			JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
			WHERE j.run_id = ? AND j.purpose IN ('eval_candidate', 'eval_judge') AND j.repeat_no = 0
				AND j.status = 'answered' AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8'))
			GROUP BY j.eval_item_id HAVING count(DISTINCT j.slot) = 4`, run)
		require.NoError(t, qErr)
		return ids
	}
	const extra = 7
	cloneItem(t, e.db, run, template, llmeval.AuditSampleTolerant+extra-len(applied()))
	ids := applied()
	require.Len(t, ids, llmeval.AuditSampleTolerant+extra)
	slices.SortFunc(ids, func(x, y int64) int {
		kx, ky := llmeval.AuditKey(run, x), llmeval.AuditKey(run, y)
		return bytes.Compare(kx[:], ky[:])
	})
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)

	// The sample is the first 473, in the database's order as in llmeval's.
	audit, err := services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	var sampled []int64
	for _, it := range audit.Items {
		sampled = append(sampled, it.EvalItemID)
	}
	assert.ElementsMatch(t, ids[:llmeval.AuditSampleTolerant], sampled)
	label := func(id int64, l string) {
		t.Helper()
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, id, l, "", "coordinator"))
	}
	for _, id := range ids[llmeval.AuditSampleTolerant:] {
		require.ErrorIs(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, id, database.AdjudicationSame, "",
			"coordinator"), database.ErrAuthorLLMNotAdjudicable, "past rank 473: %d", id)
	}
	for _, id := range ids[:llmeval.AuditSampleTolerant] {
		label(id, database.AdjudicationSame)
	}
	first, last := ids[3], ids[llmeval.AuditSampleTolerant-1]
	admin := services.NewAuthorEntityAdmin(e.db)

	// Two different: the kind fails.
	label(first, database.AdjudicationDifferent)
	label(last, database.AdjudicationDifferent)
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateOrphan, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed)

	// One different, at rank 473: it passes, and the apply skips it.
	label(first, database.AdjudicationSame)
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateOrphan, 0)
	require.NoError(t, err)
	rep, err := admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	assert.Equal(t, len(ids)-1, rep.Accepted, "every applied orphan but the different")
	assert.Equal(t, 1, rep.SkippedAdjudicatedDifferent)
	var decided int
	_, err = e.db.QueryOne(pg.Scan(&decided), `
		SELECT count(*) FROM author_entity_merge_decision d
		JOIN author_llm_eval_item i ON i.source_fingerprint = d.digest
		WHERE i.id = ? AND d.revoked_at IS NULL`, last)
	require.NoError(t, err)
	assert.Zero(t, decided, "no decision of the different")
}
