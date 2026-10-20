package telegram_test

import (
	"net/http"
	"testing"

	"gopds-api/telegram/profile"
	"gopds-api/telegram/tgtest"

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

	code, out := s.api(user, http.MethodPost, "/profile", "")
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
	code, out = s.api(user, http.MethodPost, "/profile", "")
	assert.Equal(t, http.StatusBadGateway, code)
	assert.Equal(t, "telegram_unavailable", out["error"])
	assert.Equal(t, false, s.status(user)["profile_applied"])
	assert.Zero(t, s.count(`SELECT count(*) FROM telegram_bot_profile`))
}
