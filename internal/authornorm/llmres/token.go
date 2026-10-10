package llmres

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
)

// Input limits of the request contract (design doc section 2): a longer input
// is never sent to a model and falls into the input_too_long class.
const (
	// MaxTokens bounds the token count of one request.
	MaxTokens = 16
	// MaxCodePoints bounds the total code points of all token texts.
	MaxCodePoints = 200
)

// ErrInputTooLong marks a request that exceeds MaxTokens or MaxCodePoints.
var ErrInputTooLong = errors.New("llmres: input exceeds the request limits")

// Field is the FB2 name-bearing component a token came from.
type Field string

// The closed set of fields, mirroring the authornorm component kinds.
const (
	FieldFirst    Field = "first"
	FieldMiddle   Field = "middle"
	FieldLast     Field = "last"
	FieldNickname Field = "nickname"
)

// valid reports whether f is one of the four FB2 name fields.
func (f Field) valid() bool {
	switch f {
	case FieldFirst, FieldMiddle, FieldLast, FieldNickname:
		return true
	}
	return false
}

// Token is one token of a tokenized source display name: its index in the
// request, its exact source text, and the FB2 field it came from.
type Token struct {
	I     int
	Text  string
	Field Field
}

// FieldValue is one name field's canonical text in the order the fields
// appear in the source display name.
type FieldValue struct {
	Field Field
	Text  string
}

// Tokenize splits field values into request tokens (design doc section 2):
// white space separates words, punctuation at a word's edges becomes its own
// token with brackets as single-character tokens, and the trailing dot of an
// initial stays attached to the initial.
func Tokenize(fields []FieldValue) []Token {
	var tokens []Token
	for _, fv := range fields {
		for _, word := range strings.Fields(fv.Text) {
			for _, part := range splitWord(word) {
				tokens = append(tokens, Token{I: len(tokens), Text: part, Field: fv.Field})
			}
		}
	}
	return tokens
}

// splitWord cuts one white-space-delimited word into tokens: the leading and
// trailing punctuation runs come off as separate tokens, except a trailing
// dot that completes an initial-like token ("Л.", "Дж.Р.Р.", "Ж.-П.").
func splitWord(word string) []string {
	runes := []rune(word)
	start := 0
	for start < len(runes) && unicode.IsPunct(runes[start]) {
		start++
	}
	end := len(runes)
	for end > start && unicode.IsPunct(runes[end-1]) {
		end--
	}
	prefix, core, suffix := runes[:start], runes[start:end], runes[end:]
	if len(suffix) > 0 && suffix[0] == '.' && len(core) > 0 && authornorm.IsInitialToken(string(core)+".") {
		core = []rune(string(core) + ".")
		suffix = suffix[1:]
	}
	if len(core) == 0 {
		return groupEdgePunct(runes)
	}
	var out []string
	out = append(out, groupEdgePunct(prefix)...)
	out = append(out, string(core))
	out = append(out, groupEdgePunct(suffix)...)
	return out
}

// groupEdgePunct emits an edge punctuation run as tokens: every bracket
// (including quotation marks that act as brackets) is its own token, and a
// run of other punctuation collapses into one token.
func groupEdgePunct(runes []rune) []string {
	var out []string
	var pending []rune
	flush := func() {
		if len(pending) > 0 {
			out = append(out, string(pending))
			pending = nil
		}
	}
	for _, r := range runes {
		if isBracketPunct(r) {
			flush()
			out = append(out, string(r))
		} else {
			pending = append(pending, r)
		}
	}
	flush()
	return out
}

// isBracketPunct reports the punctuation that always stands alone: open and
// close brackets and the quotation marks that play the same role («», “”).
func isBracketPunct(r rune) bool {
	return unicode.Is(unicode.Ps, r) || unicode.Is(unicode.Pe, r) ||
		unicode.Is(unicode.Pi, r) || unicode.Is(unicode.Pf, r)
}

// FieldValuesOf recovers the document order of a canonical source value's
// name fields: the XML child order is not stored on SourceValue, but the
// display name is the non-empty field values joined by single spaces in that
// order, so the unique matching permutation reconstructs it. When duplicate
// field values make several permutations match, the first in the fixed
// enumeration order wins — such credits carry duplicate_component anyway.
func FieldValuesOf(v authornorm.SourceValue) []FieldValue {
	var present []FieldValue
	for _, pair := range []struct {
		field Field
		value *string
	}{
		{FieldFirst, v.First()},
		{FieldMiddle, v.Middle()},
		{FieldLast, v.Last()},
		{FieldNickname, v.Nickname()},
	} {
		if pair.value != nil && *pair.value != "" {
			present = append(present, FieldValue{Field: pair.field, Text: *pair.value})
		}
	}
	display := v.DisplayName()
	var found []FieldValue
	var walk func(taken []bool, acc []FieldValue)
	walk = func(taken []bool, acc []FieldValue) {
		if found != nil {
			return
		}
		if len(acc) == len(present) {
			parts := make([]string, len(acc))
			for i, fv := range acc {
				parts[i] = fv.Text
			}
			if strings.Join(parts, " ") == display {
				found = slices.Clone(acc)
			}
			return
		}
		for i := range present {
			if taken[i] {
				continue
			}
			taken[i] = true
			walk(taken, append(acc, present[i]))
			taken[i] = false
			if found != nil {
				return
			}
		}
	}
	walk(make([]bool, len(present)), nil)
	if found == nil {
		// The display always is a join of the fields; reaching this line means
		// a hand-assembled value. Fall back to the canonical field order.
		return present
	}
	return found
}

// CheckLimits enforces the request size limits of the contract.
func CheckLimits(tokens []Token) error {
	if len(tokens) > MaxTokens {
		return ErrInputTooLong
	}
	total := 0
	for _, t := range tokens {
		total += utf8.RuneCountInString(t.Text)
		if total > MaxCodePoints {
			return ErrInputTooLong
		}
	}
	return nil
}
