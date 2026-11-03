package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
)

// petrovs: «Евгений Петров» on books 1 and 2, «Евгений Петрович Петров» on
// book 11 and «Евгений Александрович Петров» on book 21; no title, series
// or co-author links them, so the bucket keeps both short books.
func petrovs(t *testing.T) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Книга А").credit(1, 1, "fp-a", person("Петров", "Евгений")).
		book(2, "Книга Б").credit(2, 2, "fp-b", person("Петров", "Евгений")).
		book(11, "Книга В").credit(11, 11, "fp-c", person("Петров", "Евгений", "Петрович")).
		book(21, "Книга Г").credit(21, 21, "fp-d", person("Петров", "Евгений", "Александрович"))
	return ch
}

// The orphan asks a short name with each full name of it, and every side
// with the side of most books: two full names of other patronymics too.
func TestOrphanCandidates(t *testing.T) {
	p := sealed(t, petrovs(t).c, nil)
	requireCandidate(t, p, authorentity.CandidateOrphan, "Cyrl", 1, 11)
	requireCandidate(t, p, authorentity.CandidateOrphan, "Cyrl", 1, 21)
	assert.Nil(t, candidateOf(p, authorentity.CandidateOrphan, 11, 21), "two full names apart from the main")
	assert.Equal(t, 2, counted(p, authorentity.CandidateOrphan, "Cyrl"))

	p = sealed(t, distinctNames(t, person("Алексеев", "Михаил", "Егорович"),
		person("Алексеев", "Михаил", "Алексеевич"), person("Алексеев", "Михаил", "Алексеевич")).c, nil)
	requireCandidate(t, p, authorentity.CandidateOrphan, "Cyrl", 1, 11)
}

// A same joins the short name's entity with the full name's, every book of
// it, though the bucket kind asked them one by one; the rule is the
// smaller entity's.
func TestAnOrphanSameJoinsTheEntities(t *testing.T) {
	w := newWorld(t, petrovs(t))
	p := w.pass()
	apart(t, p, 1, 11)
	same(t, p, 1, 2)
	id := w.decide(authorentity.CandidateOrphan, 1, 11)
	p = w.pass()
	assert.Equal(t, applied(id), w.state(id))
	same(t, p, 1, 11)
	same(t, p, 2, 11)
	apart(t, p, 1, 21)
	assert.Equal(t, "model:orphan", p.RuleOf(11))
	assert.NotEqual(t, "model:orphan", p.RuleOf(1))
}

// Sames of one short name for two full names hold both: the two are
// another question.
func TestTwoOrphanTargetsHoldBoth(t *testing.T) {
	w := newWorld(t, petrovs(t))
	w.pass()
	a := w.decide(authorentity.CandidateOrphan, 1, 11)
	b := w.decide(authorentity.CandidateOrphan, 1, 21)
	p := w.pass()
	assert.Equal(t, conflict(a, authorentity.ConflictTwoTargets), w.state(a))
	assert.Equal(t, conflict(b, authorentity.ConflictTwoTargets), w.state(b))
	apart(t, p, 1, 11)
	apart(t, p, 1, 21)
}
