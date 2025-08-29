package telegram

import (
	"context"
	"fmt"
	"gopds-api/logging"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
	tele "gopkg.in/telebot.v3"
)

// BotManager manages Telegram bots linked to system users
type BotManager struct {
	bots   map[string]*Bot // token -> Bot
	mutex  sync.RWMutex
	config *Config
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
func NewBotManager(config *Config) *BotManager {
	return &BotManager{
		bots:   make(map[string]*Bot),
		config: config,
	}
}

// InitializeExistingBots initializes bots for all users with tokens
func (bm *BotManager) InitializeExistingBots() error {
	// Get all users with bot tokens
	users, err := database.GetUsersWithBotTokens()
	if err != nil {
		return fmt.Errorf("failed to get users with bot tokens: %v", err)
	}

	log.Printf("Found %d users with bot tokens, initializing bots...", len(users))

	for _, user := range users {
		err := bm.createBotForUser(user.BotToken, user.ID)
		if err != nil {
			log.Printf("Failed to initialize bot for user %d: %v", user.ID, err)
			continue
		}

		// Set webhook
		err = bm.SetWebhook(user.BotToken)
		if err != nil {
			log.Printf("Failed to set webhook for user %d: %v", user.ID, err)
		}
	}

	log.Printf("Initialized %d telegram bots", len(bm.bots))
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
		log.Printf("tele.NewBot failed for user %d: %v", userID, err)
		return nil, err
	}

	bot := &Bot{
		token:  token,
		bot:    teleBot,
		userID: userID,
	}

	// Set up command handlers
	bot.setupHandlers()

	return bot, nil
}

// setupHandlers sets up command handlers for the bot
func (b *Bot) setupHandlers() {
	// Handler for /start command
	b.bot.Handle("/start", func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Get the owner of this bot from the database
		botOwner, err := database.GetUserByBotToken(b.token)
		if err != nil {
			log.Printf("Failed to get bot owner for token %s: %v", maskToken(b.token), err)
			return c.Send("Bot configuration error. Please contact administrator.")
		}

		// If the bot owner already has telegram_id, check exclusivity
		if botOwner.TelegramID != 0 {
			if int64(botOwner.TelegramID) != telegramID {
				// This is someone else's account - ignore
				return nil
			}
			// This is the bot owner
			return c.Send("You are already linked to the library account!")
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
			log.Printf("Failed to update telegram_id for token %s: %v", maskToken(b.token), err)
			return c.Send("Error linking account. Please try again later.")
		}

		return c.Send("Welcome! Your account has been successfully linked to the library.")
	})

	// Handler for /search command for book search
	b.bot.Handle("/search", func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Check exclusivity: only bot owner can use commands
		if !b.isAuthorizedUser(telegramID) {
			return nil // Ignore messages from unauthorized users
		}

		user, err := database.GetUserByTelegramID(telegramID)
		if err != nil {
			return c.Send("Please send /start first to link your account.")
		}

		// Get search query
		query := strings.TrimPrefix(c.Text(), "/search ")
		if query == "/search" || query == "" {
			return c.Send("Usage: /search <book title or author>")
		}

		// Book search logic will be here
		// Placeholder for now
		return c.Send(fmt.Sprintf("Searching books for: %s\nUser: %s", query, user.Login))
	})

	// Handler for all other messages
	b.bot.Handle(tele.OnText, func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Check exclusivity: only bot owner can use commands
		if !b.isAuthorizedUser(telegramID) {
			return nil // Ignore messages from unauthorized users
		}

		_, err := database.GetUserByTelegramID(telegramID)
		if err != nil {
			return c.Send("Please send /start first to link your account.")
		}

		// If this is not a command, show help
		return c.Send("Available commands:\n/start - link account\n/search <query> - search books")
	})
}

// isAuthorizedUser checks if the user is the owner of this bot
func (b *Bot) isAuthorizedUser(telegramID int64) bool {
	// Get bot owner
	botOwner, err := database.GetUserByBotToken(b.token)
	if err != nil {
		log.Printf("Failed to get bot owner for authorization check: %v", err)
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
		log.Printf("Webhook received for unknown bot token: %s", maskToken(token))
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	// Read request body to process update
	var update tele.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		log.Printf("Error parsing webhook for token %s: %v", maskToken(token), err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook data"})
		return
	}

	logging.Infof("Processing webhook for user %d, update ID: %d", bot.userID, update.ID)

	// Process update through telebot
	bot.bot.ProcessUpdate(update)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
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

	webhook := &tele.Webhook{
		Endpoint: &tele.WebhookEndpoint{
			PublicURL: webhookURL,
		},
	}

	// Set webhook with timeout to avoid hanging
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type webhookResult struct {
		err error
	}

	resultChan := make(chan webhookResult, 1)
	go func() {
		logging.Infof("Calling Telegram API to set webhook...")
		err := bot.bot.SetWebhook(webhook)
		if err != nil {
			logging.Errorf("Telegram API returned error: %v", err)
		}
		resultChan <- webhookResult{err: err}
	}()

	select {
	case result := <-resultChan:
		if result.err != nil {
			logging.Errorf("Failed to set webhook for user %d with URL %s: %v", bot.userID, webhookURL, result.err)
			return fmt.Errorf("failed to set webhook: %v", result.err)
		}
	case <-ctx.Done():
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
			log.Printf("Warning: failed to remove webhook for bot %s: %v", maskToken(token), err)
		} else {
			log.Printf("Webhook removed successfully for bot %s", maskToken(token))
		}
	case <-ctx.Done():
		log.Printf("Warning: timeout removing webhook for bot %s", maskToken(token))
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
		log.Printf("Bot stopped successfully for token %s", maskToken(token))
	case <-ctx2.Done():
		log.Printf("Warning: timeout stopping bot for token %s", maskToken(token))
	}

	log.Printf("Bot removed successfully for user %d", bot.userID)
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

// maskToken masks token for logging
func maskToken(token string) string {
	if len(token) < 10 {
		return "***"
	}
	return token[:10] + "***"
}
