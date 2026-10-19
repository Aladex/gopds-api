package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"
	"gopds-api/telegram"
	"gopds-api/telegram/secrets"
	"gopds-api/telegram/tgapi"
	"gopds-api/telegram/tgtest"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/require"
)

// stack is the bot service as production wires it — scratch database, Redis,
// the real routes — pointed at a fake Telegram. Each slice's end-to-end tests
// drive it from the outside: updates through the webhook route, the site
// through its API, assertions on what reached Telegram and the database.
type stack struct {
	t     *testing.T
	db    *pg.DB
	mr    *miniredis.Miniredis
	rc    *redis.Client
	fake  *tgtest.FakeBotAPI
	box   *secrets.Box
	books map[string]int64
	u     *tgtest.Updates

	// The running "process". restart replaces it, as a new pod would.
	bm     *telegram.BotManager
	engine *gin.Engine
}

// secondPod is another process of the same service on the same database and
// the same Telegram — what a rolling update runs for a minute.
func (s *stack) secondPod() *stack {
	s.t.Helper()
	other := *s
	return other.start()
}

const (
	stackBaseURL = "https://bot.example.test"
	stackSiteURL = "https://library.example.test"
	stackSecret  = "stack secret key"
)

// newStack migrates a scratch database and ingests the fixture books. The
// service itself starts with start, so a test can seed legacy rows first.
func newStack(t *testing.T) *stack {
	t.Helper()
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	box, err := secrets.NewBox(stackSecret)
	require.NoError(t, err)
	return &stack{
		t: t, db: db, mr: mr, rc: rc, fake: tgtest.NewFakeBotAPI(t), box: box, books: books,
		u: tgtest.NewUpdates(),
	}
}

// start brings the service up: legacy bots adopted, bots served, webhooks set.
func (s *stack) start() *stack {
	s.t.Helper()
	s.bm = s.newManager()
	svc, err := telegram.NewTelegramService(s.bm)
	require.NoError(s.t, err)
	gin.SetMode(gin.TestMode)
	s.engine = gin.New()
	svc.SetupWebhookRoutes(s.engine.Group("/telegram"))
	// The site's auth middleware puts the user id into the context; the
	// tests name the user in a header instead.
	svc.SetupApiRoutes(s.engine.Group("/api/telegram", func(c *gin.Context) {
		if id, err := strconv.ParseInt(c.GetHeader("X-Test-User"), 10, 64); err == nil {
			c.Set("user_id", id)
		}
	}))
	bm := s.bm
	s.t.Cleanup(bm.WaitIdle)
	return s
}

func (s *stack) newManager() *telegram.BotManager {
	return telegram.NewBotManager(&telegram.Config{
		BaseURL: stackBaseURL,
		SiteURL: stackSiteURL,
		API:     tgapi.Endpoint{ServerURL: s.fake.URL()},
		Secrets: s.box,
	}, s.rc, services.NewSearchService(database.NewPGSearchRepository(s.db)), nil)
}

// seedUser adds a library account and returns its id.
func (s *stack) seedUser(name string) int64 {
	s.t.Helper()
	var id int64
	_, err := s.db.QueryOne(pg.Scan(&id), `INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', false, ?, ?, now()) RETURNING id`, name, name+"@fixture.local")
	require.NoError(s.t, err)
	return id
}

// seedLegacyBot gives an account an old-style bot in auth_user.
func (s *stack) seedLegacyBot(userID int64, token, webhookUUID string, telegramID *int64) {
	s.t.Helper()
	_, err := s.db.Exec(`UPDATE auth_user SET bot_token = ?, webhook_uuid = ?, telegram_id = ? WHERE id = ?`,
		token, webhookUUID, telegramID, userID)
	require.NoError(s.t, err)
}

// api calls the site's Telegram API as userID.
func (s *stack) api(userID int64, method, path, body string) (code int, out map[string]any) {
	s.t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, "/api/telegram"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", strconv.FormatInt(userID, 10))
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// connect enters token on the site as userID and returns the link secret.
func (s *stack) connect(userID int64, token string) (secret string, status map[string]any) {
	s.t.Helper()
	code, out := s.api(userID, http.MethodPost, "/bot", fmt.Sprintf(`{"token":%q}`, token))
	require.Equal(s.t, http.StatusOK, code, "connect answered %v", out)
	link, _ := out["link_url"].(string)
	_, secret, found := strings.Cut(link, "?start=link_")
	require.True(s.t, found, "link_url %q", link)
	return secret, out
}

// hook is the webhook token's bot registered with Telegram.
func (s *stack) hook(token string) tgtest.Webhook {
	s.t.Helper()
	return tgtest.WebhookFromSetWebhook(s.t, s.fake, token, s.engine)
}

// count runs a count(*) query.
func (s *stack) count(query string, args ...any) int {
	s.t.Helper()
	var n int
	_, err := s.db.QueryOne(pg.Scan(&n), query, args...)
	require.NoError(s.t, err)
	return n
}

// messagesTo returns the texts of the sendMessage calls to chatID that
// reached Telegram, answered or not.
func (s *stack) messagesTo(chatID int64) []string {
	var texts []string
	for _, c := range s.fake.Calls("sendMessage") {
		if c.Params["chat_id"] == strconv.FormatInt(chatID, 10) {
			texts = append(texts, c.Params["text"])
		}
	}
	return texts
}

// waitMessage waits until the n-th message to chatID has been answered —
// the test may need its message id — and returns its call.
func (s *stack) waitMessage(chatID int64, n int) tgtest.Call {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var calls []tgtest.Call
		for _, c := range s.fake.AnsweredCalls("sendMessage") {
			if c.Params["chat_id"] == strconv.FormatInt(chatID, 10) {
				calls = append(calls, c)
			}
		}
		if len(calls) >= n {
			return calls[n-1]
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("no message #%d to %d; sent: %v", n, chatID, s.messagesTo(chatID))
	return tgtest.Call{}
}

// settle waits for the service to finish what it took, then a little for
// stray sends, and asserts how many messages chatID got in total.
func (s *stack) settle(chatID int64, want int) {
	s.t.Helper()
	s.bm.WaitIdle()
	time.Sleep(200 * time.Millisecond)
	require.Len(s.t, s.messagesTo(chatID), want, "messages to %d: %v", chatID, s.messagesTo(chatID))
}
