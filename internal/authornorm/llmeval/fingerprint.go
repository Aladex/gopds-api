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

// PromptTokenTolerance is how far apart two prompt token counts of the fixed
// fingerprint request may be and still be the same model. The tokenizer is
// deterministic, but some providers (glm-5.3 through cli-proxy-api on prod)
// report the same request of the same model one token off now and then; a
// different model or an injected prompt moves the count by far more.
const PromptTokenTolerance = 1

// PromptTokensMatch reports whether two prompt token counts of the fixed
// fingerprint request are the same model's: at most PromptTokenTolerance
// apart.
func PromptTokensMatch(a, b int64) bool {
	d := a - b
	return -PromptTokenTolerance <= d && d <= PromptTokenTolerance
}

// SameLabPromptTokens reports whether two participants' reference counts of
// the fixed fingerprint request may be one tokenizer's, which the lab gate
// takes as one lab. The margin is twice PromptTokenTolerance: each
// reference is one jittered sample of its tokenizer's count, so two
// participants of one tokenizer can learn references up to two apart (543
// from 543, 544, 543 and 544 from 544, 543, 544 when the true count jitters
// between them, or 542 and 544 around 543). Labs whose real counts are that
// close are refused too; a failed lab gate is a safe stop.
func SameLabPromptTokens(a, b int64) bool {
	d := a - b
	return -2*PromptTokenTolerance <= d && d <= 2*PromptTokenTolerance
}

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
// count up to PromptTokenTolerance (the tokenizer is deterministic; a count
// further off is a different model or an injected prompt). The reference is
// the most frequent count. The token counts also tell the labs apart:
// the production pair must differ, and the second judge must differ from
// both, by more than SameLabPromptTokens allows. A participant in reasoning gets a floor of half its least reasoning,
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
// all answered, prompt token counts no further apart than
// PromptTokenTolerance, the model ids named, and for a reasoning participant
// a floor of half its least reasoning. The reference is the most frequent
// count, the lowest on a tie: with the counts this close every one measured
// is within the tolerance of it, and the choice does not hang on the order
// the repeats came in. reason says why it does not hold.
func learnParticipant(p string, got []FingerprintMeasurement, reasoning bool) (fp ParticipantFingerprint, reason string) {
	if len(got) == 0 {
		return fp, "unanswered: " + p
	}
	fp = ParticipantFingerprint{Participant: p}
	lo, hi := got[0].PromptTokens, got[0].PromptTokens
	seen := map[int64]int{}
	least := int64(-1)
	for _, m := range got {
		if !m.Answered {
			return fp, "unanswered: " + p
		}
		lo, hi = min(lo, m.PromptTokens), max(hi, m.PromptTokens)
		if !PromptTokensMatch(lo, hi) {
			return fp, fmt.Sprintf("unstable: %s prompt tokens %d and %d", p, lo, hi)
		}
		seen[m.PromptTokens]++
		if !slices.Contains(fp.Models, m.Model) {
			fp.Models = append(fp.Models, m.Model)
		}
		if least < 0 || m.Reasoning < least {
			least = m.Reasoning
		}
	}
	fp.PromptTokens = mostFrequent(seen)
	slices.Sort(fp.Models)
	if reasoning {
		fp.ReasoningFloor = max(least/reasoningFloorDivisor, 1)
	}
	return fp, ""
}

// mostFrequent is the count seen most often, the lowest on a tie.
func mostFrequent(seen map[int64]int) int64 {
	var best int64
	n := 0
	for v, c := range seen {
		if c > n || c == n && v < best {
			best, n = v, c
		}
	}
	return best
}

// The reasons the labs are not told apart.
const (
	reasonSameLabPair  = "same_lab: production_a and production_b"
	reasonSameLabJudge = "same_lab: judge_b and a production participant"
)

// labsReason checks that the prompt token counts still tell the labs apart:
// the production pair differs, and the second judge differs from both, by
// more than SameLabPromptTokens allows.
func labsReason(prompt map[string]int64) string {
	switch {
	case SameLabPromptTokens(prompt[slotProductionA], prompt[slotProductionB]):
		return reasonSameLabPair
	case SameLabPromptTokens(prompt[slotJudgeB], prompt[slotProductionA]) ||
		SameLabPromptTokens(prompt[slotJudgeB], prompt[slotProductionB]):
		return reasonSameLabJudge
	}
	return ""
}

// RelearnFingerprint re-learns one drifted participant mid-run under the
// stage's rule: its repeats of the fixed request (ms) must agree, and with
// the others' current prompt token counts the labs must still be told apart
// (SameLabPromptTokens, as at the stage).
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
		if v, ok := others[other]; ok && SameLabPromptTokens(v, n) {
			return fp, reasonSameLabPair
		}
		if v, ok := others[slotJudgeB]; ok && SameLabPromptTokens(v, n) {
			return fp, reasonSameLabJudge
		}
	case slotJudgeB:
		for _, p := range []string{slotProductionA, slotProductionB} {
			if v, ok := others[p]; ok && SameLabPromptTokens(v, n) {
				return fp, reasonSameLabJudge
			}
		}
	}
	return fp, ""
}
