package authornorm

import (
	"errors"
	"reflect"
	"testing"
)

// strptr helpers make present-but-empty components explicit next to absent
// ones: nil means the element was not there, &"" means it was there and empty.
func strptr(s string) *string { return &s }

func TestCanonicalSourceValueTrimsOnlyXMLWhitespaceAndAppliesNFC(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain value untouched", "Иван", "Иван"},
		{"spaces trimmed", "  Иван  ", "Иван"},
		{"tab cr lf trimmed", "\t\r\nИван\n\r", "Иван"},
		{"nbsp kept", "\u00A0Иван\u00A0", "\u00A0Иван\u00A0"},
		{"em space kept", "\u2003Иван\u2003", "\u2003Иван\u2003"},
		{"lower case preserved", "иван", "иван"},
		{"yo preserved", "Ёлкин", "Ёлкин"},
		{"punctuation preserved", "О'Нил", "О'Нил"},
		{"decomposed acute composed by NFC", "e\u0301", "\u00E9"},
		{"interior whitespace kept", "ван  Гogh", "ван  Гogh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := NewSourceValue([]SourceComponent{{Kind: ComponentLast, Value: tc.raw}})
			if err != nil {
				t.Fatalf("NewSourceValue: %v", err)
			}
			got := v.Last()
			if got == nil {
				t.Fatalf("Last() is nil, want %q", tc.want)
			}
			if *got != tc.want {
				t.Errorf("Last() = %q, want %q", *got, tc.want)
			}
		})
	}
}

func TestNewSourceValueRejectsContributorWithoutNameComponents(t *testing.T) {
	if _, err := NewSourceValue(nil); !errors.Is(err, ErrNoNameComponents) {
		t.Errorf("nil components: err = %v, want ErrNoNameComponents", err)
	}
	if _, err := NewSourceValue([]SourceComponent{{Kind: ComponentFirst, Value: "  "}}); !errors.Is(err, ErrNoNameComponents) {
		t.Errorf("whitespace-only component: err = %v, want ErrNoNameComponents", err)
	}
	if _, err := NewSourceValue([]SourceComponent{{Kind: ComponentKind(42), Value: "x"}}); !errors.Is(err, ErrUnknownComponentKind) {
		t.Errorf("unknown kind: err = %v, want ErrUnknownComponentKind", err)
	}
}

func TestPresentEmptyElementDiffersFromAbsent(t *testing.T) {
	v, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentFirst, Value: ""},
		{Kind: ComponentLast, Value: "Петров"},
	})
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	if v.First() == nil {
		t.Fatal("First() is nil for a present-but-empty first-name element")
	}
	if *v.First() != "" {
		t.Errorf("First() = %q, want empty string", *v.First())
	}
	if v.Middle() != nil {
		t.Errorf("Middle() = %q, want nil for an absent element", *v.Middle())
	}
	if got := v.DisplayName(); got != "Петров" {
		t.Errorf("DisplayName() = %q, want %q: empty components do not join the display", got, "Петров")
	}
}

func TestDisplayNameUsesOriginalChildOrder(t *testing.T) {
	lastFirst, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentLast, Value: "Петров"},
		{Kind: ComponentFirst, Value: "Иван"},
	})
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	if got := lastFirst.DisplayName(); got != "Петров Иван" {
		t.Errorf("DisplayName() = %q, want %q", got, "Петров Иван")
	}

	firstLast, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentFirst, Value: "Иван"},
		{Kind: ComponentLast, Value: "Петров"},
	})
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	if got := firstLast.DisplayName(); got != "Иван Петров" {
		t.Errorf("DisplayName() = %q, want %q", got, "Иван Петров")
	}

	// A contributor with a missing middle name must not shift the components
	// of the next contributor: each value is built inside its own element.
	firstContributor, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentFirst, Value: "Анна"},
		{Kind: ComponentLast, Value: "Смирнова"},
	})
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	secondContributor, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentFirst, Value: "Борис"},
		{Kind: ComponentMiddle, Value: "Владимирович"},
		{Kind: ComponentLast, Value: "Волков"},
	})
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	if firstContributor.Middle() != nil {
		t.Errorf("first contributor Middle() = %q, want nil", *firstContributor.Middle())
	}
	middle := secondContributor.Middle()
	if middle == nil {
		t.Fatal("second contributor Middle() is nil, want \"Владимирович\"")
	}
	if *middle != "Владимирович" {
		t.Errorf("second contributor Middle() = %q, want %q", *middle, "Владимирович")
	}
	last := secondContributor.Last()
	if last == nil {
		t.Fatal("second contributor Last() is nil, want \"Волков\"")
	}
	if *last != "Волков" {
		t.Errorf("second contributor Last() = %q, want %q", *last, "Волков")
	}
}

func TestDuplicateComponentGoesToDisplayFirstValueWinsNamedField(t *testing.T) {
	v, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentFirst, Value: "А"},
		{Kind: ComponentFirst, Value: "Б"},
		{Kind: ComponentLast, Value: "В"},
	})
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	first := v.First()
	if first == nil {
		t.Fatal("First() is nil, want the first occurrence \"А\"")
	}
	if *first != "А" {
		t.Errorf("First() = %q, want the first occurrence %q", *first, "А")
	}
	if got := v.DisplayName(); got != "А Б В" {
		t.Errorf("DisplayName() = %q, want %q: duplicated children stay in the display", got, "А Б В")
	}
	if !v.HasDuplicateComponent() {
		t.Error("HasDuplicateComponent() = false, want true")
	}

	clean, err := NewSourceValue([]SourceComponent{{Kind: ComponentLast, Value: "В"}})
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	if clean.HasDuplicateComponent() {
		t.Error("HasDuplicateComponent() = true for a single component, want false")
	}
}

func TestAccessorsDoNotExposeInternalState(t *testing.T) {
	accessors := []struct {
		name string
		kind ComponentKind
		get  func(SourceValue) *string
	}{
		{"first", ComponentFirst, SourceValue.First},
		{"middle", ComponentMiddle, SourceValue.Middle},
		{"last", ComponentLast, SourceValue.Last},
		{"nickname", ComponentNickname, SourceValue.Nickname},
	}
	for _, a := range accessors {
		t.Run(a.name, func(t *testing.T) {
			v := mustValue(t, SourceComponent{Kind: a.kind, Value: "Original"})
			copied := v
			before := SourceFingerprint(v)

			p := a.get(v)
			if p == nil {
				t.Fatalf("%s() is nil, want a present field", a.name)
			}
			*p = "\tchanged\u00A0"

			got := a.get(v)
			if got == nil {
				t.Fatalf("after external write, %s() is nil, want the stored %q", a.name, "Original")
			}
			if *got != "Original" {
				t.Errorf("after external write, %s() = %q, want the stored %q", a.name, *got, "Original")
			}
			copiedGot := a.get(copied)
			if copiedGot == nil {
				t.Fatalf("after external write, copied value's %s() is nil, want %q", a.name, "Original")
			}
			if *copiedGot != "Original" {
				t.Errorf("after external write, copied value's %s() = %q, want %q", a.name, *copiedGot, "Original")
			}
			if got == p {
				t.Errorf("%s() returned the same pointer twice: callers can alias the stored field", a.name)
			}
			if v.DisplayName() != "Original" {
				t.Errorf("DisplayName() = %q after external write, want %q", v.DisplayName(), "Original")
			}
			if SourceFingerprint(v) != before {
				t.Error("SourceFingerprint changed after an external write through an accessor")
			}
		})
	}
}

func TestCopiedValueKeepsNilDistinctFromPresentEmpty(t *testing.T) {
	v := mustValue(t,
		SourceComponent{Kind: ComponentFirst, Value: ""},
		SourceComponent{Kind: ComponentLast, Value: "Петров"},
	)
	copied := v
	for _, sv := range []SourceValue{v, copied} {
		if sv.First() == nil {
			t.Error("present-but-empty first became nil after copy")
		}
		if sv.Middle() != nil {
			t.Error("absent middle became non-nil after copy")
		}
	}
}

// restoreFrom rebuilds a value from exactly what the credit row stores.
func restoreFrom(v SourceValue) (SourceValue, error) {
	return RestoreSourceValue(v.First(), v.Middle(), v.Last(), v.Nickname(), v.DisplayName(), v.HasDuplicateComponent())
}

func TestRestoreSourceValueRoundTripsEveryStoredShape(t *testing.T) {
	cases := []struct {
		name       string
		components []SourceComponent
	}{
		{"first last", []SourceComponent{{ComponentFirst, "Иван"}, {ComponentLast, "Петров"}}},
		{"last before first", []SourceComponent{{ComponentLast, "Петров"}, {ComponentFirst, "Иван"}}},
		{"all four out of order", []SourceComponent{
			{ComponentNickname, "Ник"}, {ComponentLast, "Петров"}, {ComponentMiddle, "Ильич"}, {ComponentFirst, "Иван"},
		}},
		{"present empty element", []SourceComponent{{ComponentFirst, ""}, {ComponentLast, "Петров"}}},
		{"value with inner spaces", []SourceComponent{{ComponentLast, "ван  Гог"}, {ComponentFirst, "Винсент"}}},
		{"nbsp edges kept", []SourceComponent{{ComponentLast, " Петров "}}},
		{"duplicate with text", []SourceComponent{{ComponentFirst, "Иван"}, {ComponentFirst, "Пётр"}, {ComponentLast, "Петров"}}},
		{"duplicate empty first", []SourceComponent{{ComponentFirst, ""}, {ComponentFirst, "Иван"}, {ComponentLast, "Петров"}}},
		{"duplicate that adds nothing", []SourceComponent{{ComponentFirst, "Иван"}, {ComponentFirst, ""}}},
		{"identical repeated value", []SourceComponent{{ComponentFirst, "Иван"}, {ComponentFirst, "Иван"}}},
		{"repeat after another kind", []SourceComponent{
			{ComponentLast, "Петров"}, {ComponentFirst, "Иван"}, {ComponentLast, "Сидоров"},
		}},
		{"repeat of an empty kind before a value", []SourceComponent{
			{ComponentNickname, ""}, {ComponentLast, "Петров"}, {ComponentNickname, "Ник"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := NewSourceValue(tc.components)
			if err != nil {
				t.Fatalf("NewSourceValue: %v", err)
			}
			restored, err := restoreFrom(v)
			if err != nil {
				t.Fatalf("RestoreSourceValue: %v", err)
			}
			if !reflect.DeepEqual(restored, v) {
				t.Errorf("restored %+v, want %+v", restored, v)
			}
			if SourceFingerprint(restored) != SourceFingerprint(v) {
				t.Error("restored value has another fingerprint")
			}
		})
	}
}

func TestRestoreSourceValueRejectsWhatNoExtractionProduces(t *testing.T) {
	cases := []struct {
		name      string
		first     *string
		last      *string
		display   string
		duplicate bool
		want      error
	}{
		{"untrimmed field", strptr(" Иван"), strptr("Петров"), " Иван Петров", false, ErrNonCanonicalSource},
		{"untrimmed field inside a consistent display", strptr(" Иван"), strptr("Петров"), "Петров  Иван", false, ErrNonCanonicalSource},
		{"decomposed field", strptr("Rene\u0301"), nil, "Rene\u0301", false, ErrNonCanonicalSource},
		{"empty display", strptr(""), nil, "", false, ErrNoNameComponents},
		{"display names another person", strptr("Иван"), strptr("Петров"), "Пётр Петров", false, ErrNonCanonicalSource},
		{"display drops a field", strptr("Иван"), strptr("Петров"), "Петров", false, ErrNonCanonicalSource},
		{"display with an extra word but no duplicate", strptr("Иван"), strptr("Петров"), "Иван Петров Сидоров", false, ErrNonCanonicalSource},
		{"display with doubled separator", strptr("Иван"), strptr("Петров"), "Иван  Петров", false, ErrNonCanonicalSource},
		{"duplicate display misses a field", strptr("Иван"), strptr("Петров"), "Пётр Петров", true, ErrNonCanonicalSource},
		{"duplicate display untrimmed", strptr("Иван"), nil, "Иван Пётр ", true, ErrNonCanonicalSource},
		// Fix round 1 (review B3): duplicate forms no extraction can produce.
		{"duplicate without any name child", nil, nil, "Иван", true, ErrNonCanonicalSource},
		{"one occurrence used by two children", strptr("Иван"), strptr("Иван"), "Иван", true, ErrNonCanonicalSource},
		{"overlapping child values", strptr("Анна Мария"), strptr("Мария Петрова"), "Анна Мария Петрова", true, ErrNonCanonicalSource},
		{"repeat before the first occurrence of its kind", strptr("Иван"), nil, "Пётр Иван", true, ErrNonCanonicalSource},
		{"repeat cut at a doubled separator", strptr("Иван"), nil, "Иван  Пётр", true, ErrNonCanonicalSource},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RestoreSourceValue(tc.first, nil, tc.last, nil, tc.display, tc.duplicate)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
