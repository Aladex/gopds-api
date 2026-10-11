package services

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
)

// An anchor set frozen before run 4 stores its expected answers with an
// order. A model's answer no longer carries one, so the anchor matches on
// the roles alone.
func TestAnchorCountsMatchAStoredAnswerWithAnOrder(t *testing.T) {
	stored := json.RawMessage(`{"form":"person","order":"given_first",` +
		`"roles":[{"i":0,"role":"given"},{"i":1,"role":"family"}],"case_fix":[]}`)
	answered := sha256.Sum256([]byte(`{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"family"}],"case_fix":[]}`))
	anchors := []database.AuthorLLMEvalItemRow{{ID: 1, Expected: stored}}
	byItem := map[int64]map[evalAnswerKey]*database.AuthorLLMEvalAnswer{1: {}}
	for _, slot := range llmreq.Slots() {
		byItem[1][evalAnswerKey{string(models.AuthorLLMPurposeEvalAnchor), string(slot), "", 1, 0}] =
			&database.AuthorLLMEvalAnswer{Status: string(models.AuthorLLMJobAnswered), CanonicalSHA256: answered[:]}
	}
	for _, c := range anchorCounts(anchors, byItem) {
		assert.Equal(t, 1, c.Matched, c.Participant)
	}
}
