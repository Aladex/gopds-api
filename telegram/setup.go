package telegram

import (
	"github.com/gin-gonic/gin"
)

var (
	// Global instance of bot manager
	globalBotManager *BotManager
	// Global instance of routes
	globalRoutes *Routes
)

// InitializeTelegram initializes Telegram components
func InitializeTelegram(botManager *BotManager) {
	globalBotManager = botManager
	globalRoutes = NewRoutes(botManager)

	// Initialize existing bots for users
	err := botManager.InitializeExistingBots()
	if err != nil {
		panic("Failed to initialize existing bots: " + err.Error())
	}
}

// SetupWebhookRoutes sets up webhook routes for Telegram bots
func SetupWebhookRoutes(group *gin.RouterGroup) {
	if globalRoutes == nil {
		panic("Telegram not initialized. Call InitializeTelegram first.")
	}

	// Dynamic route for bot webhooks
	// Format: /telegram/{token}
	group.POST("/:token", globalRoutes.HandleWebhook)
}

// SetupApiRoutes sets up API routes for managing Telegram bots
func SetupApiRoutes(group *gin.RouterGroup) {
	if globalRoutes == nil {
		panic("Telegram not initialized. Call InitializeTelegram first.")
	}

	// Routes for managing user's bot
	group.POST("/bot", globalRoutes.SetBotToken)
	group.DELETE("/bot", globalRoutes.RemoveBotToken)
	group.GET("/bot/status", globalRoutes.GetBotStatus)
}
