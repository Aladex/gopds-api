package telegram_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"
	"gopds-api/telegram"
	"gopds-api/telegram/tgapi"
	"gopds-api/telegram/tgtest"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
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
	legacyBaseURL = "https://bot.example.test"
)

type legacyBot struct {
	fake  *tgtest.FakeBotAPI
	hook  tgtest.Webhook
	u     *tgtest.Updates
	books map[string]int64
	uuid  string
}

func startLegacyBot(t *testing.T) legacyBot {
	t.Helper()
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)

	webhookUUID := uuid.NewString()
	_, err := db.Exec(`INSERT INTO auth_user
		(password, is_superuser, username, email, date_joined, bot_token, webhook_uuid, telegram_id)
		VALUES ('', false, 'legacy-e2e', 'legacy-e2e@fixture.local', now(), ?, ?, ?)`,
		legacyToken, webhookUUID, legacyOwnerTG)
	require.NoError(t, err)

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	fake := tgtest.NewFakeBotAPI(t)
	bm := telegram.NewBotManager(&telegram.Config{
		BaseURL: legacyBaseURL,
		API:     tgapi.Endpoint{ServerURL: fake.URL()},
	}, rc, services.NewSearchService(database.NewPGSearchRepository(db)), nil)

	svc, err := telegram.NewTelegramService(bm)
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	svc.SetupWebhookRoutes(engine.Group("/telegram"))

	return legacyBot{
		fake:  fake,
		hook:  tgtest.WebhookFromSetWebhook(t, fake, legacyToken, engine),
		u:     tgtest.NewUpdates(),
		books: books,
		uuid:  webhookUUID,
	}
}

func TestLegacyStartupRegistersTheBotWithTelegram(t *testing.T) {
	b := startLegacyBot(t)

	require.Len(t, b.fake.Calls("getMe"), 1, "bot.New validates the token once")
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
	assert.Equal(t, legacyBaseURL+"/telegram/"+b.uuid, hooks[0].Params["url"])
	assert.Len(t, hooks[0].Params["secret_token"], 32)
	assert.Equal(t, legacyToken, hooks[0].Token)
}

func TestLegacyBookSearchAndCard(t *testing.T) {
	b := startLegacyBot(t)
	bookID := b.books[scanfixture.EntryMultiAuthor]

	rec := b.hook.Post(t, b.u.Text(legacyOwnerTG, "/b мир"))
	require.Equal(t, http.StatusOK, rec.Code)

	list := b.fake.WaitCalls(t, "sendMessage", 1, 5*time.Second)[0]
	assert.Equal(t, "4242", list.Params["chat_id"])
	assert.Equal(t, "📚 Результаты поиска для \"мир\":\nСтраница 1 из 1 (всего найдено 1 книг)\n\n"+
		"1. война и МИР — Толстой лев, пушкин Анна (серия: Эпопея)\n\n"+
		"💡 Выберите книгу по номеру или используйте навигацию:", list.Params["text"])
	data, ok := tgtest.ButtonData(t, &list, "1")
	require.True(t, ok)
	assert.Equal(t, fmt.Sprintf("select:%d", bookID), data)

	rec = b.hook.Post(t, b.u.Tap(t, legacyOwnerTG, &list, "1"))
	require.Equal(t, http.StatusOK, rec.Code)

	card := b.fake.WaitCalls(t, "sendMessage", 2, 5*time.Second)[1]
	assert.Equal(t, "<b>война и МИР</b>\n\nРоман-эпопея о войне и мире.\n\nВыберите формат для скачивания:",
		card.Params["text"])
	assert.Equal(t, "HTML", card.Params["parse_mode"])
	assert.Equal(t, [][]string{{"📄 FB2", "📚 EPUB"}, {"📱 MOBI", "🗂 ZIP"}}, tgtest.ButtonTexts(t, &card))
	epub, _ := tgtest.ButtonData(t, &card, "📚 EPUB")
	assert.Equal(t, fmt.Sprintf("download:epub:%d", bookID), epub)

	answers := b.fake.WaitCalls(t, "answerCallbackQuery", 1, 5*time.Second)
	assert.Equal(t, "Book selected", answers[0].Params["text"])
}

// The legacy bot answers only its linked owner; anyone else gets silence.
func TestLegacyBotIgnoresStrangers(t *testing.T) {
	b := startLegacyBot(t)

	rec := b.hook.Post(t, b.u.Text(5555, "/b мир"))
	require.Equal(t, http.StatusOK, rec.Code)
	b.fake.Quiet(t, "sendMessage", 0, 300*time.Millisecond)

	// The owner is still served, so the silence above is about the sender.
	b.hook.Post(t, b.u.Text(legacyOwnerTG, "/b мир"))
	b.fake.WaitCalls(t, "sendMessage", 1, 5*time.Second)
}

func TestLegacyWebhookRejectsAWrongSecret(t *testing.T) {
	b := startLegacyBot(t)
	forged := b.hook
	forged.Secret = "not-the-secret"

	rec := forged.Post(t, b.u.Text(legacyOwnerTG, "/b мир"))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	b.fake.Quiet(t, "sendMessage", 0, 300*time.Millisecond)
}
