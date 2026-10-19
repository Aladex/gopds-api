package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/telegram/secrets"

	tgbotapi "github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// The one-time link the site gives out: https://t.me/{bot}?start=link_{secret}.
const (
	linkPrefix = "link_"
	// linkSecretBytes of randomness, base64url without padding: 32
	// characters, well inside the 64 a /start payload may carry.
	linkSecretBytes = 24
	// LinkTTL is how long a link works.
	LinkTTL = 15 * time.Minute
)

// The bot's words for connecting. Russian, informal, as the concept's §C.3
// screens have them; the first line is the screen's title.
const (
	copyLinked = "Бот подключён\n\n" +
		"Теперь книги можно искать здесь. Напиши название или автора —\n" +
		"например, «Основание» или «Айзек Азимов».\n\n" +
		"Основной формат — EPUB. Остальные форматы тоже доступны в карточке."
	copyUnlinked = "Бот ещё не подключён к библиотеке\n\n" +
		"Открой свой профиль на сайте и заверши подключение."
	copyLinkExpired = "Ссылка подключения истекла\n\n" +
		"Открой профиль на сайте и получи новую ссылку."
	copyLinkUsed = "Эта ссылка уже использована\n\n" +
		"Открой профиль на сайте и получи новую ссылку."
	copyTelegramTaken = "Этот аккаунт Telegram уже подключён к другому аккаунту библиотеки\n\n" +
		"Сначала отключи его в профиле того аккаунта."
	copyBotTaken = "Этот бот уже подключён к другому аккаунту библиотеки\n\n" +
		"Своего бота можно подключить в профиле библиотеки."
	copyStranger = "Это личный бот другого пользователя\n\n" +
		"Своего бота можно подключить в профиле библиотеки."
	copyButtonUnavailable  = "Эта кнопка недоступна."
	copyLibraryUnavailable = "Библиотека временно недоступна. Попробуй ещё раз позже."

	buttonOpenProfile = "Открыть профиль"
	buttonOpenSite    = "Открыть сайт"
)

// NewLinkSecret returns a fresh link secret and the hash stored for it.
func NewLinkSecret() (secret string, hash []byte) {
	secret = base64.RawURLEncoding.EncodeToString(secrets.Random(linkSecretBytes))
	return secret, linkHash(secret)
}

// LinkURL is the deep link that opens username's chat with /start link_….
func LinkURL(username, secret string) string {
	return "https://t.me/" + username + "?start=" + linkPrefix + secret
}

// wellFormedLinkSecret reports whether secret has the shape NewLinkSecret
// gives: unpadded base64url of linkSecretBytes.
func wellFormedLinkSecret(secret string) bool {
	if len(secret) != base64.RawURLEncoding.EncodedLen(linkSecretBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(secret)
	return err == nil
}

func linkHash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// startPayload is what follows /start, if anything.
func startPayload(text string) string {
	_, payload, _ := strings.Cut(text, " ")
	return strings.TrimSpace(payload)
}

// handleStart answers /start. With a link it binds the bot; without one it
// tells the owner the bot is connected — the intake lets no one else's plain
// /start through to here, except on a bot that has no owner yet.
func (b *Bot) handleStart(ctx context.Context, update *tgbot.Update) {
	msg := update.Message
	if payload := startPayload(msg.Text); strings.HasPrefix(payload, linkPrefix) {
		b.handleLink(ctx, msg, strings.TrimPrefix(payload, linkPrefix))
		return
	}
	binding, err := database.GetActiveTelegramBinding(ctx, b.ref)
	if err != nil {
		b.sendScreen(ctx, msg.Chat.ID, copyLibraryUnavailable, nil)
		return
	}
	if binding != nil && binding.TGUserID == msg.From.ID {
		b.sendLinked(ctx, msg.Chat.ID)
		return
	}
	b.sendScreen(ctx, msg.Chat.ID, copyUnlinked, b.manager.profileButton())
}

// handleLink consumes a link secret. The secret is never stored or logged:
// only its hash is looked up.
func (b *Bot) handleLink(ctx context.Context, msg *tgbot.Message, secret string) {
	outcome, _, err := database.ConsumeTelegramLinkToken(ctx, b.ref, linkHash(secret), msg.From.ID,
		displayName(msg.From), b.manager.freshNavKey())
	if errors.Is(err, database.ErrInboxLeaseLost) {
		// This process no longer holds the update; the one that does answers.
		return
	}
	if err != nil {
		logging.Errorf("Linking telegram bot %d: %v", b.ref, err)
		b.sendScreen(ctx, msg.Chat.ID, copyLibraryUnavailable, nil)
		return
	}
	switch outcome {
	case database.LinkCreated:
		logging.Infof("Telegram bot %d linked", b.ref)
		b.linked(ctx, msg.Chat.ID)
	case database.LinkAlreadyLinked:
		b.linked(ctx, msg.Chat.ID)
	case database.LinkUsedByOther:
		b.sendScreen(ctx, msg.Chat.ID, copyLinkUsed, b.manager.profileButton())
	case database.LinkTelegramTaken:
		b.sendScreen(ctx, msg.Chat.ID, copyTelegramTaken, b.manager.siteButton())
	case database.LinkBotTaken:
		b.sendScreen(ctx, msg.Chat.ID, copyBotTaken, b.manager.siteButton())
	case database.LinkExpired:
		b.sendScreen(ctx, msg.Chat.ID, copyLinkExpired, b.manager.profileButton())
	}
}

// linked greets a chat just bound: the v2 bot with its home panel, the
// legacy one with its screen and keyboard.
func (b *Bot) linked(ctx context.Context, chatID int64) {
	vb, err := b.manager.v2Bot(ctx, b)
	if err == nil && vb != nil {
		b.manager.router.Linked(ctx, vb, chatID)
		return
	}
	b.sendLinked(ctx, chatID)
}

// sendLinked says the bot is connected. Until the new menu exists the
// legacy keyboard comes with it, so the reader has a way on.
func (b *Bot) sendLinked(ctx context.Context, chatID int64) {
	if _, err := b.bot.SendMessage(ctx, &tgbotapi.SendMessageParams{
		ChatID: chatID, Text: copyLinked, ReplyMarkup: GetMainKeyboard(),
	}); err != nil {
		logging.Warnf("Telegram bot %d: sending the linked screen: %v", b.ref, err)
	}
}

// sendScreen sends a text with an optional single URL button, link previews
// off.
func (b *Bot) sendScreen(ctx context.Context, chatID int64, text string, button *tgbot.InlineKeyboardButton) {
	disabled := true
	params := &tgbotapi.SendMessageParams{
		ChatID:             chatID,
		Text:               text,
		LinkPreviewOptions: &tgbot.LinkPreviewOptions{IsDisabled: &disabled},
	}
	if button != nil {
		params.ReplyMarkup = &tgbot.InlineKeyboardMarkup{InlineKeyboard: [][]tgbot.InlineKeyboardButton{{*button}}}
	}
	if _, err := b.bot.SendMessage(ctx, params); err != nil {
		logging.Warnf("Telegram bot %d: sending a screen: %v", b.ref, err)
	}
}

// profileButton opens the site's profile on the Telegram connection.
func (bm *BotManager) profileButton() *tgbot.InlineKeyboardButton {
	if bm.config.SiteURL == "" {
		return nil
	}
	return &tgbot.InlineKeyboardButton{Text: buttonOpenProfile, URL: bm.config.SiteURL + "/?profile=telegram"}
}

// siteButton opens the site.
func (bm *BotManager) siteButton() *tgbot.InlineKeyboardButton {
	if bm.config.SiteURL == "" {
		return nil
	}
	return &tgbot.InlineKeyboardButton{Text: buttonOpenSite, URL: bm.config.SiteURL + "/"}
}

// displayName is how the site names the Telegram account a bot is bound to.
func displayName(u *tgbot.User) string {
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	switch {
	case name != "" && u.Username != "":
		return name + " (@" + u.Username + ")"
	case u.Username != "":
		return "@" + u.Username
	default:
		return name
	}
}
