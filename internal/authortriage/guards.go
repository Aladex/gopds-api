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
// 2). A fix never changes a name that could be right: a person's look-alike
// letters are folded only where the word is a defect for sure, and a dot or
// comma is dropped only where the source set it apart as a word of its
// own. A look-alike word it cannot tell from a real one, or a display that
// ends in a comma, is refused; so is an answer that loses a word of the
// proposal's display, shows a word none of its parts holds, or keeps a role
// as a name.
func storedAnswerGuard(c *Candidate, after *Name, ev *Evidence) Refusal {
	if after.Kind == KindPerson {
		folded, ok := foldNameLookalikes(after, c.Proposal.Display)
		if !ok {
			return RefusedLookalikeWord
		}
		if folded != *after {
			*after = folded
			ev.Fixes = append(ev.Fixes, FixLookalike)
		}
		if cleaned := fixStrayPunctuation(after, c.Proposal.Display); cleaned != *after {
			*after = cleaned
			ev.Fixes = append(ev.Fixes, FixPunctuation)
		}
		if strings.HasSuffix(after.Display, ",") {
			return RefusedStrayPunctuation
		}
	}
	if lostWord(c.Proposal.Display, after) {
		return RefusedLostToken
	}
	if unparsedWord(after) {
		return RefusedUnparsedWord
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
// with the build's look-alike table (authorentity.FoldLookalikeWord), only
// where the word is a defect for sure: a word of both scripts, to the script
// its own letters tell («Аpтуp», «Мантель-Heчко»); a whole word of look-alikes
// of the name's minor script when it is an initial («B.», «C.») or the
// source writes it mixed. Initials of the minor script of which one has no
// look-alike («E. L. Джеймс») are the name's own: ok is false. Any other whole look-alike word («Capa», «Cox»)
// may be a real word of the other script: ok is false, the answer is not
// rewritten. A word with a letter of no look-alike stays («Игорь Numus»,
// «Стогоff»). The display and the sort follow the parts.
func foldNameLookalikes(n *Name, source string) (Name, bool) {
	words := slices.Concat(strings.Fields(n.Given), strings.Fields(n.Additional), strings.Fields(n.Family))
	cyr, lat := scripts(strings.Join(words, " "))
	if cyr == lat {
		return *n, true
	}
	toCyrillic := cyr > lat
	mixedInSource := mixedWordsOf(source)
	repl := map[string]string{}
	foldedInitial, keptInitial := false, false
	for _, w := range words {
		folded, has := authorentity.FoldLookalikeWord(w, foldsTo(w, toCyrillic))
		whole := !mixedScript(w)
		keptInitial = keptInitial || (whole && initial(w) && !has)
		if !has || folded == w {
			continue
		}
		foldedInitial = foldedInitial || (whole && initial(w))
		if whole && !initial(w) && !mixedInSource[compareKey(strings.Trim(w, wordPunctuation))] {
			return *n, false
		}
		repl[w] = folded
	}
	if foldedInitial && keptInitial {
		return *n, false
	}
	out := *n
	out.Given, out.Additional, out.Family = replaceWords(n.Given, repl), replaceWords(n.Additional, repl),
		replaceWords(n.Family, repl)
	out.Display, out.Sort = replaceWords(n.Display, repl), replaceWords(n.Sort, repl)
	return out, true
}

// foldsTo is the script a word folds to: a mixed word's own, told by its
// letters, unless they tie; else the name's (toCyrillic).
func foldsTo(w string, toCyrillic bool) bool {
	if mixedScript(w) {
		if wc, wl := scripts(w); wc != wl {
			return wc > wl
		}
	}
	return toCyrillic
}

// mixedWordsOf are the words (compareKey) the source writes in both scripts.
func mixedWordsOf(source string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.Fields(source) {
		if mixedScript(t) {
			out[compareKey(strings.Trim(t, wordPunctuation))] = true
		}
	}
	return out
}

// mixedScript reports a word with letters of both scripts.
func mixedScript(w string) bool {
	return strings.ContainsFunc(w, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }) &&
		strings.ContainsFunc(w, func(r rune) bool { return unicode.Is(unicode.Latin, r) })
}

// initial reports one letter and a dot («B.»).
func initial(w string) bool {
	core, dotted := strings.CutSuffix(w, ".")
	return dotted && utf8.RuneCountInString(core) == 1
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

// fixStrayPunctuation drops the dots and commas the source set apart as
// words of their own («ЧАРЛЗ . МЕССЕНДЖЕР», «Роберт , Блох»): such a mark as
// a word, and the same mark glued to the name word the source wrote it
// after («Чарлз. Мессенджер», «Роберт, Блох»). Any other mark stays: an
// abbreviation («Влад.», «Dr. med.»), an initial («J-P.»), the author's own
// comma («James Wesley, Rawles»). The sort keeps its comma after the sort
// key.
func fixStrayPunctuation(n *Name, source string) Name {
	stray := strayAfter(source)
	if len(stray) == 0 {
		return *n
	}
	out := *n
	out.Given, out.Additional, out.Family = cleanMarks(n.Given, n, stray, false),
		cleanMarks(n.Additional, n, stray, false), cleanMarks(n.Family, n, stray, false)
	out.Display, out.Sort = cleanMarks(n.Display, n, stray, false), cleanMarks(n.Sort, n, stray, true)
	return out
}

// marksOnly reports a token of dots and commas only.
func marksOnly(t string) bool {
	return strings.Trim(t, ".,") == ""
}

// strayAfter are the words (compareKey) the source wrote a dot or comma
// after as a word of its own.
func strayAfter(source string) map[string]bool {
	out := map[string]bool{}
	tokens := strings.Fields(source)
	for i := 1; i < len(tokens); i++ {
		if !marksOnly(tokens[i]) || marksOnly(tokens[i-1]) {
			continue
		}
		if words := compareWords(tokens[i-1]); len(words) > 0 {
			out[words[len(words)-1]] = true
		}
	}
	return out
}

// cleanMarks drops from s the marks-only words and the one mark glued to a
// word of stray («Б..» keeps its initial's dot). A comma before a suffix stays («Иванов, мл.»); in a sort
// (sort) only the last word's comma may go: the others are the sort key's.
func cleanMarks(s string, n *Name, stray map[string]bool, sort bool) string {
	var tokens []string
	for _, t := range strings.Fields(s) {
		if !marksOnly(t) {
			tokens = append(tokens, t)
		}
	}
	for i, t := range tokens {
		if !strings.HasSuffix(t, ".") && !strings.HasSuffix(t, ",") {
			continue
		}
		core := t[:len(t)-1]
		if words := compareWords(core); len(words) == 0 || !stray[words[len(words)-1]] {
			continue
		}
		last := i == len(tokens)-1
		if strings.HasSuffix(t, ",") && !last && (sort || suffixWord(tokens[i+1], n)) {
			continue
		}
		tokens[i] = core
	}
	return strings.Join(tokens, " ")
}

// suffixes are the generational suffixes a comma may set off («Иванов,
// мл.»).
var suffixes = setOf("мл", "ст", "младший", "старший", "jr", "sr", "junior", "senior", "ii", "iii", "iv")

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

// compareWords are the words of s as the lost-word test compares them
// (compareKey).
func compareWords(s string) []string {
	words := rawWords(s)
	for i, w := range words {
		words[i] = compareKey(w)
	}
	return words
}

// rawWords are the runs of letters and digits of s.
func rawWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// compareKey is a word in capitals, its look-alike letters read as
// Cyrillic, «Ё» as «Е», a Latin or Ukrainian «I» as «И».
func compareKey(w string) string {
	var b strings.Builder
	for _, r := range w {
		r = unicode.ToUpper(r)
		if folded, ok := authorentity.FoldLookalikeWord(string(r), true); ok {
			r, _ = utf8.DecodeRuneInString(folded)
		}
		if to, ok := compareLetters[r]; ok {
			r = to
		}
		b.WriteRune(r)
	}
	return b.String()
}

// compareLetters are the letters the lost-word test reads as one.
var compareLetters = map[rune]rune{'Ё': 'Е', 'I': 'И', 'І': 'И'}

// wordSet is the compared words of s.
func wordSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range compareWords(s) {
		set[w] = true
	}
	return set
}

// quoteMarks are the parentheses and quotes that set a nickname apart
// («Мария Капилина (Пичугина)», «Хардин Джесси «Волк»»).
const quoteMarks = "()«»\"„“”"

// lostWord reports a word of the proposal's display that the answer's
// display and prefix do not show. A role or clerical title may go
// («Рафаил Нойка иеромонах»), and so may the word of a one-word nickname
// beside a display of two words or more, when the proposal sets nothing
// apart in parentheses or quotes and the nickname is no bare number
// («Руслан Балабеков Lorgar666»; not «Евгений Александров 2»).
func lostWord(source string, after *Name) bool {
	have := wordSet(after.Display + " " + after.Prefix)
	var nickname map[string]bool
	if len(strings.Fields(after.Nickname)) == 1 && len(strings.Fields(after.Display)) >= 2 &&
		!strings.ContainsAny(source, quoteMarks) && strings.ContainsFunc(after.Nickname, unicode.IsLetter) {
		nickname = wordSet(after.Nickname)
	}
	tokens := strings.Fields(source)
	for i, t := range tokens {
		afterComma := i > 0 && strings.HasSuffix(tokens[i-1], ",")
		words := rawWords(t)
		for _, w := range words {
			key := compareKey(w)
			if have[key] || nickname[key] || clericalTitles[strings.ToLower(w)] ||
				(len(words) == 1 && roleToken(t, afterComma)) {
				continue
			}
			return true
		}
	}
	return false
}

// unparsedWord reports a person answer with name parts whose display or
// sort shows a word none of the parts holds («Kelley St. John» with the
// family «John»). A name with no parts, a mononym's display only, is not
// read.
func unparsedWord(n *Name) bool {
	if n.Kind != KindPerson || strings.TrimSpace(n.Given+n.Additional+n.Family+n.Nickname) == "" {
		return false
	}
	parts := wordSet(strings.Join([]string{n.Given, n.Additional, n.Family, n.Nickname, n.Prefix, n.Suffix}, " "))
	return slices.ContainsFunc(compareWords(n.Display+" "+n.Sort), func(w string) bool { return !parts[w] })
}

// roleKey is a word as the role lists hold it.
func roleKey(w string) string {
	return strings.ToLower(strings.Trim(w, wordPunctuation))
}

// roleToken reports a token that names a role: a role word, or a role's
// short form written as one, with its dot, in parentheses or after a comma
// («ed.», «(ред)», «, eds»). A bare short form is a name («Ed McBain»).
func roleToken(t string, afterComma bool) bool {
	key := roleKey(t)
	if roleWords[key] {
		return true
	}
	return roleAbbreviations[key] &&
		(strings.HasSuffix(strings.TrimRight(t, ")"), ".") || strings.HasPrefix(t, "(") || afterComma)
}

// roleAsName reports a person whose given or additional names hold a role
// or a clerical title, or whose prefix is a role or a title written in
// capitals («ИЕРОМОНАХ»; not «DJ»). A family name may be one («Евгений
// Монах»).
func roleAsName(n *Name) bool {
	if n.Kind != KindPerson {
		return false
	}
	for _, w := range strings.Fields(n.Given + " " + n.Additional) {
		if roleToken(w, false) || clericalTitles[roleKey(w)] {
			return true
		}
	}
	return allCaps(n.Prefix, capsPrefixLetters, 0) && slices.ContainsFunc(strings.Fields(n.Prefix), func(w string) bool {
		k := roleKey(w)
		return roleWords[k] || roleAbbreviations[k] || clericalTitles[k]
	})
}

// personRoleIn reports a person's role among the words of a collective's
// display («Shane McKenzie, ed.»).
func personRoleIn(display string) bool {
	tokens := strings.Fields(display)
	for i, t := range tokens {
		if roleToken(t, i > 0 && strings.HasSuffix(tokens[i-1], ",")) {
			return true
		}
	}
	return false
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
		return handleNames[w] || slices.ContainsFunc(handleStems, func(s string) bool { return strings.HasPrefix(w, s) })
	})
}

// shortCaps reports a handle of one to five letters, all capitals («РПЦ»,
// «ИГСО», «VS»).
func shortCaps(display string) bool {
	return allCaps(display, 1, shortCapsLetters)
}
