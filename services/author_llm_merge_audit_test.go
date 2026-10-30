package services_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

// The census audit (E4c, migration 70): a census mode's stratum passes on
// a human audit of its applied set — a sample of the items all four
// participants answered same — instead of on the pair's answers against
// the judges'. Only an item of that sample can be labeled; a gold error is
// reported and gates nothing.

// goldErrorStub answers like mergeStub, but both judges answer different
// on every item whose cards name name: the pair still accepts it — a gold
// error, never applied.
func goldErrorStub(name string) func(*fakeRequest) fakeReply {
	return func(req *fakeRequest) fakeReply {
		reply := mergeStub(req)
		judge := req.Model == "gpt-6-sol" || req.Model == "claude-opus-5"
		if !judge || !strings.Contains(req.User, name) {
			return reply
		}
		var out struct {
			Results []map[string]any `json:"results"`
		}
		if json.Unmarshal([]byte(reply.Content), &out) != nil {
			return reply
		}
		for _, r := range out.Results {
			r["answer"] = llmreq.MergeDifferent
		}
		content, _ := json.Marshal(out)
		reply.Content = string(content)
		return reply
	}
}

// auditGate is a stratum's audit and decision in a stored report.
type auditGate struct {
	Audit struct {
		Applied              int     `json:"applied"`
		Sample               []int64 `json:"sample"`
		AdjudicatedSame      int     `json:"adjudicated_same"`
		AdjudicatedDifferent int     `json:"adjudicated_different"`
		Unclear              int     `json:"unclear"`
		Pending              int     `json:"pending"`
		Failed               int     `json:"failed"`
		GoldErrors           int     `json:"gold_errors"`
		GoldErrorIDs         []int64 `json:"gold_error_ids"`
	}
	Rule, Reason string
	Passed       bool
	Registrable  bool
}

// readAudit reads the latest report's audit of the swap stratum.
func readAudit(t *testing.T, e *llmEnv, run int64) auditGate {
	const stratum = "swap"
	t.Helper()
	latest, err := database.GetAuthorLLMEvalReport(context.Background(), e.db, run)
	require.NoError(t, err)
	var doc struct {
		Evaluation struct {
			Strata map[string]struct {
				Combos map[string]struct {
					Audit *json.RawMessage `json:"audit"`
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
	raw := doc.Evaluation.Strata[stratum].Combos["A1"].Audit
	require.NotNil(t, raw, "%s has no audit", stratum)
	var g auditGate
	require.NoError(t, json.Unmarshal(*raw, &g.Audit))
	g.Registrable = doc.Evaluation.Decision.Registrable
	for _, d := range doc.Evaluation.Decision.Strata {
		if d.Stratum == stratum {
			g.Rule, g.Reason, g.Passed = d.Rule, d.Reason, d.Passed
		}
	}
	return g
}

// runCensus runs merge1 to completion on the stage fixture with respond
// answering, stores n copies of an applied item (cloneMergeItems: enough
// repeats for the run-wide gates) and recomputes the report; it returns
// the run and the merge admin.
func runCensus(t *testing.T, respond func(*fakeRequest) fakeReply, n int) (*llmEnv, *services.AuthorLLMAdmin, int64) {
	t.Helper()
	e, m, _, _ := stageEnv(t)
	e.fake.setRespond(respond)
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	cloneMergeItems(t, e.db, run, n)
	merge := services.NewAuthorLLMMergeAdmin(e.db, &m.identity, m.version, "", &e.settings)
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	return e, merge, run
}

// labelAll labels items as label.
func labelAll(t *testing.T, merge *services.AuthorLLMAdmin, ids []int64, label string) {
	t.Helper()
	for _, id := range ids {
		require.NoError(t, merge.Adjudicate(context.Background(), id, label, "", "coordinator"), id)
	}
}

// A census passes on its audit: while its sample is pending it fails; one
// sampled item labeled different fails it; same and unclear pass it, and
// the report backs a registration. A gold error — the pair same, both
// judges different — is reported by id, cannot be labeled and fails
// nothing. The review lists the sample with its four answers.
func TestACensusPassesOnACleanAuditOfItsAppliedSet(t *testing.T) {
	e, merge, run := runCensus(t, goldErrorStub("Волков"), 200)
	ctx := context.Background()

	g := readAudit(t, e, run)
	require.Equal(t, llmeval.GateRuleCensusAudit, g.Rule)
	require.Greater(t, g.Audit.Applied, 200)
	require.Len(t, g.Audit.Sample, g.Audit.Applied, "fewer than 300 applied: all sampled")
	assert.Equal(t, g.Audit.Applied, g.Audit.Pending)
	assert.False(t, g.Passed)
	assert.Equal(t, fmt.Sprintf("audit: %d of %d sampled items failed in A1 (0 different, %d pending; %d applied)",
		g.Audit.Applied, g.Audit.Applied, g.Audit.Applied, g.Audit.Applied), g.Reason)
	require.Equal(t, 1, g.Audit.GoldErrors, "the judges answered different on «Волков»")
	gold := g.Audit.GoldErrorIDs[0]
	assert.NotContains(t, g.Audit.Sample, gold)

	review, err := merge.AuditItems(ctx, run)
	require.NoError(t, err)
	ids := make([]int64, 0, len(review.Items))
	for _, it := range review.Items {
		ids = append(ids, it.EvalItemID)
		assert.Equal(t, map[string]string{"production_a": "same", "production_b": "same", "judge_a": "same",
			"judge_b": "same"}, it.Verdicts, it.EvalItemID)
		assert.Nil(t, it.Adjudication)
		assert.NotEmpty(t, it.A.Names)
	}
	assert.Equal(t, slices.Sorted(slices.Values(g.Audit.Sample)), ids)

	require.ErrorIs(t, merge.Adjudicate(ctx, gold, database.AdjudicationSame, "", "coordinator"),
		database.ErrAuthorLLMNotAdjudicable, "a gold error is not applied")

	odd := g.Audit.Sample[len(g.Audit.Sample)/2]
	labelAll(t, merge, g.Audit.Sample, database.AdjudicationSame)
	labelAll(t, merge, []int64{odd}, database.AdjudicationDifferent)
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	g = readAudit(t, e, run)
	assert.False(t, g.Passed)
	assert.Equal(t, 1, g.Audit.AdjudicatedDifferent)
	assert.Equal(t, 1, g.Audit.Failed)
	assert.Contains(t, g.Reason, "(1 different, 0 pending;")
	admin := services.NewAuthorEntityAdmin(e.db)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed)

	labelAll(t, merge, []int64{odd}, database.AdjudicationUnclear)
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	g = readAudit(t, e, run)
	require.True(t, g.Passed, g.Reason)
	assert.Equal(t, 1, g.Audit.Unclear)
	assert.Equal(t, g.Audit.Applied-1, g.Audit.AdjudicatedSame)
	assert.Equal(t, 1, g.Audit.GoldErrors, "still reported")
	require.True(t, g.Registrable)
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)
}

// isAudited asks migration 70's function directly.
func isAudited(t *testing.T, db *pg.DB, item int64, sample int) bool {
	t.Helper()
	var ok bool
	_, err := db.QueryOne(pg.Scan(&ok), `SELECT public.author_llm_eval_item_audited(?, ?)`, item, sample)
	require.NoError(t, err)
	return ok
}

// Past 300 applied items the sample is the first 300 in the run's audit
// order: each of them can be labeled, no other applied item can — through
// the application's insert or straight into the table (the trigger) — and
// the database's order is llmeval's and the coordinator's. Nor can a suite item only the pair
// accepted. The function's default sample is llmeval's.
func TestOnlyAnItemOfTheAuditSampleIsAdjudicable(t *testing.T) {
	e, merge, run := runCensus(t, mergeStub, 310)
	ctx := context.Background()

	g := readAudit(t, e, run)
	require.Greater(t, g.Audit.Applied, llmeval.AuditSampleSize)
	require.Len(t, g.Audit.Sample, llmeval.AuditSampleSize)
	var census []int64
	_, err := e.db.Query(&census, `SELECT id FROM author_llm_eval_item
		WHERE set_version = 'm1.merge1.holdout' AND stratum LIKE 'swap.%' ORDER BY id`)
	require.NoError(t, err)
	require.Len(t, census, g.Audit.Applied, "every census item is applied")
	var outside []int64
	for _, id := range census {
		if !slices.Contains(g.Audit.Sample, id) {
			outside = append(outside, id)
		}
	}
	require.Len(t, outside, g.Audit.Applied-llmeval.AuditSampleSize)

	for _, id := range outside {
		assert.False(t, isAudited(t, e.db, id, llmeval.AuditSampleSize), id)
		require.ErrorIs(t, merge.Adjudicate(ctx, id, database.AdjudicationSame, "", "coordinator"),
			database.ErrAuthorLLMNotAdjudicable, id)
		_, err = e.db.Exec(`INSERT INTO author_llm_eval_adjudication (eval_item_id, label, adjudicated_by)
			VALUES (?, 'same', 'coordinator')`, id)
		require.ErrorContains(t, err, "nor an audited one", id)
	}
	labelAll(t, merge, g.Audit.Sample, database.AdjudicationSame)

	// The audit order: the first item alone at a sample of one, the
	// second only from two.
	assert.True(t, isAudited(t, e.db, g.Audit.Sample[0], 1))
	assert.False(t, isAudited(t, e.db, g.Audit.Sample[1], 1))
	assert.True(t, isAudited(t, e.db, g.Audit.Sample[1], 2))
	last := g.Audit.Sample[llmeval.AuditSampleSize-1]
	assert.False(t, isAudited(t, e.db, last, llmeval.AuditSampleSize-1))

	// The coordinator's own spelling of the order picks the same sample.
	var coordinator []int64
	_, err = e.db.Query(&coordinator, `SELECT id FROM author_llm_eval_item
		WHERE set_version = 'm1.merge1.holdout' AND stratum LIKE 'swap.%'
		ORDER BY encode(sha256(convert_to(?::text || ':' || id::text, 'UTF8')), 'hex') LIMIT ?`,
		run, llmeval.AuditSampleSize)
	require.NoError(t, err)
	assert.Equal(t, g.Audit.Sample, coordinator)

	var suite []int64
	_, err = e.db.Query(&suite, `SELECT id FROM author_llm_eval_item WHERE set_version = 'm1.merge1.adversarial'`)
	require.NoError(t, err)
	require.NotEmpty(t, suite)
	for _, id := range suite {
		require.ErrorIs(t, merge.Adjudicate(ctx, id, database.AdjudicationSame, "", "coordinator"),
			database.ErrAuthorLLMNotAdjudicable, "suite item %d only the pair accepted", id)
	}

	var args string
	_, err = e.db.QueryOne(pg.Scan(&args),
		`SELECT pg_get_function_arguments('public.author_llm_eval_item_audited'::regproc)`)
	require.NoError(t, err)
	assert.Contains(t, args, fmt.Sprintf("DEFAULT %d", llmeval.AuditSampleSize))
}

// A label written after a recompute read the labels and before it stored
// the report fails the recompute (the report is bound to the labels it
// read): no version computed from the old labels is stored, and the next
// recompute reads the new label.
func TestMergeReportRecomputeRejectsAConcurrentAdjudication(t *testing.T) {
	e, merge, run := runCensus(t, mergeStub, 200)
	ctx := context.Background()
	g := readAudit(t, e, run)
	labelAll(t, merge, g.Audit.Sample, database.AdjudicationSame)
	before, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)

	odd := g.Audit.Sample[len(g.Audit.Sample)/2]
	fired := false
	services.SetMergeReportComputedHookForTest(t, func(id int64) {
		if fired || id != run {
			return
		}
		fired = true
		labelAll(t, merge, []int64{odd}, database.AdjudicationDifferent)
	})
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.True(t, fired)
	require.ErrorIs(t, err, database.ErrAuthorLLMReportStale)
	after, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)
	assert.Equal(t, before.Version, after.Version, "nothing stored")
	assert.False(t, readAudit(t, e, run).Passed)

	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	g = readAudit(t, e, run)
	assert.False(t, g.Passed)
	assert.Equal(t, 1, g.Audit.AdjudicatedDifferent)
}

// A passing report whose sample is relabeled different afterwards, and not
// recomputed, registers nothing; nor does a report stored before reports
// were bound to their labels. Recomputed, the report fails on the label;
// relabeled unclear and recomputed, it registers.
func TestANewerAuditAdjudicationInvalidatesRegistrationEvidence(t *testing.T) {
	e, merge, run := runCensus(t, mergeStub, 200)
	ctx := context.Background()
	admin := services.NewAuthorEntityAdmin(e.db)
	g := readAudit(t, e, run)
	labelAll(t, merge, g.Audit.Sample, database.AdjudicationSame)
	_, err := merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	g = readAudit(t, e, run)
	require.True(t, g.Passed, g.Reason)
	require.True(t, g.Registrable)

	odd := g.Audit.Sample[len(g.Audit.Sample)/2]
	labelAll(t, merge, []int64{odd}, database.AdjudicationDifferent)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.ErrorIs(t, err, database.ErrMergeRegistrationEvidenceStale)
	var registrations int
	_, err = e.db.QueryOne(pg.Scan(&registrations), `SELECT count(*) FROM author_entity_merge_registration`)
	require.NoError(t, err)
	assert.Zero(t, registrations)

	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	assert.False(t, readAudit(t, e, run).Passed)
	labelAll(t, merge, []int64{odd}, database.AdjudicationUnclear)
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	require.True(t, readAudit(t, e, run).Passed)

	// The same passing report without its binding, as stored before it.
	latest, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)
	_, err = e.db.Exec(`INSERT INTO author_llm_eval_report (run_id, config_version, version, report, report_sha256)
		SELECT ?0, ?1, ?2, r, sha256(convert_to(r::text, 'UTF8')) FROM (SELECT ?3::jsonb - 'adjudications' AS r) d`,
		run, latest.ConfigVersion, latest.Version+1, latest.Report)
	require.NoError(t, err)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.ErrorIs(t, err, database.ErrMergeRegistrationEvidenceStale, "an unbound report")

	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateSwap, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)
}

// The report stage is bound the same way: a label written after it read
// the labels refuses its store, and the stage computes the report again —
// the run's first stored report already reads the label.
func TestAMergeReportStageRereadsAConcurrentAdjudication(t *testing.T) {
	e, m, _, _ := stageEnv(t)
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	var labeled int64
	services.SetMergeReportComputedHookForTest(t, func(id int64) {
		if labeled != 0 || id != run {
			return
		}
		_, qErr := e.db.QueryOne(pg.Scan(&labeled), `SELECT min(id) FROM author_llm_eval_item
			WHERE set_version = 'm1.merge1.holdout' AND public.author_llm_eval_item_audited(id)`)
		require.NoError(t, qErr)
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, labeled, database.AdjudicationSame, "",
			"coordinator"))
	})
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	require.NotZero(t, labeled)
	latest, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)
	assert.Equal(t, 1, latest.Version)
	g := readAudit(t, e, run)
	assert.Contains(t, g.Audit.Sample, labeled)
	assert.Equal(t, 1, g.Audit.AdjudicatedSame, "the first report reads the label")
	assert.Equal(t, g.Audit.Applied-1, g.Audit.Pending)
}
