package llmeval

import "testing"

// The smallest accepted set that passes the 1% bound with k errors, from the
// exact one-sided 95% Clopper–Pearson bound.
func TestErrorShareBoundAtOnePercent(t *testing.T) {
	for _, tc := range []struct{ errors, fails, passes int }{
		{0, 298, 299},
		{1, 472, 473},
		{2, 627, 628},
		{3, 772, 773},
	} {
		if PassesBound(tc.errors, tc.fails) {
			t.Errorf("%d errors in %d passed (upper %.6f)", tc.errors, tc.fails, ClopperPearsonUpper(tc.errors, tc.fails))
		}
		if !PassesBound(tc.errors, tc.passes) {
			t.Errorf("%d errors in %d failed (upper %.6f)", tc.errors, tc.passes, ClopperPearsonUpper(tc.errors, tc.passes))
		}
	}
}

// Pilot run 10: initials.cyrl had 2 errors among 876 accepted items, an upper
// bound of about 0.72%. The stratum passes at 1%.
func TestPilotRun10TwoErrorsIn876Pass(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 876, func(i int, c Combo) [2]Answer {
		if i < 2 {
			return [2]Answer{answer("z", "confirm"), answer("z", "confirm")}
		}
		return agreeX(i, c)
	})
	r := Evaluate(&in)
	d := decisionOf(t, &r, "initials.cyrl")
	if !d.Passed {
		t.Fatalf("2 errors in 876 failed: %+v", d)
	}
	if m := r.Strata["initials.cyrl"].Combos[comboKey(both1)]; m.Accepted != 876 || m.Errors != 2 {
		t.Errorf("metrics = %+v", m)
	}
	// A third error on fewer items is still out: 3 in 700 is above 1%.
	in.Items = stratumItems("initials.cyrl", 700, func(i int, c Combo) [2]Answer {
		if i < 3 {
			return [2]Answer{answer("z", "confirm"), answer("z", "confirm")}
		}
		return agreeX(i, c)
	})
	r = Evaluate(&in)
	if d = decisionOf(t, &r, "initials.cyrl"); d.Passed {
		t.Fatalf("3 errors in 700 passed: %+v", d)
	}
}

// The credit-weighted share uses the same 1%: one error carrying 10 of 1 309
// credits (0.76%) passes, one carrying 14 of 1 313 (1.07%) does not.
func TestWeightedErrorShareAtOnePercent(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 1300, func(i int, c Combo) [2]Answer {
		if i == 0 {
			return [2]Answer{answer("z", "confirm"), answer("z", "confirm")}
		}
		return agreeX(i, c)
	})
	in.Items[0].Weight = 10
	r := Evaluate(&in)
	if d := decisionOf(t, &r, "initials.cyrl"); !d.Passed {
		t.Fatalf("a weighted error share of 10/1309 failed: %+v", d)
	}
	in.Items[0].Weight = 14
	r = Evaluate(&in)
	if d := decisionOf(t, &r, "initials.cyrl"); d.Passed {
		t.Fatalf("a weighted error share of 14/1313 passed: %+v", d)
	}
}
