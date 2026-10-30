package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A model decision never joins two people a short form bridges (E4c, prod
// build 22): «В. Савченко» paired with «Виталий Савченко» and with
// «Владимир Иванович Савченко» put Vitaly and Vladimir into one entity.
// Each pair was right on its own; their union is not. And a conflict holds
// only the decisions that would bridge it, in a stable order — the ones
// applied in the previous pass first — so a later kind never splits a live
// merge.

// threeNames is a catalog of one credit per name, on books 1, 11 and 21.
func threeNames(t *testing.T, a, b, c *name) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i, n := range []*name{a, b, c} {
		id := int64(1 + 10*i)
		ch.c.book(id, "Книга").credit(id, id, "fp-"+string(rune('a'+i)), n)
	}
	return ch
}

// savchenkos: «Виталий Савченко» (1), «В. Савченко» (11), «Владимир
// Иванович Савченко» (21).
func savchenkos(t *testing.T) *churn {
	return threeNames(t, person("Савченко", "Виталий"), person("Савченко", "В."),
		person("Савченко", "Владимир", "Иванович"))
}

// The Savchenko shape: of the two initials decisions on «В. Савченко», the
// first in order applies; the second would join Vitaly and Vladimir and is
// held (given_name). The two full names stay apart.
func TestAShortFormBridgesNoTwoFullGivenNames(t *testing.T) {
	w := newWorld(t, savchenkos(t))
	w.pass()
	vladimir := w.decide(authorentity.CandidateInitials, 11, 21)
	vitaly := w.decide(authorentity.CandidateInitials, 11, 1)
	p := w.pass()
	assert.Equal(t, applied(vladimir), w.state(vladimir))
	assert.Equal(t, conflict(vitaly, authorentity.ConflictGivenName), w.state(vitaly))
	same(t, p, 11, 21)
	apart(t, p, 1, 21)
	assert.Equal(t, 1, p.Report.Model.Conflict[authorentity.ConflictGivenName])
}

// Two full patronymics are two people too: «Алексей Викторович Иванов» and
// «Алексей Александрович Иванов» are never joined through «А. Иванов».
func TestAShortFormBridgesNoTwoFullPatronymics(t *testing.T) {
	w := newWorld(t, threeNames(t, person("Иванов", "Алексей", "Викторович"), person("Иванов", "А."),
		person("Иванов", "Алексей", "Александрович")))
	w.pass()
	first := w.decide(authorentity.CandidateInitials, 11, 1)
	second := w.decide(authorentity.CandidateInitials, 11, 21)
	p := w.pass()
	assert.Equal(t, applied(first), w.state(first))
	assert.Equal(t, conflict(second, authorentity.ConflictGivenName), w.state(second))
	apart(t, p, 1, 21)
}

// The order is stable: a decision applied in the previous pass comes first
// whatever its id, then the rest by registration and id. Vitaly's decision,
// applied alone, keeps its state when Vladimir's — a lower id — comes
// later; and among new decisions an earlier registration wins over a
// lower id.
func TestAnAppliedDecisionKeepsItsStateAgainstALaterOne(t *testing.T) {
	w := newWorld(t, savchenkos(t))
	w.pass()
	w.nextID = 100
	vitaly := w.decide(authorentity.CandidateInitials, 11, 1)
	w.pass()
	require.Equal(t, applied(vitaly), w.state(vitaly))
	w.nextID = 0
	vladimir := w.decide(authorentity.CandidateInitials, 11, 21)
	require.Less(t, vladimir, vitaly)
	p := w.pass()
	assert.Equal(t, applied(vitaly), w.state(vitaly))
	assert.Equal(t, conflict(vladimir, authorentity.ConflictGivenName), w.state(vladimir))
	same(t, p, 1, 11)

	w2 := newWorld(t, savchenkos(t))
	w2.pass()
	low := w2.decide(authorentity.CandidateInitials, 11, 21)
	high := w2.decide(authorentity.CandidateInitials, 11, 1)
	w2.register(low, 2)
	w2.register(high, 1)
	w2.pass()
	assert.Equal(t, applied(high), w2.state(high), "the earlier registration")
	assert.Equal(t, conflict(low, authorentity.ConflictGivenName), w2.state(low))
}

// The Asimov shape: «Айзек Азимов» and «Исаак Азимов», each joined to
// «Isaac Asimov» by translit, are a live component (prod). It stays
// applied, and «А. Азимов» joins it: a side with no full name adds nothing
// new. A new translit decision joining the two spellings is held: they are
// two names to the strict comparison, and being too strict only holds a
// decision.
func TestTranslitSpellingsOfOneGivenNameStayApplied(t *testing.T) {
	ch := threeNames(t, person("Азимов", "Айзек"), person("Азимов", "Исаак"), person("Asimov", "Isaac").latin())
	ch.c.book(31, "Книга").credit(31, 31, "fp-d", person("Азимов", "А."))
	w := newWorld(t, ch)
	w.pass()
	a := w.decide(authorentity.CandidateTranslit, 1, 21)
	b := w.decide(authorentity.CandidateTranslit, 11, 21)
	for i := range w.decisions {
		w.decisions[i].WasApplied = true
	}
	initials := w.decide(authorentity.CandidateInitials, 31, 1)
	p := w.pass()
	assert.Equal(t, applied(a), w.state(a))
	assert.Equal(t, applied(b), w.state(b))
	assert.Equal(t, applied(initials), w.state(initials))
	same(t, p, 1, 11)
	same(t, p, 1, 31)

	w2 := newWorld(t, threeNames(t, person("Азимов", "Айзек"), person("Азимов", "Исаак"),
		person("Asimov", "Isaac").latin()))
	w2.pass()
	first := w2.decide(authorentity.CandidateTranslit, 1, 21)
	second := w2.decide(authorentity.CandidateTranslit, 11, 21)
	w2.pass()
	assert.Equal(t, applied(first), w2.state(first))
	assert.Equal(t, conflict(second, authorentity.ConflictGivenName), w2.state(second))
}

// The Bradbury shape: «Рэй Брэдбери», «Ray Bradbury» (translit) and the
// swapped «Брэдбери Рэй» (swap) are one person: a swapped name's given
// name is the other's family name, never compared with it.
func TestASwappedNameStaysApplied(t *testing.T) {
	w := newWorld(t, threeNames(t, person("Брэдбери", "Рэй"), person("Bradbury", "Ray").latin(),
		person("Рэй", "Брэдбери")))
	w.pass()
	tr := w.decide(authorentity.CandidateTranslit, 1, 11)
	sw := w.decide(authorentity.CandidateSwap, 1, 21)
	p := w.pass()
	assert.Equal(t, applied(tr), w.state(tr))
	assert.Equal(t, applied(sw), w.state(sw))
	same(t, p, 11, 21)
}

// Typo-level spellings of one Cyrillic name — ё/е, ь/и — are one name:
// «Наталья Демурова» and «Наталия Демурова» through «Н. Демурова» apply.
func TestSpellingVariantsOfOneGivenNameAreOneName(t *testing.T) {
	w := newWorld(t, threeNames(t, person("Демурова", "Наталья"), person("Демурова", "Н."),
		person("Демурова", "Наталия")))
	w.pass()
	a := w.decide(authorentity.CandidateInitials, 11, 1)
	b := w.decide(authorentity.CandidateInitials, 11, 21)
	p := w.pass()
	assert.Equal(t, applied(a), w.state(a))
	assert.Equal(t, applied(b), w.state(b))
	same(t, p, 1, 21)
}

// Latin given names are not compared (their skeletons fold Stephen and
// Steven apart): the guard is Cyrillic only.
func TestLatinGivenNamesAreNotCompared(t *testing.T) {
	w := newWorld(t, threeNames(t, person("King", "Stephen").latin(), person("King", "S.").latin(),
		person("King", "Steven").latin()))
	w.pass()
	a := w.decide(authorentity.CandidateInitials, 11, 1)
	b := w.decide(authorentity.CandidateInitials, 11, 21)
	w.pass()
	assert.Equal(t, applied(a), w.state(a))
	assert.Equal(t, applied(b), w.state(b))
}

// naipauls: «Видиадхар Найпол» (1), «Vidiadhar Naipaul» (11), «V. Naipaul»
// (21) and «Vidia Naipaul» (31).
func naipauls(t *testing.T) *churn {
	ch := threeNames(t, person("Найпол", "Видиадхар"), person("Naipaul", "Vidiadhar").latin(),
		person("Naipaul", "V.").latin())
	ch.c.book(31, "Книга").credit(31, 31, "fp-d", person("Naipaul", "Vidia").latin())
	return ch
}

// The Naipaul shape: a translit merge applied in the previous pass stays
// applied when a new initials decision would join it with a side of a pair
// the production pair answered different (the translit «Видиадхар Найпол» —
// «Vidia Naipaul»): only that new decision is held (not_same). The other
// new decision, which bridges nothing, applies.
func TestALiveMergeStaysAppliedWhenANewDecisionBridgesADifferent(t *testing.T) {
	w := newWorld(t, naipauls(t))
	w.pass()
	notSame := candidateOf(w.level0, authorentity.CandidateTranslit, 1, 31)
	require.NotNil(t, notSame)
	tr := w.decide(authorentity.CandidateTranslit, 1, 11)
	w.pass()
	require.Equal(t, applied(tr), w.state(tr))

	w.ch.c.in.NotSame = map[[32]byte]bool{notSame.Digest: true}
	joins := w.decide(authorentity.CandidateInitials, 21, 11)
	bridges := w.decide(authorentity.CandidateInitials, 21, 31)
	p := w.pass()
	assert.Equal(t, applied(tr), w.state(tr), "the live merge")
	assert.Equal(t, applied(joins), w.state(joins))
	assert.Equal(t, conflict(bridges, authorentity.ConflictNotSame), w.state(bridges))
	same(t, p, 1, 21)
	apart(t, p, 1, 31)
}

// A given name and a patronymic written the other way round are one name:
// the swap of «Алексей Викторович Иванов» and «Викторович Алексей Иванов»
// applies.
func TestAGivenNameAndPatronymicInTheOtherOrderAreOneName(t *testing.T) {
	w := newWorld(t, threeNames(t, person("Иванов", "Алексей", "Викторович"),
		person("Иванов", "Викторович", "Алексей"), person("Петров", "Пётр")))
	w.pass()
	sw := w.decide(authorentity.CandidateSwap, 1, 11)
	p := w.pass()
	assert.Equal(t, applied(sw), w.state(sw))
	same(t, p, 1, 11)
}

// The guard never holds a decision the previous pass applied: a live merge
// keeps its state under the new rule (prod's swap and translit merges).
// Both Savchenko decisions, applied before, stay applied; a new one would
// be held.
func TestTheGuardKeepsADecisionThePreviousPassApplied(t *testing.T) {
	w := newWorld(t, savchenkos(t))
	w.pass()
	vladimir := w.decide(authorentity.CandidateInitials, 11, 21)
	vitaly := w.decide(authorentity.CandidateInitials, 11, 1)
	for i := range w.decisions {
		w.decisions[i].WasApplied = true
	}
	p := w.pass()
	assert.Equal(t, applied(vladimir), w.state(vladimir))
	assert.Equal(t, applied(vitaly), w.state(vitaly))
	same(t, p, 1, 21)
}

// Every common different-name pair a shared initial can bridge is two
// people (the review's table): the second initials decision is held.
func TestCommonDifferentNamesThroughOneInitialAreHeld(t *testing.T) {
	for _, tc := range []struct{ first, short, second string }{
		{"Олег", "О.", "Ольга"},
		{"Алина", "А.", "Алёна"},
		{"Антон", "А.", "Антонина"},
		{"Александр", "А.", "Александра"},
		{"Евгений", "Е.", "Евгения"},
		{"Валерий", "В.", "Валерия"},
		{"Иван", "И.", "Ивон"},
		{"Милана", "М.", "Милена"},
		{"Марина", "М.", "Мадина"},
		{"Дмитрий", "Д.", "Дмитирий"},
		{"Римма", "Р.", "Рима"},
	} {
		t.Run(tc.first+"/"+tc.second, func(t *testing.T) {
			w := newWorld(t, threeNames(t, person("Савченко", tc.first), person("Савченко", tc.short),
				person("Савченко", tc.second)))
			w.pass()
			kept := w.decide(authorentity.CandidateInitials, 11, 1)
			held := w.decide(authorentity.CandidateInitials, 11, 21)
			p := w.pass()
			assert.Equal(t, applied(kept), w.state(kept))
			assert.Equal(t, conflict(held, authorentity.ConflictGivenName), w.state(held))
			apart(t, p, 1, 21)
		})
	}
}

// The comparison: one name only when equal after folding (case, ё/е,
// і/ї/и, є/е, ў/у, й/и, a soft sign before a vowel as и, ь/ъ out); no
// typo is tolerated, and a doubled letter is a letter of its own.
func TestTheGivenNameComparison(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"Наталья", "Наталия", true},
		{"Юрий", "Юрій", true},
		{"Владимир", "Владиміръ", true},
		{"Дарья", "Дария", true},
		{"Пётр", "Петр", true},
		{"Ефим", "Єфим", true},
		{"Ўладзімір", "Уладзимир", true},
		// Short names, where only the fold can join them.
		{"Юрий", "Юрии", true},
		{"Игорь", "Игор", true},
		{"Игоръ", "Игорь", true},
		{"Марїя", "Мария", true},
		{"Олег", "Ольга", false},
		{"Алина", "Алёна", false},
		{"Антон", "Антонина", false},
		{"Александр", "Александра", false},
		{"Евгений", "Евгения", false},
		{"Валерий", "Валерия", false},
		{"Иван", "Ивон", false},
		{"Анна", "Нина", false},
		{"Елена", "Алёна", false},
		{"Михаил", "Ммхаил", false},
		{"Айзек", "Исаак", false},
		{"Дмитрий", "Тмитрий", false},
		{"Кристина", "Кристиан", false},
		// No typo is tolerated (round 2).
		{"Дмитирий", "Дмитрий", false},
		{"Михайлвич", "Михайлович", false},
		{"Милана", "Милена", false},
		{"Марина", "Мадина", false},
		{"Милена", "Мелена", false},
		// A doubled letter is never collapsed (round 3): «Римма» and «Рима»
		// are two real names, and «Савва»/«Сава» is held with them.
		{"Римма", "Рима", false},
		{"Савва", "Сава", false},
		{"Аполлон", "Апполон", false},
	} {
		assert.Equal(t, tc.same, authorentity.SameGivenName(tc.a, tc.b), "%s / %s", tc.a, tc.b)
		assert.Equal(t, tc.same, authorentity.SameGivenName(tc.b, tc.a), "%s / %s", tc.b, tc.a)
	}
}

// A chain of near names never passes: «Милана», «Милена» and «Мелена»
// through «М.» — each one letter from the next — are three people. The
// first decision applies; the other two are held, and no two of the names
// end in one entity.
func TestAChainOfNearNamesIsHeld(t *testing.T) {
	ch := threeNames(t, person("Савченко", "Милана"), person("Савченко", "М."), person("Савченко", "Милена"))
	ch.c.book(31, "Книга").credit(31, 31, "fp-d", person("Савченко", "Мелена"))
	w := newWorld(t, ch)
	w.pass()
	first := w.decide(authorentity.CandidateInitials, 11, 1)
	second := w.decide(authorentity.CandidateInitials, 11, 21)
	third := w.decide(authorentity.CandidateInitials, 11, 31)
	p := w.pass()
	assert.Equal(t, applied(first), w.state(first))
	assert.Equal(t, conflict(second, authorentity.ConflictGivenName), w.state(second))
	assert.Equal(t, conflict(third, authorentity.ConflictGivenName), w.state(third))
	apart(t, p, 1, 21)
	apart(t, p, 1, 31)
	apart(t, p, 21, 31)
}
