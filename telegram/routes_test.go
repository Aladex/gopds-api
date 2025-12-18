package telegram

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRouter(t *testing.T) (*gin.Engine, *BotManager, func()) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	config := &Config{
		BaseURL: "https://test.example.com",
	}
	botManager := NewBotManager(config, redisClient)

	router := gin.New()

	return router, botManager, func() {
		redisClient.Close()
		mr.Close()
	}
}

func TestNewRoutes(t *testing.T) {
	_, botManager, cleanup := setupTestRouter(t)
	defer cleanup()

	routes := NewRoutes(botManager)

	assert.NotNil(t, routes)
	assert.Equal(t, botManager, routes.botManager)
}

func TestIsValidToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected bool
	}{
		{"valid token", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ", true},
		{"valid long token", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ_123456789", true},
		{"empty token", "", false},
		{"too short", "abc", false},
		{"no colon", "12345678901234567890", false},
		{"spaces in token", "123:abc def", false},
		{"newline in token", "123:abc\ndef", false},
		{"tab in token", "123:abc\tdef", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidToken(tt.token)
			assert.Equal(t, tt.expected, result, "Token: %q", tt.token)
		})
	}
}

func TestHandleWebhook_NoToken(t *testing.T) {
	router, botManager, cleanup := setupTestRouter(t)
	defer cleanup()

	routes := NewRoutes(botManager)
	router.POST("/telegram/:token", routes.HandleWebhook)

	req := httptest.NewRequest(http.MethodPost, "/telegram/", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should return 404 as no route matches empty token
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleWebhook_InvalidToken(t *testing.T) {
	router, botManager, cleanup := setupTestRouter(t)
	defer cleanup()

	routes := NewRoutes(botManager)
	router.POST("/telegram/:token", routes.HandleWebhook)

	req := httptest.NewRequest(http.MethodPost, "/telegram/invalid", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should return 404 for unknown bot (HandleWebhook checks bot existence, not token format)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleWebhook_UnknownBot(t *testing.T) {
	router, botManager, cleanup := setupTestRouter(t)
	defer cleanup()

	routes := NewRoutes(botManager)
	router.POST("/telegram/:token", routes.HandleWebhook)

	// Valid token format but unknown bot
	req := httptest.NewRequest(http.MethodPost, "/telegram/123456789:ABCdefGHIjklMNOpqrSTUvwxYZ", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should return 404 for unknown bot
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSetBotToken_NoAuth(t *testing.T) {
	router, botManager, cleanup := setupTestRouter(t)
	defer cleanup()

	routes := NewRoutes(botManager)
	router.POST("/bot", routes.SetBotToken)

	req := httptest.NewRequest(http.MethodPost, "/bot", strings.NewReader(`{"bot_token": "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should fail without user context
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRemoveBotToken_NoAuth(t *testing.T) {
	router, botManager, cleanup := setupTestRouter(t)
	defer cleanup()

	routes := NewRoutes(botManager)
	router.DELETE("/bot", routes.RemoveBotToken)

	req := httptest.NewRequest(http.MethodDelete, "/bot", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should fail without user context
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetBotStatus_NoAuth(t *testing.T) {
	router, botManager, cleanup := setupTestRouter(t)
	defer cleanup()

	routes := NewRoutes(botManager)
	router.GET("/bot/status", routes.GetBotStatus)

	req := httptest.NewRequest(http.MethodGet, "/bot/status", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should fail without user context
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{"normal token", "1234567890:ABCDEFGHIJ", "1234567890***"},
		{"short token", "abc", "***"},
		{"empty token", "", "***"},
		{"exactly 10 chars", "0123456789", "0123456789***"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskToken(tt.token)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTelegramService_NilBotManager(t *testing.T) {
	service, err := NewTelegramService(nil)

	assert.Error(t, err)
	assert.Nil(t, service)
	assert.Contains(t, err.Error(), "botManager cannot be nil")
}

func TestTelegramService_SetupRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://test.example.com",
	}
	botManager := NewBotManager(config, redisClient)

	// Skip bot initialization by creating service without InitializeExistingBots
	service := &TelegramService{
		botManager: botManager,
		routes:     NewRoutes(botManager),
	}

	router := gin.New()
	telegramGroup := router.Group("/telegram")
	apiGroup := router.Group("/api/telegram")

	service.SetupWebhookRoutes(telegramGroup)
	service.SetupApiRoutes(apiGroup)

	// Check that routes were set up
	routes := router.Routes()
	var foundWebhook, foundSetBot, foundRemoveBot, foundStatus bool

	for _, r := range routes {
		if r.Path == "/telegram/:token" && r.Method == "POST" {
			foundWebhook = true
		}
		if r.Path == "/api/telegram/bot" && r.Method == "POST" {
			foundSetBot = true
		}
		if r.Path == "/api/telegram/bot" && r.Method == "DELETE" {
			foundRemoveBot = true
		}
		if r.Path == "/api/telegram/bot/status" && r.Method == "GET" {
			foundStatus = true
		}
	}

	assert.True(t, foundWebhook, "Webhook route should be registered")
	assert.True(t, foundSetBot, "SetBotToken route should be registered")
	assert.True(t, foundRemoveBot, "RemoveBotToken route should be registered")
	assert.True(t, foundStatus, "GetBotStatus route should be registered")
}

func TestTelegramService_Getters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer redisClient.Close()

	config := &Config{
		BaseURL: "https://test.example.com",
	}
	botManager := NewBotManager(config, redisClient)
	routes := NewRoutes(botManager)

	service := &TelegramService{
		botManager: botManager,
		routes:     routes,
	}

	assert.Equal(t, botManager, service.GetBotManager())
	assert.Equal(t, routes, service.GetRoutes())
}
