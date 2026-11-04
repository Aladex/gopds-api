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

// A veto within one side holds too (E4o): side a's two fingerprints
// a veto keeps apart are never joined through the pin.
func TestAPinNeverOverridesAVetoWithinASide(t *testing.T) {
	w := pinWorld(t, authorentity.MergePin{ID: 5, A: []string{"fp-short", "fp-other"}, B: []string{"fp-full"}})
	w.ch.c.book(30, "Песни").credit(30, 30, "fp-other", person("Смирнов", "Андрей", "Петрович"))
	w.ch.c.veto(1, []bookFP{{1, "fp-short"}}, []bookFP{{30, "fp-other"}})
	w.ch.c.in.VetoBooks = map[int64][]string{1: {"fp-short"}, 30: {"fp-other"}}
	p := w.pass()
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(30))
	assert.NotEqual(t, p.EntityOf(30), p.EntityOf(11))
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(11))
	assert.Equal(t, authorentity.PinVetoed, pinState(p, 5))
}

func TestAPinNeverOverridesAVeto(t *testing.T) {
	w := pinWorld(t, authorentity.MergePin{ID: 6, A: []string{"fp-short"}, B: []string{"fp-full"}})
	w.ch.c.veto(1, []bookFP{{1, "fp-short"}}, []bookFP{{11, "fp-full"}})
	w.ch.c.in.VetoBooks = map[int64][]string{1: {"fp-short"}, 11: {"fp-full"}}
	p := w.pass()
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(11))
	assert.Equal(t, authorentity.PinVetoed, pinState(p, 6))
}

// A side may name fallback credits (E4z): «Андрей Смирнов (Шуня)» is a
// person as written of its own — its qualifier is no evidence — until a pin
// names its fingerprint.
func aliasWorld(t *testing.T, pins ...authorentity.MergePin) *world {
	w := pinWorld(t, pins...)
	alias := &name{family: "Смирнов", given: "Андрей", script: "Cyrl", displayAs: "Андрей Смирнов (Шуня)"}
	w.ch.c.book(40, "Шуня 1").unselected(40, 40, "fp-alias", "unusual_characters", nil, alias).done().
		book(41, "Шуня 2").unselected(41, 41, "fp-alias", "unusual_characters", nil, alias).done()
	return w
}

func TestAPinJoinsFallbackCreditsToItsEntity(t *testing.T) {
	w := aliasWorld(t)
	p := w.pass()
	fallback(t, p, 40)
	alias := p.EntityOf(40)
	apart(t, p, 40, 11)

	w.ch.c.in.Pins = []authorentity.MergePin{{ID: 7, A: []string{"fp-alias"}, B: []string{"fp-full"}}}
	p = w.pass()
	assert.Equal(t, authorentity.PinApplied, pinState(p, 7))
	same(t, p, 40, 11)
	same(t, p, 41, 11)
	assert.Equal(t, "pin:7", p.RuleOf(40))
	apart(t, p, 40, 1)
	assert.Equal(t, 2, p.Report.Fallback.Pinned)
	if e := w.s.Entities[alias]; assert.NotNil(t, e.MergedInto, "the alias entity is absorbed in the same pass") {
		assert.Equal(t, p.EntityOf(11), *e.MergedInto)
	}
	assert.Zero(t, w.pass().Changes(), "a second pass changes nothing")
	assert.Equal(t, authorentity.PinApplied, pinState(w.last, 7))

	// Revoked, the alias is a person as written again.
	w.ch.c.in.Pins = nil
	p = w.pass()
	fallback(t, p, 40)
	apart(t, p, 40, 11)
	assert.Zero(t, w.pass().Changes())
}

func TestAPinNeverJoinsFallbackCreditsAcrossAVeto(t *testing.T) {
	w := aliasWorld(t, authorentity.MergePin{ID: 7, A: []string{"fp-alias"}, B: []string{"fp-full"}})
	w.ch.c.veto(1, []bookFP{{40, "fp-alias"}}, []bookFP{{11, "fp-full"}})
	w.ch.c.in.VetoBooks = map[int64][]string{40: {"fp-alias"}, 11: {"fp-full"}}
	p := w.pass()
	assert.Equal(t, authorentity.PinVetoed, pinState(p, 7))
	fallback(t, p, 40)
	apart(t, p, 40, 11)
}

// Two sides of fallback credits alone are one entity of their own.
func TestAPinJoinsTwoFallbackSides(t *testing.T) {
	w := aliasWorld(t, authorentity.MergePin{ID: 7, A: []string{"fp-alias"}, B: []string{"fp-handle"}})
	w.ch.c.book(42, "Ник").unselected(42, 42, "fp-handle", "mononym", nil, nickOnly("shunya1984")).done()
	p := w.pass()
	assert.Equal(t, authorentity.PinApplied, pinState(p, 7))
	same(t, p, 40, 42)
	apart(t, p, 40, 11)
	apart(t, p, 40, 1)
	assert.Zero(t, w.pass().Changes())
}

// A credit the same-name join reads goes where its pin puts it.
func TestAPinTakesACreditFromTheSameNameJoin(t *testing.T) {
	w := pinWorld(t, authorentity.MergePin{ID: 7, A: []string{"fp-pending"}, B: []string{"fp-full"}})
	w.ch.c.book(43, "Чародей 4").unselected(43, 43, "fp-pending", "partial_structure", nil, person("Смирнов", "Андрей")).done()
	p := w.pass()
	assert.Equal(t, authorentity.PinApplied, pinState(p, 7))
	same(t, p, 43, 11)
	apart(t, p, 43, 1)
	assert.Equal(t, "pin:7", p.RuleOf(43))
	assert.Zero(t, w.pass().Changes())
}

// A list's fingerprint names several people: a pin never moves its
// segments, and it anchors nothing.
func TestAPinNeverMovesAList(t *testing.T) {
	w := pinWorld(t, authorentity.MergePin{ID: 7, A: []string{"fp-list"}, B: []string{"fp-full"}})
	list := &name{displayAs: "Александр Силецкий, Эзиз Овезов", script: "Cyrl", given: "Александр", family: "Силецкий"}
	w.ch.c.book(44, "Сборник").unselected(44, 44, "fp-list", "unusual_characters", nil, list).done()
	w.ch.c.knownGiven("Александр", 100, 100).knownGiven("Эзиз", 200, 200)
	p := w.pass()
	assert.Equal(t, authorentity.PinUnanchored, pinState(p, 7))
	assert.Equal(t, authorentity.RuleListed, p.RuleOf(44))
	apart(t, p, 44, 11)
}
