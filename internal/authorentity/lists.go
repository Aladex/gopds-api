package authorentity

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
)

// Lists of names in the fallback step (E4q priority). A source writes
// several authors in one field: «Александр Силецкий, Эзиз Овезов, …,
// Станислав Лем, …». Read as written that is one person. Before the step
// reads a credit:
//   - a suffix or title after a comma («Robert Ladner, Jr.», «Вениамин,
//     еп.») stays with the name before it, and a part of years
//     («Galsworthy, John, 1867-1933») is dropped;
//   - two parts «Family, Given», the family one capitalized word and the
//     given part one to three capitalized words or initials («Feifer,
//     George», «Bolton, S. J.»), are one person, read as Given Family;
//   - parts that are each a person's name — two to four capitalized words
//     (a particle «де», «фон», «van» in lower case), no word of an
//     organization, no digit, no address — at least half of them with a
//     given name the catalog knows or an initial, are a list: a segment per
//     person, each a row of the credit (listed), joining the one entity of
//     its name or an entity of its own;
//   - anything else (an organization with a comma, «Общество Сторожевой
//     Башни, Библий и трактатов Пенсильвании») is read as before.

// RuleListed is the rule of a segment of a list that names an entity of
// its own.
const RuleListed = "listed"

// listSeparator splits a list of names; commaSeparator the two parts of
// «Family, Given».
var (
	listSeparator  = regexp.MustCompile(`\s*[,;]\s*|\s+(?:и|&|and)\s+`)
	commaSeparator = regexp.MustCompile(`\s*,\s*`)
)

// listSuffix is a part that belongs to the name before it.
var listSuffix = regexp.MustCompile(
	`(?i)^(jr|sr|junior|senior|мл|ст|младший|старший|ii|iii|iv|еп|прот|свящ|фон|von|van|де|de|ван|дер|der)\.?$`)

// nameParticles are the lower-case words a person's name has.
var nameParticles = map[string]bool{
	"де": true, "фон": true, "ван": true, "дер": true, "дю": true, "ди": true, "да": true, "ла": true, "ле": true,
	"де ла": true, "бен": true, "ибн": true, "аль": true, "эль": true, "de": true, "von": true, "van": true,
	"der": true, "den": true, "du": true, "di": true, "da": true, "del": true, "la": true, "le": true, "bin": true,
}

// gluedInitials are initials written onto the family («П.И.Бакулин»).
var gluedInitials = regexp.MustCompile(`^((?:\p{L}\.){1,3})(\p{L}{2,}.*)$`)

// spaceInitials writes a part's glued initials apart: «П.И. Бакулин».
func spaceInitials(part string) string {
	words := strings.Fields(part)
	for i, w := range words {
		if m := gluedInitials.FindStringSubmatch(w); m != nil {
			words[i] = m[1] + " " + m[2]
		}
	}
	return strings.Join(words, " ")
}

// listYears is a part of years or dates alone.
var listYears = regexp.MustCompile(`^[\d\s.\-–—?]+$`)

// listParts are the parts of a display name split at sep, a suffix kept
// with the part before it and a part of years dropped.
func listParts(display string, sep *regexp.Regexp) []string {
	var out []string
	for _, p := range sep.Split(display, -1) {
		p = spaceInitials(strings.TrimSpace(strings.Trim(p, " .")))
		switch {
		case p == "" || listYears.MatchString(p):
			continue
		case listSuffix.MatchString(p) || nameTitles[strings.ToLower(strings.TrimSuffix(p, "."))]:
			if len(out) > 0 {
				out[len(out)-1] += " " + p
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// initialWord reports a word of initials: one or two letters with or
// without dots («Д.», «Б.Л», «J.»).
func initialWord(w string) bool {
	letters := strings.ReplaceAll(w, ".", "")
	return letters != "" && utf8.RuneCountInString(letters) <= 2 && strings.IndexFunc(letters, func(r rune) bool {
		return !unicode.IsLetter(r)
	}) < 0
}

// personPart reports a part shaped as a person's name (see above), and
// whether it has a given name the catalog knows or an initial.
func personPart(part string, g givenNames) (shaped, named bool) {
	words := strings.Fields(part)
	if len(words) < minD2bWords || len(words) > maxListWords || strings.Contains(part, "://") ||
		strings.ContainsAny(part, "@«»\"") {
		return false, false
	}
	nameWords := 0
	for _, w := range words {
		first, _ := utf8.DecodeRuneInString(w)
		if nameParticles[w] {
			continue
		}
		nameWords++
		if !unicode.IsUpper(first) || strings.ContainsFunc(w, unicode.IsDigit) ||
			organizationWords[strings.ToLower(strings.Trim(w, ".,"))] {
			return false, false
		}
		named = named || initialWord(w) || g.known(strings.TrimSuffix(w, "."))
	}
	// «де Ришелье» is the end of a name, not a name: «Арман Жан, де
	// Ришелье» is one person.
	return nameWords >= minD2bWords, named
}

// maxListWords is the most words a person's part of a list has.
const maxListWords = 4

// familyGiven is the name of two parts «Family, Given» read as Given
// Family; ok is false for any other shape.
func familyGiven(parts []string) (given string, additional []string, family string, ok bool) {
	if len(parts) != minGroup || len(strings.Fields(parts[0])) != 1 || initialWord(parts[0]) || !capitalized(parts[0]) {
		return "", nil, "", false
	}
	words := strings.Fields(parts[1])
	if len(words) == 0 || len(words) > maxListWords-1 {
		return "", nil, "", false
	}
	for _, w := range words {
		if !initialWord(w) && !capitalized(w) {
			return "", nil, "", false
		}
	}
	return words[0], words[1:], parts[0], true
}

// capitalized reports a word of letters (a hyphen or an apostrophe inside)
// that starts with a capital.
func capitalized(w string) bool {
	first, _ := utf8.DecodeRuneInString(w)
	return unicode.IsUpper(first) && !strings.ContainsFunc(w, func(r rune) bool {
		return !unicode.IsLetter(r) && r != '-' && r != '\'' && r != '’'
	})
}

// knownGivenNames are the given names of the selected credits, kept from
// the unselected read when it made them.
func (b *builder) knownGivenNames() givenNames {
	if b.givenKnown == nil {
		b.givenKnown = givenNames{}
		for i := range b.in.Credits[:b.selected] {
			b.givenKnown.add(&b.in.Credits[i])
		}
	}
	return b.givenKnown
}

// readListed is how the fallback step reads a credit before anything else:
// a «Family, Given» rewritten as one person (one = true), or a list's
// segments as people's results (len(segments) > 1); neither for any other
// credit.
func (b *builder) readListed(c *Credit) (one bool, segments []*Result) {
	if c.DisplayName == "" || organizationName(c.DisplayName) {
		return false, nil
	}
	g := b.knownGivenNames()
	if given, additional, family, ok := familyGiven(listParts(c.DisplayName, commaSeparator)); ok {
		res := *c.Result
		res.Kind, res.Given, res.Additional, res.Family = KindPerson, given, additional, family
		res.Nickname, res.Suffix = "", ""
		res.DisplayName = strings.Join(append(append([]string{given}, additional...), family), " ")
		res.SortName = res.DisplayName
		c.Result = &res
		return true, nil
	}
	parts := listParts(c.DisplayName, listSeparator)
	if len(parts) < minGroup {
		return false, nil
	}
	named := 0
	for _, p := range parts {
		shaped, known := personPart(p, g)
		if !shaped {
			return false, nil
		}
		if known {
			named++
		}
	}
	// A pair of shaped names is two people even when the catalog knows
	// neither given name («Дженика Шоу, Сем Крезент»); three or more parts
	// still need a known name in half of them.
	if 2*named < len(parts) && !(len(parts) == minGroup && strings.Contains(c.DisplayName, ",")) {
		return false, nil
	}
	for _, p := range parts {
		words := strings.Fields(p)
		res := &Result{ResultID: c.ResultID, Kind: KindPerson, Given: words[0], Family: words[len(words)-1],
			DisplayName: p, SortName: p, Script: string(authornorm.DetectScript(p))}
		if middle := words[1 : len(words)-1]; len(middle) > 0 {
			res.Additional = []string{strings.Join(middle, " ")}
		}
		segments = append(segments, res)
	}
	return false, segments
}
