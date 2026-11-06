package authorentity_test

import (
	"fmt"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoNames is a catalog of two credits on unrelated books: 1 and 2.
func twoNames(t *testing.T, a, b *name) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Книга А").credit(1, 1, "fp-a", a).book(2, "Книга Б").credit(2, 2, "fp-b", b)
	return ch
}

// A misspelled family or given name is a typo pair; the other words are equal.
func TestATypoPairsTwoNamesOfOneOtherWords(t *testing.T) {
	p := sealed(t, twoNames(t, person("Чуковсктй", "Корней", "Иванович"), person("Чуковский", "Корней", "Иванович")).c, nil)
	requireCandidate(t, p, authorentity.CandidateTypo, "Cyrl", 1, 2)

	p = sealed(t, twoNames(t, person("Толстой", "Алекей"), person("Толстой", "Алексей")).c, nil)
	requireCandidate(t, p, authorentity.CandidateTypo, "Cyrl", 1, 2)

	p = sealed(t, twoNames(t, person("Hemingway", "Ernest").latin(), person("Hemingwey", "Ernest").latin()).c, nil)
	requireCandidate(t, p, authorentity.CandidateTypo, "Latn", 1, 2)

	p = sealed(t, twoNames(t, person("Чуковсктй", "Корней"), person("Чуковский", "Николай")).c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateTypo, 1, 2), "a second word differs")
}

// Two different patronymics are two people, and a misspelled one is the
// variant kind's question.
func TestATypoNeverPairsPatronymics(t *testing.T) {
	p := sealed(t, twoNames(t, person("Толстой", "Алексей", "Николаевич"), person("Толстой", "Алексей", "Константинович")).c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateTypo, 1, 2))

	p = sealed(t, twoNames(t, person("Толстой", "Алексей", "Николаевич"), person("Толстой", "Алексей", "Николаевчи")).c, nil)
	requireCandidate(t, p, authorentity.CandidateVariant, "Cyrl", 1, 2)
	assert.Nil(t, candidateOf(p, authorentity.CandidateTypo, 1, 2))

	// A patronymic written in the given name's place.
	p = sealed(t, twoNames(t, person("Толстой", "Николаевич"), person("Толстой", "Николаевна")).c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateTypo, 1, 2))
}

// Gender endings go to the model only on a title or a series the sides share.
func TestGenderEndingsNeedBookEvidence(t *testing.T) {
	p := sealed(t, twoNames(t, person("Бергер", "Евгений"), person("Бергер", "Евгения")).c, nil)
	requireExcluded(t, p, authorentity.CandidateTypo, authorentity.ExcludedTypoGender, 1, 2)

	ch := twoNames(t, person("Бергер", "Евгений"), person("Бергер", "Евгения"))
	ch.c.in.Books[2] = authorentity.Book{Title: "Книга А"}
	requireCandidate(t, sealed(t, ch.c, nil), authorentity.CandidateTypo, "Cyrl", 1, 2)

	ch = twoNames(t, person("Бергер", "Евгений"), person("Бергер", "Евгения"))
	ch.c.in.Books[1] = authorentity.Book{Title: "Книга А", Series: []int64{7}}
	ch.c.in.Books[2] = authorentity.Book{Title: "Книга Б", Series: []int64{7}}
	requireCandidate(t, sealed(t, ch.c, nil), authorentity.CandidateTypo, "Cyrl", 1, 2)
}

// A word of three letters is no typo of another.
func TestShortWordsPairNoTypo(t *testing.T) {
	p := sealed(t, twoNames(t, person("Чайлд", "Ли"), person("Чайлд", "Лия")).c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateTypo, 1, 2))
	p = sealed(t, twoNames(t, person("Кон", "Иван"), person("Ком", "Иван")).c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateTypo, 1, 2))
}

func TestTypoVerdict(t *testing.T) {
	const no, yes, gender = 0, 1, 2
	for _, c := range []struct {
		x, y   string
		nx, ny int
		want   int
	}{
		{"алекей", "алексей", 1, 900, yes},
		{"лукяненко", "лукьяненко", 2, 3, yes},
		{"дмитирий", "дмитрий", 1, 900, yes},
		// A vowel or a sign changes spelling, however common both are.
		{"реймонд", "раймонд", 40, 40, yes},
		{"наталия", "наталья", 396, 1188, yes},
		{"виталій", "виталий", 30, 400, yes},
		// Letters that sound alike.
		{"патриция", "патрисия", 30, 30, yes},
		{"харри", "гарри", 30, 300, yes},
		{"вильям", "уильям", 90, 300, yes},
		// A consonant cut from the end.
		{"бернар", "бернард", 30, 300, yes},
		// Two common words of other consonants are two names.
		{"юрий", "юлий", 1800, 60, no},
		{"мария", "марина", 900, 700, no},
		{"прохоров", "прозоров", 60, 30, no},
		// The same words, rare: the model tells them apart.
		{"прохоров", "прозоров", 2, 3, yes},
		// A first letter that changes, unless one spelling is a rare slip.
		{"фотев", "ботев", 1, 1, no},
		{"юровских", "еловских", 1, 1, no},
		{"марина", "арина", 700, 50, no},
		{"тейман", "гейман", 1, 40, yes},
		// Too far, too short.
		{"иванов", "иваненко", 1, 1, no},
		{"ковальчук", "коваленко", 1, 1, no},
		{"ким", "кин", 1, 1, no},
		// Gender endings.
		{"иванов", "иванова", 1, 1, gender},
		{"евгений", "евгения", 400, 400, gender},
		{"толстой", "толстая", 1, 1, gender},
		{"robert", "roberta", 9, 1, gender},
	} {
		assert.Equal(t, c.want, authorentity.TypoVerdict(c.x, c.y, c.nx, c.ny), "%s, %s", c.x, c.y)
	}
}

// nearWords finds every pair within the distance, once.
func TestNearWords(t *testing.T) {
	assert.Equal(t, [][2]string{{"алекей", "алексей"}, {"дмитирий", "дмитрий"}, {"иванов", "иваонв"}},
		authorentity.NearWords([]string{"алекей", "алексей", "дмитирий", "дмитрий", "иванов", "иваонв", "ким", "кин"}))
}

// «Иван Петров» on two full names is a bucket: a pair the plain kinds keep
// out for it goes to name_form.
func bucketWith(t *testing.T, other *name) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Книга А").credit(1, 1, "fp-a", person("Петров", "Иван")).
		book(2, "Книга Б").credit(2, 2, "fp-b", person("Петров", "Иван")).
		book(11, "Книга В").credit(11, 11, "fp-c", person("Петров", "Иван", "Сергеевич")).
		book(21, "Книга Г").credit(21, 21, "fp-d", person("Петров", "Иван", "Александрович")).
		book(31, "Книга Д").credit(31, 31, "fp-e", other)
	return ch
}

func TestNameFormAsksWhatABucketKeepsOut(t *testing.T) {
	p := sealed(t, bucketWith(t, person("Иван", "Петров")).c, nil)
	requireExcluded(t, p, authorentity.CandidateSwap, authorentity.ExcludedBucketSide, 31, 1)
	requireCandidate(t, p, authorentity.CandidateNameForm, "Cyrl", 31, 1)

	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Книга А").credit(1, 1, "fp-a", person("Петров", "Иван")).
		book(2, "Книга Б").credit(2, 2, "fp-b", person("Петров", "Иван")).
		// A full name of another script makes the bucket and is no target.
		book(11, "Книга В").credit(11, 11, "fp-c", person("Петров", "Иван", "Сергеевич").latin()).
		book(31, "Книга Д").credit(31, 31, "fp-e", person("Петров", "И"))
	p = sealed(t, ch.c, nil)
	requireExcluded(t, p, authorentity.CandidateInitials, authorentity.ExcludedBucketSide, 31, 1)
	requireCandidate(t, p, authorentity.CandidateNameForm, "Cyrl", 31, 1)
}

// Initials of several targets stay apart.
func TestNameFormLeavesInitialsOfManyTargets(t *testing.T) {
	ch := bucketWith(t, person("Петров", "И"))
	ch.c.book(41, "Книга Е").credit(41, 41, "fp-f", person("Петров", "Игорь", "Павлович"))
	p := sealed(t, ch.c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateNameForm, 31, 1))
}

// A given name cut short pairs with its one full name, when the source writes
// it so: of no vowel, or with a period.
func TestNameFormAsksAGivenNameCutShort(t *testing.T) {
	p := sealed(t, twoNames(t, person("Олдридж", "Дж"), person("Олдридж", "Джеймс")).c, nil)
	requireCandidate(t, p, authorentity.CandidateNameForm, "Cyrl", 1, 2)

	cut := person("Сахаров", "Ив")
	cut.displayAs = "Ив. Сахаров"
	p = sealed(t, twoNames(t, cut, person("Сахаров", "Иван", "Петрович")).c, nil)
	requireCandidate(t, p, authorentity.CandidateNameForm, "Cyrl", 1, 2)

	p = sealed(t, twoNames(t, person("Чайлд", "Ли"), person("Чайлд", "Линкольн")).c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateNameForm, 1, 2), "a name of its own")

	ch := twoNames(t, person("Олдридж", "Дж"), person("Олдридж", "Джеймс"))
	ch.c.book(3, "Книга В").credit(3, 3, "fp-c", person("Олдридж", "Джон"))
	assert.Nil(t, candidateOf(sealed(t, ch.c, nil), authorentity.CandidateNameForm, 1, 2), "two targets")
}

// A same of a spelling kind joins the sides; the rule is the smaller entity's.
func TestASpellingSameJoins(t *testing.T) {
	ch := twoNames(t, person("Чуковсктй", "Корней"), person("Чуковский", "Корней"))
	ch.c.book(3, "Книга В").credit(3, 3, "fp-b", person("Чуковский", "Корней"))
	w := newWorld(t, ch)
	apart(t, w.pass(), 1, 2)
	id := w.decide(authorentity.CandidateTypo, 1, 2)
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	same(t, p, 1, 2)
	assert.Equal(t, "model:typo", p.RuleOf(1))
	second := w.pass()
	assert.Zero(t, second.Changes(), "a second pass changes nothing")
	assert.Equal(t, applied(id), w.state(id))
}

// A typo in a given name joins though the guard holds two given names: the
// decision asked that one difference.
func TestATypoInAGivenNameJoinsPastTheGuard(t *testing.T) {
	ch := twoNames(t, person("Толстой", "Алекей"), person("Толстой", "Алексей"))
	w := newWorld(t, ch)
	w.pass()
	id := w.decide(authorentity.CandidateTypo, 1, 2)
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	same(t, p, 1, 2)
}

// A spelling decision never holds an orphan's: the orphan joins first, and
// the spelling pair of the same side after it, no second target.
func TestASpellingDecisionNeverHoldsAnOrphan(t *testing.T) {
	ch := petrovs(t)
	for id := int64(31); id < 34; id++ {
		ch.c.book(id, fmt.Sprintf("Книга %d", id)).credit(id, id, "fp-e", person("Петрав", "Евгений"))
	}
	w := newWorld(t, ch)
	w.pass()
	orphan := w.decide(authorentity.CandidateOrphan, 1, 11)
	typo := w.decide(authorentity.CandidateTypo, 1, 31)
	p := w.pass()
	assert.Equal(t, applied(orphan), w.state(orphan))
	assert.Equal(t, applied(typo), w.state(typo))
	same(t, p, 31, 1)
	same(t, p, 31, 11)
}

// A pair a live kind asks stays there; one a name kind keeps out may be asked
// as a typo.
func TestATypoLeavesAPairAskedElsewhere(t *testing.T) {
	ch := twoNames(t, person("Толстой", "Алекей"), person("Толстой", "Алексей"))
	ch.c.credit(3, 2, "fp-c", person("Толстой", "А"))
	p := sealed(t, ch.c, nil)
	assert.Equal(t, 1, counted(p, authorentity.CandidateTypo, "Cyrl"), "one pair, once")
}

// A side of two scripts pairs through the keys that match, in their script.
func TestATypoPairsASideOfTwoScriptsInTheKeysScript(t *testing.T) {
	c := newCatalog(t).book(1, "Книга А").book(2, "Книга Б").book(3, "Книга В").
		credit(1, 1, "fp-a", person("Хемингуэй", "Эрнест")).
		credit(2, 2, "fp-b", person("Hemingway", "Ernest").latin()).
		credit(3, 3, "fp-c", person("Хемингой", "Эрнест"))
	c.with(newReference().item("Q23434", []string{"Эрнест Хемингуэй", "Ernest Hemingway"}))
	p := sealed(t, c, nil)
	assert.Equal(t, p.EntityOf(1), p.EntityOf(2), "one side of two scripts")
	requireCandidate(t, p, authorentity.CandidateTypo, "Cyrl", 3, 1)
}

// The partner cap holds for the spelling kinds as for the name kinds.
func TestTypoPartnerCap(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Книга").credit(1, 1, "fp-0", person("Корнеев", "Иван"))
	for i, letter := range []rune("абвгдежзиклмнопрстуфхцчшщ") {
		id := int64(i + 2)
		ch.c.book(id, fmt.Sprintf("Книга %d", id)).
			credit(id, id, fmt.Sprintf("fp-%d", id), person("Корн"+string(letter)+"ев", "Иван"))
	}
	p := sealed(t, ch.c, nil)
	assert.Positive(t, excluded(p, authorentity.CandidateTypo, "Cyrl", authorentity.ExcludedPartnerCap))
}

// A typo suite item shows one person's name, and the same name misspelled on
// the other's books.
func TestTheTypoSuiteItemMisspellsOnePersonsFamily(t *testing.T) {
	p := sealed(t, distinctNames(t, person("Смирнов", "Сергей", "Иванович"), person("Смирнов", "Сергей", "Николаевич")).c,
		nil)
	pairs := p.MergeSuitePairs(nil)
	require.Len(t, pairs, 1)
	in := p.RenderSuiteItem(&pairs[0], authorentity.CandidateTypo, false, nil)
	assert.ElementsMatch(t, []string{"Сергей Смирнов", "Сергей Смирнав"}, append(in.A.Names, in.B.Names...))
}
