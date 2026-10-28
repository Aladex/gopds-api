package llmeval

import (
	"strings"
	"testing"
)

// suiteInput is a healthy merge stratum: 3,000 accepted holdout items with
// gold, and four accepted suite items labeled as given.
func suiteInput(labels ...string) Input {
	in := healthy()
	same := Answer{Valid: true, Canonical: "same", Tier: "merge"}
	both := [2]Answer{same, same}
	for i := range 3000 {
		it := Item{ID: int64(i + 1), Stratum: "swap", Script: "cyrl", Weight: 1, Holdout: true,
			Judges: both, Candidates: map[Combo][2]Answer{{Arm: ArmA, Batch: 1}: both}}
		if i < MinPooledRepeatItems {
			it.Repeated = map[string]bool{}
			for _, p := range participants {
				it.Repeated[p] = true
			}
		}
		in.Items = append(in.Items, it)
	}
	for i, l := range labels {
		in.Items = append(in.Items, Item{ID: int64(10000 + i), Stratum: "swap", Script: "cyrl", Weight: 1,
			Holdout: true, Adversarial: true, Adjudication: l, Judges: both,
			Candidates: map[Combo][2]Answer{{Arm: ArmA, Batch: 1}: both}})
	}
	in.Combos = []Combo{{Arm: ArmA, Batch: 1}}
	return in
}

func evaluateSuite(t *testing.T, labels ...string) (StratumDecision, *AdversarialMetrics) {
	t.Helper()
	in := suiteInput(labels...)
	o := Options{MaxErrorShare: 0.001, ContestedIsError: true, AcceptOnly: "same", AnchorRecall: true, ByScript: true}
	r := EvaluateWith(&in, &o)
	if len(r.Decision.Reasons) > 0 {
		t.Fatalf("the fixture run decides nothing: %v", r.Decision.Reasons)
	}
	return r.Decision.Strata[0], r.Strata["swap"].Combos["A1"].Adversarial
}

// An accepted suite item labeled same or unclear does not fail its
// stratum.
func TestSameAndUnclearClearAnAcceptedSuiteItem(t *testing.T) {
	d, a := evaluateSuite(t, AdjudicatedSame, AdjudicatedUnclear, AdjudicatedSame, AdjudicatedUnclear)
	if !d.Passed || a.Accepted != 4 || a.AdjudicatedSame != 2 || a.Unclear != 2 || a.Failed != 0 {
		t.Fatalf("decision %+v, suite %+v", d, a)
	}
}

// An accepted suite item labeled different, or not labeled, fails it;
// the reason names the counts.
func TestDifferentAndPendingFailAnAcceptedSuiteItem(t *testing.T) {
	for _, labels := range [][]string{
		{AdjudicatedSame, AdjudicatedSame, AdjudicatedUnclear, AdjudicatedDifferent},
		{AdjudicatedSame, AdjudicatedSame, AdjudicatedUnclear, ""},
	} {
		d, a := evaluateSuite(t, labels...)
		if d.Passed || a.Failed != 1 {
			t.Fatalf("%v: decision %+v, suite %+v", labels, d, a)
		}
		if !strings.HasPrefix(d.Reason, "adversarial: 4 accepted in A1 (") {
			t.Fatalf("%v: reason %q", labels, d.Reason)
		}
	}
	d, _ := evaluateSuite(t, AdjudicatedDifferent, "", AdjudicatedUnclear, AdjudicatedSame)
	if d.Reason != "adversarial: 4 accepted in A1 (1 different, 1 pending; 1 unclear, 1 same cleared)" {
		t.Fatalf("reason %q", d.Reason)
	}
}
