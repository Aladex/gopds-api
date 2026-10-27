package authorentity_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateNoD2bGolden rewrites the golden of TestNoD2bWorkChangesNothing. It
// was written by the build before D2b (74bbfbf) and must not be rewritten by
// a build that has it, unless the change is meant to alter a pass without
// D2b work.
var updateNoD2bGolden = flag.Bool("update-no-d2b-golden", false, "rewrite testdata/no_d2b_work.golden.json")

const noD2bGolden = "testdata/no_d2b_work.golden.json"

// noD2bWorkCatalog is a catalog with every rule of the build at work — D1,
// D2, a typo merge, D3 by title, series and co-author, a conflicting and a
// samizdat bucket book, initials, a namesake tag, a suffix, a collective, a
// placeholder, a nickname, Wikidata merges and exclusions, legacy authors —
// and no two keys of one word sequence: nothing for D2b to do.
func noD2bWorkCatalog(t *testing.T) *catalog {
	c := newCatalog(t).
		book(1, "Война и мир").book(2, "Анна Каренина").book(3, "Первая", 5).book(4, "Вторая", 5).
		book(5, "Первая").book(6, "Третья", 5).book(7, "Совместная").book(8, "Ещё одна").book(9, "Чужая").
		book(10, "Мост", 9).book(11, "Мост").book(12, "Мост").book(13, "Мост [СИ]", 9).book(14, "Детство").
		book(15, "Оно").book(16, "It").book(17, "Сияние").book(18, "Mother Night").book(19, "Letters").
		book(20, "Журнал 1").book(21, "Сказки").book(22, "Капитан").book(23, "Пикник").
		book(24, "Хоббит").book(25, "Мост")
	c.credit(1, 1, "fp-tolstoy", person("Толстой", "Лев", "Николаевич")).
		credit(2, 2, "fp-tolstoy-upper", person("ТОЛСТОЙ", "Лев", "Николаевич")).
		credit(3, 3, "fp-right", person("Иванов", "Алексей", "Викторович")).
		credit(4, 4, "fp-typo", person("Иванов", "Алексей", "Викторовыч")).
		credit(5, 5, "fp-short", person("Иванов", "Алексей")).
		credit(6, 6, "fp-short", person("Иванов", "Алексей")).
		credit(7, 7, "fp-right", person("Иванов", "Алексей", "Викторович")).
		credit(8, 7, "fp-co", person("Михайлов", "Сергей", "Петрович")).
		credit(9, 8, "fp-short", person("Иванов", "Алексей")).
		credit(10, 8, "fp-co", person("Михайлов", "Сергей", "Петрович")).
		credit(11, 9, "fp-short", person("Иванов", "Алексей")).
		credit(12, 10, "fp-petrov-s", person("Петров", "Иван", "Сергеевич")).
		credit(13, 11, "fp-petrov-p", person("Петров", "Иван", "Петрович")).
		credit(14, 12, "fp-petrov", person("Петров", "Иван")).
		credit(15, 13, "fp-petrov", person("Петров", "Иван")).
		credit(16, 14, "fp-initials", person("Толстой", "Л.", "Н.")).
		credit(17, 15, "fp-king", person("Кинг", "Стивен")).
		credit(18, 16, "fp-king-en", shown("Stephen King", person("King", "Stephen").latin())).
		credit(19, 17, "fp-king-tagged", person("Кинг", "Стивен").withTag(2)).
		credit(20, 18, "fp-vonnegut-jr", person("Vonnegut", "Kurt").latin().withSuffix("Jr.")).
		credit(21, 19, "fp-vonnegut", person("Vonnegut", "Kurt").latin()).
		collective(22, 20, "fp-nauka", "Журнал Наука").
		credit(23, 21, "fp-unknown", person("неизвестен", "Автор")).
		credit(24, 22, "fp-captain", nickOnly("Капитан")).
		credit(25, 23, "fp-strugatsky", person("Стругацкий", "Аркадий", "Натанович")).
		credit(26, 24, "fp-tolkien", shown("J. R. R. Tolkien", person("Tolkien", "J. R. R.").latin())).
		credit(27, 25, "fp-ivanov-other", person("Иванов", "Алексей", "Александрович"))
	c.in.Legacy = map[int64]int64{1: 100, 2: 100, 3: 200, 4: 201, 5: 200, 17: 300, 18: 301}
	return c
}

// newReportSections are the report sections the build before D2b did not
// have: D2b's own, and the merge candidates' (E4a), which write nothing.
var newReportSections = []string{"d2b", "merge_candidates"}

// noD2bPass is what one pass of the fixture writes and reports. The report
// is kept as JSON without the new sections.
type noD2bPass struct {
	Units   []authorentity.Unit        `json:"units"`
	Credits map[int64]string           `json:"credits"`
	Report  map[string]json.RawMessage `json:"report"`
	State   string                     `json:"state"`
}

func recordNoD2bPass(t *testing.T, c *catalog, s *authorentity.State) noD2bPass {
	t.Helper()
	c.in.Old = s
	p := c.build()
	p.Apply(s)
	out := noD2bPass{Units: p.Units, Credits: map[int64]string{}, State: dump(s, func(uuid.UUID) bool { return true })}
	for _, cr := range c.in.Credits {
		out.Credits[cr.ID] = fmt.Sprintf("%v %s", p.EntityOf(cr.ID), p.RuleOf(cr.ID))
	}
	raw, err := json.Marshal(p.Report)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &out.Report))
	if d2b, ok := out.Report["d2b"]; ok {
		var r struct {
			Sizes []struct {
				Components int `json:"components"`
			} `json:"sizes"`
		}
		require.NoError(t, json.Unmarshal(d2b, &r))
		for _, row := range r.Sizes {
			require.Zero(t, row.Components, "the fixture must give D2b nothing to do")
		}
	}
	for _, section := range newReportSections {
		delete(out.Report, section)
	}
	return out
}

// Review r1, P3: a pass with no D2b work writes the same units, assigns the
// same entities by the same rules, leaves the same store and reports every
// field the build had before D2b exactly as that build did — over a first
// pass, an unchanged second pass, a pass after a merge and a pass with a
// Wikidata reference. Only the new sections are new.
func TestNoD2bWorkChangesNothing(t *testing.T) {
	next := 0
	c := noD2bWorkCatalog(t)
	c.in.NewID = func() uuid.UUID {
		next++
		return uuid.MustParse(fmt.Sprintf("00000000-0000-7000-8000-%012d", next))
	}
	s := authorentity.NewState()
	var passes []noD2bPass
	passes = append(passes, recordNoD2bPass(t, c, s), recordNoD2bPass(t, c, s))
	// The other patronymic is the typo's after all: a merge.
	c.in.Credits[26].Additional = []string{"Викторович"}
	passes = append(passes, recordNoD2bPass(t, c, s))
	c.with(newReference().item("Q39829", []string{"Стивен Кинг", "Stephen King"}).
		item("Q892", []string{"J. R. R. Tolkien"}).item("Q7", []string{"Иван Петров"}).
		item("Q8", []string{"Аркадий Натанович Стругацкий"}, "pseudonym:С. Ярославцев"))
	passes = append(passes, recordNoD2bPass(t, c, s))

	got, err := json.MarshalIndent(passes, "", "  ")
	require.NoError(t, err)
	if *updateNoD2bGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(noD2bGolden), 0o750))
		require.NoError(t, os.WriteFile(noD2bGolden, append(got, '\n'), 0o600))
		return
	}
	want, err := os.ReadFile(noD2bGolden)
	require.NoError(t, err)
	var wantPasses []noD2bPass
	require.NoError(t, json.Unmarshal(want, &wantPasses))
	var gotPasses []noD2bPass
	require.NoError(t, json.Unmarshal(got, &gotPasses))
	assert.Equal(t, wantPasses, gotPasses)
}
