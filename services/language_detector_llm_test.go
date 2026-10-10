package services

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/llm"
)

// The language detector's OpenAI arbiter used to run its own HTTP call with
// its own key read; it must now send through the one shared LLM client and the
// llm configuration section, prompt unchanged, and keep skipping quietly when
// the LLM is not configured.

// recordingLLMServer serves one canned chat completion and records what the
// last request carried.
type recordingLLMServer struct {
	URL string

	mu            sync.Mutex
	hits          int
	path          string
	authorization string
	model         string
	content       string
}

func newRecordingLLMServer(t *testing.T, reply string, delay time.Duration) *recordingLLMServer {
	t.Helper()

	rec := &recordingLLMServer{}
	body, err := json.Marshal(llm.OpenAIResponse{
		Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: reply}}},
	})
	if err != nil {
		t.Fatalf("marshaling stub reply: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string        `json:"model"`
			Messages []llm.Message `json:"messages"`
		}
		if b, err := io.ReadAll(r.Body); err == nil {
			_ = json.Unmarshal(b, &request)
		}
		rec.mu.Lock()
		rec.hits++
		rec.path = r.URL.Path
		rec.authorization = r.Header.Get("Authorization")
		rec.model = request.Model
		if len(request.Messages) > 0 {
			rec.content = request.Messages[0].Content
		}
		rec.mu.Unlock()

		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	rec.URL = srv.URL
	return rec
}

func TestDetectWithOpenAIGoesThroughConfiguredEndpoint(t *testing.T) {
	server := newRecordingLLMServer(t, "en", 0)

	detector := NewLanguageDetectorWithClient(true, 2*time.Second, llm.NewClient(config.LLMConfig{
		BaseURL: server.URL,
		APIKey:  "detector-key",
		Model:   "detector-model",
	}))

	text := "This is a clearly English text sample used to exercise the arbiter."
	if got := detector.detectWithOpenAI(text); got != "en" {
		t.Fatalf("detectWithOpenAI() = %q, want en", got)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", server.path)
	}
	if server.authorization != "Bearer detector-key" {
		t.Errorf("Authorization = %q, want the configured key", server.authorization)
	}
	if server.model != "detector-model" {
		t.Errorf("body model = %q, want detector-model", server.model)
	}
	if !strings.Contains(server.content, "Detect the language of the following text") {
		t.Errorf("the arbiter prompt changed: %q", server.content)
	}
	if !strings.Contains(server.content, text) {
		t.Error("the arbiter prompt no longer carries the text sample")
	}
}

// Behind a keyless gateway the arbiter request carries no Authorization
// header and still works.
func TestDetectWithOpenAIKeylessGateway(t *testing.T) {
	server := newRecordingLLMServer(t, "ru", 0)

	detector := NewLanguageDetectorWithClient(true, 2*time.Second, llm.NewClient(config.LLMConfig{
		BaseURL: server.URL,
	}))

	text := "Это явно русский текст, достаточный для передачи в арбитр."
	if got := detector.detectWithOpenAI(text); got != "ru" {
		t.Fatalf("detectWithOpenAI() = %q, want ru", got)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.authorization != "" {
		t.Errorf("Authorization = %q, want no header", server.authorization)
	}
}

// Not configured — or no client wired — means no request at all, exactly as a
// missing key did before.
func TestDetectWithOpenAISkipsWhenNotConfigured(t *testing.T) {
	server := newRecordingLLMServer(t, "en", 0)

	for _, tc := range []struct {
		name     string
		detector *LanguageDetector
	}{
		{name: "unconfigured client", detector: NewLanguageDetectorWithClient(true, 2*time.Second, llm.NewClient(config.LLMConfig{}))},
		{name: "nil client", detector: NewLanguageDetectorWithClient(true, 2*time.Second, nil)},
		{
			name:     "arbiter disabled",
			detector: NewLanguageDetectorWithClient(false, 2*time.Second, llm.NewClient(config.LLMConfig{BaseURL: server.URL})),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.detector.detectWithOpenAI("some text for the arbiter"); got != "" {
				t.Errorf("detectWithOpenAI() = %q, want empty", got)
			}
		})
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.hits != 0 {
		t.Errorf("endpoint saw %d requests, want none", server.hits)
	}
}

// The detector's own (shorter) deadline bounds the arbiter request on top of
// the shared client's timeout.
func TestDetectWithOpenAIRespectsDetectorDeadline(t *testing.T) {
	server := newRecordingLLMServer(t, "en", 200*time.Millisecond)

	detector := NewLanguageDetectorWithClient(true, 30*time.Millisecond, llm.NewClient(config.LLMConfig{
		BaseURL: server.URL,
	}))

	if got := detector.detectWithOpenAI("text that will not be answered in time"); got != "" {
		t.Errorf("detectWithOpenAI() = %q, want empty on deadline", got)
	}
}

// End to end: tag and lingua disagree, the armed arbiter answers through the
// configured endpoint, and the detection result carries the OpenAI method.
func TestDetectLanguageUsesLLMArbiter(t *testing.T) {
	server := newRecordingLLMServer(t, "en", 0)

	detector := NewLanguageDetectorWithClient(true, 2*time.Second, llm.NewClient(config.LLMConfig{
		BaseURL: server.URL,
	}))

	englishText := `
	This is a story about a young man's adventures in America.
	He traveled across different cities and met interesting people.
	The story begins in New York, where the main character worked as a programmer.
	Every day he went to work and dreamed about traveling the world.
	`

	result := detector.DetectLanguage("ru", englishText)
	if result.Language != "en" {
		t.Errorf("Language = %q, want en (the arbiter's answer)", result.Language)
	}
	if result.Method != MethodOpenAI {
		t.Errorf("Method = %q, want %q", result.Method, MethodOpenAI)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.hits != 1 {
		t.Errorf("endpoint saw %d requests, want 1", server.hits)
	}
}
