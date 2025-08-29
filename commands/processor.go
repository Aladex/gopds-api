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
	Authors     []models.Author
	ReplyMarkup *tele.ReplyMarkup
	// Pagination state for conversation context
	SearchParams *SearchParams
}

// SearchParams represents search parameters for pagination
type SearchParams struct {
	Query      string `json:"query"`
	QueryType  string `json:"query_type"` // "book" or "author"
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	TotalCount int    `json:"total_count"`
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
	case "find_author":
		return cp.executeFindAuthor(command.Author, userID)
	case "unknown":
		return cp.createUnknownResponse(), nil
	default:
		logging.Errorf("Unknown command: %s", command.Command)
		return cp.createUnknownResponse(), nil
	}
}

// executeFindBook executes a book search command
func (cp *CommandProcessor) executeFindBook(title string, userID int64) (*CommandResult, error) {
	return cp.executeFindBookWithPagination(title, userID, 0, 5)
}

// ExecuteFindBookWithPagination executes a book search command with pagination (exported for callback handlers)
func (cp *CommandProcessor) ExecuteFindBookWithPagination(title string, userID int64, offset, limit int) (*CommandResult, error) {
	return cp.executeFindBookWithPagination(title, userID, offset, limit)
}

// executeFindBookWithPagination executes a book search command with pagination
func (cp *CommandProcessor) executeFindBookWithPagination(title string, userID int64, offset, limit int) (*CommandResult, error) {
	if title == "" {
		return &CommandResult{
			Message: "Пожалуйста, укажите название книги для поиска.",
		}, nil
	}

	// Create filters for book search with pagination
	filters := models.BookFilters{
		Title:  title,
		Limit:  limit,
		Offset: offset,
	}

	// Search for books using the existing database function
	books, totalCount, err := database.GetBooksEnhanced(userID, filters)
	if err != nil {
		logging.Errorf("Failed to search books: %v", err)
		return &CommandResult{
			Message: "Произошла ошибка при поиске книг. Попробуйте позже.",
		}, nil
	}

	if len(books) == 0 && offset == 0 {
		return &CommandResult{
			Message: fmt.Sprintf("📚 Книги с названием \"%s\" не найдены.\n\nПопробуйте изменить запрос или использовать другие ключевые слова.", title),
		}, nil
	}

	if len(books) == 0 && offset > 0 {
		return &CommandResult{
			Message: "На этой странице нет результатов.",
		}, nil
	}

	// Format the response message with pagination info
	message := cp.formatBookSearchResultsWithPagination(title, books, totalCount, offset, limit)

	// Create inline keyboard with number-based buttons and pagination
	replyMarkup := cp.createBookButtonsWithPagination(books, offset, limit, totalCount)

	return &CommandResult{
		Message:     message,
		Books:       books,
		ReplyMarkup: replyMarkup,
		SearchParams: &SearchParams{
			Query:      title,
			Offset:     offset,
			Limit:      limit,
			TotalCount: totalCount,
		},
	}, nil
}

// executeFindAuthor executes an author search command
func (cp *CommandProcessor) executeFindAuthor(author string, userID int64) (*CommandResult, error) {
	_ = userID // Authors are global, not user-specific
	return cp.executeFindAuthorWithPagination(author, 0, 5)
}

// ExecuteFindAuthorWithPagination executes an author search command with pagination (exported for callback handlers)
func (cp *CommandProcessor) ExecuteFindAuthorWithPagination(author string, userID int64, offset, limit int) (*CommandResult, error) {
	_ = userID // Authors are global, not user-specific
	return cp.executeFindAuthorWithPagination(author, offset, limit)
}

// executeFindAuthorWithPagination executes an author search command with pagination
func (cp *CommandProcessor) executeFindAuthorWithPagination(author string, offset, limit int) (*CommandResult, error) {
	if author == "" {
		return &CommandResult{
			Message: "Пожалуйста, укажите имя автора для поиска.",
		}, nil
	}

	// Create filters for author search with pagination
	filters := models.AuthorFilters{
		Author: author,
		Limit:  limit,
		Offset: offset,
	}

	// Search for authors using the existing database function
	authors, totalCount, err := database.GetAuthors(filters)
	if err != nil {
		logging.Errorf("Failed to search authors: %v", err)
		return &CommandResult{
			Message: "Произошла ошибка при поиске авторов. Попробуйте позже.",
		}, nil
	}

	if len(authors) == 0 && offset == 0 {
		return &CommandResult{
			Message: fmt.Sprintf("👤 Авторы с именем \"%s\" не найдены.\n\nПопробуйте изменить запрос или использовать другие ключевые слова.", author),
		}, nil
	}

	if len(authors) == 0 && offset > 0 {
		return &CommandResult{
			Message: "На этой странице нет результатов.",
		}, nil
	}

	// Format the response message with pagination info
	message := cp.formatAuthorSearchResultsWithPagination(author, authors, totalCount, offset, limit)

	// Create inline keyboard with number-based buttons and pagination
	replyMarkup := cp.createAuthorButtonsWithPagination(authors, offset, limit, totalCount)

	return &CommandResult{
		Message:     message,
		Authors:     authors,
		ReplyMarkup: replyMarkup,
		SearchParams: &SearchParams{
			Query:      author,
			QueryType:  "author",
			Offset:     offset,
			Limit:      limit,
			TotalCount: totalCount,
		},
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

// formatBookSearchResultsWithPagination formats the search results into a message with pagination info
func (cp *CommandProcessor) formatBookSearchResultsWithPagination(query string, books []models.Book, totalCount, offset, limit int) string {
	var builder strings.Builder

	currentPage := (offset / limit) + 1
	totalPages := (totalCount + limit - 1) / limit

	builder.WriteString(fmt.Sprintf("📚 Результаты поиска для \"%s\":\n", query))
	builder.WriteString(fmt.Sprintf("Страница %d из %d (всего найдено %d книг)\n\n", currentPage, totalPages, totalCount))

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

		// Add book entry with correct numbering
		bookNumber := offset + i + 1
		builder.WriteString(fmt.Sprintf("%d. %s — %s", bookNumber, book.Title, authorsStr))

		// Add series information if available
		if len(book.Series) > 0 && book.Series[0].Ser != "" {
			builder.WriteString(fmt.Sprintf(" (серия: %s)", book.Series[0].Ser))
		}

		builder.WriteString("\n")
	}

	builder.WriteString("\n💡 Выберите книгу по номеру или используйте навигацию:")

	return builder.String()
}

// formatAuthorSearchResultsWithPagination formats the author search results into a message with pagination info
func (cp *CommandProcessor) formatAuthorSearchResultsWithPagination(query string, authors []models.Author, totalCount, offset, limit int) string {
	var builder strings.Builder

	currentPage := (offset / limit) + 1
	totalPages := (totalCount + limit - 1) / limit

	builder.WriteString(fmt.Sprintf("👤 Результаты поиска авторов для \"%s\":\n", query))
	builder.WriteString(fmt.Sprintf("Страница %d из %d (всего найдено %d авторов)\n\n", currentPage, totalPages, totalCount))

	for i, author := range authors {
		// Add author entry with correct numbering
		authorNumber := offset + i + 1
		builder.WriteString(fmt.Sprintf("%d. %s", authorNumber, author.FullName))
		builder.WriteString("\n")
	}

	builder.WriteString("\n💡 Выберите автора по номеру или используйте навигацию:")

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

// createBookButtonsWithPagination creates inline keyboard buttons for books with pagination
func (cp *CommandProcessor) createBookButtonsWithPagination(books []models.Book, offset, limit, totalCount int) *tele.ReplyMarkup {
	if len(books) == 0 {
		return nil
	}

	markup := &tele.ReplyMarkup{}
	var rows []tele.Row

	// Create number-based selection buttons (2-3 per row)
	var currentRow []tele.Btn
	for i, book := range books {
		bookNumber := offset + i + 1
		button := markup.Data(fmt.Sprintf("%d", bookNumber), fmt.Sprintf("select:%d", book.ID))
		currentRow = append(currentRow, button)

		// Add row when we have 3 buttons or it's the last book
		if len(currentRow) == 3 || i == len(books)-1 {
			rows = append(rows, markup.Row(currentRow...))
			currentRow = []tele.Btn{}
		}
	}

	// Add pagination buttons
	var paginationRow []tele.Btn

	// Previous page button
	if offset > 0 {
		paginationRow = append(paginationRow, markup.Data("⬅️ Назад", "prev_page"))
	}

	// Next page button
	if offset+limit < totalCount {
		paginationRow = append(paginationRow, markup.Data("➡️ Вперед", "next_page"))
	}

	if len(paginationRow) > 0 {
		rows = append(rows, markup.Row(paginationRow...))
	}

	markup.Inline(rows...)
	return markup
}

// CreateBookButtonsWithPagination creates inline keyboard buttons for books with pagination (exported for external use)
func (cp *CommandProcessor) CreateBookButtonsWithPagination(books []models.Book, offset, limit, totalCount int) *tele.ReplyMarkup {
	return cp.createBookButtonsWithPagination(books, offset, limit, totalCount)
}

// createAuthorButtonsWithPagination creates inline keyboard buttons for authors with pagination
func (cp *CommandProcessor) createAuthorButtonsWithPagination(authors []models.Author, offset, limit, totalCount int) *tele.ReplyMarkup {
	if len(authors) == 0 {
		return nil
	}

	markup := &tele.ReplyMarkup{}
	var rows []tele.Row

	// Create number-based selection buttons (2-3 per row)
	var currentRow []tele.Btn
	for i, author := range authors {
		authorNumber := offset + i + 1
		button := markup.Data(fmt.Sprintf("%d", authorNumber), fmt.Sprintf("author:%d", author.ID))
		currentRow = append(currentRow, button)

		// Add row when we have 3 buttons or it's the last author
		if len(currentRow) == 3 || i == len(authors)-1 {
			rows = append(rows, markup.Row(currentRow...))
			currentRow = []tele.Btn{}
		}
	}

	// Add pagination buttons
	var paginationRow []tele.Btn

	// Previous page button
	if offset > 0 {
		paginationRow = append(paginationRow, markup.Data("⬅️ Назад", "prev_page"))
	}

	// Next page button
	if offset+limit < totalCount {
		paginationRow = append(paginationRow, markup.Data("➡️ Вперед", "next_page"))
	}

	if len(paginationRow) > 0 {
		rows = append(rows, markup.Row(paginationRow...))
	}

	markup.Inline(rows...)
	return markup
}

// createUnknownResponse creates a response for unknown/unrelated queries
func (cp *CommandProcessor) createUnknownResponse() *CommandResult {
	return &CommandResult{
		Message: "Я не понимаю запрос. Попробуйте искать книги или авторов, например:\n\n" +
			"• Найти книгу Властелин Колец\n" +
			"• Ищу книги Толкиена\n" +
			"• Покажи авторов фантастики\n" +
			"• Книги Стругацких\n\n" +
			"Или используйте команду /search <название книги или автор>",
	}
}
