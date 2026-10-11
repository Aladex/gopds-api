package llmeval

import (
	"strings"
	"testing"
)

// A drifted participant's re-learn applies the fingerprint stage's rule to
// that one participant: every repeat answered with one prompt token count,
// the model ids it named, the reasoning floor, and the labs still told apart
// from the others' current counts.
func TestRelearnFingerprintKeepsTheStageRule(t *testing.T) {
	others := map[string]int64{"production_a": 700, "production_b": 800, "judge_a": 700}
	ms := []FingerprintMeasurement{
		{Participant: "judge_b", Answered: true, Model: "claude-opus-5", PromptTokens: 950, Reasoning: 40},
		{Participant: "judge_b", Answered: true, Model: "claude-opus-5-0901", PromptTokens: 950, Reasoning: 30},
		{Participant: "judge_b", Answered: true, Model: "claude-opus-5", PromptTokens: 950, Reasoning: 60},
	}
	fp, reason := RelearnFingerprint("judge_b", ms, true, others)
	if reason != "" {
		t.Fatalf("a stable re-learn failed: %s", reason)
	}
	if fp.PromptTokens != 950 || fp.ReasoningFloor != 15 || strings.Join(fp.Models, ",") != "claude-opus-5,claude-opus-5-0901" {
		t.Errorf("learned %+v", fp)
	}
	if _, reason = RelearnFingerprint("judge_b", ms, false, others); reason != "" {
		t.Errorf("without reasoning: %s", reason)
	}

	unstable := append([]FingerprintMeasurement(nil), ms...)
	unstable[1].PromptTokens = 951
	if _, reason = RelearnFingerprint("judge_b", unstable, false, others); !strings.HasPrefix(reason, "unstable:") {
		t.Errorf("disagreeing calls: %q", reason)
	}
	unanswered := append([]FingerprintMeasurement(nil), ms...)
	unanswered[2].Answered = false
	if _, reason = RelearnFingerprint("judge_b", unanswered, false, others); !strings.HasPrefix(reason, "unanswered:") {
		t.Errorf("an unanswered call: %q", reason)
	}
	if _, reason = RelearnFingerprint("judge_b", nil, false, others); !strings.HasPrefix(reason, "unanswered:") {
		t.Errorf("no calls: %q", reason)
	}
	// The new model is the production pair's lab: the judges no longer
	// tell the labs apart.
	same := append([]FingerprintMeasurement(nil), ms...)
	for i := range same {
		same[i].PromptTokens = 800
	}
	if _, reason = RelearnFingerprint("judge_b", same, false, others); !strings.HasPrefix(reason, "same_lab:") {
		t.Errorf("the production lab: %q", reason)
	}
	pair := []FingerprintMeasurement{{Participant: "production_b", Answered: true, Model: "m", PromptTokens: 700}}
	if _, reason = RelearnFingerprint("production_b", pair, false,
		map[string]int64{"production_a": 700, "judge_a": 700, "judge_b": 900}); !strings.HasPrefix(reason, "same_lab:") {
		t.Errorf("the pair in one lab: %q", reason)
	}
	// A collision among the others is not this participant's: the first
	// judge may share a lab, and two others sharing one were checked when
	// they were learned.
	if _, reason = RelearnFingerprint("judge_b", ms, false,
		map[string]int64{"production_a": 777, "production_b": 777, "judge_a": 777}); reason != "" {
		t.Errorf("the others' counts: %q", reason)
	}
	if _, reason = RelearnFingerprint("judge_a", pair, false, others); reason != "" {
		t.Errorf("judge_a may share a lab: %q", reason)
	}
}
