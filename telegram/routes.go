package telegram

import (
	"net/http"
	"strconv"
	"strings"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
)

// Routes содержит все роуты для работы с Telegram ботами
type Routes struct {
	botManager *BotManager
}

// NewRoutes создает новый экземпляр роутов
func NewRoutes(botManager *BotManager) *Routes {
	return &Routes{
		botManager: botManager,
	}
}

// SetBotTokenRequest структура запроса для установки токена бота
type SetBotTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

// SetBotTokenResponse структура ответа при установке токена бота
type SetBotTokenResponse struct {
	Message    string `json:"message"`
	WebhookURL string `json:"webhook_url"`
}

// ErrorResponse структура ошибки
type ErrorResponse struct {
	Error string `json:"error"`
}

// SetBotToken устанавливает токен бота для текущего пользователя
// @Summary Установить токен Telegram бота
// @Description Устанавливает токен Telegram бота для текущего пользователя
// @Tags telegram
// @Accept json
// @Produce json
// @Param request body SetBotTokenRequest true "Токен бота"
// @Success 200 {object} SetBotTokenResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/telegram/bot [post]
func (r *Routes) SetBotToken(c *gin.Context) {
	// Получаем ID пользователя из контекста (должен быть установлен middleware авторизации)
	userIDInterface, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "User not authenticated"})
		return
	}

	userID, ok := userIDInterface.(int64)
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid user ID"})
		return
	}

	var req SetBotTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid request: " + err.Error()})
		return
	}

	// Валидация токена
	if !isValidToken(req.Token) {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid bot token format"})
		return
	}

	// Проверяем, не используется ли уже этот токен другим пользователем
	existingUser, err := database.GetUserByBotToken(req.Token)
	if err == nil && existingUser.ID != userID {
		c.JSON(http.StatusConflict, ErrorResponse{Error: "This bot token is already used by another user"})
		return
	}

	// Обновляем токен в базе данных
	err = database.UpdateBotToken(userID, req.Token)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to update bot token: " + err.Error()})
		return
	}

	// Создаем бота
	err = r.botManager.CreateBotForUser(req.Token, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create bot: " + err.Error()})
		return
	}

	// Устанавливаем webhook
	err = r.botManager.SetWebhook(req.Token)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Bot created but failed to set webhook: " + err.Error()})
		return
	}

	webhookURL := r.botManager.config.BaseURL + "/telegram/" + req.Token
	c.JSON(http.StatusOK, SetBotTokenResponse{
		Message:    "Bot token set successfully",
		WebhookURL: webhookURL,
	})
}

// RemoveBotToken удаляет токен бота для текущего пользователя
// @Summary Удалить токен Telegram бота
// @Description Удаляет токен Telegram бота для текущего пользователя
// @Tags telegram
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/telegram/bot [delete]
func (r *Routes) RemoveBotToken(c *gin.Context) {
	// Получаем ID пользователя из контекста
	userIDInterface, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "User not authenticated"})
		return
	}

	userID, ok := userIDInterface.(int64)
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid user ID"})
		return
	}

	// Получаем пользователя для получения токена
	user, err := database.GetUser(strconv.FormatInt(userID, 10))
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "User not found"})
		return
	}

	if user.BotToken == "" {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "No bot token found for user"})
		return
	}

	// Удаляем бота
	err = r.botManager.RemoveBot(user.BotToken)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to remove bot: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Bot token removed successfully"})
}

// GetBotStatus получает статус бота для текущего пользователя
// @Summary Получить статус Telegram бота
// @Description Получает информацию о статусе Telegram бота для текущего пользователя
// @Tags telegram
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/telegram/bot/status [get]
func (r *Routes) GetBotStatus(c *gin.Context) {
	// Получаем ID пользователя из контекста
	userIDInterface, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "User not authenticated"})
		return
	}

	userID, ok := userIDInterface.(int64)
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid user ID"})
		return
	}

	// Получаем пользователя
	user, err := database.GetUser(strconv.FormatInt(userID, 10))
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "User not found"})
		return
	}

	response := map[string]interface{}{
		"has_bot_token":   user.BotToken != "",
		"telegram_linked": user.TelegramID != 0,
	}

	if user.BotToken != "" {
		response["webhook_url"] = r.botManager.config.BaseURL + "/telegram/" + user.BotToken
	}

	c.JSON(http.StatusOK, response)
}

// HandleWebhook обрабатывает webhook от Telegram
func (r *Routes) HandleWebhook(c *gin.Context) {
	r.botManager.HandleWebhook(c)
}

// isValidToken проверяет формат токена Telegram бота
func isValidToken(token string) bool {
	// Токен Telegram бота имеет формат: число:строка_из_букв_цифр_дефисов_подчеркиваний
	// Например: 5106077210:AAEtczjlz4LAnpb5ANSvFe26lm-bxmdQeeo
	parts := strings.Split(token, ":")
	if len(parts) != 2 {
		return false
	}

	// Первая часть должна быть числом
	botID := parts[0]
	if len(botID) < 1 {
		return false
	}
	for _, char := range botID {
		if char < '0' || char > '9' {
			return false
		}
	}

	// Вторая часть должна содержать минимум 20 символов
	authToken := parts[1]
	if len(authToken) < 20 {
		return false
	}

	return true
}
