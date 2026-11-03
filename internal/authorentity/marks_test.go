package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
)

// The marks of a name (E4p): «Джон О`Хара» and «Джон О'Хара» are one
// written name, joined after the decisions; another name stays apart.
func TestABacktickAndAnApostropheAreOneName(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Свидание в Самарре").book(2, "Дорогой Джо").book(3, "Река").
		credit(1, 1, "fp-quote", person("О'Хара", "Джон")).
		credit(2, 2, "fp-tick", person("О`Хара", "Джон")).
		credit(3, 3, "fp-other", person("О`Нил", "Джон"))
	withSwap(ch.c)
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateSwap, 90, 91)
	p := w.pass()
	same(t, p, 1, 2)
	apart(t, p, 1, 3)
	assert.Equal(t, authorentity.RuleMarks, p.RuleOf(2))
	assert.Equal(t, 1, p.Report.Model.Marks)
}
