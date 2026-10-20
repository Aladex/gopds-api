package router

import (
	"context"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/screens"
)

// The recent sends (plan.md P1-3): S-RECENT, a send again from it, and its
// settings' actions.

// recentPainter reads a page of S-RECENT. A page past the list's end — it
// shrank — shows the last one.
func (r *Router) recentPainter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	ui, err := database.GetTelegramUserUI(ctx, b.Binding.UserID)
	if err != nil {
		return nil, screens.CopyLibraryDown, false
	}
	page, toast := int(node.Page), ""
	recent, total, err := database.ListTelegramRecent(ctx, b.Binding.UserID, page*screens.PageSize, screens.PageSize)
	if err == nil && page > 0 && len(recent) == 0 && total > 0 {
		page = screens.Pages(total) - 1
		recent, total, err = database.ListTelegramRecent(ctx, b.Binding.UserID, page*screens.PageSize, screens.PageSize)
		toast = screens.CopyListChanged
	}
	if err != nil {
		return nil, screens.CopyLibraryDown, false
	}
	items := make([]screens.RecentItem, 0, len(recent))
	for i := range recent {
		it := screens.RecentItem{BookID: recent[i].BookID, Format: recent[i].Format, SentAt: recent[i].SentAt}
		book, err := database.GetBookWithAuthors(ctx, recent[i].BookID)
		switch {
		case err == nil:
			it.Book = book
		case !isGone(err):
			return nil, screens.CopyLibraryDown, false
		}
		items = append(items, it)
	}
	node.Page = int32(page) // #nosec G115 -- pages are small
	return func(k *keys) screens.Screen {
		parent, self := uint64(node.ParentID), uint64(node.ID) // #nosec G115 -- serial ids
		return screens.RecentScreen(items, total, page, ui.KeepRecent, &screens.RecentKeys{
			Page: func(p int) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpRecent, Page: uint32(p), ReturnID: parent}) // #nosec G115 -- pages are small
			},
			Resend: func(bookID int64, format string) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpResend, EntityID: uint64(bookID), // #nosec G115 -- serial ids
					Arg: screens.FormatArg(format), ReturnID: self})
			},
			Open: func(bookID int64) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(bookID), ReturnID: self}) // #nosec G115 -- serial ids
			},
			Home: k.home(),
		})
	}, toast, true
}

// resend is «Отправить» on S-RECENT: a new request, in the format last sent,
// through the same job and the same checks of access as any other.
func (r *Router) resend(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	format := screens.FormatOf(env.Arg)
	if format == "" || r.cfg.Delivery == nil {
		return screens.CopyUnsupported
	}
	bookID := int64(env.EntityID) // #nosec G115 -- decoded ids fit
	job, admission, err := r.cfg.Delivery.Enqueue(ctx, &delivery.Request{
		UserID: b.Binding.UserID, BindingID: b.Binding.ID, BotRef: b.Ref, ChatID: panel.ChatID,
		BookID: bookID, Format: format, Origin: models.DeliveryFromRecent,
		// The list is not a card: the job is not shown on it, and the chat
		// hears of a slow one as of an old card's.
		OriginMessageID: panel.MessageID,
		OriginRevision:  0,
	})
	toast, _ := enqueueToast(b, bookID, format, &job, admission, err, r.cfg.Delivery.Cached(ctx, b.Ref, bookID, format))
	return toast
}

// setKeepRecent turns keeping recent sends on or off and paints its screen
// again. Off does not clear what is kept.
func (r *Router) setKeepRecent(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	keep := env.Arg == 1
	if err := database.SetTelegramKeepRecent(ctx, b.Binding.UserID, keep); err != nil {
		return failedWrite(b, "saving the recent sends' setting", err)
	}
	r.repaint(ctx, b, panel)
	if keep {
		return screens.CopyRecentKept
	}
	return screens.CopyRecentNotKept
}

// clearRecent is S-CLEAR-RECENT's «Очистить список»: the list goes, the
// documents in the chat stay, and the panel goes back to the settings it was
// asked from.
func (r *Router) clearRecent(ctx context.Context, b *Bot, panel *models.TelegramPanel, parent int64) string {
	if err := database.ClearTelegramRecent(ctx, b.Binding.UserID); err != nil {
		return failedWrite(b, "clearing the recent sends", err)
	}
	logging.Infof("Telegram bot %d: user %d cleared the recent sends", b.Ref, b.Binding.UserID)
	if toast := r.back(ctx, b, panel, parent); toast != "" {
		return toast
	}
	return screens.CopyRecentCleared
}
