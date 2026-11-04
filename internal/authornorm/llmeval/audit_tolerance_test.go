package llmeval

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E4q plan B: a mode may fix a wider audit before any label — merge8
// samples 473 applied items and tolerates one labeled different, the same
// 1% / 95% bound as 300 with none. A pending item still fails it; every
// other mode keeps 300 and no tolerance.

// tolerantOpts are merge8's audit options.
var tolerantOpts = func() Options {
	o := auditOpts
	o.AuditSample, o.AuditTolerated = AuditSampleTolerant, AuditToleratedDifferent
	return o
}()

// auditOrderOf is the stratum's item ids in the run's audit order.
func auditOrderOf(in *Input) []int64 {
	ids := make([]int64, 0, len(in.Items))
	for i := range in.Items {
		ids = append(ids, in.Items[i].ID)
	}
	slices.SortFunc(ids, func(a, b int64) int {
		ka, kb := AuditKey(in.AuditRun, a), AuditKey(in.AuditRun, b)
		return bytes.Compare(ka[:], kb[:])
	})
	return ids
}

// The tolerance is the bound's: one error is within 1% at 473, not at 472.
func TestTheToleratedAuditKeepsTheBound(t *testing.T) {
	assert.True(t, PassesBound(0, AuditSampleSize))
	assert.False(t, PassesBound(1, AuditSampleSize))
	assert.True(t, PassesBound(AuditToleratedDifferent, AuditSampleTolerant))
	assert.False(t, PassesBound(AuditToleratedDifferent, AuditSampleTolerant-1))
	assert.False(t, PassesBound(AuditToleratedDifferent+1, AuditSampleTolerant))
}

// 0 or 1 sampled item labeled different passes merge8's audit of 473; 2
// fail it, and so does 1 with a sampled item pending. A different outside
// the 473 is never read.
func TestTheToleratedAuditPassesOneDifferentInFourHundredSeventyThree(t *testing.T) {
	const applied = AuditSampleTolerant + 30
	for _, tc := range []struct {
		name                     string
		different, pending       []int // ranks in the audit order
		passed                   bool
		tolerated, failed, diffs int
	}{
		{"no different", nil, nil, true, 0, 0, 0},
		{"one different", []int{0}, nil, true, 1, 0, 1},
		{"one different, the last rank", []int{AuditSampleTolerant - 1}, nil, true, 1, 0, 1},
		{"two different", []int{5, AuditSampleTolerant - 1}, nil, false, 1, 1, 2},
		{"one different, one pending", []int{5}, []int{7}, false, 1, 1, 1},
		{"one pending", nil, []int{AuditSampleTolerant - 1}, false, 0, 1, 0},
		{"two different, one outside", []int{5, AuditSampleTolerant}, nil, true, 1, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := auditStratum(applied)
			ids := auditOrderOf(&in)
			for _, r := range tc.different {
				byID(&in, ids[r]).Adjudication = AdjudicatedDifferent
			}
			for _, r := range tc.pending {
				byID(&in, ids[r]).Adjudication = ""
			}
			d, m := evaluateCensus(t, &in, &tolerantOpts)
			require.NotNil(t, m.Audit)
			assert.Equal(t, tc.passed, d.Passed, d.Reason)
			assert.Equal(t, ids[:AuditSampleTolerant], m.Audit.Sample, "the first 473 in the audit order")
			assert.Equal(t, applied, m.Audit.Applied)
			assert.Equal(t, tc.tolerated, m.Audit.Tolerated)
			assert.Equal(t, tc.failed, m.Audit.Failed)
			assert.Equal(t, tc.diffs, m.Audit.AdjudicatedDifferent)
		})
	}
}

// The same census under every other mode's options: 300 sampled, and one
// different fails it.
func TestOtherModesKeepThreeHundredAndNoTolerance(t *testing.T) {
	in := auditStratum(AuditSampleTolerant + 30)
	ids := auditOrderOf(&in)
	d, m := evaluateAudit(t, &in)
	assert.True(t, d.Passed, d.Reason)
	assert.Equal(t, ids[:AuditSampleSize], m.Audit.Sample)
	assert.Zero(t, m.Audit.Tolerated)

	byID(&in, ids[0]).Adjudication = AdjudicatedDifferent
	d, m = evaluateAudit(t, &in)
	assert.False(t, d.Passed)
	assert.Equal(t, 1, m.Audit.Failed)
	assert.Zero(t, m.Audit.Tolerated)

	// A rank past 300 is outside every other mode's sample.
	byID(&in, ids[0]).Adjudication = AdjudicatedSame
	byID(&in, ids[AuditSampleSize]).Adjudication = ""
	d, _ = evaluateAudit(t, &in)
	assert.True(t, d.Passed, d.Reason)
}
