package services

import (
	"context"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pair is registered for its stratum's script: an agreed answer whose
// assembled name is written in another script is not the registered pair's,
// and stays unresolved — whatever the judges confirmed.
func TestResolveDecideRefusesAnotherScript(t *testing.T) {
	input := llmreq.ItemInput{Fields: []llmres.FieldValue{
		{Field: llmres.FieldFirst, Text: "J."}, {Field: llmres.FieldMiddle, Text: "K."},
		{Field: llmres.FieldLast, Text: "Smith"}}, Flags: []string{}}
	class, script, confirmed, tier := "initials", "Cyrl", string(models.AuthorLLMJudgementConfirmed), "confirm"
	v := &database.AuthorLLMVerdictToApply{
		ID: 1, SourceFingerprint: make([]byte, 32), ExtractorVersion: "x1", Verdict: "agree", Tier: &tier,
		ContextState: "none", Judgement: &confirmed, DecisionClass: &class, ProposalScript: &script,
		Reply: []byte(`{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"additional"},` +
			`{"i":2,"role":"family"}],"case_fix":[]}`),
	}
	run := &resolveRun{Plan: database.AuthorLLMResolveSettings{Strata: map[string]database.AuthorLLMResolveStratum{
		"initials.cyrl": {Class: "initials", Script: "Cyrl", Arm: "A", ItemsPerRequest: 1,
			Tiers: []string{"confirm", "restructure"}}}}}
	d := &AuthorLLMResolveDriver{version: "llm1-test"}
	s, out, err := d.decide(context.Background(), nil, run, v, &input)
	require.NoError(t, err)
	require.NotNil(t, s.Reason)
	assert.Equal(t, string(models.UnresolvedLLMNotRegistered), *s.Reason)
	assert.Nil(t, s.ResultID)
	assert.Nil(t, out, "no local proposal to leave unresolved on")
}

// A pair is registered for a stratum "<class>.<script>" only: an ambiguous
// local class and a script of its own.
func TestRegistrablePairNeedsAScript(t *testing.T) {
	class, script, ok := registrablePair("initials.cyrl")
	assert.True(t, ok)
	assert.Equal(t, "initials", string(class))
	assert.Equal(t, "Cyrl", script)
	for _, stratum := range []string{"homoglyph", "single_field.none", "mixed_script.mixed", "structured_person.cyrl",
		"initials.zzzz", "initials.", ".cyrl"} {
		_, _, ok := registrablePair(stratum)
		assert.False(t, ok, stratum)
	}
}
