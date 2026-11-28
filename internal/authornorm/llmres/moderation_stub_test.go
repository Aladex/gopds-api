package llmres

import "testing"

func TestIsModerationStub(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		content string
		want    bool
	}{
		{"canned refusal, no model", "", "关于这个问题，我没有相关信息，您可以尝试问我其它问题，我会尽力为您解答~", true},
		{"padded refusal, no model", "", "  \nSorry, I cannot answer that.\n", true},
		{"JSON answer, no model", "", `{"results":[]}`, false},
		{"fenced JSON answer, no model", "", "```json\n{\"results\":[]}\n```", false},
		{"text around a JSON object, no model", "", `Here it is: {"results":[]}`, false},
		{"empty reply, no model", "", "", false},
		{"blank reply, no model", "", " \n\t", false},
		{"canned refusal under a model id", "deepseek-v4-flash", "关于这个问题，我没有相关信息", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsModerationStub(tc.model, []byte(tc.content)); got != tc.want {
				t.Fatalf("IsModerationStub(%q, %q) = %v, want %v", tc.model, tc.content, got, tc.want)
			}
		})
	}
}
