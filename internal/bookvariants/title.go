// Package bookvariants decides which files of the catalog are one book — one
// card on the site — and how the files inside a card rank. It is pure: it reads
// what the caller loaded and touches no database (design/book-variants).
//
// A card is the same set of author entities, the same language and the same
// normalised title. Inside it, files of one FB2 document are revisions of each
// other; files of different documents are variants.
package bookvariants

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// trailingBracket is the last square bracket of a title and its text.
	trailingBracket = regexp.MustCompile(`\s*\[([^\[\]]*)\]\s*$`)

	// A volume word, and the number or ordinal that makes it a volume. \b
	// does not see Cyrillic, so the end of the ordinal is bounded by hand.
	volumeWord = `(?:том|т\.|книга|кн\.|часть|ч\.|выпуск|вып\.|vol\.?|volume|book|part|tome|band)`
	ordinal    = `(?:\d+|[ivxlc]+|перв\p{L}*|втор\p{L}*|трет\p{L}*|четв[её]рт\p{L}*|пят\p{L}*|шест\p{L}*|` +
		`седьм\p{L}*|восьм\p{L}*|девят\p{L}*|десят\p{L}*)`
	wordEnd = `(?:$|[^\p{L}\p{N}])`

	// keptBrackets are the brackets that make another book, not another
	// edition: a volume or a part, an issue, a bilingual edition.
	keptBrackets = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*` + volumeWord + `\s*` + ordinal + wordEnd),
		regexp.MustCompile(`(?i)^\s*` + ordinal + `(?:\s*-?\s*(?:й|я|ой|ая|ый|ий|е|ое))?\s+` + volumeWord + wordEnd),
		regexp.MustCompile(`^\s*#?\d{1,3}\s*$`),
		regexp.MustCompile(`№\s*\d`),
		regexp.MustCompile(`(?i)параллельн|билингв|bilingual|двуязычн`),
	}

	// formWords name a literary form. A bracket of nothing else names another
	// text under the same title — «[проза]» and «[поэзия]» of one book are two
	// documents — so it is kept (coordinator's decision, 08.11.2026).
	formWords = wordSet(
		"проза", "поэзия", "стихи", "стихотворения", "роман", "романы", "повесть", "повести",
		"рассказ", "рассказы", "новелла", "новеллы", "пьеса", "пьесы", "поэма", "поэмы",
		"сказка", "сказки", "басни", "очерк", "очерки", "эссе", "лирика", "драма", "комедия", "трагедия",
		"story", "stories", "novel", "novella", "poetry", "prose", "poems", "play", "plays",
		// Connectors, so «[Повести и рассказы]» is a form too.
		"и", "and",
	)

	// junkTitles are titles that say nothing about the book: two files under
	// one of them are two unknown books, not one.
	junkTitles = wordSet(
		"unknown", "upload", "untitled", "без названия", "книга", "тест", "test", "book", "noname",
		"документ", "document", "новый документ", "new document",
	)
)

func wordSet(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// NormalizeTitle returns the card title key, the trailing tags it stripped in
// title order, and whether the title may group at all: false for a junk title.
//
// Trailing square brackets are stripped one by one from the end, as long as
// the bracket is not a kept one and something would be left. The rest is
// folded: lowercase, ё read as е, letters and digits kept, every run of
// anything else one space. Round brackets are text like any other.
func NormalizeTitle(raw string) (key string, tags []string, groupable bool) {
	title := strings.Join(strings.Fields(raw), " ")
	for {
		m := trailingBracket.FindStringSubmatchIndex(title)
		if m == nil {
			break
		}
		inner := title[m[2]:m[3]]
		rest := strings.TrimSpace(title[:m[0]])
		if rest == "" || keptBracket(inner) {
			break
		}
		tags = append(tags, strings.TrimSpace(inner))
		title = rest
	}
	// Stripped from the end, reported in the order the title has them.
	for i, j := 0, len(tags)-1; i < j; i, j = i+1, j-1 {
		tags[i], tags[j] = tags[j], tags[i]
	}
	key = fold(title)
	return key, tags, key != "" && !junkTitles[key]
}

// keptBracket says whether a bracket's text makes another book.
func keptBracket(inner string) bool {
	for _, re := range keptBrackets {
		if re.MatchString(inner) {
			return true
		}
	}
	words := strings.Fields(fold(inner))
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !formWords[w] {
			return false
		}
	}
	return true
}

// fold lowercases s, reads ё as е, keeps letters and digits and makes every
// run of anything else one space, with none at either end.
func fold(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(s) {
		if r == 'ё' {
			r = 'е'
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte(' ')
		}
		gap = false
		b.WriteRune(r)
	}
	return b.String()
}
