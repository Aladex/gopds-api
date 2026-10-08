package telegram

import (
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Part A of phase 9: the exact book card the bot sends after a book is picked,
// for books written by the real scan path. It is built from database.GetBook,
// the same lookup the select callback uses.
func TestScanCharacterizationTelegramBookCard(t *testing.T) {
	scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)

	h := &CallbackHandler{}
	for entry, want := range map[string]string{
		scanfixture.EntryMultiAuthor: "<b>война и МИР</b>\n\nРоман-эпопея о войне и мире.\n\nВыберите формат для скачивания:",
		scanfixture.EntryNoAuthor:    "<b>Сборник без автора</b>\n\nВыберите формат для скачивания:",
		scanfixture.EntryLatin:       "<b>the GREAT book</b>\n\nВыберите формат для скачивания:",
	} {
		t.Run(entry, func(t *testing.T) {
			book, err := database.GetBook(books[entry])
			require.NoError(t, err)
			assert.Equal(t, want, h.formatBookDetailsMessage(book))
		})
	}
}
