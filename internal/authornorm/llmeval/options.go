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
	// AcceptOnly, when set, is the one canonical answer that accepts: the
	// pair agreeing on any other accepts nothing and is no error.
	AcceptOnly string `json:"accept_only,omitempty"`
	// AnchorRecall reports the anchor counts as recall: they neither gate
	// the run nor need the anchor set's full size.
	AnchorRecall bool `json:"anchor_recall,omitempty"`
	// ByScript breaks every combo down per item script.
	ByScript bool `json:"by_script,omitempty"`
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
	if g != answer {
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
