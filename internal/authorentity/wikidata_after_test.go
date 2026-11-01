package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E4f, Wikidata labels (2): the Wikidata step after the decisions joins the
// entities, as they stand once the decisions are applied and the bucket
// books moved, that one item names by any of their credits' names; a
// pseudonym keeps apart only an entity it names. The candidates are sealed
// before it: no decision changes state.

// withSwap adds a swap pair a decision joins, so that a pass enters its
// decision step: «Кашин Ярослав» (credit 90) and «Ярослав Кашин» (91).
func withSwap(c *catalog) {
	c.book(90, "Записки").book(91, "Дневник").
		credit(90, 90, "fp-swap", person("Кашин", "Ярослав")).
		credit(91, 91, "fp-swap-swapped", person("Ярослав", "Кашин"))
}

// zelazny: the full name «Роджер Джозеф Желязны» on books 1 and 5 — its
// display name, no label — the short name «Роджер Желязны» on books 2 and
// 3 (its bucket), «Roger Zelazny» on books 4, 6, 7 and 8 (the larger side,
// which a join keeps).
func zelazny(t *testing.T) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Князь Света").book(2, "Хроники Амбера").book(3, "Остров мёртвых").book(4, "Lord of Light").
		book(5, "Джек-из-Тени").book(6, "Nine Princes in Amber").book(7, "This Immortal").book(8, "Isle of the Dead").
		credit(1, 1, "fp-full", person("Желязны", "Роджер", "Джозеф")).
		credit(5, 5, "fp-full", person("Желязны", "Роджер", "Джозеф")).
		credit(2, 2, "fp-short", person("Желязны", "Роджер")).
		credit(3, 3, "fp-short", person("Желязны", "Роджер"))
	for _, id := range []int64{4, 6, 7, 8} {
		ch.c.credit(id, id, "fp-en", shown("Roger Zelazny", person("Zelazny", "Roger").latin()))
	}
	withSwap(ch.c)
	return ch
}

// zelaznyItem is Q295406: two main labels and a pseudonym (P742).
func zelaznyItem() *reference {
	return newReference().item("Q295406", []string{"Роджер Желязны", "Roger Zelazny"}, "pseudonym:Harrison Denmark")
}

// decidedWorld is a world over ch with the reference, the swap decided and
// the bucket book 2 moved to the full name: the pass after them.
func decidedWorld(t *testing.T, ch *churn, r *reference) *world {
	ch.c.with(r)
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateSwap, 90, 91)
	return w
}

// The short name's main label sits on its bucket at level 0, which rule 5
// keeps out, and the item records a pseudonym: level 0 joins nothing. Once
// a book of the bucket moved to the full name, the full name goes by the
// label too, and the step joins it with «Roger Zelazny»; the book left in
// the bucket stays there. Every decision stays applied.
func TestTheStepJoinsAFullNameByTheNameOfItsMovedBook(t *testing.T) {
	w := decidedWorld(t, zelazny(t), zelaznyItem())
	apart(t, w.last, 1, 4)
	book := w.decide(authorentity.CandidateBucket, 2, 1)
	p := w.pass()
	same(t, p, 1, 2)
	same(t, p, 1, 4)
	same(t, p, 2, 4)
	apart(t, p, 3, 1)
	assert.Equal(t, "model:bucket", p.RuleOf(2), "the moved book keeps its decision's row")
	for _, s := range p.DecisionStates() {
		assert.Equal(t, authorentity.DecisionApplied, s.State, "decision %d", s.DecisionID)
	}
	assert.Equal(t, applied(book), w.state(book))
	require.NotNil(t, p.Report.Model.Wikidata)
	assert.Equal(t, map[string]int{authorentity.WikidataCrossScript: 1}, p.Report.Model.Wikidata.Merges)
	assert.Equal(t, map[string]int{authorentity.WikidataExcludedShortName: 1}, p.Report.Model.Wikidata.Excluded)
	assert.Equal(t, "wikidata:Q295406@"+extract, p.RuleOf(1), "the absorbed side carries the evidence")
	assert.Equal(t, p.EntityOf(4), w.level0.EntityOf(4), "the larger side keeps its ID")
	assert.Zero(t, w.pass().Changes(), "the next pass changes nothing")
}

// A pseudonym keeps apart the entity it names, never the item's others:
// «Agatha Mallowan», a main label and a P742 of Q35064, stays apart while
// «Агата Кристи» and «Agatha Christie» are joined.
func TestTheStepKeepsOnlyThePseudonymApart(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Убийство в Восточном экспрессе").book(2, "Murder on the Orient Express").book(3, "Come, Tell Me").
		credit(1, 1, "ru", person("Кристи", "Агата")).
		credit(2, 2, "en", shown("Agatha Christie", person("Christie", "Agatha").latin())).
		credit(3, 3, "pen", shown("Agatha Mallowan", person("Mallowan", "Agatha").latin()))
	withSwap(ch.c)
	w := decidedWorld(t, ch, newReference().item("Q35064", []string{"Агата Кристи", "Agatha Christie",
		"Agatha Mallowan"}, "pseudonym:Agatha Mallowan"))
	apart(t, w.last, 1, 2)
	p := w.pass()
	same(t, p, 1, 2)
	apart(t, p, 1, 3)
	assert.Equal(t, map[string]int{authorentity.WikidataExcludedPseudonym: 1}, p.Report.Model.Wikidata.Excluded)
}

// The rule's holds after the decisions as at level 0: a namesake tag, an
// initial, one key per script — the reviewed counterexamples stay apart
// (Mary Jones / Beth Smith through a label of another language; Марианна
// Алферова / Roman Burevoj through a Czech label of the pen name).
func TestTheStepKeepsTheReviewedCounterexamplesApart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(c *catalog) *reference
		pairs [][2]int64
	}{
		{"a namesake tag", func(c *catalog) *reference {
			c.book(1, "Оно").book(2, "It").
				credit(1, 1, "ru", person("Кинг", "Стивен").withTag(2)).
				credit(2, 2, "en", shown("Stephen King", person("King", "Stephen").latin()))
			return newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})
		}, [][2]int64{{1, 2}}},
		{"an initial", func(c *catalog) *reference {
			c.book(1, "Оно").book(2, "It").
				credit(1, 1, "ru", shown("С. Кинг", person("Кинг", "С."))).
				credit(2, 2, "en", shown("Stephen King", person("King", "Stephen").latin()))
			return newReference().item("Q39829", []string{"С. Кинг", "Stephen King"})
		}, [][2]int64{{1, 2}}},
		{"r6-1 Mary Jones / Beth Smith", func(c *catalog) *reference {
			c.book(1, "One", 900).book(2, "Two").book(3, "Three", 900).
				credit(1, 1, "mary", shown("Mary Jones", person("Jones", "Mary").latin())).
				credit(2, 2, "mary", shown("Mary Jones", person("Jones", "Mary").latin())).
				credit(3, 3, "beth", shown("Beth Smith", person("Smith", "Beth").latin()))
			return newReference().item("Q1", []string{"Mary Jones", "Anna Smith"}, "pseudonym:Ann Smith",
				"pseudonym:Annalise Smith", "label:Beth Smith")
		}, [][2]int64{{1, 3}}},
		{"r1-1 Алферова / Roman Burevoj", func(c *catalog) *reference {
			c.book(1, "Одна", 7).book(2, "Другая", 7).
				credit(1, 1, "alferova", shown("Марианна Алферова", person("Алферова", "Марианна"))).
				credit(2, 2, "burevoj", shown("Roman Burevoj", person("Burevoj", "Roman").latin()))
			return newReference().item("Q124752133", []string{"Марианна Алферова"}, "label:Roman Burevoj",
				"pseudonym:Роман Буревой")
		}, [][2]int64{{1, 2}}},
		{"r2-1 Иван Иванов / Иван Петров, a pen name", func(c *catalog) *reference {
			c.book(1, "Один", 4).book(2, "Два", 4).
				credit(1, 1, "birth", person("Иванов", "Иван")).
				credit(2, 2, "pen", person("Петров", "Иван"))
			return newReference().item("Q5002", []string{"Иван Иванов", "Иван Петров"}, "pseudonym:Иван Петров")
		}, [][2]int64{{1, 2}}},
		{"two keys of one script", func(c *catalog) *reference {
			c.book(1, "Оно").book(2, "Сияние").
				credit(1, 1, "a", person("Кинг", "Стивен")).
				credit(2, 2, "b", person("Кинг", "Стив"))
			return newReference().item("Q39829", []string{"Стивен Кинг", "Стив Кинг"})
		}, [][2]int64{{1, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
			r := tc.build(ch.c)
			withSwap(ch.c)
			w := decidedWorld(t, ch, r)
			p := w.pass()
			require.NotNil(t, p.Report.Model)
			for _, pair := range tc.pairs {
				apart(t, p, pair[0], pair[1])
			}
		})
	}
}

// Two entities the production pair answered other than same on are never
// joined by the step: «Рэй Дуглас Брэдбери» against «Ray Bradbury», a
// translit candidate.
func TestTheStepNeverJoinsANotSamePair(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Марсианские хроники").book(2, "Вино из одуванчиков").book(3, "Канун всех святых").
		book(4, "Fahrenheit 451").
		credit(1, 1, "fp-full", person("Брэдбери", "Рэй", "Дуглас")).
		credit(2, 2, "fp-short", person("Брэдбери", "Рэй")).
		credit(3, 3, "fp-short", person("Брэдбери", "Рэй")).
		credit(4, 4, "fp-en", shown("Ray Bradbury", person("Bradbury", "Ray").latin()))
	withSwap(ch.c)
	w := decidedWorld(t, ch, newReference().item("Q40640", []string{"Рэй Брэдбери", "Ray Bradbury"},
		"pseudonym:William Elliott"))
	w.decide(authorentity.CandidateBucket, 2, 1)
	p := w.pass()
	same(t, p, 1, 4)
	translit := candidateOf(w.level0, authorentity.CandidateTranslit, 1, 4)
	require.NotNil(t, translit, "«Рэй Дуглас Брэдбери» against «Ray Bradbury»")
	ch.c.in.NotSame = map[[32]byte]bool{translit.Digest: true}
	p = w.pass()
	apart(t, p, 1, 4)
	assert.Equal(t, map[string]int{authorentity.WikidataExcludedShortName: 1, authorentity.WikidataExcludedNotSame: 1},
		p.Report.Model.Wikidata.Excluded)
}

// Two full Cyrillic patronymics of one family that are not one name are
// never joined by the step (a father and a son one item conflates), and two
// that are — «Юрьевич» and «Юриевич» — are: a bucket book two_targets held
// between them moves to the one entity, its decisions applied.
func TestTheStepHoldsTwoPatronymicsAndClearsTwoTargets(t *testing.T) {
	build := func(t *testing.T, other string) (*world, int64, int64) {
		ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
		ch.c.book(1, "Первая").book(2, "Вторая").book(3, "Третья").book(4, "Четвёртая").book(5, "Пятая").
			credit(1, 1, "fp-a", person("Раззаков", "Федор", "Юрьевич")).
			credit(2, 2, "fp-b", person("Раззаков", "Федор", other)).
			credit(3, 3, "fp-short", person("Раззаков", "Федор")).
			credit(4, 4, "fp-short", person("Раззаков", "Федор")).
			credit(5, 5, "fp-short", person("Раззаков", "Федор"))
		withSwap(ch.c)
		w := decidedWorld(t, ch, newReference().item("Q4", []string{"Федор Раззаков"}))
		w.decide(authorentity.CandidateBucket, 3, 1)
		w.decide(authorentity.CandidateBucket, 4, 2)
		toA := w.decide(authorentity.CandidateBucket, 5, 1)
		toB := w.decide(authorentity.CandidateBucket, 5, 2)
		return w, toA, toB
	}
	t.Run("two names", func(t *testing.T) {
		w, toA, toB := build(t, "Игоревич")
		p := w.pass()
		apart(t, p, 1, 2)
		assert.Equal(t, conflict(toA, authorentity.ConflictTwoTargets), w.state(toA))
		assert.Equal(t, conflict(toB, authorentity.ConflictTwoTargets), w.state(toB))
		assert.Equal(t, 1, p.Report.Model.Wikidata.Excluded[authorentity.WikidataExcludedGivenName])
	})
	t.Run("one name", func(t *testing.T) {
		w, toA, toB := build(t, "Юриевич")
		p := w.pass()
		same(t, p, 1, 2)
		same(t, p, 5, 1)
		assert.Equal(t, applied(toA), w.state(toA))
		assert.Equal(t, applied(toB), w.state(toB))
		assert.Equal(t, 1, p.Report.Model.TwoTargetsJoined)
		assert.Zero(t, p.Report.Model.Conflict[authorentity.ConflictTwoTargets])
		assert.Zero(t, w.pass().Changes(), "the next pass changes nothing")
	})
}

// An item the deny list names joins nothing after the decisions: the way a
// wrong merge the hand check finds is undone, the next pass splitting it.
func TestADeniedItemJoinsNothing(t *testing.T) {
	w := decidedWorld(t, zelazny(t), zelaznyItem())
	w.decide(authorentity.CandidateBucket, 2, 1)
	p := w.pass()
	same(t, p, 1, 4)
	restore := authorentity.DenyWikidataAfter("Q295406")
	defer restore()
	p = w.pass()
	apart(t, p, 1, 4)
	same(t, p, 1, 2)
	assert.Empty(t, p.Report.Model.Wikidata.Merges)
}

// A book moved into an entity the step joins into another follows it: here
// the Latin full name «Roger Joseph Zelazny», whose bucket book «Roger
// Zelazny» moved in, joins «Роджер Желязны», whose key comes first.
func TestAMovedBookFollowsItsEntityIntoTheJoin(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Lord of Light").book(2, "This Immortal").book(3, "Isle of the Dead").book(4, "Doorways in the Sand").
		book(5, "Князь Света").
		credit(1, 1, "fp-full", shown("Roger Joseph Zelazny", person("Zelazny", "Roger", "Joseph").latin())).
		credit(2, 2, "fp-full", shown("Roger Joseph Zelazny", person("Zelazny", "Roger", "Joseph").latin())).
		credit(3, 3, "fp-short", shown("Roger Zelazny", person("Zelazny", "Roger").latin())).
		credit(4, 4, "fp-short", shown("Roger Zelazny", person("Zelazny", "Roger").latin())).
		credit(5, 5, "fp-ru", person("Желязны", "Роджер"))
	withSwap(ch.c)
	w := decidedWorld(t, ch, zelaznyItem())
	moved := w.decide(authorentity.CandidateBucket, 3, 1)
	p := w.pass()
	assert.Equal(t, applied(moved), w.state(moved))
	same(t, p, 1, 5)
	same(t, p, 3, 5)
	apart(t, p, 4, 5)
	assert.Equal(t, "model:bucket", p.RuleOf(3))
}

// An entity two of whose names are main labels of two items is named by
// neither; a collective is never named.
func TestTheStepNamesNoEntityOfTwoItemsAndNoCollective(t *testing.T) {
	ch := zelazny(t)
	ch.c.book(9, "Сборник").collective(9, 9, "fp-coll", "Роджер Желязны").
		book(10, "Creatures of Light").credit(10, 10, "fp-en-full",
		shown("Roger Joseph Zelazny", person("Zelazny", "Roger", "Joseph").latin()))
	w := decidedWorld(t, ch, zelaznyItem().item("Q2", []string{"Роджер Джозеф Желязны", "Roger Joseph Zelazny"},
		"pseudonym:Harrison Denmark Jr"))
	w.decide(authorentity.CandidateBucket, 2, 1)
	p := w.pass()
	apart(t, p, 1, 4)
	apart(t, p, 1, 10)
	apart(t, p, 9, 4)
	assert.Empty(t, p.Report.Model.Wikidata.Merges)
}
