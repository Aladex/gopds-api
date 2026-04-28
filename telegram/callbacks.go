package telegram

import (
	"context"
	"fmt"
	"gopds-api/commands"
	"gopds-api/database"
	"gopds-api/internal/posters"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"
	"io"
	"net/http"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
	"github.com/spf13/viper"
)

// CallbackHandler handles Telegram callback queries
type CallbackHandler struct {
	bot                 *Bot
	conversationManager *ConversationManager
}

// NewCallbackHandler creates a new callback handler
func NewCallbackHandler(bot *Bot, conversationManager *ConversationManager) *CallbackHandler {
	return &CallbackHandler{
		bot:                 bot,
		conversationManager: conversationManager,
	}
}

// callbackMessageInfo extracts chatID and messageID from a callback query's message.
func callbackMessageInfo(q *tgbot.CallbackQuery) (chatID int64, messageID int, ok bool) {
	if q.Message.Message == nil {
		return 0, 0, false
	}
	return q.Message.Message.Chat.ID, q.Message.Message.ID, true
}

// Handle routes callback to appropriate handler based on callback data
func (h *CallbackHandler) Handle(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update) error {
	q := update.CallbackQuery
	telegramID := q.From.ID
	callbackData := h.cleanCallbackData(q.Data)

	logging.Infof("Received callback from user %d, data: %q", telegramID, callbackData)

	if !h.bot.isAuthorizedUser(telegramID) {
		logging.Warnf("Unauthorized callback attempt from user %d", telegramID)
		_, _ = b.AnswerCallbackQuery(ctx, &tgbotapi.AnswerCallbackQueryParams{
			CallbackQueryID: q.ID,
			Text:            "Unauthorized",
		})
		return nil
	}

	switch {
	case callbackData == "prev_page" || callbackData == "next_page":
		return h.handlePagination(ctx, b, update, callbackData)
	case strings.HasPrefix(callbackData, "author:"):
		return h.handleAuthorSelection(ctx, b, update, callbackData)
	case strings.HasPrefix(callbackData, "collection:"):
		return h.handleCollectionSelection(ctx, b, update, callbackData)
	case strings.HasPrefix(callbackData, "select:"):
		return h.handleBookSelection(ctx, b, update, callbackData)
	case strings.HasPrefix(callbackData, "download:"):
		return h.handleDownload(ctx, b, update, callbackData)
	default:
		logging.Warnf("Unknown callback type received: %s from user %d", callbackData, telegramID)
		return nil
	}
}

// cleanCallbackData removes unwanted characters from callback data
func (h *CallbackHandler) cleanCallbackData(data string) string {
	data = strings.TrimSpace(data)
	data = strings.Trim(data, "\f\n\r\t")
	return data
}

// answerCallback acknowledges the callback query (no text shown to user).
func (h *CallbackHandler) answerCallback(ctx context.Context, b *tgbotapi.Bot, q *tgbot.CallbackQuery) {
	_, err := b.AnswerCallbackQuery(ctx, &tgbotapi.AnswerCallbackQueryParams{
		CallbackQueryID: q.ID,
	})
	if err != nil {
		logging.Errorf("Failed to answer callback: %v", err)
	}
}

// answerCallbackText acknowledges the callback query with a toast text.
func (h *CallbackHandler) answerCallbackText(ctx context.Context, b *tgbotapi.Bot, q *tgbot.CallbackQuery, text string) {
	_, err := b.AnswerCallbackQuery(ctx, &tgbotapi.AnswerCallbackQueryParams{
		CallbackQueryID: q.ID,
		Text:            text,
	})
	if err != nil {
		logging.Errorf("Failed to answer callback: %v", err)
	}
}

// handlePagination handles prev_page and next_page callbacks
func (h *CallbackHandler) handlePagination(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update, callbackData string) error {
	q := update.CallbackQuery
	telegramID := q.From.ID
	logging.Infof("Processing pagination callback: %s for user %d", callbackData, telegramID)

	direction := "next"
	if callbackData == "prev_page" {
		direction = "prev"
	}

	_, err := database.GetUserByTelegramID(telegramID)
	if err != nil {
		logging.Errorf("Failed to get user by telegram ID %d: %v", telegramID, err)
		h.answerCallbackText(ctx, b, q, "User not found")
		return nil
	}

	convContext, err := h.conversationManager.GetContext(h.bot.token, telegramID)
	if err != nil {
		logging.Errorf("Failed to get context for pagination: %v", err)
		h.answerCallbackText(ctx, b, q, "Error getting context")
		return nil
	}

	if convContext.SearchParams == nil {
		logging.Warnf("No search params found in context for user %d", telegramID)
		h.answerCallbackText(ctx, b, q, "No active search for navigation")
		return nil
	}

	newOffset := h.calculateNewOffset(convContext.SearchParams, direction)
	logging.Infof("Navigating from offset %d to %d for user %d",
		convContext.SearchParams.Offset, newOffset, telegramID)

	result, err := h.executeSearchWithPagination(convContext.SearchParams, telegramID, newOffset)
	if err != nil {
		logging.Errorf("Failed to execute paginated search: %v", err)
		h.answerCallbackText(ctx, b, q, "Search error")
		return nil
	}

	h.updateSearchParamsInContext(telegramID, result.SearchParams)

	return h.editMessageWithResult(ctx, b, q, result, telegramID)
}

// calculateNewOffset calculates the new offset based on direction
func (h *CallbackHandler) calculateNewOffset(params *commands.SearchParams, direction string) int {
	newOffset := params.Offset
	if direction == "next" {
		newOffset += params.Limit
	} else {
		newOffset -= params.Limit
		if newOffset < 0 {
			newOffset = 0
		}
	}
	return newOffset
}

// executeSearchWithPagination executes search based on query type
func (h *CallbackHandler) executeSearchWithPagination(
	params *commands.SearchParams,
	telegramID int64,
	newOffset int,
) (*commands.CommandResult, error) {
	processor := commands.NewCommandProcessor()

	switch params.QueryType {
	case "author":
		return processor.ExecuteFindAuthorWithPagination(params.Query, telegramID, newOffset, params.Limit)
	case "author_books":
		return processor.ExecuteFindAuthorBooksWithPagination(
			params.RefID, params.Query, telegramID, newOffset, params.Limit)
	case "combined":
		return h.executeCombinedSearch(processor, params.Query, telegramID, newOffset, params.Limit)
	case "favorites":
		return processor.ExecuteShowFavorites(telegramID, newOffset, params.Limit)
	case "collection_books":
		return processor.ExecuteCollectionBooks(params.RefID, telegramID, newOffset, params.Limit)
	case "collections":
		return processor.ExecuteShowCollections(newOffset, params.Limit)
	default:
		return processor.ExecuteFindBookWithPagination(params.Query, telegramID, newOffset, params.Limit)
	}
}

// executeCombinedSearch handles combined title+author search
func (h *CallbackHandler) executeCombinedSearch(
	processor *commands.CommandProcessor,
	query string,
	telegramID int64,
	offset, limit int,
) (*commands.CommandResult, error) {
	var title, author string

	if strings.Contains(query, " by ") {
		parts := strings.SplitN(query, " by ", 2)
		if len(parts) == 2 {
			title = strings.Trim(parts[0], "\"")
			author = parts[1]
		}
	} else {
		title = query
	}

	if title != "" && author != "" {
		return processor.ExecuteFindBookWithAuthorWithPagination(title, author, telegramID, offset, limit)
	}
	return processor.ExecuteFindBookWithPagination(title, telegramID, offset, limit)
}

// handleCollectionSelection handles collection:ID callbacks
func (h *CallbackHandler) handleCollectionSelection(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update, callbackData string) error {
	q := update.CallbackQuery
	telegramID := q.From.ID
	logging.Infof("Processing collection selection callback: %s for user %d", callbackData, telegramID)

	collectionIDStr := strings.TrimPrefix(callbackData, "collection:")
	collectionID, err := strconv.ParseInt(collectionIDStr, 10, 64)
	if err != nil {
		logging.Errorf("Invalid collection ID in callback: %s", collectionIDStr)
		h.answerCallbackText(ctx, b, q, "Invalid collection ID")
		return nil
	}

	h.answerCallback(ctx, b, q)

	processor := commands.NewCommandProcessor()
	result, err := processor.ExecuteCollectionBooks(collectionID, telegramID, 0, 5)
	if err != nil {
		logging.Errorf("Failed to get collection books for user %d: %v", telegramID, err)
		h.editOrSend(ctx, b, q, "Error loading collection books.", nil)
		return nil
	}

	h.editOrSend(ctx, b, q, result.Message, result.ReplyMarkup)

	h.updateSearchParamsInContext(telegramID, result.SearchParams)
	h.processOutgoingMessage(telegramID, result.Message)

	return nil
}

// handleAuthorSelection handles author:ID callbacks
func (h *CallbackHandler) handleAuthorSelection(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update, callbackData string) error {
	q := update.CallbackQuery
	telegramID := q.From.ID
	logging.Infof("Processing author selection callback: %s for user %d", callbackData, telegramID)

	authorIDStr := strings.TrimPrefix(callbackData, "author:")
	authorID, err := strconv.ParseInt(authorIDStr, 10, 64)
	if err != nil {
		logging.Errorf("Invalid author ID in callback: %s", authorIDStr)
		h.answerCallbackText(ctx, b, q, "Invalid author ID")
		return nil
	}

	_, err = database.GetUserByTelegramID(telegramID)
	if err != nil {
		logging.Errorf("Failed to get user by telegram ID %d: %v", telegramID, err)
		h.answerCallbackText(ctx, b, q, "User not found")
		return nil
	}

	authorRequest := models.AuthorRequest{ID: authorID}
	author, err := database.GetAuthor(authorRequest)
	if err != nil {
		logging.Errorf("Failed to get author %d: %v", authorID, err)
		h.answerCallbackText(ctx, b, q, "Author not found")
		return nil
	}

	h.answerCallback(ctx, b, q)

	processor := commands.NewCommandProcessor()
	result, err := processor.ExecuteFindAuthorBooksWithPagination(authorID, author.FullName, telegramID, 0, 5)
	if err != nil {
		logging.Errorf("Failed to get author books for user %d: %v", telegramID, err)
		h.editOrSend(ctx, b, q, "Error searching for books by this author.", nil)
		return nil
	}

	h.editOrSend(ctx, b, q, result.Message, result.ReplyMarkup)

	logging.Infof("Successfully edited message with author books for user %d", telegramID)

	h.updateSearchParamsInContext(telegramID, result.SearchParams)
	h.processOutgoingMessage(telegramID, result.Message)

	return nil
}

// handleBookSelection handles select:ID callbacks
func (h *CallbackHandler) handleBookSelection(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update, callbackData string) error {
	q := update.CallbackQuery
	telegramID := q.From.ID
	logging.Infof("Processing book selection callback: %s for user %d", callbackData, telegramID)

	bookIDStr := strings.TrimPrefix(callbackData, "select:")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		logging.Errorf("Invalid book ID in callback: %s", bookIDStr)
		h.answerCallbackText(ctx, b, q, "Invalid book ID")
		return nil
	}

	if err := h.conversationManager.UpdateSelectedBookID(h.bot.token, telegramID, bookID); err != nil {
		logging.Errorf("Failed to update selected book ID: %v", err)
	} else {
		logging.Infof("Selected book ID %d for user %d", bookID, telegramID)
	}

	book, err := database.GetBook(bookID)
	if err != nil {
		logging.Errorf("Failed to get book %d: %v", bookID, err)
		h.answerCallbackText(ctx, b, q, "Book not found")
		return nil
	}

	markup := h.buildFormatSelectionKeyboard(bookID)

	h.answerCallbackText(ctx, b, q, "Book selected")

	messageText := h.formatBookDetailsMessage(book)

	chatID, _, hasMsg := callbackMessageInfo(q)
	if !hasMsg {
		chatID = q.From.ID
	}
	coverURL := h.getBookCoverURL(book)
	if coverURL != "" && h.isCoverAvailable(coverURL) {
		_, sendErr := b.SendPhoto(ctx, &tgbotapi.SendPhotoParams{
			ChatID:    chatID,
			Photo:     &tgbot.InputFileString{Data: coverURL},
			Caption:   messageText,
			ParseMode: tgbot.ParseModeHTML,
			ReplyMarkup: &tgbot.InlineKeyboardMarkup{
				InlineKeyboard: markup.InlineKeyboard,
			},
		})
		if sendErr != nil {
			logging.Warnf("Failed to send photo for book %d, falling back to text: %v", bookID, sendErr)
			_, textErr := b.SendMessage(ctx, &tgbotapi.SendMessageParams{
				ChatID:    chatID,
				Text:      messageText,
				ParseMode: tgbot.ParseModeHTML,
				ReplyMarkup: &tgbot.InlineKeyboardMarkup{
					InlineKeyboard: markup.InlineKeyboard,
				},
			})
			if textErr != nil {
				logging.Errorf("Failed to send download options for user %d: %v", telegramID, textErr)
			}
		}
	} else {
		_, sendErr := b.SendMessage(ctx, &tgbotapi.SendMessageParams{
			ChatID:    chatID,
			Text:      messageText,
			ParseMode: tgbot.ParseModeHTML,
			ReplyMarkup: &tgbot.InlineKeyboardMarkup{
				InlineKeyboard: markup.InlineKeyboard,
			},
		})
		if sendErr != nil {
			logging.Errorf("Failed to send download options for user %d: %v", telegramID, sendErr)
			return nil
		}
	}

	h.processOutgoingMessage(telegramID, messageText)
	return nil
}

// buildFormatSelectionKeyboard builds inline keyboard for format selection
func (h *CallbackHandler) buildFormatSelectionKeyboard(bookID int64) *tgbot.InlineKeyboardMarkup {
	return &tgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]tgbot.InlineKeyboardButton{
			{
				{Text: "📄 FB2", CallbackData: fmt.Sprintf("download:fb2:%d", bookID), Style: "success"},
				{Text: "📚 EPUB", CallbackData: fmt.Sprintf("download:epub:%d", bookID), Style: "success"},
			},
			{
				{Text: "📱 MOBI", CallbackData: fmt.Sprintf("download:mobi:%d", bookID), Style: "success"},
				{Text: "🗂 ZIP", CallbackData: fmt.Sprintf("download:zip:%d", bookID), Style: "success"},
			},
		},
	}
}

// handleDownload handles download:format:ID callbacks
func (h *CallbackHandler) handleDownload(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update, callbackData string) error {
	q := update.CallbackQuery
	telegramID := q.From.ID
	logging.Infof("Processing download callback: %s for user %d", callbackData, telegramID)

	parts := strings.Split(callbackData, ":")
	if len(parts) != 3 {
		logging.Warnf("Invalid download callback format: %s", callbackData)
		h.answerCallbackText(ctx, b, q, "Invalid download request")
		return nil
	}

	format := parts[1]
	bookID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		logging.Errorf("Invalid book ID in download callback: %s", parts[2])
		h.answerCallbackText(ctx, b, q, "Invalid book ID")
		return nil
	}

	book, err := database.GetBook(bookID)
	if err != nil {
		logging.Errorf("Failed to get book %d: %v", bookID, err)
		h.answerCallbackText(ctx, b, q, "Book not found")
		return nil
	}

	h.answerCallbackText(ctx, b, q, "Готовим файл...")

	if err := h.sendBookFile(ctx, b, telegramID, book, format); err != nil {
		logging.Errorf("Failed to send book %d in format %s: %v", bookID, format, err)
		_, _ = b.SendMessage(ctx, &tgbotapi.SendMessageParams{
			ChatID: telegramID,
			Text:   "Не удалось отправить книгу. Попробуйте другой формат или позже.",
		})
	}

	return nil
}

// sendBookFile sends the book file to user in specified format
func (h *CallbackHandler) sendBookFile(ctx context.Context, b *tgbotapi.Bot, chatID int64, book models.Book, format string) error {
	if !book.Approved {
		return fmt.Errorf("book not approved for download")
	}

	format = strings.ToLower(format)
	basePath := viper.GetString("app.files_path")
	if basePath == "" {
		return fmt.Errorf("files path not configured")
	}

	zipPath := basePath + book.Path
	if !utils.FileExists(zipPath) {
		return fmt.Errorf("book file not found at %s", zipPath)
	}

	bp := utils.NewBookProcessor(book.FileName, zipPath)

	rc, fileName, err := h.getBookReader(bp, book, format)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil {
			logging.Errorf("Failed to close book reader: %v", cerr)
		}
	}()

	_, err = b.SendDocument(ctx, &tgbotapi.SendDocumentParams{
		ChatID: chatID,
		Document: &tgbot.InputFileUpload{
			Filename: fileName,
			Data:     rc,
		},
		Caption: fmt.Sprintf("📖 %s", book.Title),
		ReplyMarkup: GetMainKeyboard(),
	})
	if err != nil {
		return err
	}

	msg := fmt.Sprintf("Отправлена книга \"%s\" в формате %s", book.Title, strings.ToUpper(format))
	h.processOutgoingMessage(chatID, msg)

	return nil
}

// getBookReader returns reader and filename for specified format
func (h *CallbackHandler) getBookReader(bp *utils.BookProcessor, book models.Book, format string) (io.ReadCloser, string, error) {
	var (
		rc       io.ReadCloser
		err      error
		fileName string
	)

	switch format {
	case "fb2":
		rc, err = bp.FB2()
		fileName = fmt.Sprintf("%s.fb2", book.DownloadName())
	case "epub":
		rc, err = bp.Epub()
		fileName = fmt.Sprintf("%s.epub", book.DownloadName())
	case "mobi":
		rc, err = bp.Mobi()
		fileName = fmt.Sprintf("%s.mobi", book.DownloadName())
	case "zip":
		rc, err = bp.Zip(book.FileName)
		fileName = fmt.Sprintf("%s.zip", book.DownloadName())
	default:
		return nil, "", fmt.Errorf("unsupported format: %s", format)
	}

	if err != nil {
		return nil, "", err
	}

	return rc, fileName, nil
}

// updateSearchParamsInContext updates search params in conversation context
func (h *CallbackHandler) updateSearchParamsInContext(telegramID int64, params *commands.SearchParams) {
	if params == nil {
		return
	}
	if err := h.conversationManager.UpdateSearchParams(h.bot.token, telegramID, params); err != nil {
		logging.Errorf("Failed to update search params: %v", err)
	} else {
		logging.Infof("Updated search params in context: Offset=%d", params.Offset)
	}
}

// processOutgoingMessage adds message to conversation context
func (h *CallbackHandler) processOutgoingMessage(telegramID int64, message string) {
	if err := h.conversationManager.ProcessOutgoingMessage(h.bot.token, telegramID, message); err != nil {
		logging.Errorf("Failed to process outgoing message: %v", err)
	}
}

// editOrSend tries to edit the callback message; on failure sends a new one.
func (h *CallbackHandler) editOrSend(ctx context.Context, b *tgbotapi.Bot, q *tgbot.CallbackQuery, text string, markup *tgbot.InlineKeyboardMarkup) {
	chatID, messageID, ok := callbackMessageInfo(q)
	if !ok {
		logging.Warnf("Cannot extract message info from callback, sending new message to user %d", q.From.ID)
		h.sendMessage(ctx, b, q.From.ID, text, markup)
		return
	}

	editParams := &tgbotapi.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	}
	if markup != nil {
		editParams.ReplyMarkup = markup
	}

	_, editErr := b.EditMessageText(ctx, editParams)
	if editErr != nil {
		logging.Errorf("Failed to edit message for chat %d: %v, sending new message", chatID, editErr)
		h.sendMessage(ctx, b, chatID, text, markup)
	}
}

// sendMessage sends a text message with optional inline keyboard.
func (h *CallbackHandler) sendMessage(ctx context.Context, b *tgbotapi.Bot, chatID int64, text string, markup *tgbot.InlineKeyboardMarkup) {
	params := &tgbotapi.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	}
	if markup != nil {
		params.ReplyMarkup = markup
	}
	_, err := b.SendMessage(ctx, params)
	if err != nil {
		logging.Errorf("Failed to send message to chat %d: %v", chatID, err)
	}
}

// editMessageWithResult edits message with search result
func (h *CallbackHandler) editMessageWithResult(ctx context.Context, b *tgbotapi.Bot, q *tgbot.CallbackQuery, result *commands.CommandResult, telegramID int64) error {
	logging.Infof("Editing message for user %d with new pagination results", telegramID)

	h.answerCallback(ctx, b, q)

	chatID, messageID, ok := callbackMessageInfo(q)
	if !ok {
		logging.Warnf("Cannot extract message info from callback for user %d, sending new message", telegramID)
		h.sendMessage(ctx, b, telegramID, result.Message, result.ReplyMarkup)
		return nil
	}

	editParams := &tgbotapi.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      result.Message,
	}
	if result.ReplyMarkup != nil {
		editParams.ReplyMarkup = result.ReplyMarkup
	}

	_, editErr := b.EditMessageText(ctx, editParams)
	if editErr != nil {
		logging.Errorf("Failed to edit message for user %d: %v, sending new message", telegramID, editErr)
		h.sendMessage(ctx, b, chatID, result.Message, result.ReplyMarkup)
		return nil
	}

	logging.Infof("Successfully edited message for user %d", telegramID)
	return nil
}

// formatBookDetailsMessage formats book details for display
func (h *CallbackHandler) formatBookDetailsMessage(book models.Book) string {
	var message strings.Builder

	message.WriteString(fmt.Sprintf("<b>%s</b>\n\n", escapeHTML(book.Title)))

	if book.Annotation != "" {
		annotation := book.Annotation
		maxLength := 500
		if len(annotation) > maxLength {
			annotation = annotation[:maxLength] + "..."
		}
		message.WriteString(fmt.Sprintf("%s\n\n", escapeHTML(annotation)))
	}

	message.WriteString("Выберите формат для скачивания:")

	return message.String()
}

// getBookCoverURL returns the cover URL for a book
func (h *CallbackHandler) getBookCoverURL(book models.Book) string {
	cdn := viper.GetString("app.cdn")
	if cdn == "" {
		logging.Warn("CDN URL is not configured")
		return ""
	}

	if !book.Cover {
		return ""
	}

	coverURL := fmt.Sprintf("%s/books-posters/%s",
		cdn,
		posters.RelativePath(book.Path, book.FileName))

	return coverURL
}

// isCoverAvailable checks if the cover URL is accessible
func (h *CallbackHandler) isCoverAvailable(coverURL string) bool {
	if coverURL == "" {
		return false
	}

	resp, err := http.Head(coverURL)
	if err != nil {
		logging.Debugf("Failed to check cover availability: %v", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// escapeHTML escapes HTML special characters for Telegram
func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}
