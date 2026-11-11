package authorentity

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
)

// The deterministic reads of the review classes (E4l). The model strata of
// these classes failed the gate (runs 27, 30, 31): the models restructure
// single tokens and single fields wrongly far more often than the 1% bound
// allows. Most of what they were asked is plain enough to read without them,
// when the name is clearly a person's: its given name is one the catalog
// already gives several people. A rule reads an unselected credit as a
// selected one, its result corrected (case, look-alike letters, a title
// dropped, one field split); what no rule reads stays under review.
//
//   - case: an all-caps or an all-lower-case name (THIERRY JONQUET);
//   - lookalike: a name of one script but a few look-alike letters of the
//     other (Андрeй Коломицкий);
//   - nickname: a name of one script whose nickname is of another (Всеволод
//     Бобров | Vector), and a Latin name with a nickname, a class the model
//     strata never had;
//   - single_field: a two-word name the source left in one field (Дейзи
//     Уитни, Винниченко Володимир);
//   - affix: a name with a title (Священник Николай Бабкин);
//   - mononym: a single name that is the main label of one Wikidata item
//     (Вольтер, Эсхил).
//
// A name whose given name the catalog does not know (БСЭ БСЭ, журнал
// Русская жизнь, Вокруг Света), a placeholder or a phrase stays out.

// Rules of the reads, as the report counts them.
const (
	ReadCase        = "case"
	ReadLookalike   = "lookalike"
	ReadNickname    = "nickname"
	ReadSingleField = "single_field"
	ReadAffix       = "affix"
	ReadMononym     = "mononym"
)

// knownGivenFamilies is how many family names of the selected credits a
// given name must stand beside to be known: a given name of one family is
// as likely a misparsed word as a name.
const knownGivenFamilies = 5

// notGivenNames are words the catalog's misparsed credits put in the given
// field beside several family names: never a person's given name.
var notGivenNames = map[string]bool{
	"журнал": true, "газета": true, "альманах": true, "издательство": true, "редакция": true,
	"министерство": true, "автор": true,
	"литературная": true, "the": true, "a": true,
}

// readTitles are the titles the affix read drops: the same-name join's but
// "st", which in a Latin name is often the family's («St Martin»).
var readTitles = func() map[string]bool {
	set := make(map[string]bool, len(nameTitles))
	for t := range nameTitles {
		set[t] = true
	}
	delete(set, "st")
	return set
}()

// patronymicEnding is the ending of a Russian patronymic: a two-word field ending
// in one has no family name.
var patronymicEnding = regexp.MustCompile(`(ович|евич|ьич|овна|евна|ична|инична)$`)

// familyEnding is the ending of a Slavic family name: the first word of a
// one-field name with it, beside a known given name, is the family.
var familyEnding = regexp.MustCompile(`(ов|ев|ёв|ин|ын|ко|ский|цкий|ская|цкая|ова|ева|ина|ына|ук|юк|ич)$`)

// GivenNames are the given names of the selected credits, with the family
// names each stands beside.
type GivenNames map[string]map[string]bool

// Add counts the given name of a selected person credit beside its family.
func (g GivenNames) Add(c *Credit) {
	if c.Kind != KindPerson || c.Given == "" || c.Family == "" {
		return
	}
	first := strings.Fields(authornorm.SearchKey(c.Given))
	if len(first) == 0 {
		return
	}
	if g[first[0]] == nil {
		g[first[0]] = map[string]bool{}
	}
	g[first[0]][authornorm.SearchKey(c.Family)] = true
}

// Known reports a given name the catalog gives several people.
func (g GivenNames) Known(word string) bool {
	k := authornorm.SearchKey(word)
	return !notGivenNames[k] && len(g[k]) >= knownGivenFamilies
}

// readAsSelected is the result an unselected credit is read as selected by
// and the rule's name; ok is false when no rule reads it.
func (b *builder) readAsSelected(u *Unselected, g GivenNames) (res *Result, rule string, ok bool) {
	if u.Kind != KindPerson || u.Tag > 0 {
		return nil, "", false
	}
	switch u.Class {
	case string(authornorm.ClassCaseDefect):
		res, ok = readCase(u, g)
		rule = ReadCase
	case string(authornorm.ClassMixedScript):
		if res, ok = readLookalike(u, g); ok {
			rule = ReadLookalike
		} else {
			res, ok = readNickname(u, g)
			rule = ReadNickname
		}
	case string(authornorm.ClassNicknameWithName):
		res, ok = readNickname(u, g)
		rule = ReadNickname
	case string(authornorm.ClassSingleField):
		res, ok = readSingleField(u, g)
		rule = ReadSingleField
	case string(authornorm.ClassAffix):
		res, ok = readAffix(u, g)
		rule = ReadAffix
	case string(authornorm.ClassMononym):
		res, ok = b.readMononym(u, g)
		rule = ReadMononym
	}
	return res, rule, ok
}

// flagsWithin reports flags all in allowed or the particle rule's calm ones.
func flagsWithin(flags []string, allowed ...authornorm.QualityFlag) bool {
	for _, f := range flags {
		if !particleCalmFlags[f] && !slices.Contains(allowed, authornorm.QualityFlag(f)) {
			return false
		}
	}
	return true
}

// nameWord reports a word of letters (a hyphen or an apostrophe inside) of
// at least two of them.
func nameWord(w string) bool {
	letters := 0
	for _, r := range w {
		switch {
		case unicode.IsLetter(r):
			letters++
		case r == '-' || r == '\'' || r == '’':
		default:
			return false
		}
	}
	return letters >= minNameWordLetters
}

// minNameWordLetters is the fewest letters of a name word.
const minNameWordLetters = 2

// readResult is a result of the parts, its display and sort names built
// from them: given, additional and family in order, the nickname after.
func readResult(given string, additional []string, family, suffix, nickname string) *Result {
	words := append(append([]string{given}, additional...), family)
	display := strings.Join(strings.Fields(strings.Join(append(words, suffix), " ")), " ")
	sort := strings.Join(strings.Fields(family+", "+strings.Join(append([]string{given}, additional...), " ")), " ")
	if nickname != "" {
		display += " " + nickname
	}
	return &Result{Kind: KindPerson, Given: given, Additional: additional, Family: family, Suffix: suffix,
		Nickname: nickname, DisplayName: display, SortName: sort,
		Script: string(authornorm.DetectScript(strings.Join(words, " ")))}
}

// distinctWords reports no word of the name twice (БСЭ БСЭ, SAKSE SAKSE).
func distinctWords(words []string) bool {
	seen := map[string]bool{}
	for _, w := range words {
		k := authornorm.SearchKey(w)
		if seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}

// titleCase writes a word upper case first, lower case after, each part of
// a hyphenated word alike (ЛОРТКИПАНИДЗЕ → Лорткипанидзе).
func titleCase(w string) string {
	parts := strings.Split(w, "-")
	for i, p := range parts {
		r, size := utf8.DecodeRuneInString(p)
		if size == 0 {
			continue
		}
		parts[i] = string(unicode.ToUpper(r)) + strings.ToLower(p[size:])
	}
	return strings.Join(parts, "-")
}

// fixCase writes the words a case defect spells all upper or all lower
// case title case; the others as they are.
func fixCase(part string) string {
	words := strings.Fields(part)
	for i, w := range words {
		if strings.ToUpper(w) == w || strings.ToLower(w) == w {
			words[i] = titleCase(w)
		}
	}
	return strings.Join(words, " ")
}

// readCase reads a name whose only defect is its case: given and family
// names, words of letters, none twice, a known given name; a lower-case
// name is lower case throughout (not a phrase: «Джек из тени»).
func readCase(u *Unselected, g GivenNames) (*Result, bool) {
	if u.Given == "" || u.Family == "" || !flagsWithin(u.Flags, authornorm.FlagAllCaps, authornorm.FlagLowercase) {
		return nil, false
	}
	words := strings.Fields(u.Given + " " + strings.Join(u.Additional, " ") + " " + u.Family)
	if !distinctWords(words) || !g.Known(strings.Fields(u.Given)[0]) {
		return nil, false
	}
	lower := slices.Contains(u.Flags, string(authornorm.FlagLowercase))
	for _, w := range words {
		if !nameWord(w) || (lower && strings.ToLower(w) != w) {
			return nil, false
		}
	}
	additional := make([]string, 0, len(u.Additional))
	for _, a := range u.Additional {
		additional = append(additional, fixCase(a))
	}
	return readResult(fixCase(u.Given), additional, fixCase(u.Family), u.Suffix, u.Nickname), true
}

// readLookalike reads a name whose other-script letters all have a
// look-alike: folded, it is of one script, its given name known.
func readLookalike(u *Unselected, g GivenNames) (*Result, bool) {
	if u.Given == "" || u.Family == "" || u.Nickname != "" || !flagsWithin(u.Flags, authornorm.FlagMixedScript) {
		return nil, false
	}
	folded, ok := foldLookalikes([3][]string{strings.Fields(u.Given), u.Additional, strings.Fields(u.Family)})
	if !ok {
		return nil, false
	}
	given, family := strings.Join(folded[0], " "), strings.Join(folded[2], " ")
	res := readResult(given, folded[1], family, u.Suffix, "")
	if res.Script == string(authornorm.ScriptMixed) || !g.Known(folded[0][0]) {
		return nil, false
	}
	for _, w := range strings.Fields(res.DisplayName) {
		if !nameWord(w) {
			return nil, false
		}
	}
	return res, true
}

// readNickname reads a name with a nickname: the name of one script, its
// given name known and not the family, the nickname beside it whatever its
// script.
func readNickname(u *Unselected, g GivenNames) (*Result, bool) {
	if u.Given == "" || u.Family == "" || u.Nickname == "" ||
		!flagsWithin(u.Flags, authornorm.FlagMixedScript, authornorm.FlagNickname) {
		return nil, false
	}
	words := strings.Fields(u.Given + " " + strings.Join(u.Additional, " ") + " " + u.Family)
	if !distinctWords(words) || !g.Known(words[0]) {
		return nil, false
	}
	for _, w := range words {
		if !nameWord(w) {
			return nil, false
		}
	}
	res := readResult(u.Given, u.Additional, u.Family, u.Suffix, u.Nickname)
	if res.Script == string(authornorm.ScriptMixed) {
		return nil, false
	}
	return res, true
}

// readSingleField reads a two-word name the source left in one field: a
// known given name and a word that is no given name, in that order, or the
// other way round when the first word ends as a family name does; a
// patronymic, a title or a stop word keeps it out.
func readSingleField(u *Unselected, g GivenNames) (*Result, bool) {
	if u.Given != "" || u.Family != "" || len(u.Additional) > 0 ||
		!flagsWithin(u.Flags, authornorm.FlagUnstructured, authornorm.FlagEmptyComponent, authornorm.FlagAllCaps) {
		return nil, false
	}
	field, ok := u.oneSourceField()
	if !ok {
		return nil, false
	}
	given, family, ok := twoWordName(strings.Fields(field), g)
	if !ok {
		return nil, false
	}
	return readResult(fixCase(given), nil, fixCase(family), "", u.Nickname), true
}

// oneSourceField is the one source field of a credit that has a value;
// ok is false for none or several.
func (u *Unselected) oneSourceField() (field string, ok bool) {
	for _, f := range []string{u.SourceGiven, u.SourceAdditional, u.SourceFamily} {
		if strings.TrimSpace(f) == "" {
			continue
		}
		if field != "" {
			return "", false
		}
		field = f
	}
	return field, field != ""
}

// twoWordName splits two words into a given and a family name (see
// readSingleField).
func twoWordName(words []string, g GivenNames) (given, family string, ok bool) {
	if len(words) != 2 || !distinctWords(words) {
		return "", "", false
	}
	for _, w := range words {
		k := authornorm.SearchKey(w)
		if !nameWord(w) || abbreviation(w) || patronymicEnding.MatchString(k) || notGivenNames[k] || readTitles[k] {
			return "", "", false
		}
	}
	first, second := g.Known(words[0]), g.Known(words[1])
	switch {
	case first && !second:
		return words[0], words[1], true
	case second && !first && familyEnding.MatchString(authornorm.SearchKey(words[0])):
		return words[1], words[0], true
	}
	return "", "", false
}

// abbreviation reports a word of a few capitals: «МО РФ» is no name.
func abbreviation(w string) bool {
	return utf8.RuneCountInString(w) <= maxAbbreviationRunes && strings.ToUpper(w) == w
}

// maxAbbreviationRunes is the longest abbreviation a one-field name refuses.
const maxAbbreviationRunes = 3

// readAffix reads a name with a title: the title words dropped from the
// given and additional names, a known given name left and a family that is
// no title.
func readAffix(u *Unselected, g GivenNames) (*Result, bool) {
	if u.Given == "" || u.Family == "" || !flagsWithin(u.Flags, authornorm.FlagAffix, authornorm.FlagNickname) {
		return nil, false
	}
	drop := func(words []string) []string {
		var out []string
		for _, w := range words {
			if !readTitles[strings.TrimSuffix(authornorm.SearchKey(w), ".")] {
				out = append(out, w)
			}
		}
		return out
	}
	given := drop(strings.Fields(u.Given))
	additional := drop(strings.Fields(strings.Join(u.Additional, " ")))
	if len(given) == 0 {
		given, additional = additional, nil
	}
	nickname := u.Nickname
	if readTitles[strings.TrimSuffix(authornorm.SearchKey(nickname), ".")] {
		nickname = ""
	}
	if len(given) == 0 || readTitles[strings.TrimSuffix(authornorm.SearchKey(u.Family), ".")] || !g.Known(given[0]) {
		return nil, false
	}
	var add []string
	if len(additional) > 0 {
		add = []string{strings.Join(additional, " ")}
	}
	return readResult(strings.Join(given, " "), add, u.Family, u.Suffix, nickname), true
}

// readMononym reads a single name that is the main label (Russian, English
// or "mul") of exactly one Wikidata item and no given name the catalog
// knows: a name known as one person's (Вольтер, Эсхил, Платон), read as a
// person known by it alone.
func (b *builder) readMononym(u *Unselected, g GivenNames) (*Result, bool) {
	ref := b.in.Reference
	if ref == nil || !flagsWithin(u.Flags, authornorm.FlagSingleToken, authornorm.FlagEmptyComponent,
		authornorm.FlagNickname) {
		return nil, false
	}
	name := strings.TrimSpace(u.DisplayName)
	key := ref.ReviewNameKey[name]
	if key == "" {
		key = ref.NameKey[name]
	}
	// A given name of the catalog's people («Дарья», «Irene») may be any of
	// them, whatever single item carries it.
	if key == "" || len(strings.Fields(name)) != 1 || g.Known(name) {
		return nil, false
	}
	qid := ""
	for _, n := range ref.KeyItems[key] {
		it := ref.Items[n.QID]
		if it == nil || !slices.Contains(it.MainLabelKeys, key) || n.QID == qid {
			continue
		}
		if qid != "" {
			return nil, false
		}
		qid = n.QID
	}
	if qid == "" {
		return nil, false
	}
	return &Result{Kind: KindPerson, Nickname: name, DisplayName: name, SortName: name,
		Script: string(authornorm.DetectScript(name))}, true
}
