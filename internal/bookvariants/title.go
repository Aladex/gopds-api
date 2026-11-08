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
	// does not see Cyrillic, so both ends of a word are bounded by hand: the
	// marker may stand anywhere inside the bracket («[… Том II]»), but not
	// inside another word («[Томское издательство]»).
	volumeWord = `(?:том|т\.|книга|кн\.|часть|ч\.|выпуск|вып\.|vol\.?|volume|book|part|tome|band)`
	ordinal    = `(?:\d+|[ivxlc]+|перв\p{L}*|втор\p{L}*|трет\p{L}*|четв[её]рт\p{L}*|пят\p{L}*|шест\p{L}*|` +
		`седьм\p{L}*|восьм\p{L}*|девят\p{L}*|десят\p{L}*)`
	wordStart = `(?:^|[^\p{L}\p{N}])`
	wordEnd   = `(?:$|[^\p{L}\p{N}])`

	// keptBrackets are the brackets that make another book, not another
	// edition: a volume or a part, an issue, a bilingual edition.
	keptBrackets = []*regexp.Regexp{
		regexp.MustCompile(`(?i)` + wordStart + volumeWord + `\s*` + ordinal + wordEnd),
		regexp.MustCompile(`(?i)` + wordStart + ordinal + `(?:\s*-?\s*(?:й|я|ой|ая|ый|ий|е|ое))?\s+` + volumeWord + wordEnd),
		regexp.MustCompile(`^\s*#?\d{1,3}\s*$`),
		regexp.MustCompile(`№\s*\d`),
		regexp.MustCompile(`(?i)параллельн|билингв|bilingual|двуязычн`),
	}

	// setSize is how many volumes an edition has («[в 2 т.]», «[in 3
	// volumes]»): an edition, not a volume, so it is read past.
	setSize = regexp.MustCompile(`(?i)` + wordStart + `(?:в|in)\s+\d+\s*` +
		`(?:т\.|тт\.|томах|кн\.|книгах|частях|volumes|vols\.?|books|parts)` + wordEnd)

	// formWords name a literary form. A bracket of nothing else names another
	// text under the same title — «[проза]» and «[поэзия]» of one book are two
	// documents — so it is kept (coordinator's decision, 08.11.2026).
	formWords = wordSet(
		"проза", "поэзия", "стихи", "стихотворения", "роман", "романы", "повесть", "повести",
		"рассказ", "рассказы", "новелла", "новеллы", "пьеса", "пьесы", "поэма", "поэмы",
		"сказка", "сказки", "сказ", "басни", "очерк", "очерки", "эссе", "лирика", "драма", "комедия", "трагедия",
		"быль", "притча", "притчи", "миниатюра", "миниатюры",
		"story", "stories", "novel", "novels", "novella", "novellas", "novelette", "novelettes",
		"poetry", "prose", "poem", "poems", "verse", "play", "plays", "tale", "tales",
	)

	// formModifiers may stand with a form word in a form phrase — «[short
	// story]», «[маленькая повесть]», «[повести и рассказы]», «[роман в
	// стихах]» — but make none on their own (review 1-2).
	formModifiers = adjectiveSet(
		[]string{"коротк", "маленьк", "мал", "небольш", "больш", "фантастическ", "историческ",
			"документальн", "приключенческ", "детективн", "юмористическ", "сатирическ", "лирическ",
			"философск", "автобиографическ", "святочн"},
		"и", "and", "a", "an", "в", "in", "стихах", "прозе", "научно",
		"short", "long", "small", "little", "graphic",
	)

	// sourceTagWords make up a bracket that names where a file came from. A
	// title that is such a bracket alone says nothing of the book.
	sourceTagWords = wordSet(
		"litres", //nolint:misspell // a catalog tag
		"литрес", "си", "лп", "самиздат", "selfpub", "сборник", "fb2", "epub",
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

// adjectiveSet is the words and every form of the adjective stems: each stem
// with each adjective ending, which overshoots harmlessly.
func adjectiveSet(stems []string, words ...string) map[string]bool {
	set := wordSet(words...)
	for _, stem := range stems {
		for _, end := range []string{"ий", "ый", "ой", "ая", "ое", "ие", "ые"} {
			set[stem+end] = true
		}
	}
	return set
}

// NormalizeTitle returns the card title key, the trailing tags it stripped in
// title order, and whether the title may group at all: false for a junk title,
// and for a title that is nothing but a source tag («[СИ]»).
//
// Trailing square brackets are stripped one by one from the end, as long as
// the bracket is not a kept one and something would be left: a title that is
// only a bracket keeps its text as the key. The rest is folded: lowercase, ё
// read as е, letters and digits kept, every run of anything else one space.
// Round brackets are text like any other.
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
	return key, tags, key != "" && !junkTitles[key] && !onlySourceTag(title)
}

// onlySourceTag says whether the title is one bracket that names a source:
// its words are all source tag words, or it names a publisher.
func onlySourceTag(title string) bool {
	m := trailingBracket.FindStringSubmatchIndex(title)
	if len(m) < 4 || strings.TrimSpace(title[:m[0]]) != "" {
		return false
	}
	inner := strings.TrimSpace(title[m[2]:m[3]])
	if strings.HasPrefix(strings.ToLower(inner), "publisher:") {
		return true
	}
	words := strings.Fields(fold(inner))
	for _, w := range words {
		if !sourceTagWords[w] {
			return false
		}
	}
	return len(words) > 0
}

// keptBracket says whether a bracket's text makes another book: a volume
// marker anywhere in it, or a form phrase that is all of it.
func keptBracket(inner string) bool {
	marked := setSize.ReplaceAllString(inner, " ")
	for _, re := range keptBrackets {
		if re.MatchString(marked) {
			return true
		}
	}
	return formPhrase(inner)
}

// formPhrase says whether the text is a form: form words and their modifiers
// only, with at least one form word.
func formPhrase(text string) bool {
	form := false
	for _, w := range strings.Fields(fold(text)) {
		switch {
		case formWords[w]:
			form = true
		case !formModifiers[w]:
			return false
		}
	}
	return form
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
