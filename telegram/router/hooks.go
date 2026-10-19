package router

import (
	"context"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/screens"

	tgbot "github.com/go-telegram/bot/models"
)

// The router's part in delivery jobs pressed on its cards (delivery.CardHooks).
var _ delivery.CardHooks = (*Router)(nil)

// File names and captions a card's document (plan-ui.md §1.10), with
// «Открыть карточку».
func (r *Router) File(
	ctx context.Context, job *models.TelegramDelivery, book *models.Book, size int,
) (name, caption string, markup *tgbot.InlineKeyboardMarkup) {
	name = screens.FileName(book, job.Format)
	caption = screens.FileCaption(book, job.Format, delivery.HumanSize(size))
	b, err := r.bots.V2Bot(ctx, job.BotRef)
	if err != nil || b == nil || b.Binding == nil || b.Binding.ID != job.BindingID {
		return name, caption, nil
	}
	k := &keys{b: b, rev: cbv2.DocumentRevision}
	open := k.env(cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(book.ID)}) // #nosec G115 -- serial ids
	markup, err = r.markup(b, job.ChatID, [][]screens.Button{{{Text: screens.CopyOpenCard, Env: open}}})
	if err != nil {
		logging.Warnf("Telegram bot %d: the document's button: %v", job.BotRef, err)
		return name, caption, nil
	}
	return name, caption, markup
}

// JobEnded repaints the card a job was pressed on, without its «⏳» — if the
// panel still shows that card. A panel that moved on is left alone. Like an
// update, it holds the chat: the press's own edit lands first.
func (r *Router) JobEnded(ctx context.Context, job *models.TelegramDelivery) {
	if job.OriginMessageID == nil {
		return
	}
	b, err := r.bots.V2Bot(ctx, job.BotRef)
	if err != nil || b == nil || b.Binding == nil || b.Binding.ID != job.BindingID {
		return
	}
	unlock, err := database.LockTelegramChat(ctx, job.BotRef, job.ChatID, r.cfg.ChatHold, r.cfg.ChatWait)
	if err != nil {
		logging.Warnf("Telegram bot %d: the card of job %d is left as it is: %v", job.BotRef, job.ID, err)
		return
	}
	defer unlock()
	panel, err := database.GetTelegramPanel(ctx, job.BotRef, job.ChatID, *job.OriginMessageID)
	if err != nil {
		return
	}
	node, err := database.GetTelegramNode(ctx, b.Binding.UserID, panel.NodeID)
	if err != nil || Screen(node.Screen) != ScrBook || node.EntityID != job.BookID {
		return
	}
	r.showCard(ctx, b, panel, panel.Revision, node)
}
