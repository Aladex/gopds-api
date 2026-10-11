package llmreq

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Replies recorded on production (v0.48.0, eval run 1) through the gateway,
// which does not enforce response_format json_schema for every model. Their
// shapes are kept exactly; only the item is one realistic three-token name.

// prodValid is gpt-6-luna's and gpt-6-sol's reply: the schema's shape.
const prodValid = `{"results":[{"case_fix":[],"form":"person","id":"1","order":"given_first",` +
	`"roles":[{"i":0,"role":"given"},{"i":1,"role":"additional"},{"i":2,"role":"family"}]}]}`

// prodIndexKeys is deepseek-v4-pro's reply: "index" for "i", no case_fix.
const prodIndexKeys = `{"results":[{"id":"1","form":"person","order":"given_first",` +
	`"roles":[{"index":0,"role":"given"},{"index":1,"role":"additional"},{"index":2,"role":"family"}]}]}`

// prodFenced is claude-opus-5's reply: the same invented shape in a
// Markdown fence.
const prodFenced = "```json\n" + prodIndexKeys + "\n```"

func TestProductionRepliesParseStrictlyAndAFenceIsAccepted(t *testing.T) {
	// The schema's shape parses, bare or in one fence.
	for name, content := range map[string]string{
		"bare":             prodValid,
		"json fence":       "```json\n" + prodValid + "\n```",
		"plain fence":      "```\n" + prodValid + "\n```",
		"fence and spaces": "  \n```json\n" + prodValid + "\n```\n  ",
	} {
		got, err := SplitReply([]byte(content), []string{"1"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got["1"].Err != nil || got["1"].Reply == nil {
			t.Errorf("%s: item = %+v", name, got["1"])
		}
	}

	// The invented keys stay invalid, fenced or not: the prompt fixes the
	// shape, the parser does not guess it.
	for name, content := range map[string]string{"deepseek": prodIndexKeys, "claude": prodFenced} {
		got, err := SplitReply([]byte(content), []string{"1"})
		if err != nil {
			t.Fatalf("%s: the envelope did not parse: %v", name, err)
		}
		var ie *ItemError
		if !errors.As(got["1"].Err, &ie) || ie.Class != ItemInvalid {
			t.Errorf("%s: item = %+v, want invalid", name, got["1"])
		}
	}
}

func TestSplitReplyAcceptsOnlyOneFence(t *testing.T) {
	for _, content := range []string{
		"Here you go:\n```json\n" + prodValid + "\n```",
		"```json\n" + prodValid + "\n```\nDone.",
		"```json\n```json\n" + prodValid + "\n```\n```",
		"```json\n" + prodValid,
		prodValid + "\n```",
		"```js\n" + prodValid + "\n```",
		"```json " + prodValid + "\n```",
		"`" + prodValid + "`",
		"```json\n" + prodValid + "```",
	} {
		if _, err := SplitReply([]byte(content), []string{"1"}); !errors.Is(err, ErrMalformedBatch) {
			t.Errorf("%q: err = %v, want ErrMalformedBatch", content, err)
		}
	}
}

// schemaWords lists every property name and every enum value of a JSON
// schema.
func schemaWords(node any, words map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			for name := range props {
				words[name] = true
			}
		}
		if enum, ok := v["enum"].([]any); ok {
			for _, e := range enum {
				words[e.(string)] = true
			}
		}
		for _, child := range v {
			schemaWords(child, words)
		}
	case []any:
		for _, child := range v {
			schemaWords(child, words)
		}
	}
}

// The prompt names every key and every value the strict reply requires, as
// the schema sent with it declares them: a model the gateway does not hold to
// the schema still knows the exact shape.
func TestSystemPromptStatesTheWholeReplyShape(t *testing.T) {
	var schema any
	if err := json.Unmarshal(BatchSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	words := map[string]bool{}
	schemaWords(schema, words)
	if len(words) < 20 {
		t.Fatalf("the schema walk found only %d words", len(words))
	}
	// The worked example in the prompt is itself a valid reply; everything
	// else is checked on the prose without it, so the example cannot stand in
	// for a description.
	start := strings.Index(SystemPrompt, `{"results":[{`)
	if start < 0 {
		t.Fatal("the system prompt has no example reply")
	}
	example := SystemPrompt[start:]
	end := strings.Index(example, "\n")
	if end < 0 {
		t.Fatal("the example reply is not on a line of its own")
	}
	example = example[:end]
	prose := strings.Replace(SystemPrompt, example, "", 1)

	var missing []string
	for w := range words {
		if !strings.Contains(prose, `"`+w+`"`) {
			missing = append(missing, w)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the system prompt does not quote %v", missing)
	}
	// Every key of a result is a described item of the list, and every
	// array element is spelled as an object with all its keys, in the
	// schema's required order.
	results := schema.(map[string]any)["properties"].(map[string]any)["results"].(map[string]any)
	item := results["items"].(map[string]any)
	for _, key := range item["required"].([]any) {
		if !strings.Contains(prose, "\n- \""+key.(string)+"\": ") {
			t.Errorf("the system prompt does not describe the key %q", key)
		}
	}
	for name, prop := range item["properties"].(map[string]any) {
		elem, ok := prop.(map[string]any)["items"].(map[string]any)
		if !ok {
			continue
		}
		parts := make([]string, 0, len(elem["required"].([]any)))
		for _, key := range elem["required"].([]any) {
			parts = append(parts, regexp.QuoteMeta(`"`+key.(string)+`":`))
		}
		shape := regexp.MustCompile(`\{` + strings.Join(parts, `[^{}]*`) + `[^{}]*\}`)
		if !shape.MatchString(prose) {
			t.Errorf("the system prompt does not spell the %q element as %s", name, shape)
		}
	}

	got, err := SplitReply([]byte(example), []string{"1"})
	if err != nil || got["1"].Err != nil {
		t.Fatalf("the prompt's example does not parse: %v %+v", err, got["1"])
	}
}
