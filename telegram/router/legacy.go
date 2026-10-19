package router

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/screens"

	tgbot "github.com/go-telegram/bot/models"
)

// The old interface's buttons, still on messages in the v2 bots' chats
// (plan.md S5, legacy compat). Lists cannot be paged any more — their state
// lived in Redis — so their pager says so; a book, an author or a collection
// opens in a new panel. A file button makes an S2 delivery job, as it did
// (plan-ui.md §1.11).

// The old callback data.
const (
	legacyPrev       = "prev_page"
	legacyNext       = "next_page"
	legacySelect     = "select:"
	legacyAuthor     = "author:"
	legacyCollection = "collection:"
	legacyDownload   = "download:"
)

// legacy handles a press on an old button.
func (r *Router) legacy(ctx context.Context, b *Bot, q *tgbot.CallbackQuery, msg *tgbot.Message) bool {
	data := strings.TrimSpace(q.Data)
	if msg == nil || b.Binding == nil || q.From.ID != b.Binding.TGUserID || msg.Chat.Type != tgbot.ChatTypePrivate {
		r.answer(ctx, b, q, screens.CopyButtonDenied)
		return true
	}
	r.setupChat(ctx, b, msg.Chat.ID)
	if strings.HasPrefix(data, legacyDownload) {
		if toast, answer := r.legacyDownload(ctx, b, q, msg, data); answer {
			r.answer(ctx, b, q, toast)
		}
		return true
	}
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

// legacyDownload is an old card's file button, «download:{format}:{id}»: a
// delivery job like any other, answered at once; the old card is never
// edited. A second press while the file is on its way gets «already being
// prepared». It reports false when this process lost the press.
func (r *Router) legacyDownload(
	ctx context.Context, b *Bot, q *tgbot.CallbackQuery, msg *tgbot.Message, data string,
) (toast string, answer bool) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || r.cfg.Delivery == nil {
		return screens.CopyUnsupported, true
	}
	bookID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || bookID <= 0 {
		return screens.CopyUnsupported, true
	}
	format := strings.ToLower(parts[1])
	job, admission, err := r.cfg.Delivery.Enqueue(ctx, &delivery.Request{
		UserID: b.Binding.UserID, BindingID: b.Binding.ID, BotRef: b.Ref, ChatID: msg.Chat.ID,
		BookID: bookID, Format: format, Origin: models.DeliveryFromBot,
		TapKey:          fmt.Sprintf("legacy:%d:%s", b.Ref, q.ID),
		OriginMessageID: int64(msg.ID),
		OriginRevision:  0, // the old card is never edited
	})
	return enqueueToast(b, bookID, format, &job, admission, err)
}

// enqueueToast is the answer to a press that asked for a file.
func enqueueToast(b *Bot, bookID int64, format string, job *models.TelegramDelivery, admission database.Admission,
	err error) (toast string, answer bool) {
	switch {
	case errors.Is(err, database.ErrInboxLeaseLost):
		return "", false
	case errors.Is(err, delivery.ErrNotFound), errors.Is(err, delivery.ErrUnavailable):
		return screens.CopyUnavailable, true
	case errors.Is(err, delivery.ErrUnknownFormat):
		return screens.CopyUnsupported, true
	case err != nil:
		logging.Warnf("Telegram bot %d: enqueueing book %d as %s: %v", b.Ref, bookID, format, err)
		return screens.CopyLibraryDown, true
	case admission == database.BusyAccount:
		return delivery.ToastBusyAccount, true
	case admission == database.BusyService:
		return delivery.ToastBusyService, true
	case admission == database.InProgress || admission == database.AlreadyTapped:
		return delivery.RepeatToast(job), true
	}
	return fmt.Sprintf(delivery.ToastPreparing, strings.ToUpper(format)), true
}
