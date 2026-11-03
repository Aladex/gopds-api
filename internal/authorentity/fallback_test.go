package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fallback entities (E4p): every author credit names an entity.

// leftOut adds a credit of the fallback input — unresolved, malformed, a
// placeholder or a collective under review — of kind and class.
func (c *catalog) leftOut(id, book int64, fp, kind, class, display string, n *name) *catalog {
	c.t.Helper()
	c.next++
	r := &authorentity.Result{ResultID: c.next, Kind: kind, Script: "Cyrl", DisplayName: display, SortName: display}
	if n != nil {
		r.Family, r.Given, r.Additional, r.Nickname = n.family, n.given, n.additional, n.nickname
	}
	c.in.Fallback = append(c.in.Fallback, authorentity.Unselected{
		Credit: authorentity.Credit{ID: id, BookID: book, Fingerprint: fp, ExtractorVersion: "v1", Result: r},
		Class:  class,
	})
	return c
}

// A handle no rule reads is the author's identity: one entity per handle,
// as written.
func TestAHandleNamesAnEntityOfItsOwn(t *testing.T) {
	p := newCatalog(t).book(1, "Кровавый трон [СИ]").book(2, "Кровавый король [СИ]").book(3, "Мост").
		credit(10, 3, "fp-selected", person("Смирнов", "Роман")).
		unselected(20, 1, "fp-handle", "mononym", nil, nickOnly("lucifer712217")).done().
		unselected(21, 2, "fp-handle", "mononym", nil, nickOnly("lucifer712217")).done().build()
	fallback(t, p, 20)
	same(t, p, 20, 21)
	apart(t, p, 10, 20)
	assert.Equal(t, authorentity.RuleAsWritten, p.RuleOf(20))
	assert.Equal(t, "lucifer712217", p.Entity(p.EntityOf(20)).DisplayName)
}

// Collectives, placeholders and organizations name an entity by their name;
// a credit with nothing to name names the placeholder.
func TestACollectiveAPlaceholderAndAnOrganizationNameEntitiesByName(t *testing.T) {
	p := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").book(4, "Г").book(5, "Д").
		collective(10, 1, "fp-stub", "Коллектив авторов").
		collective(11, 2, "fp-stub", "Коллектив авторов").
		leftOut(12, 3, "fp-unknown", "unknown", "placeholder", "Неизвестный автор", nil).
		unselected(13, 4, "fp-journal", "unusual_characters", nil, &name{displayAs: "Журнал «Новый мир»",
			family: "Новый мир", given: "Журнал", script: "Cyrl"}).done().
		leftOut(14, 5, "fp-dot", "malformed", "malformed", ".", nil).build()
	for _, id := range []int64{10, 12, 13} {
		fallback(t, p, id)
		assert.Equal(t, authorentity.RuleAsNamed, p.RuleOf(id), "credit %d", id)
		assert.Equal(t, authorentity.KindCollective, p.Entity(p.EntityOf(id)).Kind, "credit %d", id)
	}
	same(t, p, 10, 11)
	apart(t, p, 10, 12)
	assert.Equal(t, "Журнал «Новый мир»", p.Entity(p.EntityOf(13)).DisplayName)
	assert.Equal(t, authorentity.RulePlaceholder, p.RuleOf(14))
	assert.Equal(t, "Автор не указан", p.Entity(p.EntityOf(14)).DisplayName)
}

// A credit the model resolve left unresolved joins the one entity of its
// name; one of no such name names its own.
func TestAnUnresolvedCreditJoinsTheEntityOfItsName(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Река").book(3, "Лес").
		credit(10, 1, "fp-selected", person("Новый", "Николай", "Николаевич")).
		leftOut(20, 2, "fp-same", "person", "initials", "Николай Николаевич Новый",
			person("Новый", "Николай", "Николаевич")).
		leftOut(21, 3, "fp-other", "person", "initials", "М. Левуш", person("Левуш", "М.")).build()
	same(t, p, 10, 20)
	assert.Equal(t, authorentity.RuleSameName, p.RuleOf(20))
	fallback(t, p, 21)
}

// A pair of two people the pass has names both, a row per segment;
// otherwise it is a collective.
func TestAPairNamesBothPeople(t *testing.T) {
	c := newCatalog(t).book(1, "Сказки").book(2, "Предания").book(3, "Похищение").
		credit(10, 1, "fp-anna", person("Литвинова", "Анна")).
		credit(11, 2, "fp-sergey", person("Литвинов", "Сергей")).
		leftOut(20, 3, "fp-pair", "collective", "collective_candidate", "Анна и Сергей Литвиновы", nil)
	p := c.build()
	assert.Equal(t, p.EntityOf(10), p.EntityOf(20))
	assert.Equal(t, authorentity.RulePair, p.RuleOf(20))
	state := authorentity.NewState()
	p.Apply(state)
	second, ok := state.Fingerprints[authorentity.FPKey{Fingerprint: "fp-pair", ExtractorVersion: "v1", Ordinal: 1}]
	require.True(t, ok, "the second segment's row")
	assert.Equal(t, p.EntityOf(11), second.EntityID)

	alone := newCatalog(t).book(1, "Сказки").book(3, "Похищение").
		credit(10, 1, "fp-anna", person("Литвинова", "Анна")).
		leftOut(20, 3, "fp-pair", "collective", "collective_candidate", "Анна и Сергей Литвиновы", nil).build()
	assert.Equal(t, authorentity.RuleAsNamed, alone.RuleOf(20))
	apart(t, alone, 10, 20)
}

// A pass over its own rows writes nothing more: the fallback entities keep
// their IDs.
func TestTheFallbackEntitiesAreStable(t *testing.T) {
	c := newCatalog(t).book(1, "А").book(2, "Б").
		credit(10, 1, "fp-selected", person("Смирнов", "Роман")).
		unselected(20, 2, "fp-handle", "mononym", nil, nickOnly("Shin_Stark")).done().
		leftOut(21, 2, "fp-unknown", "unknown", "placeholder", "Unknown", nil)
	first, s := stored(t, c)
	second := rebuild(t, c, s)
	assert.Equal(t, first.EntityOf(20), second.EntityOf(20))
	assert.Equal(t, first.EntityOf(21), second.EntityOf(21))
	assert.Zero(t, rebuild(t, c, s).Changes())
}

// A proposal that lost its given name, or a name with a pen name in
// parentheses, joins the one entity its display name names; a part in
// parentheses that names a place tells a namesake apart and keeps it out.
func TestTheDisplayNameJoinsTheEntityOfItsName(t *testing.T) {
	lost := &name{family: "Брянчанинов", script: "Cyrl", displayAs: "Игнатий Брянчанинов"}
	pen := &name{family: "Дар", given: "Фредерик", script: "Cyrl", displayAs: "Фредерик Дар (Сан-Антонио)"}
	place := &name{family: "Смирнов", given: "Сергей", additional: []string{"(Москва)"}, script: "Cyrl",
		displayAs: "Сергей Смирнов (Москва)"}
	p := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").book(4, "Г").book(5, "Д").book(6, "Е").
		credit(10, 1, "fp-ignaty", person("Брянчанинов", "Игнатий")).
		credit(11, 2, "fp-dard", person("Дар", "Фредерик")).
		credit(12, 3, "fp-smirnov", person("Смирнов", "Сергей")).
		unselected(20, 4, "fp-lost", "partial_structure", nil, lost).done().
		unselected(21, 5, "fp-pen", "unusual_characters", nil, pen).done().
		unselected(22, 6, "fp-place", "unusual_characters", nil, place).done().build()
	same(t, p, 10, 20)
	same(t, p, 11, 21)
	assert.Equal(t, authorentity.RuleSameName, p.RuleOf(20))
	fallback(t, p, 22)
	apart(t, p, 12, 22)
}
