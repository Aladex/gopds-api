package services_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mixed-script kinds (E4h) through the whole flow: a Wikidata
// cross-script merge makes «Михаил Афанасьевич Булгаков» and «Mihail
// Afanasyevich Bulgakov» one side of two scripts; the Latin «Mihail
// Afanasievich Bulgakov» pairs with it as a translit_mixed, which a registered translit
// never makes due; merge7 is the census of the mixed kinds, its card the
// side's every written form and its Wikidata item; a passing report
// registers translit_mixed, the apply inserts the decision and the build
// joins the Latin side to the mixed one.

// mixedFixture: the two forms one item names (fills 0xa0, 0xa1) and
// another Latin spelling (0xa2) on books of their own.
var mixedFixture = []mergePerson{
	{"Булгаков", "Михаил", "Афанасьевич", "Cyrl", "prose_classic", 3, 0},
	{"Bulgakov", "Mihail", "Afanasyevich", "Latn", "prose_classic", 1, 0},
	{"Bulgakov", "Mihail", "Afanasievich", "Latn", "prose_classic", 2, 0},
}

const (
	fillBulgakovRu    = 0xa0
	fillBulgakovLatin = 0xa2
)

func TestTheMixedScriptCensusRegistersAndAppliesApartFromThePlainKinds(t *testing.T) {
	e := newLLMEnv(t)
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	c.seedStageFixture()
	// The Cyrillic form's result carries a nickname, the mixed card's
	// variant (the credits reuse the result of their fingerprint).
	fp := bytes.Repeat([]byte{fillBulgakovRu}, 32)
	display := "Михаил Афанасьевич Булгаков"
	c.exec(`INSERT INTO contributor_normalization_result
		(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
		 method, kind, status, decision_class, family_name, given_name, additional_names, nickname,
		 display_name, sort_name, search_key, script)
		VALUES (?, ?, 'extractor-v1', 'normalizer-v1', 'result-v1', 'rules', 'person', 'normalized',
		 'structured_person', 'Булгаков', 'Михаил', '{Афанасьевич}', 'Мастер', ?, ?, ?, 'Cyrl')`,
		fp, bytes.Repeat([]byte{fillBulgakovRu ^ 0xff}, 32), display, display, authornorm.SearchKey(display))
	c.seedMergePersons(mixedFixture, fillBulgakovRu)
	for _, n := range []struct{ name, lang string }{{"Михаил Афанасьевич Булгаков", "ru"}, {"Mihail Afanasyevich Bulgakov", "en"}} {
		c.exec(`INSERT INTO wikidata_name (extract_id, key, qid, kind, lang, label)
			SELECT id, public.search_normalize(?), 'Q835', 'label', ?, ? FROM wikidata_extract`, n.name, n.lang, n.name)
	}
	rep := c.build()
	m := e.mergeConfig()
	e.fake.setRespond(mergeStub)
	ctx := context.Background()
	stored := storedDigests(t, e, authorentity.CandidateTranslitMixed)
	require.Len(t, stored, 1, "Булгаков: %v", rep.Candidates.ByKind[authorentity.CandidateTranslitMixed])
	for kind, scripts := range rep.Candidates.ByKind {
		for script, counts := range scripts {
			assert.Zero(t, counts.Excluded[authorentity.ExcludedMixedScript], "%s %s", kind, script)
		}
	}

	// A live translit registration leaves the mixed candidate undue.
	c.exec(`INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		VALUES ('eval', 'merge4', 'completed', ?, now())`, m.version)
	c.exec(`INSERT INTO author_llm_eval_report (run_id, config_version, report, report_sha256)
		SELECT max(id), ?0, '{}', sha256('translit') FROM author_llm_run WHERE config_version = ?0`, m.version)
	c.exec(`INSERT INTO author_entity_merge_registration (config_version, kind, evidence_report_id,
		evidence_report_sha256, gate) SELECT config_version, 'translit', id, report_sha256, 'judge_consensus'
		FROM author_llm_eval_report WHERE report_sha256 = sha256('translit')`)
	due, err := database.MergeCandidatesDue(ctx, e.db, m.version)
	require.NoError(t, err)
	require.NotEmpty(t, due, "the translit candidates are due")
	for _, d := range due {
		assert.Equal(t, authorentity.CandidateTranslit, d.Kind)
	}

	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge7, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	sets := frozenOf(t, e, models.AuthorLLMEvalModeMerge7)
	require.Len(t, sets["holdout"], 1)
	assert.Equal(t, "translit_mixed.cyrl-latn", sets["holdout"][0].Stratum)
	assert.Equal(t, stored, fingerprints(sets["holdout"]))
	shapes := map[string]bool{}
	for _, it := range sets["adversarial"] {
		kind, _, _ := strings.Cut(it.Stratum, ".")
		require.True(t, authorentity.MixedKind(kind), it.Stratum)
		shapes[kind] = true
	}
	assert.NotEmpty(t, shapes, "the suite is of the mixed shapes (as many as the fixture's namesake pairs fill)")

	// The mixed side's card: every written form, both scripts, and its item.
	var in authorentity.MergeInput
	require.NoError(t, json.Unmarshal([]byte(sets["holdout"][0].Input), &in))
	mixed := in.A
	if mixed.WikidataID == "" {
		mixed = in.B
	}
	assert.Equal(t, "Q835", mixed.WikidataID)
	assert.Equal(t, "Михаил Афанасьевич Булгаков", mixed.Wikidata)
	assert.Equal(t, []string{"Мастер"}, mixed.Variants)
	assert.Equal(t, []string{"Mihail Afanasyevich Bulgakov", "Михаил Афанасьевич Булгаков"}, mixed.Names)

	raw, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run)
	require.NoError(t, err)
	var r stageReport
	require.NoError(t, json.Unmarshal(raw, &r))
	assert.Equal(t, "census_audit", r.Options.GateRule)
	var strata []string
	for _, s := range r.Evaluation.Decision.Strata {
		strata = append(strata, s.Stratum)
		assert.Equal(t, "census_audit", s.Rule, s.Stratum)
	}
	assert.Contains(t, strata, authorentity.CandidateTranslitMixed)

	// The audit gates it; a passing report registers translit_mixed, never
	// translit.
	admin := services.NewAuthorEntityAdmin(e.db)
	cloneMixedItems(t, e.db, run, 200)
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateTranslitMixed, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed, "the audit sample is pending")
	audit, err := services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	require.NotEmpty(t, audit.Items)
	// The auditor sees the identity evidence the models saw: the mixed
	// side's variants, Wikidata label and QID, exactly as frozen.
	for _, it := range audit.Items {
		side := it.A
		if side.WikidataID == "" {
			side = it.B
		}
		assert.Equal(t, mixed.WikidataID, side.WikidataID, "audit item %d", it.EvalItemID)
		assert.Equal(t, mixed.Wikidata, side.Wikidata, "audit item %d", it.EvalItemID)
		assert.Equal(t, mixed.Variants, side.Variants, "audit item %d", it.EvalItemID)
		assert.Equal(t, mixed.Names, side.Names, "audit item %d", it.EvalItemID)
	}
	for _, it := range audit.Items {
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, it.EvalItemID, database.AdjudicationSame, "",
			"coordinator"))
	}
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateTranslit, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed, "a merge7 report passes no translit")
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateTranslitMixed, 0)
	require.NoError(t, err)

	require.NotEqual(t, c.entityOf(fillBulgakovRu), c.entityOf(fillBulgakovLatin))
	applied, err := admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_merge_decision WHERE kind = 'translit_mixed'
		AND revoked_at IS NULL AND digest = decode('`+hex.EncodeToString(sets["holdout"][0].SourceFingerprint)+`', 'hex')`),
		"%+v", applied)
	c.build()
	assert.Equal(t, c.entityOf(fillBulgakovRu), c.entityOf(fillBulgakovLatin), "the build joins the Latin side")
}

// cloneMixedItems stores n copies of the translit_mixed census item of run
// whose every answer is same and that every participant was asked again.
func cloneMixedItems(t *testing.T, db *pg.DB, run int64, n int) {
	t.Helper()
	var template int64
	_, err := db.QueryOne(pg.Scan(&template), `
		SELECT i.id FROM author_llm_eval_item i
		JOIN author_llm_job j ON j.eval_item_id = i.id AND j.run_id = ?0
		LEFT JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.item_set = 'holdout' AND i.stratum LIKE 'translit_mixed.%'
		GROUP BY i.id
		HAVING count(*) FILTER (WHERE j.purpose = 'eval_repeat') = 4
			AND bool_and(j.status = 'answered' AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8')))
		ORDER BY i.id LIMIT 1`, run)
	require.NoError(t, err, "a translit_mixed census item answered same and repeated by all")
	cloneItem(t, db, run, template, n)
}
