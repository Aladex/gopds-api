package telegram

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// TelegramService contains all Telegram-related components
type TelegramService struct {
	botManager *BotManager
	routes     *Routes
}

// NewTelegramService creates and initializes a new Telegram service
func NewTelegramService(botManager *BotManager) (*TelegramService, error) {
	if botManager == nil {
		return nil, fmt.Errorf("botManager cannot be nil")
	}

	routes := NewRoutes(botManager)

	// Initialize existing bots for users
	if err := botManager.InitializeExistingBots(); err != nil {
		return nil, fmt.Errorf("failed to initialize existing bots: %w", err)
	}

	return &TelegramService{
		botManager: botManager,
		routes:     routes,
	}, nil
}

// SetupWebhookRoutes sets up webhook routes for Telegram bots
func (s *TelegramService) SetupWebhookRoutes(group *gin.RouterGroup) {
	// Dynamic route for bot webhooks
	// Format: /telegram/{token}
	group.POST("/:token", s.routes.HandleWebhook)
}

// SetupApiRoutes sets up API routes for managing Telegram bots
func (s *TelegramService) SetupApiRoutes(group *gin.RouterGroup) {
	// Routes for managing user's bot
	group.POST("/bot", s.routes.SetBotToken)
	group.DELETE("/bot", s.routes.RemoveBotToken)
	group.GET("/bot/status", s.routes.GetBotStatus)
}

// GetBotManager returns the bot manager instance
func (s *TelegramService) GetBotManager() *BotManager {
	return s.botManager
}

// GetRoutes returns the routes instance
func (s *TelegramService) GetRoutes() *Routes {
	return s.routes
}
