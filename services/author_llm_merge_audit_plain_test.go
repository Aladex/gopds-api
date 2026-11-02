package services_test

import (
	"context"
	"encoding/json"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A plain kind's audit (E4h round 3): the merge1 swap census's audit sample
// loads in the full projection — every field of the frozen card, the
// mixed-script ones absent since a plain card has none — and labels as
// before. No digest or label binding reads the payload: reading the audit
// again changes no report (the recompute stores nothing new), and the
// labels bind the registration.
func TestAPlainAuditLoadsAndLabelsAsBefore(t *testing.T) {
	e, m, _, _ := stageEnv(t)
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	cloneMergeItems(t, e.db, run, 200)
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)

	audit, err := services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	require.NotEmpty(t, audit.Items)
	ids := make([]int64, 0, len(audit.Items))
	for _, it := range audit.Items {
		ids = append(ids, it.EvalItemID)
	}
	frozen, err := database.AuthorLLMEvalItemsByID(ctx, e.db, ids)
	require.NoError(t, err)
	byID := map[int64]authorentity.MergeInput{}
	for _, f := range frozen {
		var in authorentity.MergeInput
		require.NoError(t, json.Unmarshal(f.Input, &in))
		byID[f.ID] = in
	}
	for _, it := range audit.Items {
		in := byID[it.EvalItemID]
		for _, pair := range []struct {
			side services.AuthorLLMAdversarialSide
			card authorentity.MergeCard
		}{{it.A, in.A}, {it.B, in.B}} {
			assert.Equal(t, pair.card.Names, pair.side.Names)
			assert.Equal(t, pair.card.Size, pair.side.Size)
			assert.Len(t, pair.side.Books, len(pair.card.Books))
			raw, marshalErr := json.Marshal(pair.side)
			require.NoError(t, marshalErr)
			var keys map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &keys))
			for key := range keys {
				assert.Contains(t, []string{"names", "size", "books"}, key, "a plain side carries no %s", key)
			}
		}
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, it.EvalItemID, database.AdjudicationSame, "",
			"coordinator"))
	}
	before, stored, err := services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	require.True(t, stored)

	_, err = services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	after, stored, err := services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	assert.False(t, stored, "reading the audit changes no report")
	assert.Equal(t, before.ReportSHA256, after.ReportSHA256)

	registration, err := services.NewAuthorEntityAdmin(e.db).RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.NoError(t, err, "the labels bind the registration as before")
	assert.Positive(t, registration)
}
