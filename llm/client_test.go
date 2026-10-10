package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/config"
)

// The shared client is the only place an LLM request is built. These tests pin
// the wire contract against a stub endpoint: requests go to the configured
// base URL, carry the Authorization header only when a key is configured, ask
// for the configured model, honor the caller's context deadline, and never
// let response content into an error (which is where a caller's log would put
// it).

// recordedRequest is what the stub endpoint saw of one request.
type recordedRequest struct {
	Method        string
	Path          string
	Authorization string
	ContentType   string
	Body          map[string]any
}

// llmEndpoint records every request it serves and answers with a fixed body
// and status.
type llmEndpoint struct {
	mu       sync.Mutex
	requests []recordedRequest
	delay    time.Duration
}

func (e *llmEndpoint) record(r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	e.mu.Lock()
	e.requests = append(e.requests, recordedRequest{
		Method:        r.Method,
		Path:          r.URL.Path,
		Authorization: r.Header.Get("Authorization"),
		ContentType:   r.Header.Get("Content-Type"),
		Body:          body,
	})
	e.mu.Unlock()
}

func (e *llmEndpoint) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.requests)
}

func (e *llmEndpoint) last(t *testing.T) recordedRequest {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.requests) == 0 {
		t.Fatal("the endpoint saw no request")
	}
	return e.requests[len(e.requests)-1]
}

// chatCompletionBody is the reply body of a successful chat completion.
func chatCompletionBody(content string) string {
	payload, err := json.Marshal(OpenAIResponse{
		Choices: []Choice{{Message: Message{Role: "assistant", Content: content}}},
	})
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func (e *llmEndpoint) server(t *testing.T, reply string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.record(r)
		if e.delay > 0 {
			time.Sleep(e.delay)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientSendsToConfiguredBaseURLWithKey(t *testing.T) {
	endpoint := &llmEndpoint{}
	srv := endpoint.server(t, chatCompletionBody("ok"), http.StatusOK)

	client := NewClient(config.LLMConfig{
		BaseURL: srv.URL + "/v1",
		APIKey:  "secret-key",
		Model:   "test-model",
		Timeout: 2 * time.Second,
	})

	resp, err := client.ChatCompletion(context.Background(), []Message{{Role: messageRoleUser, Content: "prompt text"}})
	if err != nil {
		t.Fatalf("ChatCompletion() = %v, want nil", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "ok" {
		t.Errorf("response decoded wrongly: %+v", resp)
	}

	got := endpoint.last(t)
	if got.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.Method)
	}
	if got.Path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", got.Path)
	}
	if got.Authorization != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want the configured key", got.Authorization)
	}
	if got.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.ContentType)
	}
	if got.Body["model"] != "test-model" {
		t.Errorf("body model = %v, want test-model", got.Body["model"])
	}
	messages, _ := got.Body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("body messages = %v, want one message", got.Body["messages"])
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "prompt text" {
		t.Errorf("body message = %v, want the sent role and content", first)
	}
}

// A keyless gateway is a valid provider: the request must simply carry no
// Authorization header.
func TestClientKeylessGatewaySendsNoAuthorization(t *testing.T) {
	endpoint := &llmEndpoint{}
	srv := endpoint.server(t, chatCompletionBody("ok"), http.StatusOK)

	client := NewClient(config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second})
	if !client.Configured() {
		t.Fatal("a non-OpenAI base URL without a key must read as configured")
	}

	if _, err := client.ChatCompletion(context.Background(), []Message{{Role: messageRoleUser, Content: "prompt"}}); err != nil {
		t.Fatalf("ChatCompletion() = %v, want nil", err)
	}
	if auth := endpoint.last(t).Authorization; auth != "" {
		t.Errorf("Authorization = %q, want no header", auth)
	}
}

func TestClientConfiguredMatchesSection(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.LLMConfig
		want bool
	}{
		{name: "no base URL", cfg: config.LLMConfig{}, want: false},
		{name: "gateway", cfg: config.LLMConfig{BaseURL: "http://gateway:8317/v1"}, want: true},
		{name: "public API with key", cfg: config.LLMConfig{BaseURL: "https://api.openai.com/v1", APIKey: "k"}, want: true},
		{name: "public API keyless", cfg: config.LLMConfig{BaseURL: "https://api.openai.com/v1"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewClient(tc.cfg).Configured(); got != tc.want {
				t.Errorf("Configured() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The model the client asks for defaults to the application default when the
// section named none.
func TestClientDefaultsModel(t *testing.T) {
	endpoint := &llmEndpoint{}
	srv := endpoint.server(t, chatCompletionBody("ok"), http.StatusOK)

	client := NewClient(config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second})
	if client.Model() != config.LLMDefaultModel {
		t.Errorf("Model() = %q, want %q", client.Model(), config.LLMDefaultModel)
	}
	if _, err := client.ChatCompletion(context.Background(), []Message{{Role: messageRoleUser, Content: "prompt"}}); err != nil {
		t.Fatalf("ChatCompletion() = %v, want nil", err)
	}
	if model := endpoint.last(t).Body["model"]; model != config.LLMDefaultModel {
		t.Errorf("body model = %v, want %q", model, config.LLMDefaultModel)
	}
}

// The caller's context bounds the request — the language detector relies on
// that for its shorter deadline.
func TestClientHonoursContextDeadline(t *testing.T) {
	endpoint := &llmEndpoint{delay: 300 * time.Millisecond}
	srv := endpoint.server(t, chatCompletionBody("ok"), http.StatusOK)

	client := NewClient(config.LLMConfig{BaseURL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := client.ChatCompletion(ctx, []Message{{Role: messageRoleUser, Content: "prompt"}})
	if err == nil {
		t.Fatal("ChatCompletion() = nil, want the context deadline to fail it")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ChatCompletion() = %v, want context.DeadlineExceeded", err)
	}
}

// Errors must not carry response content: a gateway can echo parts of a
// rejected request, and the callers log whatever the error says.
func TestClientErrorsCarryNoResponseContent(t *testing.T) {
	t.Run("http status", func(t *testing.T) {
		endpoint := &llmEndpoint{}
		srv := endpoint.server(t, `{"error":{"message":"echo of Война и мир from the rejected body"}}`, http.StatusInternalServerError)

		client := NewClient(config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second})
		_, err := client.ChatCompletion(context.Background(), []Message{{Role: messageRoleUser, Content: "prompt"}})
		if err == nil {
			t.Fatal("ChatCompletion() = nil, want a status error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("error = %q, want the status in it", err)
		}
		if strings.Contains(err.Error(), "Война и мир") || strings.Contains(err.Error(), "rejected body") {
			t.Errorf("error = %q, want no response content in it", err)
		}
	})

	t.Run("api error field", func(t *testing.T) {
		endpoint := &llmEndpoint{}
		srv := endpoint.server(t, `{"error":{"message":"model quoted the prompt Война и мир","type":"rate_limited","code":"429"}}`, http.StatusOK)

		client := NewClient(config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second})
		_, err := client.ChatCompletion(context.Background(), []Message{{Role: messageRoleUser, Content: "prompt"}})
		if err == nil {
			t.Fatal("ChatCompletion() = nil, want the api error surfaced")
		}
		if !strings.Contains(err.Error(), "rate_limited") || !strings.Contains(err.Error(), "429") {
			t.Errorf("error = %q, want the api error type and code in it", err)
		}
		if strings.Contains(err.Error(), "Война и мир") {
			t.Errorf("error = %q, want no api error message content in it", err)
		}
	})
}
