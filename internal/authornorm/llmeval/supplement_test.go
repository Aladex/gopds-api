package llmeval

import (
	"strings"
	"testing"
)

// mergeOpts are the merge gate's options.
var mergeOpts = Options{MaxErrorShare: 0.001, ContestedIsError: true, AcceptOnly: "same", AnchorRecall: true,
	ByScript: true}

// mergeStratum is a healthy merge stratum of n accepted Cyrillic holdout
// items with gold, and m accepted Latin items gated outside |A|.
func mergeStratum(n, m int) Input {
	in := healthy()
	same := Answer{Valid: true, Canonical: "same", Tier: "merge"}
	both := [2]Answer{same, same}
	for i := range n + m {
		it := Item{ID: int64(i + 1), Stratum: "bucket", Script: "cyrl", Weight: 1, Holdout: true,
			Judges: both, Candidates: map[Combo][2]Answer{{Arm: ArmA, Batch: 1}: both}}
		if i < MinPooledRepeatItems {
			it.Repeated = map[string]bool{}
			for _, p := range participants {
				it.Repeated[p] = true
			}
		}
		if i >= n {
			it.Script, it.Supplement = "latn", true
		}
		in.Items = append(in.Items, it)
	}
	in.Combos = []Combo{{Arm: ArmA, Batch: 1}}
	return in
}

func evaluateMerge(t *testing.T, in *Input) (StratumDecision, *ComboMetrics) {
	t.Helper()
	r := EvaluateWith(in, &mergeOpts)
	if len(r.Decision.Reasons) > 0 {
		t.Fatalf("the fixture run decides nothing: %v", r.Decision.Reasons)
	}
	m := r.Strata["bucket"].Combos["A1"]
	return r.Decision.Strata[0], &m
}

// The extra Latin questions are gated but never in |A|: 2,994 pooled
// accepts and 1,000 Latin ones still fail the bound, 2,995 pass it; the
// per-script breakdown counts them.
func TestSupplementItemsNeverCountTowardTheBound(t *testing.T) {
	d, m := evaluateMerge(t, new(mergeStratum(2994, 1000)))
	if d.Passed || m.Accepted != 2994 || m.Supplement == nil || m.Supplement.Accepted != 1000 {
		t.Fatalf("2,994 + 1,000: decision %+v, accepted %d, supplement %+v", d, m.Accepted, m.Supplement)
	}
	d, m = evaluateMerge(t, new(mergeStratum(2995, 1000)))
	if !d.Passed || m.Accepted != 2995 || m.Supplement.Errors != 0 {
		t.Fatalf("2,995 + 1,000: decision %+v, metrics %+v", d, m)
	}
	if latn := m.Scripts["latn"]; latn.Items != 1000 || latn.Accepted != 1000 {
		t.Fatalf("Latin breakdown %+v", latn)
	}
	if cyrl := m.Scripts["cyrl"]; cyrl.Items != 2995 {
		t.Fatalf("Cyrillic breakdown %+v", cyrl)
	}
}

// One error, or one contested accept, among the extra questions fails the
// stratum, whatever the pooled bound says; the reason names it.
func TestOneSupplementErrorFailsTheStratum(t *testing.T) {
	for name, spoil := range map[string]func(it *Item){
		"error":     func(it *Item) { it.Judges = [2]Answer{answer("different", ""), answer("different", "")} },
		"contested": func(it *Item) { it.Judges[1] = answer("different", "") },
	} {
		in := mergeStratum(3000, 1000)
		spoil(&in.Items[3500])
		d, m := evaluateMerge(t, &in)
		if d.Passed || m.Supplement.Errors != 1 || m.Accepted != 3000 || m.Errors != 0 {
			t.Fatalf("%s: decision %+v, accepted %d errors %d, supplement %+v", name, d, m.Accepted, m.Errors,
				m.Supplement)
		}
		if !strings.HasPrefix(d.Reason, "supplement: 1 errors of 1000 accepted outside |A| in A1") {
			t.Fatalf("%s: reason %q", name, d.Reason)
		}
	}
}

// In merge mode a contested accept is an error already in the accepted
// population: the contested share is of the accepts once, not of the
// accepts plus the contested again.
func TestContestedShareCountsAContestedAcceptOnce(t *testing.T) {
	in := mergeStratum(273, 0)
	for i := range 3 {
		in.Items[i].Judges[1] = answer("different", "")
	}
	_, m := evaluateMerge(t, &in)
	if m.Accepted != 273 || m.Contested != 3 || m.ContestedShare != 3.0/273 {
		t.Fatalf("accepted %d contested %d share %v, want 3/273", m.Accepted, m.Contested, m.ContestedShare)
	}
	// K's gate: a contested item is not accepted, so the share is of both.
	in = mergeStratum(273, 0)
	for i := range in.Items {
		in.Items[i].Stratum = "s"
	}
	for i := range 3 {
		in.Items[i].Judges[1] = answer("different", "")
	}
	r := Evaluate(&in)
	k := r.Strata["s"].Combos["A1"]
	if k.Accepted != 270 || k.Contested != 3 || k.ContestedShare != 3.0/273 {
		t.Fatalf("K: accepted %d contested %d share %v, want 3/273", k.Accepted, k.Contested, k.ContestedShare)
	}
}
