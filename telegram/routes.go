package telegram

import (
	"gopds-api/logging"
	"net/http"
	"strconv"
	"strings"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
)

// Routes contains all routes for working with Telegram bots
type Routes struct {
	botManager *BotManager
}

// NewRoutes creates a new instance of routes
func NewRoutes(botManager *BotManager) *Routes {
	return &Routes{
		botManager: botManager,
	}
}

// SetBotTokenRequest structure of the request for setting the bot token
type SetBotTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

// SetBotTokenResponse structure of the response when setting the bot token
type SetBotTokenResponse struct {
	Message    string `json:"message"`
	WebhookURL string `json:"webhook_url"`
}

// ErrorResponse error structure
type ErrorResponse struct {
	Error string `json:"error"`
}

// SetBotToken sets the bot token for the current user
// @Summary Set Telegram bot token
// @Description Sets the Telegram bot token for the current user
// @Tags telegram
// @Accept json
// @Produce json
// @Param request body SetBotTokenRequest true "Bot token"
// @Success 200 {object} SetBotTokenResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/telegram/bot [post]
func (r *Routes) SetBotToken(c *gin.Context) {
	// Get user ID from context (must be set by authorization middleware)
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

	// Token validation
	if !isValidToken(req.Token) {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid bot token format"})
		return
	}

	// Check if this token is already used by another user
	existingUser, err := database.GetUserByBotToken(req.Token)
	if err == nil && existingUser.ID != userID {
		c.JSON(http.StatusConflict, ErrorResponse{Error: "This bot token is already used by another user"})
		return
	}

	// Get current user to check if they have an existing bot token
	currentUser, err := database.GetUser(strconv.FormatInt(userID, 10))
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to get user info: " + err.Error()})
		return
	}

	// If user has an existing bot token, remove the old bot first
	if currentUser.BotToken != "" && currentUser.BotToken != req.Token {
		logging.Infof("User %d changing bot token from %s to %s", userID, maskToken(currentUser.BotToken), maskToken(req.Token))

		// Remove old bot (this will also remove its webhook)
		err = r.botManager.RemoveBot(currentUser.BotToken)
		if err != nil {
			logging.Errorf("Failed to remove old bot for user %d: %v", userID, err)
			// Continue anyway - maybe the bot was already removed
		}
	}

	// Update token in database
	err = database.UpdateBotToken(userID, req.Token)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to update bot token: " + err.Error()})
		return
	}

	// Create bot
	err = r.botManager.CreateBotForUser(req.Token, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create bot: " + err.Error()})
		return
	}

	// Set webhook
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

// RemoveBotToken removes the bot token for the current user
// @Summary Remove Telegram bot token
// @Description Removes the Telegram bot token for the current user
// @Tags telegram
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/telegram/bot [delete]
func (r *Routes) RemoveBotToken(c *gin.Context) {
	// Get user ID from context
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

	// Get user to get the token
	user, err := database.GetUser(strconv.FormatInt(userID, 10))
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "User not found"})
		return
	}

	if user.BotToken == "" {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "No bot token found for user"})
		return
	}

	logging.Infof("User %d removing bot token %s", userID, maskToken(user.BotToken))

	// Remove bot (this will also remove its webhook)
	err = r.botManager.RemoveBot(user.BotToken)
	if err != nil {
		logging.Errorf("Failed to remove bot for user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to remove bot: " + err.Error()})
		return
	}

	// Remove token from database
	err = database.UpdateBotToken(userID, "")
	if err != nil {
		logging.Errorf("Failed to clear bot token for user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Bot removed but failed to clear token from database: " + err.Error()})
		return
	}

	logging.Infof("Successfully removed bot and cleared token for user %d", userID)
	c.JSON(http.StatusOK, gin.H{"message": "Bot token removed successfully"})
}

// GetBotStatus gets the status of the bot for the current user
// @Summary Get Telegram bot status
// @Description Gets information about the status of the Telegram bot for the current user
// @Tags telegram
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/telegram/bot/status [get]
func (r *Routes) GetBotStatus(c *gin.Context) {
	// Get user ID from context
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

	// Get user
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

// HandleWebhook handles webhook from Telegram
func (r *Routes) HandleWebhook(c *gin.Context) {
	r.botManager.HandleWebhook(c)
}

// isValidToken checks the format of the Telegram bot token
func isValidToken(token string) bool {
	// Telegram bot token format: number:string_of_letters_digits_dashes_underscores
	// For example: 5106077210:AAEtczjlz4LAnpb5ANSvFe26lm-bxmdQeeo
	parts := strings.Split(token, ":")
	if len(parts) != 2 {
		return false
	}

	// First part must be a number
	botID := parts[0]
	if len(botID) < 1 {
		return false
	}
	for _, char := range botID {
		if char < '0' || char > '9' {
			return false
		}
	}

	// Second part must contain at least 20 characters
	authToken := parts[1]
	if len(authToken) < 20 {
		return false
	}

	return true
}
