package llm

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"gopds-api/config"
)

// Success-path routing for the remaining feature methods and the URL join:
// each one must answer from the configured endpoint's reply — proving the
// request left through the shared client — and a trailing slash in the base
// URL must still land on /chat/completions.

func TestGenerateGenreTitleRoutesThroughConfiguredEndpoint(t *testing.T) {
	endpoint := &llmEndpoint{}
	srv := endpoint.server(t, chatCompletionBody("Детектив"), http.StatusOK)

	svc := NewLLMServiceWithClient(NewClient(config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second}))

	if got := svc.GenerateGenreTitle("detective_tag"); got != "Детектив" {
		t.Fatalf("GenerateGenreTitle() = %q, want the model's title", got)
	}

	got := endpoint.last(t)
	if got.Path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", got.Path)
	}
	if got.Authorization != "" {
		t.Errorf("Authorization = %q, want none: the endpoint is a keyless gateway", got.Authorization)
	}
	first := lastMessageContent(t, got)
	if !strings.Contains(first, "detective_tag") {
		t.Errorf("the genre prompt no longer carries the tag")
	}
}

func TestGenerateGenreTitleUniqueRoutesThroughConfiguredEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reply      string
		excluded   []string
		wantReturn string
	}{
		{name: "model picks a free name", reply: "Свободное имя", excluded: []string{"Занятое имя"}, wantReturn: "Свободное имя"},
		// A reply that collides with a taken name is refused: the tag itself
		// comes back rather than a duplicate title.
		{name: "model picks a taken name", reply: "Занятое имя", excluded: []string{"Занятое имя"}, wantReturn: "detective_tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := &llmEndpoint{}
			srv := endpoint.server(t, chatCompletionBody(tc.reply), http.StatusOK)

			svc := NewLLMServiceWithClient(NewClient(config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second}))

			if got := svc.GenerateGenreTitleUnique("detective_tag", nil, tc.excluded); got != tc.wantReturn {
				t.Fatalf("GenerateGenreTitleUnique() = %q, want %q", got, tc.wantReturn)
			}

			if endpoint.count() != 1 {
				t.Fatalf("endpoint saw %d requests, want 1", endpoint.count())
			}
			first := lastMessageContent(t, endpoint.last(t))
			if !strings.Contains(first, "detective_tag") || !strings.Contains(first, tc.excluded[0]) {
				t.Errorf("the unique-genre prompt no longer carries the tag and the taken names")
			}
		})
	}
}

func TestSuggestAlternativeQueryRoutesThroughConfiguredEndpoint(t *testing.T) {
	endpoint := &llmEndpoint{}
	srv := endpoint.server(t, chatCompletionBody(`{"title": "Скотный двор", "author": "Джордж Оруэлл"}`), http.StatusOK)

	svc := NewLLMServiceWithClient(NewClient(config.LLMConfig{BaseURL: srv.URL, APIKey: "suggest-key", Timeout: 2 * time.Second}))

	suggestion, err := svc.SuggestAlternativeQuery("Скотское хозяйство", "Джорж Оруэлл")
	if err != nil {
		t.Fatalf("SuggestAlternativeQuery() = %v, want nil", err)
	}
	if suggestion == nil || suggestion.Title != "Скотный двор" || suggestion.Author != "Джордж Оруэлл" {
		t.Fatalf("SuggestAlternativeQuery() = %+v, want the model's title and author", suggestion)
	}

	got := endpoint.last(t)
	if got.Path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", got.Path)
	}
	if got.Authorization != "Bearer suggest-key" {
		t.Errorf("Authorization = %q, want the configured key", got.Authorization)
	}
	first := lastMessageContent(t, got)
	if !strings.Contains(first, "Скотское хозяйство") {
		t.Errorf("the suggestion prompt no longer carries the external title")
	}
}

// A base URL with a trailing slash is the easy way to write a gateway
// address; it must join to exactly one /chat/completions.
func TestClientJoinsTrailingSlashBaseURL(t *testing.T) {
	endpoint := &llmEndpoint{}
	srv := endpoint.server(t, chatCompletionBody("ok"), http.StatusOK)

	client := NewClient(config.LLMConfig{BaseURL: srv.URL + "/v1/", Timeout: 2 * time.Second})

	if _, err := client.ChatCompletion(t.Context(), []Message{{Role: messageRoleUser, Content: "prompt"}}); err != nil {
		t.Fatalf("ChatCompletion() = %v, want nil", err)
	}
	if got := endpoint.last(t).Path; got != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", got)
	}
}

// lastMessageContent pulls the single user message's content out of a
// recorded request body.
func lastMessageContent(t *testing.T, req recordedRequest) string {
	t.Helper()
	messages, _ := req.Body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("body messages = %v, want one message", req.Body["messages"])
	}
	first, _ := messages[0].(map[string]any)
	content, _ := first["content"].(string)
	return content
}
