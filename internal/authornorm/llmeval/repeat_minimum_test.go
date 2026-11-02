package llmeval

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The run-wide repeat evidence (E4g, run 40): a census asks every
// candidate, so one smaller than MinRepeatItems — 122 hub candidates —
// can never repeat 200 items. It needs every census item repeated by
// every participant instead; a census at least that big, a holdout and K's
// gate still need MinRepeatItems. The suite's items and the supplement are
// never repeated, so they do not count toward the census.

// repeatedBy is the run-wide repeat count of n items reproduced by every
// participant.
func repeatedBy(n int) []ParticipantCount {
	out := make([]ParticipantCount, 0, len(participants))
	for _, p := range participants {
		out = append(out, ParticipantCount{Participant: p, Matched: n, Total: n})
	}
	return out
}

// repeatEvidence is a run's repeat evidence reasons.
func repeatEvidence(r *Report) []string {
	var out []string
	for _, reason := range r.Decision.Reasons {
		if strings.HasPrefix(reason, "repeat evidence:") {
			out = append(out, reason)
		}
	}
	return out
}

// missing is every participant's repeat evidence reason at got of need.
func missing(got, need int) []string {
	var out []string
	for _, p := range participants {
		out = append(out, fmt.Sprintf("repeat evidence: %s answered %d of the %d items required", p, got, need))
	}
	return out
}

func TestASmallCensusNeedsEveryItemRepeated(t *testing.T) {
	for name, c := range map[string]struct {
		opts                 Options
		census, suite, extra int
		repeated             int
		want                 []string
	}{
		"a census audit of 122, every item repeated": {opts: auditOpts, census: 122, repeated: 122},
		"a plain census of 122, every item repeated": {opts: censusOpts, census: 122, repeated: 122},
		"a census of 122, one item not repeated": {opts: auditOpts, census: 122, repeated: 121,
			want: missing(121, 122)},
		"a census of 122 beside a suite and a supplement": {opts: auditOpts, census: 122, suite: 300, extra: 100,
			repeated: 121, want: missing(121, 122)},
		"a census of 300 still needs 200": {opts: auditOpts, census: 300, repeated: 199, want: missing(199, 200)},
		"a census of 300, 200 repeated":   {opts: auditOpts, census: 300, repeated: 200},
		"a holdout of 122 still needs 200": {opts: holdoutOpts, census: 122, repeated: 122,
			want: missing(122, 200)},
		"K's gate of 122 still needs 200": {opts: Options{}, census: 122, repeated: 122, want: missing(122, 200)},
	} {
		t.Run(name, func(t *testing.T) {
			in := auditStratum(c.census)
			same := Answer{Valid: true, Canonical: "same", Tier: "merge"}
			for i := range c.suite + c.extra {
				it := Item{ID: int64(10000 + i), Stratum: "bucket", Script: "cyrl", Weight: 1, Holdout: true,
					Judges: [2]Answer{same, same}, Candidates: map[Combo][2]Answer{{Arm: ArmA, Batch: 1}: {same, same}},
					Adversarial: i < c.suite, Supplement: i >= c.suite}
				in.Items = append(in.Items, it)
			}
			in.Repeat = repeatedBy(c.repeated)
			r := EvaluateWith(&in, &c.opts)
			assert.Equal(t, c.want, repeatEvidence(&r))
		})
	}
}
