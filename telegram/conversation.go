package telegram

import (
	"encoding/json"
	"fmt"
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
	// ConversationTTL TTL for context (7 days)
	ConversationTTL = 7 * 24 * time.Hour
)

// ConversationManager manages the conversation context with users
type ConversationManager struct {
	redisClient *redis.Client
}

// ConversationContext represents the conversation context with a user
type ConversationContext struct {
	UserID    int64     `json:"user_id"`
	BotToken  string    `json:"bot_token"`
	Messages  []Message `json:"messages"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
			BotToken:  botToken,
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
	key := cm.getRedisKey(context.BotToken, context.UserID)

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

	// Add the user message to the context
	err := cm.AddUserMessage(botToken, userID, message.Text)
	if err != nil {
		logging.Errorf("Failed to add user message to context: %v", err)
		return err
	}

	logging.Debugf("Added user message to context for user %d: %s", userID, message.Text)
	return nil
}

// ProcessOutgoingMessage processes an outgoing message from the bot
func (cm *ConversationManager) ProcessOutgoingMessage(botToken string, userID int64, text string) error {
	if text == "" {
		return nil
	}

	err := cm.AddBotMessage(botToken, userID, text)
	if err != nil {
		logging.Errorf("Failed to add bot message to context: %v", err)
		return err
	}

	logging.Debugf("Added bot message to context for user %d: %s", userID, text)
	return nil
}

// getRedisKey generates a Redis key for the context
func (cm *ConversationManager) getRedisKey(botToken string, userID int64) string {
	// Use the last 8 characters of the token for brevity
	tokenSuffix := botToken
	if len(botToken) > 8 {
		tokenSuffix = botToken[len(botToken)-8:]
	}
	return fmt.Sprintf("%s%s:%d", ConversationKeyPrefix, tokenSuffix, userID)
}

// GetContextStats returns context statistics
func (cm *ConversationManager) GetContextStats(botToken string, userID int64) (map[string]interface{}, error) {
	context, err := cm.GetContext(botToken, userID)
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"messages_count": len(context.Messages),
		"context_length": cm.calculateContextLength(context),
		"max_length":     MaxContextLength,
		"created_at":     context.CreatedAt,
		"updated_at":     context.UpdatedAt,
	}

	if len(context.Messages) > 0 {
		userMessages := 0
		botMessages := 0
		for _, msg := range context.Messages {
			if msg.Type == "user" {
				userMessages++
			} else {
				botMessages++
			}
		}
		stats["user_messages"] = userMessages
		stats["bot_messages"] = botMessages
	}

	return stats, nil
}
