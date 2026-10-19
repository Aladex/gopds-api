package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	tgbotapi "github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// How the inbox holds an update.
const (
	// inboxLease is how long a process may hold an update without renewing
	// it; it renews every third of it while the handler runs.
	inboxLease = 2 * time.Minute
	// inboxMaxAge: an update still pending this long after it arrived is
	// closed rather than handled — an answer that late surprises more than
	// it helps, and a button press that old can no longer be acknowledged.
	inboxMaxAge = 10 * time.Minute
	// inboxMaxAttempts is how many processes may try one update.
	inboxMaxAttempts = 3
	// inboxRetention is how long finished rows are kept.
	inboxRetention = 7 * 24 * time.Hour
	// maxUpdateBytes bounds the body read from Telegram.
	maxUpdateBytes = 1 << 20
	// strangerReplyEvery limits the "this is someone else's bot" answer to
	// one per stranger per bot in this window.
	strangerReplyEvery = time.Hour
	// leaseRenewals is how many times a lease is renewed within its length.
	leaseRenewals = 3
	// strangerReplyTimeout bounds the answer to a stranger.
	strangerReplyTimeout = 10 * time.Second
)

// What the webhook answers.
const (
	webhookSecretHeader = "X-Telegram-Bot-Api-Secret-Token" // #nosec G101 -- a header name, not a credential
	statusOK            = "ok"
	statusSkipped       = "skipped"
	statusDuplicate     = "duplicate"
	errUnavailable      = "temporarily unavailable"
)

// Update kinds as recorded in the inbox.
const (
	kindMessage  = "message"
	kindCallback = "callback_query"
)

// HandleWebhook takes one update from Telegram.
//
// The order is the point. The path and the secret identify the bot; the
// sender is checked against the bot's active binding before anything is
// written; an accepted update is recorded in the inbox before Telegram hears
// 200, so it survives a restart; and a redelivered update — same bot, same
// update_id — is recognized there and not handled twice. Strangers get a
// short answer and leave nothing behind: no inbox row, no conversation.
//
// A 5xx makes Telegram deliver the update again later, so it is the answer
// whenever the database could not be asked; anything Telegram would only send
// again unchanged is acknowledged instead.
func (bm *BotManager) HandleWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	b, err := bm.botByPath(ctx, c.Param("token"))
	if err != nil {
		logging.Errorf("Webhook: looking up a telegram bot: %v", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errUnavailable})
		return
	}
	if b == nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "Bot not found"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(c.GetHeader(webhookSecretHeader)), []byte(b.webhookSecret)) != 1 {
		logging.Warnf("Webhook secret mismatch for telegram bot %d", b.ref)
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid secret token"})
		return
	}
	body, update, ok := readUpdate(c, b)
	if !ok {
		answerStatus(c, statusSkipped)
		return
	}
	in := readIncoming(update)
	if in.kind == "" {
		answerStatus(c, statusSkipped)
		return
	}

	binding, err := database.GetActiveTelegramBinding(ctx, b.ref)
	if err != nil {
		logging.Errorf("Webhook: reading the binding of telegram bot %d: %v", b.ref, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errUnavailable})
		return
	}
	if !in.acceptedBy(binding) {
		go bm.answerStranger(context.WithoutCancel(ctx), b, in, binding)
		answerStatus(c, statusOK)
		return
	}
	bm.accept(c, b, body, update, in, binding)
}

// accept records an accepted update in the inbox and hands it to a handler.
func (bm *BotManager) accept(c *gin.Context, b *Bot, body []byte, update *tgbot.Update, in incoming, binding *models.TelegramBinding) {
	ctx := c.Request.Context()
	inserted, err := database.InsertTelegramInboxUpdate(ctx, b.ref, update.ID, in.kind, string(body), bm.process, inboxLease)
	if err != nil {
		logging.Errorf("Webhook: recording update %d of telegram bot %d: %v", update.ID, b.ref, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errUnavailable})
		return
	}
	if !inserted {
		answerStatus(c, statusDuplicate)
		return
	}
	if binding != nil && binding.BlockedAt != nil && in.from.ID == binding.TGUserID {
		// The owner wrote, so the bot is not blocked any more.
		if clearErr := database.SetTelegramBindingBlocked(ctx, b.ref, false); clearErr != nil {
			logging.Warnf("Webhook: clearing the blocked mark of telegram bot %d: %v", b.ref, clearErr)
		}
	}
	bm.handling.Add(1)
	go bm.dispatch(context.WithoutCancel(ctx), b, update)
	answerStatus(c, statusOK)
}

// answerStatus acknowledges an update with 200.
func answerStatus(c *gin.Context, status string) {
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// readUpdate reads and decodes the body. An oversized or undecodable update
// is reported and refused: Telegram would send the same bytes again, and
// nothing would change.
func readUpdate(c *gin.Context, b *Bot) ([]byte, *tgbot.Update, bool) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxUpdateBytes+1))
	if err != nil || len(body) > maxUpdateBytes {
		logging.Warnf("Webhook: unreadable or oversized update for telegram bot %d", b.ref)
		return nil, nil, false
	}
	update := new(tgbot.Update)
	if err := json.Unmarshal(body, update); err != nil {
		logging.Warnf("Webhook: undecodable update for telegram bot %d: %v", b.ref, err)
		return nil, nil, false
	}
	return body, update, true
}

// incoming is what the intake needs to know about an update.
type incoming struct {
	kind       string
	from       *tgbot.User
	chat       *tgbot.Chat
	text       string
	callbackID string
}

// readIncoming reads the sender of the update kinds the bot handles. Any
// other kind comes back with an empty kind and is acknowledged unread.
func readIncoming(u *tgbot.Update) incoming {
	switch {
	case u.Message != nil && u.Message.From != nil:
		return incoming{kind: kindMessage, from: u.Message.From, chat: &u.Message.Chat, text: u.Message.Text}
	case u.CallbackQuery != nil:
		return incoming{kind: kindCallback, from: &u.CallbackQuery.From, callbackID: u.CallbackQuery.ID}
	}
	return incoming{}
}

// acceptedBy reports whether the update may reach the bot's handlers: the
// owner's, or anyone's /start with a link — that is how a bot gets an owner.
func (in incoming) acceptedBy(binding *models.TelegramBinding) bool {
	if in.kind == kindMessage && isLinkStart(in.text) {
		return true
	}
	return binding != nil && in.from.ID == binding.TGUserID
}

// isLinkStart reports whether text is "/start link_…", the deep link the
// site hands out.
func isLinkStart(text string) bool {
	cmd, payload, ok := strings.Cut(text, " ")
	if !ok {
		return false
	}
	if cmd != "/start" && !strings.HasPrefix(cmd, "/start@") {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(payload), linkPrefix)
}

// dispatch runs one accepted update through the bot's handlers while holding
// its inbox lease, and closes it. parent carries no cancellation: the update
// is handled to the end even when what started it is gone.
func (bm *BotManager) dispatch(parent context.Context, b *Bot, update *tgbot.Update) {
	defer bm.handling.Done()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go bm.keepLease(ctx, b.ref, update.ID)

	bm.inflight <- struct{}{}
	defer func() { <-bm.inflight }()

	b.bot.ProcessUpdate(ctx, update)

	if err := database.FinishTelegramInboxUpdate(parent, b.ref, update.ID, bm.process, "done"); err != nil {
		logging.Errorf("Inbox: closing update %d of telegram bot %d: %v", update.ID, b.ref, err)
	}
}

// keepLease renews the lease until ctx ends.
func (bm *BotManager) keepLease(ctx context.Context, ref, updateID int64) {
	ticker := time.NewTicker(inboxLease / leaseRenewals)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := database.ExtendTelegramInboxLease(ctx, ref, updateID, bm.process, inboxLease)
			if err != nil || !ok {
				logging.Warnf("Inbox: lost the lease of update %d of telegram bot %d", updateID, ref)
				return
			}
		}
	}
}

// StartInboxSweeper hands updates whose process died to this one, every
// interval, and prunes finished rows.
func (bm *BotManager) StartInboxSweeper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastPrune := time.Time{}
		for {
			bm.sweepInbox(ctx)
			if time.Since(lastPrune) > time.Hour {
				if err := database.PruneTelegramInbox(ctx, inboxRetention); err != nil {
					logging.Warnf("Inbox: pruning: %v", err)
				}
				lastPrune = time.Now()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// sweepInbox claims stale updates and dispatches them.
func (bm *BotManager) sweepInbox(ctx context.Context) {
	claimed, err := database.ClaimStaleTelegramInboxUpdates(ctx, bm.process, inboxLease, inboxMaxAge, inboxMaxAttempts, maxInflightUpdates)
	if err != nil {
		logging.Warnf("Inbox: claiming stale updates: %v", err)
		return
	}
	for i := range claimed {
		row := &claimed[i]
		b, err := bm.botByRef(ctx, row.BotRef)
		if err != nil || b == nil || row.Payload == nil {
			_ = database.FinishTelegramInboxUpdate(ctx, row.BotRef, row.UpdateID, bm.process, "skipped")
			continue
		}
		var update tgbot.Update
		if err := json.Unmarshal([]byte(*row.Payload), &update); err != nil {
			_ = database.FinishTelegramInboxUpdate(ctx, row.BotRef, row.UpdateID, bm.process, "failed")
			continue
		}
		logging.Infof("Inbox: resuming update %d of telegram bot %d", row.UpdateID, row.BotRef)
		bm.handling.Add(1)
		go bm.dispatch(context.WithoutCancel(ctx), b, &update)
	}
}

// WaitIdle waits until every update this process took has been handled.
func (bm *BotManager) WaitIdle() { bm.handling.Wait() }

// answerStranger answers someone who is not the bot's owner. A button gets
// "unavailable"; a message in a private chat gets one explanation per hour —
// that the bot is someone else's, or, for a bot nobody owns yet, that it is
// not connected. Nothing about the owner is said.
func (bm *BotManager) answerStranger(parent context.Context, b *Bot, in incoming, binding *models.TelegramBinding) {
	ctx, cancel := context.WithTimeout(parent, strangerReplyTimeout)
	defer cancel()

	if in.kind == kindCallback {
		_, _ = b.bot.AnswerCallbackQuery(ctx, &tgbotapi.AnswerCallbackQueryParams{
			CallbackQueryID: in.callbackID, Text: copyButtonUnavailable,
		})
		return
	}
	if in.chat == nil || in.chat.Type != tgbot.ChatTypePrivate || !bm.firstStrangerReply(b, in.from.ID) {
		return
	}
	text, button := copyStranger, bm.siteButton()
	if binding == nil {
		text, button = copyUnlinked, bm.profileButton()
	}
	b.sendScreen(ctx, in.chat.ID, text, button)
}

// firstStrangerReply reports whether this stranger has had no answer from
// this bot within strangerReplyEvery. It is shared by every process through
// Redis; without Redis the answer is given.
func (bm *BotManager) firstStrangerReply(b *Bot, userID int64) bool {
	if bm.redis == nil {
		return true
	}
	key := fmt.Sprintf("tg2:stranger:%d:%d", b.tgBotID, userID)
	ok, err := bm.redis.SetNX(key, 1, strangerReplyEvery).Result()
	if err != nil {
		return true
	}
	return ok
}
