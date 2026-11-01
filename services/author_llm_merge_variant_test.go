package services_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The variant kind (E4f) through the whole flow: merge5 is a census of
// every stored variant candidate, its natural Wikidata namesakes asked in
// the suite instead; the census is gated on the audit of its applied set
// like the other censuses; a passing report registers variant; a merge run
// asks a variant candidate no eval asked and the build joins the two
// spellings.

// variantFixture adds to the stage fixture two spellings of one patronymic
// (P1), a Latin middle-name typo (P2) and two people whose names fold to
// one, «Александр Воинов» and «Александр Войнов», whom Wikidata tells
// apart.
var variantFixture = []mergePerson{
	{"Борушко", "Олег", "Матвевич", "Cyrl", "nonf_biography", 2, 0},
	{"Борушко", "Олег", "Матвеевич", "Cyrl", "nonf_biography", 1, 0},
	{"Brackett", "Leigh", "Douglass", "Latn", "sf", 1, 0}, {"Brackett", "Leigh", "Douglas", "Latn", "sf", 1, 0},
	{"Воинов", "Александр", "", "Cyrl", "prose_history", 1, 0},
	{"Войнов", "Александр", "", "Cyrl", "sci_history", 1, 0},
}

// The fills of variantFixture (seedMergePersons, 0x80 + i).
const (
	fillBorushko      = 0x80
	fillBorushkoOther = 0x81
	fillVoinov        = 0x84
)

// variantEnv is the stage fixture with variantFixture and the Wikidata
// items of «Олег Матвеевич Борушко», «Александр Воинов» and «Александр
// Войнов», built, with the merge configuration.
func variantEnv(t *testing.T) (e *llmEnv, m *mergeConfig, c *entityCatalog, rep services.AuthorEntityBuildReport) {
	t.Helper()
	e = newLLMEnv(t)
	c = &entityCatalog{t: t, db: e.db, next: 1000}
	c.seedStageFixture()
	c.seedMergePersons(variantFixture, fillBorushko)
	for _, n := range []struct{ name, qid string }{
		{"Олег Матвеевич Борушко", "Q911"}, {"Александр Воинов", "Q912"}, {"Александр Войнов", "Q913"},
	} {
		c.exec(`INSERT INTO wikidata_name (extract_id, key, qid, kind, lang, label)
			SELECT id, public.search_normalize(?), ?, 'label', 'ru', ? FROM wikidata_extract`, n.name, n.qid, n.name)
	}
	rep = c.build()
	m = e.mergeConfig()
	e.fake.setRespond(mergeStub)
	return e, m, c, rep
}

// cloneVariantItems stores n copies of a variant census item of run whose
// every answer is same and that every participant was asked again.
func cloneVariantItems(t *testing.T, db *pg.DB, run int64, n int) {
	t.Helper()
	var template int64
	_, err := db.QueryOne(pg.Scan(&template), `
		SELECT i.id FROM author_llm_eval_item i
		JOIN author_llm_job j ON j.eval_item_id = i.id AND j.run_id = ?0
		LEFT JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.item_set = 'holdout' AND i.stratum LIKE 'variant.%'
		GROUP BY i.id
		HAVING count(*) FILTER (WHERE j.purpose = 'eval_repeat') = 4
			AND bool_and(j.status = 'answered' AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8')))
		ORDER BY i.id LIMIT 1`, run)
	require.NoError(t, err, "a variant census item answered same and repeated by all")
	cloneItem(t, db, run, template, n)
}

func TestTheVariantCensusRegistersAndAMergeRunAppliesIt(t *testing.T) {
	e, m, c, rep := variantEnv(t)
	ctx := context.Background()
	stored := storedDigests(t, e, authorentity.CandidateVariant)
	require.Len(t, stored, 3, "Борушко, Brackett, Воинов: %v", rep.Candidates.ByKind[authorentity.CandidateVariant])
	require.Len(t, stored, kindCount(&rep, authorentity.CandidateVariant))

	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge5, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	sets := frozenOf(t, e, models.AuthorLLMEvalModeMerge5)

	// The natural namesakes are asked in the suite, never in the census;
	// together they are every candidate.
	var natural []frozenItem
	for _, it := range sets["adversarial"] {
		assert.True(t, strings.HasPrefix(it.Stratum, "variant."), it.Stratum)
		if it.ExtractorVersion == authorentity.MergeCandidateVersion {
			natural = append(natural, it)
		}
	}
	require.Len(t, natural, 1)
	assert.Contains(t, natural[0].Input, "Войнов")
	assert.Len(t, sets["adversarial"], stageEvalSizes.SuitePerShape, "the natural namesakes count in the suite")
	census := append(fingerprints(sets["holdout"]), fingerprints(natural)...)
	slices.Sort(census)
	assert.Equal(t, stored, census)
	require.Len(t, sets["holdout"], 2)
	assert.Equal(t, 1, countScript(sets["holdout"], "latn"))
	assert.Empty(t, sets["supplement"])

	// The variant card carries the Wikidata label of its side's item.
	var labeled int
	for _, it := range sets["holdout"] {
		var in authorentity.MergeInput
		require.NoError(t, json.Unmarshal([]byte(it.Input), &in))
		if in.A.Wikidata != "" || in.B.Wikidata != "" {
			labeled++
			assert.Contains(t, []string{in.A.Wikidata, in.B.Wikidata}, "Олег Матвеевич Борушко")
		}
	}
	assert.Equal(t, 1, labeled)

	raw, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run)
	require.NoError(t, err)
	var r stageReport
	require.NoError(t, json.Unmarshal(raw, &r))
	assert.Equal(t, "census_audit", r.Options.GateRule)
	require.Len(t, r.Evaluation.Decision.Strata, 1)
	assert.Equal(t, authorentity.CandidateVariant, r.Evaluation.Decision.Strata[0].Stratum)
	assert.Equal(t, "census_audit", r.Evaluation.Decision.Strata[0].Rule)
	require.NotEmpty(t, r.Recall, "the typo merge «Михайлович»/«Михайлвич» is a variant anchor")
	for _, a := range r.Recall {
		assert.Equal(t, authorentity.CandidateVariant, a.Kind)
	}

	// The audit gates it, and a passing report registers variant.
	admin := services.NewAuthorEntityAdmin(e.db)
	cloneVariantItems(t, e.db, run, 200)
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateVariant, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed, "the audit sample is pending")
	audit, err := services.AuthorLLMAuditItems(ctx, e.db, run)
	require.NoError(t, err)
	require.NotEmpty(t, audit.Items)
	for _, it := range audit.Items {
		require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, e.db, it.EvalItemID, database.AdjudicationSame, "",
			"coordinator"))
	}
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateVariant, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)
	due, err := database.MergeCandidatesDue(ctx, e.db, m.version)
	require.NoError(t, err)
	assert.Empty(t, due, "the census asked every variant candidate")

	// A changed card is a candidate no eval asked: the merge run asks it,
	// the pair and both judges answer same, and the build joins the two
	// spellings of «Матвеевич».
	require.NotEqual(t, c.entityOf(fillBorushko), c.entityOf(fillBorushkoOther))
	c.exec(`UPDATE opds_catalog_book SET title = title || ' (2-е изд.)' WHERE id = (SELECT s.book_id
		FROM book_contributor_credit cr JOIN book_metadata_snapshot s ON s.id = cr.snapshot_id AND s.is_current
		WHERE cr.source_fingerprint = ? LIMIT 1)`, bytes.Repeat([]byte{fillBorushkoOther}, 32))
	c.build()
	due, err = database.MergeCandidatesDue(ctx, e.db, m.version)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, authorentity.CandidateVariant, due[0].Kind)
	mergeRun, err := services.StartAuthorLLMMergeRun(ctx, e.db, m.version, models.AuthorLLMEvalModeFull, 0)
	require.NoError(t, err)
	driver := services.NewAuthorLLMMergeRunDriver(e.db, m.version, time.Millisecond)
	w := e.mergeWorker(m)
	for range 300 {
		if _, status := e.runStage(mergeRun); status == string(models.AuthorLLMRunCompleted) {
			break
		}
		_, err = driver.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond)
	}
	_, status := e.runStage(mergeRun)
	require.Equal(t, string(models.AuthorLLMRunCompleted), status)
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_merge_decision WHERE kind = 'variant'
		AND revoked_at IS NULL AND digest = decode('`+hex.EncodeToString(due[0].Digest)+`', 'hex')`))
	c.build()
	assert.Equal(t, c.entityOf(fillBorushko), c.entityOf(fillBorushkoOther), "the build joins the two spellings")
	assert.NotEqual(t, c.entityOf(fillVoinov), c.entityOf(fillVoinov+1), "the namesakes stay apart")
}
