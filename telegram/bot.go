package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"gopds-api/commands"
	"gopds-api/logging"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
	tele "gopkg.in/telebot.v3"
)

// BotManager manages Telegram bots linked to system users
type BotManager struct {
	bots                map[string]*Bot // token -> Bot
	mutex               sync.RWMutex
	config              *Config
	conversationManager *ConversationManager
}

// Bot represents a bot linked to a system user
type Bot struct {
	token  string
	bot    *tele.Bot
	userID int64 // ID of the user in our system who owns this bot
}

// Config contains settings for bots
type Config struct {
	BaseURL string // base URL for webhooks
}

// NewBotManager creates a new bot manager
func NewBotManager(config *Config, redisClient *redis.Client) *BotManager {
	return &BotManager{
		bots:                make(map[string]*Bot),
		config:              config,
		conversationManager: NewConversationManager(redisClient),
	}
}

// InitializeExistingBots initializes bots for all users with tokens
func (bm *BotManager) InitializeExistingBots() error {
	// Get all users with bot tokens
	users, err := database.GetUsersWithBotTokens()
	if err != nil {
		return fmt.Errorf("failed to get users with bot tokens: %v", err)
	}

	logging.Infof("Found %d users with bot tokens, initializing bots...", len(users))

	successfullyInitialized := 0
	for _, user := range users {
		err := bm.createBotForUser(user.BotToken, user.ID)
		if err != nil {
			logging.Errorf("Failed to initialize bot for user %d: %v", user.ID, err)
			continue
		}

		// Check if webhook is already correctly configured, only set if needed
		isCorrect, err := bm.checkWebhookStatus(user.BotToken)
		if err != nil {
			logging.Warnf("Failed to check webhook status for user %d during initialization: %v, will set webhook", user.ID, err)
			// Proceed to set webhook if we can't check status
			err = bm.SetWebhook(user.BotToken)
			if err != nil {
				logging.Errorf("Failed to set webhook for user %d: %v", user.ID, err)
			}
		} else if isCorrect {
			logging.Infof("Webhook already correctly configured for user %d, skipping webhook setup", user.ID)
		} else {
			logging.Infof("Webhook needs to be set for user %d", user.ID)
			err = bm.SetWebhook(user.BotToken)
			if err != nil {
				logging.Errorf("Failed to set webhook for user %d: %v", user.ID, err)
			}
		}

		successfullyInitialized++
	}

	logging.Infof("Initialized %d telegram bots successfully", successfullyInitialized)
	return nil
}

// CreateBotForUser creates a bot for a specific user
func (bm *BotManager) CreateBotForUser(token string, userID int64) error {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	// Check if a bot with this token already exists
	if _, exists := bm.bots[token]; exists {
		return fmt.Errorf("bot with token already exists")
	}

	return bm.createBotForUser(token, userID)
}

// createBotForUser internal function for creating bot (without mutex)
func (bm *BotManager) createBotForUser(token string, userID int64) error {
	bot, err := bm.createBotInstance(token, userID)
	if err != nil {
		return fmt.Errorf("failed to create bot: %v", err)
	}

	bm.bots[token] = bot
	logging.Infof("Bot created successfully for user %d", userID)
	return nil
}

// createBotInstance creates a bot instance
func (bm *BotManager) createBotInstance(token string, userID int64) (*Bot, error) {
	// Bot settings for webhook operation
	settings := tele.Settings{
		Token: token,
		Poller: &tele.Webhook{
			Listen: "", // We will handle webhooks through gin router
		},
	}

	// Create bot with timeout to avoid hanging on invalid tokens
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type botResult struct {
		bot *tele.Bot
		err error
	}

	resultChan := make(chan botResult, 1)
	go func() {
		teleBot, err := tele.NewBot(settings)
		resultChan <- botResult{bot: teleBot, err: err}
	}()

	var teleBot *tele.Bot
	var err error

	select {
	case result := <-resultChan:
		teleBot = result.bot
		err = result.err
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout creating bot - token might be invalid")
	}

	if err != nil {
		logging.Infof("tele.NewBot failed for user %d: %v", userID, err)
		return nil, err
	}

	bot := &Bot{
		token:  token,
		bot:    teleBot,
		userID: userID,
	}

	// Set up command handlers with conversation manager
	bot.setupHandlers(bm.conversationManager)

	return bot, nil
}

// setupHandlers sets up command handlers for the bot
func (b *Bot) setupHandlers(conversationManager *ConversationManager) {
	// Handler for /start command
	b.bot.Handle("/start", func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Process incoming message for context
		if err := conversationManager.ProcessIncomingMessage(b.token, c.Message()); err != nil {
			logging.Errorf("Failed to process incoming message: %v", err)
		}

		// Get the owner of this bot from the database
		botOwner, err := database.GetUserByBotToken(b.token)
		if err != nil {
			logging.Infof("Failed to get bot owner for token %s: %v", maskToken(b.token), err)
			response := "Bot configuration error. Please contact administrator."
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		var response string

		// If the bot owner already has telegram_id, check exclusivity
		if botOwner.TelegramID != 0 {
			if int64(botOwner.TelegramID) != telegramID {
				// This is someone else's account - ignore
				return nil
			}
			// This is the bot owner
			response = "You are already linked to the library account!"
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Check if this telegram_id is linked to another account
		existingUser, err := database.GetUserByTelegramID(telegramID)
		if err == nil && existingUser.ID != 0 && existingUser.ID != botOwner.ID {
			// This telegram_id is already linked to another account
			return nil
		}

		// Link telegram_id with the bot owner
		err = database.UpdateTelegramID(b.token, telegramID)
		if err != nil {
			logging.Infof("Failed to update telegram_id for token %s: %v", maskToken(b.token), err)
			response = "Error linking account. Please try again later."
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		response = "Welcome! Your account has been successfully linked to the library."
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response)
	})

	// Handler for /context command to show current conversation context
	b.bot.Handle("/context", func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Process incoming message for context
		if err := conversationManager.ProcessIncomingMessage(b.token, c.Message()); err != nil {
			logging.Errorf("Failed to process incoming message: %v", err)
		}

		// Check exclusivity: only bot owner can use commands
		if !b.isAuthorizedUser(telegramID) {
			return nil // Ignore messages from unauthorized users
		}

		stats, err := conversationManager.GetContextStats(b.token, telegramID)
		if err != nil {
			response := "Error getting context stats."
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		response := fmt.Sprintf("📊 Context Stats:\n"+
			"Messages: %d\n"+
			"Context length: %d/%d chars\n"+
			"User messages: %d\n"+
			"Bot messages: %d\n"+
			"Created: %v\n"+
			"Updated: %v",
			stats["messages_count"],
			stats["context_length"],
			stats["max_length"],
			stats["user_messages"],
			stats["bot_messages"],
			stats["created_at"],
			stats["updated_at"])

		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response)
	})

	// Handler for /clear command to clear conversation context
	b.bot.Handle("/clear", func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Process incoming message for context
		if err := conversationManager.ProcessIncomingMessage(b.token, c.Message()); err != nil {
			logging.Errorf("Failed to process incoming message: %v", err)
		}

		// Check exclusivity: only bot owner can use commands
		if !b.isAuthorizedUser(telegramID) {
			return nil // Ignore messages from unauthorized users
		}

		err := conversationManager.ClearContext(b.token, telegramID)
		if err != nil {
			response := "Error clearing context."
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		response := "🗑️ Conversation context cleared successfully."
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response)
	})

	// Handler for /search command for book search
	b.bot.Handle("/search", func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Process incoming message for context
		if err := conversationManager.ProcessIncomingMessage(b.token, c.Message()); err != nil {
			logging.Errorf("Failed to process incoming message: %v", err)
		}

		// Check exclusivity: only bot owner can use commands
		if !b.isAuthorizedUser(telegramID) {
			return nil // Ignore messages from unauthorized users
		}

		user, err := database.GetUserByTelegramID(telegramID)
		if err != nil {
			response := "Please send /start first to link your account."
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Get search query
		query := strings.TrimPrefix(c.Text(), "/search ")
		if query == "/search" || query == "" {
			response := "Usage: /search <book title or author>"
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Get conversation context for AI integration
		contextStr, err := conversationManager.GetContextAsString(b.token, telegramID)
		if err != nil {
			logging.Errorf("Failed to get context string: %v", err)
		} else if contextStr != "" {
			logging.Infof("Search with context for user %d: %s", telegramID, contextStr)
		}

		// Book search logic will be here
		// Placeholder for now
		response := fmt.Sprintf("🔍 Searching books for: %s\nUser: %s", query, user.Login)
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response)
	})

	// Handler for all other messages
	b.bot.Handle(tele.OnText, func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Process incoming message for context
		if err := conversationManager.ProcessIncomingMessage(b.token, c.Message()); err != nil {
			logging.Errorf("Failed to process incoming message: %v", err)
		}

		// Check exclusivity: only bot owner can use commands
		if !b.isAuthorizedUser(telegramID) {
			return nil // Ignore messages from unauthorized users
		}

		user, err := database.GetUserByTelegramID(telegramID)
		if err != nil {
			response := "Please send /start first to link your account."
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Get conversation context for LLM processing
		contextStr, err := conversationManager.GetContextAsString(b.token, telegramID)
		if err != nil {
			logging.Errorf("Failed to get context string: %v", err)
			contextStr = "" // Continue with empty context
		}

		// Create command processor and process the message with LLM
		processor := commands.NewCommandProcessor()
		result, err := processor.ProcessMessage(c.Text(), contextStr, user.ID)
		if err != nil {
			logging.Errorf("Failed to process message with LLM: %v", err)
			response := "Произошла ошибка при обработке запроса. Попробуйте позже."
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Send response with optional inline keyboard
		var sendOptions []interface{}
		if result.ReplyMarkup != nil {
			sendOptions = append(sendOptions, result.ReplyMarkup)
		}

		// Add bot message to context
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, result.Message); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}

		// Update search params in context if available
		if result.SearchParams != nil {
			if err := conversationManager.UpdateSearchParams(b.token, telegramID, result.SearchParams); err != nil {
				logging.Errorf("Failed to update search params in context: %v", err)
			}
		}

		return c.Send(result.Message, sendOptions...)
	})

	// Handler for all callback queries (pagination and book selection)
	b.bot.Handle(tele.OnCallback, func(c tele.Context) error {
		return b.handleAllCallbacks(c, conversationManager)
	})
}

// isAuthorizedUser checks if the user is the owner of this bot
func (b *Bot) isAuthorizedUser(telegramID int64) bool {
	// Get bot owner
	botOwner, err := database.GetUserByBotToken(b.token)
	if err != nil {
		logging.Infof("Failed to get bot owner for authorization check: %v", err)
		return false
	}

	// Check that telegram_id matches the owner
	return int64(botOwner.TelegramID) == telegramID
}

// HandleWebhook handles incoming webhooks from Telegram
func (bm *BotManager) HandleWebhook(c *gin.Context) {
	token := c.Param("token")

	bm.mutex.RLock()
	bot, exists := bm.bots[token]
	bm.mutex.RUnlock()

	if !exists {
		logging.Infof("Webhook received for unknown bot token: %s", maskToken(token))
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	// Read request body to process update
	var update tele.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		logging.Infof("Error parsing webhook for token %s: %v", maskToken(token), err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook data"})
		return
	}

	logging.Infof("Processing webhook for user %d, update ID: %d", bot.userID, update.ID)

	// Process update through telebot
	bot.bot.ProcessUpdate(update)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// checkWebhookStatus checks if webhook is already correctly configured
func (bm *BotManager) checkWebhookStatus(token string) (bool, error) {
	bm.mutex.RLock()
	bot, exists := bm.bots[token]
	bm.mutex.RUnlock()

	if !exists {
		return false, fmt.Errorf("bot with token not found")
	}

	expectedURL := fmt.Sprintf("%s/telegram/%s", bm.config.BaseURL, token)

	// Since telebot.v3 doesn't provide GetWebhookInfo, we'll use a different approach
	// We'll try to make a direct HTTP request to Telegram API to get webhook info
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type webhookInfoResult struct {
		ok  bool
		url string
		err error
	}

	resultChan := make(chan webhookInfoResult, 1)
	go func() {
		// Make direct HTTP request to Telegram API
		apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getWebhookInfo", token)

		client := &http.Client{Timeout: 8 * time.Second}
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			resultChan <- webhookInfoResult{ok: false, err: err}
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			resultChan <- webhookInfoResult{ok: false, err: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			resultChan <- webhookInfoResult{ok: false, err: fmt.Errorf("API returned status %d", resp.StatusCode)}
			return
		}

		// Parse the response to get webhook URL
		var result struct {
			Ok     bool `json:"ok"`
			Result struct {
				URL string `json:"url"`
			} `json:"result"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resultChan <- webhookInfoResult{ok: false, err: err}
			return
		}

		if !result.Ok {
			resultChan <- webhookInfoResult{ok: false, err: fmt.Errorf("API returned ok=false")}
			return
		}

		resultChan <- webhookInfoResult{ok: true, url: result.Result.URL, err: nil}
	}()

	select {
	case result := <-resultChan:
		if result.err != nil {
			logging.Errorf("Failed to get webhook info for user %d: %v", bot.userID, result.err)
			return false, result.err
		}

		// Check if webhook URL matches expected URL
		if result.url == expectedURL {
			logging.Infof("Webhook already correctly configured for user %d: %s", bot.userID, expectedURL)
			return true, nil
		}

		if result.url != "" {
			logging.Infof("Webhook exists but URL mismatch for user %d. Expected: %s, Current: %s",
				bot.userID, expectedURL, result.url)
		} else {
			logging.Infof("No webhook configured for user %d", bot.userID)
		}

		return false, nil
	case <-ctx.Done():
		logging.Warnf("Timeout getting webhook info for user %d", bot.userID)
		return false, fmt.Errorf("timeout getting webhook info")
	}
}

// SetWebhook sets webhook for the bot
func (bm *BotManager) SetWebhook(token string) error {
	bm.mutex.RLock()
	bot, exists := bm.bots[token]
	bm.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("bot with token not found")
	}

	webhookURL := fmt.Sprintf("%s/telegram/%s", bm.config.BaseURL, token)

	// Log the webhook configuration details
	logging.Infof("Setting webhook for user %d", bot.userID)
	logging.Infof("BaseURL configured: %s", bm.config.BaseURL)
	logging.Infof("Webhook URL: %s", webhookURL)
	logging.Infof("Bot token (masked): %s...%s", token[:5], token[len(token)-5:])

	// Check if webhook is already correctly configured
	isCorrect, err := bm.checkWebhookStatus(token)
	if err != nil {
		logging.Warnf("Failed to check webhook status for user %d: %v, proceeding with setup", bot.userID, err)
	} else if isCorrect {
		logging.Infof("Webhook already set up correctly for user %d, skipping setup", bot.userID)
		return nil
	}

	// Step 1: First remove any existing webhook
	logging.Infof("Step 1: Removing existing webhook for user %d...", bot.userID)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel1()

	type removeResult struct {
		err error
	}

	removeResultChan := make(chan removeResult, 1)
	go func() {
		logging.Infof("Calling Telegram API to remove existing webhook...")
		err := bot.bot.RemoveWebhook()
		if err != nil {
			logging.Errorf("Telegram API returned error when removing webhook: %v", err)
		} else {
			logging.Infof("Successfully removed existing webhook for user %d", bot.userID)
		}
		removeResultChan <- removeResult{err: err}
	}()

	select {
	case result := <-removeResultChan:
		if result.err != nil {
			logging.Warnf("Failed to remove existing webhook for user %d: %v (continuing anyway)", bot.userID, result.err)
			// Continue anyway - this might be the first time setting webhook
		}
	case <-ctx1.Done():
		logging.Warnf("Timeout removing existing webhook for user %d (continuing anyway)", bot.userID)
		// Continue anyway
	}

	// Step 2: Now set the new webhook
	logging.Infof("Step 2: Setting new webhook for user %d...", bot.userID)

	webhook := &tele.Webhook{
		Endpoint: &tele.WebhookEndpoint{
			PublicURL: webhookURL,
		},
	}

	// Set webhook with timeout to avoid hanging
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	type webhookResult struct {
		err error
	}

	resultChan := make(chan webhookResult, 1)
	go func() {
		logging.Infof("Calling Telegram API to set new webhook...")
		err := bot.bot.SetWebhook(webhook)
		if err != nil {
			logging.Errorf("Telegram API returned error when setting webhook: %v", err)
		}
		resultChan <- webhookResult{err: err}
	}()

	select {
	case result := <-resultChan:
		if result.err != nil {
			logging.Errorf("Failed to set webhook for user %d with URL %s: %v", bot.userID, webhookURL, result.err)
			return fmt.Errorf("failed to set webhook: %v", result.err)
		}
	case <-ctx2.Done():
		logging.Errorf("Timeout setting webhook for user %d with URL %s", bot.userID, webhookURL)
		return fmt.Errorf("timeout setting webhook - token might be invalid")
	}

	logging.Infof("Webhook set successfully for user %d: %s", bot.userID, webhookURL)
	return nil
}

// RemoveBot removes bot and clears connection with user
func (bm *BotManager) RemoveBot(token string) error {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	bot, exists := bm.bots[token]
	if !exists {
		return fmt.Errorf("bot with token not found")
	}

	logging.Infof("Starting bot removal process for user %d", bot.userID)

	// Remove from map first to prevent new webhook processing
	delete(bm.bots, token)

	// Remove webhook synchronously with timeout
	webhookDone := make(chan error, 1)
	go func() {
		webhookDone <- bot.bot.RemoveWebhook()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	select {
	case err := <-webhookDone:
		if err != nil {
			logging.Errorf("Warning: failed to remove webhook for bot %s: %v", maskToken(token), err)
		} else {
			logging.Infof("Webhook removed successfully for bot %s", maskToken(token))
		}
	case <-ctx.Done():
		logging.Infof("Warning: timeout removing webhook for bot %s", maskToken(token))
	}

	// Stop bot synchronously with timeout
	stopDone := make(chan struct{}, 1)
	go func() {
		bot.bot.Stop()
		stopDone <- struct{}{}
	}()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	select {
	case <-stopDone:
		logging.Infof("Bot stopped successfully for token %s", maskToken(token))
	case <-ctx2.Done():
		logging.Infof("Warning: timeout stopping bot for token %s", maskToken(token))
	}

	logging.Infof("Bot removed successfully for user %d", bot.userID)
	return nil
}

// GetBotCount returns the number of active bots for debugging
func (bm *BotManager) GetBotCount() int {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()
	return len(bm.bots)
}

// ListActiveBots returns list of active bot tokens for debugging
func (bm *BotManager) ListActiveBots() []string {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()

	tokens := make([]string, 0, len(bm.bots))
	for token, bot := range bm.bots {
		tokens = append(tokens, fmt.Sprintf("%s (user %d)", maskToken(token), bot.userID))
	}
	return tokens
}

// GetConversationManager returns the conversation manager instance
func (bm *BotManager) GetConversationManager() *ConversationManager {
	return bm.conversationManager
}

// GetConversationContext returns conversation context for a specific user and bot
func (bm *BotManager) GetConversationContext(token string, userID int64) (*ConversationContext, error) {
	return bm.conversationManager.GetContext(token, userID)
}

// GetConversationContextAsString returns conversation context as formatted string
func (bm *BotManager) GetConversationContextAsString(token string, userID int64) (string, error) {
	return bm.conversationManager.GetContextAsString(token, userID)
}

// ClearConversationContext clears conversation context for a specific user and bot
func (bm *BotManager) ClearConversationContext(token string, userID int64) error {
	return bm.conversationManager.ClearContext(token, userID)
}

// handleAllCallbacks handles all callback queries (pagination and book selection)
func (b *Bot) handleAllCallbacks(c tele.Context, conversationManager *ConversationManager) error {
	telegramID := c.Sender().ID
	callbackData := c.Callback().Data

	// Clean callback data from any unwanted characters
	callbackData = strings.TrimSpace(callbackData)
	callbackData = strings.Trim(callbackData, "\f\n\r\t")

	// Add detailed logging for debugging
	logging.Infof("Received callback from user %d, data: %q (cleaned: %q)", telegramID, c.Callback().Data, callbackData)

	// Check authorization
	if !b.isAuthorizedUser(telegramID) {
		logging.Warnf("Unauthorized callback attempt from user %d", telegramID)
		return c.Respond(&tele.CallbackResponse{Text: "Unauthorized"})
	}

	// Handle pagination callbacks (prev_page, next_page)
	if callbackData == "prev_page" || callbackData == "next_page" {
		logging.Infof("Processing pagination callback: %s for user %d", callbackData, telegramID)

		direction := "next"
		if callbackData == "prev_page" {
			direction = "prev"
		}

		user, err := database.GetUserByTelegramID(telegramID)
		if err != nil {
			logging.Errorf("Failed to get user by telegram ID %d: %v", telegramID, err)
			return c.Respond(&tele.CallbackResponse{Text: "Пользователь не найден"})
		}

		// Get current context to retrieve search parameters
		convContext, err := conversationManager.GetContext(b.token, telegramID)
		if err != nil {
			logging.Errorf("Failed to get context for pagination: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: "Ошибка получения контекста"})
		}

		if convContext.SearchParams == nil {
			logging.Warnf("No search params found in context for user %d", telegramID)
			return c.Respond(&tele.CallbackResponse{Text: "Нет активного поиска для навигации"})
		}

		logging.Infof("Current search params for user %d: Query=%s, Offset=%d, Limit=%d",
			telegramID, convContext.SearchParams.Query, convContext.SearchParams.Offset, convContext.SearchParams.Limit)

		// Calculate new offset based on direction
		newOffset := convContext.SearchParams.Offset
		if direction == "next" {
			newOffset += convContext.SearchParams.Limit
		} else {
			// direction == "prev"
			newOffset -= convContext.SearchParams.Limit
			if newOffset < 0 {
				newOffset = 0
			}
		}

		logging.Infof("Navigating from offset %d to %d for user %d", convContext.SearchParams.Offset, newOffset, telegramID)

		// Execute search with new pagination
		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteFindBookWithPagination(convContext.SearchParams.Query, user.ID, newOffset, convContext.SearchParams.Limit)
		if err != nil {
			logging.Errorf("Failed to execute paginated search: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: "Ошибка поиска"})
		}

		logging.Infof("Pagination search completed, found %d books", len(result.Books))

		// Update search params in context
		if result.SearchParams != nil {
			if err := conversationManager.UpdateSearchParams(b.token, telegramID, result.SearchParams); err != nil {
				logging.Errorf("Failed to update search params: %v", err)
			} else {
				logging.Infof("Updated search params in context: Offset=%d", result.SearchParams.Offset)
			}
		}

		// Edit the message with new results
		var sendOptions []interface{}
		if result.ReplyMarkup != nil {
			sendOptions = append(sendOptions, result.ReplyMarkup)
		}

		logging.Infof("Editing message for user %d with new pagination results", telegramID)

		// Add callback response first to acknowledge the callback
		err = c.Respond()
		if err != nil {
			logging.Errorf("Failed to respond to callback: %v", err)
		}

		// Edit the message with new results
		editErr := c.Edit(result.Message, sendOptions...)
		if editErr != nil {
			logging.Errorf("Failed to edit message for user %d: %v", telegramID, editErr)
			// Try to send a new message if editing fails
			_, sendErr := c.Bot().Send(c.Chat(), result.Message, sendOptions...)
			if sendErr != nil {
				logging.Errorf("Failed to send new message after edit failure for user %d: %v", telegramID, sendErr)
				return c.Respond(&tele.CallbackResponse{Text: "Ошибка обновления страницы"})
			}
			return nil
		}

		logging.Infof("Successfully edited message for user %d", telegramID)
		return nil
	}

	// Handle book selection callbacks (select:ID)
	if strings.HasPrefix(callbackData, "select:") {
		logging.Infof("Processing book selection callback: %s for user %d", callbackData, telegramID)

		bookIDStr := strings.TrimPrefix(callbackData, "select:")
		bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
		if err != nil {
			logging.Errorf("Invalid book ID in callback: %s", bookIDStr)
			return c.Respond(&tele.CallbackResponse{Text: "Неверный ID книги"})
		}

		// Update selected book ID in context
		if err := conversationManager.UpdateSelectedBookID(b.token, telegramID, bookID); err != nil {
			logging.Errorf("Failed to update selected book ID: %v", err)
		} else {
			logging.Infof("Selected book ID %d for user %d", bookID, telegramID)
		}

		// Get book information for confirmation
		responseText := fmt.Sprintf("📖 Книга выбрана (ID: %d)\n\nВ будущем здесь будет возможность скачивания.", bookID)

		return c.Respond(&tele.CallbackResponse{
			Text:      responseText,
			ShowAlert: true,
		})
	}

	// If not a known callback type, log and ignore
	logging.Warnf("Unknown callback type received: %s from user %d", callbackData, telegramID)
	return nil
}

// maskToken masks token for logging
func maskToken(token string) string {
	if len(token) < 10 {
		return "***"
	}
	return token[:10] + "***"
}
