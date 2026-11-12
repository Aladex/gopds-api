package services

import (
	"testing"

	"gopds-api/database"
	"gopds-api/llm"
)

// The fingerprint check tolerates a one-token difference of the fixed
// request's prompt tokens on either side of the reference (a provider's
// count jitters by one for the same model), and fails at two.
func TestFingerprintPassedToleratesOnePromptToken(t *testing.T) {
	const ref = 543
	claim := &database.AuthorLLMClaim{Reference: &database.AuthorLLMFingerprintReference{PromptTokens: ref}}
	for _, c := range []struct {
		prompt int64
		want   bool
	}{
		{ref - 2, false}, {ref - 1, true}, {ref, true}, {ref + 1, true}, {ref + 2, false},
	} {
		resp := &llm.StructuredResponse{Usage: llm.Usage{Present: true, PromptTokens: c.prompt}}
		if got := fingerprintPassed(claim, resp, false); got != c.want {
			t.Errorf("prompt tokens %d against %d: passed %v, want %v", c.prompt, ref, got, c.want)
		}
	}
}

// The tolerance is the prompt tokens' only: a model mismatch, absent usage,
// a missing reference or reasoning below the floor still fail with the
// prompt tokens within it.
func TestFingerprintPassedKeepsTheOtherConditions(t *testing.T) {
	const ref, floor = 543, 100
	reasoning := func(n int64) *int64 { return &n }
	plain := &database.AuthorLLMClaim{Reference: &database.AuthorLLMFingerprintReference{PromptTokens: ref}}
	reasoner := &database.AuthorLLMClaim{Reference: &database.AuthorLLMFingerprintReference{PromptTokens: ref,
		ReasoningFloor: floor}}
	for _, delta := range []int64{-1, 0, 1} {
		usage := llm.Usage{Present: true, PromptTokens: ref + delta}
		if fingerprintPassed(plain, &llm.StructuredResponse{Usage: usage}, true) {
			t.Errorf("±%d: a model mismatch passed", delta)
		}
		absent := usage
		absent.Present = false
		if fingerprintPassed(plain, &llm.StructuredResponse{Usage: absent}, false) {
			t.Errorf("±%d: absent usage passed", delta)
		}
		if fingerprintPassed(&database.AuthorLLMClaim{}, &llm.StructuredResponse{Usage: usage}, false) {
			t.Errorf("±%d: no reference passed", delta)
		}

		below := usage
		below.ReasoningTokens = reasoning(floor - 1)
		if fingerprintPassed(reasoner, &llm.StructuredResponse{Usage: below}, false) {
			t.Errorf("±%d: reasoning below the floor passed", delta)
		}
		belowCompletion := usage
		belowCompletion.CompletionTokens = floor - 1
		if fingerprintPassed(reasoner, &llm.StructuredResponse{Usage: belowCompletion}, false) {
			t.Errorf("±%d: completion below the floor (no reasoning reported) passed", delta)
		}
		at := usage
		at.ReasoningTokens = reasoning(floor)
		if !fingerprintPassed(reasoner, &llm.StructuredResponse{Usage: at}, false) {
			t.Errorf("±%d: reasoning at the floor failed", delta)
		}
	}
}

// Two evals' references are the same only for the same participants: one
// that has a participant the other lacks is not, whatever the counts.
func TestSameReferencesNeedsTheSameParticipants(t *testing.T) {
	both := []byte(`{"judge_a":{"prompt_tokens":700,"output_mode":"tool"},"judge_b":{"prompt_tokens":900,"output_mode":"tool"}}`)
	one := []byte(`{"judge_a":{"prompt_tokens":700,"output_mode":"tool"}}`)
	jitter := []byte(`{"judge_a":{"prompt_tokens":701,"output_mode":"tool"},"judge_b":{"prompt_tokens":899,"output_mode":"tool"}}`)
	for _, c := range []struct {
		name string
		a, b []byte
		want bool
	}{
		{"one participant fewer", both, one, false},
		{"one participant more", one, both, false},
		{"none and one", nil, one, false},
		{"none and none", nil, []byte("null"), true},
		{"one-token jitter", both, jitter, true},
	} {
		got, err := sameReferences(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}
