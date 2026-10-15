package services

import (
	"strings"
	"unicode"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmres"
)

// The catalog's role statistics (design K, section 4): for every token — the
// search key of a word of a name field, as the request tokenizes it — how
// many current structured_person author credits hold it in the first, the
// middle and the last field. They find the swap candidates without an LLM,
// and at application they are the third voice besides the two models: a swap
// is applied only when the statistics say the same as the answer.

// The thresholds of the candidate rule: a token is dominant in a field when
// it stands there in at least swapMinSharePercent of its occurrences, with at
// least swapMinSupport occurrences.
const (
	swapMinSupport      = 5
	swapMinSharePercent = 90
)

// swapPatronymicSuffixes are the endings of a Cyrillic patronymic: the
// morphology generator's second source of candidates. "-ична" covers
// "-инична". Only a word of the first field counts: in the last field these
// endings are mostly real family names (-ович, -евич), which the acceptance
// rule — the words of the first field are the family name — could never
// apply; on the production catalog 1 854 of 1 919 such inputs had it there.
var swapPatronymicSuffixes = []string{"ович", "евич", "овна", "евна", "ична"}

// roleStats are the counts per token key.
type roleStats map[string]database.AuthorTokenRoleCounts

// swapToken is one word of an input as the statistics count it.
type swapToken struct {
	i     int
	key   string
	field llmres.Field
}

// swapTokensOf are the words of the fields that carry a letter, with their
// search keys. The request's own tokenizer splits them, so the statistics,
// the candidates and the answers speak of the same tokens.
func swapTokensOf(fields []llmres.FieldValue) ([]swapToken, error) {
	in, err := llmres.NewInput(fields)
	if err != nil {
		return nil, err
	}
	out := make([]swapToken, 0, len(in.Tokens))
	for _, tok := range in.Tokens {
		key := authornorm.SearchKey(tok.Text)
		if key == "" || !strings.ContainsFunc(key, unicode.IsLetter) {
			continue
		}
		out = append(out, swapToken{i: tok.I, key: key, field: tok.Field})
	}
	return out, nil
}

// add counts one source value of credits credits.
func (s roleStats) add(fields []llmres.FieldValue, credits int) error {
	tokens, err := swapTokensOf(fields)
	if err != nil {
		return err
	}
	for _, t := range tokens {
		c := s[t.key]
		switch t.field {
		case llmres.FieldFirst:
			c.First += credits
		case llmres.FieldMiddle:
			c.Middle += credits
		case llmres.FieldLast:
			c.Last += credits
		case llmres.FieldNickname:
			// A nickname has no role of a name component.
			continue
		}
		s[t.key] = c
	}
	return nil
}

// dominant is the field a token is dominant in; ok is false when it has too
// little support or no field holds the share.
func (s roleStats) dominant(key string) (llmres.Field, bool) {
	c, found := s[key]
	if !found {
		return "", false
	}
	total := c.First + c.Middle + c.Last
	if total < swapMinSupport {
		return "", false
	}
	for _, f := range []struct {
		field llmres.Field
		n     int
	}{{llmres.FieldFirst, c.First}, {llmres.FieldMiddle, c.Middle}, {llmres.FieldLast, c.Last}} {
		if f.n*100 >= swapMinSharePercent*total {
			return f.field, true
		}
	}
	return "", false
}

// The two sources of swap candidates.
const (
	swapByStatistics = "statistics"
	swapByMorphology = "morphology"
)

// swapCandidate says whether a selected structured_person input is a swap
// candidate and by which rule (design K, section 4):
//   - statistics: every word of the first field is dominant in the last
//     field and every word of the last field in the first one;
//   - morphology: a Cyrillic word of the first field has a patronymic
//     ending.
func swapCandidate(stats roleStats, tokens []swapToken, script string) (string, bool) {
	if swappedByStatistics(stats, tokens) {
		return swapByStatistics, true
	}
	if script == string(authornorm.ScriptCyrillic) && patronymicFirst(tokens) {
		return swapByMorphology, true
	}
	return "", false
}

// swappedByStatistics: the first and the last field have words, every word
// of the first field is dominant in the last one, and every word of the last
// field in the first one.
func swappedByStatistics(stats roleStats, tokens []swapToken) bool {
	firsts, lasts := 0, 0
	for _, t := range tokens {
		var want llmres.Field
		switch t.field {
		case llmres.FieldFirst:
			firsts++
			want = llmres.FieldLast
		case llmres.FieldLast:
			lasts++
			want = llmres.FieldFirst
		case llmres.FieldMiddle, llmres.FieldNickname:
			continue
		}
		if f, ok := stats.dominant(t.key); !ok || f != want {
			return false
		}
	}
	return firsts > 0 && lasts > 0
}

// patronymicFirst: a word of the first field has a patronymic ending.
func patronymicFirst(tokens []swapToken) bool {
	for _, t := range tokens {
		if t.field != llmres.FieldFirst {
			continue
		}
		for _, suffix := range swapPatronymicSuffixes {
			if len([]rune(t.key)) > len([]rune(suffix))+1 && strings.HasSuffix(t.key, suffix) {
				return true
			}
		}
	}
	return false
}

// swapFieldRole is the role a word dominant in a field has in a person name.
var swapFieldRole = map[llmres.Field]llmres.Role{
	llmres.FieldFirst: llmres.RoleGiven, llmres.FieldMiddle: llmres.RoleAdditional, llmres.FieldLast: llmres.RoleFamily,
}

// swapStatsAgree reports whether the statistics say the same as an answer
// (decision 8): every word is dominant in a field and the answer gives it
// that field's role, and the words of the first field are the family name.
// Where the statistics are silent — a rare word, as in a Chinese name in its
// own order — they do not agree, and nothing is swapped.
func swapStatsAgree(stats roleStats, tokens []swapToken, roles map[int]llmres.Role) bool {
	familyFromFirst := false
	for _, t := range tokens {
		f, ok := stats.dominant(t.key)
		if !ok || roles[t.i] != swapFieldRole[f] {
			return false
		}
		if t.field == llmres.FieldFirst {
			if roles[t.i] != llmres.RoleFamily {
				return false
			}
			familyFromFirst = true
		}
	}
	return familyFromFirst
}

// swapStatKeys are the distinct keys of tokens.
func swapStatKeys(tokens []swapToken) []string {
	seen := map[string]bool{}
	keys := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !seen[t.key] {
			seen[t.key] = true
			keys = append(keys, t.key)
		}
	}
	return keys
}
