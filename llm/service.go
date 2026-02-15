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
	"unicode"
	"unicode/utf8"
)

// GetModel returns the OpenAI model from OPENAI_MODEL env, defaulting to gpt-4o-mini.
func GetModel() string {
	if m := os.Getenv("OPENAI_MODEL"); m != "" {
		return m
	}
	return "gpt-4o-mini"
}

// LLMService handles interaction with OpenAI API
type LLMService struct {
	apiKey     string
	httpClient *http.Client
}

// Command represents a parsed command from LLM response
type Command struct {
	Command string `json:"command"`
	Title   string `json:"title,omitempty"`
	Author  string `json:"author,omitempty"`
	// New field for combined search
	SearchType string `json:"search_type,omitempty"` // "title_only", "author_only", "combined"
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
	promptTemplate = `You are a library assistant bot that helps find books and authors in multiple languages. Parse the user query and conversation context to return a JSON object with the command and parameters.

Supported commands:
- find_book: search books by title only
- find_author: search authors by name only  
- find_book_with_author: search books with both title and author specified
- unknown: for unrelated queries

IMPORTANT PARSING RULES:
1. Handle multiple languages: Russian, English, and others
2. NORMALIZE AUTHOR NAMES: Always return author names in their standard nominative form
3. For Russian names, convert genitive/accusative forms to nominative (Толстого → Толстой, Достоевского → Достоевский)
4. For English names, remove possessive forms (Tolkien's → Tolkien, Rowling's → Rowling)
5. Common author patterns: surname only, surname + first name, surname + patronymic
6. For combined searches, typically the last word(s) are the author's surname
7. Recognize famous international author-book pairs

Examples:
Russian literature:
- "найди книгу Война и мир" -> {"command": "find_book", "title": "Война и мир", "search_type": "title_only"}
- "книги Толстого" -> {"command": "find_author", "author": "Толстой", "search_type": "author_only"}
- "найди буратино толстого" -> {"command": "find_book_with_author", "title": "буратино", "author": "Толстой", "search_type": "combined"}
- "золотой ключик толстой" -> {"command": "find_book_with_author", "title": "золотой ключик", "author": "Толстой", "search_type": "combined"}

English literature:
- "find Harry Potter Rowling" -> {"command": "find_book_with_author", "title": "Harry Potter", "author": "Rowling", "search_type": "combined"}
- "Lord of the Rings Tolkien's" -> {"command": "find_book_with_author", "title": "Lord of the Rings", "author": "Tolkien", "search_type": "combined"}
- "books by Shakespeare" -> {"command": "find_author", "author": "Shakespeare", "search_type": "author_only"}
- "find book Pride and Prejudice" -> {"command": "find_book", "title": "Pride and Prejudice", "search_type": "title_only"}

Mixed/Other:
- "незнайка носова" -> {"command": "find_book_with_author", "title": "незнайка", "author": "Носов", "search_type": "combined"}
- "1984 Orwell" -> {"command": "find_book_with_author", "title": "1984", "author": "Orwell", "search_type": "combined"}
- "достоевского книги" -> {"command": "find_author", "author": "Достоевский", "search_type": "author_only"}
- "что такое погода" -> {"command": "unknown"}

AUTHOR NAME NORMALIZATION EXAMPLES:
- Толстого/толстого → Толстой
- Достоевского/достоевского → Достоевский  
- Пушкина/пушкина → Пушкин
- Чехова/чехова → Чехов
- Носова/носова → Носов
- Tolkien's → Tolkien
- Rowling's → Rowling
- Shakespeare's → Shakespeare

For combined searches, recognize these patterns:
- Russian surnames: Толстой, Достоевский, Пушкин, Гоголь, Чехов, Носов, Барто, Чуковский
- English surnames: Shakespeare, Tolkien, Rowling, Dickens, Austen, Hemingway, Fitzgerald
- International surnames: Dumas, Verne, Cervantes, Dante, Hugo, Kafka, Borges
- Author name usually comes last in queries
- Always normalize to standard form (nominative case for Russian, no possessive for English)

Conversation context: {{context}}

User query: {{query}}

Return format:
{
  "command": "find_book" | "find_author" | "find_book_with_author" | "unknown",
  "title": string (optional for find_book or find_book_with_author),
  "author": string (optional for find_author or find_book_with_author, NORMALIZED FORM),
  "search_type": "title_only" | "author_only" | "combined"
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
		Model: GetModel(),
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

	logging.Infof("LLM processed query '%s' -> command: %s, title: %s, author: %s", userQuery, command.Command, command.Title, command.Author)
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
		command.Title = strings.ToLower(command.Title)
		if command.Title == "" {
			// If no title provided for find_book, treat as unknown
			return &Command{Command: "unknown"}
		}
		command.SearchType = "title_only"
	case "find_author":
		// Just trim whitespace - LLM handles normalization
		command.Author = strings.TrimSpace(command.Author)
		if command.Author == "" {
			// If no author provided for find_author, treat as unknown
			return &Command{Command: "unknown"}
		}
		command.SearchType = "author_only"
	case "find_book_with_author":
		// Normalize title and trim author - LLM handles author normalization
		command.Title = strings.TrimSpace(command.Title)
		command.Title = strings.ToLower(command.Title)
		command.Author = strings.TrimSpace(command.Author)
		if command.Title == "" && command.Author == "" {
			// If both title and author are empty, treat as unknown
			return &Command{Command: "unknown"}
		}
		command.SearchType = "combined"
	case "unknown":
		// Clear title and author for unknown commands
		command.Title = ""
		command.Author = ""
	default:
		// Any unrecognized command becomes unknown
		return &Command{Command: "unknown"}
	}

	return command
}

// capitalizeFirst upper-cases the first rune of s (works for cyrillic).
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// GenreBookContext holds minimal book info passed to the LLM for better genre naming.
type GenreBookContext struct {
	Title      string
	Authors    string
	Annotation string
}

// GenerateGenreTitle asks OpenAI to produce a human-readable title for a genre tag.
// Returns the genre tag itself if OpenAI is unavailable.
func (s *LLMService) GenerateGenreTitle(genreTag string) string {
	return s.GenerateGenreTitleWithBooks(genreTag, nil)
}

// GenerateGenreTitleWithBooks asks OpenAI to produce a human-readable title for a genre tag,
// using sample books as additional context for better accuracy.
func (s *LLMService) GenerateGenreTitleWithBooks(genreTag string, books []GenreBookContext) string {
	if s.apiKey == "" {
		return genreTag
	}

	var booksContext string
	if len(books) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\nHere are some books in this genre for context:\n")
		for i, b := range books {
			sb.WriteString(fmt.Sprintf("%d. \"%s\"", i+1, b.Title))
			if b.Authors != "" {
				sb.WriteString(fmt.Sprintf(" — %s", b.Authors))
			}
			if b.Annotation != "" {
				sb.WriteString(fmt.Sprintf("\n   %s", b.Annotation))
			}
			sb.WriteString("\n")
		}
		booksContext = sb.String()
	}

	request := OpenAIRequest{
		Model: GetModel(),
		Messages: []Message{
			{
				Role: "user",
				Content: fmt.Sprintf(
					"You are a librarian. Given the machine-readable book genre tag \"%s\", "+
						"provide a short human-readable genre name in Russian. "+
						"Reply with just the genre name, nothing else. No quotes, no punctuation, no explanation.%s",
					genreTag, booksContext),
			},
		},
	}

	response, err := s.callOpenAI(request)
	if err != nil {
		logging.Warnf("Failed to generate genre title for %q: %v", genreTag, err)
		return genreTag
	}

	if len(response.Choices) == 0 {
		return genreTag
	}

	title := strings.TrimSpace(response.Choices[0].Message.Content)
	title = strings.Trim(title, "\"'`")
	if title == "" {
		return genreTag
	}

	title = capitalizeFirst(title)
	logging.Infof("Generated genre title: %q -> %q", genreTag, title)
	return title
}

// GenerateGenreTitleUnique retries genre title generation, explicitly excluding conflicting titles.
func (s *LLMService) GenerateGenreTitleUnique(genreTag string, books []GenreBookContext, excluded []string) string {
	if s.apiKey == "" {
		return genreTag
	}

	var booksContext string
	if len(books) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\nHere are some books in this genre for context:\n")
		for i, b := range books {
			sb.WriteString(fmt.Sprintf("%d. \"%s\"", i+1, b.Title))
			if b.Authors != "" {
				sb.WriteString(fmt.Sprintf(" — %s", b.Authors))
			}
			if b.Annotation != "" {
				sb.WriteString(fmt.Sprintf("\n   %s", b.Annotation))
			}
			sb.WriteString("\n")
		}
		booksContext = sb.String()
	}

	excludeList := strings.Join(excluded, "\", \"")

	request := OpenAIRequest{
		Model: GetModel(),
		Messages: []Message{
			{
				Role: "user",
				Content: fmt.Sprintf(
					"You are a librarian. Given the machine-readable book genre tag \"%s\", "+
						"provide a short human-readable genre name in Russian. "+
						"The following names are already taken by other genres: \"%s\". You MUST suggest a different name. "+
						"Reply with just the genre name, nothing else. No quotes, no punctuation, no explanation.%s",
					genreTag, excludeList, booksContext),
			},
		},
	}

	response, err := s.callOpenAI(request)
	if err != nil {
		logging.Warnf("Failed to generate unique genre title for %q: %v", genreTag, err)
		return genreTag
	}

	if len(response.Choices) == 0 {
		return genreTag
	}

	title := strings.TrimSpace(response.Choices[0].Message.Content)
	title = strings.Trim(title, "\"'`")
	if title == "" {
		return genreTag
	}
	for _, ex := range excluded {
		if title == ex {
			return genreTag
		}
	}

	title = capitalizeFirst(title)
	logging.Infof("Generated unique genre title: %q -> %q (excluded %v)", genreTag, title, excluded)
	return title
}
