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
		fp, reason := learnParticipant(p, by[p], reasoning[p])
		if reason != "" {
			return out, FingerprintResult{Reason: reason}
		}
		prompt[p] = fp.PromptTokens
		out = append(out, fp)
	}
	if reason := labsReason(prompt); reason != "" {
		return out, FingerprintResult{Reason: reason}
	}
	return out, FingerprintResult{OK: true}
}

// learnParticipant applies the stage's rule to one participant's repeats:
// all answered, one prompt token count, the model ids named, and for a
// reasoning participant a floor of half its least reasoning. reason says why
// it does not hold.
func learnParticipant(p string, got []FingerprintMeasurement, reasoning bool) (fp ParticipantFingerprint, reason string) {
	if len(got) == 0 {
		return fp, "unanswered: " + p
	}
	fp = ParticipantFingerprint{Participant: p, PromptTokens: got[0].PromptTokens}
	least := int64(-1)
	for _, m := range got {
		if !m.Answered {
			return fp, "unanswered: " + p
		}
		if m.PromptTokens != fp.PromptTokens {
			return fp, fmt.Sprintf("unstable: %s prompt tokens %d and %d", p, fp.PromptTokens, m.PromptTokens)
		}
		if !slices.Contains(fp.Models, m.Model) {
			fp.Models = append(fp.Models, m.Model)
		}
		if least < 0 || m.Reasoning < least {
			least = m.Reasoning
		}
	}
	slices.Sort(fp.Models)
	if reasoning {
		fp.ReasoningFloor = max(least/reasoningFloorDivisor, 1)
	}
	return fp, ""
}

// The reasons the labs are not told apart.
const (
	reasonSameLabPair  = "same_lab: production_a and production_b"
	reasonSameLabJudge = "same_lab: judge_b and a production participant"
)

// labsReason checks that the prompt token counts still tell the labs apart:
// the production pair differs, and the second judge differs from both.
func labsReason(prompt map[string]int64) string {
	switch {
	case prompt[slotProductionA] == prompt[slotProductionB]:
		return reasonSameLabPair
	case prompt[slotJudgeB] == prompt[slotProductionA] || prompt[slotJudgeB] == prompt[slotProductionB]:
		return reasonSameLabJudge
	}
	return ""
}

// RelearnFingerprint re-learns one drifted participant mid-run under the
// stage's rule: its repeats of the fixed request (ms) must agree, and with
// the others' current prompt token counts the labs must still be told apart.
// reason says why it does not settle.
func RelearnFingerprint(
	participant string, ms []FingerprintMeasurement, reasoning bool, others map[string]int64,
) (fp ParticipantFingerprint, reason string) {
	fp, reason = learnParticipant(participant, ms, reasoning)
	if reason != "" {
		return fp, reason
	}
	// Only the pairs the participant is in: the others' references were
	// checked when they were learned.
	n := fp.PromptTokens
	switch participant {
	case slotProductionA, slotProductionB:
		other := slotProductionB
		if participant == slotProductionB {
			other = slotProductionA
		}
		if v, ok := others[other]; ok && v == n {
			return fp, reasonSameLabPair
		}
		if v, ok := others[slotJudgeB]; ok && v == n {
			return fp, reasonSameLabJudge
		}
	case slotJudgeB:
		for _, p := range []string{slotProductionA, slotProductionB} {
			if v, ok := others[p]; ok && v == n {
				return fp, reasonSameLabJudge
			}
		}
	}
	return fp, ""
}
