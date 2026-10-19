package telegram_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/telegram/tgtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The legacy router, characterized from the outside: an update goes through
// the real webhook route of a bot built by the real startup path, and the
// assertions are on what reached the (fake) Telegram. These pin today's
// behavior so the slices that replace the router cannot change it by
// accident before they mean to.

const (
	legacyOwnerTG = 4242
	legacyToken   = "123456789:AAH-legacy-e2e-token-0123456789abcdef"
)

type legacyBot struct {
	*stack
	hook   tgtest.Webhook
	uuid   string
	userID int64
}

// startLegacyBot starts the service over an account whose bot was connected
// the old way, owner included: the startup adopts it.
func startLegacyBot(t *testing.T) legacyBot {
	t.Helper()
	s := newStack(t)
	userID := s.seedUser("legacy-e2e")
	webhookUUID := uuid.NewString()
	owner := int64(legacyOwnerTG)
	s.seedLegacyBot(userID, legacyToken, webhookUUID, &owner)
	s.start()
	return legacyBot{stack: s, hook: s.hook(legacyToken), uuid: webhookUUID, userID: userID}
}

func TestLegacyStartupRegistersTheBotWithTelegram(t *testing.T) {
	b := startLegacyBot(t)

	// The adopted bot is served without a call; its name is read afterwards,
	// once, because adoption only had its token.
	getMe := b.fake.WaitCalls(t, "getMe", 1, 5*time.Second)
	require.Len(t, getMe, 1)
	assert.True(t, getMe[0].At.After(b.fake.Calls("setWebhook")[0].At), "the name is read after the webhook is set")
	commands := b.fake.Calls("setMyCommands")
	require.Len(t, commands, 1)
	var registered []struct{ Command string }
	require.NoError(t, commands[0].Decode("commands", &registered))
	var names []string
	for _, c := range registered {
		names = append(names, c.Command)
	}
	assert.Equal(t, []string{"start", "search", "b", "a", "ba", "favorites", "collections", "context", "clear", "donate"}, names)

	hooks := b.fake.Calls("setWebhook")
	require.Len(t, hooks, 1)
	assert.Equal(t, stackBaseURL+"/telegram/"+b.uuid, hooks[0].Params["url"])
	assert.Regexp(t, `^[0-9a-f]{48}$`, hooks[0].Params["secret_token"], "a random secret, not one derived from the token")
	assert.Equal(t, legacyToken, hooks[0].Token)
}

func TestLegacyBookSearchAndCard(t *testing.T) {
	b := startLegacyBot(t)
	bookID := b.books[scanfixture.EntryMultiAuthor]

	rec := b.hook.Post(t, b.u.Text(legacyOwnerTG, "/b мир"))
	require.Equal(t, http.StatusOK, rec.Code)

	list := b.fake.WaitAnswered(t, "sendMessage", 1, 5*time.Second)[0]
	assert.Equal(t, "4242", list.Params["chat_id"])
	assert.Equal(t, "📚 Результаты поиска для \"мир\":\nСтраница 1 из 1 (всего найдено 1 книг)\n\n"+
		"1. война и МИР — Толстой лев, пушкин Анна (серия: Эпопея)\n\n"+
		"💡 Выберите книгу по номеру или используйте навигацию:", list.Params["text"])
	data, ok := tgtest.ButtonData(t, &list, "1")
	require.True(t, ok)
	assert.Equal(t, fmt.Sprintf("select:%d", bookID), data)

	rec = b.hook.Post(t, b.u.Tap(t, legacyOwnerTG, &list, "1"))
	require.Equal(t, http.StatusOK, rec.Code)

	card := b.fake.WaitAnswered(t, "sendMessage", 2, 5*time.Second)[1]
	assert.Equal(t, "<b>война и МИР</b>\n\nРоман-эпопея о войне и мире.\n\nВыберите формат для скачивания:",
		card.Params["text"])
	assert.Equal(t, "HTML", card.Params["parse_mode"])
	assert.Equal(t, [][]string{{"📄 FB2", "📚 EPUB"}, {"📱 MOBI", "🗂 ZIP"}}, tgtest.ButtonTexts(t, &card))
	epub, _ := tgtest.ButtonData(t, &card, "📚 EPUB")
	assert.Equal(t, fmt.Sprintf("download:epub:%d", bookID), epub)

	answers := b.fake.WaitCalls(t, "answerCallbackQuery", 1, 5*time.Second)
	assert.Equal(t, "Book selected", answers[0].Params["text"])
}

// The bot serves only its owner. A stranger used to get silence; now they
// get one explanation an hour, and none of the owner's library.
func TestLegacyBotIgnoresStrangers(t *testing.T) {
	b := startLegacyBot(t)

	rec := b.hook.Post(t, b.u.Text(5555, "/b мир"))
	require.Equal(t, http.StatusOK, rec.Code)
	got := b.waitMessage(5555, 1)
	assert.Equal(t, "Это личный бот другого пользователя\n\nСвоего бота можно подключить в профиле библиотеки.",
		got.Params["text"])
	b.hook.Post(t, b.u.Text(5555, "/b мир"))
	b.settle(5555, 1)

	// The owner is still served, so the silence above is about the sender.
	b.hook.Post(t, b.u.Text(legacyOwnerTG, "/b мир"))
	b.waitMessage(legacyOwnerTG, 1)
}

func TestLegacyWebhookRejectsAWrongSecret(t *testing.T) {
	b := startLegacyBot(t)
	forged := b.hook
	forged.Secret = "not-the-secret"

	rec := forged.Post(t, b.u.Text(legacyOwnerTG, "/b мир"))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	b.fake.Quiet(t, "sendMessage", 0, 300*time.Millisecond)
}
