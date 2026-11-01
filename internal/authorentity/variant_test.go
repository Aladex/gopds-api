package authorentity_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E4f, variant: two full names of one script that spell one name two ways —
// a patronymic spelled two ways (P1: equal under the spelling fold), with a
// typo (P2: one or two edits, the same gender), one middle name a word
// prefix of the other (P3), or the whole name equal under the spelling fold
// (S: э/е, і/и, й/и, the signs, a doubled letter). Two different
// patronymics (P4) are two people and never a candidate.

// variantCatalog is one credit per name, each on a book of its own.
func variantCatalog(t *testing.T, names ...*name) *catalog {
	c := newCatalog(t)
	for i, n := range names {
		id := int64(10 + i)
		c.book(id, "Книга "+string(rune('А'+i))).credit(id, id, "fp-"+string(rune('a'+i)), n)
	}
	return c
}

func TestVariantCandidates(t *testing.T) {
	p := sealed(t, variantCatalog(t,
		person("Борушко", "Олег", "Матвевич"), person("Борушко", "Олег", "Матвеевич"), // 10, 11: P1
		person("Раззаков", "Федор", "Ибатович"), person("Раззаков", "Федор", "Ипатович"), // 12, 13: P2
		person("Шиллер", "Фридрих", "Иоганн"), person("Шиллер", "Фридрих", "Иоганн", "Кристоф"), // 14, 15: P3
		person("Брэдбери", "Рэй"), person("Бредбері", "Рей"), // 16, 17: S
		person("Brackett", "Leigh", "Douglass").latin(), person("Brackett", "Leigh", "Douglas").latin(), // 18, 19
		person("Степанов", "Александр", "Владимирович"), person("Степанов", "Александр", "Николаевич"), // 20, 21: P4
		person("Петров", "Иван", "Иванович"), person("Петров", "Иван", "Ивановна"), // 22, 23: two genders
		person("Ли", "Ан", "Ким"), person("Ли", "Ан", "Кин"), // 24, 25: too short for a typo
		person("Даррел", "Лоренс"), person("Даррелл", "Лоренс"), // 26, 27: S, a doubled letter
	), nil)
	requireCandidate(t, p, authorentity.CandidateVariant, "Cyrl", 10, 11)
	requireCandidate(t, p, authorentity.CandidateVariant, "Cyrl", 12, 13)
	requireCandidate(t, p, authorentity.CandidateVariant, "Cyrl", 14, 15)
	requireCandidate(t, p, authorentity.CandidateVariant, "Cyrl", 16, 17)
	requireCandidate(t, p, authorentity.CandidateVariant, "Latn", 18, 19)
	assert.Nil(t, candidateOf(p, authorentity.CandidateVariant, 20, 21), "two patronymics are two people")
	assert.Nil(t, candidateOf(p, authorentity.CandidateVariant, 22, 23), "a son and a daughter")
	assert.Nil(t, candidateOf(p, authorentity.CandidateVariant, 24, 25), "a short patronymic tolerates no typo")
	requireCandidate(t, p, authorentity.CandidateVariant, "Cyrl", 26, 27)
	assert.Equal(t, 5, counted(p, authorentity.CandidateVariant, "Cyrl"))
	assert.Equal(t, 1, counted(p, authorentity.CandidateVariant, "Latn"))
}

// A variant never crosses scripts, never takes initials for a name, and a
// pair that is another kind's stays there: variant is the lowest kind.
func TestShapesTheVariantLeavesOut(t *testing.T) {
	p := sealed(t, variantCatalog(t,
		person("Брэдбери", "Рэй"), person("Bradbury", "Ray").latin(), // 10, 11: two scripts
		person("Мур", "Роджер", "Е"), person("Мур", "Роджер", "Э"), // 12, 13: initials
		person("Кинг", "Стивен"), person("Стивен", "Кинг"), // 14, 15: a swap
		// 16, 17: one display key on two spellings, a swap and a variant
		// at once: the swap keeps it.
		person("Брэдбери", "Рэй"), shown("Рэй Брэдбери", person("Бредбери", "Рэй")),
	), nil)
	assert.Nil(t, candidateOf(p, authorentity.CandidateVariant, 10, 11))
	assert.Nil(t, candidateOf(p, authorentity.CandidateVariant, 12, 13))
	requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 14, 15)
	assert.Nil(t, candidateOf(p, authorentity.CandidateVariant, 14, 15))
	requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 16, 17)
	requireExcluded(t, p, authorentity.CandidateVariant, authorentity.ExcludedPrecedence, 16, 17)
	assert.Zero(t, counted(p, authorentity.CandidateVariant, "Cyrl"))
}

// The namesake guards of every kind hold a variant too: two sides on one
// book (a father and his son), a namesake tag, a short name's bucket.
func TestTheVariantNamesakeGuards(t *testing.T) {
	p := sealed(t, newCatalog(t).book(1, "Отцы и дети").book(2, "Другая").book(3, "Третья").book(4, "Четвёртая").
		book(5, "Пятая").book(6, "Шестая").
		credit(10, 1, "fp-father", person("Воинов", "Александр")).
		credit(11, 1, "fp-son", person("Войнов", "Александр")).
		credit(12, 2, "fp-tagged", person("Мирнев", "Владимир", "Никанорович").withTag(2)).
		credit(13, 3, "fp-plain", person("Мирнев", "Владимир", "Никонорович")).
		credit(14, 4, "fp-short", person("Бредбери", "Рэй")).
		credit(15, 5, "fp-other-short", person("Брэдбери", "Рэй")).
		credit(16, 6, "fp-full", person("Брэдбери", "Рэй", "Дуглас")), nil)
	requireExcluded(t, p, authorentity.CandidateVariant, authorentity.ExcludedCoCredited, 10, 11)
	requireExcluded(t, p, authorentity.CandidateVariant, authorentity.ExcludedNamesakeTag, 12, 13)
	requireExcluded(t, p, authorentity.CandidateVariant, authorentity.ExcludedBucketSide, 14, 15)
}

// razzakovs: «Федор Ибатович Раззаков» (1), «Федор Ипатович Раззаков» (11),
// «Федор Ипатьевич Раззаков» (21) — a variant of the second, not of the
// first — and a short-name book «Федор Раззаков» (31).
func razzakovs(t *testing.T) *churn {
	return distinctNames(t, person("Раззаков", "Федор", "Ибатович"), person("Раззаков", "Федор", "Ипатович"),
		person("Раззаков", "Федор", "Ипатьевич"), person("Раззаков", "Федор"))
}

// distinctNames is a catalog of one credit per name, on books 1, 11, 21, …
// of titles of their own: no title links two of them, so the typo merge
// leaves them apart.
func distinctNames(t *testing.T, names ...*name) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i, n := range names {
		id := int64(1 + 10*i)
		ch.c.book(id, "Книга "+string(rune('А'+i))).credit(id, id, "fp-"+string(rune('a'+i)), n)
	}
	return ch
}

// The variant card shows the side's Wikidata label, when there is one; a
// card of another kind does not.
func TestTheVariantCardShowsTheWikidataLabel(t *testing.T) {
	c := razzakovs(t).c.with(newReference().item("Q1", []string{"Fyodor Razzakov", "Федор Ибатович Раззаков"}))
	p := sealed(t, c, nil)
	cand := requireCandidate(t, p, authorentity.CandidateVariant, "Cyrl", 1, 11)
	var in authorentity.MergeInput
	require.NoError(t, json.Unmarshal(inputOf(p, cand), &in))
	labels := []string{in.A.Wikidata, in.B.Wikidata}
	assert.ElementsMatch(t, []string{"Fyodor Razzakov", ""}, labels)
	for i := range p.MergeCandidates() {
		other := &p.MergeCandidates()[i]
		if other.Kind == authorentity.CandidateVariant {
			continue
		}
		assert.NotContains(t, string(p.MergeInput(i)), `"wikidata"`, other.Kind)
	}
}

// inputOf is a candidate's input.
func inputOf(p *authorentity.Plan, c *authorentity.MergeCandidate) []byte {
	for i := range p.MergeCandidates() {
		if p.MergeCandidates()[i].Digest == c.Digest {
			return p.MergeInput(i)
		}
	}
	return nil
}

// A variant decision joins the two spellings it asked about: the given-name
// guard, which holds two full patronymics of one family apart, lets the
// asked pair through. A chain of variants does not pass: «Ипатьевич» is a
// variant of «Ипатович», not of «Ибатович», and the union would hold both.
func TestAVariantDecisionJoinsItsTwoSpellingsOnly(t *testing.T) {
	w := newWorld(t, razzakovs(t))
	w.pass()
	first := w.decide(authorentity.CandidateVariant, 1, 11)
	p := w.pass()
	assert.Equal(t, applied(first), w.state(first))
	same(t, p, 1, 11)
	chain := w.decide(authorentity.CandidateVariant, 11, 21)
	p = w.pass()
	assert.Equal(t, applied(first), w.state(first))
	assert.Equal(t, conflict(chain, authorentity.ConflictGivenName), w.state(chain))
	apart(t, p, 1, 21)
}

// The guard reads a patronymic under the variant's spelling fold too:
// «Эммануэлевич» and «Емануелевич» are three edits apart and one name.
func TestAVariantDecisionJoinsTwoFoldedPatronymics(t *testing.T) {
	w := newWorld(t, distinctNames(t, person("Шмидт", "Карл", "Эммануэлевич"), person("Шмидт", "Карл", "Емануелевич")))
	w.pass()
	id := w.decide(authorentity.CandidateVariant, 1, 11)
	p := w.pass()
	assert.Equal(t, applied(id), w.state(id))
	same(t, p, 1, 11)
}

// An initials decision is held as before: the variant's tolerance is its own.
func TestAnInitialsDecisionStillHoldsTwoPatronymics(t *testing.T) {
	w := newWorld(t, distinctNames(t, person("Раззаков", "Федор", "Ибатович"), person("Раззаков", "Ф.", "И."),
		person("Раззаков", "Федор", "Ипатович")))
	w.pass()
	first := w.decide(authorentity.CandidateInitials, 11, 1)
	second := w.decide(authorentity.CandidateInitials, 11, 21)
	p := w.pass()
	assert.Equal(t, applied(first), w.state(first))
	assert.Equal(t, conflict(second, authorentity.ConflictGivenName), w.state(second))
	apart(t, p, 1, 21)
}

// A short-name book both of whose targets are one person: two_targets while
// the targets are apart; once a variant decision joins them, the next pass
// applies both book decisions without a new question.
func TestAVariantMergeClearsTwoTargets(t *testing.T) {
	w := newWorld(t, razzakovs(t))
	w.pass()
	toIbatovich := w.decide(authorentity.CandidateBucket, 31, 1)
	toIpatovich := w.decide(authorentity.CandidateBucket, 31, 11)
	p := w.pass()
	assert.Equal(t, conflict(toIbatovich, authorentity.ConflictTwoTargets), w.state(toIbatovich))
	assert.Equal(t, conflict(toIpatovich, authorentity.ConflictTwoTargets), w.state(toIpatovich))
	apart(t, p, 31, 1)

	variant := w.decide(authorentity.CandidateVariant, 1, 11)
	p = w.pass()
	assert.Equal(t, applied(variant), w.state(variant))
	assert.Equal(t, applied(toIbatovich), w.state(toIbatovich))
	assert.Equal(t, applied(toIpatovich), w.state(toIpatovich))
	same(t, p, 31, 1)
	same(t, p, 31, 11)
	assert.Equal(t, 1, p.Report.Model.BookMoves)
}

// A typo merge the build made on evidence is a variant anchor: two
// spellings of one patronymic in one entity, shown as two cards; a side
// whose key groups are no variant of each other (a D2b union) gives none.
func TestATypoMergeIsAVariantAnchor(t *testing.T) {
	p := sealed(t, newCatalog(t).book(1, "Первая", 7).book(2, "Вторая", 7).book(3, "Третья").book(4, "Четвёртая").
		credit(1, 1, "fp-sergeevich", person("Котов", "Иван", "Сергеевич")).
		credit(2, 2, "fp-sergevich", person("Котов", "Иван", "Сергевич")).
		// One person by D2b: the same words, the patronymic in the given
		// name; two key groups that are no variant of each other.
		credit(3, 3, "fp-plain", person("Ершов", "Пётр", "Павлович")).
		credit(4, 4, "fp-joined", shown("Ершов Пётр Павлович", person("Ершов", "Пётр Павлович"))), nil)
	same(t, p, 1, 2)
	same(t, p, 3, 4)
	anchors := p.MergeVariantAnchorPairs(nil)
	require.Len(t, anchors, 1)
	assert.Equal(t, authorentity.CandidateVariant, anchors[0].Kind)
	assert.Equal(t, p.EntityOf(1), anchors[0].Entity)
	in := p.RenderAnchor(&anchors[0], nil)
	assert.ElementsMatch(t, []string{"Иван Сергеевич Котов", "Иван Сергевич Котов"},
		append(in.A.Names, in.B.Names...))
	assert.Empty(t, in.A.Wikidata+in.B.Wikidata)
}

// A suite item writes one person's patronymic as a variant would.
func TestRespell(t *testing.T) {
	for in, want := range map[string]string{
		"Сергеевич": "Сергевич", "Геннадьевич": "Генадьевич", "Васильевич": "Василиевич",
		"Валериевич": "Валерьевич", "Петрович": "Петравич", "Douglass": "Douglas", "Kowalski": "Kawalski",
		"": "",
	} {
		assert.Equal(t, want, authorentity.Respell(in), in)
	}
}

// A suite item in the variant shape shows one person's full name against
// the same name respelled on the other person's books: only the books tell
// them apart.
func TestTheVariantSuiteItemRespellsOnePersonsName(t *testing.T) {
	p := sealed(t, distinctNames(t, person("Смирнов", "Сергей", "Иванович"), person("Смирнов", "Сергей", "Николаевич")).c,
		nil)
	pairs := p.MergeSuitePairs(nil)
	require.Len(t, pairs, 1)
	in := p.RenderSuiteItem(&pairs[0], authorentity.CandidateVariant, false, nil)
	require.Len(t, in.A.Names, 1)
	require.Len(t, in.B.Names, 1)
	names := []string{in.A.Names[0], in.B.Names[0]}
	full := slices.IndexFunc(names, func(n string) bool {
		return n == "Сергей Иванович Смирнов" || n == "Сергей Николаевич Смирнов"
	})
	require.GreaterOrEqual(t, full, 0, "one side shows a person's full name: %v", names)
	words := strings.Fields(names[full])
	assert.Equal(t, words[0]+" "+authorentity.Respell(words[1])+" "+words[2], names[1-full])
	assert.NotEqual(t, names[0], names[1])
}
