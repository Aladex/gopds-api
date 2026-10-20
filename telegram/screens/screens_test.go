package screens

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env() *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpHome} }

var testCardKeys = &CardKeys{
	Download:   func(string) *cbv2.Envelope { return env() },
	Favorite:   func(bool) *cbv2.Envelope { return env() },
	Annotation: env(),
	Back:       env(),
	Home:       env(),
}

// worstBook is a catalog row at every column's limit: a 10 000-character
// annotation of many short paragraphs, long names, many authors and genres.
func worstBook() *models.Book {
	b := &models.Book{ID: 1, Title: strings.Repeat("Очень длинное название ", 60), Lang: "ru",
		DocDate:    time.Now().AddDate(-25, 0, 0).Format(time.DateOnly),
		Annotation: strings.Repeat("Абзац аннотации.\n", 588)}
	for i := 0; i < 50; i++ {
		b.Authors = append(b.Authors, models.Author{FullName: strings.Repeat("Имя ", 25)})
		b.Genres = append(b.Genres, models.Genre{Title: strings.Repeat("жанр ", 10)})
	}
	b.Series = []*models.Series{{Ser: strings.Repeat("Серия ", 40), SerNo: 12}}
	return b
}

// (p) The worst row renders inside Telegram's limits either way.
func TestTheWorstBookFitsBothRenderings(t *testing.T) {
	plain, cut := CardScreen(&Card{Book: worstBook(), DefaultFormat: "epub", SiteURL: "https://x"}, testCardKeys)
	assert.True(t, cut)
	assert.LessOrEqual(t, plain.Doc.PlainLength(), 4096)

	rich, _ := CardScreen(&Card{Book: worstBook(), Rich: true, DefaultFormat: "epub", SiteURL: "https://x",
		Cover: &Photo{BookID: 1, Data: []byte("jpg")}}, testCardKeys)
	msg := rich.Doc.Rich()
	assert.Less(t, countBlocks(t, msg.Blocks), 60)
	raw, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.Less(t, utf8.RuneCount(raw), 32768, "text and markup together stay under the text limit")
}

func countBlocks(t *testing.T, blocks any) int {
	t.Helper()
	raw, err := json.Marshal(blocks)
	require.NoError(t, err)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(raw, &list))
	n := 0
	for _, b := range list {
		n++
		if inner, ok := b["blocks"].([]any); ok {
			n += len(inner)
		}
		if cells, ok := b["cells"].([]any); ok {
			n += len(cells)
		}
	}
	return n
}

func TestTheCardsKeyboard(t *testing.T) {
	book := worstBook()
	screen, _ := CardScreen(&Card{Book: book, DefaultFormat: "mobi", Favorite: true,
		Preparing: map[string]bool{"fb2": true}, BackText: ButtonToResults, SiteURL: "https://x"}, testCardKeys)
	var labels [][]string
	for _, row := range screen.Keys {
		var r []string
		for _, b := range row {
			r = append(r, b.Text)
		}
		labels = append(labels, r)
	}
	assert.Equal(t, []string{"MOBI", "EPUB", "⏳ Готовлю FB2…", "ZIP"}, labels[0])
	assert.Equal(t, "primary", screen.Keys[0][0].Style)
	assert.True(t, screen.Keys[0][2].Disabled, "a file on its way is shown, not pressed")
	assert.Nil(t, screen.Keys[0][2].Env)
	assert.False(t, screen.Keys[0][1].Disabled)
	assert.Equal(t, []string{"★ В избранном"}, labels[1])
	assert.Equal(t, []string{ButtonToResults, ButtonHome}, labels[len(labels)-1])
}

func TestTheFileName(t *testing.T) {
	b := &models.Book{Title: "Пикник: на обочине?", Authors: []models.Author{{FullName: "Аркадий Стругацкий"}}}
	assert.Equal(t, "Аркадий Стругацкий - Пикник_ на обочине_.epub", FileName(b, "epub"))
	b.Authors = append(b.Authors, models.Author{FullName: "Борис Стругацкий"})
	assert.Equal(t, "Аркадий Стругацкий и др. - Пикник_ на обочине_.fb2", FileName(b, "fb2"))
	assert.Equal(t, "book.zip", FileName(&models.Book{}, "zip"))
}

func TestAResultsPageInBothRenderings(t *testing.T) {
	var books []models.Book
	for i := 0; i < 5; i++ {
		books = append(books, *worstBook())
	}
	res := &Results{Query: "<b>&amp;", Lang: "ru", Books: books, BookTotal: 37, AuthorTotal: 2}
	keys := &ResultsKeys{
		Tab: func(uint8) *cbv2.Envelope { return env() }, RetryTab: func(uint8) *cbv2.Envelope { return env() },
		RetryAll: env(), Page: func(int) *cbv2.Envelope { return env() }, Book: func(int64) *cbv2.Envelope { return env() },
		Home: env(),
	}
	s := ResultsScreen(res, keys)
	text := s.Doc.Plain()
	assert.Contains(t, text, "Поиск: «&lt;b&gt;&amp;amp;»", "escaped")
	assert.Contains(t, text, "Страница 1 из 8")
	assert.LessOrEqual(t, s.Doc.PlainLength(), 4096)
	assert.Equal(t, "✓ Книги · 37", s.Keys[0][0].Text)
	assert.LessOrEqual(t, utf8.RuneCountInString(s.Keys[1][0].Text), 32)
}
