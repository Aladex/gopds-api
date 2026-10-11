package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The card shows each participant's changed repeats. It reads them from the
// report's repeat counts, so a report stored before the share existed (run 5)
// shows them too.
func TestReportSummaryShowsTheRepeatChange(t *testing.T) {
	raw := []byte(`{"evaluation":{"repeat":[` +
		`{"participant":"production_a","matched":196,"total":200},` +
		`{"participant":"production_b","matched":198,"total":200},` +
		`{"participant":"judge_a","matched":197,"total":200},` +
		`{"participant":"judge_b","matched":196,"total":200}],` +
		`"decision":{"registrable":true,"strata":[{"stratum":"initials.cyrl","passed":true}]}}}`)
	sum, err := summarizeEvalReport(raw, "ab")
	require.NoError(t, err)
	assert.Equal(t, []AuthorLLMRepeatChange{
		{Participant: "production_a", Changed: 4, Total: 200},
		{Participant: "production_b", Changed: 2, Total: 200},
		{Participant: "judge_a", Changed: 3, Total: 200},
		{Participant: "judge_b", Changed: 4, Total: 200},
	}, sum.Repeats)
	assert.Equal(t, 1, sum.StrataPassed)
	assert.True(t, sum.Registrable)
}
