package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handMove writes the manual row of the route: credit on a new card.
func handMove(w *world, credit int64, display string) uuid.UUID {
	card := uuid.New()
	w.s.Entities[card] = authorentity.EntityRow{ID: card, Kind: "person", DisplayName: display, SortName: display,
		SearchKey: display}
	key := authorentity.CreditKey{CreditID: credit}
	w.s.Credits[key] = authorentity.CreditRow{Key: key, EntityID: &card, Method: authorentity.MethodManual,
		Rule: authorentity.RuleCreditMove}
	return card
}

// A hand move says where one book goes. The fingerprint's other credits stay
// with their author and the decisions on the side stay applied: a side of a
// common written name must not fall apart over one book (D5).
func TestAHandMoveLeavesTheSideItsDecisions(t *testing.T) {
	w := newWorld(t, newChurn(t, 40))
	w.pass()
	id := w.decide(authorentity.CandidateSwap, 1, 500)
	same(t, w.pass(), 1, 500)

	card := handMove(w, 39, "Другой Автор")
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	assert.Equal(t, card, p.EntityOf(39))
	same(t, p, 1, 500)
	assert.NotEqual(t, card, p.EntityOf(1))
	assert.Zero(t, w.pass().Changes(), "a second pass changes nothing")
	assert.Equal(t, card, w.last.EntityOf(39))
}

// Another manual row keeps what it always did: its side is a manual side.
func TestAWholeManualRowStillHoldsItsSide(t *testing.T) {
	w := newWorld(t, newChurn(t, 40))
	w.pass()
	id := w.decide(authorentity.CandidateSwap, 1, 500)
	w.pass()
	card := handMove(w, 39, "Другой Автор")
	key := authorentity.CreditKey{CreditID: 39}
	row := w.s.Credits[key]
	row.Rule = "manual"
	w.s.Credits[key] = row
	p := w.pass()
	assert.Equal(t, authorentity.ExcludedManualSide, w.state(id).Reason)
	assert.Equal(t, card, p.EntityOf(39))
}

// A revoked move hands the credit back: the next pass puts it on its author,
// and the card only it held goes.
func TestARevokedHandMoveGoesBack(t *testing.T) {
	w := newWorld(t, newChurn(t, 40))
	w.pass()
	home := w.last.EntityOf(39)
	card := handMove(w, 39, "Другой Автор")
	p := w.pass()
	require.Equal(t, card, p.EntityOf(39))

	delete(w.s.Credits, authorentity.CreditKey{CreditID: 39})
	p = w.pass()
	assert.Equal(t, home, p.EntityOf(39))
	w.pass()
	assert.NotContains(t, w.s.Entities, card)
}
