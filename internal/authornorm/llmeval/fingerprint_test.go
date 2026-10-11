package llmeval

import (
	"slices"
	"testing"
)

func measured(p string, prompt int64, model string, reasoning int64) []FingerprintMeasurement {
	out := make([]FingerprintMeasurement, 3)
	for i := range out {
		out[i] = FingerprintMeasurement{Participant: p, Answered: true, Model: model, PromptTokens: prompt, Reasoning: reasoning}
	}
	return out
}

func healthyMeasurements() []FingerprintMeasurement {
	var ms []FingerprintMeasurement
	ms = append(ms, measured("production_a", 700, "gpt-6-luna", 0)...)
	ms = append(ms, measured("production_b", 800, "deepseek-v4-pro-0813", 0)...)
	ms = append(ms, measured("judge_a", 700, "gpt-6-sol", 400)...)
	ms = append(ms, measured("judge_b", 900, "claude-opus-5", 0)...)
	return ms
}

func TestFingerprintLearnsIDsAndReferences(t *testing.T) {
	ms := healthyMeasurements()
	ms[4].Model = "deepseek-v4-pro-0901" // a second snapshot id answered once
	ms[7].Reasoning = 300
	got, res := EvaluateFingerprint(ms, map[string]bool{"judge_a": true})
	if !res.OK {
		t.Fatalf("healthy measurements failed: %+v", res)
	}
	by := map[string]ParticipantFingerprint{}
	for _, p := range got {
		by[p.Participant] = p
	}
	if !slices.Equal(by["production_b"].Models, []string{"deepseek-v4-pro-0813", "deepseek-v4-pro-0901"}) {
		t.Errorf("learned ids = %v", by["production_b"].Models)
	}
	if by["production_a"].PromptTokens != 700 || by["judge_b"].PromptTokens != 900 {
		t.Errorf("references = %+v", by)
	}
	if by["judge_a"].ReasoningFloor != 150 || by["production_a"].ReasoningFloor != 0 {
		t.Errorf("floors = %d/%d, want half the least reasoning for the reasoning participant only",
			by["judge_a"].ReasoningFloor, by["production_a"].ReasoningFloor)
	}
}

func TestFingerprintFailures(t *testing.T) {
	cases := map[string]func([]FingerprintMeasurement) []FingerprintMeasurement{
		"unstable": func(ms []FingerprintMeasurement) []FingerprintMeasurement { ms[1].PromptTokens = 701; return ms },
		"unanswered": func(ms []FingerprintMeasurement) []FingerprintMeasurement {
			ms[10].Answered = false
			return ms
		},
		"missing participant": func(ms []FingerprintMeasurement) []FingerprintMeasurement { return ms[:9] },
		"production pair from one lab": func(ms []FingerprintMeasurement) []FingerprintMeasurement {
			for i := 3; i < 6; i++ {
				ms[i].PromptTokens = 700
			}
			return ms
		},
		"second judge from a production lab": func(ms []FingerprintMeasurement) []FingerprintMeasurement {
			for i := 9; i < 12; i++ {
				ms[i].PromptTokens = 800
			}
			return ms
		},
	}
	for name, mutate := range cases {
		_, res := EvaluateFingerprint(mutate(healthyMeasurements()), nil)
		if res.OK || res.Reason == "" {
			t.Errorf("%s: %+v", name, res)
		}
	}
}
