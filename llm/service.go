package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/logging"
)

// Message represents a message in the LLM request
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIResponse represents the response structure from the LLM API
type OpenAIResponse struct {
	Choices []Choice  `json:"choices"`
	Error   *APIError `json:"error,omitempty"`
}

// Choice represents a choice in the LLM response
type Choice struct {
	Message Message `json:"message"`
}

// APIError represents an error from the LLM API
type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// LLMService is the feature layer over the shared LLM client: genre titles,
// curated-collection matching and alternative queries. It sends
// no HTTP of its own — every request goes through llm.Client and the llm
// configuration section.
type LLMService struct {
	client *Client
}

// NewLLMService creates a new LLM service from the loaded llm configuration
// section. When that section leaves the provider unconfigured, every method
// degrades to its no-LLM result.
func NewLLMService() *LLMService {
	return NewLLMServiceWithClient(NewClientFromConfig())
}

// NewLLMServiceWithClient wires the service onto an explicit client — the seam
// that points the service at a stub endpoint in tests.
func NewLLMServiceWithClient(client *Client) *LLMService {
	if !client.Configured() {
		logging.Error("LLM is not configured (llm.base_url with llm.api_key, or a non-OpenAI base URL); LLM features stay off")
	}
	return &LLMService{client: client}
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

// GenerateGenreTitle asks the LLM to produce a human-readable title for a genre tag.
// Returns the genre tag itself if OpenAI is unavailable.
func (s *LLMService) GenerateGenreTitle(genreTag string) string {
	return s.GenerateGenreTitleWithBooks(genreTag, nil)
}

// GenerateGenreTitleWithBooks asks the LLM to produce a human-readable title for a genre tag,
// using sample books as additional context for better accuracy.
func (s *LLMService) GenerateGenreTitleWithBooks(genreTag string, books []GenreBookContext) string {
	if !s.client.Configured() {
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

	messages := []Message{
		{
			Role: messageRoleUser,
			Content: fmt.Sprintf(
				"You are a librarian. Given the machine-readable book genre tag \"%s\", "+
					"provide a short human-readable genre name in Russian. "+
					"Reply with just the genre name, nothing else. No quotes, no punctuation, no explanation.%s",
				genreTag, booksContext),
		},
	}

	response, err := s.client.ChatCompletion(context.Background(), messages)
	if err != nil {
		// The tag is catalog content; the log carries its length instead.
		logging.Warnf("Failed to generate genre title (tag %d runes): %v", utf8.RuneCountInString(genreTag), err)
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
	// The tag and the model's title are catalog content; only their lengths
	// reach the log.
	logging.Infof("Generated genre title: tag %d runes -> title %d runes",
		utf8.RuneCountInString(genreTag), utf8.RuneCountInString(title))
	return title
}

// AmbiguousCandidate is one local-library book that the matcher proposed for an
// ambiguous curated-collection item. Used by ResolveAmbiguousMatch.
type AmbiguousCandidate struct {
	BookID     int64
	Title      string
	Authors    string
	Annotation string
}

// ResolveAmbiguousMatch asks the model to pick the best local book among
// candidates for one external (title, author) pair. Returns nil if the model
// cannot confidently pick or the API is unavailable.
func (s *LLMService) ResolveAmbiguousMatch(externalTitle, externalAuthor string, candidates []AmbiguousCandidate) (*int64, error) {
	if !s.client.Configured() || len(candidates) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	sb.WriteString("You are a librarian matching an external book listing to one local book in the library.\n")
	sb.WriteString("External record:\n")
	sb.WriteString(fmt.Sprintf("  title: %q\n", externalTitle))
	sb.WriteString(fmt.Sprintf("  author: %q\n", externalAuthor))
	sb.WriteString("\nCandidates already retrieved from the local catalog:\n")
	for _, c := range candidates {
		sb.WriteString(fmt.Sprintf("- id=%d | title=%q | author=%q\n", c.BookID, c.Title, c.Authors))
		if a := strings.TrimSpace(c.Annotation); a != "" {
			// Trim long annotations: a couple of paragraphs worth is plenty for
			// disambiguation and keeps the prompt cheap.
			if len([]rune(a)) > 600 {
				a = string([]rune(a)[:600]) + "…"
			}
			sb.WriteString(fmt.Sprintf("  annotation: %s\n", a))
		}
	}
	sb.WriteString(`
Pick the candidate that most likely refers to the SAME work as the external record.
Match across translations, alternative titles and transliteration. Be conservative —
if no candidate is plausibly the same work, answer null.
Reply with a single line of strict JSON, no comments, no markdown:
{"book_id": <id from list> | null}`)

	response, err := s.client.ChatCompletion(context.Background(), []Message{
		{Role: messageRoleUser, Content: sb.String()},
	})
	if err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, nil
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	startIdx := strings.Index(content, "{")
	endIdx := strings.LastIndex(content, "}")
	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return nil, nil
	}
	var payload struct {
		BookID *int64 `json:"book_id"`
	}
	if err := json.Unmarshal([]byte(content[startIdx:endIdx+1]), &payload); err != nil {
		return nil, nil
	}
	if payload.BookID == nil {
		return nil, nil
	}
	// Validate: result must be one of the candidate ids.
	for _, c := range candidates {
		if c.BookID == *payload.BookID {
			return payload.BookID, nil
		}
	}
	return nil, nil
}

// SuggestedQuery is one alternative title/author pair returned by
// SuggestAlternativeQuery. Either field may be empty if the model is unsure.
type SuggestedQuery struct {
	Title  string
	Author string
}

// SuggestAlternativeQuery asks the model to propose a more search-friendly
// title/author pair when the matcher could not find anything for the original
// external record. Returns a single suggestion (the model's best guess).
//
// Typical use: external "Скотское хозяйство" by "Джорж Оруэлл" → suggestion
// {"Скотный двор", "Джордж Оруэлл"}.
func (s *LLMService) SuggestAlternativeQuery(externalTitle, externalAuthor string) (*SuggestedQuery, error) {
	if !s.client.Configured() {
		return nil, nil
	}
	prompt := fmt.Sprintf(`You are a librarian. A book listing was found in an external catalog
that my local Russian-language library cannot match. Suggest the most likely
alternative title and author so the catalog has a better chance of matching it.
The replacement could be the canonical Russian translation, the original-language
title, an alternative transliteration of the author's name, or a clarified
multi-volume short title.

External record:
  title: %q
  author: %q

Rules:
- Prefer the form an average Russian-language library uses.
- If the input is already canonical, return the same values unchanged.
- Output exactly one suggestion.

Reply with strict JSON, no markdown, no commentary:
{"title": "...", "author": "..."}`, externalTitle, externalAuthor)

	response, err := s.client.ChatCompletion(context.Background(), []Message{
		{Role: messageRoleUser, Content: prompt},
	})
	if err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, nil
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	startIdx := strings.Index(content, "{")
	endIdx := strings.LastIndex(content, "}")
	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return nil, nil
	}
	var payload struct {
		Title  string `json:"title"`
		Author string `json:"author"`
	}
	if err := json.Unmarshal([]byte(content[startIdx:endIdx+1]), &payload); err != nil {
		return nil, nil
	}
	payload.Title = strings.TrimSpace(payload.Title)
	payload.Author = strings.TrimSpace(payload.Author)
	if payload.Title == "" && payload.Author == "" {
		return nil, nil
	}
	return &SuggestedQuery{Title: payload.Title, Author: payload.Author}, nil
}

// GenerateGenreTitleUnique retries genre title generation, explicitly excluding conflicting titles.
func (s *LLMService) GenerateGenreTitleUnique(genreTag string, books []GenreBookContext, excluded []string) string {
	if !s.client.Configured() {
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

	messages := []Message{
		{
			Role: messageRoleUser,
			Content: fmt.Sprintf(
				"You are a librarian. Given the machine-readable book genre tag \"%s\", "+
					"provide a short human-readable genre name in Russian. "+
					"The following names are already taken by other genres: \"%s\". You MUST suggest a different name. "+
					"Reply with just the genre name, nothing else. No quotes, no punctuation, no explanation.%s",
				genreTag, excludeList, booksContext),
		},
	}

	response, err := s.client.ChatCompletion(context.Background(), messages)
	if err != nil {
		// The tag is catalog content; the log carries its length instead.
		logging.Warnf("Failed to generate unique genre title (tag %d runes): %v", utf8.RuneCountInString(genreTag), err)
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
	// The tag, the model's title and the taken names are catalog content; the
	// log carries lengths and the taken-name count instead.
	logging.Infof("Generated unique genre title: tag %d runes -> title %d runes, %d taken names excluded",
		utf8.RuneCountInString(genreTag), utf8.RuneCountInString(title), len(excluded))
	return title
}
