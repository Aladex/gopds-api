package commands

import (
	"fmt"
	"gopds-api/database"
	"gopds-api/llm"
	"gopds-api/logging"
	"gopds-api/models"
	"strings"

	tele "gopkg.in/telebot.v3"
)

// CommandProcessor handles execution of parsed commands
type CommandProcessor struct {
	llmService *llm.LLMService
}

// CommandResult represents the result of command execution
type CommandResult struct {
	Message     string
	Books       []models.Book
	ReplyMarkup *tele.ReplyMarkup
}

// NewCommandProcessor creates a new command processor
func NewCommandProcessor() *CommandProcessor {
	return &CommandProcessor{
		llmService: llm.NewLLMService(),
	}
}

// ProcessMessage processes a user message and returns a response
func (cp *CommandProcessor) ProcessMessage(userMessage, context string, userID int64) (*CommandResult, error) {
	// Use LLM to parse the user message
	command, err := cp.llmService.ProcessQuery(userMessage, context)
	if err != nil {
		logging.Errorf("Failed to process query with LLM: %v", err)
		return cp.createUnknownResponse(), nil
	}

	// Execute the command
	return cp.executeCommand(command, userID)
}

// executeCommand executes a parsed command
func (cp *CommandProcessor) executeCommand(command *llm.Command, userID int64) (*CommandResult, error) {
	switch command.Command {
	case "find_book":
		return cp.executeFindBook(command.Title, userID)
	case "unknown":
		return cp.createUnknownResponse(), nil
	default:
		logging.Errorf("Unknown command: %s", command.Command)
		return cp.createUnknownResponse(), nil
	}
}

// executeFindBook executes a book search command
func (cp *CommandProcessor) executeFindBook(title string, userID int64) (*CommandResult, error) {
	if title == "" {
		return &CommandResult{
			Message: "Пожалуйста, укажите название книги для поиска.",
		}, nil
	}

	// Create filters for book search
	filters := models.BookFilters{
		Title:  title,
		Limit:  10,
		Offset: 0,
	}

	// Search for books using the existing database function
	books, totalCount, err := database.GetBooksEnhanced(userID, filters)
	if err != nil {
		logging.Errorf("Failed to search books: %v", err)
		return &CommandResult{
			Message: "Произошла ошибка при поиске книг. Попробуйте позже.",
		}, nil
	}

	if len(books) == 0 {
		return &CommandResult{
			Message: fmt.Sprintf("📚 Книги с названием \"%s\" не найдены.\n\nПопробуйте изменить запрос или использовать другие ключевые слова.", title),
		}, nil
	}

	// Format the response message
	message := cp.formatBookSearchResults(title, books, totalCount)

	// Create inline keyboard with book buttons
	replyMarkup := cp.createBookButtons(books)

	return &CommandResult{
		Message:     message,
		Books:       books,
		ReplyMarkup: replyMarkup,
	}, nil
}

// formatBookSearchResults formats the search results into a readable message
func (cp *CommandProcessor) formatBookSearchResults(query string, books []models.Book, totalCount int) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("📚 Результаты поиска для \"%s\":\n", query))

	if totalCount > len(books) {
		builder.WriteString(fmt.Sprintf("Показано %d из %d найденных книг\n\n", len(books), totalCount))
	} else {
		builder.WriteString(fmt.Sprintf("Найдено %d книг(и)\n\n", len(books)))
	}

	for i, book := range books {
		// Format authors
		var authorNames []string
		for _, author := range book.Authors {
			authorNames = append(authorNames, author.FullName)
		}
		authorsStr := strings.Join(authorNames, ", ")
		if authorsStr == "" {
			authorsStr = "Автор неизвестен"
		}

		// Add book entry
		builder.WriteString(fmt.Sprintf("%d. %s — %s", i+1, book.Title, authorsStr))

		// Add series information if available
		if len(book.Series) > 0 && book.Series[0].Ser != "" {
			builder.WriteString(fmt.Sprintf(" (серия: %s)", book.Series[0].Ser))
		}

		builder.WriteString("\n")
	}

	builder.WriteString("\n💡 Нажмите на название книги ниже для получения дополнительной информации.")

	return builder.String()
}

// createBookButtons creates inline keyboard buttons for books
func (cp *CommandProcessor) createBookButtons(books []models.Book) *tele.ReplyMarkup {
	if len(books) == 0 {
		return nil
	}

	markup := &tele.ReplyMarkup{}
	var rows []tele.Row

	for _, book := range books {
		// Truncate title if too long for button
		buttonText := book.Title
		if len(buttonText) > 50 {
			buttonText = buttonText[:47] + "..."
		}

		button := markup.Data(buttonText, fmt.Sprintf("book:%d", book.ID))

		// Each book gets its own row
		rows = append(rows, markup.Row(button))
	}

	markup.Inline(rows...)
	return markup
}

// createUnknownResponse creates a response for unknown/unrelated queries
func (cp *CommandProcessor) createUnknownResponse() *CommandResult {
	return &CommandResult{
		Message: "Я не понимаю запрос. Попробуйте искать книги, например:\n\n" +
			"• Найти книгу Властелин Колец\n" +
			"• Ищу книги Толкиена\n" +
			"• Покажи фантастику\n\n" +
			"Или используйте команду /search <название книги>",
	}
}
