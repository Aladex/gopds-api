package services

import (
	"bytes"
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
	class, script, confirmed, tier, arm := "initials", "Cyrl", string(models.AuthorLLMJudgementConfirmed), "confirm", "A"
	v := &database.AuthorLLMVerdictToApply{
		ID: 1, SourceFingerprint: bytes.Repeat([]byte{7}, 32), ExtractorVersion: "x1", Verdict: "agree", Tier: &tier,
		ContextStateA: "none", ContextStateB: "none", Arm: &arm, BatchSize: 1, Judgement: &confirmed,
		DecisionClass: &class, ProposalScript: &script,
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

// An answer asked in another arm or batch than the stratum's plan is not the
// registered pair's.
func TestResolveDecideRefusesAnotherArmOrBatch(t *testing.T) {
	input := llmreq.ItemInput{Fields: []llmres.FieldValue{
		{Field: llmres.FieldFirst, Text: "А."}, {Field: llmres.FieldMiddle, Text: "Б."},
		{Field: llmres.FieldLast, Text: "Петров"}}, Flags: []string{}}
	class, script, confirmed, tier := "initials", "Cyrl", string(models.AuthorLLMJudgementConfirmed), "confirm"
	run := &resolveRun{Plan: database.AuthorLLMResolveSettings{Strata: map[string]database.AuthorLLMResolveStratum{
		"initials.cyrl": {Class: "initials", Script: "Cyrl", Arm: "A", ItemsPerRequest: 2,
			Tiers: []string{"confirm", "restructure"}}}}}
	d := &AuthorLLMResolveDriver{version: "llm1-test"}
	for name, c := range map[string]struct {
		arm   string
		batch int
	}{"another arm": {"B", 2}, "another batch": {"A", 1}} {
		v := &database.AuthorLLMVerdictToApply{
			ID: 1, SourceFingerprint: bytes.Repeat([]byte{7}, 32), ExtractorVersion: "x1", Verdict: "agree", Tier: &tier,
			ContextStateA: "none", ContextStateB: "none", Arm: &c.arm, BatchSize: c.batch, Judgement: &confirmed,
			DecisionClass: &class, ProposalScript: &script,
			Reply: []byte(`{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"additional"},` +
				`{"i":2,"role":"family"}],"case_fix":[]}`),
		}
		s, _, err := d.decide(context.Background(), nil, run, v, &input)
		require.NoError(t, err, name)
		require.NotNil(t, s.Reason, name)
		assert.Equal(t, string(models.UnresolvedLLMNotRegistered), *s.Reason, name)
	}
}
