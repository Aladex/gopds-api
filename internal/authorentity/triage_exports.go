package authorentity

import "gopds-api/internal/authornorm"

// The review triage (internal/authortriage) decides open
// review items with a few of the build's own name tests, so that a triage
// decision and the build never read one name two ways. These are the entry
// points; the tests themselves stay where the build uses them.

// GivenNamesOf is the given names of the selected credits, each with the
// family names it stands beside (see GivenNames.Known).
func GivenNamesOf(credits []Credit) GivenNames {
	g := GivenNames{}
	for i := range credits {
		g.Add(&credits[i])
	}
	return g
}

// ReadReviewRule reads an unselected credit with the three reads the judges
// agree with (case, look-alike letters, a nickname), under the guards of the
// build's readAsSelected: a person name without a namesake tag. The single
// field, affix and mononym reads stay the build's. ok is false when no read
// applies.
func ReadReviewRule(u *Unselected, g GivenNames) (res *Result, rule string, ok bool) {
	if u == nil || u.Result == nil || u.Kind != KindPerson || u.Tag != 0 {
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
	}
	if !ok {
		return nil, "", false
	}
	return res, rule, true
}

// OrganizationName reports a name an organization writes: an organization
// word first, a title in quotes, a role after a comma, or abbreviations
// (see organizationName).
func OrganizationName(display string) bool {
	return organizationName(display)
}

// FoldLookalikeWord writes every letter of word of the other script as its
// look-alike, the build's look-alike read letter by letter: Latin letters
// as Cyrillic when toCyrillic, Cyrillic as Latin otherwise. ok is false when
// one has none («Стогоff» to Cyrillic).
func FoldLookalikeWord(word string, toCyrillic bool) (string, bool) {
	return foldWord(word, toCyrillic)
}
