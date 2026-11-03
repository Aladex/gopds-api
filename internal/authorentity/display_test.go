package authorentity_test

import (
	"fmt"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E4i: the name an entity is shown under is the reader's: the Wikidata
// Russian label when it is one of the entity's full given-first forms; else
// its commonest full Cyrillic given-first form (a Russian spelling first);
// else, when its Cyrillic books are as many as its Latin ones, its best
// Cyrillic form, given-first initials before a surname-first one; else its
// best Latin form. Forms are counted by their books, never by the result
// they come from, and the name is always one of them, never assembled. The
// name the build matches an entity by — Wikidata, candidates — is the old
// one and does not change.

// forms is a catalog of one entity — every credit of one key — written in
// the given forms, n books each, books and fingerprints of their own.
func forms(t *testing.T, key *name, written ...any) *catalog {
	c := newCatalog(t)
	id := int64(1)
	for i := 0; i < len(written); i += 2 {
		display, n := written[i].(string), written[i+1].(int)
		for range n {
			n := *key
			c.book(id, fmt.Sprintf("Книга %d", id)).credit(id, id, fmt.Sprintf("fp-%d-%d", i, id), shown(display, &n))
			id++
		}
	}
	return c
}

// shownAs is the entity of credit 1's display name.
func shownAs(t *testing.T, p *authorentity.Plan) string {
	t.Helper()
	e := p.Entity(p.EntityOf(1))
	require.NotNil(t, e)
	return e.DisplayName
}

// Forms count by books, not by result: «Гарри Гаррисон» on three results
// of two books each beats «Гарри Максвелл Гаррисон» on one result of four
// (the old rule took the biggest result).
func TestTheDisplayIsTheCommonestForm(t *testing.T) {
	c := newCatalog(t)
	for i := int64(1); i <= 4; i++ {
		c.book(i, fmt.Sprintf("Книга %d", i)).credit(i, i, "fp-maxwell",
			shown("Гарри Максвелл Гаррисон", person("Гаррисон", "Гарри")))
	}
	for i := int64(11); i <= 16; i++ {
		c.book(i, fmt.Sprintf("Книга %d", i)).credit(i, i, fmt.Sprintf("fp-h%d", (i-11)/2),
			shown("Гарри Гаррисон", person("Гаррисон", "Гарри")))
	}
	p := c.build()
	same(t, p, 1, 11)
	assert.Equal(t, "Гарри Гаррисон", shownAs(t, p))
	e := p.Entity(p.EntityOf(1))
	assert.Equal(t, "гарри гаррисон", e.SearchKey)
}

// A given-first full form beats a surname-first one and initials, however
// many books those have; the sort name is the chosen form's.
func TestAGivenFirstFullFormBeatsSurnameFirstAndInitials(t *testing.T) {
	p := forms(t, person("Цвейг", "Стефан"), "Цвейг Стефан", 5, "Стефан Цвейг", 1).build()
	assert.Equal(t, "Стефан Цвейг", shownAs(t, p))
	p = forms(t, person("Толстой", "Лев", "Николаевич"), "Толстой Л.Н.", 6, "Л. Н. Толстой", 3,
		"Лев Николаевич Толстой", 1).build()
	assert.Equal(t, "Лев Николаевич Толстой", shownAs(t, p))
}

// A Russian spelling beats another Cyrillic one: «Галина Тарасюк» over
// «Галина Тимофіївна Тарасюк», «Ридли Пирсон» over the Bulgarian «Ридли
// Пиърсън».
func TestARussianSpellingBeatsAnotherCyrillicOne(t *testing.T) {
	p := forms(t, person("Тарасюк", "Галина"), "Галина Тимофіївна Тарасюк", 5, "Галина Тарасюк", 1).build()
	assert.Equal(t, "Галина Тарасюк", shownAs(t, p))
	p = forms(t, person("Пирсон", "Ридли"), "Ридли Пиърсън", 5, "Ридли Пирсон", 2).build()
	assert.Equal(t, "Ридли Пирсон", shownAs(t, p))
}

// A Russian full form beats a Latin one of more books: the site reads
// Russian. Without one, the Cyrillic forms win when they hold as many
// books as the Latin ones (given-first initials before surname-first), the
// Latin full form when they hold fewer.
func TestCyrillicBeatsLatin(t *testing.T) {
	p := forms(t, person("Card", "Orson", "Scott").latin(), "Orson Scott Card", 5, "Орсон Скотт Кард", 1).build()
	assert.Equal(t, "Орсон Скотт Кард", shownAs(t, p))
	p = forms(t, person("Толстой", "Лев"), "Толстой Л.", 4, "Л. Толстой", 1, "Leo Tolstoy", 3).build()
	assert.Equal(t, "Л. Толстой", shownAs(t, p))
	p = forms(t, person("Толстой", "Лев"), "Толстой Л.", 1, "Leo Tolstoy", 3).build()
	assert.Equal(t, "Leo Tolstoy", shownAs(t, p))
	// Another Cyrillic spelling beats Latin only by books: «Harry Harrison»
	// stays itself beside the Bulgarian «Хари Харисън».
	p = forms(t, person("Harrison", "Harry").latin(), "Harry Harrison", 5, "Хари Харисън", 1).build()
	assert.Equal(t, "Harry Harrison", shownAs(t, p))
	p = forms(t, person("Harrison", "Harry").latin(), "Harry Harrison", 1, "Хари Харисън", 5).build()
	assert.Equal(t, "Хари Харисън", shownAs(t, p))
}

// The entity's Wikidata Russian label wins when it is one of its full
// given-first forms, whatever its books; a label written in initials
// does not.
func TestTheWikidataLabelWinsWhenItIsAFullForm(t *testing.T) {
	c := forms(t, person("Толстой", "Лев", "Николаевич"), "Лев Толстой", 5, "Лев Николаевич Толстой", 2)
	c.with(newReference().item("Q7243", []string{"Лев Николаевич Толстой", "Leo Tolstoy"}))
	assert.Equal(t, "Лев Николаевич Толстой", shownAs(t, c.build()))

	c = forms(t, person("Уилсон", "Фрэнсис", "Пол"), "Ф. Пол Уилсон", 5, "Фрэнсис Пол Уилсон", 2)
	c.with(newReference().item("Q1", []string{"Ф. Пол Уилсон"}))
	assert.Equal(t, "Фрэнсис Пол Уилсон", shownAs(t, c.build()))
}

// The name the build matches an entity by stays the old one: the swap of
// the old display («Гарри Максвелл Гаррисон», the biggest result) is still
// a candidate, whatever the entity is now shown as.
func TestTheMatchingNameIsTheOldOne(t *testing.T) {
	c := newCatalog(t)
	for i := int64(1); i <= 4; i++ {
		c.book(i, fmt.Sprintf("Книга %d", i)).credit(i, i, "fp-maxwell",
			shown("Гарри Максвелл Гаррисон", person("Гаррисон", "Гарри")))
	}
	for i := int64(11); i <= 16; i++ {
		c.book(i, fmt.Sprintf("Книга %d", i)).credit(i, i, fmt.Sprintf("fp-h%d", (i-11)/2),
			shown("Гарри Гаррисон", person("Гаррисон", "Гарри")))
	}
	c.book(21, "Сборник").credit(21, 21, "fp-swap", shown("Гаррисон Гарри Максвелл", person("Максвелл", "Гаррисон", "Гарри")))
	p := sealed(t, c, nil)
	assert.Equal(t, "Гарри Гаррисон", shownAs(t, p))
	requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 21, 1)
}

// A pinned name stays as the administrator set it.
func TestAPinnedNameStays(t *testing.T) {
	c := forms(t, person("Цвейг", "Стефан"), "Цвейг Стефан", 5, "Стефан Цвейг", 1)
	p := c.build()
	id := p.EntityOf(1)
	st := authorentity.NewState()
	p.Apply(st)
	row := st.Entities[id]
	row.DisplayName, row.NamePinned = "Цвейг Стефан", true
	st.Entities[id] = row
	c.in.Old = st
	p = c.build()
	assert.Equal(t, "Цвейг Стефан", shownAs(t, p))
}

// The order of shapes and ties: as many Cyrillic books as Latin ones is
// enough; given-first initials beat a surname-first full form; a Russian
// spelling beats another of the same shape; of equal books the fuller form.
func TestTheShapesOrderAndTies(t *testing.T) {
	p := forms(t, person("Толстой", "Лев"), "Л. Толстой", 2, "Leo Tolstoy", 2).build()
	assert.Equal(t, "Л. Толстой", shownAs(t, p))
	p = forms(t, person("Толстой", "Лев"), "Толстой Лев", 5, "Л. Толстой", 1).build()
	assert.Equal(t, "Л. Толстой", shownAs(t, p))
	p = forms(t, person("Гуцало", "Евген"), "Є. Гуцало", 3, "Е. Гуцало", 1).build()
	assert.Equal(t, "Е. Гуцало", shownAs(t, p))
	p = forms(t, person("Толстой", "Лев", "Николаевич"), "Лев Толстой", 2, "Лев Николаевич Толстой", 2).build()
	assert.Equal(t, "Лев Николаевич Толстой", shownAs(t, p))
}

// The Wikidata link reads the matching name, not the name shown: «Harry
// Harrison», the biggest result and a label, links the entity to its item
// though it is shown as «Гарри Гаррисон».
func TestTheLinkReadsTheMatchingName(t *testing.T) {
	c := newCatalog(t)
	for i := int64(1); i <= 5; i++ {
		c.book(i, fmt.Sprintf("Book %d", i)).credit(i, i, "fp-en", person("Harrison", "Harry").latin())
	}
	c.book(11, "Книга").credit(11, 11, "fp-ru", shown("Гарри Гаррисон", person("Harrison", "Harry").latin()))
	c.with(newReference().item("Q1", []string{"Harry Harrison"}))
	p := c.build()
	same(t, p, 1, 11)
	assert.Equal(t, "Гарри Гаррисон", shownAs(t, p))
	assert.Equal(t, "Q1", p.WikidataOf(p.EntityOf(1)))
}

// A pinned name is matched by as before: the swap of the name the
// administrator set («Цвейг Стефан Фридрих») is a candidate, though the
// matching rule alone would name the entity «Стефан Цвейг».
func TestAPinnedNameIsMatchedBy(t *testing.T) {
	c := forms(t, person("Цвейг", "Стефан"), "Стефан Цвейг", 3)
	c.book(21, "Сборник").credit(21, 21, "fp-swap", shown("Фридрих Стефан Цвейг", person("Фридрих", "Стефан", "Цвейг")))
	p := c.build()
	id := p.EntityOf(1)
	st := authorentity.NewState()
	p.Apply(st)
	row := st.Entities[id]
	row.DisplayName, row.SearchKey, row.NamePinned = "Цвейг Стефан Фридрих", "цвейг стефан фридрих", true
	st.Entities[id] = row
	c.in.Old = st
	p = sealed(t, c, nil)
	assert.Equal(t, "Цвейг Стефан Фридрих", shownAs(t, p))
	requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 21, 1)
}

// The Wikidata step after the decisions names a component by its matching
// name too: the side of «H. Kuttner», the biggest result, is shown under its
// Russian full form, and joins «Генри Каттнер»: its initial stands for the
// full name «Henry Kuttner» of the side, a name of the item alone (E4m).
func TestTheStepAfterTheDecisionsReadsTheMatchingName(t *testing.T) {
	c := newCatalog(t)
	for i := int64(1); i <= 3; i++ {
		c.book(i, fmt.Sprintf("Book %d", i)).credit(i, i, "fp-h", shown("H. Kuttner", person("Kuttner", "Henry").latin()))
	}
	c.book(4, "Книга 4").credit(4, 4, "fp-ru-a", shown("Генри Каттнер", person("Kuttner", "Henry").latin()))
	c.book(11, "Книга 11").credit(11, 11, "fp-ru", person("Каттнер", "Генри"))
	// A decision of its own, so the decision step runs.
	c.book(31, "Повесть").credit(31, 31, "fp-swap-a", person("Петров", "Иван")).
		book(32, "Рассказ").credit(32, 32, "fp-swap-b", person("Иван", "Петров"))
	c.with(newReference().item("Q551710", []string{"Генри Каттнер", "Henry Kuttner"}))
	w := newWorld(t, &churn{t: t, c: c, fields: map[int64]authorentity.CardBookFields{}})
	w.pass()
	w.decide(authorentity.CandidateSwap, 31, 32)
	p := w.pass()
	same(t, p, 31, 32)
	assert.Equal(t, "Генри Каттнер", p.Entity(p.EntityOf(1)).DisplayName)
	same(t, p, 1, 11)
}

// A Russian full form beats Latin only when the Cyrillic forms hold a
// tenth of the entity's books at least: «Mark Twain» stays beside «Марк
// Твэн» on two of 55; one in ten is enough.
func TestARussianFormNeedsATenthOfTheBooks(t *testing.T) {
	p := forms(t, person("Twain", "Mark").latin(), "Mark Twain", 53, "Марк Твэн", 2).build()
	assert.Equal(t, "Mark Twain", shownAs(t, p))
	p = forms(t, person("Flint", "Eric").latin(), "Eric Flint", 9, "Эрик Флинт", 1).build()
	assert.Equal(t, "Эрик Флинт", shownAs(t, p))
	p = forms(t, person("Flint", "Eric").latin(), "Eric Flint", 10, "Эрик Флинт", 1).build()
	assert.Equal(t, "Eric Flint", shownAs(t, p))
}

// A form whose family name is not its last word («Томас Рид Майн») is
// scrambled: never shown while another form is well ordered, whatever its
// books; a suffix after the family name («Kurt Vonnegut Jr.») is no
// scrambling.
func TestAScrambledFormIsShownLast(t *testing.T) {
	p := forms(t, person("Рид", "Томас", "Майн"), "Томас Рид Майн", 6, "Томас Майн Рид", 3).build()
	assert.Equal(t, "Томас Майн Рид", shownAs(t, p))
	p = forms(t, person("Ховард", "Роберт", "Эрвин"), "Роберт Ховард Эрвин", 3, "Robert Ervin Howard", 2).build()
	assert.Equal(t, "Robert Ervin Howard", shownAs(t, p))
	p = forms(t, person("Рид", "Томас", "Майн"), "Томас Рид Майн", 2).build()
	assert.Equal(t, "Томас Рид Майн", shownAs(t, p), "the only form is shown, scrambled or not")
	p = forms(t, person("Vonnegut", "Kurt").withSuffix("Jr.").latin(), "Kurt Vonnegut Jr.", 3, "K. Vonnegut", 5).build()
	assert.Equal(t, "Kurt Vonnegut Jr.", shownAs(t, p))
	p = forms(t, person("Howard", "Robert", "Ervin").latin(), "Robert Howard Ervin", 5, "Robert Ervin Howard", 2).build()
	assert.Equal(t, "Robert Ervin Howard", shownAs(t, p), "a scrambled Latin form neither")
	// A surname-first form is no scrambling: the Cyrillic forms holding most
	// books, «Толстой Лев» is shown before «Leo Tolstoy».
	p = forms(t, person("Толстой", "Лев"), "Толстой Лев", 3, "Leo Tolstoy", 1).build()
	assert.Equal(t, "Толстой Лев", shownAs(t, p))
}

// Round 2. Written forms that differ only in dots, commas, hyphens and spaces
// are one form: their books count together, and it is shown in its best
// punctuated spelling — every initial with its dot, a space after each dot,
// one space between words — whatever the books of the others («Кэтрин Л.
// Мур», not «Кэтрин Л Мур»).
func TestAFormIsShownPunctuated(t *testing.T) {
	p := forms(t, person("Мур", "Кэтрин", "Л"), "Кэтрин Л Мур", 5, "Кэтрин Л. Мур", 3).build()
	assert.Equal(t, "Кэтрин Л. Мур", shownAs(t, p))
	p = forms(t, person("Salvatore", "R", "A").latin(), "R.A Salvatore", 5, "R. A. Salvatore", 1, "R.A. Salvatore", 3).build()
	assert.Equal(t, "R. A. Salvatore", shownAs(t, p))
	p = forms(t, person("Salvatore", "R", "A").latin(), "R A Salvatore", 5, "R.A. Salvatore", 1).build()
	assert.Equal(t, "R.A. Salvatore", shownAs(t, p), "a dot cramped beats a dot missing")
	p = forms(t, person("Уилсон", "Ф", "Пол"), "Ф Пол Уилсон", 2, "Ф. Пол Уилсон", 2, "Ф. П. Уилсон", 3).build()
	assert.Equal(t, "Ф. Пол Уилсон", shownAs(t, p), "the books of the spellings count together")
	p = forms(t, person("Ле Гуин", "Урсула", "К"), "Урсула К Ле Гуин", 3, "Урсула К. Ле Гуин", 2,
		"Ursula K. Le Guin", 4).build()
	assert.Equal(t, "Урсула К. Ле Гуин", shownAs(t, p))
	p = forms(t, person("Толстой", "Лев", "Николаевич"), "Лев Николаевич ТОЛСТОЙ", 1, "Лев Николаевич Толстой", 1).build()
	assert.Equal(t, "Лев Николаевич ТОЛСТОЙ", shownAs(t, p), "case is no punctuation: the lower result id as before")
	p = forms(t, person("Толстой", "Лев", "Николаевич"), "Лев ТОЛСТОЙ", 1, "Лев Толстой", 1, "Лев Николаевич Толстой", 1).build()
	assert.Equal(t, "Лев Николаевич Толстой", shownAs(t, p), "nor do spellings in another case count together")
	p = forms(t, person("Толстой", "Лев"), "Лев  Толстой", 1, "Лев Толстой", 3).build()
	assert.Equal(t, "Лев Толстой", shownAs(t, p), "a double space is a fault")
	p = spelled(t, person("Salvatore", "R", "A").latin(), "R.A. Salvatore", 3, "R. A. Salvatore", 1).build()
	assert.Equal(t, "R. A. Salvatore", shownAs(t, p), "a fault is mended in the old name too")
}

// Round 2. A hyphen is punctuation too, but no fault either way: «Конан
// Дойль» is right without one, «Башевис-Зингер» with one. Spellings of no
// fault apart keep the one the old rule shows (the matching name), else
// the one of the most books: punctuation alone never changes a name but
// to mend it.
func TestAHyphenStaysAsItWas(t *testing.T) {
	key := person("Дойль", "Артур", "Конан")
	c := spelled(t, key, "Артур Конан Дойль", 3, "Артур Конан-Дойль", 5)
	assert.Equal(t, "Артур Конан Дойль", shownAs(t, c.build()))
	key = person("Зингер", "Исаак", "Башевис")
	c = spelled(t, key, "Исаак Башевис-Зингер", 2, "Исаак Башевис Зингер", 5)
	assert.Equal(t, "Исаак Башевис-Зингер", shownAs(t, c.build()))
	c = spelled(t, key, "И. Зингер", 4, "Исаак Башевис Зингер", 2, "Исаак Башевис-Зингер", 3)
	assert.Equal(t, "Исаак Башевис-Зингер", shownAs(t, c.build()), "the old name another form: the most books")
}

// spelled is a catalog of one entity whose first spelling is one result of
// n books — the name the old rule shows — and whose other spellings are a
// result a book, n books each.
func spelled(t *testing.T, key *name, old string, n int, others ...any) *catalog {
	c := newCatalog(t)
	id := int64(1)
	for range n {
		k := *key
		c.book(id, fmt.Sprintf("Книга %d", id)).credit(id, id, "fp-old", shown(old, &k))
		id++
	}
	for i := 0; i < len(others); i += 2 {
		display, m := others[i].(string), others[i+1].(int)
		for range m {
			k := *key
			c.book(id, fmt.Sprintf("Книга %d", id)).credit(id, id, fmt.Sprintf("fp-%d-%d", i, id), shown(display, &k))
			id++
		}
	}
	return c
}

// Round 2. A Latin form beats a Cyrillic one only when no Russian Cyrillic
// form of its shape or a better one holds the share: two initials forms keep
// the Cyrillic one («Р. А. Салваторе», not «R. A. Salvatore»), a Latin full
// form still beats Cyrillic initials of fewer books.
func TestLatinNeedsABetterShapeThanCyrillic(t *testing.T) {
	p := forms(t, person("Salvatore", "R", "A").latin(), "R. A. Salvatore", 21, "Р. А. Салваторе", 4).build()
	assert.Equal(t, "Р. А. Салваторе", shownAs(t, p))
	p = forms(t, person("Salvatore", "R", "A").latin(), "R. A. Salvatore", 21, "Р. А. Салваторе", 2).build()
	assert.Equal(t, "R. A. Salvatore", shownAs(t, p), "under a tenth of the books Latin stays")
	p = forms(t, person("Salvatore", "Robert").latin(), "Robert Salvatore", 21, "Р. Салваторе", 5).build()
	assert.Equal(t, "Robert Salvatore", shownAs(t, p), "Cyrillic initials do not beat a Latin full form")
	p = forms(t, person("Салваторе", "Р", "А"), "R. A. Salvatore", 21, "Салваторе Р. А.", 5).build()
	assert.Equal(t, "R. A. Salvatore", shownAs(t, p), "nor does a Cyrillic surname-first form beat Latin initials")
	p = forms(t, person("Тарасюк", "Галина"), "Галина Тимофіївна Тарасюк", 5, "Г. Тарасюк", 3).build()
	assert.Equal(t, "Галина Тимофіївна Тарасюк", shownAs(t, p), "with no Latin form the full form stays first")
}
