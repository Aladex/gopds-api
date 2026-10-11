package services

import (
	"testing"

	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
)

// PanicOnApplyForTest makes the resolve driver panic while applying the
// verdict of every input whose last-name field matches, until the test ends.
func PanicOnApplyForTest(t *testing.T, match func(last string) bool) {
	t.Helper()
	previous := applyHook
	applyHook = func(input *llmreq.ItemInput) {
		for _, f := range input.Fields {
			if f.Field == llmres.FieldLast && match(f.Text) {
				panic("a value nobody expected")
			}
		}
	}
	t.Cleanup(func() { applyHook = previous })
}

// SetEvalReportCollectedHookForTest calls fn each time the eval driver's
// report stage has collected the run's answers, before it computes and
// stores the report, until the test ends.
func SetEvalReportCollectedHookForTest(t *testing.T, fn func()) {
	t.Helper()
	previous := evalReportCollectedHook
	evalReportCollectedHook = fn
	t.Cleanup(func() { evalReportCollectedHook = previous })
}
