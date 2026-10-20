package telegram_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The P1-1 review's round (review-p1-1.md B1–B3, C1): events applied in
// order, canceled links dead, an owner change stopping a send already
// prepared, and one process per event.

// B1: a notice delivered late, older than a transfer already applied, does
// not give the bot back to its former owner.
func TestManagedALateEventCannotUndoATransfer(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("ordered-reader")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	s.link(botToken1, creatorTG, s.newLink(user))
	delayed := s.u.ManagedBot(creatorTG, botToken1, username)
	transfer := s.u.ManagedBot(strangerTG, botToken1, username)
	hook := s.hook(managerToken)
	require.Equal(t, http.StatusOK, hook.Post(t, transfer).Code)
	require.Equal(t, http.StatusOK, hook.Post(t, delayed).Code)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot WHERE creator_tg_id = ? AND status = 'owner_changed'`, strangerTG),
		"the older notice is kept, not applied")
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_managed_event WHERE update_id = ? AND processed_at IS NOT NULL`, delayed.ID))

	secret := s.newLink(user)
	s.link(botToken1, creatorTG, secret)
	assert.Equal(t, "owner_changed", s.status(user)["state"], "the former owner cannot take it back")
	s.link(botToken1, strangerTG, secret)
	assert.Equal(t, linkedText, s.lastMessageTo(strangerTG))
	assert.Equal(t, "connected", s.status(user)["state"])
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active' AND tg_user_id = ?`, creatorTG))
}

// B1: a newer event naming the first owner again is a transfer back.
func TestManagedATransferBackIsApplied(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("returner")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	s.link(botToken1, creatorTG, s.newLink(user))
	s.createBot(strangerTG, botToken1, username)
	s.createBot(creatorTG, botToken1, username)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot WHERE creator_tg_id = ? AND status = 'owner_changed'`, creatorTG))
	s.link(botToken1, creatorTG, s.newLink(user))
	assert.Equal(t, "connected", s.status(user)["state"])
}

// B2: a link given before the connection was canceled binds nothing; the
// bot still connected stays.
func TestManagedACanceledLinkCannotReplaceTheWorkingBot(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("cancel-reader")
	secret, _ := s.connect(user, botToken2)
	s.link(botToken2, creatorTG, secret)
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	canceled := s.newLink(user)
	code, out := s.api(user, http.MethodDelete, "/managed", "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_link_token t JOIN telegram_bot b ON b.id = t.bot_ref
		WHERE b.tg_bot_id = 987654321 AND t.consumed_at IS NULL AND t.expires_at > now()`), "its links are dead")

	s.link(botToken1, creatorTG, canceled)
	assert.Equal(t, expiredText, s.lastMessageTo(creatorTG))
	assert.Equal(t, botName2, s.status(user)["bot_username"])
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding b JOIN telegram_bot t ON t.id = b.bot_ref
		WHERE b.status = 'active' AND t.tg_bot_id = 876543210`))
}

// B2: a new «Создать личного бота» replaces the connection, and the links
// of the bot it had found die with it.
func TestManagedARestartedConnectionKillsTheOldLinks(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("restarter")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	old := s.newLink(user)
	s.startManaged(user)
	s.link(botToken1, creatorTG, old)
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_binding`))
}

// B2: a link stands only for the account's current choice: after «Бот уже
// создан» names another bot, the first one's link binds nothing.
func TestManagedALinkOfAnAbandonedCandidateBindsNothing(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("chooser")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	first := s.newLink(user)
	s.createBot(creatorTG, botToken2, "my_second_books_bot")
	code, out := s.api(user, http.MethodPost, "/managed/claim", `{"username":"my_second_books_bot"}`)
	require.Equal(t, http.StatusOK, code, "%v", out)

	s.link(botToken1, creatorTG, first)
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_binding`))
	s.link(botToken2, creatorTG, s.newLink(user))
	assert.Equal(t, "my_second_books_bot", s.status(user)["bot_username"])
}

// B3: a send prepared before the bot changed owner does not go out; the
// site hears why, the chat nothing.
func TestManagedAnOwnerChangeStopsAPreparedSend(t *testing.T) {
	s := newStack(t)
	s.managerToken = managerToken
	s.fake.Manager(managerToken)
	entered, gate := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	s.deliveryCfg.Prepare = func(ctx context.Context, _ *models.Book, _ string, _ int64) (delivery.File, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return delivery.File{}, ctx.Err()
		case <-gate:
		}
		return delivery.File{Data: []byte("PK book"), Name: "book.epub"}, nil
	}
	s.start()
	user := s.seedUser("job-reader")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	s.link(botToken1, creatorTG, s.newLink(user))
	messages := len(s.messagesTo(creatorTG))
	refused, outcome := s.delivery.SendFromSite(context.Background(), user, s.books[scanfixture.EntryMultiAuthor])
	require.Empty(t, refused)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the preparation never began")
	}
	s.createBot(strangerTG, botToken1, username)
	release.Do(func() { close(gate) })
	select {
	case got := <-outcome:
		assert.Equal(t, delivery.SiteOwnerChanged, got)
	case <-time.After(8 * time.Second):
		t.Fatal("the job never ended")
	}
	assert.Empty(t, s.fake.Calls("sendDocument"))
	assert.Len(t, s.messagesTo(creatorTG), messages, "the chat is not told")
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_delivery WHERE outcome = ?`, models.OutcomeOwnerChanged))

	again, _ := s.delivery.SendFromSite(context.Background(), user, s.books[scanfixture.EntryMultiAuthor])
	assert.Equal(t, delivery.SiteOwnerChanged, again, "a new request is refused at once")
}

// C1: the same update at two processes at once is handled by one.
func TestManagedOneEventIsHandledByOneProcess(t *testing.T) {
	s := newManaged(t)
	other := s.secondPod()
	user := s.seedUser("event-reader")
	username := s.startManaged(user)
	s.fake.CreateManagedBot(botToken1, username)
	entered, gate := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	s.fake.On(tgtest.Rule{Method: "getManagedBotToken", Times: 1, Reply: func(w http.ResponseWriter, r *http.Request, c *tgtest.Call) {
		close(entered)
		<-gate
		tgtest.OK(botToken1)(w, r, c)
	}})
	update := s.u.ManagedBot(creatorTG, botToken1, username)
	done := make(chan int, 1)
	go func() { done <- s.hook(managerToken).Post(t, update).Code }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the token request never began")
	}
	second := other.hook(managerToken).Post(t, update)
	release.Do(func() { close(gate) })
	require.Equal(t, http.StatusOK, <-done)
	require.Equal(t, http.StatusOK, second.Code)
	assert.Len(t, s.fake.Calls("getManagedBotToken"), 1)
	assert.Equal(t, "ready", s.managedState(user))
}

// C1: a failed handling lets the event go — Telegram's next delivery is
// handled at once; one a stopped process left claimed is taken up by the
// sweep.
func TestManagedAnUnfinishedEventIsTakenUpAgain(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("retried")
	username := s.startManaged(user)
	s.fake.CreateManagedBot(botToken1, username)
	s.fake.On(tgtest.Rule{Method: "getManagedBotToken", Times: 1,
		Reply: tgtest.Error(http.StatusInternalServerError, "Internal Server Error")})
	update := s.u.ManagedBot(creatorTG, botToken1, username)
	hook := s.hook(managerToken)
	assert.Equal(t, http.StatusInternalServerError, hook.Post(t, update).Code)
	assert.Equal(t, http.StatusOK, hook.Post(t, update).Code)
	assert.Equal(t, "ready", s.managedState(user))

	late := s.seedUser("late")
	lateName := s.startManaged(late)
	s.fake.CreateManagedBot(botToken2, lateName)
	// A process claimed it and stopped: the claim runs out, nothing handled.
	_, err := s.db.Exec(`INSERT INTO telegram_managed_event (update_id, bot_tg_id, creator_tg_id, bot_username, claimed_by, claim_expires_at)
		VALUES (99999, 876543210, ?, ?, gen_random_uuid(), now() - interval '1 second')`, otherTG, lateName)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := s.bm.StartInboxSweeper(ctx, time.Hour)
	defer func() { cancel(); <-stopped }()
	require.Eventually(t, func() bool { return s.managedState(late) == "ready" }, 10*time.Second, 50*time.Millisecond)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_managed_event WHERE update_id = 99999 AND processed_at IS NOT NULL`))
}
