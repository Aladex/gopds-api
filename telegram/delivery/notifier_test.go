package delivery

import (
	"strings"
	"testing"
	"time"

	"gopds-api/models"

	tgbot "github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
)

func TestHumanSizeWritesRussianUnits(t *testing.T) {
	for n, want := range map[int]string{
		1:          "1 КБ",
		999:        "1 КБ",
		850_000:    "850 КБ",
		1_000_000:  "1 МБ",
		1_449_000:  "1,4 МБ",
		49_990_000: "49,9 МБ",
	} {
		assert.Equal(t, want, HumanSize(n), "size %d", n)
	}
}

func TestBookLineNamesTheAuthors(t *testing.T) {
	book := &models.Book{Title: "Основание", Authors: []models.Author{{FullName: "Айзек Азимов"}, {FullName: " "}}}
	assert.Equal(t, "Основание — Айзек Азимов", BookLine(book))
	assert.Equal(t, "Без автора", BookLine(&models.Book{Title: "Без автора"}))
}

func TestFileCaptionStaysInsideTelegramsLimit(t *testing.T) {
	book := &models.Book{Title: strings.Repeat("Очень длинное название ", 100)}
	caption := fileCaption(book, "epub", 1_449_000)
	assert.LessOrEqual(t, len([]rune(caption)), 1024)
	assert.True(t, strings.HasSuffix(caption, "\nEPUB · 1,4 МБ"), caption)
}

// Every outcome the reader can act on has words and a way on; outcomes the
// chat cannot hear, or the file itself, have none.
func TestOutcomeScreens(t *testing.T) {
	book := &models.Book{ID: 7, Title: "Основание"}
	site := "https://library.example.test"

	text, markup := outcomeScreen(site, book, "mobi", models.OutcomeConversionFailed)
	assert.True(t, strings.HasPrefix(text, "Не удалось подготовить MOBI\nОснование"))
	assert.Equal(t, "download:mobi:7", markup.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "Повторить MOBI", markup.InlineKeyboard[0][0].Text)
	assert.Len(t, markup.InlineKeyboard[1], 3, "the other three formats")
	assert.Equal(t, site+"/books/page/1?book_id=7", markup.InlineKeyboard[2][0].URL)

	text, markup = outcomeScreen(site, book, "epub", models.OutcomeUncertain)
	assert.Contains(t, text, "Повторная отправка может создать второй файл.")
	assert.Equal(t, [][]string{{"Отправить ещё раз"}}, labels(markup.InlineKeyboard))

	text, _ = outcomeScreen(site, book, "epub", models.OutcomeSendRejected)
	assert.True(t, strings.HasPrefix(text, "Файл готов, но Telegram не принял отправку."))

	for _, silent := range []string{models.OutcomeSent, models.OutcomeBotBlocked, models.OutcomeTokenRejected} {
		text, markup = outcomeScreen(site, book, "epub", silent)
		assert.Empty(t, text, silent)
		assert.Nil(t, markup, silent)
	}
}

func labels(rows [][]tgbot.InlineKeyboardButton) [][]string {
	var out [][]string
	for _, row := range rows {
		var texts []string
		for i := range row {
			texts = append(texts, row[i].Text)
		}
		out = append(out, texts)
	}
	return out
}

func TestAFailedLibraryIsSaidAsSuch(t *testing.T) {
	text, markup := outcomeScreen("", &models.Book{ID: 7, Title: "Основание"}, "epub", models.OutcomeLibraryFailed)
	assert.Equal(t, "Не удалось отправить EPUB\nОснование\n\nБиблиотека сейчас не отвечает. Попробуй ещё раз чуть позже.", text)
	assert.Equal(t, [][]string{{"Повторить EPUB"}}, labels(markup.InlineKeyboard))

	text, _ = outcomeScreen("", &models.Book{ID: 7}, "epub", models.OutcomeLibraryFailed)
	assert.Equal(t, "Не удалось отправить EPUB\n\nБиблиотека сейчас не отвечает. Попробуй ещё раз чуть позже.", text,
		"a book that could not be read has no title to name")
}

func TestARepeatedPressHearsWhereItsFileIs(t *testing.T) {
	ended := time.Now()
	assert.Equal(t, ToastInProgress, RepeatToast(&models.TelegramDelivery{State: models.DeliverySending}))
	assert.Equal(t, ToastAlreadySent, RepeatToast(&models.TelegramDelivery{State: models.DeliverySent, FinishedAt: &ended}))
	assert.Equal(t, ToastJustEnded, RepeatToast(&models.TelegramDelivery{State: models.DeliveryFailed, FinishedAt: &ended}))
}
