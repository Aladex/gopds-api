package services_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// The holdout audit (E4c option (c), migration 72): merge3's bucket passes
// on a human audit of its applied holdout items — the first 300 in the
// run's audit order — and of its applied extra Latin questions outside
// |A|, the first 100 in the same order. Only an item of either sample can
// be labeled; the report is bound to their labels as a census's is.

// The merge3 sets.
const (
	merge3Holdout    = "m1.merge3.holdout"
	merge3Supplement = "m1.merge3.supplement"
)

// holdoutAuditGate is the bucket stratum's two audits and decision in a
// stored report.
type holdoutAuditGate struct {
	Holdout, Supplement llmeval.AuditMetrics
	Rule, Reason        string
	Passed, Registrable bool
}

// readHoldoutAudit reads the latest report's audits of the bucket stratum.
func readHoldoutAudit(t *testing.T, e *llmEnv, run int64) holdoutAuditGate {
	t.Helper()
	latest, err := database.GetAuthorLLMEvalReport(context.Background(), e.db, run)
	require.NoError(t, err)
	var doc struct {
		Evaluation struct {
			Strata map[string]struct {
				Combos map[string]struct {
					Census     *json.RawMessage      `json:"census"`
					Audit      *llmeval.AuditMetrics `json:"audit"`
					Supplement *struct {
						Audit *llmeval.AuditMetrics `json:"audit"`
					} `json:"supplement"`
				} `json:"combos"`
			} `json:"strata"`
			Decision struct {
				Registrable bool `json:"registrable"`
				Strata      []struct {
					Stratum string `json:"stratum"`
					Rule    string `json:"rule"`
					Passed  bool   `json:"passed"`
					Reason  string `json:"reason"`
				} `json:"strata"`
			} `json:"decision"`
		} `json:"evaluation"`
	}
	require.NoError(t, json.Unmarshal([]byte(latest.Report), &doc))
	combo := doc.Evaluation.Strata[authorentity.CandidateBucket].Combos["A1"]
	require.Nil(t, combo.Census, "a holdout is no census")
	require.NotNil(t, combo.Audit, "the bucket has no holdout audit")
	require.NotNil(t, combo.Supplement)
	require.NotNil(t, combo.Supplement.Audit, "the bucket has no Latin audit")
	g := holdoutAuditGate{Holdout: *combo.Audit, Supplement: *combo.Supplement.Audit,
		Registrable: doc.Evaluation.Decision.Registrable}
	for _, d := range doc.Evaluation.Decision.Strata {
		if d.Stratum == authorentity.CandidateBucket {
			g.Rule, g.Reason, g.Passed = d.Rule, d.Reason, d.Passed
		}
	}
	return g
}

// appliedTemplate is an item of set in run all four of whose first answers
// are same, a repeated one first (its copies carry the run-wide repeat
// evidence), then the lowest id.
func appliedTemplate(t *testing.T, db *pg.DB, run int64, set string) int64 {
	t.Helper()
	var id int64
	_, err := db.QueryOne(pg.Scan(&id), `
		SELECT i.id FROM author_llm_eval_item i
		JOIN author_llm_job j ON j.eval_item_id = i.id AND j.run_id = ?0 AND j.repeat_no = 0
			AND j.purpose IN ('eval_candidate', 'eval_judge')
		JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.set_version = ?1 AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8'))
		GROUP BY i.id HAVING count(DISTINCT j.slot) = 4
		ORDER BY (SELECT count(*) FROM author_llm_job r WHERE r.run_id = ?0 AND r.eval_item_id = i.id
			AND r.purpose = 'eval_repeat') DESC, i.id LIMIT 1`, run, set)
	require.NoError(t, err, "an applied item of %s", set)
	return id
}

// runMerge3 runs merge3 to completion on the stage fixture with respond
// answering, stores holdout and supplement more copies of an applied item of
// each set — the holdout's repeated, so that 200 or more copies carry the
// run-wide repeat evidence — and recomputes the report; it returns the run
// and the merge admin.
func runMerge3(t *testing.T, respond func(*fakeRequest) fakeReply, holdout, supplement int) (
	*llmEnv, *services.AuthorLLMAdmin, int64,
) {
	t.Helper()
	e, m, _, _ := stageEnv(t)
	e.fake.setRespond(respond)
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge3, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	cloneItem(t, e.db, run, appliedTemplate(t, e.db, run, merge3Holdout), holdout)
	cloneItem(t, e.db, run, appliedTemplate(t, e.db, run, merge3Supplement), supplement)
	merge := services.NewAuthorLLMMergeAdmin(e.db, &m.identity, m.version, "", &e.settings)
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	return e, merge, run
}

// labelSuite labels every suite item of merge3 all four answered same.
func labelSuite(t *testing.T, merge *services.AuthorLLMAdmin, run int64) {
	t.Helper()
	review, err := merge.AdversarialItems(context.Background(), run)
	require.NoError(t, err)
	for i := range review.Items {
		labelAll(t, merge, []int64{review.Items[i].EvalItemID}, database.AdjudicationSame)
	}
}

// appliedIDs are the items of set in run all four answered same, in the
// coordinator's spelling of the audit order.
func appliedIDs(t *testing.T, db *pg.DB, run int64, set string) []int64 {
	t.Helper()
	var ids []int64
	_, err := db.Query(&ids, `
		SELECT i.id FROM author_llm_eval_item i
		JOIN author_llm_job j ON j.eval_item_id = i.id AND j.run_id = ?0 AND j.repeat_no = 0
			AND j.purpose IN ('eval_candidate', 'eval_judge')
		JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.set_version = ?1 AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8'))
		GROUP BY i.id HAVING count(DISTINCT j.slot) = 4
		ORDER BY encode(sha256(convert_to(?0::text || ':' || i.id::text, 'UTF8')), 'hex')`, run, set)
	require.NoError(t, err)
	return ids
}

// merge3 is gated on its two audits: pending, it fails on the Latin one's;
// with the Latin one's labeled, on the holdout's; one Latin item labeled
// different fails it; with both labeled same or unclear it passes and
// registers the bucket. The review lists both samples, each
// item with its set and four answers.
func TestMerge3PassesOnACleanAuditOfBothSamples(t *testing.T) {
	e, merge, run := runMerge3(t, mergeStub, 310, 105)
	ctx := context.Background()

	g := readHoldoutAudit(t, e, run)
	require.Equal(t, llmeval.GateRuleHoldoutAudit, g.Rule)
	require.Greater(t, g.Holdout.Applied, llmeval.AuditSampleSize)
	require.Len(t, g.Holdout.Sample, llmeval.AuditSampleSize)
	require.Greater(t, g.Supplement.Applied, llmeval.AuditSupplementSize)
	require.Len(t, g.Supplement.Sample, llmeval.AuditSupplementSize)
	assert.Equal(t, appliedIDs(t, e.db, run, merge3Holdout)[:llmeval.AuditSampleSize], g.Holdout.Sample)
	assert.Equal(t, appliedIDs(t, e.db, run, merge3Supplement)[:llmeval.AuditSupplementSize], g.Supplement.Sample)
	assert.False(t, g.Passed)
	assert.Equal(t, fmt.Sprintf("supplement audit: 100 of 100 sampled items failed in A1 (0 different, 100 pending; %d applied)",
		g.Supplement.Applied), g.Reason)

	review, err := merge.AuditItems(ctx, run)
	require.NoError(t, err)
	sets := map[int64]string{}
	for i := range review.Items {
		it := &review.Items[i]
		sets[it.EvalItemID] = it.Set
		assert.Equal(t, map[string]string{"production_a": "same", "production_b": "same", "judge_a": "same",
			"judge_b": "same"}, it.Verdicts, it.EvalItemID)
	}
	require.Len(t, sets, llmeval.AuditSampleSize+llmeval.AuditSupplementSize)
	for _, id := range g.Holdout.Sample {
		assert.Equal(t, merge3Holdout, sets[id], id)
	}
	for _, id := range g.Supplement.Sample {
		assert.Equal(t, merge3Supplement, sets[id], id)
	}

	labelSuite(t, merge, run)
	labelAll(t, merge, g.Supplement.Sample, database.AdjudicationSame)
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	g = readHoldoutAudit(t, e, run)
	assert.False(t, g.Passed)
	assert.Equal(t, fmt.Sprintf("audit: 300 of 300 sampled items failed in A1 (0 different, 300 pending; %d applied)",
		g.Holdout.Applied), g.Reason)

	labelAll(t, merge, g.Holdout.Sample, database.AdjudicationSame)
	labelAll(t, merge, g.Supplement.Sample[:1], database.AdjudicationDifferent)
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	g = readHoldoutAudit(t, e, run)
	assert.False(t, g.Passed)
	assert.Contains(t, g.Reason, "supplement audit: 1 of 100 sampled items failed in A1 (1 different, 0 pending;")
	admin := services.NewAuthorEntityAdmin(e.db)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateBucket, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed)

	labelAll(t, merge, g.Supplement.Sample[:1], database.AdjudicationUnclear)
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	g = readHoldoutAudit(t, e, run)
	require.True(t, g.Passed, g.Reason)
	require.True(t, g.Registrable)
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateBucket, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)
}

// Each sample is the first of its set's applied items in the run's audit
// order: each of them can be labeled, no other applied item of the set can
// — through the application's insert or straight into the table (the
// trigger) —, and the database's order is llmeval's.
func TestOnlyAnItemOfAMerge3AuditSampleIsAdjudicable(t *testing.T) {
	e, merge, run := runMerge3(t, mergeStub, 310, 105)
	ctx := context.Background()
	g := readHoldoutAudit(t, e, run)
	for _, tc := range []struct {
		set    string
		sample []int64
		size   int
	}{
		{merge3Holdout, g.Holdout.Sample, llmeval.AuditSampleSize},
		{merge3Supplement, g.Supplement.Sample, llmeval.AuditSupplementSize},
	} {
		applied := appliedIDs(t, e.db, run, tc.set)
		require.Greater(t, len(applied), tc.size, tc.set)
		for _, id := range applied[tc.size:] {
			assert.False(t, isAudited(t, e.db, id, llmeval.AuditSampleSize), "%s %d", tc.set, id)
			require.ErrorIs(t, merge.Adjudicate(ctx, id, database.AdjudicationSame, "", "coordinator"),
				database.ErrAuthorLLMNotAdjudicable, "%s %d", tc.set, id)
			_, err := e.db.Exec(`INSERT INTO author_llm_eval_adjudication (eval_item_id, label, adjudicated_by)
				VALUES (?, 'same', 'coordinator')`, id)
			require.ErrorContains(t, err, "nor an audited one", "%s %d", tc.set, id)
		}
		labelAll(t, merge, tc.sample, database.AdjudicationSame)

		// The order: the first item alone at a sample of one, the second
		// only from two, the last only at the full size.
		assert.True(t, isAudited(t, e.db, tc.sample[0], 1), tc.set)
		assert.False(t, isAudited(t, e.db, tc.sample[1], 1), tc.set)
		assert.True(t, isAudited(t, e.db, tc.sample[1], 2), tc.set)
		last := tc.sample[tc.size-1]
		assert.False(t, isAudited(t, e.db, last, tc.size-1), tc.set)
		assert.True(t, isAudited(t, e.db, last, llmeval.AuditSampleSize), tc.set)
	}
}

// A passing merge3 report whose Latin sample is relabeled different
// afterwards, and not recomputed, registers nothing; recomputed, it fails
// on the label. A label written while a recompute computes refuses its
// store.
func TestANewerLatinAuditLabelInvalidatesMerge3Evidence(t *testing.T) {
	e, merge, run := runMerge3(t, mergeStub, 210, 10)
	ctx := context.Background()
	g := readHoldoutAudit(t, e, run)
	labelSuite(t, merge, run)
	labelAll(t, merge, g.Holdout.Sample, database.AdjudicationSame)
	labelAll(t, merge, g.Supplement.Sample, database.AdjudicationSame)
	_, err := merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	require.True(t, readHoldoutAudit(t, e, run).Passed)

	odd := g.Supplement.Sample[len(g.Supplement.Sample)/2]
	labelAll(t, merge, []int64{odd}, database.AdjudicationDifferent)
	admin := services.NewAuthorEntityAdmin(e.db)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateBucket, 0)
	require.ErrorIs(t, err, database.ErrMergeRegistrationEvidenceStale)

	fired := false
	services.SetMergeReportComputedHookForTest(t, func(id int64) {
		if fired || id != run {
			return
		}
		fired = true
		labelAll(t, merge, []int64{odd}, database.AdjudicationUnclear)
	})
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.True(t, fired)
	require.ErrorIs(t, err, database.ErrAuthorLLMReportStale)

	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	g = readHoldoutAudit(t, e, run)
	require.True(t, g.Passed, g.Reason)
	assert.Equal(t, 1, g.Supplement.Unclear)
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateBucket, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)
}

// A merge3 report stored before the holdout audit — its bucket passed under
// the bound (rule holdout), registrable, with a binding still current — is
// refused: the bucket registers only from a report that passed its audits.
// The same run recomputed under the audit registers.
func TestALegacyHoldoutPassOfTheBucketDoesNotRegister(t *testing.T) {
	e, merge, run := runMerge3(t, mergeStub, 210, 10)
	ctx := context.Background()
	g := readHoldoutAudit(t, e, run)
	labelSuite(t, merge, run)
	labelAll(t, merge, g.Holdout.Sample, database.AdjudicationSame)
	labelAll(t, merge, g.Supplement.Sample, database.AdjudicationSame)
	_, err := merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	require.True(t, readHoldoutAudit(t, e, run).Passed)

	// The same passing report as the bound would have stored it.
	latest, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)
	_, err = e.db.Exec(`INSERT INTO author_llm_eval_report (run_id, config_version, version, report, report_sha256)
		SELECT ?0, ?1, ?2, r, sha256(convert_to(r::text, 'UTF8'))
		FROM (SELECT jsonb_set(jsonb_set(?3::jsonb, '{evaluation,decision,strata,0,rule}', '"holdout"'),
			'{options,gate_rule}', '"holdout"') AS r) d`,
		run, latest.ConfigVersion, latest.Version+1, latest.Report)
	require.NoError(t, err)
	var legacy registrableReport
	stored, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(stored.Report), &legacy))
	require.True(t, legacy.Evaluation.Decision.Registrable)
	require.Len(t, legacy.Evaluation.Decision.Strata, 1)
	require.Equal(t, llmeval.GateRuleHoldout, legacy.Evaluation.Decision.Strata[0].Rule)
	require.True(t, legacy.Evaluation.Decision.Strata[0].Passed)

	admin := services.NewAuthorEntityAdmin(e.db)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateBucket, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed)
	var registrations int
	_, err = e.db.QueryOne(pg.Scan(&registrations), `SELECT count(*) FROM author_entity_merge_registration`)
	require.NoError(t, err)
	assert.Zero(t, registrations)

	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateBucket, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)
}

// Other modes keep their registration check: a merge1 report whose swap
// passed under the census rule stored before the audit (rule census) still
// registers swap — only the bucket must have passed its holdout audit.
func TestALegacyCensusPassStillRegisters(t *testing.T) {
	e, merge, run := runCensus(t, mergeStub, 200)
	ctx := context.Background()
	labelAll(t, merge, readAudit(t, e, run).Audit.Sample, database.AdjudicationSame)
	_, err := merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	latest, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)
	_, err = e.db.Exec(`INSERT INTO author_llm_eval_report (run_id, config_version, version, report, report_sha256)
		SELECT ?0, ?1, ?2, r, sha256(convert_to(r::text, 'UTF8'))
		FROM (SELECT jsonb_set(jsonb_set(?3::jsonb, '{evaluation,decision,strata,0,rule}', '"census"'),
			'{options,gate_rule}', '"census"') AS r) d`,
		run, latest.ConfigVersion, latest.Version+1, latest.Report)
	require.NoError(t, err)
	g := readAudit(t, e, run)
	require.Equal(t, llmeval.GateRuleCensus, g.Rule)
	require.True(t, g.Passed)
	require.True(t, g.Registrable)

	registration, err := services.NewAuthorEntityAdmin(e.db).RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)
}
