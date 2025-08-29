package telegram

import (
	"github.com/gin-gonic/gin"
)

var (
	// Глобальный экземпляр менеджера ботов
	globalBotManager *BotManager
	// Глобальный экземпляр роутов
	globalRoutes *Routes
)

// InitializeTelegram инициализирует Telegram компоненты
func InitializeTelegram(botManager *BotManager) {
	globalBotManager = botManager
	globalRoutes = NewRoutes(botManager)

	// Инициализируем существующих ботов для пользователей
	err := botManager.InitializeExistingBots()
	if err != nil {
		panic("Failed to initialize existing bots: " + err.Error())
	}
}

// SetupWebhookRoutes настраивает webhook роуты для Telegram ботов
func SetupWebhookRoutes(group *gin.RouterGroup) {
	if globalRoutes == nil {
		panic("Telegram not initialized. Call InitializeTelegram first.")
	}

	// Динамический роут для webhook'ов ботов
	// Формат: /telegram/{token}
	group.POST("/:token", globalRoutes.HandleWebhook)
}

// SetupApiRoutes настраивает API роуты для управления Telegram ботами
func SetupApiRoutes(group *gin.RouterGroup) {
	if globalRoutes == nil {
		panic("Telegram not initialized. Call InitializeTelegram first.")
	}

	// Роуты для управления ботом пользователя
	group.POST("/bot", globalRoutes.SetBotToken)
	group.DELETE("/bot", globalRoutes.RemoveBotToken)
	group.GET("/bot/status", globalRoutes.GetBotStatus)
}
