package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oldridges are a given name cut short and its one full name.
func oldridges(t *testing.T) *world {
	return newWorld(t, twoNames(t, person("Олдридж", "Дж"), person("Олдридж", "Джеймс")))
}

// A second full name makes «Дж» ambiguous for a new question, not for the
// pair the models already accepted on these two cards: the decision stays
// applied, the pair together and the namesake apart (D5).
func TestANewNamesakeKeepsAnAppliedNameForm(t *testing.T) {
	w := oldridges(t)
	w.pass()
	id := w.decide(authorentity.CandidateNameForm, 1, 2)
	same(t, w.pass(), 1, 2)
	require.Equal(t, applied(id), w.state(id))

	w.ch.c.book(3, "Книга В").credit(3, 3, "fp-c", person("Олдридж", "Джон"))
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	same(t, p, 1, 2)
	apart(t, p, 1, 3)
	assert.Nil(t, candidateOf(w.level0, authorentity.CandidateNameForm, 1, 3), "the namesake's pair is no candidate")
	assert.Zero(t, w.pass().Changes(), "a second pass changes nothing")
	assert.Equal(t, applied(id), w.state(id))
}

// The same for initials a bucket keeps out: a second full name of those
// initials arrives, the applied pair stays and the new pair is not asked.
func TestANewNamesakeKeepsAnAppliedInitialsNameForm(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Книга А").credit(1, 1, "fp-a", person("Петров", "Иван")).
		book(2, "Книга Б").credit(2, 2, "fp-b", person("Петров", "Иван")).
		book(11, "Книга В").credit(11, 11, "fp-c", person("Петров", "Иван", "Сергеевич").latin()).
		book(31, "Книга Д").credit(31, 31, "fp-e", person("Петров", "И"))
	w := newWorld(t, ch)
	w.pass()
	id := w.decide(authorentity.CandidateNameForm, 31, 1)
	same(t, w.pass(), 31, 1)
	require.Equal(t, applied(id), w.state(id))

	ch.c.book(41, "Книга Е").credit(41, 41, "fp-f", person("Петров", "Игорь", "Павлович"))
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	same(t, p, 31, 1)
	apart(t, p, 31, 41)
	assert.Nil(t, candidateOf(w.level0, authorentity.CandidateNameForm, 31, 41), "the namesake's pair is no candidate")
	assert.Zero(t, w.pass().Changes(), "a second pass changes nothing")
}

// Nothing was accepted: a pair ambiguous from the start is never asked, a
// decision of another pair admits nothing.
func TestAnAmbiguousNameFormStaysOutWithNoDecision(t *testing.T) {
	ch := twoNames(t, person("Олдридж", "Дж"), person("Олдридж", "Джеймс"))
	ch.c.book(3, "Книга В").credit(3, 3, "fp-c", person("Олдридж", "Джон"))
	w := newWorld(t, ch)
	w.pass()
	assert.Nil(t, candidateOf(w.level0, authorentity.CandidateNameForm, 1, 2))
	assert.Nil(t, candidateOf(w.level0, authorentity.CandidateNameForm, 1, 3))
}

// A card that changed while the pair is ambiguous is a new question, and an
// ambiguous pair is never asked: the decision lapses, the pair parts and no
// candidate is left for a run to ask.
func TestAKeptNameFormLapsesOnAChangedCard(t *testing.T) {
	w := oldridges(t)
	w.pass()
	id := w.decide(authorentity.CandidateNameForm, 1, 2)
	w.pass()
	w.ch.c.book(3, "Книга В").credit(3, 3, "fp-c", person("Олдридж", "Джон"))
	w.pass()
	require.Equal(t, applied(id), w.state(id))

	w.ch.c.book(5, "Книга Д").credit(5, 5, "fp-a", person("Олдридж", "Дж"))
	p := w.pass()
	assert.Equal(t, authorentity.DecisionLapsed, w.state(id).State)
	assert.Equal(t, authorentity.ExcludedNameFormAmbiguous, w.state(id).Reason)
	assert.Nil(t, candidateOf(w.level0, authorentity.CandidateNameForm, 1, 2))
	apart(t, p, 1, 2)
}

// A withdrawn registration wins: the kept pair lapses as any decision of the
// kind does.
func TestAWithheldNameFormIsNotKept(t *testing.T) {
	w := oldridges(t)
	w.pass()
	id := w.decide(authorentity.CandidateNameForm, 1, 2)
	w.pass()
	w.ch.c.book(3, "Книга В").credit(3, 3, "fp-c", person("Олдридж", "Джон"))
	for i := range w.decisions {
		w.decisions[i].Withheld = "registration_withdrawn"
	}
	p := w.pass()
	assert.Equal(t, authorentity.DecisionLapsed, w.state(id).State)
	apart(t, p, 1, 2)
	assert.Nil(t, candidateOf(w.level0, authorentity.CandidateNameForm, 1, 2))
}
