package authorentity_test

import (
	"encoding/json"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The durable veto in the build (§4.11, E4b-3): every union pass keeps the
// two sides of a live veto apart, the candidates and the decision step
// refuse them, and a pass without a veto is what it was.

// bookFP is one recorded (book, fingerprint) of a veto side.
type bookFP struct {
	book int64
	fp   string
}

// veto records a live veto with sides a and b; the books' current
// fingerprints are read from the credits at build time.
func (c *catalog) veto(id int64, a, b []bookFP) *catalog {
	v := authorentity.MergeVeto{ID: id, ItemKind: authorentity.ItemEntity}
	for side, members := range map[string][]bookFP{authorentity.VetoSideA: a, authorentity.VetoSideB: b} {
		for _, m := range members {
			v.Members = append(v.Members, authorentity.VetoMember{Side: side, BookID: m.book, Fingerprint: m.fp})
		}
	}
	c.in.Vetoes = append(c.in.Vetoes, v)
	return c
}

// bookVeto is a book item's veto: the book with its credit's fingerprint
// against the full side.
func (c *catalog) bookVeto(id, book int64, fp string, full []bookFP) *catalog {
	c.veto(id, []bookFP{{book, fp}}, full)
	v := &c.in.Vetoes[len(c.in.Vetoes)-1]
	v.ItemKind, v.BookID = authorentity.ItemBook, book
	return c
}

// vetoBuild builds with the current fingerprints of every recorded book.
func (c *catalog) vetoBuild() *authorentity.Plan {
	c.t.Helper()
	c.in.VetoBooks = map[int64][]string{}
	for _, cr := range c.in.Credits {
		c.in.VetoBooks[cr.BookID] = append(c.in.VetoBooks[cr.BookID], cr.Fingerprint)
	}
	return c.build()
}

// state is the veto's state in the pass.
func vetoState(t *testing.T, p *authorentity.Plan, id int64) authorentity.VetoState {
	t.Helper()
	for _, s := range p.VetoStates() {
		if s.VetoID == id {
			return s
		}
	}
	t.Fatalf("no state of veto %d", id)
	return authorentity.VetoState{}
}

// (a) A normalizer change gives A and B one name key, fingerprints kept:
// D2 would join them; the veto splits the key group.
func TestAVetoSplitsAKeyGroupHoldingBothSides(t *testing.T) {
	base := func() *catalog {
		return newCatalog(t).book(1, "Оно").book(2, "Сияние").book(3, "Рассказы").
			credit(1, 1, "fp-a", person("Кинг", "Стивен")).credit(2, 2, "fp-a", person("Кинг", "Стивен")).
			credit(3, 3, "fp-b", person("Кинг", "Стивен"))
	}
	same(t, base().build(), 1, 3)
	p := base().veto(7, []bookFP{{1, "fp-a"}, {2, "fp-a"}}, []bookFP{{3, "fp-b"}}).vetoBuild()
	apart(t, p, 1, 3)
	same(t, p, 1, 2)
	require.NotNil(t, p.Report.Veto)
	assert.Equal(t, 1, p.Report.Veto.SplitD2)
	assert.Zero(t, p.Report.Veto.SplitWidened)
	assert.Equal(t, authorentity.VetoState{VetoID: 7, State: authorentity.VetoAnchored, HeldBooks: 3},
		vetoState(t, p, 7))
}

// (b) A field split puts A's words in B's order: D2b would join the
// word-sequence component; the veto leaves it apart whole.
func TestAVetoLeavesAD2bComponentApart(t *testing.T) {
	base := func() *catalog {
		return newCatalog(t).book(1, "Оно").book(2, "Рассказы").
			credit(1, 1, "fp-a", person("Кинг", "Стивен")).
			credit(2, 2, "fp-b", person("", "Стивен", "Кинг"))
	}
	same(t, base().build(), 1, 2)
	p := base().veto(7, []bookFP{{1, "fp-a"}}, []bookFP{{2, "fp-b"}}).vetoBuild()
	apart(t, p, 1, 2)
	assert.Equal(t, 1, p.Report.Veto.D2bVetoed)
	assert.Equal(t, 1, p.Report.D2b.Refused[authorentity.D2bRefusedVetoed])
}

// (c) An extractor rewrite gives A new raw fields D2-equal to B: A's books
// are lost, the veto unanchored, side a widened to the new fingerprint, and
// the key group is split on the widened side, reported.
func TestAWidenedSideSplitsAndIsReported(t *testing.T) {
	p := newCatalog(t).book(1, "Оно").book(2, "Сияние").book(3, "Рассказы").
		credit(1, 1, "fp-a2", person("Кинг", "Стивен")).credit(2, 2, "fp-a2", person("Кинг", "Стивен")).
		credit(3, 3, "fp-b", person("Кинг", "Стивен")).
		veto(7, []bookFP{{1, "fp-a"}, {2, "fp-a"}}, []bookFP{{3, "fp-b"}}).vetoBuild()
	apart(t, p, 1, 3)
	assert.Equal(t, authorentity.VetoState{VetoID: 7, State: authorentity.VetoUnanchored, HeldBooks: 1, LostBooks: 2,
		WidenedFingerprints: 1}, vetoState(t, p, 7))
	v := p.Report.Veto
	assert.Equal(t, 1, v.SplitD2)
	assert.Equal(t, 1, v.SplitWidened)
	require.Len(t, v.Widened, 1)
	assert.Equal(t, int64(7), v.Widened[0].VetoID)
	assert.ElementsMatch(t, []any{p.EntityOf(1), p.EntityOf(3)}, []any{v.Widened[0].Kept, v.Widened[0].New})
	assert.Len(t, v.Widened[0].Fingerprints, 1)
}

// (d) A typo edge between A and B is refused.
func TestAVetoRefusesATypoMerge(t *testing.T) {
	base := func() *catalog {
		return newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).
			credit(10, 1, "fp-a", person("Иванов", "Алексей", "Викторович")).
			credit(11, 2, "fp-b", person("Иванов", "Алексей", "Викторовыч"))
	}
	same(t, base().build(), 10, 11)
	p := base().veto(7, []bookFP{{1, "fp-a"}}, []bookFP{{2, "fp-b"}}).vetoBuild()
	apart(t, p, 10, 11)
	assert.Equal(t, 1, p.Report.Veto.Refused[authorentity.VetoRefusedTypo])
	assert.Zero(t, p.Report.TypoMerges)
}

// (e) A Wikidata item naming A and B is refused: the set is left apart.
func TestAVetoRefusesAWikidataMerge(t *testing.T) {
	base := func() *catalog {
		return newCatalog(t).book(1, "Сияние").book(3, "It").
			credit(1, 1, "king-ru", person("Кинг", "Стивен")).
			credit(3, 3, "king-en", shown("Stephen King", person("King", "Stephen").latin()))
	}
	ref := func() *reference {
		return newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"})
	}
	same(t, base().with(ref()).build(), 1, 3)
	p := base().with(ref()).veto(7, []bookFP{{1, "king-ru"}}, []bookFP{{3, "king-en"}}).vetoBuild()
	apart(t, p, 1, 3)
	assert.Equal(t, 1, p.Report.Veto.Refused[authorentity.VetoRefusedWikidata])
}

// A chain A–C (D2b), C–B (typo): the second link would join the sides, and
// is refused over the components' full member sets.
func TestAVetoRefusesTheSecondLinkOfAChain(t *testing.T) {
	base := func() *catalog {
		return newCatalog(t).book(1, "Первая").book(2, "Вторая", 3).book(3, "Третья", 3).
			credit(1, 1, "fp-a", person("Викторавич Иванов", "Алексей")).
			credit(2, 2, "fp-c", person("Иванов", "Алексей", "Викторавич")).
			credit(3, 3, "fp-b", person("Иванов", "Алексей", "Викторович"))
	}
	free := base().build()
	same(t, free, 1, 2)
	same(t, free, 2, 3)
	p := base().veto(7, []bookFP{{1, "fp-a"}}, []bookFP{{3, "fp-b"}}).vetoBuild()
	same(t, p, 1, 2)
	apart(t, p, 2, 3)
	assert.Equal(t, 1, p.Report.Veto.Refused[authorentity.VetoRefusedTypo])
}

// A book veto keeps its book in the bucket against D3, which would move it
// to the full name it was revoked from.
func TestABookVetoKeepsTheBookInItsBucket(t *testing.T) {
	base := func() *catalog {
		return newCatalog(t).book(1, "Сердце пармы", 501).book(2, "Золото бунта", 501).
			credit(1, 1, "fp-full", person("Иванов", "Алексей", "Викторович")).
			credit(2, 2, "fp-short", person("Иванов", "Алексей"))
	}
	same(t, base().build(), 1, 2)
	p := base().bookVeto(7, 2, "fp-short", []bookFP{{1, "fp-full"}}).vetoBuild()
	apart(t, p, 1, 2)
	assert.Equal(t, 1, p.Report.Veto.KeptInBucket)
	assert.Zero(t, p.Report.Buckets.Moved)
}

// The vetoed pair is no candidate (revoked_pair), and a decision older than
// the veto whose union would join its sides through a third entity is a
// conflict, vetoed.
func TestAVetoRefusesCandidatesAndDecisions(t *testing.T) {
	ch := threePoselyagins(t)
	w := newWorld(t, ch)
	w.pass()
	ab := w.decide(authorentity.CandidateSwap, 1, 11)
	bc := w.decide(authorentity.CandidateSwap, 11, 21)
	var a []bookFP
	for i := int64(1); i <= 6; i++ {
		a = append(a, bookFP{i, "fp-a"})
	}
	ch.c.veto(7, a, []bookFP{{21, "fp-c"}})
	ch.c.in.VetoBooks = map[int64][]string{21: {"fp-c"}}
	for i := int64(1); i <= 6; i++ {
		ch.c.in.VetoBooks[i] = []string{"fp-a"}
	}
	p := w.pass()
	assert.Equal(t, conflict(ab, authorentity.ConflictVetoed), w.state(ab))
	assert.Equal(t, conflict(bc, authorentity.ConflictVetoed), w.state(bc))
	apart(t, p, 1, 21)
	apart(t, p, 1, 11)
	assert.Equal(t, 1, excluded(w.level0, authorentity.CandidateSwap, "Cyrl", authorentity.ExcludedRevokedPair))
	assert.Equal(t, 1, p.Report.Veto.Refused[authorentity.VetoRefusedModel])
}

// A veto on another pair (the C–D control) changes nothing of A and B, and
// no veto at all leaves the report without a veto section.
func TestAnUnrelatedVetoChangesNothingElse(t *testing.T) {
	base := func() *catalog {
		return newCatalog(t).book(1, "Оно").book(2, "Рассказы").book(3, "Война и мир").book(4, "Анна Каренина").
			credit(1, 1, "fp-a", person("Кинг", "Стивен")).credit(2, 2, "fp-b", person("Кинг", "Стивен")).
			credit(3, 3, "fp-c", person("Толстой", "Лев")).credit(4, 4, "fp-d", person("Достоевский", "Фёдор"))
	}
	plain := base().build()
	assert.Nil(t, plain.Report.Veto)
	raw, err := json.Marshal(plain.Report)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "veto")
	vetoed := base().veto(7, []bookFP{{3, "fp-c"}}, []bookFP{{4, "fp-d"}}).vetoBuild()
	same(t, vetoed, 1, 2)
	apart(t, vetoed, 3, 4)
	assert.Len(t, vetoed.Units, len(plain.Units))
	assert.Zero(t, vetoed.Report.Veto.SplitD2)
	assert.Zero(t, excluded(vetoed, authorentity.CandidateSwap, "Cyrl", authorentity.ExcludedRevokedPair))
}

// §4.2 case 10: a member's ordinal changed since the decision (person order
// in the credit). The decision's members no longer name the candidate's: it
// lapses and the pair stays apart.
func TestADecisionOnAnotherOrdinalLapses(t *testing.T) {
	w := newWorld(t, newChurn(t, 40))
	w.pass()
	id := w.decide(authorentity.CandidateSwap, 1, 500)
	d := &w.decisions[len(w.decisions)-1]
	d.MembersB = append([]authorentity.FPKey(nil), d.MembersB...)
	for i := range d.MembersB {
		d.MembersB[i].Ordinal = 1
	}
	d.MembersSHA256 = authorentity.MembersSHA256(d.MembersA, d.MembersB, nil)
	d.Digest[0] ^= 0xff
	p := w.pass()
	assert.Equal(t, lapsed(id, authorentity.LapsedMembersChanged), w.state(id))
	apart(t, p, 1, 500)
}

// Continuity is per recorded book, not per side: one of A's two recorded
// books re-extracted with other raw fields is lost even though A's
// recorded fingerprint is still held on the other book.
func TestOneLostRecordedBookUnanchors(t *testing.T) {
	p := newCatalog(t).book(1, "Оно").book(2, "Сияние").book(3, "Рассказы").
		credit(1, 1, "fp-a2", person("Кинг", "Стивен")).credit(2, 2, "fp-a", person("Кинг", "Стивен")).
		credit(3, 3, "fp-b", person("Стивен", "Кинг")).
		veto(7, []bookFP{{1, "fp-a"}, {2, "fp-a"}}, []bookFP{{3, "fp-b"}}).vetoBuild()
	assert.Equal(t, authorentity.VetoState{VetoID: 7, State: authorentity.VetoUnanchored, HeldBooks: 2, LostBooks: 1,
		WidenedFingerprints: 1}, vetoState(t, p, 7))
	same(t, p, 1, 2)
	apart(t, p, 1, 3)
}

// A veto with no recorded book on a side could keep nothing apart: it is
// refused like an overlap, never gone.
func TestAVetoWithAnEmptySideRefusesThePass(t *testing.T) {
	p := newCatalog(t).book(1, "Оно").book(3, "Рассказы").
		credit(1, 1, "fp-a", person("Кинг", "Стивен")).credit(3, 3, "fp-b", person("Стивен", "Кинг")).
		veto(7, []bookFP{{1, "fp-a"}}, nil).vetoBuild()
	assert.Equal(t, []authorentity.VetoOverlap{{VetoID: 7, EmptySide: true}}, p.VetoOverlaps())
}

// A decision on the very pair a later veto keeps apart: its candidate is
// excluded revoked_pair, and the decision is a conflict, vetoed.
func TestADirectDecisionAcrossAVetoIsAConflict(t *testing.T) {
	ch := threePoselyagins(t)
	w := newWorld(t, ch)
	w.pass()
	ab := w.decide(authorentity.CandidateSwap, 1, 11)
	var a []bookFP
	for i := int64(1); i <= 6; i++ {
		a = append(a, bookFP{i, "fp-a"})
	}
	ch.c.veto(7, a, []bookFP{{11, "fp-b"}, {12, "fp-b"}, {13, "fp-b"}})
	ch.c.in.VetoBooks = map[int64][]string{}
	for _, cr := range ch.c.in.Credits {
		ch.c.in.VetoBooks[cr.BookID] = append(ch.c.in.VetoBooks[cr.BookID], cr.Fingerprint)
	}
	p := w.pass()
	assert.Equal(t, conflict(ab, authorentity.ConflictVetoed), w.state(ab))
	apart(t, p, 1, 11)
	assert.Equal(t, 1, p.Report.Model.Conflict[authorentity.ConflictVetoed])
}

// A book decision whose target grew across a veto in the decision step: the
// bucket book's decision names V, a level-0 candidate the veto does not
// touch, but V's union with X (a translit decision) puts the veto's other
// side into the target. The book is not moved there: a conflict, vetoed.
func TestABookDecisionIntoAVetoedUnionIsAConflict(t *testing.T) {
	ch := bucketBeside(t)
	w := newWorld(t, ch)
	w.pass()
	vx := w.decide(authorentity.CandidateTranslit, 1, 4)
	book := w.decide(authorentity.CandidateBucket, 3, 1)
	ch.c.bookVeto(7, 3, "fp-short", []bookFP{{3, "fp-x"}, {5, "fp-x"}})
	ch.c.in.VetoBooks = map[int64][]string{}
	for _, cr := range ch.c.in.Credits {
		ch.c.in.VetoBooks[cr.BookID] = append(ch.c.in.VetoBooks[cr.BookID], cr.Fingerprint)
	}
	p := w.pass()
	assert.Equal(t, applied(vx), w.state(vx))
	assert.Equal(t, conflict(book, authorentity.ConflictVetoed), w.state(book))
	apart(t, p, 3, 1)
}
