package commands

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"gopds-api/models"

	tgbot "github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePublicSearch implements services.PublicSearch: it records every request
// the processor makes and answers with canned pages. No PostgreSQL, no LLM.
type fakePublicSearch struct {
	bookRequests   []models.BookSearchRequest
	authorRequests []models.AuthorSearchRequest
	bookPage       models.BookSearchPage
	authorPage     models.AuthorSearchPage
	err            error
}

func (f *fakePublicSearch) SearchBooks(_ context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	f.bookRequests = append(f.bookRequests, req)
	if f.err != nil {
		return models.BookSearchPage{}, f.err
	}
	return f.bookPage, nil
}

func (f *fakePublicSearch) SearchAuthors(_ context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	f.authorRequests = append(f.authorRequests, req)
	if f.err != nil {
		return models.AuthorSearchPage{}, f.err
	}
	return f.authorPage, nil
}

func (f *fakePublicSearch) Suggestions(_ context.Context, _ models.SuggestionRequest) (models.SuggestionResult, error) {
	return models.SuggestionResult{}, nil
}

// fakeUserLookup stands in for database.GetUserByTelegramID.
func fakeUserLookup(user *models.User, err error) telegramUserLookup {
	return func(int64) (models.User, error) {
		return *user, err
	}
}

func newTestProcessor(search *fakePublicSearch, user *models.User) *CommandProcessor {
	return newCommandProcessorWithDeps(search, fakeUserLookup(user, nil))
}

func cannedBooks(ids ...int64) []models.Book {
	books := make([]models.Book, 0, len(ids))
	for _, id := range ids {
		books = append(books, models.Book{
			ID:      id,
			Title:   fmt.Sprintf("Book %d", id),
			Authors: []models.Author{{ID: id + 1000, FullName: fmt.Sprintf("Author %d", id)}},
		})
	}
	return books
}

func callbackDataOf(markup *tgbot.InlineKeyboardMarkup) []string {
	var data []string
	for _, row := range markup.InlineKeyboard {
		for i := range row {
			data = append(data, row[i].CallbackData)
		}
	}
	return data
}

func TestDirectBookSearchMapsEveryFieldToTheService(t *testing.T) {
	search := &fakePublicSearch{bookPage: models.BookSearchPage{
		Books: cannedBooks(1, 2, 3, 4, 5), Total: 37, Limit: 5, Offset: 0,
	}}
	cp := newTestProcessor(search, &models.User{ID: 42, BooksLang: "ru"})

	result, err := cp.ExecuteDirectBookSearch(context.Background(), "война", 777)
	require.NoError(t, err)

	require.Len(t, search.bookRequests, 1)
	req := search.bookRequests[0]
	assert.Equal(t, "война", req.Query)
	assert.Equal(t, "", req.AuthorQuery)
	assert.Equal(t, int64(42), req.UserID, "the internal user ID, not the Telegram ID")
	assert.Equal(t, "ru", req.Language)
	assert.Equal(t, 5, req.Limit)
	assert.Equal(t, 0, req.Offset)
	// The bot never declares a moderator and never asks for widened visibility.
	assert.False(t, req.Moderator)
	assert.False(t, req.Unapproved)
	assert.False(t, req.IncludeHidden)

	assert.Contains(t, result.Message, "37", "the exact total reaches the message")
	assert.Equal(t, 37, result.SearchParams.TotalCount)
	assert.Equal(t, "book", result.SearchParams.QueryType)
	assert.Equal(t, "война", result.SearchParams.Query)

	data := callbackDataOf(result.ReplyMarkup)
	assert.Contains(t, data, "next_page", "offset 0 + limit 5 < total 37 must offer the next page")
	assert.NotContains(t, data, "prev_page")
}

func TestDirectBookSearchNotFoundMentionsTheLanguageFilter(t *testing.T) {
	search := &fakePublicSearch{bookPage: models.BookSearchPage{Limit: 5}}
	cp := newTestProcessor(search, &models.User{ID: 42, BooksLang: "ru"})

	result, err := cp.ExecuteDirectBookSearch(context.Background(), "война", 777)
	require.NoError(t, err)
	assert.Contains(t, result.Message, `Books with title "война" in ru language were not found`)
	assert.Nil(t, result.SearchParams)
}

func TestDirectBookSearchErrorKeepsTheUserFacingMessage(t *testing.T) {
	search := &fakePublicSearch{err: errors.New("database is down")}
	cp := newTestProcessor(search, &models.User{ID: 42})

	result, err := cp.ExecuteDirectBookSearch(context.Background(), "война", 777)
	require.NoError(t, err)
	assert.Contains(t, result.Message, "An error occurred while searching for books")
}

func TestDirectBookSearchRejectsEmptyTitleWithoutAServiceCall(t *testing.T) {
	search := &fakePublicSearch{}
	cp := newTestProcessor(search, &models.User{ID: 42})

	result, err := cp.ExecuteDirectBookSearch(context.Background(), "", 777)
	require.NoError(t, err)
	assert.Contains(t, result.Message, "Please specify a book title")
	assert.Empty(t, search.bookRequests)
}

func TestDirectAuthorSearchUsesTheReadersBookLanguage(t *testing.T) {
	search := &fakePublicSearch{authorPage: models.AuthorSearchPage{
		Authors: []models.Author{
			{ID: 501, FullName: "Толстой Лев", BooksCount: 200},
			{ID: 502, FullName: "Толстой Алексей", BooksCount: 80},
		},
		Total: 12, Limit: 5, Offset: 0,
	}}
	cp := newTestProcessor(search, &models.User{ID: 42, BooksLang: "en"})

	result, err := cp.ExecuteDirectAuthorSearch(context.Background(), "толстой", 777)
	require.NoError(t, err)

	require.Len(t, search.authorRequests, 1)
	req := search.authorRequests[0]
	assert.Equal(t, "толстой", req.Query)
	assert.Equal(t, "en", req.Language, "author search uses the same language as the book list behind it")
	assert.Equal(t, 5, req.Limit)
	assert.Equal(t, 0, req.Offset)

	assert.Contains(t, result.Message, "12")
	require.NotNil(t, result.SearchParams)
	assert.Equal(t, "author", result.SearchParams.QueryType)
	assert.Equal(t, 12, result.SearchParams.TotalCount)
}

func TestAuthorPaginationMapsOffsetAndLimit(t *testing.T) {
	search := &fakePublicSearch{authorPage: models.AuthorSearchPage{
		Authors: []models.Author{{ID: 511, FullName: "Толстой Лев"}},
		Total:   12, Limit: 5, Offset: 10,
	}}
	cp := newTestProcessor(search, &models.User{ID: 42, BooksLang: "ru"})

	result, err := cp.ExecuteFindAuthorWithPagination(context.Background(), "толстой", 777, 10, 5)
	require.NoError(t, err)

	require.Len(t, search.authorRequests, 1)
	assert.Equal(t, 10, search.authorRequests[0].Offset)
	assert.Equal(t, 5, search.authorRequests[0].Limit)
	assert.Equal(t, 12, result.SearchParams.TotalCount)
}

func TestAuthorSearchLookupFailureAsksForRelink(t *testing.T) {
	search := &fakePublicSearch{}
	cp := newCommandProcessorWithDeps(search, fakeUserLookup(&models.User{}, errors.New("no such user")))

	result, err := cp.ExecuteDirectAuthorSearch(context.Background(), "толстой", 777)
	require.NoError(t, err)
	assert.Contains(t, result.Message, "Не удалось найти пользователя")
	assert.Empty(t, search.authorRequests, "a missing user record never reaches the search service")
}

func TestCombinedSearchIssuesExactlyOneServiceCall(t *testing.T) {
	search := &fakePublicSearch{bookPage: models.BookSearchPage{
		Books: cannedBooks(6, 7, 8, 9, 10), Total: 12, Limit: 5, Offset: 5,
	}}
	cp := newTestProcessor(search, &models.User{ID: 42, BooksLang: "ru"})

	result, err := cp.ExecuteFindBookWithAuthorWithPagination(context.Background(), "война", "толстой", 777, 5, 5)
	require.NoError(t, err)

	require.Len(t, search.bookRequests, 1, "combined search is one database search, not a candidate window")
	req := search.bookRequests[0]
	assert.Equal(t, "война", req.Query)
	assert.Equal(t, "толстой", req.AuthorQuery)
	assert.Equal(t, int64(42), req.UserID)
	assert.Equal(t, "ru", req.Language)
	assert.Equal(t, 5, req.Limit)
	assert.Equal(t, 5, req.Offset)

	require.NotNil(t, result.SearchParams)
	assert.Equal(t, 12, result.SearchParams.TotalCount, "the exact total survives pagination")
	assert.Equal(t, "combined", result.SearchParams.QueryType)
	assert.Equal(t, "война by толстой", result.SearchParams.Query)
	assert.Contains(t, result.Message, "12")
	assert.Len(t, result.Books, 5)

	data := callbackDataOf(result.ReplyMarkup)
	assert.Contains(t, data, "prev_page")
	assert.Contains(t, data, "next_page", "offset 5 + limit 5 < total 12 must offer the next page")
}

func TestCombinedSearchWithoutMatchDoesNotFallBackToTitleOnly(t *testing.T) {
	search := &fakePublicSearch{bookPage: models.BookSearchPage{Limit: 5, Offset: 0}}
	cp := newTestProcessor(search, &models.User{ID: 42, BooksLang: "ru"})

	result, err := cp.ExecuteDirectCombinedSearch(context.Background(), "война", "толстой", 777)
	require.NoError(t, err)

	assert.Contains(t, result.Message, `Books with title "война" by author "толстой"`)
	assert.Contains(t, result.Message, "were not found")
	require.Len(t, search.bookRequests, 1, "no silent retry with the author filter dropped")
	assert.Equal(t, "толстой", search.bookRequests[0].AuthorQuery)
}

func TestCombinedSearchWithoutTitleDelegatesToAuthorSearch(t *testing.T) {
	search := &fakePublicSearch{authorPage: models.AuthorSearchPage{
		Authors: []models.Author{{ID: 501, FullName: "Толстой Лев"}},
		Total:   1, Limit: 5, Offset: 0,
	}}
	cp := newTestProcessor(search, &models.User{ID: 42, BooksLang: "ru"})

	_, err := cp.ExecuteFindBookWithAuthorWithPagination(context.Background(), "", "толстой", 777, 0, 5)
	require.NoError(t, err)

	assert.Empty(t, search.bookRequests)
	require.Len(t, search.authorRequests, 1)
	assert.Equal(t, "толстой", search.authorRequests[0].Query)
}

func TestCombinedSearchValidatesEmptyInput(t *testing.T) {
	search := &fakePublicSearch{}
	cp := newTestProcessor(search, &models.User{ID: 42})

	result, err := cp.ExecuteDirectCombinedSearch(context.Background(), "", "", 777)
	require.NoError(t, err)
	assert.Contains(t, result.Message, "Please specify both book title and author name")
	assert.Empty(t, search.bookRequests)
	assert.Empty(t, search.authorRequests)
}

func TestAuthorBooksLookupFailureAsksForRelink(t *testing.T) {
	search := &fakePublicSearch{}
	cp := newCommandProcessorWithDeps(search, fakeUserLookup(&models.User{}, errors.New("no such user")))

	result, err := cp.ExecuteFindAuthorBooksWithPagination(501, "Толстой Лев", 777, 0, 5)
	require.NoError(t, err)
	assert.Contains(t, result.Message, "Не удалось найти пользователя")
	assert.Empty(t, search.bookRequests)
}

func TestNextPageButtonFollowsTheExactTotal(t *testing.T) {
	search := &fakePublicSearch{bookPage: models.BookSearchPage{
		Books: cannedBooks(6, 7, 8, 9, 10), Total: 10, Limit: 5, Offset: 5,
	}}
	cp := newTestProcessor(search, &models.User{ID: 42})

	result, err := cp.ExecuteFindBookWithPagination(context.Background(), "война", 777, 5, 5)
	require.NoError(t, err)

	data := callbackDataOf(result.ReplyMarkup)
	assert.Contains(t, data, "prev_page")
	assert.NotContains(t, data, "next_page", "an exactly full final page must not promise another one")
}
