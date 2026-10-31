package llmeval

import "fmt"

// Options change the gate for a question other than the name
// normalization's (E4c, plan §4.5): the merge question. The zero value is
// K's gate, and K's reports are byte-identical under it (a stored report
// pins that).
type Options struct {
	// MaxErrorShare replaces the package's MaxErrorShare when positive:
	// the bound and the weighted share at most this.
	MaxErrorShare float64 `json:"max_error_share,omitempty"`
	// ContestedIsError counts an accepted answer without a gold (the
	// judges disagree, refuse or did not answer) as an error in |A|.
	ContestedIsError bool `json:"contested_is_error,omitempty"`
	// ContestedAdjudicated lets a human clear such an error: a contested
	// accept adjudicated same or unclear counts as a correct one; pending
	// or different stays an error (the suite's rule). Contested items are
	// reported by script with their labels.
	ContestedAdjudicated bool `json:"contested_adjudicated,omitempty"`
	// AcceptOnly, when set, is the one canonical answer that accepts: the
	// pair agreeing on any other accepts nothing and is no error.
	AcceptOnly string `json:"accept_only,omitempty"`
	// AnchorRecall reports the anchor counts as recall: they neither gate
	// the run nor need the anchor set's full size.
	AnchorRecall bool `json:"anchor_recall,omitempty"`
	// ByScript breaks every combo down per item script.
	ByScript bool `json:"by_script,omitempty"`
	// GateRule names the error gate of every stratum, and the report says
	// it per stratum: empty (K) and GateRuleHoldout bound the error share
	// of a sample; GateRuleCensus is for a run that asked every candidate.
	GateRule string `json:"gate_rule,omitempty"`
	// auditRun is Input.AuditRun, for the audit order.
	auditRun int64
}

// The error gates of a stratum (Options.GateRule).
const (
	// GateRuleHoldout: the one-sided bound and the weighted share at most
	// the largest error share.
	GateRuleHoldout = "holdout"
	// GateRuleCensus: every candidate asked has a settled pair answer, and
	// no accept is an error (a contested one included, until a human
	// clears it). Nothing is estimated, so no bound applies.
	GateRuleCensus = "census"
)

// CensusMetrics is a census combo's completeness: Settled counts the
// candidates both production participants answered validly (a refusal
// included); Unsettled lists the others — not asked, failed, rejected by
// the provider or by the validators. Complete is none unsettled.
type CensusMetrics struct {
	Candidates int     `json:"candidates"`
	Settled    int     `json:"settled"`
	Unsettled  []int64 `json:"unsettled"`
	Complete   bool    `json:"complete"`
}

// measureCensus counts a combo's settled candidates.
func measureCensus(c Combo, scope []*Item) *CensusMetrics {
	m := &CensusMetrics{Unsettled: []int64{}}
	for _, it := range scope {
		m.Candidates++
		ans, ok := candidates(it, c)
		if ok && ans[0].Valid && ans[1].Valid {
			m.Settled++
		} else {
			m.Unsettled = append(m.Unsettled, it.ID)
		}
	}
	m.Complete = len(m.Unsettled) == 0
	return m
}

// censusReason names the first combo whose census gate failed, or "".
func censusReason(combos []Combo, report *StratumReport) string {
	for _, c := range combos {
		m := report.Combos[comboKey(c)]
		switch {
		case m.Census == nil || m.ErrorsPassed:
		case !m.Census.Complete:
			return fmt.Sprintf("census: %d of %d candidates without a settled pair answer in %s",
				len(m.Census.Unsettled), m.Census.Candidates, comboKey(c))
		case m.Audit != nil:
			return auditReason(c, m.Audit, "census")
		case m.AllowedErrors > 0:
			return fmt.Sprintf("census: %d errors of %d accepted in %s", m.AllowedErrors, m.AllowedAccepted, comboKey(c))
		default:
			return "census: nothing accepted in " + comboKey(c)
		}
	}
	return ""
}

// contestedError reports whether a contested accept counts as an error:
// always, unless a human cleared it (ContestedAdjudicated).
func (o *Options) contestedError(it *Item) bool {
	if !o.ContestedAdjudicated {
		return true
	}
	return it.Adjudication != AdjudicatedSame && it.Adjudication != AdjudicatedUnclear
}

// ContestedMetrics are a combo's contested accepts by their adjudication,
// and their item ids by script: the items a human is to label. Failed are
// those still errors (pending or different).
type ContestedMetrics struct {
	AdjudicatedSame      int                `json:"adjudicated_same"`
	AdjudicatedDifferent int                `json:"adjudicated_different"`
	Unclear              int                `json:"unclear"`
	Pending              int                `json:"pending"`
	Failed               int                `json:"failed"`
	IDs                  map[string][]int64 `json:"ids"`
}

// add counts one contested accept.
func (m *ContestedMetrics) add(it *Item) {
	if m.IDs == nil {
		m.IDs = map[string][]int64{}
	}
	m.IDs[it.Script] = append(m.IDs[it.Script], it.ID)
	switch it.Adjudication {
	case AdjudicatedSame:
		m.AdjudicatedSame++
	case AdjudicatedUnclear:
		m.Unclear++
	case AdjudicatedDifferent:
		m.AdjudicatedDifferent++
		m.Failed++
	default:
		m.Pending++
		m.Failed++
	}
}

// review is the combo's contested review, made on first use; nil while
// contested accepts are not adjudicated.
func (o *Options) review(m **ContestedMetrics) *ContestedMetrics {
	if !o.ContestedAdjudicated {
		return nil
	}
	if *m == nil {
		*m = &ContestedMetrics{}
	}
	return *m
}

// accepts reports an agreed answer that accepts: any, or AcceptOnly's.
func (o *Options) accepts(answer string) bool {
	return answer != "" && (o.AcceptOnly == "" || answer == o.AcceptOnly)
}

// splitSuite separates a stratum's suite items from the rest.
func splitSuite(all []*Item) (rest, suite []*Item) {
	for _, it := range all {
		if it.Adversarial {
			suite = append(suite, it)
		} else {
			rest = append(rest, it)
		}
	}
	return rest, suite
}

// suiteReason names the first combo the suite failed, or "".
func suiteReason(combos []Combo, report *StratumReport) string {
	for _, c := range combos {
		if a := report.Combos[comboKey(c)].Adversarial; a != nil && a.Failed > 0 {
			return fmt.Sprintf("adversarial: %d accepted in %s (%d different, %d pending; %d unclear, %d same cleared)",
				a.Accepted, comboKey(c), a.AdjudicatedDifferent, a.Pending, a.Unclear, a.AdjudicatedSame)
		}
	}
	return ""
}

// splitSupplement separates a stratum's items gated outside |A| from the
// rest.
func splitSupplement(all []*Item) (rest, supplement []*Item) {
	for _, it := range all {
		if it.Supplement {
			supplement = append(supplement, it)
		} else {
			rest = append(rest, it)
		}
	}
	return rest, supplement
}

// SupplementMetrics is a combo's result on the items gated outside |A|:
// Errors counts the accepts whose gold differs, and, where a contested
// accept is an error (Options), the contested ones; any fails the stratum.
type SupplementMetrics struct {
	Items        int `json:"items"`
	PairAccepted int `json:"pair_accepted"`
	Accepted     int `json:"accepted"`
	Contested    int `json:"contested"`
	Errors       int `json:"errors"`
	// ContestedReview is the contested accepts' labels (ContestedAdjudicated).
	ContestedReview *ContestedMetrics `json:"contested_review,omitempty"`
	// Audit is the audit of the applied extra questions
	// (GateRuleHoldoutAudit): it gates them instead of Errors.
	Audit *AuditMetrics `json:"audit,omitempty"`
}

// passed reports the extra questions holding their gate: no sampled item
// failed under the holdout audit — none applied fails nothing —, else no
// error.
func (m *SupplementMetrics) passed() bool {
	if m.Audit != nil {
		return m.Audit.Failed == 0
	}
	return m.Errors == 0
}

// measureSupplement counts the items gated outside |A| in a combo.
func measureSupplement(c Combo, items []*Item, o *Options) *SupplementMetrics {
	m := &SupplementMetrics{}
	for _, it := range items {
		m.Items++
		ans, ok := candidates(it, c)
		answer := agreed(&ans, ok)
		if !o.accepts(answer) {
			continue
		}
		m.PairAccepted++
		g := gold(it)
		if g == "" {
			m.Contested++
			if r := o.review(&m.ContestedReview); r != nil {
				r.add(it)
			}
			if !o.ContestedIsError {
				continue
			}
		}
		m.Accepted++
		if g != answer && (g != "" || o.contestedError(it)) {
			m.Errors++
		}
	}
	return m
}

// supplementReason names the first combo whose items outside |A| failed,
// or "".
func supplementReason(combos []Combo, report *StratumReport) string {
	for _, c := range combos {
		s := report.Combos[comboKey(c)].Supplement
		if s != nil && s.Audit != nil && !s.passed() {
			return sampleReason("supplement audit", c, s.Audit)
		}
		if s != nil && s.Audit == nil && s.Errors > 0 {
			return fmt.Sprintf("supplement: %d errors of %d accepted outside |A| in %s (%d contested)",
				s.Errors, s.Accepted, comboKey(c), s.Contested)
		}
	}
	return ""
}

func (o *Options) maxErrorShare() float64 {
	if o.MaxErrorShare > 0 {
		return o.MaxErrorShare
	}
	return MaxErrorShare
}

// ScriptMetrics is a combo's result on the items of one script.
type ScriptMetrics struct {
	Items int `json:"items"`
	// PairAccepted counts the items the pair agreed on an accepting
	// answer; Accepted those in |A|, Errors and Contested among them.
	PairAccepted int `json:"pair_accepted"`
	Accepted     int `json:"accepted"`
	Errors       int `json:"errors"`
	Contested    int `json:"contested"`
	// AcceptedShare is Accepted of Items; UpperBound the one-sided 95%
	// bound on the error share of this script's accepts alone.
	AcceptedShare float64 `json:"accepted_share"`
	UpperBound    float64 `json:"upper_bound"`
	// Invalid counts the production pair's invalid answers of all asked.
	Invalid     int                 `json:"invalid"`
	Answers     int                 `json:"answers"`
	Adversarial *AdversarialMetrics `json:"adversarial,omitempty"`
}

// AdversarialMetrics is the adversarial suite in a combo: every item's
// expected answer is not the accepting one, so an item the production rule
// accepts (the pair and both judges agree on the accepting answer) is a
// failure of its stratum when a human adjudicated it two people or nobody
// has adjudicated it yet; one person and unclear clear it: the suite's
// label (a different patronymic) is noisy on a real catalog.
type AdversarialMetrics struct {
	Items int `json:"items"`
	// PairAccepted counts the items the pair agreed on the accepting
	// answer; Accepted those both judges confirmed.
	PairAccepted int     `json:"pair_accepted"`
	Accepted     int     `json:"accepted"`
	AcceptedIDs  []int64 `json:"accepted_ids,omitempty"`
	// The accepted items by their adjudication; Failed are those
	// adjudicated different or still pending: any fails the stratum.
	AdjudicatedSame      int `json:"adjudicated_same"`
	AdjudicatedDifferent int `json:"adjudicated_different"`
	Unclear              int `json:"unclear"`
	Pending              int `json:"pending"`
	Failed               int `json:"failed"`
}

// The labels of an adjudication.
const (
	AdjudicatedSame      = "same"
	AdjudicatedDifferent = "different"
	AdjudicatedUnclear   = "unclear"
)

// suiteAccepts reports a suite item the production rule accepts: the pair
// agreed on an accepting answer, and the judges' consensus is that answer.
func suiteAccepts(it *Item, c Combo, o *Options) (pair, accepted bool) {
	ans, ok := candidates(it, c)
	answer := agreed(&ans, ok)
	if !o.accepts(answer) {
		return false, false
	}
	return true, gold(it) == answer
}

// measureSuite counts the suite's items in a combo.
func measureSuite(c Combo, suite []*Item, o *Options) *AdversarialMetrics {
	m := &AdversarialMetrics{}
	for _, it := range suite {
		m.add(it, c, o)
	}
	return m
}

// measureScripts breaks a combo down per script: the measured items as the
// gate counts them, and the suite's.
func measureScripts(c Combo, scope, suite []*Item, o *Options) map[string]ScriptMetrics {
	out := map[string]ScriptMetrics{}
	for _, it := range scope {
		s := out[it.Script]
		s.add(it, c, o)
		out[it.Script] = s
	}
	for _, it := range suite {
		s := out[it.Script]
		if s.Adversarial == nil {
			s.Adversarial = &AdversarialMetrics{}
		}
		s.Adversarial.add(it, c, o)
		out[it.Script] = s
	}
	for name, s := range out {
		if s.Items > 0 {
			s.AcceptedShare = float64(s.Accepted) / float64(s.Items)
		}
		s.UpperBound = ClopperPearsonUpper(s.Errors, s.Accepted)
		out[name] = s
	}
	return out
}

// add counts one measured item into its script's metrics.
func (s *ScriptMetrics) add(it *Item, c Combo, o *Options) {
	s.Items++
	ans, ok := candidates(it, c)
	for p := range ans {
		if ok && (ans[p].Valid || ans[p].Invalid) {
			s.Answers++
			if ans[p].Invalid {
				s.Invalid++
			}
		}
	}
	answer := agreed(&ans, ok)
	if !o.accepts(answer) {
		return
	}
	s.PairAccepted++
	g := gold(it)
	if g == "" {
		s.Contested++
		if !o.ContestedIsError {
			return
		}
	}
	s.Accepted++
	if g != answer && (g != "" || o.contestedError(it)) {
		s.Errors++
	}
}

// add counts one suite item.
func (m *AdversarialMetrics) add(it *Item, c Combo, o *Options) {
	m.Items++
	pair, accepted := suiteAccepts(it, c, o)
	if pair {
		m.PairAccepted++
	}
	if !accepted {
		return
	}
	m.Accepted++
	m.AcceptedIDs = append(m.AcceptedIDs, it.ID)
	switch it.Adjudication {
	case AdjudicatedSame:
		m.AdjudicatedSame++
	case AdjudicatedUnclear:
		// Nobody read the books either: no evidence of two people, and a
		// merge is reversible with a veto.
		m.Unclear++
	case AdjudicatedDifferent:
		m.AdjudicatedDifferent++
		m.Failed++
	default:
		m.Pending++
		m.Failed++
	}
}
