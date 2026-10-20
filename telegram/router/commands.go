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

// The bot's one command (decisions-ui §1, amended 2026-10-20): /start
// brings the home panel back to the bottom of the chat.
const (
	cmdStart         = "start"
	cmdStartTitle    = "Главное меню"
	commandsLanguage = "ru"
)

// commandLanguages are the languages commands may have been set for: none,
// and the two the bot's readers use.
var commandLanguages = []string{"", commandsLanguage, "en"}

// EnsureCommands sets the bot's command menu to /start and its menu button
// to the commands, once per bot, and records it: at startup for every served
// bot, and at a chat's first interaction. The commands are those of the
// default scope, in no language and in Russian; whatever the legacy bot or
// BotFather left on the narrower scopes, or in English, would show instead,
// so it is deleted. Every call is idempotent: a failed run is run whole
// again next time.
func (r *Router) EnsureCommands(ctx context.Context, b *Bot) {
	if ui, err := database.GetTelegramBotUI(ctx, b.Ref); err == nil && ui.CommandsSetAt != nil {
		return
	}
	if !r.setCommands(ctx, b) {
		return
	}
	if err := database.MarkTelegramCommandsSet(ctx, b.Ref); err != nil {
		logging.Warnf("Telegram bot %d: recording the command menu: %v", b.Ref, err)
	}
}

// setCommands makes the calls of EnsureCommands; it reports whether every
// one succeeded.
func (r *Router) setCommands(ctx context.Context, b *Bot) bool {
	ok := true
	failed := func(what string, err error) {
		logging.Warnf("Telegram bot %d: %s: %v", b.Ref, what, tgapi.RedactURL(err))
		ok = false
	}
	narrower := []tgbot.BotCommandScope{&tgbot.BotCommandScopeAllPrivateChats{}}
	if b.Binding != nil {
		narrower = append(narrower, &tgbot.BotCommandScopeChat{ChatID: b.Binding.TGUserID})
	}
	for _, scope := range narrower {
		for _, lang := range commandLanguages {
			if _, err := b.Client.DeleteMyCommands(ctx, &bot.DeleteMyCommandsParams{Scope: scope, LanguageCode: lang}); err != nil {
				failed("deleting commands", err)
			}
		}
	}
	for _, lang := range commandLanguages {
		var err error
		if lang == "" || lang == commandsLanguage {
			_, err = b.Client.SetMyCommands(ctx, &bot.SetMyCommandsParams{
				Commands:     []tgbot.BotCommand{{Command: cmdStart, Description: cmdStartTitle}},
				Scope:        &tgbot.BotCommandScopeDefault{},
				LanguageCode: lang,
			})
		} else {
			_, err = b.Client.DeleteMyCommands(ctx, &bot.DeleteMyCommandsParams{Scope: &tgbot.BotCommandScopeDefault{}, LanguageCode: lang})
		}
		if err != nil {
			failed("setting the commands", err)
		}
	}
	menu := &tgbot.MenuButtonCommands{Type: tgbot.MenuButtonTypeCommands}
	if _, err := b.Client.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{MenuButton: menu}); err != nil {
		failed("setting the menu button", err)
	}
	if b.Binding != nil {
		if _, err := b.Client.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{ChatID: b.Binding.TGUserID, MenuButton: menu}); err != nil {
			failed("setting the chat's menu button", err)
		}
	}
	return ok
}

// copyKeyboardGone takes the old interface's keyboard away (plan.md S6).
const copyKeyboardGone = "Клавиатура внизу больше не нужна — меню теперь в сообщениях."

// setupChat is the bot's first-interaction setup, each step once: the
// command menu of /start, and no keyboard of the old interface in the chat.
func (r *Router) setupChat(ctx context.Context, b *Bot, chatID int64) {
	r.EnsureCommands(ctx, b)
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
