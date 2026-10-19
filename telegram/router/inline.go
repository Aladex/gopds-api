package router

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgapi"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// Inline search (plan-ui.md §3): the owner types «@bot …» in the chat with
// the bot and picks a book; the pick comes back as their own message «via»
// the bot, which becomes a card.

// The inline answer's limits (§3.2–3.3).
const (
	inlinePage         = 20
	inlineMaxOffset    = 200
	inlineCacheSeconds = 5
	// inlineSender is the chat type of a query typed in the chat with the
	// bot itself.
	inlineSender = "sender"
)

// inline answers an inline query: books for the owner in the bot's own chat,
// nothing for anyone or anywhere else. Every answer is personal and short-
// lived.
func (r *Router) inline(ctx context.Context, b *Bot, q *tgbot.InlineQuery) {
	if !r.inlineOn(ctx, b) {
		// A query arriving proves inline mode is on.
		if _, err := database.SetTelegramInline(ctx, b.Ref, true); err != nil {
			logging.Warnf("Telegram bot %d: recording inline mode: %v", b.Ref, err)
		}
	}
	results, next := []tgbot.InlineQueryResult{}, ""
	if mayAskInline(b, q) {
		results, next = r.inlineResults(ctx, b, q)
	}
	if _, err := b.Client.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
		InlineQueryID: q.ID, Results: results, CacheTime: inlineCacheSeconds, IsPersonal: true, NextOffset: next,
	}); err != nil && !errors.Is(err, context.Canceled) {
		logging.Debugf("Telegram bot %d: answering an inline query: %v", b.Ref, tgapi.RedactURL(err))
	}
}

// mayAskInline is §3.2's guard: the owner, an active bot and binding, and
// the bot's own chat.
func mayAskInline(b *Bot, q *tgbot.InlineQuery) bool {
	return b.Active && b.Binding != nil && b.Binding.BlockedAt == nil && q.From != nil &&
		q.From.ID == b.Binding.TGUserID && q.ChatType == inlineSender
}

// inlineResults is one page of books for a query, and the next page's
// offset: a search in the account's search language, or with no query the
// newest favorites.
func (r *Router) inlineResults(ctx context.Context, b *Bot, q *tgbot.InlineQuery) (
	results []tgbot.InlineQueryResult, next string) {
	results = []tgbot.InlineQueryResult{}
	offset, err := strconv.Atoi(q.Offset)
	if q.Offset == "" {
		offset, err = 0, nil
	}
	if err != nil || offset < 0 || offset >= inlineMaxOffset {
		return results, ""
	}
	query := strings.Join(strings.Fields(q.Query), " ")
	if utf8.RuneCountInString(query) > screens.MaxQuery {
		return results, ""
	}
	books, total, err := r.inlineBooks(ctx, b, query, offset)
	if err != nil {
		logging.Warnf("Telegram bot %d: inline search: %v", b.Ref, err)
		return results, ""
	}
	for i := range books {
		results = append(results, screens.InlineArticle(&books[i], r.cfg.SiteURL, r.cfg.MediaBaseURL))
	}
	following := offset + inlinePage
	if len(books) < inlinePage || following >= total || following >= inlineMaxOffset {
		return results, ""
	}
	return results, strconv.Itoa(following)
}

// inlineBooks reads one page of an inline answer's books.
func (r *Router) inlineBooks(ctx context.Context, b *Bot, query string, offset int) ([]models.Book, int, error) {
	if query == "" {
		return database.GetBooks(b.Binding.UserID, models.BookFilters{Fav: true, Limit: inlinePage, Offset: offset})
	}
	ui, err := database.GetTelegramUserUI(ctx, b.Binding.UserID)
	if err != nil {
		return nil, 0, err
	}
	sctx, cancel := context.WithTimeout(ctx, r.cfg.SearchTimeout)
	defer cancel()
	got, err := r.cfg.Search.SearchBooks(sctx, models.BookSearchRequest{
		Query: query, Language: ui.SearchLang, Limit: inlinePage, Offset: offset,
	})
	return got.Books, got.Total, err
}

// viaBot handles the owner's message sent through an inline pick: if it is
// this bot's pick of a book still there, a card in a new panel takes the
// message's place. Another bot's message, or a link that is not one of ours,
// is left alone.
func (r *Router) viaBot(ctx context.Context, b *Bot, msg *tgbot.Message) {
	if msg.ViaBot.ID != b.TGBotID {
		return
	}
	bookID, ok := pickedBook(r.cfg.SiteURL, msg)
	if !ok {
		logging.Debugf("Telegram bot %d: a message via the bot names no book", b.Ref)
		return
	}
	book, err := database.GetBookCard(ctx, bookID)
	switch {
	case isNoRows(err) || err == nil && !visible(book):
		r.sendPlainText(ctx, b, msg.Chat.ID, screens.CopyUnavailable)
		return
	case err != nil:
		r.sendPlainText(ctx, b, msg.Chat.ID, screens.CopyLibraryDown)
		return
	}
	home := &models.TelegramNode{UserID: b.Binding.UserID, Screen: int16(ScrHome)}
	if err := upsertNode(ctx, home); err != nil {
		r.sendPlainText(ctx, b, msg.Chat.ID, screens.CopyLibraryDown)
		return
	}
	panel, toast := r.openPanel(ctx, b, msg.Chat.ID, cardNode(b.Binding.UserID, 0, bookID, home.ID))
	if panel == nil {
		// No card: the pick stays where it is. A send Telegram refused
		// (a 429) left nothing sent, and the inbox runs the update again.
		if toast != "" {
			r.sendPlainText(ctx, b, msg.Chat.ID, toast)
		}
		return
	}
	if _, err := b.Client.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: msg.Chat.ID, MessageID: msg.ID}); err != nil {
		logging.Infof("Telegram bot %d: removing the picked link: %v", b.Ref, tgapi.RedactURL(err))
	}
}

// pickedBook is the book an inline pick's link names.
func pickedBook(siteURL string, msg *tgbot.Message) (int64, bool) {
	for _, e := range msg.Entities {
		if e.Type == tgbot.MessageEntityTypeTextLink {
			if id, ok := screens.BookIDFromURL(siteURL, e.URL); ok {
				return id, true
			}
		}
	}
	return 0, false
}
