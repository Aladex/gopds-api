package authorentity_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E4g: an initials side of more than ten books («Толстой Л.Н.» 25 against
// «Лев Николаевич Толстой» 717) is no longer dropped. The pair is a
// candidate of a kind of its own, initials_hub, asked and gated apart from
// initials, its card a bounded sample like a full side's; every other
// exclusion and the given-name guard hold as before.

// hubs is a catalog of full names, one credit each on books 1, 101, 201, …,
// and a short form on books of its own after them: n books, credits from
// 1001 on, titles of their own, so no typo merge links any two.
func hubs(t *testing.T, short *name, n int, fulls ...*name) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i, f := range fulls {
		id := int64(1 + 100*i)
		ch.c.book(id, fmt.Sprintf("Повесть %d", id)).credit(id, id, fmt.Sprintf("fp-full-%d", i), f)
	}
	for i := range n {
		id := int64(1001 + i)
		ch.c.book(id, fmt.Sprintf("Рассказ %d", id)).credit(id, id, "fp-short", short)
	}
	return ch
}

// nosovs: «Н. Носов» on n books against «Николай Николаевич Носов» (1).
func nosovs(t *testing.T, n int) *churn {
	return hubs(t, person("Носов", "Н."), n, person("Носов", "Николай", "Николаевич"))
}

// Eleven books: an initials_hub candidate, not an initials one, nor an
// exclusion; ten books: an initials candidate, as before.
func TestAnInitialsSideOfMoreThanTenBooksIsAHub(t *testing.T) {
	p := sealed(t, nosovs(t, 11).c, nil)
	requireCandidate(t, p, authorentity.CandidateInitialsHub, "Cyrl", 1001, 1)
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitials, 1001, 1))
	assert.Nil(t, sampleOf(p, authorentity.CandidateInitials, 1001, 1))
	assert.Equal(t, 1, counted(p, authorentity.CandidateInitialsHub, "Cyrl"))
	assert.Zero(t, counted(p, authorentity.CandidateInitials, "Cyrl"))
	assert.Zero(t, p.Report.Candidates.Excluded)

	p = sealed(t, nosovs(t, 10).c, nil)
	requireCandidate(t, p, authorentity.CandidateInitials, "Cyrl", 1001, 1)
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitialsHub, 1001, 1))
	assert.Zero(t, counted(p, authorentity.CandidateInitialsHub, "Cyrl"))
}

// A Latin hub is one too, in its script.
func TestALatinInitialsSideOfMoreThanTenBooksIsAHub(t *testing.T) {
	p := sealed(t, hubs(t, person("Dick", "Philip", "K.").latin(), 18, person("Dick", "Philip", "Kindred").latin()).c, nil)
	requireCandidate(t, p, authorentity.CandidateInitialsHub, "Latn", 1001, 1)
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitials, 1001, 1))
}

// The hub's card lists every one of its books (round 2): the decision moves
// them all, so the models and the audit see them all; its size class as
// any card's. The full side's card keeps the oldest ten, as an initials
// pair shows it.
func TestTheHubCardListsEveryBook(t *testing.T) {
	ch := nosovs(t, 25)
	for i := range int64(14) {
		ch.c.book(2+i, fmt.Sprintf("Повесть %d", 2+i)).credit(2+i, 2+i, "fp-full-0",
			person("Носов", "Николай", "Николаевич"))
	}
	p := sealed(t, ch.c, nil)
	c := requireCandidate(t, p, authorentity.CandidateInitialsHub, "Cyrl", 1001, 1)
	hub, full := hubCards(t, p, c)
	require.Len(t, hub.Books, 25)
	for i, b := range hub.Books {
		assert.Equal(t, fmt.Sprintf("Рассказ %d", 1001+i), b.Title)
	}
	assert.Equal(t, authorentity.MergeSizeClass(25), hub.Size)
	assert.Equal(t, []string{"Н. Носов"}, hub.Names)
	require.Len(t, full.Books, 10)
	assert.Equal(t, authorentity.MergeSizeClass(15), full.Size)
	for i, b := range full.Books {
		assert.Equal(t, fmt.Sprintf("Повесть %d", 1+i), b.Title)
	}
}

// Evidence of another person after the tenth book — an annotation naming
// the other given name, a co-author, a genre of its own on book eleven —
// is on the card, with the book's loaded fields like the first ten.
func TestTheHubCardShowsEvidenceAfterTheTenthBook(t *testing.T) {
	ch := hubs(t, person("Иванов", "С.", "В."), 12, person("Иванов", "Семён", "Викторович"))
	ch.c.credit(3001, 1011, "fp-coauthor", person("Петров", "Олег"))
	fields := map[int64]authorentity.CardBookFields{}
	for id := int64(1001); id <= 1012; id++ {
		fields[id] = authorentity.CardBookFields{Year: "2001", Lang: "ru", Genres: []string{"sf"}}
	}
	fields[1011] = authorentity.CardBookFields{Year: "1975", Lang: "ru", Genres: []string{"sci_history"},
		Annotation: "Сергей Владимирович Иванов — историк флота"}
	p := sealed(t, ch.c, fields)
	c := requireCandidate(t, p, authorentity.CandidateInitialsHub, "Cyrl", 1001, 1)
	hub, _ := hubCards(t, p, c)
	require.Len(t, hub.Books, 12)
	eleventh := hub.Books[10]
	assert.Equal(t, "Рассказ 1011", eleventh.Title)
	assert.Equal(t, "Сергей Владимирович Иванов — историк флота", eleventh.Annotation)
	assert.Equal(t, []string{"sci_history"}, eleventh.Genres)
	assert.Equal(t, "1975", eleventh.Year)
	assert.Equal(t, []string{"Олег Петров"}, eleventh.Coauthors)
}

// hubCards are a hub candidate's cards: the initials side's, the full
// side's.
func hubCards(t *testing.T, p *authorentity.Plan, c *authorentity.MergeCandidate) (hub, full authorentity.MergeCard) {
	t.Helper()
	var in authorentity.MergeInput
	require.NoError(t, json.Unmarshal(inputOf(p, c), &in))
	if c.SideA == p.EntityOf(1) {
		return in.B, in.A
	}
	return in.A, in.B
}

// The hub's side in an ordinary pair keeps its sample: a swap of «Н.
// Носов» on eleven books is carded with ten, byte for byte as before, while
// its hub pair lists all eleven.
func TestAHubSideKeepsItsSampleInAnotherKind(t *testing.T) {
	ch := nosovs(t, 11)
	ch.c.book(2001, "Сборник").credit(2001, 2001, "fp-swap", person("Н.", "Носов"))
	p := sealed(t, ch.c, nil)
	hub := requireCandidate(t, p, authorentity.CandidateInitialsHub, "Cyrl", 1001, 1)
	h, _ := hubCards(t, p, hub)
	assert.Len(t, h.Books, 11)
	swap := candidateOf(p, authorentity.CandidateSwap, 1001, 2001)
	require.NotNil(t, swap, "a swap of the hub side: %+v", p.MergeCandidateSample())
	var in authorentity.MergeInput
	require.NoError(t, json.Unmarshal(inputOf(p, swap), &in))
	short := in.A
	if swap.SideA == p.EntityOf(2001) {
		short = in.B
	}
	assert.Len(t, short.Books, 10)
}

// The other exclusions come first, as for any initials pair: a hub beside
// more than twenty full names is capped, a hub on a book of its full name
// is co-credited, a hub with a namesake tag is out — none is a hub
// candidate.
func TestAHubKeepsTheOtherExclusions(t *testing.T) {
	var fulls []*name
	for i := range 21 {
		fulls = append(fulls, person("Смирнов", "Борис", fmt.Sprintf("Отчество%c", 'А'+rune(i))))
	}
	p := sealed(t, hubs(t, person("Смирнов", "Б."), 11, fulls...).c, nil)
	assert.Zero(t, counted(p, authorentity.CandidateInitialsHub, "Cyrl"))
	assert.Equal(t, 21, excluded(p, authorentity.CandidateInitials, "Cyrl", authorentity.ExcludedPartnerCap))

	ch := nosovs(t, 11)
	ch.c.credit(2, 1001, "fp-full-0", person("Носов", "Николай", "Николаевич"))
	p = sealed(t, ch.c, nil)
	assert.Zero(t, counted(p, authorentity.CandidateInitialsHub, "Cyrl"))
	requireExcluded(t, p, authorentity.CandidateInitials, authorentity.ExcludedCoCredited, 1001, 1)

	p = sealed(t, hubs(t, person("Носов", "Н.").withTag(2), 11, person("Носов", "Николай", "Николаевич")).c, nil)
	assert.Zero(t, counted(p, authorentity.CandidateInitialsHub, "Cyrl"))
	requireExcluded(t, p, authorentity.CandidateInitials, authorentity.ExcludedNamesakeTag, 1001, 1)
}

// A hub and an ordinary initials pair in one catalog: the ordinary pair's
// candidate is byte for byte the one a catalog without the hub has.
func TestAHubChangesNoOtherCandidate(t *testing.T) {
	ordinary := func(ch *churn) *churn {
		ch.c.book(501, "Очерк").credit(501, 501, "fp-ivanov", person("Иванов", "Алексей", "Викторович")).
			book(502, "Заметки").credit(502, 502, "fp-a-ivanov", person("Иванов", "А."))
		return ch
	}
	without := sealed(t, ordinary(hubs(t, person("Носов", "Н."), 1, person("Носов", "Николай", "Николаевич"))).c, nil)
	with := sealed(t, ordinary(nosovs(t, 11)).c, nil)
	before := requireCandidate(t, without, authorentity.CandidateInitials, "Cyrl", 502, 501)
	after := requireCandidate(t, with, authorentity.CandidateInitials, "Cyrl", 502, 501)
	assert.Equal(t, before.Digest, after.Digest)
	assert.Equal(t, inputOf(without, before), inputOf(with, after))
	requireCandidate(t, with, authorentity.CandidateInitialsHub, "Cyrl", 1001, 1)
}

// «С. В. Иванов» on eleven books beside «Семён Викторович» and «Сергей
// Владимирович»: two hub candidates; of the two decisions the first
// applies, the second would join two full given names and is held
// (given_name), as for initials.
func TestTheGivenNameGuardHoldsAHub(t *testing.T) {
	w := newWorld(t, hubs(t, person("Иванов", "С.", "В."), 11, person("Иванов", "Семён", "Викторович"),
		person("Иванов", "Сергей", "Владимирович")))
	w.pass()
	semyon := w.decide(authorentity.CandidateInitialsHub, 1001, 1)
	sergey := w.decide(authorentity.CandidateInitialsHub, 1001, 101)
	p := w.pass()
	assert.Equal(t, applied(semyon), w.state(semyon))
	assert.Equal(t, conflict(sergey, authorentity.ConflictGivenName), w.state(sergey))
	same(t, p, 1001, 1)
	apart(t, p, 1, 101)
}

// A suite item in the hub shape is the initials shape with the initial on
// the side of more books, as in the slice: «С. Смирнов» on Sergey
// Ivanovich's three books against Sergey Nikolaevich's full name. The
// initials shape keeps it on the side of fewer.
func TestTheHubSuiteItemPutsTheInitialOnTheLargerSide(t *testing.T) {
	c := newCatalog(t)
	for _, id := range []int64{1, 2, 3} {
		c.book(id, fmt.Sprintf("Повесть %d", id)).credit(id, id, "fp-ivanovich", person("Смирнов", "Сергей", "Иванович"))
	}
	c.book(11, "Рассказ").credit(11, 11, "fp-nikolaevich", person("Смирнов", "Сергей", "Николаевич"))
	p := sealed(t, c, nil)
	pairs := p.MergeSuitePairs(nil)
	require.Len(t, pairs, 1)
	initialBooks := func(shape string) (int, string) {
		in := p.RenderSuiteItem(&pairs[0], shape, false, nil)
		for _, card := range []authorentity.MergeCard{in.A, in.B} {
			if card.Names[0] == "С. Смирнов" {
				other := in.A
				if other.Names[0] == "С. Смирнов" {
					other = in.B
				}
				return len(card.Books), other.Names[0]
			}
		}
		t.Fatalf("%s: no side of initials: %+v", shape, in)
		return 0, ""
	}
	books, full := initialBooks(authorentity.CandidateInitialsHub)
	assert.Equal(t, 3, books)
	assert.Equal(t, "Сергей Николаевич Смирнов", full)
	books, full = initialBooks(authorentity.CandidateInitials)
	assert.Equal(t, 1, books)
	assert.Equal(t, "Сергей Иванович Смирнов", full)
}

// longFields are card fields of books from..to with an annotation of 300
// Cyrillic letters each: about 700 bytes a book on the card.
func longFields(from, to int64) map[int64]authorentity.CardBookFields {
	fields := map[int64]authorentity.CardBookFields{}
	for id := from; id <= to; id++ {
		fields[id] = authorentity.CardBookFields{Year: "1999", Lang: "ru", Genres: []string{"prose"},
			Annotation: strings.Repeat("ж", 300)}
	}
	return fields
}

// A hub whose whole card would not fit one question (MergeInputMaxBytes)
// is never truncated: it is no candidate of either kind but an exclusion,
// initials_hub_too_long, counted and sampled.
func TestAHubTooLongToAskIsExcluded(t *testing.T) {
	p := sealed(t, nosovs(t, 60).c, longFields(1001, 1060))
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitialsHub, 1001, 1))
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitials, 1001, 1))
	requireExcluded(t, p, authorentity.CandidateInitialsHub, authorentity.ExcludedHubTooLong, 1001, 1)
	assert.Zero(t, counted(p, authorentity.CandidateInitialsHub, "Cyrl"))
	assert.Equal(t, 1, excluded(p, authorentity.CandidateInitialsHub, "Cyrl", authorentity.ExcludedHubTooLong))
	assert.Empty(t, p.MergeCandidates())
	assert.Equal(t, 0, p.Report.Candidates.Candidates)
	assert.Equal(t, 1, p.Report.Candidates.Excluded)

	// The same hub on fewer books fits and is asked whole.
	p = sealed(t, nosovs(t, 30).c, longFields(1001, 1030))
	c := requireCandidate(t, p, authorentity.CandidateInitialsHub, "Cyrl", 1001, 1)
	assert.LessOrEqual(t, len(inputOf(p, c)), authorentity.MergeInputMaxBytes)
	hub, _ := hubCards(t, p, c)
	assert.Len(t, hub.Books, 30)
}

// A hub decision whose card grew past the bound lapses with the
// exclusion: the hub is never applied on a card that can no longer be
// asked whole.
func TestAHubDecisionLapsesWhenItsCardOutgrowsTheBound(t *testing.T) {
	ch := nosovs(t, 30)
	ch.fields = longFields(1001, 1060)
	w := newWorld(t, ch)
	w.pass()
	id := w.decide(authorentity.CandidateInitialsHub, 1001, 1)
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	same(t, p, 1001, 1)
	for i := range 30 {
		book := int64(1031 + i)
		ch.c.book(book, fmt.Sprintf("Рассказ %d", book)).credit(book, book, "fp-short", person("Носов", "Н."))
	}
	p = w.pass()
	assert.Equal(t, lapsed(id, authorentity.ExcludedHubTooLong), w.state(id))
	apart(t, p, 1001, 1)
}

// The hub suite item shows the initial's side whole, as a hub's card does,
// and its books are loaded whole: «С. Смирнов» on all twelve of Sergey
// Ivanovich's books. Other shapes keep their samples and their loads.
func TestTheHubSuiteItemListsTheInitialsSideWhole(t *testing.T) {
	c := newCatalog(t)
	for id := int64(1); id <= 12; id++ {
		c.book(id, fmt.Sprintf("Повесть %d", id)).credit(id, id, "fp-ivanovich", person("Смирнов", "Сергей", "Иванович"))
	}
	c.book(101, "Рассказ").credit(101, 101, "fp-nikolaevich", person("Смирнов", "Сергей", "Николаевич"))
	p := sealed(t, c, nil)
	pairs := p.MergeSuitePairs(nil)
	require.Len(t, pairs, 1)
	in := p.RenderSuiteItem(&pairs[0], authorentity.CandidateInitialsHub, false, nil)
	initial := in.A
	if initial.Names[0] != "С. Смирнов" {
		initial = in.B
	}
	require.Equal(t, "С. Смирнов", initial.Names[0])
	assert.Len(t, initial.Books, 12)
	assert.Len(t, p.MergeSuiteBookIDs(pairs, true), 13)
	assert.Len(t, p.MergeSuiteBookIDs(pairs, false), 11)
	in = p.RenderSuiteItem(&pairs[0], authorentity.CandidateSwap, false, nil)
	assert.Equal(t, 10, max(len(in.A.Books), len(in.B.Books)), "the swap shape keeps the sample")
}

// The bound excludes a hub only: an ordinary pair whose cards are over it
// stays a candidate as before (the eval and the merge run skip its input as
// too long), its digest unchanged by the hub's rule.
func TestTheBoundExcludesNoOrdinaryPair(t *testing.T) {
	c := newCatalog(t)
	long := strings.Repeat("Длинное название ", 120)
	for i := int64(0); i < 10; i++ {
		c.book(1+i, fmt.Sprintf("%s%d", long, 1+i)).credit(1+i, 1+i, "fp-a", person("Петров", "Иван"))
		c.book(101+i, fmt.Sprintf("%s%d", long, 101+i)).credit(101+i, 101+i, "fp-b", person("Иван", "Петров"))
	}
	p := sealed(t, c, nil)
	swap := requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 1, 101)
	assert.Greater(t, len(inputOf(p, swap)), authorentity.MergeInputMaxBytes)
}
