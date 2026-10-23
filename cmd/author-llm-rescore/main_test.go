package main

import (
	"bytes"
	"strings"
	"testing"

	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/services"
)

// One row per stratum of either report, sorted, with the decision before and
// after and the invalid shares; a stratum one report lacks says so.
func TestWriteTSV(t *testing.T) {
	share := func(p string, n, total int) llmeval.InvalidShare {
		return llmeval.InvalidShare{Participant: p, Invalid: n, Total: total}
	}
	p := &services.AuthorLLMRescorePreview{
		Before: llmeval.Report{
			Strata: map[string]llmeval.StratumReport{
				"mononym.cyrl": {Holdout: 900, JudgeInvalid: []llmeval.InvalidShare{share("judge_a", 99, 900)},
					Combos: map[string]llmeval.ComboMetrics{"A1": {Invalid: []llmeval.InvalidShare{share("production_a", 86, 900)}}}},
			},
			Decision: llmeval.Decision{Strata: []llmeval.StratumDecision{{Stratum: "mononym.cyrl", Reason: "invalid: judge_a 99 of 900"}}},
		},
		After: llmeval.Report{
			Strata: map[string]llmeval.StratumReport{
				"mononym.cyrl":  {Holdout: 900, JudgeInvalid: []llmeval.InvalidShare{share("judge_a", 1, 900)}},
				"initials.cyrl": {Holdout: 900},
			},
			Decision: llmeval.Decision{Strata: []llmeval.StratumDecision{
				{Stratum: "initials.cyrl", Passed: true}, {Stratum: "mononym.cyrl", Reason: "gate: no combo passed"}}},
		},
	}
	var out bytes.Buffer
	if err := writeTSV(&out, p); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	want := []string{
		strings.Join(tsvHeader, "\t"),
		"initials.cyrl\t900\tfalse\tnot measured\ttrue\t\t\t\t\t",
		"mononym.cyrl\t900\tfalse\tinvalid: judge_a 99 of 900\tfalse\tgate: no combo passed\t" +
			"judge_a=99/900\tjudge_a=1/900\tproduction_a=86/900\t",
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %q", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}
