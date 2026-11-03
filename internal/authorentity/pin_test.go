package authorentity_test

import (
	"fmt"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
)

// The hand join (E4o): a pin joins the components of its sides'
// fingerprints after the decisions, never across a veto.

// pinWorld is «Андрей Смирнов» on books 1–3 and «Андрей Иванович Смирнов»
// on books 11–14, apart; an unrelated swap pair gets a decision, so the
// pass has its decision step.
func pinWorld(t *testing.T, pins ...authorentity.MergePin) *world {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := int64(1); i <= 4; i++ {
		ch.c.book(10+i, fmt.Sprintf("Записки %d", i)).credit(10+i, 10+i, "fp-full", person("Смирнов", "Андрей", "Иванович"))
		if i < 4 {
			ch.c.book(i, fmt.Sprintf("Чародей %d", i)).credit(i, i, "fp-short", person("Смирнов", "Андрей"))
		}
	}
	ch.c.in.Pins = pins
	withSwap(ch.c)
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateSwap, 90, 91)
	return w
}

// pinState is the pin's state in the pass.
func pinState(p *authorentity.Plan, id int64) string {
	for _, s := range p.PinStates() {
		if s.PinID == id {
			return s.State
		}
	}
	return ""
}

func TestAPinJoinsItsSides(t *testing.T) {
	p := pinWorld(t, authorentity.MergePin{ID: 5, A: []string{"fp-short"}, B: []string{"fp-full"}}).pass()
	assert.Equal(t, p.EntityOf(1), p.EntityOf(11))
	assert.Equal(t, authorentity.PinApplied, pinState(p, 5))
	assert.Equal(t, "pin:5", p.RuleOf(1), "the smaller side goes by the pin")
	assert.Equal(t, 1, p.Report.Model.Pins[authorentity.PinApplied])
}

func TestAPinWithoutItsSideJoinsNothing(t *testing.T) {
	p := pinWorld(t, authorentity.MergePin{ID: 5, A: []string{"fp-gone"}, B: []string{"fp-full"}}).pass()
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(11))
	assert.Equal(t, authorentity.PinUnanchored, pinState(p, 5))
}

func TestAPinNeverOverridesAVeto(t *testing.T) {
	w := pinWorld(t, authorentity.MergePin{ID: 5, A: []string{"fp-short"}, B: []string{"fp-full"}})
	w.ch.c.veto(1, []bookFP{{1, "fp-short"}}, []bookFP{{11, "fp-full"}})
	w.ch.c.in.VetoBooks = map[int64][]string{1: {"fp-short"}, 11: {"fp-full"}}
	p := w.pass()
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(11))
	assert.Equal(t, authorentity.PinVetoed, pinState(p, 5))
}
