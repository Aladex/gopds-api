package llmeval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contestedOpts are the merge gate's options with contested accepts
// adjudicated (the coordinator's decision after merge1).
var contestedOpts = func() Options {
	o := mergeOpts
	o.ContestedAdjudicated = true
	return o
}()

// contestedStratum is mergeStratum(n, m) with item 0 contested: the pair
// agreed same, one judge answered different. It is a Cyrillic holdout item,
// or, with supplement, an extra Latin question outside |A|.
func contestedStratum(n, m int, supplement bool, label string) Input {
	in := mergeStratum(n, m)
	at := 0
	if supplement {
		at = n
	}
	in.Items[at].Judges[1] = answer("different", "")
	in.Items[at].Adjudication = label
	return in
}

// A contested accept counts as an error while it is pending or labeled
// different, and is cleared (a correct accept) once labeled same or
// unclear: at 2,995 accepts one error fails the bound, none passes it.
// The report lists the item by script with the labels' counts.
func TestAContestedAcceptIsAnErrorUntilAHumanClearsIt(t *testing.T) {
	for _, tc := range []struct {
		label  string
		passed bool
		errors int
		review ContestedMetrics
	}{
		{"", false, 1, ContestedMetrics{Pending: 1, Failed: 1}},
		{AdjudicatedDifferent, false, 1, ContestedMetrics{AdjudicatedDifferent: 1, Failed: 1}},
		{AdjudicatedSame, true, 0, ContestedMetrics{AdjudicatedSame: 1}},
		{AdjudicatedUnclear, true, 0, ContestedMetrics{Unclear: 1}},
	} {
		t.Run("label "+tc.label, func(t *testing.T) {
			in := contestedStratum(2995, 0, false, tc.label)
			r := EvaluateWith(&in, &contestedOpts)
			require.Empty(t, r.Decision.Reasons)
			m := r.Strata["bucket"].Combos["A1"]
			assert.Equal(t, tc.passed, r.Decision.Strata[0].Passed)
			assert.Equal(t, 2995, m.Accepted, "the contested accept stays in |A|")
			assert.Equal(t, tc.errors, m.Errors)
			assert.Equal(t, 1, m.Contested)
			assert.InDelta(t, 1.0/2995, m.ContestedShare, 1e-12, "the share is reported as is")
			require.NotNil(t, m.ContestedReview)
			tc.review.IDs = map[string][]int64{"cyrl": {1}}
			assert.Equal(t, tc.review, *m.ContestedReview)
			assert.Equal(t, tc.errors, m.Scripts["cyrl"].Errors, "the per-script breakdown agrees")
		})
	}
}

// An extra Latin question contested is cleared the same way: pending it
// fails the stratum, labeled same it does not.
func TestAContestedSupplementQuestionIsClearedByItsLabel(t *testing.T) {
	for label, passed := range map[string]bool{"": false, AdjudicatedSame: true, AdjudicatedDifferent: false} {
		in := contestedStratum(3000, 1000, true, label)
		r := EvaluateWith(&in, &contestedOpts)
		m := r.Strata["bucket"].Combos["A1"]
		require.NotNil(t, m.Supplement)
		assert.Equal(t, passed, r.Decision.Strata[0].Passed, "label %q", label)
		assert.Equal(t, 1, m.Supplement.Contested)
		require.NotNil(t, m.Supplement.ContestedReview)
		assert.Equal(t, map[string][]int64{"latn": {3001}}, m.Supplement.ContestedReview.IDs)
	}
}

// Without the option a label changes nothing: the contested accept is an
// error whatever it says, and no review is reported (the merge gate before
// the decision; K never sets either option).
func TestALabelClearsNothingWithoutTheOption(t *testing.T) {
	in := contestedStratum(2995, 0, false, AdjudicatedSame)
	r := EvaluateWith(&in, &mergeOpts)
	m := r.Strata["bucket"].Combos["A1"]
	assert.False(t, r.Decision.Strata[0].Passed)
	assert.Equal(t, 1, m.Errors)
	assert.Nil(t, m.ContestedReview)
}
