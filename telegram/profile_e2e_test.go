package telegram_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/telegram"
	"gopds-api/telegram/profile"
	"gopds-api/telegram/tgapi"
	"gopds-api/telegram/tgtest"

	"github.com/go-pg/pg/v10"
	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice P1-2 (plan.md §6 P1-2, concept §G.5): a reader's bot looks like the
// library's — the avatar and the descriptions Telegram shows before Start.
// A managed bot gets them when it is made; a bot connected by its token only
// when its owner asks.

// countFor is how many calls of method the bot of token made.
func countFor(f *tgtest.FakeBotAPI, method, token string) int {
	n := 0
	for _, c := range f.Calls(method) {
		if c.Token == token {
			n++
		}
	}
	return n
}

// assertLibraryLook checks the bot of token has the library's look.
func assertLibraryLook(t *testing.T, f *tgtest.FakeBotAPI, token string) {
	t.Helper()
	p := f.ProfileOf(token)
	require.NotNil(t, p, "the bot was given a look")
	assert.Equal(t, profile.Description, p.Description)
	assert.Equal(t, profile.ShortDescription, p.ShortDescription)
	require.NotNil(t, p.Photo, "and an avatar")
	assert.Positive(t, p.Photo.Size)
}

// A new managed bot is ready only with its look; the reader sees it applied.
func TestAManagedBotGetsTheLibrarysLook(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("styled")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	assert.Equal(t, "ready", s.managedState(user))
	assertLibraryLook(t, s.fake, botToken1)
	assert.Zero(t, countFor(s.fake, "setMyProfilePhoto", managerToken), "the manager's own look is untouched")

	s.link(botToken1, creatorTG, s.newLink(user))
	assert.Equal(t, true, s.status(user)["profile_applied"])
}

// The look is part of readiness: a refused photo or descriptions Telegram
// did not keep leave the bot not ready, and «Проверить ещё раз» finishes it.
func TestAManagedBotIsNotReadyWithoutItsLook(t *testing.T) {
	failures := map[string]tgtest.Reply{
		"setMyProfilePhoto": tgtest.Error(http.StatusBadRequest, "Bad Request: PHOTO_INVALID_DIMENSIONS"),
		"getMyDescription":  tgtest.OK(map[string]string{"description": "something else"}),
	}
	for method, reply := range failures {
		t.Run(method, func(t *testing.T) {
			s := newManaged(t)
			user := s.seedUser("patient")
			username := s.startManaged(user)
			s.fake.On(tgtest.Rule{Method: method, Match: func(c tgtest.Call) bool { return c.Token == botToken1 },
				Reply: reply, Times: 1})
			s.createBot(creatorTG, botToken1, username)
			assert.Equal(t, "not_ready", s.managedState(user))
			code, _ := s.api(user, http.MethodPost, "/managed/check", "")
			require.Equal(t, http.StatusOK, code)
			assert.Equal(t, "ready", s.managedState(user))
			assertLibraryLook(t, s.fake, botToken1)
		})
	}
}

// A bot that has its look is never given it again by itself: a later check
// sets nothing, so an owner's own avatar stays.
func TestTheLookIsGivenOnce(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("once")
	username := s.startManaged(user)
	s.fake.On(tgtest.Rule{Method: "setWebhook", Match: func(c tgtest.Call) bool { return c.Token == botToken1 },
		Reply: tgtest.Error(http.StatusBadGateway, "Bad Gateway"), Times: 1})
	s.createBot(creatorTG, botToken1, username)
	require.Equal(t, "not_ready", s.managedState(user))
	code, _ := s.api(user, http.MethodPost, "/managed/check", "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "ready", s.managedState(user))
	assert.Equal(t, 1, countFor(s.fake, "setMyProfilePhoto", botToken1))
	assert.Equal(t, 1, countFor(s.fake, "setMyDescription", botToken1))
}

// A bot connected by its token keeps its own look, until its owner asks for
// the library's on the site.
func TestAManualBotIsStyledOnlyOnRequest(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("own-look")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	assert.Nil(t, s.fake.ProfileOf(botToken1), "nothing set by itself")
	assert.Equal(t, false, s.status(user)["profile_applied"])

	code, out := s.api(user, http.MethodPost, "/profile", `{"bot_username":"`+botName1+`"}`)
	require.Equal(t, http.StatusOK, code, "%v", out)
	assert.Equal(t, true, out["profile_applied"])
	assertLibraryLook(t, s.fake, botToken1)
	assert.Equal(t, "connected", out["state"])
}

// «Оформить бота» needs a connected bot, and says when Telegram refused.
func TestStylingAManualBotCanFail(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("unstyled")
	code, out := s.api(user, http.MethodPost, "/profile", "")
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "no_pending_bot", out["error"])

	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	s.fake.On(tgtest.Rule{Method: "setMyProfilePhoto", Reply: tgtest.Error(http.StatusBadRequest, "Bad Request: PHOTO_INVALID"), Times: 1})
	code, out = s.api(user, http.MethodPost, "/profile", `{"bot_username":"`+botName1+`"}`)
	assert.Equal(t, http.StatusBadGateway, code)
	assert.Equal(t, "telegram_unavailable", out["error"])
	assert.Equal(t, false, s.status(user)["profile_applied"])
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_bot_profile`))
}

// The owner's consent is for the bot the page showed: a page left open while
// another bot was connected styles neither (review-p1-2 B1).
func TestStylingNamesTheBotShown(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("two-tabs")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	secret, _ = s.connect(user, botToken2)
	s.linkVia(botToken2, ownerTG, secret)
	s.bm.WaitIdle()
	require.Equal(t, botName2, s.status(user)["bot_username"])

	for _, body := range []string{`{"bot_username":"` + botName1 + `"}`, `{}`, ``} {
		code, out := s.api(user, http.MethodPost, "/profile", body)
		assert.Equal(t, http.StatusConflict, code, "body %q: %v", body, out)
		assert.Equal(t, "bot_changed", out["error"])
	}
	assert.Nil(t, s.fake.ProfileOf(botToken2), "the new bot's owner did not agree to it")
	assert.Nil(t, s.fake.ProfileOf(botToken1))

	code, out := s.api(user, http.MethodPost, "/profile", `{"bot_username":"@`+strings.ToUpper(botName2)+`"}`)
	require.Equal(t, http.StatusOK, code, "%v", out)
	assertLibraryLook(t, s.fake, botToken2)
}

// Two checks at once set the look up once: the second, holding no claim,
// calls Telegram for nothing — so an owner's change after the bot got ready
// stays (review-p1-2 B2).
func TestTwoChecksSetTheLookUpOnce(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("two-checks")
	username := s.startManaged(user)
	s.fake.On(tgtest.Rule{Method: "setMyProfilePhoto", Times: 1, Reply: tgtest.Error(http.StatusBadRequest, "Bad Request: PHOTO_INVALID")})
	s.createBot(creatorTG, botToken1, username)
	require.Equal(t, "not_ready", s.managedState(user))
	require.Equal(t, 1, countFor(s.fake, "setMyDescription", botToken1))

	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	s.fake.On(tgtest.Rule{Method: "setMyDescription", Times: 1, Reply: s.fake.Gate(gate)})
	first := make(chan int, 1)
	go func() { code, _ := s.api(user, http.MethodPost, "/managed/check", ""); first <- code }()
	s.fake.WaitCalls(t, "setMyDescription", 2, 5*time.Second)

	code, _ := s.api(user, http.MethodPost, "/managed/check", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "not_ready", s.managedState(user), "not ready while the first still sets it up")
	assert.Equal(t, 2, countFor(s.fake, "setMyDescription", botToken1), "the second calls Telegram for nothing")

	release.Do(func() { close(gate) })
	require.Equal(t, http.StatusOK, <-first)
	require.Equal(t, "ready", s.managedState(user))

	// The owner changes the avatar of the ready bot; a later check keeps it.
	client, err := tgapi.New(botToken1, tgapi.Endpoint{ServerURL: s.fake.URL()})
	require.NoError(t, err)
	_, err = client.SetMyProfilePhoto(context.Background(), &bot.SetMyProfilePhotoParams{Photo: &tgbot.InputProfilePhotoStatic{
		Photo: "attach://owner", MediaAttachment: bytes.NewReader([]byte("owner's own photo")),
	}})
	require.NoError(t, err)
	own := s.fake.ProfileOf(botToken1).Photo.SHA256
	code, _ = s.api(user, http.MethodPost, "/managed/check", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, own, s.fake.ProfileOf(botToken1).Photo.SHA256, "the owner's avatar stays")
}

// A setup slower than its claim gives up before the claim runs out: the next
// check takes it and finishes, and nothing the slow one sent later changes
// the owner's look (review-p1-2-fixes B2, the slow recheck).
func TestASlowSetUpGivesUpBeforeItsClaimRunsOut(t *testing.T) {
	defer telegram.ProfileTimings(3*time.Second, time.Second)()
	s := newManaged(t)
	user := s.seedUser("slow-telegram")
	username := s.startManaged(user)
	s.fake.On(tgtest.Rule{Method: "setMyProfilePhoto", Times: 1, Reply: tgtest.Error(http.StatusBadRequest, "Bad Request: PHOTO_INVALID")})
	s.createBot(creatorTG, botToken1, username)
	require.Equal(t, "not_ready", s.managedState(user))

	stuck := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(stuck) })
	s.fake.On(tgtest.Rule{Method: "setMyDescription", Times: 1, Reply: s.fake.Gate(stuck)})
	started := time.Now()
	code, _ := s.api(user, http.MethodPost, "/managed/check", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Less(t, time.Since(started), 3*time.Second, "cut off before the claim runs out")
	assert.Equal(t, "not_ready", s.managedState(user))
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_bot_profile WHERE claimed_until IS NOT NULL OR applied_at IS NOT NULL`),
		"the claim is given up, nothing recorded")

	code, _ = s.api(user, http.MethodPost, "/managed/check", "")
	assert.Equal(t, http.StatusOK, code)
	require.Equal(t, "ready", s.managedState(user))

	client, err := tgapi.New(botToken1, tgapi.Endpoint{ServerURL: s.fake.URL()})
	require.NoError(t, err)
	_, err = client.SetMyProfilePhoto(context.Background(), &bot.SetMyProfilePhotoParams{Photo: &tgbot.InputProfilePhotoStatic{
		Photo: "attach://owner", MediaAttachment: bytes.NewReader([]byte("owner's own photo")),
	}})
	require.NoError(t, err)
	own := s.fake.ProfileOf(botToken1).Photo.SHA256
	release.Do(func() { close(stuck) })
	code, _ = s.api(user, http.MethodPost, "/managed/check", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, own, s.fake.ProfileOf(botToken1).Photo.SHA256, "the owner's avatar stays")
}

// Only the claim's holder records the look applied, or lets the claim go: a
// setup whose claim was taken over records nothing.
func TestOnlyTheClaimsHolderRecordsTheLook(t *testing.T) {
	s := newManaged(t)
	user := s.seedUser("fenced")
	username := s.startManaged(user)
	s.createBot(creatorTG, botToken1, username)
	var ref int64
	_, err := s.db.QueryOne(pg.Scan(&ref), `SELECT id FROM telegram_bot WHERE tg_bot_id = 987654321`)
	require.NoError(t, err)
	_, err = s.db.Exec(`DELETE FROM telegram_bot_profile`)
	require.NoError(t, err)

	ctx := context.Background()
	stale, current := uuid.New(), uuid.New()
	claimed, err := database.ClaimTelegramBotProfile(ctx, ref, stale, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	again, err := database.ClaimTelegramBotProfile(ctx, ref, current, time.Minute)
	require.NoError(t, err)
	assert.False(t, again, "a live claim is not taken")
	_, err = s.db.Exec(`UPDATE telegram_bot_profile SET claimed_until = now() - interval '1 second'`)
	require.NoError(t, err)
	claimed, err = database.ClaimTelegramBotProfile(ctx, ref, current, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed, "a run-out claim is taken over")

	done, err := database.CompleteTelegramBotProfile(ctx, ref, stale)
	require.NoError(t, err)
	assert.False(t, done, "the stale setup records nothing")
	require.NoError(t, database.ReleaseTelegramBotProfile(ctx, ref, stale))
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_bot_profile WHERE claim_token = ?`, current), "nor lets the new claim go")
	done, err = database.CompleteTelegramBotProfile(ctx, ref, current)
	require.NoError(t, err)
	assert.True(t, done)
	profiled, err := database.TelegramBotProfiled(ctx, ref)
	require.NoError(t, err)
	assert.True(t, profiled)
}
