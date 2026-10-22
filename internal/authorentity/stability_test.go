package authorentity_test

import (
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

// The store may return the name variants in its own collation order; a pass
// over them in any order writes nothing.
func TestTheOrderOfStoredNamesIsNoChange(t *testing.T) {
	c := newCatalog(t).book(1, "А").book(2, "Б").
		credit(10, 1, "fp-a", person("Гончарова", "Галина", "Дмитриевна")).
		credit(11, 2, "fp-b", person("Гончарова", "Галина"))
	c.book(3, "А").credit(12, 3, "fp-b", person("Гончарова", "Галина"))
	_, s := stored(t, c)
	for id, names := range s.Names {
		reversed := make([]authorentity.Name, 0, len(names))
		for i := len(names) - 1; i >= 0; i-- {
			reversed = append(reversed, names[i])
		}
		s.Names[id] = reversed
	}
	if n := rebuild(t, c, s).Changes(); n != 0 {
		t.Errorf("a pass over reordered names: want 0 changes, got %d", n)
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
