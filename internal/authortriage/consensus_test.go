package authortriage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJudgesConsensusGroups(t *testing.T) {
	n := ptrName(person("Михаил", "Андреев"))
	other := ptrName(person("Андрей", "Михайлов"))

	t.Run("the same canonical in two groups is no conflict", func(t *testing.T) {
		got := JudgesConsensus(append(answers(31, "c1", "c2", "person", n), answers(14, "c1", "c3", "person", n)...))
		assert.False(t, got.Conflict)
		require.NotNil(t, got.Agreed)
		assert.Equal(t, int64(14), got.Agreed.RunID, "the lowest run's judge_a")
		assert.Equal(t, "judge_a", got.Agreed.Slot)
		assert.False(t, got.FourModel)
	})

	t.Run("two canonicals are a conflict", func(t *testing.T) {
		got := JudgesConsensus(append(answers(14, "c1", "c1", "person", n), answers(27, "c9", "c9", "person", other)...))
		assert.True(t, got.Conflict)
		assert.Nil(t, got.Agreed)
		assert.False(t, got.FourModel)
	})

	t.Run("the same run in two arms or batch sizes is two groups", func(t *testing.T) {
		a := answers(14, "c1", "c1", "person", n)
		b := answers(14, "c9", "c9", "person", other)
		for i := range b {
			b[i].Arm = "b"
		}
		assert.True(t, JudgesConsensus(append(a, b...)).Conflict, "arms")
		for i := range b {
			b[i].Arm, b[i].BatchSize = "a", 1
		}
		assert.True(t, JudgesConsensus(append(a, b...)).Conflict, "batch sizes")
	})

	t.Run("the pair counts only inside the group the judges agree in", func(t *testing.T) {
		judged := answers(14, "c1", "c2", "person", n)
		// Run 27: the pair gives c1, the judges did not answer.
		pair := answers(27, "c1", "c1", "person", n)[:2]
		got := JudgesConsensus(append(judged, pair...))
		require.NotNil(t, got.Agreed)
		assert.False(t, got.FourModel)

		got = JudgesConsensus(answers(14, "c1", "c1", "person", n))
		assert.True(t, got.FourModel)

		// One of the pair refused: no four-model.
		refused := answers(14, "c1", "c1", "person", n)
		refused[1].Refused = true
		assert.False(t, JudgesConsensus(refused).FourModel)
	})

	t.Run("judges who disagree or refuse give nothing", func(t *testing.T) {
		split := answers(14, "c1", "c1", "person", n)
		split[3].Canonical = "c5"
		got := JudgesConsensus(split)
		assert.Nil(t, got.Agreed)
		assert.False(t, got.Conflict)

		refused := answers(14, "c1", "c1", "person", n)
		refused[2].Refused = true
		assert.Nil(t, JudgesConsensus(refused).Agreed)

		missing := answers(14, "c1", "c1", "person", n)[:3]
		assert.Nil(t, JudgesConsensus(missing).Agreed, "judge_b never answered")

		assert.Nil(t, JudgesConsensus(nil).Agreed)
	})

	t.Run("a group whose judge answered twice differently has no judge answer", func(t *testing.T) {
		twice := answers(14, "c1", "c1", "person", n)
		dup := twice[2]
		dup.Canonical = "c7"
		got := JudgesConsensus(append(twice, dup))
		assert.Nil(t, got.Agreed)
	})
}
