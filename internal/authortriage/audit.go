package authortriage

// The audit gate (DECISION §5). A stratified sample of each step's planned
// decisions is labeled against the judges' evidence by two labelers; a row is
// "different" when either says so, and an "unclear" row is replaced by the
// next reserve row of its class, so Labeled counts same and different
// verdicts only. Step 1 passes with at least 300 labels and none different;
// any other step with at least 50 and at most 2 different, or it is dropped.
//
// auditV1 holds the recorded labels of triage-v1. It is empty until the audit
// commit fills it, so no step applies before then.
var auditV1 = map[Step]Audit{}

// Audit is one step's labels.
type Audit struct {
	Labeled   int
	Different int
}

// AuditEntry is one step's labels and verdict, as a batch stores the audit
// it was applied under.
type AuditEntry struct {
	Step      Step `json:"step"`
	Labeled   int  `json:"labeled"`
	Different int  `json:"different"`
	Passed    bool `json:"passed"`
}

// Sample and reserve sizes of the audit.
const (
	fourModelSample = 300
	stepSample      = 50
	reserveShare    = 5 // a reserve is a fifth of the sample
)

// AuditPasses reports whether a step's labels pass its gate.
func AuditPasses(s Step, a Audit) bool {
	switch {
	case s == StepFourModel:
		return a.Labeled >= fourModelSample && a.Different == 0
	case s > StepFourModel && s <= StepParticleOnly:
		return a.Labeled >= stepSample && a.Different <= 2
	}
	return false
}

// AuditedSteps are the steps of the recorded audit that pass, in step order:
// the only steps a batch applies.
func AuditedSteps() []Step {
	var out []Step
	for _, e := range AuditLog() {
		if e.Passed {
			out = append(out, e.Step)
		}
	}
	return out
}

// AuditLog is every step of the recorded audit in step order, passed or not.
func AuditLog() []AuditEntry {
	var out []AuditEntry
	for _, s := range Steps() {
		a, ok := auditV1[s]
		if !ok {
			continue
		}
		out = append(out, AuditEntry{Step: s, Labeled: a.Labeled, Different: a.Different, Passed: AuditPasses(s, a)})
	}
	return out
}

// SampleSize is how many of a step's planned items are labeled.
func SampleSize(s Step) int {
	if s == StepFourModel {
		return fourModelSample
	}
	return stepSample
}

// ReserveSize is how many more rows of a step are drawn to replace unclear
// labels.
func ReserveSize(s Step) int { return SampleSize(s) / reserveShare }
