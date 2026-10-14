package llmeval

import (
	"slices"
	"strings"
	"testing"
)

// shapedStratum is a stratum shaped from a measured run: n holdout items both
// judges agree on and the pair agrees on in every combo. prodInvalid is how
// many items each production participant answered invalid in every combo,
// judgeInvalid how many judge answers each judge left invalid; repeats is how
// many of the stratum's items were repeated and reproduced how many of them
// each participant (production_a, production_b, judge_a, judge_b) reproduced.
type shapedStratum struct {
	name         string
	n            int
	prodInvalid  [2]int
	judgeInvalid [2]int
	repeats      int
	reproduced   [4]int
}

var repeatParticipants = [4]string{slotProductionA, slotProductionB, slotJudgeA, slotJudgeB}

// items builds the stratum's items. The production participants' invalid
// answers are on the first items, the judges' on the last ones, the repeats
// on the items in between, so no mark hides another.
func (s *shapedStratum) items(firstID int64) []Item {
	items := stratumItems(s.name, s.n, agreeX)
	for i := range items {
		it := &items[i]
		it.ID = firstID + int64(i)
		it.Repeated = nil
		for p := range 2 {
			if i < s.prodInvalid[p] {
				for c, ans := range it.Candidates {
					ans[p] = Answer{Invalid: true}
					it.Candidates[c] = ans
				}
			}
			if i >= s.n-s.judgeInvalid[p] {
				it.Judges[p] = Answer{Invalid: true}
			}
		}
	}
	start := max(s.prodInvalid[0], s.prodInvalid[1])
	for k := range s.repeats {
		it := &items[start+k]
		it.Repeated = map[string]bool{}
		for p, name := range repeatParticipants {
			it.Repeated[name] = k < s.reproduced[p]
		}
	}
	return items
}

// run14 is run 14 (v0.50.2) in shape: the run-wide invalid and repeat counts
// as measured, two clean strata (initials, 0.4–1.9% invalid per participant,
// 89 and 14 repeat items) and two junk strata that fail on invalid answers
// alone — one on a production participant, one on a judge — and carry the
// scattered repeats that broke the run-wide reproduction.
func run14() Input {
	in := healthy()
	in.Repeat = []ParticipantCount{
		{Participant: slotProductionA, Matched: 166, Total: 200}, {Participant: slotProductionB, Matched: 157, Total: 200},
		{Participant: slotJudgeA, Matched: 188, Total: 200}, {Participant: slotJudgeB, Matched: 167, Total: 200},
	}
	in.Invalid = []ParticipantCount{
		{Participant: slotProductionA, Matched: 6052, Total: 50912}, {Participant: slotProductionB, Matched: 8748, Total: 50912},
		{Participant: slotJudgeA, Matched: 1432, Total: 14837}, {Participant: slotJudgeB, Matched: 2590, Total: 14837},
	}
	strata := []shapedStratum{
		{name: "initials.cyrl", n: 900, prodInvalid: [2]int{4, 17}, judgeInvalid: [2]int{5, 8},
			repeats: 89, reproduced: [4]int{87, 86, 89, 88}},
		{name: "initials.latn", n: 900, prodInvalid: [2]int{9, 3}, judgeInvalid: [2]int{6, 4},
			repeats: 14, reproduced: [4]int{14, 13, 14, 14}},
		// No error at all, but production_b answered 12% invalid.
		{name: "single_field.cyrl", n: 900, prodInvalid: [2]int{36, 108}, repeats: 8, reproduced: [4]int{2, 1, 6, 3}},
		// No error at all, but judge_b answered 17.5% invalid.
		{name: "mononym.cyrl", n: 900, prodInvalid: [2]int{5, 5}, judgeInvalid: [2]int{20, 158},
			repeats: 8, reproduced: [4]int{3, 2, 7, 1}},
	}
	for i := range strata {
		in.Items = append(in.Items, strata[i].items(int64(i*10000+1))...)
	}
	return in
}

// Run 14 was not registrable for the run-wide invalid and repeat shares
// alone, which the junk strata made. The clean strata register; a stratum
// with no errors but more than 2% invalid answers does not, whether the
// invalid answers are a production participant's or a judge's.
func TestRun14CleanStrataRegisterDespiteJunk(t *testing.T) {
	in := run14()
	r := Evaluate(&in)
	if !r.Decision.Registrable {
		t.Fatalf("run 14 shape is not registrable: %v", r.Decision.Reasons)
	}
	for _, s := range []string{"initials.cyrl", "initials.latn"} {
		if d := decisionOf(t, &r, s); !d.Passed {
			t.Errorf("%s did not pass: %+v", s, d)
		}
	}
	for _, s := range []string{"single_field.cyrl", "mononym.cyrl"} {
		d := decisionOf(t, &r, s)
		if d.Passed {
			t.Errorf("%s passed on more than 2%% invalid answers: %+v", s, d)
		}
		if !strings.HasPrefix(d.Reason, "invalid:") {
			t.Errorf("%s: reason %q does not name the invalid answers", s, d.Reason)
		}
		// The error gate alone would have passed it: the invalid gate is what
		// keeps it out.
		if m := r.Strata[s].Combos[comboKey(both1)]; !m.ErrorsPassed || m.Errors != 0 {
			t.Errorf("%s: the error gate did not pass on its own: %+v", s, m)
		}
	}
	// The run-wide shares stay in the report as information.
	if !slices.Equal(r.Invalid, in.Invalid) || !slices.Equal(r.Repeat, in.Repeat) {
		t.Errorf("the run-wide counts are not reported: %+v %+v", r.Invalid, r.Repeat)
	}
	// The pooled repeat set is the clean strata's 103 items.
	if !slices.Equal(r.PooledRepeat.Strata, []string{"initials.cyrl", "initials.latn"}) {
		t.Errorf("pooled strata = %v", r.PooledRepeat.Strata)
	}
	for i, want := range [4][2]int{{101, 103}, {99, 103}, {103, 103}, {102, 103}} {
		c := r.PooledRepeat.Participants[i]
		if c.Participant != repeatParticipants[i] || c.Reproduced != want[0] || c.Total != want[1] || !c.Passed {
			t.Errorf("pooled repeat %d = %+v, want %v", i, c, want)
		}
	}
}

// The per-stratum invalid shares are in the report: per combo for the
// production pair, per stratum for the judges.
func TestStratumReportShowsTheInvalidShares(t *testing.T) {
	in := run14()
	r := Evaluate(&in)
	m := r.Strata["initials.cyrl"].Combos[comboKey(both1)]
	want := []InvalidShare{
		{Participant: slotProductionA, Invalid: 4, Total: 900, Share: 4.0 / 900, Passed: true},
		{Participant: slotProductionB, Invalid: 17, Total: 900, Share: 17.0 / 900, Passed: true},
	}
	if !slices.Equal(m.Invalid, want) || !m.InvalidPassed {
		t.Errorf("production invalid = %+v (%v), want %+v", m.Invalid, m.InvalidPassed, want)
	}
	j := r.Strata["mononym.cyrl"].JudgeInvalid
	wantJ := []InvalidShare{
		{Participant: slotJudgeA, Invalid: 20, Total: 900, Share: 20.0 / 900, Passed: false},
		{Participant: slotJudgeB, Invalid: 158, Total: 900, Share: 158.0 / 900, Passed: false},
	}
	if !slices.Equal(j, wantJ) {
		t.Errorf("judge invalid = %+v, want %+v", j, wantJ)
	}
}

// The invalid gate is exactly 2% per production participant and combo: 18 of
// 900 passes, 19 does not.
func TestProductionInvalidGateAtTwoPercent(t *testing.T) {
	for _, c := range []struct {
		invalid int
		pass    bool
	}{{18, true}, {19, false}} {
		in := run14()
		in.Items = (&shapedStratum{name: "initials.cyrl", n: 900, prodInvalid: [2]int{0, c.invalid}, repeats: 60,
			reproduced: [4]int{60, 60, 60, 60}}).items(1)
		r := Evaluate(&in)
		if got := decisionOf(t, &r, "initials.cyrl").Passed; got != c.pass {
			t.Errorf("%d of 900 invalid: passed = %v", c.invalid, got)
		}
	}
	// One combo over the limit leaves the others: the stratum is asked in a
	// combo whose own answers were valid enough.
	in := run14()
	in.Items = (&shapedStratum{name: "initials.cyrl", n: 900, repeats: 60, reproduced: [4]int{60, 60, 60, 60}}).items(1)
	for i := range 19 {
		ans := in.Items[i].Candidates[both1]
		ans[0] = Answer{Invalid: true}
		in.Items[i].Candidates[both1] = ans
	}
	r := Evaluate(&in)
	if m := r.Strata["initials.cyrl"].Combos[comboKey(both1)]; m.Passed || m.InvalidPassed {
		t.Errorf("A1 with 19 of 900 invalid passed: %+v", m)
	}
	if d := decisionOf(t, &r, "initials.cyrl"); !d.Passed || d.Arm != ArmA || d.ItemsPerRequest != 10 {
		t.Errorf("the stratum did not fall back to A10: %+v", d)
	}
}

// The judges' invalid gate is exactly 2% per judge on the stratum's items.
func TestJudgeInvalidGateAtTwoPercent(t *testing.T) {
	for _, c := range []struct {
		invalid int
		pass    bool
	}{{18, true}, {19, false}} {
		in := run14()
		in.Items = (&shapedStratum{name: "initials.cyrl", n: 900, judgeInvalid: [2]int{0, c.invalid}, repeats: 60,
			reproduced: [4]int{60, 60, 60, 60}}).items(1)
		r := Evaluate(&in)
		if got := decisionOf(t, &r, "initials.cyrl").Passed; got != c.pass {
			t.Errorf("%d of 900 judge answers invalid: passed = %v", c.invalid, got)
		}
	}
}

// The pooled repeat needs MinPooledRepeatItems repeats per participant on the
// eligible strata: below it nothing registers.
func TestPooledRepeatFloor(t *testing.T) {
	for _, c := range []struct {
		repeats int
		pass    bool
	}{{MinPooledRepeatItems, true}, {MinPooledRepeatItems - 1, false}} {
		in := run14()
		in.Items = (&shapedStratum{name: "initials.cyrl", n: 900, repeats: c.repeats,
			reproduced: [4]int{c.repeats, c.repeats, c.repeats, c.repeats}}).items(1)
		// A junk stratum's repeats are not evidence for the clean one.
		in.Items = append(in.Items, (&shapedStratum{name: "single_field.cyrl", n: 900, prodInvalid: [2]int{0, 100},
			repeats: 100, reproduced: [4]int{100, 100, 100, 100}}).items(10001)...)
		r := Evaluate(&in)
		if r.Decision.Registrable != c.pass || decisionOf(t, &r, "initials.cyrl").Passed != c.pass {
			t.Errorf("%d pooled repeats: registrable = %v, decision %+v", c.repeats, r.Decision.Registrable, r.Decision)
		}
		if !c.pass && !slices.ContainsFunc(r.Decision.Reasons, func(s string) bool {
			return strings.HasPrefix(s, "pooled repeat evidence:")
		}) {
			t.Errorf("reasons %v do not name the pooled evidence", r.Decision.Reasons)
		}
	}
}

// The pooled repeat keeps the 10% limit on changed answers.
func TestPooledRepeatShare(t *testing.T) {
	for _, c := range []struct {
		reproduced int
		pass       bool
	}{{90, true}, {89, false}} {
		in := run14()
		in.Items = (&shapedStratum{name: "initials.cyrl", n: 900, repeats: 100,
			reproduced: [4]int{100, 100, 100, c.reproduced}}).items(1)
		r := Evaluate(&in)
		if r.Decision.Registrable != c.pass || decisionOf(t, &r, "initials.cyrl").Passed != c.pass {
			t.Errorf("judge_b reproduced %d of 100: registrable = %v", c.reproduced, r.Decision.Registrable)
		}
		if !c.pass && !slices.ContainsFunc(r.Decision.Reasons, func(s string) bool {
			return strings.HasPrefix(s, "pooled repeat: judge_b reproduced 89 of 100")
		}) {
			t.Errorf("reasons %v do not name the pooled repeat", r.Decision.Reasons)
		}
	}
}
