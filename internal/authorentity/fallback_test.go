package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
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

// A proposal that lost its given name joins the one entity its display
// name names. A part in parentheses is dropped for that only when it is
// known to be no other person: a role, a spelling of the name's own word,
// or a name the entity is already credited by; anything else — a pen name the catalog does not show, a
// place, «не Воха» — may tell a namesake apart and keeps the credit out.
func TestTheDisplayNameJoinsTheEntityOfItsName(t *testing.T) {
	lost := &name{family: "Брянчанинов", script: "Cyrl", displayAs: "Игнатий Брянчанинов"}
	role := &name{family: "Стариков", given: "Николай", script: "Cyrl", displayAs: "Николай Стариков (составитель)"}
	spelling := &name{family: "Дашнер", given: "Джеймс", script: "Cyrl", displayAs: "Джеймс Дашнер (Дэшнер)"}
	alias := &name{family: "Суворов", given: "Виктор", script: "Cyrl", displayAs: "Виктор Суворов (Резун)"}
	pen := &name{family: "Дар", given: "Фредерик", script: "Cyrl", displayAs: "Фредерик Дар (Сан-Антонио)"}
	place := &name{family: "Смирнов", given: "Сергей", additional: []string{"(Москва)"}, script: "Cyrl",
		displayAs: "Сергей Смирнов (Москва)"}
	city := &name{family: "Васильев", given: "Владимир", additional: []string{"(Ташкент)"}, script: "Cyrl",
		displayAs: "Владимир Васильев (Ташкент)"}
	not := &name{family: "Васильев (не Воха)", given: "Владимир", script: "Cyrl",
		displayAs: "Владимир Васильев (не Воха)"}
	p := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").book(4, "Г").book(5, "Д").book(6, "Е").
		book(7, "Ж").book(8, "З").book(9, "И").book(10, "К").book(11, "Л").book(12, "М").book(13, "Н").
		credit(10, 1, "fp-ignaty", person("Брянчанинов", "Игнатий")).
		credit(11, 2, "fp-dard", person("Дар", "Фредерик")).
		credit(12, 3, "fp-smirnov", person("Смирнов", "Сергей")).
		credit(13, 4, "fp-vasiliev", person("Васильев", "Владимир")).
		credit(14, 5, "fp-starikov", person("Стариков", "Николай")).
		credit(15, 6, "fp-suvorov", person("Суворов", "Виктор")).
		credit(16, 7, "fp-rezun", person("Резун", "Владимир")).
		unselected(20, 8, "fp-lost", "partial_structure", nil, lost).done().
		unselected(21, 9, "fp-pen", "unusual_characters", nil, pen).done().
		unselected(22, 10, "fp-place", "unusual_characters", nil, place).done().
		unselected(23, 11, "fp-city", "unusual_characters", nil, city).done().
		unselected(24, 12, "fp-not", "unusual_characters", nil, not).done().
		unselected(25, 13, "fp-role", "unusual_characters", nil, role).done().
		book(14, "О").unselected(26, 14, "fp-alias", "unusual_characters", nil, alias).done().
		book(15, "Бегущий в лабиринте").book(16, "Лихорадка").credit(17, 15, "fp-dashner", person("Дашнер", "Джеймс")).
		unselected(27, 16, "fp-spelling", "unusual_characters", nil, spelling).done().
		build()
	same(t, p, 10, 20)
	assert.Equal(t, authorentity.RuleSameName, p.RuleOf(20))
	same(t, p, 14, 25)
	same(t, p, 17, 27)
	for _, ci := range []int64{21, 22, 23, 24} {
		fallback(t, p, ci)
	}
	apart(t, p, 11, 21)
	apart(t, p, 12, 22)
	apart(t, p, 13, 23)
	apart(t, p, 13, 24)
	apart(t, p, 23, 24)
	// «Резун» is a name the joined entity carries only when a credit of
	// the entity is written so: here it is its own person, so no join.
	apart(t, p, 15, 26)
}

// A part in parentheses that the entity is credited by elsewhere is an
// alias of its person: «Леонид (LLeo) Каганов» joins the «Леонид Каганов»
// a selected credit «Леонид Каганов LLeo» is part of, and the parsed name
// that leaves the nickname out never joins by itself.
func TestAQualifierTheEntityCarriesJoins(t *testing.T) {
	alias := &name{family: "Каганов", given: "Леонид", nickname: "LLeo", script: "Cyrl",
		displayAs: "Леонид (LLeo) Каганов"}
	other := &name{family: "Каганов", given: "Леонид", nickname: "Лео", script: "Cyrl",
		displayAs: "Леонид (Лео) Каганов"}
	withNick := person("Каганов", "Леонид").withNickname("LLeo")
	withNick.displayAs = "Леонид Каганов LLeo"
	p := newCatalog(t).book(1, "Хомяк в совятнике").book(2, "Коммутация").book(3, "Майские").book(4, "Другое").
		credit(10, 1, "fp-kaganov", person("Каганов", "Леонид")).
		credit(11, 2, "fp-kaganov-lleo", withNick).
		unselected(20, 3, "fp-alias", "unusual_characters", nil, alias).done().
		unselected(21, 4, "fp-other", "unusual_characters", nil, other).done().build()
	require.Equal(t, p.EntityOf(10), p.EntityOf(11), "the fixture's entity carries both spellings")
	same(t, p, 10, 20)
	assert.Equal(t, authorentity.RuleSameName, p.RuleOf(20))
	apart(t, p, 10, 21)
}

// knownGiven adds selected people of given across knownGiven families, so the
// catalog knows the name.
func (c *catalog) knownGiven(given string, book, id int64) *catalog {
	for i, f := range []string{"Аров", "Боров", "Варов", "Гаров", "Даров"} {
		n := id + int64(i)
		c.book(book+int64(i), "Книга").credit(n, book+int64(i), "fp-known-"+given+f, person(f, given))
	}
	return c
}

// A list of names is a segment per person: one joins the entity of its
// name, the others get their own; «Family, Given» is one person; an
// organization with a comma stays one collective.
func TestAListOfNamesIsOnePersonPerName(t *testing.T) {
	list := &name{displayAs: "Александр Силецкий, Эзиз Овезов, Станислав Лем", script: "Cyrl",
		given: "Александр", family: "Силецкий"}
	inverted := &name{displayAs: "Feifer, George", script: "Latn", family: "Feifer, George"}
	org := &name{displayAs: "Общество Сторожевой Башни, Библий и трактатов Пенсильвании", script: "Cyrl",
		family: "Общество"}
	richelieu := &name{displayAs: "Арман Жан, де Ришелье", script: "Cyrl", family: "Ришелье"}
	c := newCatalog(t).book(1, "Солярис").book(2, "Сборник").book(3, "Russia").book(4, "Журнал").
		credit(10, 1, "fp-lem", person("Лем", "Станислав")).
		unselected(20, 2, "fp-list", "unusual_characters", nil, list).done().
		unselected(21, 3, "fp-inverted", "unusual_characters", nil, inverted).done().
		unselected(22, 4, "fp-org", "unusual_characters", nil, org).done().
		book(5, "Политическое завещание").
		unselected(23, 5, "fp-richelieu", "unusual_characters", nil, richelieu).done()
	c.knownGiven("Александр", 100, 100).knownGiven("Эзиз", 200, 200).knownGiven("George", 300, 300).
		knownGiven("Станислав", 400, 400).knownGiven("Жан", 500, 500)
	p := c.build()
	state := authorentity.NewState()
	p.Apply(state)
	row := func(ordinal int) authorentity.AssignmentRow {
		return state.Fingerprints[authorentity.FPKey{Fingerprint: "fp-list", ExtractorVersion: "v1", Ordinal: ordinal}]
	}
	assert.Equal(t, p.EntityOf(10), row(2).EntityID, "Станислав Лем joins his entity")
	assert.Equal(t, authorentity.RuleSameName, row(2).Rule)
	assert.Equal(t, authorentity.RuleListed, row(0).Rule)
	assert.NotEqual(t, row(0).EntityID, row(1).EntityID)
	assert.Equal(t, "Эзиз Овезов", p.Entity(row(1).EntityID).DisplayName)
	assert.Equal(t, "George Feifer", p.Entity(p.EntityOf(21)).DisplayName)
	assert.Equal(t, authorentity.KindPerson, p.Entity(p.EntityOf(21)).Kind)
	assert.Equal(t, authorentity.KindCollective, p.Entity(p.EntityOf(22)).Kind)
	_, segmented := state.Fingerprints[authorentity.FPKey{Fingerprint: "fp-richelieu", ExtractorVersion: "v1", Ordinal: 1}]
	assert.False(t, segmented, "«де Ришелье» ends a name")
}

// Initials written onto the family are a name of their own in a list.
func TestAListOfGluedInitialsSplits(t *testing.T) {
	list := &name{displayAs: "П.И.Бакулин, Э.В.Кононович, В.И. Мороз", script: "Cyrl", family: "Бакулин"}
	p := newCatalog(t).book(1, "Курс общей астрономии").
		unselected(20, 1, "fp-list", "unusual_characters", nil, list).done().build()
	state := authorentity.NewState()
	p.Apply(state)
	for i, want := range []string{"П.И. Бакулин", "Э.В. Кононович", "В.И. Мороз"} {
		row := state.Fingerprints[authorentity.FPKey{Fingerprint: "fp-list", ExtractorVersion: "v1", Ordinal: i}]
		require.NotNil(t, p.Entity(row.EntityID), "segment %d", i)
		assert.Equal(t, want, p.Entity(row.EntityID).DisplayName)
	}
}

// A name of one word joins the one entity a selected credit of its written
// name stands in: «О`Санчес.» beside «О'Санчес».
func TestAWrittenNameJoinsItsEntity(t *testing.T) {
	tick := &name{displayAs: "О`Санчес.", nickname: "О`Санчес.", script: "Cyrl"}
	p := newCatalog(t).book(1, "Кромешник").book(2, "Хвак").
		credit(10, 1, "fp-sanchez", shown("О'Санчес", nickOnly("О'Санчес"))).
		unselected(20, 2, "fp-tick", "mononym", nil, tick).done().build()
	same(t, p, 10, 20)
	assert.Equal(t, authorentity.RuleSameName, p.RuleOf(20))
}

// A list's segments keep their entities: a second identical pass over what
// the first stored changes nothing, and every segment, the ones that made
// an entity of their own included, keeps its ID.
func TestAListKeepsItsSegmentEntities(t *testing.T) {
	list := &name{displayAs: "Александр Силецкий, Эзиз Овезов, Римма Кошурникова", script: "Cyrl",
		given: "Александр", family: "Силецкий"}
	c := newCatalog(t).book(1, "Сборник").
		unselected(20, 1, "fp-list", "unusual_characters", nil, list).done()
	c.knownGiven("Александр", 100, 100).knownGiven("Эзиз", 200, 200).knownGiven("Римма", 300, 300)
	_, s := stored(t, c)
	row := func(s *authorentity.State, ordinal int) authorentity.AssignmentRow {
		return s.Fingerprints[authorentity.FPKey{Fingerprint: "fp-list", ExtractorVersion: "v1", Ordinal: ordinal}]
	}
	var first []uuid.UUID
	for i := range 3 {
		require.Equal(t, authorentity.RuleListed, row(s, i).Rule, "segment %d", i)
		first = append(first, row(s, i).EntityID)
	}
	require.Len(t, map[uuid.UUID]bool{first[0]: true, first[1]: true, first[2]: true}, 3)
	second := rebuild(t, c, s)
	assert.Zero(t, second.Changes())
	for i := range 3 {
		assert.Equal(t, first[i], row(s, i).EntityID, "segment %d keeps its entity", i)
	}
	assert.Zero(t, rebuild(t, c, s).Changes())
}

// A list whose credit the same-name join takes whole («Михаил Ежов» with
// his pen names in the nickname) makes no entity for the segment the join
// took: a second pass changes nothing.
func TestAListTheJoinTakesMakesNoEmptyEntity(t *testing.T) {
	list := &name{family: "Ежов", given: "Михаил", nickname: "Виктор Глебов; Майк Германов", script: "Cyrl",
		displayAs: "Михаил Ежов Виктор Глебов; Майк Германов"}
	c := newCatalog(t).book(1, "Мир").book(2, "Война").
		credit(10, 1, "fp-ezhov", person("Ежов", "Михаил")).
		unselected(20, 2, "fp-list", "unusual_characters", nil, list).done()
	c.knownGiven("Михаил", 100, 100).knownGiven("Майк", 200, 200)
	first, s := stored(t, c)
	require.Equal(t, first.EntityOf(10), first.EntityOf(20))
	for id, e := range s.Entities {
		used := false
		for _, row := range s.Fingerprints {
			used = used || row.EntityID == id
		}
		assert.True(t, used, "entity %s (%s) has no row", id, e.DisplayName)
	}
	assert.Zero(t, rebuild(t, c, s).Changes())
}
