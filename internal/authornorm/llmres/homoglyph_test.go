package llmres

import "testing"

// The homoglyph rule is deterministic: a letter is replaced only when every
// other letter of the token belongs to one other script and the replacement
// makes the token single-script. Anything else stays untouched.

func TestRepairHomoglyphs(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		want     string
		repaired bool
	}{
		{"latin H in cyrillic", "Hиколай", "Николай", true},
		{"latin I in cyrillic", "Iван", "Иван", true},
		{"latin B in cyrillic", "Bажный", "Важный", true},
		{"cyrillic С in latin", "Сanada", "Canada", true},
		{"cyrillic а in latin", "Annа", "Anna", true},
		{"two strays stay", "Hиколаi", "Hиколаi", false},
		{"unmappable letter stays", "Lев", "Lев", false},
		{"pure latin untouched", "Holmes", "Holmes", false},
		{"pure cyrillic untouched", "Холмс", "Холмс", false},
		{"digits are not letters", "Н2О", "Н2О", false},
		{"three scripts stay", "ΑHн", "ΑHн", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := []Token{{I: 0, Text: tt.in, Field: FieldFirst}}
			got, repaired := RepairHomoglyphs(tokens)
			if got[0].Text != tt.want {
				t.Errorf("RepairHomoglyphs(%q) = %q, want %q", tt.in, got[0].Text, tt.want)
			}
			if repaired != tt.repaired {
				t.Errorf("RepairHomoglyphs(%q) repaired = %v, want %v", tt.in, repaired, tt.repaired)
			}
		})
	}
}

// "three scripts stay": for every letter of "ΑHн" (Greek, Latin, Cyrillic)
// the remaining letters span two scripts, so no replacement is allowed.

func TestRepairHomoglyphsKeepsIndexes(t *testing.T) {
	tokens := []Token{{I: 0, Text: "Hиколай", Field: FieldFirst}, {I: 1, Text: ",", Field: FieldFirst}, {I: 2, Text: "Iван", Field: FieldLast}}
	got, repaired := RepairHomoglyphs(tokens)
	if !repaired {
		t.Fatal("repair expected")
	}
	if len(got) != 3 || got[0].I != 0 || got[1].Text != "," || got[2].Text != "Иван" {
		t.Fatalf("got %v", got)
	}
}
