package telegram

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"gopds-api/commands"
	"gopds-api/logging"
	"strings"
	"time"

	"github.com/go-redis/redis"
	tele "gopkg.in/telebot.v3"
)

const (
	// MaxContextLength Maximum context size in characters
	MaxContextLength = 4096
	// ConversationKeyPrefix Prefix for Redis keys
	ConversationKeyPrefix = "telegram_conversation:"
	// UserStateKeyPrefix Prefix for user state Redis keys
	UserStateKeyPrefix = "telegram_user_state:"
	// ConversationTTL TTL for context (7 days)
	ConversationTTL = 7 * 24 * time.Hour
	// UserStateTTL TTL for user state (1 hour)
	UserStateTTL = 1 * time.Hour
)

// ConversationManager manages the conversation context with users
type ConversationManager struct {
	redisClient *redis.Client
}

// ConversationContext represents the conversation context with a user
type ConversationContext struct {
	UserID    int64     `json:"user_id"`
	TokenHash string    `json:"token_hash"` // Hash of bot token for identification (not the actual token)
	Messages  []Message `json:"messages"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Pagination state for book search
	SearchParams *commands.SearchParams `json:"search_params,omitempty"`
	// Selected book ID for future operations
	SelectedBookID int64 `json:"selected_book_id,omitempty"`
}

// Message represents a message in the context
type Message struct {
	Type      string    `json:"type"` // "user" or "bot"
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// NewConversationManager creates a new conversation manager
func NewConversationManager(redisClient *redis.Client) *ConversationManager {
	return &ConversationManager{
		redisClient: redisClient,
	}
}

// GetContext retrieves the conversation context for a user
func (cm *ConversationManager) GetContext(botToken string, userID int64) (*ConversationContext, error) {
	key := cm.getRedisKey(botToken, userID)

	data, err := cm.redisClient.Get(key).Result()
	if err == redis.Nil {
		// Context not found, create a new one
		return &ConversationContext{
			UserID:    userID,
			TokenHash: hashToken(botToken),
			Messages:  make([]Message, 0),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to get context from Redis: %v", err)
	}

	var context ConversationContext
	if err := json.Unmarshal([]byte(data), &context); err != nil {
		return nil, fmt.Errorf("failed to unmarshal context: %v", err)
	}

	return &context, nil
}

// AddUserMessage adds a user message to the context
func (cm *ConversationManager) AddUserMessage(botToken string, userID int64, text string) error {
	return cm.addMessage(botToken, userID, "user", text)
}

// AddBotMessage adds a bot message to the context
func (cm *ConversationManager) AddBotMessage(botToken string, userID int64, text string) error {
	return cm.addMessage(botToken, userID, "bot", text)
}

// addMessage adds a message to the context
func (cm *ConversationManager) addMessage(botToken string, userID int64, messageType, text string) error {
	context, err := cm.GetContext(botToken, userID)
	if err != nil {
		return fmt.Errorf("failed to get context: %v", err)
	}

	// Add the new message
	message := Message{
		Type:      messageType,
		Text:      text,
		Timestamp: time.Now(),
	}

	context.Messages = append(context.Messages, message)
	context.UpdatedAt = time.Now()

	// Check the context size and trim if needed
	context = cm.trimContext(context)

	return cm.saveContext(context)
}

// trimContext trims the context to the maximum size
func (cm *ConversationManager) trimContext(context *ConversationContext) *ConversationContext {
	for {
		totalLength := cm.calculateContextLength(context)
		if totalLength <= MaxContextLength {
			break
		}

		// Remove the oldest messages (except the last one)
		if len(context.Messages) <= 1 {
			break
		}

		context.Messages = context.Messages[1:]
	}

	return context
}

// calculateContextLength calculates the total context length in characters
func (cm *ConversationManager) calculateContextLength(context *ConversationContext) int {
	totalLength := 0
	for _, message := range context.Messages {
		totalLength += len(message.Text) + len(message.Type) + 50 // +50 для метаданных
	}
	return totalLength
}

// saveContext saves the context to Redis
func (cm *ConversationManager) saveContext(context *ConversationContext) error {
	// Build key using the hash stored in context
	key := fmt.Sprintf("%s%s:%d", ConversationKeyPrefix, context.TokenHash, context.UserID)

	data, err := json.Marshal(context)
	if err != nil {
		return fmt.Errorf("failed to marshal context: %v", err)
	}

	err = cm.redisClient.Set(key, data, ConversationTTL).Err()
	if err != nil {
		return fmt.Errorf("failed to save context to Redis: %v", err)
	}

	return nil
}

// GetContextAsString returns the context as a formatted string for AI
func (cm *ConversationManager) GetContextAsString(botToken string, userID int64) (string, error) {
	context, err := cm.GetContext(botToken, userID)
	if err != nil {
		return "", err
	}

	if len(context.Messages) == 0 {
		return "", nil
	}

	var builder strings.Builder
	builder.WriteString("Context of previous messages:\n")

	for _, message := range context.Messages {
		if message.Type == "user" {
			builder.WriteString("User: ")
		} else {
			builder.WriteString("Bot: ")
		}
		builder.WriteString(message.Text)
		builder.WriteString("\n")
	}

	return builder.String(), nil
}

// ClearContext clears the context for a user
func (cm *ConversationManager) ClearContext(botToken string, userID int64) error {
	key := cm.getRedisKey(botToken, userID)

	err := cm.redisClient.Del(key).Err()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("failed to clear context: %v", err)
	}

	logging.Infof("Cleared conversation context for user %d, bot %s", userID, botToken)
	return nil
}

// ProcessIncomingMessage processes an incoming message from a user
func (cm *ConversationManager) ProcessIncomingMessage(botToken string, message *tele.Message) error {
	if message.Text == "" {
		return nil // Ignore messages without text
	}

	userID := message.Sender.ID
	return cm.AddUserMessage(botToken, userID, message.Text)
}

// ProcessOutgoingMessage processes an outgoing message to a user
func (cm *ConversationManager) ProcessOutgoingMessage(botToken string, userID int64, text string) error {
	return cm.AddBotMessage(botToken, userID, text)
}

// GetContextStats returns statistics about the conversation context
func (cm *ConversationManager) GetContextStats(botToken string, userID int64) (map[string]interface{}, error) {
	context, err := cm.GetContext(botToken, userID)
	if err != nil {
		return nil, err
	}

	userMessages := 0
	botMessages := 0
	for _, message := range context.Messages {
		if message.Type == "user" {
			userMessages++
		} else {
			botMessages++
		}
	}

	stats := map[string]interface{}{
		"messages_count": len(context.Messages),
		"context_length": cm.calculateContextLength(context),
		"max_length":     MaxContextLength,
		"user_messages":  userMessages,
		"bot_messages":   botMessages,
		"created_at":     context.CreatedAt.Format("2006-01-02 15:04:05"),
		"updated_at":     context.UpdatedAt.Format("2006-01-02 15:04:05"),
	}

	return stats, nil
}

// UpdateSearchParams updates the search parameters in the conversation context
func (cm *ConversationManager) UpdateSearchParams(botToken string, userID int64, searchParams *commands.SearchParams) error {
	context, err := cm.GetContext(botToken, userID)
	if err != nil {
		return fmt.Errorf("failed to get context: %v", err)
	}

	context.SearchParams = searchParams
	context.UpdatedAt = time.Now()

	return cm.saveContext(context)
}

// UpdateSelectedBookID updates the selected book ID in the conversation context
func (cm *ConversationManager) UpdateSelectedBookID(botToken string, userID int64, bookID int64) error {
	context, err := cm.GetContext(botToken, userID)
	if err != nil {
		return fmt.Errorf("failed to get context: %v", err)
	}

	context.SelectedBookID = bookID
	context.UpdatedAt = time.Now()

	return cm.saveContext(context)
}

// getRedisKey generates a Redis key for the conversation context
// Uses SHA256 hash of bot token to avoid storing sensitive data in key names
func (cm *ConversationManager) getRedisKey(botToken string, userID int64) string {
	tokenHash := hashToken(botToken)
	return fmt.Sprintf("%s%s:%d", ConversationKeyPrefix, tokenHash, userID)
}

// hashToken creates a SHA256 hash of the token for secure storage in Redis keys
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])[:16] // Use first 16 chars for brevity
}

// SetUserState saves the user state to Redis
// State can be: "waiting_for_search", "waiting_for_author", "waiting_for_book", or "" (empty)
func (cm *ConversationManager) SetUserState(botToken string, userID int64, state string) error {
	key := cm.getUserStateKey(botToken, userID)

	if state == "" {
		// Empty state means clear it
		return cm.ClearUserState(botToken, userID)
	}

	err := cm.redisClient.Set(key, state, UserStateTTL).Err()
	if err != nil {
		return fmt.Errorf("failed to save user state to Redis: %v", err)
	}

	logging.Infof("Set user state for user %d: %s", userID, state)
	return nil
}

// GetUserState retrieves the user state from Redis
// Returns empty string if no state is set
func (cm *ConversationManager) GetUserState(botToken string, userID int64) (string, error) {
	key := cm.getUserStateKey(botToken, userID)

	state, err := cm.redisClient.Get(key).Result()
	if err == redis.Nil {
		// No state set, return empty string
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("failed to get user state from Redis: %v", err)
	}

	return state, nil
}

// ClearUserState clears the user state from Redis
func (cm *ConversationManager) ClearUserState(botToken string, userID int64) error {
	key := cm.getUserStateKey(botToken, userID)

	err := cm.redisClient.Del(key).Err()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("failed to clear user state: %v", err)
	}

	logging.Infof("Cleared user state for user %d", userID)
	return nil
}

// getUserStateKey generates a Redis key for user state
func (cm *ConversationManager) getUserStateKey(botToken string, userID int64) string {
	tokenHash := hashToken(botToken)
	return fmt.Sprintf("%s%s:%d", UserStateKeyPrefix, tokenHash, userID)
}
