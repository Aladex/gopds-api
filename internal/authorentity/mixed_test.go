package authorentity_test

import (
	"encoding/json"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E4h: a level-0 side whose keys span two scripts — a Wikidata cross-script
// merge — took part in no pair (mixed_script). It now pairs through the key
// that matches, the item recorded in that key's script, as a kind of its
// own per shape (bucket_mixed, swap_mixed, initials_mixed, translit_mixed,
// variant_mixed): asked, gated and registered apart from the plain kinds,
// whose candidates stay as they were.

// bulgakovs: «Михаил Афанасьевич Булгаков» (1–3) and «Mikhail Bulgakov» (4)
// one side by Q835's labels; «Mihail Bulgakov» (11, 12) a Latin side of its
// own; the bucket «Михаил Булгаков» (21, 22); «Михаил Афанасиевич
// Булгаков» (31), a plain spelling of the mixed side's name.
func bulgakovs(t *testing.T) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	c := ch.c
	for _, id := range []int64{1, 2, 3} {
		c.book(id, "Мастер и Маргарита "+string(rune('А'+id))).credit(id, id, "fp-ru", person("Булгаков", "Михаил", "Афанасьевич"))
	}
	c.book(4, "The Master and Margarita").credit(4, 4, "fp-en", person("Bulgakov", "Mikhail").latin())
	c.book(11, "Sobachye serdtse").credit(11, 11, "fp-mihail", person("Bulgakov", "Mihail").latin())
	c.book(12, "Belaya gvardiya").credit(12, 12, "fp-mihail", person("Bulgakov", "Mihail").latin())
	c.book(21, "Роковые яйца").credit(21, 21, "fp-bucket", person("Булгаков", "Михаил"))
	c.book(22, "Дни Турбиных").credit(22, 22, "fp-bucket", person("Булгаков", "Михаил"))
	c.book(31, "Записки юного врача").credit(31, 31, "fp-variant", person("Булгаков", "Михаил", "Афанасиевич"))
	c.with(newReference().item("Q835", []string{"Михаил Афанасьевич Булгаков", "Mikhail Bulgakov"}))
	return ch
}

// The mixed side pairs through its matching keys: a translit with the Latin
// «Mihail Bulgakov» (Cyrl-Latn), a variant with «Афанасиевич» (Cyrl), and
// each bucket book against it (Cyrl) — every one a _mixed kind, none a
// mixed_script exclusion, none a plain kind.
func TestAMixedSidePairsThroughItsMatchingKey(t *testing.T) {
	p := sealed(t, bulgakovs(t).c, nil)
	same(t, p, 1, 4)
	requireCandidate(t, p, authorentity.CandidateTranslitMixed, authorentity.ScriptCyrlLatn, 11, 1)
	requireCandidate(t, p, authorentity.CandidateVariantMixed, "Cyrl", 31, 1)
	assert.Nil(t, candidateOf(p, authorentity.CandidateTranslit, 11, 1))
	assert.Nil(t, candidateOf(p, authorentity.CandidateVariant, 31, 1))
	for _, credit := range []int64{21, 22} {
		c := bookItemOf(p, credit, 1)
		require.NotNil(t, c, "bucket book %d against the mixed side", credit)
		assert.Equal(t, authorentity.CandidateBucketMixed, c.Kind)
		assert.Equal(t, "Cyrl", c.Script)
		assert.Equal(t, authorentity.ItemBook, c.ItemKind)
	}
	for kind, scripts := range p.Report.Candidates.ByKind {
		for script, counts := range scripts {
			assert.Zero(t, counts.Excluded[authorentity.ExcludedMixedScript], "%s %s", kind, script)
		}
	}
}

// The mixed side's card shows every written form of it, both scripts, and
// the Wikidata item it is, label and QID; a plain kind's card carries no
// Wikidata field.
func TestTheMixedCardShowsItsFormsAndItsItem(t *testing.T) {
	p := sealed(t, bulgakovs(t).c, nil)
	c := requireCandidate(t, p, authorentity.CandidateTranslitMixed, authorentity.ScriptCyrlLatn, 11, 1)
	var in authorentity.MergeInput
	require.NoError(t, json.Unmarshal(inputOf(p, c), &in))
	mixed, latin := in.A, in.B
	if c.SideA == p.EntityOf(11) {
		mixed, latin = in.B, in.A
	}
	assert.Equal(t, []string{"Mikhail Bulgakov", "Михаил Афанасьевич Булгаков"}, mixed.Names)
	assert.Equal(t, "Михаил Афанасьевич Булгаков", mixed.Wikidata)
	assert.Equal(t, "Q835", mixed.WikidataID)
	assert.Empty(t, latin.Wikidata)
	assert.Empty(t, latin.WikidataID)
	for i := range p.MergeCandidates() {
		other := &p.MergeCandidates()[i]
		if authorentity.MixedKind(other.Kind) || other.Kind == authorentity.CandidateVariant {
			continue
		}
		assert.NotContains(t, string(p.MergeInput(i)), `"wikidata`, other.Kind)
	}
}

// A plain pair in the same catalog is byte for byte the candidate a catalog
// without the mixed side has.
func TestAMixedSideChangesNoPlainCandidate(t *testing.T) {
	plain := func(ch *churn) *churn {
		ch.c.book(501, "Очерк").credit(501, 501, "fp-ivanov", person("Иванов", "Алексей", "Викторович")).
			book(502, "Заметки").credit(502, 502, "fp-a-ivanov", person("Иванов", "А."))
		return ch
	}
	without := sealed(t, plain(&churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}).c, nil)
	ch := bulgakovs(t)
	ch.c.book(501, "Очерк").credit(501, 501, "fp-ivanov", person("Иванов", "Алексей", "Викторович")).
		book(502, "Заметки").credit(502, 502, "fp-a-ivanov", person("Иванов", "А."))
	ch.c.with(newReference().item("Q835", []string{"Михаил Афанасьевич Булгаков", "Mikhail Bulgakov"}))
	with := sealed(t, ch.c, nil)
	before := requireCandidate(t, without, authorentity.CandidateInitials, "Cyrl", 502, 501)
	after := requireCandidate(t, with, authorentity.CandidateInitials, "Cyrl", 502, 501)
	assert.Equal(t, before.Digest, after.Digest)
	assert.Equal(t, inputOf(without, before), inputOf(with, after))
	requireCandidate(t, with, authorentity.CandidateTranslitMixed, authorentity.ScriptCyrlLatn, 11, 1)
}

// tolstoys: «Лев Николаевич Толстой» (1, 2) and «Lev Nikolayevich Tolstoy»
// (3) one side by Q7243; «Толстой Лев Николаевич» (11) shown so; «Л. Н.
// Толстой» on n books from 101.
func tolstoys(t *testing.T, n int) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	c := ch.c
	c.book(1, "Война и мир").credit(1, 1, "fp-ru", person("Толстой", "Лев", "Николаевич")).
		book(2, "Анна Каренина").credit(2, 2, "fp-ru", person("Толстой", "Лев", "Николаевич")).
		book(3, "War and Peace").credit(3, 3, "fp-en", person("Tolstoy", "Lev", "Nikolayevich").latin()).
		book(11, "Детство").credit(11, 11, "fp-swap",
		shown("Толстой Лев Николаевич", person("Лев", "Толстой", "Николаевич")))
	for i := range int64(n) {
		c.book(101+i, "Рассказ "+string(rune('А'+i))).credit(101+i, 101+i, "fp-short", person("Толстой", "Л.", "Н."))
	}
	c.with(newReference().item("Q7243", []string{"Лев Николаевич Толстой", "Lev Nikolayevich Tolstoy"}))
	return ch
}

// The swap of a mixed side's Cyrillic display is a swap_mixed in Cyrillic;
// its initials of ten books an initials_mixed; of eleven, the initials side
// a hub, the pair stays out as mixed_script (never asked on a ten-book
// sample).
func TestASwapAndInitialsOfAMixedSide(t *testing.T) {
	p := sealed(t, tolstoys(t, 10).c, nil)
	same(t, p, 1, 3)
	requireCandidate(t, p, authorentity.CandidateSwapMixed, "Cyrl", 11, 1)
	requireCandidate(t, p, authorentity.CandidateInitialsMixed, "Cyrl", 101, 1)
	assert.Nil(t, candidateOf(p, authorentity.CandidateSwap, 11, 1))
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitials, 101, 1))

	p = sealed(t, tolstoys(t, 11).c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitialsMixed, 101, 1))
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitialsHub, 101, 1))
	requireExcluded(t, p, authorentity.CandidateInitials, authorentity.ExcludedMixedScript, 101, 1)
}

// A translit_mixed decision joins the Latin side to the mixed one; a
// variant_mixed passes the given-name guard as a variant does.
func TestMixedDecisionsJoin(t *testing.T) {
	w := newWorld(t, bulgakovs(t))
	w.pass()
	translit := w.decide(authorentity.CandidateTranslitMixed, 11, 1)
	variant := w.decide(authorentity.CandidateVariantMixed, 31, 1)
	p := w.pass()
	assert.Equal(t, applied(translit), w.state(translit))
	assert.Equal(t, applied(variant), w.state(variant))
	same(t, p, 11, 1)
	same(t, p, 31, 1)
}

// A bucket_mixed decision never holds a live book move: a book a plain
// bucket decision moved to «Афанасиевич» keeps it while the mixed side is
// another entity (the mixed decision is held two_targets); once a
// variant_mixed joins the two, both apply, the book moved once.
func TestABucketMixedDecisionNeverHoldsALiveMove(t *testing.T) {
	w := newWorld(t, bulgakovs(t))
	w.pass()
	plain := w.decide(authorentity.CandidateBucket, 21, 31)
	p := w.pass()
	assert.Equal(t, applied(plain), w.state(plain))
	same(t, p, 21, 31)

	mixed := w.decide(authorentity.CandidateBucketMixed, 21, 1)
	p = w.pass()
	assert.Equal(t, applied(plain), w.state(plain))
	assert.Equal(t, conflict(mixed, authorentity.ConflictTwoTargets), w.state(mixed))
	same(t, p, 21, 31)
	apart(t, p, 21, 1)

	variant := w.decide(authorentity.CandidateVariantMixed, 31, 1)
	p = w.pass()
	assert.Equal(t, applied(variant), w.state(variant))
	assert.Equal(t, applied(plain), w.state(plain))
	assert.Equal(t, applied(mixed), w.state(mixed))
	same(t, p, 21, 1)
	assert.Equal(t, 1, p.Report.Model.BookMoves)
}

// Two plain decisions to two targets are held both, as before: the rule is
// the mixed decisions' only.
func TestTwoPlainTargetsAreStillHeldBoth(t *testing.T) {
	w := newWorld(t, razzakovs(t))
	w.pass()
	a := w.decide(authorentity.CandidateBucket, 31, 1)
	b := w.decide(authorentity.CandidateBucket, 31, 11)
	w.pass()
	assert.Equal(t, conflict(a, authorentity.ConflictTwoTargets), w.state(a))
	assert.Equal(t, conflict(b, authorentity.ConflictTwoTargets), w.state(b))
}

// The suite renders a _mixed shape as its plain one.
func TestAMixedSuiteShapeIsItsPlainShape(t *testing.T) {
	p := sealed(t, distinctNames(t, person("Смирнов", "Сергей", "Иванович"), person("Смирнов", "Сергей", "Николаевич")).c,
		nil)
	pairs := p.MergeSuitePairs(nil)
	require.Len(t, pairs, 1)
	for _, kind := range []string{authorentity.CandidateSwap, authorentity.CandidateInitials, authorentity.CandidateTranslit,
		authorentity.CandidateBucket, authorentity.CandidateVariant} {
		assert.Equal(t, p.RenderSuiteItem(&pairs[0], kind, false, nil),
			p.RenderSuiteItem(&pairs[0], kind+"_mixed", false, nil), kind)
	}
}

// stokers: «Брэм Стокер» (1, 2) and «Bram Stoker» (3) one side by Q2; a
// partner of the Cyrillic script on books of its own (11).
func stokers(t *testing.T, partner *name) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Дракула").credit(1, 1, "fp-ru", person("Стокер", "Брэм")).
		book(2, "Логово белого червя").credit(2, 2, "fp-ru", person("Стокер", "Брэм")).
		book(3, "Dracula").credit(3, 3, "fp-en", person("Stoker", "Bram").latin()).
		book(11, "Гость Дракулы").credit(11, 11, "fp-partner", partner)
	ch.c.with(newReference().item("Q2", []string{"Брэм Стокер", "Bram Stoker"}))
	return ch
}

// A translit through the mixed side's alias pairs two names of one script:
// it is asked only when the partner is one of the mixed side's names of
// its script — the family two edits at most and the given name the same or
// one the start of the other, under the spelling fold. «Брем Стокер» is
// one; «Бернард Стокер» (another given name) and «Брэм Стаукиэр» (the same
// skeleton, four edits off) are not (mixed_other_name).
func TestATranslitMixedOfAnotherNameIsExcluded(t *testing.T) {
	p := sealed(t, stokers(t, person("Стокер", "Брем")).c, nil)
	same(t, p, 1, 3)
	requireCandidate(t, p, authorentity.CandidateTranslitMixed, authorentity.ScriptCyrlLatn, 11, 1)
	for _, other := range []*name{person("Стокер", "Бернард"), person("Стаукиэр", "Брэм")} {
		p = sealed(t, stokers(t, other).c, nil)
		assert.Nil(t, candidateOf(p, authorentity.CandidateTranslitMixed, 11, 1), "%+v", other)
		requireExcluded(t, p, authorentity.CandidateTranslitMixed, authorentity.ExcludedMixedOtherName, 11, 1)
	}
	// A prefix of three letters is one given name: «Ник» and «Николас».
	p = sealed(t, stokers(t, person("Стокер", "Брэмуэлл")).c, nil)
	requireCandidate(t, p, authorentity.CandidateTranslitMixed, authorentity.ScriptCyrlLatn, 11, 1)
}

// A translit_mixed decision passes the given-name guard on a spelling of
// one name, as a variant does: «Брэм» and «Брем» are one under the fold.
func TestATranslitMixedJoinsASpelling(t *testing.T) {
	w := newWorld(t, stokers(t, person("Стокер", "Брем")))
	w.pass()
	id := w.decide(authorentity.CandidateTranslitMixed, 11, 1)
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	same(t, p, 11, 1)
}

// The gate reads a Latin given name without its marks: «Emile Gaboriau» is
// one of «Эмиль Габорио» / «Émile Gaboriau»'s names; a given name of two
// letters is the start of none («Бр» is not «Брэм»).
func TestTheGateFoldsMarksAndWantsThreeLetters(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Дело Леруж").credit(1, 1, "fp-ru", person("Габорио", "Эмиль")).
		book(2, "L'Affaire Lerouge").credit(2, 2, "fp-fr", person("Gaboriau", "Émile").latin()).
		book(11, "Monsieur Lecoq").credit(11, 11, "fp-partner", person("Gaboriau", "Emile").latin())
	ch.c.with(newReference().item("Q4", []string{"Эмиль Габорио", "Émile Gaboriau"}))
	p := sealed(t, ch.c, nil)
	requireCandidate(t, p, authorentity.CandidateTranslitMixed, authorentity.ScriptCyrlLatn, 11, 1)

	p = sealed(t, stokers(t, person("Стокер", "Бр")).c, nil)
	requireExcluded(t, p, authorentity.CandidateTranslitMixed, authorentity.ExcludedMixedOtherName, 11, 1)
}

// A mixed side without a family or given name — a nickname in two
// scripts — is never asked: its swap stays out (no_family_or_given) under
// the mixed kind.
func TestAMixedSideWithoutANameStaysOut(t *testing.T) {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	ch.c.book(1, "Двадцать тысяч лье").credit(1, 1, "fp-ru", nickOnly("Капитан Немо")).
		book(2, "Таинственный остров").credit(2, 2, "fp-ru", nickOnly("Капитан Немо")).
		book(3, "Twenty Thousand Leagues").credit(3, 3, "fp-en", nickOnly("Captain Nemo").latin()).
		book(11, "Наутилус").credit(11, 11, "fp-swap", nickOnly("Немо Капитан"))
	ch.c.with(newReference().item("Q5", []string{"Капитан Немо", "Captain Nemo"}))
	p := sealed(t, ch.c, nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateSwapMixed, 11, 1))
	assert.Nil(t, candidateOf(p, authorentity.CandidateSwap, 11, 1))
}
