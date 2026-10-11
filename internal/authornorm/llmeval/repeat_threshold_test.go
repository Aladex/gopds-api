package llmeval

import (
	"slices"
	"strings"
	"testing"
)

// The repeat check guards against a broken or swapped participant, not
// against honest ambiguity: resolve applies only what the pair and both
// judges agree on, so an unstable answer costs coverage, not correctness.
// Run 5 (initials.cyrl) changed 4, 2, 3 and 4 answers of 200 on ambiguous
// names and must pass; more than 10% changed still stops the run.
func TestRepeatToleratesHonestAmbiguity(t *testing.T) {
	cases := []struct {
		name    string
		matched [4]int
		pass    bool
	}{
		{"run 5", [4]int{196, 198, 197, 196}, true},
		{"6% changed", [4]int{188, 200, 200, 200}, true},
		{"10% changed", [4]int{200, 180, 200, 200}, true},
		{"11% changed", [4]int{200, 200, 178, 200}, false},
		{"a broken participant", [4]int{200, 200, 200, 120}, false},
	}
	for _, c := range cases {
		in := healthy()
		in.Items = stratumItems("initials.cyrl", 700, agreeX)
		for i := range in.Repeat {
			in.Repeat[i].Matched = c.matched[i]
		}
		r := Evaluate(&in)
		repeatReason := slices.ContainsFunc(r.Decision.Reasons, func(s string) bool { return strings.HasPrefix(s, "repeat:") })
		if c.pass && (repeatReason || !r.Decision.Registrable || !decisionOf(t, &r, "initials.cyrl").Passed) {
			t.Errorf("%s: failed: %+v", c.name, r.Decision)
		}
		if !c.pass && (!repeatReason || r.Decision.Registrable) {
			t.Errorf("%s: passed: %+v", c.name, r.Decision)
		}
	}
}

// The report carries each participant's changed share so it can be watched.
func TestReportShowsTheRepeatChangeShare(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("initials.cyrl", 700, agreeX)
	for i, m := range []int{196, 198, 197, 196} {
		in.Repeat[i].Matched = m
	}
	r := Evaluate(&in)
	want := []RepeatChange{
		{Participant: "production_a", Changed: 4, Total: 200, Share: 0.02},
		{Participant: "production_b", Changed: 2, Total: 200, Share: 0.01},
		{Participant: "judge_a", Changed: 3, Total: 200, Share: 0.015},
		{Participant: "judge_b", Changed: 4, Total: 200, Share: 0.02},
	}
	if !slices.Equal(r.RepeatChange, want) {
		t.Errorf("repeat change = %+v, want %+v", r.RepeatChange, want)
	}
}
