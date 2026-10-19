package screens

import (
	"strings"
	"testing"
	"unicode/utf16"

	"gopds-api/models"

	tgmodels "github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func inlineBook() *models.Book {
	return &models.Book{ID: 42, Title: "Солярис", Lang: "ru", Path: "lib/a.zip", FileName: "1.fb2", Cover: true,
		Authors: []models.Author{{ID: 1, FullName: "Станислав Лем"}},
		Series:  []*models.Series{{ID: 3, Ser: "Космос", SerNo: 2}}}
}

// A result: the id, the title, the description, the text a pick sends with
// its one link over the title, and no buttons.
func TestAnInlineArticle(t *testing.T) {
	a := InlineArticle(inlineBook(), "https://site.example/", "")
	assert.Equal(t, "b42", a.ID)
	assert.Equal(t, "Солярис", a.Title)
	assert.Equal(t, "Станислав Лем · Космос, кн. 2 · русский", a.Description)
	assert.Empty(t, a.ThumbnailURL, "no media host, no cover")
	assert.Nil(t, a.ReplyMarkup)
	content, ok := a.InputMessageContent.(*tgmodels.InputTextMessageContent)
	require.True(t, ok)
	assert.Equal(t, "📖 Солярис — Станислав Лем", content.MessageText)
	require.Len(t, content.Entities, 1)
	e := content.Entities[0]
	assert.Equal(t, tgmodels.MessageEntityTypeTextLink, e.Type)
	assert.Equal(t, "https://site.example/books/page/1?book_id=42", e.URL)
	units := utf16.Encode([]rune(content.MessageText))
	assert.Equal(t, "Солярис", string(utf16.Decode(units[e.Offset:e.Offset+e.Length])), "the link covers the title")
}

// With a media host, a book with a cover gets its public cover URL; one
// without a cover gets none.
func TestAnInlineArticleCover(t *testing.T) {
	b := inlineBook()
	a := InlineArticle(b, "https://site.example", "https://media.example/")
	assert.Equal(t, "https://media.example/books-posters/lib/a-zip/1-fb2.jpg", a.ThumbnailURL)
	b.Cover = false
	assert.Empty(t, InlineArticle(b, "https://site.example", "https://media.example").ThumbnailURL)
}

// A long title is cut for the picker.
func TestAnInlineArticleCutsALongTitle(t *testing.T) {
	b := inlineBook()
	b.Title = strings.Repeat("Очень длинное название ", 20)
	a := InlineArticle(b, "https://site.example", "")
	assert.LessOrEqual(t, len([]rune(a.Title)), inlineTitleRunes)
}

// Only this site's book link names a book.
func TestBookIDFromURL(t *testing.T) {
	const site = "https://site.example"
	id, ok := BookIDFromURL(site, "https://site.example/books/page/1?book_id=42")
	assert.True(t, ok)
	assert.Equal(t, int64(42), id)
	for _, link := range []string{
		"https://evil.example/books/page/1?book_id=42",
		"https://site.example/books/page/1?book_id=42&x=1",
		"https://site.example/books/page/2?book_id=42",
		"https://site.example/books/page/1?book_id=",
		"https://site.example/books/page/1?book_id=0",
		"https://site.example/books/page/1?book_id=-4",
	} {
		_, ok := BookIDFromURL(site, link)
		assert.False(t, ok, link)
	}
}
