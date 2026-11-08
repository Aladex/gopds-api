package bookvariants

import (
	"crypto/md5" // #nosec G501 -- the key's SQL twin is md5(); not a security use
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	entityA = "01a163f8-dce3-7aad-83e0-45f1c60bb306"
	entityB = "01a163f8-dc5b-7263-81a1-751980737eee"
)

// day is one calendar day.
const day = 24 * time.Hour

// book is a groupable input of one entity, in Russian, titled «Сага», added
// on the given day; options set the rest.
func book(id int64, added time.Time, opts ...func(*Input)) Input {
	in := Input{
		BookID: id, Lang: "ru", Title: "Сага", EntityIDs: []string{entityA}, EntityNames: []string{"Джон Толкин"},
		Series: map[int64]int64{}, RegisterDate: added,
	}
	for _, o := range opts {
		o(&in)
	}
	return in
}

func doc(id, version string) func(*Input) {
	return func(in *Input) { in.DocumentID, in.DocVersion = id, version }
}

func docDate(s string) func(*Input) { return func(in *Input) { in.DocDate = s } }

func numbered(series, no int64) func(*Input) {
	return func(in *Input) { in.Series[series] = no }
}

func complete(in *Input) { in.Cover, in.Annotation, in.Publisher, in.Year = true, true, true, true }

func baseOf(t *testing.T, books ...Input) string {
	t.Helper()
	base := BaseKey(&books[0])
	if base == "" {
		t.Fatalf("fixture book %d is a card of its own", books[0].BookID)
	}
	for i := range books[1:] {
		if b := &books[i+1]; BaseKey(b) != base {
			t.Fatalf("fixture book %d has another base", b.BookID)
		}
	}
	return base
}

func memberIDs(g *Group) []int64 {
	ids := make([]int64, len(g.Members))
	for i, m := range g.Members {
		ids[i] = m.BookID
	}
	return ids
}

func TestBaseKeyOwnCard(t *testing.T) {
	now := time.Now()
	cases := map[string]Input{
		"junk title":  book(1, now, func(in *Input) { in.Title = "unknown" }),
		"no entities": book(2, now, func(in *Input) { in.EntityIDs, in.EntityNames = nil, nil }),
		"unlinked":    book(3, now, func(in *Input) { in.Unlinked = true }),
		"placeholder": book(4, now, func(in *Input) {
			in.EntityIDs, in.EntityNames = []string{entityA, entityB}, []string{"Джон Толкин", "Unknown"}
		}),
	}
	for name, in := range cases {
		if got := BaseKey(&in); got != "" {
			t.Errorf("%s: BaseKey = %q, want own card", name, got)
		}
	}

	two := book(5, now, func(in *Input) { in.EntityIDs = []string{entityA, entityB} })
	swapped := book(6, now, func(in *Input) { in.EntityIDs = []string{entityB, entityA, entityB} })
	if BaseKey(&two) == "" || BaseKey(&two) != BaseKey(&swapped) {
		t.Errorf("the same entities in another order give %q and %q", BaseKey(&two), BaseKey(&swapped))
	}
	other, plain := book(7, now, func(in *Input) { in.Lang = "en" }), book(8, now)
	if BaseKey(&other) == BaseKey(&plain) {
		t.Error("another language shares the base key")
	}
	tagged := book(9, now, func(in *Input) { in.Title = "Сага [litres]" }) //nolint:misspell // a catalog tag
	if BaseKey(&tagged) != BaseKey(&plain) {
		t.Error("a stripped tag changes the base key")
	}
}

func TestBuildSeriesConflict(t *testing.T) {
	now := time.Now()
	books := []Input{
		book(1, now, numbered(7, 1)),
		book(2, now, numbered(7, 2)),
		book(3, now, numbered(7, 2), numbered(9, 4)),
		book(4, now),
	}
	base := baseOf(t, books...)
	groups := Build(base, books)
	got := map[uuid.UUID][]int64{}
	for i := range groups {
		g := &groups[i]
		ids := memberIDs(g)
		slices.Sort(ids)
		got[g.Key] = ids
	}
	want := map[uuid.UUID][]int64{
		GroupKey(base, "7:1"):   {1},
		GroupKey(base, "7:2"):   {2, 3},
		GroupKey(base, "own:4"): {4},
	}
	if len(got) != len(want) {
		t.Fatalf("Build gave %d groups %v, want %v", len(got), got, want)
	}
	for k, ids := range want {
		if !slices.Equal(got[k], ids) {
			t.Errorf("group %s = %v, want %v", k, got[k], ids)
		}
	}
}

// splitsOf is each card's sorted members by card key.
func splitsOf(groups []Group) map[uuid.UUID][]int64 {
	got := map[uuid.UUID][]int64{}
	for i := range groups {
		ids := memberIDs(&groups[i])
		slices.Sort(ids)
		got[groups[i].Key] = ids
	}
	return got
}

// assertNoSeriesConflictInACard fails when two files of one card carry two
// numbers of one series.
func assertNoSeriesConflictInACard(t *testing.T, books []Input, groups []Group) {
	t.Helper()
	byID := map[int64]*Input{}
	for i := range books {
		byID[books[i].BookID] = &books[i]
	}
	for i := range groups {
		for _, a := range groups[i].Members {
			for _, b := range groups[i].Members {
				for series, no := range byID[a.BookID].Series {
					if other := byID[b.BookID].Series[series]; no > 0 && other > 0 && other != no {
						t.Errorf("books %d and %d share a card with numbers %d and %d of series %d",
							a.BookID, b.BookID, no, other, series)
					}
				}
			}
		}
	}
}

// Every shared series a book is numbered in decides, not the lowest only
// (review 1-2, blocker 4): A and B are one volume of series 7 and two of
// series 9.
func TestBuildSeriesConflictInEverySeries(t *testing.T) {
	now := time.Now()
	books := []Input{
		book(1, now, numbered(7, 1), numbered(9, 1)), // A
		book(2, now, numbered(7, 1), numbered(9, 2)), // B
		book(3, now, numbered(7, 2)),                 // C
		// Numbered 1 in series 7 only: A's volume or B's, a guess, so alone.
		book(4, now, numbered(7, 1)),
		// Numbered 1 in series 9 only: A's volume and no one else's.
		book(5, now, numbered(9, 1)),
		// Numbered in no conflicting series: alone, as before.
		book(6, now),
	}
	base := baseOf(t, books...)
	groups := Build(base, books)
	want := map[uuid.UUID][]int64{
		GroupKey(base, "7:1,9:1"): {1, 5},
		GroupKey(base, "7:1,9:2"): {2},
		GroupKey(base, "7:2"):     {3},
		GroupKey(base, "own:4"):   {4},
		GroupKey(base, "own:6"):   {6},
	}
	got := splitsOf(groups)
	if len(got) != len(want) {
		t.Fatalf("Build gave %v, want %v", got, want)
	}
	for k, ids := range want {
		if !slices.Equal(got[k], ids) {
			t.Errorf("card %s = %v, want %v (all: %v)", k, got[k], ids, got)
		}
	}
	assertNoSeriesConflictInACard(t, books, groups)
}

// A chain of books each agreeing with the next can join two that disagree:
// P and R are two numbers of series 20 and meet only through Q and S. Such a
// chain is not one card; each book stays with its exact numbers.
func TestBuildSeriesConflictChain(t *testing.T) {
	now := time.Now()
	books := []Input{
		book(1, now, numbered(7, 1), numbered(20, 1)),                  // P
		book(2, now, numbered(7, 1), numbered(11, 1)),                  // Q
		book(3, now, numbered(11, 1), numbered(13, 1)),                 // S
		book(4, now, numbered(13, 1), numbered(20, 2)),                 // R
		book(5, now, numbered(7, 2), numbered(11, 2), numbered(13, 2)), // makes 7, 11, 13 conflict
		book(6, now, numbered(7, 1), numbered(20, 1)),                  // P's twin
	}
	groups := Build(baseOf(t, books...), books)
	got := splitsOf(groups)
	if len(got) != 5 {
		t.Errorf("Build gave %v, want five cards, P with its twin", got)
	}
	found := false
	for _, ids := range got {
		found = found || slices.Equal(ids, []int64{1, 6})
	}
	if !found {
		t.Errorf("P and its twin are apart: %v", got)
	}
	assertNoSeriesConflictInACard(t, books, groups)
}

func TestBuildSeriesNoConflict(t *testing.T) {
	now := time.Now()
	books := []Input{book(1, now, numbered(7, 3)), book(2, now), book(3, now, numbered(11, 0))}
	base := baseOf(t, books...)
	groups := Build(base, books)
	if len(groups) != 1 || len(groups[0].Members) != 3 || groups[0].Key != GroupKey(base, "") {
		t.Fatalf("Build gave %+v, want one group of 3 under the plain key", groups)
	}
}

func TestBuildRevisionsKind(t *testing.T) {
	now := time.Now()
	var books []Input
	versions := []string{"1.2", "1.20", "2.1", "1.9"}
	for i := 0; i < 22; i++ {
		v := versions[i%len(versions)]
		if i >= len(versions) {
			v = "1." + string(rune('0'+i%10))
		}
		books = append(books, book(int64(100+i), now, doc("DOC-1", v)))
	}
	g := Build(baseOf(t, books...), books)
	if len(g) != 1 {
		t.Fatalf("Build gave %d groups, want 1", len(g))
	}
	if g[0].Kind != KindRevisions || g[0].Count != 22 {
		t.Errorf("kind %q count %d, want revisions 22", g[0].Kind, g[0].Count)
	}
	top := g[0].Members[0]
	if top.BookID != 102 || !top.Latest || top.VariantRank != 1 {
		t.Errorf("default is %+v, want book 102 (ред. 2.1), latest, rank 1", top)
	}
	latest := 0
	for _, m := range g[0].Members {
		if m.Latest {
			latest++
		}
		if m.Lineage != "doc-1" {
			t.Errorf("member %d lineage %q, want doc-1", m.BookID, m.Lineage)
		}
	}
	if latest != 1 {
		t.Errorf("%d latest members, want 1", latest)
	}
}

func TestBuildVariantsKind(t *testing.T) {
	now := time.Now()
	books := []Input{
		book(1, now, doc("A", "1.0")), book(2, now, doc("A", "1.1")),
		book(3, now, doc("B", "1.0")), book(4, now, doc("B", "2.0")),
		book(5, now, doc("C", "1.0")), book(6, now, doc("D", "1.0")),
		book(7, now, doc("E", "1.0")), book(8, now), // no document: a lineage of its own
	}
	g := Build(baseOf(t, books...), books)
	if len(g) != 1 || g[0].Kind != KindVariants || g[0].Count != 6 {
		t.Fatalf("Build gave %+v, want one variants group counting 6", g)
	}
	var latest []int64
	for i, m := range g[0].Members {
		if m.VariantRank != i+1 {
			t.Errorf("member %d at %d has rank %d", m.BookID, i, m.VariantRank)
		}
		if m.Latest {
			latest = append(latest, m.BookID)
		}
	}
	slices.Sort(latest)
	if !slices.Equal(latest, []int64{2, 4, 5, 6, 7, 8}) {
		t.Errorf("latest members %v, want [2 4 5 6 7 8]", latest)
	}
	// Older revisions come after every latest member.
	for _, m := range g[0].Members[:6] {
		if !m.Latest {
			t.Errorf("an older revision %d ranks among the latest", m.BookID)
		}
	}
	if g[0].Members[0].Lineage == "" || g[0].Members[0].Tag != "" {
		t.Errorf("default member %+v", g[0].Members[0])
	}
}

func TestBuildFoldsRevisionsUnderTheirLatest(t *testing.T) {
	now := time.Now()
	books := []Input{
		book(1, now, doc("A", "1.0")), book(2, now, doc("A", "2.0"), complete),
		book(3, now, doc("B", "1.0")), book(4, now, doc("B", "1.5")),
	}
	g := Build(baseOf(t, books...), books)
	if got := memberIDs(&g[0]); !slices.Equal(got, []int64{2, 4, 1, 3}) {
		t.Errorf("order %v, want [2 4 1 3]: latest by rank, then each lineage's older revisions", got)
	}
}

func TestDefaultVariantPrefersComplete(t *testing.T) {
	now := time.Now()
	newer := now.Format("02.01.2006")
	older := now.Add(-400 * day).Format("02.01.2006")

	books := []Input{
		book(1, now, doc("A", "1.0"), docDate(newer)),
		book(2, now, doc("B", "1.0"), docDate(older), complete),
	}
	if got := Build(baseOf(t, books...), books)[0].Members[0].BookID; got != 2 {
		t.Errorf("default %d, want the complete file 2 over a newer docdate", got)
	}

	ties := []struct {
		name string
		want int64
		a, b Input
	}{
		{"docdate", 1, book(1, now, doc("A", "1.0"), docDate(newer)), book(2, now, doc("B", "1.0"), docDate(older))},
		{"version", 2, book(1, now, doc("A", "1.0")), book(2, now, doc("B", "1.1"))},
		{"added", 1, book(1, now, doc("A", "1.0")), book(2, now.Add(-day), doc("B", "1.0"))},
		{"id", 2, book(1, now, doc("A", "1.0")), book(2, now, doc("B", "1.0"))},
	}
	for _, tc := range ties {
		books := []Input{tc.a, tc.b}
		if got := Build(baseOf(t, books...), books)[0].Members[0].BookID; got != tc.want {
			t.Errorf("tie on %s: default %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestMarkOnlyForLaterDay(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * day)
	cases := []struct {
		name  string
		books []Input
		want  Mark
	}{
		{"same day", []Input{book(1, old, doc("A", "1.0")), book(2, old, doc("B", "1.0"))}, MarkNone},
		{"new variant", []Input{book(1, old, doc("A", "1.0")), book(2, now, doc("B", "1.0"))}, MarkNewVariant},
		{"new revision", []Input{book(1, old, doc("A", "1.0")), book(2, now, doc("A", "1.1"))}, MarkNewRevision},
		{"newest is older", []Input{book(1, now, doc("A", "1.0")), book(2, old, doc("B", "1.0"))}, MarkNone},
		{"single", []Input{book(1, now)}, MarkNone},
	}
	for _, tc := range cases {
		g := Build(baseOf(t, tc.books...), tc.books)[0]
		if g.Mark != tc.want || g.NewestBookID != 2 && len(tc.books) > 1 {
			t.Errorf("%s: mark %q newest %d, want %q newest 2", tc.name, g.Mark, g.NewestBookID, tc.want)
		}
	}
}

func TestBuildSingleAndSolo(t *testing.T) {
	now := time.Now()
	in := book(1, now, func(in *Input) { in.Title = "Сага [litres]" }) //nolint:misspell // a catalog tag
	g := Build(BaseKey(&in), []Input{in})
	if len(g) != 1 || g[0].Kind != KindSingle || g[0].Count != 1 || g[0].Members[0].Tag != "litres" { //nolint:misspell // a catalog tag
		t.Errorf("single group %+v", g)
	}
	solo := book(2, now, func(in *Input) { in.Title = "unknown" })
	g = Build("", []Input{solo})
	if len(g) != 1 || g[0].Key != SoloKey(2) || g[0].Kind != KindSingle || g[0].Members[0].Lineage != "book:2" {
		t.Errorf("solo group %+v", g)
	}
}

func TestCompareDocVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10", "1.9", 1}, {"2.1", "1.20", 1}, {"", "0.1", -1}, {"1.0", "1", 0},
		{"v2", "2", 0}, {"1.01", "1.1", 0}, {"", "", 0}, {"1.2.3", "1.2", 1},
	}
	for _, tc := range cases {
		if got := CompareDocVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareDocVersion(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := CompareDocVersion(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareDocVersion(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestParseDocDate(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, layout := range []string{"2006-01-02", "02.01.2006"} {
		got, ok := ParseDocDate(now.Format(layout))
		if !ok || !got.Equal(today) {
			t.Errorf("ParseDocDate(%q) = %v, %v; want %v", now.Format(layout), got, ok, today)
		}
	}
	got, ok := ParseDocDate(now.Format("2006"))
	if want := time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("year only: %v, %v; want %v", got, ok, want)
	}
	for _, garbage := range []string{"", "вчера", "32.13.20", "abc-de-fg"} {
		if _, ok := ParseDocDate(garbage); ok {
			t.Errorf("ParseDocDate(%q) parsed", garbage)
		}
	}
}

func TestKeysAreStable(t *testing.T) {
	sum := md5.Sum([]byte("book:42")) // #nosec G401 -- see the import
	if want := uuid.UUID(sum); SoloKey(42) != want {
		t.Errorf("SoloKey(42) = %s, want %s", SoloKey(42), want)
	}
	sum = md5.Sum([]byte("b\x1e7:1")) // #nosec G401 -- see the import
	if want := uuid.UUID(sum); GroupKey("b", "7:1") != want {
		t.Errorf("GroupKey(b, 7:1) = %s, want %s", GroupKey("b", "7:1"), want)
	}
	if GroupKey("b", "") == GroupKey("b", "7:1") || GroupKey("b", "7:1") == GroupKey("b", "7:2") {
		t.Error("splits share a key")
	}
}

// cardsOf groups books by base key as a partition rebuild does and builds
// every base: the cards the books make.
func cardsOf(books []Input) []Group {
	bases := map[string][]Input{}
	var order []string
	for i := range books {
		base := BaseKey(&books[i])
		if _, seen := bases[base]; !seen {
			order = append(order, base)
		}
		bases[base] = append(bases[base], books[i])
	}
	var out []Group
	for _, base := range order {
		out = append(out, Build(base, bases[base])...)
	}
	return out
}

// Books 179345 and 179359 are the novel, 460623 the short story of one title
// (review 1-2, blocker 2).
func TestBuildKeepsAShortStoryOffTheNovel(t *testing.T) {
	now := time.Now()
	title := func(s string) func(*Input) { return func(in *Input) { in.Title, in.Lang = s, "en" } }
	books := []Input{
		book(179345, now, title("The Deep Range"), doc("A", "1.0")),
		book(179359, now, title("The Deep Range"), doc("B", "1.0")),
		book(460623, now, title("The Deep Range [short story]"), doc("C", "1.0")),
	}
	got := map[int64][]int64{}
	for _, g := range cardsOf(books) {
		got[g.Members[0].BookID] = memberIDs(&g)
	}
	if len(got) != 2 || len(got[460623]) != 1 {
		t.Fatalf("cards = %v, want the two novels together and the story alone", got)
	}
}
