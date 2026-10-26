package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// d2bSize is the D2b report row of components of the given size label.
func d2bSize(t *testing.T, p *authorentity.Plan, label string) authorentity.D2bSize {
	t.Helper()
	for _, s := range p.Report.D2b.Sizes {
		if s.Label == label {
			return s
		}
	}
	t.Fatalf("d2b report: no size %q in %+v", label, p.Report.D2b.Sizes)
	return authorentity.D2bSize{}
}

// D2b (E4·D2b, §2.0): the same words in the same order, the source's field
// boundary aside, are one name. The real field-split forms of the catalog.
func TestD2bJoinsFieldSplitNames(t *testing.T) {
	t.Run("a given name of two words against a middle name", func(t *testing.T) {
		// «Рэй Дуглас / Брэдбери» and «Рэй / Дуглас / Брэдбери».
		p := newCatalog(t).book(1, "451° по Фаренгейту").book(2, "Марсианские хроники").
			credit(10, 1, "fp-two-words", person("Брэдбери", "Рэй Дуглас")).
			credit(11, 2, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).build()
		same(t, p, 10, 11)
		for _, id := range []int64{10, 11} {
			if got := p.RuleOf(id); got != authorentity.RuleD2b {
				t.Errorf("credit %d rule: want %s, got %s", id, authorentity.RuleD2b, got)
			}
		}
		if s := d2bSize(t, p, "2"); s.Components != 1 || s.Groups != 2 || s.PairEdges != 1 || s.Unions != 1 {
			t.Errorf("d2b size 2: got %+v", s)
		}
		if got := p.Report.D2b.EdgesByClass[authorentity.D2bClassPlain]; got != 1 {
			t.Errorf("plain edges: want 1, got %v", p.Report.D2b.EdgesByClass)
		}
	})
	t.Run("a hyphenated family against a family and a middle name", func(t *testing.T) {
		// «Конан-Дойль / Артур» and «Дойль / Артур / Конан»: given, additional
		// and family render «Артур Конан Дойль» both; the hyphen is a
		// separator, as in D2.
		p := newCatalog(t).book(1, "Собака Баскервилей").book(2, "Этюд в багровых тонах").
			credit(10, 1, "fp-hyphen", person("Конан-Дойль", "Артур")).
			credit(11, 2, "fp-split", person("Дойль", "Артур", "Конан")).build()
		same(t, p, 10, 11)
	})
	t.Run("initials of the same letters", func(t *testing.T) {
		// «Дж. Р. Р. / Толкин» and «Дж. / Р. Р. / Толкин»: the same written
		// letters, not an initial matched to a word.
		p := newCatalog(t).book(1, "Хоббит").book(2, "Сильмариллион").
			credit(10, 1, "fp-given", person("Толкин", "Дж. Р. Р.")).
			credit(11, 2, "fp-split", person("Толкин", "Дж.", "Р. Р.")).build()
		same(t, p, 10, 11)
		if got := p.Report.D2b.EdgesByClass[authorentity.D2bClassInitials]; got != 1 {
			t.Errorf("initials edges: want 1, got %v", p.Report.D2b.EdgesByClass)
		}
	})
	t.Run("an empty family", func(t *testing.T) {
		p := newCatalog(t).book(1, "Вино из одуванчиков").book(2, "Марсианские хроники").
			credit(10, 1, "fp-given-only", person("", "Рэй Дуглас Брэдбери")).
			credit(11, 2, "fp-full", person("Брэдбери", "Рэй", "Дуглас")).build()
		same(t, p, 10, 11)
		if got := p.Report.D2b.EdgesByClass[authorentity.D2bClassEmptyPart]; got != 1 {
			t.Errorf("empty-part edges: want 1, got %v", p.Report.D2b.EdgesByClass)
		}
	})
	t.Run("Latin", func(t *testing.T) {
		p := newCatalog(t).book(1, "Fahrenheit 451").book(2, "The Martian Chronicles").
			credit(10, 1, "fp-two-words", person("Bradbury", "Ray Douglas").latin()).
			credit(11, 2, "fp-middle", person("Bradbury", "Ray", "Douglas").latin()).build()
		same(t, p, 10, 11)
	})
}

// D2b keeps apart what is not the same words in the same order, and what D2
// keeps apart: a namesake tag, another script, another suffix, a collective.
func TestD2bKeepsOtherNamesApart(t *testing.T) {
	t.Run("another word order is a swap", func(t *testing.T) {
		// «Кинг Стивен» against «Стивен Кинг»: the model's swap kind.
		p := newCatalog(t).book(1, "Оно").book(2, "Сияние").
			credit(10, 1, "fp-a", person("Кинг", "Стивен")).
			credit(11, 2, "fp-b", person("Стивен", "Кинг")).build()
		apart(t, p, 10, 11)
	})
	t.Run("a three-word swap", func(t *testing.T) {
		p := newCatalog(t).book(1, "Оно").book(2, "Сияние").
			credit(10, 1, "fp-a", person("Кинг", "Стивен Эдвин")).
			credit(11, 2, "fp-b", person("Кинг", "Эдвин", "Стивен")).build()
		apart(t, p, 10, 11)
	})
	t.Run("an initial against a word", func(t *testing.T) {
		p := newCatalog(t).book(1, "Война и мир").book(2, "Анна Каренина").
			credit(10, 1, "fp-a", person("Толстой", "Л.")).
			credit(11, 2, "fp-b", person("Толстой", "Лев")).build()
		apart(t, p, 10, 11)
	})
	t.Run("a single word", func(t *testing.T) {
		p := newCatalog(t).book(1, "Стихи").book(2, "Поэмы").
			credit(10, 1, "fp-family", person("Гомер", "")).
			credit(11, 2, "fp-given", person("", "Гомер")).build()
		apart(t, p, 10, 11)
	})
	t.Run("a namesake tag", func(t *testing.T) {
		p := newCatalog(t).book(1, "451° по Фаренгейту").book(2, "Марсианские хроники").
			credit(10, 1, "fp-tagged", person("Брэдбери", "Рэй Дуглас").withTag(2)).
			credit(11, 2, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).build()
		apart(t, p, 10, 11)
	})
	t.Run("another script", func(t *testing.T) {
		mixed := person("Bradbury", "Ray Douglas")
		mixed.script = "mixed"
		p := newCatalog(t).book(1, "Fahrenheit 451").book(2, "The Martian Chronicles").
			credit(10, 1, "fp-mixed", mixed).
			credit(11, 2, "fp-latin", person("Bradbury", "Ray", "Douglas").latin()).build()
		apart(t, p, 10, 11)
	})
	t.Run("another suffix", func(t *testing.T) {
		p := newCatalog(t).book(1, "Mother Night").book(2, "Letters").
			credit(10, 1, "fp-jr", person("Vonnegut", "Kurt Edward").latin().withSuffix("Jr.")).
			credit(11, 2, "fp-plain", person("Vonnegut", "Kurt", "Edward").latin()).build()
		apart(t, p, 10, 11)
	})
	t.Run("a collective against a person", func(t *testing.T) {
		c := newCatalog(t).book(1, "Пикник на обочине").book(2, "Трудно быть богом")
		c.collective(10, 1, "fp-collective", "Братья Стругацкие").
			credit(11, 2, "fp-person", person("Стругацкие", "Братья"))
		p := c.build()
		apart(t, p, 10, 11)
	})
	t.Run("nothing to join reports empty sizes", func(t *testing.T) {
		p := newCatalog(t).book(1, "Оно").
			credit(10, 1, "fp-a", person("Кинг", "Стивен")).build()
		for _, s := range p.Report.D2b.Sizes {
			if s != (authorentity.D2bSize{Label: s.Label}) {
				t.Errorf("d2b size %s: want zero, got %+v", s.Label, s)
			}
		}
	})
}

// D2bComponentOfThree: three groups of one word sequence, no shared book,
// are one component: three pair edges, two unions, one entity.
func TestD2bComponentOfThree(t *testing.T) {
	p := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").
		credit(10, 1, "fp-given", person("Брэдбери", "Рэй Дуглас")).
		credit(11, 2, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).
		credit(12, 3, "fp-family", person("Дуглас Брэдбери", "Рэй")).build()
	same(t, p, 10, 11)
	same(t, p, 10, 12)
	if s := d2bSize(t, p, "3"); s.Components != 1 || s.Groups != 3 || s.PairEdges != 3 || s.Unions != 2 || s.Refused != 0 {
		t.Errorf("d2b size 3: got %+v", s)
	}
	if s := d2bSize(t, p, "2"); s.Components != 0 {
		t.Errorf("d2b size 2: got %+v", s)
	}
}

// Components of four and more groups are counted in the last row.
func TestD2bCountsLargeComponents(t *testing.T) {
	p := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").book(4, "Г").
		credit(10, 1, "fp-given", person("Брэдбери", "Рэй Дуглас")).
		credit(11, 2, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).
		credit(12, 3, "fp-family", person("Дуглас Брэдбери", "Рэй")).
		credit(13, 4, "fp-given-only", person("", "Рэй Дуглас Брэдбери")).build()
	same(t, p, 10, 13)
	if s := d2bSize(t, p, "4+"); s.Components != 1 || s.Groups != 4 || s.PairEdges != 6 || s.Unions != 3 {
		t.Errorf("d2b size 4+: got %+v", s)
	}
}

// D2bRefusesCoCredited: two credits on one book are two people the catalog
// itself names; D2b never joins them.
func TestD2bRefusesCoCredited(t *testing.T) {
	p := newCatalog(t).book(1, "Сборник").book(2, "Марсианские хроники").
		credit(10, 1, "fp-two-words", person("Брэдбери", "Рэй Дуглас")).
		credit(11, 1, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).
		credit(12, 2, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).build()
	apart(t, p, 10, 11)
	if got := p.Report.D2b.Refused[authorentity.D2bRefusedCoCredited]; got != 1 {
		t.Errorf("refused co-credited: want 1, got %v", p.Report.D2b.Refused)
	}
	s := d2bSize(t, p, "2")
	if s.Components != 1 || s.PairEdges != 1 || s.Unions != 0 || s.Refused != 1 {
		t.Errorf("d2b size 2: got %+v", s)
	}
	if got := p.RuleOf(10); got == authorentity.RuleD2b {
		t.Errorf("a refused component took the rule %s", got)
	}
	want := map[uuid.UUID]bool{p.EntityOf(10): true, p.EntityOf(11): true}
	if r := p.Report.D2b.RefusedEntities; len(r) != 1 || len(r[0]) != 2 || !want[r[0][0]] || !want[r[0][1]] {
		t.Errorf("refused entities: want %v, got %v", want, r)
	}
}

// D2bCoCreditedComponentStaysWhole: three groups of one sequence, two of
// them on one book, the third on none of theirs: no union at all, whatever
// pair is co-credited (the groups are ordered by key, so each case puts the
// co-credited pair at another place in the order).
func TestD2bCoCreditedComponentStaysWhole(t *testing.T) {
	names := []*name{
		person("Брэдбери", "Рэй Дуглас"),
		person("Брэдбери", "Рэй", "Дуглас"),
		person("Дуглас Брэдбери", "Рэй"),
	}
	fps := []string{"fp-given", "fp-middle", "fp-family"}
	for _, pair := range [][2]int{{0, 1}, {0, 2}, {1, 2}} {
		c := newCatalog(t).book(1, "Сборник").book(2, "А").book(3, "Б").book(4, "В")
		for i, n := range names {
			c.credit(int64(20+i), int64(2+i), fps[i], n)
		}
		c.credit(30, 1, fps[pair[0]], names[pair[0]]).credit(31, 1, fps[pair[1]], names[pair[1]])
		p := c.build()
		for i := range names {
			for j := i + 1; j < len(names); j++ {
				apart(t, p, int64(20+i), int64(20+j))
			}
		}
		s := d2bSize(t, p, "3")
		if s.Components != 1 || s.PairEdges != 3 || s.Unions != 0 || s.Refused != 1 {
			t.Errorf("co-credited %v: d2b size 3: got %+v", pair, s)
		}
	}
}

// TypoSeesD2bComponent: the typo merge's co-credit check reads the D2b
// unions. Петровыч's D2b partner («Алексей / Петровыч Иванов») shares a book
// with Петрович, so the typo edge Петрович–Петровыч would put two people of
// one book in one entity: refused.
func TestTypoSeesD2bComponent(t *testing.T) {
	p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).book(3, "Сборник").book(4, "Третья").
		credit(10, 1, "fp-right", person("Иванов", "Алексей", "Петрович")).
		credit(11, 2, "fp-typo", person("Иванов", "Алексей", "Петровыч")).
		// The partner sorts after Петровыч, so it is not the component's root.
		credit(12, 4, "fp-partner", person("Петровыч Иванов", "Алексей")).
		credit(13, 3, "fp-right", person("Иванов", "Алексей", "Петрович")).
		credit(14, 3, "fp-partner", person("Петровыч Иванов", "Алексей")).build()
	same(t, p, 11, 12)
	apart(t, p, 10, 11)
	apart(t, p, 13, 14)
	if p.Report.TypoMerges != 0 {
		t.Errorf("typo merges: want 0, got %d", p.Report.TypoMerges)
	}
}

// Without the co-credited partner the same typo edge merges: the refusal
// above is the partner's book, not the D2b union itself.
func TestTypoMergesAcrossAD2bComponent(t *testing.T) {
	p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).book(4, "Третья").
		credit(10, 1, "fp-right", person("Иванов", "Алексей", "Петрович")).
		credit(11, 2, "fp-typo", person("Иванов", "Алексей", "Петровыч")).
		credit(12, 4, "fp-partner", person("Петровыч Иванов", "Алексей")).build()
	same(t, p, 10, 11)
	same(t, p, 11, 12)
	if got := p.RuleOf(12); got != authorentity.RuleTypo {
		t.Errorf("rule: want %s, got %s", authorentity.RuleTypo, got)
	}
}

// The rollout: two stored entities a D2b union now joins. The side of more
// books keeps its ID — here the one that is not the component's root — the
// other is merged into it and logged deterministic; a second pass changes
// nothing.
func TestD2bMergeKeepsTheLargerIDAndASecondPassChangesNothing(t *testing.T) {
	c := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").book(4, "Г").
		credit(10, 1, "fp-two-words", person("Брэдбери", "Рэй Дуглас")).
		credit(11, 2, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).
		credit(12, 3, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).
		credit(13, 4, "fp-middle", person("Брэдбери", "Рэй", "Дуглас"))
	// The first pass sees «Рэй Дуглас / Брэдбери» under a namesake tag: two
	// entities, as before D2b.
	c.in.Credits[0].Tag = 2
	first, s := stored(t, c)
	small, large := first.EntityOf(10), first.EntityOf(11)
	if small == large {
		t.Fatal("the first pass already joined the two names")
	}
	c.in.Credits[0].Tag = 0
	second := rebuild(t, c, s)
	for _, id := range []int64{10, 11, 12, 13} {
		if second.EntityOf(id) != large {
			t.Errorf("credit %d: want the larger entity %v, got %v", id, large, second.EntityOf(id))
		}
	}
	if row := s.Entities[small]; row.MergedInto == nil || *row.MergedInto != large {
		t.Errorf("absorbed entity: want merged_into %v, got %+v", large, row)
	}
	if len(s.Merges) != 1 || s.Merges[0].From != small || s.Merges[0].To != large ||
		s.Merges[0].Method != authorentity.MethodDeterministic {
		t.Errorf("merge log: got %+v", s.Merges)
	}
	if got := s.Fingerprints[authorentity.FPKey{Fingerprint: "fp-two-words", ExtractorVersion: "v1"}]; got.Rule != authorentity.RuleD2b {
		t.Errorf("fingerprint row: want rule %s, got %+v", authorentity.RuleD2b, got)
	}
	third := rebuild(t, c, s)
	if n := third.Changes(); n != 0 {
		t.Errorf("a pass after the D2b merge: want 0 changes, got %d", n)
	}
	if third.EntityOf(10) != large {
		t.Errorf("the D2b entity changed its ID on a rebuild")
	}
}

// noBuckets is the bucket report of a pass that has no short name's bucket
// to look at.
func noBuckets() authorentity.BucketReport {
	sizes := []authorentity.SizeBucket{{Label: "1"}, {Label: "2-5"}, {Label: "6-20"}, {Label: "21-100"}, {Label: "100+"}}
	return authorentity.BucketReport{MovedBy: map[string]int{}, Sizes: sizes}
}

// A D2b component whose root is a short key holds a full name: it is the
// full name split by the source, no short name's bucket — no bucket entity,
// and no bucket D3 looks at or counts.
func TestAD2bComponentIsNoBucketEntity(t *testing.T) {
	p := newCatalog(t).book(1, "Вино из одуванчиков").book(2, "Марсианские хроники").
		credit(10, 1, "fp-two-words", person("Брэдбери", "Рэй Дуглас")).
		credit(11, 2, "fp-middle", person("Брэдбери", "Рэй", "Дуглас")).build()
	same(t, p, 10, 11)
	assert.Equal(t, noBuckets(), p.Report.Buckets)
}

// Review r1, P1: a short key D2b joined is a field-split full name, not a
// bucket, so D3 never moves its books — not even to a fuller name of its
// family and given name that title, series or co-author evidence points to.
// Without its D2b partner the same key is a bucket and its book moves (the
// control), so the evidence of each fixture is real.
func TestD3NeverSplitsAD2bComponent(t *testing.T) {
	for _, tc := range []struct {
		evidence string
		catalog  func(t *testing.T) *catalog
	}{
		{authorentity.EvidenceTitle, func(t *testing.T) *catalog {
			return newCatalog(t).book(1, "Вино из одуванчиков").book(2, "Марсианские хроники").
				book(3, "Вино из одуванчиков")
		}},
		{authorentity.EvidenceSeries, func(t *testing.T) *catalog {
			return newCatalog(t).book(1, "Первая", 7).book(2, "Марсианские хроники").book(3, "Вторая", 7)
		}},
		{authorentity.EvidenceCoauthor, func(t *testing.T) *catalog {
			c := newCatalog(t).book(1, "Первая").book(2, "Марсианские хроники").book(3, "Вторая")
			return c.credit(20, 1, "fp-co", person("Петров", "Иван", "Сергеевич")).
				credit(21, 3, "fp-co", person("Петров", "Иван", "Сергеевич"))
		}},
	} {
		t.Run(tc.evidence, func(t *testing.T) {
			// A «Рэй Дуглас / Брэдбери» (short), B «Рэй / Дуглас / Брэдбери»,
			// C «Брэдбери / Рэй Дуглас / Иванович», a fuller name of A's base
			// that the evidence links to A's book 1.
			c := tc.catalog(t).
				credit(10, 1, "fp-a", person("Брэдбери", "Рэй Дуглас")).
				credit(11, 2, "fp-b", person("Брэдбери", "Рэй", "Дуглас")).
				credit(12, 3, "fp-c", person("Брэдбери", "Рэй Дуглас", "Иванович"))
			p, s := stored(t, c)
			same(t, p, 10, 11)
			apart(t, p, 10, 12)
			for _, id := range []int64{10, 11} {
				if got := p.RuleOf(id); got != authorentity.RuleD2b {
					t.Errorf("credit %d rule: want %s, got %s", id, authorentity.RuleD2b, got)
				}
			}
			if len(s.Credits) != 0 {
				t.Errorf("credit overrides: want none, got %+v", s.Credits)
			}
			assert.Equal(t, noBuckets(), p.Report.Buckets)

			control := tc.catalog(t).
				credit(10, 1, "fp-a", person("Брэдбери", "Рэй Дуглас")).
				credit(12, 3, "fp-c", person("Брэдбери", "Рэй Дуглас", "Иванович")).build()
			same(t, control, 10, 12)
			if got, want := control.RuleOf(10), "D3:"+tc.evidence; got != want {
				t.Errorf("control rule: want %s, got %s", want, got)
			}
		})
	}
}

// A short name's key beside a full one keeps a D2b component out of the
// Wikidata merges (rule 5), also when it is not the component's root.
func TestAD2bComponentWithABucketKeyStaysOutOfWikidata(t *testing.T) {
	c := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").book(4, "Fahrenheit 451").
		credit(1, 1, "full", person("Брэдбери", "Рэй", "Дуглас")).
		// The root is «Рэй / Дуглас / Брэдбери»; this short key sorts after it.
		credit(2, 2, "short", person("Дуглас Брэдбери", "Рэй")).
		credit(3, 3, "fuller", person("Дуглас Брэдбери", "Рэй", "Иванович")).
		credit(4, 4, "en", person("Bradbury", "Ray", "Douglas").latin())
	p := c.with(newReference().item("Q1", []string{"Рэй Дуглас Брэдбери", "Ray Douglas Bradbury"})).build()
	same(t, p, 1, 2)
	apart(t, p, 1, 4)
	if got := p.Report.Wikidata.Excluded[authorentity.WikidataExcludedShortName]; got != 1 {
		t.Errorf("excluded short name: want 1, got %v", p.Report.Wikidata.Excluded)
	}
}
