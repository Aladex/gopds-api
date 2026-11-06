package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tremaynes are two spellings of a name and its Latin form: two translit
// decisions join all three, and a typo candidate lies between the spellings.
func tremaynes(t *testing.T) *churn {
	ch := twoNames(t, person("Тремейн", "Питер"), person("Тримейн", "Питер"))
	ch.c.book(3, "Книга В").credit(3, 3, "fp-c", person("Tremayne", "Peter").latin())
	return ch
}

// A different on a spelling pair never undoes what another kind joins, the
// decision's own card having more to go on: «Тремейн» and «Тримейн» on two
// thin cards say nothing against two decisions on «Peter Tremayne» (D4).
func TestASpellingDifferentNeverHoldsAnotherKindsJoin(t *testing.T) {
	w := newWorld(t, tremaynes(t))
	w.pass()
	typo := candidateOf(w.level0, authorentity.CandidateTypo, 1, 2)
	require.NotNil(t, typo)
	w.ch.c.in.NotSame = map[[32]byte]bool{typo.Digest: true}
	first := w.decide(authorentity.CandidateTranslit, 1, 3)
	second := w.decide(authorentity.CandidateTranslit, 2, 3)
	p := w.pass()
	assert.Equal(t, applied(first), w.state(first))
	assert.Equal(t, applied(second), w.state(second))
	same(t, p, 1, 2)
	assert.Zero(t, w.pass().Changes(), "a second pass changes nothing")
}

// A different on a spelling pair still holds a join nothing else settled: a
// chain of two spelling decisions cannot join what the pair refused.
func TestASpellingDifferentHoldsASpellingChain(t *testing.T) {
	ch := twoNames(t, person("Тартлдав", "Гарри"), person("Тертлдав", "Гарри"))
	ch.c.book(3, "Книга В").credit(3, 3, "fp-c", person("Терлдав", "Гарри"))
	w := newWorld(t, ch)
	w.pass()
	refused := candidateOf(w.level0, authorentity.CandidateTypo, 1, 3)
	require.NotNil(t, refused)
	ch.c.in.NotSame = map[[32]byte]bool{refused.Digest: true}
	first := w.decide(authorentity.CandidateTypo, 1, 2)
	second := w.decide(authorentity.CandidateTypo, 2, 3)
	p := w.pass()
	states := []authorentity.DecisionState{w.state(first), w.state(second)}
	assert.Contains(t, states, conflict(second, authorentity.ConflictNotSame))
	apart(t, p, 1, 3)
}

// A spelling same joins the person of the books. The short name's books all
// moved to the full name, so the misspelling joins the full name, and the
// short key lends the stub neither its name nor its unselected credits (D4).
func TestASpellingSameFollowsTheBooksOfAnEmptiedSide(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Туманность Андромеды").credit(1, 1, "fp-full", person("Ефремов", "Иван", "Антонович")).
		book(11, "Лезвие бритвы").credit(11, 11, "fp-short", person("Ефремов", "Иван")).
		book(12, "Час быка").credit(12, 12, "fp-short", person("Ефремов", "Иван")).
		book(21, "Сердце змеи").credit(21, 21, "fp-typo", person("Ефримов", "Иван"))
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateBucket, 11, 1)
	w.decide(authorentity.CandidateBucket, 12, 1)
	typo := w.decide(authorentity.CandidateTypo, 11, 21)
	p := w.pass()
	assert.Equal(t, applied(typo), w.state(typo))
	same(t, p, 11, 1)
	same(t, p, 21, 1)
	assert.Zero(t, w.pass().Changes(), "a second pass changes nothing")
}

// A different on a non-spelling pair still undoes an applied join, and the
// pass says so: the autopilot, whose merge runs ask these unattended, does
// not write such a pass (D5). A hold that was a hold before is not reported.
func TestANonSpellingDifferentReportsTheAppliedDecisionItHolds(t *testing.T) {
	w := newWorld(t, tremaynes(t))
	w.pass()
	refused := candidateOf(w.level0, authorentity.CandidateTranslit, 1, 3)
	require.NotNil(t, refused)
	bridge := w.decide(authorentity.CandidateTranslit, 2, 3)
	typo := w.decide(authorentity.CandidateTypo, 1, 2)
	same(t, w.pass(), 1, 3)
	for i := range w.decisions {
		w.decisions[i].Applied = w.state(w.decisions[i].ID).State == authorentity.DecisionApplied
	}

	w.ch.c.in.NotSame = map[[32]byte]bool{refused.Digest: true}
	p := w.pass()
	require.Equal(t, conflict(typo, authorentity.ConflictNotSame), w.state(typo))
	assert.Equal(t, applied(bridge), w.state(bridge))
	assert.Equal(t, []int64{typo}, p.Report.Model.AppliedNotSame)
	apart(t, p, 1, 3)

	for i := range w.decisions {
		w.decisions[i].Applied = w.state(w.decisions[i].ID).State == authorentity.DecisionApplied
	}
	assert.Empty(t, w.pass().Report.Model.AppliedNotSame, "a held decision is no new hold")
}
