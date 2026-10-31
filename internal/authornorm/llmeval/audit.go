package llmeval

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"slices"
	"strconv"
)

// The census audit (E4c, Andrey's decision of 2026-10-30): a census
// stratum is gated on a human audit of what reaches the catalog — the
// applied set, the items the pair and both judges answered the accepting
// answer — instead of on the pair's answers against the judges'. A gold
// error (the pair accepted, the judges agreed on another answer) and a
// contested accept (the judges did not agree) are never applied: both are
// reported, neither gates.

// GateRuleCensusAudit: every candidate asked has a settled pair answer,
// at least one item is applied, and every item of the audit sample is
// labeled same or unclear.
const GateRuleCensusAudit = "census_audit"

// GateRuleHoldoutAudit is the census audit without the census (E4c option
// (c), merge3's bucket): a random holdout stands for the kind, at least one
// of its items is applied, every item of its audit sample is labeled same
// or unclear, and so is every item of the audit sample of the extra Latin
// questions outside |A|. The bound is reported and gates nothing.
const GateRuleHoldoutAudit = "holdout_audit"

// AuditSampleSize is the most applied items a stratum's audit samples
// (migration 70's author_llm_eval_item_audited takes the same number).
const AuditSampleSize = 300

// AuditSupplementSize is the most applied extra Latin questions a holdout's
// audit samples besides (migration 72 takes the same number).
const AuditSupplementSize = 100

// AuditKey is an item's place in a run's audit order: sha256 of the UTF-8
// text "<run id>:<item id>" in decimal; ascending bytes are ascending hex.
// The sample is the applied items in this order, first AuditSampleSize.
func AuditKey(run, item int64) [32]byte {
	return sha256.Sum256([]byte(strconv.FormatInt(run, 10) + ":" + strconv.FormatInt(item, 10)))
}

// AuditMetrics is a census combo's audit: Applied counts the items the
// pair and both judges answered the accepting answer; Sample lists the
// sampled ones in audit order with their labels' counts — Failed are those
// labeled different or still pending: any fails the stratum. GoldErrors
// are the accepts the judges agreed against, reported only.
type AuditMetrics struct {
	Applied              int     `json:"applied"`
	Sample               []int64 `json:"sample"`
	AdjudicatedSame      int     `json:"adjudicated_same"`
	AdjudicatedDifferent int     `json:"adjudicated_different"`
	Unclear              int     `json:"unclear"`
	Pending              int     `json:"pending"`
	Failed               int     `json:"failed"`
	GoldErrors           int     `json:"gold_errors"`
	GoldErrorIDs         []int64 `json:"gold_error_ids"`
}

// passed is the audit's part of the gate: something applied, no sampled
// item failed.
func (m *AuditMetrics) passed() bool { return m.Applied > 0 && m.Failed == 0 }

// audited reports a gate on a human audit of the applied set.
func (o *Options) audited() bool {
	return o.GateRule == GateRuleCensusAudit || o.GateRule == GateRuleHoldoutAudit
}

// measureAudit samples a combo's applied items in the run's audit order,
// the first size of them, and counts the sample's labels.
func measureAudit(c Combo, scope []*Item, o *Options, size int) *AuditMetrics {
	type keyed struct {
		key [32]byte
		it  *Item
	}
	m := &AuditMetrics{Sample: []int64{}, GoldErrorIDs: []int64{}}
	var applied []keyed
	for _, it := range scope {
		ans, ok := candidates(it, c)
		answer := agreed(&ans, ok)
		if !o.accepts(answer) {
			continue
		}
		switch g := gold(it); {
		case g == answer:
			applied = append(applied, keyed{AuditKey(o.auditRun, it.ID), it})
		case g != "":
			m.GoldErrors++
			m.GoldErrorIDs = append(m.GoldErrorIDs, it.ID)
		}
	}
	slices.SortFunc(applied, func(a, b keyed) int { return bytes.Compare(a.key[:], b.key[:]) })
	m.Applied = len(applied)
	for _, a := range applied[:min(size, len(applied))] {
		m.Sample = append(m.Sample, a.it.ID)
		switch a.it.Adjudication {
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
	return m
}

// auditReason says why a complete census's or a holdout's (what) audit
// failed a combo.
func auditReason(c Combo, m *AuditMetrics, what string) string {
	if m.Applied == 0 {
		return what + ": nothing applied in " + comboKey(c)
	}
	return sampleReason("audit", c, m)
}

// sampleReason says why an audit sample failed a combo.
func sampleReason(audit string, c Combo, m *AuditMetrics) string {
	return fmt.Sprintf("%s: %d of %d sampled items failed in %s (%d different, %d pending; %d applied)",
		audit, m.Failed, len(m.Sample), comboKey(c), m.AdjudicatedDifferent, m.Pending, m.Applied)
}

// holdoutAuditReason names the first combo whose holdout audit failed, or
// "".
func holdoutAuditReason(combos []Combo, report *StratumReport) string {
	for _, c := range combos {
		m := report.Combos[comboKey(c)]
		if m.Census == nil && m.Audit != nil && !m.Audit.passed() {
			return auditReason(c, m.Audit, "holdout")
		}
	}
	return ""
}
