package llmreq

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gopds-api/internal/authornorm/llmres"
)

func thilliezInput() ItemInput {
	return ItemInput{
		Fields: []llmres.FieldValue{
			{Field: llmres.FieldFirst, Text: "Thilliez,"},
			{Field: llmres.FieldLast, Text: "Franck"},
		},
		Script: "Latn",
		Flags:  []string{"unusual_characters"},
	}
}

func TestBuildUserMessageCarriesTokensNotFields(t *testing.T) {
	ctx := &llmres.BookContext{Title: "Le Syndrome E", OtherCredits: []llmres.Credit{{Role: "translator", Name: "N. N."}}}
	msg, err := BuildUserMessage([]Item{
		{Key: "1", Input: thilliezInput()},
		{Key: "2", Input: thilliezInput(), Context: ctx},
	})
	if err != nil {
		t.Fatalf("BuildUserMessage: %v", err)
	}
	var got struct {
		Items []struct {
			ID          string          `json:"id"`
			Tokens      []wireToken     `json:"tokens"`
			Script      string          `json:"script"`
			Flags       []string        `json:"flags"`
			BookContext json.RawMessage `json:"book_context"`
		} `json:"items"`
	}
	dec := json.NewDecoder(strings.NewReader(string(msg)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("user message is not the documented shape: %v\n%s", err, msg)
	}
	if len(got.Items) != 2 || got.Items[0].ID != "1" || got.Items[1].ID != "2" {
		t.Fatalf("items = %+v", got.Items)
	}
	want := []wireToken{{0, "Thilliez", "first"}, {1, ",", "first"}, {2, "Franck", "last"}}
	if len(got.Items[0].Tokens) != len(want) {
		t.Fatalf("tokens = %+v, want %+v", got.Items[0].Tokens, want)
	}
	for i := range want {
		if got.Items[0].Tokens[i] != want[i] {
			t.Errorf("token %d = %+v, want %+v", i, got.Items[0].Tokens[i], want[i])
		}
	}
	if string(got.Items[0].BookContext) != "null" {
		t.Errorf("item without excerpt carries book_context %s, want null", got.Items[0].BookContext)
	}
	if !strings.Contains(string(got.Items[1].BookContext), `"translator"`) {
		t.Errorf("excerpt not carried: %s", got.Items[1].BookContext)
	}
	if got.Items[0].Script != "Latn" || len(got.Items[0].Flags) != 1 {
		t.Errorf("script/flags = %q/%v", got.Items[0].Script, got.Items[0].Flags)
	}
}

func TestBuildUserMessageRefusesWhatCannotBeSent(t *testing.T) {
	long := ItemInput{Fields: []llmres.FieldValue{{Field: llmres.FieldLast, Text: strings.Repeat("а ", 20)}}, Script: "Cyrl"}
	if _, err := BuildUserMessage([]Item{{Key: "1", Input: long}}); !errors.Is(err, llmres.ErrInputTooLong) {
		t.Errorf("too long input: err = %v, want ErrInputTooLong", err)
	}
	if _, err := BuildUserMessage(nil); err == nil {
		t.Error("empty batch accepted")
	}
	dup := []Item{{Key: "1", Input: thilliezInput()}, {Key: "1", Input: thilliezInput()}}
	if _, err := BuildUserMessage(dup); err == nil {
		t.Error("duplicate item keys accepted")
	}
	if _, err := BuildUserMessage([]Item{{Key: "", Input: thilliezInput()}}); err == nil {
		t.Error("empty item key accepted")
	}
}

func TestBatchSchemaWrapsTheReplySchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(BatchSchema(), &schema); err != nil {
		t.Fatalf("batch schema is not JSON: %v", err)
	}
	if schema["additionalProperties"] != false {
		t.Error("batch envelope must refuse unknown properties")
	}
	defs, ok := schema["$defs"].(map[string]any)
	if !ok || defs["role"] == nil || defs["form"] == nil {
		t.Fatalf("$defs not hoisted to the root, where the item $refs resolve: %v", schema["$defs"])
	}
	item := schema["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)
	required := item["required"].([]any)
	if required[0] != "id" || len(required) != 5 {
		t.Errorf("item required = %v, want id plus the four reply fields", required)
	}
	if _, has := item["$defs"]; has {
		t.Error("item keeps its own $defs")
	}
	if first, second := BatchSchema(), BatchSchema(); !bytes.Equal(first, second) {
		t.Error("schema bytes are not deterministic")
	}
}

const okReply = `"form":"person","order":"family_first",` +
	`"roles":[{"i":0,"role":"family"},{"i":1,"role":"separator"},{"i":2,"role":"given"}],"case_fix":[]`

func TestSplitReplyPerItem(t *testing.T) {
	content := `{"results":[` +
		`{"id":"1",` + okReply + `},` +
		`{"id":"3",` + okReply + `},` +
		`{"id":"3",` + okReply + `},` +
		`{"id":"9",` + okReply + `},` +
		`{"id":"4","form":"person"},` +
		`{"form":"person"}` +
		`]}`
	got, err := SplitReply([]byte(content), []string{"1", "2", "3", "4"})
	if err != nil {
		t.Fatalf("SplitReply: %v", err)
	}
	if got["1"].Err != nil || got["1"].Reply == nil || got["1"].Reply.Form != llmres.FormPerson {
		t.Errorf("item 1 = %+v, want a parsed reply", got["1"])
	}
	for key, want := range map[string]ItemErrorClass{
		"2": ItemMissing, "3": ItemDuplicate, "4": ItemInvalid,
	} {
		var ie *ItemError
		if !errors.As(got[key].Err, &ie) || ie.Class != want {
			t.Errorf("item %s err = %v, want class %s", key, got[key].Err, want)
		}
		if got[key].Reply != nil {
			t.Errorf("item %s carries a reply despite its error", key)
		}
	}
	if _, has := got["9"]; has {
		t.Error("an unknown id became an item")
	}
}

func TestSplitReplyMalformedBatch(t *testing.T) {
	for _, content := range []string{
		"```json\n{\"results\":[]}\n```",
		`{"results":[]} trailing`,
		`{"results":{}}`,
		`{"items":[]}`,
		`{"results":[],"note":"x"}`,
		``,
		`[]`,
	} {
		if _, err := SplitReply([]byte(content), []string{"1"}); !errors.Is(err, ErrMalformedBatch) {
			t.Errorf("%q: err = %v, want ErrMalformedBatch", content, err)
		}
	}
}

func TestPromptAndSchemaDigestsAreStable(t *testing.T) {
	prompt, schema := PromptSHA256(), SchemaSHA256()
	if prompt != PromptSHA256() || schema != SchemaSHA256() {
		t.Fatal("digests are not deterministic")
	}
	if PromptSHA256() == SchemaSHA256() {
		t.Fatal("prompt and schema share a digest")
	}
	for _, word := range []string{"pen name", "translator", "cannot_tell", "results", "id"} {
		if !strings.Contains(SystemPrompt, word) {
			t.Errorf("system prompt does not mention %q", word)
		}
	}
}

func TestFingerprintItemIsFixed(t *testing.T) {
	a, err := BuildUserMessage([]Item{FingerprintItem()})
	if err != nil {
		t.Fatalf("fingerprint item: %v", err)
	}
	b, _ := BuildUserMessage([]Item{FingerprintItem()})
	if !bytes.Equal(a, b) {
		t.Error("fingerprint request is not byte-stable")
	}
}
