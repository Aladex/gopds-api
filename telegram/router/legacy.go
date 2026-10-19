package router

import (
	"context"
	"strconv"
	"strings"

	"gopds-api/models"
	"gopds-api/telegram/screens"

	tgbot "github.com/go-telegram/bot/models"
)

// The old interface's buttons, still on messages in the v2 bots' chats
// (plan.md S5, legacy compat). Lists cannot be paged any more — their state
// lived in Redis — so their pager says so; a book, an author or a collection
// opens in a new panel. A file button keeps going to the legacy handlers,
// which make an S2 delivery job of it (plan-ui.md §1.11).

// The old callback data.
const (
	legacyPrev       = "prev_page"
	legacyNext       = "next_page"
	legacySelect     = "select:"
	legacyAuthor     = "author:"
	legacyCollection = "collection:"
	legacyDownload   = "download:"
)

// legacy handles a press on an old button. It reports false for what the
// legacy handlers still do.
func (r *Router) legacy(ctx context.Context, b *Bot, q *tgbot.CallbackQuery, msg *tgbot.Message) bool {
	data := strings.TrimSpace(q.Data)
	if strings.HasPrefix(data, legacyDownload) {
		return false
	}
	if msg == nil || b.Binding == nil || q.From.ID != b.Binding.TGUserID || msg.Chat.Type != tgbot.ChatTypePrivate {
		r.answer(ctx, b, q, screens.CopyButtonDenied)
		return true
	}
	r.ensureNoCommands(ctx, b)
	if data == legacyPrev || data == legacyNext {
		r.answer(ctx, b, q, "")
		r.notice(ctx, b, msg.Chat.ID, screens.CopyOldList, true)
		return true
	}
	r.answer(ctx, b, q, r.legacyOpen(ctx, b, msg.Chat.ID, data))
	return true
}

// legacyOpen opens what an old button names, in a new panel, and returns
// the toast.
func (r *Router) legacyOpen(ctx context.Context, b *Bot, chatID int64, data string) string {
	prefix, rest, _ := strings.Cut(data, ":")
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return screens.CopyUnsupported
	}
	userID := b.Binding.UserID
	switch prefix + ":" {
	case legacySelect:
		return r.sendCard(ctx, b, chatID, id)
	case legacyAuthor:
		return r.openNewList(ctx, b, chatID, &models.TelegramView{UserID: userID, Kind: ViewAuthorBooks, AuthorID: id,
			Lang: inheritLang(ctx, b, 0)})
	case legacyCollection:
		return r.openNewList(ctx, b, chatID, &models.TelegramView{UserID: userID, Kind: ViewCollectionBooks, CollectionID: id})
	}
	return screens.CopyUnsupported
}

// openNewList sends page one of a new list view as a new panel.
func (r *Router) openNewList(ctx context.Context, b *Bot, chatID int64, v *models.TelegramView) string {
	if err := newView(ctx, v); err != nil {
		return failedWrite(b, "recording a list", err)
	}
	return r.open(ctx, b, chatID, &models.TelegramNode{UserID: v.UserID, ViewID: v.ID, Screen: int16(screenOfView(v.Kind))})
}
