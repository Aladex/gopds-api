package models

import (
	"slices"
	"strings"
	"unicode"
)

// nameParticles are words that belong to a name in its own order — Arabic
// kunyas and nasabs, Turkic patronymic markers, European particles — so a
// name holding one is not a plain «Фамилия Имя» and is left as it is.
// Lower-cased; a hyphenated word is checked by its first part too
// (Аль-Харис).
var nameParticles = []string{
	"ибн", "бин", "бен", "бинт", "абу", "абд", "абдель", "абдуль", "абуль", "аль", "ал", "ас", "аш", "ат", "ан",
	"ад", "эль", "оглы", "кызы", "ван", "фон", "де", "ди", "да", "дю", "дер", "дель", "делла", "ла", "ле",
	"ibn", "bin", "ben", "abu", "al", "el", "van", "von", "de", "di", "da", "du", "der", "del", "della", "la", "le",
}

// notPeople are first words of a catalog name that name a publisher, a
// periodical or a collective, not a family: «Литагент Щепетнов Евгений» is
// not «Евгений Литагент Щепетнов». Lower-cased.
var notPeople = []string{
	"литагент", "коллектив", "журнал", "газета", "сборник", "редакция", "автор", "издательство", "альманах",
	"антология",
}

// GivenNameFirst turns a legacy catalog name, which a scan writes «Фамилия
// Имя» (last name, then the first-name field), into «Имя Фамилия»: the
// leading family name moves to the end, with a note in brackets that follows
// it («Зубков (ред.) Владимир» → «Владимир Зубков (ред.)»).
//
// The catalog is edited by hand and holds anything, so a name is turned only
// when it reads as a plain personal name: two words or more outside brackets,
// each starting with a capital letter, none all capitals, no digits or
// punctuation beyond a dot, an apostrophe or a hyphen, one script for every
// word (Latin look-alikes in a Cyrillic word aside), no name
// particle (Абу, ибн, Ле, van, …), initials only at the end, and a first word
// that is a family name rather than an initial or a publisher. Any other name comes back as it is.
func GivenNameFirst(name string) string {
	if name == UnknownAuthorName {
		return name
	}
	tokens, ok := nameTokens(name)
	if !ok || len(tokens) < 2 {
		return name
	}
	family := tokens[0].word
	if len([]rune(family)) < 2 || strings.HasSuffix(family, ".") ||
		slices.Contains(notPeople, strings.ToLower(family)) {
		return name
	}
	script, initials := "", false
	for _, t := range tokens {
		if !plainNameWord(t.word) || isParticle(t.word) {
			return name
		}
		// Initials close a «Фамилия И. О.»; a word after one is a name
		// already given first («Кевин Дж. Андерсон»).
		switch {
		case isInitial(t.word):
			initials = true
		case initials:
			return name
		}
		switch s := wordScript(t.word); {
		case s == "other", s != "" && script != "" && s != script:
			return name
		case s != "":
			script = s
		}
	}
	out := make([]string, 0, len(tokens))
	for _, t := range tokens[1:] {
		out = append(out, t.text)
	}
	return strings.Join(append(out, tokens[0].text), " ")
}

// nameToken is a word of a name and the bracketed note that follows it, if
// any: text is both as written, word the word alone.
type nameToken struct {
	text, word string
}

// nameTokens splits a name into its words, a bracketed run glued to the word
// before it. A name that opens with a bracket, or leaves one open, is not
// split.
func nameTokens(name string) ([]nameToken, bool) {
	var tokens []nameToken
	depth := 0
	for _, field := range strings.Fields(name) {
		opens := depth > 0 || strings.HasPrefix(field, "(")
		depth += strings.Count(field, "(") - strings.Count(field, ")")
		if opens {
			if len(tokens) == 0 {
				return nil, false
			}
			tokens[len(tokens)-1].text += " " + field
			continue
		}
		if depth != 0 || strings.ContainsAny(field, "()") {
			// A bracket inside a word («Бузина(Олесь)») is not a note beside it.
			return nil, false
		}
		tokens = append(tokens, nameToken{text: field, word: field})
	}
	return tokens, depth == 0
}

// plainNameWord reports whether a word reads as part of a personal name: a
// capital letter first, then letters, marks, dots, apostrophes, hyphens and
// invisible format characters (a soft hyphen), and not all capitals unless it
// is made of initials («C.А»).
func plainNameWord(word string) bool {
	runes := []rune(word)
	if len(runes) == 0 || !unicode.IsUpper(runes[0]) {
		return false
	}
	letters, upper := 0, 0
	for _, r := range runes {
		switch {
		case unicode.IsLetter(r):
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		case unicode.IsMark(r), unicode.Is(unicode.Cf, r), r == '.', r == '-', r == '\'', r == '’':
		default:
			return false
		}
	}
	return upper < letters || isInitial(word)
}

// isInitial reports whether a word is an initial or two: «Н.», «Дж.»,
// «C.А», or a lone letter.
func isInitial(word string) bool {
	letters := 0
	for _, r := range word {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters == 1 || (letters <= 3 && strings.Contains(word, "."))
}

// cyrillicLookAlikes reads the Latin look-alikes of a word as the Cyrillic
// letters they stand for.
var cyrillicLookAlikes = strings.NewReplacer(
	"A", "А", "B", "В", "C", "С", "E", "Е", "H", "Н", "K", "К", "M", "М", "O", "О", "P", "Р", "T", "Т", "X", "Х",
	"a", "а", "c", "с", "e", "е", "o", "о", "p", "р", "x", "х", "y", "у")

// isParticle reports whether a word, or the first part of a hyphenated one,
// is a name particle, Latin look-alikes in a Cyrillic word read as Cyrillic
// («Лe»).
func isParticle(word string) bool {
	if wordScript(word) == "Cyrillic" {
		word = cyrillicLookAlikes.Replace(word)
	}
	lower := strings.ToLower(word)
	head, _, _ := strings.Cut(lower, "-")
	return slices.Contains(nameParticles, lower) || slices.Contains(nameParticles, head)
}

// lookAlikes are Latin letters a Cyrillic word is often typed with
// («Щеpбаков», «Cергей»): they say nothing of a word's script.
const lookAlikes = "ABCEHKMOPTXaceopxy"

// wordScript is the script most of a word's letters are written in —
// "Cyrillic", "Latin" or "Greek" — leaving out Latin look-alikes of Cyrillic
// letters; empty for a word of those alone, "other" for any other script.
func wordScript(word string) string {
	counts := map[string]int{}
	for _, r := range word {
		switch {
		case !unicode.IsLetter(r), strings.ContainsRune(lookAlikes, r):
		case unicode.Is(unicode.Cyrillic, r):
			counts["Cyrillic"]++
		case unicode.Is(unicode.Latin, r):
			counts["Latin"]++
		case unicode.Is(unicode.Greek, r):
			counts["Greek"]++
		default:
			return "other"
		}
	}
	script, most := "", 0
	for _, s := range []string{"Cyrillic", "Latin", "Greek"} {
		if counts[s] > most {
			script, most = s, counts[s]
		}
	}
	return script
}

// ShowsShortNames reports whether a book's language writes its authors by
// their short names (design/author-display, rule 2): Russian, after
// lower-casing and dropping a region, or no language at all.
func ShowsShortNames(lang string) bool {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	return lang == "" || lang == "ru"
}
