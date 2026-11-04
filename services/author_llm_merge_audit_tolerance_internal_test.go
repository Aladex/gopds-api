package services

import (
	"testing"

	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
)

// Only merge8 samples 473 and tolerates one different (E4q plan B); every
// other merge mode keeps 300 with none, so its report is unchanged on a
// recompute.
func TestOnlyMerge8ToleratesADifferent(t *testing.T) {
	for _, mode := range models.AuthorLLMEvalMergeModes() {
		o := mergeOptions(mode)
		if mode == models.AuthorLLMEvalModeMerge8 {
			assert.Equal(t, llmeval.AuditSampleTolerant, o.AuditSample, mode)
			assert.Equal(t, llmeval.AuditToleratedDifferent, o.AuditTolerated, mode)
			continue
		}
		assert.Zero(t, o.AuditSample, mode)
		assert.Zero(t, o.AuditTolerated, mode)
	}
}
