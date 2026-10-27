package services

// SetAuthorEntityHooks installs the apply's test seams until the returned
// func restores them.
func SetAuthorEntityHooks(beforeApply func(), apply func(step string) error) (restore func()) {
	authorEntityBeforeApplyHook, authorEntityApplyHook = beforeApply, apply
	return func() { authorEntityBeforeApplyHook, authorEntityApplyHook = nil, nil }
}

// WithoutAuthorEntityCandidates makes the pass the one of the binary before
// migration 63: no merge candidates read or written, until restore.
func WithoutAuthorEntityCandidates() (restore func()) {
	authorEntityWithoutCandidates = true
	return func() { authorEntityWithoutCandidates = false }
}

// SetAuthorEntityCensusHook installs the census's test seam until the
// returned func restores it.
func SetAuthorEntityCensusHook(hook func(step string) error) (restore func()) {
	authorEntityCensusHook = hook
	return func() { authorEntityCensusHook = nil }
}

// SetAuthorEntityCensusHoldHook installs the hook that can hold a census
// write until the returned func restores it.
func SetAuthorEntityCensusHoldHook(hook func(runID int64, step string)) (restore func()) {
	authorEntityCensusHoldHook = hook
	return func() { authorEntityCensusHoldHook = nil }
}
