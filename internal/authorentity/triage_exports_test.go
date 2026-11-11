package authorentity_test

import (
	"fmt"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The review triage (internal/authortriage) reads a review item with only the
// three reads the judges agree with at 99% (case, look-alike letters, a
// nickname); the build's other reads stay the build's.

func TestReviewRuleReadsOnlyTheSafeThree(t *testing.T) {
	cat := knownGivens(newCatalog(t), "Николай", "Дейзи", "Андрей", "Всеволод", "Thierry").book(1, "Книга")
	g := authorentity.GivenNamesOf(cat.in.Credits)

	t.Run("the build reads an affix and a single field, the triage does not", func(t *testing.T) {
		cat.unselected(20, 1, "fp-affix", "affix", flags("affix"), person("Бабкин", "Священник Николай"))
		cat.unselected(21, 1, "fp-single", "single_field", flags("unstructured", "empty_component"),
			&name{script: "Cyrl"}).fromSource("", "", "Дейзи Уитни")
		for i, want := range map[int]string{0: authorentity.ReadAffix, 1: authorentity.ReadSingleField} {
			u := &cat.in.Unselected[i]
			rule, ok := authorentity.ReadAsSelected(u, g)
			require.True(t, ok, "the build reads %s", u.Class)
			assert.Equal(t, want, rule)
			res, rule, ok := authorentity.ReadReviewRule(u, g)
			assert.False(t, ok, "the triage reads no %s", u.Class)
			assert.Nil(t, res)
			assert.Empty(t, rule)
		}
	})

	t.Run("the three it reads", func(t *testing.T) {
		for title, c := range map[string]struct {
			class, rule, display string
			flags                []string
			n                    *name
		}{
			"all caps": {class: "case_defect", rule: authorentity.ReadCase, display: "Thierry Jonquet",
				flags: flags("all_caps"), n: person("JONQUET", "THIERRY").latin()},
			"look-alike letters": {class: "mixed_script", rule: authorentity.ReadLookalike, display: "Андрей Коломицкий",
				flags: flags("mixed_script"), n: person("Коломицкий", "Андрeй").mixed()},
			"a nickname of another script": {class: "mixed_script", rule: authorentity.ReadNickname,
				display: "Всеволод Бобров Vector", flags: flags("mixed_script", "nickname"),
				n: person("Бобров", "Всеволод").withNickname("Vector").mixed()},
			"a nickname": {class: "nickname_with_name", rule: authorentity.ReadNickname,
				display: "Всеволод Бобров Вектор", flags: flags("nickname"),
				n: person("Бобров", "Всеволод").withNickname("Вектор")},
		} {
			t.Run(title, func(t *testing.T) {
				cat := knownGivens(newCatalog(t), "Андрей", "Всеволод", "Thierry").book(1, "Книга")
				cat.unselected(20, 1, "fp-review", c.class, c.flags, c.n)
				u := &cat.in.Unselected[0]
				res, rule, ok := authorentity.ReadReviewRule(u, authorentity.GivenNamesOf(cat.in.Credits))
				require.True(t, ok)
				assert.Equal(t, c.rule, rule)
				assert.Equal(t, c.display, res.DisplayName)
				assert.Equal(t, authorentity.KindPerson, res.Kind)

				// The same guards as the build's: a namesake tag, or no person.
				u.Tag = 2
				_, _, ok = authorentity.ReadReviewRule(u, authorentity.GivenNamesOf(cat.in.Credits))
				assert.False(t, ok, "a tagged name is read by no rule")
				u.Tag = 0
				u.Kind = authorentity.KindCollective
				_, _, ok = authorentity.ReadReviewRule(u, authorentity.GivenNamesOf(cat.in.Credits))
				assert.False(t, ok, "a collective is read by no rule")
			})
		}
	})

	t.Run("an unknown given name is read by none", func(t *testing.T) {
		cat := knownGivens(newCatalog(t), "Андрей").book(1, "Книга")
		cat.unselected(20, 1, "fp-review", "case_defect", flags("all_caps"), person("АНДРЕЕВ", "МИХАИЛ"))
		_, _, ok := authorentity.ReadReviewRule(&cat.in.Unselected[0], authorentity.GivenNamesOf(cat.in.Credits))
		assert.False(t, ok)
		_, _, ok = authorentity.ReadReviewRule(nil, authorentity.GivenNamesOf(cat.in.Credits))
		assert.False(t, ok, "no source, no read")
	})
}

func TestGivenNamesAreKnownBesideFiveFamilies(t *testing.T) {
	cat := knownGivens(newCatalog(t), "Михаил").book(1, "Книга")
	for i := 0; i < 4; i++ {
		id := int64(100 + i)
		cat.book(id, "Книга").credit(id, id, fmt.Sprintf("fp-%d", id), person(fmt.Sprintf("Род%c", 'А'+rune(i)), "Платон"))
	}
	g := authorentity.GivenNamesOf(cat.in.Credits)
	assert.True(t, g.Known("Михаил"))
	assert.True(t, g.Known("МИХАИЛ"), "known by its search key")
	assert.False(t, g.Known("Платон"), "four families are not enough")
	assert.False(t, authorentity.GivenNames(nil).Known("Михаил"))

	cat.book(200, "Книга").credit(200, 200, "fp-200", person("РодД", "Платон"))
	assert.True(t, authorentity.GivenNamesOf(cat.in.Credits).Known("Платон"))
}

func TestTheTriageSharesTheBuildsNameTests(t *testing.T) {
	assert.True(t, authorentity.OrganizationName(`Журнал "Юный техник"`))
	assert.True(t, authorentity.OrganizationName(`«Подвиг»`))
	assert.False(t, authorentity.OrganizationName("Михаил Андреев"))

	cat := newCatalog(t).book(1, "Книга")
	cat.unselected(20, 1, "fp-a", "particle", flags("particle"), person("де Мопассан", "Ги"))
	cat.unselected(21, 1, "fp-b", "particle", flags("particle"), person("Де", "Мопассан", "Ги"))
	assert.True(t, cat.in.Unselected[0].ParticleOnly(), "Ги | де Мопассан")
	assert.False(t, cat.in.Unselected[1].ParticleOnly(), "a family that ends in a particle")
}
