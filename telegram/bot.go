package telegram

import (
	"fmt"
	"gopds-api/logging"
	"log"
	"net/http"
	"strings"
	"sync"

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

	teleBot, err := tele.NewBot(settings)
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

	webhook := &tele.Webhook{
		Endpoint: &tele.WebhookEndpoint{
			PublicURL: webhookURL,
		},
	}

	err := bot.bot.SetWebhook(webhook)
	if err != nil {
		return fmt.Errorf("failed to set webhook: %v", err)
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

	// Remove webhook
	if err := bot.bot.RemoveWebhook(); err != nil {
		log.Printf("Warning: failed to remove webhook for bot %s: %v", maskToken(token), err)
	}

	// Stop bot
	bot.bot.Stop()

	// Clear token and telegram_id in database
	err := database.ClearBotToken(bot.userID)
	if err != nil {
		log.Printf("Warning: failed to clear bot token for user %d: %v", bot.userID, err)
	}

	// Remove from map
	delete(bm.bots, token)

	log.Printf("Bot removed successfully for user %d", bot.userID)
	return nil
}

// maskToken masks token for logging
func maskToken(token string) string {
	if len(token) < 10 {
		return "***"
	}
	return token[:10] + "***"
}
