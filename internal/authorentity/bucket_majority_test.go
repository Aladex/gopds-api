package authorentity_test

import (
	"fmt"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bucket majority (E4j): a short name's bucket whose moved books all
// went to the one full name of that short name — at least three of them and
// no fewer than stay — gives that full name the rest of its books, after
// the decisions.

// kingBucket is a full name of 6 books and its short name's bucket: moved
// books share a title with the full name (D3), the rest share nothing. An
// unrelated swap pair gets a decision, so the pass has its decision step.
func kingBucket(t *testing.T, moved, rest int) (*world, *churn) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := int64(1); i <= 6; i++ {
		ch.c.book(i, fmt.Sprintf("Роман %d", i)).credit(i, i, "fp-full", person("Кинг", "Стивен", "Эдвин"))
	}
	for i := int64(0); i < int64(moved); i++ {
		ch.c.book(100+i, fmt.Sprintf("Роман %d", i+1)).credit(100+i, 100+i, "fp-short", person("Кинг", "Стивен"))
	}
	for i := int64(0); i < int64(rest); i++ {
		ch.c.book(200+i, fmt.Sprintf("Рассказ %d", i)).credit(200+i, 200+i, "fp-short", person("Кинг", "Стивен"))
	}
	ch.c.book(900, "Мост").credit(900, 900, "fp-swap-a", person("Петров", "Иван")).
		book(901, "Река").credit(901, 901, "fp-swap-b", person("Иван", "Петров"))
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateSwap, 900, 901)
	return w, ch
}

func TestABucketItsOneFullNameTookMostOfGivesItTheRest(t *testing.T) {
	w, _ := kingBucket(t, 3, 3)
	p := w.pass()
	for _, id := range []int64{100, 200, 201, 202} {
		assert.Equal(t, p.EntityOf(1), p.EntityOf(id), "credit %d", id)
	}
	assert.Equal(t, "D3:majority", p.RuleOf(200))
	row, ok := w.s.Credits[authorentity.CreditKey{CreditID: 200}]
	require.True(t, ok, "the rest's books are stored as credit rows")
	assert.Equal(t, authorentity.MethodEvidence, row.Method)
	assert.Equal(t, 3, p.Report.Buckets.MajorityBooks)
	assert.Equal(t, 1, p.Report.Buckets.MajorityBuckets)
	// Level 0, which the cards are sealed from, keeps the bucket.
	assert.NotEqual(t, w.level0.EntityOf(1), w.level0.EntityOf(200))
	assert.Zero(t, w.pass().Changes())
}

func TestABucketKeepsItsRestUnderTheMajority(t *testing.T) {
	for title, c := range map[string]struct{ moved, rest int }{
		"two moved":            {moved: 2, rest: 1},
		"more stay than moved": {moved: 3, rest: 4},
	} {
		t.Run(title, func(t *testing.T) {
			w, _ := kingBucket(t, c.moved, c.rest)
			p := w.pass()
			assert.NotEqual(t, p.EntityOf(1), p.EntityOf(200))
			assert.Equal(t, p.EntityOf(1), p.EntityOf(100), "D3 still moves the books it has evidence for")
		})
	}
}

func TestANamesakeShortNameKeepsItsBucket(t *testing.T) {
	w, ch := kingBucket(t, 3, 3)
	// A second full name of the short name, another person of three books.
	for i := int64(50); i < 53; i++ {
		ch.c.book(i, fmt.Sprintf("Записки %d", i)).credit(i, i, "fp-other", person("Кинг", "Стивен", "Петрович"))
	}
	w.pass()
	p := w.pass()
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(50))
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(200))
	assert.NotEqual(t, p.EntityOf(50), p.EntityOf(200))
}

func TestABucketBookThatTheMajorityMayNotTakeStays(t *testing.T) {
	// Four moved, four stay: the two kept books count among those that stay.
	w, ch := kingBucket(t, 4, 2)
	ch.c.book(300, "Песни [СИ]").credit(300, 300, "fp-short", person("Кинг", "Стивен"))
	// The full name stands on this book itself: the short name is someone else.
	ch.c.book(301, "Сборник").credit(301, 301, "fp-short", person("Кинг", "Стивен")).
		credit(302, 301, "fp-full", person("Кинг", "Стивен", "Эдвин"))
	w.pass()
	p := w.pass()
	assert.Equal(t, p.EntityOf(1), p.EntityOf(200))
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(300), "a samizdat book")
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(301), "a book of the full name")
}

func TestAVetoedBucketBookStaysOutOfTheMajority(t *testing.T) {
	w, ch := kingBucket(t, 3, 2)
	ch.c.bookVeto(1, 201, "fp-short", []bookFP{{1, "fp-full"}})
	ch.c.in.VetoBooks = map[int64][]string{201: {"fp-short"}, 1: {"fp-full"}}
	w.pass()
	p := w.pass()
	assert.Equal(t, p.EntityOf(1), p.EntityOf(200))
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(201), "the vetoed book")
}

// A spelling of the full name is the same person for the majority (E4j
// round 2: «Роберт Ирвин / Эрвин Говард», «Василий В. Головачев»); any other
// full name, one book or more, is a namesake.
func TestASpellingOfTheFullNameIsNoNamesake(t *testing.T) {
	for title, c := range map[string]struct {
		additional string
		books      int64
		joins      bool
	}{
		"a typo of the middle name":        {additional: "Эдвен", books: 3, joins: true},
		"the initial of the middle name":   {additional: "Э.", books: 3, joins: true},
		"the given name again, a misparse": {additional: "Стивен", books: 1, joins: true},
		"another middle name, one book":    {additional: "Петрович", books: 1},
		"another middle name, two books":   {additional: "Ильич", books: 2},
	} {
		t.Run(title, func(t *testing.T) {
			w, ch := kingBucket(t, 3, 3)
			for i := int64(0); i < c.books; i++ {
				ch.c.book(50+i, fmt.Sprintf("Записки %d", i)).credit(50+i, 50+i, "fp-other",
					person("Кинг", "Стивен", c.additional))
			}
			w.pass()
			p := w.pass()
			assert.Equal(t, c.joins, p.EntityOf(1) == p.EntityOf(200), "the rest joins the dominant full name")
			assert.NotEqual(t, p.EntityOf(1), p.EntityOf(50), "the other full name stays its own")
		})
	}
}

// largeBucket is kingBucket with a full name of as many books as moved.
func largeBucket(t *testing.T, moved, rest int) *world {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := int64(1); i <= int64(moved); i++ {
		ch.c.book(i, fmt.Sprintf("Роман %d", i)).credit(i, i, "fp-full", person("Кинг", "Стивен", "Эдвин"))
		ch.c.book(100+i, fmt.Sprintf("Роман %d", i)).credit(100+i, 100+i, "fp-short", person("Кинг", "Стивен"))
	}
	for i := int64(0); i < int64(rest); i++ {
		ch.c.book(200+i, fmt.Sprintf("Рассказ %d", i)).credit(200+i, 200+i, "fp-short", person("Кинг", "Стивен"))
	}
	ch.c.book(900, "Мост").credit(900, 900, "fp-swap-a", person("Петров", "Иван")).
		book(901, "Река").credit(901, 901, "fp-swap-b", person("Иван", "Петров"))
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateSwap, 900, 901)
	return w
}

// Ten moved books are a majority at a third of the bucket (E4n): fewer, or
// less than half of what stays, are not.
func TestTenMovedBooksAreAMajorityAtAThird(t *testing.T) {
	for title, c := range map[string]struct {
		moved, rest int
		joins       bool
	}{
		"ten moved, twenty stay":     {moved: 10, rest: 20, joins: true},
		"nine moved, eighteen stay":  {moved: 9, rest: 18},
		"ten moved, twenty-one stay": {moved: 10, rest: 21},
	} {
		t.Run(title, func(t *testing.T) {
			p := largeBucket(t, c.moved, c.rest).pass()
			assert.Equal(t, p.EntityOf(1), p.EntityOf(101), "D3 moves the books it has evidence for")
			assert.Equal(t, c.joins, p.EntityOf(1) == p.EntityOf(200))
		})
	}
}

// An unselected credit of the short name joins the full name its emptied
// bucket went to (E4n): the name goes by the entity its books moved to.
func TestAnEmptiedBucketsNameJoinsWhereItsBooksWent(t *testing.T) {
	w, ch := kingBucket(t, 3, 3)
	ch.c.book(400, "Сборник").unselected(400, 400, "fp-review", "case_defect", flags("all_caps"),
		person("КИНГ", "СТИВЕН")).done()
	w.pass()
	p := w.pass()
	assert.Equal(t, p.EntityOf(1), p.EntityOf(200), "the majority empties the bucket")
	assert.Equal(t, p.EntityOf(1), p.EntityOf(400))
}

// The bucket majority runs once, over the components the decisions made,
// before the later equivalence rules (E4z review, missing test 5). A short
// key the marks step joins to the bucket afterwards — «Джон О`Хара» beside
// «Джон О'Хара» — neither counts toward the majority nor follows it: its
// books stay on the bucket's entity, apart from the full name, while the
// majority's own moves stand. The outcome is a fixed point: a second pass
// changes nothing. Reordering the steps changes this (the joined key's books
// would count as staying), so a change here is a change of semantics.
func TestABucketMajorityIsDecidedBeforeTheMarksJoin(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := int64(1); i <= 6; i++ {
		ch.c.book(i, fmt.Sprintf("Роман %d", i)).credit(i, i, "fp-full", person("О'Хара", "Джон", "Генри"))
	}
	for i := int64(0); i < 3; i++ {
		ch.c.book(100+i, fmt.Sprintf("Роман %d", i+1)).credit(100+i, 100+i, "fp-short", person("О'Хара", "Джон"))
		ch.c.book(200+i, fmt.Sprintf("Рассказ %d", i)).credit(200+i, 200+i, "fp-short", person("О'Хара", "Джон"))
	}
	ch.c.book(300, "Очерк").credit(300, 300, "fp-tick", person("О`Хара", "Джон"))
	ch.c.book(900, "Мост").credit(900, 900, "fp-swap-a", person("Петров", "Иван")).
		book(901, "Река").credit(901, 901, "fp-swap-b", person("Иван", "Петров"))
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateSwap, 900, 901)
	p := w.pass()

	// The majority: three moved, three stay, the rest goes to the full name.
	for _, id := range []int64{100, 200, 201, 202} {
		same(t, p, 1, id)
	}
	assert.Equal(t, "D3:majority", p.RuleOf(200))
	assert.Equal(t, 3, p.Report.Buckets.MajorityBooks)
	// The marks step joins the backtick key to the bucket, not to the full
	// name, and leaves the majority's moves where they are.
	assert.Equal(t, 1, p.Report.Model.Marks)
	assert.Equal(t, authorentity.RuleMarks, p.RuleOf(300))
	apart(t, p, 1, 300)
	assert.Zero(t, w.pass().Changes(), "the order of the steps is a fixed point")
}
