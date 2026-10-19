package router

import (
	"context"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/telegram/tgapi"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// commandLanguages are the languages commands may have been set for: none,
// and the two the bot's readers use.
var commandLanguages = []string{"", "ru", "en"}

// ensureNoCommands clears the bot's command menu once (plan-ui.md §4.2): the
// v2 bot has no commands, and a menu left by the legacy bot or by BotFather
// would send them on a tap.
func (r *Router) ensureNoCommands(ctx context.Context, b *Bot) {
	ui, err := database.GetTelegramBotUI(ctx, b.Ref)
	if err != nil || ui.CommandsClearedAt != nil {
		return
	}
	if r.ClearCommands(ctx, b) {
		if err := database.MarkTelegramCommandsCleared(ctx, b.Ref); err != nil {
			logging.Warnf("Telegram bot %d: recording the cleared menu: %v", b.Ref, err)
		}
	}
}

// ClearCommands deletes the bot's commands on every scope and language they
// may have been set for, and resets its menu button. It reports whether
// every call succeeded.
func (r *Router) ClearCommands(ctx context.Context, b *Bot) bool {
	scopes := []tgbot.BotCommandScope{&tgbot.BotCommandScopeDefault{}, &tgbot.BotCommandScopeAllPrivateChats{}}
	if b.Binding != nil {
		scopes = append(scopes, &tgbot.BotCommandScopeChat{ChatID: b.Binding.TGUserID})
	}
	ok := true
	for _, scope := range scopes {
		for _, lang := range commandLanguages {
			if _, err := b.Client.DeleteMyCommands(ctx, &bot.DeleteMyCommandsParams{Scope: scope, LanguageCode: lang}); err != nil {
				logging.Warnf("Telegram bot %d: deleting commands: %v", b.Ref, tgapi.RedactURL(err))
				ok = false
			}
		}
	}
	menu := &tgbot.MenuButtonDefault{Type: tgbot.MenuButtonTypeDefault}
	if _, err := b.Client.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{MenuButton: menu}); err != nil {
		logging.Warnf("Telegram bot %d: resetting the menu button: %v", b.Ref, tgapi.RedactURL(err))
		ok = false
	}
	if b.Binding != nil {
		if _, err := b.Client.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{ChatID: b.Binding.TGUserID, MenuButton: menu}); err != nil {
			logging.Warnf("Telegram bot %d: resetting the chat's menu button: %v", b.Ref, tgapi.RedactURL(err))
			ok = false
		}
	}
	return ok
}

// copyKeyboardGone takes the old interface's keyboard away (plan.md S6).
const copyKeyboardGone = "Клавиатура внизу больше не нужна — меню теперь в сообщениях."

// setupChat is the bot's first-interaction setup, each step once: no command
// menu, and no keyboard of the old interface in the chat.
func (r *Router) setupChat(ctx context.Context, b *Bot, chatID int64) {
	r.ensureNoCommands(ctx, b)
	r.dropReplyKeyboard(ctx, b, chatID)
}

// dropReplyKeyboard sends, once per binding, the one message that can take a
// reply keyboard away: a plain one with ReplyKeyboardRemove. A chat linked
// since S6 never had the keyboard, and its binding comes marked.
func (r *Router) dropReplyKeyboard(ctx context.Context, b *Bot, chatID int64) {
	if b.Binding == nil || b.Binding.ReplyKBRemovedAt != nil {
		return
	}
	if _, err := b.Client.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID, Text: copyKeyboardGone, ReplyMarkup: &tgbot.ReplyKeyboardRemove{RemoveKeyboard: true},
	}); err != nil {
		logging.Warnf("Telegram bot %d: taking the old keyboard away: %v", b.Ref, tgapi.RedactURL(err))
		return
	}
	if err := database.MarkTelegramReplyKeyboardRemoved(ctx, b.Binding.ID); err != nil {
		logging.Warnf("Telegram bot %d: recording the old keyboard gone: %v", b.Ref, err)
		return
	}
	now := time.Now()
	b.Binding.ReplyKBRemovedAt = &now
}
