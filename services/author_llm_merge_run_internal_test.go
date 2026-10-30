package services

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The production rule of a merge run: a decision only for a pair agreed
// same that both judges confirmed; every other verdict says why not.
func TestTheMergeRunsProductionRule(t *testing.T) {
	same := sha256.Sum256(llmreq.MergeCanonical(llmreq.MergeSame))
	different := sha256.Sum256(llmreq.MergeCanonical(llmreq.MergeDifferent))
	judge := int64(7)
	verdict := func(value models.AuthorLLMVerdictValue, canonical []byte, judgement string) *database.AuthorLLMMergeVerdict {
		v := &database.AuthorLLMMergeVerdict{Verdict: string(value), Canonical: canonical, JudgeA: &judge,
			JudgeB: &judge}
		if judgement != "" {
			v.Judgement = &judgement
		}
		return v
	}
	agree := models.AuthorLLMVerdictAgree
	for name, c := range map[string]struct {
		v    *database.AuthorLLMMergeVerdict
		want string
	}{
		"same, confirmed": {verdict(agree, same[:], string(models.AuthorLLMJudgementConfirmed)), ""},
		"different, confirmed": {verdict(agree, different[:], string(models.AuthorLLMJudgementConfirmed)),
			models.AuthorLLMMergeNotSame},
		"same, a judge doubts": {verdict(agree, same[:], string(models.AuthorLLMJudgementDisagree)),
			string(models.UnresolvedLLMJudgeDisagreement)},
		"same, a judge refused": {verdict(agree, same[:], string(models.AuthorLLMJudgementRefused)),
			string(models.UnresolvedLLMJudgeRefused)},
		"same, not judged": {verdict(agree, same[:], ""), string(models.UnresolvedLLMJudgeFailed)},
		"pair split": {verdict(models.AuthorLLMVerdictDisagree, same[:], ""),
			string(models.UnresolvedLLMDisagreement)},
		"pair refused": {verdict(models.AuthorLLMVerdictRefused, nil, ""),
			string(models.UnresolvedLLMRefused)},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, mergeVerdictReason(c.v))
		})
	}
}

// A frozen card is asked only when it reads as the worker reads it and
// hashes to the digest's input hash.
func TestAFrozenCardMustHashToItsDigest(t *testing.T) {
	book := authorentity.MergeCardBookOf("Оно", &authorentity.CardBookFields{Year: "1986", Lang: "ru"}, nil)
	a := authorentity.MergeCardOf([]string{"Кинг Стивен"}, nil, 2, []authorentity.MergeCardBook{book})
	b := authorentity.MergeCardOf([]string{"Стивен Кинг"}, nil, 1, []authorentity.MergeCardBook{book})
	card := authorentity.MergeInputJSON(&a, &b)
	sum := authorentity.MergeInputSHA256(&a, &b)
	assert.True(t, mergeCardHashes(card, sum[:]))
	other := authorentity.MergeInputSHA256(&b, &a)
	assert.False(t, mergeCardHashes(card, other[:]), "the sides swapped are another question")
	var loose map[string]any
	require.NoError(t, json.Unmarshal(card, &loose))
	loose["extra"] = true
	raw, err := json.Marshal(loose)
	require.NoError(t, err)
	assert.False(t, mergeCardHashes(raw, sum[:]), "a field the worker does not read")
	assert.False(t, mergeCardHashes(nil, sum[:]), "nothing frozen")
}
