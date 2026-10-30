package llmeval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Under the census audit the reproduction gate reads the applied set (E4c,
// run 35): the repeat answers of the items whose first answers were all
// four same, per participant, at the pooled threshold. The pooled figure
// over every repeated item is still reported; it no longer gates.

// repeatApplied is the applied items of repeatStratum.
const repeatApplied = 20

// repeatStratum is a census of repeatApplied items, each labeled same, followed by
// rejected ones — both judges answered different, and the pair too or, every
// other item, same (a gold error) — with every item's repeat reproduced or
// not as given.
func repeatStratum(rejected int, appliedReproduced, rejectedReproduced func(i int) bool) Input {
	in := auditStratum(repeatApplied + rejected)
	for i := range in.Items {
		it := &in.Items[i]
		var reproduced bool
		if i < repeatApplied {
			reproduced = appliedReproduced(i)
		} else {
			it.Judges = [2]Answer{answer("different", ""), answer("different", "")}
			if i%2 == 0 {
				it.Candidates[both1] = it.Judges
			}
			it.Adjudication = ""
			reproduced = rejectedReproduced(i)
		}
		it.Repeated = map[string]bool{}
		for _, p := range participants {
			it.Repeated[p] = reproduced
		}
	}
	return in
}

func always(int) bool { return true }

// evaluateRepeat evaluates a census under the audit: the run's decision on
// its one stratum and the pooled figures.
func evaluateRepeat(in *Input) (Report, StratumDecision) {
	r := EvaluateWith(in, &auditOpts)
	return r, r.Decision.Strata[0]
}

// Flips on items never applied — different against cannot_tell — no longer
// fail the run: 40 of 100 rejected items changed (60 of 120 pooled
// reproduced, under 90%), the 20 applied items all reproduced. The pooled
// figure is reported, failing, as information.
func TestFlipsOutsideTheAppliedSetDoNotFailACensus(t *testing.T) {
	in := repeatStratum(100, always, func(i int) bool { return i%5 >= 2 })
	r, d := evaluateRepeat(&in)
	assert.Empty(t, r.Decision.Reasons)
	assert.True(t, d.Passed, d.Reason)
	assert.True(t, r.Decision.Registrable)
	assert.Equal(t, PooledRepeatApplied, r.PooledRepeat.Gate)
	require.Len(t, r.PooledRepeat.Participants, len(participants))
	for _, c := range r.PooledRepeat.Participants {
		assert.Equal(t, 20, c.Total, c.Participant)
		assert.Equal(t, 20, c.Reproduced, c.Participant)
		assert.True(t, c.Passed, c.Participant)
	}
	require.Len(t, r.PooledRepeat.AllItems, len(participants))
	for _, c := range r.PooledRepeat.AllItems {
		assert.Equal(t, 120, c.Total, c.Participant)
		assert.Equal(t, 80, c.Reproduced, c.Participant)
		assert.False(t, c.Passed, "the pooled rule would fail it: %s", c.Participant)
	}
}

// More than 10% of the applied set's repeats changed fails the run, at the
// pooled threshold: 3 of 20 changed fails, 2 of 20 passes.
func TestFlipsInTheAppliedSetFailACensus(t *testing.T) {
	in := repeatStratum(10, func(i int) bool { return i >= 3 }, always)
	r, _ := evaluateRepeat(&in)
	assert.Contains(t, r.Decision.Reasons, "applied repeat: production_a reproduced 17 of 20")
	assert.False(t, r.Decision.Registrable)
	assert.False(t, r.Decision.Strata[0].Passed)

	in = repeatStratum(10, func(i int) bool { return i >= 2 }, always)
	r, d := evaluateRepeat(&in)
	assert.Empty(t, r.Decision.Reasons)
	assert.True(t, d.Passed, d.Reason)
}

// An applied set with no repeated item is no evidence: the run fails,
// however well the rest reproduced.
func TestAnAppliedSetWithoutRepeatsFailsACensus(t *testing.T) {
	in := repeatStratum(100, always, always)
	for i := range 20 {
		in.Items[i].Repeated = nil
	}
	r, _ := evaluateRepeat(&in)
	assert.Contains(t, r.Decision.Reasons, "applied repeat evidence: production_a answered no repeated applied item")
	assert.False(t, r.Decision.Registrable)
}

// Every other rule keeps the pooled gate over every repeated item: the
// holdout census of the old rule fails on flips outside the applied set,
// and reports no applied figure.
func TestOtherRulesKeepThePooledRepeatGate(t *testing.T) {
	in := repeatStratum(100, always, func(i int) bool { return i%5 >= 2 })
	for i := 20; i < len(in.Items); i++ {
		// The old census rule fails a gold error: no pair accepts here.
		in.Items[i].Candidates[both1] = in.Items[i].Judges
	}
	r := EvaluateWith(&in, &censusOpts)
	assert.Contains(t, r.Decision.Reasons, "pooled repeat: production_a reproduced 80 of 120")
	assert.Empty(t, r.PooledRepeat.Gate)
	assert.Nil(t, r.PooledRepeat.AllItems)
}
