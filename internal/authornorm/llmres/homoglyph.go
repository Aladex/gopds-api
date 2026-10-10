package llmres

import (
	"unicode"

	"gopds-api/internal/authornorm"
)

// The homoglyph rule of design doc section 2: a letter is replaced by its
// lookalike from the other script only when every other letter of the token
// belongs to one other script and the replacement makes the token
// single-script. The model never does this — it is a deterministic rule of
// the LLM layer, flagged homoglyph_repaired in the outcome.

// homoglyphPairs is the closed table of Latin/Cyrillic lookalike letters. It
// is part of the LLM-layer contract: editing it is a new configuration
// version.
var homoglyphPairs = [][2]rune{
	{'A', 'А'}, {'B', 'В'}, {'C', 'С'}, {'E', 'Е'}, {'H', 'Н'}, {'I', 'И'},
	{'K', 'К'}, {'M', 'М'}, {'O', 'О'}, {'P', 'Р'}, {'T', 'Т'}, {'X', 'Х'},
	{'Y', 'У'},
	{'a', 'а'}, {'c', 'с'}, {'e', 'е'}, {'i', 'и'}, {'k', 'к'}, {'o', 'о'},
	{'p', 'р'}, {'x', 'х'}, {'y', 'у'},
}

// minLettersForScriptCheck is two things at once: the two directions of one
// homoglyph pair, and the smallest letter count the script rule can reason
// about — a stray letter plus at least one other letter.
const minLettersForScriptCheck = 2

// homoglyphOther maps each letter of a pair to its counterpart, both ways.
var homoglyphOther = func() map[rune]rune {
	m := make(map[rune]rune, len(homoglyphPairs)*minLettersForScriptCheck)
	for _, pair := range homoglyphPairs {
		m[pair[0]] = pair[1]
		m[pair[1]] = pair[0]
	}
	return m
}()

// scriptOfRune is the letter's ISO 15924 script via the authornorm contract.
func scriptOfRune(r rune) authornorm.Script {
	return authornorm.DetectScript(string(r))
}

// RepairHomoglyphs applies the homoglyph rule to every token, keeping indexes
// and fields; it reports whether any token changed.
func RepairHomoglyphs(tokens []Token) ([]Token, bool) {
	out := make([]Token, len(tokens))
	repaired := false
	for i, t := range tokens {
		out[i] = t
		out[i].Text = repairToken(t.Text)
		if out[i].Text != t.Text {
			repaired = true
		}
	}
	return out, repaired
}

// repairToken applies the rule to one token: for every letter whose all other
// letters belong to a single different script, a table replacement into that
// script is applied. A token with several stray letters, three scripts, or a
// stray without a table entry is left untouched — the stricter reading of the
// rule, so a borderline repair never happens silently.
func repairToken(text string) string {
	runes := []rune(text)
	type letter struct {
		pos    int
		r      rune
		script authornorm.Script
	}
	var letters []letter
	for pos, r := range runes {
		if unicode.IsLetter(r) {
			letters = append(letters, letter{pos, r, scriptOfRune(r)})
		}
	}
	if len(letters) < minLettersForScriptCheck {
		return text
	}
	changed := false
	for _, l := range letters {
		var other authornorm.Script
		uniform := true
		for _, m := range letters {
			if m.pos == l.pos {
				continue
			}
			if other == "" {
				other = m.script
			} else if m.script != other {
				uniform = false
				break
			}
		}
		if !uniform || other == "" || other == l.script {
			continue
		}
		if repl, ok := homoglyphOther[l.r]; ok && scriptOfRune(repl) == other {
			runes[l.pos] = repl
			changed = true
		}
	}
	if !changed {
		return text
	}
	return string(runes)
}
