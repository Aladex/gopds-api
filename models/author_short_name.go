package models

import (
	"slices"
	"strings"
	"unicode"
)

// patronymicSuffixes end a Cyrillic patronymic, lower-cased: the male -вич
// (-ович, -евич), the female -овна, -евна and -ична (-инична), and the
// Ukrainian female -івна, -ївна. A bare -ич is no suffix of its own: it ends
// family names too (Синтия Лейтич Смит), so only the short male forms below
// count with it.
var patronymicSuffixes = []string{"вич", "овна", "евна", "ична", "івна", "ївна"}

// shortPatronymics are the male patronymics that end in -ич without -вич,
// lower-cased.
var shortPatronymics = []string{"ильич", "иллич", "кузьмич", "козьмич", "кузмич", "никитич", "лукич", "фомич", "фокич", "хомич"}

// ShortAuthorName is an author's name as book lists show it, «Имя Фамилия»:
// the name with a patronymic dropped. A patronymic is the middle word of a
// three-word name, written only in Cyrillic letters (U+0400–U+04FF), between
// a given name and a family name that carry no bracket: one of the short male
// forms, or a word ending in a patronymic suffix after at least two more
// letters. Every other name — a second given name, initials, a particle, a
// monastic name in brackets, a collective, a name of two words or four —
// comes back as it is.
//
// public.author_short_name (migration 86) is the same rule for SQL; a
// database test holds the two together.
func ShortAuthorName(name string) string {
	words := strings.Fields(name)
	if len(words) != 3 || !isPatronymic(words[1]) || hasBracket(words[0]) || hasBracket(words[2]) {
		return name
	}
	return words[0] + " " + words[2]
}

// isPatronymic reports whether a word reads as a Cyrillic patronymic.
func isPatronymic(word string) bool {
	runes := []rune(word)
	for _, r := range runes {
		if r < 0x0400 || r > 0x04FF || !unicode.IsLetter(r) {
			return false
		}
	}
	lower := strings.Map(unicode.ToLower, word)
	if slices.Contains(shortPatronymics, lower) {
		return true
	}
	for _, suffix := range patronymicSuffixes {
		if strings.HasSuffix(lower, suffix) && len(runes) >= len([]rune(suffix))+2 {
			return true
		}
	}
	return false
}

// hasBracket reports whether a word carries a bracket: «(Крестьянкин)» is a
// note beside a name, not a given or family name.
func hasBracket(word string) bool {
	return strings.ContainsAny(word, "()[]")
}
