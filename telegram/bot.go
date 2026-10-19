package telegram

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"gopds-api/commands"
	"gopds-api/logging"

	"gopds-api/database"

	tgbotapi "github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// The bot sees the reader's own words: a search phrase, a name, the running
// conversation. None of it belongs in a log — what debugging needs is that the
// path was taken and how much text it carried. These two functions are where
// that decision lives, so it can be pinned by a test instead of restated at
// every call site.

// logSearchContext records that a search ran with conversation context, by size.
func logSearchContext(telegramID int64, contextStr string) {
	logging.Infof("Search with context for user %d: %d context runes",
		telegramID, utf8.RuneCountInString(contextStr))
}

// logStatefulInput records that a pending state consumed an input, by size.
func logStatefulInput(telegramID int64, state, text string) {
	logging.Infof("User %d has state %q, processing %d input runes",
		telegramID, state, utf8.RuneCountInString(text))
}

// setupHandlers sets up command handlers for the bot
func (b *Bot) setupHandlers(conversationManager *ConversationManager) {
	// /start command: linking, or a reminder of the connection. It never
	// enters the conversation context: a link carries a secret.
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "start", tgbotapi.MatchTypeCommand,
		func(ctx context.Context, _ *tgbotapi.Bot, update *tgbot.Update) {
			b.handleStart(ctx, update)
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
			logSearchContext(telegramID, contextStr)
		}

		processor := b.newProcessor()
		result, err := processor.ProcessMessage(ctx, query, contextStr, telegramID)
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

		processor := b.newProcessor()
		result, err := processor.ExecuteDirectBookSearch(ctx, query, telegramID)
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

		processor := b.newProcessor()
		result, err := processor.ExecuteDirectAuthorSearch(ctx, query, telegramID)
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

		processor := b.newProcessor()
		result, err := processor.ExecuteDirectCombinedSearch(ctx, title, author, telegramID)
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

		processor := b.newProcessor()
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

		processor := b.newProcessor()
		result, err := processor.ExecuteShowCollections(0, 5)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID, "show collections", err)
			return
		}

		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, update.Message.Chat.ID)
	}))

	// /donate command
	b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, "donate", tgbotapi.MatchTypeCommand, b.withAuth(conversationManager, func(ctx context.Context, bot *tgbotapi.Bot, update *tgbot.Update, telegramID int64) {
		b.sendDonate(ctx, bot, conversationManager, telegramID, update.Message.Chat.ID)
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
			logStatefulInput(telegramID, userState, text)

			processor := b.newProcessor()
			var result *commands.CommandResult
			var cmdErr error

			switch userState {
			case "waiting_for_search":
				contextStr, _ := conversationManager.GetContextAsString(b.token, telegramID)
				result, cmdErr = processor.ProcessMessage(ctx, text, contextStr, telegramID)
			case "waiting_for_author":
				result, cmdErr = processor.ExecuteDirectAuthorSearch(ctx, text, telegramID)
			case "waiting_for_book":
				result, cmdErr = processor.ExecuteDirectBookSearch(ctx, text, telegramID)
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

		processor := b.newProcessor()
		result, err := processor.ProcessMessage(ctx, text, contextStr, telegramID)
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

// isAuthorizedUser checks if the user is the owner of this bot: the Telegram
// user of its active binding.
func (b *Bot) isAuthorizedUser(telegramID int64) bool {
	binding, err := database.GetActiveTelegramBinding(context.Background(), b.ref)
	if err != nil {
		logging.Infof("Failed to read the binding of telegram bot %d: %v", b.ref, err)
		return false
	}
	return binding != nil && binding.TGUserID == telegramID
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
			logging.Errorf("Failed to register commands for telegram bot %d: %v", b.ref, result.err)
			return result.err
		}
		logging.Infof("Successfully registered %d commands for telegram bot %d", len(botCommands), b.ref)
		// Back on the legacy router: if the bot is switched to v2 again, its
		// menu must be cleared again.
		if err := database.ResetTelegramCommandsCleared(ctx, b.ref); err != nil {
			logging.Warnf("Telegram bot %d: resetting the cleared-menu mark: %v", b.ref, err)
		}
		return nil
	case <-ctx.Done():
		logging.Errorf("Timeout registering commands for telegram bot %d", b.ref)
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
		processor := b.newProcessor()
		result, err := processor.ExecuteShowFavorites(telegramID, 0, 5)
		if err != nil {
			b.handleCommandError(ctx, bot, conversationManager, telegramID, chatID, "show favorites", err)
			return
		}
		b.processCommandResult(ctx, bot, conversationManager, result, telegramID, chatID)

	case "/collections":
		processor := b.newProcessor()
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
		b.sendDonate(ctx, bot, conversationManager, telegramID, chatID)

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

// sendDonate sends the donate message used by both the /donate slash command
// and the keyboard "❤️ Поддержать" button. HTML mode is used because Telegram's
// legacy Markdown parser silently rejects messages on the slightest format
// surprise (mismatched underscore/star, characters in code spans), and the
// previous code swallowed the SendMessage error so failures were invisible.
func (b *Bot) sendDonate(ctx context.Context, bot *tgbotapi.Bot, conversationManager *ConversationManager, telegramID int64, chatID int64) {
	const response = "❤️ Если проект вам полезен, вы можете поддержать его развитие:\n\n" +
		"💳 <b>Крипто:</b>\n" +
		"<code>bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f</code> — Bitcoin\n" +
		"<code>0xD053A0fE7C450b57da9FF169620EB178644b54C9</code> — Ethereum\n" +
		"<code>TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec</code> — USDT (TRC-20)"

	markup := &tgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]tgbot.InlineKeyboardButton{
			{{Text: "Т-Банк", URL: "https://tbank.ru/cf/634wAzuZc0Z"}},
			{
				{Text: "PayPal", URL: "https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62"},
				{Text: "☕ Coffee", URL: "https://www.buymeacoffee.com/aladex"},
			},
		},
	}

	_ = conversationManager.ProcessOutgoingMessage(b.token, telegramID, response)
	if _, err := bot.SendMessage(ctx, &tgbotapi.SendMessageParams{
		ChatID:      chatID,
		Text:        response,
		ParseMode:   tgbot.ParseModeHTML,
		ReplyMarkup: markup,
	}); err != nil {
		logging.Errorf("Failed to send donate message to user %d: %v", telegramID, err)
	}
}

// recoverMiddleware catches panics from downstream handlers so a single
// faulty update can't take down the whole bot process: the SDK runs each
// handler in its own goroutine, where an unrecovered panic would end it.
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
