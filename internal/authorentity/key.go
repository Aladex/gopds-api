// Package authorentity builds author entities — one record per real person
// or collective — from the selected author credits, deterministically and
// without a model (design E, §2.2, phase E1). The package is pure: it reads
// the credits, the books and what the previous pass stored, and returns a
// plan of the rows to write; the database layer applies it.
//
// The principle is "separate by default, merge only on evidence" (§2.0):
//   - D1: one fingerprint is one entity;
//   - D2: fingerprints with the same full name key are one entity;
//   - a typo in the patronymic merges two full names only with strong
//     evidence linking their books;
//   - D3: a book under a short name (no patronymic) moves to the one full
//     name its strong evidence points to — the same title under the full
//     name, a shared series or a shared co-author — and otherwise stays in
//     the short name's bucket, which is an entity of its own;
//   - D4: a nickname is a name variant of its entity.
//
// Initials, transliteration and anything contested are left to the model
// (E4); placeholders and names without a letter or digit get no entity.
package authorentity

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
)

// nameKey is the normalized key of a person name (§2.2): the script and the
// family, given and additional parts, each folded by authornorm.SearchKey —
// lower case, ё as е, punctuation and white space (a no-break space too) as
// one separator — in a fixed order whatever the source order was. The
// generational suffix splits (Jr.); an honorific prefix does not.
type nameKey struct {
	// kind separates a collective from a person of the same words.
	kind       string
	script     string
	family     string
	given      string
	additional string
	suffix     string
	// name is the key of a name without person parts: a collective's
	// selected name, or the nickname of a person known by it alone.
	name string
	// tagged is the fingerprint of a credit with a namesake tag "(N)": such
	// a key is its fingerprint's own, never shared (§2.0).
	tagged string
}

// base is the key without its additional part: the short name's bucket and
// every full name D3 may move its books to share it.
func (k *nameKey) base() nameKey {
	b := *k
	b.additional = ""
	return b
}

// short reports a key without a patronymic or middle name.
func (k *nameKey) short() bool { return k.additional == "" }

// initials reports a key with a one-letter given or additional word: "Л.",
// "Л. Н.", "Лев Н.". They are not merged in E1 (E4 asks the model).
func (k *nameKey) initials() bool {
	for _, part := range []string{k.given, k.additional} {
		for _, w := range strings.Fields(part) {
			if utf8.RuneCountInString(w) == 1 {
				return true
			}
		}
	}
	return false
}

// evidenceEligible reports a key D3 and the typo merge may use: untagged,
// with a family and a given name, and not of initials.
func (k *nameKey) evidenceEligible() bool {
	return k.tagged == "" && k.family != "" && k.given != "" && !k.initials()
}

func (k *nameKey) String() string {
	return k.kind + "|" + k.script + "|" + k.family + "|" + k.given + "|" + k.additional + "|" + k.suffix + "|" +
		k.name + "|" + k.tagged
}

// keyOf is a credit's key. A collective is keyed by its selected name; a
// person without name parts by its nickname (else its display name).
func keyOf(c *Credit) nameKey {
	if c.Kind == KindCollective {
		k := nameKey{kind: KindCollective, script: c.Script, name: authornorm.SearchKey(c.DisplayName)}
		if c.Tag > 0 {
			k.tagged = c.Fingerprint
		}
		return k
	}
	k := nameKey{
		kind:       KindPerson,
		script:     c.Script,
		family:     authornorm.SearchKey(c.Family),
		given:      authornorm.SearchKey(c.Given),
		additional: authornorm.SearchKey(strings.Join(c.Additional, " ")),
		suffix:     authornorm.SearchKey(c.Suffix),
	}
	if k.family == "" && k.given == "" && k.additional == "" {
		k.name = authornorm.SearchKey(c.Nickname)
		if k.name == "" {
			k.name = authornorm.SearchKey(c.DisplayName)
		}
	}
	if c.Tag > 0 {
		k.tagged = c.Fingerprint
	}
	return k
}

// hasLetterOrDigit reports a key with something to name: a credit whose
// parts are punctuation only gets no entity (the J3 hotfix rule).
func (k *nameKey) hasLetterOrDigit() bool {
	return strings.ContainsFunc(k.family+k.given+k.additional+k.name, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	})
}

// placeholderKeys are the normalizer's whole-key placeholders ("Автор
// неизвестен"): a credit named by one gets no entity (§1.4).
var placeholderKeys = func() map[string]bool {
	keys, _ := authornorm.PlaceholderVocabulary()
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}()

// collectiveStubKeys are the whole search keys of a collective that names no
// one in particular — the label of many unnamed authors (§1.4, «Коллектив
// авторов» as a stub). The normalizer has no such vocabulary (its collective
// prefixes mark every collective, «Братья Гримм» too), and its versioned
// contract stays as it is: the exclusion is the entity build's own.
var collectiveStubKeys = map[string]bool{
	"коллектив авторов": true, "авторский коллектив": true, "коллектив": true, "группа авторов": true,
	"разные авторы": true, "сборник": true, "антология": true, "народное творчество": true,
	"various": true, "various authors": true, "collective": true, "anthology": true,
}

// isPlaceholder reports a credit whose name is a placeholder, a placeholder
// with a label after it, or a collective that is only a stub.
func isPlaceholder(c *Credit) bool {
	key := authornorm.SearchKey(c.DisplayName)
	return placeholderKeys[key] || (c.Kind == KindCollective && collectiveStubKeys[key]) || labelledPlaceholder(c)
}

// labelSeparator finds every separator run of a display name: a dash of any
// kind, a colon, a bar or a slash, with the white space around it.
var labelSeparator = regexp.MustCompile(`\s*[-‐‑‒–—―:|/]+\s*`)

// placeholderRun reports a display name with a run of pieces between its
// separators that is a whole placeholder (the whole name too: a placeholder
// spelled with a hyphen of its own).
func placeholderRun(display string) bool {
	seps := labelSeparator.FindAllStringIndex(display, -1)
	if len(seps) == 0 {
		return false
	}
	// Piece i runs from the end of separator i-1 to the start of separator i.
	starts := []int{0}
	ends := []int{}
	for _, sep := range seps {
		ends = append(ends, sep[0])
		starts = append(starts, sep[1])
	}
	ends = append(ends, len(display))
	strong := func(sep []int) bool {
		text := display[sep[0]:sep[1]]
		return strings.TrimSpace(text) != "-" || text != strings.TrimSpace(text)
	}
	for i := range starts {
		for j := i; j < len(starts); j++ {
			key := authornorm.SearchKey(display[starts[i]:ends[j]])
			if !placeholderKeys[key] {
				continue
			}
			if strings.Contains(key, " ") || (i > 0 && strong(seps[i-1])) || (j < len(seps) && strong(seps[j])) {
				return true
			}
		}
	}
	return false
}

// labelledPlaceholder reports a placeholder with a label, the shapes the
// sources give a genre collection: a run of the display name's pieces
// between separators — at its start («Автор неизвестен -- Кулинария», with
// a hyphen of its own: «Автор-неизвестен — Кулинария», «no-name: Cooking»),
// at its end («Кулинария — автор неизвестен») or between labels — that is a
// whole placeholder, with some other piece beside it; a name part that is a
// placeholder of several words («Автор неизвестен» as the given name,
// whatever follows); or the placeholder with its words the other way round
// («Неизвестен Автор»). A one-word placeholder counts only next to a strong
// separator — a dash, a colon, a bar, a slash or a spaced hyphen — never as
// the end of a compound glued by a plain hyphen: «Неизвестный-Сказочник»,
// «Петров-Неизвестный» and Эрнст Неизвестный are names.
func labelledPlaceholder(c *Credit) bool {
	if placeholderRun(c.DisplayName) {
		return true
	}
	for _, part := range []string{c.Given, c.Family, c.Nickname} {
		if key := authornorm.SearchKey(part); strings.Contains(key, " ") && placeholderKeys[key] {
			return true
		}
	}
	return len(c.Additional) == 0 && c.Family != "" && c.Given != "" &&
		placeholderKeys[authornorm.SearchKey(c.Family+" "+c.Given)]
}

// samizdatMark is the samizdat mark of a title, "[СИ]" (§2.0, decision 3b).
var samizdatMark = regexp.MustCompile(`(?i)\[\s*си\s*\]`)

// isSamizdat reports a title with the samizdat mark.
func isSamizdat(title string) bool { return samizdatMark.MatchString(title) }

// titleKey is the normalized title D3 compares.
func titleKey(title string) string { return authornorm.SearchKey(title) }

// Typo merge bounds (§2.0, P12): the additional parts differ by one or two
// edits and both have at least four letters.
const (
	typoMaxDistance = 2
	typoMinLetters  = 4
)

// typoCandidates reports two additional parts close enough to be one
// patronymic misspelled: within the edit distance, long enough, and not a
// male and a female patronymic ("-ич" and "-на").
func typoCandidates(a, b string) (int, bool) {
	if a == b || utf8.RuneCountInString(a) < typoMinLetters || utf8.RuneCountInString(b) < typoMinLetters {
		return 0, false
	}
	if gender(a) != "" && gender(b) != "" && gender(a) != gender(b) {
		return 0, false
	}
	d := levenshtein(a, b)
	return d, d >= 1 && d <= typoMaxDistance
}

// gender is the grammatical gender a patronymic ending shows, or "".
func gender(additional string) string {
	switch {
	case strings.HasSuffix(additional, "ич"):
		return "m"
	case strings.HasSuffix(additional, "на"):
		return "f"
	}
	return ""
}

// levenshtein is the edit distance of two strings in runes.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
