package authortriage

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"
)

// The guards of triage-v2 (design/review-triage/STEP-V2.md), from the
// error shapes of the triage-v1 audit (design/review-triage/audit). A guard
// runs after its step holds: it fixes the name the step gives when the fix
// is deterministic, or refuses it, and a refused item is left for the
// people with the reason.

// heavyHandleCredits is how many credits make a kept handle a person's
// decision (step 7).
const heavyHandleCredits = 20

// capsPrefixLetters is how many capitals make a prefix written in capitals
// («ИЕРОМОНАХ»); shortCapsLetters the most a handle of capitals an
// abbreviation has («РПЦ», «ИГСО»).
const (
	capsPrefixLetters = 2
	shortCapsLetters  = 5
)

// storedAnswerGuard guards a stored answer the judges agree on (steps 1 and
// 2). A person's look-alike letters are folded and a stray dot or comma
// dropped; then the answer is refused when it loses a word of the
// proposal's display, or keeps a role as a name.
func storedAnswerGuard(c *Candidate, after *Name, ev *Evidence) Refusal {
	if after.Kind == KindPerson {
		if folded := foldNameLookalikes(after); folded != *after {
			*after = folded
			ev.Fixes = append(ev.Fixes, FixLookalike)
		}
		if cleaned := fixStrayPunctuation(after); cleaned != *after {
			*after = cleaned
			ev.Fixes = append(ev.Fixes, FixPunctuation)
		}
	}
	if lostWord(c.Proposal.Display, after) {
		return RefusedLostToken
	}
	if roleAsName(after) {
		return RefusedRoleAsName
	}
	return ""
}

// collectiveGuard guards a collective of steps 5 and 6: a placeholder with
// more after it, a list of genres, one person with titles and a person's
// role are no collective; with titleShapes (step 6) neither is a pair whose
// words are not names.
func collectiveGuard(titleShapes bool) guard {
	return func(_ *Candidate, after *Name, _ *Evidence) Refusal {
		if after.Kind != KindCollective {
			return ""
		}
		d := after.Display
		switch {
		case placeholderHead(d):
			return RefusedPlaceholderTitle
		case genreList(d):
			return RefusedGenreList
		case onePersonTitles(d):
			return RefusedOnePersonTitles
		case personRoleIn(d):
			return RefusedRoleAsName
		case titleShapes && titleShape(d):
			return RefusedTitleShape
		}
		return ""
	}
}

// keepHandleGuard guards a kept handle (step 7): a name on the list of
// organizations, magazines and titles, an abbreviation in capitals, and a
// handle on many credits go to the people.
func keepHandleGuard(c *Candidate, after *Name, _ *Evidence) Refusal {
	switch {
	case listedHandle(after.Display):
		return RefusedListedName
	case shortCaps(after.Display):
		return RefusedShortCaps
	case c.Credits >= heavyHandleCredits:
		return RefusedHeavy
	}
	return ""
}

// scripts counts the Cyrillic and Latin letters of s that have no
// look-alike in the other script: the letters that tell the script. When
// every letter has one («Capa»), it counts them all.
func scripts(s string) (cyr, lat int) {
	var allCyr, allLat int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			allCyr++
			if _, alike := authorentity.FoldLookalikeWord(string(r), false); !alike {
				cyr++
			}
		case unicode.Is(unicode.Latin, r):
			allLat++
			if _, alike := authorentity.FoldLookalikeWord(string(r), true); !alike {
				lat++
			}
		}
	}
	if cyr == 0 && lat == 0 {
		return allCyr, allLat
	}
	return cyr, lat
}

// foldNameLookalikes folds the look-alike letters of a person's name parts
// with the build's look-alike table (authorentity.FoldLookalikeWord): a
// word of both scripts to the script its own letters tell, a word all of
// look-alikes («Capa», «B.») to the script the name's letters tell. A real
// word of the other script («Игорь Numus») stays, and so does a word of
// both scripts with a letter of no look-alike («Стогоff»). The display and
// the sort follow the parts.
func foldNameLookalikes(n *Name) Name {
	words := slices.Concat(strings.Fields(n.Given), strings.Fields(n.Additional), strings.Fields(n.Family))
	cyr, lat := scripts(strings.Join(words, " "))
	if cyr == lat {
		return *n
	}
	toCyrillic := cyr > lat
	repl := map[string]string{}
	for _, w := range words {
		wc, wl := scripts(w)
		to := toCyrillic
		if wc > 0 && wl > 0 && wc != wl {
			to = wc > wl
		}
		if folded, has := authorentity.FoldLookalikeWord(w, to); has && folded != w {
			repl[w] = folded
		}
	}
	out := *n
	out.Given, out.Additional, out.Family = replaceWords(n.Given, repl), replaceWords(n.Additional, repl),
		replaceWords(n.Family, repl)
	out.Display, out.Sort = replaceWords(n.Display, repl), replaceWords(n.Sort, repl)
	return out
}

// wordPunctuation is what may stand around a name word in a display or a
// sort.
const wordPunctuation = ".,;:()[]«»\"'"

// replaceWords replaces each space-separated word of s found in repl, as
// it is or within its punctuation («Мантель-Heчко,»).
func replaceWords(s string, repl map[string]string) string {
	if len(repl) == 0 || s == "" {
		return s
	}
	tokens := strings.Split(s, " ")
	for i, t := range tokens {
		if to, ok := repl[t]; ok {
			tokens[i] = to
			continue
		}
		core := strings.Trim(t, wordPunctuation)
		if to, ok := repl[core]; ok && core != "" {
			tokens[i] = strings.Replace(t, core, to, 1)
		}
	}
	return strings.Join(tokens, " ")
}

// fixStrayPunctuation drops a person's stray dots and commas: a part that
// is only a mark, a comma after a name word that no suffix follows, a dot
// after a word that is no initial and no abbreviation («Чарлз. Мессенджер»,
// «Роберт, Блох», «Иван Трушков.»). The sort keeps its comma after the
// sort key.
func fixStrayPunctuation(n *Name) Name {
	out := *n
	out.Given, out.Additional, out.Family = cleanPart(n.Given), cleanPart(n.Additional), cleanPart(n.Family)
	out.Display, out.Sort = cleanDisplay(n.Display, n, false), cleanDisplay(n.Sort, n, true)
	return out
}

// marksOnly reports a token of dots and commas only.
func marksOnly(t string) bool {
	return strings.Trim(t, ".,") == ""
}

func cleanPart(s string) string {
	var out []string
	for _, w := range strings.Fields(s) {
		if marksOnly(w) {
			continue
		}
		w = strings.TrimRight(w, ",")
		if strings.HasSuffix(w, ".") && strayDot(w) {
			w = strings.TrimRight(w, ".")
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// cleanDisplay cleans a display or, with sort, a sort name: there a comma
// before the last word is the sort key's.
func cleanDisplay(s string, n *Name, sort bool) string {
	var tokens []string
	for _, t := range strings.Fields(s) {
		if !marksOnly(t) {
			tokens = append(tokens, t)
		}
	}
	for i, t := range tokens {
		last := i == len(tokens)-1
		if strings.HasSuffix(t, ",") && (last || (!sort && !suffixWord(tokens[i+1], n))) {
			t = strings.TrimRight(t, ",")
		}
		if strings.HasSuffix(t, ".") && strayDot(t) {
			t = strings.TrimRight(t, ".")
		}
		tokens[i] = t
	}
	return strings.Join(tokens, " ")
}

// abbreviations are the shortened words a dot ends in a name: generational
// suffixes, honorifics, clerical titles and the roles a file writes.
var abbreviations = setOf("мл", "ст", "jr", "sr", "св", "свт", "прп", "прот", "свящ", "иером", "игум", "архиеп",
	"еп", "митр", "проф", "акад", "д-р", "dr", "prof", "rev", "st", "mr", "mrs", "ms", "doc", "ed", "eds", "ред",
	"сост", "пер", "ил", "илл", "бл", "блж", "ген", "кн", "гр")

// suffixes are the generational suffixes a comma may set off («Иванов,
// мл.»).
var suffixes = setOf("мл", "ст", "младший", "старший", "jr", "sr", "junior", "senior", "ii", "iii", "iv")

// strayDot reports a dot that ends a word (w ends in it) which is no
// initial («K.», «Дж.», «Ж.-П.») and no abbreviation («мл.», «doc.»,
// «Онкен-мл.»).
func strayDot(w string) bool {
	core := strings.TrimRight(w, ".")
	if strings.Contains(core, ".") || utf8.RuneCountInString(core) <= 2 {
		return false
	}
	lower := strings.ToLower(core)
	if abbreviations[lower] {
		return false
	}
	if i := strings.LastIndex(lower, "-"); i >= 0 && abbreviations[lower[i+1:]] {
		return false
	}
	return true
}

// suffixWord reports a word that is a generational suffix or the name's
// suffix.
func suffixWord(w string, n *Name) bool {
	core := strings.Trim(w, wordPunctuation)
	if suffixes[strings.ToLower(core)] {
		return true
	}
	first := strings.Fields(n.Suffix)
	return len(first) > 0 && strings.Trim(first[0], wordPunctuation) == core
}

// compareWords are the words of s as the lost-word test compares them:
// letters and digits only, in capitals, look-alike letters read as
// Cyrillic, «Ё» as «Е», a Latin or Ukrainian «I» as «И».
func compareWords(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		r = unicode.ToUpper(r)
		if folded, ok := authorentity.FoldLookalikeWord(string(r), true); ok {
			r, _ = utf8.DecodeRuneInString(folded)
		}
		if to, ok := compareLetters[r]; ok {
			r = to
		}
		b.WriteRune(r)
	}
	flush()
	return out
}

// compareLetters are the letters the lost-word test reads as one.
var compareLetters = map[rune]rune{'Ё': 'Е', 'I': 'И', 'І': 'И'}

// lostWord reports a word of the proposal's display that the answer's
// display does not show, unless the answer moved it to the prefix.
func lostWord(source string, after *Name) bool {
	have := map[string]bool{}
	for _, w := range compareWords(after.Display + " " + after.Prefix) {
		have[w] = true
	}
	return slices.ContainsFunc(compareWords(source), func(w string) bool { return !have[w] })
}

// roleKey is a word as the role lists hold it.
func roleKey(w string) string {
	return strings.ToLower(strings.Trim(w, wordPunctuation))
}

// roleAsName reports a person whose given or additional names hold a role
// or a clerical title, or whose prefix is written in capitals. A family name
// may be one («Евгений Монах»).
func roleAsName(n *Name) bool {
	if n.Kind != KindPerson {
		return false
	}
	for _, w := range strings.Fields(n.Given + " " + n.Additional) {
		if personRoles[roleKey(w)] || clericalTitles[roleKey(w)] {
			return true
		}
	}
	return allCaps(n.Prefix, capsPrefixLetters, 0)
}

// personRoleIn reports a person's role among the words of a collective's
// display («Shane McKenzie, ed.»).
func personRoleIn(display string) bool {
	return slices.ContainsFunc(strings.Fields(display), func(w string) bool { return personRoles[roleKey(w)] })
}

// allCaps reports s with at least least letters, at most most (0: any), all
// of them capitals.
func allCaps(s string, least, most int) bool {
	letters := 0
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		if !unicode.IsUpper(r) {
			return false
		}
		letters++
	}
	return letters >= least && (most == 0 || letters <= most)
}

// keyWords are the words of a display's search key.
func keyWords(display string) []string {
	return strings.Fields(authornorm.SearchKey(display))
}

// placeholderHead reports a display that begins with a placeholder phrase
// of more than one word and goes on («Автор неизвестен -- Эпосы, …»). The
// placeholder alone is step 5's unknown.
func placeholderHead(display string) bool {
	key := displayKey(display)
	return slices.ContainsFunc(placeholderHeads, func(p string) bool { return strings.HasPrefix(key, p+" ") })
}

// placeholderHeads are the placeholder phrases of more than one word.
var placeholderHeads = func() []string {
	var out []string
	for _, set := range []map[string]bool{placeholderKeys, placeholderPhrasesV1} {
		for p := range set {
			if strings.Contains(p, " ") {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}()

// quotedTitle reports a display with a title in quotes: the name of a
// magazine or a publisher, whatever the title says.
func quotedTitle(display string) bool {
	return strings.ContainsAny(display, "«»\"„“”")
}

// genreList reports a display with a genre or a kind of book in it
// («Эпосы, мифы, легенды и сказания», «[ сборник ]»), unless it names a
// title in quotes.
func genreList(display string) bool {
	if quotedTitle(display) {
		return false
	}
	return slices.ContainsFunc(keyWords(display), func(w string) bool {
		return genreWords[w] || slices.ContainsFunc(genreStems, func(s string) bool { return strings.HasPrefix(w, s) })
	})
}

// conjunction reports «и» or its like between two names.
func conjunction(w string) bool {
	return conjunctions[strings.ToLower(w)]
}

// onePersonTitles reports one person with titles joined by «и» («Давид Царь
// и Пророк», «Христофор Греческий и Датский»): both words beside the
// conjunction are titles.
func onePersonTitles(display string) bool {
	tokens := strings.Fields(display)
	for i := 1; i+1 < len(tokens); i++ {
		if conjunction(tokens[i]) && titleWord(tokens[i-1]) && titleWord(tokens[i+1]) {
			return true
		}
	}
	return false
}

// titleWord reports a title noun or a capitalized adjective of a land
// («Греческий», «Датский»).
func titleWord(w string) bool {
	core := strings.Trim(w, wordPunctuation)
	lower := strings.ToLower(core)
	if titleNouns[lower] {
		return true
	}
	first, _ := utf8.DecodeRuneInString(core)
	return unicode.IsUpper(first) &&
		slices.ContainsFunc(landSuffixes, func(s string) bool { return strings.HasSuffix(lower, s) })
}

// titleShape reports a pair that is no two people: a display with «и»
// whose words are not all names, a lower-case word beside the few a pair
// writes («и др.», «братья», «фон»). An organization or a collective phrase
// is not read.
func titleShape(display string) bool {
	if collectiveName(display) || collectiveMarker(display) {
		return false
	}
	tokens := strings.Fields(display)
	if !slices.ContainsFunc(tokens, conjunction) {
		return false
	}
	return slices.ContainsFunc(tokens, func(t string) bool {
		core := strings.Trim(t, wordPunctuation)
		first, _ := utf8.DecodeRuneInString(core)
		return unicode.IsLetter(first) && unicode.IsLower(first) && !conjunction(core) &&
			!pairWords[strings.ToLower(core)]
	})
}

// collectiveMarker reports a display with a word that marks a collective
// the organization test does not read («Коллектив авторов -- …»,
// «Литагент 2 редакция»).
func collectiveMarker(display string) bool {
	return slices.ContainsFunc(keyWords(display), func(w string) bool {
		return slices.ContainsFunc(collectiveStems, func(s string) bool { return strings.HasPrefix(w, s) })
	})
}

// listedHandle reports a one-word handle on the list of organizations,
// magazines, titles and placeholder words.
func listedHandle(display string) bool {
	return slices.ContainsFunc(keyWords(display), func(w string) bool {
		return slices.ContainsFunc(handleStems, func(s string) bool { return strings.HasPrefix(w, s) })
	})
}

// shortCaps reports a handle of one to five letters, all capitals («РПЦ»,
// «ИГСО», «VS»).
func shortCaps(display string) bool {
	return allCaps(display, 1, shortCapsLetters)
}
