package services

// SetAuthorEntityHooks installs the apply's test seams until the returned
// func restores them.
func SetAuthorEntityHooks(beforeApply func(), apply func(step string) error) (restore func()) {
	authorEntityBeforeApplyHook, authorEntityApplyHook = beforeApply, apply
	return func() { authorEntityBeforeApplyHook, authorEntityApplyHook = nil, nil }
}
