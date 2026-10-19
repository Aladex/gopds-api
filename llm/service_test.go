package llm

import (
	"strings"
	"testing"
	"time"

	"gopds-api/config"
)

// The service is the feature layer over the shared client: its callers (genre
// titles, curated-collection matching) must reach the
// configured endpoint through that client with their prompts untouched, and
// degrade exactly as before when the provider is not configured.

// Every optional feature returns its no-LLM value when unconfigured: the
// genre tag itself, no match and no suggestion.
func TestOptionalFeaturesDegradeWhenUnconfigured(t *testing.T) {
	svc := NewLLMServiceWithClient(NewClient(config.LLMConfig{}))

	if title := svc.GenerateGenreTitle("sf_history"); title != "sf_history" {
		t.Errorf("GenerateGenreTitle() = %q, want the tag itself", title)
	}
	if title := svc.GenerateGenreTitleUnique("sf_history", nil, nil); title != "sf_history" {
		t.Errorf("GenerateGenreTitleUnique() = %q, want the tag itself", title)
	}
	if id, err := svc.ResolveAmbiguousMatch("t", "a", []AmbiguousCandidate{{BookID: 1, Title: "t", Authors: "a"}}); err != nil || id != nil {
		t.Errorf("ResolveAmbiguousMatch() = (%v, %v), want (nil, nil)", id, err)
	}
	if q, err := svc.SuggestAlternativeQuery("t", "a"); err != nil || q != nil {
		t.Errorf("SuggestAlternativeQuery() = (%v, %v), want (nil, nil)", q, err)
	}
}

// ResolveAmbiguousMatch goes through the same endpoint and validates the model
// picked one of the proposed candidates.
func TestResolveAmbiguousMatchGoesThroughConfiguredEndpoint(t *testing.T) {
	endpoint := &llmEndpoint{}
	srv := endpoint.server(t, chatCompletionBody(`{"book_id": 7}`), 200)

	svc := NewLLMServiceWithClient(NewClient(config.LLMConfig{BaseURL: srv.URL, APIKey: "svc-key", Timeout: 2 * time.Second}))

	id, err := svc.ResolveAmbiguousMatch("Скотное хозяйство", "Джордж Оруэлл", []AmbiguousCandidate{
		{BookID: 3, Title: "Другая книга", Authors: "Другой автор"},
		{BookID: 7, Title: "Скотный двор", Authors: "Джордж Оруэлл"},
	})
	if err != nil {
		t.Fatalf("ResolveAmbiguousMatch() = %v, want nil", err)
	}
	if id == nil || *id != 7 {
		t.Fatalf("ResolveAmbiguousMatch() = %v, want 7", id)
	}

	got := endpoint.last(t)
	if got.Authorization != "Bearer svc-key" {
		t.Errorf("Authorization = %q, want the configured key", got.Authorization)
	}
	messages, _ := got.Body["messages"].([]any)
	first, _ := messages[0].(map[string]any)
	if content, _ := first["content"].(string); !strings.Contains(content, "Скотное хозяйство") {
		t.Error("the match prompt no longer carries the external title")
	}
}
