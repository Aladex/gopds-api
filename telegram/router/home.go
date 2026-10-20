package router

import (
	"context"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgapi"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// The home panel and its pin (plan-ui.md §4.1). A pin is restored only on
// explicit triggers — «Домой», a home message found gone — never on every
// message: there is no update for an unpin, and each pin leaves a service
// line in the chat. /start sends a new home panel instead.

// start answers /start — the bot's one command, «Главное меню» — and greets
// a chat just linked: a new home panel at the bottom of the chat, pinned, so
// the reader need not scroll up to it. The one before is retired (retireHome):
// one home panel at a time, however many /start.
func (r *Router) start(ctx context.Context, b *Bot, chatID int64, linked bool) {
	old, err := database.GetTelegramHomePanel(ctx, b.Ref, chatID)
	if err != nil {
		logging.Warnf("Telegram bot %d: reading the home panel: %v", b.Ref, err)
		r.sendPlainText(ctx, b, chatID, screens.CopyLibraryDown)
		return
	}
	if r.newHome(ctx, b, chatID, linked) == nil || old == nil {
		// Without a new home the old one stays the home.
		return
	}
	r.retireHome(ctx, b, old)
}

// retireHome takes a home panel a new one replaced out of the chat: unpinned
// and deleted, or — when Telegram will not delete it, older than 48 hours —
// left without its keyboard. Either way it is retired at a new revision, so
// a button still on it is refused as an old screen's.
func (r *Router) retireHome(ctx context.Context, b *Bot, old *models.TelegramPanel) {
	if err := database.RetireTelegramHome(ctx, old); err != nil {
		logging.Warnf("Telegram bot %d: retiring the old home panel: %v", b.Ref, err)
	}
	if _, err := b.Client.UnpinChatMessage(ctx, &bot.UnpinChatMessageParams{
		ChatID: old.ChatID, MessageID: int(old.MessageID),
	}); err != nil {
		logging.Debugf("Telegram bot %d: unpinning the old home panel: %v", b.Ref, tgapi.RedactURL(err))
	}
	if _, err := b.Client.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID: old.ChatID, MessageID: int(old.MessageID),
	}); err != nil {
		logging.Debugf("Telegram bot %d: deleting the old home panel, its buttons go instead: %v", b.Ref, tgapi.RedactURL(err))
		r.stripKeyboard(ctx, b, old)
	}
}

// newHome sends a home panel and pins it; nil when it could not be sent.
func (r *Router) newHome(ctx context.Context, b *Bot, chatID int64, linked bool) *models.TelegramPanel {
	node := &models.TelegramNode{UserID: b.Binding.UserID, Screen: int16(ScrHome)}
	if err := upsertNode(ctx, node); err != nil {
		logging.Warnf("Telegram bot %d: recording the home node: %v", b.Ref, err)
		return nil
	}
	panel := r.sendPanel(ctx, b, chatID, node, r.paintHome(linked, r.inlineOn(ctx, b)), true)
	if panel != nil {
		r.pin(ctx, b, panel)
	}
	return panel
}

// pin pins the home panel. It reports false only when the message is gone;
// any other failure is logged and left for the next trigger.
func (r *Router) pin(ctx context.Context, b *Bot, home *models.TelegramPanel) bool {
	_, err := b.Client.PinChatMessage(ctx, &bot.PinChatMessageParams{
		ChatID: home.ChatID, MessageID: int(home.MessageID), DisableNotification: true,
	})
	if err == nil {
		if markErr := database.MarkTelegramHomePinned(ctx, home); markErr != nil {
			logging.Warnf("Telegram bot %d: recording a pin: %v", b.Ref, markErr)
		}
		return true
	}
	if class, _ := tgapi.Classify(err); class == tgapi.ClassMessageGone {
		return false
	}
	logging.Warnf("Telegram bot %d: pinning the home panel: %v", b.Ref, tgapi.RedactURL(err))
	return true
}

// checkPin is «Домой»'s trigger: at most every PinCheckEvery, the chat's
// pinned message is read, and the home panel pinned again when it is not
// the one — or sent anew when it is gone.
func (r *Router) checkPin(ctx context.Context, b *Bot, chatID int64) {
	home, err := database.GetTelegramHomePanel(ctx, b.Ref, chatID)
	if err != nil || home == nil {
		return
	}
	if home.PinCheckedAt != nil && time.Since(*home.PinCheckedAt) < r.cfg.PinCheckEvery {
		return
	}
	chat, err := b.Client.GetChat(ctx, &bot.GetChatParams{ChatID: chatID})
	if err != nil {
		logging.Warnf("Telegram bot %d: reading the pinned message: %v", b.Ref, tgapi.RedactURL(err))
		return
	}
	if pinnedIs(chat, home.MessageID) {
		_ = database.MarkTelegramHomeChecked(ctx, home)
		return
	}
	if !r.pin(ctx, b, home) {
		_ = database.DropTelegramHome(ctx, home)
		r.newHome(ctx, b, chatID, false)
	}
}

func pinnedIs(chat *tgbot.ChatFullInfo, messageID int64) bool {
	return chat != nil && chat.PinnedMessage != nil && int64(chat.PinnedMessage.ID) == messageID
}

// inlineOn reports whether the bot answers inline queries, as last seen.
func (r *Router) inlineOn(ctx context.Context, b *Bot) bool {
	ui, err := database.GetTelegramBotUI(ctx, b.Ref)
	return err == nil && ui.SupportsInline
}

// paintHome paints the home screen with its sections; linked greets a chat
// just bound to the library, inline offers inline search.
func (r *Router) paintHome(linked, inline bool) render {
	return func(k *keys) screens.Screen {
		return screens.HomeScreen(linked, inline, &screens.HomeKeys{
			Favorites:   k.env(cbv2.Envelope{Op: cbv2.OpFavorites}),
			Recent:      k.env(cbv2.Envelope{Op: cbv2.OpRecent}),
			Collections: k.env(cbv2.Envelope{Op: cbv2.OpCollections}),
			Settings:    k.env(cbv2.Envelope{Op: cbv2.OpSettings}),
		})
	}
}
