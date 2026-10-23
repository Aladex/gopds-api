package authornorm

import (
	"maps"
	"testing"
)

// NameWordCaseDefects widens the lowercase defect to every name word of a
// component; the all-caps defect and the normalizer's own view stay as they
// are.
func TestNameWordCaseDefects(t *testing.T) {
	cases := []struct {
		value string
		words int
		want  map[int]QualityFlag
	}{
		{"Исидор севильский", 2, map[int]QualityFlag{1: FlagLowercase}},
		{"Лама Сопа ринпоче", 3, map[int]QualityFlag{2: FlagLowercase}},
		{"иван петров", 2, map[int]QualityFlag{0: FlagLowercase, 1: FlagLowercase}},
		{"ИВАН петров", 2, map[int]QualityFlag{0: FlagAllCaps, 1: FlagLowercase}},
		// Particles, affixes and initials keep their case.
		{"Шарль де Голль", 3, nil},
		{"Лазарь архимандрит", 2, nil},
		{"Иван и. Петров", 3, nil},
		// A lone lower-case letter is a conjunction, not a word to fix.
		{"Иван и Пётр", 3, nil},
	}
	for _, c := range cases {
		if got := NameWordCaseDefects(c.value, c.words); !maps.Equal(got, c.want) {
			t.Errorf("NameWordCaseDefects(%q) = %v, want %v", c.value, got, c.want)
		}
	}
	// The normalizer's view is unchanged: only the first name word.
	if got := WordCaseDefects("Исидор севильский", 2); len(got) != 0 {
		t.Errorf("WordCaseDefects changed: %v", got)
	}
}
