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

	t.Run("the pair counts only inside the run the judges agree in", func(t *testing.T) {
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

	t.Run("the pair counts in any arm and batch size of the run the judges agree in", func(t *testing.T) {
		// The stored shape of an eval run: the judges once, with no arm and a
		// batch of one; the production pair per arm and batch size.
		stored := func(run int64, judges, pairA, pairB string) []Answer {
			out := []Answer{
				{RunID: run, Arm: "-", BatchSize: 1, Slot: SlotJudgeA, Canonical: judges, Form: "person", Name: n},
				{RunID: run, Arm: "-", BatchSize: 1, Slot: SlotJudgeB, Canonical: judges, Form: "person", Name: n},
			}
			for _, g := range []struct {
				arm   string
				batch int
				c     string
			}{{"A", 10, pairA}, {"B", 1, pairB}} {
				out = append(out,
					Answer{RunID: run, Arm: g.arm, BatchSize: g.batch, Slot: SlotProductionA, Canonical: g.c},
					Answer{RunID: run, Arm: g.arm, BatchSize: g.batch, Slot: SlotProductionB, Canonical: g.c})
			}
			return out
		}
		got := JudgesConsensus(stored(14, "c1", "c1", "c9"))
		require.NotNil(t, got.Agreed)
		assert.Equal(t, "-", got.Agreed.Arm)
		assert.True(t, got.FourModel, "the pair agrees with the judges in arm A of the same run")
		assert.False(t, JudgesConsensus(stored(14, "c1", "c9", "c9")).FourModel, "the pair never gives c1")

		judgesOnlyRun := stored(14, "c1", "c9", "c9")
		pairOnlyRun := stored(27, "c5", "c1", "c1")
		got = JudgesConsensus(append(judgesOnlyRun, pairOnlyRun[2:]...))
		require.NotNil(t, got.Agreed)
		assert.False(t, got.FourModel, "the pair of another run, whose judges did not answer, does not count")
	})

	t.Run("the confirming pair is the lowest group whose pair gives the canonical", func(t *testing.T) {
		stored := func(run int64, judges, pairA, pairB string) []Answer {
			out := []Answer{
				{RunID: run, Arm: "-", BatchSize: 1, Slot: SlotJudgeA, Canonical: judges, Form: "person", Name: n},
				{RunID: run, Arm: "-", BatchSize: 1, Slot: SlotJudgeB, Canonical: judges, Form: "person", Name: n},
			}
			for _, g := range []struct {
				arm   string
				batch int
				c     string
			}{{"A", 10, pairA}, {"B", 1, pairB}} {
				out = append(out,
					Answer{RunID: run, Arm: g.arm, BatchSize: g.batch, Slot: SlotProductionA, Canonical: g.c},
					Answer{RunID: run, Arm: g.arm, BatchSize: g.batch, Slot: SlotProductionB, Canonical: g.c})
			}
			return out
		}
		// Run 14: the judges agree, the pair never does. Run 27: the judges
		// agree, the pair agrees in arm B only.
		got := JudgesConsensus(append(stored(14, "c1", "c9", "c8"), stored(27, "c1", "c7", "c1")...))
		require.NotNil(t, got.Agreed)
		assert.Equal(t, int64(14), got.Agreed.RunID, "the evidence's judges stay the lowest run's")
		require.True(t, got.FourModel)
		require.NotNil(t, got.Pair)
		assert.Equal(t, [3]any{int64(27), "B", 1}, [3]any{got.Pair.RunID, got.Pair.Arm, got.Pair.BatchSize},
			"the four-model proof is run 27, arm B, batch 1")
		assert.Equal(t, SlotProductionA, got.Pair.Slot)

		got = JudgesConsensus(append(stored(14, "c1", "c1", "c1"), stored(27, "c1", "c1", "c1")...))
		require.NotNil(t, got.Pair)
		assert.Equal(t, [3]any{int64(14), "A", 10}, [3]any{got.Pair.RunID, got.Pair.Arm, got.Pair.BatchSize},
			"the lowest of several")

		assert.Nil(t, JudgesConsensus(stored(14, "c1", "c9", "c9")).Pair, "no four-model, no pair")
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
