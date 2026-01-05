package parser

import "testing"

func TestTagHandlerCollectsValues(t *testing.T) {
	handler := NewTagHandler([]string{"a", "b", "c"})

	handler.OpenTag("a", nil)
	handler.OpenTag("b", nil)
	handler.OpenTag("c", nil)
	handler.SetValue("foo")
	handler.CloseTag("c")

	if got := handler.GetText("\n"); got != "foo" {
		t.Fatalf("expected value %q, got %q", "foo", got)
	}
}

func TestTagHandlerAccumulatesValue(t *testing.T) {
	handler := NewTagHandler([]string{"root", "item"})

	handler.OpenTag("root", nil)
	handler.OpenTag("item", nil)
	handler.SetValue("foo")
	handler.SetValue("bar")
	handler.CloseTag("item")

	values := handler.GetValues()
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0] != "foobar" {
		t.Fatalf("expected %q, got %q", "foobar", values[0])
	}
}

func TestTagHandlerAttributes(t *testing.T) {
	handler := NewTagHandler([]string{"x", "y"})

	handler.OpenTag("x", nil)
	handler.OpenTag("y", map[string]string{"id": "first"})
	handler.CloseTag("y")
	handler.CloseTag("x")

	handler.OpenTag("x", nil)
	handler.OpenTag("y", map[string]string{"id": "second"})
	handler.CloseTag("y")

	if val, ok := handler.GetAttribute("id"); !ok || val != "second" {
		t.Fatalf("expected last attribute %q, got %q (ok=%v)", "second", val, ok)
	}

	attrs := handler.GetAttributes("id")
	if len(attrs) != 2 || attrs[0] != "first" || attrs[1] != "second" {
		t.Fatalf("unexpected attributes: %#v", attrs)
	}
}

func TestTagHandlerReset(t *testing.T) {
	handler := NewTagHandler([]string{"a"})

	handler.OpenTag("a", nil)
	handler.SetValue("value")
	handler.CloseTag("a")
	handler.Reset()

	if len(handler.GetValues()) != 0 {
		t.Fatalf("expected values to be cleared")
	}
	if _, ok := handler.GetAttribute("id"); ok {
		t.Fatalf("expected current attributes to be cleared")
	}
}
