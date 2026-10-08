package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Part A of phase 9: the exact Telegram texts and keyboards the bot builds
// for books written by the real scan path — authors joined in link order,
// the legacy names, the first series — so the dual write can be shown to
// change none of it. Only row IDs in callback data vary per run.
func TestScanCharacterizationTelegramLists(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)

	// A linked Telegram user with no language preference: every book is in
	// scope.
	const telegramID = 4242
	_, err := db.Exec(`INSERT INTO auth_user (password, is_superuser, username, email, date_joined, telegram_id)
		VALUES ('', false, 'characterization', 'characterization@fixture.local', now(), ?)`, telegramID)
	require.NoError(t, err)

	authors := map[string]int64{}
	var rows []struct {
		ID       int64
		FullName string
	}
	_, err = db.Query(&rows, `SELECT id, full_name FROM opds_catalog_author`)
	require.NoError(t, err)
	for _, r := range rows {
		authors[r.FullName] = r.ID
	}

	cp := newCommandProcessorWithDeps(services.NewSearchService(database.NewPGSearchRepository(db)),
		database.GetUserByTelegramID)
	ctx := context.Background()

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
				"1. война и МИР — Толстой лев, пушкин Анна (series: Эпопея)\n\n" +
				"💡 Select a book by number or use navigation:",
			markup: fmt.Sprintf(`{"inline_keyboard":[[{"text":"1","callback_data":"select:%d"}]]}`,
				books[scanfixture.EntryMultiAuthor]),
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
			run:  func() (*CommandResult, error) { return cp.ExecuteDirectBookSearch(ctx, "the GREAT", telegramID) },
			message: "📚 Результаты поиска для \"the GREAT\":\nСтраница 1 из 1 (всего найдено 1 книг)\n\n" +
				"1. the GREAT book — O'brien jOHN, толстой ЛЕВ (серия: Saga)\n\n" +
				"💡 Выберите книгу по номеру или используйте навигацию:",
			markup: fmt.Sprintf(`{"inline_keyboard":[[{"text":"1","callback_data":"select:%d"}]]}`,
				books[scanfixture.EntryLatin]),
		},
		{
			// Two author rows that differ only in case stay two authors.
			name: "author search",
			run:  func() (*CommandResult, error) { return cp.ExecuteDirectAuthorSearch(ctx, "толстой", telegramID) },
			message: "👤 Результаты поиска авторов для \"толстой\":\nСтраница 1 из 1 (всего найдено 2 авторов)\n\n" +
				"1. Толстой лев\n2. толстой ЛЕВ\n\n" +
				"💡 Выберите автора по номеру или используйте навигацию:",
			markup: fmt.Sprintf(`{"inline_keyboard":[[{"text":"1","callback_data":"author:%d"},`+
				`{"text":"2","callback_data":"author:%d"}]]}`, authors["Толстой лев"], authors["толстой ЛЕВ"]),
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
