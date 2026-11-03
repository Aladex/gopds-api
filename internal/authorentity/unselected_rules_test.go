package authorentity_test

import (
	"fmt"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// The deterministic reads of the review classes (E4l).

// knownGivens gives the catalog selected credits under each given name,
// beside five families each: a given name the catalog knows.
func knownGivens(c *catalog, names ...string) *catalog {
	id := int64(5000)
	for _, g := range names {
		for i := 0; i < 5; i++ {
			id++
			c.book(id, fmt.Sprintf("Книга %d", id)).credit(id, id, fmt.Sprintf("fp-%d", id),
				person(fmt.Sprintf("Фамилия%c", 'А'+rune(i)), g))
		}
	}
	return c
}

func TestAReviewNameIsReadAsSelected(t *testing.T) {
	for title, c := range map[string]struct {
		class   string
		flags   []string
		n       *name
		src     [3]string
		display string
	}{
		"all caps":   {class: "case_defect", flags: flags("all_caps"), n: person("JONQUET", "THIERRY").latin(), display: "Thierry Jonquet"},
		"lower case": {class: "case_defect", flags: flags("lowercase"), n: person("keller", "nora").latin(), display: "Nora Keller"},
		"look-alike letters": {class: "mixed_script", flags: flags("mixed_script"),
			n: person("Коломицкий", "Андрeй").mixed(), display: "Андрей Коломицкий"},
		"a Latin nickname": {class: "mixed_script", flags: flags("mixed_script", "nickname"),
			n: person("Бобров", "Всеволод").withNickname("Vector").mixed(), display: "Всеволод Бобров Vector"},
		"a Latin name with a nickname": {class: "nickname_with_name", flags: flags("nickname"),
			n: person("Metzen", "Chris").withNickname("Thundergod").latin(), display: "Chris Metzen Thundergod"},
		"one field": {class: "single_field", flags: flags("unstructured", "empty_component"),
			n: &name{script: "Cyrl"}, src: [3]string{"", "", "Дейзи Уитни"}, display: "Дейзи Уитни"},
		"one field, the family first": {class: "single_field", flags: flags("unstructured", "empty_component"),
			n: &name{script: "Cyrl"}, src: [3]string{"", "", "Винниченко Володимир"}, display: "Володимир Винниченко"},
		"a title": {class: "affix", flags: flags("affix"), n: person("Бабкин", "Священник Николай"),
			display: "Николай Бабкин"},
		"a one-word particle given name": {class: "particle", flags: flags("particle"), n: person("Бова", "Бен"),
			display: "Бен Бова"},
	} {
		t.Run(title, func(t *testing.T) {
			cat := knownGivens(newCatalog(t), "Thierry", "Nora", "Андрей", "Всеволод", "Chris", "Дейзи", "Володимир",
				"Николай").book(1, "Книга")
			u := cat.unselected(20, 1, "fp-review", c.class, c.flags, c.n)
			if c.src != [3]string{} {
				u.fromSource(c.src[0], c.src[1], c.src[2])
			}
			p := cat.build()
			id := p.EntityOf(20)
			if id == uuid.Nil {
				t.Fatal("want an entity")
			}
			assert.Equal(t, c.display, p.Entity(id).DisplayName)
		})
	}
}

func TestAReviewNameNoReadTrustsStaysOut(t *testing.T) {
	for title, c := range map[string]struct {
		class string
		flags []string
		n     *name
		src   [3]string
	}{
		"an unknown given name, all caps": {class: "case_defect", flags: flags("all_caps"), n: person("БСЭ", "БСЭ")},
		"a phrase in lower case":          {class: "case_defect", flags: flags("lowercase"), n: person("тени", "Джек из")},
		"a magazine in lower case":        {class: "case_defect", flags: flags("lowercase"), n: person("Русская жизнь", "журнал")},
		"a letter with no look-alike":     {class: "mixed_script", flags: flags("mixed_script"), n: person("Стогоff", "Андрей").mixed()},
		"one field, a magazine": {class: "single_field", flags: flags("unstructured"), n: &name{script: "Cyrl"},
			src: [3]string{"", "", "Вокруг Света"}},
		"one field, a given and a patronymic": {class: "single_field", flags: flags("unstructured"), n: &name{script: "Cyrl"},
			src: [3]string{"", "", "Андрей Николаевич"}},
		"one field, three words": {class: "single_field", flags: flags("unstructured"), n: &name{script: "Cyrl"},
			src: [3]string{"", "", "Андрей Петров Сидоров"}},
		"a mononym of no single item": {class: "mononym", flags: flags("single_token"), n: nickOnly("Андрей")},
		"one field, an abbreviation": {class: "single_field", flags: flags("unstructured", "all_caps"),
			n: &name{script: "Cyrl"}, src: [3]string{"", "", "Андрей РФ"}},
	} {
		t.Run(title, func(t *testing.T) {
			cat := knownGivens(newCatalog(t), "Андрей", "Света").book(1, "Книга")
			u := cat.unselected(20, 1, "fp-review", c.class, c.flags, c.n)
			if c.src != [3]string{} {
				u.fromSource(c.src[0], c.src[1], c.src[2])
			}
			none(t, cat.build(), 20)
		})
	}
}

func TestAMononymOfOneItemIsReadAsSelected(t *testing.T) {
	cat := newCatalog(t).book(1, "Кандид").book(2, "Задиг")
	cat.unselected(20, 1, "fp-voltaire", "mononym", flags("single_token", "empty_component"), nickOnly("Вольтер"))
	cat.unselected(21, 2, "fp-voltaire2", "mononym", flags("single_token", "empty_component"), nickOnly("Вольтер"))
	cat.unselected(22, 2, "fp-anna", "mononym", flags("single_token", "empty_component"), nickOnly("Анна"))
	cat.unselected(23, 2, "fp-darya", "mononym", flags("single_token", "empty_component"), nickOnly("Дарья"))
	knownGivens(cat, "Дарья")
	r := newReference().item("Q9068", []string{"Вольтер", "Voltaire"}).
		item("Q1", []string{"Анна"}).item("Q2", []string{"Анна"}).item("Q3", []string{"Дарья"})
	cat.with(r)
	r.ref.ReviewNameKey = map[string]string{"Вольтер": key("Вольтер"), "Анна": key("Анна"), "Дарья": key("Дарья")}
	p := cat.build()
	same(t, p, 20, 21)
	none(t, p, 22)
	none(t, p, 23)
	assert.Equal(t, 2, p.Report.Unselected.Rules[authorentity.ReadMononym])
}
