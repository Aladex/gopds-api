package authortriage

import (
	"cmp"
	"slices"
)

// Consensus is what the stored answers agree on.
type Consensus struct {
	// Agreed is the judge_a answer of the lowest run whose judges both gave
	// the single canonical; nil when there is none.
	Agreed *Answer
	// FourModel: in some group of a run whose judges agree, the production
	// pair gave that canonical too.
	FourModel bool
	// Pair is the production_a answer of the lowest such group: its run, arm
	// and batch size are where the four-model proof is. Nil without
	// FourModel.
	Pair *Answer
	// Conflict: the groups' judges named more than one canonical. A conflict
	// across runs is no consensus.
	Conflict bool
}

// group is one asking of the models: a run, an arm and a batch size.
type group struct {
	run   int64
	arm   string
	batch int
}

// JudgesConsensus reads the answers grouped by (RunID, Arm, BatchSize). A
// group's judge answer is the canonical both judge_a and judge_b gave, neither
// refused. One canonical across every group that has a judge answer is the
// consensus; more than one is a conflict. Four models agree when the
// production pair gave it in some group of a run whose judges agree; the
// lowest such group is the Pair.
func JudgesConsensus(answers []Answer) Consensus {
	bySlot := map[group]map[string][]*Answer{}
	var groups []group
	for i := range answers {
		a := &answers[i]
		g := group{run: a.RunID, arm: a.Arm, batch: a.BatchSize}
		if bySlot[g] == nil {
			bySlot[g] = map[string][]*Answer{}
			groups = append(groups, g)
		}
		bySlot[g][a.Slot] = append(bySlot[g][a.Slot], a)
	}
	slices.SortFunc(groups, func(x, y group) int {
		return cmp.Or(cmp.Compare(x.run, y.run), cmp.Compare(x.arm, y.arm), cmp.Compare(x.batch, y.batch))
	})

	judged := map[group]string{}
	canonicals := map[string]bool{}
	for _, g := range groups {
		a, aOK := slotAnswer(bySlot[g][SlotJudgeA])
		b, bOK := slotAnswer(bySlot[g][SlotJudgeB])
		if aOK && bOK && a.Canonical == b.Canonical {
			judged[g] = a.Canonical
			canonicals[a.Canonical] = true
		}
	}
	if len(canonicals) != 1 {
		return Consensus{Conflict: len(canonicals) > 1}
	}

	var c Consensus
	agreedRuns := map[int64]string{}
	for _, g := range groups {
		canonical, ok := judged[g]
		if !ok {
			continue
		}
		agreedRuns[g.run] = canonical
		if c.Agreed == nil {
			c.Agreed, _ = slotAnswer(bySlot[g][SlotJudgeA])
		}
	}
	// An eval run asks the judges once (no arm, a batch of one) and the
	// production pair per arm and batch size: the pair counts in any group of
	// a run whose judges agree.
	for _, g := range groups {
		canonical, ok := agreedRuns[g.run]
		if !ok {
			continue
		}
		pa, paOK := slotAnswer(bySlot[g][SlotProductionA])
		pb, pbOK := slotAnswer(bySlot[g][SlotProductionB])
		if paOK && pbOK && pa.Canonical == canonical && pb.Canonical == canonical && c.Pair == nil {
			c.FourModel, c.Pair = true, pa
		}
	}
	return c
}

// slotAnswer is a slot's one answer in a group: ok is false when the slot did
// not answer, refused, or answered more than one canonical.
func slotAnswer(answers []*Answer) (*Answer, bool) {
	if len(answers) == 0 {
		return nil, false
	}
	for _, a := range answers {
		if a.Refused || a.Canonical == "" || a.Canonical != answers[0].Canonical {
			return nil, false
		}
	}
	return answers[0], true
}
