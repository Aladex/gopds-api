package commands

import (
	"context"
	"fmt"
	"gopds-api/database"
	"gopds-api/llm"
	"gopds-api/logging"
	"gopds-api/models"
	"strings"

	tgbot "github.com/go-telegram/bot/models"
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
	ReplyMarkup *tgbot.InlineKeyboardMarkup
	// Pagination state for conversation context
	SearchParams *SearchParams
}

// SearchParams represents search parameters for pagination
type SearchParams struct {
	Query      string `json:"query"`
	QueryType  string `json:"query_type"`          // "book", "author", or "author_books"
	RefID      int64  `json:"ref_id,omitempty"`  // ID of related entity (author, collection, etc.)
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
	case "find_book_with_author":
		return cp.executeFindBookWithAuthor(command.Title, command.Author, userID)
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
			Message: "Please specify a book title to search for.",
		}, nil
	}

	// Get user's language preference and internal ID
	user, err := database.GetUserByTelegramID(userID)
	if err != nil {
		logging.Warnf("Failed to get user language preference for user %d: %v", userID, err)
		return &CommandResult{
			Message: "Не удалось найти пользователя. Перепривяжите Telegram в настройках.",
		}, nil
	}

	// Create filters for book search with pagination and user's language preference
	filters := models.BookFilters{
		Title:  title,
		Limit:  limit,
		Offset: offset,
	}

	// Apply user's language preference if available
	if err == nil && user.BooksLang != "" {
		filters.Lang = user.BooksLang
		logging.Infof("Applying language filter '%s' for user %d", user.BooksLang, userID)
	}

	// Search for books using the existing database function
	books, totalCount, err := database.GetBooksEnhanced(user.ID, filters)
	if err != nil {
		logging.Errorf("Failed to search books: %v", err)
		return &CommandResult{
			Message: "An error occurred while searching for books. Please try again later.",
		}, nil
	}

	if len(books) == 0 && offset == 0 {
		languageMsg := ""
		if user.BooksLang != "" {
			languageMsg = fmt.Sprintf(" in %s language", user.BooksLang)
		}
		return &CommandResult{
			Message: fmt.Sprintf("📚 Books with title \"%s\"%s were not found.\n\nTry changing your search query or using other keywords.", title, languageMsg),
		}, nil
	}

	if len(books) == 0 && offset > 0 {
		return &CommandResult{
			Message: "No results on this page.",
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
			Message: "Please specify an author name to search for.",
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
			Message: "An error occurred while searching for authors. Please try again later.",
		}, nil
	}

	if len(authors) == 0 && offset == 0 {
		return &CommandResult{
			Message: fmt.Sprintf("👤 Authors with name \"%s\" were not found.\n\nTry changing your search query or using other keywords.", author),
		}, nil
	}

	if len(authors) == 0 && offset > 0 {
		return &CommandResult{
			Message: "No results on this page.",
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

// ExecuteFindAuthorBooksWithPagination executes a search for books by specific author ID with pagination
func (cp *CommandProcessor) ExecuteFindAuthorBooksWithPagination(authorID int64, authorName string, userID int64, offset, limit int) (*CommandResult, error) {
	// Get user's language preference and internal ID
	user, err := database.GetUserByTelegramID(userID)
	if err != nil {
		logging.Warnf("Failed to get user language preference for user %d: %v", userID, err)
		return &CommandResult{
			Message: "Не удалось найти пользователя. Перепривяжите Telegram в настройках.",
		}, nil
	}

	// Create filters for author books search with pagination and user's language preference
	filters := models.BookFilters{
		Author: int(authorID),
		Limit:  limit,
		Offset: offset,
	}

	// Apply user's language preference if available
	if err == nil && user.BooksLang != "" {
		filters.Lang = user.BooksLang
		logging.Infof("Applying language filter '%s' for author books search, user %d", user.BooksLang, userID)
	}

	// Search for books using the existing database function
	books, totalCount, err := database.GetBooksEnhanced(user.ID, filters)
	if err != nil {
		logging.Errorf("Failed to search books by author ID %d: %v", authorID, err)
		return &CommandResult{
			Message: "An error occurred while searching for books by this author. Please try again later.",
		}, nil
	}

	if len(books) == 0 && offset == 0 {
		languageMsg := ""
		if user.BooksLang != "" {
			languageMsg = fmt.Sprintf(" in %s language", user.BooksLang)
		}
		return &CommandResult{
			Message: fmt.Sprintf("📚 Books by %s%s were not found in the library.", authorName, languageMsg),
		}, nil
	}

	if len(books) == 0 && offset > 0 {
		return &CommandResult{
			Message: "No results on this page.",
		}, nil
	}

	// Format the message like book search results
	currentPage := (offset / limit) + 1
	totalPages := (totalCount + limit - 1) / limit

	var messageBuilder strings.Builder
	messageBuilder.WriteString(fmt.Sprintf("📚 Books by %s:\n", authorName))
	messageBuilder.WriteString(fmt.Sprintf("Page %d of %d (total found %d books)\n\n", currentPage, totalPages, totalCount))

	for i, book := range books {
		// Format authors
		var authorNames []string
		for _, bookAuthor := range book.Authors {
			authorNames = append(authorNames, bookAuthor.FullName)
		}
		authorsStr := strings.Join(authorNames, ", ")
		if authorsStr == "" {
			authorsStr = "Unknown author"
		}

		// Add book entry with correct numbering
		bookNumber := offset + i + 1
		messageBuilder.WriteString(fmt.Sprintf("%d. %s — %s", bookNumber, book.Title, authorsStr))

		// Add series information if available
		if len(book.Series) > 0 && book.Series[0].Ser != "" {
			messageBuilder.WriteString(fmt.Sprintf(" (series: %s)", book.Series[0].Ser))
		}

		messageBuilder.WriteString("\n")
	}

	messageBuilder.WriteString("\n💡 Select a book by number or use navigation:")

	// Create inline keyboard with book selection buttons and pagination
	replyMarkup := cp.createBookButtonsWithPagination(books, offset, limit, totalCount)

	return &CommandResult{
		Message:     messageBuilder.String(),
		Books:       books,
		ReplyMarkup: replyMarkup,
		SearchParams: &SearchParams{
			Query:      authorName,
			QueryType:  "author_books",
			RefID:      authorID,
			Offset:     offset,
			Limit:      limit,
			TotalCount: totalCount,
		},
	}, nil
}

// executeFindBookWithAuthor executes a combined book and author search command
func (cp *CommandProcessor) executeFindBookWithAuthor(title, author string, userID int64) (*CommandResult, error) {
	return cp.executeFindBookWithAuthorWithPagination(title, author, userID, 0, 5)
}

// ExecuteFindBookWithAuthorWithPagination executes a combined book and author search with pagination (exported for callback handlers)
func (cp *CommandProcessor) ExecuteFindBookWithAuthorWithPagination(title, author string, userID int64, offset, limit int) (*CommandResult, error) {
	return cp.executeFindBookWithAuthorWithPagination(title, author, userID, offset, limit)
}

// executeFindBookWithAuthorWithPagination executes a combined book and author search with pagination
func (cp *CommandProcessor) executeFindBookWithAuthorWithPagination(title, author string, userID int64, offset, limit int) (*CommandResult, error) {
	if title == "" && author == "" {
		return &CommandResult{
			Message: "Please specify both book title and author name to search for.",
		}, nil
	}

	// Get user's language preference and internal ID
	user, err := database.GetUserByTelegramID(userID)
	if err != nil {
		logging.Warnf("Failed to get user language preference for user %d: %v", userID, err)
		return &CommandResult{
			Message: "Не удалось найти пользователя. Перепривяжите Telegram в настройках.",
		}, nil
	}

	// Step 1: Search by title first to get candidate books
	var candidateBooks []models.Book

	if title != "" {
		// Create filters for book search with higher limit to get more candidates
		filters := models.BookFilters{
			Title:  title,
			Limit:  200, // Get more candidates for filtering
			Offset: 0,   // Always start from beginning for filtering
		}

		// Apply user's language preference if available
		if err == nil && user.BooksLang != "" {
			filters.Lang = user.BooksLang
			logging.Infof("Applying language filter '%s' for combined search, user %d", user.BooksLang, userID)
		}

		// Search for books using the existing database function
		books, _, err := database.GetBooksEnhanced(user.ID, filters)
		if err != nil {
			logging.Errorf("Failed to search books for combined search: %v", err)
			return &CommandResult{
				Message: "An error occurred while searching for books. Please try again later.",
			}, nil
		}
		candidateBooks = books
	} else {
		// If no title provided, search by author only
		return cp.executeFindAuthor(author, userID)
	}

	// Step 2: Filter books by author if author name is provided
	var filteredBooks []models.Book
	if author != "" {
		logging.Infof("Filtering %d candidate books by author '%s'", len(candidateBooks), author)

		// Normalize author name for comparison (remove common words, convert to lowercase)
		normalizedSearchAuthor := cp.normalizeAuthorName(author)

		for _, book := range candidateBooks {
			// Check if any of the book's authors match the search author
			for _, bookAuthor := range book.Authors {
				normalizedBookAuthor := cp.normalizeAuthorName(bookAuthor.FullName)

				// Check for various types of matches
				if cp.authorsMatch(normalizedSearchAuthor, normalizedBookAuthor) {
					filteredBooks = append(filteredBooks, book)
					logging.Infof("Book '%s' matches: search author '%s' ≈ book author '%s'",
						book.Title, normalizedSearchAuthor, normalizedBookAuthor)
					break // Only add the book once even if multiple authors match
				}
			}
		}
	} else {
		filteredBooks = candidateBooks
	}

	totalCount := len(filteredBooks)

	// Step 3: Apply pagination to filtered results
	var paginatedBooks []models.Book
	if offset < len(filteredBooks) {
		end := offset + limit
		if end > len(filteredBooks) {
			end = len(filteredBooks)
		}
		paginatedBooks = filteredBooks[offset:end]
	}

	// Step 4: Handle empty results
	if len(filteredBooks) == 0 && offset == 0 {
		languageMsg := ""
		if err == nil && user.BooksLang != "" {
			languageMsg = fmt.Sprintf(" in %s language", user.BooksLang)
		}

		// Since we've reached this point, title is always non-empty due to the logic above
		// The only variable condition is whether author filtering was applied
		if author != "" {
			return &CommandResult{
				Message: fmt.Sprintf("📚 Books with title \"%s\" by author \"%s\"%s were not found.\n\nTry using different keywords or check the spelling.", title, author, languageMsg),
			}, nil
		} else {
			return &CommandResult{
				Message: fmt.Sprintf("📚 Books with title \"%s\"%s were not found.\n\nTry using different keywords.", title, languageMsg),
			}, nil
		}
	}

	if len(paginatedBooks) == 0 && offset > 0 {
		return &CommandResult{
			Message: "No results on this page.",
		}, nil
	}

	// Step 5: Format the response
	queryDescription := cp.formatCombinedQuery(title, author)
	message := cp.formatCombinedSearchResultsWithPagination(queryDescription, paginatedBooks, totalCount, offset, limit)

	// Create inline keyboard with number-based buttons and pagination
	replyMarkup := cp.createBookButtonsWithPagination(paginatedBooks, offset, limit, totalCount)

	return &CommandResult{
		Message:     message,
		Books:       paginatedBooks,
		ReplyMarkup: replyMarkup,
		SearchParams: &SearchParams{
			Query:      fmt.Sprintf("%s by %s", title, author),
			QueryType:  "combined",
			Offset:     offset,
			Limit:      limit,
			TotalCount: totalCount,
		},
	}, nil
}

// normalizeAuthorName normalizes author name for comparison
func (cp *CommandProcessor) normalizeAuthorName(name string) string {
	// Convert to lowercase and trim spaces
	normalized := strings.ToLower(strings.TrimSpace(name))

	// Remove common words that might interfere with matching
	commonWords := []string{"и", "и.", "and", "де", "ван", "фон", "von", "van", "de", "la", "le", "du"}

	words := strings.Fields(normalized)
	var filteredWords []string

	for _, word := range words {
		isCommon := false
		for _, common := range commonWords {
			if word == common {
				isCommon = true
				break
			}
		}
		if !isCommon && len(word) > 1 {
			filteredWords = append(filteredWords, word)
		}
	}

	return strings.Join(filteredWords, " ")
}

// authorsMatch checks if two normalized author names match
func (cp *CommandProcessor) authorsMatch(searchAuthor, bookAuthor string) bool {
	// Exact match
	if searchAuthor == bookAuthor {
		return true
	}

	// Split into words for partial matching
	searchWords := strings.Fields(searchAuthor)
	bookWords := strings.Fields(bookAuthor)

	if len(searchWords) == 0 || len(bookWords) == 0 {
		return false
	}

	// Case 1: Search contains book author (e.g., search "толстой лев" contains book author "толстой")
	if strings.Contains(searchAuthor, bookAuthor) {
		return true
	}

	// Case 2: Book author contains search author (e.g., book author "лев толстой" contains search "толстой")
	if strings.Contains(bookAuthor, searchAuthor) {
		return true
	}

	// Case 3: Check if any search word matches any book word (for surname matching)
	for _, searchWord := range searchWords {
		if len(searchWord) < 3 { // Skip very short words
			continue
		}

		for _, bookWord := range bookWords {
			if len(bookWord) < 3 {
				continue
			}

			// Exact word match
			if searchWord == bookWord {
				return true
			}

			// Partial match for longer words (to handle different forms)
			if len(searchWord) >= 4 && len(bookWord) >= 4 {
				// Check if one word starts with the other (for surname variations)
				if strings.HasPrefix(searchWord, bookWord) || strings.HasPrefix(bookWord, searchWord) {
					return true
				}

				// Check for common substring (at least 4 characters)
				if len(searchWord) >= 5 && len(bookWord) >= 5 {
					for i := 0; i <= len(searchWord)-4; i++ {
						substr := searchWord[i : i+4]
						if strings.Contains(bookWord, substr) {
							return true
						}
					}
				}
			}
		}
	}

	return false
}

// formatCombinedQuery formats the combined query description
func (cp *CommandProcessor) formatCombinedQuery(title, author string) string {
	if title != "" && author != "" {
		return fmt.Sprintf("\"%s\" by %s", title, author)
	} else if title != "" {
		return fmt.Sprintf("\"%s\"", title)
	} else if author != "" {
		return fmt.Sprintf("books by %s", author)
	}
	return "books"
}

// formatCombinedSearchResultsWithPagination formats combined search results with pagination
func (cp *CommandProcessor) formatCombinedSearchResultsWithPagination(query string, books []models.Book, totalCount, offset, limit int) string {
	var builder strings.Builder

	currentPage := (offset / limit) + 1
	totalPages := (totalCount + limit - 1) / limit

	builder.WriteString(fmt.Sprintf("📚 Search results for %s:\n", query))
	builder.WriteString(fmt.Sprintf("Page %d of %d (total found %d books)\n\n", currentPage, totalPages, totalCount))

	for i, book := range books {
		// Format authors
		var authorNames []string
		for _, author := range book.Authors {
			authorNames = append(authorNames, author.FullName)
		}
		authorsStr := strings.Join(authorNames, ", ")
		if authorsStr == "" {
			authorsStr = "Unknown author"
		}

		// Add book entry with correct numbering
		bookNumber := offset + i + 1
		builder.WriteString(fmt.Sprintf("%d. %s — %s", bookNumber, book.Title, authorsStr))

		// Add series information if available
		if len(book.Series) > 0 && book.Series[0].Ser != "" {
			builder.WriteString(fmt.Sprintf(" (series: %s)", book.Series[0].Ser))
		}

		builder.WriteString("\n")
	}

	builder.WriteString("\n💡 Select a book by number or use navigation:")

	return builder.String()
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

// inlineRow builds a single-row inline keyboard from buttons.
func inlineRow(btns ...tgbot.InlineKeyboardButton) []tgbot.InlineKeyboardButton {
	return btns
}

// appendPaginationRow adds prev/next navigation buttons to the keyboard rows.
func appendPaginationRow(rows [][]tgbot.InlineKeyboardButton, offset, limit, totalCount int) [][]tgbot.InlineKeyboardButton {
	var paginationRow []tgbot.InlineKeyboardButton
	if offset > 0 {
		paginationRow = append(paginationRow, tgbot.InlineKeyboardButton{
			Text:         "⬅️ Назад",
			CallbackData: "prev_page",
			Style:        "primary",
		})
	}
	if offset+limit < totalCount {
		paginationRow = append(paginationRow, tgbot.InlineKeyboardButton{
			Text:         "➡️ Вперед",
			CallbackData: "next_page",
			Style:        "primary",
		})
	}
	if len(paginationRow) > 0 {
		rows = append(rows, paginationRow)
	}
	return rows
}

// createBookButtons creates inline keyboard buttons for books
func (cp *CommandProcessor) createBookButtons(books []models.Book) *tgbot.InlineKeyboardMarkup {
	if len(books) == 0 {
		return nil
	}

	var rows [][]tgbot.InlineKeyboardButton

	for _, book := range books {
		buttonText := book.Title
		if len(buttonText) > 50 {
			buttonText = buttonText[:47] + "..."
		}
		rows = append(rows, inlineRow(tgbot.InlineKeyboardButton{
			Text:         buttonText,
			CallbackData: fmt.Sprintf("book:%d", book.ID),
		}))
	}

	return &tgbot.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// createBookButtonsWithPagination creates inline keyboard buttons for books with pagination
func (cp *CommandProcessor) createBookButtonsWithPagination(books []models.Book, offset, limit, totalCount int) *tgbot.InlineKeyboardMarkup {
	if len(books) == 0 {
		return nil
	}

	var rows [][]tgbot.InlineKeyboardButton

	var currentRow []tgbot.InlineKeyboardButton
	for i, book := range books {
		bookNumber := offset + i + 1
		currentRow = append(currentRow, tgbot.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d", bookNumber),
			CallbackData: fmt.Sprintf("select:%d", book.ID),
		})

		if len(currentRow) == 3 || i == len(books)-1 {
			rows = append(rows, currentRow)
			currentRow = nil
		}
	}

	rows = appendPaginationRow(rows, offset, limit, totalCount)

	return &tgbot.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// CreateBookButtonsWithPagination creates inline keyboard buttons for books with pagination (exported for external use)
func (cp *CommandProcessor) CreateBookButtonsWithPagination(books []models.Book, offset, limit, totalCount int) *tgbot.InlineKeyboardMarkup {
	return cp.createBookButtonsWithPagination(books, offset, limit, totalCount)
}

// createAuthorButtonsWithPagination creates inline keyboard buttons for authors with pagination
func (cp *CommandProcessor) createAuthorButtonsWithPagination(authors []models.Author, offset, limit, totalCount int) *tgbot.InlineKeyboardMarkup {
	if len(authors) == 0 {
		return nil
	}

	var rows [][]tgbot.InlineKeyboardButton

	var currentRow []tgbot.InlineKeyboardButton
	for i, author := range authors {
		authorNumber := offset + i + 1
		currentRow = append(currentRow, tgbot.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d", authorNumber),
			CallbackData: fmt.Sprintf("author:%d", author.ID),
		})

		if len(currentRow) == 3 || i == len(authors)-1 {
			rows = append(rows, currentRow)
			currentRow = nil
		}
	}

	rows = appendPaginationRow(rows, offset, limit, totalCount)

	return &tgbot.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// ExecuteDirectBookSearch performs exact book search without LLM (for /b command)
func (cp *CommandProcessor) ExecuteDirectBookSearch(title string, userID int64) (*CommandResult, error) {
	result, err := cp.executeFindBookWithPagination(title, userID, 0, 5)
	if err != nil {
		return nil, err
	}
	// Set query type for proper pagination handling
	if result.SearchParams != nil {
		result.SearchParams.QueryType = "book"
	}
	return result, nil
}

// ExecuteDirectAuthorSearch performs exact author search without LLM (for /a command)
func (cp *CommandProcessor) ExecuteDirectAuthorSearch(author string) (*CommandResult, error) {
	result, err := cp.executeFindAuthorWithPagination(author, 0, 5)
	if err != nil {
		return nil, err
	}
	// QueryType is already set to "author" in executeFindAuthorWithPagination
	return result, nil
}

// ExecuteDirectCombinedSearch performs exact combined search without LLM (for /ba command)
func (cp *CommandProcessor) ExecuteDirectCombinedSearch(title, author string, userID int64) (*CommandResult, error) {
	result, err := cp.executeFindBookWithAuthorWithPagination(title, author, userID, 0, 5)
	if err != nil {
		return nil, err
	}
	// QueryType is already set to "combined" in executeFindBookWithAuthorWithPagination
	return result, nil
}

// ExecuteShowCollections shows public curated collections with pagination
func (cp *CommandProcessor) ExecuteShowCollections(offset, limit int) (*CommandResult, error) {
	ctx := context.Background()
	page := (offset / limit) + 1
	collections, total, err := database.ListPublicCuratedCollections(ctx, page, limit)
	if err != nil {
		logging.Errorf("Failed to list public collections: %v", err)
		return &CommandResult{
			Message: "Произошла ошибка при получении подборок. Попробуйте позже.",
		}, nil
	}

	if len(collections) == 0 && offset == 0 {
		return &CommandResult{
			Message: "📦 Публичные подборки пока недоступны.",
		}, nil
	}

	if len(collections) == 0 && offset > 0 {
		return &CommandResult{
			Message: "На этой странице нет результатов.",
		}, nil
	}

	message := cp.formatCollectionsWithPagination(collections, total, offset, limit)
	replyMarkup := cp.createCollectionButtonsWithPagination(collections, offset, limit, total)

	return &CommandResult{
		Message:     message,
		ReplyMarkup: replyMarkup,
		SearchParams: &SearchParams{
			Query:      "collections",
			QueryType:  "collections",
			Offset:     offset,
			Limit:      limit,
			TotalCount: total,
		},
	}, nil
}

// ExecuteCollectionBooks shows books from a specific collection with pagination
func (cp *CommandProcessor) ExecuteCollectionBooks(collectionID int64, userID int64, offset, limit int) (*CommandResult, error) {
	ctx := context.Background()
	col, err := database.GetPublicCuratedCollection(ctx, collectionID)
	if err != nil {
		return &CommandResult{
			Message: "📦 Подборка не найдена.",
		}, nil
	}

	books, total, err := database.GetPublicCollectionBooksPage(ctx, collectionID, offset, limit)
	if err != nil {
		logging.Errorf("Failed to get collection books: %v", err)
		return &CommandResult{
			Message: "Произошла ошибка при получении книг подборки. Попробуйте позже.",
		}, nil
	}

	if len(books) == 0 && offset == 0 {
		return &CommandResult{
			Message: fmt.Sprintf("📦 Подборка \"%s\" пока не содержит книг.", col.Name),
		}, nil
	}

	if len(books) == 0 && offset > 0 {
		return &CommandResult{
			Message: "На этой странице нет результатов.",
		}, nil
	}

	_ = userID // reserved for future fav support
	message := cp.formatCollectionBooksWithPagination(col.Name, books, total, offset, limit)
	replyMarkup := cp.createBookButtonsWithPagination(books, offset, limit, total)

	return &CommandResult{
		Message:     message,
		Books:       books,
		ReplyMarkup: replyMarkup,
		SearchParams: &SearchParams{
			Query:      col.Name,
			QueryType:  "collection_books",
			RefID:      collectionID,
			Offset:     offset,
			Limit:      limit,
			TotalCount: total,
		},
	}, nil
}

// formatCollectionsWithPagination formats collections list with pagination info
func (cp *CommandProcessor) formatCollectionsWithPagination(collections []models.BookCollection, totalCount, offset, limit int) string {
	var builder strings.Builder

	currentPage := (offset / limit) + 1
	totalPages := (totalCount + limit - 1) / limit

	builder.WriteString("📦 Подборки книг:\n")
	builder.WriteString(fmt.Sprintf("Страница %d из %d (всего %d подборок)\n\n", currentPage, totalPages, totalCount))

	for i, col := range collections {
		number := offset + i + 1
		builder.WriteString(fmt.Sprintf("%d. %s", number, col.Name))
		if col.SourceURL != "" {
			builder.WriteString(fmt.Sprintf(" (источник: %s)", col.SourceURL))
		}
		builder.WriteString("\n")
	}

	builder.WriteString("\n💡 Выберите подборку по номеру или используйте навигацию:")
	return builder.String()
}

// createCollectionButtonsWithPagination creates inline keyboard for collections with pagination
func (cp *CommandProcessor) createCollectionButtonsWithPagination(collections []models.BookCollection, offset, limit, totalCount int) *tgbot.InlineKeyboardMarkup {
	if len(collections) == 0 {
		return nil
	}

	var rows [][]tgbot.InlineKeyboardButton

	var currentRow []tgbot.InlineKeyboardButton
	for i, col := range collections {
		number := offset + i + 1
		currentRow = append(currentRow, tgbot.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d", number),
			CallbackData: fmt.Sprintf("collection:%d", col.ID),
		})

		if len(currentRow) == 3 || i == len(collections)-1 {
			rows = append(rows, currentRow)
			currentRow = nil
		}
	}

	rows = appendPaginationRow(rows, offset, limit, totalCount)

	return &tgbot.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// formatCollectionBooksWithPagination formats collection books list with pagination info
func (cp *CommandProcessor) formatCollectionBooksWithPagination(collectionName string, books []models.Book, totalCount, offset, limit int) string {
	var builder strings.Builder

	currentPage := (offset / limit) + 1
	totalPages := (totalCount + limit - 1) / limit

	builder.WriteString(fmt.Sprintf("📦 Подборка \"%s\":\n", collectionName))
	builder.WriteString(fmt.Sprintf("Страница %d из %d (всего %d книг)\n\n", currentPage, totalPages, totalCount))

	for i, book := range books {
		var authorNames []string
		for _, author := range book.Authors {
			authorNames = append(authorNames, author.FullName)
		}
		authorsStr := strings.Join(authorNames, ", ")
		if authorsStr == "" {
			authorsStr = "Автор неизвестен"
		}

		bookNumber := offset + i + 1
		builder.WriteString(fmt.Sprintf("%d. %s — %s", bookNumber, book.Title, authorsStr))

		if len(book.Series) > 0 && book.Series[0].Ser != "" {
			builder.WriteString(fmt.Sprintf(" (серия: %s)", book.Series[0].Ser))
		}

		builder.WriteString("\n")
	}

	builder.WriteString("\n💡 Выберите книгу по номеру или используйте навигацию:")
	return builder.String()
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

// ExecuteShowFavorites shows user's favorite books with pagination
func (cp *CommandProcessor) ExecuteShowFavorites(userID int64, offset, limit int) (*CommandResult, error) {
	user, err := database.GetUserByTelegramID(userID)
	if err != nil {
		logging.Warnf("Failed to get user for favorites %d: %v", userID, err)
		return &CommandResult{
			Message: "Не удалось найти пользователя. Перепривяжите Telegram в настройках.",
		}, nil
	}

	// Get user's favorite books using the Fav filter
	filters := models.BookFilters{
		Fav:    true,
		Limit:  limit,
		Offset: offset,
	}

	books, totalCount, err := database.GetBooksEnhanced(user.ID, filters)
	if err != nil {
		logging.Errorf("Failed to get favorite books for user %d: %v", userID, err)
		return &CommandResult{
			Message: "Произошла ошибка при получении избранного. Попробуйте позже.",
		}, nil
	}

	// Check if user has any favorites
	if len(books) == 0 && offset == 0 {
		return &CommandResult{
			Message: "📚 Ваш список избранного пуст.\n\n" +
				"Чтобы добавить книгу в избранное, найдите её через поиск и используйте соответствующую функцию.",
		}, nil
	}

	if len(books) == 0 && offset > 0 {
		return &CommandResult{
			Message: "На этой странице нет результатов.",
		}, nil
	}

	// Format the response message with pagination info
	message := cp.formatFavoriteBooksWithPagination(books, totalCount, offset, limit)

	// Create inline keyboard with book selection buttons and pagination
	replyMarkup := cp.createBookButtonsWithPagination(books, offset, limit, totalCount)

	return &CommandResult{
		Message:     message,
		Books:       books,
		ReplyMarkup: replyMarkup,
		SearchParams: &SearchParams{
			Query:      "favorites",
			QueryType:  "favorites",
			Offset:     offset,
			Limit:      limit,
			TotalCount: totalCount,
		},
	}, nil
}

// formatFavoriteBooksWithPagination formats favorite books list with pagination info
func (cp *CommandProcessor) formatFavoriteBooksWithPagination(books []models.Book, totalCount, offset, limit int) string {
	var builder strings.Builder

	currentPage := (offset / limit) + 1
	totalPages := (totalCount + limit - 1) / limit

	builder.WriteString("⭐ Избранные книги:\n")
	builder.WriteString(fmt.Sprintf("Страница %d из %d (всего %d книг)\n\n", currentPage, totalPages, totalCount))

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

		// Add language info if available
		if book.Lang != "" {
			builder.WriteString(fmt.Sprintf(" [%s]", book.Lang))
		}

		builder.WriteString("\n")
	}

	builder.WriteString("\n💡 Выберите книгу по номеру или используйте навигацию:")

	return builder.String()
}
