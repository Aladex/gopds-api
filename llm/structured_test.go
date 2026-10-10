package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gopds-api/config"
)

// The structured call is what the author layer sends: one of three output
// modes, the participant's own model and parameters, its own per-request
// timeout, and a response reduced to the standard OpenAI fields — model,
// content (or the joined tool-call arguments), finish reason and usage.
// Failures come back as closed classes, never with response content.

const structuredCanary = "CANARY-Zxq-structured"

var testSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"string"}}}`)

func structuredClient(url, key string) *Client {
	return NewClient(config.LLMConfig{BaseURL: url, APIKey: key, Model: "shared-default", Timeout: 30 * time.Second})
}

func serve(t *testing.T, status int, body string, hdr map[string]string) (*httptest.Server, *llmEndpoint) {
	t.Helper()
	ep := &llmEndpoint{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ep.record(r)
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, ep
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestStructuredRequestShapePerMode(t *testing.T) {
	temp := 1.0
	for _, tc := range []struct {
		mode  OutputMode
		check func(t *testing.T, body map[string]any)
	}{
		{OutputJSONSchema, func(t *testing.T, body map[string]any) {
			rf := body["response_format"].(map[string]any)
			js := rf["json_schema"].(map[string]any)
			if rf["type"] != "json_schema" || js["name"] != "submit_results" || js["strict"] != true || js["schema"] == nil {
				t.Errorf("response_format = %v", rf)
			}
			if body["tools"] != nil {
				t.Error("json_schema mode sends tools")
			}
		}},
		{OutputJSONObject, func(t *testing.T, body map[string]any) {
			rf := body["response_format"].(map[string]any)
			if rf["type"] != "json_object" || rf["json_schema"] != nil {
				t.Errorf("response_format = %v", rf)
			}
		}},
		{OutputTool, func(t *testing.T, body map[string]any) {
			tools := body["tools"].([]any)
			fn := tools[0].(map[string]any)["function"].(map[string]any)
			if fn["name"] != "submit_results" || fn["strict"] != true || fn["parameters"] == nil {
				t.Errorf("tool = %v", fn)
			}
			choice := body["tool_choice"].(map[string]any)
			if choice["function"].(map[string]any)["name"] != "submit_results" {
				t.Errorf("tool_choice = %v", choice)
			}
			if body["response_format"] != nil {
				t.Error("tool mode sends a response_format")
			}
		}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			srv, ep := serve(t, 200, chatCompletionBody(`{"a":"x"}`), nil)
			c := structuredClient(srv.URL, "")
			_, err := c.CompleteStructured(context.Background(), &StructuredRequest{
				Model: "kimi-k3", System: "sys", User: "usr", Mode: tc.mode,
				SchemaName: "submit_results", Schema: testSchema, Temperature: &temp, Timeout: time.Second,
			})
			if err != nil {
				t.Fatalf("CompleteStructured: %v", err)
			}
			got := ep.last(t)
			if got.Authorization != "" {
				t.Error("keyless endpoint got an Authorization header")
			}
			if got.Body["model"] != "kimi-k3" {
				t.Errorf("model = %v, want the participant's own model, not the shared one", got.Body["model"])
			}
			if got.Body["temperature"] != 1.0 || got.Body["stream"] != false {
				t.Errorf("temperature/stream = %v/%v", got.Body["temperature"], got.Body["stream"])
			}
			if _, has := got.Body["reasoning_effort"]; has {
				t.Error("unset reasoning effort was sent")
			}
			msgs := got.Body["messages"].([]any)
			if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[1].(map[string]any)["content"] != "usr" {
				t.Errorf("messages = %v", msgs)
			}
			tc.check(t, got.Body)
		})
	}
}

func TestStructuredSendsKeyAndEffortOnlyWhenSet(t *testing.T) {
	srv, ep := serve(t, 200, chatCompletionBody(`{}`), nil)
	c := structuredClient(srv.URL, "sk-test")
	if _, err := c.CompleteStructured(context.Background(), &StructuredRequest{
		Model: "gpt-6-sol", System: "s", User: "u", Mode: OutputJSONObject, ReasoningEffort: "high",
	}); err != nil {
		t.Fatal(err)
	}
	got := ep.last(t)
	if got.Authorization != "Bearer sk-test" || got.Body["reasoning_effort"] != "high" {
		t.Errorf("authorization/effort = %q/%v", got.Authorization, got.Body["reasoning_effort"])
	}
	if _, has := got.Body["temperature"]; has {
		t.Error("unset temperature was sent")
	}
}

func TestStructuredJoinsToolCallFragments(t *testing.T) {
	srv, _ := serve(t, 200, fixture(t, "deepseek_v4_pro_tool_fragments.json"), nil)
	resp, err := structuredClient(srv.URL, "").CompleteStructured(context.Background(), &StructuredRequest{
		Model: "deepseek-v4-pro", System: "s", User: "u", Mode: OutputTool, SchemaName: "submit_roles", Schema: testSchema,
	})
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	want := `{"form": "person", "order": "family_first", "roles": [{"i": 0, "role": "family"}, {"i": 1, "role": "given"}], "case_fix": []}`
	if string(resp.Content) != want {
		t.Errorf("joined arguments = %s\nwant %s", resp.Content, want)
	}
	if resp.Model != "deepseek-v4-pro-0813" || resp.FinishReason != "tool_calls" {
		t.Errorf("model/finish = %q/%q", resp.Model, resp.FinishReason)
	}
	u := resp.Usage
	if !u.Present || u.PromptTokens != 1001 || u.CompletionTokens != 427 || u.ReasoningTokens == nil || *u.ReasoningTokens != 312 {
		t.Errorf("usage = %+v", u)
	}
	if !strings.Contains(string(u.Raw), `"credit"`) {
		t.Error("provider-specific usage fields are not kept for reference")
	}
}

func TestStructuredJSONSchemaContent(t *testing.T) {
	srv, _ := serve(t, 200, fixture(t, "kimi_k3_json_schema.json"), nil)
	resp, err := structuredClient(srv.URL, "").CompleteStructured(context.Background(), &StructuredRequest{
		Model: "kimi-k3", System: "s", User: "u", Mode: OutputJSONSchema, SchemaName: "submit_roles", Schema: testSchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(resp.Content), `{"form": "person"`) || resp.Model != "k3" {
		t.Errorf("content/model = %s/%q", resp.Content, resp.Model)
	}
	if resp.Usage.PromptTokens != 827 || resp.Usage.TotalTokens != 953 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestStructuredToolModeWithoutAToolCallHasNoContent(t *testing.T) {
	srv, _ := serve(t, 200, chatCompletionBody(`{"a":"prose instead of a tool call"}`), nil)
	resp, err := structuredClient(srv.URL, "").CompleteStructured(context.Background(), &StructuredRequest{
		Model: "m", System: "s", User: "u", Mode: OutputTool, SchemaName: "submit_results", Schema: testSchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 0 {
		t.Errorf("content = %s, want none: a reply outside the tool call is not taken", resp.Content)
	}
}

func TestStructuredErrorClasses(t *testing.T) {
	canaryBody := `{"error":{"message":"` + structuredCanary + `","type":"t","code":"c"}}`
	for _, tc := range []struct {
		name   string
		status int
		body   string
		hdr    map[string]string
		class  ErrorClass
		retry  time.Duration
	}{
		{"400", 400, canaryBody, nil, ErrorBadRequest, 0},
		{"404 unknown model", 404, canaryBody, nil, ErrorBadRequest, 0},
		{"401", 401, canaryBody, nil, ErrorAuth, 0},
		{"403", 403, canaryBody, nil, ErrorAuth, 0},
		{"402", 402, canaryBody, nil, ErrorQuotaExhausted, 0},
		{"429", 429, canaryBody, map[string]string{"Retry-After": "7"}, ErrorRateLimited, 7 * time.Second},
		{"429 out of quota", 429, `{"error":{"message":"` + structuredCanary +
			`","type":"insufficient_quota","code":"insufficient_quota"}}`, nil, ErrorQuotaExhausted, 0},
		{"500", 500, canaryBody, nil, ErrorServer, 0},
		{"503", 503, canaryBody, map[string]string{"Retry-After": "2"}, ErrorServer, 2 * time.Second},
		{"200 not json", 200, "<html>" + structuredCanary, nil, ErrorMalformed, 0},
		{"200 no choices", 200, `{"model":"m","choices":[]}`, nil, ErrorMalformed, 0},
		{"200 error object", 200, canaryBody, nil, ErrorServer, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serve(t, tc.status, tc.body, tc.hdr)
			_, err := structuredClient(srv.URL, "").CompleteStructured(context.Background(), &StructuredRequest{
				Model: "m", System: "s", User: structuredCanary, Mode: OutputJSONObject,
			})
			var ce *CallError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v, want a *CallError", err)
			}
			if ce.Class != tc.class || ce.RetryAfter != tc.retry {
				t.Errorf("class/retry = %s/%v, want %s/%v", ce.Class, ce.RetryAfter, tc.class, tc.retry)
			}
			if tc.status != 200 && ce.HTTPStatus != tc.status {
				t.Errorf("status = %d, want %d", ce.HTTPStatus, tc.status)
			}
			if strings.Contains(err.Error(), structuredCanary) {
				t.Errorf("error text carries content: %q", err.Error())
			}
		})
	}
}

func TestStructuredOwnTimeoutNotTheSharedOne(t *testing.T) {
	ep := &llmEndpoint{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ep.record(r)
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(chatCompletionBody(`{}`)))
	}))
	t.Cleanup(srv.Close)
	// The shared section's timeout is shorter than the call: the author layer's
	// batches outlast it, so the structured call is bounded by its own timeout.
	c := NewClient(config.LLMConfig{BaseURL: srv.URL, Model: "m", Timeout: 50 * time.Millisecond})
	if _, err := c.CompleteStructured(context.Background(), &StructuredRequest{
		Model: "m", System: "s", User: "u", Mode: OutputJSONObject, Timeout: 2 * time.Second,
	}); err != nil {
		t.Fatalf("a call inside its own timeout failed: %v", err)
	}
	_, err := c.CompleteStructured(context.Background(), &StructuredRequest{
		Model: "m", System: "s", User: "u", Mode: OutputJSONObject, Timeout: 20 * time.Millisecond,
	})
	var ce *CallError
	if !errors.As(err, &ce) || ce.Class != ErrorTimeout {
		t.Errorf("err = %v, want class timeout", err)
	}
}

func TestStructuredTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := structuredClient(url, "").CompleteStructured(context.Background(), &StructuredRequest{
		Model: "m", System: "s", User: "u", Mode: OutputJSONObject, Timeout: time.Second,
	})
	var ce *CallError
	if !errors.As(err, &ce) || ce.Class != ErrorTransport {
		t.Errorf("err = %v, want class transport_error", err)
	}
}

func TestStructuredRefusesUnknownModeAndUnconfigured(t *testing.T) {
	srv, ep := serve(t, 200, chatCompletionBody(`{}`), nil)
	if _, err := structuredClient(srv.URL, "").CompleteStructured(context.Background(), &StructuredRequest{
		Model: "m", System: "s", User: "u", Mode: "free_text",
	}); err == nil {
		t.Error("unknown mode accepted")
	}
	if _, err := structuredClient("", "").CompleteStructured(context.Background(), &StructuredRequest{
		Model: "m", System: "s", User: "u", Mode: OutputJSONObject,
	}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("unconfigured: err = %v, want ErrNotConfigured", err)
	}
	if ep.count() != 0 {
		t.Error("a refused request reached the endpoint")
	}
}

func TestStructuredCarriesTheOutputCeiling(t *testing.T) {
	for field, other := range map[string]string{"max_completion_tokens": "max_tokens", "max_tokens": "max_completion_tokens"} {
		srv, ep := serve(t, 200, chatCompletionBody(`{}`), nil)
		_, err := structuredClient(srv.URL, "").CompleteStructured(context.Background(), &StructuredRequest{
			Model: "m", System: "s", User: "u", Mode: OutputJSONObject, MaxOutputTokens: 4000, OutputLimitField: field,
		})
		if err != nil {
			t.Fatal(err)
		}
		body := ep.last(t).Body
		if body[field] != 4000.0 {
			t.Errorf("%s = %v, want 4000", field, body[field])
		}
		if _, has := body[other]; has {
			t.Errorf("%s sent as well", other)
		}
	}
	srv, ep := serve(t, 200, chatCompletionBody(`{}`), nil)
	if _, err := structuredClient(srv.URL, "").CompleteStructured(context.Background(), &StructuredRequest{
		Model: "m", System: "s", User: "u", Mode: OutputJSONObject, MaxOutputTokens: 1, OutputLimitField: "max_output",
	}); err == nil {
		t.Error("an unknown ceiling field was accepted")
	}
	if ep.count() != 0 {
		t.Error("a refused request reached the endpoint")
	}
}
