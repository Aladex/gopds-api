package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
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
	kindInline   = "inline_query"
)

// inlineCacheSeconds is how long Telegram may keep an inline answer.
const inlineCacheSeconds = 5

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
		c.JSON(http.StatusNotFound, ErrorResponse{Error: errBotNotFound})
		return
	}
	if subtle.ConstantTimeCompare([]byte(c.GetHeader(webhookSecretHeader)), []byte(b.webhookSecret)) != 1 {
		logging.Warnf("Webhook secret mismatch for telegram bot %d", b.ref)
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errInvalidSecret})
		return
	}
	body, update, ok := readUpdate(c, b)
	if !ok {
		answerStatus(c, statusSkipped)
		return
	}
	in := readIncoming(update)
	if in.kind == "" || fromABot(b, in.from) {
		answerStatus(c, statusSkipped)
		return
	}

	binding, err := database.GetActiveTelegramBinding(ctx, b.ref)
	if err != nil {
		logging.Errorf("Webhook: reading the binding of telegram bot %d: %v", b.ref, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errUnavailable})
		return
	}
	admitted, err := admits(ctx, b, in, binding)
	if err != nil {
		logging.Errorf("Webhook: checking a link for telegram bot %d: %v", b.ref, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errUnavailable})
		return
	}
	if !admitted {
		go bm.answerStranger(context.WithoutCancel(ctx), b, in, binding)
		answerStatus(c, statusOK)
		return
	}
	bm.accept(c, b, body, update, in, binding)
}

// accept records an accepted update in the inbox and hands it to a handler.
func (bm *BotManager) accept(c *gin.Context, b *Bot, body []byte, update *tgbot.Update, in incoming, binding *models.TelegramBinding) {
	ctx := c.Request.Context()
	lease, inserted, err := database.InsertTelegramInboxUpdate(ctx, b.ref, update.ID, in.kind, string(body), bm.process, inboxLease)
	if err != nil {
		logging.Errorf("Webhook: recording update %d of telegram bot %d: %v", update.ID, b.ref, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errUnavailable})
		return
	}
	if !inserted {
		answerStatus(c, statusDuplicate)
		return
	}
	if binding != nil && binding.BlockedAt != nil && in.from.ID == binding.TGUserID && in.kind != kindInline {
		// The owner wrote, so the bot is not blocked any more. An inline
		// query does not say so: it can be typed to a blocked bot.
		if clearErr := database.SetTelegramBindingBlocked(ctx, b.ref, false); clearErr != nil {
			logging.Warnf("Webhook: clearing the blocked mark of telegram bot %d: %v", b.ref, clearErr)
		}
	}
	bm.handling.Add(1)
	go bm.dispatch(context.WithoutCancel(ctx), b, update, lease)
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
	inlineID   string
}

// readIncoming reads the sender of the update kinds the bot handles. Any
// other kind, and a service message, comes back with an empty kind and is
// acknowledged unread.
func readIncoming(u *tgbot.Update) incoming {
	switch {
	case u.Message != nil && u.Message.From != nil && !isServiceMessage(u.Message):
		return incoming{kind: kindMessage, from: u.Message.From, chat: &u.Message.Chat, text: u.Message.Text}
	case u.CallbackQuery != nil:
		return incoming{kind: kindCallback, from: &u.CallbackQuery.From, callbackID: u.CallbackQuery.ID}
	case u.InlineQuery != nil && u.InlineQuery.From != nil:
		return incoming{kind: kindInline, from: u.InlineQuery.From, inlineID: u.InlineQuery.ID}
	}
	return incoming{}
}

// fromABot reports whether the update was sent by a bot, this one included.
// Telegram delivers the service message of the bot's own pin with the bot as
// its sender; no bot is ever the owner, nor a stranger to answer.
func fromABot(b *Bot, from *tgbot.User) bool {
	return from.IsBot || from.ID == b.tgBotID
}

// isServiceMessage reports whether msg is a service message — something that
// happened in the chat (a pin, a new background, a member joining) rather
// than something a person wrote. The bot answers none of them.
func isServiceMessage(msg *tgbot.Message) bool {
	return slices.Contains([]bool{
		msg.PinnedMessage != nil,
		len(msg.NewChatMembers) > 0,
		msg.LeftChatMember != nil,
		msg.NewChatTitle != "",
		len(msg.NewChatPhoto) > 0,
		msg.DeleteChatPhoto,
		msg.GroupChatCreated,
		msg.SupergroupChatCreated,
		msg.ChannelChatCreated,
		msg.MessageAutoDeleteTimerChanged != nil,
		msg.MigrateToChatID != 0,
		msg.MigrateFromChatID != 0,
		msg.SuccessfulPayment != nil,
		msg.RefundedPayment != nil,
		msg.UsersShared != nil,
		msg.ChatShared != nil,
		msg.Gift != nil,
		msg.UniqueGift != nil,
		msg.GiftUpgradeSent != nil,
		msg.ConnectedWebsite != "",
		msg.WriteAccessAllowed != nil,
		msg.PassportData != nil,
		msg.ProximityAlertTriggered != nil,
		msg.BoostAdded != nil,
		msg.ChatBackgroundSet != nil,
		msg.ChecklistTasksDone != nil,
		msg.ChecklistTasksAdded != nil,
		msg.DirectMessagePriceChanged != nil,
		msg.PaidMessagePriceChanged != nil,
		msg.ForumTopicCreated != nil,
		msg.ForumTopicEdited != nil,
		msg.ForumTopicClosed != nil,
		msg.ForumTopicReopened != nil,
		msg.GeneralForumTopicHidden != nil,
		msg.GeneralForumTopicUnhidden != nil,
		msg.GiveawayCreated != nil,
		msg.GiveawayWinners != nil,
		msg.GiveawayCompleted != nil,
		msg.ChatOwnerLeft != nil,
		msg.ChatOwnerChanged != nil,
		msg.CommunityChatAdded != nil,
		msg.CommunityChatJoined != nil,
		msg.CommunityChatRemoved != nil,
		msg.SuggestedPostApproved != nil,
		msg.SuggestedPostApprovalFailed != nil,
		msg.SuggestedPostDeclined != nil,
		msg.SuggestedPostPaid != nil,
		msg.SuggestedPostRefunded != nil,
		msg.VideoChatScheduled != nil,
		msg.VideoChatStarted != nil,
		msg.VideoChatEnded != nil,
		msg.VideoChatParticipantsInvited != nil,
		msg.WebAppData != nil,
		msg.ManagedBotCreated != nil,
		msg.PollOptionAdded != nil,
		msg.PollOptionDeleted != nil,
	}, true)
}

// admits reports whether the update may reach the bot's handlers: the
// owner's, or a /start carrying a link of this bot that nobody has used yet —
// that is how a bot gets an owner. The link is looked up without writing
// anything; a made-up or used link is a stranger's message like any other.
func admits(ctx context.Context, b *Bot, in incoming, binding *models.TelegramBinding) (bool, error) {
	if binding != nil && in.from.ID == binding.TGUserID {
		return true, nil
	}
	if in.kind != kindMessage || !isLinkStart(in.text) {
		return false, nil
	}
	secret := strings.TrimPrefix(startPayload(in.text), linkPrefix)
	if !wellFormedLinkSecret(secret) {
		return false, nil
	}
	return database.TelegramLinkIsOpen(ctx, b.ref, linkHash(secret))
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
// is handled to the end even when what started it is gone — unless the lease
// is lost. Then the handler's context is canceled, and every outgoing call it
// still tries is refused by the observing client, which checks the lease
// (and its fence) in the database before the call starts.
func (bm *BotManager) dispatch(parent context.Context, b *Bot, update *tgbot.Update, lease database.InboxLease) {
	defer bm.handling.Done()
	ledger := &replyLedger{}
	ctx, cancel := context.WithCancelCause(withReplyLedger(database.WithInboxLease(parent, lease), ledger))
	defer cancel(nil)
	go bm.keepLease(ctx, cancel, lease)

	select {
	case bm.inflight <- struct{}{}:
	case <-ctx.Done():
		logging.Warnf("Inbox: update %d of telegram bot %d lost its lease while queued", lease.UpdateID, lease.BotRef)
		return
	}
	defer func() { <-bm.inflight }()

	if err := bm.handle(ctx, b, update); err != nil && ledger.nothingSent() {
		// No handler could be chosen, and nothing was tried: the update is
		// unread, and goes back whole for the next sweep (within its attempts).
		logging.Warnf("Inbox: update %d of telegram bot %d: choosing its handler: %v; handing it back", lease.UpdateID, lease.BotRef, err)
		if relErr := database.ReleaseTelegramInboxUpdate(parent, lease); relErr != nil {
			logging.Warnf("Inbox: handing back update %d of telegram bot %d: %v", lease.UpdateID, lease.BotRef, relErr)
		}
		return
	}
	if ledger.onlyUnsent() {
		// Every call it tried provably never left: the update is handed back
		// whole for the next sweep, rather than closed with its reply lost.
		logging.Warnf("Inbox: update %d of telegram bot %d sent nothing; handing it back", lease.UpdateID, lease.BotRef)
		if err := database.ReleaseTelegramInboxUpdate(parent, lease); err != nil {
			logging.Warnf("Inbox: handing back update %d of telegram bot %d: %v", lease.UpdateID, lease.BotRef, err)
		}
		return
	}
	if err := database.FinishTelegramInboxUpdate(parent, lease, "done"); err != nil {
		// Taken over, or past its lease: the next holder decides.
		logging.Warnf("Inbox: closing update %d of telegram bot %d: %v", lease.UpdateID, lease.BotRef, err)
	}
}

// handle runs one update through the v2 router, or answers it with the
// maintenance message when the bot is switched to maintenance (or no router
// was built). A /start with a link binds the bot either way. An error means
// no handler could be chosen and none ran: the update is unread.
func (bm *BotManager) handle(ctx context.Context, b *Bot, update *tgbot.Update) (err error) {
	defer func() {
		// One faulty update must not take the process down.
		if r := recover(); r != nil {
			logging.Errorf("Telegram bot %d: handling an update panicked: %v\n%s", b.ref, r, debug.Stack())
			err = nil
		}
	}()
	if msg := update.Message; msg != nil && isLinkStart(msg.Text) {
		b.handleStart(ctx, update)
		return nil
	}
	vb, err := bm.v2Bot(ctx, b)
	if err != nil {
		return fmt.Errorf("reading telegram bot %d: %w", b.ref, err)
	}
	if vb != nil && bm.router != nil {
		handled, err := bm.router.Handle(ctx, vb, update)
		if err != nil || handled {
			return err
		}
	}
	bm.maintenance(ctx, b, update)
	return nil
}

// copyMaintenance is a bot switched to maintenance (plan-ui.md §5.4), and
// copyOwnerChanged a managed bot Telegram moved to another owner (P1-1).
const (
	copyMaintenance  = "Поиск в боте временно недоступен. Книги можно найти на сайте."
	copyOwnerChanged = "Владелец этого бота в Telegram сменился. Подключи его заново в профиле библиотеки."
)

// maintenance answers an update while the bot is in maintenance: a message
// gets the notice with the site's address, a press the notice as a toast, an
// inline query nothing. Its jobs and binding keep their state.
func (bm *BotManager) maintenance(ctx context.Context, b *Bot, update *tgbot.Update) {
	text, button := copyMaintenance, bm.siteButton()
	if model, err := database.GetTelegramBot(ctx, b.ref); err == nil && model.Status == models.TelegramBotOwnerChanged {
		text, button = copyOwnerChanged, bm.profileButton()
	}
	switch {
	case update.Message != nil:
		if update.Message.Chat.Type == tgbot.ChatTypePrivate {
			b.sendScreen(ctx, update.Message.Chat.ID, text, button)
		}
	case update.CallbackQuery != nil:
		_, _ = b.bot.AnswerCallbackQuery(ctx, &tgbotapi.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID, Text: text,
		})
	case update.InlineQuery != nil:
		_, _ = b.bot.AnswerInlineQuery(ctx, &tgbotapi.AnswerInlineQueryParams{
			InlineQueryID: update.InlineQuery.ID, Results: []tgbot.InlineQueryResult{},
			CacheTime: inlineCacheSeconds, IsPersonal: true,
		})
	}
}

// replyLedger counts what a handler's outgoing calls may have done: calls
// that may have reached Telegram, and calls that provably did not.
type replyLedger struct {
	mu             sync.Mutex
	possiblySent   int
	provablyUnsent int
}

// nothingSent reports that no call may have reached Telegram.
func (l *replyLedger) nothingSent() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.possiblySent == 0
}

// onlyUnsent reports that calls were tried and none may have reached Telegram.
func (l *replyLedger) onlyUnsent() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.possiblySent == 0 && l.provablyUnsent > 0
}

// record notes one call's result; it reports whether every call so far was
// provably unsent, so the update's reply mark may be taken back.
func (l *replyLedger) record(unsent bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if unsent {
		l.provablyUnsent++
	} else {
		l.possiblySent++
	}
	return l.possiblySent == 0
}

// replyLedgerKey carries a handler's ledger.
type replyLedgerKey struct{}

func withReplyLedger(ctx context.Context, l *replyLedger) context.Context {
	return context.WithValue(ctx, replyLedgerKey{}, l)
}

func replyLedgerOf(ctx context.Context) *replyLedger {
	l, _ := ctx.Value(replyLedgerKey{}).(*replyLedger)
	return l
}

// keepLease renews the lease until ctx ends. When a renewal fails — the row
// was taken over, or the database did not answer — the lease cannot be
// vouched for, and the handler is canceled.
func (bm *BotManager) keepLease(ctx context.Context, cancel context.CancelCauseFunc, lease database.InboxLease) {
	ticker := time.NewTicker(inboxLease / leaseRenewals)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := database.ExtendTelegramInboxLease(ctx, lease, inboxLease); err != nil {
				logging.Warnf("Inbox: lost the lease of update %d of telegram bot %d: %v", lease.UpdateID, lease.BotRef, err)
				cancel(database.ErrInboxLeaseLost)
				return
			}
		}
	}
}

// StartInboxSweeper hands updates whose process died to this one, every
// interval, runs the repaints left behind, and prunes finished rows. The
// returned channel closes once it has stopped, after ctx is done.
func (bm *BotManager) StartInboxSweeper(ctx context.Context, interval time.Duration) <-chan struct{} {
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastPrune := time.Time{}
		for {
			bm.sweepInbox(ctx)
			if bm.router != nil {
				bm.router.SweepRepaints(ctx)
			}
			if m := bm.managerOf(); m != nil {
				m.sweepEvents(ctx)
			}
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
	return stopped
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
		lease := database.InboxLease{BotRef: row.BotRef, UpdateID: row.UpdateID, Owner: bm.process, Fence: row.Fence}
		if err != nil || b == nil || row.Payload == nil {
			_ = database.FinishTelegramInboxUpdate(ctx, lease, "skipped")
			continue
		}
		var update tgbot.Update
		if err := json.Unmarshal([]byte(*row.Payload), &update); err != nil {
			_ = database.FinishTelegramInboxUpdate(ctx, lease, "failed")
			continue
		}
		logging.Infof("Inbox: resuming update %d of telegram bot %d", row.UpdateID, row.BotRef)
		bm.handling.Add(1)
		go bm.dispatch(context.WithoutCancel(ctx), b, &update, lease)
	}
}

// WaitIdle waits until every update this process took has been handled,
// and the background naming of adopted bots has finished.
func (bm *BotManager) WaitIdle() { bm.handling.Wait() }

// WaitSends waits for the Kindle sends the updates started.
func (bm *BotManager) WaitSends() {
	if bm.router != nil {
		bm.router.Wait()
	}
}

// answerStranger answers someone who is not the bot's owner. A button gets
// "unavailable"; a message in a private chat gets one explanation per hour —
// that the bot is someone else's, or, for a bot nobody owns yet, that it is
// not connected. Nothing about the owner is said.
func (bm *BotManager) answerStranger(parent context.Context, b *Bot, in incoming, binding *models.TelegramBinding) {
	ctx, cancel := context.WithTimeout(parent, strangerReplyTimeout)
	defer cancel()

	switch in.kind {
	case kindCallback:
		_, _ = b.bot.AnswerCallbackQuery(ctx, &tgbotapi.AnswerCallbackQueryParams{
			CallbackQueryID: in.callbackID, Text: copyButtonUnavailable,
		})
		return
	case kindInline:
		// Nothing for a stranger, and Telegram keeps that for them alone.
		_, _ = b.bot.AnswerInlineQuery(ctx, &tgbotapi.AnswerInlineQueryParams{
			InlineQueryID: in.inlineID, Results: []tgbot.InlineQueryResult{}, CacheTime: inlineCacheSeconds, IsPersonal: true,
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
