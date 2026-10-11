package authornorm

import (
	"errors"
	"testing"
)

// An LLM result names the local class it resolved under the prefix "llm.";
// only an ambiguous class is ever sent, so only those are valid.
func TestLLMClassNamesTheResolvedLocalClass(t *testing.T) {
	c := LLMClass(ClassInitials)
	if c != "llm.initials" {
		t.Fatalf("LLMClass(initials) = %q", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("llm.initials: %v", err)
	}
	if base, ok := c.LLMBase(); !ok || base != ClassInitials {
		t.Errorf("LLMBase = %q, %v", base, ok)
	}
	for _, bad := range []DecisionClass{"llm.structured_person", "llm.malformed", "llm.nonsense", "llm.", "llm.llm.initials"} {
		if err := bad.Validate(); !errors.Is(err, ErrUnknownDecisionClass) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, ok := ClassInitials.LLMBase(); ok {
		t.Error("a local class has an LLM base")
	}
	if LLMClass(ClassInitials).Selectable() {
		t.Error("an LLM class is selectable by the local policy")
	}
}
