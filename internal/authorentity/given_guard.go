package authorentity

import (
	"slices"
	"strings"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
)

// The given-name guard of the decision step (E4c): a model decision never
// joins two people a short form bridges. A component's full Cyrillic given
// names and patronymics are grouped by family (its translit skeleton, so a
// masculine and a feminine form share a group: stricter, never looser).
// Two names are one name only when they are equal after folding
// (foldName): case, ё/е, the Ukrainian and Belarusian letters, й/и, a soft
// sign before a vowel as и («Наталья»/«Наталия»), the signs out. No typo
// is tolerated: «Олег» and «Ольга», «Милана» and «Милена», «Дмитирий» and
// «Дмитрий», «Айзек» and «Исаак» are two names, and so are «Римма» and
// «Рима» — a doubled letter is a letter of its own, so «Савва» and «Сава»
// are two names too. Too strict only holds a decision, too loose merges
// two people. A union that adds a name neither side already holds next to
// another one — «Виталий» joined to «Владимир» through «В.» — would join
// two people; a side whose names the other already holds (or that has
// none) adds nothing new. Initials are never compared, nor Latin names,
// nor names of two families (a swapped name's given name is the other's
// family). A patronymic written first and a given name second are read the
// other way round, and a given name of several words as the given name and
// middle names (splitGiven). Middle names, not patronymics, are one name
// when one spells the other (oneMiddleName, E4n). The guard never holds a
// decision the previous pass applied.

// familyNames are one family's full names in a component, folded: given
// names and patronymics.
type familyNames struct{ given, additional []string }

// nameClasses are a component's familyNames by family skeleton.
type nameClasses map[string]*familyNames

// nameFolds are the letters the comparison folds: ё/е, the Ukrainian and
// Belarusian і, ї, є, ў, й/и, and the signs out.
var nameFolds = strings.NewReplacer("ё", "е", "і", "и", "ї", "и", "є", "е", "ў", "у", "й", "и", "ь", "", "ъ", "")

// softVowels reads a soft sign before a vowel as и: «Наталья» is
// «Наталия», «Дарья» «Дария».
var softVowels = strings.NewReplacer("ья", "ия", "ье", "ие", "ьё", "иё", "ью", "ию", "ьи", "ии")

// foldName folds a Cyrillic name part: lower case, softVowels, nameFolds.
func foldName(part string) string {
	return nameFolds.Replace(softVowels.Replace(strings.ToLower(strings.TrimSpace(part))))
}

// fullName reports a name part spelled out: words, none an initial.
func fullName(part string) bool {
	words := strings.Fields(part)
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if utf8.RuneCountInString(strings.TrimSuffix(w, ".")) <= 1 {
			return false
		}
	}
	return true
}

// patronymic reports a Cyrillic patronymic: -ич, -овна, -евна, -ична.
func patronymic(part string) bool {
	part = strings.ToLower(strings.TrimSpace(part))
	for _, suffix := range []string{"ич", "овна", "евна", "ична"} {
		if strings.HasSuffix(part, suffix) {
			return true
		}
	}
	return false
}

// nameClassesOf are the full Cyrillic names of a level-0 component.
func (b *builder) nameClassesOf(root int) nameClasses {
	out := nameClasses{}
	for _, k := range b.membersOf(root) {
		key := &b.keys[k].key
		if key.kind != KindPerson || key.script != string(authornorm.ScriptCyrillic) || key.family == "" {
			continue
		}
		family := familySkeleton(strings.ToLower(key.family))
		f := out[family]
		if f == nil {
			f = &familyNames{}
			out[family] = f
		}
		given, additional := key.given, key.additional
		if patronymic(given) && !patronymic(additional) {
			given, additional = additional, given
		}
		given, additional = splitGiven(given, additional)
		if fullName(given) {
			f.given = addName(f.given, foldName(given))
		}
		if fullName(additional) {
			f.additional = addName(f.additional, foldName(additional))
		}
	}
	return out
}

// splitGiven reads a given name of several words as the given name and the
// middle names before the additional part (E4n): «Джоан Кэтлин» is «Джоан»
// with the middle name «Кэтлин», as another credit of hers writes it.
func splitGiven(given, additional string) (first, middle string) {
	words := strings.Fields(given)
	if len(words) <= 1 {
		return given, additional
	}
	return words[0], strings.Join(append(words[1:], strings.Fields(additional)...), " ")
}

// joins reports a union of the two components that would join two people:
// in some family both hold full names, and together they hold more than
// either alone. Middle names count as one when one spells the other
// (oneMiddleName).
func (n nameClasses) joins(o nameClasses) bool {
	for family, x := range n {
		y := o[family]
		if y == nil {
			continue
		}
		if grows(x.given, y.given) || growsNear(x.additional, y.additional, oneMiddleName) {
			return true
		}
	}
	return false
}

// oneMiddleName reports two middle names (not patronymics) one name spells
// two ways (E4n): equal under the spelling fold, one edit apart under it
// at four letters or more («Кэтлинг», «Кетлин»), one the
// other's first words («Роналд», «Роналд Руэл») or its initials («Дж»,
// «Джон»). A patronymic is never near another: «Карпович» and «Карлович»,
// «Трофимович» and «Трифонович» are two fathers. No skeleton: it drops the
// vowels, and «Анна» and «Нина» are two names.
func oneMiddleName(a, b string) bool {
	if a == b {
		return true
	}
	if patronymic(a) || patronymic(b) {
		return false
	}
	fa, fb := variantFold(a), variantFold(b)
	if fa == fb || initialsOf(a, b) || initialsOf(b, a) {
		return true
	}
	if utf8.RuneCountInString(fa) >= typoMinLetters && utf8.RuneCountInString(fb) >= typoMinLetters &&
		levenshtein(fa, fb) == 1 {
		return true
	}
	wa, wb := strings.Fields(a), strings.Fields(b)
	if len(wa) > len(wb) {
		wa, wb = wb, wa
	}
	return len(wa) < len(wb) && slices.Equal(wa, wb[:len(wa)])
}

// variantOf reports a union a variant decision asked about (E4f): in every
// family both hold names in, each of one side's names is the same name as
// each of the other's or the variant's spelling of it — a given name equal
// under the spelling fold, a patronymic within variantPatronymics. Only the
// asked difference passes: a chain of variants («Ибатович», «Ипатович»,
// «Ипатьевич») holds two names that are no variant of each other.
func (n nameClasses) variantOf(o nameClasses) bool {
	for family, x := range n {
		y := o[family]
		if y == nil {
			continue
		}
		if !allPairs(x.given, y.given, func(a, b string) bool { return variantFold(a) == variantFold(b) }) ||
			!allPairs(x.additional, y.additional, variantPatronymics) {
			return false
		}
	}
	return true
}

// allPairs reports every name of x and every name of y equal or near.
func allPairs(x, y []string, near func(a, b string) bool) bool {
	for _, a := range x {
		for _, b := range y {
			if a != b && !near(a, b) {
				return false
			}
		}
	}
	return true
}

// grows reports two name sets neither of which holds the other: each has a
// name the other lacks. It relies on one name being an equivalence — equal
// folded strings — so that holding is transitive: a name a side holds is
// the name itself, never one of its neighbors, and no chain of near names
// passes.
func grows(x, y []string) bool {
	return !holds(x, y) && !holds(y, x)
}

// growsNear is grows with near names for one: each set has a name near
// none of the other's.
func growsNear(x, y []string, near func(a, b string) bool) bool {
	return !holdsNear(x, y, near) && !holdsNear(y, x, near)
}

// holdsNear reports every name of y near some name of x.
func holdsNear(x, y []string, near func(a, b string) bool) bool {
	for _, b := range y {
		if !slices.ContainsFunc(x, func(a string) bool { return near(a, b) }) {
			return false
		}
	}
	return true
}

// holds reports every name of y in x.
func holds(x, y []string) bool {
	for _, b := range y {
		if !slices.Contains(x, b) {
			return false
		}
	}
	return true
}

// addName adds a folded name not already in names.
func addName(names []string, name string) []string {
	if slices.Contains(names, name) {
		return names
	}
	return append(names, name)
}

// merged is the union of two components' names.
func (n nameClasses) merged(o nameClasses) nameClasses {
	out := nameClasses{}
	for _, src := range []nameClasses{n, o} {
		for family, f := range src {
			dst := out[family]
			if dst == nil {
				dst = &familyNames{}
				out[family] = dst
			}
			for _, name := range f.given {
				dst.given = addName(dst.given, name)
			}
			for _, name := range f.additional {
				dst.additional = addName(dst.additional, name)
			}
		}
	}
	return out
}
