package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pen name has another given name by definition («Мэри Уэстмакотт» is
// «Агата Кристи»), so the given-name guard never holds a pen_name decision
// (prod build 60: 359 held). The other guards still do.

// penNamesOfOneFamily: «Виталий Савченко» (1) writing as «Владимир
// Савченко», and «Владимир Савченко» (11): one family, two full given
// names.
func penNamesOfOneFamily(t *testing.T) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Книга").credit(1, 1, "fp-a", person("Савченко", "Виталий").withNickname("Владимир Савченко"))
	ch.c.book(11, "Книга").credit(11, 11, "fp-b", person("Савченко", "Владимир"))
	return ch
}

func TestAPenNameDecisionPassesTheGivenNameGuard(t *testing.T) {
	w := newWorld(t, penNamesOfOneFamily(t))
	w.pass()
	pen := w.decide(authorentity.CandidatePenName, 1, 11)
	p := w.pass()
	assert.Equal(t, applied(pen), w.state(pen))
	same(t, p, 1, 11)
	assert.Zero(t, p.Report.Model.Conflict[authorentity.ConflictGivenName])

	// A second pass changes nothing.
	p2 := w.pass()
	assert.Equal(t, applied(pen), w.state(pen))
	same(t, p2, 1, 11)
	assert.Empty(t, p2.Changes())
}

// The guard still holds the same pair under any other kind.
func TestTheGivenNameGuardStillHoldsOtherKinds(t *testing.T) {
	w := newWorld(t, savchenkos(t))
	w.pass()
	w.decide(authorentity.CandidateInitials, 11, 21)
	vitaly := w.decide(authorentity.CandidateInitials, 11, 1)
	w.pass()
	assert.Equal(t, conflict(vitaly, authorentity.ConflictGivenName), w.state(vitaly))
}

// A pen_name decision across a not_same pair is still held.
func TestAPenNameDecisionStillHonoursNotSame(t *testing.T) {
	w := newWorld(t, penNamesOfOneFamily(t))
	w.pass()
	c := candidateOf(w.level0, authorentity.CandidatePenName, 1, 11)
	require.NotNil(t, c)
	pen := w.decide(authorentity.CandidatePenName, 1, 11)
	w.ch.c.in.NotSame = map[[32]byte]bool{c.Digest: true}
	p := w.pass()
	assert.Equal(t, conflict(pen, authorentity.ConflictNotSame), w.state(pen))
	apart(t, p, 1, 11)
}

// A pen_name join lets through a decision of another kind the guard held
// before it: «В. Савченко» to Vitaly, once Vitaly and Vladimir are one by
// their pen name. The next pass reads the pen name first and lets it
// through, so the pass settles it at once: the second pass changes nothing
// (prod copy, build 60: 53 decisions and 121 rows a pass later otherwise).
func TestAPenNameJoinSettlesInOnePass(t *testing.T) {
	ch := penNamesOfOneFamily(t)
	ch.c.book(21, "Книга").credit(21, 21, "fp-c", person("Савченко", "В."))
	w := newWorld(t, ch)
	w.pass()
	vladimir := w.decide(authorentity.CandidateInitials, 21, 11)
	vitaly := w.decide(authorentity.CandidateInitials, 21, 1)
	pen := w.decide(authorentity.CandidatePenName, 1, 11)
	w.register(vladimir, 1)
	w.register(vitaly, 1)
	w.register(pen, 2)
	p := w.pass()
	for _, id := range []int64{vladimir, vitaly, pen} {
		assert.Equal(t, applied(id), w.state(id))
	}
	same(t, p, 1, 11)
	same(t, p, 1, 21)
	assert.Zero(t, p.Report.Model.Conflict[authorentity.ConflictGivenName])

	p2 := w.pass()
	for _, id := range []int64{vladimir, vitaly, pen} {
		assert.Equal(t, applied(id), w.state(id))
	}
	assert.Zero(t, p2.Changes())
}
