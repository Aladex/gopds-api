package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A name the source wrote with Russian stress marks keys like the plain
// one: one entity, whatever the spelling (E4r).
func TestStressMarksKeyLikeThePlainName(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Река").book(3, "Лес").
		credit(10, 1, "fp-plain", person("Шостакович", "Дмитрий", "Дмитриевич")).
		credit(11, 2, "fp-stressed", person("Шостако́вич", "Дми́трий", "Дми́триевич")).
		credit(12, 3, "fp-other", person("Познер", "Владимир", "Соломонович")).build()
	same(t, p, 10, 11)
	apart(t, p, 10, 12)
}

// A stray stress mark before its letter is no part of the name either.
func TestAStrayMarkBeforeItsLetterJoinsThePlainName(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Река").
		credit(10, 1, "fp-plain", person("Познер", "Владимир", "Соломонович")).
		credit(11, 2, "fp-stray", person("Познер", "Владимир", "́Соломонович")).build()
	same(t, p, 10, 11)
}

// A review credit of a stressed name joins the entity of the plain one.
func TestAStressedReviewCreditJoinsTheEntityOfItsName(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Река").
		credit(10, 1, "fp-plain", person("Марцеллин", "Аммиан")).
		unselected(20, 2, "fp-stressed", "unusual_characters", nil,
			&name{displayAs: "Аммиа́н Марцелли́н", family: "Марцелли́н", given: "Аммиа́н",
				script: "Cyrl"}).done().build()
	same(t, p, 10, 20)
}

// A publication a person-kind proposal carries names no person: the first
// word's hyphen part an organization word, or a comma part that begins
// with a lower-case word a name never begins with (E4r).
func TestPublicationsAreCollectivesHoweverTheyAreWritten(t *testing.T) {
	p := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").book(4, "Г").book(5, "Д").book(6, "Е").
		unselected(20, 1, "fp-net", "unusual_characters", nil, &name{displayAs: "Интернет-журнал Виноградова №4, 2011",
			family: "№4, 2011", given: "Интернет-журнал Виноградова", script: "Cyrl"}).done().
		unselected(21, 2, "fp-editor", "unusual_characters", nil, &name{displayAs: "Горкин А. П., гл. редактор",
			script: "Cyrl"}).done().
		unselected(22, 3, "fp-write", "unusual_characters", nil, &name{displayAs: "Автор, пиши еще!",
			script: "Cyrl"}).done().
		unselected(23, 4, "fp-notes", "unusual_characters", nil,
			&name{displayAs: "Конспекты, шпаргалки, учебники «ЭКСМО»", script: "Cyrl"}).done().
		unselected(24, 5, "fp-particle", "unusual_characters", nil, &name{displayAs: "Арман Жан, де Ришелье",
			script: "Cyrl"}).done().
		unselected(25, 6, "fp-suffix", "unusual_characters", nil, &name{displayAs: "Д М Маршалл, мл.",
			script: "Cyrl"}).done().
		unselected(26, 1, "fp-artist", "unusual_characters", nil,
			&name{displayAs: "Питер Браун художник, детский писатель", script: "Cyrl"}).done().build()
	for _, id := range []int64{20, 21, 22, 23} {
		assert.Equal(t, authorentity.RuleAsNamed, p.RuleOf(id), "credit %d", id)
		assert.Equal(t, authorentity.KindCollective, p.Entity(p.EntityOf(id)).Kind, "credit %d", id)
	}
	assert.Equal(t, "Интернет-журнал Виноградова №4, 2011", p.Entity(p.EntityOf(20)).DisplayName)
	// A particle after the comma, a generational suffix and a plain
	// occupation stay a name.
	assert.Equal(t, authorentity.RuleAsWritten, p.RuleOf(24))
	assert.Equal(t, authorentity.RuleAsWritten, p.RuleOf(25))
	assert.Equal(t, authorentity.RuleAsWritten, p.RuleOf(26))
}

// A comma pair of two names nobody knows is still two people: a segment
// per name, joining the entity of its name when there is one (E4r).
func TestACommaPairOfNamesIsTwoPeople(t *testing.T) {
	p := newCatalog(t).book(1, "Табу").book(2, "Сборник").
		credit(10, 2, "fp-shaw", person("Шоу", "Дженика")).
		leftOut(20, 1, "fp-pair", "person", "unusual_characters", "Дженика Шоу, Сем Крезент",
			&name{family: "Крезент", given: "Дженика"}).build()
	assert.Equal(t, authorentity.RuleSameName, p.RuleOf(20))
	same(t, p, 10, 20)
	state := authorentity.NewState()
	p.Apply(state)
	second, ok := state.Fingerprints[authorentity.FPKey{Fingerprint: "fp-pair", ExtractorVersion: "v1", Ordinal: 1}]
	require.True(t, ok, "the second segment's row")
	assert.NotEqual(t, p.EntityOf(10), second.EntityID)
	assert.Equal(t, authorentity.RuleListed, second.Rule)
}

// A name and a title of the same person is one person, not a pair: a
// part that is a title and one word («Герцог Сассекский»), or a word the
// other part has at another place in the name («Гарри Виндзор»). Two
// brothers who share the family stay two people.
func TestANameAndATitleAreOnePerson(t *testing.T) {
	segments := func(p *authorentity.Plan, fp string) int {
		state := authorentity.NewState()
		p.Apply(state)
		n := 0
		for k := range state.Fingerprints {
			if k.Fingerprint == fp {
				n++
			}
		}
		return n
	}
	p := newCatalog(t).book(1, "Запасной").book(2, "Интервью").book(3, "Пикник на обочине").book(4, "Другое").
		credit(10, 4, "fp-harry", person("Гарри", "Принц")).
		leftOut(20, 1, "fp-title", "person", "unusual_characters", "Принц Гарри, Герцог Сассекский",
			&name{family: "Сассекский", given: "Принц"}).
		leftOut(21, 2, "fp-alias", "person", "unusual_characters", "Принц Гарри, Гарри Виндзор",
			&name{family: "Виндзор", given: "Принц"}).
		leftOut(22, 3, "fp-brothers", "person", "unusual_characters", "Аркадий Стругацкий, Борис Стругацкий",
			&name{family: "Стругацкий", given: "Аркадий"}).
		book(5, "Сказки").leftOut(23, 5, "fp-three", "person", "unusual_characters",
		"Вера Ковальчук Ярослав Коваль, Игорь Ковальчук", &name{family: "Ковальчук", given: "Вера"}).
		book(6, "Вегетарианка").leftOut(24, 6, "fp-han", "person", "unusual_characters",
		"Хан Ган, Дебора Смит", &name{family: "Смит", given: "Хан"}).
		book(7, "Письма").leftOut(25, 7, "fp-saints", "person", "unusual_characters",
		"Святитель Игнатий, Святитель Феофан", &name{family: "Феофан", given: "Святитель"}).build()
	assert.Equal(t, 1, segments(p, "fp-title"), "«Принц Гарри, Герцог Сассекский» is one person")
	assert.Equal(t, 1, segments(p, "fp-alias"), "«Принц Гарри, Гарри Виндзор» is one person")
	assert.Equal(t, 2, segments(p, "fp-brothers"), "two brothers are two people")
	assert.Equal(t, 2, segments(p, "fp-three"), "a family inside a part is no alias")
	assert.Equal(t, 2, segments(p, "fp-han"), "«Хан» is a name, not a rank")
	assert.Equal(t, 2, segments(p, "fp-saints"), "a clerical title is no alias")
	assert.NotEqual(t, uuid.Nil, p.EntityOf(20))
}

// A live entity no row names any more — its credits all joined another
// entity in an earlier pass — is removed, and the pass after that writes
// nothing (E4r).
func TestAnEntityNothingNamesAnyMoreIsRemoved(t *testing.T) {
	c := newCatalog(t).book(1, "Мост").book(2, "Река").
		credit(10, 1, "fp-real", person("Петров", "Иван", "Сергеевич"))
	first, s := stored(t, c)
	require.NotEqual(t, uuid.Nil, first.EntityOf(10))
	// A fallback credit of its own name arrives and is stored.
	c.leftOut(20, 2, "fp-alone", "person", "unusual_characters", "Сидор Сидоров", nil)
	second := rebuild(t, c, s)
	alone := second.EntityOf(20)
	require.NotEqual(t, uuid.Nil, alone)
	// A selected credit of that name arrives: the fallback credit joins its
	// entity, and its own entity loses its only row.
	c.credit(21, 2, "fp-joined", person("Сидоров", "Сидор"))
	third := rebuild(t, c, s)
	require.Equal(t, third.EntityOf(21), third.EntityOf(20))
	if _, ok := s.Entities[alone]; !ok {
		t.Fatalf("the entity row went before its rows did")
	}
	// Nothing names it any more: the next pass removes the row itself.
	fourth := rebuild(t, c, s)
	if _, ok := s.Entities[alone]; ok {
		t.Errorf("the abandoned entity row stayed")
	}
	if fourth.Report.Retired != 1 {
		t.Errorf("retired: got %d", fourth.Report.Retired)
	}
	if n := rebuild(t, c, s).Changes(); n != 0 {
		t.Errorf("a pass after the removal: want 0 changes, got %d", n)
	}
}

// An entity a pinned name holds stays.
func TestAPinnedEntityIsNotRemoved(t *testing.T) {
	c := newCatalog(t).book(1, "Мост").
		credit(10, 1, "fp-real", person("Петров", "Иван", "Сергеевич"))
	first, s := stored(t, c)
	id := first.EntityOf(10)
	row := s.Entities[id]
	row.NamePinned = true
	s.Entities[id] = row
	c.in.Credits = nil
	if second := rebuild(t, c, s); second.Report.Retired != 0 {
		t.Errorf("a pinned entity was retired")
	}
}
