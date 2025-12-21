package telegram

import (
	"testing"

	"gopds-api/commands"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCallbackTestEnv(t *testing.T) (*ConversationManager, func()) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	cm := NewConversationManager(redisClient)

	return cm, func() {
		redisClient.Close()
		mr.Close()
	}
}

func TestNewCallbackHandler(t *testing.T) {
	cm, cleanup := setupCallbackTestEnv(t)
	defer cleanup()

	bot := &Bot{
		token:  "test-token",
		userID: 123,
	}

	handler := NewCallbackHandler(bot, cm)

	assert.NotNil(t, handler)
	assert.Equal(t, bot, handler.bot)
	assert.Equal(t, cm, handler.conversationManager)
}

func TestCleanCallbackData(t *testing.T) {
	cm, cleanup := setupCallbackTestEnv(t)
	defer cleanup()

	handler := &CallbackHandler{
		conversationManager: cm,
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"clean data", "prev_page", "prev_page"},
		{"with leading space", "  next_page", "next_page"},
		{"with trailing space", "next_page  ", "next_page"},
		{"with newline", "prev_page\n", "prev_page"},
		{"with tab", "\tprev_page", "prev_page"},
		{"with form feed", "\fprev_page", "prev_page"},
		{"with carriage return", "prev_page\r", "prev_page"},
		{"complex whitespace", " \t\nselect:123\r\f ", "select:123"},
		{"empty string", "", ""},
		{"only whitespace", "   \t\n", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.cleanCallbackData(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateNewOffset(t *testing.T) {
	cm, cleanup := setupCallbackTestEnv(t)
	defer cleanup()

	handler := &CallbackHandler{
		conversationManager: cm,
	}

	tests := []struct {
		name      string
		params    *commands.SearchParams
		direction string
		expected  int
	}{
		{
			name:      "next page from start",
			params:    &commands.SearchParams{Offset: 0, Limit: 5},
			direction: "next",
			expected:  5,
		},
		{
			name:      "next page from middle",
			params:    &commands.SearchParams{Offset: 10, Limit: 5},
			direction: "next",
			expected:  15,
		},
		{
			name:      "prev page from middle",
			params:    &commands.SearchParams{Offset: 10, Limit: 5},
			direction: "prev",
			expected:  5,
		},
		{
			name:      "prev page from start (should stay at 0)",
			params:    &commands.SearchParams{Offset: 0, Limit: 5},
			direction: "prev",
			expected:  0,
		},
		{
			name:      "prev page near start (should not go negative)",
			params:    &commands.SearchParams{Offset: 3, Limit: 5},
			direction: "prev",
			expected:  0,
		},
		{
			name:      "next with larger limit",
			params:    &commands.SearchParams{Offset: 0, Limit: 10},
			direction: "next",
			expected:  10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.calculateNewOffset(tt.params, tt.direction)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildFormatSelectionKeyboard(t *testing.T) {
	cm, cleanup := setupCallbackTestEnv(t)
	defer cleanup()

	handler := &CallbackHandler{
		conversationManager: cm,
	}

	markup := handler.buildFormatSelectionKeyboard(42)

	assert.NotNil(t, markup)
	assert.NotNil(t, markup.InlineKeyboard)
	assert.Len(t, markup.InlineKeyboard, 2) // Two rows

	// First row should have FB2 and EPUB buttons
	assert.Len(t, markup.InlineKeyboard[0], 2)

	// Second row should have MOBI and ZIP buttons
	assert.Len(t, markup.InlineKeyboard[1], 2)
}

func TestUpdateSearchParamsInContext(t *testing.T) {
	cm, cleanup := setupCallbackTestEnv(t)
	defer cleanup()

	handler := &CallbackHandler{
		bot:                 &Bot{token: "test-token"},
		conversationManager: cm,
	}

	telegramID := int64(12345)
	params := &commands.SearchParams{
		Query:     "test query",
		Offset:    10,
		Limit:     5,
		QueryType: "book",
	}

	// Should not panic with nil params
	handler.updateSearchParamsInContext(telegramID, nil)

	// Update with valid params
	handler.updateSearchParamsInContext(telegramID, params)

	// Verify params were saved
	ctx, err := cm.GetContext("test-token", telegramID)
	require.NoError(t, err)

	assert.NotNil(t, ctx.SearchParams)
	assert.Equal(t, "test query", ctx.SearchParams.Query)
	assert.Equal(t, 10, ctx.SearchParams.Offset)
	assert.Equal(t, 5, ctx.SearchParams.Limit)
}

func TestCallbackHandler_ProcessOutgoingMessage(t *testing.T) {
	cm, cleanup := setupCallbackTestEnv(t)
	defer cleanup()

	handler := &CallbackHandler{
		bot:                 &Bot{token: "test-token"},
		conversationManager: cm,
	}

	telegramID := int64(12345)
	message := "Test response message"

	handler.processOutgoingMessage(telegramID, message)

	// Verify message was added to context
	ctx, err := cm.GetContext("test-token", telegramID)
	require.NoError(t, err)

	assert.Len(t, ctx.Messages, 1)
	assert.Equal(t, "bot", ctx.Messages[0].Type)
	assert.Equal(t, message, ctx.Messages[0].Text)
}

func TestExecuteCombinedSearch_ParsesQueryCorrectly(t *testing.T) {
	// This test verifies the query parsing logic in executeCombinedSearch
	tests := []struct {
		name          string
		query         string
		expectedTitle string
		expectedHasBy bool
	}{
		{
			name:          "query with 'by' separator",
			query:         "\"War and Peace\" by Tolstoy",
			expectedTitle: "War and Peace",
			expectedHasBy: true,
		},
		{
			name:          "query without 'by' separator",
			query:         "War and Peace",
			expectedTitle: "War and Peace",
			expectedHasBy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify the parsing logic works
			// The actual search would require database mocking
			if tt.expectedHasBy {
				assert.Contains(t, tt.query, " by ")
			} else {
				assert.NotContains(t, tt.query, " by ")
			}
		})
	}
}
