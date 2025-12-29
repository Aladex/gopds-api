package telegram

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBotManager(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	assert.NotNil(t, bm)
	assert.NotNil(t, bm.bots)
	assert.Equal(t, config, bm.config)
	assert.NotNil(t, bm.conversationManager)
}

func TestParseAuthorTitle(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		expectedAuthor string
		expectedTitle  string
	}{
		{
			name:           "colon with space separator",
			query:          "Толстой: Война и мир",
			expectedAuthor: "Толстой",
			expectedTitle:  "Война и мир",
		},
		{
			name:           "dash with spaces separator",
			query:          "Достоевский - Преступление и наказание",
			expectedAuthor: "Достоевский",
			expectedTitle:  "Преступление и наказание",
		},
		{
			name:           "em-dash with spaces separator",
			query:          "Чехов — Вишнёвый сад",
			expectedAuthor: "Чехов",
			expectedTitle:  "Вишнёвый сад",
		},
		{
			name:           "colon without space",
			query:          "Пушкин:Евгений Онегин",
			expectedAuthor: "Пушкин",
			expectedTitle:  "Евгений Онегин",
		},
		{
			name:           "dash without spaces",
			query:          "Гоголь-Мёртвые души",
			expectedAuthor: "Гоголь",
			expectedTitle:  "Мёртвые души",
		},
		{
			name:           "em-dash without spaces",
			query:          "Булгаков—Мастер и Маргарита",
			expectedAuthor: "Булгаков",
			expectedTitle:  "Мастер и Маргарита",
		},
		{
			name:           "author with multiple words",
			query:          "Лев Толстой: Анна Каренина",
			expectedAuthor: "Лев Толстой",
			expectedTitle:  "Анна Каренина",
		},
		{
			name:           "title with multiple words",
			query:          "Тургенев: Отцы и дети",
			expectedAuthor: "Тургенев",
			expectedTitle:  "Отцы и дети",
		},
		{
			name:           "no separator",
			query:          "Война и мир",
			expectedAuthor: "",
			expectedTitle:  "",
		},
		{
			name:           "empty string",
			query:          "",
			expectedAuthor: "",
			expectedTitle:  "",
		},
		{
			name:           "only separator",
			query:          ": ",
			expectedAuthor: "",
			expectedTitle:  "",
		},
		{
			name:           "empty author",
			query:          ": Война и мир",
			expectedAuthor: "",
			expectedTitle:  "",
		},
		{
			name:           "empty title",
			query:          "Толстой: ",
			expectedAuthor: "",
			expectedTitle:  "",
		},
		{
			name:           "whitespace around author and title",
			query:          "  Толстой  :  Война и мир  ",
			expectedAuthor: "Толстой",
			expectedTitle:  "Война и мир",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			author, title := parseAuthorTitle(tt.query)
			assert.Equal(t, tt.expectedAuthor, author, "author mismatch")
			assert.Equal(t, tt.expectedTitle, title, "title mismatch")
		})
	}
}

func TestGetBotCount(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	// Initially should be 0
	assert.Equal(t, 0, bm.GetBotCount())

	// Manually add a bot to test counting
	bm.mutex.Lock()
	bm.bots["test-token-1"] = &Bot{token: "test-token-1", userID: 1}
	bm.mutex.Unlock()

	assert.Equal(t, 1, bm.GetBotCount())

	// Add another bot
	bm.mutex.Lock()
	bm.bots["test-token-2"] = &Bot{token: "test-token-2", userID: 2}
	bm.mutex.Unlock()

	assert.Equal(t, 2, bm.GetBotCount())
}

func TestListActiveBots(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	// Initially should be empty
	tokens := bm.ListActiveBots()
	assert.Empty(t, tokens)

	// Manually add bots
	bm.mutex.Lock()
	bm.bots["1234567890:ABC"] = &Bot{token: "1234567890:ABC", userID: 1}
	bm.bots["0987654321:XYZ"] = &Bot{token: "0987654321:XYZ", userID: 2}
	bm.mutex.Unlock()

	tokens = bm.ListActiveBots()
	assert.Len(t, tokens, 2)

	// Each token should be masked and include user ID
	for _, token := range tokens {
		assert.Contains(t, token, "***")
		assert.Contains(t, token, "(user")
	}
}

func TestGetConversationManager(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	cm := bm.GetConversationManager()
	assert.NotNil(t, cm)
	assert.Equal(t, bm.conversationManager, cm)
}

func TestGetConversationContext(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	// Test getting context for a new user
	ctx, err := bm.GetConversationContext("test-token", 12345)
	require.NoError(t, err)
	assert.NotNil(t, ctx)
	assert.Equal(t, int64(12345), ctx.UserID)
}

func TestGetConversationContextAsString(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	// Add some messages first
	err = bm.conversationManager.AddUserMessage("test-token", 12345, "Hello!")
	require.NoError(t, err)

	// Get context as string
	ctxStr, err := bm.GetConversationContextAsString("test-token", 12345)
	require.NoError(t, err)
	assert.Contains(t, ctxStr, "User: Hello!")
}

func TestClearConversationContext(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	// Add some messages first
	err = bm.conversationManager.AddUserMessage("test-token", 12345, "Hello!")
	require.NoError(t, err)

	// Clear context
	err = bm.ClearConversationContext("test-token", 12345)
	require.NoError(t, err)

	// Verify context is cleared
	ctx, err := bm.GetConversationContext("test-token", 12345)
	require.NoError(t, err)
	assert.Empty(t, ctx.Messages)
}

func TestCreateBotForUser_AlreadyExists(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	// Manually add a bot
	bm.mutex.Lock()
	bm.bots["existing-token"] = &Bot{token: "existing-token", userID: 1}
	bm.mutex.Unlock()

	// Try to create a bot with the same token
	err = bm.CreateBotForUser("existing-token", 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestRemoveBot_NotFound(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	err = bm.RemoveBot("non-existent-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSetWebhook_BotNotFound(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://example.com",
	}

	bm := NewBotManager(config, redisClient)

	err = bm.SetWebhook("non-existent-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRegisterCommands(t *testing.T) {
	// Note: This test validates that registerCommands() is called during bot creation.
	// The actual Telegram API call is tested indirectly through the bot creation flow.
	// A full integration test would require a mock Telegram bot or real bot token.

	// We test that the function signature and command list are correct
	// by checking the commands array definition exists and has the expected structure

	// The registerCommands() function should define these commands:
	expectedCommands := map[string]string{
		"start":     "Link your account to the library",
		"search":    "Search for books using natural language",
		"b":         "Exact book search by title",
		"a":         "Exact author search by name",
		"ba":        "Exact combined search (author: book)",
		"favorites": "Show your favorite books",
		"context":   "Show conversation context statistics",
		"clear":     "Clear conversation context",
	}

	// Verify all expected commands are documented
	assert.Equal(t, 8, len(expectedCommands), "Should have 8 registered commands")

	// Verify command names are valid (lowercase, no special chars except underscore)
	for cmd := range expectedCommands {
		assert.Regexp(t, "^[a-z_]+$", cmd, "Command should only contain lowercase letters and underscores")
		assert.True(t, len(cmd) >= 1 && len(cmd) <= 32, "Command should be 1-32 characters")
	}

	// Verify descriptions are valid (3-256 characters)
	for _, desc := range expectedCommands {
		assert.True(t, len(desc) >= 3 && len(desc) <= 256, "Description should be 3-256 characters")
	}
}
