package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"gopds-api/commands"
	"gopds-api/logging"
	"net/http"
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
	uuidToBots          map[string]*Bot // webhook_uuid -> Bot
	mutex               sync.RWMutex
	config              *Config
	conversationManager *ConversationManager
}

// Bot represents a bot linked to a system user
type Bot struct {
	token       string
	bot         *tele.Bot
	userID      int64  // ID of the user in our system who owns this bot
	webhookUUID string // UUID used in webhook URL instead of token
}

// Config contains settings for bots
type Config struct {
	BaseURL string // base URL for webhooks
}

// NewBotManager creates a new bot manager
func NewBotManager(config *Config, redisClient *redis.Client) *BotManager {
	return &BotManager{
		bots:                make(map[string]*Bot),
		uuidToBots:          make(map[string]*Bot),
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
		if user.WebhookUUID == "" {
			logging.Warnf("User %d has bot token but no webhook_uuid, skipping", user.ID)
			continue
		}

		err := bm.createBotForUser(user.BotToken, user.ID, user.WebhookUUID)
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
func (bm *BotManager) CreateBotForUser(token string, userID int64, webhookUUID string) error {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	// Check if a bot with this token already exists
	if _, exists := bm.bots[token]; exists {
		return fmt.Errorf("bot with token already exists")
	}

	return bm.createBotForUser(token, userID, webhookUUID)
}

// createBotForUser internal function for creating bot (without mutex)
func (bm *BotManager) createBotForUser(token string, userID int64, webhookUUID string) error {
	bot, err := bm.createBotInstance(token, userID)
	if err != nil {
		return fmt.Errorf("failed to create bot: %v", err)
	}

	bot.webhookUUID = webhookUUID
	bm.bots[token] = bot
	bm.uuidToBots[webhookUUID] = bot
	logging.Infof("Bot created successfully for user %d (webhook UUID: %s)", userID, webhookUUID)
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

	// Register commands in Telegram
	if err := bot.registerCommands(); err != nil {
		logging.Warnf("Failed to register commands for user %d: %v", userID, err)
		// Not a critical error, continue with bot initialization
	}

	return bot, nil
}

// PrivateChatMiddleware ensures bot only responds in private chats
func PrivateChatMiddleware() tele.MiddlewareFunc {
	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			if c.Chat() != nil && c.Chat().Type != tele.ChatPrivate {
				// For callbacks show alert, for messages send text
				if c.Callback() != nil {
					return c.Respond(&tele.CallbackResponse{
						Text:      "Бот работает только в личных сообщениях",
						ShowAlert: true,
					})
				}
				return c.Send("Этот бот работает только в личных сообщениях. Пожалуйста, напишите мне напрямую.")
			}
			return next(c)
		}
	}
}

// withAuth wraps a handler with authentication and message processing logic
// It processes incoming messages and checks if the user is authorized
// The telegramID is stored in context and can be retrieved via c.Get("telegramID").(int64)
func (b *Bot) withAuth(conversationManager *ConversationManager, handler tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		telegramID := c.Sender().ID

		// Process incoming message for context
		if err := conversationManager.ProcessIncomingMessage(b.token, c.Message()); err != nil {
			logging.Errorf("Failed to process incoming message: %v", err)
		}

		// Check exclusivity: only bot owner can use commands
		if !b.isAuthorizedUser(telegramID) {
			return nil // Ignore messages from unauthorized users
		}

		// Store telegramID in context for handler access
		c.Set("telegramID", telegramID)

		return handler(c)
	}
}

// setupHandlers sets up command handlers for the bot
func (b *Bot) setupHandlers(conversationManager *ConversationManager) {
	// Register middleware for private chat check
	b.bot.Use(PrivateChatMiddleware())

	// Handler for /start command (special case - doesn't require full authorization)
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
			response = "You are already linked to the library account!\n\nUse the keyboard buttons below to interact with the library:"
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response, GetMainKeyboard())
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

		response = "Welcome! Your account has been successfully linked to the library.\n\nUse the keyboard buttons below to interact with the library:"
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response, GetMainKeyboard())
	})

	// Handler for /context command to show current conversation context
	b.bot.Handle("/context", b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

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
	}))

	// Handler for /clear command to clear conversation context
	b.bot.Handle("/clear", b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

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
	}))

	// Handler for /search command for book search
	b.bot.Handle("/search", b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

		// Validate user is linked
		if err := b.validateUserLinked(c, conversationManager, telegramID); err != nil {
			return err
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

		processor := commands.NewCommandProcessor()
		result, err := processor.ProcessMessage(query, contextStr, telegramID)
		if err != nil {
			return b.handleCommandError(c, conversationManager, telegramID, "/search with LLM", err)
		}

		return b.processCommandResult(c, conversationManager, result, telegramID)
	}))

	// Handler for /b command - exact book search (bypasses LLM)
	b.bot.Handle("/b", b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

		// Validate user is linked
		if err := b.validateUserLinked(c, conversationManager, telegramID); err != nil {
			return err
		}

		// Get search query
		query := strings.TrimPrefix(c.Text(), "/b ")
		if query == "/b" || query == "" {
			response := "📚 Exact book search\nUsage: /b <book title>\nExample: /b Война и мир"
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Direct search without LLM
		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteDirectBookSearch(query, telegramID)
		if err != nil {
			return b.handleCommandError(c, conversationManager, telegramID, "direct book search", err)
		}

		return b.processCommandResult(c, conversationManager, result, telegramID)
	}))

	// Handler for /a command - exact author search (bypasses LLM)
	b.bot.Handle("/a", b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

		// Validate user is linked
		if err := b.validateUserLinked(c, conversationManager, telegramID); err != nil {
			return err
		}

		// Get search query
		query := strings.TrimPrefix(c.Text(), "/a ")
		if query == "/a" || query == "" {
			response := "👤 Exact author search\nUsage: /a <author name>\nExample: /a Толстой"
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Direct search without LLM
		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteDirectAuthorSearch(query)
		if err != nil {
			return b.handleCommandError(c, conversationManager, telegramID, "direct author search", err)
		}

		return b.processCommandResult(c, conversationManager, result, telegramID)
	}))

	// Handler for /ba command - exact combined search (bypasses LLM)
	b.bot.Handle("/ba", b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

		// Validate user is linked
		if err := b.validateUserLinked(c, conversationManager, telegramID); err != nil {
			return err
		}

		// Get search query
		query := strings.TrimPrefix(c.Text(), "/ba ")
		if query == "/ba" || query == "" {
			response := "📚👤 Exact book+author search\nUsage: /ba <author>: <book title>\nExample: /ba Толстой: Война и мир"
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Parse author and title from query (format: "author: title" or "author - title")
		author, title := parseAuthorTitle(query)
		if author == "" || title == "" {
			response := "📚👤 Please use format: /ba <author>: <book title>\nExample: /ba Толстой: Война и мир"
			if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
				logging.Errorf("Failed to process outgoing message: %v", err)
			}
			return c.Send(response)
		}

		// Direct search without LLM
		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteDirectCombinedSearch(title, author, telegramID)
		if err != nil {
			return b.handleCommandError(c, conversationManager, telegramID, "direct combined search", err)
		}

		return b.processCommandResult(c, conversationManager, result, telegramID)
	}))

	// Handler for /favorites command - show user's favorite books
	b.bot.Handle("/favorites", b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

		// Validate user is linked
		if err := b.validateUserLinked(c, conversationManager, telegramID); err != nil {
			return err
		}

		// Show favorites with pagination (start with page 1, 5 books per page)
		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteShowFavorites(telegramID, 0, 5)
		if err != nil {
			return b.handleCommandError(c, conversationManager, telegramID, "show favorites", err)
		}

		return b.processCommandResult(c, conversationManager, result, telegramID)
	}))

	// Handler for /donate command
	b.bot.Handle("/donate", b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

		response := "❤️ Если проект вам полезен, вы можете поддержать его развитие:\n\n" +
			"💳 *Крипто:*\n" +
			"`bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f` — Bitcoin\n" +
			"`0xD053A0fE7C450b57da9FF169620EB178644b54C9` — Ethereum\n" +
			"`TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec` — USDT (TRC-20)"

		markup := &tele.ReplyMarkup{}
		markup.Inline(
			markup.Row(markup.URL("Т-Банк", "https://tbank.ru/cf/634wAzuZc0Z")),
			markup.Row(
				markup.URL("PayPal", "https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62"),
				markup.URL("☕ Coffee", "https://www.buymeacoffee.com/aladex"),
			),
		)

		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response, markup, tele.ModeMarkdown)
	}))

	// Handler for all other messages
	b.bot.Handle(tele.OnText, b.withAuth(conversationManager, func(c tele.Context) error {
		telegramID := c.Get("telegramID").(int64)

		// Validate user is linked
		if err := b.validateUserLinked(c, conversationManager, telegramID); err != nil {
			return err
		}

		// Check if this is a keyboard button press
		if command, isKeyboardButton := GetCommandFromButtonText(c.Text()); isKeyboardButton {
			logging.Infof("Keyboard button pressed by user %d: %s -> %s", telegramID, c.Text(), command)
			return b.handleKeyboardCommand(c, conversationManager, command, telegramID)
		}

		// Check if user has a pending state (waiting for input after button press)
		userState, err := conversationManager.GetUserState(b.token, telegramID)
		if err != nil {
			logging.Errorf("Failed to get user state: %v", err)
			// Continue with normal processing
		}

		// If there's a state, execute the corresponding command directly
		if userState != "" {
			logging.Infof("User %d has state '%s', processing input: %s", telegramID, userState, c.Text())

			processor := commands.NewCommandProcessor()
			var result *commands.CommandResult
			var cmdErr error

			switch userState {
			case "waiting_for_search":
				// Execute search with LLM processing
				contextStr, _ := conversationManager.GetContextAsString(b.token, telegramID)
				result, cmdErr = processor.ProcessMessage(c.Text(), contextStr, telegramID)

			case "waiting_for_author":
				// Execute direct author search (bypasses LLM)
				result, cmdErr = processor.ExecuteDirectAuthorSearch(c.Text())

			case "waiting_for_book":
				// Execute direct book search (bypasses LLM)
				result, cmdErr = processor.ExecuteDirectBookSearch(c.Text(), telegramID)

			default:
				logging.Warnf("Unknown user state: %s", userState)
			}

			// Clear user state after processing
			if clearErr := conversationManager.ClearUserState(b.token, telegramID); clearErr != nil {
				logging.Errorf("Failed to clear user state: %v", clearErr)
			}

			// Process the result
			if cmdErr != nil {
				return b.handleCommandError(c, conversationManager, telegramID, fmt.Sprintf("state command %s", userState), cmdErr)
			}

			if result != nil {
				return b.processCommandResult(c, conversationManager, result, telegramID)
			}
		}

		// No state - process as normal message with LLM
		contextStr, err := conversationManager.GetContextAsString(b.token, telegramID)
		if err != nil {
			logging.Errorf("Failed to get context string: %v", err)
			contextStr = "" // Continue with empty context
		}

		// Create command processor and process the message with LLM
		processor := commands.NewCommandProcessor()
		result, err := processor.ProcessMessage(c.Text(), contextStr, telegramID)
		if err != nil {
			return b.handleCommandError(c, conversationManager, telegramID, "message with LLM", err)
		}

		return b.processCommandResult(c, conversationManager, result, telegramID)
	}))

	// Handler for all callback queries (pagination and book selection)
	callbackHandler := NewCallbackHandler(b, conversationManager)
	b.bot.Handle(tele.OnCallback, callbackHandler.Handle)
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

// processCommandResult processes the result from command processor and sends response
func (b *Bot) processCommandResult(c tele.Context, conversationManager *ConversationManager, result *commands.CommandResult, telegramID int64) error {
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
}

// handleCommandError handles errors from command processor
func (b *Bot) handleCommandError(c tele.Context, conversationManager *ConversationManager, telegramID int64, cmdType string, err error) error {
	logging.Errorf("Failed to execute %s: %v", cmdType, err)
	response := "An error occurred while processing the request. Please try again later."
	if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
		logging.Errorf("Failed to process outgoing message: %v", err)
	}
	return c.Send(response)
}

// validateUserLinked checks if user is linked to account and sends error if not
func (b *Bot) validateUserLinked(c tele.Context, conversationManager *ConversationManager, telegramID int64) error {
	_, err := database.GetUserByTelegramID(telegramID)
	if err != nil {
		response := "Please send /start first to link your account."
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response)
	}
	return nil
}

// isPrivateChat checks if the chat is a private chat
func (b *Bot) isPrivateChat(c tele.Context) bool {
	return c.Chat().Type == tele.ChatPrivate
}

// sendPrivateChatWarning sends a warning that the bot only works in private chats
func (b *Bot) sendPrivateChatWarning(c tele.Context) error {
	response := "⚠️ Этот бот работает только в личных сообщениях. Пожалуйста, напишите мне напрямую."
	return c.Send(response)
}

// HandleWebhook handles incoming webhooks from Telegram
func (bm *BotManager) HandleWebhook(c *gin.Context) {
	webhookUUID := c.Param("token")

	bm.mutex.RLock()
	bot, exists := bm.uuidToBots[webhookUUID]
	bm.mutex.RUnlock()

	if !exists {
		logging.Infof("Webhook received for unknown UUID: %s", webhookUUID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	// Read request body to process update
	var update tele.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		logging.Infof("Error parsing webhook for UUID %s: %v", webhookUUID, err)
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

	expectedURL := fmt.Sprintf("%s/telegram/%s", bm.config.BaseURL, bot.webhookUUID)

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
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				logging.Errorf("Failed to close response body: %v", closeErr)
			}
		}()

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

	webhookURL := fmt.Sprintf("%s/telegram/%s", bm.config.BaseURL, bot.webhookUUID)

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

	// Remove from maps first to prevent new webhook processing
	delete(bm.uuidToBots, bot.webhookUUID)
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

// StartHealthCheck starts a periodic health check goroutine for all bots
func (bm *BotManager) StartHealthCheck(ctx context.Context, interval time.Duration) {
	go func() {
		logging.Infof("Bot health check started with interval %v", interval)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logging.Info("Bot health check stopped")
				return
			case <-ticker.C:
				bm.runHealthCheck()
			}
		}
	}()
}

// runHealthCheck validates all active bots and cleans up invalid ones
func (bm *BotManager) runHealthCheck() {
	bm.mutex.RLock()
	tokens := make([]string, 0, len(bm.bots))
	botUsers := make(map[string]int64)
	for token, bot := range bm.bots {
		tokens = append(tokens, token)
		botUsers[token] = bot.userID
	}
	bm.mutex.RUnlock()

	logging.Infof("Running health check for %d bots", len(tokens))

	for _, token := range tokens {
		userID := botUsers[token]

		// Validate token via Telegram API
		valid, err := bm.validateBotToken(token)
		if err != nil {
			logging.Warnf("Health check: failed to validate token for user %d: %v", userID, err)
			continue
		}

		if !valid {
			logging.Warnf("Health check: invalid token detected for user %d, removing bot", userID)
			// Remove bot from manager
			if err := bm.RemoveBot(token); err != nil {
				logging.Errorf("Health check: failed to remove bot for user %d: %v", userID, err)
			}
			// Clear token in database
			if err := database.ClearBotToken(userID); err != nil {
				logging.Errorf("Health check: failed to clear bot token in DB for user %d: %v", userID, err)
			}
			continue
		}

		// Check webhook status
		isCorrect, err := bm.checkWebhookStatus(token)
		if err != nil {
			logging.Warnf("Health check: failed to check webhook for user %d: %v", userID, err)
			continue
		}

		if !isCorrect {
			logging.Infof("Health check: webhook misconfigured for user %d, resetting", userID)
			if err := bm.SetWebhook(token); err != nil {
				logging.Errorf("Health check: failed to set webhook for user %d: %v", userID, err)
			}
		}
	}

	logging.Info("Bot health check completed")
}

// validateBotToken checks if a bot token is valid by calling Telegram's getMe API
func (bm *BotManager) validateBotToken(token string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to call getMe: %v", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logging.Errorf("Failed to close response body: %v", closeErr)
		}
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("getMe returned status %d", resp.StatusCode)
	}

	var result struct {
		Ok bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode getMe response: %v", err)
	}

	return result.Ok, nil
}

// maskToken masks token for logging
func maskToken(token string) string {
	if len(token) < 10 {
		return "***"
	}
	return token[:10] + "***"
}

// registerCommands registers bot commands in Telegram via setMyCommands API
func (b *Bot) registerCommands() error {
	botCommands := []tele.Command{
		{
			Text:        "start",
			Description: "Link your account to the library",
		},
		{
			Text:        "search",
			Description: "Search for books using natural language",
		},
		{
			Text:        "b",
			Description: "Exact book search by title",
		},
		{
			Text:        "a",
			Description: "Exact author search by name",
		},
		{
			Text:        "ba",
			Description: "Exact combined search (author: book)",
		},
		{
			Text:        "favorites",
			Description: "Show your favorite books",
		},
		{
			Text:        "context",
			Description: "Show conversation context statistics",
		},
		{
			Text:        "clear",
			Description: "Clear conversation context",
		},
		{
			Text:        "donate",
			Description: "Support the project",
		},
	}

	// Set commands with timeout to avoid hanging
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type commandResult struct {
		err error
	}

	resultChan := make(chan commandResult, 1)
	go func() {
		err := b.bot.SetCommands(botCommands)
		resultChan <- commandResult{err: err}
	}()

	select {
	case result := <-resultChan:
		if result.err != nil {
			logging.Errorf("Failed to register commands for user %d: %v", b.userID, result.err)
			return result.err
		}
		logging.Infof("Successfully registered %d commands for user %d", len(botCommands), b.userID)
		return nil
	case <-ctx.Done():
		logging.Errorf("Timeout registering commands for user %d", b.userID)
		return fmt.Errorf("timeout registering commands")
	}
}

// handleKeyboardCommand handles commands triggered by keyboard buttons
func (b *Bot) handleKeyboardCommand(c tele.Context, conversationManager *ConversationManager, command string, telegramID int64) error {
	switch command {
	case "/search":
		// Set user state to waiting for search query
		if err := conversationManager.SetUserState(b.token, telegramID, "waiting_for_search"); err != nil {
			logging.Errorf("Failed to set user state: %v", err)
		}

		// Ask user for search query
		response := "🔍 Please enter your search query (book title or author name):"
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response)

	case "/favorites":
		// Execute favorites command directly (no state needed)
		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteShowFavorites(telegramID, 0, 5)
		if err != nil {
			return b.handleCommandError(c, conversationManager, telegramID, "show favorites", err)
		}
		return b.processCommandResultWithKeyboard(c, conversationManager, result, telegramID)

	case "/a":
		// Set user state to waiting for author name
		if err := conversationManager.SetUserState(b.token, telegramID, "waiting_for_author"); err != nil {
			logging.Errorf("Failed to set user state: %v", err)
		}

		// Ask user for author name
		response := "👤 Please enter the author name:\nExample: Толстой"
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response)

	case "/b":
		// Set user state to waiting for book title
		if err := conversationManager.SetUserState(b.token, telegramID, "waiting_for_book"); err != nil {
			logging.Errorf("Failed to set user state: %v", err)
		}

		// Ask user for book title
		response := "📚 Please enter the book title:\nExample: Война и мир"
		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response)

	case "/donate":
		response := "❤️ Если проект вам полезен, вы можете поддержать его развитие:\n\n" +
			"💳 *Крипто:*\n" +
			"`bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f` — Bitcoin\n" +
			"`0xD053A0fE7C450b57da9FF169620EB178644b54C9` — Ethereum\n" +
			"`TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec` — USDT (TRC-20)"

		markup := &tele.ReplyMarkup{}
		markup.Inline(
			markup.Row(markup.URL("Т-Банк", "https://tbank.ru/cf/634wAzuZc0Z")),
			markup.Row(
				markup.URL("PayPal", "https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62"),
				markup.URL("☕ Coffee", "https://www.buymeacoffee.com/aladex"),
			),
		)

		if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, response); err != nil {
			logging.Errorf("Failed to process outgoing message: %v", err)
		}
		return c.Send(response, markup, tele.ModeMarkdown)

	default:
		logging.Warnf("Unknown keyboard command: %s", command)
		return nil
	}
}

// processCommandResultWithKeyboard processes command result (keeping for backwards compatibility)
// Note: Main keyboard is persistent and doesn't need to be re-sent
func (b *Bot) processCommandResultWithKeyboard(c tele.Context, conversationManager *ConversationManager, result *commands.CommandResult, telegramID int64) error {
	var sendOptions []interface{}

	// Add inline markup if present (for pagination, etc.)
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
}

// parseAuthorTitle parses author and title from query string
// Supports formats: "author: title", "author - title", "author — title"
func parseAuthorTitle(query string) (author, title string) {
	// Try different separators
	separators := []string{": ", " - ", " — ", ":", "-", "—"}

	for _, sep := range separators {
		if idx := strings.Index(query, sep); idx > 0 {
			author = strings.TrimSpace(query[:idx])
			title = strings.TrimSpace(query[idx+len(sep):])
			if author != "" && title != "" {
				return author, title
			}
		}
	}

	return "", ""
}
