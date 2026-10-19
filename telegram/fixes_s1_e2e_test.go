package telegram_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/telegram/tgtest"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regressions of the S1 review: a pre-issued link cannot move a bot another
// account confirmed; a holder that lost its inbox lease sends nothing, and a
// reply that may have gone is not sent again; made-up links are strangers'
// messages; adopted bots learn their name.

const botTakenText = "Этот бот уже подключён к другому аккаунту библиотеки\n\n" +
	"Своего бота можно подключить в профиле библиотеки."

// B1, the reviewer's ordering: A and B both enter the same token before
// anyone confirms; A confirms first; B's link no longer moves the bot.
func TestAPendingLinkCannotTakeAnActiveBot(t *testing.T) {
	s := newStack(t).start()
	accountA := s.seedUser("a")
	accountB := s.seedUser("b")
	secretA, _ := s.connect(accountA, botToken1)
	secretB, _ := s.connect(accountB, botToken1)

	s.linkVia(botToken1, ownerTG, secretA)
	assert.Equal(t, linkedText, s.waitMessage(ownerTG, 1).Params["text"])

	s.linkVia(botToken1, otherTG, secretB)
	refused := s.waitMessage(otherTG, 1)
	assert.Equal(t, botTakenText, refused.Params["text"])

	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE user_id = ? AND status = 'active'`, accountA),
		"A keeps the bot")
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_binding WHERE user_id = ?`, accountB))
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_link_token
		WHERE user_id = ? AND consumed_at IS NULL AND expires_at > now()`, accountB),
		"the bot's claim retired B's links")

	// Even a link of B's that escaped retiring — issued in the same instant,
	// say — does not move the bot: consumption checks the bot's owner itself.
	_, err := s.db.Exec(`UPDATE telegram_link_token SET expires_at = now() + interval '10 minutes' WHERE user_id = ?`, accountB)
	require.NoError(t, err)
	s.linkVia(botToken1, otherTG, secretB)
	assert.Equal(t, botTakenText, s.waitMessage(otherTG, 2).Params["text"])
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE user_id = ? AND status = 'active'`, accountA))

	// A relinking the same bot still works.
	_, out := s.api(accountA, http.MethodPost, "/bot", fmt.Sprintf(`{"token":%q}`, botToken1))
	again := linkSecretOf(out["link_url"])
	pins := len(s.fake.Calls("pinChatMessage"))
	s.linkVia(botToken1, ownerTG, again)
	s.bm.WaitIdle()
	// The chat has its home panel already: it is pinned again, not sent anew.
	assert.Greater(t, len(s.fake.Calls("pinChatMessage")), pins)
	assert.Len(t, s.messagesTo(ownerTG), 1)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE user_id = ? AND status = 'active' AND generation = 2`,
		accountA))
}

// B2, the reviewer's probe: the first holder's reply is in flight when its
// lease runs out; the sweeper of another process takes the row over. The reply
// may have gone, so it is not sent again — exactly one send.
func TestAReplyInFlightIsNotRepeatedOnTakeover(t *testing.T) {
	a := newStack(t).start()
	user := a.seedUser("reader")
	a.plainPanels(user)
	secret, _ := a.connect(user, botToken1)
	a.linkVia(botToken1, ownerTG, secret)
	a.waitMessage(ownerTG, 1)
	a.bm.WaitIdle()

	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letGo) // a failing assertion must not leave the reply parked
	var entered atomic.Int32
	a.fake.On(tgtest.Rule{
		Method: "sendMessage",
		Match: func(c tgtest.Call) bool {
			if !strings.Contains(c.Params["text"], "война") {
				return false
			}
			entered.Add(1)
			return true
		},
		Reply: func(w http.ResponseWriter, r *http.Request, c *tgtest.Call) {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			tgtest.OK(map[string]any{"message_id": 999, "date": 0, "chat": map[string]any{"id": ownerTG, "type": "private"}})(w, r, c)
		},
	})

	update := a.u.Text(ownerTG, "/b мир")
	a.hook(botToken1).Post(t, update)
	require.Eventually(t, func() bool { return entered.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	// The holder stalls past its lease; another process sweeps.
	_, err := a.db.Exec(`UPDATE telegram_update_inbox SET lease_expires_at = now() - interval '1 second' WHERE update_id = ?`, update.ID)
	require.NoError(t, err)
	b := a.secondPod()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.bm.StartInboxSweeper(ctx, time.Hour)
	require.Eventually(t, func() bool {
		return a.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND state = 'uncertain'`, update.ID) == 1
	}, 5*time.Second, 20*time.Millisecond, "the row is closed as uncertain, not handled again")
	assert.Equal(t, 1, a.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND fence = 2`, update.ID),
		"the takeover moved the fence on, so the first holder can no longer close it")

	letGo()
	a.bm.WaitIdle()
	b.bm.WaitIdle()
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), entered.Load(), "exactly one send of the reply")
}

// B2, fencing: a holder whose row was taken over makes no outgoing call at
// all, and cannot close the row.
func TestAHolderThatLostItsLeaseSendsNothing(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	s.bm.WaitIdle()

	update := s.u.Text(ownerTG, "/b мир")
	var ref int64
	_, err := s.db.QueryOne(pg.Scan(&ref), `SELECT id FROM telegram_bot`)
	require.NoError(t, err)
	stale := database.InboxLease{BotRef: ref, UpdateID: update.ID, Owner: uuid.New(), Fence: 1}
	// The row as the new holder left it: same owner id even, but the fence
	// has moved on.
	_, err = s.db.Exec(`INSERT INTO telegram_update_inbox (bot_ref, update_id, kind, payload, lease_owner, lease_expires_at, attempts, fence)
		VALUES (?, ?, 'message', '{}'::jsonb, ?, now() + interval '1 minute', 2, 2)`, ref, update.ID, stale.Owner)
	require.NoError(t, err)

	before := len(s.fake.Calls(""))
	s.bm.DispatchWithLease(ref, update, stale)
	assert.Len(t, s.fake.Calls(""), before, "no outgoing call of any kind")
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox
		WHERE update_id = ? AND state = 'pending' AND fence = 2 AND reply_started_at IS NULL`, update.ID),
		"the new holder's row is untouched")
}

// F1: a /start with a made-up, malformed or used link from a stranger is a
// stranger's message: nothing recorded, one explanation an hour.
func TestMadeUpLinksAreStrangersMessages(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	rows := s.count(`SELECT count(*) FROM telegram_update_inbox`)

	hook := s.hook(botToken1)
	for _, payload := range []string{
		"link_not-a-real-link",
		"link_" + strings.Repeat("A", 32), // well formed, never issued
		"link_" + secret,                  // used
		"link_",
	} {
		rec := hook.Post(t, s.u.Text(otherTG, "/start "+payload))
		assert.Equal(t, http.StatusOK, rec.Code)
	}
	assert.Equal(t, strangerTxt, s.waitMessage(otherTG, 1).Params["text"])
	s.settle(otherTG, 1)
	assert.Equal(t, rows, s.count(`SELECT count(*) FROM telegram_update_inbox`), "nothing a stranger sent is recorded")
}

// F2: an adopted bot learns its public name, once, after it is served; the
// site shows it.
func TestAdoptedBotsLearnTheirName(t *testing.T) {
	s := newStack(t)
	user := s.seedUser("reader")
	owner := ownerTG
	s.seedLegacyBot(user, botToken1, uuid.NewString(), &owner)
	s.start()

	require.Eventually(t, func() bool {
		_, status := s.api(user, http.MethodGet, "/bot/status", "")
		return status["bot_username"] == botName1
	}, 5*time.Second, 50*time.Millisecond)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot WHERE display_name = 'Test Bot'`))
	assert.Len(t, s.fake.Calls("getMe"), 1)

	// Served again — another process, or the next start — the name is
	// known; getMe is asked once more, for whether the bot answers inline
	// queries (S5b), now that every bot is v2 (S6).
	s.secondPod()
	require.Eventually(t, func() bool { return len(s.fake.Calls("getMe")) == 2 }, 5*time.Second, 20*time.Millisecond)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot WHERE display_name = 'Test Bot'`))
}

// F2: a bot whose name could not be read is tried again by the health check;
// one failing bot does not stop the others.
func TestAdoptionNameFailuresDoNotBlockOtherBots(t *testing.T) {
	s := newStack(t)
	first := s.seedUser("first")
	second := s.seedUser("second")
	s.seedLegacyBot(first, botToken1, uuid.NewString(), nil)
	s.seedLegacyBot(second, botToken2, uuid.NewString(), nil)
	s.fake.On(tgtest.Rule{Method: "getMe", Match: func(c tgtest.Call) bool { return c.Token == botToken1 },
		Reply: tgtest.Error(http.StatusInternalServerError, "Internal Server Error")})
	s.start()

	require.Eventually(t, func() bool {
		return s.count(`SELECT count(*) FROM telegram_bot WHERE username = ?`, botName2) == 1
	}, 5*time.Second, 50*time.Millisecond)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot WHERE username = ''`))
}
