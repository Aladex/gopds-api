package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"

	//nolint:depguard // the test raises the logger to debug to see what would reach each level
	"github.com/sirupsen/logrus"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/internal/safeio"
	"gopds-api/logging"
	"gopds-api/middlewares"
	"gopds-api/models"
)

// kindleWorld is everything outside a Kindle send, faked: the reader's row,
// the book and the relay. Every message the relay was handed is kept.
type kindleWorld struct {
	target    database.KindleTarget
	targetErr error
	book      kindleBook
	bookErr   error
	mailErr   error

	// gate, when set, holds every conversion until it is closed.
	gate chan struct{}

	mu   sync.Mutex
	sent []email.KindleDelivery

	converting    atomic.Int32
	maxConverting atomic.Int32
}

func (w *kindleWorld) mailed() []email.KindleDelivery {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]email.KindleDelivery(nil), w.sent...)
}

func installKindleWorld(t *testing.T, w *kindleWorld) {
	t.Helper()

	previousTarget, previousBook, previousMail := kindleTargetOf, kindleBookOf, mailToKindle
	previousQueue := kindleSends
	t.Cleanup(func() {
		kindleTargetOf, kindleBookOf, mailToKindle = previousTarget, previousBook, previousMail
		kindleSends = previousQueue
		viper.Set("kindle.sender_domain", "")
	})
	kindleSends = newKindleQueue(kindleConcurrency, kindleAdmission)
	viper.Set("kindle.sender_domain", "booksdump.test")

	kindleTargetOf = func(int64) (database.KindleTarget, error) { return w.target, w.targetErr }
	kindleBookOf = func(int64) (kindleBook, error) {
		now := w.converting.Add(1)
		defer w.converting.Add(-1)
		for {
			highest := w.maxConverting.Load()
			if now <= highest || w.maxConverting.CompareAndSwap(highest, now) {
				break
			}
		}
		if w.gate != nil {
			<-w.gate
		}
		return w.book, w.bookErr
	}
	mailToKindle = func(d email.KindleDelivery) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.mailErr != nil {
			return w.mailErr
		}
		w.sent = append(w.sent, d)
		return nil
	}
}

func configuredReader() *kindleWorld {
	return &kindleWorld{
		target: database.KindleTarget{Email: "reader@kindle.com", SenderID: "5f0c7a52-3c1e-4bb4-9a77-0b3c1f6a2d10"},
		book:   kindleBook{title: "Полдень", filename: "polden.epub", data: []byte("PK-epub-bytes")},
	}
}

func readKindleReply(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var msg map[string]interface{}
		err := wsjson.Read(ctx, conn, &msg)
		cancel()
		require.NoError(t, err)
		if msg["type"] == wsMsgKindle {
			return msg
		}
	}
}

func askKindle(t *testing.T, conn *websocket.Conn, bookID int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, wsjson.Write(ctx, conn, map[string]interface{}{"type": "kindle", "bookID": bookID}))
}

// A configured reader asks; the book goes out from their own sender address to
// their Kindle, and the reply says sent.
func TestKindleSendsFromTheReadersOwnAddress(t *testing.T) {
	world := configuredReader()
	installKindleWorld(t, world)
	s, _ := setupTestServer(t, 7, "reader", false)
	conn := dialWS(t, s)

	askKindle(t, conn, 42)
	reply := readKindleReply(t, conn)

	assert.Equal(t, "sent", reply["status"])
	assert.EqualValues(t, 42, reply["bookID"])
	assert.Nil(t, reply["code"])

	mailed := world.mailed()
	require.Len(t, mailed, 1)
	assert.Equal(t, "5f0c7a52-3c1e-4bb4-9a77-0b3c1f6a2d10@booksdump.test", mailed[0].From)
	assert.Equal(t, "reader@kindle.com", mailed[0].To)
	assert.Equal(t, "Полдень", mailed[0].Title)
	assert.Equal(t, "polden.epub", mailed[0].Book.Filename)
	assert.Equal(t, "application/epub+zip", mailed[0].Book.ContentType)
	assert.Equal(t, []byte("PK-epub-bytes"), mailed[0].Book.Data)
}

// A reader with no Kindle address gets not_configured, and nothing is
// converted or mailed.
func TestKindleUnconfiguredSendsNothing(t *testing.T) {
	world := configuredReader()
	world.target.Email = ""
	installKindleWorld(t, world)
	s, _ := setupTestServer(t, 7, "reader", false)
	conn := dialWS(t, s)

	askKindle(t, conn, 42)
	reply := readKindleReply(t, conn)

	assert.Equal(t, "error", reply["status"])
	assert.Equal(t, kindleCodeNotConfigured, reply["code"])
	assert.Empty(t, world.mailed())
	assert.Zero(t, world.maxConverting.Load(), "a book was converted for a reader with no Kindle")
}

// A second request for the same book while the first is under way is answered
// at once with in_progress, and only one message goes out.
func TestKindleSecondRequestWhileInFlightSendsOnce(t *testing.T) {
	world := configuredReader()
	world.gate = make(chan struct{})
	installKindleWorld(t, world)
	s, _ := setupTestServer(t, 7, "reader", false)
	first := dialWS(t, s)
	second := dialWS(t, s)

	askKindle(t, first, 42)
	// Wait for the first send to be inside the conversion.
	require.Eventually(t, func() bool { return world.converting.Load() == 1 }, 2*time.Second, 5*time.Millisecond)

	// The same reader from another tab, and again from the first.
	askKindle(t, second, 42)
	assert.Equal(t, kindleCodeInProgress, readKindleReply(t, second)["code"])
	askKindle(t, first, 42)
	assert.Equal(t, kindleCodeInProgress, readKindleReply(t, first)["code"])

	close(world.gate)
	assert.Equal(t, "sent", readKindleReply(t, first)["status"])
	assert.Len(t, world.mailed(), 1)

	// Once it is done the book can be sent again.
	askKindle(t, first, 42)
	assert.Equal(t, "sent", readKindleReply(t, first)["status"])
	assert.Len(t, world.mailed(), 2)
}

// A burst of different books queues behind the semaphore rather than starting
// every conversion at once.
func TestKindleBoundsConcurrentConversions(t *testing.T) {
	world := configuredReader()
	world.gate = make(chan struct{})
	installKindleWorld(t, world)
	s, _ := setupTestServer(t, 7, "reader", false)
	conn := dialWS(t, s)

	const books = 6
	for id := int64(1); id <= books; id++ {
		askKindle(t, conn, id)
	}
	require.Eventually(t, func() bool { return world.converting.Load() == kindleConcurrency }, 2*time.Second, 5*time.Millisecond)
	// Give the rest every chance to start if nothing held them back.
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, kindleConcurrency, world.maxConverting.Load())

	close(world.gate)
	for i := 0; i < books; i++ {
		assert.Equal(t, "sent", readKindleReply(t, conn)["status"])
	}
	assert.Len(t, world.mailed(), books)
}

// Every failure has a code of its own, and none of them mails anything.
func TestKindleErrorCodes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*kindleWorld)
		code  string
	}{
		{"no such book", func(w *kindleWorld) { w.bookErr = pg.ErrNoRows }, kindleCodeNotFound},
		{"file missing", func(w *kindleWorld) { w.bookErr = fmt.Errorf("%w: /x.zip", errBookFileMissing) }, kindleCodeNotFound},
		{"too large to read", func(w *kindleWorld) { w.bookErr = safeio.ErrTooLarge }, kindleCodeTooLarge},
		{"conversion", func(w *kindleWorld) { w.bookErr = errors.New("failed to parse FB2") }, kindleCodeConversionFailed},
		{"relay refused", func(w *kindleWorld) { w.mailErr = errors.New("550 no") }, kindleCodeSendFailed},
		{"relay says too large", func(w *kindleWorld) { w.mailErr = email.ErrBookTooLarge }, kindleCodeTooLarge},
		{"reader unreadable", func(w *kindleWorld) { w.targetErr = errors.New("db down") }, kindleCodeSendFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			world := configuredReader()
			tc.setup(world)
			installKindleWorld(t, world)
			s, _ := setupTestServer(t, 7, "reader", false)
			conn := dialWS(t, s)

			askKindle(t, conn, 42)
			reply := readKindleReply(t, conn)
			assert.Equal(t, "error", reply["status"])
			assert.Equal(t, tc.code, reply["code"])
			assert.Empty(t, world.mailed())
		})
	}
}

// With no domain to put a sender address on, nothing is sent.
func TestKindleWithoutASenderDomainSendsNothing(t *testing.T) {
	world := configuredReader()
	installKindleWorld(t, world)
	viper.Set("kindle.sender_domain", "")
	viper.Set("email.from", "")
	s, _ := setupTestServer(t, 7, "reader", false)
	conn := dialWS(t, s)

	askKindle(t, conn, 42)
	reply := readKindleReply(t, conn)
	assert.Equal(t, kindleCodeSendFailed, reply["code"])
	assert.Empty(t, world.mailed())
}

// The address endpoint refuses anything that is not a Kindle address before
// it reaches the database.
func TestSetKindleEmailRejectsANonKindleAddress(t *testing.T) {
	for _, body := range []string{
		`{"kindle_email":"reader@gmail.com"}`,
		`{"kindle_email":""}`,
		`{"kindle_email":"Reader <reader@kindle.com>"}`,
		`{}`,
		`not json`,
	} {
		t.Run(body, func(t *testing.T) {
			router := gin.New()
			router.PUT("/kindle", SetKindleEmail)
			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/kindle", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

// The profile carries the reader's Kindle settings, with the sender address in
// full — the one they copy into Amazon's approved list.
func TestProfileCarriesTheKindleSettings(t *testing.T) {
	viper.Set("kindle.sender_domain", "booksdump.test")
	t.Cleanup(func() { viper.Set("kindle.sender_domain", "") })

	user := models.User{
		Login:              "reader",
		KindleEmail:        "reader@kindle.com",
		KindleSenderID:     "5f0c7a52-3c1e-4bb4-9a77-0b3c1f6a2d10",
		ShowKindleButton:   false,
		ShowTelegramButton: true,
	}
	profile := profileOf(&user, false)

	assert.Equal(t, "reader@kindle.com", profile.KindleEmail)
	assert.Equal(t, "5f0c7a52-3c1e-4bb4-9a77-0b3c1f6a2d10@booksdump.test", profile.KindleSender)
	require.NotNil(t, profile.ShowKindleButton)
	require.NotNil(t, profile.ShowTelegramButton)
	assert.False(t, *profile.ShowKindleButton)
	assert.True(t, *profile.ShowTelegramButton)
}

// queueSize counts the sends admitted and not yet finished.
func queueSize() int {
	kindleSends.mu.Lock()
	defer kindleSends.mu.Unlock()
	return len(kindleSends.inFlight)
}

// The queue is bounded as a whole: past its limit a request is told busy at
// once, before any goroutine or claim is spent on it.
func TestKindleRefusesPastTheGlobalAdmissionLimit(t *testing.T) {
	world := configuredReader()
	world.gate = make(chan struct{})
	installKindleWorld(t, world)
	kindleSends = newKindleQueue(1, 3)
	s, _ := setupTestServer(t, 7, "reader", false)
	conn := dialWS(t, s)

	for id := int64(1); id <= 3; id++ {
		askKindle(t, conn, id)
	}
	require.Eventually(t, func() bool { return queueSize() == 3 }, 2*time.Second, 5*time.Millisecond)
	askKindle(t, conn, 4)
	reply := readKindleReply(t, conn)
	assert.EqualValues(t, 4, reply["bookID"])
	assert.Equal(t, kindleCodeBusy, reply["code"])
	assert.Equal(t, 3, queueSize())

	close(world.gate)
	for i := 0; i < 3; i++ {
		assert.Equal(t, "sent", readKindleReply(t, conn)["status"])
	}
	assert.Len(t, world.mailed(), 3)
}

// One connection may not hold more than its share of the queue.
func TestKindleRefusesPastTheSocketLimit(t *testing.T) {
	world := configuredReader()
	world.gate = make(chan struct{})
	installKindleWorld(t, world)
	s, _ := setupTestServer(t, 7, "reader", false)
	conn := dialWS(t, s)

	for id := int64(1); id <= kindleSocketPending; id++ {
		askKindle(t, conn, id)
	}
	require.Eventually(t, func() bool { return queueSize() == kindleSocketPending }, 2*time.Second, 5*time.Millisecond)
	askKindle(t, conn, 99)
	reply := readKindleReply(t, conn)
	assert.EqualValues(t, 99, reply["bookID"])
	assert.Equal(t, kindleCodeBusy, reply["code"])
	assert.Equal(t, kindleSocketPending, queueSize(), "the refused request kept its claim")

	close(world.gate)
	for i := 0; i < kindleSocketPending; i++ {
		readKindleReply(t, conn)
	}
}

// A request still waiting for a slot when its connection closes is dropped:
// no conversion, no email, and its claim goes back. The ones already running
// finish.
func TestKindleDropsAWaitingRequestWhenTheSocketCloses(t *testing.T) {
	world := configuredReader()
	world.gate = make(chan struct{})
	installKindleWorld(t, world)
	s, _ := setupTestServer(t, 7, "reader", false)
	running := dialWS(t, s)
	waiting := dialWS(t, s)

	askKindle(t, running, 1)
	askKindle(t, running, 2)
	require.Eventually(t, func() bool { return world.converting.Load() == kindleConcurrency }, 2*time.Second, 5*time.Millisecond)
	askKindle(t, waiting, 3)
	require.Eventually(t, func() bool { return queueSize() == 3 }, 2*time.Second, 5*time.Millisecond)

	require.NoError(t, waiting.Close(websocket.StatusNormalClosure, "bye"))
	require.Eventually(t, func() bool { return queueSize() == 2 }, 2*time.Second, 5*time.Millisecond,
		"the waiting request kept its claim after its socket closed")

	close(world.gate)
	readKindleReply(t, running)
	readKindleReply(t, running)
	time.Sleep(50 * time.Millisecond)
	assert.Len(t, world.mailed(), 2, "a request from a closed socket was mailed")
}

// A socket outlives a logout elsewhere. Sending mail over it checks the
// session first, and nothing goes out on one that has ended.
func TestKindleRefusesASessionThatHasEnded(t *testing.T) {
	world := configuredReader()
	installKindleWorld(t, world)
	s, _ := setupTestServer(t, 7, "reader", false)
	var checks atomic.Int32
	validateWSSession = func(string) error {
		checks.Add(1)
		return middlewares.ErrSessionMissing
	}
	conn := dialWS(t, s)

	askKindle(t, conn, 42)
	reply := readKindleReply(t, conn)

	assert.Equal(t, kindleCodeSessionExpired, reply["code"])
	assert.Positive(t, checks.Load())
	assert.Empty(t, world.mailed())
	assert.Zero(t, world.maxConverting.Load())
}

// The session is checked again after the wait for a slot, which may have
// lasted long enough for the reader to log out.
func TestKindleChecksTheSessionAgainAfterWaiting(t *testing.T) {
	world := configuredReader()
	world.gate = make(chan struct{})
	installKindleWorld(t, world)
	s, _ := setupTestServer(t, 7, "reader", false)
	var revoked atomic.Bool
	validateWSSession = func(string) error {
		if revoked.Load() {
			return middlewares.ErrSessionMissing
		}
		return nil
	}
	conn := dialWS(t, s)

	askKindle(t, conn, 1)
	askKindle(t, conn, 2)
	require.Eventually(t, func() bool { return world.converting.Load() == kindleConcurrency }, 2*time.Second, 5*time.Millisecond)
	askKindle(t, conn, 3)
	require.Eventually(t, func() bool { return queueSize() == 3 }, 2*time.Second, 5*time.Millisecond)
	revoked.Store(true)
	close(world.gate)

	codes := map[float64]interface{}{}
	for i := 0; i < 3; i++ {
		reply := readKindleReply(t, conn)
		codes[reply["bookID"].(float64)] = reply["code"]
	}
	assert.Equal(t, kindleCodeSessionExpired, codes[3])
	assert.Len(t, world.mailed(), 2)
}

// captureLogs records every entry the application logs, debug included.
func captureLogs(t *testing.T) *logtest.Hook {
	t.Helper()
	logger, hook := logtest.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)
	logging.SetLogger(logger)
	t.Cleanup(func() { logging.SetLogger(nil) })
	return hook
}

// A panic in the conversion is a failure in the reply and in the log alike.
// The log line was written before the recovery and said sent.
func TestKindlePanicIsLoggedAsAFailure(t *testing.T) {
	world := configuredReader()
	installKindleWorld(t, world)
	kindleBookOf = func(int64) (kindleBook, error) { panic("malformed book") }
	hook := captureLogs(t)
	s, _ := setupTestServer(t, 7, "reader", false)
	conn := dialWS(t, s)

	askKindle(t, conn, 42)
	assert.Equal(t, kindleCodeConversionFailed, readKindleReply(t, conn)["code"])

	var outcomes []string
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "outcome=") {
			outcomes = append(outcomes, entry.Message)
		}
	}
	require.Len(t, outcomes, 1)
	assert.Contains(t, outcomes[0], "outcome=conversion_failed")
	assert.Empty(t, world.mailed())
}

// A refused send is logged by its class, never by its addresses — at any
// level a production log keeps. The server's own words, which echo the
// address, are for debug only.
func TestKindleRelayFailureLogsNoAddresses(t *testing.T) {
	world := configuredReader()
	world.mailErr = &email.SendError{
		Stage: email.StageRcpt,
		Err:   &textproto.Error{Code: 550, Msg: "5.1.1 <reader@kindle.com>: no such user"},
	}
	installKindleWorld(t, world)
	hook := captureLogs(t)
	s, _ := setupTestServer(t, 7, "reader", false)
	conn := dialWS(t, s)

	askKindle(t, conn, 42)
	assert.Equal(t, kindleCodeSendFailed, readKindleReply(t, conn)["code"])

	sender := email.KindleSenderAddress(world.target.SenderID)
	var classified bool
	for _, entry := range hook.AllEntries() {
		if entry.Level <= logrus.InfoLevel {
			assert.NotContains(t, entry.Message, world.target.Email, "an address at %s", entry.Level)
			assert.NotContains(t, entry.Message, world.target.SenderID, "a sender id at %s", entry.Level)
			if strings.Contains(entry.Message, "stage=rcpt code=550") &&
				strings.Contains(entry.Message, "user_id=7") && strings.Contains(entry.Message, "book_id=42") {
				classified = true
			}
		}
	}
	assert.True(t, classified, "no classified error line")
	assert.NotEmpty(t, sender)
}
