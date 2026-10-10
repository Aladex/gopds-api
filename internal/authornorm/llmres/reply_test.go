package llmres

import (
	"encoding/json"
	"errors"
	"testing"
)

// V1 is strict parsing of the model reply: the closed schema, required
// fields, closed enums, and no text around the JSON value. A Markdown
// wrapper, a missing field or an extra field all make the reply invalid.

func TestParseReplyValid(t *testing.T) {
	raw := `{"form":"person","order":"family_first","roles":[{"i":0,"role":"family"},{"i":1,"role":"separator"},` +
		`{"i":2,"role":"given"}],"case_fix":[]}`
	rep, err := ParseReply([]byte(raw))
	if err != nil {
		t.Fatalf("ParseReply: %v", err)
	}
	if rep.Form != FormPerson || rep.Order != OrderFamilyFirst {
		t.Fatalf("form/order = %q/%q", rep.Form, rep.Order)
	}
	wantRoles := []RoleAssignment{{0, RoleFamily}, {1, RoleSeparator}, {2, RoleGiven}}
	if len(rep.Roles) != 3 {
		t.Fatalf("roles = %v", rep.Roles)
	}
	for i, r := range wantRoles {
		if rep.Roles[i] != r {
			t.Errorf("role %d = %+v, want %+v", i, rep.Roles[i], r)
		}
	}
	if len(rep.CaseFix) != 0 {
		t.Fatalf("case_fix = %v", rep.CaseFix)
	}
}

func TestParseReplyInvalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"missing case_fix", `{"form":"person","order":"given_first","roles":[{"i":0,"role":"given"}]}`},
		{"missing form", `{"order":"given_first","roles":[],"case_fix":[]}`},
		{"missing order", `{"form":"person","roles":[],"case_fix":[]}`},
		{"missing roles", `{"form":"person","order":"given_first","case_fix":[]}`},
		{"unknown top-level field", `{"form":"person","order":"given_first","roles":[],"case_fix":[],"confidence":0.9}`},
		{"unknown role field", `{"form":"person","order":"given_first","roles":[{"i":0,"role":"given","note":"x"}],"case_fix":[]}`},
		{"role entry without role", `{"form":"person","order":"given_first","roles":[{"i":0}],"case_fix":[]}`},
		{"role entry without i", `{"form":"person","order":"given_first","roles":[{"role":"given"}],"case_fix":[]}`},
		{"index instead of i", `{"form":"person","order":"given_first","roles":[{"index":0,"role":"given"}],"case_fix":[]}`},
		{"unknown form enum", `{"form":"human","order":"given_first","roles":[],"case_fix":[]}`},
		{"unknown order enum", `{"form":"person","order":"surname_first","roles":[],"case_fix":[]}`},
		{"unknown role enum", `{"form":"person","order":"given_first","roles":[{"i":0,"role":"name"}],"case_fix":[]}`},
		{"unknown case_fix target", `{"form":"person","order":"given_first","roles":[],"case_fix":[{"i":0,"to":"uppercase"}]}`},
		{"case_fix entry without to", `{"form":"person","order":"given_first","roles":[],"case_fix":[{"i":0}]}`},
		{"markdown wrapper", "```json\n{\"form\":\"person\",\"order\":\"given_first\",\"roles\":[],\"case_fix\":[]}\n```"},
		{"trailing explanation", `{"form":"person","order":"given_first","roles":[],"case_fix":[]} because reasons`},
		{"null form", `{"form":null,"order":"given_first","roles":[],"case_fix":[]}`},
		{"negative index", `{"form":"person","order":"given_first","roles":[{"i":-1,"role":"given"}],"case_fix":[]}`},
		{"fractional index", `{"form":"person","order":"given_first","roles":[{"i":0.5,"role":"given"}],"case_fix":[]}`},
		{"not json at all", `the author is John Doe`},
		{"empty input", ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseReply([]byte(tt.raw))
			if err == nil {
				t.Fatalf("ParseReply(%q) succeeded, want V1 violation", tt.raw)
			}
			var v *Violation
			if !errors.As(err, &v) || v.Validator != "V1" {
				t.Fatalf("ParseReply(%q) error = %v, want a V1 Violation", tt.raw, err)
			}
		})
	}
}

// TestReplySchemaMatchesClosedSets guards the schema document that is sent to
// the model against the Go closed sets: the two must never drift apart.
func TestReplySchemaMatchesClosedSets(t *testing.T) {
	var schema struct {
		Required []string `json:"required"`
		Defs     map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(ReplySchema), &schema); err != nil {
		t.Fatalf("ReplySchema is not valid JSON: %v", err)
	}
	for _, field := range []string{"form", "order", "roles", "case_fix"} {
		found := false
		for _, r := range schema.Required {
			if r == field {
				found = true
			}
		}
		if !found {
			t.Errorf("schema does not require %q", field)
		}
	}
	checkEnum := func(name string, want []string) {
		t.Helper()
		def, ok := schema.Defs[name]
		if !ok {
			t.Errorf("schema has no $defs/%s", name)
			return
		}
		if len(def.Enum) != len(want) {
			t.Errorf("$defs/%s enum = %v, want %v", name, def.Enum, want)
			return
		}
		for i, w := range want {
			if def.Enum[i] != w {
				t.Errorf("$defs/%s enum[%d] = %q, want %q", name, i, def.Enum[i], w)
			}
		}
	}
	checkEnum("form", enumStrings(Forms()))
	checkEnum("order", enumStrings(Orders()))
	checkEnum("role", enumStrings(Roles()))
	checkEnum("caseFix", enumStrings(CaseFixKinds()))
}

// enumStrings converts a slice of ~string closed-set values for comparison.
func enumStrings[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

func TestEnumHelpers(t *testing.T) {
	if got := enumStrings(Forms()); got[0] != "person" || len(got) != 6 {
		t.Fatalf("Forms() = %v", got)
	}
	if got := enumStrings(Orders()); len(got) != 4 {
		t.Fatalf("Orders() = %v", got)
	}
	if got := enumStrings(Roles()); len(got) != 10 {
		t.Fatalf("Roles() = %v", got)
	}
	if got := enumStrings(CaseFixKinds()); len(got) != 2 {
		t.Fatalf("CaseFixKinds() = %v", got)
	}
}
