package llmeval

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The holdout audit (E4c option (c), merge3's bucket): the census audit
// without the census. The stratum passes on a human audit of the applied
// holdout items — the first AuditSampleSize in the run's audit order — and
// of the applied extra Latin questions outside |A|, the first
// AuditSupplementSize in the same order. The bound, the gold errors and the
// contested accepts are reported and gate nothing.

// holdoutAuditOpts are merge3's options under the holdout audit.
var holdoutAuditOpts = func() Options {
	o := contestedOpts
	o.GateRule = GateRuleHoldoutAudit
	return o
}()

// holdoutAuditStratum is mergeStratum(n, m) of run auditRun with every item
// applied and labeled same.
func holdoutAuditStratum(n, m int) Input {
	in := mergeStratum(n, m)
	in.AuditRun = auditRun
	for i := range in.Items {
		in.Items[i].Adjudication = AdjudicatedSame
	}
	return in
}

// evaluateHoldoutAudit evaluates a holdout under the audit and returns its
// decision and combo; the combo has a holdout audit and no census.
func evaluateHoldoutAudit(t *testing.T, in *Input) (StratumDecision, *ComboMetrics) {
	t.Helper()
	d, m := evaluateCensus(t, in, &holdoutAuditOpts)
	require.NotNil(t, m.Audit)
	assert.Nil(t, m.Census, "a holdout is no census")
	return d, m
}

// auditOrder is ids in the run's audit order.
func auditOrder(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b int64) int {
		ka, kb := AuditKey(auditRun, a), AuditKey(auditRun, b)
		return bytes.Compare(ka[:], kb[:])
	})
	return out
}

// idsOf are the fixture's ids of the holdout (supplement false) or of the
// extra Latin questions.
func idsOf(in *Input, supplement bool) []int64 {
	var ids []int64
	for i := range in.Items {
		if in.Items[i].Supplement == supplement && !in.Items[i].Adversarial {
			ids = append(ids, in.Items[i].ID)
		}
	}
	return ids
}

// A clean audit of both samples passes the holdout however few it accepted
// (400, far below the 2,995 the bound needs). The holdout sample is the
// first 300 applied holdout items in the audit order, the Latin sample the
// first 100 applied extra questions in the same order; neither holds the
// other's items. The report names the rule; the bound is still reported.
func TestAHoldoutPassesOnACleanAuditOfBothSamples(t *testing.T) {
	in := holdoutAuditStratum(400, 150)
	d, m := evaluateHoldoutAudit(t, &in)
	require.True(t, d.Passed, d.Reason)
	assert.Equal(t, GateRuleHoldoutAudit, d.Rule)
	assert.Equal(t, 400, m.Audit.Applied)
	assert.Equal(t, auditOrder(idsOf(&in, false))[:AuditSampleSize], m.Audit.Sample)
	assert.Equal(t, AuditSampleSize, m.Audit.AdjudicatedSame)
	assert.Greater(t, m.UpperBound, 0.001, "the bound is reported, not gated")

	require.NotNil(t, m.Supplement)
	require.NotNil(t, m.Supplement.Audit)
	assert.Equal(t, 150, m.Supplement.Audit.Applied)
	assert.Equal(t, auditOrder(idsOf(&in, true))[:AuditSupplementSize], m.Supplement.Audit.Sample)
	assert.Equal(t, AuditSupplementSize, m.Supplement.Audit.AdjudicatedSame)
	for _, id := range m.Supplement.Audit.Sample {
		assert.NotContains(t, m.Audit.Sample, id)
	}

	// The same holdout under the bound fails: 400 accepts are too few.
	d, _ = evaluateCensus(t, &in, &holdoutOpts)
	assert.False(t, d.Passed)
}

// A sampled item still pending, or labeled different, fails the holdout,
// in either sample; same and unclear pass it. An item outside its sample
// is never read: pending, it fails nothing.
func TestEitherAuditSampleFailsTheHoldout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		supplement bool
		rank       int
		label      string
		reason     string
	}{
		{"holdout pending", false, 7, "",
			"audit: 1 of 300 sampled items failed in A1 (0 different, 1 pending; 400 applied)"},
		{"holdout different", false, AuditSampleSize - 1, AdjudicatedDifferent,
			"audit: 1 of 300 sampled items failed in A1 (1 different, 0 pending; 400 applied)"},
		{"holdout unclear", false, 0, AdjudicatedUnclear, ""},
		{"holdout outside", false, AuditSampleSize, "", ""},
		{"supplement pending", true, 3, "",
			"supplement audit: 1 of 100 sampled items failed in A1 (0 different, 1 pending; 150 applied)"},
		{"supplement different", true, AuditSupplementSize - 1, AdjudicatedDifferent,
			"supplement audit: 1 of 100 sampled items failed in A1 (1 different, 0 pending; 150 applied)"},
		{"supplement unclear", true, 0, AdjudicatedUnclear, ""},
		{"supplement outside", true, AuditSupplementSize, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := holdoutAuditStratum(400, 150)
			byID(&in, auditOrder(idsOf(&in, tc.supplement))[tc.rank]).Adjudication = tc.label
			d, _ := evaluateHoldoutAudit(t, &in)
			assert.Equal(t, tc.reason == "", d.Passed, d.Reason)
			assert.Equal(t, tc.reason, d.Reason)
		})
	}
}

// Run 34's failure under the audit: three gold errors in |A| (the pair
// same, both judges different), one among the extra Latin questions and a
// contested accept in each are never applied — none is sampled, none fails
// the holdout; all are reported.
func TestGoldErrorsAndContestedAcceptsNoLongerFailAHoldout(t *testing.T) {
	in := holdoutAuditStratum(400, 150)
	different := [2]Answer{answer("different", ""), answer("different", "")}
	for _, i := range []int{2, 40, 41, 450} {
		in.Items[i].Judges, in.Items[i].Adjudication = different, ""
	}
	in.Items[5].Judges[1], in.Items[5].Adjudication = answer("different", ""), ""
	in.Items[460].Judges[1], in.Items[460].Adjudication = answer("different", ""), ""
	d, m := evaluateHoldoutAudit(t, &in)
	require.True(t, d.Passed, d.Reason)
	assert.Equal(t, 4, m.Errors, "the gold errors and the pending contested accept, still counted")
	assert.Equal(t, []int64{3, 41, 42}, m.Audit.GoldErrorIDs)
	assert.Equal(t, 396, m.Audit.Applied)
	assert.Equal(t, 1, m.Contested)
	assert.Equal(t, 2, m.Supplement.Errors, "the gold error and the contested accept, reported")
	assert.Equal(t, []int64{451}, m.Supplement.Audit.GoldErrorIDs)
	assert.Equal(t, 148, m.Supplement.Audit.Applied)
	for _, id := range []int64{3, 6, 41, 42, 451, 461} {
		assert.NotContains(t, m.Audit.Sample, id)
		assert.NotContains(t, m.Supplement.Audit.Sample, id)
	}

	// A pending holdout sample item fails it on the audit, not on the
	// extra questions' errors.
	byID(&in, m.Audit.Sample[0]).Adjudication = ""
	d, _ = evaluateHoldoutAudit(t, &in)
	assert.False(t, d.Passed)
	assert.Equal(t, "audit: 1 of 300 sampled items failed in A1 (0 different, 1 pending; 396 applied)", d.Reason)
	byID(&in, m.Audit.Sample[0]).Adjudication = AdjudicatedSame

	// The same run under the bound fails on them.
	d, _ = evaluateCensus(t, &in, &holdoutOpts)
	assert.False(t, d.Passed)
}

// A holdout that applied nothing fails; extra questions that applied
// nothing fail nothing (no Latin is applied from them).
func TestAHoldoutWithNothingAppliedFails(t *testing.T) {
	in := holdoutAuditStratum(60, 20)
	different := [2]Answer{answer("different", ""), answer("different", "")}
	for i := range 60 {
		in.Items[i].Judges = different
	}
	d, m := evaluateHoldoutAudit(t, &in)
	assert.False(t, d.Passed)
	assert.Zero(t, m.Audit.Applied)
	assert.Equal(t, "holdout: nothing applied in A1", d.Reason)

	in = holdoutAuditStratum(60, 20)
	for i := 60; i < 80; i++ {
		in.Items[i].Judges = different
	}
	d, m = evaluateHoldoutAudit(t, &in)
	assert.True(t, d.Passed, d.Reason)
	assert.Zero(t, m.Supplement.Audit.Applied)
	assert.Empty(t, m.Supplement.Audit.Sample)
}

// The reproduction is gated on the applied set, as under the census audit:
// flips on items never applied fail nothing (run 34: production_a changed
// 29 of 200 repeats, 2 of its 112 applied ones).
func TestAHoldoutGatesTheRepeatsOfItsAppliedSet(t *testing.T) {
	in := repeatStratum(100, always, func(i int) bool { return i%5 >= 2 })
	r := EvaluateWith(&in, &holdoutAuditOpts)
	assert.Empty(t, r.Decision.Reasons)
	assert.True(t, r.Decision.Strata[0].Passed, r.Decision.Strata[0].Reason)
	assert.Equal(t, PooledRepeatApplied, r.PooledRepeat.Gate)

	in = repeatStratum(100, func(i int) bool { return i >= 3 }, always)
	r = EvaluateWith(&in, &holdoutAuditOpts)
	assert.Contains(t, r.Decision.Reasons, "applied repeat: production_a reproduced 17 of 20")
}

// The suite is gated as under the census audit: an item all four answered
// same fails while pending; one only the pair accepted fails nothing.
func TestTheSuiteGatesOnlyFourSameUnderTheHoldoutAudit(t *testing.T) {
	same := Answer{Valid: true, Canonical: "same", Tier: "merge"}
	both := [2]Answer{same, same}
	suite := func(label string, judges [2]Answer) Input {
		in := holdoutAuditStratum(60, 10)
		in.Items = append(in.Items, Item{ID: 9000, Stratum: "bucket", Script: "cyrl", Weight: 1, Holdout: true,
			Adversarial: true, Adjudication: label, Judges: judges,
			Candidates: map[Combo][2]Answer{{Arm: ArmA, Batch: 1}: both}})
		return in
	}
	in := suite("", both)
	d, m := evaluateHoldoutAudit(t, &in)
	assert.False(t, d.Passed)
	assert.Equal(t, 1, m.Adversarial.Failed)
	assert.NotContains(t, m.Audit.Sample, int64(9000))
	assert.NotContains(t, m.Supplement.Audit.Sample, int64(9000))

	in = suite("", [2]Answer{answer("different", ""), answer("different", "")})
	d, _ = evaluateHoldoutAudit(t, &in)
	assert.True(t, d.Passed, d.Reason)
}
