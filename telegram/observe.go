package telegram

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/telegram/tgapi"
)

// observingClient carries a bot's outgoing calls and notices the one answer
// the site needs to know about: Telegram refusing to deliver because the
// owner blocked the bot. The site shows that state ("the bot cannot write to
// you"), since the bot itself can no longer say it.
type observingClient struct {
	inner  *http.Client
	onDeny func()
}

// observedClient is the HTTP client of the bot with row id ref.
func (bm *BotManager) observedClient(ref int64) *observingClient {
	return &observingClient{
		inner: bm.config.API.Client(),
		onDeny: func() {
			if err := database.SetTelegramBindingBlocked(context.Background(), ref, true); err != nil {
				logging.Warnf("Failed to mark telegram bot %d as blocked: %v", ref, err)
			}
		},
	}
}

// Do implements the SDK's HttpClient. A call made while handling an inbox
// update first proves, in the database, that the update's lease is still
// held — and marks that a reply may be on its way. A handler that lost its
// lease to another process makes no call at all.
func (c *observingClient) Do(req *http.Request) (*http.Response, error) {
	lease, handling := database.InboxLeaseFrom(req.Context())
	if handling {
		if err := database.MarkTelegramInboxReplyStarted(req.Context(), lease); err != nil {
			return nil, fmt.Errorf("not sending for update %d: %w", lease.UpdateID, err)
		}
	}
	resp, err := c.inner.Do(req) // #nosec G704 -- the request goes to the configured Bot API endpoint
	if handling {
		// A 429 is Telegram refusing the call before doing anything.
		refused := err == nil && resp.StatusCode == http.StatusTooManyRequests
		c.reconcile(req.Context(), lease, err, refused)
	}
	if err != nil || resp.StatusCode != http.StatusForbidden {
		return resp, err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr == nil && deniesTheOwner(string(body)) {
		c.onDeny()
	}
	return resp, nil
}

// clearReplyMark takes back a reply mark; a variable so a test can make it fail.
var clearReplyMark = database.ClearTelegramInboxReplyStarted

// reconcile records a handler's call in its ledger. A call that provably never
// reached Telegram — or that Telegram refused, with a 429 — takes back the update's reply mark — unless an earlier call
// of the same update may have reached it — so the update stays safe to run
// again instead of being closed as uncertain.
func (c *observingClient) reconcile(ctx context.Context, lease database.InboxLease, err error, refused bool) {
	class, _ := tgapi.Classify(err)
	unsent := refused || class == tgapi.ClassNotSent
	ledger := replyLedgerOf(ctx)
	if ledger == nil {
		return
	}
	if ledger.record(unsent) && unsent {
		if clearErr := clearReplyMark(context.WithoutCancel(ctx), lease); clearErr != nil {
			logging.Warnf("Inbox: taking back the reply mark of update %d: %v", lease.UpdateID, clearErr)
		}
	}
}

// deniesTheOwner reports whether a 403 says the chat is closed to the bot:
// blocked by the user, or the user's account is gone.
func deniesTheOwner(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "bot was blocked") || strings.Contains(lower, "user is deactivated")
}
