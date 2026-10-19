package screens

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"

	"gopds-api/internal/posters"
	"gopds-api/models"
	"gopds-api/telegram/ru"

	tgmodels "github.com/go-telegram/bot/models"
)

// An inline result (plan-ui.md §3.3, §3.5): a book as the picker shows it,
// and the text the reader sends by choosing it — a link to the book's page
// on the site, which the bot turns into a card.

// inlineTitleRunes is the longest title in the picker.
const inlineTitleRunes = 120

// inlineMarker starts the text a chosen result sends.
const inlineMarker = "📖 "

// InlineResultID is the result id of a book: «b{id}».
func InlineResultID(bookID int64) string { return "b" + strconv.FormatInt(bookID, 10) }

// InlineArticle is one book in the inline picker. mediaBase is where
// Telegram can fetch covers from; "" leaves the result without one. Nothing
// in it is private: a public page link, a public cover URL, the title.
func InlineArticle(b *models.Book, siteURL, mediaBase string) *tgmodels.InlineQueryResultArticle {
	title := ru.Truncate(strings.TrimSpace(b.Title), inlineTitleRunes)
	text := inlineMarker + title
	if author := Authors(b, listAuthors); author != "" {
		text += " — " + author
	}
	link := tgmodels.MessageEntity{
		Type:   tgmodels.MessageEntityTypeTextLink,
		Offset: utf16Len(inlineMarker),
		Length: utf16Len(title),
		URL:    BookURL(siteURL, b.ID),
	}
	off := true
	return &tgmodels.InlineQueryResultArticle{
		ID:          InlineResultID(b.ID),
		Title:       title,
		Description: inlineDescription(b),
		InputMessageContent: &tgmodels.InputTextMessageContent{
			MessageText: text, Entities: []tgmodels.MessageEntity{link},
			LinkPreviewOptions: &tgmodels.LinkPreviewOptions{IsDisabled: &off},
		},
		ThumbnailURL: CoverURL(mediaBase, b),
	}
}

// inlineDescription is «{Автор} · {Серия, кн. N | год} · {язык}».
func inlineDescription(b *models.Book) string {
	var parts []string
	if author := Authors(b, listAuthors); author != "" {
		parts = append(parts, author)
	}
	if series := SeriesLine(b); series != "" {
		parts = append(parts, series)
	} else if year := Year(b); year != "" {
		parts = append(parts, year)
	}
	if lang := strings.TrimSpace(b.Lang); lang != "" {
		parts = append(parts, ru.Language(lang))
	}
	return strings.Join(parts, " · ")
}

// CoverURL is a book's cover on the public media host; "" without the host
// or a cover.
func CoverURL(mediaBase string, b *models.Book) string {
	if mediaBase == "" || !b.Cover {
		return ""
	}
	path := (&url.URL{Path: "/books-posters/" + posters.RelativePath(b.Path, b.FileName)}).EscapedPath()
	return strings.TrimRight(mediaBase, "/") + path
}

// utf16Len is a string's length as Telegram counts entity offsets.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// BookIDFromURL is the book a link made by BookURL names, if link is exactly
// such a link for this site.
func BookIDFromURL(siteURL, link string) (int64, bool) {
	prefix := strings.TrimRight(siteURL, "/") + "/books/page/1?book_id="
	digits, ok := strings.CutPrefix(link, prefix)
	if !ok || digits == "" || len(digits) > 18 || strings.TrimLeft(digits, "0123456789") != "" {
		return 0, false
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	return id, err == nil && id > 0
}
