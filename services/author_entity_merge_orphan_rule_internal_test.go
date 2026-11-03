package services

import (
	"encoding/hex"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
)

// mergeAnswer is an answered eval job of item on slot with word.
func mergeAnswer(item int64, slot llmreq.Slot, purpose models.AuthorLLMJobPurpose, repeat int, word string) database.AuthorLLMEvalAnswer {
	sum, _ := hex.DecodeString(mergeCanonicalHex(word))
	return database.AuthorLLMEvalAnswer{EvalItemID: item, Slot: string(slot), Purpose: string(purpose), RepeatNo: repeat,
		Status: string(models.AuthorLLMJobAnswered), CanonicalSHA256: sum}
}

// An orphan's same is applied only when no production answer of its item,
// a repeat included, is other than same; another kind keeps the rule of
// first answers, and a judge's repeat is no production answer.
func TestAnOrphanAsksASameOfEveryProductionAnswer(t *testing.T) {
	var answers []database.AuthorLLMEvalAnswer
	for item := int64(1); item <= 3; item++ {
		for _, slot := range llmreq.Slots() {
			purpose := models.AuthorLLMPurposeEvalCandidate
			if slot == llmreq.SlotJudgeA || slot == llmreq.SlotJudgeB {
				purpose = models.AuthorLLMPurposeEvalJudge
			}
			answers = append(answers, mergeAnswer(item, slot, purpose, 0, llmreq.MergeSame))
		}
	}
	answers = append(answers,
		mergeAnswer(2, llmreq.SlotProductionB, models.AuthorLLMPurposeEvalRepeat, 1, llmreq.MergeDifferent),
		mergeAnswer(3, llmreq.SlotJudgeA, models.AuthorLLMPurposeEvalRepeat, 1, llmreq.MergeCannotTell))
	verdicts, dissent := mergeVerdicts(answers), mergeProductionDissent(answers)

	assert.True(t, mergeApplyAccepted(authorentity.CandidateOrphan, verdicts[1], dissent[1]))
	assert.False(t, mergeApplyAccepted(authorentity.CandidateOrphan, verdicts[2], dissent[2]))
	assert.True(t, mergeApplyAccepted(authorentity.CandidateOrphan, verdicts[3], dissent[3]))
	assert.True(t, mergeApplyAccepted(authorentity.CandidateBucket, verdicts[2], dissent[2]))
	assert.True(t, mergeApplyAccepted(authorentity.CandidatePenName, verdicts[2], dissent[2]))
}

// Only an orphan item is withheld by a production answer other than same;
// a judge's repeat withholds nothing.
func TestOnlyAnOrphanIsWithheldByAProductionDissent(t *testing.T) {
	jobs := map[evalAnswerKey]*database.AuthorLLMEvalAnswer{}
	add := func(slot llmreq.Slot, purpose models.AuthorLLMJobPurpose, repeat int, word string) {
		a := mergeAnswer(1, slot, purpose, repeat, word)
		jobs[evalAnswerKey{purpose: string(purpose), slot: string(slot), repeat: repeat}] = &a
	}
	add(llmreq.SlotProductionA, models.AuthorLLMPurposeEvalCandidate, 0, llmreq.MergeSame)
	add(llmreq.SlotJudgeA, models.AuthorLLMPurposeEvalRepeat, 1, llmreq.MergeDifferent)
	assert.False(t, mergeItemWithheld(authorentity.CandidateOrphan, jobs))
	add(llmreq.SlotProductionA, models.AuthorLLMPurposeEvalRepeat, 1, llmreq.MergeCannotTell)
	assert.True(t, mergeItemWithheld(authorentity.CandidateOrphan, jobs))
	for _, kind := range []string{authorentity.CandidatePenName, authorentity.CandidateBucket} {
		assert.False(t, mergeItemWithheld(kind, jobs), kind)
	}
}
