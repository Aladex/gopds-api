package services_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"slices"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An orphan a production repeat dissents on is withheld from the apply
// (E4q), so the merge8 audit never samples it: it is not adjudicable, it
// takes no rank of the capped sample — the applied orphan it would have
// displaced is audited —, and the registration it cannot back passes on the
// others while the apply inserts none of it.

// orphanFixture: «Евгений Петров» on two books and «Евгений Петрович
// Петров» on one, of one genre: an orphan pair the stub answers same.
var orphanFixture = []mergePerson{
	{"Петров", "Евгений", "", "Cyrl", "sf", 2, 0},
	{"Петров", "Евгений", "Петрович", "Cyrl", "sf", 1, 0},
}

func TestADissentingOrphanRepeatIsNeitherAuditedNorApplied(t *testing.T) {
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

	// One applied orphan over the audit cap: the template's clones.
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
	cloneItem(t, e.db, run, template, llmeval.AuditSampleSize-len(applied()))
	// One more whose production_b repeat answered different.
	different := sha256.Sum256([]byte(`{"answer":"different"}`))
	dissenter := cloneItemAnswering(t, e.db, run, template, 1, "dissenting", func(slot, purpose string) []byte {
		if slot == string(llmreq.SlotProductionB) && purpose == string(models.AuthorLLMPurposeEvalRepeat) {
			return different[:]
		}
		return nil
	})[0]
	ids := applied()
	require.Len(t, ids, llmeval.AuditSampleSize+1)
	slices.SortFunc(ids, func(x, y int64) int {
		kx, ky := llmeval.AuditKey(run, x), llmeval.AuditKey(run, y)
		return bytes.Compare(kx[:], ky[:])
	})
	// Inside the cap: by first answers it would take a rank of the sample.
	require.Less(t, slices.Index(ids, dissenter), llmeval.AuditSampleSize)
	others := slices.DeleteFunc(slices.Clone(ids), func(id int64) bool { return id == dissenter })
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)

	audit, err := services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	var sampled []int64
	for _, it := range audit.Items {
		sampled = append(sampled, it.EvalItemID)
	}
	assert.NotContains(t, sampled, dissenter)
	assert.ElementsMatch(t, others, sampled, "every other applied orphan, the cap's last included")
	require.ErrorIs(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, dissenter, database.AdjudicationSame, "",
		"coordinator"), database.ErrAuthorLLMNotAdjudicable)

	for _, id := range sampled {
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, id, database.AdjudicationSame, "",
			"coordinator"))
	}
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	admin := services.NewAuthorEntityAdmin(e.db)
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateOrphan, 0)
	require.NoError(t, err)
	rep, err := admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	assert.Equal(t, llmeval.AuditSampleSize, rep.Accepted, "the dissenting orphan is never inserted")
}
