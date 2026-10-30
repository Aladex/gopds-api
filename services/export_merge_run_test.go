package services

import "testing"

// SetMergeEnqueueHookForTest calls fn after the merge run's due read and
// card freeze of each chunk, before its questions are inserted, until the
// test ends.
func SetMergeEnqueueHookForTest(t *testing.T, fn func()) {
	t.Helper()
	previous := authorLLMMergeEnqueueHook
	authorLLMMergeEnqueueHook = fn
	t.Cleanup(func() { authorLLMMergeEnqueueHook = previous })
}
