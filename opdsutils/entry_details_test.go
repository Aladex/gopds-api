package opdsutils

import (
	"strings"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a book entry shows beyond its row: the translators the file credits
// name it in the title and the summary, and its card's other files are linked
// as their own feed. All of it on top of the entry every book always had.

// entryBook is one book with a translator-creditless detail, the smallest
// input CreateItem renders an entry from.
func entryBook() *models.Book {
	return &models.Book{
		ID:           42,
		Title:        "Солярис",
		Format:       "fb2",
		Lang:         "ru",
		Annotation:   "Роман о контакте.",
		RegisterDate: time.Now().Add(-24 * time.Hour),
	}
}

// variantsFeedLink finds the entry's link to the feed of its card's other
// files, if it has one.
func variantsFeedLink(links []Link) (Link, bool) {
	for _, l := range links {
		if strings.HasPrefix(l.Href, "/opds/book/") && strings.HasSuffix(l.Href, "/variants") {
			return l, true
		}
	}
	return Link{}, false
}

func TestCreateItemNamesTheFirstTranslatorInTheTitle(t *testing.T) {
	item := CreateItem(entryBook(), nil, models.BookEntryDetail{
		Translators: []string{"Нора Галь", "Рита Райт-Ковалёва"},
	}, false)
	assert.Equal(t, "Солярис (пер. Нора Галь)", item.Title,
		"the suffix names the first translator only, so files of one work differ")
}

func TestCreateItemTitlesTheTranslatorLineInTheSummary(t *testing.T) {
	item := CreateItem(entryBook(), nil, models.BookEntryDetail{
		Translators: []string{"Нора Галь", "Рита Райт-Ковалёва"},
	}, false)
	assert.Equal(t, "Роман о контакте.\nПеревод: Нора Галь, Рита Райт-Ковалёва", item.Description)
}

func TestCreateItemSummarySaysAndOthersWhenMoreTranslatorsRemain(t *testing.T) {
	book := entryBook()
	book.Annotation = ""
	item := CreateItem(book, nil, models.BookEntryDetail{
		Translators:     []string{"Нора Галь", "Рита Райт-Ковалёва"},
		TranslatorsMore: true,
	}, false)
	assert.Equal(t, "Перевод: Нора Галь, Рита Райт-Ковалёва и др.", item.Description)
}

func TestCreateItemWithoutTranslatorsKeepsTheTitleAndSummary(t *testing.T) {
	item := CreateItem(entryBook(), nil, models.BookEntryDetail{VariantCount: 2}, false)
	assert.Equal(t, "Солярис", item.Title, "no translator, no suffix")
	assert.Equal(t, "Роман о контакте.", item.Description, "no translator, no line")
}

func TestCreateItemKOReaderKeepsTheTranslatorLine(t *testing.T) {
	book := entryBook()
	book.Annotation = strings.Repeat("Длинная аннотация романа. ", 4)
	item := CreateItem(book, nil, models.BookEntryDetail{Translators: []string{"Нора Галь"}}, true)
	want := book.Annotation[:20] + "\nПеревод: Нора Галь"
	assert.Equal(t, want, item.Description,
		"the truncated annotation keeps its cut, the translator line stays whole")
}

func TestCreateItemLinksTheVariantsOfACardOfMoreThanOneFile(t *testing.T) {
	item := CreateItem(entryBook(), nil, models.BookEntryDetail{VariantCount: 3}, false)
	link, ok := variantsFeedLink(item.Link)
	require.True(t, ok, "a card of three files links its variants feed")
	assert.Equal(t, "/opds/book/42/variants", link.Href)
	assert.Equal(t, "related", link.Rel)
	assert.Equal(t, "application/atom+xml;profile=opds-catalog;kind=acquisition", link.Type)
	assert.Equal(t, "Другие переводы и редакции (3)", link.Title, "the title counts the card's files")
}

func TestCreateItemLinksNoVariantsOfACardOfOneFile(t *testing.T) {
	item := CreateItem(entryBook(), nil, models.BookEntryDetail{VariantCount: 1}, false)
	_, ok := variantsFeedLink(item.Link)
	assert.False(t, ok, "a card of one file links nothing")
}

func TestCreateItemOfAnUnreadPageKeepsThePlainEntry(t *testing.T) {
	item := CreateItem(entryBook(), nil, models.BookEntryDetail{}, false)
	assert.Equal(t, "Солярис", item.Title)
	assert.Equal(t, "Роман о контакте.", item.Description)
	_, ok := variantsFeedLink(item.Link)
	assert.False(t, ok, "an unread card count links no variants")
}
