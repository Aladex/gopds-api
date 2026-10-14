package services

import (
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
)

// The per-stratum invalid gate reads each job's answer: a job that ended
// without one is invalid; a job still waiting, or never asked, is neither
// valid nor invalid and counts in no share.
func TestAnswerOfMarksTheEndedJobsInvalid(t *testing.T) {
	job := func(status models.AuthorLLMJobStatus) *database.AuthorLLMEvalAnswer {
		return &database.AuthorLLMEvalAnswer{Status: string(status), CanonicalSHA256: []byte{1}}
	}
	assert.Equal(t, llmeval.Answer{}, answerOf(nil))
	assert.Equal(t, llmeval.Answer{}, answerOf(job(models.AuthorLLMJobPending)))
	assert.Equal(t, llmeval.Answer{}, answerOf(job(models.AuthorLLMJobClaimed)))
	assert.Equal(t, llmeval.Answer{Invalid: true}, answerOf(job(models.AuthorLLMJobInvalid)))
	assert.Equal(t, llmeval.Answer{Invalid: true}, answerOf(job(models.AuthorLLMJobFailed)))
	answered := answerOf(job(models.AuthorLLMJobAnswered))
	assert.True(t, answered.Valid)
	assert.False(t, answered.Invalid)
}

// The pooled repeat gate reads each item's repeats: whether each
// participant asked again reproduced its first answer, and nothing for a
// participant whose repeat is missing or waiting.
func TestCountRepeatsReturnsTheItemsRepeats(t *testing.T) {
	answered := func(sha byte) *database.AuthorLLMEvalAnswer {
		return &database.AuthorLLMEvalAnswer{Status: string(models.AuthorLLMJobAnswered), CanonicalSHA256: []byte{sha}}
	}
	first := func(slot llmreq.Slot) evalAnswerKey {
		if slot == llmreq.SlotJudgeA || slot == llmreq.SlotJudgeB {
			return evalAnswerKey{string(models.AuthorLLMPurposeEvalJudge), string(slot), "", 1, 0}
		}
		return evalAnswerKey{string(models.AuthorLLMPurposeEvalCandidate), string(slot), llmeval.ArmA, 1, 0}
	}
	again := func(slot llmreq.Slot) evalAnswerKey {
		k := first(slot)
		k.purpose, k.repeat = string(models.AuthorLLMPurposeEvalRepeat), 1
		return k
	}
	jobs := map[evalAnswerKey]*database.AuthorLLMEvalAnswer{}
	for _, slot := range llmreq.Slots() {
		jobs[first(slot)] = answered(1)
	}
	jobs[again(llmreq.SlotProductionA)] = answered(1)
	jobs[again(llmreq.SlotProductionB)] = answered(2)
	jobs[again(llmreq.SlotJudgeA)] = &database.AuthorLLMEvalAnswer{Status: string(models.AuthorLLMJobPending)}
	counts := map[string]*llmeval.ParticipantCount{}
	item := countRepeats(counts, jobs)
	assert.Equal(t, map[string]bool{"production_a": true, "production_b": false}, item)
	assert.Equal(t, 1, counts["production_a"].Matched)
	assert.Equal(t, 0, counts["production_b"].Matched)
	assert.Nil(t, counts["judge_a"])
	assert.Nil(t, countRepeats(counts, map[evalAnswerKey]*database.AuthorLLMEvalAnswer{}), "an item not repeated")
}
