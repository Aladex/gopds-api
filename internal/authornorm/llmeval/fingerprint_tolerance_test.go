package llmeval

import (
	"strings"
	"testing"
)

func judgeB(prompts ...int64) []FingerprintMeasurement {
	out := make([]FingerprintMeasurement, 0, len(prompts))
	for _, p := range prompts {
		out = append(out, FingerprintMeasurement{Participant: "judge_b", Answered: true, Model: "glm-5.3", PromptTokens: p})
	}
	return out
}

// A re-learn whose repeats jitter by one prompt token (the provider-side
// count of the same model) settles, and the reference it stores is the most
// frequent count, the lower one on a tie.
func TestRelearnToleratesOnePromptToken(t *testing.T) {
	others := map[string]int64{"production_a": 700, "production_b": 800, "judge_a": 700}
	for _, c := range []struct {
		prompts []int64
		want    int64
	}{
		{[]int64{543, 544, 543}, 543},
		{[]int64{544, 543, 544}, 544},
		{[]int64{544, 544, 543}, 544},
		{[]int64{543, 544, 544}, 544},
		{[]int64{544, 543, 543}, 543},
		{[]int64{543, 542, 543}, 543},
		{[]int64{544, 543}, 543},
		{[]int64{543, 543, 543}, 543},
	} {
		fp, reason := RelearnFingerprint("judge_b", judgeB(c.prompts...), false, others)
		if reason != "" {
			t.Errorf("%v: %s", c.prompts, reason)
			continue
		}
		if fp.PromptTokens != c.want {
			t.Errorf("%v: learned %d, want %d", c.prompts, fp.PromptTokens, c.want)
		}
	}
}

// Two tokens apart is not jitter, on either side, and neither is a spread of
// two whose counts are each within one of the middle.
func TestRelearnRefusesTwoPromptTokens(t *testing.T) {
	others := map[string]int64{"production_a": 700, "production_b": 800, "judge_a": 700}
	for _, prompts := range [][]int64{
		{543, 545, 543}, {543, 541, 543}, {545, 543, 543},
		// Each within one of its neighbour, or of the first, but not of all.
		{542, 543, 544}, {543, 542, 544}, {543, 544, 542},
	} {
		if _, reason := RelearnFingerprint("judge_b", judgeB(prompts...), false, others); !strings.HasPrefix(reason, "unstable:") {
			t.Errorf("%v: reason %q, want unstable", prompts, reason)
		}
	}
}

// The fingerprint stage shares the rule: a participant whose repeats differ
// by one is learned, by two is unstable.
func TestFingerprintStageToleratesOnePromptToken(t *testing.T) {
	jitter := healthyMeasurements()
	jitter[10].PromptTokens = 901
	got, res := EvaluateFingerprint(jitter, nil)
	if !res.OK {
		t.Fatalf("a one-token jitter failed the stage: %+v", res)
	}
	for _, fp := range got {
		if fp.Participant == "judge_b" && fp.PromptTokens != 900 {
			t.Errorf("judge_b learned %d, want the most frequent 900", fp.PromptTokens)
		}
	}
	for _, delta := range []int64{-2, 2} {
		two := healthyMeasurements()
		two[10].PromptTokens = 900 + delta
		if _, res = EvaluateFingerprint(two, nil); res.OK || !strings.HasPrefix(res.Reason, "unstable:") {
			t.Errorf("%+d: %+v, want unstable", delta, res)
		}
	}
}

func TestPromptTokensMatch(t *testing.T) {
	for _, c := range []struct {
		a, b int64
		want bool
	}{
		{543, 541, false}, {543, 542, true}, {543, 543, true}, {543, 544, true}, {543, 545, false},
	} {
		if got := PromptTokensMatch(c.a, c.b); got != c.want {
			t.Errorf("PromptTokensMatch(%d, %d) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// withPrompts sets a participant's three repeats of healthyMeasurements to
// the given prompt token counts.
func withPrompts(ms []FingerprintMeasurement, p string, prompts ...int64) []FingerprintMeasurement {
	i := 0
	for j := range ms {
		if ms[j].Participant == p && i < len(prompts) {
			ms[j].PromptTokens = prompts[i]
			i++
		}
	}
	return ms
}

// Two participants of one tokenizer whose jittered majorities differ
// (543, 544, 543 learns 543; 544, 543, 544 learns 544) are one lab, in
// either pair the stage separates.
func TestFingerprintStageSeesOneLabThroughTheJitter(t *testing.T) {
	judge := healthyMeasurements()
	judge = withPrompts(judge, "production_a", 543, 544, 543)
	judge = withPrompts(judge, "judge_a", 543, 543, 543)
	judge = withPrompts(judge, "judge_b", 544, 543, 544)
	if _, res := EvaluateFingerprint(judge, nil); res.OK || res.Reason != reasonSameLabJudge {
		t.Errorf("judge_b in production_a's lab: %+v", res)
	}
	pair := healthyMeasurements()
	pair = withPrompts(pair, "production_a", 543, 544, 543)
	pair = withPrompts(pair, "production_b", 544, 543, 544)
	pair = withPrompts(pair, "judge_a", 543, 543, 543)
	if _, res := EvaluateFingerprint(pair, nil); res.OK || res.Reason != reasonSameLabPair {
		t.Errorf("the production pair in one lab: %+v", res)
	}
}

// Stable references two apart are taken as one lab (two jittered samples
// of one tokenizer can land that far apart); three apart are told apart.
func TestFingerprintStageLabGap(t *testing.T) {
	for _, c := range []struct {
		name         string
		a, b, judgeB int64
		want         string
	}{
		{"production pair two apart", 700, 702, 900, reasonSameLabPair},
		{"production pair two apart, below", 700, 698, 900, reasonSameLabPair},
		{"judge_b two from production_b", 700, 800, 802, reasonSameLabJudge},
		{"judge_b two from production_a", 700, 800, 698, reasonSameLabJudge},
		{"production pair three apart", 700, 703, 900, ""},
		{"judge_b three from both", 700, 706, 703, ""},
	} {
		ms := healthyMeasurements()
		ms = withPrompts(ms, "production_a", c.a, c.a, c.a)
		ms = withPrompts(ms, "production_b", c.b, c.b, c.b)
		ms = withPrompts(ms, "judge_b", c.judgeB, c.judgeB, c.judgeB)
		_, res := EvaluateFingerprint(ms, nil)
		if res.Reason != c.want || res.OK != (c.want == "") {
			t.Errorf("%s: %+v, want reason %q", c.name, res, c.want)
		}
	}
}

// A re-learn checks the same lab gap against the others' stored references:
// judge_b re-learned from 544, 543, 544 beside production_a's 543 is in
// production_a's lab.
func TestRelearnSeesOneLabThroughTheJitter(t *testing.T) {
	others := map[string]int64{"production_a": 543, "production_b": 800, "judge_a": 543}
	if _, reason := RelearnFingerprint("judge_b", judgeB(544, 543, 544), false, others); reason != reasonSameLabJudge {
		t.Errorf("judge_b beside production_a: %q", reason)
	}
	for _, c := range []struct {
		name, participant string
		prompt            int64
		others            map[string]int64
		want              string
	}{
		{"judge_b two from production_b", "judge_b", 802,
			map[string]int64{"production_a": 700, "production_b": 800, "judge_a": 700}, reasonSameLabJudge},
		{"judge_b three from both", "judge_b", 803,
			map[string]int64{"production_a": 700, "production_b": 800, "judge_a": 700}, ""},
		{"production_b two from production_a", "production_b", 702,
			map[string]int64{"production_a": 700, "judge_a": 700, "judge_b": 900}, reasonSameLabPair},
		{"production_b two from judge_b", "production_b", 898,
			map[string]int64{"production_a": 700, "judge_a": 700, "judge_b": 900}, reasonSameLabJudge},
		{"production_a two from production_b", "production_a", 798,
			map[string]int64{"production_b": 800, "judge_a": 700, "judge_b": 900}, reasonSameLabPair},
		{"production_b three from both", "production_b", 703,
			map[string]int64{"production_a": 700, "judge_a": 700, "judge_b": 900}, ""},
		{"judge_a may sit by production_a", "judge_a", 701,
			map[string]int64{"production_a": 700, "production_b": 800, "judge_b": 900}, ""},
	} {
		ms := []FingerprintMeasurement{{Participant: c.participant, Answered: true, Model: "m", PromptTokens: c.prompt}}
		if _, reason := RelearnFingerprint(c.participant, ms, false, c.others); reason != c.want {
			t.Errorf("%s: %q, want %q", c.name, reason, c.want)
		}
	}
}

func TestSameLabPromptTokens(t *testing.T) {
	for _, c := range []struct {
		a, b int64
		want bool
	}{
		{543, 540, false}, {543, 541, true}, {543, 543, true}, {543, 545, true}, {543, 546, false},
	} {
		if got := SameLabPromptTokens(c.a, c.b); got != c.want {
			t.Errorf("SameLabPromptTokens(%d, %d) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
