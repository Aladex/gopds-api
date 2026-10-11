package llmeval

import (
	"fmt"
	"slices"
)

// The participants of a configuration (llmreq's slots), in their fixed order.
const (
	slotProductionA = "production_a"
	slotProductionB = "production_b"
	slotJudgeA      = "judge_a"
	slotJudgeB      = "judge_b"
	// reasoningFloorDivisor: a reasoning participant's floor is this
	// fraction of its least reasoning at the fingerprint stage.
	reasoningFloorDivisor = 2
)

var participants = []string{slotProductionA, slotProductionB, slotJudgeA, slotJudgeB}

// FingerprintMeasurement is one answer to the fixed fingerprint request.
type FingerprintMeasurement struct {
	Participant string
	Answered    bool
	// Model is the response's model id.
	Model        string
	PromptTokens int64
	// Reasoning is the reasoning tokens, or the completion tokens when the
	// endpoint reports none.
	Reasoning int64
}

// ParticipantFingerprint is what the fingerprint stage learned of one
// participant: the model ids V0 accepts from now on and the references every
// later check compares with.
type ParticipantFingerprint struct {
	Participant    string   `json:"participant"`
	Models         []string `json:"models"`
	PromptTokens   int64    `json:"prompt_tokens"`
	ReasoningFloor int64    `json:"reasoning_floor,omitempty"`
}

// EvaluateFingerprint learns the participants' model ids and fingerprint
// references from the repeated fixed request, and checks them. Every
// participant must have answered every repeat with the same prompt token
// count (the tokenizer is deterministic; a different count is a different
// model or an injected prompt). The token counts also tell the labs apart:
// the production pair must differ, and the second judge must differ from
// both. A participant in reasoning gets a floor of half its least reasoning,
// below which a later check fails.
func EvaluateFingerprint(ms []FingerprintMeasurement, reasoning map[string]bool) ([]ParticipantFingerprint, FingerprintResult) {
	by := map[string][]FingerprintMeasurement{}
	for _, m := range ms {
		by[m.Participant] = append(by[m.Participant], m)
	}
	out := make([]ParticipantFingerprint, 0, len(participants))
	prompt := map[string]int64{}
	for _, p := range participants {
		got := by[p]
		if len(got) == 0 {
			return out, FingerprintResult{Reason: "unanswered: " + p}
		}
		fp := ParticipantFingerprint{Participant: p, PromptTokens: got[0].PromptTokens}
		least := int64(-1)
		for _, m := range got {
			if !m.Answered {
				return out, FingerprintResult{Reason: "unanswered: " + p}
			}
			if m.PromptTokens != fp.PromptTokens {
				return out, FingerprintResult{Reason: fmt.Sprintf("unstable: %s prompt tokens %d and %d", p,
					fp.PromptTokens, m.PromptTokens)}
			}
			if !slices.Contains(fp.Models, m.Model) {
				fp.Models = append(fp.Models, m.Model)
			}
			if least < 0 || m.Reasoning < least {
				least = m.Reasoning
			}
		}
		slices.Sort(fp.Models)
		if reasoning[p] {
			fp.ReasoningFloor = max(least/reasoningFloorDivisor, 1)
		}
		prompt[p] = fp.PromptTokens
		out = append(out, fp)
	}
	switch {
	case prompt[slotProductionA] == prompt[slotProductionB]:
		return out, FingerprintResult{Reason: "same_lab: production_a and production_b"}
	case prompt[slotJudgeB] == prompt[slotProductionA] || prompt[slotJudgeB] == prompt[slotProductionB]:
		return out, FingerprintResult{Reason: "same_lab: judge_b and a production participant"}
	}
	return out, FingerprintResult{OK: true}
}
