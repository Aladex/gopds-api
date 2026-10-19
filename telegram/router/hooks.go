package router

import (
	"context"
	"time"

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
// update, it holds the chat, so the press's own edit lands first; and the
// repaint is recorded before the wait, so a chat held for longer than the
// wait gets it from its next holder, or from the sweeper.
func (r *Router) JobEnded(ctx context.Context, job *models.TelegramDelivery) {
	if job.OriginMessageID == nil {
		return
	}
	if err := database.AddTelegramRepaint(ctx, &models.TelegramPanelRepaint{BotRef: job.BotRef, ChatID: job.ChatID,
		MessageID: *job.OriginMessageID, BindingID: job.BindingID, BookID: job.BookID}); err != nil {
		logging.Warnf("Telegram bot %d: recording the repaint of job %d: %v", job.BotRef, job.ID, err)
		return
	}
	r.repaintChat(ctx, job.BotRef, job.ChatID, r.cfg.ChatWait)
}

// repaintChat holds a chat and runs the repaints owed in it. A chat that
// stays busy keeps them for its holder.
func (r *Router) repaintChat(ctx context.Context, botRef, chatID int64, wait time.Duration) {
	unlock, err := database.LockTelegramChat(ctx, botRef, chatID, r.cfg.ChatHold, wait)
	if err != nil {
		logging.Infof("Telegram bot %d: repaints in a busy chat are left to its holder: %v", botRef, err)
		return
	}
	defer unlock()
	b, err := r.bots.V2Bot(ctx, botRef)
	if err != nil || b == nil || b.Binding == nil {
		return
	}
	r.runRepaints(ctx, b, chatID)
}

// runRepaints runs the repaints owed in a chat its caller holds. One owed
// under an older binding is dropped: its card belongs to the old owner. One
// that could not be read through is kept for the next holder or sweep.
func (r *Router) runRepaints(ctx context.Context, b *Bot, chatID int64) {
	owed, err := database.TelegramRepaints(ctx, b.Ref, chatID)
	if err != nil {
		logging.Warnf("Telegram bot %d: reading the repaints owed: %v", b.Ref, err)
		return
	}
	for i := range owed {
		job := &owed[i]
		if b.Binding != nil && job.BindingID == b.Binding.ID && !r.repaintCard(ctx, b, job) {
			continue
		}
		if err := database.DropTelegramRepaint(ctx, job.ID); err != nil {
			logging.Warnf("Telegram bot %d: forgetting a repaint: %v", b.Ref, err)
		}
	}
}

// repaintCard paints a card again if its panel still shows it. It reports
// whether the repaint is done with: painted, or provably not wanted — the
// panel or its node is gone, or shows something else. A read that failed is
// not done.
func (r *Router) repaintCard(ctx context.Context, b *Bot, job *models.TelegramPanelRepaint) bool {
	panel, err := database.GetTelegramPanel(ctx, job.BotRef, job.ChatID, job.MessageID)
	if err != nil {
		return isNoRows(err)
	}
	node, err := database.GetTelegramNode(ctx, b.Binding.UserID, panel.NodeID)
	if err != nil {
		return isNoRows(err)
	}
	if Screen(node.Screen) != ScrBook || node.EntityID != job.BookID {
		return true
	}
	return r.show(ctx, b, panel, panel.Revision, node) != screens.CopyLibraryDown
}

// repaintSweepAge is how long a repaint waits before the sweeper takes it;
// repaintDropAge is when it is given up.
const (
	repaintSweepAge = 5 * time.Second
	repaintDropAge  = 24 * time.Hour
	repaintSweepFor = time.Second
)

// SweepRepaints runs the repaints left behind: their job's wait ran out, or
// the holder that was to run them died.
func (r *Router) SweepRepaints(ctx context.Context) {
	chats, err := database.StaleTelegramRepaintChats(ctx, repaintSweepAge, repaintDropAge)
	if err != nil {
		logging.Warnf("Telegram: reading the repaints left behind: %v", err)
		return
	}
	for _, c := range chats {
		r.repaintChat(ctx, c.BotRef, c.ChatID, repaintSweepFor)
	}
}
