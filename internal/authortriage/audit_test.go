package authortriage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAuditGateFollowsDecision(t *testing.T) {
	for _, c := range []struct {
		step               Step
		labeled, different int
		passes             bool
	}{
		{StepFourModel, 300, 0, true},
		{StepFourModel, 300, 1, false},
		{StepFourModel, 299, 0, false},
		{StepJudgesOnly, 50, 2, true},
		{StepJudgesOnly, 50, 3, false},
		{StepJudgesOnly, 49, 0, false},
		{StepParticleOnly, 50, 2, true},
		{StepParticleOnly, 51, 3, false},
		{Step(0), 1000, 0, false},
		{Step(9), 1000, 0, false},
	} {
		assert.Equal(t, c.passes, AuditPasses(c.step, Audit{Labeled: c.labeled, Different: c.different}),
			"step %d, %d labeled, %d different", c.step, c.labeled, c.different)
	}
}

func TestSampleSizesFollowDecision(t *testing.T) {
	assert.Equal(t, 300, SampleSize(StepFourModel))
	assert.Equal(t, 60, ReserveSize(StepFourModel))
	for _, s := range Steps()[1:] {
		assert.Equal(t, 50, SampleSize(s))
		assert.Equal(t, 10, ReserveSize(s))
	}
}

// The audit commit (Task 6) records the labels and changes this test on
// purpose: until then no step applies.
func TestNothingIsAuditedYet(t *testing.T) {
	assert.Empty(t, AuditedSteps())
	assert.Empty(t, AuditLog())
}

func TestAuditLogListsEveryStepOfTheAudit(t *testing.T) {
	saved := auditV1
	t.Cleanup(func() { auditV1 = saved })
	auditV1 = map[Step]Audit{
		StepParticleOnly: {Labeled: 50, Different: 1},
		StepFourModel:    {Labeled: 300},
		StepJudgesOnly:   {Labeled: 50, Different: 3},
	}
	assert.Equal(t, []Step{StepFourModel, StepParticleOnly}, AuditedSteps())
	assert.Equal(t, []AuditEntry{
		{Step: StepFourModel, Labeled: 300, Different: 0, Passed: true},
		{Step: StepJudgesOnly, Labeled: 50, Different: 3, Passed: false},
		{Step: StepParticleOnly, Labeled: 50, Different: 1, Passed: true},
	}, AuditLog())
}
