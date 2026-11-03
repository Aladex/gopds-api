package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The credits under review (E4k). A credit whose result is only a proposal
// joins the one entity of exactly its name — the name a selected credit
// already carries — and never starts one; a credit whose only doubt is a
// name particle is read as selected.

// mixed is a name the normalizer gives the mixed script: a Latin nickname
// beside a Cyrillic name, a Latin look-alike letter.
func (n *name) mixed() *name { n.script = "mixed"; return n }

func flags(f ...string) []string { return f }

func TestAnUnselectedCreditJoinsTheEntityOfItsName(t *testing.T) {
	for title, c := range map[string]struct {
		class string
		flags []string
		n     *name
		src   [3]string
		rule  string
	}{
		"a Latin nickname beside the name": {class: "mixed_script", flags: flags("mixed_script", "nickname"),
			n: person("Смирнов", "Роман").withNickname("roman00").mixed()},
		"all caps": {class: "case_defect", flags: flags("all_caps"), n: person("СМИРНОВ", "РОМАН")},
		"a title in the given name": {class: "affix", flags: flags("affix"),
			n: person("Смирнов", "протоиерей Роман")},
		"a Latin look-alike letter": {class: "mixed_script", flags: flags("mixed_script"),
			n: person("Cмирнов", "Роман").mixed()},
		"one field the source did not split": {class: "single_field", flags: flags("unstructured"),
			n: &name{script: "Cyrl"}, src: [3]string{"", "", "Роман Смирнов"}},
		"the words in another order": {class: "partial_structure", flags: flags("partial_structure"),
			n: person("", "Смирнов", "Роман")},
	} {
		t.Run(title, func(t *testing.T) {
			cat := newCatalog(t).book(1, "Рассвет").book(2, "Закат").
				credit(10, 1, "fp-selected", person("Смирнов", "Роман"))
			u := cat.unselected(20, 2, "fp-review", c.class, c.flags, c.n)
			if c.src != [3]string{} {
				u.fromSource(c.src[0], c.src[1], c.src[2])
			}
			p := cat.build()
			same(t, p, 10, 20)
			if got := p.RuleOf(20); got != authorentity.RuleSameName {
				t.Errorf("rule %q, want %q", got, authorentity.RuleSameName)
			}
			if p.Report.Unselected.SameName != 1 {
				t.Errorf("report %+v, want one same-name join", p.Report.Unselected)
			}
		})
	}
}

func TestAJoinedCreditIsStoredAndNamesNoOne(t *testing.T) {
	cat := newCatalog(t).book(1, "Рассвет").book(2, "Закат").
		credit(10, 1, "fp-selected", person("Смирнов", "Роман")).
		unselected(20, 2, "fp-review", "case_defect", flags("all_caps"), person("СМИРНОВ", "РОМАН")).done()
	p := cat.build()
	id := p.EntityOf(10)
	state := authorentity.NewState()
	p.Apply(state)
	row, ok := state.Fingerprints[authorentity.FPKey{Fingerprint: "fp-review", ExtractorVersion: "v1"}]
	if !ok || row.EntityID != id || row.Rule != authorentity.RuleSameName ||
		row.Method != authorentity.MethodDeterministic {
		t.Fatalf("the joined credit's row %+v (stored %v), want %v by %s", row, ok, id, authorentity.RuleSameName)
	}
	if e := p.Entity(id); e == nil || e.DisplayName != "Роман Смирнов" {
		t.Errorf("entity %+v: the selected spelling names it", e)
	}
	// The selected credit gone, nothing is left to join: the credit names
	// its own entity, as written (E4p).
	cat.in.Credits = nil
	cat.in.Old = state
	next := cat.build()
	fallback(t, next, 20)
	next.Apply(state)
	if row := state.Fingerprints[authorentity.FPKey{Fingerprint: "fp-review", ExtractorVersion: "v1"}]; row.Rule !=
		authorentity.RuleAsWritten {
		t.Errorf("the credit's row %+v, want %s", row, authorentity.RuleAsWritten)
	}
}

func TestAnUnselectedCreditStartsNoEntity(t *testing.T) {
	for title, c := range map[string]struct {
		class string
		flags []string
		n     *name
	}{
		"no entity of its name": {class: "case_defect", flags: flags("all_caps"), n: person("ПЕТРОВ", "РОМАН")},
		"several people in one credit": {class: "duplicate_component", flags: flags("duplicate_component"),
			n: person("Смирнов", "Роман")},
		"a placeholder":    {class: "case_defect", flags: flags("lowercase"), n: person("Автор", "неизвестен")},
		"swapped initials": {class: "initials", flags: flags("initials"), n: person("Смирнов", "Р", "В")},
		"one word":         {class: "mononym", flags: flags("single_token"), n: person("Смирнов", "")},
		"a namesake tag": {class: "case_defect", flags: flags("all_caps"),
			n: person("СМИРНОВ", "РОМАН").withTag(2)},
	} {
		t.Run(title, func(t *testing.T) {
			p := newCatalog(t).book(1, "Рассвет").book(2, "Закат").
				credit(10, 1, "fp-selected", person("Смирнов", "Роман")).
				credit(11, 1, "fp-initials", person("Смирнов", "В", "Р")).
				credit(12, 1, "fp-family", person("Смирнов", "")).
				unselected(20, 2, "fp-review", c.class, c.flags, c.n).done().build()
			fallback(t, p, 20)
		})
	}
}

func TestTwoEntitiesOfTheNameJoinNeither(t *testing.T) {
	p := newCatalog(t).book(1, "Рассвет").book(2, "Закат").book(3, "Полдень").
		credit(10, 1, "fp-one", person("Смирнов", "Роман").withTag(1)).
		credit(11, 3, "fp-two", person("Смирнов", "Роман").withTag(2)).
		unselected(20, 2, "fp-review", "case_defect", flags("all_caps"), person("СМИРНОВ", "РОМАН")).done().build()
	apart(t, p, 10, 11)
	fallback(t, p, 20)
	if p.Report.Unselected.Ambiguous != 1 {
		t.Errorf("report %+v, want one left out for several entities", p.Report.Unselected)
	}
}

func TestAJoinedCreditMovesNoEvidence(t *testing.T) {
	// The short name's book shares a title with the full name only through
	// the joined credit: D3 moves nothing for it.
	p := newCatalog(t).book(1, "Рассвет").book(2, "Рассвет").book(3, "Закат").
		credit(10, 1, "fp-full", person("Смирнов", "Роман", "Петрович")).
		credit(11, 3, "fp-short", person("Смирнов", "Роман")).
		unselected(20, 2, "fp-review", "case_defect", flags("all_caps"), person("СМИРНОВ", "РОМАН")).done().build()
	apart(t, p, 10, 11)
	same(t, p, 11, 20)
}

func TestAParticleNameIsReadAsSelected(t *testing.T) {
	cat := newCatalog(t).book(1, "Судья Ди").book(2, "Убийство в лабиринте").
		unselected(20, 1, "fp-gulik", "particle", flags("particle"), person("ван Гулик", "Роберт")).done().
		unselected(21, 2, "fp-gulik2", "particle", flags("particle", "hyphen"), person("ван Гулик", "Роберт")).done()
	p := cat.build()
	same(t, p, 20, 21)
	if r := p.RuleOf(20); r == authorentity.RuleSameName || r == "" {
		t.Errorf("rule %q: a particle name is an entity of its own", r)
	}
	if p.Report.Unselected.Particles != 2 {
		t.Errorf("report %+v, want two particle credits read", p.Report.Unselected)
	}
}

func TestAParticleNameJoinsTheEntityOfItsNameFirst(t *testing.T) {
	p := newCatalog(t).book(1, "Слан").book(2, "Ультраземлянин").
		credit(10, 1, "fp-selected", person("Ван Вогт", "Альфред", "Элтон")).
		unselected(20, 2, "fp-review", "particle", flags("particle"), person("Вогт", "Альфред", "Элтон Ван")).
		done().build()
	same(t, p, 10, 20)
	if r := p.RuleOf(20); r != authorentity.RuleSameName {
		t.Errorf("rule %q, want %q", r, authorentity.RuleSameName)
	}
}

func TestAParticleNameWithAnotherDoubtStaysOut(t *testing.T) {
	for title, c := range map[string]struct {
		flags []string
		n     *name
	}{
		"a nickname as well":     {flags: flags("particle", "nickname"), n: person("ван Гулик", "Роберт").withNickname("Ди")},
		"an empty component":     {flags: flags("particle", "empty_component"), n: person("ван Гулик", "")},
		"a family of a particle": {flags: flags("particle"), n: person("Де", "Камп", "Лион")},
		"a given of a particle, the family led by one": {flags: flags("particle"),
			n: person("де Камп", "Ла")},
		"a given led by one, a pair": {flags: flags("particle"),
			n: person("Джон Моффет", "Ван Сюаньцзе")},
		"a given led by one, swapped": {flags: flags("particle"), n: person("Надежда", "ван Иттерсум")},
		"a family ending in one":      {flags: flags("particle"), n: person("Мэр де", "Ла")},
		"lower case as well":          {flags: flags("particle", "lowercase"), n: person("ван гулик", "роберт")},
	} {
		t.Run(title, func(t *testing.T) {
			p := newCatalog(t).book(1, "Судья Ди").
				unselected(20, 1, "fp-review", "particle", c.flags, c.n).done().build()
			fallback(t, p, 20)
		})
	}
	// A particle in the middle field is the parse as it stands.
	p := newCatalog(t).book(1, "Утерянные победы").
		unselected(20, 1, "fp-review", "particle", flags("particle"), person("Манштейн", "Эрих", "фон")).
		done().build()
	if p.EntityOf(20) == uuid.Nil {
		t.Error("Эрих фон Манштейн: want an entity")
	}
}

func TestAJoinTakesNoRowItDoesNotOwn(t *testing.T) {
	// The administrator put the fingerprint elsewhere: the join leaves it.
	cat := newCatalog(t).book(1, "Рассвет").book(2, "Закат").book(3, "Полдень").
		credit(10, 1, "fp-selected", person("Смирнов", "Роман")).
		credit(11, 3, "fp-other", person("Петров", "Иван"))
	first := cat.build()
	state := authorentity.NewState()
	first.Apply(state)
	other := first.EntityOf(11)
	fp := authorentity.FPKey{Fingerprint: "fp-review", ExtractorVersion: "v1"}
	state.Fingerprints[fp] = authorentity.AssignmentRow{Key: fp, EntityID: other, Method: authorentity.MethodManual,
		Rule: "manual"}
	cat.in.Old = state
	cat.unselected(20, 2, "fp-review", "case_defect", flags("all_caps"), person("СМИРНОВ", "РОМАН"))
	if p := cat.build(); p.EntityOf(20) == first.EntityOf(10) || p.RuleOf(20) == authorentity.RuleSameName {
		t.Errorf("credit 20 joined %v by %q over the manual row", p.EntityOf(20), p.RuleOf(20))
	}

	// A fingerprint a selected credit holds keeps that credit's entity.
	p := newCatalog(t).book(1, "Рассвет").book(2, "Закат").
		credit(10, 1, "fp-selected", person("Смирнов", "Роман")).
		credit(11, 2, "fp-shared", person("Петров", "Иван")).
		unselected(20, 2, "fp-shared", "case_defect", flags("all_caps"), person("СМИРНОВ", "РОМАН")).done().build()
	state = authorentity.NewState()
	p.Apply(state)
	if row := state.Fingerprints[authorentity.FPKey{Fingerprint: "fp-shared", ExtractorVersion: "v1"}]; row.EntityID != p.EntityOf(11) {
		t.Errorf("the shared fingerprint went to %v, want Иван Петров's %v", row.EntityID, p.EntityOf(11))
	}
}

func TestInitialsInOneFieldJoinByTheirOrder(t *testing.T) {
	cat := newCatalog(t).book(1, "Рассвет").book(2, "Закат").
		credit(10, 1, "fp-selected", person("Каст", "Ф.", "К."))
	cat.unselected(20, 2, "fp-review", "initials", flags("initials"), &name{script: "Cyrl"}).
		fromSource("", "", "Ф. К. Каст")
	same(t, cat.build(), 10, 20)
}

func TestALabelledPlaceholderJoinsNoOne(t *testing.T) {
	// The parts name someone a selected credit names; the display is a
	// placeholder with a label.
	label := person("литература", "Европейская")
	label.displayAs = "Автор неизвестен - Европейская литература"
	p := newCatalog(t).book(1, "Рассвет").book(2, "Закат").
		credit(10, 1, "fp-selected", person("литература", "Европейская")).
		unselected(20, 2, "fp-review", "case_defect", flags("lowercase"), label).done().build()
	fallback(t, p, 20)
}

func TestOnlyTheParticleClassIsReadAsSelected(t *testing.T) {
	p := newCatalog(t).book(1, "Судья Ди").
		unselected(20, 1, "fp-review", "mixed_script", flags("hyphen"), person("ван Гулик", "Роберт")).done().build()
	fallback(t, p, 20)
}

func TestEveryCreditOfAJoinedFingerprintJoins(t *testing.T) {
	cat := newCatalog(t).book(1, "Рассвет").book(2, "Закат").book(3, "Полдень").
		credit(10, 1, "fp-selected", person("Смирнов", "Роман"))
	cat.unselected(20, 2, "fp-review", "case_defect", flags("all_caps"), person("СМИРНОВ", "РОМАН"))
	cat.unselected(21, 3, "fp-review", "case_defect", flags("all_caps"), person("СМИРНОВ", "РОМАН"))
	p := cat.build()
	same(t, p, 10, 20)
	same(t, p, 10, 21)
	if r := p.RuleOf(21); r != authorentity.RuleSameName {
		t.Errorf("rule %q, want %q", r, authorentity.RuleSameName)
	}
	if p.Report.Unselected.SameName != 2 {
		t.Errorf("report %+v, want two credits joined", p.Report.Unselected)
	}
}

// A particle name read as selected is no co-author on a merge card: the
// cards, and so the decisions on them, stay what they were (E4k).
func TestAParticleNameLeavesTheCardsAsTheyWere(t *testing.T) {
	base := func() *catalog {
		return newCatalog(t).book(1, "Война и мир").book(2, "Детство").
			credit(10, 1, "fp-short", person("Толстой", "Л.")).
			credit(11, 2, "fp-full", person("Толстой", "Лев", "Николаевич"))
	}
	before := candidateOf(sealed(t, base(), nil), authorentity.CandidateInitials, 10, 11)
	require.NotNil(t, before)
	with := base()
	with.unselected(20, 2, "fp-gulik", "particle", flags("particle"), person("ван Гулик", "Роберт"))
	p := sealed(t, with, nil)
	require.NotEqual(t, uuid.Nil, p.EntityOf(20), "the particle name is read as selected")
	after := candidateOf(p, authorentity.CandidateInitials, 10, 11)
	require.NotNil(t, after)
	assert.Equal(t, before.Digest, after.Digest)
}
