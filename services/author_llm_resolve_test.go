package services_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resolve run end to end (design K, phase 6) on the real scanner's
// catalog: the review queue of the fixture books, the eval report that
// registers its strata, the production pair and both judges answered by the
// fake endpoint, the verdicts applied to the credits. An answer is applied
// only when the pair agrees and both judges confirm it (run-4 rule); every
// other input ends unresolved with a closed reason, its review item closed by
// the service.

// resolveSizes caps the pilot to a fixture catalog.
var resolveSizes = services.AuthorLLMResolveSizes{Pilot: 500}

// allSlotsEvidence is what a completed eval run learned for all four
// participants: their model ids and the fixed request's prompt tokens.
func allSlotsEvidence() (
	expected map[llmreq.Slot][]string, reference map[llmreq.Slot]database.AuthorLLMFingerprintReference,
) {
	expected = map[llmreq.Slot][]string{}
	reference = map[llmreq.Slot]database.AuthorLLMFingerprintReference{}
	for _, p := range testParticipants {
		expected[p.Slot] = []string{p.Model}
		reference[p.Slot] = database.AuthorLLMFingerprintReference{PromptTokens: checkPrompt, OutputMode: "json_schema"}
	}
	return expected, reference
}

// evalReportDoc is the part of a stored eval report a resolve run reads, in
// the stored report's shape.
type evalReportDoc struct {
	Evaluation llmeval.Report `json:"evaluation"`
}

// passedStratum is a stratum the eval passed at items per request batch on
// arm, accepting tiers.
func passedStratum(name, arm string, batch int, tiers ...string) llmeval.StratumDecision {
	return llmeval.StratumDecision{Stratum: name, Passed: true, Arm: arm, ItemsPerRequest: batch, Tiers: tiers}
}

// completedFullEval records a completed full eval run of the configuration
// whose report decides strata as given; combos are the strata's combo
// results (arm A's pass matters to an unreadable excerpt on arm B).
func (e *llmEnv) completedFullEval(
	t *testing.T, registrable bool, decisions []llmeval.StratumDecision, combos map[string]map[string]llmeval.ComboMetrics,
) int64 {
	t.Helper()
	// Every participant answers by the FB2 fields unless the test says
	// otherwise.
	e.fake.setRespond(byFields(nil))
	expected, reference := allSlotsEvidence()
	run := e.run(&database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval, Mode: models.AuthorLLMEvalModeFull,
		ExpectedModels: expected, Reference: reference})
	doc := evalReportDoc{Evaluation: llmeval.Report{Strata: map[string]llmeval.StratumReport{},
		Decision: llmeval.Decision{Registrable: registrable, Strata: decisions}}}
	for stratum, c := range combos {
		doc.Evaluation.Strata[stratum] = llmeval.StratumReport{Combos: c}
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, database.StoreAuthorLLMEvalReport(context.Background(), e.db, run, e.version, raw))
	return run
}

func (e *llmEnv) resolveDriver() *services.AuthorLLMResolveDriver {
	return services.NewAuthorLLMResolveDriver(e.db, &e.identity, e.version, resolveSizes, time.Second)
}

// resolveUntilDone steps the resolve driver and drains the worker until the
// run completes.
func (e *llmEnv) resolveUntilDone(run int64) {
	e.t.Helper()
	d, w := e.resolveDriver(), e.worker()
	for range 200 {
		_, err := d.Step(context.Background())
		require.NoError(e.t, err)
		_, err = w.RunOnce(context.Background())
		require.NoError(e.t, err)
		if _, status := e.runStage(run); status == string(models.AuthorLLMRunCompleted) {
			return
		}
		time.Sleep(15 * time.Millisecond) // past the test backoff
	}
	stage, status := e.runStage(run)
	e.t.Fatalf("the resolve run never completed: stage %q, status %q", stage, status)
}

// resolvedCredit is one author credit's selection, by the credit's last
// name.
type resolvedCredit struct {
	Last     string  `pg:"last"`
	State    string  `pg:"state"`
	Basis    *string `pg:"basis"`
	Reason   *string `pg:"unresolved_reason"`
	Method   *string `pg:"method"`
	Class    *string `pg:"decision_class"`
	Display  *string `pg:"display_name"`
	Resolved *string `pg:"resolution"`
}

func (e *llmEnv) selections() map[string]resolvedCredit {
	e.t.Helper()
	var rows []resolvedCredit
	_, err := e.db.Query(&rows, `
		SELECT c.source_last_name AS last, s.state, s.basis, s.unresolved_reason, r.method, r.decision_class,
			r.display_name,
			(SELECT i.resolution FROM contributor_review_item i WHERE i.source_fingerprint = c.source_fingerprint
				ORDER BY i.id DESC LIMIT 1) AS resolution
		FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id
		LEFT JOIN contributor_normalization_result r ON r.id = s.result_id
		WHERE c.role = 'author'`)
	require.NoError(e.t, err)
	out := map[string]resolvedCredit{}
	for _, r := range rows {
		out[r.Last] = r
	}
	return out
}

// initialsCyrl is the fixture's initials stratum: seven review inputs.
var initialsCyrl = []string{"Петров", "Сидоров", "Кузнецов", "Смирнов", "Попов", "Соколов", "Лебедев"}

// judgesDiffer answers byFields, except that judge_b reads the middle
// initial of the inputs whose last name is in differ as a second given name.
func judgesDiffer(differ ...string) func(req *fakeRequest) fakeReply {
	answer := byFields(nil)
	return func(req *fakeRequest) fakeReply {
		if req.Model != "claude-opus-5" || isFingerprint(req) {
			return answer(req)
		}
		results := make([]any, 0, len(req.Items))
		for _, it := range req.Items {
			roles := make([]any, 0, len(it.Tokens))
			odd := false
			for _, tok := range it.Tokens {
				if tok.Field == "last" && contains(differ, tok.Text) {
					odd = true
				}
			}
			for _, tok := range it.Tokens {
				role := map[string]string{"first": "given", "middle": "additional", "last": "family",
					"nickname": "nickname"}[tok.Field]
				if odd && tok.Field == "middle" {
					role = "given"
				}
				roles = append(roles, map[string]any{"i": tok.I, "role": role})
			}
			results = append(results, map[string]any{"id": it.ID, "form": "person", "roles": roles, "case_fix": []any{}})
		}
		out, _ := json.Marshal(map[string]any{"results": results})
		return fakeReply{Content: string(out)}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestAuthorLLMResolveStepsNeedAPassedFullEval(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	admin := e.llmAdmin()
	_, err := admin.Start(ctx, "resolve", "pilot", e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite, "no eval at all")

	// A full eval whose report registers nothing is no ground either.
	e.completedFullEval(t, false, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm")}, nil)
	_, err = admin.Start(ctx, "resolve", "pilot", e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite, "a report that is not registrable")
	_, err = admin.Start(ctx, "resolve", "full", e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite, "full before the pilot")

	st, err := admin.Status(ctx)
	require.NoError(t, err)
	steps := stepsByKey(st)
	assert.True(t, steps["resolve_pilot"].Available, "the resolve pilot is built")
	assert.True(t, steps["resolve_full"].Available, "the full resolve is built")
	assert.False(t, steps["swap"].Available, "swaps come in phase 7")
	assert.False(t, steps["resolve_pilot"].Enabled)
}

func TestAuthorLLMResolveStartRegistersThePassedStrataAtOnce(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	evalRun := e.completedFullEval(t, true, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm", "restructure"),
		{Stratum: "single_field.cyrl"}, // failed
		passedStratum("homoglyph", "A", 1, "confirm"),
	}, nil)
	policyBefore := e.count(`SELECT greatest(coalesce(max(policy_version::int), 0), 1) FROM author_acceptance_class`)
	_, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite, "the full run before a completed pilot")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'`),
		"a refused start registers nothing")

	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)

	var rows []struct {
		PolicyVersion string `pg:"policy_version"`
		Class         string `pg:"decision_class"`
		Script        string `pg:"script"`
		Config        string `pg:"config_version"`
		Source        string `pg:"source"`
		ReportID      int64  `pg:"evidence_report_id"`
		Ref           string `pg:"evidence_ref"`
		By            int64  `pg:"registered_by_user_id"`
	}
	_, err = e.db.Query(&rows, `SELECT policy_version, decision_class, script, config_version, source,
		evidence_report_id, evidence_ref, registered_by_user_id FROM author_acceptance_class WHERE source = 'llm_eval'`)
	require.NoError(t, err)
	require.Len(t, rows, 1, "only the passed stratum with a script registers; homoglyph has none")
	reportID := e.count(`SELECT id FROM author_llm_eval_report WHERE run_id = ?`, evalRun)
	assert.Equal(t, "llm.initials", rows[0].Class)
	assert.Equal(t, "Cyrl", rows[0].Script)
	assert.Equal(t, e.version, rows[0].Config)
	assert.Equal(t, int64(reportID), rows[0].ReportID)
	assert.Equal(t, "author_llm_eval_report/"+itoa(reportID)+".json", rows[0].Ref)
	assert.Equal(t, e.adminUser(), rows[0].By)
	assert.Equal(t, itoa(policyBefore+1), rows[0].PolicyVersion)

	// The run carries the eval's evidence for all four participants and the
	// plan of every registered stratum.
	r, err := database.GetAuthorLLMRun(ctx, e.db, run.ID)
	require.NoError(t, err)
	var expected map[string][]string
	require.NoError(t, json.Unmarshal(r.ExpectedModels, &expected))
	assert.Len(t, expected, 4)
	var settings struct {
		ReportID int64 `json:"report_id"`
		Strata   map[string]struct {
			Arm   string   `json:"arm"`
			Batch int      `json:"items_per_request"`
			Tiers []string `json:"tiers"`
		} `json:"strata"`
		NotRegistrable []string `json:"not_registrable"`
	}
	require.NoError(t, json.Unmarshal(r.Settings, &settings))
	assert.Equal(t, int64(reportID), settings.ReportID)
	require.Contains(t, settings.Strata, "initials.cyrl")
	assert.Equal(t, []string{"confirm", "restructure"}, settings.Strata["initials.cyrl"].Tiers)
	assert.Equal(t, []string{"homoglyph"}, settings.NotRegistrable)

	// The full run after the pilot registers nothing twice.
	e.resolveUntilDone(run.ID)
	_, err = e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'`))
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestAuthorLLMResolveStartRegistersAllPassedStrata(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm"),
		passedStratum("single_field.cyrl", "B", 1, "restructure"),
	}, nil)
	_, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	var rows []struct {
		Class  string `pg:"decision_class"`
		Policy string `pg:"policy_version"`
	}
	_, err = e.db.Query(&rows, `SELECT decision_class, policy_version FROM author_acceptance_class
		WHERE source = 'llm_eval' ORDER BY decision_class`)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "llm.initials", rows[0].Class)
	assert.Equal(t, "llm.single_field", rows[1].Class)
	assert.Equal(t, rows[0].Policy, rows[1].Policy, "one start, one policy version")
}

func TestAuthorLLMResolveAppliesOnlyWhatBothJudgesConfirm(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	e.fake.setRespond(judgesDiffer("Петров"))
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)

	sel := e.selections()
	for _, last := range initialsCyrl {
		s := sel[last]
		if last == "Петров" {
			// The pair agreed; judge_b read the initial as a surname.
			assert.Equal(t, "unresolved", s.State, last)
			assert.Equal(t, "llm_judge_disagreement", deref(s.Reason), last)
			assert.Equal(t, "automatic", deref(s.Resolved), last)
			continue
		}
		assert.Equal(t, "selected", s.State, last)
		assert.Equal(t, "automatic", deref(s.Basis), last)
		assert.Equal(t, "llm", deref(s.Method), last)
		assert.Equal(t, "llm.initials", deref(s.Class), last)
		assert.Equal(t, "automatic", deref(s.Resolved), last, "the review item is closed by the service")
	}
	assert.Equal(t, "В. Г. Сидоров", deref(sel["Сидоров"].Display))
	// The unregistered stratum was not sent at all: its review stays open.
	for _, last := range []string{"Джон Брэндон", "Лу Синь"} {
		assert.Equal(t, "review", sel[last].State, last)
	}
	for _, req := range e.fake.served() {
		for _, it := range req.Items {
			for _, tok := range it.Tokens {
				assert.NotEqual(t, "Брэндон", tok.Text, "an input of an unregistered stratum was sent")
			}
		}
	}
	// Judges were asked only about the agreed answers, and each input once.
	assert.Equal(t, 7, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_a'`, run.ID))
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'
		AND decision_class = 'initials'`))
}

func TestAuthorLLMResolvePairDisagreementAndRefusalStayUnresolved(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	e.fake.setRespond(pairDiffers())
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	sel := e.selections()
	assert.Equal(t, "llm_refused", deref(sel["Попов"].Reason))
	assert.Equal(t, "llm_disagreement", deref(sel["Соколов"].Reason))
	assert.Equal(t, "selected", sel["Петров"].State)
	// Judges are not asked about what the pair did not agree on.
	assert.Equal(t, 5, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_b'`, run.ID))
}

// pairDiffers answers byFields, except that production_b refuses «Попов»
// and reads the middle initial of «Соколов» as a second given name.
func pairDiffers() func(req *fakeRequest) fakeReply {
	answer := byFields(nil)
	return func(req *fakeRequest) fakeReply {
		if isFingerprint(req) || req.Model != "deepseek-v4-pro" {
			return answer(req)
		}
		results := make([]any, 0, len(req.Items))
		for _, it := range req.Items {
			last := ""
			for _, tok := range it.Tokens {
				if tok.Field == "last" {
					last = tok.Text
				}
			}
			switch last {
			case "Попов":
				results = append(results, map[string]any{"id": it.ID, "form": "cannot_tell",
					"roles": []any{}, "case_fix": []any{}})
			case "Соколов":
				roles := make([]any, 0, len(it.Tokens))
				for _, tok := range it.Tokens {
					roles = append(roles, map[string]any{"i": tok.I, "role": map[string]string{
						"first": "given", "middle": "given", "last": "family"}[tok.Field]})
				}
				results = append(results, map[string]any{"id": it.ID, "form": "person", "roles": roles, "case_fix": []any{}})
			default:
				var one map[string]any
				_ = json.Unmarshal([]byte(answer(&fakeRequest{Items: []fakeItem{it}}).Content), &one)
				results = append(results, one["results"].([]any)[0])
			}
		}
		out, _ := json.Marshal(map[string]any{"results": results})
		return fakeReply{Content: string(out)}
	}
}

func TestAuthorLLMResolveNeverOverwritesAManualDecision(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	// The admin leaves «Сидоров» unresolved before the run reaches it.
	item := e.count(`SELECT i.id FROM contributor_review_item i JOIN book_contributor_credit c
		ON c.source_fingerprint = i.source_fingerprint WHERE c.source_last_name = 'Сидоров' AND i.status = 'open'`)
	review, err := services.NewAuthorMetadataReviewService(e.db, services.DefaultAuthorMetadataReviewConfig())
	require.NoError(t, err)
	_, err = review.LeaveUnresolved(ctx, int64(item), e.adminUser())
	require.NoError(t, err)

	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	sel := e.selections()
	assert.Equal(t, "unresolved", sel["Сидоров"].State)
	assert.Equal(t, "review_left_unresolved", deref(sel["Сидоров"].Reason), "the admin's decision stands")
	assert.Equal(t, "left_unresolved", deref(sel["Сидоров"].Resolved))
	assert.Equal(t, "selected", sel["Петров"].State)
}

func TestAuthorLLMResolvedFingerprintServesANewBookWithoutACall(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	served := len(e.fake.served())

	// A new book by «А. Б. Петров»: the same fingerprint.
	ingestBooks(t, e, [][3]string{{"А.", "Б.", "Петров"}}, 100)
	var rows []struct {
		State  string `pg:"state"`
		Method string `pg:"method"`
	}
	_, err = e.db.Query(&rows, `SELECT s.state, r.method FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id
		JOIN contributor_normalization_result r ON r.id = s.result_id
		WHERE c.source_last_name = 'Петров'`)
	require.NoError(t, err)
	require.Len(t, rows, 2, "both books' credits are decided")
	for _, r := range rows {
		assert.Equal(t, "selected", r.State)
		assert.Equal(t, "llm", r.Method)
	}
	assert.Len(t, e.fake.served(), served, "the new book cost no LLM call")
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'
		AND decision_class = 'initials'`), "no review reopened")
}

func TestAuthorLLMResolveRespectsTheRegisteredTiersAndExcerpt(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	// Only confirm registered: initials parsed as given/additional/family
	// are a restructure tier (the FB2 layout had them in other fields)?
	// The fixture's agreed answers follow the FB2 fields: tier confirm. A
	// stratum registered for restructure only accepts none of them.
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "restructure")}, nil)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	for _, last := range initialsCyrl {
		assert.Equal(t, "llm_not_registered", deref(e.selections()[last].Reason), last)
	}
}

func TestAuthorLLMResolveUnreadableExcerptNeedsArmA(t *testing.T) {
	// The eval chose arm B (with the excerpt) for confirm and restructure.
	// Without the excerpt the request is arm A's: it is accepted only when arm
	// A passed at that batch size for the answer's own tier.
	for name, c := range map[string]struct {
		armA     llmeval.ComboMetrics
		selected bool
	}{
		"arm A passed for the tier":     {llmeval.ComboMetrics{Passed: true, AllowedTiers: []string{"confirm"}}, true},
		"arm A passed without the tier": {llmeval.ComboMetrics{Passed: true, AllowedTiers: []string{"restructure"}}, false},
		"arm A failed":                  {llmeval.ComboMetrics{AllowedTiers: []string{"confirm", "restructure"}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLLMEnv(t)
			ingestEvalCatalogue(t, e)
			ctx := context.Background()
			e.completedFullEval(t, true,
				[]llmeval.StratumDecision{passedStratum("initials.cyrl", "B", 1, "confirm", "restructure")},
				map[string]map[string]llmeval.ComboMetrics{"initials.cyrl": {"A1": c.armA,
					"B1": {Passed: true, AllowedTiers: []string{"confirm", "restructure"}}}})
			// The archive is gone: no excerpt can be read.
			require.NoError(t, removeArchives(e))
			run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
			require.NoError(t, err)
			e.resolveUntilDone(run.ID)
			s := e.selections()["Петров"]
			if c.selected {
				assert.Equal(t, "selected", s.State)
				return
			}
			assert.Equal(t, "unresolved", s.State)
			assert.Equal(t, "llm_context_unavailable", deref(s.Reason))
			assert.Zero(t, e.count(`SELECT count(*) FROM contributor_normalization_result WHERE method = 'llm'`))
		})
	}
}

func TestAuthorLLMResolvePilotIsCappedAndFullTakesTheRest(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 2, "confirm", "restructure")}, nil)
	d := services.NewAuthorLLMResolveDriver(e.db, &e.identity, e.version, services.AuthorLLMResolveSizes{Pilot: 3}, time.Second)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	w := e.worker()
	for range 200 {
		_, err = d.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		if _, status := e.runStage(run.ID); status == "completed" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	assert.Equal(t, 3, e.count(`SELECT count(DISTINCT source_fingerprint) FROM author_llm_job WHERE run_id = ?`, run.ID))
	// The pair asks two items per request, as the eval chose.
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_call WHERE run_id = ? AND kind = 'jobs' AND items = 2`, run.ID))

	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(full.ID)
	assert.Equal(t, 4, e.count(`SELECT count(DISTINCT source_fingerprint) FROM author_llm_job WHERE run_id = ?`, full.ID),
		"the full run asks only what the pilot did not")
	for _, last := range initialsCyrl {
		assert.Equal(t, "selected", e.selections()[last].State, last)
	}
}

func TestAuthorLLMResolveReportCountsAppliedAndNot(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	e.fake.setRespond(judgesDiffer("Петров", "Попов"))
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)

	st, err := e.llmAdmin().Status(ctx)
	require.NoError(t, err)
	require.NotNil(t, st.Run.Resolve, "the card's line of a resolve report")
	assert.Equal(t, 5, st.Run.Resolve.Applied)
	assert.Equal(t, 7, st.Run.Resolve.Total)
	raw, sha, err := e.llmAdmin().EvalReport(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, sha, st.Run.Resolve.SHA256)
	var doc struct {
		RunID  int64 `json:"run_id"`
		Strata map[string]struct {
			Sent       int            `json:"sent"`
			Applied    int            `json:"applied"`
			NotApplied map[string]int `json:"not_applied"`
		} `json:"strata"`
		Tokens map[string]int64 `json:"tokens"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, run.ID, doc.RunID)
	s := doc.Strata["initials.cyrl"]
	assert.Equal(t, 7, s.Sent)
	assert.Equal(t, 5, s.Applied)
	assert.Equal(t, map[string]int{"llm_judge_disagreement": 2}, s.NotApplied)
	for _, slot := range []string{"production_a", "production_b", "judge_a", "judge_b"} {
		assert.Positive(t, doc.Tokens[slot], slot)
	}
	assert.NotContains(t, string(raw), "Петров", "no names in the report")
}

func TestAuthorLLMResolveOneBadItemDoesNotBlockTheRest(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	services.PanicOnApplyForTest(t, func(last string) bool { return last == "Смирнов" })
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	sel := e.selections()
	assert.Equal(t, "review", sel["Смирнов"].State, "the panicking item is left as it was")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'apply_failed'`, run.ID))
	for _, last := range initialsCyrl {
		if last != "Смирнов" {
			assert.Equal(t, "selected", sel[last].State, last)
		}
	}
}

// An input whose application failed was not decided by the LLM layer: a new
// book of it goes where the local outcome sends it — back to review.
func TestAuthorLLMResolveApplyFailedLeavesTheNewBookToReview(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	services.PanicOnApplyForTest(t, func(last string) bool { return last == "Смирнов" })
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	ingestBooks(t, e, [][3]string{{"Ж.", "З.", "Смирнов"}}, 200)
	var states []string
	_, err = e.db.Query(&states, `SELECT s.state FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id WHERE c.source_last_name = 'Смирнов'`)
	require.NoError(t, err)
	assert.Equal(t, []string{"review", "review"}, states)
}

func TestAuthorLLMResolveAcceptancePassLeavesLLMDecisionsAlone(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	e.fake.setRespond(judgesDiffer("Петров"))
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	before := e.selections()
	_, err = services.ApplyAcceptancePolicy(ctx, e.db, localNormalizerVersion(t), 100)
	require.NoError(t, err)
	drainLocal(t, e.db)
	assert.Equal(t, before, e.selections())
}

// ingestBooks scans more fixture books into the catalog, numbered from
// first, and runs the local normalizer.
func ingestBooks(t *testing.T, e *llmEnv, authors [][3]string, first int) {
	t.Helper()
	path := filepath.Join(e.archives, "more-"+strconv.Itoa(first)+".zip")
	writeEvalArchive(t, path, authors, first)
	scanArchive(t, e, path)
	drainLocal(t, e.db)
}

// removeArchives deletes every archive of the catalog: no excerpt can be
// read any more.
func removeArchives(e *llmEnv) error {
	entries, err := os.ReadDir(e.archives)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(e.archives, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func localNormalizerVersion(t *testing.T) string {
	t.Helper()
	return services.DefaultAuthorMetadataLocalWorkerConfig().NormalizerVersion
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
