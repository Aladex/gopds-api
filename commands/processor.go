package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gopds-api/database"
	"gopds-api/llm"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	tgbot "github.com/go-telegram/bot/models"
)

// telegramUserLookup resolves a Telegram ID to the system user. It is a seam:
// production uses database.GetUserByTelegramID, tests use a fake, so no
// search test touches PostgreSQL.
type telegramUserLookup func(int64) (models.User, error)

// defaultSearchPageSize is the page size a bot search flow starts with when
// the caller passes no explicit pagination.
const defaultSearchPageSize = 5

// userLookupFailure answers a reader whose account could not be resolved.
//
// The two causes deserve different answers and used to get the same one. A
// missing row means the account really is unlinked and relinking is the fix. Any
// other error — the database refusing connections, a timeout — is ours, and the
// reader has nothing to repair: telling them to relink during an outage invites
// them to tear down a working link, and every one of those has to be put back by
// hand afterwards.
//
// Both answers are logged, so an outage is still visible to us even though it is
// invisible to them.
func userLookupFailure(userID int64, err error) *CommandResult {
	if errors.Is(err, pg.ErrNoRows) {
		logging.Warnf("No linked account for Telegram user %d", userID)
		return &CommandResult{
			Message: "Не удалось найти пользователя. Перепривяжите Telegram в настройках.",
		}
	}
	logging.Errorf("Looking up Telegram user %d: %v", userID, err)
	return &CommandResult{
		Message: "Сервис временно недоступен. Попробуйте позже.",
	}
}

// noResultsOnPageMessage is shown when a search page beyond the first comes
// back empty.
const noResultsOnPageMessage = "No results on this page."

// CommandProcessor handles execution of parsed commands
type CommandProcessor struct {
	llmService *llm.LLMService
	search     services.PublicSearch
	findUser   telegramUserLookup
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
	QueryType  string `json:"query_type"`       // "book", "author", or "author_books"
	RefID      int64  `json:"ref_id,omitempty"` // ID of related entity (author, collection, etc.)
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	TotalCount int    `json:"total_count"`
}

// NewCommandProcessor creates a new command processor on the one search
// service every adapter shares.
func NewCommandProcessor(search services.PublicSearch) *CommandProcessor {
	cp := newCommandProcessorWithDeps(search, database.GetUserByTelegramID)
	cp.llmService = llm.NewLLMService()
	return cp
}

// newCommandProcessorWithDeps builds a processor with explicit seams — the
// shared search surface and the Telegram user lookup. It has no LLM: package
// tests drive the search flows directly and never call ProcessMessage.
func newCommandProcessorWithDeps(search services.PublicSearch, findUser telegramUserLookup) *CommandProcessor {
	return &CommandProcessor{search: search, findUser: findUser}
}

// ProcessMessage processes a user message and returns a response
func (cp *CommandProcessor) ProcessMessage(
	ctx context.Context, userMessage, conversationContext string, userID int64,
) (*CommandResult, error) {
	// Use LLM to parse the user message
	command, err := cp.llmService.ProcessQuery(userMessage, conversationContext)
	if err != nil {
		logging.Errorf("Failed to process query with LLM: %v", err)
		return cp.createUnknownResponse(), nil
	}

	// Execute the command
	return cp.executeCommand(ctx, command, userID)
}

// executeCommand executes a parsed command
func (cp *CommandProcessor) executeCommand(ctx context.Context, command *llm.Command, userID int64) (*CommandResult, error) {
	switch command.Command {
	case "find_book":
		return cp.executeFindBook(ctx, command.Title, userID)
	case "find_author":
		return cp.executeFindAuthor(ctx, command.Author, userID)
	case "find_book_with_author":
		return cp.executeFindBookWithAuthor(ctx, command.Title, command.Author, userID)
	case "unknown":
		return cp.createUnknownResponse(), nil
	default:
		logging.Errorf("Unknown command: %s", command.Command)
		return cp.createUnknownResponse(), nil
	}
}

// executeFindBook executes a book search command
func (cp *CommandProcessor) executeFindBook(ctx context.Context, title string, userID int64) (*CommandResult, error) {
	return cp.executeFindBookWithPagination(ctx, title, userID, 0, defaultSearchPageSize)
}

// ExecuteFindBookWithPagination executes a book search command with pagination (exported for callback handlers)
func (cp *CommandProcessor) ExecuteFindBookWithPagination(
	ctx context.Context, title string, userID int64, offset, limit int,
) (*CommandResult, error) {
	return cp.executeFindBookWithPagination(ctx, title, userID, offset, limit)
}

// executeFindBookWithPagination executes a book search command with pagination
func (cp *CommandProcessor) executeFindBookWithPagination(
	ctx context.Context, title string, userID int64, offset, limit int,
) (*CommandResult, error) {
	if title == "" {
		return &CommandResult{
			Message: "Please specify a book title to search for.",
		}, nil
	}

	// Get user's language preference and internal ID
	user, err := cp.findUser(userID)
	if err != nil {
		return userLookupFailure(userID, err), nil
	}

	// One ranked page from the shared search service: the reader's book
	// language, the internal user ID and the exact pre-pagination total.
	page, err := cp.search.SearchBooks(ctx, models.BookSearchRequest{
		Query:    title,
		UserID:   user.ID,
		Language: user.BooksLang,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		logging.Errorf("Failed to search books: %v", err)
		return &CommandResult{
			Message: "An error occurred while searching for books. Please try again later.",
		}, nil
	}

	books, totalCount := page.Books, page.Total

	if len(books) == 0 {
		return bookSearchNotFound(title, user.BooksLang, offset), nil
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

// bookSearchNotFound renders the empty-result message for a book search: the
// first page names the language scope, later pages just say they are empty.
func bookSearchNotFound(title, lang string, offset int) *CommandResult {
	if offset > 0 {
		return &CommandResult{
			Message: noResultsOnPageMessage,
		}
	}
	languageMsg := ""
	if lang != "" && lang != database.AllLanguages {
		languageMsg = fmt.Sprintf(" in %s language", lang)
	}
	return &CommandResult{
		Message: fmt.Sprintf(
			"📚 Books with title %q%s were not found.\n\nTry changing your search query or using other keywords.",
			title, languageMsg,
		),
	}
}

// executeFindAuthor executes an author search command
func (cp *CommandProcessor) executeFindAuthor(ctx context.Context, author string, userID int64) (*CommandResult, error) {
	return cp.executeFindAuthorWithPagination(ctx, author, userID, 0, defaultSearchPageSize)
}

// ExecuteFindAuthorWithPagination executes an author search command with pagination (exported for callback handlers)
func (cp *CommandProcessor) ExecuteFindAuthorWithPagination(
	ctx context.Context, author string, userID int64, offset, limit int,
) (*CommandResult, error) {
	return cp.executeFindAuthorWithPagination(ctx, author, userID, offset, limit)
}

// executeFindAuthorWithPagination executes an author search command with pagination
func (cp *CommandProcessor) executeFindAuthorWithPagination(
	ctx context.Context, author string, userID int64, offset, limit int,
) (*CommandResult, error) {
	if author == "" {
		return &CommandResult{
			Message: "Please specify an author name to search for.",
		}, nil
	}

	// The reader's book language scopes the author list the same way it
	// scopes the book list an author opens into.
	user, err := cp.findUser(userID)
	if err != nil {
		return userLookupFailure(userID, err), nil
	}

	page, err := cp.search.SearchAuthors(ctx, models.AuthorSearchRequest{
		Query:    author,
		Language: user.BooksLang,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		logging.Errorf("Failed to search authors: %v", err)
		return &CommandResult{
			Message: "An error occurred while searching for authors. Please try again later.",
		}, nil
	}

	authors, totalCount := page.Authors, page.Total

	if len(authors) == 0 && offset == 0 {
		return &CommandResult{
			Message: fmt.Sprintf("👤 Authors with name \"%s\" were not found.\n\nTry changing your search query or using other keywords.", author),
		}, nil
	}

	if len(authors) == 0 && offset > 0 {
		return &CommandResult{
			Message: noResultsOnPageMessage,
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
	user, err := cp.findUser(userID)
	if err != nil {
		return userLookupFailure(userID, err), nil
	}

	// Listing an author's books is a scoped list, not a text search — the same
	// ordinary-list path REST uses for /api/books?author=<id>. The search
	// service has no scope-only mode by design.
	filters := models.BookFilters{
		Author: int(authorID),
		Limit:  limit,
		Offset: offset,
	}

	// Apply user's language preference if available — the same language the
	// author search that led here used.
	if user.BooksLang != "" {
		filters.Lang = user.BooksLang
	}

	// Search for books using the existing database function
	books, totalCount, err := database.GetBooks(user.ID, filters)
	if err != nil {
		logging.Errorf("Failed to search books by author ID %d: %v", authorID, err)
		return &CommandResult{
			Message: "An error occurred while searching for books by this author. Please try again later.",
		}, nil
	}

	if len(books) == 0 && offset == 0 {
		languageMsg := ""
		if user.BooksLang != "" && user.BooksLang != database.AllLanguages {
			languageMsg = fmt.Sprintf(" in %s language", user.BooksLang)
		}
		return &CommandResult{
			Message: fmt.Sprintf("📚 Books by %s%s were not found in the library.", authorName, languageMsg),
		}, nil
	}

	if len(books) == 0 && offset > 0 {
		return &CommandResult{
			Message: noResultsOnPageMessage,
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
func (cp *CommandProcessor) executeFindBookWithAuthor(ctx context.Context, title, author string, userID int64) (*CommandResult, error) {
	return cp.executeFindBookWithAuthorWithPagination(ctx, title, author, userID, 0, defaultSearchPageSize)
}

// ExecuteFindBookWithAuthorWithPagination executes a combined book and author search with pagination (exported for callback handlers)
func (cp *CommandProcessor) ExecuteFindBookWithAuthorWithPagination(
	ctx context.Context, title, author string, userID int64, offset, limit int,
) (*CommandResult, error) {
	return cp.executeFindBookWithAuthorWithPagination(ctx, title, author, userID, offset, limit)
}

// executeFindBookWithAuthorWithPagination executes a combined book and author search with pagination
func (cp *CommandProcessor) executeFindBookWithAuthorWithPagination(
	ctx context.Context, title, author string, userID int64, offset, limit int,
) (*CommandResult, error) {
	if title == "" && author == "" {
		return &CommandResult{
			Message: "Please specify both book title and author name to search for.",
		}, nil
	}

	// Without a title this is an author search.
	if title == "" {
		return cp.executeFindAuthorWithPagination(ctx, author, userID, offset, limit)
	}

	// Get user's language preference and internal ID
	user, err := cp.findUser(userID)
	if err != nil {
		return userLookupFailure(userID, err), nil
	}

	// Title and author narrow one database search: the repository keeps both
	// predicates in SQL, so the total is exact instead of a count over a
	// Go-filtered candidate window, and no match below the first page is lost.
	page, err := cp.search.SearchBooks(ctx, models.BookSearchRequest{
		Query:       title,
		AuthorQuery: author,
		UserID:      user.ID,
		Language:    user.BooksLang,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		logging.Errorf("Failed to search books for combined search: %v", err)
		return &CommandResult{
			Message: "An error occurred while searching for books. Please try again later.",
		}, nil
	}

	books, totalCount := page.Books, page.Total

	if len(books) == 0 {
		return combinedSearchNotFound(title, author, user.BooksLang, offset), nil
	}

	// Format the response
	queryDescription := cp.formatCombinedQuery(title, author)
	message := cp.formatCombinedSearchResultsWithPagination(queryDescription, books, totalCount, offset, limit)

	// Create inline keyboard with number-based buttons and pagination
	replyMarkup := cp.createBookButtonsWithPagination(books, offset, limit, totalCount)

	return &CommandResult{
		Message:     message,
		Books:       books,
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

// combinedSearchNotFound renders the empty-result message for a combined
// search: with an author both predicates are named, without one it falls back
// to the title-only wording; later pages just say they are empty.
func combinedSearchNotFound(title, author, lang string, offset int) *CommandResult {
	if offset > 0 {
		return &CommandResult{
			Message: noResultsOnPageMessage,
		}
	}
	languageMsg := ""
	if lang != "" && lang != database.AllLanguages {
		languageMsg = fmt.Sprintf(" in %s language", lang)
	}
	if author != "" {
		return &CommandResult{
			Message: fmt.Sprintf(
				"📚 Books with title %q by author %q%s were not found.\n\nTry using different keywords or check the spelling.",
				title, author, languageMsg,
			),
		}
	}
	return &CommandResult{
		Message: fmt.Sprintf("📚 Books with title %q%s were not found.\n\nTry using different keywords.", title, languageMsg),
	}
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
func (cp *CommandProcessor) ExecuteDirectBookSearch(ctx context.Context, title string, userID int64) (*CommandResult, error) {
	result, err := cp.executeFindBookWithPagination(ctx, title, userID, 0, defaultSearchPageSize)
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
func (cp *CommandProcessor) ExecuteDirectAuthorSearch(ctx context.Context, author string, userID int64) (*CommandResult, error) {
	result, err := cp.executeFindAuthorWithPagination(ctx, author, userID, 0, defaultSearchPageSize)
	if err != nil {
		return nil, err
	}
	// QueryType is already set to "author" in executeFindAuthorWithPagination
	return result, nil
}

// ExecuteDirectCombinedSearch performs exact combined search without LLM (for /ba command)
func (cp *CommandProcessor) ExecuteDirectCombinedSearch(ctx context.Context, title, author string, userID int64) (*CommandResult, error) {
	result, err := cp.executeFindBookWithAuthorWithPagination(ctx, title, author, userID, 0, defaultSearchPageSize)
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
	user, err := cp.findUser(userID)
	if err != nil {
		return userLookupFailure(userID, err), nil
	}

	// Get user's favorite books using the Fav filter
	filters := models.BookFilters{
		Fav:    true,
		Limit:  limit,
		Offset: offset,
	}

	books, totalCount, err := database.GetBooks(user.ID, filters)
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
