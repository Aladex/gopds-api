package services

import (
	"testing"

	"gopds-api/internal/authornorm/llmeval"
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

// SetEvalGateForTest makes every eval report — the report stage's and a
// recomputation — computed by gate instead of llmeval.Evaluate, until the
// test ends or it is set again.
func SetEvalGateForTest(t *testing.T, gate func(*llmeval.Input) llmeval.Report) {
	t.Helper()
	previous := evaluateEval
	evaluateEval = gate
	t.Cleanup(func() { evaluateEval = previous })
}

// SetApplyHookForTest calls fn with each verdict's input right before the
// resolve driver applies it, until the test ends.
func SetApplyHookForTest(t *testing.T, fn func(input *llmreq.ItemInput)) {
	t.Helper()
	previous := applyHook
	applyHook = fn
	t.Cleanup(func() { applyHook = previous })
}

// SetResolveStartPlannedHookForTest calls fn between a resolve start's read
// of its eval evidence and its transaction, until the test ends.
func SetResolveStartPlannedHookForTest(t *testing.T, fn func()) {
	t.Helper()
	previous := resolveStartPlannedHook
	resolveStartPlannedHook = fn
	t.Cleanup(func() { resolveStartPlannedHook = previous })
}

// SetResolveEnqueuePlannedHookForTest calls fn in every resolve enqueue once
// it has planned and before it writes, until the test ends.
func SetResolveEnqueuePlannedHookForTest(t *testing.T, fn func()) {
	t.Helper()
	previous := resolveEnqueuePlannedHook
	resolveEnqueuePlannedHook = fn
	t.Cleanup(func() { resolveEnqueuePlannedHook = previous })
}

// OnApplyForTest calls fn with each verdict's input right before the resolve
// driver applies it, until the test ends.
func OnApplyForTest(t *testing.T, fn func(input *llmreq.ItemInput)) {
	t.Helper()
	previous := applyHook
	applyHook = fn
	t.Cleanup(func() { applyHook = previous })
}
