package authorentity_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// typoWordsSHA256 pins the table of authorentity.TypoRuleVersion.
const typoWordsSHA256 = "be15fe13811f9fc0dc9d7a0154fab80cd514d78f9f9eb1d21d765ef748bc511e"

// The table in the repository is the table of the rule version: a table
// changed in place is a rule changed without a version.
func TestTheWordTableIsItsRuleVersion(t *testing.T) {
	sum := sha256.Sum256([]byte(authorentity.TypoWordsFile))
	assert.Equal(t, typoWordsSHA256, hex.EncodeToString(sum[:]), "a new table is a new rule version")

	words := authorentity.ParseTypoWords(authorentity.TypoWordsFile)
	require.NotEmpty(t, words)
	rendered, err := authorentity.RenderTypoWords(authorentity.TypoRuleVersion, words)
	require.NoError(t, err)
	assert.Equal(t, authorentity.TypoWordsFile, string(rendered), "the file is what the generator writes for this version")
	for word, sides := range words {
		require.GreaterOrEqual(t, sides, authorentity.TypoWordFloor, word)
	}
}

// The rule reads the shipped table with no catalog at all.
func TestTheShippedTableKeepsTheRealNames(t *testing.T) {
	const no, yes = 0, 1
	for _, c := range []struct {
		x, y string
		want int
	}{
		{"мария", "марина", no},
		{"юрий", "юлий", no},
		{"прохоров", "прозоров", no},
		{"лукяненко", "лукьяненко", yes},
		{"чуковсктй", "чуковский", yes},
	} {
		assert.Equal(t, c.want, authorentity.TypoVerdict(c.x, c.y), "%s, %s", c.x, c.y)
	}
}

// A word the table does not list is rare, however often it is seen.
func TestAWordOutsideTheTableIsRare(t *testing.T) {
	const no, yes = 0, 1
	defer authorentity.FreezeTypoWords(map[string]int{"гейман": 40, "прозоров": 60, "прохоров": 30})()
	assert.Equal(t, no, authorentity.TypoVerdict("прозоров", "прохоров"), "both listed as common")
	assert.Equal(t, yes, authorentity.TypoVerdict("тейман", "гейман"), "one side against many: a slip")
	assert.Equal(t, no, authorentity.TypoVerdict("фотев", "ботев"), "two words of one side each: no slip")
	assert.Equal(t, yes, authorentity.TypoVerdict("юлий", "юрий"), "neither listed: the model tells them apart")
}

// Authors nobody asked about cross a bound of the rule by the live count. The
// table does not move, so the candidate, its digest, the decision and the join
// all stay.
func TestUnrelatedGrowthKeepsATypoDecision(t *testing.T) {
	const no = 0
	given := []string{"Анатолий", "Борис", "Виктор", "Георгий", "Дмитрий", "Евгений", "Константин", "Леонид", "Михаил", "Николай"}
	for _, c := range []struct {
		name         string
		rare, common string
		nRare, nComm int
	}{
		{"common words", "Прохоров", "Прозоров", 8, 9},
		{"first letter", "Теймер", "Геймер", 2, 10},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, m := strings.ToLower(c.rare), strings.ToLower(c.common)
			require.NotEqual(t, no, authorentity.TypoVerdictOfSides(r, m, c.nRare, c.nComm), "the pair passes before the growth")
			require.Equal(t, no, authorentity.TypoVerdictOfSides(r, m, c.nRare+1, c.nComm), "live counts would refuse it after")
			defer authorentity.FreezeTypoWords(map[string]int{r: c.nRare, m: c.nComm})()

			ch := twoNames(t, person(c.rare, "Иван"), person(c.common, "Иван"))
			id := int64(100)
			add := func(family, name string) {
				id++
				ch.c.book(id, fmt.Sprintf("Книга %d", id)).credit(id, id, fmt.Sprintf("fp-%d", id), person(family, name))
			}
			for i := range c.nRare - 1 {
				add(c.rare, given[i])
			}
			for i := range c.nComm - 1 {
				add(c.common, given[len(given)-1-i])
			}
			w := newWorld(t, ch)
			w.pass()
			before := candidateOf(w.level0, authorentity.CandidateTypo, 1, 2)
			require.NotNil(t, before)
			decision := w.decide(authorentity.CandidateTypo, 1, 2)
			same(t, w.pass(), 1, 2)
			require.Equal(t, applied(decision), w.state(decision))

			add(c.rare, "Роман")
			p := w.pass()
			after := candidateOf(w.level0, authorentity.CandidateTypo, 1, 2)
			require.NotNil(t, after, "the candidate stays")
			assert.Equal(t, before.Digest, after.Digest)
			assert.Equal(t, applied(decision), w.state(decision))
			same(t, p, 1, 2)
		})
	}
}

// The generator counts sides, not credits, and a word in either place.
func TestTheWordCountsAreOfSides(t *testing.T) {
	ch := twoNames(t, person("Толстой", "Иван"), person("Иван", "Роман"))
	ch.c.book(3, "Книга В").credit(3, 3, "fp-a", person("Толстой", "Иван"))
	counts := sealed(t, ch.c, nil).TypoWordCounts()
	assert.Equal(t, 1, counts["толстой"], "two credits of one side")
	assert.Equal(t, 1, counts["иван"], "the larger of its family and its given sides")
	assert.Equal(t, 1, counts["роман"])
}

// The table lists what the floor lets through, in word order, and refuses a
// word it could not read back.
func TestRenderTypoWords(t *testing.T) {
	table, err := authorentity.RenderTypoWords(7, map[string]int{"юрий": 9, "анна": 2, "редкий": 1})
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(table)), "\n")
	assert.Contains(t, lines[0], "typo rule 7")
	assert.Equal(t, []string{"анна\t2", "юрий\t9"}, lines[1:])
	assert.Equal(t, map[string]int{"юрий": 9, "анна": 2}, authorentity.ParseTypoWords(string(table)))

	for _, bad := range []string{"a\tb", "a\nb", "#a"} {
		_, err = authorentity.RenderTypoWords(1, map[string]int{bad: 5})
		assert.Error(t, err, "%q", bad)
	}
}
