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

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Part A of phase 9: the exact Telegram texts and keyboards the bot builds
// for books written by the real scan path — authors joined in link order,
// the legacy names, the first series — so the dual write can be shown to
// change none of it. Only row IDs in callback data vary per run.
// characterizationTelegramID is the linked Telegram user of the scanned
// fixture catalog; it has no language preference, so every book is in scope.
const characterizationTelegramID = 4242

// scanCharacterization scans the fixture books into a scratch database with
// a linked Telegram user, and returns the database, the book IDs by fixture
// entry and the catalog author IDs by name.
func scanCharacterization(t *testing.T) (db *pg.DB, books, authors map[string]int64) {
	t.Helper()
	db = scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books = scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)

	// The Telegram user reaches the account through an active binding of a
	// bot; the bot's secrets are never read here, so they are placeholders.
	_, err := db.Exec(`
		WITH u AS (
			INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
			VALUES ('', false, 'characterization', 'characterization@fixture.local', now()) RETURNING id),
		b AS (
			INSERT INTO telegram_bot (tg_bot_id, kind, webhook_path, webhook_secret_enc, nav_key_enc)
			VALUES (1, 'manual', gen_random_uuid(), decode('00', 'hex'), decode('00', 'hex')) RETURNING id)
		INSERT INTO telegram_binding (user_id, bot_ref, tg_user_id, generation, status)
		SELECT u.id, b.id, ?, 1, 'active' FROM u, b`, characterizationTelegramID)
	require.NoError(t, err)

	authors = map[string]int64{}
	var rows []struct {
		ID       int64
		FullName string
	}
	_, err = db.Query(&rows, `SELECT id, full_name FROM opds_catalog_author`)
	require.NoError(t, err)
	for _, r := range rows {
		authors[r.FullName] = r.ID
	}
	return db, books, authors
}

func TestScanCharacterizationTelegramLists(t *testing.T) {
	db, books, authors := scanCharacterization(t)
	const telegramID = characterizationTelegramID

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
