package authornorm

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file is the narrow exported view of the NormalizerVersion v1 contract
// that the LLM layer (internal/authornorm/llmres) validates model answers
// against. It adds no behavior to the local normalizer: every function only
// exposes what the contract already computes, so the LLM layer cannot drift
// from it by keeping a copy.

// PlaceholderVocabulary returns the closed placeholder dictionary of
// NormalizerVersion v1 as two copies: the whole-key placeholders (matched
// against a full search key) and the fragment prefixes (matched against a
// single folded token). Editing the vocabulary is a new normalizer version.
func PlaceholderVocabulary() (keys, fragmentPrefixes []string) {
	keys = make([]string, 0, len(placeholders))
	for key := range placeholders {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, slices.Clone(placeholderFragmentPrefixes)
}

// WordCaseDefects reports the case defects NormalizerVersion v1 observes on
// the white-space words of one component value: FlagAllCaps on every name
// word of two or more cased letters written wholly in uppercase, and
// FlagLowercase on the component's first name word when it starts lowercase.
// Word indexes are into strings.Fields(value); displayWordCount is the word
// count of the whole display the component belongs to, because the particle
// rule depends on it. The LLM layer's case-fix validator uses exactly this
// view to decide whether a case_fix is admitted for a token.
func WordCaseDefects(value string, displayWordCount int) map[int]QualityFlag {
	words := strings.Fields(value)
	var defects map[int]QualityFlag
	for i, word := range words {
		t := classifyToken(word, displayWordCount)
		if !t.nameWord() {
			continue
		}
		if upper, lower := casedLetters(word); upper >= minCaseDefectLetters && lower == 0 {
			if defects == nil {
				defects = make(map[int]QualityFlag)
			}
			defects[i] = FlagAllCaps
		}
	}
	// The lowercase defect belongs to the first name word of the component
	// only: later words of a phrase are legitimately lower-case.
	for i, word := range words {
		t := classifyToken(word, displayWordCount)
		if !t.nameWord() {
			continue
		}
		first, _ := utf8.DecodeRuneInString(word)
		if upper, lower := casedLetters(word); unicode.IsLower(first) && upper+lower >= minCaseDefectLetters {
			if defects == nil {
				defects = make(map[int]QualityFlag)
			}
			defects[i] = FlagLowercase
		}
		break
	}
	return defects
}

// IsInitialToken reports whether token is an initial-like abbreviation per
// NormalizerVersion v1: "И.", "Дж.Р.Р.", "Ж.-П.", "Ю.Несбё", a lone capital.
func IsInitialToken(token string) bool {
	return isInitialToken(token)
}
