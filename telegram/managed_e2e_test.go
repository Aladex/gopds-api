package telegram_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice P1-1 (plan.md §6 P1-1, concept §C.3 and §I.2 «Managed»): a reader
// creates a personal bot from Telegram's creation window through the
// manager, without copying a token; the bot is bound by the one-time link
// only from its creator.

const (
	managerToken = "555000111:AAH-manager-bot-token-0123456789ab"
	managerName  = "test555000111_bot"
	creatorTG    = int64(7101)
	strangerTG   = int64(7102)

	copyNotCreator   = "Подтвердить подключение может только создатель этого бота\n\nСвоего бота можно подключить в профиле библиотеки."
	copyOwnerChanged = "Владелец этого бота в Telegram сменился. Подключи его заново в профиле библиотеки."
)

// newManaged is a stack whose manager may manage bots.
func newManaged(t *testing.T) *stack {
	t.Helper()
	s := newStack(t)
	s.managerToken = managerToken
	s.fake.Manager(managerToken)
	return s.start()
}

// startManaged is «Создать личного бота» as userID: it returns the username
// the creation window suggests.
func (s *stack) startManaged(userID int64) string {
	s.t.Helper()
	code, out := s.api(userID, http.MethodPost, "/managed", "")
	require.Equal(s.t, http.StatusOK, code, "managed answered %v", out)
	managed, _ := out["managed"].(map[string]any)
	require.Equal(s.t, "waiting", managed["state"], "status %v", out)
	create, err := url.Parse(managed["create_url"].(string))
	require.NoError(s.t, err)
	parts := strings.Split(strings.Trim(create.Path, "/"), "/")
	require.Len(s.t, parts, 3, "create_url %s", create)
	require.Equal(s.t, []string{"newbot", managerName}, parts[:2])
	assert.Equal(s.t, "Мои книги", create.Query().Get("name"))
	return parts[2]
}

// createBot is creatorID finishing the creation window: Telegram makes the
// bot of token, named username, and tells the manager.
func (s *stack) createBot(creatorID int64, token, username string) {
	s.t.Helper()
	s.fake.CreateManagedBot(token, username)
	rec := s.hook(managerToken).Post(s.t, s.u.ManagedBot(creatorID, token, username))
	require.Equal(s.t, http.StatusOK, rec.Code, rec.Body.String())
}

// status is userID's connection as the site sees it.
func (s *stack) status(userID int64) map[string]any {
	s.t.Helper()
	code, out := s.api(userID, http.MethodGet, "/bot/status", "")
	require.Equal(s.t, http.StatusOK, code)
	return out
}

// managedState is the state of userID's Managed connection, or "".
func (s *stack) managedState(userID int64) string {
	m, _ := s.status(userID)["managed"].(map[string]any)
	state, _ := m["state"].(string)
	return state
}

// newLink is the site's one-time link for userID's pending bot.
func (s *stack) newLink(userID int64) string {
	s.t.Helper()
	code, out := s.api(userID, http.MethodPost, "/link", "")
	require.Equal(s.t, http.StatusOK, code, "link answered %v", out)
	_, secret, found := strings.Cut(out["link_url"].(string), "?start=link_")
	require.True(s.t, found)
	return secret
}

// A new reader creates the bot, opens its link and is connected: the
// manager never writes to anyone, the bot is restricted to its owner, its
// webhook is here, it starts on v2 and answers a search.
func TestManagedBotFromCreationToSearch(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("managed-reader")
	assert.Equal(t, true, s.status(user)["managed_available"])

	username := s.startManaged(user)
	assert.Regexp(t, `^bd[a-z2-7]{8}bot$`, username)
	s.createBot(creatorTG, botToken1, username)

	status := s.status(user)
	assert.Equal(t, "pending_link", status["state"])
	assert.Equal(t, username, status["pending_bot_username"])
	assert.Equal(t, "ready", s.managedState(user))
	assert.True(t, s.fake.AccessRestricted(987654321))
	assert.Equal(t, stackBaseURL+"/telegram/"+s.hook(botToken1).Path[len("/telegram/"):], s.fake.Webhook(botToken1))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot WHERE kind = 'managed' AND router = 'v2' AND creator_tg_id = ?`, creatorTG))

	s.link(botToken1, creatorTG, s.newLink(user))
	assert.Equal(t, linkedText, s.waitMessage(creatorTG, 1).Params["text"])
	status = s.status(user)
	assert.Equal(t, "connected", status["state"])
	assert.Equal(t, "managed", status["bot_kind"])
	assert.Nil(t, status["managed"], "the connection is done")
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_pending_connection WHERE state = 'linked'`))

	s.hook(botToken1).Post(t, s.u.Text(creatorTG, "мир"))
	s.bm.WaitIdle()
	assert.Contains(t, s.messagesTo(creatorTG)[len(s.messagesTo(creatorTG))-1], "Поиск: «мир»")
	for _, c := range append(s.fake.Calls("sendMessage"), s.fake.Calls("sendRichMessage")...) {
		assert.NotEqual(t, managerToken, c.Token, "the manager has no chat with readers")
	}
}

// A reader who changed the suggested username names the bot instead; the
// proof is still the creator's /start.
func TestManagedBotWithAChangedUsername(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("renamer")
	s.startManaged(user)
	s.createBot(creatorTG, botToken1, "my_own_books_bot")
	assert.Equal(t, "waiting", s.managedState(user), "nothing to find it by")

	code, out := s.api(user, http.MethodPost, "/managed/claim", `{"username":"@My_Own_Books_Bot"}`)
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, "ready", s.managedState(user))
	s.link(botToken1, creatorTG, s.newLink(user))
	assert.Equal(t, "connected", s.status(user)["state"])

	code, out = s.api(user, http.MethodPost, "/managed/claim", `{"username":"someone_elses_bot"}`)
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "managed_not_found", out["error"])
}

// Two readers connecting at once each get their own bot; one cannot take the
// other's, by claim or by link.
func TestManagedConnectionsDoNotMix(t *testing.T) {
	s := newManaged(t)
	alice, bob := s.seedUser("alice"), s.seedUser("bob")
	aliceName, bobName := s.startManaged(alice), s.startManaged(bob)
	require.NotEqual(t, aliceName, bobName)

	s.createBot(strangerTG, botToken2, bobName)
	assert.Equal(t, "waiting", s.managedState(alice))
	assert.Equal(t, "ready", s.managedState(bob))

	code, out := s.api(alice, http.MethodPost, "/managed/claim", fmt.Sprintf(`{"username":%q}`, bobName))
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "bot_taken", out["error"])

	s.createBot(creatorTG, botToken1, aliceName)
	s.link(botToken1, creatorTG, s.newLink(alice))
	s.link(botToken2, strangerTG, s.newLink(bob))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding b JOIN telegram_bot t ON t.id = b.bot_ref
		WHERE b.status = 'active' AND b.user_id = ? AND t.tg_bot_id = 987654321 AND b.tg_user_id = ?`, alice, creatorTG))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding b JOIN telegram_bot t ON t.id = b.bot_ref
		WHERE b.status = 'active' AND b.user_id = ? AND t.tg_bot_id = 876543210 AND b.tg_user_id = ?`, bob, strangerTG))
}

// A repeated or late managed_bot update changes nothing: one bot, one
// token fetch, one binding.
func TestManagedRepeatedEventsMakeNothingTwice(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("repeat")
	username := s.startManaged(user)
	s.fake.CreateManagedBot(botToken1, username)
	update := s.u.ManagedBot(creatorTG, botToken1, username)
	manager := s.hook(managerToken)
	for range 3 {
		assert.Equal(t, http.StatusOK, manager.Post(t, update).Code)
	}
	s.link(botToken1, creatorTG, s.newLink(user))
	// Telegram resends the creation late, as a new update.
	assert.Equal(t, http.StatusOK, manager.Post(t, s.u.ManagedBot(creatorTG, botToken1, username)).Code)

	assert.Len(t, s.fake.Calls("getManagedBotToken"), 1)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot WHERE tg_bot_id = 987654321`))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding`))
	assert.Equal(t, "connected", s.status(user)["state"])
}

// Only the bot's creator links it, and only with a live link; a waiting
// connection past its 15 minutes finds nothing.
func TestManagedLinkRefusesStrangersAndExpiry(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("careful")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)

	s.link(botToken1, strangerTG, s.newLink(user))
	assert.Equal(t, copyNotCreator, s.waitMessage(strangerTG, 1).Params["text"])
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_binding`))

	secret := s.newLink(user)
	_, err := s.db.Exec(`UPDATE telegram_link_token SET expires_at = now() - interval '1 second'`)
	require.NoError(t, err)
	s.link(botToken1, creatorTG, secret)
	assert.Equal(t, expiredText, s.waitMessage(creatorTG, 1).Params["text"])
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_binding`))

	late := s.seedUser("late")
	lateName := s.startManaged(late)
	_, err = s.db.Exec(`UPDATE telegram_pending_connection SET expires_at = now() - interval '1 second' WHERE user_id = ?`, late)
	require.NoError(t, err)
	s.createBot(otherTG, botToken2, lateName)
	assert.Equal(t, "none", s.status(late)["state"])
	assert.Empty(t, s.managedState(late))
}

// A reader already connected keeps the old bot until the new one is linked;
// an old bot entered by its token keeps working beside the manager.
func TestManagedKeepsTheOldConnectionUntilLinked(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("switcher")
	secret, _ := s.connect(user, botToken2)
	s.link(botToken2, ownerTG, secret)
	require.Equal(t, "connected", s.status(user)["state"])

	username := s.startManaged(user)
	s.createBot(ownerTG, botToken1, username)
	status := s.status(user)
	assert.Equal(t, "connected", status["state"], "the old bot is still the connection")
	assert.Equal(t, "manual", status["bot_kind"])
	assert.Equal(t, username, status["pending_bot_username"])

	s.hook(botToken2).Post(t, s.u.Text(ownerTG, "мир"))
	s.bm.WaitIdle()
	assert.Contains(t, s.messagesTo(ownerTG)[len(s.messagesTo(ownerTG))-1], "Поиск: «мир»", "the old bot still answers")

	s.link(botToken1, ownerTG, s.newLink(user))
	status = s.status(user)
	assert.Equal(t, username, status["bot_username"])
	assert.Equal(t, "managed", status["bot_kind"])
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active'`))
}

// A bot moved to another owner in Telegram is suspended: it says so, and
// works again once its new owner links it from the account.
func TestManagedOwnerChangeSuspendsUntilLinked(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("giver")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	s.link(botToken1, creatorTG, s.newLink(user))

	s.createBot(strangerTG, botToken1, username)
	assert.Equal(t, "owner_changed", s.status(user)["state"])
	s.hook(botToken1).Post(t, s.u.Text(creatorTG, "мир"))
	s.bm.WaitIdle()
	assert.Equal(t, copyOwnerChanged, s.lastMessageTo(creatorTG))
	assert.Len(t, s.fake.Calls("replaceManagedBotToken"), 0)

	secret := s.newLink(user)
	s.link(botToken1, creatorTG, secret)
	assert.Equal(t, copyNotCreator, s.lastMessageTo(creatorTG), "the old owner cannot take it back")
	s.link(botToken1, strangerTG, secret)
	assert.Equal(t, "connected", s.status(user)["state"])
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active' AND tg_user_id = ?`, strangerTG))
}

// A rejected token is replaced through the manager on the owner's click, and
// never by itself.
func TestManagedRestoreOnClick(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("restorer")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	s.link(botToken1, creatorTG, s.newLink(user))
	_, err := s.db.Exec(`UPDATE telegram_bot SET status = 'token_rejected' WHERE tg_bot_id = 987654321`)
	require.NoError(t, err)
	s.createBot(creatorTG, botToken1, username)
	assert.Empty(t, s.fake.Calls("replaceManagedBotToken"), "not without the click")
	assert.Equal(t, "token_rejected", s.status(user)["state"])

	code, out := s.api(user, http.MethodPost, "/managed/restore", "")
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, "connected", out["state"])
	require.Len(t, s.fake.Calls("replaceManagedBotToken"), 1)
	fresh := s.fake.ManagedToken(987654321)
	require.NotEqual(t, botToken1, fresh)
	s.hook(fresh).Post(t, s.u.Text(creatorTG, "мир"))
	s.bm.WaitIdle()
	assert.Contains(t, s.lastMessageTo(creatorTG), "Поиск: «мир»")
}

// A bot that does not answer yet, or whose access Telegram did not restrict,
// is kept but not ready; «Проверить ещё раз» finishes it.
func TestManagedNotReadyThenCheckedAgain(t *testing.T) {
	failures := map[string]tgtest.Reply{
		"setWebhook":                  tgtest.Error(http.StatusBadGateway, "Bad Gateway"),
		"setManagedBotAccessSettings": tgtest.Error(http.StatusBadGateway, "Bad Gateway"),
		// Set, but Telegram says otherwise when asked back.
		"getManagedBotAccessSettings": tgtest.OK(map[string]any{"is_access_restricted": false}),
	}
	for method, reply := range failures {
		t.Run(method, func(t *testing.T) {
			s := newManaged(t)
			user := s.seedUser("patient")
			username := s.startManaged(user)
			s.fake.On(tgtest.Rule{Method: method, Match: func(c tgtest.Call) bool {
				return c.Token == botToken1 || c.Params["user_id"] == "987654321"
			}, Reply: reply, Times: 1})
			s.createBot(creatorTG, botToken1, username)
			assert.Equal(t, "not_ready", s.managedState(user))
			assert.Equal(t, "none", s.status(user)["state"], "no link before it is ready")
			code, _ := s.api(user, http.MethodPost, "/link", "")
			assert.Equal(t, http.StatusConflict, code)

			code, out := s.api(user, http.MethodPost, "/managed/check", "")
			require.Equal(t, http.StatusOK, code, "%v", out)
			assert.Equal(t, "ready", s.managedState(user))
			assert.True(t, s.fake.AccessRestricted(987654321))
		})
	}
}

// «Отменить подключение» stops the wait; Telegram's bot is not touched.
func TestManagedCancel(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("changed-mind")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	code, out := s.api(user, http.MethodDelete, "/managed", "")
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, out["managed"])
	assert.Equal(t, "none", out["state"])
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_pending_connection WHERE state = 'canceled'`))
}

// Without a manager token, or with a manager Telegram does not let manage
// bots, the Managed path is off and the token path is the way.
func TestManagedOffWithoutAManager(t *testing.T) {
	for name, managerOn := range map[string]bool{"no token": false, "not allowed": true} {
		t.Run(name, func(t *testing.T) {
			s := newStack(t)
			if managerOn {
				s.managerToken = managerToken
			}
			s.start()
			user := s.seedUser("plain")
			assert.Equal(t, false, s.status(user)["managed_available"])
			code, out := s.api(user, http.MethodPost, "/managed", "")
			assert.Equal(t, http.StatusConflict, code)
			assert.Equal(t, "managed_unavailable", out["error"])
			for _, c := range s.fake.Calls("setWebhook") {
				assert.NotEqual(t, managerToken, c.Token)
			}
			secret, _ := s.connect(user, botToken1)
			s.link(botToken1, ownerTG, secret)
			assert.Equal(t, "connected", s.status(user)["state"])
		})
	}
}

// The manager's webhook answers only at its path and with its secret.
func TestManagedWebhookIsGuarded(t *testing.T) {
	s := newManaged(t)
	hook := s.hook(managerToken)
	update := s.u.ManagedBot(creatorTG, botToken1, "bdaaaaaaaabot")
	wrong := hook
	wrong.Secret = "not the secret"
	assert.Equal(t, http.StatusUnauthorized, wrong.Post(t, update).Code)
	elsewhere := hook
	elsewhere.Path = "/telegram/manager/" + strings.Repeat("0", 8) + "-0000-4000-8000-000000000000"
	assert.Equal(t, http.StatusNotFound, elsewhere.Post(t, update).Code)
	assert.Empty(t, s.fake.Calls("getManagedBotToken"))
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_managed_event`))
}

// link sends /start link_{secret} from tgUser to the bot of token and waits
// until it is handled.
func (s *stack) link(token string, tgUser int64, secret string) {
	s.t.Helper()
	s.linkVia(token, tgUser, secret)
	s.bm.WaitIdle()
}

// lastMessageTo is the text of the last message sent to chatID.
func (s *stack) lastMessageTo(chatID int64) string {
	texts := s.messagesTo(chatID)
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}
