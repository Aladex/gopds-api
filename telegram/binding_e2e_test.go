package telegram_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/telegram/tgtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice S1: a bot gets its owner only through a one-time link from the
// authorized site profile, and every update is recorded before Telegram is
// told it arrived.

const (
	botToken1 = "987654321:AAH-first-bot-token-0123456789abcdef"
	botToken2 = "876543210:AAH-second-bot-token-0123456789abcd"
	botName1  = "test987654321_bot"
	botName2  = "test876543210_bot"
	ownerTG   = int64(7001)
	otherTG   = int64(7002)
)

const (
	// linkedText is the home panel a chat just bound gets (plan-ui.md §1.1).
	linkedText  = "Бот подключён.\n\nНапиши название книги или имя автора — или выбери:"
	expiredText = "Ссылка подключения истекла\n\nОткрой профиль на сайте и получи новую ссылку."
	unlinked    = "Бот ещё не подключён к библиотеке\n\nОткрой свой профиль на сайте и заверши подключение."
	strangerTxt = "Это личный бот другого пользователя\n\nСвоего бота можно подключить в профиле библиотеки."
)

// linkVia sends /start link_{secret} from tgUser to the bot of token.
func (s *stack) linkVia(token string, tgUser int64, secret string) {
	s.t.Helper()
	rec := s.hook(token).Post(s.t, s.u.Text(tgUser, "/start link_"+secret))
	require.Equal(s.t, http.StatusOK, rec.Code)
}

// (a) The site checks the token, registers the bot and gives a link; the
// owner opens it and the bot is bound to the account that asked.
func TestConnectAnExistingBotThroughTheSite(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")

	secret, pending := s.connect(user, botToken1)
	assert.Equal(t, "pending_link", pending["state"])
	assert.Equal(t, botName1, pending["pending_bot_username"])
	assert.Contains(t, pending["link_url"], "https://t.me/"+botName1+"?start=link_")
	assert.NotEmpty(t, pending["link_expires_at"])
	require.Len(t, s.fake.Calls("getMe"), 1, "the token is checked with Telegram")
	hooks := s.fake.Calls("setWebhook")
	require.Len(t, hooks, 1)
	assert.Regexp(t, `^`+stackBaseURL+`/telegram/[0-9a-f-]{36}$`, hooks[0].Params["url"])

	s.linkVia(botToken1, ownerTG, secret)
	linked := s.waitMessage(ownerTG, 1)
	assert.Equal(t, linkedText, linked.Params["text"])

	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding
		WHERE user_id = ? AND tg_user_id = ? AND status = 'active' AND generation = 1`, user, ownerTG))
	code, status := s.api(user, http.MethodGet, "/bot/status", "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "connected", status["state"])
	assert.Equal(t, botName1, status["bot_username"])
	assert.Equal(t, "Reader", status["telegram_name"])
	assert.Nil(t, status["link_url"], "the link is given only once")

	// The token is not readable at rest, and the old columns are not used.
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_bot WHERE position(? in encode(token_enc, 'escape')) > 0`,
		botToken1))
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM auth_user WHERE bot_token <> ''`))
}

// (b) Telegram delivers an update again when it did not hear 200 in time.
// The second copy is recognized by (bot, update_id) and not handled.
func TestARedeliveredUpdateIsHandledOnce(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)

	update := s.u.Text(ownerTG, "/start link_"+secret)
	first := s.hook(botToken1).Post(t, update)
	second := s.hook(botToken1).Post(t, update)
	assert.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, http.StatusOK, second.Code)
	s.settle(ownerTG, 1)

	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox
		WHERE update_id = ? AND state = 'done' AND payload IS NULL AND lease_owner IS NULL`, update.ID))
}

// (c) A stranger writing to a linked bot learns it is someone else's, once
// an hour, and leaves nothing behind; their buttons do nothing; a used link
// does not hand them the bot, nor even reach it.
func TestAStrangerCannotTakeALinkedBot(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)

	hook := s.hook(botToken1)
	hook.Post(t, s.u.Text(otherTG, "/start"))
	hook.Post(t, s.u.Text(otherTG, "Основание"))
	stranger := s.waitMessage(otherTG, 1)
	assert.Equal(t, strangerTxt, stranger.Params["text"])
	assert.Equal(t, [][]string{{"Открыть сайт"}}, tgtest.ButtonTexts(t, &stranger))
	s.settle(otherTG, 1)

	hook.Post(t, s.u.Callback(otherTG, &stranger, "select:1"))
	answers := s.fake.WaitCalls(t, "answerCallbackQuery", 1, 5*time.Second)
	assert.Equal(t, "Эта кнопка недоступна.", answers[0].Params["text"])

	// A used link is no key: the stranger stays a stranger, already answered
	// this hour.
	hook.Post(t, s.u.Text(otherTG, "/start link_"+secret))
	s.settle(otherTG, 1)

	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active' AND tg_user_id = ?`, ownerTG))
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_binding WHERE tg_user_id = ?`, otherTG))
	// Recorded: the owner's /start with the link, nothing of the stranger's.
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_update_inbox`))
	for _, key := range s.mr.Keys() {
		if strings.HasPrefix(key, "tg2:stranger:") {
			continue
		}
		assert.NotContains(t, key, strconv.FormatInt(otherTG, 10), "a stranger leaves no conversation: %s", key)
	}
}

// (d) A link works for 15 minutes; after that, or when it was never issued,
// the bot says it expired and binds nobody.
func TestAnExpiredLinkDoesNotBind(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	_, err := s.db.Exec(`UPDATE telegram_link_token SET expires_at = now() - interval '1 second'`)
	require.NoError(t, err)

	s.linkVia(botToken1, ownerTG, secret)
	expired := s.waitMessage(ownerTG, 1)
	assert.Equal(t, expiredText, expired.Params["text"])
	// The exact URL the site's app-router test opens (src/app/__tests__/ProfileLink.test.tsx).
	keyboard := tgtest.Keyboard(t, &expired)
	require.Len(t, keyboard, 1)
	assert.Equal(t, "Открыть профиль", keyboard[0][0].Text)
	assert.Equal(t, stackSiteURL+"/?profile=telegram", keyboard[0][0].URL)
	// A link never issued is no key: on a bot nobody owns yet, a stranger
	// hears that the bot is not connected.
	s.linkVia(botToken1, ownerTG, "never-issued-secret-0123456789abc")
	assert.Equal(t, unlinked, s.waitMessage(ownerTG, 2).Params["text"])
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_binding`))

	code, status := s.api(user, http.MethodGet, "/bot/status", "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "pending_link", status["state"])
	assert.Nil(t, status["link_expires_at"], "no live link")

	code, fresh := s.api(user, http.MethodPost, "/link", "")
	require.Equal(t, http.StatusOK, code)
	freshSecret := linkSecretOf(fresh["link_url"])
	s.linkVia(botToken1, ownerTG, freshSecret)
	assert.Equal(t, linkedText, s.waitMessage(ownerTG, 3).Params["text"])
}

// linkSecretOf reads the secret from a link_url the site API returned.
func linkSecretOf(v any) string {
	link, _ := v.(string)
	_, secret, _ := strings.Cut(link, "?start=link_")
	return secret
}

// (e) Changing the bot: the old connection keeps working until the new bot
// is confirmed from Telegram, then ends in the same transaction.
func TestChangingTheBotKeepsTheOldOneUntilConfirmed(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret1, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret1)
	s.waitMessage(ownerTG, 1)

	secret2, status := s.connect(user, botToken2)
	assert.Equal(t, "connected", status["state"], "the old bot still works")
	assert.Equal(t, botName1, status["bot_username"])
	assert.Equal(t, botName2, status["pending_bot_username"])

	s.hook(botToken1).Post(t, s.u.Text(ownerTG, "/b мир"))
	assert.Contains(t, s.waitMessage(ownerTG, 2).Params["text"], "война и МИР", "the old bot is served meanwhile")

	s.linkVia(botToken2, ownerTG, secret2)
	assert.Equal(t, linkedText, s.waitMessage(ownerTG, 3).Params["text"])
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active' AND user_id = ?`, user))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding b JOIN telegram_bot t ON t.id = b.bot_ref
		WHERE b.status = 'active' AND t.tg_bot_id = 876543210`))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding b JOIN telegram_bot t ON t.id = b.bot_ref
		WHERE b.status = 'revoked' AND t.tg_bot_id = 987654321`))

	_, status = s.api(user, http.MethodGet, "/bot/status", "")
	assert.Equal(t, botName2, status["bot_username"])
	assert.Nil(t, status["pending_bot_username"])

	// The old bot has no owner now: it says so and serves nobody.
	s.hook(botToken1).Post(t, s.u.Text(ownerTG, "/b мир"))
	assert.Equal(t, unlinked, s.waitMessage(ownerTG, 4).Params["text"])
}

// (f) Telegram user ids no longer fit the old int32 column; they fit here.
func TestALargeTelegramIDLinks(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	const bigID = int64(5_000_000_000)

	s.linkVia(botToken1, bigID, secret)
	assert.Equal(t, linkedText, s.waitMessage(bigID, 1).Params["text"])
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE tg_user_id = ? AND status = 'active'`, bigID))

	s.hook(botToken1).Post(t, s.u.Text(bigID, "/b мир"))
	assert.Contains(t, s.waitMessage(bigID, 2).Params["text"], "война и МИР", "the account is found through the binding")
}

// (g) Update kinds the bot does not handle, and ones newer than the SDK, are
// acknowledged and dropped: Telegram does not retry them, nothing is sent.
func TestUnhandledUpdatesAreAcknowledged(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	before := len(s.fake.Calls(""))

	hook := s.hook(botToken1)
	for _, body := range []string{
		`{"update_id": 9001, "some_future_field": {"x": 1}}`,
		`{"update_id": 9002, "edited_message": {"message_id": 1, "date": 1, "chat": {"id": 7001, "type": "private"}, "text": "x"}}`,
		`{"update_id": 9003, "message": {"message_id": 2, "date": 1, "chat": {"id": 7001, "type": "private"},` +
			` "from": {"id": 7001, "is_bot": false, "first_name": "R"}, "text": "hi",` +
			` "entities": [{"type": "some_future_entity", "offset": 0, "length": 2}]}}`,
		`not json at all`,
	} {
		rec := hook.PostRaw(t, []byte(body))
		assert.Equal(t, http.StatusOK, rec.Code, body)
	}
	// The owner's text with an unknown entity is still the owner's text: it
	// is handled (today by the legacy router's fallback answer); the rest
	// left no row and drew no answer.
	s.settle(ownerTG, 2)
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id IN (9001, 9002)`))
	_ = before
}

// (h) Startup adopts legacy bots once, however often and in however many
// processes it runs; one bad legacy row does not stop the others.
func TestLegacyAdoptionIsIdempotent(t *testing.T) {
	s := newStack(t)
	good := s.seedUser("good")
	bad := s.seedUser("bad")
	owner := ownerTG
	s.seedLegacyBot(good, botToken1, uuid.NewString(), &owner)
	s.seedLegacyBot(bad, "not-a-token", uuid.NewString(), nil)
	s.start()
	s.secondPod()
	s.secondPod()

	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot`))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_binding WHERE user_id = ? AND tg_user_id = ? AND status = 'active'`,
		good, ownerTG))
	_, status := s.api(good, http.MethodGet, "/bot/status", "")
	assert.Equal(t, "connected", status["state"], "the existing owner keeps the bot without relinking")
}

// Q7: during a rollout two processes serve the same bots. An update both
// receive is handled once, by whichever recorded it first.
func TestTwoProcessesHandleAnUpdateOnce(t *testing.T) {
	a := newStack(t).start()
	user := a.seedUser("reader")
	secret, _ := a.connect(user, botToken1)
	a.linkVia(botToken1, ownerTG, secret)
	a.waitMessage(ownerTG, 1)
	b := a.secondPod()

	update := a.u.Text(ownerTG, "/b мир")
	var wg sync.WaitGroup
	for _, pod := range []*stack{a, b} {
		wg.Add(1)
		go func(pod *stack) {
			defer wg.Done()
			pod.hook(botToken1).Post(t, update)
		}(pod)
	}
	wg.Wait()
	a.bm.WaitIdle()
	b.bm.WaitIdle()
	a.settle(ownerTG, 2)
}

// Q7: an update whose process died mid-way is resumed by another, once —
// two processes sweeping at the same moment do not both take it.
func TestAStalledUpdateIsResumedOnce(t *testing.T) {
	a := newStack(t).start()
	user := a.seedUser("reader")
	secret, _ := a.connect(user, botToken1)
	a.linkVia(botToken1, ownerTG, secret)
	a.waitMessage(ownerTG, 1)
	a.bm.WaitIdle()

	update := a.u.Text(ownerTG, "/b мир")
	payload := fmt.Sprintf(`{"update_id": %d, "message": {"message_id": 9, "date": %d, "chat": {"id": %d, "type": "private"},`+
		` "from": {"id": %d, "is_bot": false, "first_name": "Reader"}, "text": "/b мир",`+
		` "entities": [{"type": "bot_command", "offset": 0, "length": 2}]}}`, update.ID, time.Now().Unix(), ownerTG, ownerTG)
	_, err := a.db.Exec(`INSERT INTO telegram_update_inbox (bot_ref, update_id, kind, payload, lease_owner, lease_expires_at, attempts)
		SELECT id, ?, 'message', ?::jsonb, ?, now() - interval '1 second', 1 FROM telegram_bot`, update.ID, payload, uuid.New())
	require.NoError(t, err)
	// A row a live process still holds is not anyone else's to take.
	_, err = a.db.Exec(`INSERT INTO telegram_update_inbox (bot_ref, update_id, kind, payload, lease_owner, lease_expires_at, attempts)
		SELECT id, ?, 'message', ?::jsonb, ?, now() + interval '1 minute', 1 FROM telegram_bot`,
		update.ID+1, strings.Replace(payload, fmt.Sprintf(`"update_id": %d`, update.ID), fmt.Sprintf(`"update_id": %d`, update.ID+1), 1),
		uuid.New())
	require.NoError(t, err)

	b := a.secondPod()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bm.StartInboxSweeper(ctx, time.Hour)
	b.bm.StartInboxSweeper(ctx, time.Hour)
	assert.Contains(t, a.waitMessage(ownerTG, 2).Params["text"], "война и МИР")
	time.Sleep(300 * time.Millisecond)
	a.bm.WaitIdle()
	b.bm.WaitIdle()
	a.settle(ownerTG, 2)
	assert.Equal(t, 1, a.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND state = 'done'`, update.ID))
	assert.Equal(t, 1, a.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND state = 'pending'`, update.ID+1),
		"the live lease is left alone")
}

// Disconnecting ends the binding, forgets the token and removes the webhook;
// the bot is no longer served.
func TestDisconnectForgetsTheBot(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	hook := s.hook(botToken1)

	code, status := s.api(user, http.MethodDelete, "/bot", "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "none", status["state"])
	require.Len(t, s.fake.Calls("deleteWebhook"), 1)
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active'`))
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_bot WHERE token_enc IS NOT NULL`))

	rec := hook.Post(t, s.u.Text(ownerTG, "/b мир"))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	s.settle(ownerTG, 1)
}

// When Telegram says the owner blocked the bot, the site shows it; the next
// message from the owner clears it.
func TestABlockedBotShowsOnTheSite(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	s.plainPanels(user)
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)

	s.fake.On(tgtest.Rule{Method: "sendMessage", Reply: tgtest.Forbidden(""), Times: 1})
	s.hook(botToken1).Post(t, s.u.Text(ownerTG, "/b мир"))
	s.bm.WaitIdle()
	_, status := s.api(user, http.MethodGet, "/bot/status", "")
	assert.Equal(t, "blocked", status["state"])

	s.hook(botToken1).Post(t, s.u.Text(ownerTG, "/b мир"))
	s.bm.WaitIdle()
	_, status = s.api(user, http.MethodGet, "/bot/status", "")
	assert.Equal(t, "connected", status["state"])
}

// A bot serving one account cannot be claimed by another with its token; a
// Telegram account serving one library account cannot be bound to a second.
func TestOneBotAndOneTelegramAccountPerLibraryAccount(t *testing.T) {
	s := newStack(t).start()
	first := s.seedUser("first")
	second := s.seedUser("second")
	secret, _ := s.connect(first, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)

	code, out := s.api(second, http.MethodPost, "/bot", fmt.Sprintf(`{"token":%q}`, botToken1))
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "bot_taken", out["error"])

	secret2, _ := s.connect(second, botToken2)
	s.linkVia(botToken2, ownerTG, secret2)
	assert.Equal(t, "Этот аккаунт Telegram уже подключён к другому аккаунту библиотеки\n\nСначала отключи его в профиле того аккаунта.",
		s.waitMessage(ownerTG, 2).Params["text"])
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_binding WHERE user_id = ?`, second))
}

// A token Telegram rejects is refused with a code the site translates.
func TestARejectedTokenIsRefused(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	s.fake.On(tgtest.Rule{Method: "getMe", Reply: tgtest.Error(http.StatusUnauthorized, "Unauthorized")})

	code, out := s.api(user, http.MethodPost, "/bot", fmt.Sprintf(`{"token":%q}`, botToken1))
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid_token", out["error"])
	code, out = s.api(user, http.MethodPost, "/bot", `{"token":"nonsense"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid_token", out["error"])
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_bot`))
}
