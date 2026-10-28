package llmeval

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden rewrites the stored K report; only an intended change to K's
// gate may do that.
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/k_report.golden.json")

// goldenInput is a fixed K evaluation over strata of every shape the gate
// meets: all accepted, errors, contested items, invalid answers, an arm that
// answers otherwise with the excerpt, a batch of ten that answers worse,
// dev items, thin repeat and anchor evidence of one participant, and one
// stratum that passes.
func goldenInput() Input {
	in := healthy()
	in.Anchor[1] = ParticipantCount{Participant: "production_b", Matched: 297, Total: 300}
	in.Repeat[3] = ParticipantCount{Participant: "judge_b", Matched: 188, Total: 200}
	in.Invalid = []ParticipantCount{{Participant: "production_a", Matched: 1, Total: 400},
		{Participant: "production_b", Matched: 9, Total: 400}}
	in.ProviderRejected = []ParticipantCount{{Participant: "production_b", Matched: 2, Total: 400}}
	add := func(stratum string, n int, prod func(i int, c Combo) [2]Answer, judges func(i int) [2]Answer) {
		items := stratumItems(stratum, n, prod)
		for i := range items {
			if judges != nil {
				items[i].Judges = judges(i)
			}
			items[i].Weight = 1 + i%3
			items[i].HasContext = i%4 != 0
			if i%17 == 0 {
				items[i].Holdout = false
			}
		}
		in.Items = append(in.Items, items...)
	}
	add("a.cyrl", 120, agreeX, nil)
	// Large enough to pass the gate: a decision with its arm and batch.
	add("e.cyrl", 700, func(i int, c Combo) [2]Answer {
		if c.Batch == 10 && i%250 == 7 {
			return [2]Answer{answer("x", "confirm"), answer("u", "case")}
		}
		return [2]Answer{answer("x", "confirm"), answer("x", "confirm")}
	}, nil)
	add("b.latn", 80, func(i int, _ Combo) [2]Answer {
		if i%40 == 3 {
			return [2]Answer{answer("y", "restructure"), answer("y", "restructure")}
		}
		return [2]Answer{answer("x", "confirm"), answer("x", "confirm")}
	}, nil)
	add("c.cyrl", 60, agreeX, func(i int) [2]Answer {
		if i%12 == 5 {
			return [2]Answer{answer("x", "confirm"), answer("z", "reclassify")}
		}
		return [2]Answer{answer("x", "confirm"), answer("x", "confirm")}
	})
	add("d.cyrl", 50, func(i int, c Combo) [2]Answer {
		switch {
		case i%10 == 1:
			return [2]Answer{answer("x", "confirm"), {Invalid: true}}
		case c.Arm == ArmB && i%7 == 2:
			return [2]Answer{answer("w", "case"), answer("w", "case")}
		case c.Batch == 10 && i%9 == 4:
			return [2]Answer{answer("x", "confirm"), answer("v", "case")}
		case i%11 == 6:
			return [2]Answer{{Valid: true, Refused: true}, {Valid: true, Refused: true}}
		}
		return [2]Answer{answer("x", "confirm"), answer("x", "confirm")}
	}, nil)
	return in
}

// The K report is byte-identical to the stored one (E4c, plan §4.5): the
// merge eval's options must leave K's evaluation exactly as it was.
func TestKReportIsByteIdenticalToTheStoredOne(t *testing.T) {
	in := goldenInput()
	report := Evaluate(&in)
	got, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "k_report.golden.json")
	if *updateGolden {
		if err = os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) // #nosec G304 -- a fixed path under testdata
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the K report moved from %s; rerun with -update-golden only for an intended change to K's gate", path)
	}
}
