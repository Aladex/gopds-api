package llmreq

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

var testOutput = OutputLimits{Field: OutputFieldMaxCompletionTokens, PerCall: 1000, PerItem: 3000}

func baseIdentity(t *testing.T) Identity {
	t.Helper()
	id, err := NewIdentity("http://cli-proxy-api.bots.svc:8317/v1", []Participant{
		{Slot: SlotProductionA, Model: "gpt-6-luna"},
		{Slot: SlotProductionB, Model: "deepseek-v4-pro"},
		{Slot: SlotJudgeA, Model: "gpt-6-sol", ReasoningEffort: "high"},
		{Slot: SlotJudgeB, Model: "claude-opus-5"},
	}, testOutput)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	return id
}

func TestNormalizeBaseURL(t *testing.T) {
	for raw, want := range map[string]string{
		"http://cli-proxy-api.bots.svc:8317/v1":          "http://cli-proxy-api.bots.svc:8317/v1",
		"HTTP://CLI-Proxy-API.bots.svc:8317/v1/":         "http://cli-proxy-api.bots.svc:8317/v1",
		"https://user:secret@api.v2.neokens.com/v1?k=s3": "https://api.v2.neokens.com/v1",
		"https://api.v2.neokens.com/v1#frag":             "https://api.v2.neokens.com/v1",
	} {
		got, err := NormalizeBaseURL(raw)
		if err != nil || got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "not a url", "ftp://host/v1", "/v1"} {
		if _, err := NormalizeBaseURL(raw); err == nil {
			t.Errorf("NormalizeBaseURL(%q) accepted", raw)
		}
	}
}

func TestIdentityVersionCoversEveryAnswerInput(t *testing.T) {
	base := baseIdentity(t)
	v := base.Version()
	if !strings.HasPrefix(v, VersionPrefix) || len(v) != len(VersionPrefix)+64 {
		t.Fatalf("version %q has the wrong shape", v)
	}
	same := baseIdentity(t)
	if same.Version() != v {
		t.Fatal("equal identities give different versions")
	}

	temp := 1.0
	mutations := map[string]func(*Identity){
		"base url":         func(i *Identity) { i.BaseURL = "https://api.v2.neokens.com/v1" },
		"model":            func(i *Identity) { i.Participants[3].Model = "kimi-k3" },
		"temperature":      func(i *Identity) { i.Participants[3].Temperature = &temp },
		"reasoning effort": func(i *Identity) { i.Participants[2].ReasoningEffort = "medium" },
		"prompt":           func(i *Identity) { i.PromptSHA256 = strings.Repeat("0", 64) },
		"schema":           func(i *Identity) { i.SchemaSHA256 = strings.Repeat("0", 64) },
		"validator":        func(i *Identity) { i.ValidatorVersion = "x" },
		"tokenizer":        func(i *Identity) { i.TokenizerVersion = "x" },
		"context":          func(i *Identity) { i.ContextVersion = "x" },
		"output field":     func(i *Identity) { i.Output.Field = OutputFieldMaxTokens },
		"output ceiling":   func(i *Identity) { i.Output.PerItem++ },
	}
	for name, mutate := range mutations {
		changed := baseIdentity(t)
		mutate(&changed)
		if changed.Version() == v {
			t.Errorf("changing the %s keeps the version", name)
		}
	}
}

func TestIdentityNeverCarriesAKey(t *testing.T) {
	id, err := NewIdentity("https://k:sk-canary-key@api.v2.neokens.com/v1?api_key=sk-canary-key", []Participant{
		{Slot: SlotProductionA, Model: "a"}, {Slot: SlotProductionB, Model: "b"},
		{Slot: SlotJudgeA, Model: "c"}, {Slot: SlotJudgeB, Model: "d"},
	}, testOutput)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	raw, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "canary") || strings.Contains(id.Version(), "canary") {
		t.Errorf("identity carries the key: %s", raw)
	}
}

func TestNewIdentityNeedsFourDistinctParticipants(t *testing.T) {
	four := func() []Participant {
		return []Participant{
			{Slot: SlotProductionA, Model: "a"}, {Slot: SlotProductionB, Model: "b"},
			{Slot: SlotJudgeA, Model: "c"}, {Slot: SlotJudgeB, Model: "d"},
		}
	}
	cases := map[string]func([]Participant) []Participant{
		"three":           func(p []Participant) []Participant { return p[:3] },
		"empty model":     func(p []Participant) []Participant { p[1].Model = ""; return p },
		"same model":      func(p []Participant) []Participant { p[3].Model = "a"; return p },
		"same slot":       func(p []Participant) []Participant { p[3].Slot = SlotJudgeA; return p },
		"unknown slot":    func(p []Participant) []Participant { p[3].Slot = "judge_c"; return p },
		"padded model":    func(p []Participant) []Participant { p[0].Model = " a"; return p },
		"bad effort":      func(p []Participant) []Participant { p[0].ReasoningEffort = "extreme"; return p },
		"bad temperature": func(p []Participant) []Participant { x := 3.5; p[0].Temperature = &x; return p },
	}
	for name, mutate := range cases {
		if _, err := NewIdentity("http://gw/v1", mutate(four()), testOutput); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The slot order of the input does not matter.
	p := four()
	p[0], p[3] = p[3], p[0]
	a, err := NewIdentity("http://gw/v1", p, testOutput)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewIdentity("http://gw/v1", four(), testOutput)
	if a.Version() != b.Version() {
		t.Error("participant order changes the version")
	}
}

func TestNewIdentityRefusesUnknownOutputLimits(t *testing.T) {
	p := []Participant{
		{Slot: SlotProductionA, Model: "a"}, {Slot: SlotProductionB, Model: "b"},
		{Slot: SlotJudgeA, Model: "c"}, {Slot: SlotJudgeB, Model: "d"},
	}
	for _, o := range []OutputLimits{
		{Field: "max_output", PerCall: 1, PerItem: 1},
		{Field: OutputFieldMaxTokens, PerCall: math.MaxInt, PerItem: 1},
		{Field: OutputFieldMaxTokens, PerCall: 0, PerItem: math.MaxInt},
		{Field: OutputFieldMaxTokens, PerCall: 1, PerItem: 60000},
		{Field: OutputFieldMaxTokens, PerCall: 1, PerItem: 0},
		{Field: OutputFieldMaxTokens, PerCall: -1, PerItem: 1},
	} {
		if _, err := NewIdentity("http://gw/v1", p, o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

func TestReserveCoversTheRequestBoundAndTheCeiling(t *testing.T) {
	o := testOutput
	msg, err := BuildUserMessage([]Item{FingerprintItem()})
	if err != nil {
		t.Fatal(err)
	}
	if got := RequestBytes(msg); got > RequestBoundBytes(1, 0) {
		t.Fatalf("a one-item request (%d bytes) exceeds its bound %d", got, RequestBoundBytes(1, 0))
	}
	if r := o.Reserve(10, 3); r != int64(RequestBoundBytes(10, 3))+PromptTemplateTokens+int64(o.Ceiling(10)) {
		t.Errorf("reserve %d is not the prompt bound plus the ceiling", r)
	}
	if o.Ceiling(10) != 31000 {
		t.Errorf("ceiling of ten = %d", o.Ceiling(10))
	}
}
