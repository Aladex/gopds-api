package llmeval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// censusOpts are the merge gate's options for a census mode (merge1,
// merge2, merge4); holdoutOpts for a holdout (merge0, merge3).
var (
	censusOpts = func() Options {
		o := contestedOpts
		o.GateRule = GateRuleCensus
		return o
	}()
	holdoutOpts = func() Options {
		o := contestedOpts
		o.GateRule = GateRuleHoldout
		return o
	}()
)

// evaluateCensus evaluates a merge stratum under o and returns its
// decision and combo.
func evaluateCensus(t *testing.T, in *Input, o *Options) (StratumDecision, *ComboMetrics) {
	t.Helper()
	r := EvaluateWith(in, o)
	require.Empty(t, r.Decision.Reasons, "the fixture run decides nothing")
	m := r.Strata["bucket"].Combos["A1"]
	return r.Decision.Strata[0], &m
}

// A census asked every candidate: with no error it passes however few
// they are — 883 accepts (merge1's swap) are far below the 2,995 the bound
// needs — and the report names the rule and the census's completeness. The
// bound is still reported.
func TestACensusWithNoErrorPassesBelowTheBound(t *testing.T) {
	in := contestedStratum(883, 0, false, AdjudicatedSame)
	d, m := evaluateCensus(t, &in, &censusOpts)
	assert.True(t, d.Passed, d.Reason)
	assert.Equal(t, GateRuleCensus, d.Rule)
	assert.Equal(t, 883, m.Accepted)
	assert.Zero(t, m.Errors)
	assert.Greater(t, m.UpperBound, censusOpts.MaxErrorShare, "the bound would have failed it")
	require.NotNil(t, m.Census)
	assert.Equal(t, CensusMetrics{Candidates: 883, Settled: 883, Unsettled: []int64{}, Complete: true}, *m.Census)
}

// One error fails a census: an accept the judges answered different, or a
// contested accept still pending.
func TestOneErrorFailsACensus(t *testing.T) {
	wrong := mergeStratum(883, 0)
	wrong.Items[5].Judges = [2]Answer{answer("different", ""), answer("different", "")}
	for name, in := range map[string]Input{
		"gold different":    wrong,
		"contested pending": contestedStratum(883, 0, false, ""),
	} {
		t.Run(name, func(t *testing.T) {
			d, m := evaluateCensus(t, &in, &censusOpts)
			assert.False(t, d.Passed)
			assert.Equal(t, 1, m.Errors)
			assert.True(t, m.Census.Complete)
			assert.Equal(t, "census: 1 errors of 883 accepted in A1", d.Reason)
		})
	}
}

// One candidate without a settled pair answer fails a census though
// nothing is an error: one never asked (no answer at all), one whose
// answer failed (invalid — within the 2% invalid gate), one answered by
// a single participant. The report lists it.
func TestOneUnsettledCandidateFailsACensus(t *testing.T) {
	failed := Answer{Invalid: true}
	same := Answer{Valid: true, Canonical: "same", Tier: "merge"}
	for name, unsettle := range map[string]func(it *Item){
		"not asked":   func(it *Item) { it.Candidates = map[Combo][2]Answer{} },
		"failed":      func(it *Item) { it.Candidates[both1] = [2]Answer{same, failed} },
		"half asked":  func(it *Item) { it.Candidates[both1] = [2]Answer{{}, same} },
		"both failed": func(it *Item) { it.Candidates[both1] = [2]Answer{failed, failed} },
	} {
		t.Run(name, func(t *testing.T) {
			in := mergeStratum(883, 0)
			unsettle(&in.Items[7])
			d, m := evaluateCensus(t, &in, &censusOpts)
			assert.False(t, d.Passed)
			assert.Zero(t, m.Errors)
			assert.True(t, m.InvalidPassed, "the invalid gate alone would pass it")
			assert.Equal(t, CensusMetrics{Candidates: 883, Settled: 882, Unsettled: []int64{8}}, *m.Census)
			assert.Equal(t, "census: 1 of 883 candidates without a settled pair answer in A1", d.Reason)
		})
	}
}

// A refusal settles a candidate: the pair answered, it accepts nothing.
func TestARefusalSettlesACensusCandidate(t *testing.T) {
	in := mergeStratum(883, 0)
	in.Items[3].Candidates[both1] = [2]Answer{{Valid: true, Refused: true, Canonical: "cannot_tell"},
		{Valid: true, Canonical: "different"}}
	d, m := evaluateCensus(t, &in, &censusOpts)
	assert.True(t, d.Passed, d.Reason)
	assert.Equal(t, 882, m.Accepted)
	assert.True(t, m.Census.Complete)
}

// A census that accepted nothing passes nothing: there is nothing it would
// apply.
func TestACensusThatAcceptedNothingDoesNotPass(t *testing.T) {
	in := mergeStratum(300, 0)
	for i := range in.Items {
		d := Answer{Valid: true, Canonical: "different"}
		in.Items[i].Candidates[both1] = [2]Answer{d, d}
	}
	d, _ := evaluateCensus(t, &in, &censusOpts)
	assert.False(t, d.Passed)
	assert.Equal(t, "census: nothing accepted in A1", d.Reason)
}

// The holdout path keeps the bound: the same 883 clean accepts fail it,
// 2,995 pass it; no census is reported.
func TestTheHoldoutPathStillAppliesTheBound(t *testing.T) {
	in := mergeStratum(883, 0)
	d, m := evaluateCensus(t, &in, &holdoutOpts)
	assert.False(t, d.Passed)
	assert.Equal(t, GateRuleHoldout, d.Rule)
	assert.Equal(t, "gate: no combo passed", d.Reason)
	assert.Nil(t, m.Census)
	in = mergeStratum(2995, 0)
	d, _ = evaluateCensus(t, &in, &holdoutOpts)
	assert.True(t, d.Passed, d.Reason)
}

// A run-wide failure decides nothing for a census either; the rule is
// still reported.
func TestARunWideFailureKeepsTheCensusRule(t *testing.T) {
	in := mergeStratum(883, 0)
	in.Fingerprint = FingerprintResult{Reason: "drift"}
	r := EvaluateWith(&in, &censusOpts)
	require.NotEmpty(t, r.Decision.Reasons)
	assert.Equal(t, StratumDecision{Stratum: "bucket", Rule: GateRuleCensus}, r.Decision.Strata[0])
}

// K's gate names no rule and reports no census.
func TestKsGateNamesNoRule(t *testing.T) {
	in := healthy()
	in.Items = stratumItems("s", 300, agreeX)
	r := Evaluate(&in)
	require.Len(t, r.Decision.Strata, 1)
	for _, d := range r.Decision.Strata {
		assert.Empty(t, d.Rule)
	}
	for _, s := range r.Strata {
		for _, m := range s.Combos {
			assert.Nil(t, m.Census)
		}
	}
}
