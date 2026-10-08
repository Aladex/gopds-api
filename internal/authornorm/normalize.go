package authornorm

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Normalize builds the deterministic local candidate of NormalizerVersion for
// one canonical source value. It never reorders the display, never applies
// blanket case changes, never transliterates and never fills a field the
// source did not carry explicitly: every form it cannot settle without a
// guess gets an ambiguous decision class instead. Whether the candidate is
// selected is a separate decision of AcceptancePolicy.
func Normalize(v SourceValue, extractorVersion string) (Result, error) {
	if v.displayName == "" {
		return Result{}, ErrNoNameComponents
	}
	fingerprint := SourceFingerprint(v)
	key, err := NormalizationKey(fingerprint, extractorVersion, NormalizerVersion)
	if err != nil {
		return Result{}, err
	}

	display := collapseWhitespace(v.displayName)
	searchKey := SearchKey(display)
	script := DetectScript(display)
	shape := inspect(v, display, searchKey, script)
	class := shape.classify()

	r := Result{
		DisplayName:       display,
		SearchKey:         searchKey,
		Script:            script,
		Kind:              class.Kind(),
		Status:            class.Status(),
		Method:            MethodRules,
		DecisionClass:     class,
		QualityFlags:      shape.flags.list(),
		SourceFingerprint: fingerprint,
		NormalizationKey:  key,
		ExtractorVersion:  extractorVersion,
		NormalizerVersion: NormalizerVersion,
		SchemaVersion:     ResultSchemaVersion,
	}
	r.transferExplicitFields(v, shape)
	return r, nil
}

// transferExplicitFields copies the explicit FB2 components of a person into
// the result fields, without splitting or reordering them. A single person
// component is not a structure: it stays only in display, because deciding
// whether it is a given name, a family name or both would be a guess.
func (r *Result) transferExplicitFields(v SourceValue, s shape) {
	if r.Kind != KindPerson {
		return
	}
	r.Nickname = collapseWhitespace(deref(v.nickname))
	if s.personParts < minStructuredParts {
		return
	}
	r.Method = MethodStructured
	r.GivenName = collapseWhitespace(deref(v.first))
	r.AdditionalNames = collapseWhitespace(deref(v.middle))
	r.FamilyName = collapseWhitespace(deref(v.last))
	r.SortName = sortName(r.FamilyName, r.GivenName, r.AdditionalNames)
}

// minStructuredParts is the number of non-empty first/middle/last components
// that makes the source an explicit structure.
const minStructuredParts = 2

// sortName is "Family, Given Additional". Without a family name there is no
// safe sort form and none is invented; additional names only follow a given
// name.
func sortName(family, given, additional string) string {
	if family == "" {
		return ""
	}
	if given == "" {
		return family
	}
	if additional == "" {
		return family + ", " + given
	}
	return family + ", " + given + " " + additional
}

func deref(field *string) string {
	if field == nil {
		return ""
	}
	return *field
}

// collapseWhitespace replaces every run of Unicode white space (NBSP
// included) with one U+0020 and drops it at the edges.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// SearchKey derives the search representation of contract 3.5: NFC, full
// Unicode case folding, ё→е, then every run of punctuation, separators and
// white space becomes one U+0020, trimmed at the edges. Diacritics, digits
// and symbols are kept; nothing is transliterated.
func SearchKey(s string) string {
	folded := cases.Fold().String(norm.NFC.String(s))
	folded = strings.ReplaceAll(folded, "ё", "е")

	var b strings.Builder
	b.Grow(len(folded))
	pendingSeparator := false
	for _, r := range folded {
		if unicode.IsPunct(r) || unicode.Is(unicode.Z, r) || unicode.IsSpace(r) {
			pendingSeparator = b.Len() > 0
			continue
		}
		if pendingSeparator {
			b.WriteByte(' ')
			pendingSeparator = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

type letterScript struct {
	code  Script
	table *unicode.RangeTable
}

// letterScripts lists every Unicode script that can name a letter, in a fixed
// order; Common and Inherited never contribute a script.
var letterScripts = func() []letterScript {
	names := make([]string, 0, len(unicode.Scripts))
	for name := range unicode.Scripts {
		if name != "Common" && name != "Inherited" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	scripts := make([]letterScript, 0, len(names))
	for _, name := range names {
		if code, ok := iso15924ByUnicodeScript[name]; ok {
			scripts = append(scripts, letterScript{code: Script(code), table: unicode.Scripts[name]})
		}
	}
	return scripts
}()

func scriptOf(r rune) Script {
	switch {
	case unicode.Is(unicode.Cyrillic, r):
		return ScriptCyrillic
	case unicode.Is(unicode.Latin, r):
		return ScriptLatin
	}
	for _, s := range letterScripts {
		if unicode.Is(s.table, r) {
			return s.code
		}
	}
	return ""
}

// DetectScript returns the ISO 15924 code of the letters of s, ScriptMixed
// when letters belong to more than one script, and ScriptUndetermined when s
// has no letter. Digits, punctuation, symbols and combining marks are Common
// or Inherited and never add a script.
func DetectScript(s string) Script {
	var found Script
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		code := scriptOf(r)
		switch {
		case code == "":
			continue
		case found == "":
			found = code
		case found != code:
			return ScriptMixed
		}
	}
	if found == "" {
		return ScriptUndetermined
	}
	return found
}

// flagSet collects observed quality flags; list returns them in canonical
// order so equal inputs always produce equal results.
type flagSet map[QualityFlag]bool

func (f flagSet) list() []QualityFlag {
	out := make([]QualityFlag, 0, len(f))
	for _, flag := range qualityFlagOrder {
		if f[flag] {
			out = append(out, flag)
		}
	}
	return out
}

// classPrecedence maps quality flags to decision classes, earliest first.
// The first rule with any of its flags set names the result; a source that
// trips none is a complete explicit structure and is structured_person. A
// selectable rule (placeholder) is skipped when the source carries structural
// ambiguity, so the result falls to the ambiguous class that names it.
var classPrecedence = []struct {
	class DecisionClass
	flags []QualityFlag
}{
	{ClassMalformed, []QualityFlag{FlagNoLetters, FlagReplacementCharacter}},
	{ClassPlaceholder, []QualityFlag{FlagPlaceholder}},
	{ClassCollectiveCandidate, []QualityFlag{FlagCollectiveMarker}},
	{ClassPlaceholderFragment, []QualityFlag{FlagPlaceholderFragment}},
	{ClassDuplicateComponent, []QualityFlag{FlagDuplicateComponent}},
	{ClassMixedScript, []QualityFlag{FlagMixedScript}},
	{ClassUnusualCharacters, []QualityFlag{FlagUnusualCharacters}},
	{ClassInitials, []QualityFlag{FlagInitials}},
	{ClassParticle, []QualityFlag{FlagParticle}},
	{ClassAffix, []QualityFlag{FlagAffix}},
	{ClassSingleField, []QualityFlag{FlagUnstructured}},
	{ClassMononym, []QualityFlag{FlagSingleToken}},
	{ClassPartialStructure, []QualityFlag{FlagPartialStructure}},
	{ClassNicknameWithName, []QualityFlag{FlagNickname}},
	{ClassCaseDefect, []QualityFlag{FlagAllCaps, FlagLowercase}},
}

// shape is everything the classifier observed about one source value.
type shape struct {
	flags       flagSet
	personParts int
}

func (s shape) classify() DecisionClass {
	ambiguous := hasStructuralAmbiguity(func(f QualityFlag) bool { return s.flags[f] })
	for _, rule := range classPrecedence {
		if ambiguous && rule.class.selectable() {
			continue
		}
		for _, flag := range rule.flags {
			if s.flags[flag] {
				return rule.class
			}
		}
	}
	return ClassStructuredPerson
}

func inspect(v SourceValue, display, searchKey string, script Script) shape {
	s := shape{flags: flagSet{}}
	s.inspectStructure(v, display)
	s.inspectCharacters(display, script)
	tokens := strings.Fields(display)
	s.inspectTokens(tokens)
	s.inspectLowercase(v, len(tokens))
	s.inspectVocabulary(tokens, searchKey)
	return s
}

// inspectStructure reads which explicit components carry text.
func (s *shape) inspectStructure(v SourceValue, display string) {
	fieldsWithText := 0
	for _, field := range []*string{v.first, v.middle, v.last, v.nickname} {
		switch {
		case field == nil:
		case *field == "":
			s.flags[FlagEmptyComponent] = true
		default:
			fieldsWithText++
		}
	}
	for _, field := range []*string{v.first, v.middle, v.last} {
		if deref(field) != "" {
			s.personParts++
		}
	}
	s.flags[FlagDuplicateComponent] = v.duplicateComponent
	s.flags[FlagNickname] = deref(v.nickname) != ""
	s.flags[FlagWhitespaceCollapsed] = display != v.displayName

	if fieldsWithText == 1 {
		if len(strings.Fields(display)) == 1 {
			s.flags[FlagSingleToken] = true
		} else {
			s.flags[FlagUnstructured] = true
		}
		return
	}
	s.flags[FlagPartialStructure] = deref(v.first) == "" || deref(v.last) == ""
}

func (s *shape) inspectCharacters(display string, script Script) {
	s.flags[FlagMixedScript] = script == ScriptMixed
	s.flags[FlagReplacementCharacter] = strings.ContainsRune(display, utf8.RuneError)
	hasLetter := false
	for _, r := range display {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case isHyphen(r):
			s.flags[FlagHyphen] = true
		case isApostrophe(r):
			s.flags[FlagApostrophe] = true
		}
	}
	s.flags[FlagNoLetters] = !hasLetter
}

// inspectTokens classifies each white-space separated display token.
func (s *shape) inspectTokens(tokens []string) {
	for _, token := range tokens {
		t := classifyToken(token, len(tokens))
		s.flags[FlagAffix] = s.flags[FlagAffix] || t.affix
		s.flags[FlagParticle] = s.flags[FlagParticle] || t.particle
		s.flags[FlagInitials] = s.flags[FlagInitials] || t.initial
		if hasUnusualCharacters(token, t.affix || t.initial) {
			s.flags[FlagUnusualCharacters] = true
		}
		if t.nameWord() {
			if upper, lower := casedLetters(token); upper >= minCaseDefectLetters && lower == 0 {
				s.flags[FlagAllCaps] = true
			}
		}
	}
}

// inspectLowercase flags a component whose first name word starts with a
// lower-case letter. Only the first word is checked: later words of a phrase
// ("Коллектив авторов") are legitimately lower-case.
func (s *shape) inspectLowercase(v SourceValue, tokenCount int) {
	for _, field := range []*string{v.first, v.middle, v.last, v.nickname} {
		for _, token := range strings.Fields(deref(field)) {
			if !classifyToken(token, tokenCount).nameWord() {
				continue
			}
			first, _ := utf8.DecodeRuneInString(token)
			if upper, lower := casedLetters(token); unicode.IsLower(first) && upper+lower >= minCaseDefectLetters {
				s.flags[FlagLowercase] = true
			}
			break
		}
	}
}

type tokenClass struct {
	affix, particle, initial bool
}

// nameWord reports a token that carries a name proper, as opposed to an
// affix, a particle or an initial, whose case follows its own conventions.
func (t tokenClass) nameWord() bool { return !t.affix && !t.particle && !t.initial }

func classifyToken(token string, tokenCount int) tokenClass {
	folded := cases.Fold().String(token)
	affix := isAffix(token, folded)
	return tokenClass{
		affix:    affix,
		particle: hasParticlePrefix(folded) || (tokenCount > 1 && particles[folded]),
		initial:  !affix && isInitialToken(token),
	}
}

// minCaseDefectLetters keeps one-letter tokens out of the case checks: a
// lone capital is an initial, a lone lower-case letter is a conjunction.
const minCaseDefectLetters = 2

// inspectVocabulary matches the closed v1 vocabularies: placeholders and
// collective words against the folded search tokens, conjunctions against
// the white-space display tokens.
func (s *shape) inspectVocabulary(displayTokens []string, searchKey string) {
	searchTokens := strings.Fields(searchKey)
	if placeholders[searchKey] {
		s.flags[FlagPlaceholder] = true
	} else if anyTokenHasPrefix(searchTokens, placeholderFragmentPrefixes) {
		s.flags[FlagPlaceholderFragment] = true
	}
	s.flags[FlagCollectiveMarker] = anyTokenHasPrefix(searchTokens, collectivePrefixes) || hasInnerConjunction(displayTokens)
}

func anyTokenHasPrefix(tokens, prefixes []string) bool {
	for _, token := range tokens {
		for _, prefix := range prefixes {
			if strings.HasPrefix(token, prefix) {
				return true
			}
		}
	}
	return false
}

// hasInnerConjunction spots "A и B" / "A and B" — two credits in one value —
// over white-space display tokens, so that a hyphenated "Сеспедес-и-Менесес"
// or an initial "И." is not mistaken for a conjunction.
func hasInnerConjunction(displayTokens []string) bool {
	for i := 1; i < len(displayTokens)-1; i++ {
		if conjunctions[cases.Fold().String(displayTokens[i])] {
			return true
		}
	}
	return false
}

func isHyphen(r rune) bool { return r == '-' || r == '‐' || r == '‑' }

func isApostrophe(r rune) bool {
	switch r {
	case '\'', '’', '‘', 'ʼ', 'ʻ', '`':
		return true
	}
	return false
}

// hasUnusualCharacters reports a rune that has no place in a personal name:
// digits, symbols and punctuation other than hyphens and apostrophes. A dot
// belongs only to an initial or an abbreviated affix.
func hasUnusualCharacters(token string, dotAllowed bool) bool {
	for _, r := range token {
		switch {
		case unicode.IsLetter(r), unicode.IsMark(r), isHyphen(r), isApostrophe(r):
		case r == '.' && dotAllowed:
		default:
			return true
		}
	}
	return false
}

func casedLetters(token string) (upper, lower int) {
	for _, r := range token {
		switch {
		case unicode.IsUpper(r):
			upper++
		case unicode.IsLower(r):
			lower++
		}
	}
	return upper, lower
}

// maxInitialLetters admits two-letter abbreviations such as "Дж." or "Ch.".
const maxInitialLetters = 2

// isInitialToken reports a lone capital letter, or a token with at least one
// dot-terminated segment of one or two letters that starts with a capital:
// "И.", "Дж.Р.Р.", "Ж.-П.", "Ю.Несбё".
func isInitialToken(token string) bool {
	if utf8.RuneCountInString(token) == 1 {
		r, _ := utf8.DecodeRuneInString(token)
		return unicode.IsUpper(r)
	}
	segments := strings.Split(token, ".")
	for _, segment := range segments[:len(segments)-1] {
		segment = strings.TrimLeftFunc(segment, isHyphen)
		n := utf8.RuneCountInString(segment)
		if n == 0 || n > maxInitialLetters {
			continue
		}
		first, _ := utf8.DecodeRuneInString(segment)
		if unicode.IsUpper(first) && strings.IndexFunc(segment, func(r rune) bool { return !unicode.IsLetter(r) }) < 0 {
			return true
		}
	}
	return false
}

func isAffix(token, folded string) bool {
	return isRomanNumeral(token) || affixes[strings.TrimSuffix(folded, ".")]
}

// maxRomanTens bounds regnal numbers at XXXIX.
const maxRomanTens = 3

// isRomanNumeral reports an upper-case regnal or generational number from II
// to XXXIX. A single I, V or X is left to the initial check.
func isRomanNumeral(token string) bool {
	if len(token) < 2 || strings.Trim(token, "IVX") != "" {
		return false
	}
	rest := strings.TrimLeft(token, "X")
	if len(token)-len(rest) > maxRomanTens {
		return false
	}
	switch rest {
	case "", "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX":
		return true
	}
	return false
}

func hasParticlePrefix(folded string) bool {
	for _, prefix := range particlePrefixes {
		if strings.HasPrefix(folded, prefix) && len(folded) > len(prefix) {
			return true
		}
	}
	return false
}

// The vocabularies below are part of NormalizerVersion v1. They are matched
// against case-folded tokens; editing them is a new normalizer version.
var (
	// placeholders are whole search keys that name no one in particular.
	placeholders = setOf(
		"автор неизвестен", "неизвестный автор", "неизвестен", "неизвестно", "неизвестный",
		"аноним", "анонимный автор", "анонимус", "без автора", "автор не указан", "нет автора",
		"anonymous", "anonym", "anon", "anonymus", "unknown", "unknown author", "author unknown",
		"noname", "no name",
	)
	// placeholderFragmentPrefixes mark a placeholder word inside a longer
	// value, where it may be a real surname or a stray label.
	placeholderFragmentPrefixes = []string{"аноним", "anonym", "неизвестн", "unknown", "noname"}
	// collectivePrefixes mark collectives, anthologies, folklore and
	// publisher or agent labels.
	collectivePrefixes = []string{
		"коллектив", "сборник", "народн", "антолог", "литагент", "издательств", "редакци",
		"редколлег", "фольклор", "братья", "авторов",
		"various", "collective", "anthology", "folklore", "editorial", "brothers", "authors",
	}
	conjunctions = setOf("и", "and", "und", "et")
	// particles are standalone name particles; they mark ambiguity only next
	// to another token.
	particles = setOf(
		"de", "da", "del", "della", "der", "den", "di", "du", "des", "dos", "das", "do", "la", "le",
		"van", "von", "ten", "ter", "zu", "af", "av", "bin", "ibn", "ben", "al", "el",
		"де", "да", "дель", "делла", "дер", "ди", "дю", "дос", "дас", "ду", "ла", "ле",
		"ван", "фон", "тен", "тер", "цу", "бен", "бин", "ибн", "аль", "эль", "ал",
	)
	// particlePrefixes are particles glued to the next word.
	particlePrefixes = []string{
		"d'", "d’", "д'", "д’", "l'", "l’", "л'", "л’",
		"al-", "el-", "аль-", "эль-", "ал-", "ибн-", "бен-", "ben-", "ibn-", "абу-", "abu-",
	}
	// affixes are generational suffixes, honorifics and clerical titles,
	// compared without a trailing dot.
	affixes = setOf(
		"jr", "sr", "junior", "senior", "младший", "старший", "мл",
		"св", "свт", "прп", "святой", "святитель", "преподобный", "блаженный",
		"протоиерей", "прот", "священник", "свящ", "иерей", "архимандрит", "игумен", "игумения",
		"иеромонах", "монах", "монахиня", "епископ", "архиепископ", "митрополит", "патриарх",
		"dr", "prof", "sir", "dame", "lord", "lady", "rev", "st", "mr", "mrs", "ms",
		"проф", "академик", "д-р",
	)
)

func setOf(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}
