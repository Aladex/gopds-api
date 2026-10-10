package commands

import (
	"encoding/json"
	"fmt"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bot's lists of the scanned books with the author line read from the
// author layer. The book whose catalog names the legacy scan glued by index
// lists the file's four authors in file order; the Latin book its two names
// in the file's order; the book without an author is still the placeholder.
// The keyboards are the catalog's: they select books, never authors.
func TestScanCharacterizationTelegramListsFromTheAuthorLayer(t *testing.T) {
	db, books, authors := scanCharacterization(t)
	const telegramID = characterizationTelegramID
	cp := newCommandProcessorWithDeps(services.NewSearchService(database.NewPGSearchRepository(db)),
		database.GetUserByTelegramID)
	cp.authorLines = &services.AuthorLines{Lookup: database.NewPGBookSourceRepository(db)}

	multi := books[scanfixture.EntryMultiAuthor]
	selectMulti := fmt.Sprintf(`{"inline_keyboard":[[{"text":"1","callback_data":"select:%d"}]]}`, multi)
	const multiLine = "война и МИР — лев Николаевич Толстой, пушкин, Анна, Аноним"

	var collection int64
	_, err := db.QueryOne(pg.Scan(&collection), `INSERT INTO book_collections (name, is_curated, is_public)
		VALUES ('Подборка', true, true) RETURNING id`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO book_collection_items (collection_id, book_id, external_title, match_status, position)
		VALUES (?, ?, 'x', 'manual', 1)`, collection, multi)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO favorite_books (user_id, book_id)
		SELECT id, ? FROM auth_user WHERE telegram_id = ?`, multi, telegramID)
	require.NoError(t, err)

	cases := []struct {
		name    string
		run     func() (*CommandResult, error)
		message string
		markup  string
	}{
		{
			name: "books of an author",
			run: func() (*CommandResult, error) {
				return cp.ExecuteFindAuthorBooksWithPagination(authors["Толстой лев"], "Толстой лев", telegramID, 0, 5)
			},
			message: "📚 Books by Толстой лев:\nPage 1 of 1 (total found 1 books)\n\n" +
				"1. " + multiLine + " (series: Эпопея)\n\n" +
				"💡 Select a book by number or use navigation:",
			markup: selectMulti,
		},
		{
			name: "books of the fallback author",
			run: func() (*CommandResult, error) {
				return cp.ExecuteFindAuthorBooksWithPagination(authors["Автор неизвестен"], "Автор неизвестен", telegramID, 0, 5)
			},
			message: "📚 Books by Автор неизвестен:\nPage 1 of 1 (total found 1 books)\n\n" +
				"1. Сборник без автора — Автор неизвестен\n\n" +
				"💡 Select a book by number or use navigation:",
			markup: fmt.Sprintf(`{"inline_keyboard":[[{"text":"1","callback_data":"select:%d"}]]}`,
				books[scanfixture.EntryNoAuthor]),
		},
		{
			name: "title search",
			run: func() (*CommandResult, error) {
				return cp.ExecuteDirectBookSearch(t.Context(), "the GREAT", telegramID)
			},
			message: "📚 Результаты поиска для \"the GREAT\":\nСтраница 1 из 1 (всего найдено 1 книг)\n\n" +
				"1. the GREAT book — jOHN O'brien, ЛЕВ толстой (серия: Saga)\n\n" +
				"💡 Выберите книгу по номеру или используйте навигацию:",
			markup: fmt.Sprintf(`{"inline_keyboard":[[{"text":"1","callback_data":"select:%d"}]]}`,
				books[scanfixture.EntryLatin]),
		},
		{
			name: "title and author search",
			run: func() (*CommandResult, error) {
				return cp.ExecuteDirectCombinedSearch(t.Context(), "война", "толстой", telegramID)
			},
			message: "📚 Search results for \"война\" by толстой:\nPage 1 of 1 (total found 1 books)\n\n" +
				"1. " + multiLine + " (series: Эпопея)\n\n" +
				"💡 Select a book by number or use navigation:",
			markup: selectMulti,
		},
		{
			name: "collection",
			run:  func() (*CommandResult, error) { return cp.ExecuteCollectionBooks(collection, telegramID, 0, 5) },
			message: "📦 Подборка \"Подборка\":\nСтраница 1 из 1 (всего 1 книг)\n\n" +
				"1. " + multiLine + " (серия: Эпопея)\n\n" +
				"💡 Выберите книгу по номеру или используйте навигацию:",
			markup: selectMulti,
		},
		{
			name: "favorites",
			run:  func() (*CommandResult, error) { return cp.ExecuteShowFavorites(telegramID, 0, 5) },
			message: "⭐ Избранные книги:\nСтраница 1 из 1 (всего 1 книг)\n\n" +
				"1. " + multiLine + " (серия: Эпопея) [ru]\n\n" +
				"💡 Выберите книгу по номеру или используйте навигацию:",
			markup: selectMulti,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, runErr := c.run()
			require.NoError(t, runErr)
			assert.Equal(t, c.message, res.Message)
			markup, marshalErr := json.Marshal(res.ReplyMarkup)
			require.NoError(t, marshalErr)
			assert.Equal(t, c.markup, string(markup))
		})
	}
}
