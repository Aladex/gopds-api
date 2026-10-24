package services

// SetAuthorAutopilotHook installs the autopilot's tick seam until the
// returned func restores it.
func SetAuthorAutopilotHook(hook func(action string) error) (restore func()) {
	authorAutopilotHook = hook
	return func() { authorAutopilotHook = nil }
}
