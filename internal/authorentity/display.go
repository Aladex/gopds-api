package authorentity

import (
	"slices"
	"strings"
	"unicode"

	"gopds-api/internal/authornorm"
)

// The name an entity is shown under (E4i): the reader's, chosen among its
// credits' written forms — never assembled, never new letters — by their
// books, not by the result they come from:
//
//  1. its Wikidata item's Russian label when the label is one of its full
//     given-first forms (an English label is no reason to show Latin);
//  2. else, when its Cyrillic forms hold a tenth of its books at least
//     (minCyrillicShare), its best form in a Russian spelling, when it is
//     of no worse a shape than its best Latin form (with no Latin form,
//     step 3 takes the best Cyrillic one)
//     («Галина Тарасюк» over «Галина Тимофіївна Тарасюк», «Ридли Пирсон»
//     over «Ридли Пиърсън», «Роберт Силверберг» over «Robert Silverberg»,
//     «Р. А. Салваторе» over «R. A. Salvatore»; «Mark Twain» stays beside
//     «Марк Твэн» on two of 55);
//  3. else, when its Cyrillic forms hold as many books as its Latin ones, its
//     best Cyrillic form: full given-first (another Cyrillic spelling), then
//     given-first initials, then surname-first — another spelling never
//     beats Latin but by books («Harry Harrison» stays beside «Хари
//     Харисън»);
//  4. else its best Latin form, in the same order of shapes;
//  5. else its form of the most books, whatever its script or order.
//
// Names that differ only in dots, commas, hyphens and spaces are one form,
// shown in its best punctuated spelling («Кэтрин Л. Мур», not «Кэтрин Л
// Мур»); spellings of no fault apart keep the matching name's, so a hyphen
// is never added or dropped («Артур Конан Дойль», «Исаак Башевис-Зингер»).
//
// A scrambled form — given-first, its family name not its last word
// («Томас Рид Майн», «Роберт Ховард Эрвин») — takes part in steps 1–4 never:
// it is shown only when no other form is.
//
// A tie goes to the fuller form, then to the form of the lowest result id
// (the old rule's last word: «Толстой» before «ТОЛСТОЙ» as before). A
// collective keeps the matching name. The item read is the one the matching
// name names, so the name shown never decides a link; and the matching name
// (matchName) is what every key, candidate and link reads.

// shownForm is one written form of an entity: its credits' display name,
// their books and its shape.
type shownForm struct {
	name                  string
	credit                *Credit
	books                 map[int64]bool
	script                string
	initials, familyFirst bool
	scrambled             bool
	russian               bool
}

// minCyrillicShare is how many of an entity's books, one in it, its
// Cyrillic forms must hold for a Russian full form to beat a Latin one
// (step 2): one in ten.
const minCyrillicShare = 10

// full reports a full given-first form, well ordered.
func (f *shownForm) full() bool { return !f.initials && !f.familyFirst && !f.scrambled }

// shape ranks a form's shape, lower first: full given-first, given-first
// initials, surname-first full, surname-first initials.
func (f *shownForm) shape() int {
	n := 0
	if f.familyFirst {
		n += 2
	}
	if f.initials {
		n++
	}
	return n
}

// display is the name an entity of credits is shown under (see above).
func (b *builder) display(credits []int) EntityRow {
	match := b.matchName(credits)
	if match.Kind == KindCollective {
		return match
	}
	forms := b.shownForms(credits, match.DisplayName)
	if len(forms) == 0 {
		return match
	}
	best := b.labelForm(match.DisplayName, forms)
	if best == nil {
		best = bestForm(forms)
	}
	return EntityRow{Kind: match.Kind, DisplayName: best.name, SortName: best.credit.SortName,
		SearchKey: authornorm.SearchKey(best.name)}
}

// spelling is one written name of a form: its credit of the lowest result
// id and its books.
type spelling struct {
	credit *Credit
	books  map[int64]bool
}

// shownForms are the person credits' written forms: names that differ only
// in dots, commas, hyphens and spaces are one form, its books theirs
// together, its name the best punctuated of them (punctuated).
func (b *builder) shownForms(credits []int, match string) []*shownForm {
	byForm := map[string]map[string]*spelling{}
	books := map[string]map[int64]bool{}
	for _, ci := range credits {
		c := &b.in.Credits[ci]
		if c.Kind == KindCollective || c.DisplayName == "" {
			continue
		}
		k := punctuationKey(c.DisplayName)
		if byForm[k] == nil {
			byForm[k], books[k] = map[string]*spelling{}, map[int64]bool{}
		}
		s := byForm[k][c.DisplayName]
		if s == nil {
			s = &spelling{credit: c, books: map[int64]bool{}}
			byForm[k][c.DisplayName] = s
		}
		if c.ResultID < s.credit.ResultID {
			s.credit = c
		}
		s.books[c.BookID] = true
		books[k][c.BookID] = true
	}
	out := make([]*shownForm, 0, len(byForm))
	for _, k := range sortedKeys(byForm) {
		c := punctuated(byForm[k], match)
		key := keyOf(c)
		out = append(out, &shownForm{name: c.DisplayName, credit: c, books: books[k], script: pureScript(c.DisplayName),
			initials: key.initials() || hasInitial(c.DisplayName), familyFirst: familyFirst(c),
			scrambled: scrambled(c), russian: !otherCyrillic(c.DisplayName)})
	}
	return out
}

// punctuationKey is a name without its dots, commas and hyphens, its spaces
// collapsed: the letters, in their case, that a form is written in.
func punctuationKey(name string) string {
	return strings.Join(strings.FieldsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || r == '.' || r == ',' || unicode.Is(unicode.Pd, r)
	}), " ")
}

// punctuated is the credit of a form's best punctuated spelling: the fewest
// punctuation faults, then the matching name's spelling, then the most
// books, then the lowest result id.
func punctuated(spellings map[string]*spelling, match string) *Credit {
	var best *spelling
	for _, name := range sortedKeys(spellings) {
		if s := spellings[name]; best == nil || betterPunctuated(s, best, match) {
			best = s
		}
	}
	return best.credit
}

// betterPunctuated reports spelling x before y (see punctuated).
func betterPunctuated(x, y *spelling, match string) bool {
	if fx, fy := punctuationFaults(x.credit.DisplayName), punctuationFaults(y.credit.DisplayName); fx != fy {
		return fx < fy
	}
	if mx, my := x.credit.DisplayName == match, y.credit.DisplayName == match; mx != my {
		return mx
	}
	if len(x.books) != len(y.books) {
		return len(x.books) > len(y.books)
	}
	return x.credit.ResultID < y.credit.ResultID
}

// punctuationFaults counts an initial — a capital letter alone in its word —
// without its dot, a dot a letter follows with no space between them
// («R.A Salvatore» has both), and a space doubled or at either end.
func punctuationFaults(name string) int {
	runes := []rune(name)
	letter := func(i int) bool { return i >= 0 && i < len(runes) && unicode.IsLetter(runes[i]) }
	n := 0
	for i, r := range runes {
		switch {
		case unicode.IsUpper(r) && !letter(i-1) && !letter(i+1) && (i+1 == len(runes) || runes[i+1] != '.'):
			n++
		case r == '.' && letter(i+1):
			n++
		case unicode.IsSpace(r) && (i == 0 || i+1 == len(runes) || unicode.IsSpace(runes[i+1])):
			n++
		}
	}
	return n
}

// labelForm is the full given-first form that is the Russian label (the
// shown label, when Cyrillic) of the Wikidata item the matching name names,
// nil when there is none (step 1).
func (b *builder) labelForm(match string, forms []*shownForm) *shownForm {
	ref := b.in.Reference
	if ref == nil {
		return nil
	}
	item := ref.Items[b.mainLabelOf(ref.NameKey[match])]
	if item == nil || pureScript(item.Label) != string(authornorm.ScriptCyrillic) {
		return nil
	}
	label := authornorm.SearchKey(item.Label)
	var best *shownForm
	for _, f := range forms {
		if f.full() && authornorm.SearchKey(f.name) == label && better(f, best) {
			best = f
		}
	}
	return best
}

// bestForm is the form of steps 2–5.
func bestForm(forms []*shownForm) *shownForm {
	cyrl, latn := string(authornorm.ScriptCyrillic), string(authornorm.ScriptLatin)
	books, cyrlShare := scriptBooks(forms)
	if cyrlShare {
		latin := bestOf(forms, func(f *shownForm) bool { return f.script == latn && !f.scrambled })
		f := bestOf(forms, func(f *shownForm) bool { return f.script == cyrl && f.russian && !f.scrambled })
		if f != nil && latin != nil && f.shape() <= latin.shape() {
			return f
		}
	}
	if books[cyrl] > 0 && books[cyrl] >= books[latn] {
		if f := bestOf(forms, func(f *shownForm) bool { return f.script == cyrl && !f.scrambled }); f != nil {
			return f
		}
	}
	if f := bestOf(forms, func(f *shownForm) bool { return f.script == latn && !f.scrambled }); f != nil {
		return f
	}
	return bestOf(forms, func(*shownForm) bool { return true })
}

// bestOf is the first by ranks of the forms keep holds, nil when none is.
func bestOf(forms []*shownForm, keep func(*shownForm) bool) *shownForm {
	var best *shownForm
	for _, f := range forms {
		if keep(f) && (best == nil || ranks(f, best) < 0) {
			best = f
		}
	}
	return best
}

// scriptBooks are the books of each script's forms, and whether the
// Cyrillic forms hold minCyrillicShare of the entity's books (step 2).
func scriptBooks(forms []*shownForm) (map[string]int, bool) {
	cyrl := string(authornorm.ScriptCyrillic)
	all, cyrlBooks := map[int64]bool{}, map[int64]bool{}
	books := map[string]int{}
	for _, f := range forms {
		books[f.script] += len(f.books)
		for id := range f.books {
			all[id] = true
			if f.script == cyrl {
				cyrlBooks[id] = true
			}
		}
	}
	return books, len(cyrlBooks)*minCyrillicShare >= len(all)
}

// ranks orders two forms of one script: the better shape, a Russian
// spelling, more books, then fuller, then the lower result id.
func ranks(x, y *shownForm) int {
	if x.shape() != y.shape() {
		return x.shape() - y.shape()
	}
	if x.russian != y.russian {
		if x.russian {
			return -1
		}
		return 1
	}
	if better(x, y) {
		return -1
	}
	if better(y, x) {
		return 1
	}
	return 0
}

// better reports x before y by books, then fullness, then the lower result
// id; any form is better than none.
func better(x, y *shownForm) bool {
	if y == nil {
		return true
	}
	if len(x.books) != len(y.books) {
		return len(x.books) > len(y.books)
	}
	if wx, wy := len(strings.Fields(x.name)), len(strings.Fields(y.name)); wx != wy {
		return wx > wy
	}
	return x.credit.ResultID < y.credit.ResultID
}

// familyFirst reports a credit written surname first («Цвейг Стефан»): its
// display's first word is its family name's first word, not its given
// name's.
func familyFirst(c *Credit) bool {
	if c.Family == "" || c.Given == "" {
		return false
	}
	first := func(s string) string {
		words := strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == ',' })
		if len(words) == 0 {
			return ""
		}
		return authornorm.SearchKey(words[0])
	}
	shown, family, given := first(c.DisplayName), first(c.Family), first(c.Given)
	return shown != "" && shown == family && shown != given
}

// scrambled reports a credit written given name first whose family name is
// among its words but not the last, a suffix aside («Томас Рид Майн» of
// Рид, Томас Майн). A display that does not hold the family name at all is
// none: it is not this credit's order to judge.
func scrambled(c *Credit) bool {
	if c.Family == "" || c.Given == "" || familyFirst(c) {
		return false
	}
	words := strings.Fields(authornorm.SearchKey(c.DisplayName))
	if suffix := strings.Fields(authornorm.SearchKey(c.Suffix)); len(suffix) > 0 && len(words) > len(suffix) &&
		slices.Equal(words[len(words)-len(suffix):], suffix) {
		words = words[:len(words)-len(suffix)]
	}
	family := strings.Fields(authornorm.SearchKey(c.Family))
	if len(words) == 0 || len(family) == 0 {
		return false
	}
	last := family[len(family)-1]
	return slices.Contains(words, last) && words[len(words)-1] != last
}

// otherCyrillicLetters are letters of Cyrillic alphabets other than the
// Russian one (Ukrainian, Belarusian, Serbian, Macedonian).
const otherCyrillicLetters = "іїєґўђјљњћџѓќѕІЇЄҐЎЂЈЉЊЋЏЃЌЅ"

// otherCyrillic reports a name in another Cyrillic spelling than the
// Russian: a letter of another alphabet, or ъ before a consonant, as
// Bulgarian writes it («Пиърсън»).
func otherCyrillic(name string) bool {
	if strings.ContainsAny(name, otherCyrillicLetters) {
		return true
	}
	runes := []rune(strings.ToLower(name))
	for i, r := range runes {
		if r == 'ъ' && i+1 < len(runes) && strings.ContainsRune("бвгджзйклмнпрстфхцчшщ", runes[i+1]) {
			return true
		}
	}
	return false
}
