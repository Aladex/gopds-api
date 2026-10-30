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
