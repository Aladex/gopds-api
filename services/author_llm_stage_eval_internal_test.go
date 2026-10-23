package services

import (
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/models"
)

// A stage eval's report backs a registration exactly as a full eval's: only
// a dev run never registers.
func TestStageEvalReportsAreRegistrable(t *testing.T) {
	for _, mode := range append([]string{models.AuthorLLMEvalModePilot, models.AuthorLLMEvalModeFull,
		models.AuthorLLMEvalModeSwap}, models.AuthorLLMEvalStageModes()...) {
		if !evalRegistrable(mode) {
			t.Errorf("an eval of mode %s is not registrable", mode)
		}
	}
	if evalRegistrable(models.AuthorLLMEvalModeDev) {
		t.Error("a dev eval is registrable")
	}
}

// A stage eval measures exactly its frozen stage strata; any other run never
// measures them as a stage, and a swap eval keeps to the swap strata.
func TestScopedStratumSets(t *testing.T) {
	frozen := map[string]bool{}
	for _, name := range []string{"initials.cyrl", "initials.latn", "particle.cyrl", "particle.latn", "nickname_with_name.cyrl",
		"mononym.cyrl", "single_field.cyrl", "affix.cyrl", "mixed_script.mixed", "swap.cyrl"} {
		frozen[authorLLMEvalStratumSet+name] = true
	}
	frozen[authorLLMEvalAnchorSet] = true
	sets := func(names ...string) []string {
		out := make([]string, 0, len(names))
		for _, n := range names {
			out = append(out, authorLLMEvalStratumSet+n)
		}
		slices.Sort(out)
		return out
	}
	cases := map[string][]string{
		models.AuthorLLMEvalModeStage0: sets("initials.cyrl", "initials.latn"),
		models.AuthorLLMEvalModeStage1: sets("nickname_with_name.cyrl", "particle.cyrl", "particle.latn"),
		models.AuthorLLMEvalModeStage2: sets("mononym.cyrl", "single_field.cyrl"),
		models.AuthorLLMEvalModeStage3: sets("affix.cyrl"),
		models.AuthorLLMEvalModeSwap:   sets("swap.cyrl"),
		models.AuthorLLMEvalModeFull: sets("initials.cyrl", "initials.latn", "particle.cyrl", "particle.latn",
			"nickname_with_name.cyrl",
			"mononym.cyrl", "single_field.cyrl", "affix.cyrl", "mixed_script.mixed"),
	}
	for mode, want := range cases {
		if got := scopedStratumSets(mode, frozen); !slices.Equal(got, want) {
			t.Errorf("%s measures %v, want %v", mode, got, want)
		}
	}
}

// The stored stage of a stage eval is never a stage name the binary before
// stage evals steps; any other eval keeps its plain names.
func TestStageEvalStagesAreScoped(t *testing.T) {
	plain := []string{evalStageFreeze, evalStageFingerprint, evalStageAnchor, evalStageJudge, evalStageCandidates,
		evalStageRepeat, evalStageReport}
	for _, mode := range models.AuthorLLMEvalStageModes() {
		for _, stage := range plain {
			stored := models.AuthorLLMEvalStoredStage(mode, stage)
			if slices.Contains(plain, stored) {
				t.Errorf("%s stores its stage %s as %q, which the old driver steps", mode, stage, stored)
			}
			if back := models.AuthorLLMEvalStageOf(mode, stored); back != stage {
				t.Errorf("%s reads %q back as %q", mode, stored, back)
			}
		}
	}
	for _, stage := range plain {
		if stored := models.AuthorLLMEvalStoredStage(models.AuthorLLMEvalModeFull, stage); stored != stage {
			t.Errorf("a full eval stores %s as %q", stage, stored)
		}
	}
}

// evidenceOf is one completed eval's evidence with a report deciding strata.
func evidenceOf(
	t *testing.T, report int64, registrable bool, modelIDs string, decisions ...llmeval.StratumDecision,
) database.AuthorLLMEvalEvidence {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"evaluation": llmeval.Report{
		Decision: llmeval.Decision{Registrable: registrable, Strata: decisions}}})
	if err != nil {
		t.Fatal(err)
	}
	return database.AuthorLLMEvalEvidence{RunID: report * 10, ReportID: report, ReportSHA256: []byte{byte(report)},
		Report: raw, ExpectedModels: json.RawMessage(modelIDs)}
}

func passed(name string) llmeval.StratumDecision {
	return llmeval.StratumDecision{Stratum: name, Passed: true, Arm: "A", ItemsPerRequest: 1, Tiers: []string{"confirm"}}
}

// Each stratum is decided by the newest registrable report that measured it,
// and planned with that report as its own provenance: a newer stage hides no
// stratum it did not measure, an empty or gate-failed one blocks nothing, a
// newer measurement that failed a stratum wins over an older pass, and a
// report of other model ids plans nothing.
func TestPlanAuthorLLMResolveFrom(t *testing.T) {
	const ids = `{"production_a":["a"],"production_b":["b"],"judge_a":["c"],"judge_b":["d"]}`
	const otherIDs = `{"production_a":["a2"],"production_b":["b"],"judge_a":["c"],"judge_b":["d"]}`
	evs := []database.AuthorLLMEvalEvidence{ // newest first
		evidenceOf(t, 6, true, ids), // an empty stage
		evidenceOf(t, 5, false, ids, llmeval.StratumDecision{Stratum: "particle.cyrl"}), // gate failed
		evidenceOf(t, 4, true, ids, passed("single_field.cyrl"), llmeval.StratumDecision{Stratum: "mononym.cyrl"}),
		evidenceOf(t, 3, true, ids, passed("particle.cyrl"), passed("homoglyph")),
		evidenceOf(t, 2, true, otherIDs, passed("affix.cyrl")),
		evidenceOf(t, 1, true, ids, passed("initials.cyrl"), passed("mononym.cyrl"), passed("swap.cyrl")),
	}
	plan, primary, err := PlanAuthorLLMResolveFrom(evs)
	if err != nil {
		t.Fatal(err)
	}
	if primary == nil || primary.ReportID != 6 || plan.ReportID != 6 {
		t.Fatalf("primary = %+v, want the newest registrable report 6", primary)
	}
	want := map[string]int64{"single_field.cyrl": 4, "particle.cyrl": 3, "initials.cyrl": 1}
	if len(plan.Strata) != len(want) {
		t.Errorf("planned %v, want %v", plan.Strata, want)
	}
	for name, report := range want {
		st, ok := plan.Strata[name]
		if !ok || st.ReportID != report || st.ReportSHA256 != hex.EncodeToString([]byte{byte(report)}) {
			t.Errorf("%s planned %+v (%v), want report %d", name, st, ok, report)
		}
	}
	if _, ok := plan.Strata["mononym.cyrl"]; ok {
		t.Error("an older pass of mononym.cyrl outranks the newer failed measurement")
	}
	if !slices.Equal(plan.Stale, []string{"affix.cyrl"}) {
		t.Errorf("stale = %v, want affix.cyrl (other model ids)", plan.Stale)
	}
	if !slices.Equal(plan.NotRegistrable, []string{"homoglyph"}) {
		t.Errorf("not registrable = %v", plan.NotRegistrable)
	}
	if len(plan.Sources) != len(evs) || plan.Sources[0].ReportID != 6 {
		t.Errorf("sources = %v", plan.Sources)
	}

	// Without a registrable report there is no evidence at all.
	if _, primary, err = PlanAuthorLLMResolveFrom(evs[1:2]); err != nil || primary != nil {
		t.Errorf("a gate-failed report alone is evidence: %v, %v", primary, err)
	}
}

// setReference sets an evidence's participants' fingerprint reference: the
// same for all four, with the given prompt tokens, reasoning floor and mode.
func setReference(t *testing.T, ev *database.AuthorLLMEvalEvidence, prompt, floor int64, mode string) {
	t.Helper()
	ref := map[string]database.AuthorLLMFingerprintReference{}
	for _, slot := range []string{"production_a", "production_b", "judge_a", "judge_b"} {
		ref[slot] = database.AuthorLLMFingerprintReference{PromptTokens: prompt, ReasoningFloor: floor, OutputMode: mode}
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	ev.Reference = raw
}

// The same model ids are not the same participants when the fingerprint
// reference moved: an older report whose fixed-request prompt tokens,
// reasoning floor or output mode differ from the primary's measured what the
// worker's fingerprint check now refuses as drift, so its strata are stale.
func TestPlanAuthorLLMResolveFromRefusesADriftedReference(t *testing.T) {
	const ids = `{"production_a":["a"],"production_b":["b"],"judge_a":["c"],"judge_b":["d"]}`
	cases := []struct {
		name          string
		prompt, floor int64
		mode          string
		stale         bool
	}{
		{"the same reference", 1126, 0, "json_schema", false},
		{"prompt tokens 1126 → 1200", 1126, 0, "json_schema", true},
		{"another output mode", 1200, 0, "tool", true},
		{"another reasoning floor", 1200, 64, "json_schema", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			primaryPrompt := int64(1126)
			if c.stale {
				primaryPrompt = 1200
			}
			evs := []database.AuthorLLMEvalEvidence{
				evidenceOf(t, 2, true, ids, passed("particle.cyrl")),
				evidenceOf(t, 1, true, ids, passed("initials.cyrl")),
			}
			setReference(t, &evs[0], primaryPrompt, 0, "json_schema")
			setReference(t, &evs[1], c.prompt, c.floor, c.mode)
			plan, primary, err := PlanAuthorLLMResolveFrom(evs)
			if err != nil {
				t.Fatal(err)
			}
			if primary == nil || primary.ReportID != 2 {
				t.Fatalf("primary = %+v", primary)
			}
			_, planned := plan.Strata["initials.cyrl"]
			if planned == c.stale || slices.Contains(plan.Stale, "initials.cyrl") != c.stale {
				t.Errorf("initials.cyrl planned %v, stale %v; want stale %v", planned, plan.Stale, c.stale)
			}
			if _, ok := plan.Strata["particle.cyrl"]; !ok {
				t.Error("the primary's own stratum is not planned")
			}
		})
	}
}
