package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gopds-api/config"
)

// chatCompletionsPath is appended to the configured base URL.
const chatCompletionsPath = "/chat/completions"

// messageRoleUser is the only role the features send.
const messageRoleUser = "user"

// Client is the one OpenAI-compatible chat-completions transport every
// model-backed feature shares: search query parsing, genre titles,
// curated-collection matching and language detection. It carries the provider
// settings of the llm configuration section — endpoint, key, model, timeout —
// so the whole application talks to exactly one configured endpoint with one
// optional key.
//
// Errors never carry request or response content: prompts hold the readers'
// words, and a gateway can echo parts of a rejected request back, so only the
// status and the API's own error type and code are ever returned — which is
// also all a caller's log can then show.
type Client struct {
	cfg        config.LLMConfig
	httpClient *http.Client
	// structuredHTTP serves CompleteStructured: no client-wide timeout, each
	// structured request carries its own.
	structuredHTTP *http.Client
}

// NewClient builds the shared client from the llm configuration section. A
// zero or negative timeout leaves requests bounded only by their context.
func NewClient(cfg config.LLMConfig) *Client {
	if cfg.Model == "" {
		cfg.Model = config.LLMDefaultModel
	}
	return &Client{
		cfg:            cfg,
		httpClient:     &http.Client{Timeout: cfg.Timeout},
		structuredHTTP: &http.Client{},
	}
}

// NewClientFromConfig builds the shared client from the application's loaded
// llm section — the same resolution llm.NewLLMService uses.
func NewClientFromConfig() *Client {
	return NewClient(config.LLM())
}

// Configured reports whether the provider settings allow requests at all; see
// config.LLMConfig.Configured for the rule.
func (c *Client) Configured() bool {
	return c.cfg.Configured()
}

// Model returns the chat model requests ask for.
func (c *Client) Model() string {
	return c.cfg.Model
}

// endpoint joins the configured base URL with the chat-completions path.
func (c *Client) endpoint() string {
	return strings.TrimSuffix(c.cfg.BaseURL, "/") + chatCompletionsPath
}

// chatRequest is the wire body of one chat completion request.
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// ChatCompletion sends one chat completion request. The Authorization header
// is sent only when a key is configured; a keyless gateway is a valid
// provider. The request is bounded by ctx and, when the section set one, by
// the client's own timeout.
func (c *Client) ChatCompletion(ctx context.Context, messages []Message) (*OpenAIResponse, error) {
	payload, err := json.Marshal(chatRequest{Model: c.cfg.Model, Messages: messages})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	endpoint := c.endpoint()

	// The URL is operator configuration (llm.base_url), never request input.
	// #nosec:G107 -- the endpoint comes from operator configuration
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send LLM request: %w", err)
	}
	defer resp.Body.Close()

	// The response body is content the model produced or the gateway echoed;
	// it stays out of the error for the reason the type comment gives.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API returned status %d", resp.StatusCode)
	}

	var response OpenAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode LLM response: %w", err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("LLM API error: type=%s code=%s", response.Error.Type, response.Error.Code)
	}
	return &response, nil
}
