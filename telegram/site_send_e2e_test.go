package telegram_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/api"
	"gopds-api/internal/scanfixture"
	"gopds-api/middlewares"
	"gopds-api/models"
	"gopds-api/sessions"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/tgtest"
	"gopds-api/utils"

	"github.com/coder/websocket"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice S3: the site's «В Telegram» rides the socket the Kindle sends use,
// and becomes a delivery job like a press in the chat.

// siteSocket is a signed-in tab's socket to the library.
type siteSocket struct {
	t    *testing.T
	conn *websocket.Conn
}

// siteSession signs userID in and opens the site's socket, through the real
// route and the real session check.
func (s *stack) siteSession(userID int64) *siteSocket {
	s.t.Helper()
	const login = "reader"
	previousKey := viper.GetString("sessions.key")
	viper.Set("sessions.key", "stack session key")
	s.t.Cleanup(func() { viper.Set("sessions.key", previousKey) })
	sessions.SetRedisConnections(s.rc, s.rc)

	access, _, err := utils.CreateTokenPair(models.User{ID: userID, Login: login})
	require.NoError(s.t, err)
	require.NoError(s.t, sessions.SetSessionKey(context.Background(), models.LoggedInUser{User: login, Token: &access}))

	api.SetTelegramDelivery(s.delivery)
	s.t.Cleanup(func() { api.SetTelegramDelivery(nil) })
	s.engine.GET("/api/ws", middlewares.AuthMiddleware(), api.UnifiedWebSocketHandler)
	srv := httptest.NewServer(s.engine)
	s.t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {access}}})
	require.NoError(s.t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	s.t.Cleanup(func() { _ = conn.CloseNow() })
	return &siteSocket{t: s.t, conn: conn}
}

// siteReply is the socket's answer to a Telegram request.
type siteReply struct {
	Type   string `json:"type"`
	BookID int64  `json:"bookID"`
	Status string `json:"status"`
	Code   string `json:"code"`
}

// ask sends the book to Telegram.
func (w *siteSocket) ask(bookID int64) {
	w.t.Helper()
	frame, _ := json.Marshal(map[string]any{"type": "telegram", "bookID": bookID})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(w.t, w.conn.Write(ctx, websocket.MessageText, frame))
}

// reply waits for the next Telegram reply.
func (w *siteSocket) reply(within time.Duration) siteReply {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		_, data, err := w.conn.Read(ctx)
		require.NoError(w.t, err, "no Telegram reply in time")
		var r siteReply
		if json.Unmarshal(data, &r) == nil && r.Type == "telegram" {
			return r
		}
	}
}

// connectedReader is an account with its bot linked to ownerTG.
func connectedReader(t *testing.T, s *stack) int64 {
	t.Helper()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	s.waitMessage(ownerTG, 1)
	return user
}

func TestTheSiteSendsABookToTheBoundChat(t *testing.T) {
	s := newStack(t).start()
	user := connectedReader(t, s)
	book := s.books[scanfixture.EntryMultiAuthor]
	ws := s.siteSession(user)

	ws.ask(book)
	got := ws.reply(15 * time.Second)
	assert.Equal(t, siteReply{Type: "telegram", BookID: book, Status: "sent"}, got)

	doc := s.fake.Calls("sendDocument")
	require.Len(t, doc, 1)
	assert.Equal(t, strconv.FormatInt(ownerTG, 10), doc[0].Params["chat_id"], "the bound chat")
	assert.True(t, strings.HasSuffix(doc[0].Files["document"].Filename, ".epub"), "the default format")
	var job models.TelegramDelivery
	require.NoError(t, s.db.Model(&job).Select())
	assert.Equal(t, models.DeliveryFromSite, job.Origin)
	assert.Nil(t, job.StatusMessageID, "no status message for a job from the site")
	assert.Len(t, s.fake.Calls("sendMessage"), 1, "the chat gets the file and nothing else")
}

func TestTheSiteSendsInTheChosenFormat(t *testing.T) {
	s := newStack(t).start()
	user := connectedReader(t, s)
	_, err := s.db.Exec(`INSERT INTO telegram_user_settings (user_id, default_format) VALUES (?, 'fb2')`, user)
	require.NoError(t, err)
	ws := s.siteSession(user)

	ws.ask(s.books[scanfixture.EntryMultiAuthor])
	require.Equal(t, "sent", ws.reply(15*time.Second).Status)
	assert.True(t, strings.HasSuffix(s.fake.Calls("sendDocument")[0].Files["document"].Filename, ".fb2"))
}

func TestTheSiteSaysWhenNoBotIsConnected(t *testing.T) {
	s := newStack(t).start()
	user := s.seedUser("reader")
	ws := s.siteSession(user)

	ws.ask(s.books[scanfixture.EntryMultiAuthor])
	got := ws.reply(5 * time.Second)
	assert.Equal(t, "error", got.Status)
	assert.Equal(t, "not_connected", got.Code)
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_delivery`))
}

func TestASecondRequestWhileTheFirstRunsIsInProgress(t *testing.T) {
	s := newStack(t)
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		<-gate
		return delivery.PrepareFile(ctx, b, f, limit)
	}
	s.start()
	t.Cleanup(release)
	user := connectedReader(t, s)
	book := s.books[scanfixture.EntryMultiAuthor]
	ws := s.siteSession(user)

	ws.ask(book)
	require.Eventually(t, func() bool { return s.count(`SELECT count(*) FROM telegram_delivery`) == 1 },
		5*time.Second, 10*time.Millisecond)
	ws.ask(book)
	second := ws.reply(5 * time.Second)
	assert.Equal(t, "in_progress", second.Code)
	release()
	assert.Equal(t, "sent", ws.reply(15*time.Second).Status, "the first request's own answer still comes")
	s.fake.Quiet(t, "sendDocument", 1, 200*time.Millisecond)
}

func TestABlockedBotIsReportedToTheSite(t *testing.T) {
	s := newStack(t).start()
	user := connectedReader(t, s)
	s.fake.On(tgtest.Rule{Method: "sendDocument", Reply: tgtest.Forbidden("")})
	book := s.books[scanfixture.EntryMultiAuthor]
	ws := s.siteSession(user)

	ws.ask(book)
	assert.Equal(t, "bot_blocked", ws.reply(15*time.Second).Code)
	// Known now: the next request is answered without a job.
	ws.ask(book)
	assert.Equal(t, "bot_blocked", ws.reply(5*time.Second).Code)
	assert.Equal(t, 1, s.count(`SELECT count(*) FROM telegram_delivery`))
	assert.Len(t, s.fake.Calls("sendMessage"), 1, "nothing written to a chat that refuses")
}

func TestAnUnconfirmedSendIsUncertainAndNotRepeated(t *testing.T) {
	s := newStack(t)
	s.clientTimeout = time.Second
	s.start()
	user := connectedReader(t, s)
	s.fake.On(tgtest.Rule{Method: "sendDocument", Reply: s.fake.Hang(5 * time.Second)})
	ws := s.siteSession(user)

	ws.ask(s.books[scanfixture.EntryMultiAuthor])
	assert.Equal(t, "uncertain", ws.reply(15*time.Second).Code)
	time.Sleep(time.Second)
	s.fake.Quiet(t, "sendDocument", 1, 200*time.Millisecond)
	assert.Len(t, s.fake.Calls("sendMessage"), 1, "the site says it; the chat is not told")
	assert.Equal(t, models.DeliveryUncertain, func() string {
		var job models.TelegramDelivery
		require.NoError(t, s.db.Model(&job).Select())
		return job.State
	}())
}

func TestAnEndedSessionSendsNothing(t *testing.T) {
	s := newStack(t).start()
	user := connectedReader(t, s)
	ws := s.siteSession(user)
	s.mr.FlushAll() // signed out elsewhere

	ws.ask(s.books[scanfixture.EntryMultiAuthor])
	assert.Equal(t, "session_expired", ws.reply(5*time.Second).Code)
	assert.Equal(t, 0, s.count(`SELECT count(*) FROM telegram_delivery`))
}

// The job runs in another process: the socket's process still hears how it
// ended.
func TestTheSiteHearsAJobAnotherProcessRan(t *testing.T) {
	s := newStack(t).start()
	user := connectedReader(t, s)
	ws := s.siteSession(user)
	s.delivery.Stop() // this process takes no jobs; it only asks and waits
	s.secondPod()

	ws.ask(s.books[scanfixture.EntryMultiAuthor])
	assert.Equal(t, "sent", ws.reply(15*time.Second).Status)
	s.fake.Quiet(t, "sendDocument", 1, 200*time.Millisecond)
}
