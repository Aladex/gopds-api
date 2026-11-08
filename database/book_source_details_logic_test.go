package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTranslatorNamesOf(t *testing.T) {
	name := func(display string, parts ...string) translatorRow {
		c := translatorRow{Display: display}
		if len(parts) > 0 {
			c.First = &parts[0]
		}
		if len(parts) > 1 {
			c.Middle = &parts[1]
		}
		if len(parts) > 2 {
			c.Last = &parts[2]
		}
		return c
	}
	cases := []struct {
		label string
		in    []translatorRow
		names []string
		more  bool
	}{
		{"none", nil, nil, false},
		{"only empty names", []translatorRow{name("  "), name("")}, nil, false},
		{"display name trimmed", []translatorRow{name("  Нора Галь ")}, []string{"Нора Галь"}, false},
		{"parts when the display name is empty",
			[]translatorRow{name(" ", "Рита", "", "Райт-Ковалёва")}, []string{"Рита Райт-Ковалёва"}, false},
		{"empty names dropped before counting",
			[]translatorRow{name("A"), name(""), name("B")}, []string{"A", "B"}, false},
		{"a duplicate is dropped, inner spaces and case aside",
			[]translatorRow{name("Нора Галь"), name("нора  галь"), name("B")}, []string{"Нора Галь", "B"}, false},
		{"a third name sets more",
			[]translatorRow{name("A"), name("B"), name(""), name("C")}, []string{"A", "B"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			names, more := translatorNamesOf(tc.in)
			assert.Equal(t, tc.names, names)
			assert.Equal(t, tc.more, more)
		})
	}
}
