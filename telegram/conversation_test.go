package telegram

import (
	"testing"
	"time"

	"gopds-api/commands"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
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

func TestUpdateSearchParams(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	searchParams := &commands.SearchParams{
		Query:     "test query",
		Offset:    10,
		Limit:     5,
		QueryType: "book",
		AuthorID:  42,
	}

	err := cm.UpdateSearchParams(botToken, userID, searchParams)
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	assert.NotNil(t, ctx.SearchParams)
	assert.Equal(t, "test query", ctx.SearchParams.Query)
	assert.Equal(t, 10, ctx.SearchParams.Offset)
	assert.Equal(t, 5, ctx.SearchParams.Limit)
	assert.Equal(t, "book", ctx.SearchParams.QueryType)
	assert.Equal(t, int64(42), ctx.SearchParams.AuthorID)
}

func TestUpdateSearchParams_OverwritesPrevious(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	// Set initial params
	params1 := &commands.SearchParams{
		Query:  "first query",
		Offset: 0,
		Limit:  5,
	}
	err := cm.UpdateSearchParams(botToken, userID, params1)
	require.NoError(t, err)

	// Update with new params
	params2 := &commands.SearchParams{
		Query:  "second query",
		Offset: 10,
		Limit:  10,
	}
	err = cm.UpdateSearchParams(botToken, userID, params2)
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	// Should have the second params
	assert.Equal(t, "second query", ctx.SearchParams.Query)
	assert.Equal(t, 10, ctx.SearchParams.Offset)
	assert.Equal(t, 10, ctx.SearchParams.Limit)
}

func TestProcessIncomingMessage(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"

	// Create a mock telegram message
	message := &tele.Message{
		Sender: &tele.User{ID: 12345},
		Text:   "Hello from user!",
	}

	err := cm.ProcessIncomingMessage(botToken, message)
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, 12345)
	require.NoError(t, err)

	assert.Len(t, ctx.Messages, 1)
	assert.Equal(t, "user", ctx.Messages[0].Type)
	assert.Equal(t, "Hello from user!", ctx.Messages[0].Text)
}

func TestProcessIncomingMessage_EmptyText(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"

	// Create a message with empty text
	message := &tele.Message{
		Sender: &tele.User{ID: 12345},
		Text:   "",
	}

	err := cm.ProcessIncomingMessage(botToken, message)
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, 12345)
	require.NoError(t, err)

	// Should not add empty message
	assert.Empty(t, ctx.Messages)
}

func TestProcessOutgoingMessage(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.ProcessOutgoingMessage(botToken, userID, "Hello from bot!")
	require.NoError(t, err)

	ctx, err := cm.GetContext(botToken, userID)
	require.NoError(t, err)

	assert.Len(t, ctx.Messages, 1)
	assert.Equal(t, "bot", ctx.Messages[0].Type)
	assert.Equal(t, "Hello from bot!", ctx.Messages[0].Text)
}

func TestCalculateContextLength(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)

	ctx := &ConversationContext{
		Messages: []Message{
			{Type: "user", Text: "Hello"},
			{Type: "bot", Text: "World"},
		},
	}

	length := cm.calculateContextLength(ctx)

	// Each message adds: len(text) + len(type) + 50 (metadata)
	// "Hello" (5) + "user" (4) + 50 = 59
	// "World" (5) + "bot" (3) + 50 = 58
	// Total = 117
	assert.Equal(t, 117, length)
}

func TestCalculateContextLength_EmptyContext(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)

	ctx := &ConversationContext{
		Messages: []Message{},
	}

	length := cm.calculateContextLength(ctx)
	assert.Equal(t, 0, length)
}

func TestSetUserState(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	err := cm.SetUserState(botToken, userID, "waiting_for_search")
	require.NoError(t, err)

	state, err := cm.GetUserState(botToken, userID)
	require.NoError(t, err)

	assert.Equal(t, "waiting_for_search", state)
}

func TestGetUserState_NoState(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	state, err := cm.GetUserState(botToken, userID)
	require.NoError(t, err)

	assert.Empty(t, state)
}

func TestClearUserState(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	// Set state
	err := cm.SetUserState(botToken, userID, "waiting_for_author")
	require.NoError(t, err)

	// Clear state
	err = cm.ClearUserState(botToken, userID)
	require.NoError(t, err)

	// Verify state is cleared
	state, err := cm.GetUserState(botToken, userID)
	require.NoError(t, err)
	assert.Empty(t, state)
}

func TestSetUserState_EmptyStateClearsState(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	// Set state
	err := cm.SetUserState(botToken, userID, "waiting_for_book")
	require.NoError(t, err)

	// Set empty state (should clear it)
	err = cm.SetUserState(botToken, userID, "")
	require.NoError(t, err)

	// Verify state is cleared
	state, err := cm.GetUserState(botToken, userID)
	require.NoError(t, err)
	assert.Empty(t, state)
}

func TestUserState_AllStates(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	states := []string{
		"waiting_for_search",
		"waiting_for_author",
		"waiting_for_book",
	}

	for _, expectedState := range states {
		err := cm.SetUserState(botToken, userID, expectedState)
		require.NoError(t, err)

		actualState, err := cm.GetUserState(botToken, userID)
		require.NoError(t, err)
		assert.Equal(t, expectedState, actualState)
	}
}

func TestUserState_IsolatedForDifferentUsers(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"

	// Set different states for different users
	err := cm.SetUserState(botToken, 111, "waiting_for_search")
	require.NoError(t, err)

	err = cm.SetUserState(botToken, 222, "waiting_for_author")
	require.NoError(t, err)

	// Verify states are isolated
	state1, err := cm.GetUserState(botToken, 111)
	require.NoError(t, err)
	assert.Equal(t, "waiting_for_search", state1)

	state2, err := cm.GetUserState(botToken, 222)
	require.NoError(t, err)
	assert.Equal(t, "waiting_for_author", state2)
}

func TestUserState_IsolatedForDifferentBots(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	userID := int64(12345)

	// Set different states for different bots
	err := cm.SetUserState("token-bot-1", userID, "waiting_for_search")
	require.NoError(t, err)

	err = cm.SetUserState("token-bot-2", userID, "waiting_for_book")
	require.NoError(t, err)

	// Verify states are isolated
	state1, err := cm.GetUserState("token-bot-1", userID)
	require.NoError(t, err)
	assert.Equal(t, "waiting_for_search", state1)

	state2, err := cm.GetUserState("token-bot-2", userID)
	require.NoError(t, err)
	assert.Equal(t, "waiting_for_book", state2)
}

func TestUserState_OverwritesPrevious(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)
	botToken := "test-token"
	userID := int64(12345)

	// Set initial state
	err := cm.SetUserState(botToken, userID, "waiting_for_search")
	require.NoError(t, err)

	// Overwrite with new state
	err = cm.SetUserState(botToken, userID, "waiting_for_author")
	require.NoError(t, err)

	// Verify new state
	state, err := cm.GetUserState(botToken, userID)
	require.NoError(t, err)
	assert.Equal(t, "waiting_for_author", state)
}

func TestGetUserStateKey(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	cm := NewConversationManager(redisClient)

	key := cm.getUserStateKey("test-token", 12345)

	assert.Contains(t, key, UserStateKeyPrefix)
	assert.Contains(t, key, ":12345")
	// Should not contain raw token
	assert.NotContains(t, key, "test-token")
}
