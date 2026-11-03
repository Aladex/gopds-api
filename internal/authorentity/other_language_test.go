package authorentity_test

import (
	"fmt"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
)

// The other-language spelling (E4n): a few books whose language spells the
// name its own way join the one Russian entity of the name.

// inLanguage sets a book's language.
func (c *catalog) inLanguage(id int64, lang string) *catalog {
	b := c.in.Books[id]
	b.Lang = lang
	c.in.Books[id] = b
	return c
}

// gogol is «Николай Гоголь» on books 1–10 in mainLang and a spelling on
// book 100 in lang; an unrelated swap pair gets a decision, so the pass has
// its decision step.
func gogol(t *testing.T, mainLang, family, lang string) *world {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := int64(1); i <= 10; i++ {
		ch.c.book(i, fmt.Sprintf("Повесть %d", i)).inLanguage(i, mainLang).
			credit(i, i, "fp-ru", person("Гоголь", "Николай"))
	}
	ch.c.book(100, "Мъртви души").inLanguage(100, lang).credit(100, 100, "fp-other", person(family, "Николай"))
	ch.c.book(900, "Мост").credit(900, 900, "fp-swap-a", person("Петров", "Иван")).
		book(901, "Река").credit(901, 901, "fp-swap-b", person("Иван", "Петров"))
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateSwap, 900, 901)
	return w
}

func TestAnOtherLanguagesSpellingJoins(t *testing.T) {
	for title, c := range map[string]struct {
		mainLang, family, lang string
		joins                  bool
	}{
		"the Bulgarian «Гогол»":                        {mainLang: "ru", family: "Гогол", lang: "bg", joins: true},
		"a sound the language spells another way":      {mainLang: "ru", family: "Гогаль", lang: "be", joins: true},
		"the same spelling in a Russian book":          {mainLang: "ru", family: "Гогол", lang: "ru"},
		"another letter: another name":                 {mainLang: "ru", family: "Моголь", lang: "uk"},
		"beside an entity whose books are not Russian": {mainLang: "bg", family: "Гогол", lang: "bg"},
	} {
		t.Run(title, func(t *testing.T) {
			p := gogol(t, c.mainLang, c.family, c.lang).pass()
			assert.Equal(t, c.joins, p.EntityOf(1) == p.EntityOf(100))
			if c.joins {
				assert.Equal(t, authorentity.RuleOtherLanguage, p.RuleOf(100))
				assert.Equal(t, 1, p.Report.Model.OtherLanguage)
			}
		})
	}
}

// A name two entities go by joins neither: «Сергей Васильєв» beside two
// «Сергей Васильев» of ten books.
func TestAnOtherLanguagesSpellingOfTwoEntitiesJoinsNeither(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := int64(1); i <= 10; i++ {
		ch.c.book(i, fmt.Sprintf("Повесть %d", i)).inLanguage(i, "ru").
			credit(i, i, "fp-a", person("Васильев", "Сергей", "Иванович")).
			book(20+i, fmt.Sprintf("Записки %d", i)).inLanguage(20+i, "ru").
			credit(20+i, 20+i, "fp-b", person("Васильев", "Сергей", "Петрович"))
	}
	ch.c.book(100, "Жлобологія").inLanguage(100, "uk").credit(100, 100, "fp-uk", person("Васильєв", "Сергей"))
	withSwap(ch.c)
	w := newWorld(t, ch)
	w.pass()
	w.decide(authorentity.CandidateSwap, 90, 91)
	p := w.pass()
	assert.NotEqual(t, p.EntityOf(1), p.EntityOf(100))
	assert.NotEqual(t, p.EntityOf(21), p.EntityOf(100))
}
