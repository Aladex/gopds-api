package authorentity

// PanicOnCredit makes the key of one credit panic until the returned func
// restores it.
func PanicOnCredit(id int64) (restore func()) {
	keyFunc = func(c *Credit) nameKey {
		if c.ID == id {
			panic("a catalog value no code path expected")
		}
		return keyOf(c)
	}
	return func() { keyFunc = keyOf }
}

// PanicOnCandidateSide makes the candidate side holding credit id panic until
// the returned func restores it.
func PanicOnCandidateSide(id int64) (restore func()) {
	candidateSideHook = func(c *Credit) {
		if c.ID == id {
			panic("a catalog value no code path expected")
		}
	}
	return func() { candidateSideHook = nil }
}

// PanicOnCandidateGeneration makes candidate generation panic until the
// returned func restores it.
func PanicOnCandidateGeneration() (restore func()) {
	candidateGenerateHook = func() { panic("a catalog value no code path expected") }
	return func() { candidateGenerateHook = nil }
}

// PanicOnCandidateSeal makes the seal panic until the returned func
// restores it.
func PanicOnCandidateSeal() (restore func()) {
	candidateSealHook = func() { panic("a catalog value no code path expected") }
	return func() { candidateSealHook = nil }
}

// PanicOnCardBook makes every card showing book id panic until the returned
// func restores it.
func PanicOnCardBook(id int64) (restore func()) {
	candidateCardHook = func(book int64) {
		if book == id {
			panic("a catalog value no code path expected")
		}
	}
	return func() { candidateCardHook = nil }
}

// OnDecisionStep runs fn whenever a pass enters its decision step, until
// the returned func restores it.
func OnDecisionStep(fn func()) (restore func()) {
	modelStepHook = fn
	return func() { modelStepHook = nil }
}

// SameGivenName reports two full Cyrillic name parts the given-name guard
// takes for one name.
func SameGivenName(a, b string) bool { return foldName(a) == foldName(b) }

// DenyWikidataAfter adds items to the Wikidata step's deny list until the
// returned func restores it.
func DenyWikidataAfter(qids ...string) (restore func()) {
	saved := wikidataAfterDenied
	wikidataAfterDenied = map[string]bool{}
	for k, v := range saved {
		wikidataAfterDenied[k] = v
	}
	for _, q := range qids {
		wikidataAfterDenied[q] = true
	}
	return func() { wikidataAfterDenied = saved }
}

// TypoVerdict reads two near words through the frozen table: 0 no typo, 1 a
// typo, 2 gender endings.
func TypoVerdict(x, y string) int { return typoVerdict(x, y) }

// TypoVerdictOfSides reads two near words on nx and ny sides.
func TypoVerdictOfSides(x, y string, nx, ny int) int { return typoVerdictOfSides(x, y, nx, ny) }

// FreezeTypoWords replaces the frozen table until the returned func restores
// it.
func FreezeTypoWords(words map[string]int) (restore func()) {
	saved := typoWordTable
	typoWordTable = func() map[string]int { return words }
	return func() { typoWordTable = saved }
}

// The embedded table's file, the fewest sides it lists and its parser.
var (
	TypoWordsFile  = typoWordsFile
	TypoWordFloor  = typoWordFloor
	ParseTypoWords = parseTypoWords
)

// NearWords are the pairs of words within the typo distance.
func NearWords(words []string) [][2]string { return nearWords(words) }

// ReadAsSelected is the build's read of an unselected credit (every class it
// reads but the mononym, which needs the reference), for comparing with
// ReadReviewRule.
func ReadAsSelected(u *Unselected, g GivenNames) (rule string, ok bool) {
	b := &builder{in: &Input{}}
	_, rule, ok = b.readAsSelected(u, g)
	return rule, ok
}
