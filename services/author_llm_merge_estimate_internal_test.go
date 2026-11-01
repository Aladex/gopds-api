package services

import (
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
)

// merge6's cards list a hub's every book (E4g round 2): its estimate reads
// the frozen inputs' size, so a run of cards three times the measured mean
// is estimated three times the tokens per answer and is not stopped by its
// own overrun bound; a mode of ordinary cards is estimated exactly as
// before, and smaller cards never lower the measured figures.
func TestAWholeCardEstimateFollowsTheInputSize(t *testing.T) {
	sizes := DefaultAuthorLLMMergeEvalSizesOf(models.AuthorLLMEvalModeMerge6)
	sz := &sizes
	base := &mergeEvalCounts{holdout: 120, suite: 1000, anchors: 0}
	plain := mergeEstimateTokens(base, sz)
	assert.Equal(t, int64(evalParticipants*(120+120))*mergeTokensPerAnswer+1000*mergeTokensPerSuiteItem+
		mergeTokensPerRunOverall, plain, "no sizes: the measured figures")

	big := *base
	big.holdoutBytes, big.suiteBytes = 3*120*mergeReferenceInputBytes, 2*1000*mergeReferenceInputBytes
	assert.Equal(t, int64(evalParticipants*(120+120))*3*mergeTokensPerAnswer+1000*2*mergeTokensPerSuiteItem+
		mergeTokensPerRunOverall, mergeEstimateTokens(&big, sz))

	small := *base
	small.holdoutBytes, small.suiteBytes = 120*mergeReferenceInputBytes/2, 1000*mergeReferenceInputBytes/2
	assert.Equal(t, plain, mergeEstimateTokens(&small, sz), "smaller cards keep the measured figures")

	for mode, m := range mergeEvalModes {
		assert.Equal(t, mode == models.AuthorLLMEvalModeMerge6, m.wholeCards, mode)
	}
}
