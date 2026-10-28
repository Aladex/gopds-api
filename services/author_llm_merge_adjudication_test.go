package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Adjudicating the merge eval's accepted suite items (E4c-2b-0): an
// accepted namesake fails its stratum only while it is labeled two people
// or not labeled; a recompute picks the labels up as the run's next report
// version, under the merge configuration's identity.

// credulousStub answers like mergeStub, but the judges too go by the names
// alone: every namesake the pair accepts, the production rule accepts.
func credulousStub(req *fakeRequest) fakeReply {
	judge := req.Model
	req.Model = "gpt-6-luna"
	reply := mergeStub(req)
	req.Model = judge
	if reply.Prompt != 1200 {
		reply.Prompt = distinctLabs[judge]
	}
	return reply
}

// runMergePilot seeds the fixture, builds the entities and runs a merge0
// eval to completion with respond answering; it returns the run.
func runMergePilot(t *testing.T, e *llmEnv, m *mergeConfig, respond func(*fakeRequest) fakeReply) int64 {
	t.Helper()
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	c.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES ('4', 'structured_person', 'Cyrl', 'normalizer-v1', ?, 'internal/authornorm/policy_evidence/fixture.json',
		 'shipped')`, bytes.Repeat([]byte{0xee}, 32))
	c.seedMergeFixture()
	c.build()
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge0, nil)
	require.NoError(t, err)
	e.fake.setRespond(respond)
	driver := e.mergeWorker(m).EvalDriver().WithMergeSizes(mergeEvalSizes)
	w := e.mergeWorker(m)
	for range 300 {
		if _, status := e.runStage(run); status == string(models.AuthorLLMRunCompleted) {
			return run
		}
		_, err = driver.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the merge eval did not complete")
	return run
}

// suiteGate is a stratum's suite counts and decision in a stored report.
type suiteGate struct {
	Accepted, AdjudicatedSame, AdjudicatedDifferent, Unclear, Pending, Failed int
	AcceptedIDs                                                               []int64
	Passed                                                                    bool
	Reason                                                                    string
}

// reportGate reads the latest report version's suite gate of a stratum.
func reportGate(t *testing.T, e *llmEnv, run int64, stratum string) (gate suiteGate, version int) {
	t.Helper()
	latest, err := database.GetAuthorLLMEvalReport(context.Background(), e.db, run)
	require.NoError(t, err)
	var doc struct {
		Evaluation struct {
			Strata map[string]struct {
				Combos map[string]struct {
					Adversarial *struct {
						Accepted             int     `json:"accepted"`
						AdjudicatedSame      int     `json:"adjudicated_same"`
						AdjudicatedDifferent int     `json:"adjudicated_different"`
						Unclear              int     `json:"unclear"`
						Pending              int     `json:"pending"`
						Failed               int     `json:"failed"`
						AcceptedIDs          []int64 `json:"accepted_ids"`
					} `json:"adversarial"`
				} `json:"combos"`
			} `json:"strata"`
			Decision struct {
				Strata []struct {
					Stratum string `json:"stratum"`
					Reason  string `json:"reason"`
				} `json:"strata"`
			} `json:"decision"`
		} `json:"evaluation"`
	}
	require.NoError(t, json.Unmarshal([]byte(latest.Report), &doc))
	a := doc.Evaluation.Strata[stratum].Combos["A1"].Adversarial
	require.NotNil(t, a, stratum)
	gate = suiteGate{Accepted: a.Accepted, AdjudicatedSame: a.AdjudicatedSame,
		AdjudicatedDifferent: a.AdjudicatedDifferent, Unclear: a.Unclear, Pending: a.Pending, Failed: a.Failed,
		AcceptedIDs: a.AcceptedIDs}
	for _, d := range doc.Evaluation.Decision.Strata {
		if d.Stratum == stratum {
			gate.Reason = d.Reason
		}
	}
	return gate, latest.Version
}

// An accepted namesake labeled one person or unclear no longer fails its
// stratum; one labeled two people, or not labeled, still does (the
// fixture's run decides nothing as a whole — too few repeats — so the gate
// is read in its counts; llmeval's tests pin the decision). The labels
// reach the report through the merge admin's recompute, a new version of
// the same run; the latest label of an item is its label.
func TestAdjudicationsDecideTheSuiteGate(t *testing.T) {
	e := newLLMEnv(t)
	m := e.mergeConfig()
	run := runMergePilot(t, e, m, credulousStub)
	ctx := context.Background()
	merge := services.NewAuthorLLMMergeAdmin(e.db, &m.identity, m.version, "", &e.settings)

	swap, version := reportGate(t, e, run, "swap")
	require.Equal(t, 1, version)
	require.Equal(t, 3, swap.Accepted, "the judges went by the names")
	assert.Equal(t, 3, swap.Pending)
	assert.Equal(t, 3, swap.Failed)
	initials, _ := reportGate(t, e, run, "initials")
	require.Equal(t, 2, initials.Accepted)

	// The review lists every accepted item with its sides and answers.
	review, err := merge.AdversarialItems(ctx, run)
	require.NoError(t, err)
	require.Len(t, review.Items, 5)
	for _, it := range review.Items {
		assert.NotEmpty(t, it.A.Names)
		assert.NotEmpty(t, it.B.Books)
		assert.Len(t, it.Verdicts, 4)
		for slot, v := range it.Verdicts {
			assert.Equal(t, "same", v, slot)
		}
		assert.Nil(t, it.Adjudication)
	}

	ids := swap.AcceptedIDs
	require.NoError(t, merge.Adjudicate(ctx, ids[0], database.AdjudicationSame, "one person, a wrong patronymic", "admin"))
	require.NoError(t, merge.Adjudicate(ctx, ids[1], database.AdjudicationUnclear, "", "admin"))
	require.NoError(t, merge.Adjudicate(ctx, ids[2], database.AdjudicationDifferent, "", "admin"))
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	swap, version = reportGate(t, e, run, "swap")
	assert.Equal(t, 2, version, "a recompute is the run's next version")
	assert.Equal(t, suiteGate{Accepted: 3, AdjudicatedSame: 1, Unclear: 1, AdjudicatedDifferent: 1, Failed: 1,
		AcceptedIDs: ids, Reason: swap.Reason}, swap)

	// The latest label wins: two people becomes one person.
	require.NoError(t, merge.Adjudicate(ctx, ids[2], database.AdjudicationSame, "", "admin"))
	_, err = merge.RecomputeEvalReport(ctx, run)
	require.NoError(t, err)
	swap, _ = reportGate(t, e, run, "swap")
	assert.Zero(t, swap.Failed)
	initials, _ = reportGate(t, e, run, "initials")
	assert.Equal(t, 2, initials.Pending)
	assert.Equal(t, 2, initials.Failed, "unlabeled accepts still fail")
	assert.Equal(t, 4, e.count(`SELECT count(*) FROM author_llm_eval_adjudication`), "history is kept")

	review, err = merge.AdversarialItems(ctx, run)
	require.NoError(t, err)
	labeled := 0
	for _, it := range review.Items {
		if it.Adjudication != nil {
			labeled++
			assert.Equal(t, "admin", it.Adjudication.AdjudicatedBy)
		}
	}
	assert.Equal(t, 3, labeled)
}

// Only an adversarial suite item a merge eval accepted can be labeled; a
// label outside the closed set is refused; K's admin labels nothing, lists
// nothing and never recomputes a merge report; a merge recompute needs the
// merge identity.
func TestAdjudicationIsRefusedOutsideTheAcceptedSuite(t *testing.T) {
	e := newLLMEnv(t)
	m := e.mergeConfig()
	run := runMergePilot(t, e, m, mergeStub)
	ctx := context.Background()
	merge := services.NewAuthorLLMMergeAdmin(e.db, &m.identity, m.version, "", &e.settings)
	k := services.NewAuthorLLMAdmin(e.db, e.version, "", &e.settings)

	var holdout, suite int64
	_, err := e.db.QueryOne(pg.Scan(&holdout), `SELECT min(id) FROM author_llm_eval_item WHERE item_set = 'holdout'`)
	require.NoError(t, err)
	_, err = e.db.QueryOne(pg.Scan(&suite), `SELECT min(id) FROM author_llm_eval_item WHERE item_set = 'adversarial'`)
	require.NoError(t, err)
	assert.ErrorIs(t, merge.Adjudicate(ctx, holdout, database.AdjudicationSame, "", "admin"),
		database.ErrAuthorLLMNotAdjudicable, "a holdout item")
	assert.ErrorIs(t, merge.Adjudicate(ctx, suite, database.AdjudicationSame, "", "admin"),
		database.ErrAuthorLLMNotAdjudicable, "a suite item the judges refused")
	assert.ErrorIs(t, merge.Adjudicate(ctx, suite, "maybe", "", "admin"), services.ErrAuthorLLMInvalidAdjudication)
	_, err = e.db.Exec(`INSERT INTO author_llm_eval_adjudication (eval_item_id, label, adjudicated_by)
		VALUES (?, 'same', 'admin')`, holdout)
	require.Error(t, err, "the table refuses a non-adversarial item itself")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_eval_adjudication`))

	assert.ErrorIs(t, k.Adjudicate(ctx, suite, database.AdjudicationSame, "", "admin"), services.ErrAuthorLLMInvalidRun)
	_, err = k.AdversarialItems(ctx, run)
	assert.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun)
	_, err = k.RecomputeEvalReport(ctx, run)
	assert.Error(t, err, "K's admin recomputed a merge report")
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &e.identity, m.version, run)
	assert.ErrorIs(t, err, services.ErrAuthorLLMInvalidRun, "a merge recompute under K's identity")
}
