package authorentity

import (
	"strings"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
)

// The other-language spelling (E4n). A Ukrainian, Belarusian or Bulgarian
// edition spells a name the way its language does: «Николай Гогол»,
// «Джордж Мартін», «Евгений Гуляковски», «Герберт Уеллс». The few books of
// such an entity are the person's whose name the Russian editions spell:
// after the decisions, an entity of at most otherLanguageMaxBooks books, at
// least half of them in one of otherLanguages, joins the one entity of its
// name of at least otherLanguageMinBooks books, at least half of them in
// Russian and otherLanguageRatio times as many. Its name is that entity's
// under otherLanguageFold, or its family name one letter off the way these
// languages spell a sound (languageEdit): «Гайнлайн» / «Хайнлайн», «Бикав»
// / «Быков», «Соловьов» / «Соловьев», «Сомів» / «Сомов», «Майър» /
// «Майер». Any other letter off is another name: «Карась» is no «Тарас»,
// «Гурскі» no «Горский», «Гарэцкі» no «Зарецкий», «Гельман» no «Герман».
//
// The language is the evidence, not the spelling: a Russian «Сергей
// Дьяченко» of books on dieting beside the 160 of «Сергей Дяченко», or a
// Russian «Владимир Сологуб» of car repair beside «Соллогуб», is another
// person, and neither joins. Nor does one whose name two entities go by
// («Сергей Васильєв» beside several «Сергей Васильев»), nor one beside an
// entity whose books are not Russian («Христо Фотев», a Bulgarian poet,
// beside the Bulgarian «Христо Ботев»). The holds of the Wikidata step
// apply: not_same, the given-name guard, a shared book, a veto.

// RuleOtherLanguage is the rule of a key the other-language spelling
// joined.
const RuleOtherLanguage = "other_language"

// otherLanguages are the book languages whose spelling of a name the step
// reads: Ukrainian, Belarusian, Bulgarian.
var otherLanguages = map[string]bool{"uk": true, "be": true, "bg": true}

// The other-language spelling's bounds.
const (
	otherLanguageMaxBooks = 5
	otherLanguageMinBooks = 10
	otherLanguageRatio    = 10
)

// otherLanguageMarks are what the languages write a name with and Russian
// does not: the Belarusian ў as у and дз as д («Дзмітрук»), the apostrophe
// for ь («Мор'є»), a space or hyphen in a family name («Ле Клезіо»).
var otherLanguageMarks = strings.NewReplacer("ў", "у", "дз", "д", "'", "", "’", "", "ʼ", "", " ", "", "-", "")

// otherLanguageFold is a name part under otherLanguageMarks and the
// spelling fold (variantFold), with the Russian feminine -ская, -цкая as
// the Ukrainian -ська, -цька («Хмелевська»).
func otherLanguageFold(part string) string {
	s := otherLanguageMarks.Replace(strings.ToLower(part))
	for _, suffix := range []string{"ская", "цкая"} {
		if strings.HasSuffix(s, suffix) {
			s = strings.TrimSuffix(s, "ая") + "а"
		}
	}
	return variantFold(s)
}

// spelledName is a component's Cyrillic full name, folded.
type spelledName struct{ given, family string }

// languageDominant is a component the other-language spelling may join
// into, by one of its folded names' family.
type languageDominant struct {
	c      *component
	family string
}

// mergeOtherLanguages joins the other-language spellings (see above) and
// reports whether it joined any.
func (m *modelStep) mergeOtherLanguages() bool {
	b := m.b
	byGiven, small := b.otherLanguageSides()
	partners := m.notSamePartners()
	joined := false
	for _, s := range small {
		to := onlyDominant(s.name, byGiven)
		if to != nil && len(to.books) >= otherLanguageRatio*len(s.c.books) && m.joinOtherLanguage(s.c, to, partners) {
			joined = true
		}
	}
	if joined {
		// A moved book follows its target into the entity it joined, as
		// after the Wikidata step.
		for ci, to := range b.movedTo {
			b.movedTo[ci] = b.find(to)
		}
	}
	return joined
}

// languageSmall is a component of an other-language spelling and its one
// folded name.
type languageSmall struct {
	c    *component
	name spelledName
}

// otherLanguageSides are the components after the decisions an
// other-language spelling may join into, by folded given name, and the
// ones that may join.
func (b *builder) otherLanguageSides() (map[string][]languageDominant, []languageSmall) {
	byGiven := map[string][]languageDominant{}
	var small []languageSmall
	for _, c := range b.componentsAfterDecisions() {
		names := b.spelledNames(c)
		switch {
		case len(c.books) >= otherLanguageMinBooks && b.halfIn(c, "ru"):
			for _, n := range names {
				byGiven[n.given] = append(byGiven[n.given], languageDominant{c, n.family})
			}
		case len(c.books) <= otherLanguageMaxBooks && len(names) == 1 && b.halfInOther(c):
			small = append(small, languageSmall{c, names[0]})
		}
	}
	return byGiven, small
}

// onlyDominant is the one component of byGiven a name spells; nil when
// none or several do.
func onlyDominant(n spelledName, byGiven map[string][]languageDominant) *component {
	var to *component
	for _, d := range byGiven[n.given] {
		if !sameFamilySpelling(n.family, d.family) || d.c == to {
			continue
		}
		if to != nil {
			return nil
		}
		to = d.c
	}
	return to
}

// joinOtherLanguage joins component s into to unless the Wikidata step's
// holds keep them apart, and reports whether it did.
func (m *modelStep) joinOtherLanguage(s, to *component, partners map[int][]int) bool {
	b := m.b
	rs, rt := b.find(s.root), b.find(to.root)
	if rs == rt || sharesBook(s.books, to.books) || b.notSameAcross(partners, rs, rt) ||
		b.nameClassesOf(rs).joins(b.nameClassesOf(rt)) {
		return false
	}
	if b.vetoApart(rs, rt) {
		b.vetoRefused(VetoRefusedOtherLanguage)
		return false
	}
	b.union(rs, rt)
	for _, k := range s.keys {
		if b.wdRule[k] == "" {
			b.wdRule[k] = RuleOtherLanguage
		}
	}
	m.rep.OtherLanguage++
	return true
}

// spelledNames are the distinct folded Cyrillic full names of a
// component's person keys: a given name spelled out and a family name.
func (b *builder) spelledNames(c *component) []spelledName {
	var out []spelledName
	seen := map[spelledName]bool{}
	for _, k := range c.keys {
		key := &b.keys[k].key
		if key.kind != KindPerson || key.script != string(authornorm.ScriptCyrillic) || key.family == "" ||
			!fullName(key.given) {
			continue
		}
		n := spelledName{given: otherLanguageFold(key.given), family: otherLanguageFold(key.family)}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// sameFamilySpelling reports two folded family names one spelling of the
// other: equal, or a languageEdit apart at four letters or more.
func sameFamilySpelling(a, b string) bool {
	if a == b {
		return true
	}
	return utf8.RuneCountInString(a) >= typoMinLetters && utf8.RuneCountInString(b) >= typoMinLetters &&
		languageEdit([]rune(a), []rune(b))
}

// languageSounds are the letter pairs the languages spell one sound with:
// г and х (Ukrainian «Гайнлайн»), о and а (Belarusian «Бикав»), о and е
// («Соловьов»), о and и (Ukrainian «Сомів»), е and а, и and е, у and в
// («Воттс», «Лаўкрафт»), я and а («Золя»), я and е («Сянкевіч»), ю and у
// («Петкявічус»). No consonant pair beyond them: «Зорин» and «Сорин» are
// two names.
var languageSounds = map[[2]rune]bool{
	{'г', 'х'}: true, {'о', 'а'}: true, {'о', 'е'}: true, {'о', 'и'}: true, {'е', 'а'}: true, {'и', 'е'}: true,
	{'у', 'в'}: true, {'я', 'а'}: true, {'я', 'е'}: true, {'ю', 'у'}: true,
}

// languageVowels are the vowels a spelling drops or adds (Bulgarian ъ,
// which the fold takes out: «Майър», «Карпентър»).
const languageVowels = "аеои"

// languageEdit reports two names one letter apart the way the languages
// spell a sound: one of languageSounds for the other, or one of
// languageVowels more.
func languageEdit(a, b []rune) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	i := 0
	for i < len(a) && a[i] == b[i] {
		i++
	}
	switch len(b) - len(a) {
	case 0:
		return i < len(a) && string(a[i+1:]) == string(b[i+1:]) &&
			(languageSounds[[2]rune{a[i], b[i]}] || languageSounds[[2]rune{b[i], a[i]}])
	case 1:
		return strings.ContainsRune(languageVowels, b[i]) && string(a[i:]) == string(b[i+1:])
	}
	return false
}

// halfIn reports at least half of a component's books in lang.
func (b *builder) halfIn(c *component, lang string) bool {
	n := 0
	for id := range c.books {
		if b.in.Books[id].Lang == lang {
			n++
		}
	}
	return 2*n >= len(c.books)
}

// halfInOther reports at least half of a component's books in the
// otherLanguages.
func (b *builder) halfInOther(c *component) bool {
	n := 0
	for id := range c.books {
		if otherLanguages[b.in.Books[id].Lang] {
			n++
		}
	}
	return 2*n >= len(c.books)
}
