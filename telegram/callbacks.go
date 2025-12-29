package telegram

import (
	"fmt"
	"gopds-api/commands"
	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	tele "gopkg.in/telebot.v3"
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

// Handle routes callback to appropriate handler based on callback data
func (h *CallbackHandler) Handle(c tele.Context) error {
	telegramID := c.Sender().ID
	callbackData := h.cleanCallbackData(c.Callback().Data)

	logging.Infof("Received callback from user %d, data: %q", telegramID, callbackData)

	if !h.bot.isAuthorizedUser(telegramID) {
		logging.Warnf("Unauthorized callback attempt from user %d", telegramID)
		return c.Respond(&tele.CallbackResponse{Text: "Unauthorized"})
	}

	switch {
	case callbackData == "prev_page" || callbackData == "next_page":
		return h.handlePagination(c, callbackData)
	case strings.HasPrefix(callbackData, "author:"):
		return h.handleAuthorSelection(c, callbackData)
	case strings.HasPrefix(callbackData, "select:"):
		return h.handleBookSelection(c, callbackData)
	case strings.HasPrefix(callbackData, "download:"):
		return h.handleDownload(c, callbackData)
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

// handlePagination handles prev_page and next_page callbacks
func (h *CallbackHandler) handlePagination(c tele.Context, callbackData string) error {
	telegramID := c.Sender().ID
	logging.Infof("Processing pagination callback: %s for user %d", callbackData, telegramID)

	direction := "next"
	if callbackData == "prev_page" {
		direction = "prev"
	}

	_, err := database.GetUserByTelegramID(telegramID)
	if err != nil {
		logging.Errorf("Failed to get user by telegram ID %d: %v", telegramID, err)
		return c.Respond(&tele.CallbackResponse{Text: "User not found"})
	}

	convContext, err := h.conversationManager.GetContext(h.bot.token, telegramID)
	if err != nil {
		logging.Errorf("Failed to get context for pagination: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: "Error getting context"})
	}

	if convContext.SearchParams == nil {
		logging.Warnf("No search params found in context for user %d", telegramID)
		return c.Respond(&tele.CallbackResponse{Text: "No active search for navigation"})
	}

	newOffset := h.calculateNewOffset(convContext.SearchParams, direction)
	logging.Infof("Navigating from offset %d to %d for user %d",
		convContext.SearchParams.Offset, newOffset, telegramID)

	result, err := h.executeSearchWithPagination(convContext.SearchParams, telegramID, newOffset)
	if err != nil {
		logging.Errorf("Failed to execute paginated search: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: "Search error"})
	}

	h.updateSearchParamsInContext(telegramID, result.SearchParams)

	return h.editMessageWithResult(c, result, telegramID)
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
			params.AuthorID, params.Query, telegramID, newOffset, params.Limit)
	case "combined":
		return h.executeCombinedSearch(processor, params.Query, telegramID, newOffset, params.Limit)
	case "favorites":
		return processor.ExecuteShowFavorites(telegramID, newOffset, params.Limit)
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

// handleAuthorSelection handles author:ID callbacks
func (h *CallbackHandler) handleAuthorSelection(c tele.Context, callbackData string) error {
	telegramID := c.Sender().ID
	logging.Infof("Processing author selection callback: %s for user %d", callbackData, telegramID)

	authorIDStr := strings.TrimPrefix(callbackData, "author:")
	authorID, err := strconv.ParseInt(authorIDStr, 10, 64)
	if err != nil {
		logging.Errorf("Invalid author ID in callback: %s", authorIDStr)
		return c.Respond(&tele.CallbackResponse{Text: "Invalid author ID"})
	}

	_, err = database.GetUserByTelegramID(telegramID)
	if err != nil {
		logging.Errorf("Failed to get user by telegram ID %d: %v", telegramID, err)
		return c.Respond(&tele.CallbackResponse{Text: "User not found"})
	}

	authorRequest := models.AuthorRequest{ID: authorID}
	author, err := database.GetAuthor(authorRequest)
	if err != nil {
		logging.Errorf("Failed to get author %d: %v", authorID, err)
		return c.Respond(&tele.CallbackResponse{Text: "Author not found"})
	}

	if err := c.Respond(); err != nil {
		logging.Errorf("Failed to respond to callback: %v", err)
	}

	processor := commands.NewCommandProcessor()
	result, err := processor.ExecuteFindAuthorBooksWithPagination(authorID, author.FullName, telegramID, 0, 5)
	if err != nil {
		logging.Errorf("Failed to get author books for user %d: %v", telegramID, err)
		if editErr := c.Edit("Error searching for books by this author."); editErr != nil {
			logging.Errorf("Failed to edit message with error for user %d: %v", telegramID, editErr)
		}
		return nil
	}

	var sendOptions []interface{}
	if result.ReplyMarkup != nil {
		sendOptions = append(sendOptions, result.ReplyMarkup)
	}

	if editErr := c.Edit(result.Message, sendOptions...); editErr != nil {
		logging.Errorf("Failed to edit message for user %d: %v", telegramID, editErr)
		// Send new message as fallback
		if _, sendErr := c.Bot().Send(c.Chat(), result.Message, sendOptions...); sendErr != nil {
			logging.Errorf("Failed to send new message after edit failure for user %d: %v", telegramID, sendErr)
		}
		return nil
	}

	logging.Infof("Successfully edited message with author books for user %d", telegramID)

	h.updateSearchParamsInContext(telegramID, result.SearchParams)
	h.processOutgoingMessage(telegramID, result.Message)

	return nil
}

// handleBookSelection handles select:ID callbacks
func (h *CallbackHandler) handleBookSelection(c tele.Context, callbackData string) error {
	telegramID := c.Sender().ID
	logging.Infof("Processing book selection callback: %s for user %d", callbackData, telegramID)

	bookIDStr := strings.TrimPrefix(callbackData, "select:")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		logging.Errorf("Invalid book ID in callback: %s", bookIDStr)
		return c.Respond(&tele.CallbackResponse{Text: "Invalid book ID"})
	}

	if err := h.conversationManager.UpdateSelectedBookID(h.bot.token, telegramID, bookID); err != nil {
		logging.Errorf("Failed to update selected book ID: %v", err)
	} else {
		logging.Infof("Selected book ID %d for user %d", bookID, telegramID)
	}

	book, err := database.GetBook(bookID)
	if err != nil {
		logging.Errorf("Failed to get book %d: %v", bookID, err)
		return c.Respond(&tele.CallbackResponse{Text: "Book not found"})
	}

	markup := h.buildFormatSelectionKeyboard(bookID)

	if err := c.Respond(&tele.CallbackResponse{Text: "Book selected"}); err != nil {
		logging.Errorf("Failed to respond to selection callback: %v", err)
	}

	// Build message with book details
	messageText := h.formatBookDetailsMessage(book)

	// Try to get and send book cover
	coverURL := h.getBookCoverURL(book)
	if coverURL != "" && h.isCoverAvailable(coverURL) {
		// Send photo with caption and inline keyboard
		photo := &tele.Photo{
			File:    tele.FromURL(coverURL),
			Caption: messageText,
		}
		// Set ReplyMarkup in SendOptions for inline buttons to appear
		sendOpts := &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: markup,
		}
		if _, sendErr := c.Bot().Send(c.Chat(), photo, sendOpts); sendErr != nil {
			logging.Warnf("Failed to send photo for book %d, falling back to text: %v", bookID, sendErr)
			// Fallback to text message
			if _, sendErr := c.Bot().Send(c.Chat(), messageText, sendOpts); sendErr != nil {
				logging.Errorf("Failed to send download options for user %d: %v", telegramID, sendErr)
				return nil
			}
		}
	} else {
		// No cover available, send text message
		sendOpts := &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: markup,
		}
		if _, sendErr := c.Bot().Send(c.Chat(), messageText, sendOpts); sendErr != nil {
			logging.Errorf("Failed to send download options for user %d: %v", telegramID, sendErr)
			return nil
		}
	}

	h.processOutgoingMessage(telegramID, messageText)
	return nil
}

// buildFormatSelectionKeyboard builds inline keyboard for format selection
func (h *CallbackHandler) buildFormatSelectionKeyboard(bookID int64) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	btnFB2 := markup.Data("📄 FB2", fmt.Sprintf("download:fb2:%d", bookID))
	btnEPUB := markup.Data("📚 EPUB", fmt.Sprintf("download:epub:%d", bookID))
	btnMOBI := markup.Data("📱 MOBI", fmt.Sprintf("download:mobi:%d", bookID))
	btnZIP := markup.Data("🗂 ZIP", fmt.Sprintf("download:zip:%d", bookID))
	markup.Inline(
		markup.Row(btnFB2, btnEPUB),
		markup.Row(btnMOBI, btnZIP),
	)
	return markup
}

// handleDownload handles download:format:ID callbacks
func (h *CallbackHandler) handleDownload(c tele.Context, callbackData string) error {
	telegramID := c.Sender().ID
	logging.Infof("Processing download callback: %s for user %d", callbackData, telegramID)

	parts := strings.Split(callbackData, ":")
	if len(parts) != 3 {
		logging.Warnf("Invalid download callback format: %s", callbackData)
		return c.Respond(&tele.CallbackResponse{Text: "Invalid download request"})
	}

	format := parts[1]
	bookID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		logging.Errorf("Invalid book ID in download callback: %s", parts[2])
		return c.Respond(&tele.CallbackResponse{Text: "Invalid book ID"})
	}

	book, err := database.GetBook(bookID)
	if err != nil {
		logging.Errorf("Failed to get book %d: %v", bookID, err)
		return c.Respond(&tele.CallbackResponse{Text: "Book not found"})
	}

	if err := c.Respond(&tele.CallbackResponse{Text: "Готовим файл..."}); err != nil {
		logging.Errorf("Failed to respond to download callback: %v", err)
	}

	if err := h.sendBookFile(c, book, format); err != nil {
		logging.Errorf("Failed to send book %d in format %s: %v", bookID, format, err)
		if _, sendErr := c.Bot().Send(c.Chat(), "Не удалось отправить книгу. Попробуйте другой формат или позже."); sendErr != nil {
			logging.Errorf("Failed to send error message: %v", sendErr)
		}
	}

	return nil
}

// sendBookFile sends the book file to user in specified format
func (h *CallbackHandler) sendBookFile(c tele.Context, book models.Book, format string) error {
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

	doc := &tele.Document{
		File:     tele.FromReader(rc),
		FileName: fileName,
		Caption:  fmt.Sprintf("📖 %s", book.Title),
	}

	if _, err := c.Bot().Send(c.Chat(), doc, GetMainKeyboard()); err != nil {
		return err
	}

	msg := fmt.Sprintf("Отправлена книга \"%s\" в формате %s", book.Title, strings.ToUpper(format))
	h.processOutgoingMessage(c.Sender().ID, msg)

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

// editMessageWithResult edits message with search result
func (h *CallbackHandler) editMessageWithResult(c tele.Context, result *commands.CommandResult, telegramID int64) error {
	var sendOptions []interface{}
	if result.ReplyMarkup != nil {
		sendOptions = append(sendOptions, result.ReplyMarkup)
	}

	logging.Infof("Editing message for user %d with new pagination results", telegramID)

	if err := c.Respond(); err != nil {
		logging.Errorf("Failed to respond to callback: %v", err)
	}

	if editErr := c.Edit(result.Message, sendOptions...); editErr != nil {
		logging.Errorf("Failed to edit message for user %d: %v", telegramID, editErr)
		// Send new message as fallback
		if _, sendErr := c.Bot().Send(c.Chat(), result.Message, sendOptions...); sendErr != nil {
			logging.Errorf("Failed to send new message after edit failure for user %d: %v", telegramID, sendErr)
			return c.Respond(&tele.CallbackResponse{Text: "Error updating page"})
		}
		return nil
	}

	logging.Infof("Successfully edited message for user %d", telegramID)
	return nil
}

// formatBookDetailsMessage formats book details for display
func (h *CallbackHandler) formatBookDetailsMessage(book models.Book) string {
	var message strings.Builder

	// Title (bold)
	message.WriteString(fmt.Sprintf("<b>%s</b>\n\n", escapeHTML(book.Title)))

	// Description if available
	if book.Annotation != "" {
		annotation := book.Annotation
		// Limit annotation length to avoid too long messages
		maxLength := 500
		if len(annotation) > maxLength {
			annotation = annotation[:maxLength] + "..."
		}
		message.WriteString(fmt.Sprintf("%s\n\n", escapeHTML(annotation)))
	}

	// Format selection prompt
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

	// Build cover URL based on book path and filename
	// Format: /books-posters/{path}/{filename}.jpg
	coverURL := fmt.Sprintf("%s/books-posters/%s/%s.jpg",
		cdn,
		strings.ReplaceAll(book.Path, ".", "-"),
		strings.ReplaceAll(book.FileName, ".", "-"))

	return coverURL
}

// isCoverAvailable checks if the cover URL is accessible
func (h *CallbackHandler) isCoverAvailable(coverURL string) bool {
	if coverURL == "" {
		return false
	}

	// Make a HEAD request to check if the cover exists
	resp, err := http.Head(coverURL)
	if err != nil {
		logging.Debugf("Failed to check cover availability: %v", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	// Check if the response is successful (2xx status code)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// escapeHTML escapes HTML special characters for Telegram
func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}
