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
// explicit triggers — /start, «Домой», a home message found gone — never on
// every message: there is no update for an unpin, and each pin leaves a
// service line in the chat.

// start answers /start: the home panel, pinned. A chat that has one gets it
// pinned again, and nothing new is sent.
func (r *Router) start(ctx context.Context, b *Bot, chatID int64, linked bool) {
	home, err := database.GetTelegramHomePanel(ctx, b.Ref, chatID)
	if err != nil {
		logging.Warnf("Telegram bot %d: reading the home panel: %v", b.Ref, err)
		r.sendPlainText(ctx, b, chatID, screens.CopyLibraryDown)
		return
	}
	if home != nil {
		if r.pin(ctx, b, home) {
			return
		}
		if err := database.DropTelegramHome(ctx, home); err != nil {
			logging.Warnf("Telegram bot %d: forgetting a home panel: %v", b.Ref, err)
		}
	}
	r.newHome(ctx, b, chatID, linked)
}

// newHome sends a home panel and pins it.
func (r *Router) newHome(ctx context.Context, b *Bot, chatID int64, linked bool) {
	node := &models.TelegramNode{UserID: b.Binding.UserID, Screen: int16(ScrHome)}
	if err := upsertNode(ctx, node); err != nil {
		logging.Warnf("Telegram bot %d: recording the home node: %v", b.Ref, err)
		return
	}
	panel := r.sendPanel(ctx, b, chatID, node, r.paintHome(linked), true)
	if panel != nil {
		r.pin(ctx, b, panel)
	}
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

// paintHome paints the home screen with its sections; linked greets a chat
// just bound to the library.
func (r *Router) paintHome(linked bool) render {
	return func(k *keys) screens.Screen {
		return screens.HomeScreen(linked, &screens.HomeKeys{
			Favorites:   k.env(cbv2.Envelope{Op: cbv2.OpFavorites}),
			Collections: k.env(cbv2.Envelope{Op: cbv2.OpCollections}),
			Settings:    k.env(cbv2.Envelope{Op: cbv2.OpSettings}),
		})
	}
}
