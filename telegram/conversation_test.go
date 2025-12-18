package telegram

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) (*redis.Client, func()) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	return client, func() {
		client.Close()
		mr.Close()
	}
}

func TestNewConversationManager(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	assert.NotNil(t, cm)
	assert.Equal(t, redisClient, cm.redisClient)
}

func TestGetContext_NewContext(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)

	ctx, err := cm.GetContext("test-token", 12345)
	require.NoError(t, err)

	assert.Equal(t, int64(12345), ctx.UserID)
	assert.Equal(t, hashToken("test-token"), ctx.TokenHash)
	assert.Empty(t, ctx.Messages)
	assert.NotZero(t, ctx.CreatedAt)
	assert.NotZero(t, ctx.UpdatedAt)
}

func TestAddUserMessage(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.AddUserMessage(botToken, userID, "Hello, bot!")
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	assert.Len(t, ctx.Messages, 1)
	assert.Equal(t, "user", ctx.Messages[0].Type)
	assert.Equal(t, "Hello, bot!", ctx.Messages[0].Text)
}

func TestAddBotMessage(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.AddBotMessage(botToken, userID, "Hello, user!")
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	assert.Len(t, ctx.Messages, 1)
	assert.Equal(t, "bot", ctx.Messages[0].Type)
	assert.Equal(t, "Hello, user!", ctx.Messages[0].Text)
}

func TestMultipleMessages(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.AddUserMessage(botToken, userID, "Hello!")
	require.NoError(t, err)

	err = cm.AddBotMessage(botToken, userID, "Hi there!")
	require.NoError(t, err)

	err = cm.AddUserMessage(botToken, userID, "How are you?")
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	assert.Len(t, ctx.Messages, 3)
	assert.Equal(t, "user", ctx.Messages[0].Type)
	assert.Equal(t, "Hello!", ctx.Messages[0].Text)
	assert.Equal(t, "bot", ctx.Messages[1].Type)
	assert.Equal(t, "Hi there!", ctx.Messages[1].Text)
	assert.Equal(t, "user", ctx.Messages[2].Type)
	assert.Equal(t, "How are you?", ctx.Messages[2].Text)
}

func TestClearContext(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.AddUserMessage(botToken, userID, "Hello!")
	require.NoError(t, err)

	err = cm.ClearContext(botToken, userID)
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	assert.Empty(t, ctx.Messages)
}

func TestGetContextAsString(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.AddUserMessage(botToken, userID, "Hello!")
	require.NoError(t, err)

	err = cm.AddBotMessage(botToken, userID, "Hi!")
	require.NoError(t, err)

	contextStr, err := cm.GetContextAsString(botToken, userID)
	require.NoError(t, err)

	assert.Contains(t, contextStr, "User: Hello!")
	assert.Contains(t, contextStr, "Bot: Hi!")
}

func TestGetContextAsString_Empty(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)

	contextStr, err := cm.GetContextAsString("test-token", 12345)
	require.NoError(t, err)

	assert.Empty(t, contextStr)
}

func TestGetContextStats(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.AddUserMessage(botToken, userID, "Hello!")
	require.NoError(t, err)

	err = cm.AddBotMessage(botToken, userID, "Hi!")
	require.NoError(t, err)

	stats, err := cm.GetContextStats(botToken, userID)
	require.NoError(t, err)

	assert.Equal(t, 2, stats["messages_count"])
	assert.Equal(t, 1, stats["user_messages"])
	assert.Equal(t, 1, stats["bot_messages"])
	assert.Equal(t, MaxContextLength, stats["max_length"])
}

func TestTrimContext(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	// Add many long messages to exceed MaxContextLength
	longMessage := make([]byte, 1000)
	for i := range longMessage {
		longMessage[i] = 'a'
	}

	for i := 0; i < 10; i++ {
		err := cm.AddUserMessage(botToken, userID, string(longMessage))
		require.NoError(t, err)
	}

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	// Context should be trimmed
	totalLength := cm.calculateContextLength(ctx)
	assert.LessOrEqual(t, totalLength, MaxContextLength)
}

func TestHashToken(t *testing.T) {
	token1 := "test-token-1"
	token2 := "test-token-2"

	hash1 := hashToken(token1)
	hash2 := hashToken(token2)

	// Hashes should be different for different tokens
	assert.NotEqual(t, hash1, hash2)

	// Same token should produce same hash
	assert.Equal(t, hash1, hashToken(token1))

	// Hash should be 16 characters
	assert.Len(t, hash1, 16)
}

func TestGetRedisKey(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)

	key := cm.getRedisKey("test-token", 12345)

	assert.Contains(t, key, ConversationKeyPrefix)
	assert.Contains(t, key, ":12345")
	// Should not contain raw token
	assert.NotContains(t, key, "test-token")
}

func TestIsolatedContextsForDifferentUsers(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"

	err := cm.AddUserMessage(botToken, 111, "Message from user 111")
	require.NoError(t, err)

	err = cm.AddUserMessage(botToken, 222, "Message from user 222")
	require.NoError(t, err)

	ctx1, err := cm.GetContext(botToken, 111)
	require.NoError(t, err)
	assert.Len(t, ctx1.Messages, 1)
	assert.Equal(t, "Message from user 111", ctx1.Messages[0].Text)

	ctx2, err := cm.GetContext(botToken, 222)
	require.NoError(t, err)
	assert.Len(t, ctx2.Messages, 1)
	assert.Equal(t, "Message from user 222", ctx2.Messages[0].Text)
}

func TestIsolatedContextsForDifferentBots(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	userID := int64(12345)

	err := cm.AddUserMessage("token-bot-1", userID, "Message from bot 1")
	require.NoError(t, err)

	err = cm.AddUserMessage("token-bot-2", userID, "Message from bot 2")
	require.NoError(t, err)

	ctx1, err := cm.GetContext("token-bot-1", userID)
	require.NoError(t, err)
	assert.Len(t, ctx1.Messages, 1)
	assert.Equal(t, "Message from bot 1", ctx1.Messages[0].Text)

	ctx2, err := cm.GetContext("token-bot-2", userID)
	require.NoError(t, err)
	assert.Len(t, ctx2.Messages, 1)
	assert.Equal(t, "Message from bot 2", ctx2.Messages[0].Text)
}

func TestUpdateSelectedBookID(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.UpdateSelectedBookID(botToken, userID, 42)
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	assert.Equal(t, int64(42), ctx.SelectedBookID)
}

func TestContextUpdateTime(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.AddUserMessage(botToken, userID, "First message")
	require.NoError(t, err)

	ctx1, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)
	firstUpdate := ctx1.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	err = cm.AddUserMessage(botToken, userID, "Second message")
	require.NoError(t, err)

	ctx2, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	assert.True(t, ctx2.UpdatedAt.After(firstUpdate))
}
