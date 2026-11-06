package services_test

import (
	"bytes"
	"context"
	"encoding/hex"
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

// The spelling kinds (D2) through the whole flow: merge9 is a census of every
// stored typo and name_form candidate, the natural Wikidata namesakes asked
// in the suite instead; a passing report registers typo; a merge run asks a
// candidate no eval asked and the build joins the two spellings.

// spellingFixture adds to the stage fixture a misspelled family, two people a
// typo pairs and Wikidata tells apart, and a given name cut short.
var spellingFixture = []mergePerson{
	{"Чуковсктй", "Корней", "Иванович", "Cyrl", "sf", 1, 0},
	{"Чуковский", "Корней", "Иванович", "Cyrl", "sf", 2, 0},
	{"Петрухин", "Илларион", "", "Cyrl", "prose_history", 1, 0},
	{"Петрушин", "Илларион", "", "Cyrl", "sci_history", 1, 0},
	{"Олдридж", "Дж", "", "Cyrl", "sf", 1, 0},
	{"Олдридж", "Джеймс", "", "Cyrl", "sf", 1, 0},
}

// The fills of spellingFixture (seedMergePersons, 0x80 + i).
const (
	fillChukovskyTypo = 0x80
	fillChukovsky     = 0x81
	fillPetrukhin     = 0x82
)

func spellingEnv(t *testing.T) (e *llmEnv, m *mergeConfig, c *entityCatalog, rep services.AuthorEntityBuildReport) {
	t.Helper()
	e = newLLMEnv(t)
	c = &entityCatalog{t: t, db: e.db, next: 1000}
	c.seedStageFixture()
	c.seedMergePersons(spellingFixture, fillChukovskyTypo)
	for _, n := range []struct{ name, qid string }{{"Илларион Петрухин", "Q921"}, {"Илларион Петрушин", "Q922"}} {
		c.exec(`INSERT INTO wikidata_name (extract_id, key, qid, kind, lang, label)
			SELECT id, public.search_normalize(?), ?, 'label', 'ru', ? FROM wikidata_extract`, n.name, n.qid, n.name)
	}
	rep = c.build()
	m = e.mergeConfig()
	e.fake.setRespond(mergeStub)
	return e, m, c, rep
}

// cloneTypoItems stores n copies of a typo census item of run whose every
// answer is same and that every participant was asked again.
func cloneTypoItems(t *testing.T, db *pg.DB, run int64, n int) {
	t.Helper()
	var template int64
	_, err := db.QueryOne(pg.Scan(&template), `
		SELECT i.id FROM author_llm_eval_item i
		JOIN author_llm_job j ON j.eval_item_id = i.id AND j.run_id = ?0
		LEFT JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.item_set = 'holdout' AND i.stratum LIKE 'typo.%'
		GROUP BY i.id
		HAVING count(*) FILTER (WHERE j.purpose = 'eval_repeat') = 4
			AND bool_and(j.status = 'answered' AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8')))
		ORDER BY i.id LIMIT 1`, run)
	require.NoError(t, err, "a typo census item answered same and repeated by all")
	cloneItem(t, db, run, template, n)
}

func TestTheSpellingCensusRegistersAndAMergeRunJoins(t *testing.T) {
	e, m, c, _ := spellingEnv(t)
	ctx := context.Background()
	stored := append(storedDigests(t, e, authorentity.CandidateTypo), storedDigests(t, e, authorentity.CandidateNameForm)...)
	slices.Sort(stored)
	require.NotEmpty(t, storedDigests(t, e, authorentity.CandidateTypo))
	require.NotEmpty(t, storedDigests(t, e, authorentity.CandidateNameForm))

	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge9, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	sets := frozenOf(t, e, models.AuthorLLMEvalModeMerge9)
	var natural []frozenItem
	for _, it := range sets["adversarial"] {
		assert.True(t, strings.HasPrefix(it.Stratum, "typo.") || strings.HasPrefix(it.Stratum, "name_form."), it.Stratum)
		if it.ExtractorVersion == authorentity.MergeCandidateVersion {
			natural = append(natural, it)
		}
	}
	require.Len(t, natural, 1, "the two items are namesakes, asked in the suite")
	assert.Contains(t, natural[0].Input, "Петрушин")
	census := append(fingerprints(sets["holdout"]), fingerprints(natural)...)
	slices.Sort(census)
	assert.Equal(t, stored, census)

	admin := services.NewAuthorEntityAdmin(e.db)
	cloneTypoItems(t, e.db, run, 200)
	_, _, err = services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	_, err = admin.RegisterMergeKind(ctx, run, authorentity.CandidateTypo, 0)
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
	registration, err := admin.RegisterMergeKind(ctx, run, authorentity.CandidateTypo, 0)
	require.NoError(t, err)
	assert.Positive(t, registration)

	// A changed card is a candidate no eval asked: the merge run asks it and
	// the build joins the two spellings.
	require.NotEqual(t, c.entityOf(fillChukovskyTypo), c.entityOf(fillChukovsky))
	c.exec(`UPDATE opds_catalog_book SET title = title || ' (2-е изд.)' WHERE id = (SELECT s.book_id
		FROM book_contributor_credit cr JOIN book_metadata_snapshot s ON s.id = cr.snapshot_id AND s.is_current
		WHERE cr.source_fingerprint = ? LIMIT 1)`, bytes.Repeat([]byte{fillChukovsky}, 32))
	c.build()
	due, err := database.MergeCandidatesDue(ctx, e.db, m.version)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, authorentity.CandidateTypo, due[0].Kind)
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
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_merge_decision WHERE kind = 'typo'
		AND revoked_at IS NULL AND digest = decode('`+hex.EncodeToString(due[0].Digest)+`', 'hex')`))
	c.build()
	assert.Equal(t, c.entityOf(fillChukovskyTypo), c.entityOf(fillChukovsky), "the build joins the two spellings")
	assert.NotEqual(t, c.entityOf(fillPetrukhin), c.entityOf(fillPetrukhin+1), "the namesakes stay apart")
}
