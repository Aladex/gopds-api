package authorentity_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wikidata as evidence (design E, phase E4w, round 7): two entities merge on
// an item only when each display name is a main label of that item alone,
// the item records no pseudonym, neither name has an initial, neither entity
// is a short name beside its full one or a tagged namesake, and two names of
// one script are the same key. Nothing else counts.

const extract = "wd-0123456789abcdef"

// reference is the Wikidata side of a test: items with their names. Keys
// are normalized as public.search_normalize does.
type reference struct {
	ref *authorentity.Reference
}

func newReference() *reference {
	return &reference{ref: &authorentity.Reference{Extract: extract, NameKey: map[string]string{},
		KeyItems: map[string][]authorentity.ItemName{}, Items: map[string]*authorentity.Item{}}}
}

var punct = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func key(s string) string {
	return strings.TrimSpace(punct.ReplaceAllString(strings.ReplaceAll(strings.ToLower(s), "ё", "е"), " "))
}

// item adds an item: its main labels — Russian, English, "mul" — the first
// the one shown, and its other names by kind ("alias:…", "pseudonym:…",
// "native:…", and "label:…" for a label in another language).
func (r *reference) item(qid string, labels []string, others ...string) *reference {
	it := &authorentity.Item{Label: labels[0]}
	for _, l := range labels {
		it.LabelKeys = append(it.LabelKeys, key(l))
		it.MainLabelKeys = append(it.MainLabelKeys, key(l))
		r.name(qid, authorentity.NameLabel, l)
	}
	for _, o := range others {
		kind, n, _ := strings.Cut(o, ":")
		switch kind {
		case authorentity.NameLabel:
			it.LabelKeys = append(it.LabelKeys, key(n))
		case authorentity.NamePseudonym:
			it.PseudonymKeys = append(it.PseudonymKeys, key(n))
		}
		r.name(qid, kind, n)
	}
	r.ref.Items[qid] = it
	return r
}

func (r *reference) name(qid, kind, n string) {
	k := key(n)
	r.ref.KeyItems[k] = append(r.ref.KeyItems[k], authorentity.ItemName{QID: qid, Kind: kind})
}

// with gives the catalog the reference, every display name keyed, as the
// loader does.
func (c *catalog) with(r *reference) *catalog {
	for i := range c.in.Credits {
		if n := c.in.Credits[i].DisplayName; n != "" {
			r.ref.NameKey[n] = key(n)
		}
	}
	c.in.Reference = r.ref
	return c
}

func shown(display string, n *name) *name { n.displayAs = display; return n }

func wikidataRule(qid string) string { return "wikidata:" + qid + "@" + extract }

// stored applies a plan to a store and makes it the next pass's.
func (c *catalog) stored(p *authorentity.Plan) *authorentity.State {
	c.t.Helper()
	if c.in.Old == nil {
		c.in.Old = authorentity.NewState()
	}
	p.Apply(c.in.Old)
	return c.in.Old
}

func wikidataRef(s *authorentity.State, id uuid.UUID) string {
	return s.References[authorentity.RefKey{EntityID: id, Source: authorentity.SourceWikidata}].Ref
}

// Кинг, Азимов, Гейман: the Russian and the English main label of an item
// with no pseudonym merge; a Bulgarian spelling that is an alias does not.
func TestMainLabelsOfOneItemMergeAcrossScripts(t *testing.T) {
	c := newCatalog(t).
		book(1, "Сияние").book(2, "Оно").book(3, "It").
		book(11, "Я, робот").book(12, "I, Robot").book(13, "Аз, роботът").
		book(21, "Американские боги").book(22, "American Gods").
		credit(1, 1, "king-ru", person("Кинг", "Стивен")).
		credit(2, 2, "king-ru", person("Кинг", "Стивен")).
		credit(3, 3, "king-en", shown("Stephen King", person("King", "Stephen").latin())).
		credit(11, 11, "asimov-ru", person("Азимов", "Айзек")).
		credit(12, 12, "asimov-en", shown("Isaac Asimov", person("Asimov", "Isaac").latin())).
		credit(13, 13, "asimov-bg", shown("Айзък Азимов", person("Азимов", "Айзък"))).
		credit(21, 21, "gaiman-ru", person("Гейман", "Нил")).
		credit(22, 22, "gaiman-en", shown("Neil Gaiman", person("Gaiman", "Neil").latin()))
	r := newReference().
		item("Q39829", []string{"Стивен Кинг", "Stephen King"}).
		item("Q34981", []string{"Айзек Азимов", "Isaac Asimov"}, "alias:Айзък Азимов").
		item("Q210059", []string{"Нил Гейман", "Neil Gaiman"})
	p := c.with(r).build()

	same(t, p, 1, 3)
	same(t, p, 11, 12)
	same(t, p, 21, 22)
	apart(t, p, 11, 13)
	apart(t, p, 1, 11)
	assert.Equal(t, wikidataRule("Q39829"), p.RuleOf(3), "the merged credits carry the evidence")
	assert.Equal(t, authorentity.RuleD1, p.RuleOf(1), "the anchor keeps its own rule")
	w := p.Report.Wikidata
	assert.Equal(t, extract, w.Extract)
	assert.Equal(t, 6, w.Named)
	assert.Equal(t, 3, w.Groups)
	assert.Equal(t, map[string]int{authorentity.WikidataCrossScript: 3}, w.Merges)
	for _, u := range p.Units {
		for _, fp := range u.Fingerprints {
			if strings.HasPrefix(fp.Rule, "wikidata:") {
				assert.Equal(t, authorentity.MethodEvidence, fp.Method)
			}
		}
	}
	ds := p.WikidataDecisions()
	require.Len(t, ds, 3)
	assert.Contains(t, ds, authorentity.WikidataDecision{Kind: authorentity.WikidataCrossScript, QID: "Q39829",
		Member: []int64{3}, Anchor: []int64{1, 2}})
}

// Rule 2: a key another item carries in any way — a label, an alias, a
// native name, a pseudonym — is no evidence.
func TestAKeyAnotherItemCarriesIsNoEvidence(t *testing.T) {
	for _, kind := range []string{"label", "alias", "native", "pseudonym"} {
		t.Run(kind, func(t *testing.T) {
			c := newCatalog(t).book(1, "Оно").book(2, "It").
				credit(1, 1, "ru", person("Кинг", "Стивен")).
				credit(2, 2, "en", shown("Stephen King", person("King", "Stephen").latin()))
			other := kind + ":Stephen King"
			if kind == "label" {
				other = "label:Stephen King"
			}
			r := newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"}).
				item("Q999", []string{"Somebody Else"}, other)
			p := c.with(r).build()
			apart(t, p, 1, 2)
			assert.Zero(t, p.Report.Wikidata.Groups)
		})
	}
}

// Rule 1: only a main label counts — an alias, a native name or a label in
// another language of the same item does not.
func TestOnlyAMainLabelNamesAnEntity(t *testing.T) {
	for _, kind := range []string{"label", "alias", "native"} {
		t.Run(kind, func(t *testing.T) {
			c := newCatalog(t).book(1, "Оно").book(2, "Воно").
				credit(1, 1, "ru", person("Кинг", "Стивен")).
				credit(2, 2, "uk", shown("Стівен Кінг", person("Кінг", "Стівен")))
			r := newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"}, kind+":Стівен Кінг")
			p := c.with(r).build()
			apart(t, p, 1, 2)
			assert.Equal(t, 1, p.Report.Wikidata.Named)
		})
	}
}

// Rule 3: an item recording any pseudonym merges nothing, and gives no
// entity its QID.
func TestAnItemWithPseudonymsMergesNothing(t *testing.T) {
	c := newCatalog(t).book(1, "Убийство в Восточном экспрессе").book(2, "Murder on the Orient Express").
		credit(1, 1, "ru", person("Кристи", "Агата")).
		credit(2, 2, "en", shown("Agatha Christie", person("Christie", "Agatha").latin()))
	r := newReference().item("Q35064", []string{"Агата Кристи", "Agatha Christie"}, "pseudonym:Mary Westmacott")
	p := c.with(r).build()
	s := c.stored(p)
	apart(t, p, 1, 2)
	assert.Equal(t, map[string]int{authorentity.WikidataExcludedPseudonym: 2}, p.Report.Wikidata.Excluded)
	assert.Empty(t, wikidataRef(s, p.EntityOf(1)))
	assert.Zero(t, p.Report.Wikidata.References)
}

// Rule 4: a name with an initial never merges, whatever the item says and
// whatever the punctuation — in the display name with the parts written out,
// or in the parts with the display name written out.
func TestANameWithAnInitialNeverMerges(t *testing.T) {
	full := func() *name { return person("Wodehouse", "Pelham", "Grenville").latin() }
	for _, tc := range []struct {
		display string
		n       *name
	}{
		{"P. G. Wodehouse", full()},
		{"P G Wodehouse", full()},
		{"P.G.Wodehouse", full()},
		{"J.R.R. Wodehouse", full()},
		{"(P) (G) Wodehouse", full()},
		{"P, G, Wodehouse", full()},
		{"P． G． Wodehouse", full()},
		{"J.-P. Wodehouse", full()},
		// Glued to the next word by any mark.
		{"P．G．Wodehouse", full()},
		{"(P)(G)Wodehouse", full()},
		{"P,G,Wodehouse", full()},
		{"P·G·Wodehouse", full()},
		// The display name written out, an initial in the parts.
		{"Pelham Wodehouse", person("Wodehouse", "P.").latin()},
		{"Pelham Grenville Wodehouse", person("Wodehouse", "Pelham", "G").latin()},
		{"Pelham Wodehouse", person("Wodehouse", "(P)").latin()},
	} {
		t.Run(tc.display+" / "+tc.n.given+" "+strings.Join(tc.n.additional, " "), func(t *testing.T) {
			c := newCatalog(t).book(1, "Дживс и Вустер").book(2, "Right Ho, Jeeves").
				credit(1, 1, "ru", person("Вудхауз", "Пелам", "Гренвилл")).
				credit(2, 2, "en", shown(tc.display, tc.n))
			r := newReference().item("Q207515", []string{"Пелам Гренвилл Вудхауз", tc.display})
			p := c.with(r).build()
			apart(t, p, 1, 2)
			assert.Equal(t, 1, p.Report.Wikidata.Excluded[authorentity.WikidataExcludedInitials])
		})
	}
}

// An apostrophe or a hyphen glues a name: O'Dell, O’Dell, D-Day are no
// initials, in the display name or in the parts.
func TestAnApostropheOrAHyphenIsNoInitial(t *testing.T) {
	for _, en := range []*name{
		shown("Scott O'Dell", person("O'Dell", "Scott").latin()),
		shown("Scott O’Dell", person("O’Dell", "Scott").latin()),
		shown("O'Dell Scott", person("Scott", "O'Dell").latin()),
		shown("Scott D-Dell", person("D-Dell", "Scott").latin()),
	} {
		t.Run(en.displayAs, func(t *testing.T) {
			c := newCatalog(t).book(1, "Остров голубых дельфинов").book(2, "Island of the Blue Dolphins").
				credit(1, 1, "ru", person("О'Делл", "Скотт")).
				credit(2, 2, "en", en)
			r := newReference().item("Q439528", []string{"Скотт О'Делл", en.displayAs})
			p := c.with(r).build()
			same(t, p, 1, 2)
		})
	}
}

// Rule 5: a short name beside a full name of the same family and given name
// in the catalog is a bucket of possibly several people: it never merges,
// and no book of it moves.
func TestAShortNameBesideAFullNameNeverMerges(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "Мизери").book(3, "It").book(4, "Сияние").
		credit(1, 1, "short", person("Кинг", "Стивен")).
		credit(2, 2, "short", person("Кинг", "Стивен")).
		credit(3, 3, "en", shown("Stephen King", person("King", "Stephen").latin())).
		credit(4, 4, "full", person("Кинг", "Стивен", "Эдвин"))
	r := newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"}, "alias:Стивен Эдвин Кинг")
	p := c.with(r).build()
	apart(t, p, 1, 3)
	apart(t, p, 1, 4)
	apart(t, p, 3, 4)
	w := p.Report.Wikidata
	assert.Equal(t, 1, w.Excluded[authorentity.WikidataExcludedShortName])
	assert.Empty(t, w.Merges)
	assert.Empty(t, p.WikidataDecisions())
}

// Any full name of the family and given name makes the short one a bucket:
// one an administrator put in an entity by hand, a tagged one, one with an
// initial or an apostrophe in it.
func TestAnyFullNameMakesTheShortNameABucket(t *testing.T) {
	for _, tc := range []struct {
		name string
		full *name
	}{
		{"manual", person("Смит", "Иван", "Ли")},
		{"tagged", person("Смит", "Иван", "Ли").withTag(2)},
		{"initial", person("Смит", "Иван", "Л.")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCatalog(t).book(1, "Один").book(2, "One").book(3, "Полный").
				credit(1, 1, "short", person("Смит", "Иван")).
				credit(2, 2, "en", shown("Ivan Smith", person("Smith", "Ivan").latin())).
				credit(3, 3, "full", tc.full)
			if tc.name == "manual" {
				id := uuid.New()
				c.in.Old = authorentity.NewState()
				c.in.Old.Entities[id] = authorentity.EntityRow{ID: id, Kind: "person", DisplayName: "Иван Ли Смит",
					SortName: "Иван Ли Смит", SearchKey: "иван ли смит"}
				k := authorentity.FPKey{Fingerprint: "full", ExtractorVersion: "v1"}
				c.in.Old.Fingerprints[k] = authorentity.AssignmentRow{Key: k, EntityID: id,
					Method: authorentity.MethodManual, Rule: "manual"}
			}
			p := c.with(newReference().item("Q1", []string{"Иван Смит", "Ivan Smith"})).build()
			apart(t, p, 1, 2)
			assert.Equal(t, 1, p.Report.Wikidata.Excluded[authorentity.WikidataExcludedShortName])
		})
	}
	// An apostrophe in the given name makes no initial of a full name either
	// (the parts the other way round, as the extractor may read them).
	c := newCatalog(t).book(1, "Один").book(2, "Два").book(3, "Полный").
		credit(1, 1, "a", shown("Иван О'Делл", person("Иван", "О'Делл"))).
		credit(2, 2, "b", shown("Иван О'Делл", person("О'Делл", "Иван"))).
		credit(3, 3, "full", person("Иван", "О'Делл", "Ли"))
	p := c.with(newReference().item("Q1", []string{"Иван О'Делл"})).build()
	apart(t, p, 1, 2)
}

// A namesake tagged "(N)" in its source is another person by the source's
// own word: it never merges on Wikidata, not even with the same name.
func TestATaggedNamesakeNeverMerges(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "Сборник").book(3, "It").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 2, "ru-2", person("Кинг", "Стивен").withTag(2)).
		credit(3, 3, "en", shown("Stephen King", person("King", "Stephen").latin()))
	r := newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})
	p := c.with(r).build()
	same(t, p, 1, 3)
	apart(t, p, 1, 2)
	assert.Equal(t, 1, p.Report.Wikidata.Excluded[authorentity.WikidataExcludedNamesake])
}

// Two main labels of one script (a Russian and a Ukrainian-looking "mul"
// label, Leo and Lev in English and "mul"): the entities of one key merge,
// the keys do not, and no third name joins them through another script.
func TestTwoKeysInOneScriptMergeOnlyTheirOwnKey(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "Мизери").book(3, "Воно").book(4, "It").book(5, "Сияние").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 2, "ru-b", shown("Стивен Кинг", person("Кинг", "Стивен").withSuffix("x"))).
		credit(3, 3, "uk", shown("Стівен Кінг", person("Кінг", "Стівен"))).
		credit(4, 4, "en", shown("Stephen King", person("King", "Stephen").latin())).
		credit(5, 5, "ru", person("Кинг", "Стивен"))
	r := newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King", "Стівен Кінг"})
	p := c.with(r).build()
	same(t, p, 1, 2)
	apart(t, p, 1, 3)
	apart(t, p, 1, 4)
	apart(t, p, 3, 4)
	w := p.Report.Wikidata
	assert.Equal(t, 1, w.ScriptConflicts)
	assert.Equal(t, map[string]int{authorentity.WikidataSameName: 1}, w.Merges)
}

// Only display names count: a nickname that is a main label, an alias
// display name, carry nothing.
func TestOnlyDisplayNamesCount(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "It").book(3, "Мизери").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 2, "nick", shown("Steve K", person("K", "Steve").latin().withNickname("Stephen King"))).
		credit(3, 3, "other", shown("Stefan King", person("King", "Stefan").latin().withNickname("Стивен Кинг")))
	r := newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"}, "alias:Stefan King")
	p := c.with(r).build()
	apart(t, p, 1, 2)
	apart(t, p, 1, 3)
	assert.Equal(t, 1, p.Report.Wikidata.Named)
}

// A collective is never an item: the items are people. Nor is a collective
// an administrator put in an entity by hand.
func TestACollectiveIsNotMatched(t *testing.T) {
	c := newCatalog(t).book(1, "Пикник на обочине").book(2, "Трудно быть богом").book(3, "Улитка на склоне").
		credit(1, 1, "person", person("Стругацкий", "Аркадий")).
		collective(2, 2, "brothers", "Аркадий Стругацкий").
		collective(3, 3, "by-hand", "Аркадий Стругацкий")
	byHand := uuid.New()
	c.in.Old = authorentity.NewState()
	c.in.Old.Entities[byHand] = authorentity.EntityRow{ID: byHand, Kind: authorentity.KindCollective,
		DisplayName: "Аркадий Стругацкий", SortName: "Аркадий Стругацкий", SearchKey: "аркадий стругацкий"}
	k := authorentity.FPKey{Fingerprint: "by-hand", ExtractorVersion: "v1"}
	c.in.Old.Fingerprints[k] = authorentity.AssignmentRow{Key: k, EntityID: byHand, Method: authorentity.MethodManual,
		Rule: "manual"}
	r := newReference().item("Q312606", []string{"Аркадий Стругацкий"})
	p := c.with(r).build()
	s := c.stored(p)
	apart(t, p, 1, 2)
	require.Equal(t, byHand, p.EntityOf(3))
	assert.Equal(t, 1, p.Report.Wikidata.Named)
	assert.Equal(t, "Q312606", wikidataRef(s, p.EntityOf(1)))
	assert.Empty(t, wikidataRef(s, p.EntityOf(2)))
	assert.Empty(t, wikidataRef(s, byHand))
}

// Two names credited on one book are two people, whatever an item says: the
// whole set stays apart.
func TestNamesOnOneBookAreNotMerged(t *testing.T) {
	c := newCatalog(t).book(1, "Сборник").book(2, "Ещё").book(3, "Оно").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 1, "en", shown("Stephen King", person("King", "Stephen").latin())).
		credit(3, 2, "en", shown("Stephen King", person("King", "Stephen").latin())).
		credit(4, 3, "mul", shown("Stephen  King", person("King", "Stephen").latin().withSuffix("x")))
	r := newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})
	p := c.with(r).build()
	apart(t, p, 1, 2)
	apart(t, p, 2, 4)
	assert.Equal(t, 1, p.Report.Wikidata.SharedBook)
}

// The evidence is deterministic: a second pass over what the first stored
// writes nothing. A manual decision wins over it.
func TestWikidataPassesAreStableAndManualDecisionsWin(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "It").book(3, "Мизери").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 2, "en", shown("Stephen King", person("King", "Stephen").latin())).
		credit(3, 3, "en", shown("Stephen King", person("King", "Stephen").latin()))
	r := newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})
	c.with(r)
	state := authorentity.NewState()
	first := authorentity.Build(&c.in)
	require.Equal(t, first.EntityOf(1), first.EntityOf(2))
	first.Apply(state)
	c.in.Old = state
	second := authorentity.Build(&c.in)
	assert.Zero(t, second.Changes(), "a second pass writes nothing")

	// An administrator moves the Latin name to an entity of its own.
	elsewhere := uuid.New()
	state.Entities[elsewhere] = authorentity.EntityRow{ID: elsewhere, Kind: "person", DisplayName: "Stephen King",
		SortName: "Stephen King", SearchKey: "stephen king"}
	state.Fingerprints[authorentity.FPKey{Fingerprint: "en", ExtractorVersion: "v1"}] = authorentity.AssignmentRow{
		Key: authorentity.FPKey{Fingerprint: "en", ExtractorVersion: "v1"}, EntityID: elsewhere,
		Method: authorentity.MethodManual, Rule: "manual"}
	third := authorentity.Build(&c.in)
	assert.Equal(t, elsewhere, third.EntityOf(2))
	assert.NotEqual(t, third.EntityOf(1), third.EntityOf(2))
}

// Two stored entities a reference merges: the one with more books keeps its
// ID (§3), the other is merged into it, and the merge is logged as evidence
// with the item and the extract.
func TestAWikidataMergeOfStoredEntitiesIsLoggedAsEvidence(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "Мизери").book(3, "It").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 2, "ru", person("Кинг", "Стивен")).
		credit(3, 3, "en", shown("Stephen King", person("King", "Stephen").latin()))
	state := authorentity.NewState()
	before := c.build()
	before.Apply(state)
	ru, en := before.EntityOf(1), before.EntityOf(3)
	require.NotEqual(t, ru, en)

	c.in.Old = state
	p := c.with(newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})).build()
	assert.Equal(t, ru, p.EntityOf(3), "the entity of more books survives")
	var merges []authorentity.MergeRow
	for _, u := range p.Units {
		merges = append(merges, u.Absorb...)
	}
	require.Len(t, merges, 1)
	assert.Equal(t, authorentity.MergeRow{From: en, To: ru, Method: authorentity.MethodEvidence,
		Rule: wikidataRule("Q39829"), SharedBooks: 1}, merges[0])
}

// The entity's Wikidata item (author_entity_reference): the item whose main
// label its display name is, carried by no other item, with no pseudonym
// recorded — merged or not, initials included, a short name beside its full
// one never; the survivor
// of a merge keeps it and the absorbed entity's goes; each piece of a split
// takes its own; an administrator's pinned item stays.

func TestAnEntityTakesTheItemOfItsMainLabel(t *testing.T) {
	c := newCatalog(t).book(1, "Американские боги").book(2, "Коралина").book(3, "Карфаген").
		book(4, "Right Ho, Jeeves").book(5, "Мизери").book(6, "Оно").book(7, "Neverwhere").
		credit(1, 1, "gaiman", person("Гейман", "Нил")).
		credit(2, 2, "gaiman", person("Гейман", "Нил")).
		credit(3, 3, "nobody", person("Волков", "Александр", "Викторович")).
		credit(4, 4, "pgw", shown("P. G. Wodehouse", person("Wodehouse", "P.", "G.").latin())).
		credit(5, 5, "king", person("Кинг", "Стивен")).
		credit(6, 6, "king-full", person("Кинг", "Стивен", "Эдвин")).
		credit(7, 7, "gaiman-alias", shown("Neil Richard Gaiman", person("Gaiman", "Neil", "Richard").latin()))
	r := newReference().item("Q210059", []string{"Нил Гейман", "Neil Gaiman"}, "alias:Neil Richard Gaiman").
		item("Q207515", []string{"P. G. Wodehouse"}).
		item("Q39829", []string{"Стивен Кинг"})
	p := c.with(r).build()
	s := c.stored(p)
	gaiman := p.EntityOf(1)
	assert.Equal(t, "Q210059", wikidataRef(s, gaiman), "an entity that merged nothing has its item too")
	assert.Equal(t, extract, s.References[authorentity.RefKey{EntityID: gaiman, Source: authorentity.SourceWikidata}].Extract)
	assert.Empty(t, wikidataRef(s, p.EntityOf(3)), "no item")
	assert.Equal(t, "Q207515", wikidataRef(s, p.EntityOf(4)), "initials take the item, merge nothing")
	assert.Empty(t, wikidataRef(s, p.EntityOf(5)), "a short name beside its full one takes no item")
	assert.Empty(t, wikidataRef(s, p.EntityOf(7)), "an alias is no main label")
	assert.Equal(t, 2, p.Report.Wikidata.References)
	assert.Equal(t, 1, p.Report.Wikidata.ReferencesWithheld)
	assert.Zero(t, c.build().Changes(), "a second pass writes nothing")
}

// The prod-copy shape of Q1967697: «Алексей Иванов» is its main label and
// no other item's, but beside «Алексей Викторович Иванов» in the catalog it
// is a bucket of possibly several people — it takes no item, whatever full
// name it is beside (automatic or by hand). A short name with no full one
// takes its item.
func TestAShortNameBesideAFullNameTakesNoItem(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("full by hand %v", manual), func(t *testing.T) {
			c := newCatalog(t).book(1, "Психология").book(2, "Сердце пармы").book(3, "Американские боги").
				credit(1, 1, "ivanov", person("Иванов", "Алексей")).
				credit(2, 2, "ivanov-full", person("Иванов", "Алексей", "Викторович")).
				credit(3, 3, "gaiman", person("Гейман", "Нил"))
			if manual {
				id := uuid.New()
				c.in.Old = authorentity.NewState()
				c.in.Old.Entities[id] = authorentity.EntityRow{ID: id, Kind: "person",
					DisplayName: "Алексей Викторович Иванов", SortName: "Алексей Викторович Иванов",
					SearchKey: "алексей викторович иванов"}
				k := authorentity.FPKey{Fingerprint: "ivanov-full", ExtractorVersion: "v1"}
				c.in.Old.Fingerprints[k] = authorentity.AssignmentRow{Key: k, EntityID: id,
					Method: authorentity.MethodManual, Rule: "manual"}
			}
			r := newReference().item("Q1967697", []string{"Алексей Викторович Иванов", "Алексей Иванов", "Alexei Ivanov"}).
				item("Q210059", []string{"Нил Гейман", "Neil Gaiman"})
			p := c.with(r).build()
			s := c.stored(p)
			apart(t, p, 1, 2)
			assert.Empty(t, wikidataRef(s, p.EntityOf(1)), "the bucket takes no item")
			assert.Equal(t, "Q1967697", wikidataRef(s, p.EntityOf(2)))
			assert.Equal(t, "Q210059", wikidataRef(s, p.EntityOf(3)), "no full name beside it")
			assert.Equal(t, 1, p.Report.Wikidata.ReferencesWithheld)
			assert.Zero(t, c.build().Changes(), "a second pass writes nothing")
		})
	}
}

func TestTheSurvivorOfAMergeKeepsTheItemAndTheAbsorbedLosesIt(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "Мизери").book(3, "It").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 2, "ru", person("Кинг", "Стивен")).
		credit(3, 3, "en", shown("Stephen King", person("King", "Stephen").latin()))
	r := newReference().item("Q39829", []string{"Стивен Кинг"})
	p := c.with(r).build()
	s := c.stored(p)
	ru, en := p.EntityOf(1), p.EntityOf(3)
	require.NotEqual(t, ru, en)
	assert.Equal(t, "Q39829", wikidataRef(s, ru))
	s.References[authorentity.RefKey{EntityID: en, Source: authorentity.SourceWikidata}] = authorentity.ReferenceRow{
		Key: authorentity.RefKey{EntityID: en, Source: authorentity.SourceWikidata}, Ref: "Q39829", Extract: extract}

	c.in.Reference = nil
	p = c.with(newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})).build()
	s = c.stored(p)
	assert.Equal(t, ru, p.EntityOf(3), "merged into the entity of more books")
	assert.Equal(t, "Q39829", wikidataRef(s, ru))
	assert.Empty(t, wikidataRef(s, en), "the absorbed entity's item goes")
	assert.Zero(t, c.build().Changes())
}

func TestEachPieceOfASplitTakesItsOwnItem(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "Мизери").book(3, "It").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 2, "ru", person("Кинг", "Стивен")).
		credit(3, 3, "en", shown("Stephen King", person("King", "Stephen").latin()))
	p := c.with(newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})).build()
	s := c.stored(p)
	one := p.EntityOf(1)
	require.Equal(t, one, p.EntityOf(3))
	require.Equal(t, "Q39829", wikidataRef(s, one))

	// A new extract gives the Latin name to another writer: the evidence is
	// gone, the merge splits, each piece has its own item.
	r := newReference().item("Q39829", []string{"Стивен Кинг"}).item("Q777", []string{"Stephen King"})
	p = c.with(r).build()
	s = c.stored(p)
	assert.Equal(t, one, p.EntityOf(1), "the larger piece keeps the ID")
	other := p.EntityOf(3)
	require.NotEqual(t, one, other)
	assert.Equal(t, "Q39829", wikidataRef(s, one))
	assert.Equal(t, "Q777", wikidataRef(s, other))
	assert.Zero(t, c.build().Changes())
}

func TestANameOfSeveralItemsOrOfAPenNameHasNoItem(t *testing.T) {
	c := newCatalog(t).book(1, "Детство").book(2, "Бегущий человек").book(3, "Оно").
		credit(1, 1, "tolstoy", person("Толстой", "Лев")).
		credit(2, 2, "bachman", person("Бахман", "Ричард")).
		credit(3, 3, "king", person("Кинг", "Стивен"))
	r := newReference().item("Q7243", []string{"Лев Толстой"})
	p := c.with(r).build()
	s := c.stored(p)
	tolstoy := p.EntityOf(1)
	require.Equal(t, "Q7243", wikidataRef(s, tolstoy))

	// The next extract has a second writer of the name: the item goes; a pen
	// name is no main label, and its item records a pseudonym.
	r = newReference().item("Q7243", []string{"Лев Толстой"}).item("Q1000", []string{"Лев Львович Толстой"}, "alias:Лев Толстой").
		item("Q39829", []string{"Стивен Кинг"}, "pseudonym:Ричард Бахман", "alias:Ричард Бахман")
	c.in.Reference = nil
	p = c.with(r).build()
	s = c.stored(p)
	assert.Empty(t, wikidataRef(s, tolstoy), "a name of two writers is no item")
	assert.Empty(t, wikidataRef(s, p.EntityOf(2)), "a pen name is not the item")
	assert.Empty(t, wikidataRef(s, p.EntityOf(3)), "an item with a pseudonym is out of this phase")
}

func TestAPinnedItemIsTheAdministrators(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").credit(1, 1, "king", person("Кинг", "Стивен"))
	p := c.with(newReference().item("Q39829", []string{"Стивен Кинг"})).build()
	s := c.stored(p)
	king := p.EntityOf(1)
	key := authorentity.RefKey{EntityID: king, Source: authorentity.SourceWikidata}
	s.References[key] = authorentity.ReferenceRow{Key: key, Ref: "Q1", Pinned: true}

	p = c.build()
	assert.Zero(t, p.Changes(), "the pass does not touch a pinned item")
	assert.Equal(t, "Q1", p.WikidataOf(king))
	c.in.Reference = nil
	assert.Zero(t, c.build().Changes(), "nor does a pass without an extract")
}

// Review r1, finding 7: a pinned survivor keeps its item, and the absorbed
// entity's unpinned item still goes.
func TestAPinnedSurvivorStillDropsTheAbsorbedItem(t *testing.T) {
	c := newCatalog(t).book(1, "Оно").book(2, "Мизери").book(3, "It").
		credit(1, 1, "ru", person("Кинг", "Стивен")).
		credit(2, 2, "ru", person("Кинг", "Стивен")).
		credit(3, 3, "en", shown("Stephen King", person("King", "Stephen").latin()))
	p := c.with(newReference().item("Q39829", []string{"Стивен Кинг"}, "alias:Stephen King")).build()
	s := c.stored(p)
	ru, en := p.EntityOf(1), p.EntityOf(3)
	require.NotEqual(t, ru, en)
	keyRU := authorentity.RefKey{EntityID: ru, Source: authorentity.SourceWikidata}
	keyEN := authorentity.RefKey{EntityID: en, Source: authorentity.SourceWikidata}
	s.References[keyRU] = authorentity.ReferenceRow{Key: keyRU, Ref: "Q39829", Pinned: true}
	s.References[keyEN] = authorentity.ReferenceRow{Key: keyEN, Ref: "Q39829", Extract: extract}

	p = c.with(newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})).build()
	s = c.stored(p)
	require.Equal(t, ru, p.EntityOf(3))
	assert.True(t, s.References[keyRU].Pinned)
	assert.Empty(t, wikidataRef(s, en), "the absorbed entity's item goes")
	assert.Zero(t, c.build().Changes())
}

// Every wrong merge the reviews of rounds 1–6 reproduced, and the shapes the
// round-6 property test found: all stay apart.
func TestTheReviewedCounterexamplesStayApart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) (*catalog, *reference)
		pairs [][2]int64
	}{
		{"r1-1 Алферова / Роман Буревой, Czech label, series", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Одна", 7).book(2, "Другая", 7).
				credit(1, 1, "alferova", shown("Марианна Алферова", person("Алферова", "Марианна"))).
				credit(2, 2, "burevoi", person("Буревой", "Роман"))
			return c, newReference().item("Q124752133", []string{"Марианна Алферова"}, "label:Roman Burevoj",
				"pseudonym:Роман Буревой")
		}, [][2]int64{{1, 2}}},
		{"r1-1 the same without P742", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Одна", 7).book(2, "Другая", 7).
				credit(1, 1, "alferova", shown("Марианна Алферова", person("Алферова", "Марианна"))).
				credit(2, 2, "burevoi", person("Буревой", "Роман"))
			return c, newReference().item("Q124752133", []string{"Марианна Алферова"}, "label:Roman Burevoj")
		}, [][2]int64{{1, 2}}},
		{"r1-2 Анна Керн / Анна Петровна Керн / Anna Kern", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Дневник").book(2, "Письма").book(3, "Воспоминания").
				book(4, "Diary").book(5, "Letters").book(6, "Memoirs").
				credit(1, 1, "short", person("Керн", "Анна")).
				credit(2, 2, "full", person("Керн", "Анна", "Петровна")).
				credit(3, 3, "full", person("Керн", "Анна", "Петровна")).
				credit(4, 4, "en", shown("Anna Kern", person("Kern", "Anna").latin())).
				credit(5, 5, "en", shown("Anna Kern", person("Kern", "Anna").latin())).
				credit(6, 6, "en", shown("Anna Kern", person("Kern", "Anna").latin()))
			return c, newReference().item("Q193000", []string{"Анна Керн", "Анна Петровна Керн", "Anna Kern"})
		}, [][2]int64{{1, 2}, {1, 4}}},
		{"r1-3 Джон Смит of two items, a unique nickname", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "One").book(2, "Two").
				credit(1, 1, "smith", shown("Джон Смит", person("Смит", "Джон").withNickname("John Adam Smith"))).
				credit(2, 2, "adam", shown("John Adam Smith", person("Smith", "John", "Adam").latin()))
			return c, newReference().item("Q10", []string{"John Adam Smith", "Джон Смит"}).
				item("Q20", []string{"Джон Смит"})
		}, [][2]int64{{1, 2}}},
		{"r1-4 / r2-3 initials: S. J. Day, S. Day, two Л. Н. Толстой", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Bared to You").book(2, "Anthology").book(3, "Other").
				book(11, "Переписка").book(12, "Дневники").
				credit(1, 1, "sj", shown("S. J. Day", person("Day", "S.", "J.").latin())).
				credit(2, 2, "sj", shown("S. J. Day", person("Day", "S.", "J.").latin())).
				credit(3, 3, "s", shown("S. Day", person("Day", "S.").latin())).
				credit(11, 11, "ln", shown("Л. Н. Толстой", person("Толстой", "Л.", "Н."))).
				credit(12, 12, "ln-2", shown("Л. Н. Толстой", person("Толстой", "Л.", "Н.").withTag(2)))
			return c, newReference().item("Q258660", []string{"Sylvia Day", "S. J. Day", "S. Day"}).
				item("Q7243", []string{"Лев Николаевич Толстой", "Л. Н. Толстой"})
		}, [][2]int64{{1, 3}, {11, 12}}},
		{"r2-1 Агата Кристи / Agatha Mallowan, P742", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Убийство", 3).book(2, "Murder", 3).
				credit(1, 1, "christie", person("Кристи", "Агата")).
				credit(2, 2, "mallowan", shown("Agatha Mallowan", person("Mallowan", "Agatha").latin()))
			return c, newReference().item("Q35064", []string{"Агата Кристи", "Agatha Christie", "Agatha Mallowan"},
				"pseudonym:Agatha Mallowan")
		}, [][2]int64{{1, 2}}},
		{"r2-1 Иван Иванов / Иван Петров, one word, series", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Один", 4).book(2, "Два", 4).
				credit(1, 1, "birth", person("Иванов", "Иван")).
				credit(2, 2, "pen", person("Петров", "Иван"))
			return c, newReference().item("Q5002", []string{"Иван Иванов", "Иван Петров"}, "pseudonym:Иван Петров")
		}, [][2]int64{{1, 2}}},
		{"r2-2 an anchor's work does not vouch for ambiguous John Smith", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Anthology").book(11, "The Wealth").book(12, "Lectures").
				credit(1, 1, "smith", shown("John Smith", person("Smith", "John").latin().withNickname("Jonathan Smith"))).
				credit(11, 11, "smythe", shown("John Smythe", person("Smythe", "John").latin())).
				credit(12, 12, "smythe", shown("John Smythe", person("Smythe", "John").latin()))
			return c, newReference().item("Q10", []string{"Jonathan Smith", "John Smythe"}, "alias:John Smith").
				item("Q20", []string{"Bob Doe"}, "alias:John Smith")
		}, [][2]int64{{1, 11}}},
		{"r4/r5 Ann / Anna / Annalise Smith, series", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "One", 9).book(2, "Two", 9).book(3, "Three", 9).
				credit(1, 1, "ann", shown("Ann Smith", person("Smith", "Ann").latin())).
				credit(2, 2, "anna", shown("Anna Smith", person("Smith", "Anna").latin())).
				credit(3, 3, "annalise", shown("Annalise Smith", person("Smith", "Annalise").latin()))
			return c, newReference().item("Q9003", []string{"Ann Smith", "Anna Smith", "Annalise Smith"},
				"pseudonym:Ann Smith", "pseudonym:Annalise Smith")
		}, [][2]int64{{1, 2}, {2, 3}, {1, 3}}},
		{"r6-1 Mary Jones / Beth Smith through an ambiguous label", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "One", 900).book(2, "Two").book(3, "Three", 900).
				credit(1, 1, "mary", shown("Mary Jones", person("Jones", "Mary").latin())).
				credit(2, 2, "mary", shown("Mary Jones", person("Jones", "Mary").latin())).
				credit(3, 3, "beth", shown("Beth Smith", person("Smith", "Beth").latin()))
			return c, newReference().item("Q1", []string{"Mary Jones", "Anna Smith"}, "pseudonym:Ann Smith",
				"pseudonym:Annalise Smith", "label:Beth Smith")
		}, [][2]int64{{1, 3}}},
		{"r6-2 Alan Smith with the nickname A. Smith / Альберт Смит", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Один").book(2, "Два").book(3, "Other").
				credit(1, 1, "albert", person("Смит", "Альберт")).
				credit(2, 2, "albert", person("Смит", "Альберт")).
				credit(3, 3, "alan", shown("Alan Smith", person("Smith", "Alan").latin().withNickname("A. Smith")))
			return c, newReference().item("Q1", []string{"Альберт Смит", "A. Smith"}, "pseudonym:A. Smith")
		}, [][2]int64{{1, 3}}},
		{"r6-2 the same without P742", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Один").book(2, "Два").book(3, "Other").
				credit(1, 1, "albert", person("Смит", "Альберт")).
				credit(2, 2, "albert", person("Смит", "Альберт")).
				credit(3, 3, "alan", shown("Alan Smith", person("Smith", "Alan").latin().withNickname("A. Smith")))
			return c, newReference().item("Q1", []string{"Альберт Смит", "A. Smith"})
		}, [][2]int64{{1, 3}}},
		{"r6 property: Ann Smyth beside the pen name Kate Smyth", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "One", 16).book(2, "Two", 16).
				credit(1, 1, "kate", shown("Kate Smyth", person("Smyth", "Kate").latin())).
				credit(2, 2, "ann", shown("Ann Smyth", person("Smyth", "Ann").latin()))
			return c, newReference().item("Q9005", []string{"Мария Петров", "Kate Smyth"}, "pseudonym:Kate Smyth",
				"alias:Ann Smyth")
		}, [][2]int64{{1, 2}}},
		{"r6 property: A. Smyth beside the pen name Мария Смит", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "One", 22).book(2, "Три", 22).
				credit(1, 1, "a", shown("A. Smyth", person("Smyth", "A.").latin())).
				credit(2, 2, "pen", person("Смит", "Мария"))
			return c, newReference().item("Q9012", []string{"Mary Smyth", "Мария Смит", "A. Smyth"},
				"pseudonym:Мария Смит")
		}, [][2]int64{{1, 2}}},
		{"spelling: Элен Уайт is another writer's alias", func(t *testing.T) (*catalog, *reference) {
			c := newCatalog(t).book(1, "Путь ко Христу").book(2, "Steps to Christ").
				credit(1, 1, "ru", person("Уайт", "Элен")).
				credit(2, 2, "en", shown("Ellen White", person("White", "Ellen").latin()))
			return c, newReference().item("Q233967", []string{"Эллен Уайт", "Ellen G. White"}, "alias:Ellen White").
				item("Q1", []string{"Элен Уайт"}, "alias:Ellen White")
		}, [][2]int64{{1, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r := tc.build(t)
			p := c.with(r).build()
			for _, pair := range tc.pairs {
				apart(t, p, pair[0], pair[1])
			}
		})
	}
}
