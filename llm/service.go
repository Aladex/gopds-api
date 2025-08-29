package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopds-api/logging"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// LLMService handles interaction with OpenAI API
type LLMService struct {
	apiKey     string
	httpClient *http.Client
}

// Command represents a parsed command from LLM response
type Command struct {
	Command string `json:"command"`
	Title   string `json:"title,omitempty"`
}

// OpenAIRequest represents the request structure for OpenAI API
type OpenAIRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// Message represents a message in the OpenAI request
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIResponse represents the response structure from OpenAI API
type OpenAIResponse struct {
	Choices []Choice  `json:"choices"`
	Error   *APIError `json:"error,omitempty"`
}

// Choice represents a choice in OpenAI response
type Choice struct {
	Message Message `json:"message"`
}

// APIError represents an error from OpenAI API
type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

const (
	openAIAPIURL   = "https://api.openai.com/v1/chat/completions"
	promptTemplate = `You are a library assistant bot. Parse the user query and conversation context to return a JSON object with the command and parameters. Supported commands for now: find_book (search books by title), unknown (for unrelated queries).

If the query is unrelated to book search, return {"command": "unknown"}.

Conversation context: {{context}}

User query: {{query}}

Return format:
{
  "command": "find_book" | "unknown",
  "title": string (optional for find_book)
}`
)

// NewLLMService creates a new LLM service instance
func NewLLMService() *LLMService {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		logging.Errorf("OPENAI_API_KEY environment variable is not set")
	}

	return &LLMService{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ProcessQuery processes user query with conversation context and returns a command
func (s *LLMService) ProcessQuery(userQuery, context string) (*Command, error) {
	if s.apiKey == "" {
		logging.Errorf("OpenAI API key is not configured")
		return &Command{Command: "unknown"}, nil
	}

	// Build the prompt by replacing placeholders
	prompt := strings.ReplaceAll(promptTemplate, "{{context}}", context)
	prompt = strings.ReplaceAll(prompt, "{{query}}", userQuery)

	// Create OpenAI request
	request := OpenAIRequest{
		Model: "gpt-4o-mini",
		Messages: []Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	// Send request to OpenAI API
	response, err := s.callOpenAI(request)
	if err != nil {
		logging.Errorf("Failed to call OpenAI API: %v", err)
		return &Command{Command: "unknown"}, nil
	}

	// Parse the response into a command
	command, err := s.parseResponse(response)
	if err != nil {
		logging.Errorf("Failed to parse OpenAI response: %v", err)
		return &Command{Command: "unknown"}, nil
	}

	logging.Infof("LLM processed query '%s' -> command: %s, title: %s", userQuery, command.Command, command.Title)
	return command, nil
}

// callOpenAI makes a request to OpenAI API
func (s *LLMService) callOpenAI(request OpenAIRequest) (*OpenAIResponse, error) {
	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", openAIAPIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(body))
	}

	var response OpenAIResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("OpenAI API error: %s", response.Error.Message)
	}

	return &response, nil
}

// parseResponse parses OpenAI response into a Command struct
func (s *LLMService) parseResponse(response *OpenAIResponse) (*Command, error) {
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	content := response.Choices[0].Message.Content
	content = strings.TrimSpace(content)

	// Try to extract JSON from the response
	var command Command

	// First, try to parse the entire content as JSON
	if err := json.Unmarshal([]byte(content), &command); err == nil {
		return s.validateCommand(&command), nil
	}

	// If that fails, try to find JSON within the content
	startIdx := strings.Index(content, "{")
	endIdx := strings.LastIndex(content, "}")

	if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
		jsonContent := content[startIdx : endIdx+1]
		if err := json.Unmarshal([]byte(jsonContent), &command); err == nil {
			return s.validateCommand(&command), nil
		}
	}

	// If JSON parsing fails, log the content and return unknown command
	logging.Errorf("Failed to parse LLM response as JSON: %s", content)
	return &Command{Command: "unknown"}, nil
}

// validateCommand validates and normalizes the command
func (s *LLMService) validateCommand(command *Command) *Command {
	// Normalize command to lowercase
	command.Command = strings.ToLower(strings.TrimSpace(command.Command))

	// Validate command type
	switch command.Command {
	case "find_book":
		// Normalize title
		command.Title = strings.TrimSpace(command.Title)
		if command.Title == "" {
			// If no title provided for find_book, treat as unknown
			return &Command{Command: "unknown"}
		}
	case "unknown":
		// Clear title for unknown commands
		command.Title = ""
	default:
		// Any unrecognized command becomes unknown
		return &Command{Command: "unknown"}
	}

	return command
}
