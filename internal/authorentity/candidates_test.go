package authorentity_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sealed builds the catalog and seals its candidates with the card fields
// of its books (none when fields is nil).
func sealed(t *testing.T, c *catalog, fields map[int64]authorentity.CardBookFields) *authorentity.Plan {
	t.Helper()
	p := c.build()
	require.NoError(t, p.SealMergeCandidates(fields))
	return p
}

// candidateOf is the candidate of kind whose sides hold credits a and b,
// nil when there is none.
func candidateOf(p *authorentity.Plan, kind string, a, b int64) *authorentity.MergeCandidate {
	ea, eb := p.EntityOf(a), p.EntityOf(b)
	cands := p.MergeCandidates()
	for i := range cands {
		c := &cands[i]
		if c.Kind == kind && ((c.SideA == ea && c.SideB == eb) || (c.SideA == eb && c.SideB == ea)) {
			return c
		}
	}
	return nil
}

// bookItemOf is the book item of credit against the entity of full.
func bookItemOf(p *authorentity.Plan, credit, full int64) *authorentity.MergeCandidate {
	cands := p.MergeCandidates()
	for i := range cands {
		c := &cands[i]
		if c.ItemKind == authorentity.ItemBook && c.CreditID == credit && c.SideB == p.EntityOf(full) {
			return c
		}
	}
	return nil
}

// sampleOf is the sample row of the pair of credits a and b of kind, nil
// when there is none.
func sampleOf(p *authorentity.Plan, kind string, a, b int64) *authorentity.MergeCandidateSample {
	ea, eb := p.EntityOf(a), p.EntityOf(b)
	rows := p.MergeCandidateSample()
	for i := range rows {
		s := &rows[i]
		if s.Kind == kind && ((s.EntityA == ea && s.EntityB == eb) || (s.EntityA == eb && s.EntityB == ea)) {
			return s
		}
	}
	return nil
}

func excluded(p *authorentity.Plan, kind, script, reason string) int {
	if c := p.Report.Candidates.ByKind[kind][script]; c != nil {
		return c.Excluded[reason]
	}
	return 0
}

func counted(p *authorentity.Plan, kind, script string) int {
	if c := p.Report.Candidates.ByKind[kind][script]; c != nil {
		return c.Candidates
	}
	return 0
}

// requireCandidate fails unless the pair is a candidate of kind and script.
func requireCandidate(t *testing.T, p *authorentity.Plan, kind, script string, a, b int64) *authorentity.MergeCandidate {
	t.Helper()
	c := candidateOf(p, kind, a, b)
	require.NotNil(t, c, "credits %d and %d: no %s candidate; sample %+v", a, b, kind, p.MergeCandidateSample())
	assert.Equal(t, script, c.Script)
	assert.Equal(t, authorentity.ItemEntity, c.ItemKind)
	return c
}

// requireExcluded fails unless the pair is kept out of kind for reason.
func requireExcluded(t *testing.T, p *authorentity.Plan, kind, reason string, a, b int64) {
	t.Helper()
	assert.Nil(t, candidateOf(p, kind, a, b), "credits %d and %d: a %s candidate", a, b, kind)
	s := sampleOf(p, kind, a, b)
	require.NotNil(t, s, "credits %d and %d: no %s sample row", a, b, kind)
	assert.Equal(t, reason, s.Reason)
}

// E4a, swap: the same words of the display name in another order, two-
// and three-word, and the residual identical shape (one display key on two
// level-0 components); Latin is the same kind, its script recorded.
func TestSwapCandidates(t *testing.T) {
	p := sealed(t, newCatalog(t).book(1, "Оно").book(2, "Сияние").book(3, "Мир-Кольцо").book(4, "Стрелок").
		book(5, "Мизери").book(6, "Кэрри").book(7, "It").book(8, "Misery").
		credit(10, 1, "fp-king", person("Кинг", "Стивен")).
		credit(11, 2, "fp-king-swapped", person("Стивен", "Кинг")).
		credit(12, 3, "fp-poselyagin", person("Поселягин", "Владимир", "Геннадьевич")).
		credit(13, 4, "fp-poselyagin-swapped", person("Владимир", "Поселягин", "Геннадьевич")).
		// The source spells the swapped parts in the usual order: one display
		// key on two components that D2 and D2b keep apart.
		credit(14, 5, "fp-identical", shown("Стивен Кинг", person("Стивен", "Кинг"))).
		credit(15, 7, "fp-king-en", person("King", "Stephen").latin()).
		credit(16, 8, "fp-king-en-swapped", person("Stephen", "King").latin()), nil)
	requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 10, 11)
	requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 12, 13)
	requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 10, 14)
	requireCandidate(t, p, authorentity.CandidateSwap, "Latn", 15, 16)
	assert.Equal(t, 1, counted(p, authorentity.CandidateSwap, "Latn"))
	// Words of another script are no swap.
	assert.Nil(t, candidateOf(p, authorentity.CandidateSwap, 10, 15))
}

// E4a, initials: «Л. Н. Толстой» against «Лев Николаевич Толстой», not «Лев
// Толстой»; «А. Толстой» with its three partners; a Latin pair.
func TestInitialsCandidates(t *testing.T) {
	p := sealed(t, newCatalog(t).book(1, "Война и мир").book(2, "Анна Каренина").book(3, "Казаки").
		book(4, "Хождение по мукам").book(5, "Князь Серебряный").book(6, "Пётр Первый").book(7, "Аэлита").
		book(8, "The Hobbit").book(9, "The Silmarillion").
		credit(10, 1, "fp-ln", person("Толстой", "Л.", "Н.")).
		credit(11, 2, "fp-lev-nikolaevich", person("Толстой", "Лев", "Николаевич")).
		credit(12, 3, "fp-lev", person("Толстой", "Лёв")).
		credit(13, 4, "fp-a", person("Толстой", "А.")).
		credit(14, 5, "fp-aleksey-konstantinovich", person("Толстой", "Алексей", "Константинович")).
		credit(15, 6, "fp-aleksey-nikolaevich", person("Толстой", "Алексей", "Николаевич")).
		credit(16, 7, "fp-aleksandr", person("Толстой", "Александр", "Иванович")).
		credit(17, 8, "fp-jrr", person("Tolkien", "J. R. R.").latin()).
		credit(18, 9, "fp-john", person("Tolkien", "John", "Ronald", "Reuel").latin()), nil)
	requireCandidate(t, p, authorentity.CandidateInitials, "Cyrl", 10, 11)
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitials, 10, 12), "«Л. Н.» against «Лёв» alone")
	assert.Nil(t, sampleOf(p, authorentity.CandidateInitials, 10, 12))
	for _, full := range []int64{14, 15, 16} {
		requireCandidate(t, p, authorentity.CandidateInitials, "Cyrl", 13, full)
	}
	assert.Nil(t, candidateOf(p, authorentity.CandidateInitials, 13, 11), "«А.» against «Лев»")
	requireCandidate(t, p, authorentity.CandidateInitials, "Latn", 17, 18)
}

// E4a, translit: Cyrillic against Latin by the tight skeleton rule —
// family skeleton equal and the given name's skeleton starting with the
// same two consonants. Кинг/King and Азимов/Asimov are candidates; «Kong»
// with another given name of the same initial matches only the loose rule.
func TestTranslitCandidates(t *testing.T) {
	p := sealed(t, newCatalog(t).book(1, "Оно").book(2, "It").book(3, "Я, робот").book(4, "I, Robot").
		book(5, "King Kong").
		credit(10, 1, "fp-king", person("Кинг", "Стивен")).
		credit(11, 2, "fp-king-en", person("King", "Stephen").latin()).
		credit(12, 3, "fp-asimov", person("Азимов", "Айзек")).
		credit(13, 4, "fp-asimov-en", person("Asimov", "Isaac").latin()).
		credit(14, 5, "fp-kong", person("Kong", "Sam").latin()), nil)
	requireCandidate(t, p, authorentity.CandidateTranslit, authorentity.ScriptCyrlLatn, 10, 11)
	requireCandidate(t, p, authorentity.CandidateTranslit, authorentity.ScriptCyrlLatn, 12, 13)
	requireExcluded(t, p, authorentity.CandidateTranslit, authorentity.ExcludedLooseScriptOnly, 10, 14)
	// «Stephen Kong» shares Кинг's skeleton and given name: tight.
	p = sealed(t, newCatalog(t).book(1, "Оно").book(2, "King Kong").
		credit(10, 1, "fp-king", person("Кинг", "Стивен")).
		credit(14, 2, "fp-kong", person("Kong", "Stephen").latin()), nil)
	requireCandidate(t, p, authorentity.CandidateTranslit, authorentity.ScriptCyrlLatn, 10, 14)
}

// E4a, bucket: every book of a short name's bucket beside full names of its
// family and given name is asked against each of them, one item per book
// and full entity; a book D3 moved is decided and not asked.
func TestBucketBookItems(t *testing.T) {
	p := sealed(t, newCatalog(t).book(1, "Общага-на-Крови").book(2, "Психология").book(3, "Общага на крови").
		book(4, "Футбол").book(5, "Сердце пармы").
		credit(10, 1, "fp-viktorovich", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-aleksandrovich", person("Иванов", "Алексей", "Александрович")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).
		credit(13, 4, "fp-short", person("Иванов", "Алексей")).
		credit(14, 5, "fp-short", person("Иванов", "Алексей")), nil)
	// Book 3 moves to Викторович by its title.
	same(t, p, 10, 12)
	assert.Nil(t, bookItemOf(p, 12, 10))
	assert.Nil(t, bookItemOf(p, 12, 11))
	for _, credit := range []int64{13, 14} {
		for _, full := range []int64{10, 11} {
			c := bookItemOf(p, credit, full)
			require.NotNil(t, c, "bucket book %d against %d", credit, full)
			assert.Equal(t, authorentity.CandidateBucket, c.Kind)
			assert.Equal(t, "Cyrl", c.Script)
			assert.Equal(t, p.EntityOf(13), c.SideA)
			assert.Equal(t, []authorentity.FPKey{{Fingerprint: "fp-short", ExtractorVersion: "v1"}}, c.MembersA)
		}
	}
	assert.Equal(t, 4, counted(p, authorentity.CandidateBucket, "Cyrl"))
}

// Never a candidate: a pair with a short name's bucket on a side, two
// entities on one book, a namesake tag, a manual side, a side whose display
// key is a pseudonym of the other's item, a mixed-script side, a side
// without a family or given name, an initials side of more than ten books,
// a loose-only translit, too many partners, a pair already of a higher
// kind. One fixture makes every reason the build can make; revoked_pair is
// E4b's.
func TestEveryExclusionReason(t *testing.T) {
	c := newCatalog(t)
	for i := int64(1); i <= 60; i++ {
		c.book(i, fmt.Sprintf("Книга %d", i))
	}
	c.credit(1, 1, "fp-bucket", person("Иванов", "Алексей")).
		credit(2, 2, "fp-full", person("Иванов", "Алексей", "Викторович")).
		// bucket_side: a swap of the bucket.
		credit(3, 3, "fp-bucket-swap", person("Алексей", "Иванов")).
		// co_credited: a swap pair on one book.
		credit(4, 4, "fp-co", person("Петров", "Иван")).
		credit(5, 4, "fp-co-swap", person("Иван", "Петров")).
		// namesake_tag.
		credit(6, 5, "fp-tag", person("Сидоров", "Пётр")).
		credit(7, 6, "fp-tag-swap", person("Пётр", "Сидоров").withTag(2)).
		// manual_side: a manual row fixes a credit of the side's fingerprint.
		credit(8, 7, "fp-manual", person("Козлов", "Олег")).
		credit(9, 8, "fp-manual", person("Козлов", "Олег")).
		credit(10, 9, "fp-manual-swap", person("Олег", "Козлов")).
		// pseudonym_key: the other side's display key is a pseudonym of
		// this one's item.
		credit(11, 10, "fp-bulychev", person("Можейко", "Игорь")).
		credit(12, 11, "fp-bulychev-en", person("Mozheyko", "Igor").latin()).
		// mixed_script: a side whose script is neither Cyrillic nor Latin.
		credit(13, 12, "fp-mixed", mixedScript(person("Лемм", "Stas"))).
		credit(14, 13, "fp-mixed-swap", mixedScript(person("Stas", "Лемм"))).
		// no_family_or_given: two nicknames of one words in two orders.
		credit(15, 14, "fp-nick", nickOnly("Капитан Немо")).
		credit(16, 15, "fp-nick-swap", nickOnly("Немо Капитан")).
		// initials_side_gt10: «Н. Носов» on eleven books.
		credit(17, 27, "fp-nosov", person("Носов", "Николай", "Николаевич")).
		// loose_script_only.
		credit(18, 28, "fp-king", person("Кинг", "Стивен")).
		credit(19, 29, "fp-kong", person("Kong", "Sam").latin())
	for i := int64(16); i <= 26; i++ {
		c.credit(100+i, i, "fp-n-nosov", person("Носов", "Н."))
	}
	// partner_cap: «Б. Смирнов» beside 21 full names.
	c.credit(30, 30, "fp-b-smirnov", person("Смирнов", "Б."))
	for i := int64(0); i < 21; i++ {
		c.credit(200+i, 31+i%29, fmt.Sprintf("fp-smirnov-%d", i), person("Смирнов", "Борис", fmt.Sprintf("Отчество%c", 'А'+rune(i))))
	}
	manual := uuid.New()
	c.in.Old = authorentity.NewState()
	c.in.Old.Entities[manual] = authorentity.EntityRow{ID: manual, Kind: "person", DisplayName: "Олег Козлов"}
	key := authorentity.CreditKey{CreditID: 9}
	c.in.Old.Credits[key] = authorentity.CreditRow{Key: key, EntityID: &manual, Method: authorentity.MethodManual,
		Rule: "manual"}
	c.with(newReference().item("Q1", []string{"Игорь Можейко"}, "pseudonym:Igor Mozheyko"))
	p := sealed(t, c, nil)

	requireExcluded(t, p, authorentity.CandidateSwap, authorentity.ExcludedBucketSide, 1, 3)
	requireExcluded(t, p, authorentity.CandidateSwap, authorentity.ExcludedCoCredited, 4, 5)
	requireExcluded(t, p, authorentity.CandidateSwap, authorentity.ExcludedNamesakeTag, 6, 7)
	requireExcluded(t, p, authorentity.CandidateSwap, authorentity.ExcludedManualSide, 8, 10)
	requireExcluded(t, p, authorentity.CandidateTranslit, authorentity.ExcludedPseudonymKey, 11, 12)
	requireExcluded(t, p, authorentity.CandidateSwap, authorentity.ExcludedMixedScript, 13, 14)
	requireExcluded(t, p, authorentity.CandidateSwap, authorentity.ExcludedNoFamilyOrGiven, 15, 16)
	requireExcluded(t, p, authorentity.CandidateInitials, authorentity.ExcludedInitialsSideGT10, 116, 17)
	requireExcluded(t, p, authorentity.CandidateTranslit, authorentity.ExcludedLooseScriptOnly, 18, 19)
	requireExcluded(t, p, authorentity.CandidateInitials, authorentity.ExcludedPartnerCap, 30, 200)
	assert.Equal(t, 21, excluded(p, authorentity.CandidateInitials, "Cyrl", authorentity.ExcludedPartnerCap))
	for _, reason := range authorentity.ExclusionReasons {
		total := 0
		for _, scripts := range p.Report.Candidates.ByKind {
			for _, counts := range scripts {
				total += counts.Excluded[reason]
			}
		}
		switch reason {
		case authorentity.ExcludedRevokedPair:
			assert.Zero(t, total, "revoked pairs before E4b")
		case authorentity.ExcludedPrecedence:
			// A pair of two kinds needs a component of two scripts: its own test.
		default:
			assert.Positive(t, total, "no pair excluded %s", reason)
		}
	}
}

// mixedScript marks a name of letters of two scripts, as the normalizer
// records it.
func mixedScript(n *name) *name { n.script = "mixed"; return n }

// A pair of two kinds is a candidate of the higher one only: the component
// the Wikidata merge made of «Лев Николаевич Толстой» and «Lev Nikolayevich
// Tolstoy» is a swap of «Tolstoy Lev Nikolayevich» by its display name and
// a translit of it by its Cyrillic key; the translit is precedence.
func TestAPairOfTwoKindsTakesTheHigher(t *testing.T) {
	c := newCatalog(t).book(1, "Война и мир").book(2, "War and Peace").book(3, "Anna Karenina").
		book(4, "Childhood").
		credit(10, 1, "fp-ru", person("Толстой", "Лев", "Николаевич")).
		credit(11, 2, "fp-en", person("Tolstoy", "Lev", "Nikolayevich").latin()).
		credit(12, 3, "fp-en", person("Tolstoy", "Lev", "Nikolayevich").latin()).
		credit(13, 4, "fp-swap", shown("Tolstoy Lev Nikolayevich", person("Tolstoy", "Lev").latin()))
	c.with(newReference().item("Q7243", []string{"Лев Николаевич Толстой", "Lev Nikolayevich Tolstoy"}))
	p := sealed(t, c, nil)
	same(t, p, 10, 11)
	requireExcluded(t, p, authorentity.CandidateTranslit, authorentity.ExcludedPrecedence, 10, 13)
	s := sampleOf(p, authorentity.CandidateSwap, 10, 13)
	require.NotNil(t, s)
	assert.NotEqual(t, authorentity.SampleCandidate, s.Reason, "the mixed component is a candidate")
}

// ≤ 20 partners per side and kind: twenty are asked, twenty-one keep the
// side out.
func TestThePartnerCap(t *testing.T) {
	for _, tc := range []struct {
		partners int
		want     string
	}{{20, authorentity.SampleCandidate}, {21, authorentity.ExcludedPartnerCap}} {
		c := newCatalog(t).book(1, "Книга")
		c.credit(1, 1, "fp-a", person("Смирнов", "Б."))
		for i := 0; i < tc.partners; i++ {
			c.book(int64(10+i), fmt.Sprintf("Книга %d", i)).
				credit(int64(10+i), int64(10+i), fmt.Sprintf("fp-%d", i), person("Смирнов", "Борис", fmt.Sprintf("Отчество%c", 'А'+rune(i))))
		}
		p := sealed(t, c, nil)
		got := 0
		for i := 0; i < tc.partners; i++ {
			if s := sampleOf(p, authorentity.CandidateInitials, 1, int64(10+i)); s != nil && s.Reason == tc.want {
				got++
			}
		}
		assert.Equal(t, tc.partners, got, "%d partners", tc.partners)
	}
}

// One side whose handling panics is left out alone, counted; NBSP and
// mojibake names are names like any other.
func TestOneBadSideNeverStopsTheCandidates(t *testing.T) {
	defer authorentity.PanicOnCandidateSide(14)()
	p := sealed(t, newCatalog(t).book(1, "Оно").book(2, "Сияние").book(3, "А").book(4, "Б").book(5, "В").
		book(6, "Г").
		credit(10, 1, "fp-king", person("Кинг ", "Стивен")).
		credit(11, 2, "fp-king-swapped", person("Стивен", "Кинг")).
		credit(12, 3, "fp-moji", person("Ð¢Ð¾Ð»ÑÑ‚Ð¾Ð¹", "Ð›ÐµÐ²").latin()).
		credit(13, 4, "fp-moji-swapped", person("Ð›ÐµÐ²", "Ð¢Ð¾Ð»ÑÑ‚Ð¾Ð¹").latin()).
		credit(14, 5, "fp-bad", person("Петров", "Иван")).
		credit(15, 6, "fp-bad-swapped", person("Иван", "Петров")), nil)
	requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 10, 11)
	requireCandidate(t, p, authorentity.CandidateSwap, "Latn", 12, 13)
	assert.Nil(t, candidateOf(p, authorentity.CandidateSwap, 14, 15))
	assert.Equal(t, 1, p.Report.Candidates.ItemsFailed)
}

// The card the build hashes is the one MergeCardOf renders from the same
// rows, byte for byte: oldest ten books, the size class, the names, the
// co-authors' selected names, the card fields.
func TestTheCardTheBuildHashesIsTheRenderedOne(t *testing.T) {
	c := newCatalog(t)
	fields := map[int64]authorentity.CardBookFields{}
	for i := int64(1); i <= 12; i++ {
		c.book(i, fmt.Sprintf("Повесть %d", i))
		fields[i] = authorentity.CardBookFields{Year: fmt.Sprint(1900 + i), Lang: "ru", Genres: []string{"prose_classic", "prose_rus_classic"},
			Series: []string{"Собрание сочинений"}, Annotation: string(bytes.Repeat([]byte("а"), 400))}
		c.credit(i, i, "fp-king", person("Кинг", "Стивен"))
	}
	c.book(20, "Сияние [СИ]").credit(20, 20, "fp-swap", person("Стивен", "Кинг")).
		credit(21, 20, "fp-co", person("Страуб", "Питер"))
	fields[20] = authorentity.CardBookFields{Year: "1977"}
	p := sealed(t, c, fields)
	cand := requireCandidate(t, p, authorentity.CandidateSwap, "Cyrl", 1, 20)
	i := slices.IndexFunc(p.MergeCandidates(), func(m authorentity.MergeCandidate) bool { return m.Digest == cand.Digest })

	var sample []authorentity.MergeCardBook
	for id := int64(1); id <= 10; id++ {
		sample = append(sample, authorentity.MergeCardBookOf(fmt.Sprintf("Повесть %d", id), ptrTo(fields[id]), nil))
	}
	king := authorentity.MergeCardOf([]string{"Стивен Кинг"}, nil, 12, sample)
	swap := authorentity.MergeCardOf([]string{"Кинг Стивен"}, nil, 1,
		[]authorentity.MergeCardBook{authorentity.MergeCardBookOf("Сияние [СИ]", ptrTo(fields[20]), []string{"Питер Страуб"})})
	a, b := king, swap
	if cand.SideA == p.EntityOf(20) {
		a, b = swap, king
	}
	want := authorentity.MergeInputJSON(&a, &b)
	assert.Equal(t, string(want), string(p.MergeInput(i)))
	assert.Equal(t, sha256.Sum256(want), cand.InputSHA256)
	assert.Equal(t, "11-30", king.Size)
	assert.Len(t, king.Books[0].Annotation, 600, "300 Cyrillic runes")
	assert.True(t, swap.Books[0].Samizdat)
}

// The canonical side order: side A has the smaller member list, whatever the
// order of the credits.
func TestTheSideOrderIsCanonical(t *testing.T) {
	build := func(first, second int64) *authorentity.MergeCandidate {
		c := newCatalog(t).book(1, "Оно").book(2, "Сияние")
		names := map[int64]*name{1: person("Кинг", "Стивен"), 2: person("Стивен", "Кинг")}
		fps := map[int64]string{1: "fp-b", 2: "fp-a"}
		c.credit(first, first, fps[first], names[first]).credit(second, second, fps[second], names[second])
		p := sealed(t, c, nil)
		cand := candidateOf(p, authorentity.CandidateSwap, 1, 2)
		require.NotNil(t, cand)
		assert.Equal(t, "fp-a", cand.MembersA[0].Fingerprint)
		return cand
	}
	assert.Equal(t, build(1, 2).Digest, build(2, 1).Digest)
}

// The explainable sample: per (kind, script, reason) at most fifty rows,
// those of the lowest member hash, with the sides' Go keys.
func TestTheSampleIsBoundedAndExplainable(t *testing.T) {
	c := newCatalog(t)
	for i := int64(0); i < 60; i++ {
		family, given := fmt.Sprintf("Фамилия%c%c", 'А'+rune(i/26), 'а'+rune(i%26)), "Имя"
		c.book(2*i+1, "А").book(2*i+2, "Б").
			credit(2*i+1, 2*i+1, fmt.Sprintf("fp-%d", i), person(family, given)).
			credit(2*i+2, 2*i+2, fmt.Sprintf("fp-%d-swap", i), person(given, family))
	}
	p := sealed(t, c, nil)
	assert.Equal(t, 60, counted(p, authorentity.CandidateSwap, "Cyrl"))
	rows := p.MergeCandidateSample()
	assert.Len(t, rows, 50)
	for i := 1; i < len(rows); i++ {
		assert.Negative(t, bytes.Compare(rows[i-1].MembersSHA256[:], rows[i].MembersSHA256[:]))
	}
	assert.NotEmpty(t, rows[0].KeysA)
	assert.NotEqual(t, [32]byte{}, rows[0].Digest)
}

// churn is a catalog with a candidate pair, «Стивен Кинг» (big) and «Кинг
// Стивен» (one book), and a bucket item, «Алексей Иванов» on book 620
// against «Алексей Викторович Иванов» of twelve books (600–611).
type churn struct {
	t      *testing.T
	c      *catalog
	fields map[int64]authorentity.CardBookFields
}

func newChurn(t *testing.T, bigBooks int) *churn {
	ch := &churn{t: t, c: newCatalog(t), fields: map[int64]authorentity.CardBookFields{}}
	for i := 1; i <= bigBooks; i++ {
		ch.c.book(int64(i), fmt.Sprintf("Роман %d", i)).credit(int64(i), int64(i), "fp-big", person("Кинг", "Стивен"))
		ch.fields[int64(i)] = authorentity.CardBookFields{Year: "1980", Genres: []string{"horror"}}
	}
	ch.c.book(500, "Рассказы").credit(500, 500, "fp-small", person("Стивен", "Кинг"))
	for i := int64(600); i < 612; i++ {
		ch.c.book(i, fmt.Sprintf("Повесть %d", i)).credit(i, i, "fp-full", person("Иванов", "Алексей", "Викторович"))
	}
	ch.c.book(620, "Психология").credit(620, 620, "fp-bucket", person("Иванов", "Алексей"))
	return ch
}

// credit is the catalog's credit id.
func (ch *churn) credit(id int64) *authorentity.Credit {
	i := slices.IndexFunc(ch.c.in.Credits, func(c authorentity.Credit) bool { return c.ID == id })
	require.GreaterOrEqual(ch.t, i, 0)
	return &ch.c.in.Credits[i]
}

// digests are the swap pair's and the bucket item's candidates.
func (ch *churn) digests() (pair, item *authorentity.MergeCandidate) {
	ch.t.Helper()
	p := sealed(ch.t, ch.c, ch.fields)
	return candidateOf(p, authorentity.CandidateSwap, 1, 500), bookItemOf(p, 620, 600)
}

// change says which halves of a digest a churn case changed.
func change(before, after *authorentity.MergeCandidate) string {
	switch {
	case after == nil:
		return "gone"
	case before.Digest == after.Digest:
		return "equal"
	case before.MembersSHA256 != after.MembersSHA256 && before.InputSHA256 != after.InputSHA256:
		return "members+input"
	case before.MembersSHA256 != after.MembersSHA256:
		return "members"
	default:
		return "input"
	}
}

// The churn rules of §4.2: what keeps a candidate's digest, and which half
// of it a change moves. A case whose candidate disappears is "gone": its
// decision lapses as a changed one does.
func TestTheChurnCases(t *testing.T) {
	type tc struct {
		name   string
		big    int
		change func(ch *churn)
		pair   string
		item   string
	}
	for _, c := range []tc{
		{"1 nothing", 40, func(*churn) {}, "equal", "equal"},
		{"2 a large side gains a book outside the sample, its class kept", 40, func(ch *churn) {
			ch.c.book(41, "Роман 41").credit(41, 41, "fp-big", person("Кинг", "Стивен"))
		}, "equal", "equal"},
		{"3 a small side gains a book", 40, func(ch *churn) {
			ch.c.book(501, "Ещё рассказы").credit(501, 501, "fp-small", person("Стивен", "Кинг"))
		}, "input", "equal"},
		{"4 the size class changes", 10, func(ch *churn) {
			ch.c.book(11, "Роман 11").credit(11, 11, "fp-big", person("Кинг", "Стивен"))
		}, "input", "equal"},
		{"5 a sample book's field is edited", 40, func(ch *churn) {
			ch.fields[3] = authorentity.CardBookFields{Year: "1981", Genres: []string{"horror"}}
		}, "input", "equal"},
		{"5 a sample book's title is edited", 40, func(ch *churn) {
			ch.c.in.Books[3] = authorentity.Book{Title: "Роман три"}
		}, "input", "equal"},
		{"6 a side gains a fingerprint", 40, func(ch *churn) {
			ch.c.book(502, "Повести").credit(502, 502, "fp-small-2", person("СТИВЕН", "Кинг"))
		}, "members+input", "equal"},
		{"7 an extractor bump", 40, func(ch *churn) {
			for i := range ch.c.in.Credits {
				ch.c.in.Credits[i].ExtractorVersion = "v2"
			}
		}, "members", "members"},
		{"8 a normalizer bump with the rendered fields unchanged", 40, func(ch *churn) {
			r := *ch.credit(500).Result
			r.ResultID += 1000
			ch.credit(500).Result = &r
		}, "equal", "equal"},
		{"9 a member's rendered name changes", 40, func(ch *churn) {
			r := *ch.credit(500).Result
			r.DisplayName = "Кинг, Стивен"
			ch.credit(500).Result = &r
		}, "input", "equal"},
		{"11 D3 moves a book into a large side outside its sample", 40, func(ch *churn) {
			ch.c.book(650, "Повесть 600").credit(650, 650, "fp-bucket", person("Иванов", "Алексей"))
		}, "equal", "equal"},
		{"12 D3 moves a book into a side's sample", 40, func(ch *churn) {
			ch.c.book(599, "Повесть 601").credit(599, 599, "fp-bucket", person("Иванов", "Алексей"))
		}, "equal", "input"},
		{"13 a Wikidata merge joins a side with a third", 40, func(ch *churn) {
			ch.c.book(700, "It").credit(700, 700, "fp-nick", shown("Стивен Кинг", nickOnly("Стивен Кинг")))
			ch.c.with(newReference().item("Q39829", []string{"Стивен Кинг"}))
		}, "members+input", "equal"},
		{"14 a manual row fixes a member's credit", 40, func(ch *churn) {
			id := uuid.New()
			ch.c.in.Old = authorentity.NewState()
			ch.c.in.Old.Entities[id] = authorentity.EntityRow{ID: id, Kind: "person", DisplayName: "Стивен Кинг"}
			key := authorentity.CreditKey{CreditID: 2}
			ch.c.in.Old.Credits[key] = authorentity.CreditRow{Key: key, EntityID: &id, Method: authorentity.MethodManual, Rule: "manual"}
		}, "gone", "equal"},
		{"15 the bucket credit is re-selected with another name", 40, func(ch *churn) {
			r := *ch.credit(620).Result
			r.DisplayName = "Алексей ИВАНОВ"
			ch.credit(620).Result = &r
		}, "equal", "input"},
		{"15 the bucket credit is deleted", 40, func(ch *churn) {
			ch.c.in.Credits = slices.DeleteFunc(ch.c.in.Credits, func(c authorentity.Credit) bool { return c.ID == 620 })
		}, "equal", "gone"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ch := newChurn(t, c.big)
			pair, item := ch.digests()
			require.NotNil(t, pair)
			require.NotNil(t, item)
			c.change(ch)
			pair2, item2 := ch.digests()
			assert.Equal(t, c.pair, change(pair, pair2), "the pair")
			assert.Equal(t, c.item, change(item, item2), "the bucket item")
		})
	}
	// Case 10, the ordinal of a member: part of the member key itself.
	a := authorentity.MembersSHA256([]authorentity.FPKey{{Fingerprint: "f", ExtractorVersion: "v1"}}, nil, nil)
	b := authorentity.MembersSHA256([]authorentity.FPKey{{Fingerprint: "f", ExtractorVersion: "v1", Ordinal: 1}}, nil, nil)
	assert.NotEqual(t, a, b)
}

// Every field of the card is declared with the rows it is read from (§4.10):
// a new field without a declared source fails here.
func TestMergeCardFieldsDeclared(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(authorentity.MergeCard{}), reflect.TypeOf(authorentity.MergeCardBook{})} {
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Name() + "." + typ.Field(i).Name
			sources := authorentity.MergeCardSources[name]
			assert.NotEmpty(t, sources, "%s has no declared source", name)
			for _, s := range sources {
				assert.NotEmpty(t, s.Columns, "%s: a source without columns", name)
			}
		}
	}
	assert.Len(t, authorentity.MergeCardSources,
		reflect.TypeOf(authorentity.MergeCard{}).NumField()+reflect.TypeOf(authorentity.MergeCardBook{}).NumField())
}

// The candidates are observation only: the units, assignments and store of a
// pass are those without them.
func TestCandidatesWriteNothing(t *testing.T) {
	c := newChurn(t, 12).c
	p := c.build()
	units := fmt.Sprintf("%+v", p.Units)
	require.NoError(t, p.SealMergeCandidates(nil))
	require.NotEmpty(t, p.MergeCandidates())
	assert.Equal(t, units, fmt.Sprintf("%+v", p.Units))
}

// A bucket book is not asked against a full name standing on the same book,
// nor against one a manual row anchors, nor at all beside more than twenty
// full names.
func TestBucketItemExclusions(t *testing.T) {
	c := newCatalog(t).book(1, "Общага-на-Крови").book(2, "Психология").book(3, "Футбол").book(4, "Сердце пармы").
		credit(10, 1, "fp-viktorovich", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-aleksandrovich", person("Иванов", "Алексей", "Александрович")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).
		// Book 3 carries Викторович beside the short name: two people.
		credit(13, 3, "fp-viktorovich", person("Иванов", "Алексей", "Викторович")).
		credit(14, 4, "fp-aleksandrovich", person("Иванов", "Алексей", "Александрович"))
	manual := uuid.New()
	c.in.Old = authorentity.NewState()
	c.in.Old.Entities[manual] = authorentity.EntityRow{ID: manual, Kind: "person", DisplayName: "Алексей Иванов"}
	key := authorentity.CreditKey{CreditID: 14}
	c.in.Old.Credits[key] = authorentity.CreditRow{Key: key, EntityID: &manual, Method: authorentity.MethodManual, Rule: "manual"}
	p := sealed(t, c, nil)
	assert.Nil(t, bookItemOf(p, 12, 10))
	assert.Nil(t, bookItemOf(p, 12, 11))
	assert.Equal(t, 1, excluded(p, authorentity.CandidateBucket, "Cyrl", authorentity.ExcludedCoCredited))
	assert.Equal(t, 1, excluded(p, authorentity.CandidateBucket, "Cyrl", authorentity.ExcludedManualSide))

	c = newCatalog(t).book(1, "Психология")
	c.credit(1, 1, "fp-short", person("Петров", "Иван"))
	for i := int64(0); i < 21; i++ {
		c.book(10+i, fmt.Sprintf("Книга %d", i)).
			credit(10+i, 10+i, fmt.Sprintf("fp-%d", i), person("Петров", "Иван", fmt.Sprintf("Отчество%c", 'А'+rune(i))))
	}
	p = sealed(t, c, nil)
	assert.Equal(t, 21, excluded(p, authorentity.CandidateBucket, "Cyrl", authorentity.ExcludedPartnerCap))
	assert.Zero(t, counted(p, authorentity.CandidateBucket, "Cyrl"))
}

// Initials stand only for the words they begin, in their places: «Лев Н.»
// is not «Леонид Николаевич»; two names of initials are no initials pair;
// a family skeleton shorter than three letters pairs no translit.
func TestShapesTheRulesLeaveOut(t *testing.T) {
	p := sealed(t, newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").book(4, "Г").book(5, "Д").book(6, "Е").
		credit(10, 1, "fp-lev-n", person("Толстой", "Лев", "Н.")).
		credit(11, 2, "fp-leonid", person("Толстой", "Леонид", "Николаевич")).
		credit(12, 3, "fp-l", person("Толстой", "Л.")).
		credit(13, 4, "fp-ln", person("Толстой", "Л.", "Н.")).
		credit(14, 5, "fp-li", person("Ли", "Брюс")).
		credit(15, 6, "fp-lee", person("Lee", "Bruce").latin()), nil)
	assert.Nil(t, sampleOf(p, authorentity.CandidateInitials, 10, 11))
	assert.Nil(t, sampleOf(p, authorentity.CandidateInitials, 12, 13))
	assert.Nil(t, sampleOf(p, authorentity.CandidateTranslit, 14, 15))
	requireCandidate(t, p, authorentity.CandidateInitials, "Cyrl", 12, 11)
}

// A swap pair of two scripts is counted under "mixed", kept out.
func TestASwapOfTwoScriptsIsMixed(t *testing.T) {
	p := sealed(t, newCatalog(t).book(1, "А").book(2, "Б").
		credit(10, 1, "fp-mixed", mixedScript(person("Лемм", "Stas"))).
		credit(11, 2, "fp-cyrl", person("Stas", "Лемм")), nil)
	requireExcluded(t, p, authorentity.CandidateSwap, authorentity.ExcludedMixedScript, 10, 11)
	assert.Equal(t, 1, excluded(p, authorentity.CandidateSwap, "mixed", authorentity.ExcludedMixedScript))
}

// A card's lists are canonical whatever the order they are read in.
func TestMergeCardOfIsCanonical(t *testing.T) {
	card := authorentity.MergeCardOf([]string{"Стивен Кинг", "Кинг, Стивен", "Стивен Кинг", ""}, []string{"", "Ричард"}, 2,
		[]authorentity.MergeCardBook{authorentity.MergeCardBookOf("Оно", &authorentity.CardBookFields{
			Genres: []string{"thriller", "horror"}, Series: []string{"Б", "А"}}, []string{"Питер Страуб", "Джо Хилл"})})
	assert.Equal(t, []string{"Кинг, Стивен", "Стивен Кинг"}, card.Names)
	assert.Equal(t, []string{"Ричард"}, card.Variants)
	assert.Equal(t, []string{"horror", "thriller"}, card.Books[0].Genres)
	assert.Equal(t, []string{"А", "Б"}, card.Books[0].Series)
	assert.Equal(t, []string{"Джо Хилл", "Питер Страуб"}, card.Books[0].Coauthors)
	assert.Equal(t, "2-3", card.Size)
}

func ptrTo[T any](v T) *T { return &v }

// Review r1, P1: the census never takes the plan with it. Generation that
// panics leaves the plan's units as they are, reports its class and seals
// nothing; a seal that panics is an error, with no candidate kept.
func TestACandidateFailureLeavesThePlanWhole(t *testing.T) {
	c := newChurn(t, 12).c
	units := func(p *authorentity.Plan) string {
		raw, err := json.Marshal(p.Units)
		require.NoError(t, err)
		return string(raw)
	}
	build := func() *authorentity.Plan {
		next := 0
		c.in.NewID = func() uuid.UUID {
			next++
			return uuid.MustParse(fmt.Sprintf("00000000-0000-7000-8000-%012d", next))
		}
		return c.build()
	}
	want := units(build())
	restore := authorentity.PanicOnCandidateGeneration()
	p := build()
	restore()
	assert.Equal(t, want, units(p))
	assert.Equal(t, authorentity.CandidateErrorGenerate, p.Report.Candidates.Error)
	assert.Zero(t, p.Report.Candidates.Candidates)
	assert.Empty(t, p.CardBookIDs())
	require.ErrorIs(t, p.SealMergeCandidates(nil), authorentity.ErrNoCandidates)
	assert.False(t, p.MergeCandidatesSealed())

	p = build()
	restore = authorentity.PanicOnCandidateSeal()
	err := p.SealMergeCandidates(nil)
	restore()
	require.Error(t, err)
	assert.False(t, p.MergeCandidatesSealed())
	assert.Empty(t, p.MergeCandidates())
	assert.Empty(t, p.MergeCandidateSample())
	assert.Equal(t, want, units(p))

	p = build()
	require.NoError(t, p.SealMergeCandidates(nil))
	assert.True(t, p.MergeCandidatesSealed())
}
