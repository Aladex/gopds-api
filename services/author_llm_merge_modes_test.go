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

// The merge eval's stage modes (E4c-2b): merge1 a swap census, merge2 an
// initials census, merge3 a bucket holdout with extra Latin questions
// gated outside |A|, merge4 a translit census whose natural Wikidata
// namesakes are asked in the suite. Each freezes and runs end to end on
// the fixture with the stub provider.

// stageEvalSizes are the stage modes' sizes shrunk to the fixture: a
// suite of 4 with 1 Latin, a bucket holdout of 2 and 1 extra Latin
// question.
var stageEvalSizes = services.AuthorLLMMergeEvalSizes{PerKind: 2, SuitePerShape: 4, SuiteLatinPerShape: 1,
	AnchorsPerShape: 5, SupplementLatin: 1, Repeat: 3, FingerprintRepeats: 3}

// mergeStageFixture adds what the stages need: two short-name buckets
// beside a full name (one Cyrillic, one Latin), a short-name book D3 moves
// to its full name by series (a bucket anchor), a translit pair whose
// sides name two Wikidata items (natural namesakes), a name the Wikidata
// merge joins across scripts (a translit anchor), and a namesake pair one
// of whose sides is a swap candidate's.
var mergeStageFixture = []mergePerson{
	{"Сидоров", "Пётр", "", "Cyrl", "det_police", 2, 0}, {"Сидоров", "Пётр", "Ильич", "Cyrl", "det_police", 2, 0},
	{"Brown", "Dan", "", "Latn", "det_thriller", 3, 0}, {"Brown", "Dan", "Gerhard", "Latn", "det_thriller", 1, 0},
	{"Орлов", "Илья", "", "Cyrl", "sf_space", 1, 88}, {"Орлов", "Илья", "Петрович", "Cyrl", "sf_space", 1, 88},
	{"Тарт", "Донна", "", "Cyrl", "prose_contemporary", 1, 0}, {"Tartt", "Donna", "", "Latn", "prose_contemporary", 1, 0},
	{"Мураками", "Харуки", "", "Cyrl", "prose_contemporary", 1, 0},
	{"Murakami", "Haruki", "", "Latn", "prose_contemporary", 1, 0},
	{"Волков", "Олег", "Игоревич", "Cyrl", "sci_history", 2, 0}, {"Олег", "Волков", "Игоревич", "Cyrl", "sci_history", 1, 0},
	{"Волков", "Олег", "Борисович", "Cyrl", "home_cooking", 1, 0},
}

// seedStageFixture writes the merge0 fixture, the stages' additions and a
// published Wikidata extract: «Донна Тарт» and «Donna Tartt» two items,
// «Харуки Мураками» and «Haruki Murakami» one.
func (c *entityCatalog) seedStageFixture() []string {
	c.t.Helper()
	c.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES ('4', 'structured_person', 'Cyrl', 'normalizer-v1', ?, 'internal/authornorm/policy_evidence/fixture.json',
		 'shipped')`, bytes.Repeat([]byte{0xee}, 32))
	secrets := c.seedMergeFixture()
	secrets = append(secrets, c.seedMergePersons(mergeStageFixture, 0x60)...)
	c.exec(`INSERT INTO wikidata_extract (source, status, content_id, manifest)
		VALUES ('fixture', 'published', 'wd-0123456789abcdef', '{}')`)
	for _, n := range []struct{ name, qid, lang string }{
		{"Донна Тарт", "Q901", "ru"}, {"Donna Tartt", "Q902", "en"},
		{"Харуки Мураками", "Q903", "ru"}, {"Haruki Murakami", "Q903", "en"},
	} {
		c.exec(`INSERT INTO wikidata_name (extract_id, key, qid, kind, lang, label)
			SELECT id, public.search_normalize(?), ?, 'label', ?, ? FROM wikidata_extract`, n.name, n.qid, n.lang, n.name)
	}
	return secrets
}

// frozenItem is one frozen item of a mode's sets.
type frozenItem struct {
	SetVersion        string `pg:"set_version"`
	ItemSet           string `pg:"item_set"`
	Stratum           string `pg:"stratum"`
	SourceFingerprint []byte `pg:"source_fingerprint"`
	ExtractorVersion  string `pg:"extractor_version"`
	Input             string `pg:"input"`
}

// frozenOf are a mode's frozen items by set name (holdout, adversarial,
// anchor, supplement).
func frozenOf(t *testing.T, e *llmEnv, mode string) map[string][]frozenItem {
	t.Helper()
	var rows []frozenItem
	_, err := e.db.Query(&rows, `SELECT set_version, item_set, stratum, source_fingerprint, extractor_version,
		input::text AS input FROM author_llm_eval_item WHERE set_version LIKE ? ORDER BY id`, "m1."+mode+".%")
	require.NoError(t, err)
	out := map[string][]frozenItem{}
	for _, r := range rows {
		name := strings.TrimPrefix(r.SetVersion, "m1."+mode+".")
		out[name] = append(out[name], r)
	}
	return out
}

// storedDigests are the stored candidates of a kind, hex.
func storedDigests(t *testing.T, e *llmEnv, kind string) []string {
	t.Helper()
	var digests [][]byte
	_, err := e.db.Query(&digests, `SELECT digest FROM author_entity_merge_candidate WHERE kind = ?`, kind)
	require.NoError(t, err)
	out := make([]string, 0, len(digests))
	for _, d := range digests {
		out = append(out, hex.EncodeToString(d))
	}
	slices.Sort(out)
	return out
}

// fingerprints are items' source fingerprints, hex, sorted.
func fingerprints(items []frozenItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, hex.EncodeToString(it.SourceFingerprint))
	}
	slices.Sort(out)
	return out
}

// countScript counts items whose stratum is of script (lowercase).
func countScript(items []frozenItem, script string) int {
	n := 0
	for _, it := range items {
		if strings.HasSuffix(it.Stratum, "."+script) {
			n++
		}
	}
	return n
}

// stepMergeEval steps a merge eval run's driver and worker until it
// completes, or only its freeze when freezeOnly.
func stepMergeEval(t *testing.T, e *llmEnv, m *mergeConfig, run int64, sizes services.AuthorLLMMergeEvalSizes,
	freezeOnly bool) {
	t.Helper()
	ctx := context.Background()
	driver := e.mergeWorker(m).EvalDriver().WithMergeSizes(sizes)
	w := e.mergeWorker(m)
	for range 300 {
		stage, status := e.runStage(run)
		if status == string(models.AuthorLLMRunCompleted) || (freezeOnly && stage != "scoped_freeze") {
			return
		}
		require.Equal(t, string(models.AuthorLLMRunRunning), status, "stopped in %s", stage)
		_, err := driver.Step(ctx)
		require.NoError(t, err)
		if !freezeOnly {
			_, err = w.RunOnce(ctx)
			require.NoError(t, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the merge eval did not complete")
}

// stageEnv is the fixture built with the stages' additions and the merge
// configuration.
func stageEnv(t *testing.T) (e *llmEnv, m *mergeConfig, rep services.AuthorEntityBuildReport, secrets []string) {
	t.Helper()
	e = newLLMEnv(t)
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	secrets = c.seedStageFixture()
	rep = c.build()
	m = e.mergeConfig()
	e.fake.setRespond(mergeStub)
	return e, m, rep, secrets
}

// kindCount is a build report's candidates of a kind in every script.
func kindCount(rep *services.AuthorEntityBuildReport, kind string) int {
	n := 0
	for _, c := range rep.Candidates.ByKind[kind] {
		n += c.Candidates
	}
	return n
}

// Every stage mode freezes its sets and runs end to end: a census asks
// every stored candidate of its kind, merge3 a holdout and its extra Latin
// questions outside the holdout, merge4 its natural Wikidata namesakes in
// the suite and not in the census; each shape's suite has its size and
// Latin share (the translit one Cyrillic against Latin throughout), and
// every report keeps the extra questions out of |A|.
func TestEachStageModeFreezesAndRunsEndToEnd(t *testing.T) {
	e, m, rep, secrets := stageEnv(t)
	logs := captureLogs(t)
	ctx := context.Background()
	require.Positive(t, kindCount(&rep, "bucket"))
	require.Positive(t, kindCount(&rep, "translit"))

	for _, tc := range []struct {
		mode, kind string
		census     bool
	}{
		{models.AuthorLLMEvalModeMerge1, "swap", true},
		{models.AuthorLLMEvalModeMerge2, "initials", true},
		{models.AuthorLLMEvalModeMerge3, "bucket", false},
		{models.AuthorLLMEvalModeMerge4, "translit", true},
	} {
		run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, tc.mode, nil)
		require.NoError(t, err, tc.mode)
		stepMergeEval(t, e, m, run, stageEvalSizes, false)
		sets := frozenOf(t, e, tc.mode)
		stored := storedDigests(t, e, tc.kind)
		require.Len(t, stored, kindCount(&rep, tc.kind), tc.mode)

		var natural []frozenItem
		for _, it := range sets["adversarial"] {
			if it.ExtractorVersion == authorentity.MergeCandidateVersion {
				natural = append(natural, it)
			}
		}
		switch tc.mode {
		case models.AuthorLLMEvalModeMerge4:
			// The natural namesakes: «Донна Тарт» against «Donna Tartt».
			require.Len(t, natural, 1, tc.mode)
			assert.Contains(t, natural[0].Input, "Donna Tartt")
			assert.Equal(t, "translit.cyrl-latn", natural[0].Stratum)
			census := append(fingerprints(sets["holdout"]), fingerprints(natural)...)
			slices.Sort(census)
			assert.Equal(t, stored, census, "census and suite together cover every candidate")
			assert.NotContains(t, fingerprints(sets["holdout"]), fingerprints(natural)[0])
			assert.Len(t, sets["adversarial"], stageEvalSizes.SuitePerShape)
			assert.Equal(t, len(sets["adversarial"]), countScript(sets["adversarial"], "cyrl-latn"),
				"a translit item is Cyrillic against Latin")
		case models.AuthorLLMEvalModeMerge3:
			require.Len(t, sets["holdout"], stageEvalSizes.PerKind)
			require.Len(t, sets["supplement"], stageEvalSizes.SupplementLatin)
			assert.Equal(t, "bucket.latn", sets["supplement"][0].Stratum)
			assert.NotContains(t, fingerprints(sets["holdout"]), fingerprints(sets["supplement"])[0])
			assert.Subset(t, stored, append(fingerprints(sets["holdout"]), fingerprints(sets["supplement"])...))
		default:
			assert.Equal(t, stored, fingerprints(sets["holdout"]), "%s: the census is every candidate", tc.mode)
		}
		if tc.mode != models.AuthorLLMEvalModeMerge4 {
			assert.Empty(t, natural, tc.mode)
			assert.Len(t, sets["adversarial"], stageEvalSizes.SuitePerShape, tc.mode)
			assert.Equal(t, stageEvalSizes.SuiteLatinPerShape, countScript(sets["adversarial"], "latn"), tc.mode)
		}
		if tc.mode != models.AuthorLLMEvalModeMerge3 {
			assert.Empty(t, sets["supplement"], tc.mode)
		}
		for _, it := range sets["adversarial"] {
			assert.True(t, strings.HasPrefix(it.Stratum, tc.kind+"."), "%s: suite stratum %s", tc.mode, it.Stratum)
		}

		raw, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run)
		require.NoError(t, err, tc.mode)
		var r stageReport
		require.NoError(t, json.Unmarshal(raw, &r), tc.mode)
		assert.Equal(t, tc.mode, r.Mode)
		stratum := r.Evaluation.Strata[tc.kind]
		combo := stratum.Combos["A1"]
		assert.Equal(t, len(sets["holdout"]), stratum.Holdout, "%s: |A| is over the holdout alone", tc.mode)
		require.NotNil(t, combo.Adversarial, tc.mode)
		assert.Equal(t, len(sets["adversarial"]), combo.Adversarial.Items, tc.mode)
		// A census mode's stratum is gated as a census on the audit of its
		// applied set (§3.3), merge3's bucket holdout on the bound; the
		// report says which.
		rule := "holdout"
		if tc.census {
			rule = "census_audit"
			require.NotNil(t, combo.Census, tc.mode)
			assert.Equal(t, len(sets["holdout"]), combo.Census.Candidates, "%s: the census is every frozen candidate", tc.mode)
			assert.Equal(t, combo.Census.Candidates, combo.Census.Settled+len(combo.Census.Unsettled), tc.mode)
		} else {
			assert.Nil(t, combo.Census, tc.mode)
		}
		assert.Equal(t, rule, r.Options.GateRule, tc.mode)
		require.Len(t, r.Evaluation.Decision.Strata, 1, tc.mode)
		assert.Equal(t, rule, r.Evaluation.Decision.Strata[0].Rule, tc.mode)
		if tc.mode == models.AuthorLLMEvalModeMerge3 {
			require.NotNil(t, combo.Supplement)
			assert.Equal(t, len(sets["supplement"]), combo.Supplement.Items)
			assert.LessOrEqual(t, combo.Accepted, len(sets["holdout"]), "an extra question counted in |A|")
		} else {
			assert.Nil(t, combo.Supplement, tc.mode)
		}
		anchors := 1
		if tc.mode == models.AuthorLLMEvalModeMerge2 {
			anchors = 0 // the fixture holds no initials anchor (merge0's test says so too)
		}
		assert.Len(t, r.Recall, anchors, "%s: the anchors of the shape", tc.mode)
		for _, a := range r.Recall {
			assert.Equal(t, tc.kind, a.Kind, tc.mode)
		}
		var est int64
		_, err = e.db.QueryOne(pg.Scan(&est), `SELECT estimate_tokens FROM author_llm_run WHERE id = ?`, run)
		require.NoError(t, err, tc.mode)
		assert.Positive(t, est, tc.mode)
	}
	out := logs.String()
	for _, s := range secrets {
		assert.NotContains(t, out, s)
	}
}

// stageReport is the part of a stage report the tests read.
type stageReport struct {
	Mode    string `json:"mode"`
	Options struct {
		GateRule string `json:"gate_rule"`
	} `json:"options"`
	Recall []struct {
		Kind string `json:"kind"`
	} `json:"recall"`
	Evaluation struct {
		Strata map[string]struct {
			Holdout int `json:"holdout"`
			Combos  map[string]struct {
				Accepted    int `json:"accepted"`
				Adversarial *struct {
					Items int `json:"items"`
				} `json:"adversarial"`
				Supplement *struct {
					Items    int `json:"items"`
					Accepted int `json:"accepted"`
				} `json:"supplement"`
				Census *struct {
					Candidates int     `json:"candidates"`
					Settled    int     `json:"settled"`
					Unsettled  []int64 `json:"unsettled"`
				} `json:"census"`
			} `json:"combos"`
		} `json:"strata"`
		Decision struct {
			Strata []struct {
				Stratum string `json:"stratum"`
				Rule    string `json:"rule"`
			} `json:"strata"`
		} `json:"decision"`
	} `json:"evaluation"`
}

// A census freezes only the current candidates a committed pass stored: one
// whose stored row is gone, or whose stored digest is stale, is not asked.
func TestACensusFreezesOnlyStoredCurrentCandidates(t *testing.T) {
	e, m, _, _ := stageEnv(t)
	stored := storedDigests(t, e, "swap")
	require.GreaterOrEqual(t, len(stored), 3)
	gone, _ := hex.DecodeString(stored[0])
	stale, _ := hex.DecodeString(stored[1])
	e.exec(`DELETE FROM author_entity_merge_candidate WHERE digest = ?`, gone)
	// The stale one: the stored row is of an older digest of the same pair.
	e.exec(`INSERT INTO author_entity_merge_candidate (digest, kind, script, item_kind, side_a_entity, side_b_entity,
		credit_id, credit_ordinal, members_sha256, input_sha256, first_build_run_id)
		SELECT ?, kind, script, item_kind, side_a_entity, side_b_entity, credit_id, credit_ordinal, members_sha256,
		 input_sha256, first_build_run_id FROM author_entity_merge_candidate WHERE digest = ?`,
		bytes.Repeat([]byte{0x77}, 32), stale)
	e.exec(`DELETE FROM author_entity_merge_candidate WHERE digest = ?`, stale)

	run, err := services.StartAuthorLLMMergeEval(context.Background(), e.db, m.version, models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, true)
	frozen := fingerprints(frozenOf(t, e, models.AuthorLLMEvalModeMerge1)["holdout"])
	assert.Equal(t, stored[2:], frozen, "the census is the stored current candidates")
	assert.NotContains(t, frozen, stored[0], "a candidate without a stored row")
	assert.NotContains(t, frozen, stored[1], "a candidate whose stored digest is stale")
}

// The suite never uses a side of any candidate: the namesake pair «Олег
// Игоревич Волков» / «Олег Борисович Волков» has a swap candidate's side,
// so no suite item shows it, however large the suite.
func TestTheSuiteIsEntityDisjointFromEveryCandidate(t *testing.T) {
	e, m, _, _ := stageEnv(t)
	sizes := stageEvalSizes
	sizes.SuitePerShape = 50
	run, err := services.StartAuthorLLMMergeEval(context.Background(), e.db, m.version, models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, sizes, true)
	sets := frozenOf(t, e, models.AuthorLLMEvalModeMerge1)
	require.NotEmpty(t, sets["adversarial"])
	var holdout []string
	for _, it := range sets["holdout"] {
		holdout = append(holdout, it.Input)
	}
	require.Contains(t, strings.Join(holdout, " "), "Волков", "the swap candidate is in the census")
	for _, it := range sets["adversarial"] {
		assert.NotContains(t, it.Input, "Волков", "a suite item shares a candidate's side")
		assert.NotContains(t, it.Input, "Volkov")
	}
}

// merge0 is unchanged by the stages: its kinds, sizes and suite shapes.
func TestMergePilotSizesAreUnchanged(t *testing.T) {
	assert.Equal(t, services.DefaultAuthorLLMMergeEvalSizes(),
		services.DefaultAuthorLLMMergeEvalSizesOf(models.AuthorLLMEvalModeMerge0))
	assert.Equal(t, services.AuthorLLMMergeEvalSizes{PerKind: 300, SuitePerShape: 300, SuiteLatinPerShape: 30,
		AnchorsPerShape: 150, Repeat: 200, FingerprintRepeats: services.DefaultAuthorLLMMergeEvalSizes().FingerprintRepeats},
		services.DefaultAuthorLLMMergeEvalSizes())
	for _, mode := range []string{models.AuthorLLMEvalModeMerge1, models.AuthorLLMEvalModeMerge2,
		models.AuthorLLMEvalModeMerge3, models.AuthorLLMEvalModeMerge4} {
		s := services.DefaultAuthorLLMMergeEvalSizesOf(mode)
		assert.Equal(t, 1000, s.SuitePerShape, mode)
		assert.Equal(t, 100, s.SuiteLatinPerShape, mode)
		if mode == models.AuthorLLMEvalModeMerge3 {
			assert.Equal(t, 6000, s.PerKind)
			assert.Equal(t, 1000, s.SupplementLatin)
		} else {
			assert.Zero(t, s.SupplementLatin, mode)
		}
	}
}

// An idle merge worker asks no provider anything: with no run, and with
// only K's run active (whose jobs K's worker then does ask).
func TestAnIdleMergeWorkerMakesNoProviderCall(t *testing.T) {
	e := newLLMEnv(t)
	m := e.mergeConfig()
	ctx := context.Background()
	w := e.mergeWorker(m)
	for range 3 {
		_, err := w.RunOnce(ctx)
		require.NoError(t, err)
	}
	assert.Empty(t, e.fake.served(), "an idle merge worker with no run")

	run := e.fingerprintRun()
	e.reviewPair(&run, "checked", givenFamily, 1)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if isFingerprint(req) {
			return fakeReply{Prompt: 777}
		}
		return fakeReply{}
	})
	for range 3 {
		_, err := w.RunOnce(ctx)
		require.NoError(t, err)
	}
	assert.Empty(t, e.fake.served(), "the merge worker asked K's run")
	_, err := e.worker().RunOnce(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, e.fake.served(), "K's run had nothing to ask")
}

// A merge1 report stored under the rule before the census gate (no rule,
// no census, the bound) is recomputed under the census rule as the run's
// next version (run 33's case, §3.3).
func TestARecomputeAppliesTheCensusRule(t *testing.T) {
	e, m, _, _ := stageEnv(t)
	ctx := context.Background()
	run, err := services.StartAuthorLLMMergeEval(ctx, e.db, m.version, models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)

	latest, err := database.GetAuthorLLMEvalReport(ctx, e.db, run)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(latest.Report), &doc))
	delete(doc["options"].(map[string]any), "gate_rule")
	evaluation := doc["evaluation"].(map[string]any)
	for _, s := range evaluation["strata"].(map[string]any) {
		for _, c := range s.(map[string]any)["combos"].(map[string]any) {
			delete(c.(map[string]any), "census")
		}
	}
	for _, d := range evaluation["decision"].(map[string]any)["strata"].([]any) {
		delete(d.(map[string]any), "rule")
	}
	old, err := json.Marshal(doc)
	require.NoError(t, err)
	_, err = e.db.Exec(`INSERT INTO author_llm_eval_report (run_id, config_version, version, report, report_sha256)
		SELECT ?0, ?1, ?2, ?3::jsonb, sha256(convert_to(?3::jsonb::text, 'UTF8'))`,
		run, latest.ConfigVersion, latest.Version+1, string(old))
	require.NoError(t, err)

	next, stored, err := services.RecomputeAuthorLLMMergeEvalReport(ctx, e.db, &m.identity, m.version, run)
	require.NoError(t, err)
	require.True(t, stored, "the census rule changes the report")
	assert.Equal(t, latest.Version+2, next.Version)
	var r stageReport
	require.NoError(t, json.Unmarshal([]byte(next.Report), &r))
	assert.Equal(t, "census_audit", r.Options.GateRule)
	require.Len(t, r.Evaluation.Decision.Strata, 1)
	assert.Equal(t, "census_audit", r.Evaluation.Decision.Strata[0].Rule)
	assert.NotNil(t, r.Evaluation.Strata["swap"].Combos["A1"].Census)
}
