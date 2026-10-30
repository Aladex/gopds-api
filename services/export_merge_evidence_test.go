package services

import "testing"

// SetMergeReportComputedHookForTest calls fn once a merge eval report is
// computed — its labels read — before it is stored, until the test ends.
func SetMergeReportComputedHookForTest(t *testing.T, fn func(runID int64)) {
	t.Helper()
	previous := mergeReportComputedHook
	mergeReportComputedHook = fn
	t.Cleanup(func() { mergeReportComputedHook = previous })
}
