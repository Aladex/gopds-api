package llmeval

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// The arms of the excerpt ablation.
const (
	// ArmA answers without the book excerpt.
	ArmA = "A"
	// ArmB answers with it, where the excerpt is relevant.
	ArmB = "B"
)

// The run-wide evidence a registrable report needs (design K, section 8):
// every participant answered the whole anchor set and the whole repeat
// sample. A smaller set — a catalog without enough anchor candidates, repeat
// items dropped, a test fixture — is reported, never registered.
const (
	MinAnchorItems = 300
	MinRepeatItems = 200
)

// Thresholds of the run as a whole (design K, section 8).
const (
	judgeAnchorPercent      = 99
	productionAnchorPercent = 98
	// maxRepeatChangePercent catches a broken or swapped participant, not
	// honest ambiguity: resolve applies only what the pair and both judges
	// agree on, so an unstable answer costs coverage, not correctness.
	maxRepeatChangePercent = 10
	maxInvalidPercent      = 2
	// ablationMinGain and ablationMinGainPercent: the excerpt must accept at
	// least max(30, 5% of the stratum's items) more than arm A.
	ablationMinGain        = 30
	ablationMinGainPercent = 5
	percent                = 100
	// restructureTier is the tier dropped from a stratum on its first error.
	restructureTier = "restructure"
	judgePrefix     = "judge"
)

// Answer is one participant's final answer to one item: Valid when it
// answered at all and passed the validators; a refusal (cannot_tell) is valid
// and accepts nothing.
type Answer struct {
	Valid     bool   `json:"valid"`
	Refused   bool   `json:"refused"`
	Canonical string `json:"canonical"`
	Tier      string `json:"tier"`
	// Invalid is an answer asked for that ended without a valid one: the
	// validators rejected it, the provider rejected the item, or the job
	// failed. An answer never asked for is neither valid nor invalid.
	Invalid bool `json:"invalid,omitempty"`
}

// Combo is one way the production pair was asked: an arm of the ablation and
// the number of items per request.
type Combo struct {
	Arm   string `json:"arm"`
	Batch int    `json:"batch"`
}

// comboKey names a combo in the report: "A1", "B10".
func comboKey(c Combo) string { return c.Arm + strconv.Itoa(c.Batch) }

// Item is one frozen item with every answer the run collected for it.
type Item struct {
	ID      int64
	Stratum string
	// Weight is the number of credits the fingerprint carries.
	Weight int
	// Holdout is false for a dev item.
	Holdout bool
	// HasContext is true when the item has a relevant excerpt; without one
	// arm B is arm A.
	HasContext bool
	Judges     [2]Answer
	// Candidates are the production pair's answers per combo.
	Candidates map[Combo][2]Answer
	// Repeated holds, for every participant whose answer to the item was
	// asked again, whether the repeat reproduced its first answer.
	Repeated map[string]bool
}

// InvalidShare is a participant's invalid answers of all answered or
// invalid ones in a scope, and whether the share is within the limit.
type InvalidShare struct {
	Participant string  `json:"participant"`
	Invalid     int     `json:"invalid"`
	Total       int     `json:"total"`
	Share       float64 `json:"share"`
	Passed      bool    `json:"passed"`
}

// ParticipantCount is a per-participant count: Matched of Total.
type ParticipantCount struct {
	Participant string `json:"participant"`
	Matched     int    `json:"matched"`
	Total       int    `json:"total"`
}

// FingerprintResult is the fingerprint stage's verdict.
type FingerprintResult struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// Input is everything an evaluation reads.
type Input struct {
	Items       []Item
	Combos      []Combo
	Fingerprint FingerprintResult
	// Anchor counts the anchor answers matching the expected one.
	Anchor []ParticipantCount
	// Repeat counts the repeated answers equal to the first.
	Repeat []ParticipantCount
	// Invalid counts invalid answers (Matched) of all answers (Total).
	Invalid []ParticipantCount
	// ProviderRejected counts the answers the provider rejected (Matched) of
	// all answers (Total); Invalid includes them.
	ProviderRejected []ParticipantCount
	// Registrable is false for a dev run: its report never registers.
	Registrable bool
}

// TierCount is accepted items and errors of one tier.
type TierCount struct {
	Accepted int `json:"accepted"`
	Errors   int `json:"errors"`
}

// ComboMetrics is the production pair's result in one stratum and combo.
type ComboMetrics struct {
	// BothValid counts items both production participants answered validly;
	// Agreed those where they agreed (refusals included); Accepted those
	// where they agreed on an answer.
	BothValid int `json:"both_valid"`
	Agreed    int `json:"agreed"`
	Accepted  int `json:"accepted"`
	Errors    int `json:"errors"`
	// Contested counts the items the pair agreed on without a gold (the
	// judges disagreed, refused or did not answer); ContestedShare is their
	// share of the items the pair agreed on.
	Contested      int                  `json:"contested"`
	ContestedShare float64              `json:"contested_share"`
	AcceptedWeight int                  `json:"accepted_weight"`
	ErrorWeight    int                  `json:"error_weight"`
	UpperBound     float64              `json:"upper_bound"`
	Tiers          map[string]TierCount `json:"tiers"`
	// Passed is the gate on the allowed tiers (AllowedTiers); Allowed* are
	// the accepted items and errors in those tiers — what the stratum would
	// actually accept.
	Passed          bool     `json:"passed"`
	ErrorsPassed    bool     `json:"errors_passed"`
	AllowedTiers    []string `json:"allowed_tiers"`
	AllowedAccepted int      `json:"allowed_accepted"`
	AllowedErrors   int      `json:"allowed_errors"`
	// Invalid is each production participant's invalid share in the combo;
	// InvalidPassed whether both are within the limit.
	Invalid       []InvalidShare `json:"invalid"`
	InvalidPassed bool           `json:"invalid_passed"`
}

// AblationResult compares arm B with arm A at one batch size.
type AblationResult struct {
	Gain int `json:"gain"`
	// Delta lists the items whose agreed answer the excerpt changed.
	Delta       []int64 `json:"delta"`
	DeltaErrors int     `json:"delta_errors"`
	ChoseB      bool    `json:"chose_b"`
}

// StratumReport is a stratum's measurements.
type StratumReport struct {
	// Holdout is the number of items measured (the holdout items, or the
	// dev items of a dev run); Dev the dev items left out of the gate.
	Holdout  int                     `json:"holdout"`
	Dev      int                     `json:"dev"`
	Gold     int                     `json:"gold"`
	Combos   map[string]ComboMetrics `json:"combos"`
	Ablation map[int]AblationResult  `json:"ablation"`
	// JudgeInvalid is each judge's invalid share on the measured items.
	JudgeInvalid []InvalidShare `json:"judge_invalid"`
}

// StratumDecision is what the run decides for a stratum.
type StratumDecision struct {
	Stratum         string   `json:"stratum"`
	Passed          bool     `json:"passed"`
	Arm             string   `json:"arm,omitempty"`
	ItemsPerRequest int      `json:"items_per_request,omitempty"`
	Tiers           []string `json:"tiers,omitempty"`
	// Reason says why a stratum that passed no combo, or failed its judges'
	// invalid gate, decides nothing.
	Reason string `json:"reason,omitempty"`
}

// Decision is the run's decision.
type Decision struct {
	// Registrable is true when the report may back a registration: not a
	// dev run, and the run-wide conditions hold.
	Registrable bool              `json:"registrable"`
	Reasons     []string          `json:"reasons,omitempty"`
	Strata      []StratumDecision `json:"strata"`
}

// Report is the evaluation.
type Report struct {
	Strata map[string]StratumReport `json:"strata"`
	Anchor []ParticipantCount       `json:"anchor"`
	Repeat []ParticipantCount       `json:"repeat"`
	// RepeatChange is each participant's share of repeated answers that
	// changed, shown so it can be watched.
	RepeatChange []RepeatChange     `json:"repeat_change"`
	Invalid      []ParticipantCount `json:"invalid"`
	// ProviderRejected is the part of Invalid the provider rejected.
	ProviderRejected []ParticipantCount `json:"provider_rejected"`
	Fingerprint      FingerprintResult  `json:"fingerprint"`
	// PooledRepeat is the reproduction over the repeat items of the strata
	// that passed their own gates.
	PooledRepeat PooledRepeat `json:"pooled_repeat"`
	Decision     Decision     `json:"decision"`
}

// MinPooledRepeatItems is the repeat items each participant needs on the
// passing strata.
const MinPooledRepeatItems = 50

// PooledRepeat is the reproduction gate over the passing strata.
type PooledRepeat struct {
	Strata       []string            `json:"strata"`
	Participants []PooledRepeatCount `json:"participants"`
}

// PooledRepeatCount is one participant's pooled repeats: reproduced of
// total, per stratum and in all.
type PooledRepeatCount struct {
	Participant string          `json:"participant"`
	Reproduced  int             `json:"reproduced"`
	Total       int             `json:"total"`
	ByStratum   []StratumRepeat `json:"by_stratum"`
	Passed      bool            `json:"passed"`
}

// StratumRepeat is a participant's repeats in one stratum.
type StratumRepeat struct {
	Stratum    string `json:"stratum"`
	Reproduced int    `json:"reproduced"`
	Total      int    `json:"total"`
}

// RepeatChange is how many of a participant's repeated answers differ from
// its first ones, and their share.
type RepeatChange struct {
	Participant string  `json:"participant"`
	Changed     int     `json:"changed"`
	Total       int     `json:"total"`
	Share       float64 `json:"share"`
}

// RepeatChanges is the changed share of every participant's repeats.
func RepeatChanges(counts []ParticipantCount) []RepeatChange {
	out := make([]RepeatChange, 0, len(counts))
	for _, c := range counts {
		rc := RepeatChange{Participant: c.Participant, Changed: c.Total - c.Matched, Total: c.Total}
		if c.Total > 0 {
			rc.Share = float64(rc.Changed) / float64(c.Total)
		}
		out = append(out, rc)
	}
	return out
}

// Evaluate computes the report and the decision. It is deterministic: strata
// come out sorted, items in input order.
func Evaluate(in *Input) Report {
	r := Report{Strata: map[string]StratumReport{}, Anchor: in.Anchor, Repeat: in.Repeat,
		RepeatChange: RepeatChanges(in.Repeat), Invalid: in.Invalid, ProviderRejected: in.ProviderRejected,
		Fingerprint: in.Fingerprint}
	reasons := runReasons(in)
	byStratum := map[string][]*Item{}
	var names []string
	for i := range in.Items {
		it := &in.Items[i]
		if _, ok := byStratum[it.Stratum]; !ok {
			names = append(names, it.Stratum)
		}
		byStratum[it.Stratum] = append(byStratum[it.Stratum], it)
	}
	slices.Sort(names)
	for _, name := range names {
		report, decision := evaluateStratum(in.Combos, byStratum[name])
		decision.Stratum = name
		r.Strata[name] = report
		r.Decision.Strata = append(r.Decision.Strata, decision)
	}
	// The reproduction is judged on the strata that passed on their own
	// items: the junk strata's repeats neither help nor sink them.
	pooled, pooledReasons := pooledRepeat(r.Decision.Strata, byStratum)
	r.PooledRepeat = pooled
	reasons = append(reasons, pooledReasons...)
	if len(reasons) > 0 {
		for i := range r.Decision.Strata {
			r.Decision.Strata[i] = StratumDecision{Stratum: r.Decision.Strata[i].Stratum}
		}
	}
	r.Decision.Reasons = reasons
	r.Decision.Registrable = in.Registrable && len(reasons) == 0
	return r
}

// runReasons lists why the run as a whole decides nothing. The run-wide
// invalid and repeat shares are not among them: a stratum's invalid answers
// are gated on its own items, the reproduction on the passing strata's
// repeats (pooledRepeat).
func runReasons(in *Input) []string {
	var reasons []string
	if !in.Fingerprint.OK {
		reasons = append(reasons, "fingerprint: "+in.Fingerprint.Reason)
	}
	reasons = append(reasons, AnchorReasons(in.Anchor)...)
	reasons = append(reasons, evidenceReasons("anchor", in.Anchor, MinAnchorItems)...)
	reasons = append(reasons, evidenceReasons("repeat", in.Repeat, MinRepeatItems)...)
	return reasons
}

// pooledRepeat is every participant's reproduction over the repeat items of
// the strata that passed: at least MinPooledRepeatItems of them, at most 10%
// changed. With no stratum passed there is nothing to register and no
// reason.
func pooledRepeat(decisions []StratumDecision, byStratum map[string][]*Item) (pool PooledRepeat, reasons []string) {
	pool = PooledRepeat{Strata: []string{}, Participants: make([]PooledRepeatCount, 0, len(participants))}
	for _, d := range decisions {
		if d.Passed {
			pool.Strata = append(pool.Strata, d.Stratum)
		}
	}
	for _, p := range participants {
		c := PooledRepeatCount{Participant: p, ByStratum: make([]StratumRepeat, 0, len(pool.Strata))}
		for _, name := range pool.Strata {
			sr := StratumRepeat{Stratum: name}
			for _, it := range byStratum[name] {
				if reproduced, ok := it.Repeated[p]; ok {
					sr.Total++
					if reproduced {
						sr.Reproduced++
					}
				}
			}
			c.Reproduced += sr.Reproduced
			c.Total += sr.Total
			c.ByStratum = append(c.ByStratum, sr)
		}
		switch {
		case c.Total < MinPooledRepeatItems:
			reasons = append(reasons, fmt.Sprintf("pooled repeat evidence: %s answered %d of the %d items required",
				p, c.Total, MinPooledRepeatItems))
		case (c.Total-c.Reproduced)*percent > c.Total*maxRepeatChangePercent:
			reasons = append(reasons, fmt.Sprintf("pooled repeat: %s reproduced %d of %d", p, c.Reproduced, c.Total))
		default:
			c.Passed = true
		}
		pool.Participants = append(pool.Participants, c)
	}
	if len(pool.Strata) == 0 {
		return pool, nil
	}
	return pool, reasons
}

// countInvalid counts one answer into a participant's invalid share; an
// answer never asked for is not counted.
func countInvalid(s *InvalidShare, a Answer) {
	switch {
	case a.Invalid:
		s.Invalid++
		s.Total++
	case a.Valid:
		s.Total++
	}
}

// closeShares computes the shares and the 2% limit; it reports whether
// every participant is within it.
func closeShares(shares []InvalidShare) bool {
	all := true
	for i := range shares {
		s := &shares[i]
		if s.Total > 0 {
			s.Share = float64(s.Invalid) / float64(s.Total)
		}
		s.Passed = s.Invalid*percent <= s.Total*maxInvalidPercent
		all = all && s.Passed
	}
	return all
}

// invalidReason names the first participant over the invalid limit.
func invalidReason(shares []InvalidShare, where string) string {
	for _, s := range shares {
		if !s.Passed {
			return fmt.Sprintf("invalid: %s %d of %d%s", s.Participant, s.Invalid, s.Total, where)
		}
	}
	return ""
}

// judgeInvalid is each judge's invalid share on the measured items.
func judgeInvalid(scope []*Item) ([]InvalidShare, bool) {
	shares := []InvalidShare{{Participant: slotJudgeA}, {Participant: slotJudgeB}}
	for _, it := range scope {
		for j := range it.Judges {
			countInvalid(&shares[j], it.Judges[j])
		}
	}
	return shares, closeShares(shares)
}

// comboInvalid is each production participant's invalid share in a combo.
func comboInvalid(c Combo, scope []*Item) ([]InvalidShare, bool) {
	shares := []InvalidShare{{Participant: slotProductionA}, {Participant: slotProductionB}}
	for _, it := range scope {
		ans, ok := candidates(it, c)
		if !ok {
			continue
		}
		for p := range ans {
			countInvalid(&shares[p], ans[p])
		}
	}
	return shares, closeShares(shares)
}

// evidenceReasons lists the participants without the full evidence of a
// check: no count at all, or fewer items than the method requires.
func evidenceReasons(check string, counts []ParticipantCount, need int) []string {
	var reasons []string
	for _, p := range participants {
		i := slices.IndexFunc(counts, func(c ParticipantCount) bool { return c.Participant == p })
		total := 0
		if i >= 0 {
			total = counts[i].Total
		}
		if total < need {
			reasons = append(reasons, fmt.Sprintf("%s evidence: %s answered %d of the %d items required", check, p, total, need))
		}
	}
	return reasons
}

// AnchorReasons lists the participants that missed the anchor set: judges
// must match the known answer in at least 99% of the anchor items, the
// production pair in at least 98%. A failure means a model that is not what
// it claims, or a prompt that does not work; the run decides nothing.
func AnchorReasons(counts []ParticipantCount) []string {
	var reasons []string
	for _, c := range counts {
		need := productionAnchorPercent
		if strings.HasPrefix(c.Participant, judgePrefix) {
			need = judgeAnchorPercent
		}
		if c.Total == 0 || c.Matched*percent < c.Total*need {
			reasons = append(reasons, fmt.Sprintf("anchor: %s matched %d of %d", c.Participant, c.Matched, c.Total))
		}
	}
	if len(counts) == 0 {
		reasons = append(reasons, "anchor: no answers")
	}
	return reasons
}

// gold is the judges' consensus: their common canonical answer, or "" when
// they disagree, refuse or did not answer (contested).
func gold(it *Item) string {
	a, b := it.Judges[0], it.Judges[1]
	if !a.Valid || !b.Valid || a.Refused || b.Refused || a.Canonical != b.Canonical {
		return ""
	}
	return a.Canonical
}

// candidates is the pair's answers for a combo; without an excerpt arm B is
// arm A.
func candidates(it *Item, c Combo) ([2]Answer, bool) {
	if ans, ok := it.Candidates[c]; ok {
		return ans, true
	}
	if c.Arm == ArmB && !it.HasContext {
		ans, ok := it.Candidates[Combo{Arm: ArmA, Batch: c.Batch}]
		return ans, ok
	}
	return [2]Answer{}, false
}

// agreed is the pair's accepted answer, or "" when it accepts nothing.
func agreed(ans *[2]Answer, ok bool) string {
	a, b := ans[0], ans[1]
	if !ok || !a.Valid || !b.Valid || a.Refused || b.Refused || a.Canonical != b.Canonical {
		return ""
	}
	return a.Canonical
}

// evaluateStratum measures every combo and decides the arm and batch size.
// A combo is eligible only when both production participants answered it
// validly enough on the stratum's items, the stratum only when both judges
// did.
func evaluateStratum(combos []Combo, all []*Item) (StratumReport, StratumDecision) {
	scope, report := stratumScope(all)
	judgesPassed := false
	report.JudgeInvalid, judgesPassed = judgeInvalid(scope)
	for _, c := range combos {
		report.Combos[comboKey(c)] = measure(c, scope)
	}
	options := map[int]stratumOption{}
	batches := batchSizes(combos)
	for _, b := range batches {
		if o, ok := chooseArm(scope, b, &report); ok {
			options[b] = o
		}
	}
	if !judgesPassed {
		return report, StratumDecision{Reason: invalidReason(report.JudgeInvalid, "")}
	}
	decision := chooseBatch(batches, options)
	if !decision.Passed {
		decision.Reason = "gate: no combo passed"
		for _, c := range combos {
			if m := report.Combos[comboKey(c)]; m.ErrorsPassed && !m.InvalidPassed {
				decision.Reason = invalidReason(m.Invalid, " in "+comboKey(c))
				break
			}
		}
	}
	return report, decision
}

// stratumScope splits a stratum into the measured items — the holdout, or
// the dev items of a dev run — and starts its report.
func stratumScope(all []*Item) ([]*Item, StratumReport) {
	var holdout, dev []*Item
	for _, it := range all {
		if it.Holdout {
			holdout = append(holdout, it)
		} else {
			dev = append(dev, it)
		}
	}
	scope := holdout
	report := StratumReport{Holdout: len(holdout), Dev: len(dev), Combos: map[string]ComboMetrics{},
		Ablation: map[int]AblationResult{}}
	if len(holdout) == 0 {
		// A dev run measures its dev items; its report never registers.
		scope, report.Holdout, report.Dev = dev, len(dev), 0
	}
	for _, it := range scope {
		if gold(it) != "" {
			report.Gold++
		}
	}
	return scope, report
}

func batchSizes(combos []Combo) []int {
	var batches []int
	for _, c := range combos {
		if !slices.Contains(batches, c.Batch) {
			batches = append(batches, c.Batch)
		}
	}
	slices.Sort(batches)
	return batches
}

// stratumOption is a passing way to ask at one batch size.
type stratumOption struct {
	arm    string
	errors int
	tiers  []string
}

// chooseArm applies the ablation at one batch size: the excerpt when it
// earns its place, else arm A when it passes.
func chooseArm(scope []*Item, batch int, report *StratumReport) (stratumOption, bool) {
	a, okA := report.Combos[comboKey(Combo{Arm: ArmA, Batch: batch})]
	bm, okB := report.Combos[comboKey(Combo{Arm: ArmB, Batch: batch})]
	if okA && okB {
		ab := ablate(scope, batch, &a, &bm)
		report.Ablation[batch] = ab
		if ab.ChoseB {
			return stratumOption{ArmB, bm.AllowedErrors, bm.AllowedTiers}, true
		}
	}
	if okA && a.Passed {
		return stratumOption{ArmA, a.AllowedErrors, a.AllowedTiers}, true
	}
	return stratumOption{}, false
}

// chooseBatch takes the largest passing batch size that makes no more
// errors than the smaller ones: batching is cheaper, never assumed safe.
func chooseBatch(batches []int, options map[int]stratumOption) StratumDecision {
	chosen := 0
	for _, b := range batches {
		o, ok := options[b]
		if !ok {
			continue
		}
		if chosen == 0 || o.errors <= options[chosen].errors {
			chosen = b
		}
	}
	if chosen == 0 {
		return StratumDecision{}
	}
	o := options[chosen]
	return StratumDecision{Passed: true, Arm: o.arm, ItemsPerRequest: chosen, Tiers: o.tiers}
}

// acceptedItem is one item the pair accepted.
type acceptedItem struct {
	tier   string
	weight int
	err    bool
}

// measure computes a combo's metrics and gate over the scope.
func measure(c Combo, scope []*Item) ComboMetrics {
	m := ComboMetrics{Tiers: map[string]TierCount{}}
	var acc []acceptedItem
	for _, it := range scope {
		ans, ok := candidates(it, c)
		if ok && ans[0].Valid && ans[1].Valid {
			m.BothValid++
			if ans[0].Canonical == ans[1].Canonical && ans[0].Refused == ans[1].Refused {
				m.Agreed++
			}
		}
		answer := agreed(&ans, ok)
		if answer == "" {
			continue
		}
		g := gold(it)
		if g == "" {
			// No gold — the judges disagree, refuse or did not answer: resolve
			// applies only what both judges confirm, so the item is neither
			// accepted nor an error; it is reported as contested.
			m.Contested++
			continue
		}
		isErr := g != answer
		acc = append(acc, acceptedItem{tier: ans[0].Tier, weight: max(it.Weight, 1), err: isErr})
		tc := m.Tiers[ans[0].Tier]
		tc.Accepted++
		if isErr {
			tc.Errors++
		}
		m.Tiers[ans[0].Tier] = tc
	}
	if agreedTotal := len(acc) + m.Contested; agreedTotal > 0 {
		m.ContestedShare = float64(m.Contested) / float64(agreedTotal)
	}
	m.AllowedTiers = allowedTiers(m.Tiers)
	for _, a := range acc {
		m.Accepted++
		m.AcceptedWeight += a.weight
		if a.err {
			m.Errors++
			m.ErrorWeight += a.weight
		}
	}
	g := gate(acc, m.AllowedTiers)
	m.UpperBound, m.ErrorsPassed, m.AllowedAccepted, m.AllowedErrors = g.upper, g.passed, g.accepted, g.errors
	m.Invalid, m.InvalidPassed = comboInvalid(c, scope)
	m.Passed = m.ErrorsPassed && m.InvalidPassed
	return m
}

// allowedTiers is every tier seen, but restructure goes on its first error:
// the rest of the stratum is judged without it.
func allowedTiers(counts map[string]TierCount) []string {
	tiers := make([]string, 0, len(counts))
	for t := range counts {
		if t == restructureTier && counts[t].Errors > 0 {
			continue
		}
		tiers = append(tiers, t)
	}
	slices.Sort(tiers)
	return tiers
}

// gateResult is the gate over the allowed tiers.
type gateResult struct {
	upper            float64
	passed           bool
	accepted, errors int
}

// gate applies the bound and the weighted share to the allowed tiers.
func gate(acc []acceptedItem, tiers []string) gateResult {
	n, k, w, we := 0, 0, 0, 0
	for _, a := range acc {
		if !slices.Contains(tiers, a.tier) {
			continue
		}
		n++
		w += a.weight
		if a.err {
			k++
			we += a.weight
		}
	}
	return gateResult{upper: ClopperPearsonUpper(k, n), passed: PassesBound(k, n) && float64(we) <= MaxErrorShare*float64(w),
		accepted: n, errors: k}
}

// ablate compares the two arms at one batch size. The excerpt is chosen only
// when arm B passes, accepts at least max(30, 5%) more, makes no more errors,
// and makes no error on Δ — the items whose agreed outcome it changed: an
// agreed answer replaced by another, and an item the pair agrees on only with
// the excerpt. Both are where an excerpt can pull in another person's name.
func ablate(scope []*Item, batch int, a, b *ComboMetrics) AblationResult {
	// The comparison is on what each arm would actually accept: its allowed
	// tiers. Δ is over every item the excerpt changed, whatever its tier.
	r := AblationResult{Gain: b.AllowedAccepted - a.AllowedAccepted}
	for _, it := range scope {
		ansB, okB := candidates(it, Combo{Arm: ArmB, Batch: batch})
		withB := agreed(&ansB, okB)
		if withB == "" {
			continue
		}
		ansA, okA := candidates(it, Combo{Arm: ArmA, Batch: batch})
		if agreed(&ansA, okA) == withB {
			continue
		}
		r.Delta = append(r.Delta, it.ID)
		if g := gold(it); g == "" || g != withB {
			r.DeltaErrors++
		}
	}
	need := max(ablationMinGain, int(math.Ceil(float64(len(scope))*ablationMinGainPercent/percent)))
	r.ChoseB = b.Passed && r.Gain >= need && b.AllowedErrors <= a.AllowedErrors && r.DeltaErrors == 0
	return r
}
