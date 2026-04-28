package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"gopds-api/commands"
	"gopds-api/logging"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
	tgbotapi "github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// webhookSecretFromToken derives a deterministic per-bot value for the
// X-Telegram-Bot-Api-Secret-Token header. It is independent from the
// webhook URL path (UUID), so a leaked URL alone cannot forge updates.
func webhookSecretFromToken(token string) string {
	h := sha256.Sum256([]byte("webhook_secret:" + token))
	return hex.EncodeToString(h[:])[:32]
}

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
	bot         *tgbotapi.Bot
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

		// Always (re)set the webhook on startup so that the secret_token gets
		// pushed to Telegram for already-configured bots. SetWebhook is idempotent;
		// runHealthCheck still uses checkWebhookStatus shortcut in hot-path.
		if err := bm.SetWebhook(user.BotToken); err != nil {
			logging.Errorf("Failed to set webhook for user %d: %v", user.ID, err)
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

	if _, exists := bm.bots[token]; exists {
		return fmt.Errorf("bot with token already exists")
	}

	return bm.createBotForUser(token, userID, webhookUUID)
}

// createBotForUser internal function for creating bot (without mutex)
func (bm *BotManager) createBotForUser(token string, userID int64, webhookUUID string) error {
	b, err := bm.createBotInstance(token, userID)
	if err != nil {
		return fmt.Errorf("failed to create bot: %v", err)
	}

	b.webhookUUID = webhookUUID
	bm.bots[token] = b
	bm.uuidToBots[webhookUUID] = b
	logging.Infof("Bot created successfully for user %d (webhook UUID: %s)", userID, webhookUUID)
	return nil
}

// createBotInstance creates a bot instance
func (bm *BotManager) createBotInstance(token string, userID int64) (*Bot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type botResult struct {
		bot *tgbotapi.Bot
		err error
	}

	resultChan := make(chan botResult, 1)
	go func() {
		opts := []tgbotapi.Option{
			tgbotapi.WithMiddlewares(recoverMiddleware, privateChatMiddleware),
			tgbotapi.WithWebhookSecretToken(webhookSecretFromToken(token)),
			tgbotapi.WithDefaultHandler(func(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update) {
				logging.Debugf("Unhandled update type in bot for user %d", userID)
			}),
		}
		teleBot, err := tgbotapi.New(token, opts...)
		resultChan <- botResult{bot: teleBot, err: err}
	}()

	var teleBot *tgbotapi.Bot
	var err error

	select {
	case result := <-resultChan:
		teleBot = result.bot
		err = result.err
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout creating bot - token might be invalid")
	}

	if err != nil {
		logging.Infof("bot.New failed for user %d: %v", userID, err)
		return nil, err
	}

	bot := &Bot{
		token:  token,
		bot:    teleBot,
		userID: userID,
	}

	bot.setupHandlers(bm.conversationManager)

	if err := bot.registerCommands(); err != nil {
		logging.Warnf("Failed to register commands for user %d: %v", userID, err)
	}

	return bot, nil
}

// setupHandlers sets up command handlers for the bot
func (b *Bot) setupHandlers(conversationManager *ConversationManager) {
	// /start command
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "start", tgbotapi.MatchTypeCommand, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update) {
		telegramID := update.Message.From.ID

		if err := conversationManager.ProcessIncomingMessage(b.token, &IncomingMessage{
			SenderID:  update.Message.From.ID,
			Text:      update.Message.Text,
			MessageID: update.Message.ID,
		}); err != nil {
			logging.Errorf("Failed to process incoming message: %v", err)
		}

		botOwner, err := database.GetUserByBotToken(b.token)
		if err != nil {
			logging.Infof("Failed to get bot owner for token %s: %v", maskToken(b.token), err)
			response := "Bot configuration error. Please contact administrator."
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
		}

		var response string

		if botOwner.TelegramID != 0 {
			if int64(botOwner.TelegramID) != telegramID {
				return
			}
			response = "You are already linked to the library account!\n\nUse the keyboard buttons below to interact with the library:"
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessageWithReplyKeyboard(ctx, bot, update.Message.Chat.ID, response, GetMainKeyboard())
			return
		}

		existingUser, err := database.GetUserByTelegramID(telegramID)
		if err == nil && existingUser.ID != 0 && existingUser.ID != botOwner.ID {
			return
		}

		err = database.UpdateTelegramID(b.token, telegramID)
		if err != nil {
			logging.Infof("Failed to update telegram_id for token %s: %v", maskToken(b.token), err)
			response = "Error linking account. Please try again later."
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
		}

		response = "Welcome! Your account has been successfully linked to the library.\n\nUse the keyboard buttons below to interact with the library:"
		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		b.sendMessageWithReplyKeyboard(ctx, bot, update.Message.Chat.ID, response, GetMainKeyboard())
	})

	// /context command
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "context", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		stats, err := conversationManager.GetContextStats(b.token, telegramID)
		if err != nil {
			response := "Error getting context stats."
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
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

		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
	}))

	// /clear command
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "clear", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		err := conversationManager.ClearContext(b.token, telegramID)
		if err != nil {
			response := "Error clearing context."
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
		}

		response := "🗑️ Conversation context cleared successfully."
		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
	}))

	// /search command
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "search", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		if err := b.validateUserLinked(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID); err != nil {
			return
		}

		query := strings.TrimPrefix(update.Message.Text, "/search ")
		if query == "/search" || query == "" {
			response := "Usage: /search <book title or author>"
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
		}

		contextStr, err := conversationManager.GetContextAsString(b.token, telegramID)
		if err != nil {
			logging.Errorf("Failed to get context string: %v", err)
		} else if contextStr != "" {
			logging.Infof("Search with context for user %d: %s", telegramID, contextStr)
		}

		processor := commands.NewCommandProcessor()
		result, err := processor.ProcessMessage(query, contextStr, telegramID)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, "/search with LLM", err)
			return
		}

		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
	}))

	// /b command - exact book search
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "b", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		if err := b.validateUserLinked(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID); err != nil {
			return
		}

		query := strings.TrimPrefix(update.Message.Text, "/b ")
		if query == "/b" || query == "" {
			response := "📚 Exact book search\nUsage: /b <book title>\nExample: /b Война и мир"
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
		}

		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteDirectBookSearch(query, telegramID)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, "direct book search", err)
			return
		}

		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
	}))

	// /a command - exact author search
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "a", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		if err := b.validateUserLinked(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID); err != nil {
			return
		}

		query := strings.TrimPrefix(update.Message.Text, "/a ")
		if query == "/a" || query == "" {
			response := "👤 Exact author search\nUsage: /a <author name>\nExample: /a Толстой"
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
		}

		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteDirectAuthorSearch(query)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, "direct author search", err)
			return
		}

		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
	}))

	// /ba command - exact combined search
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "ba", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		if err := b.validateUserLinked(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID); err != nil {
			return
		}

		query := strings.TrimPrefix(update.Message.Text, "/ba ")
		if query == "/ba" || query == "" {
			response := "📚👤 Exact book+author search\nUsage: /ba <author>: <book title>\nExample: /ba Толстой: Война и мир"
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
		}

		author, title := parseAuthorTitle(query)
		if author == "" || title == "" {
			response := "📚👤 Please use format: /ba <author>: <book title>\nExample: /ba Толстой: Война и мир"
			_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
			b.sendMessage(ctx, bot, update.Message.Chat.ID, response, nil)
			return
		}

		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteDirectCombinedSearch(title, author, telegramID)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, "direct combined search", err)
			return
		}

		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
	}))

	// /favorites command
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "favorites", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		if err := b.validateUserLinked(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID); err != nil {
			return
		}

		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteShowFavorites(telegramID, 0, 5)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, "show favorites", err)
			return
		}

		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
	}))

	// /collections command
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "collections", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		if err := b.validateUserLinked(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID); err != nil {
			return
		}

		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteShowCollections(0, 5)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, "show collections", err)
			return
		}

		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
	}))

	// /donate command
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "donate", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		response := "❤️ Если проект вам полезен, вы можете поддержать его развитие:\n\n" +
			"💳 *Крипто:*\n" +
			"`bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f` — Bitcoin\n" +
			"`0xD053A0fE7C450b57da9FF169620EB178644b54C9` — Ethereum\n" +
			"`TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec` — USDT (TRC-20)"

		markup := &tgbot.InlineKeyboardMarkup{
			InlineKeyboard: [][]tgbot.InlineKeyboardButton{
				{
					{Text: "Т-Банк", URL: "https://tbank.ru/cf/634wAzuZc0Z"},
				},
				{
					{Text: "PayPal", URL: "https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62"},
					{Text: "☕ Coffee", URL: "https://www.buymeacoffee.com/aladex"},
				},
			},
		}

		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		_, _ = bot.SendMessage(ctx, &tgbotapi.SendMessageParams{
			ChatID:    update.Message.Chat.ID,
			Text:      response,
			ParseMode: tgbot.ParseModeMarkdown,
			ReplyMarkup: markup,
		})
	}))

	// Default handler for all text messages (keyboard buttons + free text)
	callbackHandler := NewCallbackHandler(b, conversationManager)

	b.bot.RegisterHandler(tgbotapi.HandlerTypeCallbackQueryData, "", tgbotapi.MatchTypePrefix, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update) {
		_ = callbackHandler.Handle(ctx, bot, update)
	})

	// Catch-all text handler
	b.bot.RegisterHandlerMatchFunc(func(update *tgbot.Update) bool {
		return update.Message != nil && update.Message.Text != ""
	}, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		if err := b.validateUserLinked(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID); err != nil {
			return
		}

		text := update.Message.Text

		// Check if this is a keyboard button press
		if command, isKeyboardButton := GetCommandFromButtonText(text); isKeyboardButton {
			logging.Infof("Keyboard button pressed by user %d: %s -> %s", telegramID, text, command)
			b.handleKeyboardCommand(ctx, bot, conversationManager, command, telegramID, update.Message.Chat.ID)
			return
		}

		// Check if user has a pending state
		userState, err := conversationManager.GetUserState(b.token, telegramID)
		if err != nil {
			logging.Errorf("Failed to get user state: %v", err)
		}

		if userState != "" {
			logging.Infof("User %d has state '%s', processing input: %s", telegramID, userState, text)

			processor := commands.NewCommandProcessor()
			var result *commands.CommandResult
			var cmdErr error

			switch userState {
			case "waiting_for_search":
				contextStr, _ := conversationManager.GetContextAsString(b.token, telegramID)
				result, cmdErr = processor.ProcessMessage(text, contextStr, telegramID)
			case "waiting_for_author":
				result, cmdErr = processor.ExecuteDirectAuthorSearch(text)
			case "waiting_for_book":
				result, cmdErr = processor.ExecuteDirectBookSearch(text, telegramID)
			default:
				logging.Warnf("Unknown user state: %s", userState)
			}

			_ = conversationManager.ClearUserState(b.token, telegramID)

			if cmdErr != nil {
				b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, fmt.Sprintf("state command %s", userState), cmdErr)
				return
			}

			if result != nil {
				b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
			}
			return
		}

		// No state - process as normal message with LLM
		contextStr, err := conversationManager.GetContextAsString(b.token, telegramID)
		if err != nil {
			logging.Errorf("Failed to get context string: %v", err)
			contextStr = ""
		}

		processor := commands.NewCommandProcessor()
		result, err := processor.ProcessMessage(text, contextStr, telegramID)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, "message with LLM", err)
			return
		}

		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
	}))
}

// withAuth wraps a handler with authentication and message processing logic
type authHandlerFunc func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64)

func (b *Bot) withAuth(conversationManager *ConversationManager, handler authHandlerFunc) tgbotapi.HandlerFunc {
	return func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update) {
		telegramID := update.Message.From.ID

		if !b.isAuthorizedUser(telegramID) {
			return
		}

		if err := conversationManager.ProcessIncomingMessage(b.token, &IncomingMessage{
			SenderID:  update.Message.From.ID,
			Text:      update.Message.Text,
			MessageID: update.Message.ID,
		}); err != nil {
			logging.Errorf("Failed to process incoming message: %v", err)
		}

		handler(ctx, bot, update, telegramID)
	}
}

// isAuthorizedUser checks if the user is the owner of this bot
func (b *Bot) isAuthorizedUser(telegramID int64) bool {
	botOwner, err := database.GetUserByBotToken(b.token)
	if err != nil {
		logging.Infof("Failed to get bot owner for authorization check: %v", err)
		return false
	}
	return int64(botOwner.TelegramID) == telegramID
}

// sendMessage sends a text message with optional inline keyboard.
func (b *Bot) sendMessage(ctx context.Context, bot *tgbotapi.Bot, chatID int64, text string, markup tgbot.ReplyMarkup) {
	params := &tgbotapi.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	}
	if markup != nil {
		params.ReplyMarkup = markup
	}
	if _, err := bot.SendMessage(ctx, params); err != nil {
		logging.Errorf("Failed to send message to chat %d: %v", chatID, err)
	}
}

// sendMessageWithReplyKeyboard sends a text message with a reply keyboard.
func (b *Bot) sendMessageWithReplyKeyboard(ctx context.Context, bot *tgbotapi.Bot, chatID int64, text string, keyboard *tgbot.ReplyKeyboardMarkup) {
	params := &tgbotapi.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: keyboard,
	}
	if _, err := bot.SendMessage(ctx, params); err != nil {
		logging.Errorf("Failed to send message with keyboard to chat %d: %v", chatID, err)
	}
}

// processCommandResult processes the result from command processor and sends response
func (b *Bot) processCommandResult(ctx context.Context, bot *tgbotapi.Bot, conversationManager *ConversationManager, result *commands.CommandResult, telegramID int64, chatID int64) {
	if err := conversationManager.ProcessOutgoingMessage(b.token, telegramID, result.Message); err != nil {
		logging.Errorf("Failed to process outgoing message: %v", err)
	}

	if result.SearchParams != nil {
		if err := conversationManager.UpdateSearchParams(b.token, telegramID, result.SearchParams); err != nil {
			logging.Errorf("Failed to update search params in context: %v", err)
		}
	}

	params := &tgbotapi.SendMessageParams{
		ChatID: chatID,
		Text:   result.Message,
	}
	if result.ReplyMarkup != nil {
		params.ReplyMarkup = result.ReplyMarkup
	}
	if _, err := bot.SendMessage(ctx, params); err != nil {
		logging.Errorf("Failed to send command result to chat %d: %v", chatID, err)
	}
}

// handleCommandError handles errors from command processor
func (b *Bot) handleCommandError(ctx context.Context, bot *tgbotapi.Bot, conversationManager *ConversationManager, telegramID int64, chatID int64, cmdType string, err error) {
	logging.Errorf("Failed to execute %s: %v", cmdType, err)
	response := "An error occurred while processing the request. Please try again later."
	_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
	b.sendMessage(ctx, bot, chatID, response, nil)
}

// validateUserLinked checks if user is linked to account and sends error if not
func (b *Bot) validateUserLinked(ctx context.Context, bot *tgbotapi.Bot, conversationManager *ConversationManager, telegramID int64, chatID int64) error {
	_, err := database.GetUserByTelegramID(telegramID)
	if err != nil {
		response := "Please send /start first to link your account."
		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		b.sendMessage(ctx, bot, chatID, response, nil)
		return fmt.Errorf("user not linked")
	}
	return nil
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

	if c.GetHeader("X-Telegram-Bot-Api-Secret-Token") != webhookSecretFromToken(bot.token) {
		logging.Warnf("Webhook secret mismatch for UUID %s", webhookUUID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid secret token"})
		return
	}

	var update tgbot.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		logging.Infof("Error parsing webhook for UUID %s: %v", webhookUUID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook data"})
		return
	}

	logging.Infof("Processing webhook for user %d, update ID: %d", bot.userID, update.ID)

	// Detach from gin's request context: ProcessUpdate dispatches handlers in a
	// goroutine, but gin cancels c.Request.Context() the moment we return — so
	// any outgoing Telegram API call from the handler would fail with
	// "context canceled".
	bot.bot.ProcessUpdate(context.Background(), &update)

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type webhookInfoResult struct {
		ok  bool
		url string
		err error
	}

	resultChan := make(chan webhookInfoResult, 1)
	go func() {
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

	logging.Infof("Setting webhook for user %d", bot.userID)
	logging.Infof("BaseURL configured: %s", bm.config.BaseURL)
	logging.Infof("Webhook URL: %s", webhookURL)
	logging.Infof("Bot token (masked): %s...%s", token[:5], token[len(token)-5:])

	// Note: no early-return on URL match — we cannot introspect the secret_token
	// remotely (Telegram's getWebhookInfo doesn't expose it), so always rewrite
	// the webhook to ensure the secret is in sync with the bot's token.

	// Step 1: Remove existing webhook
	logging.Infof("Step 1: Removing existing webhook for user %d...", bot.userID)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel1()

	type removeResult struct {
		err error
	}

	removeResultChan := make(chan removeResult, 1)
	go func() {
		logging.Infof("Calling Telegram API to remove existing webhook...")
		_, err := bot.bot.DeleteWebhook(ctx1, &tgbotapi.DeleteWebhookParams{})
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
		}
	case <-ctx1.Done():
		logging.Warnf("Timeout removing existing webhook for user %d (continuing anyway)", bot.userID)
	}

	// Step 2: Set new webhook
	logging.Infof("Step 2: Setting new webhook for user %d...", bot.userID)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	type webhookResult struct {
		err error
	}

	resultChan := make(chan webhookResult, 1)
	go func() {
		logging.Infof("Calling Telegram API to set new webhook...")
		_, err := bot.bot.SetWebhook(ctx2, &tgbotapi.SetWebhookParams{
			URL:         webhookURL,
			SecretToken: webhookSecretFromToken(token),
		})
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

	delete(bm.uuidToBots, bot.webhookUUID)
	delete(bm.bots, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := bot.bot.DeleteWebhook(ctx, &tgbotapi.DeleteWebhookParams{})
	if err != nil {
		logging.Errorf("Warning: failed to remove webhook for bot %s: %v", maskToken(token), err)
	} else {
		logging.Infof("Webhook removed successfully for bot %s", maskToken(token))
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

		valid, err := bm.validateBotToken(token)
		if err != nil {
			logging.Warnf("Health check: failed to validate token for user %d: %v", userID, err)
			continue
		}

		if !valid {
			logging.Warnf("Health check: invalid token detected for user %d, removing bot", userID)
			if err := bm.RemoveBot(token); err != nil {
				logging.Errorf("Health check: failed to remove bot for user %d: %v", userID, err)
			}
			if err := database.ClearBotToken(userID); err != nil {
				logging.Errorf("Health check: failed to clear bot token in DB for user %d: %v", userID, err)
			}
			continue
		}

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
	botCommands := []tgbot.BotCommand{
		{Command: "start", Description: "Link your account to the library"},
		{Command: "search", Description: "Search for books using natural language"},
		{Command: "b", Description: "Exact book search by title"},
		{Command: "a", Description: "Exact author search by name"},
		{Command: "ba", Description: "Exact combined search (author: book)"},
		{Command: "favorites", Description: "Show your favorite books"},
		{Command: "collections", Description: "Browse curated book collections"},
		{Command: "context", Description: "Show conversation context statistics"},
		{Command: "clear", Description: "Clear conversation context"},
		{Command: "donate", Description: "Support the project"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type commandResult struct {
		err error
	}

	resultChan := make(chan commandResult, 1)
	go func() {
		_, err := b.bot.SetMyCommands(ctx, &tgbotapi.SetMyCommandsParams{
			Commands: botCommands,
		})
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
func (b *Bot) handleKeyboardCommand(ctx context.Context, bot *tgbotapi.Bot, conversationManager *ConversationManager, command string, telegramID int64, chatID int64) {
	switch command {
	case "/search":
		if err := conversationManager.SetUserState(b.token, telegramID, "waiting_for_search"); err != nil {
			logging.Errorf("Failed to set user state: %v", err)
		}
		response := "🔍 Please enter your search query (book title or author name):"
		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		b.sendMessage(ctx, bot, chatID, response, nil)

	case "/favorites":
		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteShowFavorites(telegramID, 0, 5)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, chatID, "show favorites", err)
			return
		}
		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, chatID)

	case "/collections":
		processor := commands.NewCommandProcessor()
		result, err := processor.ExecuteShowCollections(0, 5)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, chatID, "show collections", err)
			return
		}
		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, chatID)

	case "/a":
		if err := conversationManager.SetUserState(b.token, telegramID, "waiting_for_author"); err != nil {
			logging.Errorf("Failed to set user state: %v", err)
		}
		response := "👤 Please enter the author name:\nExample: Толстой"
		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		b.sendMessage(ctx, bot, chatID, response, nil)

	case "/b":
		if err := conversationManager.SetUserState(b.token, telegramID, "waiting_for_book"); err != nil {
			logging.Errorf("Failed to set user state: %v", err)
		}
		response := "📚 Please enter the book title:\nExample: Война и мир"
		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		b.sendMessage(ctx, bot, chatID, response, nil)

	case "/donate":
		response := "❤️ Если проект вам полезен, вы можете поддержать его развитие:\n\n" +
			"💳 *Крипто:*\n" +
			"`bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f` — Bitcoin\n" +
			"`0xD053A0fE7C450b57da9FF169620EB178644b54C9` — Ethereum\n" +
			"`TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec` — USDT (TRC-20)"

		markup := &tgbot.InlineKeyboardMarkup{
			InlineKeyboard: [][]tgbot.InlineKeyboardButton{
				{
					{Text: "Т-Банк", URL: "https://tbank.ru/cf/634wAzuZc0Z"},
				},
				{
					{Text: "PayPal", URL: "https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62"},
					{Text: "☕ Coffee", URL: "https://www.buymeacoffee.com/aladex"},
				},
			},
		}

		_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
		_, _ = bot.SendMessage(ctx, &tgbotapi.SendMessageParams{
			ChatID:      chatID,
			Text:        response,
			ParseMode:   tgbot.ParseModeMarkdown,
			ReplyMarkup: markup,
		})

	default:
		logging.Warnf("Unknown keyboard command: %s", command)
	}
}

// parseAuthorTitle parses author and title from query string
// Supports formats: "author: title", "author - title", "author — title"
func parseAuthorTitle(query string) (author, title string) {
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

// recoverMiddleware catches panics from downstream handlers so a single
// faulty update can't take down the whole bot process. Notably, the
// go-telegram/bot library panics inside its multipart-form builder when
// InputFileUpload.Data wraps a struct-typed io.Reader (e.g. io.NopCloser),
// and that panic propagates out of its async handler goroutine.
func recoverMiddleware(next tgbotapi.HandlerFunc) tgbotapi.HandlerFunc {
	return func(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update) {
		defer func() {
			if r := recover(); r != nil {
				logging.Errorf("Bot handler panic recovered: %v\n%s", r, debug.Stack())
			}
		}()
		next(ctx, b, update)
	}
}

// privateChatMiddleware rejects updates from non-private chats.
func privateChatMiddleware(next tgbotapi.HandlerFunc) tgbotapi.HandlerFunc {
	return func(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update) {
		if update.Message != nil && update.Message.Chat.Type != tgbot.ChatTypePrivate {
			logging.Infof("Ignoring message from non-private chat %s (%d)", update.Message.Chat.Type, update.Message.Chat.ID)
			return
		}
		if update.CallbackQuery != nil {
			if update.CallbackQuery.Message.Message != nil && update.CallbackQuery.Message.Message.Chat.Type != tgbot.ChatTypePrivate {
				_, _ = b.AnswerCallbackQuery(ctx, &tgbotapi.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "Бот работает только в личных сообщениях",
					ShowAlert:       true,
				})
				return
			}
		}
		next(ctx, b, update)
	}
}
