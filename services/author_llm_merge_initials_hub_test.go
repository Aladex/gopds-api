package services_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The initials_hub kind (E4g) through the whole flow: a registered
// initials kind never makes a hub candidate due; merge6 is a census of
// every stored hub candidate, its natural Wikidata namesakes asked in the
// suite instead, gated on the audit of its applied set; a passing report
// registers initials_hub, its apply inserts the decision and the build
// joins the hub to its full name.

// hubFixture adds to the stage fixture «Н. Носов» on twelve books beside
// «Николай Николаевич Носов», and «Томас М. Рид» on eleven beside «Томас
// Майн Рид», whom Wikidata tells apart.
var hubFixture = []mergePerson{
	{"Носов", "Н.", "", "Cyrl", "child_prose", 12, 0},
	{"Носов", "Николай", "Николаевич", "Cyrl", "child_prose", 2, 0},
	{"Рид", "Томас", "М.", "Cyrl", "sf_fantasy", 11, 0},
	{"Рид", "Томас", "Майн", "Cyrl", "adv_western", 1, 0},
	// A namesake pair for the suite, one side of more than ten books.
	{"Петров", "Олег", "Иванович", "Cyrl", "sci_history", 12, 0},
	{"Петров", "Олег", "Сергеевич", "Cyrl", "home_cooking", 1, 0},
}

// hubEvidence is the annotation of the hub's eleventh book: another
// person's full name.
const hubEvidence = "Николай Петрович Носов — историк флота"

// The fills of hubFixture (seedMergePersons, 0x90 + i).
const (
	fillNosovHub  = 0x90
	fillNosovFull = 0x91
	fillReidHub   = 0x92
	fillReidFull  = 0x93
)

func TestTheInitialsHubCensusRegistersAndAppliesApartFromInitials(t *testing.T) {
	e := newLLMEnv(t)
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	c.seedStageFixture()
	c.seedMergePersons(hubFixture, fillNosovHub)
	for _, n := range []struct{ name, qid string }{{"Томас М. Рид", "Q921"}, {"Томас Майн Рид", "Q922"}} {
		c.exec(`INSERT INTO wikidata_name (extract_id, key, qid, kind, lang, label)
			SELECT id, public.search_normalize(?), ?, 'label', 'ru', ? FROM wikidata_extract`, n.name, n.qid, n.name)
	}
	// Long annotations make the hub's card three times an ordinary one;
	// evidence of another person is on its eleventh book only.
	c.exec(`UPDATE opds_catalog_book SET annotation = repeat('ж', 300) WHERE title LIKE 'Повесть 0-%'`)
	c.exec(`UPDATE opds_catalog_book SET annotation = ? WHERE title = 'Повесть 0-10'`, hubEvidence)
	rep := c.build()
	m := e.mergeConfig()
	e.fake.setRespond(mergeStub)
	ctx := context.Background()
	stored := storedDigests(t, e, authorentity.CandidateInitialsHub)
	require.Len(t, stored, 2, "Носов, Рид: %v", rep.Candidates.ByKind[authorentity.CandidateInitialsHub])
	require.Len(t, stored, kindCount(&rep, authorentity.CandidateInitialsHub))

	// A live initials registration leaves the hub candidates undue.
	c.exec(`INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		VALUES ('eval', 'merge2', 'completed', ?, now())`, m.version)
	c.exec(`INSERT INTO author_llm_eval_report (run_id, config_version, report, report_sha256)
		SELECT max(id), ?0, '{}', sha256('initials') FROM author_llm_run WHERE config_version = ?0`, m.version)
	c.exec(`INSERT INTO author_entity_merge_registration (config_version, kind, evidence_report_id,
		evidence_report_sha256, gate) SELECT config_version, 'initials', id, report_sha256, 'judge_consensus'
		FROM author_llm_eval_report WHERE report_sha256 = sha256('initials')`)
	due, err := database.MergeCandidatesDue(ctx, e.db, m.version)
	require.NoError(t, err)
	require.NotEmpty(t, due, "the initials candidates are due")
	for _, d := range due {
		assert.Equal(t, authorentity.CandidateInitials, d.Kind)
	}

	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge6, nil)
	require.NoError(t, err)
	// A suite of every namesake pair there is, Петров's among them.
	sizes := stageEvalSizes
	sizes.SuitePerShape = 40
	stepMergeEval(t, e, m, run, sizes, false)
	sets := frozenOf(t, e, models.AuthorLLMEvalModeMerge6)

	// Рид's two items make his pair a natural namesake, asked in the suite;
	// Носов is the census; together they are every candidate.
	var natural []frozenItem
	for _, it := range sets["adversarial"] {
		assert.True(t, strings.HasPrefix(it.Stratum, "initials_hub."), it.Stratum)
		if it.ExtractorVersion == authorentity.MergeCandidateVersion {
			natural = append(natural, it)
		}
	}
	require.Len(t, natural, 1)
	assert.Contains(t, natural[0].Input, "Томас М. Рид")
	assert.LessOrEqual(t, len(sets["adversarial"]), sizes.SuitePerShape, "the natural namesakes count in the suite")

	// The suite's hub-shaped item lists the initial's side whole, every
	// book loaded: «О. Петров» on Oleg Ivanovich's twelve books.
	var petrov *authorentity.MergeCard
	for _, it := range sets["adversarial"] {
		var in authorentity.MergeInput
		require.NoError(t, json.Unmarshal([]byte(it.Input), &in))
		for _, card := range []authorentity.MergeCard{in.A, in.B} {
			if slices.Contains(card.Names, "О. Петров") {
				petrov = &card
			}
		}
	}
	require.NotNil(t, petrov, "a suite item of the Петров pair")
	require.Len(t, petrov.Books, 12)
	for _, b := range petrov.Books {
		assert.Equal(t, []string{"sci_history"}, b.Genres, b.Title)
	}

	// The estimate follows the frozen inputs' size.
	var est int64
	_, err = e.db.QueryOne(pg.Scan(&est), `SELECT estimate_tokens FROM author_llm_run WHERE id = ?`, run)
	require.NoError(t, err)
	inputBytes := func(items []frozenItem) int {
		n := 0
		for _, it := range items {
			n += len(it.Input)
		}
		return n
	}
	require.Greater(t, inputBytes(sets["holdout"]), 2*4000*len(sets["holdout"]), "a hub card over twice the mean")
	assert.Equal(t, services.MergeEstimateTokensOf(sizes, len(sets["holdout"]), len(sets["adversarial"]),
		len(sets["anchor"]), inputBytes(sets["holdout"]), inputBytes(sets["adversarial"])), est)
	require.Len(t, sets["holdout"], 1)
	assert.Equal(t, "initials_hub.cyrl", sets["holdout"][0].Stratum)
	census := append(fingerprints(sets["holdout"]), fingerprints(natural)...)
	slices.Sort(census)
	assert.Equal(t, stored, census)
	assert.Empty(t, sets["supplement"])

	// The hub's card lists all twelve of its books, the eleventh's
	// evidence with it.
	var in authorentity.MergeInput
	require.NoError(t, json.Unmarshal([]byte(sets["holdout"][0].Input), &in))
	hub := in.A
	if !slices.Contains(hub.Names, "Н. Носов") {
		hub = in.B
	}
	require.Contains(t, hub.Names, "Н. Носов")
	require.Len(t, hub.Books, 12)
	assert.Equal(t, "Повесть 0-10", hub.Books[10].Title)
	assert.Equal(t, hubEvidence, hub.Books[10].Annotation)

	raw, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run)
	require.NoError(t, err)
	var r stageReport
	require.NoError(t, json.Unmarshal(raw, &r))
	assert.Equal(t, "census_audit", r.Options.GateRule)
	require.Len(t, r.Evaluation.Decision.Strata, 1)
	assert.Equal(t, authorentity.CandidateInitialsHub, r.Evaluation.Decision.Strata[0].Stratum)
	assert.Equal(t, "census_audit", r.Evaluation.Decision.Strata[0].Rule)

	// The audit gates it: every applied item is in the sample; a passing
	// report registers initials_hub, and initials is not registered from it.
	admin := services.NewAuthorEntityAdmin(e.db)
	cloneHubItems(t, e.db, run, 200)
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateInitialsHub, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed, "the audit sample is pending")
	audit, err := services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	assert.Len(t, audit.Items, 201, "every applied item is audited")
	// The auditor sees what the models saw: every book of the hub, the
	// eleventh with its annotation.
	for _, it := range audit.Items {
		side := it.A
		if !slices.Contains(side.Names, "Н. Носов") {
			side = it.B
		}
		require.Len(t, side.Books, 12, "audit item %d", it.EvalItemID)
		assert.Equal(t, hubEvidence, side.Books[10].Annotation)
	}
	for _, it := range audit.Items {
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, it.EvalItemID, database.AdjudicationSame, "",
			"coordinator"))
	}
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateInitials, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed, "a merge6 report passes no initials")
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateInitialsHub, 0)
	require.NoError(t, err)
	due, err = database.MergeCandidatesDue(ctx, e.db, m.version)
	require.NoError(t, err)
	for _, d := range due {
		assert.NotEqual(t, authorentity.CandidateInitialsHub, d.Kind, "the census asked every hub candidate")
	}

	// The apply inserts the census decision and the build joins «Н. Носов»
	// to «Николай Николаевич Носов»; Рид's namesakes stay apart.
	require.NotEqual(t, c.entityOf(fillNosovHub), c.entityOf(fillNosovFull))
	applied, err := admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_merge_decision WHERE kind = 'initials_hub'
		AND revoked_at IS NULL AND digest = decode('`+hex.EncodeToString(sets["holdout"][0].SourceFingerprint)+`', 'hex')`), "%+v", applied)
	c.build()
	assert.Equal(t, c.entityOf(fillNosovHub), c.entityOf(fillNosovFull), "the build joins the hub to its full name")
	assert.NotEqual(t, c.entityOf(fillReidHub), c.entityOf(fillReidFull), "the namesakes stay apart")
}

// cloneHubItems stores n copies of the hub census item of run whose every
// answer is same and that every participant was asked again.
func cloneHubItems(t *testing.T, db *pg.DB, run int64, n int) {
	t.Helper()
	var template int64
	_, err := db.QueryOne(pg.Scan(&template), `
		SELECT i.id FROM author_llm_eval_item i
		JOIN author_llm_job j ON j.eval_item_id = i.id AND j.run_id = ?0
		LEFT JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.item_set = 'holdout' AND i.stratum LIKE 'initials_hub.%'
		GROUP BY i.id
		HAVING count(*) FILTER (WHERE j.purpose = 'eval_repeat') = 4
			AND bool_and(j.status = 'answered' AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8')))
		ORDER BY i.id LIMIT 1`, run)
	require.NoError(t, err, "a hub census item answered same and repeated by all")
	cloneItem(t, db, run, template, n)
}

// A hub census smaller than the repeat sample registers (E4g, run 40):
// 122 census items, every one repeated by every participant, the audit
// sample labeled — the repeat evidence needs every census item, not 200 of
// them, and a recompute of the completed run picks the rule up with no run
// asked again.
func TestASmallHubCensusRegistersOnEveryItemRepeated(t *testing.T) {
	e := newLLMEnv(t)
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	c.seedStageFixture()
	c.seedMergePersons(hubFixture, fillNosovHub)
	c.build()
	m := e.mergeConfig()
	e.fake.setRespond(mergeStub)
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge6, nil)
	require.NoError(t, err)
	sizes := stageEvalSizes
	sizes.SuitePerShape = 40
	stepMergeEval(t, e, m, run, sizes, false)

	// Носов and Рид (no Wikidata tells this Рид apart) are the census; 120
	// clones make it run 40's 122.
	cloneHubItems(t, e.db, run, 120)
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	audit, err := services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	for _, it := range audit.Items {
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, it.EvalItemID, database.AdjudicationSame, "",
			"coordinator"))
	}
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)

	raw, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run)
	require.NoError(t, err)
	var r struct {
		Evaluation struct {
			Repeat []struct {
				Participant string
				Total       int
			} `json:"repeat"`
			Decision struct {
				Registrable bool     `json:"registrable"`
				Reasons     []string `json:"reasons"`
			} `json:"decision"`
		} `json:"evaluation"`
	}
	require.NoError(t, json.Unmarshal(raw, &r))
	require.Len(t, r.Evaluation.Repeat, 4)
	for _, p := range r.Evaluation.Repeat {
		assert.Equal(t, 122, p.Total, "%s repeated every census item", p.Participant)
	}
	assert.Empty(t, r.Evaluation.Decision.Reasons)
	assert.True(t, r.Evaluation.Decision.Registrable)
	_, err = services.NewAuthorEntityAdmin(e.db).RegisterMergeKind(ctx, run, authorentity.CandidateInitialsHub, 0)
	require.NoError(t, err)
}
