package llmeval

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestClopperPearsonBoundaries(t *testing.T) {
	// The smallest accepted set that passes the 0.5% bound with k errors.
	// The design's 949 and 1 259 were rounded up; the exact one-sided 95%
	// bounds are 947 and 1 258, and the rounded values pass as well.
	for _, tc := range []struct{ errors, fails, passes int }{
		{0, 597, 598},
		{1, 946, 947},
		{2, 1257, 1258},
	} {
		if PassesBound(tc.errors, tc.fails) {
			t.Errorf("%d errors in %d passed", tc.errors, tc.fails)
		}
		if !PassesBound(tc.errors, tc.passes) {
			t.Errorf("%d errors in %d failed (upper %.6f)", tc.errors, tc.passes, ClopperPearsonUpper(tc.errors, tc.passes))
		}
	}
	for _, tc := range []struct{ errors, n int }{{1, 949}, {2, 1259}} {
		if !PassesBound(tc.errors, tc.n) {
			t.Errorf("the design's %d errors in %d failed", tc.errors, tc.n)
		}
	}
	if PassesBound(0, 0) {
		t.Error("an empty set passed")
	}
	if PassesBound(3, 1000) {
		t.Error("3 errors in 1000 passed")
	}
}

func TestClopperPearsonMonotone(t *testing.T) {
	prev := 1.0
	for n := 1; n < 2000; n += 37 {
		u := ClopperPearsonUpper(0, n)
		if u > prev {
			t.Fatalf("upper bound grew with n at %d", n)
		}
		prev = u
	}
	if u := ClopperPearsonUpper(5, 5); u != 1 {
		t.Errorf("all errors: upper %v, want 1", u)
	}
}

// answer is a valid, non-refused answer with a canonical form and tier.
func answer(canonical, tier string) Answer {
	return Answer{Valid: true, Canonical: canonical, Tier: tier}
}

var (
	both1 = Combo{Arm: ArmA, Batch: 1}
	ctx1  = Combo{Arm: ArmB, Batch: 1}
	both  = []Combo{both1, ctx1, {Arm: ArmA, Batch: 10}, {Arm: ArmB, Batch: 10}}
)

// stratumItems builds n holdout items of a stratum where both judges agree
// on "x" and the production pair answers per combo with the given function.
func stratumItems(stratum string, n int, prod func(i int, c Combo) [2]Answer) []Item {
	items := make([]Item, 0, n)
	for i := range n {
		it := Item{ID: int64(i + 1), Stratum: stratum, Weight: 1, Holdout: true, HasContext: true,
			Judges:     [2]Answer{answer("x", "confirm"), answer("x", "confirm")},
			Candidates: map[Combo][2]Answer{}}
		for _, c := range both {
			it.Candidates[c] = prod(i, c)
		}
		items = append(items, it)
	}
	return items
}

func agreeX(int, Combo) [2]Answer { return [2]Answer{answer("x", "confirm"), answer("x", "confirm")} }

func healthy() Input {
	return Input{
		Combos:      both,
		Fingerprint: FingerprintResult{OK: true},
		Anchor: []ParticipantCount{
			{Participant: "production_a", Matched: 300, Total: 300}, {Participant: "production_b", Matched: 300, Total: 300},
			{Participant: "judge_a", Matched: 300, Total: 300}, {Participant: "judge_b", Matched: 300, Total: 300},
		},
		Repeat: []ParticipantCount{
			{Participant: "production_a", Matched: 200, Total: 200}, {Participant: "production_b", Matched: 200, Total: 200},
			{Participant: "judge_a", Matched: 200, Total: 200}, {Participant: "judge_b", Matched: 200, Total: 200},
		},
		Registrable: true,
	}
}

func decisionOf(t *testing.T, r *Report, stratum string) StratumDecision {
	t.Helper()
	for _, d := range r.Decision.Strata {
		if d.Stratum == stratum {
			return d
		}
	}
	t.Fatalf("no decision for %s", stratum)
	return StratumDecision{}
}

func TestGateCountsErrorsAndContested(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 700, agreeX)
	r := Evaluate(&in)
	d := decisionOf(t, &r, "initials.cyrl")
	if !d.Passed || d.Arm != ArmA {
		t.Fatalf("a clean 700-item stratum: %+v", d)
	}

	// One contested gold under an accepted answer is an error.
	in.Items[3].Judges[1] = answer("y", "confirm")
	r = Evaluate(&in)
	if d = decisionOf(t, &r, "initials.cyrl"); d.Passed {
		t.Fatalf("one error in 700 passed: %+v", d)
	}
	m := r.Strata["initials.cyrl"].Combos[comboKey(both1)]
	if m.Accepted != 700 || m.Errors != 1 || m.Contested != 1 {
		t.Errorf("metrics = %+v", m)
	}

	// A judge refusal also leaves the gold contested.
	in.Items[3].Judges[1] = Answer{Valid: true, Refused: true, Canonical: "r"}
	r = Evaluate(&in)
	if m = r.Strata["initials.cyrl"].Combos[comboKey(both1)]; m.Errors != 1 {
		t.Errorf("a refusing judge did not make the item contested: %+v", m)
	}
}

func TestGateWeightedShare(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 1300, func(i int, c Combo) [2]Answer {
		if i == 0 {
			return [2]Answer{answer("z", "confirm"), answer("z", "confirm")}
		}
		return agreeX(i, c)
	})
	// One error among 1 300 passes the count bound — unless that one
	// fingerprint carries most of the credits.
	r := Evaluate(&in)
	if !decisionOf(t, &r, "initials.cyrl").Passed {
		t.Fatal("1 error in 1 300 failed")
	}
	in.Items[0].Weight = 100
	r = Evaluate(&in)
	if d := decisionOf(t, &r, "initials.cyrl"); d.Passed {
		t.Fatalf("a weighted error share of 100/1399 passed: %+v", d)
	}
}

func TestRestructureIsDroppedOnItsFirstError(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("single_field.cyrl", 800, func(i int, c Combo) [2]Answer {
		switch {
		case i == 0:
			return [2]Answer{answer("z", "restructure"), answer("z", "restructure")}
		case i < 100:
			return [2]Answer{answer("x", "restructure"), answer("x", "restructure")}
		}
		return agreeX(i, c)
	})
	r := Evaluate(&in)
	d := decisionOf(t, &r, "single_field.cyrl")
	if !d.Passed || slices.Contains(d.Tiers, "restructure") || !slices.Contains(d.Tiers, "confirm") {
		t.Fatalf("restructure kept or the confirm tier lost: %+v", d)
	}
}

func TestAblationTiesGoToArmA(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 700, agreeX)
	r := Evaluate(&in)
	if d := decisionOf(t, &r, "initials.cyrl"); d.Arm != ArmA {
		t.Fatalf("equal arms chose %s", d.Arm)
	}
}

func TestAblationNeedsMoreAcceptedAndNoDeltaError(t *testing.T) {
	// Arm A refuses the first 100 items; with the excerpt the pair agrees.
	gain := func(i int, c Combo) [2]Answer {
		if i < 100 && c.Arm == ArmA {
			return [2]Answer{{Valid: true, Refused: true, Canonical: "r"}, answer("x", "confirm")}
		}
		return agreeX(i, c)
	}
	in := healthy()
	in.Items = stratumItems("single_field.cyrl", 800, gain)
	r := Evaluate(&in)
	d := decisionOf(t, &r, "single_field.cyrl")
	if d.Arm != ArmB {
		t.Fatalf("100 more accepted with no error did not choose the excerpt: %+v", d)
	}

	// Too small a gain: 20 more accepted is under max(30, 5%).
	small := func(i int, c Combo) [2]Answer {
		if i < 20 && c.Arm == ArmA {
			return [2]Answer{{Valid: true, Refused: true, Canonical: "r"}, answer("x", "confirm")}
		}
		return agreeX(i, c)
	}
	in.Items = stratumItems("single_field.cyrl", 800, small)
	r = Evaluate(&in)
	if d = decisionOf(t, &r, "single_field.cyrl"); d.Arm != ArmA {
		t.Fatalf("a gain of 20 chose the excerpt: %+v", d)
	}

	// A big gain, but one item where the excerpt changed the agreed answer
	// to a wrong one (the other person's name pulled in): Δ has an error.
	changed := func(i int, c Combo) [2]Answer {
		if i == 500 && c.Arm == ArmB {
			return [2]Answer{answer("other", "restructure"), answer("other", "restructure")}
		}
		return gain(i, c)
	}
	in.Items = stratumItems("single_field.cyrl", 2000, changed)
	r = Evaluate(&in)
	d = decisionOf(t, &r, "single_field.cyrl")
	if d.Arm == ArmB {
		t.Fatalf("an error on Δ still chose the excerpt: %+v", d)
	}
	// Δ holds every item whose agreed outcome the excerpt changed: the 100
	// it newly made agreed, and item 501 whose agreed answer it replaced.
	// Δ alone decides: arm A errs elsewhere on an item the excerpt fixes, so
	// both arms have one error and only the Δ error stops the excerpt.
	balanced := func(i int, c Combo) [2]Answer {
		if i == 600 && c.Arm == ArmA {
			return [2]Answer{answer("z", "confirm"), answer("z", "confirm")}
		}
		return changed(i, c)
	}
	in.Items = stratumItems("single_field.cyrl", 2000, balanced)
	r = Evaluate(&in)
	if m := r.Strata["single_field.cyrl"].Combos; m["A1"].Errors != m["B1"].Errors {
		t.Fatalf("errors A/B = %d/%d, want equal", m["A1"].Errors, m["B1"].Errors)
	}
	if d = decisionOf(t, &r, "single_field.cyrl"); d.Arm == ArmB {
		t.Fatalf("an error on Δ chose the excerpt at equal errors: %+v", d)
	}
	in.Items = stratumItems("single_field.cyrl", 2000, changed)
	r = Evaluate(&in)

	ab := r.Strata["single_field.cyrl"].Ablation[1]
	if len(ab.Delta) != 101 || !slices.Contains(ab.Delta, 501) || ab.DeltaErrors != 1 {
		t.Errorf("Δ has %d items (501 in it: %v), errors %d; want 101 with one error",
			len(ab.Delta), slices.Contains(ab.Delta, 501), ab.DeltaErrors)
	}
}

func TestItemsWithoutExcerptAnswerTheSameInBothArms(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("mononym.latn", 700, agreeX)
	for i := range in.Items {
		in.Items[i].HasContext = false
		delete(in.Items[i].Candidates, ctx1)
		delete(in.Items[i].Candidates, Combo{Arm: ArmB, Batch: 10})
	}
	r := Evaluate(&in)
	m := r.Strata["mononym.latn"]
	if m.Combos[comboKey(ctx1)].Accepted != 700 {
		t.Fatalf("arm B without excerpts did not take arm A's answers: %+v", m.Combos[comboKey(ctx1)])
	}
}

func TestBatchOfTenOnlyWhenNoWorse(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 1300, agreeX)
	r := Evaluate(&in)
	if d := decisionOf(t, &r, "initials.cyrl"); d.ItemsPerRequest != 10 {
		t.Fatalf("equal batch sizes chose %d; ten is cheaper", d.ItemsPerRequest)
	}
	// One error with batching that single requests do not make: one item per
	// request.
	in.Items = stratumItems("initials.cyrl", 1300, func(i int, c Combo) [2]Answer {
		if i == 7 && c.Batch == 10 {
			return [2]Answer{answer("z", "confirm"), answer("z", "confirm")}
		}
		return agreeX(i, c)
	})
	r = Evaluate(&in)
	d := decisionOf(t, &r, "initials.cyrl")
	if !d.Passed || d.ItemsPerRequest != 1 {
		t.Fatalf("batching with more errors was chosen: %+v", d)
	}
	cmp := r.Strata["initials.cyrl"]
	if cmp.Combos[comboKey(Combo{Arm: ArmA, Batch: 10})].Errors != 1 || cmp.Combos[comboKey(both1)].Errors != 0 {
		t.Errorf("batch comparison not reported: %+v", cmp.Combos)
	}
}

func TestAgreementIsReportedPerCombo(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 100, func(i int, c Combo) [2]Answer {
		if i%4 == 0 && c.Batch == 1 {
			return [2]Answer{answer("x", "confirm"), answer("y", "confirm")}
		}
		return agreeX(i, c)
	})
	r := Evaluate(&in)
	one := r.Strata["initials.cyrl"].Combos[comboKey(both1)]
	ten := r.Strata["initials.cyrl"].Combos[comboKey(Combo{Arm: ArmA, Batch: 10})]
	if one.Agreed != 75 || ten.Agreed != 100 || one.BothValid != 100 {
		t.Errorf("agreement 1 = %d/%d, 10 = %d", one.Agreed, one.BothValid, ten.Agreed)
	}
}

func TestGlobalFailuresStopEveryStratum(t *testing.T) {
	cases := map[string]func(in *Input){
		"anchor judge 98%": func(in *Input) { in.Anchor[2].Matched = 294 },
		"anchor production 97%": func(in *Input) {
			in.Anchor[0].Matched = 291
		},
		"repeat over 1%":      func(in *Input) { in.Repeat[3].Matched = 197 },
		"fingerprint":         func(in *Input) { in.Fingerprint = FingerprintResult{Reason: "same_lab"} },
		"invalid over 2%":     func(in *Input) { in.Invalid = []ParticipantCount{{Participant: "judge_b", Matched: 30, Total: 1000}} },
		"dev is not evidence": func(in *Input) { in.Registrable = false },
	}
	for name, mutate := range cases {
		in := healthy()
		in.Items = stratumItems("initials.cyrl", 700, agreeX)
		mutate(&in)
		r := Evaluate(&in)
		if r.Decision.Registrable {
			t.Errorf("%s: registrable", name)
		}
		if name != "dev is not evidence" && len(r.Decision.Reasons) == 0 {
			t.Errorf("%s: no reason given", name)
		}
	}
	// The thresholds themselves: 99% of judges and 98% of production pass.
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 700, agreeX)
	in.Anchor[2].Matched, in.Anchor[0].Matched = 297, 294
	in.Repeat[0].Matched = 198
	in.Invalid = []ParticipantCount{{Participant: "judge_b", Matched: 20, Total: 1000}}
	r := Evaluate(&in)
	if !r.Decision.Registrable || !decisionOf(t, &r, "initials.cyrl").Passed {
		t.Errorf("thresholds met exactly did not pass: %+v", r.Decision)
	}
}

func TestDevItemsDoNotCount(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 700, agreeX)
	for i := range 200 {
		in.Items[i].Holdout = false
	}
	r := Evaluate(&in)
	if d := decisionOf(t, &r, "initials.cyrl"); d.Passed {
		t.Fatalf("500 holdout items passed: %+v", d)
	}
	if got := r.Strata["initials.cyrl"].Holdout; got != 500 {
		t.Errorf("holdout = %d", got)
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	in := healthy()
	for _, s := range []string{"b.cyrl", "a.latn", "c.cyrl"} {
		in.Items = append(in.Items, stratumItems(s, 50, agreeX)...)
	}
	a, b := Evaluate(&in), Evaluate(&in)
	if fmt.Sprint(a.Decision) != fmt.Sprint(b.Decision) {
		t.Fatal("two evaluations differ")
	}
	var order []string
	for _, d := range a.Decision.Strata {
		order = append(order, d.Stratum)
	}
	if !slices.IsSorted(order) {
		t.Errorf("strata not sorted: %v", order)
	}
}

// B1: the anchor percentage on an undersized set is not evidence.
func TestThinAnchorEvidenceIsNotRegistrable(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 700, agreeX)
	for i := range in.Anchor {
		in.Anchor[i].Matched, in.Anchor[i].Total = 1, 1
	}
	r := Evaluate(&in)
	if r.Decision.Registrable || decisionOf(t, &r, "initials.cyrl").Passed {
		t.Fatalf("1/1 anchors passed: %+v", r.Decision)
	}
	if !slices.ContainsFunc(r.Decision.Reasons, func(s string) bool { return strings.Contains(s, "anchor evidence") }) {
		t.Errorf("reasons %v do not name the missing evidence", r.Decision.Reasons)
	}
	in.Anchor = in.Anchor[:3]
	for i := range in.Anchor {
		in.Anchor[i].Matched, in.Anchor[i].Total = MinAnchorItems, MinAnchorItems
	}
	if r = Evaluate(&in); r.Decision.Registrable {
		t.Fatal("a participant without anchor answers passed")
	}
}

// B2: missing reproducibility evidence never passes.
func TestMissingRepeatEvidenceIsNotRegistrable(t *testing.T) {
	cases := map[string]func(in *Input){
		"no repeats":          func(in *Input) { in.Repeat = nil },
		"a participant gone":  func(in *Input) { in.Repeat = in.Repeat[1:] },
		"fewer than required": func(in *Input) { in.Repeat[2].Matched, in.Repeat[2].Total = 150, 150 },
	}
	for name, mutate := range cases {
		in := healthy()
		in.Items = stratumItems("initials.cyrl", 700, agreeX)
		mutate(&in)
		r := Evaluate(&in)
		if r.Decision.Registrable || decisionOf(t, &r, "initials.cyrl").Passed {
			t.Errorf("%s: passed", name)
		}
		if !slices.ContainsFunc(r.Decision.Reasons, func(s string) bool { return strings.Contains(s, "repeat") }) {
			t.Errorf("%s: reasons %v do not name the repeat", name, r.Decision.Reasons)
		}
	}
}

// N1: the ablation compares what each arm would actually accept: the tiers
// its gate allows, not the raw totals.
func TestAblationComparesTheAllowedTiers(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("single_field.cyrl", 1000, func(i int, c Combo) [2]Answer {
		switch {
		case i == 700:
			// The same restructure error in both arms: both drop restructure.
			return [2]Answer{answer("z", "restructure"), answer("z", "restructure")}
		case c.Arm == ArmA && i < 700:
			return [2]Answer{answer("x", "confirm"), answer("x", "confirm")}
		case c.Arm == ArmA:
			return [2]Answer{{Valid: true, Refused: true, Canonical: "r"}, answer("x", "confirm")}
		case i < 598:
			return [2]Answer{answer("x", "confirm"), answer("x", "confirm")}
		}
		return [2]Answer{answer("x", "restructure"), answer("x", "restructure")}
	})
	r := Evaluate(&in)
	d := decisionOf(t, &r, "single_field.cyrl")
	if !d.Passed || d.Arm != ArmA {
		t.Fatalf("the excerpt was chosen for fewer deployable items: %+v (ablation %+v)", d, r.Strata["single_field.cyrl"].Ablation[1])
	}
}

// The batch choice compares what each option would accept: an error in a
// tier the option drops does not make a larger batch look worse.
func TestBatchChoiceComparesTheAllowedErrors(t *testing.T) {
	excluded := func(i int, c Combo) bool { return i == 0 && c.Batch == 10 }
	for name, prod := range map[string]func(int, Combo) [2]Answer{
		// Both arms answer alike: no Δ, arm A is chosen.
		"arm A": func(i int, c Combo) [2]Answer {
			if excluded(i, c) {
				return [2]Answer{answer("z", "restructure"), answer("z", "restructure")}
			}
			return [2]Answer{answer("x", "confirm"), answer("x", "confirm")}
		},
		// Only the excerpt makes the pair agree: arm B is chosen; the one
		// restructure error is the same in both arms, so it is not in Δ.
		"arm B": func(i int, c Combo) [2]Answer {
			switch {
			case excluded(i, c):
				return [2]Answer{answer("z", "restructure"), answer("z", "restructure")}
			case c.Arm == ArmA:
				return [2]Answer{{Valid: true, Refused: true, Canonical: "r"}, answer("x", "confirm")}
			}
			return [2]Answer{answer("x", "confirm"), answer("x", "confirm")}
		},
	} {
		in := healthy()
		in.Items = stratumItems("initials.cyrl", 700, prod)
		r := Evaluate(&in)
		d := decisionOf(t, &r, "initials.cyrl")
		want := ArmA
		if name == "arm B" {
			want = ArmB
		}
		if !d.Passed || d.Arm != want || d.ItemsPerRequest != 10 || slices.Contains(d.Tiers, "restructure") {
			t.Fatalf("%s: an error in a dropped tier made batch 10 look worse: %+v", name, d)
		}
	}
}
