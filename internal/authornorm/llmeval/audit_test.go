package llmeval

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditOpts are the merge gate's options for a census mode under the
// audit (merge1, merge2, merge4).
var auditOpts = func() Options {
	o := contestedOpts
	o.GateRule = GateRuleCensusAudit
	return o
}()

// auditRun is the fixture's run id, the audit order's seed.
const auditRun = 36

// auditStratum is mergeStratum(n, 0) of run auditRun with every item
// applied and labeled same.
func auditStratum(n int) Input {
	in := mergeStratum(n, 0)
	in.AuditRun = auditRun
	for i := range in.Items {
		in.Items[i].Adjudication = AdjudicatedSame
	}
	return in
}

// evaluateAudit evaluates a census under the audit and returns its
// decision and combo.
func evaluateAudit(t *testing.T, in *Input) (StratumDecision, *ComboMetrics) {
	t.Helper()
	d, m := evaluateCensus(t, in, &auditOpts)
	require.NotNil(t, m.Audit)
	require.NotNil(t, m.Census)
	return d, m
}

// byID is the fixture's item of id.
func byID(in *Input, id int64) *Item {
	for i := range in.Items {
		if in.Items[i].ID == id {
			return &in.Items[i]
		}
	}
	return nil
}

// A sampled item still pending, or labeled different, fails the census;
// same and unclear pass it. The report names the rule and the counts.
func TestTheAuditSampleDecidesACensus(t *testing.T) {
	for _, tc := range []struct {
		label  string
		passed bool
		want   AuditMetrics
		reason string
	}{
		{"", false, AuditMetrics{AdjudicatedSame: 59, Pending: 1, Failed: 1},
			"audit: 1 of 60 sampled items failed in A1 (0 different, 1 pending; 60 applied)"},
		{AdjudicatedDifferent, false, AuditMetrics{AdjudicatedSame: 59, AdjudicatedDifferent: 1, Failed: 1},
			"audit: 1 of 60 sampled items failed in A1 (1 different, 0 pending; 60 applied)"},
		{AdjudicatedSame, true, AuditMetrics{AdjudicatedSame: 60}, ""},
		{AdjudicatedUnclear, true, AuditMetrics{AdjudicatedSame: 59, Unclear: 1}, ""},
	} {
		t.Run("label "+tc.label, func(t *testing.T) {
			in := auditStratum(60)
			in.Items[3].Adjudication = tc.label
			d, m := evaluateAudit(t, &in)
			assert.Equal(t, tc.passed, d.Passed, d.Reason)
			assert.Equal(t, GateRuleCensusAudit, d.Rule)
			assert.Equal(t, tc.reason, d.Reason)
			got := *m.Audit
			got.Sample, got.GoldErrorIDs = nil, nil
			tc.want.Applied = 60
			assert.Equal(t, tc.want, got)
			assert.Len(t, m.Audit.Sample, 60)
		})
	}
}

// A gold error (the pair same, both judges different) and a contested
// accept (one judge different) are never applied: under the audit neither
// fails the census, and both are reported — the gold error by id, the
// contested accept in its review. Neither is sampled.
func TestAGoldErrorAndAContestedAcceptNoLongerFailACensus(t *testing.T) {
	in := auditStratum(60)
	in.Items[2].Judges = [2]Answer{answer("different", ""), answer("different", "")}
	in.Items[5].Judges[1] = answer("different", "")
	in.Items[2].Adjudication, in.Items[5].Adjudication = "", ""
	d, m := evaluateAudit(t, &in)
	assert.True(t, d.Passed, d.Reason)
	assert.Equal(t, 58, m.Audit.Applied)
	assert.NotContains(t, m.Audit.Sample, int64(3))
	assert.NotContains(t, m.Audit.Sample, int64(6))
	assert.Equal(t, 1, m.Audit.GoldErrors)
	assert.Equal(t, []int64{3}, m.Audit.GoldErrorIDs)
	assert.Equal(t, 1, m.Contested)
	require.NotNil(t, m.ContestedReview)
	assert.Equal(t, map[string][]int64{"cyrl": {6}}, m.ContestedReview.IDs)
	assert.Equal(t, 2, m.Errors, "the errors are still counted, only not gated")

	// The same census under the old rule fails on them.
	d, _ = evaluateCensus(t, &in, &censusOpts)
	assert.False(t, d.Passed)
}

// The sample is the first AuditSampleSize applied items in the run's audit
// order (sha256 of run id and item id): the same every time, another for
// another run; an item outside it is never read — pending, it fails
// nothing.
func TestTheAuditSampleIsDeterministic(t *testing.T) {
	in := auditStratum(AuditSampleSize + 40)
	_, m := evaluateAudit(t, &in)
	require.Len(t, m.Audit.Sample, AuditSampleSize)
	assert.Equal(t, AuditSampleSize+40, m.Audit.Applied)

	ids := make([]int64, 0, len(in.Items))
	for i := range in.Items {
		ids = append(ids, in.Items[i].ID)
	}
	slices.SortFunc(ids, func(a, b int64) int {
		ka, kb := AuditKey(auditRun, a), AuditKey(auditRun, b)
		return bytes.Compare(ka[:], kb[:])
	})
	assert.Equal(t, ids[:AuditSampleSize], m.Audit.Sample, "the audit order")
	assert.NotEqual(t, ids[:AuditSampleSize], slices.Sorted(slices.Values(m.Audit.Sample)), "not the id order")

	_, again := evaluateAudit(t, &in)
	assert.Equal(t, m.Audit.Sample, again.Audit.Sample)

	other := in
	other.AuditRun = auditRun + 1
	_, o := evaluateAudit(t, &other)
	assert.NotEqual(t, m.Audit.Sample, o.Audit.Sample, "another run samples others")

	// Every item outside the sample pending: the census still passes; one
	// inside pending fails it.
	for _, id := range ids[AuditSampleSize:] {
		byID(&in, id).Adjudication = ""
	}
	d, _ := evaluateAudit(t, &in)
	assert.True(t, d.Passed, d.Reason)
	byID(&in, ids[AuditSampleSize-1]).Adjudication = ""
	d, _ = evaluateAudit(t, &in)
	assert.False(t, d.Passed)
}

// AuditKey is sha256 of the UTF-8 text "<run id>:<item id>", as the
// coordinator recomputes it (sha256 of "36:7").
func TestAuditKeyIsTheRunAndItemText(t *testing.T) {
	key := AuditKey(36, 7)
	assert.Equal(t, "384bc4fba543322e02e368f8c4067b0cd3eb027223559ca5fd6203656a3d2211", hex.EncodeToString(key[:]))
}

// A census that applied nothing fails, however its sample reads; one with
// an unsettled candidate fails before its audit.
func TestACensusWithNothingAppliedOrUnsettledFails(t *testing.T) {
	in := auditStratum(60)
	for i := range in.Items {
		in.Items[i].Judges = [2]Answer{answer("different", ""), answer("different", "")}
	}
	d, m := evaluateAudit(t, &in)
	assert.False(t, d.Passed)
	assert.Zero(t, m.Audit.Applied)
	assert.Equal(t, "census: nothing applied in A1", d.Reason)

	in = auditStratum(60)
	in.Items[1].Candidates = map[Combo][2]Answer{}
	d, m = evaluateAudit(t, &in)
	assert.False(t, d.Passed)
	assert.False(t, m.Census.Complete)
	assert.Equal(t, "census: 1 of 60 candidates without a settled pair answer in A1", d.Reason)
}

// The suite under the audit: an item all four answered same fails while
// pending, passes labeled same; one only the pair accepted (the judges
// answered different) is never applied and fails nothing.
func TestTheSuiteGatesOnlyFourSameUnderTheAudit(t *testing.T) {
	same := Answer{Valid: true, Canonical: "same", Tier: "merge"}
	both := [2]Answer{same, same}
	suite := func(label string, judges [2]Answer) Input {
		in := auditStratum(60)
		in.Items = append(in.Items, Item{ID: 9000, Stratum: "bucket", Script: "cyrl", Weight: 1, Holdout: true,
			Adversarial: true, Adjudication: label, Judges: judges,
			Candidates: map[Combo][2]Answer{{Arm: ArmA, Batch: 1}: both}})
		return in
	}
	in := suite("", both)
	d, m := evaluateAudit(t, &in)
	assert.False(t, d.Passed)
	assert.Equal(t, 1, m.Adversarial.Failed)
	assert.NotContains(t, m.Audit.Sample, int64(9000), "a suite item is not in the census's audit")

	in = suite(AdjudicatedSame, both)
	d, _ = evaluateAudit(t, &in)
	assert.True(t, d.Passed, d.Reason)

	in = suite("", [2]Answer{answer("different", ""), answer("different", "")})
	d, m = evaluateAudit(t, &in)
	assert.True(t, d.Passed, d.Reason)
	assert.Equal(t, 1, m.Adversarial.PairAccepted)
	assert.Zero(t, m.Adversarial.Accepted)
}
