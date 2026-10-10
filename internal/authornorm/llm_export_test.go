package authornorm

import (
	"slices"
	"strings"
	"testing"
)

// The LLM layer (internal/authornorm/llmres) judges model answers against the
// same closed vocabularies and case observations as the local normalizer;
// these tests pin the exported views of that contract.

func TestPlaceholderVocabularyContainsKnownEntries(t *testing.T) {
	keys, prefixes := PlaceholderVocabulary()
	for _, want := range []string{"аноним", "unknown", "неизвестный автор", "anonymous"} {
		if !slices.Contains(keys, want) {
			t.Errorf("placeholder keys missing %q", want)
		}
	}
	for _, want := range []string{"аноним", "anonym", "неизвестн", "unknown", "noname"} {
		if !slices.Contains(prefixes, want) {
			t.Errorf("placeholder fragment prefixes missing %q", want)
		}
	}
}

func TestPlaceholderVocabularyReturnsCopies(t *testing.T) {
	keys1, prefixes1 := PlaceholderVocabulary()
	keys1[0] = "mutated"
	prefixes1[0] = "mutated"
	keys2, prefixes2 := PlaceholderVocabulary()
	if slices.Contains(keys2, "mutated") || slices.Contains(prefixes2, "mutated") {
		t.Fatal("PlaceholderVocabulary must hand out copies, not the contract tables")
	}
}

func TestWordCaseDefects(t *testing.T) {
	tests := []struct {
		name      string
		component string
		want      map[int]QualityFlag
	}{
		{"all caps words", "ИВАН ИВАНОВИЧ", map[int]QualityFlag{0: FlagAllCaps, 1: FlagAllCaps}},
		{"first word lowercase", "иван Иванович", map[int]QualityFlag{0: FlagLowercase}},
		{"later word lowercase is not flagged", "Иван иванович", nil},
		{"single letters are not defects", "А Б", nil},
		{"initials are not defects", "Л. А. КАЛУГИНА", map[int]QualityFlag{2: FlagAllCaps}},
		{"particles skip the lowercase check", "де Голль", nil},
		{"mixed case is not a defect", "Иван Иванович", nil},
		{"affixes are not name words", "МЛ. ИВАН", map[int]QualityFlag{1: FlagAllCaps}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WordCaseDefects(tt.component, len(words(tt.component)))
			if len(got) != len(tt.want) {
				t.Fatalf("WordCaseDefects(%q) = %v, want %v", tt.component, got, tt.want)
			}
			for i, flag := range tt.want {
				if got[i] != flag {
					t.Errorf("WordCaseDefects(%q)[%d] = %v, want %v", tt.component, i, got[i], flag)
				}
			}
		})
	}
}

// TestWordCaseDefectsParityWithNormalize checks the exported per-word view
// agrees with the flags Normalize reports on the whole value: a component
// flagged all-caps word by word must surface as FlagAllCaps, and a lowercase
// first name word as FlagLowercase.
func TestWordCaseDefectsParityWithNormalize(t *testing.T) {
	tests := []struct {
		first, last string
		flag        QualityFlag
		present     bool
	}{
		{"ИВАН", "ИВАНОВ", FlagAllCaps, true},
		{"Иван", "Иванов", FlagAllCaps, false},
		{"иван", "Иванов", FlagLowercase, true},
		{"Иван", "Иванов", FlagLowercase, false},
	}
	for _, tt := range tests {
		v, err := NewSourceValue([]SourceComponent{
			{Kind: ComponentFirst, Value: tt.first},
			{Kind: ComponentLast, Value: tt.last},
		})
		if err != nil {
			t.Fatal(err)
		}
		r, err := Normalize(v, "test-extractor-v1")
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(r.QualityFlags, tt.flag) != tt.present {
			t.Errorf("Normalize(%q, %q) flag %s presence = %v, want %v",
				tt.first, tt.last, tt.flag, !tt.present, tt.present)
		}
		// The per-word view must agree on the first-name component.
		defects := WordCaseDefects(tt.first, len(words(v.DisplayName())))
		_, wordFlagged := defects[0]
		if wordFlagged && defects[0] == tt.flag && !tt.present {
			t.Errorf("word view flags %s on %q but Normalize does not", tt.flag, tt.first)
		}
	}
}

func TestIsInitialToken(t *testing.T) {
	for _, token := range []string{"Л.", "А.", "Дж.Р.Р.", "Ж.-П.", "Ю.Несбё", "А"} {
		if !IsInitialToken(token) {
			t.Errorf("IsInitialToken(%q) = false, want true", token)
		}
	}
	for _, token := range []string{"Иванов", "", "де", "л."} {
		if IsInitialToken(token) {
			t.Errorf("IsInitialToken(%q) = true, want false", token)
		}
	}
}

func words(s string) []string {
	return strings.Fields(s)
}
