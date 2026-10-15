package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 7 of design K: swaps among the automatically selected names. The
// catalog's role statistics find the candidates without an LLM; a swap run
// asks the production pair and both judges, and an answer is applied — a
// new llm.swap result selected over the local one, through the resolver and
// its audit — only when the pair agrees, both judges confirm it and the
// statistics agree (decision 8). An admin decision is never touched.

// The fixture's statistics: each of these words stands in its natural field
// of ten credits, so one credit the other way round leaves it dominant there
// (10 of 11, over the 90% share, over the support of 5).
var (
	swapGivens   = []string{"Сергей", "Пётр", "Олег"}
	swapFamilies = []string{"Иванов", "Смирнов", "Козлов"}
	// The fillers: rare words, never dominant anywhere.
	swapFillerGivens   = []string{"Аркадий", "Вадим", "Глеб", "Денис", "Егор", "Илья", "Кирилл", "Лев", "Марк", "Назар"}
	swapFillerFamilies = []string{"Абрамов", "Белов", "Гусев", "Дьяков", "Ершов", "Жуков", "Зотов", "Исаев", "Карпов", "Лосев"}
)

// The swapped inputs, as their FB2 has them: the family name in the first
// field and the given name in the last.
var (
	// swappedIvanov is the swap to apply.
	swappedIvanov = [3]string{"Иванов", "", "Сергей"}
	// swappedSmirnov is swapped too, but an admin corrected it.
	swappedSmirnov = [3]string{"Смирнов", "", "Пётр"}
	// swappedKozlov is a candidate whose answer confirms the FB2 fields.
	swappedKozlov = [3]string{"Козлов", "", "Олег"}
	// luXun is a Chinese name in its own order, family first: rare words,
	// the statistics are silent, and it is never swapped.
	luXun = [3]string{"Лу", "", "Синь"}
	// abramovich is a morphology candidate (a -ович word in the first
	// field) the models read the other way round, but the statistics know
	// neither word: nothing is swapped.
	abramovich = [3]string{"Абрамович", "", "Роман"}
)

// swapCatalog scans the statistics' books and the four inputs above, and
// runs the local normalizer: every one of them is a structured_person the
// shipped policy selects.
func swapCatalog(t *testing.T, e *llmEnv) {
	t.Helper()
	var authors [][3]string
	for i, g := range swapGivens {
		for _, f := range swapFillerFamilies {
			authors = append(authors, [3]string{g, "", f})
		}
		for _, fg := range swapFillerGivens {
			authors = append(authors, [3]string{fg, "", swapFamilies[i]})
		}
	}
	authors = append(authors, swappedIvanov, swappedSmirnov, swappedKozlov, luXun, abramovich)
	ingestBooks(t, e, authors, 500)
}

// completedSwapEval records a completed eval run of mode swap whose report
// decides strata as given.
func (e *llmEnv) completedSwapEval(t *testing.T, decisions []llmeval.StratumDecision) {
	t.Helper()
	e.fake.setRespond(byFields(nil))
	expected, reference := allSlotsEvidence()
	run := e.run(&database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval, Mode: models.AuthorLLMEvalModeSwap,
		ExpectedModels: expected, Reference: reference})
	doc := evalReportDoc{Evaluation: llmeval.Report{Strata: map[string]llmeval.StratumReport{},
		Decision: llmeval.Decision{Registrable: true, Strata: decisions}}}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, database.StoreAuthorLLMEvalReport(context.Background(), e.db, run, e.version, raw))
}

// swapAnswers answers like the models of the design's probes: a family name
// in the first field is read as the family name (Иванов | Сергей, Смирнов |
// Пётр), and so is the first word of a Chinese name (Лу | Синь). Every other
// item is answered by its FB2 fields.
func swapAnswers() func(req *fakeRequest) fakeReply {
	answer := byFields(nil)
	familyFirst := []string{"Иванов", "Смирнов", "Лу", "Абрамович"}
	return func(req *fakeRequest) fakeReply {
		if isFingerprint(req) {
			return answer(req)
		}
		results := make([]any, 0, len(req.Items))
		for _, it := range req.Items {
			swapped := false
			for _, tok := range it.Tokens {
				if tok.Field == "first" && contains(familyFirst, tok.Text) {
					swapped = true
				}
			}
			roles := make([]any, 0, len(it.Tokens))
			for _, tok := range it.Tokens {
				role := map[string]string{"first": "given", "middle": "additional", "last": "family",
					"nickname": "nickname"}[tok.Field]
				if swapped {
					role = map[string]string{"first": "family", "last": "given"}[tok.Field]
				}
				roles = append(roles, map[string]any{"i": tok.I, "role": role})
			}
			results = append(results, map[string]any{"id": it.ID, "form": "person", "roles": roles, "case_fix": []any{}})
		}
		out, _ := json.Marshal(map[string]any{"results": results})
		return fakeReply{Content: string(out)}
	}
}

// swapSelection is one credit's selection with its result, by the credit's
// last-name field.
type swapSelection struct {
	CreditID int64   `pg:"credit_id"`
	State    string  `pg:"state"`
	Basis    *string `pg:"basis"`
	ResultID int64   `pg:"result_id"`
	Method   string  `pg:"method"`
	Class    *string `pg:"decision_class"`
	Given    *string `pg:"given_name"`
	Family   *string `pg:"family_name"`
	Display  *string `pg:"display_name"`
	Sort     *string `pg:"sort_name"`
}

func (e *llmEnv) swapSelections(t *testing.T, last string) []swapSelection {
	t.Helper()
	var rows []swapSelection
	_, err := e.db.Query(&rows, `
		SELECT c.id AS credit_id, s.state, s.basis, s.result_id, r.method, r.decision_class, r.given_name,
			r.family_name, r.display_name, r.sort_name
		FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id
		JOIN contributor_normalization_result r ON r.id = s.result_id
		WHERE c.role = 'author' AND c.source_last_name = ? ORDER BY c.id`, last)
	require.NoError(t, err)
	return rows
}

// swapEvidence records the swap eval that registers swap.cyrl.
func (e *llmEnv) swapEvidence(t *testing.T) {
	t.Helper()
	e.completedSwapEval(t, []llmeval.StratumDecision{passedStratum("swap.cyrl", "A", 1, "confirm", "restructure")})
}

func TestAuthorLLMSwapReselectsWithAudit(t *testing.T) {
	e := newLLMEnv(t)
	swapCatalog(t, e)
	ctx := context.Background()
	for _, last := range []string{"Сергей", "Пётр", "Олег", "Синь", "Роман"} {
		sel := e.swapSelections(t, last)
		require.Len(t, sel, 1, last)
		require.Equal(t, "selected", sel[0].State, last)
		require.Equal(t, "structured_person", deref(sel[0].Class), last)
	}
	before := e.swapSelections(t, "Сергей")[0]
	require.Equal(t, "Иванов", deref(before.Given), "the fixture is not swapped in the local result")

	// The admin corrects Смирнов | Пётр before any swap run.
	review, err := services.NewAuthorMetadataReviewService(e.db, services.DefaultAuthorMetadataReviewConfig())
	require.NoError(t, err)
	_, err = review.ApplyOverride(ctx, database.OverrideScope{Fingerprint: e.fingerprintOf("Пётр")}, e.adminUser(),
		&services.AdminCorrection{GivenName: "Пётр", FamilyName: "Смирнов", DisplayName: "Смирнов Пётр",
			SortName: "Смирнов, Пётр", SearchKey: "смирнов петр", Script: "Cyrl", Kind: models.NormalizationKind("person")})
	require.NoError(t, err)
	overridden := e.swapSelections(t, "Пётр")[0]

	e.swapEvidence(t)
	e.fake.setRespond(swapAnswers())
	run, err := e.llmAdmin().Start(ctx, "swap", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)

	// Иванов | Сергей: re-selected on a new llm.swap result.
	after := e.swapSelections(t, "Сергей")
	require.Len(t, after, 1)
	got := after[0]
	assert.Equal(t, "selected", got.State)
	assert.Equal(t, "automatic", deref(got.Basis))
	assert.Equal(t, "llm", got.Method)
	assert.Equal(t, "llm.swap", deref(got.Class))
	assert.Equal(t, "Сергей", deref(got.Given))
	assert.Equal(t, "Иванов", deref(got.Family))
	assert.Equal(t, "Иванов, Сергей", deref(got.Sort))
	assert.Equal(t, deref(before.Display), deref(got.Display), "the display stays as in the file")
	// The audit records the change from the local result to the new one.
	var audit []struct {
		Previous *int64 `pg:"previous_result_id"`
		Result   *int64 `pg:"result_id"`
		State    string `pg:"state"`
	}
	_, err = e.db.Query(&audit, `SELECT previous_result_id, result_id, state FROM book_contributor_credit_selection_audit
		WHERE credit_id = ? ORDER BY id`, got.CreditID)
	require.NoError(t, err)
	require.NotEmpty(t, audit)
	last := audit[len(audit)-1]
	assert.Equal(t, before.ResultID, deref(last.Previous))
	assert.Equal(t, got.ResultID, deref(last.Result))
	assert.Equal(t, "selected", last.State)
	verdictOf := func(lastName string) int {
		return e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
			AND purpose = 'swap_check'`, run.ID, e.fingerprintOf(lastName))
	}
	require.Equal(t, 1, verdictOf("Сергей"))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND result_id = ? AND stratum = 'swap.cyrl'`, run.ID, e.fingerprintOf("Сергей"), got.ResultID))

	// The admin's correction is never touched, and the input never asked.
	assert.Equal(t, []swapSelection{overridden}, e.swapSelections(t, "Пётр"))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE source_fingerprint = ?`, e.fingerprintOf("Пётр")))

	// Козлов | Олег: the pair confirms the FB2 fields — nothing changes.
	require.Equal(t, 1, verdictOf("Олег"))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND outcome_reason = 'swap_fields_confirmed' AND result_id IS NULL`, run.ID, e.fingerprintOf("Олег")))
	kozlov := e.swapSelections(t, "Олег")
	assert.Equal(t, "selected", kozlov[0].State)
	assert.Equal(t, "structured_person", deref(kozlov[0].Class))

	// Абрамович | Роман: the pair and both judges swap it, the statistics
	// do not say so — nothing changes.
	require.Equal(t, 1, verdictOf("Роман"))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND source_fingerprint = ?
		AND outcome_reason = 'swap_stats_disagree' AND result_id IS NULL AND judgement = 'confirmed'`,
		run.ID, e.fingerprintOf("Роман")))
	roman := e.swapSelections(t, "Роман")
	assert.Equal(t, "selected", roman[0].State)
	assert.Equal(t, "structured_person", deref(roman[0].Class))

	// Лу | Синь: the statistics are silent — no candidate, never asked,
	// though the models would read it family first.
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE source_fingerprint = ?`, e.fingerprintOf("Синь")))
	lu := e.swapSelections(t, "Синь")
	assert.Equal(t, "selected", lu[0].State)
	assert.Equal(t, "structured_person", deref(lu[0].Class))

	// The run kept its role statistics.
	var snapshot struct {
		Credits int `pg:"credits"`
		Tokens  int `pg:"tokens"`
	}
	_, err = e.db.QueryOne(&snapshot, `SELECT credits, tokens FROM author_token_role_snapshot WHERE run_id = ?`, run.ID)
	require.NoError(t, err)
	assert.Positive(t, snapshot.Credits)
	assert.Equal(t, 10, e.count(`SELECT first_count FROM author_token_role_stats t
		JOIN author_token_role_snapshot s ON s.id = t.snapshot_id WHERE s.run_id = ? AND t.token = 'сергей'`, run.ID))

	// A new book of the swapped input reuses the result, without a call.
	calls := e.count(`SELECT count(*) FROM author_llm_call`)
	ingestBooks(t, e, [][3]string{swappedIvanov}, 900)
	again := e.swapSelections(t, "Сергей")
	require.Len(t, again, 2)
	assert.Equal(t, got.ResultID, again[1].ResultID, "the new credit did not get the swap")
	assert.Equal(t, calls, e.count(`SELECT count(*) FROM author_llm_call`))
	// A new book of a candidate that was not swapped gets the local result,
	// as before: the swap verdict decided nothing.
	ingestBooks(t, e, [][3]string{swappedKozlov}, 901)
	kozlovAgain := e.swapSelections(t, "Олег")
	require.Len(t, kozlovAgain, 2)
	assert.Equal(t, "selected", kozlovAgain[1].State)
	assert.Equal(t, kozlov[0].ResultID, kozlovAgain[1].ResultID)
}

// A swap run needs the measurement of the swap strata: without an eval of
// mode swap whose report registers one, it does not start. The full run
// needs a completed pilot.
func TestAuthorLLMSwapNeedsItsOwnEvidence(t *testing.T) {
	e := newLLMEnv(t)
	swapCatalog(t, e)
	ctx := context.Background()
	// A full eval that registers initials is no ground for swaps.
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm")}, nil)
	_, err := e.llmAdmin().Start(ctx, "swap", "pilot", e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite)
	_, err = e.llmAdmin().Start(ctx, "swap", "full", e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite)
	// A swap eval whose swap.latn stratum passed but cannot reach the gate is
	// measured, and swap.cyrl failed: nothing to register.
	e.completedSwapEval(t, []llmeval.StratumDecision{{Stratum: "swap.cyrl", Passed: false}})
	_, err = e.llmAdmin().Start(ctx, "swap", "pilot", e.adminUser())
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_run WHERE kind = 'swap'`))

	// The resolve start never registers a swap stratum, even from a full
	// eval that passed one.
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm"),
		passedStratum("swap.cyrl", "A", 1, "confirm", "restructure")}, nil)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_acceptance_class WHERE decision_class = 'llm.swap'`))
	e.resolveUntilDone(run.ID)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE purpose = 'swap_check'`))
}

// An eval of mode swap freezes and measures the swap strata alone — the
// candidates the statistics find, never an initials item — on statistics of
// its own; a full eval after it does not measure them again.
func TestAuthorLLMEvalSwapMeasuresOnlyTheSwapStrata(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	swapCatalog(t, e)
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	e.fake.setRespond(byFields(distinctLabs))
	for _, mode := range []string{models.AuthorLLMEvalModePilot, models.AuthorLLMEvalModeFull} {
		run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, mode, &settings, nil)
		require.NoError(t, err)
		e.evalUntilDone(run)
	}
	require.Zero(t, e.count(`SELECT count(*) FROM author_llm_eval_set WHERE set_version LIKE 'v1.s.swap.%'`),
		"a full eval froze a swap stratum")

	swap, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeSwap, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(swap)
	var frozen []string
	_, err = e.db.Query(&frozen, `SELECT encode(source_fingerprint, 'hex') FROM author_llm_eval_item
		WHERE stratum = 'swap.cyrl' ORDER BY 1`)
	require.NoError(t, err)
	var want []string
	for _, last := range []string{"Сергей", "Пётр", "Олег", "Роман"} {
		want = append(want, encodeHex(e.fingerprintOf(last)))
	}
	assert.ElementsMatch(t, want, frozen, "the swap stratum is not the statistics' candidates")
	assert.Positive(t, e.count(`SELECT count(*) FROM author_token_role_snapshot WHERE run_id = ?`, swap))
	// Its judges and candidates were asked about swap items only.
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job j JOIN author_llm_eval_item i ON i.id = j.eval_item_id
		WHERE j.run_id = ? AND j.purpose IN ('eval_judge', 'eval_candidate', 'eval_repeat')
			AND i.stratum NOT LIKE 'swap.%'`, swap), "the swap eval measured another stratum")
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND purpose = 'eval_judge'`, swap))
	r := downloadReport(t, e, swap)
	assert.Equal(t, models.AuthorLLMEvalModeSwap, r.Mode)
	for name := range r.Evaluation.Strata {
		assert.Equal(t, "swap.cyrl", name)
	}

	// A full eval afterwards measures what it did before, not the swaps.
	full, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(full)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job j JOIN author_llm_eval_item i ON i.id = j.eval_item_id
		WHERE j.run_id = ? AND i.stratum LIKE 'swap.%'`, full), "a full eval measured the swap strata again")
}

func encodeHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}

// The judges answer after the pair's checks have passed (judge_a is busy at
// first): the swap still waits for their judgement, and is applied once both
// confirm it.
func TestAuthorLLMSwapWaitsForTheJudges(t *testing.T) {
	e := newLLMEnv(t)
	swapCatalog(t, e)
	ctx := context.Background()
	e.swapEvidence(t)
	answer := swapAnswers()
	var busy atomic.Int32
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if req.Model == judgeAModel && !isFingerprint(req) && busy.Add(1) <= 6 {
			return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"busy"}}`}
		}
		return answer(req)
	})
	run, err := e.llmAdmin().Start(ctx, "swap", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	require.Greater(t, busy.Load(), int32(6), "judge_a was never busy")
	got := e.swapSelections(t, "Сергей")[0]
	assert.Equal(t, "llm.swap", deref(got.Class), "the swap was settled before the judges confirmed it")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'llm_judge_failed'`,
		run.ID))
}
