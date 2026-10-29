package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The registration gate of the decisions (E4d, §4.7): a model decision
// whose kind is not registered, whose registration is withdrawn or whose
// script is blocked never applies, whatever its candidate; one applied
// before lapses and its pair splits with the absorbed ID revived.
func TestAWithheldDecisionNeverApplies(t *testing.T) {
	for _, reason := range []string{"unregistered", "withdrawn", "script_blocked"} {
		t.Run(reason, func(t *testing.T) {
			w := newWorld(t, threePoselyagins(t))
			w.pass()
			b := w.last.EntityOf(11)
			ab := w.decide(authorentity.CandidateSwap, 1, 11)
			p := w.pass()
			require.Equal(t, applied(ab), w.state(ab))
			same(t, p, 1, 11)

			w.decisions[0].Withheld = reason
			p = w.pass()
			assert.Equal(t, lapsed(ab, reason), w.state(ab))
			apart(t, p, 1, 11)
			assert.Equal(t, b, p.EntityOf(11), "the absorbed ID is back")
			assert.Equal(t, authorentity.RevivalExact, w.revival(b).Outcome)
			assert.Equal(t, 1, p.Report.Model.Lapsed[reason])

			// Withheld from the start: never applied.
			w2 := newWorld(t, threePoselyagins(t))
			w2.pass()
			ac := w2.decide(authorentity.CandidateSwap, 1, 21)
			w2.decisions[0].Withheld = reason
			p = w2.pass()
			assert.Equal(t, lapsed(ac, reason), w2.state(ac))
			apart(t, p, 1, 21)
		})
	}
}

// A union that would join the two sides of a pair the production pair
// answered other than same is refused: every decision of the would-be
// component is in conflict (§4.2). A decision outside it still applies.
func TestAUnionOverANotSamePairIsAConflict(t *testing.T) {
	w := newWorld(t, threePoselyagins(t))
	w.pass()
	bc := candidateOf(w.level0, authorentity.CandidateSwap, 11, 21)
	require.NotNil(t, bc)
	w.ch.c.in.NotSame = map[[32]byte]bool{bc.Digest: true}
	ab := w.decide(authorentity.CandidateSwap, 1, 11)
	ac := w.decide(authorentity.CandidateSwap, 1, 21)
	p := w.pass()
	assert.Equal(t, conflict(ab, authorentity.ConflictNotSame), w.state(ab))
	assert.Equal(t, conflict(ac, authorentity.ConflictNotSame), w.state(ac))
	apart(t, p, 1, 11)
	apart(t, p, 1, 21)

	w2 := newWorld(t, threePoselyagins(t))
	w2.pass()
	w2.ch.c.in.NotSame = map[[32]byte]bool{bc.Digest: true}
	ab = w2.decide(authorentity.CandidateSwap, 1, 11)
	p = w2.pass()
	assert.Equal(t, applied(ab), w2.state(ab))
	same(t, p, 1, 11)
}
