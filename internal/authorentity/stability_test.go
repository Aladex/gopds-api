package authorentity_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
)

// stored builds, applies the plan to a fresh store and returns both.
func stored(t *testing.T, c *catalog) (*authorentity.Plan, *authorentity.State) {
	t.Helper()
	p := c.build()
	s := authorentity.NewState()
	p.Apply(s)
	return p, s
}

// rebuild builds again over what the last pass stored and applies it.
func rebuild(t *testing.T, c *catalog, s *authorentity.State) *authorentity.Plan {
	t.Helper()
	c.in.Old = s
	p := c.build()
	p.Apply(s)
	return p
}

func twoTolstoys(t *testing.T) *catalog {
	return newCatalog(t).book(1, "Война и мир").book(2, "Анна Каренина").book(3, "Мост").book(4, "Река").
		credit(10, 1, "fp-tolstoy", person("Толстой", "Лев", "Николаевич")).
		credit(11, 2, "fp-tolstoy-upper", person("ТОЛСТОЙ", "Лев", "Николаевич")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).
		credit(13, 4, "fp-other", person("Петров", "Иван", "Сергеевич"))
}

// A second pass over the same data changes nothing and keeps every ID.
func TestASecondPassChangesNothing(t *testing.T) {
	c := twoTolstoys(t)
	first, s := stored(t, c)
	if first.Changes() == 0 {
		t.Fatal("the first pass wrote nothing")
	}
	second := rebuild(t, c, s)
	if n := second.Changes(); n != 0 {
		t.Fatalf("second pass: want 0 changes, got %d", n)
	}
	for _, id := range []int64{10, 11, 12, 13} {
		if first.EntityOf(id) != second.EntityOf(id) {
			t.Errorf("credit %d changed entity on a rebuild", id)
		}
	}
}

// A group that grows keeps the ID of the entity it shares the books with.
func TestARebuildInheritsTheIDByTheSharedBooks(t *testing.T) {
	c := twoTolstoys(t)
	first, s := stored(t, c)
	c.book(5, "Детство").credit(14, 5, "fp-tolstoy-comma", person("Толстой,", "Лев", "Николаевич"))
	second := rebuild(t, c, s)
	if second.EntityOf(14) != first.EntityOf(10) {
		t.Errorf("the grown entity got a new ID")
	}
	if second.Report.IDs.Inherited != 3 || second.Report.IDs.New != 0 {
		t.Errorf("ids: got %+v", second.Report.IDs)
	}
}

// A split: the larger piece keeps the ID, the smaller one gets a new one.
func TestASplitLeavesTheIDWithTheLargerPiece(t *testing.T) {
	c := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").
		credit(10, 1, "fp-a", person("Толстой", "Лев", "Николаевич")).
		credit(11, 2, "fp-a", person("Толстой", "Лев", "Николаевич")).
		credit(12, 3, "fp-b", person("ТОЛСТОЙ", "Лев", "Николаевич"))
	first, s := stored(t, c)
	before := first.EntityOf(10)
	// The result of fp-b now names another patronymic: the entity splits.
	c.in.Credits[2].Additional = []string{"Ильич"}
	c.in.Credits[2].DisplayName = "Лев Ильич ТОЛСТОЙ"
	second := rebuild(t, c, s)
	if second.EntityOf(10) != before || second.EntityOf(11) != before {
		t.Errorf("the larger piece lost the ID")
	}
	if got := second.EntityOf(12); got == before || got == uuid.Nil {
		t.Errorf("the smaller piece: want a new ID, got %v", got)
	}
}

// A merge: the entity with more books survives, the other one points at it
// with merged_into and the merge is logged.
func TestAMergeKeepsTheLargerIDAndMarksTheOther(t *testing.T) {
	c := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").
		credit(10, 1, "fp-a", person("Толстой", "Лев", "Николаевич")).
		credit(11, 2, "fp-a", person("Толстой", "Лев", "Николаевич")).
		credit(12, 3, "fp-b", person("Толстой", "Лев", "Ильич"))
	first, s := stored(t, c)
	survivor, absorbed := first.EntityOf(10), first.EntityOf(12)
	c.in.Credits[2].Additional = []string{"Николаевич"}
	second := rebuild(t, c, s)
	if second.EntityOf(12) != survivor {
		t.Fatalf("the merged books did not go to the larger entity")
	}
	row := s.Entities[absorbed]
	if row.MergedInto == nil || *row.MergedInto != survivor {
		t.Errorf("absorbed entity: want merged_into %v, got %+v", survivor, row)
	}
	if len(s.Merges) != 1 || s.Merges[0].From != absorbed || s.Merges[0].To != survivor {
		t.Errorf("merge log: got %+v", s.Merges)
	}
	if n := rebuild(t, c, s).Changes(); n != 0 {
		t.Errorf("a pass after the merge: want 0 changes, got %d", n)
	}
}

// A bucket whose every book moved by evidence disappears into the entity
// that took them.
func TestAnEmptiedBucketMergesIntoTheEntityThatTookItsBooks(t *testing.T) {
	c := newCatalog(t).book(1, "Мост").book(2, "Река").
		credit(10, 1, "fp-short", person("Иванов", "Алексей")).
		credit(11, 2, "fp-full", person("Иванов", "Алексей", "Викторович"))
	first, s := stored(t, c)
	bucket, full := first.EntityOf(10), first.EntityOf(11)
	c.book(3, "Мост").credit(12, 3, "fp-full", person("Иванов", "Алексей", "Викторович"))
	second := rebuild(t, c, s)
	if second.EntityOf(10) != full {
		t.Fatalf("the bucket book did not move")
	}
	if row := s.Entities[bucket]; row.MergedInto == nil || *row.MergedInto != full {
		t.Errorf("the emptied bucket: want merged_into the full name, got %+v", row)
	}
	if second.Report.Buckets.Emptied != 1 {
		t.Errorf("emptied buckets: want 1, got %d", second.Report.Buckets.Emptied)
	}
}

// Manual rows are never touched: a credit an administrator moved stays where
// it was put, its entity is never merged away, and the rebuild writes nothing
// over it.
func TestManualRowsSurviveTheRebuild(t *testing.T) {
	c := twoTolstoys(t)
	_, s := stored(t, c)
	manual := uuid.Must(uuid.NewV7())
	s.Entities[manual] = authorentity.EntityRow{ID: manual, Kind: "person", DisplayName: "Лев Толстой (2)",
		SortName: "Толстой Лев (2)", SearchKey: "лев толстой 2", NamePinned: true}
	key := authorentity.CreditKey{CreditID: 11}
	s.Credits[key] = authorentity.CreditRow{Key: key, EntityID: &manual, Method: authorentity.MethodManual, Rule: "manual"}
	p := rebuild(t, c, s)
	if p.EntityOf(11) != manual {
		t.Fatalf("the manually moved credit left its entity")
	}
	if row := s.Credits[key]; row.Method != authorentity.MethodManual || *row.EntityID != manual {
		t.Errorf("the manual row changed: %+v", row)
	}
	if row := s.Entities[manual]; row.MergedInto != nil || row.DisplayName != "Лев Толстой (2)" {
		t.Errorf("the manual entity changed: %+v", row)
	}
	if n := rebuild(t, c, s).Changes(); n != 0 {
		t.Errorf("a pass after the manual move: want 0 changes, got %d", n)
	}
}

// Review r1, finding 1: a co-author added to a book is no member of the
// entity already on it. Petrov alone on book 1; then Ivanov joins the same
// book. Petrov keeps his ID; Ivanov gets a new one, never Petrov's.
func TestACoauthorAddedToABookDoesNotTakeTheEntityOnIt(t *testing.T) {
	c := newCatalog(t).book(1, "Мост").
		credit(10, 1, "fp-petrov", person("Петров", "Иван", "Сергеевич"))
	first, s := stored(t, c)
	petrov := first.EntityOf(10)
	c.credit(11, 1, "fp-ivanov", person("Иванов", "Алексей", "Викторович"))
	second := rebuild(t, c, s)
	if second.EntityOf(10) != petrov {
		t.Errorf("Petrov lost his ID to the co-author")
	}
	if second.EntityOf(11) == petrov {
		t.Errorf("the co-author took Petrov's ID")
	}
}

// E2 §0: an entity whose every credit now gets no entity — a placeholder
// with a genre the earlier rules missed — is retired: no successor, its
// fingerprint rows go (the read model then drops it and its names); its row
// stays (IDs are never reused). A second pass writes nothing.
func TestARetiredEntityLosesItsRows(t *testing.T) {
	c := newCatalog(t).book(1, "Мост").book(2, "Река").
		credit(10, 1, "fp-real", person("Петров", "Иван", "Сергеевич")).
		credit(11, 2, "fp-label", person("Кулинария", "Повар"))
	first, s := stored(t, c)
	retired, kept := first.EntityOf(11), first.EntityOf(10)
	// The same credit now reads as a placeholder with a label.
	c.in.Credits[1].Given, c.in.Credits[1].DisplayName = "Автор неизвестен", "Автор неизвестен - Кулинария"
	second := rebuild(t, c, s)
	if second.EntityOf(11) != uuid.Nil {
		t.Fatalf("the placeholder kept an entity")
	}
	if second.EntityOf(10) != kept {
		t.Errorf("an unrelated entity changed")
	}
	if _, ok := s.Entities[retired]; !ok {
		t.Errorf("the retired entity's row was removed")
	}
	for _, row := range s.Fingerprints {
		if row.EntityID == retired {
			t.Errorf("the retired entity kept a fingerprint row")
		}
	}
	if second.Report.IDs.Orphaned != 1 {
		t.Errorf("ids: got %+v", second.Report.IDs)
	}
	if n := rebuild(t, c, s).Changes(); n != 0 {
		t.Errorf("a pass after the retirement: want 0 changes, got %d", n)
	}
}

// Review E2 r1, finding 1: the inheritance rule of design §3 counts a
// group's actual books. A bucket of three books and a full name of one;
// then a series joins the full name's book and two bucket books, which D3
// moves. The full-name group now shares two books with the old bucket and
// one with the old full name: it inherits the bucket's ID, the one-book
// remainder gets a new one, and the old full name goes into it.
func TestAMovedBookCountsForTheGroupItMovesTo(t *testing.T) {
	c := newCatalog(t).book(1, "Одна").book(2, "Две").book(3, "Три").book(4, "Своя").
		credit(10, 1, "fp-short", person("Иванов", "Алексей")).
		credit(11, 2, "fp-short", person("Иванов", "Алексей")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).
		credit(13, 4, "fp-full", person("Иванов", "Алексей", "Викторович"))
	first, s := stored(t, c)
	bucket, full := first.EntityOf(10), first.EntityOf(13)
	c.in.Books[4] = authorentity.Book{Title: "Своя", Series: []int64{9}}
	c.in.Books[1] = authorentity.Book{Title: "Одна", Series: []int64{9}}
	c.in.Books[2] = authorentity.Book{Title: "Две", Series: []int64{9}}
	second := rebuild(t, c, s)
	if second.EntityOf(10) != second.EntityOf(13) || second.EntityOf(11) != second.EntityOf(13) {
		t.Fatalf("D3 did not move the two series books")
	}
	if second.EntityOf(13) != bucket {
		t.Errorf("the full-name group: want the bucket's ID %v, got %v (full name was %v)", bucket, second.EntityOf(13), full)
	}
	if got := second.EntityOf(12); got == bucket || got == full {
		t.Errorf("the one-book remainder kept an old ID %v", got)
	}
	if row := s.Entities[full]; row.MergedInto == nil || *row.MergedInto != bucket {
		t.Errorf("the old full name: want merged into the bucket's ID, got %+v", row)
	}
}

// applyAllBut writes a plan into the store except the component holding a
// unit refused picks: the database refused that component. It returns it.
func applyAllBut(p *authorentity.Plan, s *authorentity.State, refused func(*authorentity.Unit) bool) []authorentity.Unit {
	var out []authorentity.Unit
	for _, comp := range p.Components() {
		if slices.ContainsFunc(comp, func(u authorentity.Unit) bool { return refused(&u) }) {
			out = comp
			continue
		}
		for i := range comp {
			comp[i].Apply(s)
		}
	}
	return out
}

// dump is the stored rows of the entities keep picks, one line each, sorted.
func dump(s *authorentity.State, keep func(uuid.UUID) bool) string {
	var lines []string
	for id, e := range s.Entities {
		if keep(id) {
			lines = append(lines, fmt.Sprintf("entity %v %s merged=%v", id, e.DisplayName, ptr(e.MergedInto)))
		}
	}
	for k, a := range s.Fingerprints {
		if keep(a.EntityID) {
			lines = append(lines, fmt.Sprintf("fp %v %v %s", k, a.EntityID, a.Rule))
		}
	}
	for k, a := range s.Credits {
		if a.EntityID == nil || keep(*a.EntityID) {
			lines = append(lines, fmt.Sprintf("credit %v %v %s", k, ptr(a.EntityID), a.Rule))
		}
	}
	for _, m := range s.Merges {
		if keep(m.From) || keep(m.To) {
			lines = append(lines, fmt.Sprintf("merge %v %v", m.From, m.To))
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

func ptr(id *uuid.UUID) string {
	if id == nil {
		return "-"
	}
	return id.String()
}

func writesFingerprint(fp string) func(*authorentity.Unit) bool {
	return func(u *authorentity.Unit) bool {
		return slices.ContainsFunc(u.Fingerprints, func(r authorentity.AssignmentRow) bool { return r.Key.Fingerprint == fp })
	}
}

func absorbs(id uuid.UUID) func(*authorentity.Unit) bool {
	return func(u *authorentity.Unit) bool {
		return slices.ContainsFunc(u.Absorb, func(m authorentity.MergeRow) bool { return m.From == id })
	}
}

// Review E2 r3: a split is one component. The r1 bucket case — three
// short-name books, one full name, a series moves two bucket books to it.
// When the database refuses the remainder's unit, the full group's unit
// (which takes the bucket's ID and absorbs the old full name) is refused
// with it: the store stays exactly as it was, so the remainder book is never
// linked to the full group, and an unrelated new author still lands. The
// next pass ends where the whole plan ends.
func TestARefusedSplitLeavesTheStoreAsItWas(t *testing.T) {
	c := newCatalog(t).book(1, "Одна").book(2, "Две").book(3, "Три").book(4, "Своя").
		credit(10, 1, "fp-short", person("Иванов", "Алексей")).
		credit(11, 2, "fp-short", person("Иванов", "Алексей")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).
		credit(13, 4, "fp-full", person("Иванов", "Алексей", "Викторович"))
	first, s := stored(t, c)
	bucket, full := first.EntityOf(10), first.EntityOf(13)
	before := dump(s, func(uuid.UUID) bool { return true })
	for _, b := range []int64{4, 1, 2} {
		c.in.Books[b] = authorentity.Book{Title: c.in.Books[b].Title, Series: []int64{9}}
	}
	c.book(6, "Чужая").credit(15, 6, "fp-new", person("Петров", "Иван", "Сергеевич"))

	c.in.Old = s
	refused := applyAllBut(c.build(), s, writesFingerprint("fp-short"))
	if !slices.ContainsFunc(refused, func(u authorentity.Unit) bool { return absorbs(full)(&u) }) {
		t.Fatalf("the full group's unit was not refused with the remainder's: %d units", len(refused))
	}
	newcomer := func(id uuid.UUID) bool { return id != bucket && id != full }
	if got := dump(s, func(id uuid.UUID) bool { return !newcomer(id) }); got != before {
		t.Errorf("the refused split changed the store:\nbefore\n%s\nafter\n%s", before, got)
	}
	if len(s.Entities) != 3 {
		t.Errorf("the unrelated author did not land: %d entities", len(s.Entities))
	}

	next := rebuild(t, c, s)
	if next.EntityOf(13) != bucket || next.EntityOf(10) != bucket || next.EntityOf(11) != bucket {
		t.Errorf("the full group: want the bucket's ID")
	}
	if got := next.EntityOf(12); got == bucket || got == full || got == uuid.Nil {
		t.Errorf("the remainder: want a new ID, got %v", got)
	}
	if row := s.Entities[full]; row.MergedInto == nil || *row.MergedInto != bucket {
		t.Errorf("the old full name: want merged into the bucket's ID, got %+v", row)
	}
	if next.Report.IDs.Merged != 1 || next.Report.IDs.Orphaned != 0 {
		t.Errorf("ids: got %+v", next.Report.IDs)
	}
}

// Review E2 r2/r3: a split and a merge at once is one component. X is a
// three-book bucket, Y a five-book full name, Z a four-book entity whose
// result now reads as the short name; a series moves two X books to Y. The
// whole plan keeps Y's ID for the full group, gives the bucket Z's and
// merges X into Y. When the database refuses Y's unit, the bucket's unit
// (which moves X's fingerprint to Z) is refused with it; nothing of X, Y or
// Z changes, and the next pass merges X into Y as the whole plan does.
func TestARefusedSplitAndMergeLeavesTheStoreAsItWas(t *testing.T) {
	c := newCatalog(t)
	var id int64 = 100
	add := func(fp string, title string, n *name) int64 {
		id++
		c.book(id, title).credit(id, id, fp, n)
		return id
	}
	var xs, ys, zs []int64
	for i := range 3 {
		xs = append(xs, add("fp-x", fmt.Sprintf("X%d", i), person("Иванов", "Алексей")))
	}
	for i := range 5 {
		ys = append(ys, add("fp-y", fmt.Sprintf("Y%d", i), person("Иванов", "Алексей", "Викторович")))
	}
	for i := range 4 {
		zs = append(zs, add("fp-z", fmt.Sprintf("Z%d", i), person("Петров", "Пётр", "Ильич")))
	}
	first, s := stored(t, c)
	idX, idY, idZ := first.EntityOf(xs[0]), first.EntityOf(ys[0]), first.EntityOf(zs[0])
	before := dump(s, func(uuid.UUID) bool { return true })

	z := c.results["fp-z"]
	z.Family, z.Given, z.Additional, z.DisplayName, z.SortName = "Иванов", "Алексей", nil, "Алексей Иванов", "Алексей Иванов"
	for _, b := range []int64{ys[0], xs[0], xs[1]} {
		c.in.Books[b] = authorentity.Book{Title: c.in.Books[b].Title, Series: []int64{9}}
	}
	c.in.Old = s
	whole := c.build()
	if whole.Report.IDs.Merged != 1 {
		t.Fatalf("the whole plan: want X merged, got %+v", whole.Report.IDs)
	}
	refused := applyAllBut(whole, s, absorbs(idX))
	if !slices.ContainsFunc(refused, func(u authorentity.Unit) bool { return writesFingerprint("fp-x")(&u) }) {
		t.Fatalf("the bucket's unit was not refused with Y's: %d units", len(refused))
	}
	if got := dump(s, func(uuid.UUID) bool { return true }); got != before {
		t.Errorf("the refused component changed the store:\nbefore\n%s\nafter\n%s", before, got)
	}

	next := rebuild(t, c, s)
	if next.EntityOf(ys[0]) != idY || next.EntityOf(xs[0]) != idY || next.EntityOf(xs[2]) != idZ {
		t.Errorf("IDs: Y group %v (want %v), bucket %v (want %v)", next.EntityOf(ys[0]), idY, next.EntityOf(xs[2]), idZ)
	}
	if row := s.Entities[idX]; row.MergedInto == nil || *row.MergedInto != idY {
		t.Errorf("X: want merged into Y, got %+v", row)
	}
	if next.Report.IDs.Merged != 1 || next.Report.IDs.Orphaned != 0 {
		t.Errorf("ids: got %+v", next.Report.IDs)
	}
}

// Unrelated entities are components of their own: a refusal of one never
// holds the others back.
func TestUnrelatedEntitiesAreComponentsOfTheirOwn(t *testing.T) {
	p := twoTolstoys(t).build()
	if n, units := len(p.Components()), len(p.Units); n != units || n != 3 {
		t.Errorf("components: want one per unit (3), got %d of %d units", n, units)
	}
}

// A split by a new result is one component even with no merge and no
// moved book: E holds two fingerprints of one name, one book and two; the
// two-book fingerprint's result now reads as another name, so that group
// keeps E's ID and E is renamed, while the one-book fingerprint goes to a
// new entity. They are linked only through the stored owner of the moved
// fingerprint row. Refusing the new entity's unit refuses E's rename with
// it — never Иванов's book shown under an entity renamed to Петров.
func TestASplitByANewResultIsOneComponent(t *testing.T) {
	c := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").
		credit(10, 1, "fp-a", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-b", person("ИВАНОВ", "Алексей", "Викторович")).
		credit(12, 3, "fp-b", person("ИВАНОВ", "Алексей", "Викторович"))
	first, s := stored(t, c)
	e := first.EntityOf(10)
	if first.EntityOf(11) != e {
		t.Fatalf("the two fingerprints are not one entity")
	}
	before := dump(s, func(uuid.UUID) bool { return true })
	b := c.results["fp-b"]
	b.Family, b.Given, b.Additional, b.DisplayName, b.SortName = "Петров", "Иван", []string{"Сергеевич"},
		"Иван Сергеевич Петров", "Иван Сергеевич Петров"
	c.in.Old = s
	whole := c.build()
	if whole.EntityOf(11) != e || whole.EntityOf(10) == e {
		t.Fatalf("the whole plan: want the two-book piece to keep E")
	}
	refused := applyAllBut(whole, s, writesFingerprint("fp-a"))
	if !slices.ContainsFunc(refused, func(u authorentity.Unit) bool { return u.EntityID == e }) {
		t.Fatalf("E's rename was not refused with the new entity's unit: %d units", len(refused))
	}
	if got := dump(s, func(uuid.UUID) bool { return true }); got != before {
		t.Errorf("the refused split changed the store:\nbefore\n%s\nafter\n%s", before, got)
	}
	next := rebuild(t, c, s)
	if next.EntityOf(11) != e || next.EntityOf(10) == e || next.EntityOf(10) == uuid.Nil {
		t.Errorf("the next pass: want the two-book piece on E, the other on a new entity")
	}
}

// The cleanup goes by old entity: two entities retired in one pass (their
// credits now read as placeholders) are two components, so a refusal of
// one never holds the other back.
func TestEachRetiredEntityIsAComponentOfItsOwn(t *testing.T) {
	c := newCatalog(t).book(1, "А").book(2, "Б").
		credit(10, 1, "fp-a", person("Кулинария", "Повар")).
		credit(11, 2, "fp-b", person("Юмор", "Шутник"))
	_, s := stored(t, c)
	for _, fp := range []string{"fp-a", "fp-b"} {
		r := c.results[fp]
		r.Given, r.DisplayName = "Автор неизвестен", "Автор неизвестен - "+r.Family
	}
	p := rebuild(t, c, s)
	if p.Report.IDs.Orphaned != 2 {
		t.Fatalf("want two retired entities, got %+v", p.Report.IDs)
	}
	if n := len(p.Components()); n != 2 {
		t.Errorf("components: want one per retired entity (2), got %d", n)
	}
}

// In a first pass nothing is stored, yet a moved book still ties two units:
// the full name's unit overrides the credit, the bucket's writes the
// fingerprint row the credit would otherwise follow. They are one component
// — refusing one alone would leave the moved book on the bucket and the
// next pass to decide the bucket's ID on a tie.
func TestAMovedBookTiesItsUnitsInAFirstPass(t *testing.T) {
	p := newCatalog(t).book(1, "Своя", 9).book(2, "Две", 9).book(3, "Три").
		credit(10, 1, "fp-full", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-short", person("Иванов", "Алексей")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).build()
	same(t, p, 10, 11)
	apart(t, p, 11, 12)
	overrides := func(u authorentity.Unit) bool {
		return slices.ContainsFunc(u.Credits, func(r authorentity.CreditRow) bool { return r.Key.CreditID == 11 })
	}
	for _, comp := range p.Components() {
		if slices.ContainsFunc(comp, overrides) &&
			!slices.ContainsFunc(comp, func(u authorentity.Unit) bool { return writesFingerprint("fp-short")(&u) }) {
			t.Errorf("the moved credit's override and the bucket's fingerprint row are in different components")
		}
	}
}

// Review E2 r4: a credit override bridges two units through its stored
// owner alone. A holds five full-name books, B four of another patronymic,
// and a series moved one short-name book onto A (an override) while another
// stays in the bucket. Then A's result reads as another full person (the
// group keeps A's ID and A is renamed) and the moved book's series now
// points at B, so B's unit rewrites the override. The rename and the
// override touch no common row: only the override's stored owner, A, ties
// them. Refusing B's unit refuses A's rename with it — never the moved book
// left on an A renamed to Петров.
func TestACreditOverrideTiesItsOldOwnersUnit(t *testing.T) {
	c := newCatalog(t)
	var id int64 = 100
	add := func(fp, title string, n *name, series ...int64) int64 {
		id++
		c.book(id, title, series...).credit(id, id, fp, n)
		return id
	}
	var as, bs []int64
	for i := range 5 {
		as = append(as, add("fp-a", fmt.Sprintf("A%d", i), person("Иванов", "Алексей", "Викторович"), 7))
	}
	for i := range 4 {
		bs = append(bs, add("fp-b", fmt.Sprintf("B%d", i), person("Иванов", "Алексей", "Петрович"), 8))
	}
	moved := add("fp-short", "Короткая", person("Иванов", "Алексей"), 7)
	rest := add("fp-short", "Остаток", person("Иванов", "Алексей"))
	first, s := stored(t, c)
	idA, idB := first.EntityOf(as[0]), first.EntityOf(bs[0])
	if first.EntityOf(moved) != idA || first.EntityOf(rest) == idA || first.EntityOf(rest) == idB {
		t.Fatalf("the first pass: want the series book on A and the other in the bucket")
	}
	before := dump(s, func(uuid.UUID) bool { return true })

	a := c.results["fp-a"]
	a.Family, a.Given, a.Additional, a.DisplayName, a.SortName = "Петров", "Иван", []string{"Сергеевич"},
		"Иван Сергеевич Петров", "Иван Сергеевич Петров"
	c.in.Books[moved] = authorentity.Book{Title: "Короткая", Series: []int64{8}}
	c.in.Old = s
	whole := c.build()
	if whole.EntityOf(as[0]) != idA || whole.EntityOf(moved) != idB {
		t.Fatalf("the whole plan: want A's group to keep A and the series book on B")
	}
	overrides := func(u *authorentity.Unit) bool {
		return slices.ContainsFunc(u.Credits, func(r authorentity.CreditRow) bool { return r.Key.CreditID == moved })
	}
	refused := applyAllBut(whole, s, overrides)
	if !slices.ContainsFunc(refused, func(u authorentity.Unit) bool { return u.EntityID == idA && u.Entity != nil }) {
		t.Fatalf("A's rename was not refused with B's override: %d units", len(refused))
	}
	if got := dump(s, func(uuid.UUID) bool { return true }); got != before {
		t.Errorf("the refused component changed the store:\nbefore\n%s\nafter\n%s", before, got)
	}

	next := rebuild(t, c, s)
	if next.EntityOf(as[0]) != idA || next.EntityOf(moved) != idB || next.EntityOf(bs[0]) != idB {
		t.Errorf("the next pass: want A's group on A and the series book on B")
	}
	if row := s.Entities[idA]; row.DisplayName != "Иван Сергеевич Петров" {
		t.Errorf("A: want renamed, got %+v", row)
	}
	if row := s.Credits[authorentity.CreditKey{CreditID: moved}]; row.EntityID == nil || *row.EntityID != idB {
		t.Errorf("the series book's override: want B, got %v", ptr(row.EntityID))
	}
	if next.Report.IDs.Merged != 0 || next.Report.IDs.Orphaned != 0 {
		t.Errorf("ids: got %+v", next.Report.IDs)
	}
}
