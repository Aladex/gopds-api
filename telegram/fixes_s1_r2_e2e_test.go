package telegram_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/telegram"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regressions of the second S1 fix review: a holder that lost its lease
// changes nothing durable and closes nothing; a reply that provably never
// left keeps its update replayable; concurrent confirmations never deadlock.

// botRef is the row id of the only bot.
func (s *stack) botRef() int64 {
	s.t.Helper()
	var ref int64
	_, err := s.db.QueryOne(pg.Scan(&ref), `SELECT id FROM telegram_bot ORDER BY id LIMIT 1`)
	require.NoError(s.t, err)
	return ref
}

// textPayload is the webhook JSON of a private text update.
func textPayload(updateID, from int64, text string) string {
	entities := ""
	if strings.HasPrefix(text, "/") {
		cmd, _, _ := strings.Cut(text, " ")
		entities = fmt.Sprintf(`, "entities": [{"type": "bot_command", "offset": 0, "length": %d}]`, len([]rune(cmd)))
	}
	return fmt.Sprintf(`{"update_id": %d, "message": {"message_id": 77, "date": %d, "chat": {"id": %d, "type": "private"},`+
		` "from": {"id": %d, "is_bot": false, "first_name": "Reader"}, "text": %q%s}}`,
		updateID, time.Now().Unix(), from, from, text, entities)
}

// B2a: a stale holder — its row's fence has moved on — cannot consume a link:
// the binding is not replaced, the link stays open, nothing is sent.
func TestAStaleHolderMustNotConsumeLink(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	s.bm.WaitIdle()

	// The owner asks to move the bot to another Telegram account.
	again, _ := s.connect(user, botToken1)
	update := s.u.Text(otherTG, "/start link_"+again)
	ref := s.botRef()
	stale := database.InboxLease{BotRef: ref, UpdateID: update.ID, Owner: uuid.New(), Fence: 1}
	_, err := s.db.Exec(`INSERT INTO telegram_update_inbox (bot_ref, update_id, kind, payload, lease_owner, lease_expires_at, attempts, fence)
		VALUES (?, ?, 'message', ?::jsonb, ?, now() + interval '1 minute', 2, 2)`,
		ref, update.ID, textPayload(update.ID, otherTG, "/start link_"+again), stale.Owner)
	require.NoError(t, err)

	before := len(s.fake.Calls(""))
	s.bm.DispatchWithLease(ref, update, stale)

	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active' AND tg_user_id = ?`, ownerTG),
		"the binding is unchanged")
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_binding WHERE tg_user_id = ?`, otherTG))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_link_token WHERE consumed_at IS NULL AND expires_at > now()`),
		"the link is still open")
	assert.Len(t, s.fake.Calls(""), before, "nothing sent")
}

// B2b: a holder past its lease, not yet taken over, is refused its reply —
// and does not close the update either: it stays, payload and all, for the
// next holder, who answers.
func TestAnExpiredHolderLeavesItsUpdateReplayable(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	s.bm.WaitIdle()

	update := s.u.Text(ownerTG, "/b мир")
	ref := s.botRef()
	holder := database.InboxLease{BotRef: ref, UpdateID: update.ID, Owner: uuid.New(), Fence: 1}
	_, err := s.db.Exec(`INSERT INTO telegram_update_inbox (bot_ref, update_id, kind, payload, lease_owner, lease_expires_at, attempts, fence)
		VALUES (?, ?, 'message', ?::jsonb, ?, now() - interval '1 second', 1, 1)`,
		ref, update.ID, textPayload(update.ID, ownerTG, "/b мир"), holder.Owner)
	require.NoError(t, err)

	s.bm.DispatchWithLease(ref, update, holder)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox
		WHERE update_id = ? AND state = 'pending' AND payload IS NOT NULL AND reply_started_at IS NULL`, update.ID))
	assert.Len(t, s.messagesTo(ownerTG), 1, "only the linked screen so far")

	other := s.secondPod()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	other.bm.StartInboxSweeper(ctx, time.Hour)
	assert.Contains(t, s.waitMessage(ownerTG, 2).Params["text"], "война и МИР", "the next holder answers")
}

// refuseArmedSendMessage refuses sendMessage like an unreachable server,
// before anything leaves, while armed.
type refuseArmedSendMessage struct {
	armed atomic.Bool
	hits  atomic.Int32
}

func (r *refuseArmedSendMessage) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.armed.Load() && strings.HasSuffix(req.URL.Path, "/sendMessage") {
		r.hits.Add(1)
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	return http.DefaultTransport.RoundTrip(req)
}

// B2c: the reply could not even be sent — the connection was refused. Its
// mark is taken back and the update handed back: the next holder answers.
func TestAProvablyUnsentReplyStaysReplayable(t *testing.T) {
	s := newStack(t)
	refuse := &refuseArmedSendMessage{}
	s.transport = refuse
	s.start()
	user := s.seedUser("reader")
	s.plainPanels(user)
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	s.bm.WaitIdle()

	refuse.armed.Store(true)
	update := s.u.Text(ownerTG, "/b мир")
	s.hook(botToken1).Post(t, update)
	s.bm.WaitIdle()
	require.GreaterOrEqual(t, refuse.hits.Load(), int32(1))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox
		WHERE update_id = ? AND state = 'pending' AND payload IS NOT NULL AND reply_started_at IS NULL
		  AND lease_expires_at <= now()`, update.ID), "handed back, not closed, not uncertain")

	refuse.armed.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.bm.StartInboxSweeper(ctx, time.Hour)
	assert.Contains(t, s.waitMessage(ownerTG, 2).Params["text"], "война и МИР", "answered on the replay")
	s.bm.WaitIdle()
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND state = 'done'`, update.ID))
}

// R1: two accounts' confirmations of the same bot arrive at once. Both wait
// for the bot; one binds it, the other hears that the bot is taken — never a
// deadlock answered as "the library is unavailable".
func TestConcurrentConfirmationsDoNotDeadlock(t *testing.T) {
	s := newStack(t).start()
	accountA := s.seedUser("a")
	accountB := s.seedUser("b")
	secretA, _ := s.connect(accountA, botToken1)
	secretB, _ := s.connect(accountB, botToken1)

	guard, err := s.db.Begin()
	require.NoError(t, err)
	_, err = guard.Exec(`SELECT 1 FROM telegram_bot WHERE id = ? FOR NO KEY UPDATE`, s.botRef())
	require.NoError(t, err)

	var wg sync.WaitGroup
	for _, c := range []struct {
		tg     int64
		secret string
	}{{ownerTG, secretA}, {otherTG, secretB}} {
		wg.Add(1)
		go func(tg int64, secret string) {
			defer wg.Done()
			s.hook(botToken1).Post(t, s.u.Text(tg, "/start link_"+secret))
		}(c.tg, c.secret)
	}
	wg.Wait()
	time.Sleep(500 * time.Millisecond) // both consumers are now waiting for the bot
	require.NoError(t, guard.Commit())

	a := s.waitMessage(ownerTG, 1).Params["text"]
	b := s.waitMessage(otherTG, 1).Params["text"]
	assert.ElementsMatch(t, []string{linkedText, botTakenText}, []string{a, b})
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active'`))
}

// The per-call clear of the reply mark fails — a database hiccup at that very
// moment. The hand-back clears the mark itself, in the same statement, so the
// update still replays instead of closing as uncertain.
func TestAFailedMarkClearStillLeavesTheUpdateReplayable(t *testing.T) {
	s := newStack(t)
	refuse := &refuseArmedSendMessage{}
	s.transport = refuse
	s.start()
	user := s.seedUser("reader")
	s.plainPanels(user)
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	s.bm.WaitIdle()

	restore := telegram.FailReplyMarkClears()
	defer restore()
	refuse.armed.Store(true)
	update := s.u.Text(ownerTG, "/b мир")
	s.hook(botToken1).Post(t, update)
	s.bm.WaitIdle()
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox
		WHERE update_id = ? AND state = 'pending' AND reply_started_at IS NULL AND lease_expires_at <= now()`, update.ID))

	refuse.armed.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.bm.StartInboxSweeper(ctx, time.Hour)
	assert.Contains(t, s.waitMessage(ownerTG, 2).Params["text"], "война и МИР")
}
